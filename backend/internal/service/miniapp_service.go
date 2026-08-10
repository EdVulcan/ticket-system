package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

var (
	ErrMiniappUnauthenticated = errors.New("miniapp session is invalid or expired")
	ErrMiniappUnavailable     = errors.New("miniapp channel is unavailable")
)

type MiniappService struct {
	NewXiaohongshuClient func(appID, secret, environment string) *xiaohongshu.Client
	Now                  func() time.Time
}

type MiniappLoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type MiniappCatalogProduct struct {
	ID             uint     `json:"id"`
	Name           string   `json:"name"`
	ScenicAreaName string   `json:"scenic_area_name,omitempty"`
	PriceCents     int64    `json:"price_cents"`
	Tags           []string `json:"tags"`
	ValidityType   string   `json:"validity_type"`
	ValidityDays   int      `json:"validity_days,omitempty"`
}

type MiniappCatalog struct {
	StoreName string                  `json:"store_name"`
	Products  []MiniappCatalogProduct `json:"products"`
}

func NewMiniappService() MiniappService {
	return MiniappService{
		NewXiaohongshuClient: xiaohongshu.NewClient,
	}
}

func (s MiniappService) LoginXiaohongshu(ctx context.Context, appID, code string) (*MiniappLoginResult, error) {
	appID, code = strings.TrimSpace(appID), strings.TrimSpace(code)
	if appID == "" || code == "" {
		return nil, errors.New("miniapp appid and temporary login code are required")
	}
	account, secret, err := loadActiveXiaohongshuAccount(appID)
	if err != nil {
		return nil, err
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = NewMiniappService().NewXiaohongshuClient
	}
	platformSession, err := newClient(account.AppID, secret, account.Environment).Code2Session(ctx, code)
	if err != nil {
		return nil, err
	}

	openIDCiphertext, err := utils.EncryptAES(platformSession.OpenID)
	if err != nil {
		return nil, err
	}
	sessionKeyCiphertext, err := utils.EncryptAES(platformSession.SessionKey)
	if err != nil {
		return nil, err
	}
	token, err := randomMiniappToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	expiresAt := now.Add(7 * 24 * time.Hour)
	openIDHash := hashMiniappValue(platformSession.OpenID)
	tokenHash := hashMiniappValue(token)

	err = model.Write(func(tx *gorm.DB) error {
		var current model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", account.ID, account.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&current).Error; err != nil {
			return ErrMiniappUnavailable
		}
		var tenant model.Tenant
		if err := tx.Where("id = ? AND status = ?", account.TenantID, "active").First(&tenant).Error; err != nil {
			return ErrMiniappUnavailable
		}
		if err := requireAnyActiveTenantCapability(tx, account.TenantID, "supplier", "distributor"); err != nil {
			return ErrMiniappUnavailable
		}

		var customer model.MiniappCustomer
		err := tx.Where("channel_account_id = ? AND open_id_hash = ?", account.ID, openIDHash).First(&customer).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			customer = model.MiniappCustomer{
				TenantID: account.TenantID, ChannelAccountID: account.ID, OpenIDHash: openIDHash,
				OpenIDCiphertext: openIDCiphertext, SessionKeyCiphertext: sessionKeyCiphertext,
				SessionTokenHash: tokenHash, SessionExpiresAt: expiresAt, Status: "active", LastLoginAt: now,
			}
			return tx.Create(&customer).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&customer).Updates(map[string]interface{}{
			"open_id_ciphertext": openIDCiphertext, "session_key_ciphertext": sessionKeyCiphertext,
			"session_token_hash": tokenHash, "session_expires_at": expiresAt, "status": "active", "last_login_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &MiniappLoginResult{Token: token, ExpiresAt: expiresAt}, nil
}

func (s MiniappService) Authenticate(token string) (*model.MiniappCustomer, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrMiniappUnauthenticated
	}
	var customer model.MiniappCustomer
	if err := model.DB.Where("session_token_hash = ? AND status = ? AND session_expires_at > ?", hashMiniappValue(token), "active", s.now()).First(&customer).Error; err != nil {
		return nil, ErrMiniappUnauthenticated
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", customer.ChannelAccountID, customer.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	var tenant model.Tenant
	if err := model.DB.Where("id = ? AND status = ?", customer.TenantID, "active").First(&tenant).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	if err := requireAnyActiveTenantCapability(model.DB, customer.TenantID, "supplier", "distributor"); err != nil {
		return nil, ErrMiniappUnavailable
	}
	return &customer, nil
}

func (s MiniappService) ListCatalog(customer *model.MiniappCustomer) (*MiniappCatalog, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	var tenant model.Tenant
	if err := model.DB.Select("id", "name").Where("id = ? AND status = ?", customer.TenantID, "active").First(&tenant).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	type catalogRow struct {
		MappingID        uint
		DisplayName      string
		ProductName      string
		ScenicAreaName   string
		ChannelSaleCents int64
		ProductPrice     float64
		Tags             string
		ValidityType     string
		ValidityDays     int
	}
	var rows []catalogRow
	err := model.DB.Table("channel_product_mappings AS mapping").
		Select(`mapping.id AS mapping_id, mapping.display_name, product.name AS product_name,
			scenic.name AS scenic_area_name, mapping.channel_sale_cents, product.price AS product_price,
			product.tags, product.validity_type, product.validity_days`).
		Joins("JOIN products AS product ON product.id = mapping.product_id AND product.tenant_id = ? AND product.deleted_at IS NULL", customer.TenantID).
		Joins(`LEFT JOIN scenic_areas AS scenic ON scenic.id = CASE
			WHEN product.fulfillment_scenic_area_id != 0 THEN product.fulfillment_scenic_area_id ELSE product.scenic_area_id END
			AND scenic.tenant_id = CASE WHEN product.fulfillment_tenant_id != 0 THEN product.fulfillment_tenant_id ELSE product.tenant_id END
			AND scenic.deleted_at IS NULL`).
		Where("mapping.channel_account_id = ? AND mapping.status = ? AND mapping.deleted_at IS NULL AND product.status = ?", customer.ChannelAccountID, "active", "online").
		Order("mapping.created_at ASC, mapping.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	products := make([]MiniappCatalogProduct, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = row.ProductName
		}
		priceCents := row.ChannelSaleCents
		if priceCents <= 0 {
			priceCents = int64(math.Round(row.ProductPrice * 100))
		}
		products = append(products, MiniappCatalogProduct{
			ID: row.MappingID, Name: name, ScenicAreaName: row.ScenicAreaName,
			PriceCents: priceCents, Tags: parseProductTags(row.Tags),
			ValidityType: row.ValidityType, ValidityDays: row.ValidityDays,
		})
	}
	return &MiniappCatalog{StoreName: tenant.Name, Products: products}, nil
}

func loadActiveXiaohongshuAccount(appID string) (*model.ChannelAccount, string, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("type = ? AND app_id = ? AND status IN ?", "xiaohongshu", appID, []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, "", ErrMiniappUnavailable
	}
	var tenant model.Tenant
	if err := model.DB.Where("id = ? AND status = ?", account.TenantID, "active").First(&tenant).Error; err != nil {
		return nil, "", ErrMiniappUnavailable
	}
	if err := requireAnyActiveTenantCapability(model.DB, account.TenantID, "supplier", "distributor"); err != nil {
		return nil, "", ErrMiniappUnavailable
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		return nil, "", ErrMiniappUnavailable
	}
	return &account, secret, nil
}

func randomMiniappToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashMiniappValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func parseProductTags(raw string) []string {
	var tags []string
	if json.Unmarshal([]byte(raw), &tags) == nil {
		result := make([]string, 0, len(tags))
		for _, tag := range tags {
			if value := strings.TrimSpace(tag); value != "" {
				result = append(result, value)
			}
		}
		return result
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (s MiniappService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
