package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"github.com/go-pay/gopay"
	"github.com/go-pay/gopay/alipay"
	"github.com/go-pay/gopay/wechat/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PaymentService struct {
	OrderService *OrderService
}

var ErrCashTenderInsufficient = errors.New("cash tender is less than the amount due")

type OrderPaymentProgress struct {
	OrderNo         string          `json:"order_no"`
	OrderTotalCents int64           `json:"order_total_cents"`
	PaidCents       int64           `json:"paid_cents"`
	PendingCents    int64           `json:"pending_cents"`
	RemainingCents  int64           `json:"remaining_cents"`
	HasPartialCash  bool            `json:"has_partial_cash"`
	Payments        []model.Payment `json:"payments"`
}

const paymentCentsSQL = "CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS INTEGER) END"

func sumPaymentCentsTx(tx *gorm.DB, tenantID uint, orderNo string, statuses []string) (int64, error) {
	var total int64
	err := tx.Model(&model.Payment{}).
		Where("tenant_id = ? AND order_no = ? AND status IN ?", tenantID, orderNo, statuses).
		Select("COALESCE(SUM(" + paymentCentsSQL + "), 0)").Scan(&total).Error
	return total, err
}

func (s *PaymentService) GetConfig(tenantID uint, provider string) (*model.PaymentConfig, error) {
	var paymentConfig model.PaymentConfig
	if err := model.DB.Where("tenant_id = ? AND provider = ? AND status = ?", tenantID, provider, true).First(&paymentConfig).Error; err != nil {
		return nil, err
	}
	if err := decryptPaymentConfig(&paymentConfig); err != nil {
		return nil, err
	}
	return &paymentConfig, nil
}

func decryptPaymentConfig(paymentConfig *model.PaymentConfig) error {
	var err error
	if paymentConfig.Key != "" {
		paymentConfig.Key, err = utils.DecryptAES(paymentConfig.Key)
		if err != nil {
			return fmt.Errorf("decrypt payment key: %w", err)
		}
	}
	if paymentConfig.PrivateKey != "" {
		paymentConfig.PrivateKey, err = utils.DecryptAES(paymentConfig.PrivateKey)
		if err != nil {
			return fmt.Errorf("decrypt private key: %w", err)
		}
	}
	if paymentConfig.PublicKey != "" {
		paymentConfig.PublicKey, err = utils.DecryptAES(paymentConfig.PublicKey)
		if err != nil {
			return fmt.Errorf("decrypt public key: %w", err)
		}
	}
	if paymentConfig.PlatformPublicKey != "" {
		paymentConfig.PlatformPublicKey, err = utils.DecryptAES(paymentConfig.PlatformPublicKey)
		if err != nil {
			return fmt.Errorf("decrypt payment platform public key: %w", err)
		}
	}
	return nil
}

