package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
)

func TestAgentTaskCollectsMissingProductPriceBeforePreviewAndConfirmation(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []AIMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		for _, message := range body.Messages {
			if strings.Contains(message.Content, `"scenic_area_id"`) || strings.Contains(message.Content, `"checkpoint_id"`) {
				http.Error(writer, "server IDs leaked to provider", http.StatusBadRequest)
				return
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		if calls.Load() == 1 {
			_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"{\"operation_type\":\"ticket_product_create\",\"product\":{\"name\":\"Child Ticket\",\"scenic_area_name\":\"Batch Scenic\",\"price\":null,\"settlement_price\":null,\"groups\":[{\"group_name\":\"Admission\",\"items\":[{\"checkpoint_name\":\"Main Gate\"}]}]}}"}}],"usage":{"total_tokens":32}}`)
			return
		}
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"{\"operation_type\":\"ticket_product_create\",\"product\":{\"name\":\"Child Ticket\",\"product_type\":\"online\",\"scenic_area_name\":\"Batch Scenic\",\"price\":55,\"settlement_price\":30,\"groups\":[{\"group_name\":\"Admission\",\"items\":[{\"checkpoint_name\":\"Main Gate\",\"max_per_check_in\":1}]}]}}"}}],"usage":{"total_tokens":32}}`)
	}))
	defer server.Close()
	saveCatalogAIConfig(t, server.URL, 10)
	service := &AgentTaskService{}
	first, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{InputText: "创建一个儿童票，使用主门", IdempotencyKey: "agent-product-test", TurnKey: "turn-1"})
	if err != nil {
		t.Fatalf("first agent turn: %v", err)
	}
	if first.State != AgentTaskCollecting || first.CanConfirm || len(first.MissingFields) < 2 {
		t.Fatalf("first turn should collect prices: %+v", first)
	}
	hasProductTypeQuestion := false
	for _, field := range first.MissingFields {
		if field.Field == "product_type" {
			hasProductTypeQuestion = true
			break
		}
	}
	if !hasProductTypeQuestion {
		t.Fatalf("first turn did not ask for online/window product type: %+v", first.MissingFields)
	}
	var existing int64
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND name = ?", fixture.tenant.ID, "Child Ticket").Count(&existing).Error; err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Fatal("agent wrote a product before confirmation")
	}
	second, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{TaskID: first.TaskID, InputText: "售价 55 元，结算价 30 元", TurnKey: "turn-2"})
	if err != nil {
		t.Fatalf("second agent turn: %v", err)
	}
	if second.State != AgentTaskAwaitingConfirmation || !second.CanConfirm || second.Preview == nil {
		t.Fatalf("second turn should produce confirmation preview: %+v", second)
	}
	var preview map[string]interface{}
	if err := json.Unmarshal(second.Preview, &preview); err != nil {
		t.Fatal(err)
	}
	if preview["operation_type"] != AgentOperationTicketProductCreate || preview["scenic_area_name"] != "Batch Scenic" {
		t.Fatalf("unexpected product preview: %+v", preview)
	}
	previewProduct, ok := preview["product"].(map[string]interface{})
	if !ok || previewProduct["type"] != "online" || previewProduct["type_label"] != "线上票" || previewProduct["status_label"] != "未上架" {
		t.Fatalf("product preview did not separate type from status: %+v", previewProduct)
	}
	if strings.Contains(string(second.Preview), "tenant_id") || strings.Contains(string(second.Preview), "scenic_area_id") || strings.Contains(string(second.Preview), "checkpoint_id") || strings.Contains(string(second.Preview), "rule_id") {
		t.Fatalf("product preview leaked internal ownership identifiers: %s", string(second.Preview))
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", first.TaskID)
	if err != nil {
		t.Fatalf("confirm agent task: %v", err)
	}
	if completed.State != AgentTaskCompleted || completed.CanConfirm {
		t.Fatalf("task did not complete: %+v", completed)
	}
	var created model.Product
	if err := model.DB.Where("tenant_id = ? AND name = ?", fixture.tenant.ID, "Child Ticket").First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.Type != "online" || created.Status != "offline" || created.IsDistributable {
		t.Fatalf("agent-created product type/status/distribution boundary is wrong: %+v", created)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d, want 2", calls.Load())
	}
	if !strings.Contains(string(completed.Result), "product_id") {
		t.Fatalf("completed result did not expose created product id: %s", string(completed.Result))
	}
}

