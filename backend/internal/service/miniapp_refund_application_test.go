package service

import (
	"context"
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

func xiaohongshuRefundCustomer(t *testing.T, fixture xiaohongshuRefundFixture) model.MiniappCustomer {
	t.Helper()
	var customer model.MiniappCustomer
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	return customer
}

func TestMiniappRefundButtonProjectionUsesSaleEvidence(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	customer := xiaohongshuRefundCustomer(t, fixture)
	svc := NewMiniappService()
	result, err := svc.GetXiaohongshuOrder(context.Background(), &customer, fixture.order.OrderNo)
	if err != nil || !result.CanApplyRefund {
		t.Fatalf("eligible unused order must expose refund button: result=%+v err=%v", result, err)
	}
	// Legacy operations without a product-type snapshot must explain the
	// unavailable action, never infer refund eligibility from today's product.
	var operation model.XiaohongshuOrderOperation
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := encryptXiaohongshuOrderOperationPayload(payload.Request, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&operation).Update("request_payload_ciphertext", ciphertext).Error; err != nil {
		t.Fatal(err)
	}
	result, err = svc.GetXiaohongshuOrder(context.Background(), &customer, fixture.order.OrderNo)
	if err != nil || result.CanApplyRefund || result.RefundApplicationMessage == "" {
		t.Fatalf("legacy order must explain unavailable refund: result=%+v err=%v", result, err)
	}
}

func TestMiniappRefundApplicationCreatesAtomicProcessingChain(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	customer := xiaohongshuRefundCustomer(t, fixture)
	svc := NewMiniappService()
	input := MiniappRefundApplicationInput{ClientRequestID: "customer-refund-1", Reason: "行程有变"}

	created, err := svc.ApplyXiaohongshuRefund(&customer, fixture.order.OrderNo, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.RequestNo == "" || created.Status != "processing" {
		t.Fatalf("application=%+v", created)
	}
	replayed, err := svc.ApplyXiaohongshuRefund(&customer, fixture.order.OrderNo, input)
	if err != nil || replayed.RequestNo != created.RequestNo || replayed.Status != "processing" {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err := svc.ApplyXiaohongshuRefund(&customer, fixture.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: input.ClientRequestID, Reason: "不同原因"}); err == nil {
		t.Fatal("changed idempotent content was accepted")
	}
	var request model.AfterSaleRequest
	if err := model.DB.Where("request_no = ?", created.RequestNo).First(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Type != "refund" || request.Status != "processing" || request.OperatorID != 0 || request.ReviewerID != 0 || request.ReviewedAt == nil || request.AmountCents != fixture.refundAmount || request.PaymentMethod != "xiaohongshu" || request.Reason != input.Reason || request.RefundID == 0 {
		t.Fatalf("stored request=%+v", request)
	}
	var event model.AfterSaleEvent
	if err := model.DB.Where("request_no = ? AND action = ?", created.RequestNo, "customer_applied").First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.ActorID != 0 || event.Reason == input.Reason {
		t.Fatalf("customer event=%+v", event)
	}
	event = model.AfterSaleEvent{}
	if err := model.DB.Where("request_no = ? AND action = ? AND actor_id = ?", created.RequestNo, "system_approved", 0).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	var refunds int64
	if err := model.DB.Model(&model.Refund{}).Where("tenant_id = ? AND order_no = ? AND status = ?", fixture.tenantID, fixture.order.OrderNo, "pending").Count(&refunds).Error; err != nil || refunds != 1 {
		t.Fatalf("refund count=%d err=%v", refunds, err)
	}
	var tasks, operations int64
	if err := model.DB.Model(&model.DigitalRefundTask{}).Where("refund_id = ? AND tenant_id = ? AND status = ?", request.RefundID, fixture.tenantID, "pending").Count(&tasks).Error; err != nil || tasks != 1 {
		t.Fatalf("digital refund tasks=%d err=%v", tasks, err)
	}
	if err := model.DB.Model(&model.XiaohongshuRefundOperation{}).Where("refund_id = ? AND tenant_id = ? AND state = ?", request.RefundID, fixture.tenantID, "prepared").Count(&operations).Error; err != nil || operations != 1 {
		t.Fatalf("xiaohongshu refund operations=%d err=%v", operations, err)
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.PendingRefundID != request.RefundID {
		t.Fatalf("application did not reserve ticket=%+v request=%+v", ticket, request)
	}
	result, err := svc.GetXiaohongshuOrder(context.Background(), &customer, fixture.order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if result.RefundApplicationStatus != "processing" || result.RefundApplicationNo != created.RequestNo || result.CanApplyRefund || !result.RefundPending || len(result.TicketCodes) != 0 {
		t.Fatalf("projection=%+v", result)
	}
}

func TestMiniappRefundApplicationRejectsOtherCustomerAndIneligibleTickets(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	owner := xiaohongshuRefundCustomer(t, fixture)
	other := owner
	other.Base = model.Base{}
	other.OpenIDHash = hashMiniappValue("other-customer")
	other.SessionTokenHash = hashMiniappValue("other-token")
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewMiniappService()
	if _, err := svc.ApplyXiaohongshuRefund(&other, fixture.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "other-customer", Reason: "行程有变"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("other customer error=%v", err)
	}
	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", fixture.ticket.ID).Updates(map[string]interface{}{"status": "used", "check_in_count": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyXiaohongshuRefund(&owner, fixture.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "used-ticket", Reason: "行程有变"}); err == nil {
		t.Fatal("used ticket application was accepted")
	}
	var count int64
	if err := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id = ? AND order_no = ?", fixture.tenantID, fixture.order.OrderNo).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected after-sale writes=%d err=%v", count, err)
	}
}

func TestMiniappRefundApplicationRejectsSaleTimeNoRefundAndUnsupportedSnapshot(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, fixture xiaohongshuRefundFixture)
	}{
		{
			name: "no_refund",
			prepare: func(t *testing.T, fixture xiaohongshuRefundFixture) {
				t.Helper()
				if err := model.DB.Model(&model.OrderItem{}).Where("order_id = ?", fixture.order.ID).Update("refund_type", "no_refund").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unsupported_snapshot",
			prepare: func(t *testing.T, fixture xiaohongshuRefundFixture) {
				t.Helper()
				var link model.XiaohongshuOrderLink
				if err := model.DB.Where("order_id = ?", fixture.order.ID).First(&link).Error; err != nil {
					t.Fatal(err)
				}
				var operation model.XiaohongshuOrderOperation
				if err := model.DB.Where("xiaohongshu_order_link_id = ?", link.ID).First(&operation).Error; err != nil {
					t.Fatal(err)
				}
				payload, err := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
				if err != nil {
					t.Fatal(err)
				}
				ciphertext, err := encryptXiaohongshuOrderOperationPayload(payload.Request, xiaohongshu.ProductTypePresaleVoucher)
				if err != nil {
					t.Fatal(err)
				}
				if err := model.DB.Model(&operation).Update("request_payload_ciphertext", ciphertext).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedXiaohongshuRefundFixture(t)
			test.prepare(t, fixture)
			customer := xiaohongshuRefundCustomer(t, fixture)
			if _, err := NewMiniappService().ApplyXiaohongshuRefund(&customer, fixture.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: test.name, Reason: "行程有变"}); err == nil {
				t.Fatal("ineligible order application was accepted")
			}
			var count int64
			if err := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id = ? AND order_no = ?", fixture.tenantID, fixture.order.OrderNo).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("unexpected after-sale writes=%d err=%v", count, err)
			}
		})
	}
}

