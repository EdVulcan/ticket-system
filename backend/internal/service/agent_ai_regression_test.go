package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// These tests keep the user-visible assistant boundary stable across both
// planner protocols. Providers are never authoritative for tenant-owned
// catalog references or business facts.
func TestAgentAIRegressionLegacyProductCreateCollectsAllCriticalFacts(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := startCatalogAIProvider(t, `{"operation_type":"ticket_product_create","product":{"name":"Child Ticket"}}`)
	saveCatalogAIConfig(t, server.URL, 10)

	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "创建一个 Child Ticket", IdempotencyKey: "agent-regression-legacy-missing", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("submit incomplete product request: %v", err)
	}
	if view.ProtocolMode != agentProtocolLegacyJSON || view.State != AgentTaskCollecting || view.CanConfirm {
		t.Fatalf("incomplete product unexpectedly became executable: %+v", view)
	}
	for _, field := range []string{"product_type", "scenic_area_name", "price", "settlement_price", "groups"} {
		if !agentRegressionMissingField(view.MissingFields, field) {
			t.Fatalf("agent did not collect %s: %+v", field, view.MissingFields)
		}
	}
	var products int64
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND name = ?", fixture.tenant.ID, "Child Ticket").Count(&products).Error; err != nil {
		t.Fatal(err)
	}
	if products != 0 || calls.Load() != 1 {
		t.Fatalf("incomplete request wrote a product or retried unexpectedly: products=%d calls=%d", products, calls.Load())
	}
}

func TestAgentAIRegressionProductCreateContinuationKeepsCreateToolRoute(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var providerCalls atomic.Int32
	server, _ := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		if providerCalls.Add(1) == 1 {
			return toolCallPayload("create-continuation-1", "prepare_ticket_product_create", `{"name":"Continuation Ticket","product_type":"online","price":55,"settlement_price":30}`, 20), nil
		}
		return toolCallPayload("create-continuation-2", "prepare_ticket_product_create", `{"name":"Continuation Ticket","product_type":"online","scenic_area_name":"Batch Scenic","price":55,"settlement_price":30,"groups":[{"group_name":"Admission","items":[{"checkpoint_name":"Main Gate","max_per_check_in":1}]}]}`, 22), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	first, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "创建一个线上 Continuation Ticket，售价 55 元，结算价 30 元", IdempotencyKey: "product-create-continuation", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("initial product create: %v", err)
	}
	if first.OperationType != AgentOperationTicketProductCreate || first.State != AgentTaskCollecting {
		t.Fatalf("initial product create was not kept as a collecting create task: %+v", first)
	}
	second, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		TaskID: first.TaskID, InputText: "所属景区为 Batch Scenic，使用 Main Gate，每点最多 1 次", TurnKey: "turn-2",
	})
	if err != nil {
		t.Fatalf("product create continuation: %v", err)
	}
	if second.OperationType != AgentOperationTicketProductCreate || second.State != AgentTaskAwaitingConfirmation || !second.CanConfirm {
		t.Fatalf("product continuation did not produce a create preview: %+v", second)
	}
	if providerCalls.Load() != 2 || !strings.Contains(string(second.Preview), "Continuation Ticket") {
		t.Fatalf("product continuation used the wrong route or lost preview data: calls=%d preview=%s", providerCalls.Load(), second.Preview)
	}
}

func TestAgentAIRegressionToolNewGroupContinuationKeepsTargetsAndPreviewNames(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var providerCalls atomic.Int32
	server, _ := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		switch providerCalls.Add(1) {
		case 1:
			return toolCallPayload("new-group-initial", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["North Gate"],"create_group":true}]}`, 22), nil
		case 2:
			return toolCallPayload("new-group-continuation", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","group_name":"Water Park"}]}`, 22), nil
		default:
			return nil, fmt.Errorf("unexpected provider call %d", providerCalls.Load())
		}
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}

	service := &AgentTaskService{}
	first, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "给 Adult Ticket 增加 North Gate 检票点，并新增一个规则组", IdempotencyKey: "agent-regression-new-group", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("initial new-group request: %v", err)
	}
	if first.State != AgentTaskCollecting || first.CanConfirm || !agentRegressionMissingField(first.MissingFields, "operations[0].group_name") {
		t.Fatalf("new group name was not collected: %+v", first)
	}

	second, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		TaskID: first.TaskID, InputText: "Water Park", TurnKey: "turn-2",
	})
	if err != nil {
		t.Fatalf("new-group continuation: %v", err)
	}
	if second.ProtocolMode != agentProtocolToolV1 || second.State != AgentTaskAwaitingConfirmation || !second.CanConfirm || second.Preview == nil {
		t.Fatalf("new-group continuation did not produce a preview: %+v", second)
	}
	if providerCalls.Load() != 2 || !strings.Contains(string(second.Preview), "Water Park") || !strings.Contains(string(second.Preview), "North Gate") {
		t.Fatalf("preview lost group/checkpoint names: calls=%d preview=%s", providerCalls.Load(), second.Preview)
	}
}

