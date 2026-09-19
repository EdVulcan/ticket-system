package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type commercePaymentTestProvider struct {
	queryPaymentCalls int
	queryRefundCalls  int
	payment           CommerceWechatPaymentStatus
	refund            CommerceWechatRefundStatus
	paymentErr        error
	refundErr         error
}

func (p *commercePaymentTestProvider) CreateJSAPI(context.Context, CommerceWechatCreateRequest) (CommerceWechatPaymentResult, error) {
	return CommerceWechatPaymentResult{PrepayID: "prepay-test"}, nil
}

func (p *commercePaymentTestProvider) QueryPayment(context.Context, string) (CommerceWechatPaymentStatus, error) {
	p.queryPaymentCalls++
	return p.payment, p.paymentErr
}

func (p *commercePaymentTestProvider) CreateRefund(context.Context, CommerceWechatRefundRequest) (CommerceWechatRefundResult, error) {
	return CommerceWechatRefundResult{State: "PROCESSING", ProviderID: "refund-test", ProviderAmount: 0}, nil
}

func (p *commercePaymentTestProvider) QueryRefund(context.Context, string) (CommerceWechatRefundStatus, error) {
	p.queryRefundCalls++
	return p.refund, p.refundErr
}

func createCommercePaymentAttemptFixture(t *testing.T) (uint, model.CommerceOrder, time.Time) {
	t.Helper()
	tenantID, productID, skuID, locationID, now := commerceOrderFixture(t)
	orderService := &CommerceOrderService{Clock: func() time.Time { return now }}
	input := commerceOrderInput(productID, skuID, locationID, "payment-attempt", now.Add(15*time.Minute))
	order, err := orderService.CreateOrder(tenantID, input)
	if err != nil {
		t.Fatalf("create payment order: %v", err)
	}
	if _, err := orderService.MarkPaymentPendingWithReference(tenantID, order.ID, "CP-TEST-ORDER"); err != nil {
		t.Fatalf("mark payment pending: %v", err)
	}
	if err := model.DB.Create(&model.PaymentConfig{TenantID: tenantID, Provider: "wechat", AppID: "wx-test", MchID: "mch-test", Status: true}).Error; err != nil {
		t.Fatalf("create payment config: %v", err)
	}
	return tenantID, *order, now
}

func TestCommerceProviderEventInboxIsIdempotent(t *testing.T) {
	resetBusinessData(t)
	tenantID := newCommerceTenant(t, "restaurant", "active")
	payment := &CommercePaymentService{DB: model.DB}

	first, processed, err := payment.receiveCommerceProviderEvent(tenantID, "evt-1", "TRANSACTION.SUCCESS", `{"id":"evt-1"}`)
	if err != nil || first.ID == 0 || processed {
		t.Fatalf("first event=%+v processed=%t err=%v", first, processed, err)
	}
	if err := payment.processCommerceProviderEvent(first.ID); err != nil {
		t.Fatalf("process event: %v", err)
	}
	second, processed, err := payment.receiveCommerceProviderEvent(tenantID, "evt-1", "TRANSACTION.SUCCESS", `{"id":"evt-1"}`)
	if err != nil || second.ID != first.ID || !processed {
		t.Fatalf("duplicate event=%+v processed=%t err=%v", second, processed, err)
	}
	var count int64
	if err := model.DB.Model(&model.CommercePaymentProviderEvent{}).Where("tenant_id = ? AND event_id = ?", tenantID, "evt-1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate notification created %d inbox rows", count)
	}
	if _, _, err := payment.receiveCommerceProviderEvent(tenantID, "evt-1", "TRANSACTION.SUCCESS", `{"id":"evt-1","changed":true}`); err == nil {
		t.Fatal("changed duplicate payload was accepted")
	}
}