func TestAgentTaskReusesCatalogBatchPreviewAndConfirm(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := startCatalogAIProvider(t, `{"operation_type":"catalog_batch_change","operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["North Gate"]}]}`)
	saveCatalogAIConfig(t, server.URL, 10)
	service := &AgentTaskService{}
	before := fixture.product.CurrentRevisionID
	planned, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{InputText: "给 Adult Ticket 增加 North Gate", IdempotencyKey: "agent-batch-test", TurnKey: "turn-1"})
	if err != nil {
		t.Fatalf("batch agent planning: %v", err)
	}
	if planned.OperationType != AgentOperationCatalogBatchChange || planned.State != AgentTaskAwaitingConfirmation || !planned.CanConfirm || planned.PlanID == 0 {
		t.Fatalf("unexpected batch task preview: %+v", planned)
	}
	if _, err := service.Get(fixture.tenant.ID+999, 11, planned.TaskID); err == nil {
		t.Fatal("agent task was readable across tenants")
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", planned.TaskID)
	if err != nil {
		t.Fatalf("batch agent confirmation: %v", err)
	}
	if completed.State != AgentTaskCompleted || calls.Load() != 1 {
		t.Fatalf("batch task did not complete durably: %+v calls=%d", completed, calls.Load())
	}
	var product model.Product
	if err := model.DB.First(&product, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if product.CurrentRevisionID == before {
		t.Fatal("batch agent confirmation did not create a new product revision")
	}
}

func TestAgentProductDraftPreservesExplicitWindowType(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	price := 100.0
	settlement := 60.0
	draft := &agentProductDraft{
		Name:            "Window Ticket",
		ProductType:     "offline",
		ScenicAreaName:  fixture.area.Name,
		Price:           &price,
		SettlementPrice: &settlement,
		Groups: []agentRuleDraftGroup{{
			GroupName: "Admission",
			Items:     []agentRuleDraftItem{{CheckpointName: fixture.checkpoint.Name, MaxPerCheckIn: 1}},
		}},
	}
	product, _, missing, err := productFromDraft(model.DB, fixture.tenant.ID, draft)
	if err != nil {
		t.Fatalf("resolve explicit window product: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("explicit window product still has missing fields: %+v", missing)
	}
	if product.Type != "offline" || product.Status != "offline" || product.IsDistributable {
		t.Fatalf("explicit window product lost type/status boundary: %+v", product)
	}
}

func TestAgentPlannerPolicyRejectsInventedBroadOrOppositeOperations(t *testing.T) {
	add := []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}}}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, AllProducts: true, CheckpointNames: []string{"North Gate"}}}}); err == nil {
		t.Fatal("agent policy accepted a model-invented all-products scope")
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpRemoveCheckpoint, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}}}}); err == nil {
		t.Fatal("agent policy accepted a remove operation for an add request")
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 移除 North Gate", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}}}}); err == nil {
		t.Fatal("agent policy accepted an add operation for a remove request")
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 设置检票次数", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpSetLimit, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}, MaxPerCheckIn: intPtr(2)}}}); err == nil {
		t.Fatal("agent policy accepted a model-invented checkpoint limit")
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 设置每点最多 2 次", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpSetLimit, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}, MaxPerCheckIn: intPtr(2)}}}); err != nil {
		t.Fatalf("explicit checkpoint limit was rejected: %v", err)
	}
	if err := validateAgentPlannerEnvelope("全部票种增加 North Gate", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, AllProducts: true, CheckpointNames: []string{"North Gate"}}}}); err != nil {
		t.Fatalf("explicit all-products request was rejected: %v", err)
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: add, Product: &agentProductCandidate{Name: "Other Ticket"}}); err == nil {
		t.Fatal("agent policy accepted a mixed product and batch envelope")
	}
	if err := validateAgentPlannerEnvelope("创建一个成人票", &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: add}); err == nil {
		t.Fatal("agent policy accepted a batch plan for a product-create request")
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{Name: "Other Ticket"}}); err == nil {
		t.Fatal("agent policy accepted a product plan for a batch request")
	}
	if err := validateAgentPlannerEnvelope("帮我介绍一下系统", &agentAIEnvelope{OperationType: AgentOperationPending}); err == nil {
		t.Fatal("agent policy accepted an unrelated request")
	}
}