func (s *PaymentService) CreatePayment(tenantID uint, req *model.Payment) error {
	if tenantID == 0 || strings.TrimSpace(req.OrderNo) == "" {
		return fmt.Errorf("order is required")
	}
	if req.Method == "auto" {
		if req.PayType != "bscanc" {
			return fmt.Errorf("automatic provider detection requires a payment auth code")
		}
		req.Method = getProviderType(strings.TrimSpace(req.AuthCode))
		if req.Method == "" {
			return fmt.Errorf("unrecognized payment auth code")
		}
	}
	if req.Method != "cash" && req.Method != "wechat" && req.Method != "alipay" {
		return fmt.Errorf("unsupported payment method")
	}
	if req.Method == "cash" {
		req.PayType = "cash"
	} else if req.PayType != "bscanc" && req.PayType != "cscanb" {
		return fmt.Errorf("unsupported payment type")
	}
	if req.PayType == "bscanc" && strings.TrimSpace(req.AuthCode) == "" {
		return fmt.Errorf("payment auth code is required")
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if len(req.IdempotencyKey) > 100 {
		return fmt.Errorf("payment idempotency key is too long")
	}

	var order model.Order
	replayed := false
	if err := model.Write(func(tx *gorm.DB) error {
		if req.IdempotencyKey != "" {
			var existing model.Payment
			err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, req.IdempotencyKey).First(&existing).Error
			if err == nil {
				if existing.OrderNo != req.OrderNo || existing.Method != req.Method ||
					(req.AmountCents > 0 && existing.AmountCents != req.AmountCents) ||
					(req.ShiftID > 0 && existing.ShiftID != req.ShiftID) ||
					(req.DeviceID > 0 && existing.DeviceID != req.DeviceID) {
					return errors.New("payment idempotency key was used with different data")
				}
				*req = existing
				replayed = true
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_no = ? AND tenant_id = ?", req.OrderNo, tenantID).First(&order).Error; err != nil {
			return fmt.Errorf("order not found")
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		if order.Channel == "window" {
			if req.ShiftID == 0 || req.DeviceID == 0 || req.OperatorID == 0 {
				return fmt.Errorf("an open POS shift, device and operator are required for window payment")
			}
			var shift model.POSShift
			if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", req.ShiftID, tenantID, req.DeviceID, req.OperatorID, "open").First(&shift).Error; err != nil {
				return fmt.Errorf("open POS shift not found")
			}
		}
		var activeAttempts int64
		if err := tx.Model(&model.Payment{}).
			Where("tenant_id = ? AND order_no = ? AND status = ?", tenantID, req.OrderNo, "pending").
			Count(&activeAttempts).Error; err != nil {
			return err
		}
		if activeAttempts > 0 {
			return fmt.Errorf("a provider payment is already pending for this order")
		}
		paidCents, err := sumPaymentCentsTx(tx, tenantID, req.OrderNo, []string{"paid", "partial_refunded"})
		if err != nil {
			return err
		}
		orderTotalCents := moneyCents(order.TotalAmount)
		remainingCents := orderTotalCents - paidCents
		if remainingCents <= 0 {
			return errors.New("order has no unpaid balance")
		}
		requestedCents := req.AmountCents
		if requestedCents <= 0 {
			requestedCents = remainingCents
		}
		if requestedCents <= 0 || requestedCents > remainingCents {
			return fmt.Errorf("payment amount exceeds the remaining balance")
		}
		if req.Method != "cash" && requestedCents != remainingCents {
			return errors.New("digital payment must settle the full remaining balance")
		}
		if requestedCents < remainingCents && order.Channel != "window" {
			return errors.New("partial cash payment is only supported at a POS window")
		}
		if requestedCents < remainingCents && req.IdempotencyKey == "" {
			return errors.New("partial cash payment requires an idempotency key")
		}
		req.Base = model.Base{}
		req.TenantID = tenantID
		req.PaymentNo = generatePaymentNo()
		req.AmountCents = requestedCents
		req.Amount = centsMoney(requestedCents)
		if req.Method == "cash" {
			if req.TenderedCents == 0 {
				req.TenderedCents = req.AmountCents
			}
			if req.TenderedCents < req.AmountCents {
				return ErrCashTenderInsufficient
			}
			req.ChangeCents = req.TenderedCents - req.AmountCents
		} else {
			req.TenderedCents = 0
			req.ChangeCents = 0
		}
		req.Status = "pending"
		req.TransactionID = ""
		req.CodeURL = ""
		req.ErrorMessage = ""
		return tx.Create(req).Error
	}); err != nil {
		return err
	}
	if replayed {
		if req.Method == "cash" && req.Status == "pending" {
			req.Status = "paid"
			req.TransactionID = "CASH_" + req.PaymentNo
			return s.completePayment(req)
		}
		return nil
	}

	var err error
	switch req.Method {
	case "cash":
		req.Status = "paid"
		req.TransactionID = "CASH_" + req.PaymentNo
	case "wechat":
		err = s.payWeChat(req)
	case "alipay":
		err = s.payAlipay(req)
	}
	if err != nil {
		// A transport timeout can happen after the provider accepted the
		// request. Keep the payment pending and let the durable reconciliation
		// worker query it instead of cancelling a potentially paid order.
		req.Status = "failed"
		mayHaveBeenAccepted := providerRequestMayHaveBeenAccepted(req.Method, err)
		if mayHaveBeenAccepted {
			req.Status = "pending"
		}
		req.ErrorMessage = err.Error()
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(req).Updates(map[string]interface{}{"status": req.Status, "error_message": req.ErrorMessage}).Error
		})
		if mayHaveBeenAccepted {
			if enqueueErr := s.enqueuePaymentTask(req); enqueueErr != nil {
				return fmt.Errorf("payment request unresolved and reconciliation could not be queued: %w", enqueueErr)
			}
		}
		return err
	}
	if req.Status == "paid" {
		return s.completePayment(req)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(req).Updates(map[string]interface{}{
			"status": req.Status, "transaction_id": req.TransactionID, "code_url": req.CodeURL,
		}).Error
	}); err != nil {
		return err
	}
	// The callback is the primary completion path, but a persisted task keeps
	// provider-side success recoverable when the process or client disappears.
	return s.enqueuePaymentTask(req)
}

