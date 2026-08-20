package service

import (
	"fmt"
	"strings"
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

func TestExecutionCenterProjectsOnlyGateRecoveryForCurrentTenant(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	foreign := model.Tenant{Name: "Execution Gate Foreign", SystemCode: fmt.Sprintf("EXEC-GATE-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign-secret", Status: "active"}
	if err := model.DB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	foreignArea := model.ScenicArea{TenantID: foreign.ID, Code: fmt.Sprintf("EXEC-GATE-AREA-%d", time.Now().UnixNano()), Name: "Foreign Scenic", Status: "active"}
	if err := model.DB.Create(&foreignArea).Error; err != nil {
		t.Fatal(err)
	}
	checkpointID := fixture.checkpoint.ID
	foreignCheckpointID := uint(0)
	foreignCheckpoint := model.CheckPoint{TenantID: foreign.ID, ScenicAreaID: foreignArea.ID, Name: "Foreign Gate"}
	if err := model.DB.Create(&foreignCheckpoint).Error; err != nil {
		t.Fatal(err)
	}
	foreignCheckpointID = foreignCheckpoint.ID
	gate := model.Device{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, CheckPointID: &checkpointID, Name: "Recovery Gate", SerialNumber: fmt.Sprintf("EXEC-GATE-%d", time.Now().UnixNano()), Type: "gate", Status: "online"}
	handheld := model.Device{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, CheckPointID: &checkpointID, Name: "Recovery Handheld", SerialNumber: fmt.Sprintf("EXEC-HANDHELD-%d", time.Now().UnixNano()), Type: "handheld", Status: "online"}
	foreignGate := model.Device{TenantID: foreign.ID, ScenicAreaID: foreignArea.ID, CheckPointID: &foreignCheckpointID, Name: "Foreign Gate", SerialNumber: fmt.Sprintf("EXEC-FOREIGN-GATE-%d", time.Now().UnixNano()), Type: "gate", Status: "online"}
	for _, device := range []*model.Device{&gate, &handheld, &foreignGate} {
		if err := model.DB.Create(device).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := []model.DeviceVerification{
		{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, DeviceID: gate.ID, RequestID: "gate-pending", RequestHash: "hash-gate-pending", TicketCode: "GATE-PENDING", Status: "completed", Result: "allow", OpenStatus: "pending"},
		{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, DeviceID: handheld.ID, RequestID: "handheld-failed", RequestHash: "hash-handheld-failed", TicketCode: "HANDHELD-FAILED", Status: "completed", Result: "allow", OpenStatus: "failed", OpenError: "手持机结果待确认"},
		{TenantID: foreign.ID, ScenicAreaID: foreignArea.ID, DeviceID: foreignGate.ID, RequestID: "foreign-failed", RequestHash: "hash-foreign-failed", TicketCode: "FOREIGN-FAILED", Status: "completed", Result: "allow", OpenStatus: "failed", OpenError: "foreign"},
	}
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	view, err := (&ExecutionCenterService{}).List(fixture.tenant.ID, "现场设备", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 || view.Items[0].Source != "device_verification" || view.Items[0].Status != "pending" || view.Items[0].Retryable {
		t.Fatalf("unexpected gate recovery projection: %+v", view.Items)
	}
	if strings.Contains(view.Items[0].Description, "HANDHELD") || strings.Contains(view.Items[0].Description, "FOREIGN") {
		t.Fatalf("non-gate or foreign verification leaked: %+v", view.Items[0])
	}
}