func TestMiniappRefundApplicationWorkerCompletesExistingChain(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	customer := xiaohongshuRefundCustomer(t, fixture)
	created, err := NewMiniappService().ApplyXiaohongshuRefund(&customer, fixture.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "worker-success", Reason: "行程有变"})
	if err != nil {
		t.Fatal(err)
	}
	var request model.AfterSaleRequest
	if err := model.DB.Where("request_no = ?", created.RequestNo).First(&request).Error; err != nil {
		t.Fatal(err)
	}
	_, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	refundService := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, refundService, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, refundService, time.Now().Add(time.Minute))
	if err := (&AfterSaleService{}).ReconcileRefunds(); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&request, request.ID).Error; err != nil || request.Status != "completed" || request.RefundID == 0 {
		t.Fatalf("completed request=%+v err=%v", request, err)
	}
}

func TestMiniappRefundApplicationPublicRefundReplaySurvivesTerminalPayment(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	key := "terminal-public-replay"
	created, err := (&RefundService{}).CreateDigitalRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, key, float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Refund{}).Where("id = ?", created.ID).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Payment{}).Where("id = ?", fixture.payment.ID).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	replayed, err := (&RefundService{}).CreateDigitalRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, key, float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "游客取消")
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("terminal replay=%+v created=%+v err=%v", replayed, created, err)
	}
}
