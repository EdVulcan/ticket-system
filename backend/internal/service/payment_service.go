package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
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

func (s *PaymentService) GetConfig(tenantID uint, provider string) (*model.PaymentConfig, error) {
	var paymentConfig model.PaymentConfig
	if err := model.DB.Where("tenant_id = ? AND provider = ? AND status = ?", tenantID, provider, true).First(&paymentConfig).Error; err != nil {
		return nil, err
	}
	var err error
	if paymentConfig.Key != "" {
		paymentConfig.Key, err = utils.DecryptAES(paymentConfig.Key)
		if err != nil {
			return nil, fmt.Errorf("decrypt payment key: %w", err)
		}
	}
	if paymentConfig.PrivateKey != "" {
		paymentConfig.PrivateKey, err = utils.DecryptAES(paymentConfig.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt private key: %w", err)
		}
	}
	if paymentConfig.PublicKey != "" {
		paymentConfig.PublicKey, err = utils.DecryptAES(paymentConfig.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt public key: %w", err)
		}
	}
	if paymentConfig.PlatformPublicKey != "" {
		paymentConfig.PlatformPublicKey, err = utils.DecryptAES(paymentConfig.PlatformPublicKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt payment platform public key: %w", err)
		}
	}
	return &paymentConfig, nil
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

	var order model.Order
	if err := model.Write(func(tx *gorm.DB) error {
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
			Where("tenant_id = ? AND order_no = ? AND status IN ?", tenantID, req.OrderNo, []string{"pending", "paid"}).
			Count(&activeAttempts).Error; err != nil {
			return err
		}
		if activeAttempts > 0 {
			return fmt.Errorf("an active payment already exists for this order")
		}
		req.Base = model.Base{}
		req.TenantID = tenantID
		req.PaymentNo = generatePaymentNo()
		req.Amount = order.TotalAmount
		req.AmountCents = moneyCents(order.TotalAmount)
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
		} else if s.OrderService != nil {
			_ = s.OrderService.Cancel(req.OrderNo, tenantID)
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
		if stored.Status == "paid" {
			return nil
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_no = ? AND tenant_id = ?", stored.OrderNo, stored.TenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		if err := tx.Model(&stored).Updates(map[string]interface{}{
			"status": "paid", "transaction_id": payment.TransactionID, "code_url": payment.CodeURL, "paid_at": time.Now(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("status", "paid").Error; err != nil {
			return err
		}
		return updateFulfillmentOrdersTx(tx, order.ID, "paid")
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
		_ = s.OrderService.Cancel(payment.OrderNo, tenantID)
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
		if err := tx.Model(&order).Update("status", "paid").Error; err != nil {
			return err
		}
		return updateFulfillmentOrdersTx(tx, order.ID, "paid")
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
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", payment.OrderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "unpaid" {
			return cancelOrderTx(tx, &order)
		}
		return nil
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
