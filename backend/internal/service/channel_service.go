package service

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
)

type ChannelService struct{}

type CtripChannelConfig struct {
	AESKey string `json:"aes_key"`
	AESIV  string `json:"aes_iv"`
}

type ChannelOrderSummary struct {
	ID                  uint      `json:"id"`
	OrderNo             string    `json:"order_no"`
	ExternalNo          *string   `json:"external_no,omitempty"`
	Status              string    `json:"status"`
	ContactName         string    `json:"contact_name"`
	ContactPhone        string    `json:"contact_phone"`
	TotalAmount         float64   `json:"total_amount"`
	TicketCount         int64     `json:"ticket_count"`
	UsedTicketCount     int64     `json:"used_ticket_count"`
	RefundedTicketCount int64     `json:"refunded_ticket_count"`
	PaidCents           int64     `json:"paid_cents"`
	RefundedCents       int64     `json:"refunded_cents"`
	CreatedAt           time.Time `json:"created_at"`
}

type ChannelOrderDetail struct {
	Order      model.Order              `json:"order"`
	Payments   []model.Payment          `json:"payments"`
	Refunds    []model.Refund           `json:"refunds"`
	AfterSales []model.AfterSaleRequest `json:"after_sales"`
	CheckIns   []model.CheckInRecord    `json:"check_ins"`
}

func randomChannelSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("ch_%x", b)
}

func (s *ChannelService) Create(tenantID uint, account *model.ChannelAccount, secret string) (string, error) {
	if tenantID == 0 || strings.TrimSpace(account.Code) == "" || strings.TrimSpace(account.Type) == "" {
		return "", errors.New("channel code and type are required")
	}
	if secret == "" {
		secret = randomChannelSecret()
	}
	ciphertext, err := utils.EncryptAES(secret)
	if err != nil {
		return "", err
	}
	account.Base = model.Base{}
	account.TenantID = tenantID
	account.Status = normalizeChannelStatus(account.Status)
	account.Environment = channelEnvironment(account.Status)
	account.SecretCiphertext = ciphertext
	if account.SignAlgorithm == "" {
		account.SignAlgorithm = "hmac-sha256"
	}
	if account.RateLimitPerMin <= 0 {
		account.RateLimitPerMin = 600
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := requireAnyActiveTenantCapability(tx, tenantID, "supplier", "distributor"); err != nil {
			return err
		}
		return tx.Create(account).Error
	}); err != nil {
		return "", err
	}
	return secret, nil
}

func (s *ChannelService) CreateCtrip(tenantID uint, account *model.ChannelAccount, accountID, signKey, aesKey, aesIV string) error {
	accountID, signKey = strings.TrimSpace(accountID), strings.TrimSpace(signKey)
	aesKey, aesIV = strings.TrimSpace(aesKey), strings.TrimSpace(aesIV)
	if tenantID == 0 || strings.TrimSpace(account.Code) == "" || accountID == "" || signKey == "" {
		return errors.New("渠道编码、携程接口账号和接口密钥必填")
	}
	if len([]byte(aesKey)) != 16 || len([]byte(aesIV)) != 16 {
		return errors.New("AES 密钥和初始向量必须各为 16 字节")
	}
	configJSON, _ := json.Marshal(CtripChannelConfig{AESKey: aesKey, AESIV: aesIV})
	secretCiphertext, err := utils.EncryptAES(signKey)
	if err != nil {
		return err
	}
	configCiphertext, err := utils.EncryptAES(string(configJSON))
	if err != nil {
		return err
	}
	account.Base = model.Base{}
	account.TenantID = tenantID
	account.Type = "ctrip"
	account.AppID = accountID
	account.Status = normalizeChannelStatus(account.Status)
	account.Environment = channelEnvironment(account.Status)
	account.SecretCiphertext = secretCiphertext
	account.ProtocolConfigCiphertext = configCiphertext
	account.SignAlgorithm = "md5"
	if account.RateLimitPerMin <= 0 {
		account.RateLimitPerMin = 600
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireAnyActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var duplicate int64
		if err := tx.Model(&model.ChannelAccount{}).Where("type = ? AND app_id = ?", "ctrip", accountID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return errors.New("该携程接口账号已被配置")
		}
		return tx.Create(account).Error
	})
}

