package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"

	"gorm.io/gorm"
)

type ChannelService struct{}

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

func normalizeChannelStatus(value string) string {
	if value == "disabled" || value == "sandbox" {
		return value
	}
	return "active"
}

func (s *ChannelService) List(tenantID uint) ([]model.ChannelAccount, error) {
	var accounts []model.ChannelAccount
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&accounts).Error; err != nil {
		return nil, err
	}
	for i := range accounts {
		accounts[i].SecretCiphertext = ""
		accounts[i].VerifyKeyCiphertext = ""
	}
	return accounts, nil
}

func (s *ChannelService) SetStatus(tenantID, id uint, status string) error {
	if status != "active" && status != "disabled" && status != "sandbox" {
		return errors.New("invalid channel status")
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.ChannelAccount{}).Where("id = ? AND tenant_id = ?", id, tenantID).Update("status", status)
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
