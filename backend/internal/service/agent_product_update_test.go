package service

import (
	"encoding/json"
	"strings"
	"testing"
	"ticket-backend/internal/model"
)

func TestAgentProductUpdatePreviewsAndConfirmsOnlyUnpublishedProduct(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-product-update", "prepare_ticket_product_update", `{"product_name":"Adult Ticket","changes":{"price":120,"tags":"暑期"}}`, 22), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	view, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "修改票种 Adult Ticket 的售价为 120 元，并增加标签暑期", IdempotencyKey: "agent-product-update-1", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("product update preview: %v", err)
	}
	if view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm || calls.Load() != 1 {
		t.Fatalf("unexpected product update preview: %+v provider_calls=%d", view, calls.Load())
	}
	if strings.Contains(string(view.Preview), "product_id") || strings.Contains(string(view.Preview), "revision_id") || strings.Contains(string(view.Preview), "tenant_id") {
		t.Fatalf("product update preview leaked internal IDs: %s", string(view.Preview))
	}
	var before model.Product
	if err := model.DB.First(&before, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	var revisionsBefore int64
	if err := model.DB.Model(&model.ProductRevision{}).Where("product_id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Count(&revisionsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if before.Price != fixture.product.Price || before.Tags != "" {
		t.Fatalf("preview changed product before confirmation: %+v", before)
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", view.TaskID)
	if err != nil {
		t.Fatalf("confirm product update: %v", err)
	}
	if completed.State != AgentTaskCompleted || completed.CanConfirm {
		t.Fatalf("product update did not complete: %+v", completed)
	}
	var updated model.Product
	if err := model.DB.First(&updated, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Price != 120 || updated.Tags != "暑期" || updated.Status != "offline" || updated.IsDistributable {
		t.Fatalf("unexpected updated product: %+v", updated)
	}
	var revisionsAfter int64
	if err := model.DB.Model(&model.ProductRevision{}).Where("product_id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Count(&revisionsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore+1 {
		t.Fatalf("product revision count=%d, want %d", revisionsAfter, revisionsBefore+1)
	}
}

func TestAgentProductUpdateRejectsOnlineOrDistributedProduct(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	price := 120.0
	draft := &agentProductUpdateDraft{ProductName: fixture.product.Name, Changes: agentProductUpdateChanges{Price: &price}}
	if _, _, err := resolveProductUpdateDraft(model.DB, fixture.tenant.ID, draft); err == nil {
		t.Fatal("online product was accepted for AI update")
	}
}

func TestAgentProductUpdateEnvelopeRejectsRuleMutation(t *testing.T) {
	price := 120.0
	if err := validateAgentPlannerEnvelope("修改票种 Adult Ticket 的检票点", &agentAIEnvelope{
		OperationType: AgentOperationTicketProductUpdate,
		ProductUpdate: &agentProductUpdateCandidate{ProductName: "Adult Ticket", Changes: agentProductUpdateChanges{Price: &price}},
	}); err == nil {
		t.Fatal("product update accepted a checkpoint-rule request")
	}
}

func TestAgentProductUpdatePreviewShape(t *testing.T) {
	preview := agentProductUpdatePreview{OperationType: AgentOperationTicketProductUpdate, ProductName: "Adult Ticket", Changes: []string{"售价"}}
	encoded, err := json.Marshal(preview)
	if err != nil || !strings.Contains(string(encoded), `"operation_type":"ticket_product_update"`) {
		t.Fatalf("unexpected preview shape: %s err=%v", encoded, err)
	}
}