func TestAgentAIRegressionRejectsForeignAndUnrequestedCatalogTargetsAsBadRequest(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	foreign := model.Tenant{Name: "Foreign Agent Tenant", SystemCode: fmt.Sprintf("AGENT-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&foreign).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: foreign.ID, Code: "AGENT-FOREIGN-AREA", Name: "Foreign Scenic", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		return tx.Create(&model.CheckPoint{Name: "Foreign Gate", TenantID: foreign.ID, ScenicAreaID: area.ID}).Error
	}); err != nil {
		t.Fatal(err)
	}
	server, _ := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("foreign-checkpoint", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["Foreign Gate"]}]}`, 18), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	_, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "给 Adult Ticket 增加 Foreign Gate 检票点", IdempotencyKey: "agent-regression-foreign-target", TurnKey: "turn-1",
	})
	var foreignErr *AgentTaskError
	if !errors.As(err, &foreignErr) || foreignErr.HTTPStatus != http.StatusBadRequest || foreignErr.Code != "invalid_request" {
		t.Fatalf("foreign tenant checkpoint should be a typed bad request, got %v", err)
	}

	unrequestedServer, _ := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("unrequested-checkpoint", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["Main Gate"]}]}`, 18), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(unrequestedServer.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("replace tool config: %v", err)
	}
	_, err = (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "给 Adult Ticket 增加 North Gate 检票点", IdempotencyKey: "agent-regression-unrequested-target", TurnKey: "turn-1",
	})
	var unrequestedErr *AgentTaskError
	if !errors.As(err, &unrequestedErr) || unrequestedErr.HTTPStatus != http.StatusBadRequest || !strings.Contains(unrequestedErr.Message, "未在当前请求中明确指定") {
		t.Fatalf("unrequested checkpoint should be rejected before preview, got %v", err)
	}
	var revisions int64
	if err := model.DB.Model(&model.ProductRevision{}).Where("product_id = ?", fixture.product.ID).Count(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Fatalf("rejected catalog targets changed product history: revisions=%d", revisions)
	}
}