func TestCommercePaymentReconcileAppliesPaidOnceAndSurvivesRetry(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	provider := &commercePaymentTestProvider{payment: CommerceWechatPaymentStatus{State: "SUCCESS", TransactionID: "WX-TX-1", AmountCents: order.TotalAmountCents, PaidAt: &now}}
	next := now.Add(-time.Second)
	attempt := model.CommercePaymentAttempt{
		TenantID: tenantID, OrderID: order.ID, ChannelAccountID: 1, Provider: "wechat",
		ClientRequestID: "client-payment-1", RequestFingerprint: "fingerprint-1", OutTradeNo: "CP-TEST-ORDER",
		AppID: "wx-test", MchID: "mch-test", AmountCents: order.TotalAmountCents, Currency: "CNY",
		PayerSubjectHash: "subject-hash", Status: "pending", NextQueryAt: &next,
	}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("create payment attempt: %v", err)
	}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	processed, err := payment.ReconcileDue(context.Background(), now, 20)
	if err != nil || processed != 1 {
		t.Fatalf("reconcile paid processed=%d err=%v", processed, err)
	}
	var updatedOrder model.CommerceOrder
	if err := model.DB.Where("id = ? AND tenant_id = ?", order.ID, tenantID).First(&updatedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if updatedOrder.PaymentStatus != "paid" {
		t.Fatalf("payment status=%q, want paid", updatedOrder.PaymentStatus)
	}
	var updatedAttempt model.CommercePaymentAttempt
	if err := model.DB.First(&updatedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedAttempt.Status != "paid" || updatedAttempt.ProviderReference != "WX-TX-1" {
		t.Fatalf("payment attempt=%+v", updatedAttempt)
	}

	processed, err = payment.ReconcileDue(context.Background(), now.Add(time.Minute), 20)
	if err != nil || processed != 0 || provider.queryPaymentCalls != 1 {
		t.Fatalf("paid retry processed=%d calls=%d err=%v", processed, provider.queryPaymentCalls, err)
	}
}

func TestCommercePaymentReconcileQuarantinesAmountMismatch(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	next := now.Add(-time.Second)
	attempt := model.CommercePaymentAttempt{
		TenantID: tenantID, OrderID: order.ID, ChannelAccountID: 1, Provider: "wechat",
		ClientRequestID: "client-payment-mismatch", RequestFingerprint: "fingerprint-mismatch", OutTradeNo: "CP-TEST-MISMATCH",
		AppID: "wx-test", MchID: "mch-test", AmountCents: order.TotalAmountCents, Currency: "CNY",
		PayerSubjectHash: "subject-hash", Status: "pending", NextQueryAt: &next,
	}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("create mismatch attempt: %v", err)
	}
	provider := &commercePaymentTestProvider{payment: CommerceWechatPaymentStatus{State: "SUCCESS", TransactionID: "WX-BAD", AmountCents: order.TotalAmountCents + 1}}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if processed, err := payment.ReconcileDue(context.Background(), now, 20); err != nil || processed != 1 {
		t.Fatalf("amount mismatch processed=%d err=%v", processed, err)
	}
	var updatedAttempt model.CommercePaymentAttempt
	if err := model.DB.First(&updatedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedAttempt.Status != "manual_review" || updatedAttempt.NextQueryAt != nil {
		t.Fatalf("mismatch attempt was not quarantined: %+v", updatedAttempt)
	}
	var updatedOrder model.CommerceOrder
	if err := model.DB.First(&updatedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedOrder.PaymentStatus != "pending" {
		t.Fatalf("amount mismatch changed order status to %q", updatedOrder.PaymentStatus)
	}
	if processed, err := payment.ReconcileDue(context.Background(), now.Add(time.Minute), 20); err != nil || processed != 0 || provider.queryPaymentCalls != 1 {
		t.Fatalf("manual review was queried again: processed=%d calls=%d err=%v", processed, provider.queryPaymentCalls, err)
	}
}

func TestCommercePaymentReconcileQuarantinesLateProviderPayment(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	expiredAt := now.Add(-time.Second)
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ? AND tenant_id = ?", order.ID, tenantID).Update("expires_at", expiredAt).Error; err != nil {
		t.Fatalf("expire payment order: %v", err)
	}
	next := now.Add(-time.Second)
	attempt := model.CommercePaymentAttempt{
		TenantID: tenantID, OrderID: order.ID, ChannelAccountID: 1, Provider: "wechat",
		ClientRequestID: "client-payment-late", RequestFingerprint: "fingerprint-late", OutTradeNo: "CP-TEST-LATE",
		AppID: "wx-test", MchID: "mch-test", AmountCents: order.TotalAmountCents, Currency: "CNY",
		PayerSubjectHash: "subject-hash", Status: "pending", NextQueryAt: &next,
	}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("create late payment attempt: %v", err)
	}
	paidAt := now
	provider := &commercePaymentTestProvider{payment: CommerceWechatPaymentStatus{State: "SUCCESS", TransactionID: "WX-LATE", AmountCents: order.TotalAmountCents, PaidAt: &paidAt}}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if processed, err := payment.ReconcileDue(context.Background(), now, 20); err != nil || processed != 1 {
		t.Fatalf("reconcile late payment processed=%d err=%v", processed, err)
	}
	var updatedAttempt model.CommercePaymentAttempt
	if err := model.DB.First(&updatedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedAttempt.Status != "manual_review" || updatedAttempt.NextQueryAt != nil {
		t.Fatalf("late payment was not quarantined: %+v", updatedAttempt)
	}
	var updatedOrder model.CommerceOrder
	if err := model.DB.First(&updatedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedOrder.PaymentStatus != "pending" {
		t.Fatalf("late payment changed order status to %q", updatedOrder.PaymentStatus)
	}
	if processed, err := payment.ReconcileDue(context.Background(), now.Add(time.Minute), 20); err != nil || processed != 0 || provider.queryPaymentCalls != 1 {
		t.Fatalf("manual-review late payment was queried again: processed=%d calls=%d err=%v", processed, provider.queryPaymentCalls, err)
	}
}

func TestCommerceRefundReconcileCompletesExactlyOnce(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	orderService := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	paid, err := orderService.ConfirmPayment(tenantID, order.ID)
	if err != nil {
		t.Fatalf("confirm order payment: %v", err)
	}
	request, err := orderService.CreateRefundRequest(tenantID, order.ID, "refund-attempt-1", "customer request")
	if err != nil {
		t.Fatalf("create refund request: %v", err)
	}
	next := now.Add(-time.Second)
	attempt := model.CommerceRefundAttempt{TenantID: tenantID, RequestID: request.Request.ID, OrderID: order.ID, Provider: "wechat", OutRefundNo: "CR-TEST-1", AmountCents: paid.TotalAmountCents, Status: "processing", NextQueryAt: &next}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("create refund attempt: %v", err)
	}
	provider := &commercePaymentTestProvider{refund: CommerceWechatRefundStatus{State: "SUCCESS", ProviderID: "WX-REFUND-1", ProviderAmount: paid.TotalAmountCents}}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if processed, err := payment.ReconcileDue(context.Background(), now, 20); err != nil || processed != 1 {
		t.Fatalf("reconcile refund processed=%d err=%v", processed, err)
	}
	var updatedRequest model.CommerceAfterSaleRequest
	if err := model.DB.First(&updatedRequest, request.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedRequest.Status != "completed" || updatedRequest.ProviderRefundReference != "WX-REFUND-1" {
		t.Fatalf("refund request=%+v", updatedRequest)
	}
	var updatedOrder model.CommerceOrder
	if err := model.DB.First(&updatedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedOrder.PaymentStatus != "refunded" || updatedOrder.RefundStatus != "refunded" {
		t.Fatalf("refund order=%+v", updatedOrder)
	}
	if processed, err := payment.ReconcileDue(context.Background(), now.Add(time.Minute), 20); err != nil || processed != 0 || provider.queryRefundCalls != 1 {
		t.Fatalf("refund retry processed=%d calls=%d err=%v", processed, provider.queryRefundCalls, err)
	}
}

func TestCommerceRefundReconcileConvergesProviderFailure(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	orderService := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	paid, err := orderService.ConfirmPayment(tenantID, order.ID)
	if err != nil {
		t.Fatalf("confirm order payment: %v", err)
	}
	request, err := orderService.CreateRefundRequest(tenantID, order.ID, "refund-failed-1", "customer request")
	if err != nil {
		t.Fatalf("create refund request: %v", err)
	}
	next := now.Add(-time.Second)
	attempt := model.CommerceRefundAttempt{TenantID: tenantID, RequestID: request.Request.ID, OrderID: order.ID, Provider: "wechat", OutRefundNo: "CR-TEST-FAILED", AmountCents: paid.TotalAmountCents, Status: "processing", NextQueryAt: &next}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatalf("create failed refund attempt: %v", err)
	}
	provider := &commercePaymentTestProvider{refund: CommerceWechatRefundStatus{State: "CLOSED", ProviderID: "WX-REFUND-CLOSED"}}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if processed, err := payment.ReconcileDue(context.Background(), now, 20); err != nil || processed != 1 {
		t.Fatalf("reconcile failed refund processed=%d err=%v", processed, err)
	}
	var updatedRequest model.CommerceAfterSaleRequest
	if err := model.DB.First(&updatedRequest, request.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedRequest.Status != "failed" {
		t.Fatalf("refund request status=%q, want failed", updatedRequest.Status)
	}
	var updatedOrder model.CommerceOrder
	if err := model.DB.First(&updatedOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedOrder.PaymentStatus != "paid" || updatedOrder.RefundStatus != "rejected" {
		t.Fatalf("provider failure changed order incorrectly: %+v", updatedOrder)
	}
	var updatedAttempt model.CommerceRefundAttempt
	if err := model.DB.First(&updatedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updatedAttempt.Status != "failed" || updatedAttempt.NextQueryAt != nil {
		t.Fatalf("failed refund attempt remains retryable: %+v", updatedAttempt)
	}
	if processed, err := payment.ReconcileDue(context.Background(), now.Add(time.Minute), 20); err != nil || processed != 0 || provider.queryRefundCalls != 1 {
		t.Fatalf("failed refund was queried again: processed=%d calls=%d err=%v", processed, provider.queryRefundCalls, err)
	}
}

func TestCommerceRefundRequestWithoutAttemptIsRecoveredByWorker(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	orderService := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	if _, err := orderService.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatalf("confirm order payment: %v", err)
	}
	request, err := orderService.CreateRefundRequest(tenantID, order.ID, "refund-recover-1", "customer request")
	if err != nil {
		t.Fatalf("create refund request: %v", err)
	}
	provider := &commercePaymentTestProvider{}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if err := model.DB.Where("tenant_id = ? AND provider = ?", tenantID, "wechat").Delete(&model.PaymentConfig{}).Error; err != nil {
		t.Fatalf("remove payment config: %v", err)
	}
	if processed, err := payment.ReconcileDue(context.Background(), now, 20); err == nil || processed != 1 {
		t.Fatalf("missing config should be reported after attempting recovery, processed=%d err=%v", processed, err)
	}
	var attempt model.CommerceRefundAttempt
	if err := model.DB.Where("tenant_id = ? AND request_id = ?", tenantID, request.Request.ID).First(&attempt).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unexpected refund attempt after unavailable provider: %+v err=%v", attempt, err)
	}
}
