package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
)

type commercePaymentCreateProvider struct {
	commercePaymentTestProvider
	creates      atomic.Int32
	createErr    error
	beforeCreate func(int32)
	refundResult *CommerceWechatRefundResult
}

func (p *commercePaymentCreateProvider) CreateJSAPI(context.Context, CommerceWechatCreateRequest) (CommerceWechatPaymentResult, error) {
	count := p.creates.Add(1)
	if p.beforeCreate != nil {
		p.beforeCreate(count)
	}
	return CommerceWechatPaymentResult{PrepayID: "audit-prepay"}, p.createErr
}

func (p *commercePaymentCreateProvider) CreateRefund(ctx context.Context, request CommerceWechatRefundRequest) (CommerceWechatRefundResult, error) {
	if p.refundResult == nil {
		return p.commercePaymentTestProvider.CreateRefund(ctx, request)
	}
	p.createRefundCalls++
	p.lastRefundRequest = request
	return *p.refundResult, nil
}

func newCommercePaymentCreateFixture(t *testing.T) (commerceStorefrontServiceFixture, *CommerceStorefrontLoginResult, *model.CommerceOrder, *CommercePaymentService, *commercePaymentCreateProvider) {
	t.Helper()
	f := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, f.service, f.account.AppID, "audit-subject")
	if _, err := f.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{ProductID: f.domain.product.ID, SKUID: f.domain.sku.ID, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	order, err := f.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{IdempotencyKey: "audit-payment", ContactName: "Guest", ContactPhone: "13800138000", FulfillmentMethod: "pickup", QuoteToken: storefrontPickupQuote(t, f, login.Token).RawToken})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private, err := utils.EncryptAES(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})))
	if err != nil {
		t.Fatal(err)
	}
	apiKey, err := utils.EncryptAES("12345678901234567890123456789012")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.PaymentConfig{TenantID: f.tenantID, Provider: "wechat", AppID: f.account.AppID, MchID: "audit-merchant", SerialNo: "audit-serial", Key: apiKey, PrivateKey: private, Status: true}).Error; err != nil {
		t.Fatal(err)
	}
	previous := config.GlobalConfig.Server.PublicBaseURL
	config.GlobalConfig.Server.PublicBaseURL = "https://merchant.example.test"
	t.Cleanup(func() { config.GlobalConfig.Server.PublicBaseURL = previous })
	provider := &commercePaymentCreateProvider{}
	payment := &CommercePaymentService{DB: model.DB, Storefront: f.service, Provider: provider, Clock: f.service.Now}
	return f, login, order, payment, provider
}

func TestCommercePaymentTerminalRequestCannotBeResubmitted(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	input := CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "audit-retry", LoginCode: "audit-subject"}
	provider.createErr = permanentProviderError{message: "PARAM_ERROR"}
	if _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); err == nil {
		t.Fatal("expected rejection")
	}
	var failed model.CommerceOrder
	if err := model.DB.First(&failed, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if failed.PaymentStatus != "failed" || failed.FulfillmentStatus != "cancelled" {
		t.Fatalf("unexpected order: %+v", failed)
	}
	provider.createErr = nil
	view, err := payment.CreateWechatPayment(context.Background(), login.Token, input)
	if !errors.Is(err, ErrCommerceOrderState) || view != nil && view.Payment != nil {
		t.Fatalf("terminal order retry returned payable data: view=%+v err=%v", view, err)
	}
	var item model.CommerceOrderItem
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", f.tenantID, order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.ReservationStatus != "released" || provider.creates.Load() != 1 {
		t.Fatalf("terminal retry changed inventory or submitted payment: item=%s calls=%d", item.ReservationStatus, provider.creates.Load())
	}
}

