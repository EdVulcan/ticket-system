package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
)

func seedXiaohongshuRefundCoordination(t *testing.T) (xiaohongshuVoucherFixture, model.ChannelAccount, model.XiaohongshuRefundCoordination, model.User) {
	t.Helper()
	fixture := seedXiaohongshuVoucherFixture(t)
	var account model.ChannelAccount
	if err := model.DB.First(&account, fixture.link.ChannelAccountID).Error; err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"Event":"AFTER_SALE_REFUND","OrderId":"","AfterSaleId":"after-sale-coordination","RefundId":"refund-coordination"}`)
	ciphertext, err := utils.EncryptAES(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	event := model.XiaohongshuWebhookEvent{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID,
		PayloadHash: fmt.Sprintf("refund-event-%d", time.Now().UnixNano()), EventType: "AFTER_SALE_REFUND",
		PayloadCiphertext: ciphertext, Status: "manual_review", ReceivedAt: time.Now(),
	}
	if err := model.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return CreateXiaohongshuRefundCoordinationTx(tx, &account, &event, payload)
	}); err != nil {
		t.Fatal(err)
	}
	var coordination model.XiaohongshuRefundCoordination
	if err := model.DB.Where("webhook_event_id = ?", event.ID).First(&coordination).Error; err != nil {
		t.Fatal(err)
	}
	actor := model.User{TenantID: fixture.tenantID, Username: fmt.Sprintf("refund-resolution-%d", time.Now().UnixNano()), Password: "hash", Role: "admin"}
	if err := model.DB.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	return fixture, account, coordination, actor
}

func TestXiaohongshuRefundCoordinationResolutionPreservesLocalMoneyFacts(t *testing.T) {
	fixture, _, coordination, actor := seedXiaohongshuRefundCoordination(t)
	var order model.Order
	if err := model.DB.First(&order, fixture.ticket.OrderID).Error; err != nil {
		t.Fatal(err)
	}
	var paymentBefore model.Payment
	paymentBefore = model.Payment{TenantID: fixture.tenantID, PaymentNo: fmt.Sprintf("PAY-REFUND-COORD-%d", time.Now().UnixNano()), OrderNo: order.OrderNo, Amount: order.TotalAmount, AmountCents: int64(order.TotalAmount * 100), Method: "xiaohongshu", Status: "paid"}
	if err := model.DB.Create(&paymentBefore).Error; err != nil {
		t.Fatal(err)
	}
	resolve := XiaohongshuRefundResolutionRequest{
		TenantID: fixture.tenantID, CoordinationID: coordination.ID, ActorUserID: actor.ID, ActorRole: actor.Role,
		Action: "bind_order_hold", ExternalOrderID: mustXiaohongshuExternalOrderID(t, fixture.link.XiaohongshuOrderLinkID),
		Reason: "已核对渠道订单归属", Evidence: "渠道售后工单 REF-1", IdempotencyKey: "refund-resolution-1",
	}
	first, err := (XiaohongshuRefundCoordinationService{}).Resolve(resolve)
	if err != nil || first.State != "order_held" || first.Scope != "order" || first.XiaohongshuOrderLinkID != fixture.link.XiaohongshuOrderLinkID {
		t.Fatalf("bind result=%+v err=%v", first, err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return EnsureNoXiaohongshuRefundHoldTx(tx, &order)
	}); !errors.Is(err, ErrXiaohongshuRefundHold) {
		t.Fatalf("order hold error=%v, want fulfillment hold", err)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ? AND target_id = ?", fixture.tenantID, "xiaohongshu.refund_coordination.resolve", coordination.ID).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count after first resolution=%d, want 1", auditCount)
	}
	replayed, err := (XiaohongshuRefundCoordinationService{}).Resolve(resolve)
	if err != nil || replayed.State != "order_held" {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ? AND target_id = ?", fixture.tenantID, "xiaohongshu.refund_coordination.resolve", coordination.ID).Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("idempotent replay wrote %d audit rows", auditCount)
	}
	resolve.Action = "confirm_external_refund"
	resolve.ExternalOrderID = ""
	resolve.Reason = "渠道已确认退款，继续锁定履约"
	resolve.Evidence = "渠道退款凭据 REF-2"
	resolve.IdempotencyKey = "refund-resolution-2"
	confirmed, err := (XiaohongshuRefundCoordinationService{}).Resolve(resolve)
	if err != nil || confirmed.State != "external_refund_confirmed" || confirmed.Scope != "order" {
		t.Fatalf("confirm result=%+v err=%v", confirmed, err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return EnsureNoXiaohongshuRefundHoldTx(tx, &order)
	}); !errors.Is(err, ErrXiaohongshuRefundHold) {
		t.Fatalf("confirmed external refund released hold: %v", err)
	}
	var paymentAfter model.Payment
	if err := model.DB.First(&paymentAfter, paymentBefore.ID).Error; err != nil {
		t.Fatal(err)
	}
	if paymentAfter.Status != paymentBefore.Status || paymentAfter.AmountCents != paymentBefore.AmountCents {
		t.Fatalf("external refund changed local payment before protocol confirmation: before=%+v after=%+v", paymentBefore, paymentAfter)
	}
	var refundCount int64
	if err := model.DB.Model(&model.Refund{}).Where("order_no = ?", order.OrderNo).Count(&refundCount).Error; err != nil {
		t.Fatal(err)
	}
	if refundCount != 0 {
		t.Fatalf("external refund created %d local refunds", refundCount)
	}
	resolve.Action = "dismiss_no_refund"
	resolve.Reason = "尝试错误解除"
	resolve.Evidence = "无"
	resolve.IdempotencyKey = "refund-resolution-3"
	if _, err := (XiaohongshuRefundCoordinationService{}).Resolve(resolve); !errors.Is(err, ErrXiaohongshuRefundResolutionInvalid) {
		t.Fatalf("external refund hold became dismissible: %v", err)
	}
}

func TestXiaohongshuRefundCoordinationAutoBindsKnownOrder(t *testing.T) {
	fixture := seedXiaohongshuVoucherFixture(t)
	var account model.ChannelAccount
	if err := model.DB.First(&account, fixture.link.ChannelAccountID).Error; err != nil {
		t.Fatal(err)
	}
	var link model.XiaohongshuOrderLink
	if err := model.DB.First(&link, fixture.link.XiaohongshuOrderLinkID).Error; err != nil {
		t.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"Event":"AFTER_SALE_REFUND","OrderId":%q,"AfterSaleId":"after-sale-known","RefundId":"refund-known"}`, link.ExternalOrderID))
	ciphertext, err := utils.EncryptAES(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	event := model.XiaohongshuWebhookEvent{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID,
		PayloadHash: fmt.Sprintf("refund-known-event-%d", time.Now().UnixNano()), EventType: "AFTER_SALE_REFUND",
		PayloadCiphertext: ciphertext, Status: "manual_review", ReceivedAt: time.Now(),
	}
	if err := model.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return CreateXiaohongshuRefundCoordinationTx(tx, &account, &event, payload)
	}); err != nil {
		t.Fatal(err)
	}
	var coordination model.XiaohongshuRefundCoordination
	if err := model.DB.Where("webhook_event_id = ?", event.ID).First(&coordination).Error; err != nil {
		t.Fatal(err)
	}
	if coordination.Scope != "order" || coordination.State != "order_held" || coordination.XiaohongshuOrderLinkID != link.ID {
		t.Fatalf("known order was not auto-bound: %+v", coordination)
	}
}

func mustXiaohongshuExternalOrderID(t *testing.T, orderLinkID uint) string {
	t.Helper()
	var link model.XiaohongshuOrderLink
	if err := model.DB.First(&link, orderLinkID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(link.ExternalOrderID) == "" {
		t.Fatal("fixture external order id is empty")
	}
	return link.ExternalOrderID
}