func TestAgentProductTypePolicyRejectsAmbiguousOrOppositeType(t *testing.T) {
	if err := validateAgentPlannerEnvelope("创建一个窗口票", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{ProductType: "online"}}); err == nil {
		t.Fatal("agent policy accepted online type for an explicit window request")
	}
	if err := validateAgentPlannerEnvelope("创建一个线上票", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{ProductType: "offline"}}); err == nil {
		t.Fatal("agent policy accepted window type for an explicit online request")
	}
	if err := validateAgentPlannerEnvelope("创建线上票和窗口票", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{ProductType: "online"}}); err == nil {
		t.Fatal("agent policy accepted a mixed product type request")
	}
	if err := validateAgentPlannerEnvelope("创建一个成人票", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{}}); err != nil {
		t.Fatalf("type-ambiguous request should be collected by the resolver: %v", err)
	}
	if err := validateAgentPlannerEnvelope("创建一个成人票", &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &agentProductCandidate{ProductType: "sideways"}}); err == nil {
		t.Fatal("agent policy accepted an unknown product type")
	}
}

func TestAgentRuntimeSkillIsVersionedAndSeparatesTypeFromStatus(t *testing.T) {
	skill, hash, err := agentDomainSkill(AgentOperationTicketProductCreate)
	if err != nil {
		t.Fatalf("load runtime skill: %v", err)
	}
	if agentDomainSkillVersion == "" || len(hash) != 64 {
		t.Fatalf("runtime skill metadata is incomplete: version=%q hash=%q", agentDomainSkillVersion, hash)
	}
	for _, term := range []string{"product_type=online", "product_type=offline", "status=offline", "Never default it"} {
		if !strings.Contains(skill, term) {
			t.Fatalf("runtime skill omitted %q: %s", term, skill)
		}
	}
}

func TestAgentInputIntentRejectsUnrelatedNewTasks(t *testing.T) {
	if err := validateAgentInputIntent("帮我介绍一下系统", AgentOperationPending); err == nil {
		t.Fatal("unrelated input was accepted for a new agent task")
	}
	if err := validateAgentInputIntent("有哪些检票点", AgentOperationPending); err == nil {
		t.Fatal("read-only catalog question was accepted as a mutation task")
	}
	if err := validateAgentInputIntent("给 Adult Ticket 增加 North Gate", AgentOperationPending); err != nil {
		t.Fatalf("catalog intent was rejected: %v", err)
	}
	if err := validateAgentInputIntent("创建成人票，售价 120 元", AgentOperationPending); err != nil {
		t.Fatalf("product intent was rejected: %v", err)
	}
	if err := validateAgentInputIntent("北门", AgentOperationTicketProductCreate); err != nil {
		t.Fatalf("short product continuation was rejected: %v", err)
	}
}

func TestDecodeAgentAIEnvelopeRejectsUnknownFields(t *testing.T) {
	if _, err := decodeAgentAIEnvelope(`{"operation_type":"catalog_batch_change","operations":[],"execute_sql":"drop table products"}`); err == nil {
		t.Fatal("agent decoder accepted an unknown execution field")
	}
	if _, err := decodeAgentAIEnvelope(`{"operation_type":"ticket_product_create","product":{"name":"Window Ticket","type":"offline"}}`); err == nil {
		t.Fatal("agent decoder accepted ambiguous product type field")
	}
}