func TestCommercePaymentLocalLoginFailureDoesNotReserveProviderPayment(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	f.service.LoginAdapter = WechatMiniappLoginFunc(func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
		return WechatMiniappLoginIdentity{}, errors.New("expired wx.login code")
	})
	_, err := payment.CreateWechatPayment(context.Background(), login.Token, CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "audit-login-failure", LoginCode: "expired"})
	if err == nil {
		t.Fatal("expected local login rejection")
	}
	if provider.creates.Load() != 0 {
		t.Fatal("unexpected provider submission")
	}
	now := f.service.Now()
	f.setNow(now.Add(30 * time.Minute))
	released, err := payment.orderService().ExpireUnpaidOrders(f.tenantID, f.service.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, cancelErr := payment.orderService().CancelUnpaidOrder(f.tenantID, order.ID)
	var persisted model.CommerceOrder
	if err := model.DB.First(&persisted, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PaymentStatus != "failed" || released != 1 || cancelErr != nil {
		t.Fatalf("local failure prevented release: status=%s released=%d cancel=%v", persisted.PaymentStatus, released, cancelErr)
	}
}

func TestCommerceRefundInterruptedSubmissionRecoversAfterLocalConfigFailure(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	orders := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	if _, err := orders.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	request, err := orders.CreateRefundRequest(tenantID, order.ID, "audit-refund", "customer request")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(request.Request).Update("status", "processing").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ?", order.ID).Update("refund_status", "processing").Error; err != nil {
		t.Fatal(err)
	}
	attempt := model.CommerceRefundAttempt{TenantID: tenantID, RequestID: request.Request.ID, OrderID: order.ID, Provider: "wechat", OutRefundNo: "audit-interrupted-refund", AmountCents: order.TotalAmountCents, Status: "processing"}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	previous := config.GlobalConfig.Server.PublicBaseURL
	config.GlobalConfig.Server.PublicBaseURL = ""
	t.Cleanup(func() { config.GlobalConfig.Server.PublicBaseURL = previous })
	provider := &commercePaymentTestProvider{refundErr: errors.New("RESOURCE_NOT_EXISTS")}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if _, err := payment.ReconcileDue(context.Background(), now, 20); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig.Server.PublicBaseURL = "https://merchant.example.test"
	now = now.Add(time.Minute)
	if _, err := payment.ReconcileDue(context.Background(), now, 20); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&attempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if provider.createRefundCalls != 1 || provider.queryRefundCalls != 0 || provider.lastRefundRequest.OutRefundNo != attempt.OutRefundNo {
		t.Fatalf("refund recovery lost submission identity: creates=%d queries=%d request=%+v", provider.createRefundCalls, provider.queryRefundCalls, provider.lastRefundRequest)
	}
}

func TestCommercePaymentLocalCallbackFailureDoesNotCreateAttempt(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	config.GlobalConfig.Server.PublicBaseURL = ""
	if _, err := payment.CreateWechatPayment(context.Background(), login.Token, CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "callback-failure", LoginCode: "audit-subject"}); err == nil {
		t.Fatal("invalid callback was accepted")
	}
	var count int64
	if err := model.DB.Model(&model.CommercePaymentAttempt{}).Where("order_id = ?", order.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	current, err := payment.orderService().GetOrder(f.tenantID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PaymentStatus != "unpaid" || count != 0 || provider.creates.Load() != 0 {
		t.Fatalf("local callback failure changed payment state: order=%s attempts=%d creates=%d", current.PaymentStatus, count, provider.creates.Load())
	}
}

func TestCommercePaymentReuseChecksOrderAndAttemptState(t *testing.T) {
	for _, test := range []struct {
		name, paymentStatus, fulfillmentStatus, attemptStatus string
		expired, wantParams, wantError                        bool
	}{
		{name: "pending", paymentStatus: "pending", attemptStatus: "pending", wantParams: true},
		{name: "unknown", paymentStatus: "pending", attemptStatus: "unknown", wantParams: true},
		{name: "paid attempt", paymentStatus: "pending", attemptStatus: "paid"},
		{name: "paid", paymentStatus: "paid", attemptStatus: "paid"},
		{name: "refunded", paymentStatus: "refunded", attemptStatus: "paid"},
		{name: "failed order", paymentStatus: "failed", attemptStatus: "pending", wantError: true},
		{name: "cancelled fulfillment", paymentStatus: "pending", fulfillmentStatus: "cancelled", attemptStatus: "pending", wantError: true},
		{name: "expired order", paymentStatus: "pending", attemptStatus: "pending", expired: true, wantError: true},
		{name: "failed attempt", paymentStatus: "pending", attemptStatus: "failed", wantError: true},
		{name: "manual review attempt", paymentStatus: "pending", attemptStatus: "manual_review", wantError: true},
		{name: "unpaid order with stale prepay", paymentStatus: "unpaid", attemptStatus: "pending", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
			input := CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "state-reuse", LoginCode: "audit-subject"}
			if _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); err != nil {
				t.Fatal(err)
			}
			updates := map[string]interface{}{"payment_status": test.paymentStatus}
			if test.fulfillmentStatus != "" {
				updates["fulfillment_status"] = test.fulfillmentStatus
			}
			if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ?", order.ID).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Model(&model.CommercePaymentAttempt{}).Where("order_id = ?", order.ID).Update("status", test.attemptStatus).Error; err != nil {
				t.Fatal(err)
			}
			if test.expired {
				f.setNow(f.service.Now().Add(30 * time.Minute))
			}
			f.service.LoginAdapter = WechatMiniappLoginFunc(func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
				t.Error("persisted payment reuse exchanged consumed login code")
				return WechatMiniappLoginIdentity{}, errors.New("consumed code")
			})
			input.LoginCode = "consumed"
			view, err := payment.CreateWechatPayment(context.Background(), login.Token, input)
			if (err != nil) != test.wantError {
				t.Fatalf("view=%+v err=%v", view, err)
			}
			if got := view != nil && view.Payment != nil; got != test.wantParams {
				t.Fatalf("payable parameters=%t want=%t view=%+v", got, test.wantParams, view)
			}
			if provider.creates.Load() != 1 {
				t.Fatalf("reuse resubmitted payment: %d", provider.creates.Load())
			}
		})
	}
}