func generatePaymentNo() string {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("PAY%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(random)))
}

func getProviderType(code string) string {
	if len(code) == 18 && code >= "100000000000000000" && code <= "159999999999999999" {
		return "wechat"
	}
	if len(code) >= 25 && len(code) <= 30 && code >= "2500000000000000000000000" && code < "310000000000000000000000000000" {
		return "alipay"
	}
	return ""
}

func providerRequestMayHaveBeenAccepted(method string, cause error) bool {
	if cause == nil || method == "cash" {
		return false
	}
	message := strings.ToLower(cause.Error())
	// These errors are produced before a provider request can be accepted.
	// All other provider failures are treated as ambiguous and reconciled.
	for _, permanent := range []string{
		"not configured", "not config", "invalid alipay auth code", "unsupported payment",
		"payment-code collection is not configured", "public key is required", "private key",
	} {
		if strings.Contains(message, permanent) {
			return false
		}
	}
	return true
}

func (s *PaymentService) payWeChat(req *model.Payment) error {
	if req.PayType == "bscanc" {
		return fmt.Errorf("WeChat payment-code collection is not configured; use Alipay or customer-scan mode")
	}
	cfg, err := s.GetConfig(req.TenantID, "wechat")
	if err != nil {
		return fmt.Errorf("WeChat payment is not configured")
	}
	client, err := wechat.NewClientV3(cfg.MchID, cfg.SerialNo, cfg.Key, cfg.PrivateKey)
	if err != nil {
		return err
	}
	bm := make(gopay.BodyMap)
	bm.Set("appid", cfg.AppID).
		Set("mchid", cfg.MchID).
		Set("description", "Ticket Sales").
		Set("out_trade_no", req.PaymentNo).
		Set("notify_url", cfg.NotifyURL).
		SetBodyMap("amount", func(amount gopay.BodyMap) {
			amount.Set("total", moneyCents(req.Amount)).Set("currency", "CNY")
		})
	res, err := client.V3TransactionNative(context.Background(), bm)
	if err != nil {
		return err
	}
	if res.Code != 200 || res.Response == nil {
		return fmt.Errorf("WeChat payment request failed: %s", res.Error)
	}
	req.CodeURL = res.Response.CodeUrl
	return nil
}

func (s *PaymentService) alipayClient(tenantID uint) (*alipay.Client, error) {
	cfg, err := s.GetConfig(tenantID, "alipay")
	if err != nil {
		return nil, fmt.Errorf("Alipay is not configured")
	}
	client, err := alipay.NewClient(cfg.AppID, cfg.PrivateKey, false)
	if err != nil {
		return nil, err
	}
	if cfg.PublicKey == "" {
		return nil, fmt.Errorf("Alipay public key is required")
	}
	client.AutoVerifySign([]byte(cfg.PublicKey))
	return client, nil
}

func (s *PaymentService) payAlipay(req *model.Payment) error {
	client, err := s.alipayClient(req.TenantID)
	if err != nil {
		return err
	}
	bm := make(gopay.BodyMap)
	bm.Set("subject", "Ticket Sales").
		Set("out_trade_no", req.PaymentNo).
		Set("total_amount", fmt.Sprintf("%.2f", req.Amount))

	if req.PayType == "bscanc" {
		if getProviderType(req.AuthCode) != "alipay" {
			return fmt.Errorf("invalid Alipay auth code")
		}
		bm.Set("scene", "bar_code").Set("auth_code", req.AuthCode)
		res, err := client.TradePay(context.Background(), bm)
		if err != nil {
			return err
		}
		if res.Response == nil {
			return fmt.Errorf("empty Alipay response")
		}
		switch res.Response.Code {
		case "10000":
			req.Status = "paid"
			req.TransactionID = res.Response.TradeNo
		case "10003":
			req.Status = "pending"
		default:
			return fmt.Errorf("Alipay error: %s", res.Response.SubMsg)
		}
		return nil
	}

	res, err := client.TradePrecreate(context.Background(), bm)
	if err != nil {
		return err
	}
	if res.Response == nil || res.Response.Code != "10000" {
		return fmt.Errorf("Alipay QR request failed")
	}
	req.CodeURL = res.Response.QrCode
	return nil
}

func (s *PaymentService) completePayment(payment *model.Payment) error {
	return model.Write(func(tx *gorm.DB) error {
		var stored model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ?", payment.ID, payment.TenantID).First(&stored).Error; err != nil {
			return err
		}
		if stored.Purpose == exchangeDifferencePurpose {
			stored.TransactionID = payment.TransactionID
			stored.CodeURL = payment.CodeURL
			return completeExchangeDifferencePaymentTx(tx, &stored)
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_no = ? AND tenant_id = ?", stored.OrderNo, stored.TenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "paid" && stored.Status == "paid" {
			return nil
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		if stored.Status != "paid" {
			if err := tx.Model(&stored).Updates(map[string]interface{}{
				"status": "paid", "transaction_id": payment.TransactionID, "code_url": payment.CodeURL, "paid_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		return settleOrderIfFullyPaidTx(tx, &order)
	})
}

func settleOrderIfFullyPaidTx(tx *gorm.DB, order *model.Order) error {
	paidCents, err := sumPaymentCentsTx(tx, order.TenantID, order.OrderNo, []string{"paid", "partial_refunded"})
	if err != nil {
		return err
	}
	totalCents := moneyCents(order.TotalAmount)
	if paidCents > totalCents {
		return errors.New("paid amount exceeds order total")
	}
	if paidCents < totalCents {
		return nil
	}
	if err := tx.Model(order).Update("status", "paid").Error; err != nil {
		return err
	}
	order.Status = "paid"
	return updateFulfillmentOrdersTx(tx, order.ID, "paid")
}

func (s *PaymentService) cancelOrderWithoutCollectedPayment(orderNo string, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		paidCents, err := sumPaymentCentsTx(tx, tenantID, orderNo, []string{"paid", "partial_refunded"})
		if err != nil || paidCents > 0 {
			return err
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		return cancelOrderTx(tx, &order)
	})
}

func (s *PaymentService) GetStatus(paymentID, tenantID uint) (*model.Payment, error) {
	var payment model.Payment
	if err := model.DB.Where("id = ? AND tenant_id = ?", paymentID, tenantID).First(&payment).Error; err != nil {
		return nil, err
	}
	if payment.Status != "pending" {
		return &payment, nil
	}
	if err := s.refreshProviderStatus(&payment); err != nil {
		return &payment, nil
	}
	if payment.Status == "paid" {
		if err := s.completePayment(&payment); err != nil {
			return nil, err
		}
	}
	if payment.Status == "failed" && s.OrderService != nil {
		_ = s.cancelOrderWithoutCollectedPayment(payment.OrderNo, tenantID)
	}
	return &payment, nil
}

// CompleteNotification is the single idempotent state transition used by
// provider callbacks and tests. It validates the stored amount and tenant
// scope before changing both payment and order in one write transaction.
func (s *PaymentService) CompleteNotification(tenantID uint, paymentNo, method, transactionID string, amount float64) error {
	return model.Write(func(tx *gorm.DB) error {
		var payment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("payment_no = ? AND tenant_id = ? AND method = ?", paymentNo, tenantID, method).First(&payment).Error; err != nil {
			return err
		}
		storedAmountCents := payment.AmountCents
		if storedAmountCents == 0 {
			storedAmountCents = moneyCents(payment.Amount)
		}
		if storedAmountCents != moneyCents(amount) {
			return fmt.Errorf("payment amount mismatch")
		}
		if payment.Status == "paid" {
			if transactionID != "" && payment.TransactionID != "" && payment.TransactionID != transactionID {
				return fmt.Errorf("payment transaction mismatch")
			}
			return nil
		}
		if payment.Status != "pending" {
			return fmt.Errorf("payment cannot be completed from status %s", payment.Status)
		}
		if payment.Purpose == exchangeDifferencePurpose {
			payment.TransactionID = transactionID
			return completeExchangeDifferencePaymentTx(tx, &payment)
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_no = ? AND tenant_id = ?", payment.OrderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		if err := tx.Model(&payment).Updates(map[string]interface{}{
			"status": "paid", "transaction_id": transactionID, "error_message": "", "paid_at": time.Now(), "amount_cents": storedAmountCents,
		}).Error; err != nil {
			return err
		}
		return settleOrderIfFullyPaidTx(tx, &order)
	})
}

// FailNotification records a provider-side failure and releases the unpaid
// order reservation. Repeated failure notifications are harmless.
func (s *PaymentService) FailNotification(tenantID uint, paymentNo, method, reason string) error {
	return model.Write(func(tx *gorm.DB) error {
		var payment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("payment_no = ? AND tenant_id = ? AND method = ?", paymentNo, tenantID, method).First(&payment).Error; err != nil {
			return err
		}
		if payment.Status == "failed" {
			return nil
		}
		if payment.Status == "paid" {
			return fmt.Errorf("paid payment cannot be failed")
		}
		if err := tx.Model(&payment).Updates(map[string]interface{}{"status": "failed", "error_message": reason}).Error; err != nil {
			return err
		}
		if payment.Purpose == exchangeDifferencePurpose {
			return failExchangeDifferencePaymentTx(tx, &payment, reason)
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", payment.OrderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "unpaid" {
			paidCents, err := sumPaymentCentsTx(tx, tenantID, order.OrderNo, []string{"paid", "partial_refunded"})
			if err != nil {
				return err
			}
			if paidCents == 0 {
				return cancelOrderTx(tx, &order)
			}
		}
		return nil
	})
}

func (s *PaymentService) GetOrderPaymentProgress(tenantID uint, orderNo string) (*OrderPaymentProgress, error) {
	if tenantID == 0 || strings.TrimSpace(orderNo) == "" {
		return nil, errors.New("tenant and order are required")
	}
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, orderNo).First(&order).Error; err != nil {
		return nil, err
	}
	var payments []model.Payment
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, orderNo).Order("created_at ASC, id ASC").Find(&payments).Error; err != nil {
		return nil, err
	}
	progress := &OrderPaymentProgress{OrderNo: orderNo, OrderTotalCents: moneyCents(order.TotalAmount), Payments: payments}
	for _, payment := range payments {
		amountCents := payment.AmountCents
		if amountCents == 0 {
			amountCents = moneyCents(payment.Amount)
		}
		switch payment.Status {
		case "paid", "partial_refunded":
			progress.PaidCents += amountCents
			if payment.Method == "cash" {
				progress.HasPartialCash = true
			}
		case "pending":
			progress.PendingCents += amountCents
		}
	}
	progress.RemainingCents = progress.OrderTotalCents - progress.PaidCents
	if progress.RemainingCents < 0 {
		progress.RemainingCents = 0
	}
	progress.HasPartialCash = progress.HasPartialCash && progress.RemainingCents > 0
	return progress, nil
}

