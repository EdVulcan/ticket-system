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
		var body struct{ Messages []AIMessage `json:"messages"` }
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
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"{\"operation_type\":\"ticket_product_create\",\"product\":{\"name\":\"Child Ticket\",\"scenic_area_name\":\"Batch Scenic\",\"price\":55,\"settlement_price\":30,\"groups\":[{\"group_name\":\"Admission\",\"items\":[{\"checkpoint_name\":\"Main Gate\",\"max_per_check_in\":1}]}]}}"}}],"usage":{"total_tokens":32}}`)
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
	if created.Status != "offline" || created.IsDistributable {
		t.Fatalf("agent-created product was not forced offline/non-distributable: %+v", created)
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