func TestCommercePaymentPreflightRechecksExpiry(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	f.service.LoginAdapter = WechatMiniappLoginFunc(func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
		f.setNow(f.service.Now().Add(30 * time.Minute))
		return WechatMiniappLoginIdentity{Subject: "audit-subject"}, nil
	})
	_, err := payment.CreateWechatPayment(context.Background(), login.Token, CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "expire-during-preflight", LoginCode: "audit-subject"})
	if !errors.Is(err, ErrCommerceOrderState) || provider.creates.Load() != 0 {
		t.Fatalf("payment ignored expiry during preflight: err=%v calls=%d", err, provider.creates.Load())
	}
	current, err := payment.orderService().GetOrder(f.tenantID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PaymentStatus != "unpaid" {
		t.Fatalf("expired preflight created pending payment: %s", current.PaymentStatus)
	}
}

func TestCommercePaymentUnknownSubmissionRetryDoesNotReleaseOrResubmit(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	provider.createErr = context.DeadlineExceeded
	input := CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "unknown-submission", LoginCode: "audit-subject"}
	if _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); !errors.Is(err, ErrCommercePaymentManualReview) {
		t.Fatalf("ambiguous submission error=%v", err)
	}
	f.service.LoginAdapter = WechatMiniappLoginFunc(func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
		t.Error("unknown submission retry exchanged login code")
		return WechatMiniappLoginIdentity{}, errors.New("used login code")
	})
	view, err := payment.CreateWechatPayment(context.Background(), login.Token, input)
	if err != nil || view == nil || view.Payment != nil || provider.creates.Load() != 1 {
		t.Fatalf("unknown submission was replayed: view=%+v err=%v calls=%d", view, err, provider.creates.Load())
	}
	current, err := payment.orderService().GetOrder(f.tenantID, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PaymentStatus != "pending" || current.Items[0].ReservationStatus != "reserved" {
		t.Fatalf("unknown payment released reservation: %+v", current)
	}
}

func TestCommercePaymentChangedRequestKeyCannotCreateAnotherAttempt(t *testing.T) {
	_, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	input := CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "original-key", LoginCode: "audit-subject"}
	if _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); err != nil {
		t.Fatal(err)
	}
	input.ClientRequestID = "changed-key"
	if _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); !errors.Is(err, ErrCommerceIdempotencyConflict) {
		t.Fatalf("changed request key error=%v", err)
	}
	var count int64
	if err := model.DB.Model(&model.CommercePaymentAttempt{}).Where("order_id = ?", order.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || provider.creates.Load() != 1 {
		t.Fatalf("changed key created another provider sender: attempts=%d calls=%d", count, provider.creates.Load())
	}
}

func TestCommercePaymentPreflightRechecksCancelledOrder(t *testing.T) {
	f, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	var cancelErr error
	f.service.LoginAdapter = WechatMiniappLoginFunc(func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
		_, cancelErr = payment.orderService().CancelUnpaidOrder(f.tenantID, order.ID)
		return WechatMiniappLoginIdentity{Subject: "audit-subject"}, nil
	})
	_, err := payment.CreateWechatPayment(context.Background(), login.Token, CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "cancel-during-preflight", LoginCode: "audit-subject"})
	if cancelErr != nil || !errors.Is(err, ErrCommerceOrderState) || provider.creates.Load() != 0 {
		t.Fatalf("payment did not recheck cancelled order: cancel=%v create=%v calls=%d", cancelErr, err, provider.creates.Load())
	}
}

func TestCommercePaymentConcurrentSameKeySubmitsOnce(t *testing.T) {
	_, login, order, payment, provider := newCommercePaymentCreateFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	provider.beforeCreate = func(count int32) {
		if count == 1 {
			close(started)
			<-release
		}
	}
	input := CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "concurrent-payment", LoginCode: "audit-subject"}
	done := make(chan error, 1)
	go func() { _, err := payment.CreateWechatPayment(context.Background(), login.Token, input); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("payment did not reach provider")
	}
	view, err := payment.CreateWechatPayment(context.Background(), login.Token, input)
	close(release)
	firstErr := <-done
	if firstErr != nil || err != nil || view == nil || view.Payment != nil || provider.creates.Load() != 1 {
		t.Fatalf("concurrent retry duplicated submission: first=%v retry=%v view=%+v calls=%d", firstErr, err, view, provider.creates.Load())
	}
}