// CancelPartialCashPayment explicitly records cash returned to the visitor
// before releasing an unpaid POS order. It never deletes the collected cash
// fact and refuses to act while a provider payment is unresolved.
func (s *PaymentService) CancelPartialCashPayment(tenantID uint, orderNo string, shiftID, deviceID, operatorID uint, role, reason string) error {
	reason = strings.TrimSpace(reason)
	if tenantID == 0 || strings.TrimSpace(orderNo) == "" || shiftID == 0 || deviceID == 0 || operatorID == 0 {
		return errors.New("tenant, order, shift, device and operator are required")
	}
	if reason == "" {
		return errors.New("cancellation reason is required")
	}
	if len(reason) > 255 {
		return errors.New("cancellation reason is too long")
	}
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").
			Where("tenant_id = ? AND order_no = ?", tenantID, orderNo).First(&order).Error; err != nil {
			return err
		}
		if order.Channel != "window" || order.Status != "unpaid" {
			return fmt.Errorf("order cannot cancel partial cash from status %s", order.Status)
		}
		var shift model.POSShift
		if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", shiftID, tenantID, deviceID, operatorID, "open").First(&shift).Error; err != nil {
			return errors.New("open POS shift not found")
		}
		var pending int64
		if err := tx.Model(&model.Payment{}).Where("tenant_id = ? AND order_no = ? AND status = ?", tenantID, orderNo, "pending").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return errors.New("provider payment is still pending; query or close it before returning cash")
		}
		var payments []model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND order_no = ? AND status IN ?", tenantID, orderNo, []string{"paid", "partial_refunded"}).Order("id ASC").Find(&payments).Error; err != nil {
			return err
		}
		if len(payments) == 0 {
			return errors.New("no collected cash was found")
		}
		returnedCents := int64(0)
		for i := range payments {
			payment := &payments[i]
			if payment.Method != "cash" || payment.ShiftID != shiftID || payment.DeviceID != deviceID || payment.OperatorID != operatorID {
				return errors.New("partial payment must be returned by its collecting cashier and shift")
			}
			amountCents := payment.AmountCents
			if amountCents == 0 {
				amountCents = moneyCents(payment.Amount)
			}
			refundedCents := payment.RefundedAmountCents
			if refundedCents == 0 {
				refundedCents = moneyCents(payment.RefundedAmount)
			}
			available := amountCents - refundedCents
			if available <= 0 {
				continue
			}
			refund := model.Refund{
				TenantID: tenantID, RefundNo: generateRefundNo(), IdempotencyKey: "partial-cancel:" + payment.PaymentNo,
				OrderNo: orderNo, PaymentID: payment.ID, Amount: centsMoney(available), AmountCents: available,
				Method: "cash", Status: "succeeded", Reason: reason,
			}
			if err := tx.Create(&refund).Error; err != nil {
				return err
			}
			if err := tx.Model(payment).Updates(map[string]interface{}{
				"refunded_amount": centsMoney(amountCents), "refunded_amount_cents": amountCents, "amount_cents": amountCents, "status": "refunded",
			}).Error; err != nil {
				return err
			}
			returnedCents += available
		}
		if returnedCents <= 0 {
			return errors.New("no refundable cash remains")
		}
		if err := cancelOrderTx(tx, &order); err != nil {
			return err
		}
		before, _ := json.Marshal(map[string]interface{}{"order_status": "unpaid", "collected_cash_cents": returnedCents})
		after, _ := json.Marshal(map[string]interface{}{"order_status": "cancelled", "returned_cash_cents": returnedCents})
		return recordAuditTx(tx, operatorID, tenantID, role, "tenant", "payment.partial_cash.cancel", "order", order.ID, reason, string(before), string(after))
	})
}

