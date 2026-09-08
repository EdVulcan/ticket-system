package service

import (
	"context"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

func TestXiaohongshuRefundResponseMustMatchExactSnapshot(t *testing.T) {
	request := xiaohongshu.AfterSalesAddRequest{ExternalOrderID: "order", ExternalAfterSalesOrderID: "refund", OpenID: "customer", Price: xiaohongshu.AfterSalesPriceInfo{RefundPrice: 100}, Vouchers: []xiaohongshu.AfterSalesVoucherDetail{{VoucherCode: " RAW ", RefundPrice: 100}}}
	valid := xiaohongshu.AfterSalesOrderResponse{ExternalOrderID: "order", ExternalAfterSalesOrderID: "refund", OpenID: "customer", Type: 1, ProductType: 1, Status: 2, Price: request.Price, Vouchers: request.Vouchers}
	for _, change := range []func(*xiaohongshu.AfterSalesOrderResponse){
		func(r *xiaohongshu.AfterSalesOrderResponse) { r.ExternalOrderID = "other" },
		func(r *xiaohongshu.AfterSalesOrderResponse) { r.ExternalAfterSalesOrderID = "other" },
		func(r *xiaohongshu.AfterSalesOrderResponse) { r.OpenID = "other" },
		func(r *xiaohongshu.AfterSalesOrderResponse) { r.Price.RefundPrice = 99 },
		func(r *xiaohongshu.AfterSalesOrderResponse) {
			r.Vouchers = []xiaohongshu.AfterSalesVoucherDetail{{VoucherCode: "RAW", RefundPrice: 100}}
		},
		func(r *xiaohongshu.AfterSalesOrderResponse) { r.Vouchers = nil },
	} {
		result := valid
		change(&result)
		if err := validateXiaohongshuRefundResult(request, &result); err == nil {
			t.Fatal("mismatched provider result accepted")
		}
	}
	if err := validateXiaohongshuRefundResult(request, &valid); err != nil {
		t.Fatal(err)
	}
}

func TestXiaohongshuRefundPendingSuppressesServerTicketCodes(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", fixture.order.ID, fixture.tenantID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	service := XiaohongshuOrderService{}
	before, err := service.orderResult(&link, &fixture.order, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.RefundPending || len(before.TicketCodes) != 1 {
		t.Fatal("ready order must expose its original ticket")
	}
	createXiaohongshuRefund(t, fixture, "refund-projection")
	during, err := service.orderResult(&link, &fixture.order, false)
	if err != nil {
		t.Fatal(err)
	}
	if !during.RefundPending || len(during.TicketCodes) != 0 || during.CoreOrderStatus != "paid" {
		t.Fatal("pending refund must hide ticket codes without rewriting payment state")
	}
}

func TestXiaohongshuRefundLocalCommitFailureRecoversByQueryOnly(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "commit-recovery")
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	now := time.Now()
	runXiaohongshuRefundWorker(t, service, now.Add(time.Second))
	// Inject a failure after the normal ticket/payment updates, inside the same
	// finalization transaction. This table exists only in the isolated test DB.
	if err := model.DB.Exec("ALTER TABLE xiaohongshu_refund_operations ADD CONSTRAINT test_refund_commit_failure CHECK (state <> 'succeeded')").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		model.DB.Exec("ALTER TABLE xiaohongshu_refund_operations DROP CONSTRAINT IF EXISTS test_refund_commit_failure")
	})
	if _, err := service.ProcessDigitalRefundTasks(context.Background(), now.Add(time.Minute), 1); err == nil {
		t.Fatal("injected finalization failure was ignored")
	}
	var ticket model.Ticket
	var payment model.Payment
	var stored model.Refund
	if err := model.DB.First(&ticket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&payment, fixture.payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&stored, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || payment.RefundedAmountCents != 0 || ticket.Status != "unused" || ticket.PendingRefundID != refund.ID {
		t.Fatal("failed transaction leaked partial refund facts")
	}
	if err := model.DB.Exec("ALTER TABLE xiaohongshu_refund_operations DROP CONSTRAINT test_refund_commit_failure").Error; err != nil {
		t.Fatal(err)
	}
	// A fresh worker reclaims the stale lease after a process restart.
	service = xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, service, now.Add(staleDigitalRefundTaskAfter+2*time.Minute))
	if err := model.DB.First(&stored, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&payment, fixture.payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || payment.RefundedAmountCents != fixture.refundAmount || fake.addCalls.Load() != 1 || fake.getCalls.Load() != 2 {
		t.Fatalf("query-only recovery failed: refund=%s amount=%d add=%d get=%d", stored.Status, payment.RefundedAmountCents, fake.addCalls.Load(), fake.getCalls.Load())
	}
}

func TestXiaohongshuRefundCallbackDoesNotStealProcessingLease(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "callback-lease")
	claimed, err := claimDigitalRefundTask(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// Compare the persisted lease, since PostgreSQL stores microsecond precision.
	var before model.DigitalRefundTask
	if err := model.DB.First(&before, claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	event := model.XiaohongshuWebhookEvent{TenantID: fixture.tenantID, ChannelAccountID: fixture.account.ID, PayloadHash: "callback-lease", EventType: "REFUND_RESULT", PayloadCiphertext: "test-only", ReceivedAt: time.Now()}
	if err := model.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	// The event is already authenticated by the caller; this tests only the
	// transactional wakeup, not the independently covered signature protocol.
	err = model.Write(func(tx *gorm.DB) error {
		handled, err := wakeXiaohongshuRefundTx(tx, &fixture.account, &event, []byte(fmt.Sprintf(`{"OutAfterSalesOrderId":%q,"Status":2}`, refund.RefundNo)))
		if !handled && err == nil {
			return fmt.Errorf("known refund not handled")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var task model.DigitalRefundTask
	if err := model.DB.First(&task, claimed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "processing" || task.LockedAt == nil || before.LockedAt == nil || !task.LockedAt.Equal(*before.LockedAt) {
		t.Fatal("callback changed processing lease")
	}
}

func TestXiaohongshuRefundSnapshotImmutableAndCannotRewind(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "immutable")
	var op model.XiaohongshuRefundOperation
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&op).Error; err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]interface{}{"external_after_sales_order_id": "another", "request_payload_ciphertext": "replacement", "tenant_id": fixture.tenantID + 9999} {
		if err := model.DB.Model(&op).Update(key, value).Error; err == nil {
			t.Fatalf("mutable refund snapshot: %s", key)
		}
	}
	if err := model.DB.Model(&op).Update("state", "querying").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&op).Update("state", "prepared").Error; err == nil {
		t.Fatal("query-only barrier rewound")
	}
}

func TestXiaohongshuTerminalFailedRefundCannotBeResubmitted(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	refund := createXiaohongshuRefund(t, fixture, "failed-original")
	_, server := newXiaohongshuRefundFake(t, 3, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, service, time.Now().Add(time.Minute))
	var task model.DigitalRefundTask
	if err := model.DB.Where("refund_id = ?", refund.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.RetryDigitalRefundTask(fixture.tenantID, task.ID, 1, "admin", "核对后重试"); err == nil {
		t.Fatal("terminal failed refund reactivated")
	}
	if _, err := service.CreateMixedRefundAs(RefundActor{TenantID: fixture.tenantID}, fixture.order.OrderNo, "failed-second", float64(fixture.refundAmount)/100, []string{fixture.ticket.TicketCode}, "again"); err == nil {
		t.Fatal("second add enabled after terminal failure")
	}
}