func normalizeChannelStatus(value string) string {
	if value == "disabled" || value == "sandbox" {
		return value
	}
	return "active"
}

func channelEnvironment(status string) string {
	if status == "sandbox" {
		return "sandbox"
	}
	return "production"
}

func (s *ChannelService) List(tenantID uint) ([]model.ChannelAccount, error) {
	var accounts []model.ChannelAccount
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&accounts).Error; err != nil {
		return nil, err
	}
	for i := range accounts {
		accounts[i].ProtocolConfigured = accounts[i].Type == "ctrip" && accounts[i].AppID != "" && accounts[i].SecretCiphertext != "" && accounts[i].ProtocolConfigCiphertext != ""
		accounts[i].SecretCiphertext = ""
		accounts[i].VerifyKeyCiphertext = ""
		accounts[i].ProtocolConfigCiphertext = ""
	}
	return accounts, nil
}

func (s *ChannelService) ConfigureCtrip(tenantID, id uint, accountID, signKey, aesKey, aesIV string) error {
	accountID = strings.TrimSpace(accountID)
	signKey = strings.TrimSpace(signKey)
	aesKey = strings.TrimSpace(aesKey)
	aesIV = strings.TrimSpace(aesIV)
	if tenantID == 0 || id == 0 || accountID == "" || signKey == "" {
		return errors.New("携程接口账号和接口密钥必填")
	}
	if len([]byte(aesKey)) != 16 || len([]byte(aesIV)) != 16 {
		return errors.New("AES 密钥和初始向量必须各为 16 字节")
	}
	configJSON, err := json.Marshal(CtripChannelConfig{AESKey: aesKey, AESIV: aesIV})
	if err != nil {
		return err
	}
	secretCiphertext, err := utils.EncryptAES(signKey)
	if err != nil {
		return err
	}
	configCiphertext, err := utils.EncryptAES(string(configJSON))
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var duplicate int64
		if err := tx.Model(&model.ChannelAccount{}).Where("type = ? AND app_id = ? AND id != ?", "ctrip", accountID, id).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return errors.New("该携程接口账号已被配置")
		}
		result := tx.Model(&model.ChannelAccount{}).Where("id = ? AND tenant_id = ? AND type = ?", id, tenantID, "ctrip").Updates(map[string]interface{}{
			"app_id": accountID, "secret_ciphertext": secretCiphertext, "protocol_config_ciphertext": configCiphertext,
			"sign_algorithm": "md5", "key_version": gorm.Expr("key_version + 1"),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *ChannelService) SetStatus(tenantID, id uint, status string) error {
	if status != "active" && status != "disabled" && status != "sandbox" {
		return errors.New("invalid channel status")
	}
	return model.Write(func(tx *gorm.DB) error {
		updates := map[string]interface{}{"status": status}
		if status == "active" || status == "sandbox" {
			updates["environment"] = channelEnvironment(status)
		}
		result := tx.Model(&model.ChannelAccount{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *ChannelService) RotateSecret(tenantID, id uint) (string, error) {
	var account model.ChannelAccount
	if err := model.DB.Select("id", "type").Where("id = ? AND tenant_id = ?", id, tenantID).First(&account).Error; err != nil {
		return "", err
	}
	if account.Type == "ctrip" {
		return "", errors.New("携程渠道请通过“携程参数”完整更新接口密钥和 AES 参数")
	}
	secret := randomChannelSecret()
	ciphertext, err := utils.EncryptAES(secret)
	if err != nil {
		return "", err
	}
	err = model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.ChannelAccount{}).Where("id = ? AND tenant_id = ?", id, tenantID).
			Updates(map[string]interface{}{"secret_ciphertext": ciphertext, "key_version": gorm.Expr("key_version + 1")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	return secret, err
}

func (s *ChannelService) AddMapping(tenantID uint, mapping *model.ChannelProductMapping) error {
	if mapping.ChannelAccountID == 0 || mapping.ProductID == 0 || strings.TrimSpace(mapping.ExternalCode) == "" {
		return errors.New("channel, product and external code are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND status != ?", mapping.ChannelAccountID, tenantID, "disabled").First(&account).Error; err != nil {
			return errors.New("channel account not found")
		}
		var product model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", mapping.ProductID, tenantID).First(&product).Error; err != nil {
			return errors.New("product not found")
		}
		if account.Type == "ctrip" && (mapping.ChannelSaleCents <= 0 || mapping.ChannelCostCents < 0 || mapping.ChannelCostCents > mapping.ChannelSaleCents) {
			return errors.New("ctrip mapping requires a positive sale price and a cost price not greater than the sale price")
		}
		mapping.Base = model.Base{}
		mapping.Status = "active"
		return tx.Create(mapping).Error
	})
}

func (s *ChannelService) ListMappings(tenantID, accountID uint) ([]model.ChannelProductMapping, error) {
	var rows []model.ChannelProductMapping
	query := model.DB.Table("channel_product_mappings").Joins("JOIN channel_accounts ON channel_accounts.id = channel_product_mappings.channel_account_id").Where("channel_accounts.tenant_id = ?", tenantID)
	if accountID > 0 {
		query = query.Where("channel_product_mappings.channel_account_id = ?", accountID)
	}
	return rows, query.Order("channel_product_mappings.created_at DESC").Find(&rows).Error
}

func (s *ChannelService) UpdateMappingPricing(tenantID, accountID, mappingID uint, saleCents, costCents int64) error {
	if tenantID == 0 || accountID == 0 || mappingID == 0 || saleCents <= 0 || costCents < 0 || costCents > saleCents {
		return errors.New("a positive sale price and a cost price not greater than the sale price are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", accountID, tenantID, "ctrip").First(&account).Error; err != nil {
			return errors.New("ctrip channel account not found")
		}
		result := tx.Model(&model.ChannelProductMapping{}).Where("id = ? AND channel_account_id = ?", mappingID, account.ID).
			Updates(map[string]interface{}{"channel_sale_cents": saleCents, "channel_cost_cents": costCents})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *ChannelService) GetByCode(code string) (*model.ChannelAccount, string, error) {
	var account model.ChannelAccount
	query := model.DB.Where("code = ?", strings.TrimSpace(code))
	var count int64
	if err := query.Model(&model.ChannelAccount{}).Count(&count).Error; err != nil {
		return nil, "", err
	}
	if count != 1 {
		return nil, "", errors.New("channel code is ambiguous or unknown")
	}
	if err := query.First(&account).Error; err != nil {
		return nil, "", err
	}
	if account.Status == "disabled" {
		return nil, "", errors.New("channel is disabled")
	}
	if err := requireAnyActiveTenantCapability(model.DB, account.TenantID, "supplier", "distributor"); err != nil {
		return nil, "", errors.New("channel tenant is unavailable")
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt channel secret: %w", err)
	}
	return &account, secret, nil
}

func (s *ChannelService) ListRequests(tenantID, accountID uint, status string, page, pageSize int) ([]model.ChannelRequest, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.ChannelRequest{}).
		Joins("JOIN channel_accounts ON channel_accounts.id = channel_requests.channel_account_id").
		Where("channel_accounts.tenant_id = ?", tenantID)
	if accountID > 0 {
		query = query.Where("channel_requests.channel_account_id = ?", accountID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("channel_requests.status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.ChannelRequest
	if err := query.Select("channel_requests.*").Order("channel_requests.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *ChannelService) ListOrders(tenantID, accountID uint, search, status string, page, pageSize int) ([]ChannelOrderSummary, int64, error) {
	if tenantID == 0 || accountID == 0 {
		return nil, 0, errors.New("tenant and channel are required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.Order{}).
		Joins("JOIN channel_accounts ON channel_accounts.id = orders.channel_account_id").
		Where("orders.tenant_id = ? AND orders.channel_account_id = ? AND channel_accounts.tenant_id = ?", tenantID, accountID, tenantID)
	if value := strings.TrimSpace(status); value != "" {
		query = query.Where("orders.status = ?", value)
	}
	if value := strings.TrimSpace(search); value != "" {
		pattern := "%" + value + "%"
		query = query.Where("orders.order_no LIKE ? OR orders.external_no LIKE ? OR orders.contact_name LIKE ? OR orders.contact_phone LIKE ?", pattern, pattern, pattern, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ChannelOrderSummary
	selectColumns := `orders.id, orders.order_no, orders.external_no, orders.status, orders.contact_name, orders.contact_phone,
		orders.total_amount, orders.created_at,
		(SELECT COUNT(*) FROM tickets WHERE tickets.order_id = orders.id) AS ticket_count,
		(SELECT COUNT(*) FROM tickets WHERE tickets.order_id = orders.id AND tickets.status = 'used') AS used_ticket_count,
		(SELECT COUNT(*) FROM tickets WHERE tickets.order_id = orders.id AND tickets.status = 'refunded') AS refunded_ticket_count,
		(SELECT COALESCE(SUM(amount_cents), 0) FROM payments WHERE payments.tenant_id = orders.tenant_id AND payments.order_no = orders.order_no AND payments.status IN ('paid', 'refunded')) AS paid_cents,
		(SELECT COALESCE(SUM(amount_cents), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status IN ('succeeded', 'group_succeeded')) AS refunded_cents`
	if err := query.Select(selectColumns).Order("orders.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *ChannelService) GetOrder(tenantID, accountID uint, orderNo string) (*ChannelOrderDetail, error) {
	if tenantID == 0 || accountID == 0 || strings.TrimSpace(orderNo) == "" {
		return nil, errors.New("tenant, channel and order are required")
	}
	var order model.Order
	if err := model.DB.
		Preload("Items.Tickets").Preload("Items.VisitorRecords").
		Joins("JOIN channel_accounts ON channel_accounts.id = orders.channel_account_id").
		Where("orders.order_no = ? AND orders.tenant_id = ? AND orders.channel_account_id = ? AND channel_accounts.tenant_id = ?", strings.TrimSpace(orderNo), tenantID, accountID, tenantID).
		First(&order).Error; err != nil {
		return nil, err
	}
	detail := &ChannelOrderDetail{Order: order}
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, order.OrderNo).Order("created_at").Find(&detail.Payments).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, order.OrderNo).Order("created_at").Find(&detail.Refunds).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Preload("Events").Where("tenant_id = ? AND order_no = ?", tenantID, order.OrderNo).Order("created_at").Find(&detail.AfterSales).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("check_in_records").Select("check_in_records.*").
		Joins("JOIN tickets ON tickets.id = check_in_records.ticket_id").
		Where("tickets.order_id = ?", order.ID).Order("check_in_records.check_in_time").Scan(&detail.CheckIns).Error; err != nil {
		return nil, err
	}
	return detail, nil
}

func (s *ChannelService) AuthorizeRequestRetry(tenantID, accountID, requestID, actorUserID uint, actorRole, reason string) error {
	reason = strings.TrimSpace(reason)
	if tenantID == 0 || accountID == 0 || requestID == 0 || reason == "" {
		return errors.New("channel request and retry reason are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ?", accountID, tenantID).First(&account).Error; err != nil {
			return errors.New("channel account not found")
		}
		var request model.ChannelRequest
		if err := tx.Where("id = ? AND channel_account_id = ?", requestID, account.ID).First(&request).Error; err != nil {
			return errors.New("channel request not found")
		}
		if request.Status == "processing" {
			leaseAt := request.CreatedAt
			if request.LockedAt != nil {
				leaseAt = *request.LockedAt
			}
			if time.Since(leaseAt) < 5*time.Minute {
				return errors.New("channel request is still processing")
			}
		} else if request.Status != "failed" {
			return errors.New("only failed or stale processing requests can be retried")
		}
		if err := tx.Model(&request).Updates(map[string]interface{}{"status": "retryable", "locked_at": nil}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "channel.request.retry_authorized", "channel_request", request.ID, reason,
			fmt.Sprintf(`{"status":%q,"response_status":%d,"attempt_count":%d}`, request.Status, request.ResponseStatus, request.AttemptCount),
			fmt.Sprintf(`{"status":"retryable","attempt_count":%d}`, request.AttemptCount))
	})
}