func TestAgentAIRegressionRejectsExplicitForeignTenantQualifierBeforeTaskCreation(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if err := rejectExplicitForeignTenantTarget(fixture.tenant.ID, "查询并把租户 SYS002 的成人票售价改为 1 元"); err == nil {
		t.Fatal("explicit foreign tenant qualifier was accepted")
	} else {
		var agentErr *AgentTaskError
		if !errors.As(err, &agentErr) || agentErr.Code != "invalid_request" || !strings.Contains(agentErr.Message, "当前租户") {
			t.Fatalf("unexpected foreign tenant error: %v", err)
		}
	}
	if err := rejectExplicitForeignTenantTarget(fixture.tenant.ID, "租户编号为 SYS002"); err == nil {
		t.Fatal("foreign tenant number qualifier was accepted")
	}
	if err := rejectExplicitForeignTenantTarget(fixture.tenant.ID, "票种名称为 SYS002纪念票，售价 1 元"); err != nil {
		t.Fatalf("business name containing a tenant-like token was rejected: %v", err)
	}
	if err := rejectExplicitForeignTenantTarget(fixture.tenant.ID, "票种名称为 2026成人票，售价 1 元"); err != nil {
		t.Fatalf("business name containing digits was rejected: %v", err)
	}
	if err := rejectExplicitForeignTenantTarget(fixture.tenant.ID, "租户 "+fixture.tenant.SystemCode+" 的成人票"); err != nil {
		t.Fatalf("current tenant qualifier was rejected: %v", err)
	}
	numericCodeTenant := model.Tenant{Name: "Numeric Agent Tenant", SystemCode: "1001", SecretKey: "numeric-agent", Status: "active"}
	if err := model.DB.Create(&numericCodeTenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := rejectExplicitForeignTenantTarget(numericCodeTenant.ID, "租户编号 1001 的成人票"); err != nil {
		t.Fatalf("current numeric tenant qualifier was rejected: %v", err)
	}
	if err := rejectExplicitForeignTenantTarget(numericCodeTenant.ID, "租户编号 1002 的成人票"); err == nil {
		t.Fatal("foreign numeric tenant qualifier was accepted")
	}
	var taskCount int64
	if err := model.DB.Model(&model.AgentTask{}).Where("tenant_id = ?", fixture.tenant.ID).Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != 0 {
		t.Fatalf("foreign tenant rejection created %d agent tasks", taskCount)
	}
}

func TestAgentTaskIdempotencyIsScopedToTenantAndActor(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	foreign := model.Tenant{
		Name:       "Agent Idempotency Other Tenant",
		SystemCode: fmt.Sprintf("AGENT-IDEMPOTENCY-%d", time.Now().UnixNano()),
		SecretKey:  "agent-idempotency-other",
		Status:     "active",
	}
	if err := model.DB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}

	service := &AgentTaskService{}
	request := AgentTaskRequest{IdempotencyKey: "same-agent-key"}
	first, err := service.loadOrCreateTask(fixture.tenant.ID, 11, "admin", request, "给成人票调整售价")
	if err != nil {
		t.Fatalf("create first scoped task: %v", err)
	}
	otherActor, err := service.loadOrCreateTask(fixture.tenant.ID, 22, "admin", request, "给儿童票调整售价")
	if err != nil {
		t.Fatalf("same key should be reusable by another actor: %v", err)
	}
	if otherActor.ID == first.ID {
		t.Fatalf("different actor reused task %d", first.ID)
	}
	otherTenant, err := service.loadOrCreateTask(foreign.ID, 11, "admin", request, "给外部票调整售价")
	if err != nil {
		t.Fatalf("same key should be reusable by another tenant: %v", err)
	}
	if otherTenant.ID == first.ID {
		t.Fatalf("different tenant reused task %d", first.ID)
	}

	if _, err := service.loadOrCreateTask(fixture.tenant.ID, 11, "admin", request, "改成另一项请求"); err == nil {
		t.Fatal("same actor reused an idempotency key for different input")
	} else {
		var conflict *AgentTaskError
		if !errors.As(err, &conflict) || conflict.HTTPStatus != http.StatusConflict {
			t.Fatalf("different input returned wrong error: %v", err)
		}
	}
}

func TestAgentReadOnlyCompoundIntentRecognizesNaturalSequencing(t *testing.T) {
	for _, input := range []string{
		"先查询最近一周订单，再查询明天库存，然后查看销售汇总",
		"依次查询订单、库存和核销报表",
		"第一步查看订单，第二步查看票种库存",
		"查询最近订单、库存和销售汇总",
	} {
		if !agentReadOnlyCompoundIntent(input) {
			t.Fatalf("natural read-only sequence was not recognized: %s", input)
		}
	}
	for _, input := range []string{
		"先查询订单，然后把成人票售价调整为 100 元",
		"查询库存并新增一个规则组",
		"按步骤查询订单后执行退款",
	} {
		if agentReadOnlyCompoundIntent(input) {
			t.Fatalf("mixed or mutating request was routed to read-only compound: %s", input)
		}
	}
}

