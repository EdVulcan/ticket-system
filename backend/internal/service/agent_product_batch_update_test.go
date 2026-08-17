package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"ticket-backend/internal/model"
)

func seedBatchUpdateProduct(t *testing.T, fixture catalogBatchFixture, name string) model.Product {
	t.Helper()
	product := model.Product{
		Name: name, Price: 80, SettlementPrice: 45, TenantID: fixture.tenant.ID,
		ScenicAreaID: fixture.area.ID, Type: "online", Status: "offline", ValidityType: "date",
		StockType: "unlimited", CodeMode: "ticket", RefundType: "free", GateVoiceCode: "welcome",
	}
	rule := model.TicketRule{Name: name + " Rule", TenantID: fixture.tenant.ID, ValidityType: "date", Groups: []model.RuleGroup{{GroupName: "Admission", MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: fixture.checkpoint.ID, MaxPerCheckIn: 1}}}}}
	if err := (&ProductService{}).Create(&product, &rule); err != nil {
		t.Fatal(err)
	}
	return product
}

func TestAgentProductBatchUpdatePreviewsAndConfirmsAtomically(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	child := seedBatchUpdateProduct(t, fixture, "Child Ticket")
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-product-batch-update", "prepare_ticket_product_batch_update", `{"product_names":["Adult Ticket","Child Ticket"],"changes":{"price":120,"tags":"暑期"}}`, 28), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	view, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "批量修改 Adult Ticket 和 Child Ticket 的售价为 120 元，并增加标签暑期", IdempotencyKey: "agent-product-batch-update-1", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("batch product update preview: %v", err)
	}
	if view.OperationType != AgentOperationTicketProductBatchUpdate || view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm || calls.Load() != 1 {
		t.Fatalf("unexpected batch product update preview: %+v provider_calls=%d", view, calls.Load())
	}
	var preview agentProductBatchUpdatePreview
	if err := json.Unmarshal(view.Preview, &preview); err != nil {
		t.Fatalf("decode batch preview: %v", err)
	}
	if preview.ProductCount != 2 || len(preview.Lines) != 2 || len(preview.Changes) != 2 {
		t.Fatalf("unexpected batch preview: %+v", preview)
	}
	if strings.Contains(string(view.Preview), "product_id") || strings.Contains(string(view.Preview), "revision_id") || strings.Contains(string(view.Preview), "tenant_id") {
		t.Fatalf("batch preview leaked internal IDs: %s", string(view.Preview))
	}
	var beforeAdult, beforeChild model.Product
	if err := model.DB.First(&beforeAdult, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&beforeChild, child.ID).Error; err != nil {
		t.Fatal(err)
	}
	if beforeAdult.Price != fixture.product.Price || beforeChild.Price != child.Price || beforeAdult.Tags != "" || beforeChild.Tags != "" {
		t.Fatal("batch preview changed a product before confirmation")
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", view.TaskID)
	if err != nil {
		t.Fatalf("confirm batch product update: %v", err)
	}
	if completed.State != AgentTaskCompleted || completed.CanConfirm {
		t.Fatalf("batch product update did not complete: %+v", completed)
	}
	for _, id := range []uint{fixture.product.ID, child.ID} {
		var updated model.Product
		if err := model.DB.First(&updated, id).Error; err != nil {
			t.Fatal(err)
		}
		if updated.Price != 120 || updated.Tags != "暑期" || updated.Status != "offline" || updated.IsDistributable {
			t.Fatalf("unexpected batch-updated product: %+v", updated)
		}
	}
}