func parseRSAPublicKey(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("payment public key is not PEM encoded")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported RSA public key")
}

// HandleWeChatNotify verifies and decrypts a WeChat Pay V3 callback. The
// tenant is taken from the configured callback URL path, never from JSON.
func (s *PaymentService) HandleWeChatNotify(tenantID uint, req *http.Request) error {
	notify, err := wechat.V3ParseNotify(req)
	if err != nil {
		return err
	}
	cfg, err := s.GetConfig(tenantID, "wechat")
	if err != nil {
		return err
	}
	if cfg.PlatformPublicKeyID == "" || notify.SignInfo.HeaderSerial != cfg.PlatformPublicKeyID {
		return fmt.Errorf("unknown WeChat platform key serial")
	}
	publicKey, err := parseRSAPublicKey(cfg.PlatformPublicKey)
	if err != nil {
		return err
	}
	if err := notify.VerifySignByPK(publicKey); err != nil {
		return err
	}
	result, err := notify.DecryptPayCipherText(cfg.Key)
	if err != nil {
		return err
	}
	if result.TradeState == wechat.TradeStateSuccess {
		if result.Amount == nil {
			return fmt.Errorf("WeChat callback has no amount")
		}
		return s.CompleteNotification(tenantID, result.OutTradeNo, "wechat", result.TransactionId, float64(result.Amount.Total)/100)
	}
	if result.TradeState == wechat.TradeStateClosed || result.TradeState == wechat.TradeStatePayError || result.TradeState == wechat.TradeStateRevoked {
		return s.FailNotification(tenantID, result.OutTradeNo, "wechat", result.TradeStateDesc)
	}
	return nil
}