func TestAgentPlanningTurnIsDurablySingleFlight(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	task := model.AgentTask{
		TenantID: fixture.tenant.ID, ActorUserID: 11, ActorRole: "admin",
		OperationType: AgentOperationPending, State: AgentTaskCollecting,
		InputText: "查询订单", ContextJSON: `{"operation_type":"pending"}`, MissingJSON: `[]`,
		IdempotencyKey: "planning-single-flight", Version: 1, ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := model.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	service := &AgentTaskService{}
	firstID, err := service.acquireAgentPlanningAttempt(fixture.tenant.ID, 11, task.ID, "admin", "turn-one")
	if err != nil || firstID == 0 {
		t.Fatalf("first planning turn was not acquired: id=%d err=%v", firstID, err)
	}
	if _, err := service.acquireAgentPlanningAttempt(fixture.tenant.ID, 11, task.ID, "admin", "turn-two"); err == nil {
		t.Fatal("concurrent planning turn was not rejected")
	} else {
		var inProgress *AgentTaskError
		if !errors.As(err, &inProgress) || inProgress.Code != "task_in_progress" {
			t.Fatalf("concurrent planning turn returned wrong error: %v", err)
		}
	}
	if err := service.finishAgentPlanningAttempt(firstID, "succeeded", ""); err != nil {
		t.Fatalf("finish planning turn: %v", err)
	}
	secondID, err := service.acquireAgentPlanningAttempt(fixture.tenant.ID, 11, task.ID, "admin", "turn-two")
	if err != nil || secondID == 0 || secondID == firstID {
		t.Fatalf("new planning turn was not acquired after release: id=%d first=%d err=%v", secondID, firstID, err)
	}
}

func TestAgentPlannerAcceptsProductContinuationAfterMisclassifiedCatalogTask(t *testing.T) {
	task := model.AgentTask{
		OperationType: AgentOperationCatalogBatchChange,
		InputText:     "创建一个线上成人票，名称为联调票",
		MissingJSON:   `[{"field":"operations"}]`,
	}
	envelope := &agentAIEnvelope{
		OperationType: AgentOperationTicketProductCreate,
		Product:       &agentProductCandidate{ProductType: "online"},
	}
	if err := validateAgentPlannerEnvelopeForTask("所属景区为韶关云门山，检票点北门，每点最多 1 次", task, envelope); err != nil {
		t.Fatalf("product continuation was rejected after a misclassified first turn: %v", err)
	}
	if !agentExplicitTicketProductCreateIntent("创建一个线上成人票，名称为联调票，使用北门检票点") {
		t.Fatal("explicit ticket product request was not recognized")
	}
	if agentExplicitTicketProductCreateIntent("创建规则组 W1，包含北门检票点") {
		t.Fatal("rule-group request was routed as product creation")
	}
}

func TestAgentAIRegressionRejectsToolIDsAndDoesNotReplayFailedToolCall(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	task := model.AgentTask{
		TenantID: fixture.tenant.ID, ActorUserID: 11, ActorRole: "admin",
		OperationType: AgentOperationCatalogBatchChange, State: AgentTaskCollecting,
		InputText: "给 Adult Ticket 增加 North Gate 检票点", ContextJSON: `{"operation_type":"catalog_batch_change"}`,
		MissingJSON: `[]`, IdempotencyKey: "agent-regression-tool-id-task", Version: 1,
		ExpiresAt: time.Now().Add(time.Hour), ProtocolMode: agentProtocolToolV1,
	}
	if err := model.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	call := AIToolCall{ID: "forged-id-call", Type: "function", Function: AIToolCallFunction{
		Name: "prepare_catalog_rule_change",
		Arguments: fmt.Sprintf(`{"operations":[{"kind":"add_checkpoints","product_ids":[%d],"checkpoint_ids":[%d]}]}`,
			fixture.product.ID, fixture.extra.ID),
	}}
	service := &AgentTaskService{}
	_, err := service.invokeAgentTool(fixture.tenant.ID, 11, "admin", task, task.InputText, model.PlatformAIConfig{}, "", 0, call)
	var invalid *AgentTaskError
	if !errors.As(err, &invalid) || invalid.HTTPStatus != http.StatusBadRequest || invalid.Code != "invalid_request" {
		t.Fatalf("model-supplied IDs were not rejected as input error: %v", err)
	}
	_, err = service.invokeAgentTool(fixture.tenant.ID, 11, "admin", task, task.InputText, model.PlatformAIConfig{}, "", 0, call)
	var conflict *AgentTaskError
	if !errors.As(err, &conflict) || conflict.HTTPStatus != http.StatusConflict {
		t.Fatalf("failed tool call was executed again instead of replay-protected: %v", err)
	}
	var events []model.AgentTaskEvent
	if err := model.DB.Where("task_id = ? AND tenant_id = ?", task.ID, fixture.tenant.ID).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != "failed" || events[0].ErrorCode != "invalid_request" {
		t.Fatalf("failed tool audit was not durable and single-shot: %+v", events)
	}
}

func TestAgentAIRegressionCheckpointProjectionUsesTenantNameForID8(t *testing.T) {
	rule := &model.TicketRule{Groups: []model.RuleGroup{{
		GroupName: "Water Park", Items: []model.RuleItem{{CheckPointID: 8, MaxPerCheckIn: 1}},
	}}}
	projection, err := projectCatalogRule(rule, map[uint]string{8: "水上乐园"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(projection, "\"checkpoint_name\":\"\"") || !strings.Contains(projection, "水上乐园") {
		t.Fatalf("preview projection did not hydrate checkpoint ID 8: %s", projection)
	}
	var decoded catalogRuleProjection
	if err := json.Unmarshal([]byte(projection), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Groups) != 1 || len(decoded.Groups[0].Items) != 1 || decoded.Groups[0].Items[0].CheckpointID != 8 || decoded.Groups[0].Items[0].CheckpointName != "水上乐园" {
		t.Fatalf("unexpected hydrated projection: %+v", decoded)
	}
}

func agentRegressionMissingField(fields []AgentMissingField, target string) bool {
	for _, field := range fields {
		if field.Field == target {
			return true
		}
	}
	return false
}