func TestCommercePaymentCreateRejectsOtherCustomer(t *testing.T) {
	f, _, order, payment, provider := newCommercePaymentCreateFixture(t)
	other := storefrontLogin(t, f.service, f.account.AppID, "other-customer")
	if _, err := payment.CreateWechatPayment(context.Background(), other.Token, CommercePaymentRequest{OrderNo: order.OrderNo, ClientRequestID: "foreign-customer", LoginCode: "other-customer"}); err == nil {
		t.Fatal("other customer payment was accepted")
	}
	if provider.creates.Load() != 0 {
		t.Fatal("other customer reached provider")
	}
}

func TestCommerceRefundUnknownSubmittedAttemptIsQueried(t *testing.T) {
	tenantID, order, now := createCommercePaymentAttemptFixture(t)
	orders := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
	if _, err := orders.ConfirmPayment(tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	request, err := orders.CreateRefundRequest(tenantID, order.ID, "unknown-refund", "customer request")
	if err != nil {
		t.Fatal(err)
	}
	attempt := model.CommerceRefundAttempt{TenantID: tenantID, RequestID: request.Request.ID, OrderID: order.ID, Provider: "wechat", OutRefundNo: "unknown-refund-provider", AmountCents: order.TotalAmountCents, Status: "unknown", ProviderState: "create_error"}
	if err := model.DB.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	provider := &commercePaymentTestProvider{refund: CommerceWechatRefundStatus{State: "PROCESSING"}}
	payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
	if _, err := payment.ReconcileDue(context.Background(), now, 20); err != nil {
		t.Fatal(err)
	}
	if provider.createRefundCalls != 0 || provider.queryRefundCalls != 1 {
		t.Fatalf("unknown submitted refund was blindly resubmitted: create=%d query=%d", provider.createRefundCalls, provider.queryRefundCalls)
	}
}

func TestCommerceRefundSubmissionEvidenceSurvivesEmptyProviderStates(t *testing.T) {
	for _, test := range []struct {
		name, state, providerID string
		emptySubmission         bool
	}{
		{name: "existing provider refund ID", providerID: "known-provider-refund"},
		{name: "unknown submission followed by empty query", state: "create_error"},
		{name: "empty successful submission response", emptySubmission: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tenantID, order, now := createCommercePaymentAttemptFixture(t)
			orders := &CommerceOrderService{DB: model.DB, Clock: func() time.Time { return now }}
			if _, err := orders.ConfirmPayment(tenantID, order.ID); err != nil {
				t.Fatal(err)
			}
			request, err := orders.CreateRefundRequest(tenantID, order.ID, "refund-empty-state", "customer request")
			if err != nil {
				t.Fatal(err)
			}
			attempt := model.CommerceRefundAttempt{TenantID: tenantID, RequestID: request.Request.ID, OrderID: order.ID, Provider: "wechat", OutRefundNo: "refund-empty-state-provider", AmountCents: order.TotalAmountCents, Status: "processing", ProviderState: test.state, ProviderRefundID: test.providerID}
			if err := model.DB.Create(&attempt).Error; err != nil {
				t.Fatal(err)
			}
			previous := config.GlobalConfig.Server.PublicBaseURL
			config.GlobalConfig.Server.PublicBaseURL = "https://merchant.example.test"
			t.Cleanup(func() { config.GlobalConfig.Server.PublicBaseURL = previous })
			provider := &commercePaymentCreateProvider{refundResult: &CommerceWechatRefundResult{}}
			payment := &CommercePaymentService{DB: model.DB, Provider: provider, Clock: func() time.Time { return now }}
			for i := 0; i < 3; i++ {
				if _, err := payment.ReconcileDue(context.Background(), now, 20); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Minute)
			}
			wantCreates := 0
			wantQueries := 3
			if test.emptySubmission {
				wantCreates = 1
				wantQueries = 2
			}
			if provider.createRefundCalls != wantCreates || provider.queryRefundCalls != wantQueries {
				t.Fatalf("empty state lost submission evidence: creates=%d queries=%d", provider.createRefundCalls, provider.queryRefundCalls)
			}
			if err := model.DB.First(&attempt, attempt.ID).Error; err != nil {
				t.Fatal(err)
			}
			if attempt.ProviderRefundID != test.providerID {
				t.Fatalf("provider refund ID changed: %q", attempt.ProviderRefundID)
			}
		})
	}
}