// HandleAlipayNotify verifies the signed form callback and applies the same
// idempotent payment transition as WeChat.
func (s *PaymentService) HandleAlipayNotify(tenantID uint, req *http.Request) error {
	values, err := alipay.ParseNotifyToBodyMap(req)
	if err != nil {
		return err
	}
	cfg, err := s.GetConfig(tenantID, "alipay")
	if err != nil {
		return err
	}
	if values.GetString("app_id") != "" && values.GetString("app_id") != cfg.AppID {
		return fmt.Errorf("Alipay app id mismatch")
	}
	ok, err := alipay.VerifySign(cfg.PublicKey, values)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("Alipay callback signature verification failed")
	}
	status := values.GetString("trade_status")
	paymentNo := values.GetString("out_trade_no")
	if status == "TRADE_SUCCESS" || status == "TRADE_FINISHED" {
		amount, err := strconv.ParseFloat(values.GetString("total_amount"), 64)
		if err != nil {
			return err
		}
		return s.CompleteNotification(tenantID, paymentNo, "alipay", values.GetString("trade_no"), amount)
	}
	if status == "TRADE_CLOSED" {
		return s.FailNotification(tenantID, paymentNo, "alipay", status)
	}
	return nil
}

func (s *PaymentService) refreshProviderStatus(payment *model.Payment) error {
	switch payment.Method {
	case "wechat":
		cfg, err := s.GetConfig(payment.TenantID, "wechat")
		if err != nil {
			return err
		}
		client, err := wechat.NewClientV3(cfg.MchID, cfg.SerialNo, cfg.Key, cfg.PrivateKey)
		if err != nil {
			return err
		}
		res, err := client.V3TransactionQueryOrder(context.Background(), wechat.OutTradeNo, payment.PaymentNo)
		if err != nil || res.Response == nil {
			return err
		}
		if res.Response.TradeState == wechat.TradeStateSuccess {
			payment.Status = "paid"
			payment.TransactionID = res.Response.TransactionId
		} else if res.Response.TradeState == wechat.TradeStateClosed || res.Response.TradeState == wechat.TradeStatePayError || res.Response.TradeState == wechat.TradeStateRevoked {
			payment.Status = "failed"
			payment.ErrorMessage = res.Response.TradeStateDesc
			return model.Write(func(tx *gorm.DB) error {
				return tx.Model(payment).Updates(map[string]interface{}{"status": payment.Status, "error_message": payment.ErrorMessage}).Error
			})
		}
	case "alipay":
		client, err := s.alipayClient(payment.TenantID)
		if err != nil {
			return err
		}
		bm := make(gopay.BodyMap)
		bm.Set("out_trade_no", payment.PaymentNo)
		res, err := client.TradeQuery(context.Background(), bm)
		if err != nil || res.Response == nil {
			return err
		}
		if res.Response.TradeStatus == "TRADE_SUCCESS" || res.Response.TradeStatus == "TRADE_FINISHED" {
			payment.Status = "paid"
			payment.TransactionID = res.Response.TradeNo
		} else if res.Response.TradeStatus == "TRADE_CLOSED" {
			payment.Status = "failed"
			return model.Write(func(tx *gorm.DB) error {
				return tx.Model(payment).Update("status", payment.Status).Error
			})
		}
	default:
		return errors.New("unsupported payment provider")
	}
	return nil
}
