package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
		req.Status = "failed"
		req.ErrorMessage = err.Error()
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(req).Updates(map[string]interface{}{"status": req.Status, "error_message": req.ErrorMessage}).Error
		})
		return err
	}
	if req.Status == "paid" {
		return s.completePayment(req)
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(req).Updates(map[string]interface{}{
			"status": req.Status, "transaction_id": req.TransactionID, "code_url": req.CodeURL,
		}).Error
	})
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
			amount.Set("total", int64(req.Amount*100)).Set("currency", "CNY")
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
			"status": "paid", "transaction_id": payment.TransactionID, "code_url": payment.CodeURL,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&order).Update("status", "paid").Error
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
	return &payment, nil
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
