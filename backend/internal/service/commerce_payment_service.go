package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"github.com/go-pay/gopay"
	wechatv3 "github.com/go-pay/gopay/wechat/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CommercePaymentRequest is the public payment boundary. The login code is
// exchanged server-side and is never stored or accepted as an identity.
type CommercePaymentRequest struct {
	OrderNo         string `json:"order_no"`
	ClientRequestID string `json:"client_request_id"`
	LoginCode       string `json:"login_code"`
}

// CommercePaymentView is safe for a storefront response. PrepayID, merchant
// credentials and payer identity are intentionally omitted.
type CommercePaymentView struct {
	OrderNo       string                 `json:"order_no"`
	Status        string                 `json:"status"`
	AttemptID     uint                   `json:"attempt_id,omitempty"`
	OutTradeNo    string                 `json:"out_trade_no,omitempty"`
	ProviderState string                 `json:"provider_state,omitempty"`
	LastError     string                 `json:"last_error,omitempty"`
	Payment       *wechatv3.AppletParams `json:"payment,omitempty"`
}

type CommerceRefundView struct {
	RequestID   uint   `json:"request_id"`
	OrderNo     string `json:"order_no"`
	Status      string `json:"status"`
	OutRefundNo string `json:"out_refund_no,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

type CommerceWechatCreateRequest struct {
	AppID       string
	MchID       string
	Description string
	NotifyURL   string
	OutTradeNo  string
	OpenID      string
	AmountCents int64
}

type CommerceWechatPaymentResult struct {
	PrepayID string
}

type CommerceWechatPaymentStatus struct {
	State         string
	TransactionID string
	AmountCents   int64
	PaidAt        *time.Time
}

type CommerceWechatRefundRequest struct {
	AppID       string
	MchID       string
	NotifyURL   string
	OutTradeNo  string
	OutRefundNo string
	Reason      string
	AmountCents int64
}

type CommerceWechatRefundResult struct {
	State          string
	ProviderID     string
	ProviderAmount int64
}

type CommerceWechatRefundStatus struct {
	State          string
	ProviderID     string
	ProviderAmount int64
}

// CommerceWechatProvider is injectable so state-machine tests never call a
// real merchant account. The production implementation below is the only
// code that knows the go-pay protocol details.
type CommerceWechatProvider interface {
	CreateJSAPI(context.Context, CommerceWechatCreateRequest) (CommerceWechatPaymentResult, error)
	QueryPayment(context.Context, string) (CommerceWechatPaymentStatus, error)
	CreateRefund(context.Context, CommerceWechatRefundRequest) (CommerceWechatRefundResult, error)
	QueryRefund(context.Context, string) (CommerceWechatRefundStatus, error)
}

type CommercePaymentService struct {
	DB         *gorm.DB
	Storefront *CommerceStorefrontService
	Orders     CommerceOrderService
	Provider   CommerceWechatProvider
	Clock      func() time.Time
}

func (s *CommercePaymentService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommercePaymentService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *CommercePaymentService) storefront() *CommerceStorefrontService {
	if s != nil && s.Storefront != nil {
		storefront := *s.Storefront
		if storefront.DB == nil {
			storefront.DB = s.db()
		}
		if storefront.Now == nil {
			storefront.Now = s.Clock
		}
		return &storefront
	}
	return &CommerceStorefrontService{DB: s.db(), Now: s.Clock}
}

func (s *CommercePaymentService) orderService() *CommerceOrderService {
	orders := s.Orders
	if orders.DB == nil {
		orders.DB = s.db()
	}
	if orders.Clock == nil {
		orders.Clock = s.Clock
	}
	return &orders
}

func commercePaymentNotifyURL(kind string, tenantID uint) (string, error) {
	return CommercePaymentNotifyURL(kind, tenantID)
}

func commercePaymentOutTradeNo(now time.Time) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", now.UnixNano(), now.Unix())))
	return "CP" + now.UTC().Format("060102150405") + strings.ToUpper(hex.EncodeToString(digest[:]))[:12]
}

func commerceRefundOutNo(now time.Time, requestID uint) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d", now.UnixNano(), now.Unix(), requestID)))
	return "CR" + now.UTC().Format("060102150405") + strings.ToUpper(hex.EncodeToString(digest[:]))[:12]
}

func commercePaymentFingerprint(order *model.CommerceOrder, account *model.ChannelAccount, appID, mchID, subjectHash string) string {
	payload := fmt.Sprintf("%d|%d|%s|%s|%d|%d|%s", order.TenantID, order.ID, order.OrderNo, appID, account.ID, order.TotalAmountCents, subjectHash)
	digest := sha256.Sum256([]byte(payload + "|" + mchID))
	return hex.EncodeToString(digest[:])
}

func (s *CommercePaymentService) loadWechatConfig(tenantID uint, account *model.ChannelAccount) (*model.PaymentConfig, error) {
	if tenantID == 0 || account == nil || account.ID == 0 || account.Type != "wechat_miniapp" || account.Status != "active" {
		return nil, ErrCommercePaymentUnavailable
	}
	cfg, err := (&PaymentService{}).GetConfig(tenantID, "wechat")
	if err != nil || cfg == nil || !cfg.Status || strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.MchID) == "" || cfg.AppID != account.AppID {
		return nil, ErrCommercePaymentUnavailable
	}
	return cfg, nil
}

func (s *CommercePaymentService) provider(cfg *model.PaymentConfig) (CommerceWechatProvider, error) {
	if s != nil && s.Provider != nil {
		return s.Provider, nil
	}
	if cfg == nil || strings.TrimSpace(cfg.MchID) == "" || strings.TrimSpace(cfg.SerialNo) == "" || strings.TrimSpace(cfg.Key) == "" || strings.TrimSpace(cfg.PrivateKey) == "" {
		return nil, ErrCommercePaymentUnavailable
	}
	return &commerceWechatProvider{cfg: cfg}, nil
}

func commercePaymentStatus(order *model.CommerceOrder, attempt *model.CommercePaymentAttempt) string {
	if order != nil {
		if order.PaymentStatus == "refunded" {
			return "refunded"
		}
		if order.PaymentStatus == "paid" {
			return "paid"
		}
	}
	if attempt == nil {
		if order != nil {
			return order.PaymentStatus
		}
		return "unpaid"
	}
	return attempt.Status
}

func (s *CommercePaymentService) viewFor(order *model.CommerceOrder, attempt *model.CommercePaymentAttempt, params *wechatv3.AppletParams) *CommercePaymentView {
	view := &CommercePaymentView{Status: commercePaymentStatus(order, attempt), Payment: params}
	if order != nil {
		view.OrderNo = order.OrderNo
	}
	if attempt != nil {
		view.AttemptID = attempt.ID
		view.OutTradeNo = attempt.OutTradeNo
		view.ProviderState = attempt.ProviderState
		view.LastError = attempt.LastError
	}
	return view
}

func (s *CommercePaymentService) exchangePaymentSubject(ctx context.Context, sf *CommerceStorefrontService, storeCtx *commerceStorefrontContext, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" || storeCtx == nil {
		return "", fmt.Errorf("%w: login code is required", ErrCommercePaymentInvalid)
	}
	secret := ""
	if storeCtx.Account.SecretCiphertext != "" {
		var err error
		secret, err = utils.DecryptAES(storeCtx.Account.SecretCiphertext)
		if err != nil || strings.TrimSpace(secret) == "" {
			return "", ErrCommercePaymentUnavailable
		}
	}
	identity, err := sf.loginAdapter().ExchangeCode(ctx, WechatMiniappLoginRequest{AppID: storeCtx.Account.AppID, AppSecret: secret, Code: code, Environment: storeCtx.Account.Environment})
	if err != nil {
		return "", fmt.Errorf("%w: provider login failed", ErrCommercePaymentUnavailable)
	}
	subject := strings.TrimSpace(identity.Subject)
	if subject == "" {
		subject = strings.TrimSpace(identity.OpenID)
	}
	if subject == "" || storefrontHash(subject) != storeCtx.Session.SubjectHash {
		return "", fmt.Errorf("%w: login subject does not match storefront session", ErrCommercePaymentInvalid)
	}
	openID := strings.TrimSpace(identity.OpenID)
	if openID == "" {
		openID = subject
	}
	return openID, nil
}

func (s *CommercePaymentService) CreateWechatPayment(ctx context.Context, token string, input CommercePaymentRequest) (*CommercePaymentView, error) {
	input.OrderNo = strings.TrimSpace(input.OrderNo)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.OrderNo == "" || input.ClientRequestID == "" || len([]rune(input.ClientRequestID)) > 100 {
		return nil, fmt.Errorf("%w: order and client request are required", ErrCommercePaymentInvalid)
	}
	sf := s.storefront()
	storeCtx, err := sf.authenticate(token)
	if err != nil {
		return nil, err
	}
	cfg, err := s.loadWechatConfig(storeCtx.Session.TenantID, &storeCtx.Account)
	if err != nil {
		return nil, err
	}
	provider, err := s.provider(cfg)
	if err != nil {
		return nil, err
	}
	var order model.CommerceOrder
	var attempt model.CommercePaymentAttempt
	var created bool
	err = s.db().Transaction(func(tx *gorm.DB) error {
		// Payment follows the immutable order scope. Publishing a business is a
		// gate for new carts/orders, not a reason to orphan an already-created
		// customer order when the binding is later disabled or moved.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_no = ? AND customer_id = ? AND channel = ?", storeCtx.Session.TenantID, input.OrderNo, storeCtx.CustomerID, "wechat_miniapp").First(&order).Error; err != nil {
			return err
		}
		fingerprint := commercePaymentFingerprint(&order, &storeCtx.Account, cfg.AppID, cfg.MchID, storeCtx.Session.SubjectHash)
		lookup := tx.Where("tenant_id = ? AND client_request_id = ?", order.TenantID, input.ClientRequestID).First(&attempt).Error
		if lookup == nil {
			if attempt.OrderID != order.ID || attempt.RequestFingerprint != fingerprint {
				return ErrCommerceIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if order.PaymentStatus == "paid" || order.PaymentStatus == "refunded" {
			return nil
		}
		if order.ExpiresAt != nil && !order.ExpiresAt.After(s.now()) {
			return fmt.Errorf("%w: payment window has expired", ErrCommerceOrderState)
		}
		outTradeNo := commercePaymentOutTradeNo(s.now())
		if order.PaymentStatus == "unpaid" {
			if err := tx.Model(&order).Updates(map[string]interface{}{"payment_status": "pending", "payment_reference": outTradeNo}).Error; err != nil {
				return err
			}
			order.PaymentStatus = "pending"
			order.PaymentReference = outTradeNo
		} else if order.PaymentStatus == "pending" && order.PaymentReference != "" {
			outTradeNo = order.PaymentReference
		}
		if err := ensureCommercePaymentTaskTx(tx, &order, s.now()); err != nil {
			return err
		}
		attempt = model.CommercePaymentAttempt{TenantID: order.TenantID, OrderID: order.ID, ChannelAccountID: storeCtx.Account.ID, Provider: "wechat", ClientRequestID: input.ClientRequestID, RequestFingerprint: fingerprint, OutTradeNo: outTradeNo, AppID: cfg.AppID, MchID: cfg.MchID, AmountCents: order.TotalAmountCents, Currency: "CNY", PayerSubjectHash: storeCtx.Session.SubjectHash, Status: "pending"}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if order.PaymentStatus == "paid" || attempt.Status == "paid" {
		return s.viewFor(&order, &attempt, nil), nil
	}
	if !created && attempt.PrepayID != "" {
		params, signErr := s.appletParams(cfg, attempt.PrepayID)
		if signErr != nil {
			return nil, signErr
		}
		return s.viewFor(&order, &attempt, params), nil
	}
	// wx.login codes are one-time credentials. Resolve the provider subject only
	// when a new JSAPI request is actually needed; an idempotent retry that can
	// reuse the persisted prepay_id must not fail merely because the client
	// replayed an already-consumed login code.
	openID, err := s.exchangePaymentSubject(ctx, sf, storeCtx, input.LoginCode)
	if err != nil {
		return nil, err
	}
	description := "商业订单"
	if len(order.Items) > 0 && strings.TrimSpace(order.Items[0].ProductNameSnapshot) != "" {
		description = order.Items[0].ProductNameSnapshot
	}
	notifyURL, err := commercePaymentNotifyURL("payments", order.TenantID)
	if err != nil {
		return nil, err
	}
	createdPayment, providerErr := provider.CreateJSAPI(ctx, CommerceWechatCreateRequest{AppID: cfg.AppID, MchID: cfg.MchID, Description: description, NotifyURL: notifyURL, OutTradeNo: attempt.OutTradeNo, OpenID: openID, AmountCents: attempt.AmountCents})
	if providerErr != nil {
		status := "unknown"
		if !providerRequestMayHaveBeenAccepted("wechat", providerErr) {
			status = "failed"
		}
		next := s.now().Add(5 * time.Second)
		updates := map[string]interface{}{"status": status, "last_error": providerErr.Error(), "provider_state": "create_error", "next_query_at": next}
		if status == "failed" {
			updates["next_query_at"] = nil
			updates["completed_at"] = s.now()
		}
		_ = s.db().Model(&attempt).Updates(updates).Error
		if status == "failed" {
			_, _ = s.orderService().ApplyPaymentOutcome(order.TenantID, order.ID, CommercePaymentOutcome{Status: "failed", ProviderState: "create_failed"})
		}
		return nil, providerErr
	}
	if strings.TrimSpace(createdPayment.PrepayID) == "" {
		return nil, fmt.Errorf("%w: provider did not return prepay id", ErrCommercePaymentUnavailable)
	}
	next := s.now().Add(30 * time.Second)
	if err := s.db().Model(&attempt).Updates(map[string]interface{}{"prepay_id": createdPayment.PrepayID, "provider_state": "created", "next_query_at": next, "last_error": ""}).Error; err != nil {
		return nil, err
	}
	params, err := s.appletParams(cfg, createdPayment.PrepayID)
	if err != nil {
		return nil, err
	}
	attempt.PrepayID = createdPayment.PrepayID
	attempt.ProviderState = "created"
	return s.viewFor(&order, &attempt, params), nil
}

// appletParams is kept server-side so only the short-lived signed invocation
// parameters leave the process.
func (s *CommercePaymentService) appletParams(cfg *model.PaymentConfig, prepayID string) (*wechatv3.AppletParams, error) {
	client, err := wechatv3.NewClientV3(cfg.MchID, cfg.SerialNo, cfg.Key, cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	return client.PaySignOfApplet(cfg.AppID, prepayID)
}

func (s *CommercePaymentService) GetPaymentStatus(ctx context.Context, token, orderNo string) (*CommercePaymentView, error) {
	storeCtx, err := s.storefront().authenticate(token)
	if err != nil {
		return nil, err
	}
	var order model.CommerceOrder
	if err := s.db().Where("tenant_id = ? AND order_no = ? AND customer_id = ? AND channel = ?", storeCtx.Session.TenantID, strings.TrimSpace(orderNo), storeCtx.CustomerID, "wechat_miniapp").First(&order).Error; err != nil {
		return nil, err
	}
	var attempt model.CommercePaymentAttempt
	lookup := s.db().Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).Order("id DESC").First(&attempt).Error
	if errors.Is(lookup, gorm.ErrRecordNotFound) {
		return s.viewFor(&order, nil, nil), nil
	}
	if lookup != nil {
		return nil, lookup
	}
	if attempt.Status == "pending" || attempt.Status == "unknown" {
		if err := s.queryPaymentAttempt(ctx, &order, &attempt); err != nil {
			attempt.LastError = err.Error()
		}
	}
	return s.viewFor(&order, &attempt, nil), nil
}

func (s *CommercePaymentService) queryPaymentAttempt(ctx context.Context, order *model.CommerceOrder, attempt *model.CommercePaymentAttempt) error {
	cfg, err := (&PaymentService{}).GetConfig(order.TenantID, "wechat")
	if err != nil {
		return s.rescheduleCommercePaymentAttempt(attempt, ErrCommercePaymentUnavailable, false)
	}
	provider, err := s.provider(cfg)
	if err != nil {
		return s.rescheduleCommercePaymentAttempt(attempt, err, false)
	}
	status, err := provider.QueryPayment(ctx, attempt.OutTradeNo)
	if err != nil {
		return s.rescheduleCommercePaymentAttempt(attempt, err, false)
	}
	if status.AmountCents != 0 && status.AmountCents != order.TotalAmountCents {
		err := fmt.Errorf("%w: provider amount mismatch", ErrCommercePaymentInvalid)
		return s.rescheduleCommercePaymentAttempt(attempt, err, true)
	}
	normalized := strings.ToLower(strings.TrimSpace(status.State))
	switch normalized {
	case "success", "paid", "succeeded", "completed":
		if strings.TrimSpace(status.TransactionID) == "" {
			return s.rescheduleCommercePaymentAttempt(attempt, fmt.Errorf("provider payment success has no transaction reference"), false)
		}
		_, applyErr := s.orderService().ApplyPaymentOutcome(order.TenantID, order.ID, CommercePaymentOutcome{Status: "paid", ProviderReference: status.TransactionID, ProviderPaidAt: status.PaidAt, ProviderState: normalized, ProviderAmountCents: order.TotalAmountCents})
		if applyErr != nil && !errors.Is(applyErr, ErrCommercePaymentManualReview) {
			return applyErr
		}
		attemptStatus := "paid"
		lastError := ""
		if errors.Is(applyErr, ErrCommercePaymentManualReview) {
			// The provider payment is real, but the order window or local
			// reservation state requires an operator decision. Do not mark the
			// attempt paid and make the storefront look settled when the order
			// itself is still pending.
			attemptStatus = "manual_review"
			lastError = "provider payment requires manual review"
		}
		updates := map[string]interface{}{"status": attemptStatus, "provider_reference": status.TransactionID, "provider_state": normalized, "last_error": lastError, "last_queried_at": s.now(), "next_query_at": nil, "completed_at": s.now()}
		return s.db().Model(attempt).Updates(updates).Error
	case "closed", "failed", "payerror", "revoked":
		_, applyErr := s.orderService().ApplyPaymentOutcome(order.TenantID, order.ID, CommercePaymentOutcome{Status: "failed", ProviderState: normalized})
		if applyErr != nil {
			return applyErr
		}
		return s.db().Model(attempt).Updates(map[string]interface{}{"status": "failed", "provider_state": normalized, "last_error": "", "last_queried_at": s.now(), "next_query_at": nil, "completed_at": s.now()}).Error
	default:
		next := s.now().Add(15 * time.Second)
		return s.db().Model(attempt).Updates(map[string]interface{}{"status": "pending", "provider_state": normalized, "last_queried_at": s.now(), "next_query_at": next}).Error
	}
}

func (s *CommercePaymentService) rescheduleCommercePaymentAttempt(attempt *model.CommercePaymentAttempt, cause error, manualReview bool) error {
	if attempt == nil || attempt.ID == 0 {
		return cause
	}
	status := "unknown"
	updates := map[string]interface{}{"status": status, "last_error": "", "last_queried_at": s.now()}
	if cause != nil {
		updates["last_error"] = cause.Error()
	}
	if manualReview {
		status = "manual_review"
		updates["status"] = status
		updates["next_query_at"] = nil
	} else {
		next := s.now().Add(15 * time.Second)
		updates["next_query_at"] = next
	}
	if err := s.db().Model(attempt).Updates(updates).Error; err != nil {
		return err
	}
	return cause
}

func (s *CommercePaymentService) StartWechatRefund(ctx context.Context, tenantID, requestID uint) (*CommerceRefundView, error) {
	if tenantID == 0 || requestID == 0 {
		return nil, ErrCommerceRefundInvalid
	}
	cfg, err := (&PaymentService{}).GetConfig(tenantID, "wechat")
	if err != nil {
		return nil, ErrCommercePaymentUnavailable
	}
	provider, err := s.provider(cfg)
	if err != nil {
		return nil, err
	}
	// Validate the callback address before persisting a provider attempt. If
	// local deployment configuration is incomplete, no request has reached
	// WeChat and the durable refund request must remain retryable as requested;
	// creating a processing attempt here would make recovery query an upstream
	// refund that was never submitted.
	notifyURL, err := commercePaymentNotifyURL("refunds", tenantID)
	if err != nil {
		return nil, err
	}
	var request model.CommerceAfterSaleRequest
	var order model.CommerceOrder
	var attempt model.CommerceRefundAttempt
	var created bool
	err = s.db().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&request).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", request.OrderID, tenantID).First(&order).Error; err != nil {
			return err
		}
		lookup := tx.Where("tenant_id = ? AND request_id = ?", tenantID, requestID).First(&attempt).Error
		if lookup == nil {
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if request.Status != "requested" && request.Status != "processing" {
			return fmt.Errorf("%w: refund request is %s", ErrCommerceRefundInvalid, request.Status)
		}
		if order.PaymentStatus != "paid" || order.RefundStatus != "requested" && order.RefundStatus != "processing" {
			return fmt.Errorf("%w: order is not refundable in its current state", ErrCommerceRefundInvalid)
		}
		if err := requireCommerceRefundableFulfillmentTx(tx, &order); err != nil {
			return err
		}
		outRefundNo := commerceRefundOutNo(s.now(), requestID)
		attempt = model.CommerceRefundAttempt{TenantID: tenantID, RequestID: requestID, OrderID: order.ID, Provider: "wechat", OutRefundNo: outRefundNo, AmountCents: request.AmountCents, Status: "processing"}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		if err := tx.Model(&request).Update("status", "processing").Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("refund_status", "processing").Error; err != nil {
			return err
		}
		request.Status = "processing"
		order.RefundStatus = "processing"
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	view := &CommerceRefundView{RequestID: request.ID, OrderNo: order.OrderNo, Status: attempt.Status, OutRefundNo: attempt.OutRefundNo, LastError: attempt.LastError}
	if !created {
		return view, nil
	}
	status, providerErr := s.submitWechatRefundAttempt(ctx, cfg, provider, &attempt, &request, &order, notifyURL)
	if providerErr != nil {
		return nil, providerErr
	}
	view.Status = status
	return view, nil
}

// submitWechatRefundAttempt always reuses the persisted out-refund number and
// immutable refund facts. WeChat requires the same merchant refund number for
// retrying a failed or interrupted submission, which makes a restart between
// the local commit and the HTTP response safe to recover without double pay.
func (s *CommercePaymentService) submitWechatRefundAttempt(ctx context.Context, cfg *model.PaymentConfig, provider CommerceWechatProvider, attempt *model.CommerceRefundAttempt, request *model.CommerceAfterSaleRequest, order *model.CommerceOrder, notifyURL string) (string, error) {
	if cfg == nil || provider == nil || attempt == nil || request == nil || order == nil {
		return "", ErrCommerceRefundInvalid
	}
	createdRefund, providerErr := provider.CreateRefund(ctx, CommerceWechatRefundRequest{AppID: cfg.AppID, MchID: cfg.MchID, NotifyURL: notifyURL, OutTradeNo: order.PaymentReference, OutRefundNo: attempt.OutRefundNo, Reason: request.Reason, AmountCents: request.AmountCents})
	if providerErr != nil {
		status := "unknown"
		if !providerRequestMayHaveBeenAccepted("wechat", providerErr) {
			status = "failed"
		}
		updates := map[string]interface{}{"status": status, "last_error": providerErr.Error(), "provider_state": "create_error", "next_query_at": s.now().Add(15 * time.Second)}
		if status == "failed" {
			if err := s.markCommerceRefundFailed(attempt, "create_error", providerErr.Error()); err != nil {
				return "", err
			}
			updates["next_query_at"] = nil
			updates["completed_at"] = s.now()
		}
		_ = s.db().Model(attempt).Updates(updates).Error
		return status, providerErr
	}
	updates := map[string]interface{}{"provider_state": createdRefund.State, "provider_refund_id": createdRefund.ProviderID, "next_query_at": s.now().Add(15 * time.Second), "last_error": ""}
	if err := s.db().Model(attempt).Updates(updates).Error; err != nil {
		return "", err
	}
	status := attempt.Status
	if strings.EqualFold(createdRefund.State, "success") || strings.EqualFold(createdRefund.State, "succeeded") {
		if _, err := s.orderService().CompleteRefundAfterProviderConfirmation(attempt.TenantID, request.ID, createdRefund.ProviderID, createdRefund.ProviderAmount); err != nil {
			return "", err
		}
		_ = s.db().Model(attempt).Updates(map[string]interface{}{"status": "succeeded", "completed_at": s.now(), "next_query_at": nil}).Error
		status = "succeeded"
	}
	return status, nil
}

func (s *CommercePaymentService) queryRefundAttempt(ctx context.Context, attempt *model.CommerceRefundAttempt, request *model.CommerceAfterSaleRequest) error {
	cfg, err := (&PaymentService{}).GetConfig(attempt.TenantID, "wechat")
	if err != nil {
		return s.rescheduleCommerceRefundAttempt(attempt, ErrCommercePaymentUnavailable, false)
	}
	provider, err := s.provider(cfg)
	if err != nil {
		return s.rescheduleCommerceRefundAttempt(attempt, err, false)
	}
	if strings.TrimSpace(attempt.ProviderState) == "" && attempt.LastQueriedAt == nil {
		var order model.CommerceOrder
		if err := s.db().Where("id = ? AND tenant_id = ?", attempt.OrderID, attempt.TenantID).First(&order).Error; err != nil {
			return s.rescheduleCommerceRefundAttempt(attempt, err, false)
		}
		notifyURL, err := commercePaymentNotifyURL("refunds", attempt.TenantID)
		if err != nil {
			return s.rescheduleCommerceRefundAttempt(attempt, err, false)
		}
		_, err = s.submitWechatRefundAttempt(ctx, cfg, provider, attempt, request, &order, notifyURL)
		return err
	}
	status, err := provider.QueryRefund(ctx, attempt.OutRefundNo)
	if err != nil {
		return s.rescheduleCommerceRefundAttempt(attempt, err, false)
	}
	if status.ProviderAmount != 0 && status.ProviderAmount != request.AmountCents {
		err := fmt.Errorf("%w: provider refund amount mismatch", ErrCommercePaymentInvalid)
		return s.rescheduleCommerceRefundAttempt(attempt, err, true)
	}
	if strings.EqualFold(status.State, "success") || strings.EqualFold(status.State, "succeeded") {
		if strings.TrimSpace(status.ProviderID) == "" || status.ProviderAmount <= 0 {
			return s.rescheduleCommerceRefundAttempt(attempt, fmt.Errorf("provider refund success has incomplete confirmation"), false)
		}
		if _, err := s.orderService().CompleteRefundAfterProviderConfirmation(attempt.TenantID, request.ID, status.ProviderID, status.ProviderAmount); err != nil {
			return err
		}
		return s.db().Model(attempt).Updates(map[string]interface{}{"status": "succeeded", "provider_refund_id": status.ProviderID, "provider_state": status.State, "last_queried_at": s.now(), "next_query_at": nil, "completed_at": s.now(), "last_error": ""}).Error
	}
	if strings.EqualFold(status.State, "closed") || strings.EqualFold(status.State, "abnormal") || strings.EqualFold(status.State, "failed") {
		return s.markCommerceRefundFailed(attempt, status.State, "provider refund is not successful")
	}
	next := s.now().Add(15 * time.Second)
	return s.db().Model(attempt).Updates(map[string]interface{}{"status": "processing", "provider_state": status.State, "last_queried_at": s.now(), "next_query_at": next}).Error
}

func (s *CommercePaymentService) rescheduleCommerceRefundAttempt(attempt *model.CommerceRefundAttempt, cause error, manualReview bool) error {
	if attempt == nil || attempt.ID == 0 {
		return cause
	}
	updates := map[string]interface{}{"status": "unknown", "last_error": "", "last_queried_at": s.now()}
	if cause != nil {
		updates["last_error"] = cause.Error()
	}
	if manualReview {
		updates["status"] = "manual_review"
		updates["next_query_at"] = nil
	} else {
		next := s.now().Add(15 * time.Second)
		updates["next_query_at"] = next
	}
	if err := s.db().Model(attempt).Updates(updates).Error; err != nil {
		return err
	}
	return cause
}

// ReconcileDue queries persisted commercial payment/refund attempts. It is
// safe to call from a restartable worker; each individual provider result is
// applied through the same idempotent service boundaries as callbacks.
func (s *CommercePaymentService) ReconcileDue(ctx context.Context, at time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	processed := 0
	var attempts []model.CommercePaymentAttempt
	if err := s.db().Where("status IN ? AND (next_query_at IS NULL OR next_query_at <= ?)", []string{"pending", "unknown"}, at).Order("id ASC").Limit(limit).Find(&attempts).Error; err != nil {
		return 0, err
	}
	for i := range attempts {
		var order model.CommerceOrder
		if err := s.db().Where("id = ? AND tenant_id = ?", attempts[i].OrderID, attempts[i].TenantID).First(&order).Error; err == nil {
			if err := s.queryPaymentAttempt(ctx, &order, &attempts[i]); err != nil {
				_ = s.db().Model(&attempts[i]).Update("last_error", err.Error()).Error
			}
			processed++
		}
	}
	remaining := limit - processed
	if remaining < 1 {
		return processed, nil
	}
	// A refund request is created before the provider call. If the HTTP
	// request failed before an attempt row could be persisted, recover it after
	// restart instead of leaving the customer-visible request in `requested`
	// forever. StartWechatRefund is idempotent and will still refuse a request
	// whose local order or fulfillment state is no longer refundable.
	var pendingRequests []model.CommerceAfterSaleRequest
	if err := s.db().Where("tenant_id > 0 AND status IN ? AND deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM commerce_refund_attempts a WHERE a.tenant_id = commerce_after_sale_requests.tenant_id AND a.request_id = commerce_after_sale_requests.id AND a.deleted_at IS NULL)", []string{"requested", "processing"}).Order("id ASC").Limit(remaining).Find(&pendingRequests).Error; err != nil {
		return processed, err
	}
	var firstRefundStartErr error
	for i := range pendingRequests {
		if _, err := s.StartWechatRefund(ctx, pendingRequests[i].TenantID, pendingRequests[i].ID); err != nil && firstRefundStartErr == nil {
			firstRefundStartErr = err
		}
		processed++
	}
	remaining = limit - processed
	if remaining < 1 {
		return processed, nil
	}
	var refunds []model.CommerceRefundAttempt
	if err := s.db().Where("status IN ? AND (next_query_at IS NULL OR next_query_at <= ?)", []string{"processing", "unknown"}, at).Order("id ASC").Limit(remaining).Find(&refunds).Error; err != nil {
		return processed, err
	}
	for i := range refunds {
		var request model.CommerceAfterSaleRequest
		if err := s.db().Where("id = ? AND tenant_id = ?", refunds[i].RequestID, refunds[i].TenantID).First(&request).Error; err == nil {
			if err := s.queryRefundAttempt(ctx, &refunds[i], &request); err != nil {
				_ = s.db().Model(&refunds[i]).Update("last_error", err.Error()).Error
			}
			processed++
		}
	}
	return processed, firstRefundStartErr
}

func (s *CommercePaymentService) HandleWechatNotify(ctx context.Context, tenantID uint, req *http.Request, refund bool) error {
	notify, err := wechatv3.V3ParseNotify(req)
	if err != nil {
		return err
	}
	cfg, err := (&PaymentService{}).GetConfig(tenantID, "wechat")
	if err != nil || cfg == nil {
		return ErrCommercePaymentUnavailable
	}
	if cfg.PlatformPublicKeyID == "" || notify.SignInfo == nil || notify.SignInfo.HeaderSerial != cfg.PlatformPublicKeyID {
		return fmt.Errorf("unknown WeChat platform key serial")
	}
	publicKey, err := parseRSAPublicKey(cfg.PlatformPublicKey)
	if err != nil {
		return err
	}
	if err := notify.VerifySignByPK(publicKey); err != nil {
		return err
	}
	payload := ""
	if notify.SignInfo != nil {
		payload = notify.SignInfo.SignBody
	}
	eventID := strings.TrimSpace(notify.Id)
	if eventID == "" {
		digest := sha256.Sum256([]byte(payload))
		eventID = hex.EncodeToString(digest[:])
	}
	event, alreadyProcessed, err := s.receiveCommerceProviderEvent(tenantID, eventID, notify.EventType, payload)
	if err != nil {
		return err
	}
	if alreadyProcessed {
		// WeChat retries a notification until it receives a successful response.
		// A processed event is already converged, so acknowledge it without
		// trying to insert or apply the same business fact again.
		return nil
	}

	processErr := func() error {
		if refund {
			if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(notify.EventType)), "REFUND.") {
				return fmt.Errorf("unexpected WeChat event type for refund callback: %s", notify.EventType)
			}
			result, err := notify.DecryptRefundCipherText(cfg.Key)
			if err != nil {
				return err
			}
			if result == nil || result.Mchid != cfg.MchID || result.OutRefundNo == "" {
				return fmt.Errorf("WeChat commerce refund callback identity mismatch")
			}
			if err := s.db().Model(&event).Updates(map[string]interface{}{"out_refund_no": result.OutRefundNo}).Error; err != nil {
				return err
			}
			if !strings.EqualFold(result.RefundStatus, "SUCCESS") {
				return s.markCommerceRefundProviderState(tenantID, result.OutRefundNo, result.RefundStatus)
			}
			if result.RefundId == "" || result.Amount == nil || result.Amount.Refund <= 0 {
				return fmt.Errorf("WeChat commerce refund callback has no amount")
			}
			var attempt model.CommerceRefundAttempt
			if err := s.db().Where("tenant_id = ? AND out_refund_no = ?", tenantID, result.OutRefundNo).First(&attempt).Error; err != nil {
				return err
			}
			if _, err := s.orderService().CompleteRefundAfterProviderConfirmation(tenantID, attempt.RequestID, result.RefundId, int64(result.Amount.Refund)); err != nil {
				return err
			}
			return s.db().Model(&attempt).Updates(map[string]interface{}{"status": "succeeded", "provider_refund_id": result.RefundId, "provider_state": result.RefundStatus, "last_error": "", "next_query_at": nil, "completed_at": s.now()}).Error
		}
		if strings.ToUpper(strings.TrimSpace(notify.EventType)) != "TRANSACTION.SUCCESS" {
			return fmt.Errorf("unexpected WeChat event type for payment callback: %s", notify.EventType)
		}
		result, err := notify.DecryptPayCipherText(cfg.Key)
		if err != nil {
			return err
		}
		if result == nil || result.Mchid != cfg.MchID || result.Appid != cfg.AppID || result.OutTradeNo == "" {
			return fmt.Errorf("WeChat commerce payment callback identity mismatch")
		}
		if err := s.db().Model(&event).Updates(map[string]interface{}{"out_trade_no": result.OutTradeNo}).Error; err != nil {
			return err
		}
		var attempt model.CommercePaymentAttempt
		if err := s.db().Where("tenant_id = ? AND out_trade_no = ? AND app_id = ? AND mch_id = ?", tenantID, result.OutTradeNo, result.Appid, result.Mchid).First(&attempt).Error; err != nil {
			return err
		}
		if result.Amount == nil || int64(result.Amount.Total) != attempt.AmountCents {
			return fmt.Errorf("%w: provider callback amount mismatch", ErrCommercePaymentInvalid)
		}
		paidAt := parseCommerceProviderTime(result.SuccessTime)
		state := strings.ToLower(strings.TrimSpace(result.TradeState))
		status := "unknown"
		if state == "success" {
			if strings.TrimSpace(result.TransactionId) == "" {
				return fmt.Errorf("WeChat commerce payment callback has no transaction reference")
			}
			status = "paid"
		} else if state == "closed" || state == "payerror" || state == "revoked" {
			status = "failed"
		}
		_, applyErr := s.orderService().ApplyPaymentOutcome(tenantID, attempt.OrderID, CommercePaymentOutcome{Status: status, ProviderReference: result.TransactionId, ProviderPaidAt: paidAt, ProviderState: result.TradeState, ProviderAmountCents: attempt.AmountCents})
		if applyErr != nil && !errors.Is(applyErr, ErrCommercePaymentManualReview) {
			return applyErr
		}
		attemptStatus := status
		lastError := ""
		if errors.Is(applyErr, ErrCommercePaymentManualReview) {
			attemptStatus = "manual_review"
			lastError = "provider payment requires manual review"
		}
		updates := map[string]interface{}{"status": attemptStatus, "provider_reference": result.TransactionId, "provider_state": result.TradeState, "last_error": lastError, "last_queried_at": s.now(), "next_query_at": nil}
		if attemptStatus == "paid" || attemptStatus == "failed" || attemptStatus == "manual_review" {
			updates["completed_at"] = s.now()
		}
		return s.db().Model(&attempt).Updates(updates).Error
	}()
	if processErr != nil {
		_ = s.failCommerceProviderEvent(event.ID, processErr)
		return processErr
	}
	return s.processCommerceProviderEvent(event.ID)
}

// receiveCommerceProviderEvent stores a verified provider notification before
// any business mutation. It is deliberately separate from the apply step so a
// failed decrypt, missing attempt, or temporary database error remains
// observable and can be retried by the provider or an operator.
func (s *CommercePaymentService) receiveCommerceProviderEvent(tenantID uint, eventID, eventType, payload string) (model.CommercePaymentProviderEvent, bool, error) {
	var event model.CommercePaymentProviderEvent
	alreadyProcessed := false
	err := s.db().Transaction(func(tx *gorm.DB) error {
		lookup := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND provider = ? AND event_id = ?", tenantID, "wechat", eventID).First(&event).Error
		if lookup == nil {
			if event.PayloadJSON != payload {
				return fmt.Errorf("WeChat provider event payload conflicts with the stored event")
			}
			alreadyProcessed = event.Status == "processed"
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		event = model.CommercePaymentProviderEvent{
			TenantID: tenantID, Provider: "wechat", EventID: eventID,
			EventType: eventType, PayloadJSON: payload, Status: "received",
		}
		return tx.Create(&event).Error
	})
	return event, alreadyProcessed, err
}

func (s *CommercePaymentService) processCommerceProviderEvent(eventID uint) error {
	if eventID == 0 {
		return fmt.Errorf("WeChat provider event identity is missing")
	}
	now := s.now()
	return s.db().Model(&model.CommercePaymentProviderEvent{}).Where("id = ?", eventID).Updates(map[string]interface{}{
		"status": "processed", "last_error": "", "processed_at": now,
	}).Error
}

func (s *CommercePaymentService) failCommerceProviderEvent(eventID uint, cause error) error {
	if eventID == 0 {
		return fmt.Errorf("WeChat provider event identity is missing")
	}
	message := "provider notification processing failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	return s.db().Model(&model.CommercePaymentProviderEvent{}).Where("id = ?", eventID).Updates(map[string]interface{}{
		"status": "failed", "last_error": message, "processed_at": nil,
	}).Error
}

func parseCommerceProviderTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339Nano, value)
	}
	if err != nil {
		return nil
	}
	return &parsed
}

func (s *CommercePaymentService) markCommerceRefundProviderState(tenantID uint, outRefundNo, state string) error {
	var attempt model.CommerceRefundAttempt
	if err := s.db().Where("tenant_id = ? AND out_refund_no = ?", tenantID, outRefundNo).First(&attempt).Error; err != nil {
		return err
	}
	if strings.EqualFold(state, "CLOSED") || strings.EqualFold(state, "ABNORMAL") || strings.EqualFold(state, "FAILED") {
		return s.markCommerceRefundFailed(&attempt, state, "provider refund is not successful")
	}
	return s.db().Model(&attempt).Updates(map[string]interface{}{"status": "processing", "provider_state": state, "last_queried_at": s.now(), "last_error": "provider refund is not successful"}).Error
}

// markCommerceRefundFailed converges a provider-confirmed terminal failure as
// one local state transition. The request and order must not remain in
// processing forever after the provider has closed or rejected the refund.
// It is idempotent and never regresses an already completed refund.
func (s *CommercePaymentService) markCommerceRefundFailed(attempt *model.CommerceRefundAttempt, providerState, reason string) error {
	if attempt == nil || attempt.ID == 0 {
		return ErrCommerceRefundInvalid
	}
	return s.db().Transaction(func(tx *gorm.DB) error {
		var current model.CommerceRefundAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", attempt.ID, attempt.TenantID).First(&current).Error; err != nil {
			return err
		}
		var request model.CommerceAfterSaleRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", current.RequestID, current.TenantID).First(&request).Error; err != nil {
			return err
		}
		var order model.CommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", current.OrderID, current.TenantID).First(&order).Error; err != nil {
			return err
		}
		if request.Status == "completed" || order.RefundStatus == "refunded" {
			return nil
		}
		now := s.now()
		message := strings.TrimSpace(reason)
		if message == "" {
			message = "provider refund is not successful"
		}
		if err := tx.Model(&current).Updates(map[string]interface{}{
			"status": "failed", "provider_state": providerState, "last_queried_at": now,
			"last_error": message, "next_query_at": nil, "completed_at": now,
		}).Error; err != nil {
			return err
		}
		if request.Status != "failed" {
			if err := tx.Model(&request).Updates(map[string]interface{}{"status": "failed", "processed_at": now}).Error; err != nil {
				return err
			}
		}
		if order.RefundStatus != "rejected" {
			if err := tx.Model(&order).Update("refund_status", "rejected").Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type commerceWechatProvider struct{ cfg *model.PaymentConfig }

func (p *commerceWechatProvider) client() (*wechatv3.ClientV3, error) {
	if p == nil || p.cfg == nil {
		return nil, ErrCommercePaymentUnavailable
	}
	return wechatv3.NewClientV3(p.cfg.MchID, p.cfg.SerialNo, p.cfg.Key, p.cfg.PrivateKey)
}

func (p *commerceWechatProvider) CreateJSAPI(ctx context.Context, request CommerceWechatCreateRequest) (CommerceWechatPaymentResult, error) {
	client, err := p.client()
	if err != nil {
		return CommerceWechatPaymentResult{}, err
	}
	body := make(gopay.BodyMap)
	body.Set("appid", request.AppID).Set("mchid", request.MchID).Set("description", request.Description).Set("out_trade_no", request.OutTradeNo).Set("notify_url", request.NotifyURL).SetBodyMap("amount", func(amount gopay.BodyMap) { amount.Set("total", request.AmountCents).Set("currency", "CNY") }).SetBodyMap("payer", func(payer gopay.BodyMap) { payer.Set("openid", request.OpenID) })
	response, err := client.V3TransactionJsapi(ctx, body)
	if err != nil {
		return CommerceWechatPaymentResult{}, err
	}
	if response == nil || response.Code != 200 || response.Response == nil || strings.TrimSpace(response.Response.PrepayId) == "" {
		if response == nil {
			return CommerceWechatPaymentResult{}, fmt.Errorf("微信 JSAPI 下单返回为空")
		}
		return CommerceWechatPaymentResult{}, fmt.Errorf("微信 JSAPI 下单失败: %s", response.Error)
	}
	return CommerceWechatPaymentResult{PrepayID: response.Response.PrepayId}, nil
}

func (p *commerceWechatProvider) QueryPayment(ctx context.Context, outTradeNo string) (CommerceWechatPaymentStatus, error) {
	client, err := p.client()
	if err != nil {
		return CommerceWechatPaymentStatus{}, err
	}
	response, err := client.V3TransactionQueryOrder(ctx, wechatv3.OutTradeNo, outTradeNo)
	if err != nil {
		return CommerceWechatPaymentStatus{}, err
	}
	if response == nil || response.Code != 200 || response.Response == nil {
		return CommerceWechatPaymentStatus{}, fmt.Errorf("微信支付查单失败")
	}
	status := CommerceWechatPaymentStatus{State: response.Response.TradeState, TransactionID: response.Response.TransactionId}
	if response.Response.Amount != nil {
		status.AmountCents = int64(response.Response.Amount.Total)
	}
	status.PaidAt = parseCommerceProviderTime(response.Response.SuccessTime)
	return status, nil
}

func (p *commerceWechatProvider) CreateRefund(ctx context.Context, request CommerceWechatRefundRequest) (CommerceWechatRefundResult, error) {
	client, err := p.client()
	if err != nil {
		return CommerceWechatRefundResult{}, err
	}
	body := make(gopay.BodyMap)
	body.Set("out_trade_no", request.OutTradeNo).Set("out_refund_no", request.OutRefundNo).Set("reason", request.Reason).Set("notify_url", request.NotifyURL).SetBodyMap("amount", func(amount gopay.BodyMap) {
		amount.Set("refund", request.AmountCents).Set("total", request.AmountCents).Set("currency", "CNY")
	})
	response, err := client.V3Refund(ctx, body)
	if err != nil {
		return CommerceWechatRefundResult{}, err
	}
	if response == nil || response.Code != 200 || response.Response == nil {
		if response == nil {
			return CommerceWechatRefundResult{}, fmt.Errorf("微信退款申请返回为空")
		}
		return CommerceWechatRefundResult{}, fmt.Errorf("微信退款申请失败: %s", response.Error)
	}
	amount := int64(0)
	if response.Response.Amount != nil {
		amount = int64(response.Response.Amount.Refund)
	}
	return CommerceWechatRefundResult{State: response.Response.Status, ProviderID: response.Response.RefundId, ProviderAmount: amount}, nil
}

func (p *commerceWechatProvider) QueryRefund(ctx context.Context, outRefundNo string) (CommerceWechatRefundStatus, error) {
	client, err := p.client()
	if err != nil {
		return CommerceWechatRefundStatus{}, err
	}
	response, err := client.V3RefundQuery(ctx, outRefundNo, nil)
	if err != nil {
		return CommerceWechatRefundStatus{}, err
	}
	if response == nil || response.Code != 200 || response.Response == nil {
		return CommerceWechatRefundStatus{}, fmt.Errorf("微信退款查单失败")
	}
	amount := int64(0)
	if response.Response.Amount != nil {
		amount = int64(response.Response.Amount.Refund)
	}
	return CommerceWechatRefundStatus{State: response.Response.Status, ProviderID: response.Response.RefundId, ProviderAmount: amount}, nil
}

var _ CommerceWechatProvider = (*commerceWechatProvider)(nil)