func TestAgentProductBatchUpdateAllowsListedDistributableOwnedProducts(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Updates(map[string]interface{}{"status": "online", "is_distributable": true}).Error; err != nil {
		t.Fatal(err)
	}
	child := seedBatchUpdateProduct(t, fixture, "Child Online Ticket")
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", child.ID, fixture.tenant.ID).Updates(map[string]interface{}{"status": "online", "is_distributable": true}).Error; err != nil {
		t.Fatal(err)
	}
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-product-batch-update-listed", "prepare_ticket_product_batch_update", `{"target_scope":{"version":1,"intent":"batch","product_type":"online"},"changes":{"refund_type":"free"}}`, 24), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	view, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "把所有线上门票设置为未核销随时退", IdempotencyKey: "agent-product-batch-listed", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("listed batch product preview: %v", err)
	}
	if view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm || calls.Load() != 1 {
		t.Fatalf("unexpected listed batch product preview: %+v provider_calls=%d", view, calls.Load())
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", view.TaskID)
	if err != nil {
		t.Fatalf("confirm listed batch product update: %v", err)
	}
	if completed.State != AgentTaskCompleted || completed.CanConfirm {
		t.Fatalf("listed batch product update did not complete: %+v", completed)
	}
	for _, id := range []uint{fixture.product.ID, child.ID} {
		var updated model.Product
		if err := model.DB.First(&updated, id).Error; err != nil {
			t.Fatal(err)
		}
		if updated.Status != "online" || !updated.IsDistributable || updated.RefundType != "free" {
			t.Fatalf("listed/distributable owned product was not updated: %+v", updated)
		}
	}
}

func TestAgentProductBatchUpdateRejectsPartialConflict(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", fixture.product.ID, fixture.tenant.ID).Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	child := seedBatchUpdateProduct(t, fixture, "Child Ticket")
	server, _ := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-product-batch-conflict", "prepare_ticket_product_batch_update", `{"product_names":["Adult Ticket","Child Ticket"],"changes":{"price":130}}`, 20), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	view, err := service.Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "批量修改 Adult Ticket 和 Child Ticket 的售价为 130 元", IdempotencyKey: "agent-product-batch-conflict-1", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("batch preview: %v", err)
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", child.ID, fixture.tenant.ID).Update("source_product_id", 999).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.Confirm(fixture.tenant.ID, 11, "admin", view.TaskID)
	var taskErr *AgentTaskError
	if !errors.As(err, &taskErr) || taskErr.HTTPStatus != 409 {
		t.Fatalf("expected batch conflict, got %v", err)
	}
	var adult model.Product
	if err := model.DB.First(&adult, fixture.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if adult.Price != fixture.product.Price {
		t.Fatal("batch conflict partially updated the first product")
	}
	var task model.AgentTask
	if err := model.DB.First(&task, view.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if task.State != AgentTaskAwaitingConfirmation {
		t.Fatalf("conflicted batch task should remain confirmable, state=%s", task.State)
	}
}

func TestAgentProductBatchUpdatePolicyRejectsRenameAndSingleTarget(t *testing.T) {
	price := 120.0
	if err := validateAgentPlannerEnvelope("批量修改两个票种", &agentAIEnvelope{OperationType: AgentOperationTicketProductBatchUpdate, ProductBatchUpdate: &agentProductBatchUpdateCandidate{ProductNames: []string{"Adult Ticket"}, Changes: agentProductUpdateChanges{Price: &price}}}); err == nil {
		t.Fatal("batch update accepted a single target")
	}
	name := "New Name"
	if err := validateAgentPlannerEnvelope("批量修改 Adult Ticket 和 Child Ticket", &agentAIEnvelope{OperationType: AgentOperationTicketProductBatchUpdate, ProductBatchUpdate: &agentProductBatchUpdateCandidate{ProductNames: []string{"Adult Ticket", "Child Ticket"}, Changes: agentProductUpdateChanges{Name: &name}}}); err == nil {
		t.Fatal("batch update accepted a shared rename")
	}
}

func TestAgentProductBatchUpdatePreviewShape(t *testing.T) {
	preview := agentProductBatchUpdatePreview{OperationType: AgentOperationTicketProductBatchUpdate, ProductCount: 2, Changes: []string{"售价"}}
	encoded, err := json.Marshal(preview)
	if err != nil || !strings.Contains(string(encoded), `"operation_type":"ticket_product_batch_update"`) {
		t.Fatalf("unexpected batch preview shape: %s err=%v", encoded, err)
	}
}
