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
