package service

import (
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestExecutionCenterIsTenantScopedAndKeepsSourceStatuses(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	foreign := model.Tenant{Name: "Execution Center Foreign", SystemCode: fmt.Sprintf("EXEC-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign-secret", Status: "active"}
	if err := model.DB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rows := []model.DigitalRefundTask{
		{RefundID: 8101, TenantID: fixture.tenant.ID, Provider: "wechat", PaymentNo: "PAY-MANUAL", Status: "manual_review", AttemptCount: 3, MaxAttempts: 8, ManualReviewAt: &now, LastError: "商户凭据待复核"},
		{RefundID: 8102, TenantID: fixture.tenant.ID, Provider: "alipay", PaymentNo: "PAY-PENDING", Status: "pending", AttemptCount: 1, MaxAttempts: 8},
		{RefundID: 9101, TenantID: foreign.ID, Provider: "wechat", PaymentNo: "PAY-FOREIGN", Status: "manual_review", AttemptCount: 1, MaxAttempts: 8},
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	view, err := (&ExecutionCenterService{}).List(fixture.tenant.ID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary.Total != 2 || view.Summary.Critical != 1 || view.Summary.Warning != 1 {
		t.Fatalf("unexpected summary: %+v", view.Summary)
	}
	if view.Summary.ByCategory["退款"] != 2 {
		t.Fatalf("refund category missing: %+v", view.Summary.ByCategory)
	}
	for _, item := range view.Items {
		if item.Description == "PAY-FOREIGN" || item.Description == "PAY-FOREIGN · 商户凭据待复核" {
			t.Fatalf("foreign tenant item leaked: %+v", item)
		}
	}

	critical, err := (&ExecutionCenterService{}).List(fixture.tenant.ID, "退款", "critical", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(critical.Items) != 1 || critical.Items[0].Status != "manual_review" || critical.Items[0].Source != "digital_refund" {
		t.Fatalf("unexpected filtered items: %+v", critical.Items)
	}
}
