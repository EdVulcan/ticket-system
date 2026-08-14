package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ID                  uint     `json:"id"`
	Name                string   `json:"name"`
	ScenicAreaName      string   `json:"scenic_area_name,omitempty"`
	ImageURL            string   `json:"image_url,omitempty"`
	Description         string   `json:"description,omitempty"`
	ProductType         int      `json:"product_type"`
	ProductKind         string   `json:"product_kind"`
	PriceCents          int64    `json:"price_cents"`
	Tags                []string `json:"tags"`
	ValidityType        string   `json:"validity_type"`
	ValidityDays        int      `json:"validity_days,omitempty"`
	RequiresUseDate     bool     `json:"requires_use_date"`
	HotelName           string   `json:"hotel_name,omitempty"`
	RoomTypeName        string   `json:"room_type_name,omitempty"`
	RatePlanName        string   `json:"rate_plan_name,omitempty"`
	Nights              int      `json:"nights,omitempty"`
	RoomsPerPackage     int      `json:"rooms_per_package,omitempty"`
	BookingMode         string   `json:"booking_mode,omitempty"`
	VoucherValidityDays int      `json:"voucher_validity_days,omitempty"`
	MinAdvanceDays      int      `json:"min_advance_days,omitempty"`
	MaxReschedules      int      `json:"max_reschedules,omitempty"`
}

type MiniappCatalog struct {
	StoreName     string                  `json:"store_name"`
	Environment   string                  `json:"environment"`
	MaxOrderCents int64                   `json:"max_order_cents,omitempty"`
	Products      []MiniappCatalogProduct `json:"products"`
}

type MiniappOrderCreateInput struct {
	MappingID       uint   `json:"mapping_id"`
	Quantity        int    `json:"quantity"`
	ClientRequestID string `json:"request_id"`
	UseDate         string `json:"use_date"`
	GuestName       string `json:"guest_name"`
	ContactPhone    string `json:"contact_phone"`
}

type MiniappHotelStay struct {
	HotelName    string    `json:"hotel_name"`
	RoomTypeName string    `json:"room_type_name"`
	RatePlanName string    `json:"rate_plan_name"`
	CheckInDate  time.Time `json:"check_in_date"`
	CheckOutDate time.Time `json:"check_out_date"`
	Rooms        int       `json:"rooms"`
	GuestName    string    `json:"guest_name"`
	ContactPhone string    `json:"contact_phone"`
}

type MiniappPackageEntitlement struct {
	EntitlementNo      string     `json:"entitlement_no"`
	Status             string     `json:"status"`
	ValidFrom          time.Time  `json:"valid_from"`
	ValidUntil         time.Time  `json:"valid_until"`
	CheckInDate        *time.Time `json:"check_in_date,omitempty"`
	CheckOutDate       *time.Time `json:"check_out_date,omitempty"`
	HotelName          string     `json:"hotel_name,omitempty"`
	RoomTypeName       string     `json:"room_type_name,omitempty"`
	GuestName          string     `json:"guest_name,omitempty"`
	ContactPhone       string     `json:"contact_phone,omitempty"`
	RescheduleCount    int        `json:"reschedule_count"`
	MaxReschedules     int        `json:"max_reschedules"`
	Nights             int        `json:"nights"`
	MinAdvanceDays     int        `json:"min_advance_days"`
	PlatformSyncStatus string     `json:"platform_sync_status"`
}

type MiniappPackageBookingInput struct {
	EntitlementNo   string `json:"entitlement_no"`
	CheckInDate     string `json:"check_in_date"`
	GuestName       string `json:"guest_name"`
	ContactPhone    string `json:"contact_phone"`
	ClientRequestID string `json:"request_id"`
}

type MiniappOrderResult struct {
	OrderNo              string                      `json:"order_no"`
	PlatformOrderID      string                      `json:"order_id,omitempty"`
	ProductName          string                      `json:"product_name,omitempty"`
	ImageURL             string                      `json:"image_url,omitempty"`
	Quantity             int                         `json:"quantity"`
	PayToken             string                      `json:"pay_token,omitempty"`
	AmountCents          int64                       `json:"amount_cents"`
	Status               string                      `json:"status"`
	CoreOrderStatus      string                      `json:"core_order_status"`
	PlatformPaymentState string                      `json:"platform_payment_state"`
	ExpiresAt            *time.Time                  `json:"expires_at,omitempty"`
	TicketCodes          []string                    `json:"ticket_codes,omitempty"`
	ProductKind          string                      `json:"product_kind"`
	HotelStay            *MiniappHotelStay           `json:"hotel_stay,omitempty"`
	PackageEntitlements  []MiniappPackageEntitlement `json:"package_entitlements,omitempty"`
}

type MiniappOrderSummary struct {
	OrderNo              string     `json:"order_no"`
	ProductName          string     `json:"product_name"`
	ProductKind          string     `json:"product_kind"`
	ImageURL             string     `json:"image_url,omitempty"`
	Quantity             int        `json:"quantity"`
	AmountCents          int64      `json:"amount_cents"`
	Status               string     `json:"status"`
	CoreOrderStatus      string     `json:"core_order_status"`
	PlatformPaymentState string     `json:"platform_payment_state"`
	CreatedAt            time.Time  `json:"created_at"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
}

type MiniappOrderPage struct {
	Items    []MiniappOrderSummary `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
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
	var account model.ChannelAccount
	if err := model.DB.Select("id", "environment").Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", customer.ChannelAccountID, customer.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	type catalogRow struct {
		MappingID           uint
		DisplayName         string
		ProductName         string
		ScenicAreaName      string
		ChannelSaleCents    int64
		ProductPrice        float64
		Tags                string
		ValidityType        string
		ValidityDays        int
		ImageURL            string
		Description         string
		ProductType         int
		StockType           string
		PackageID           uint
		HotelName           string
		RoomTypeName        string
		RatePlanName        string
		Nights              int
		RoomsPerPackage     int
		BookingMode         string
		VoucherValidityDays int
		MinAdvanceDays      int
		MaxReschedules      int
	}
	var rows []catalogRow
	err := model.DB.Table("channel_product_mappings AS mapping").
		Select(`mapping.id AS mapping_id, mapping.display_name, product.name AS product_name,
			scenic.name AS scenic_area_name, mapping.channel_sale_cents, product.price AS product_price,
			product.tags, product.validity_type, product.validity_days, product.stock_type,
			xhs_config.image_url, xhs_config.description, xhs_config.product_type,
			hotel_package.id AS package_id, hotel.name AS hotel_name, room.name AS room_type_name,
			rate.name AS rate_plan_name, hotel_package.nights, hotel_package.rooms_per_package,
			hotel_package.booking_mode, hotel_package.voucher_validity_days,
			hotel_package.min_advance_days, hotel_package.max_reschedules`).
		Joins("JOIN products AS product ON product.id = mapping.product_id AND product.tenant_id = ? AND product.deleted_at IS NULL", customer.TenantID).
		Joins(`JOIN tenants AS fulfillment_tenant ON fulfillment_tenant.id = COALESCE(NULLIF(product.fulfillment_tenant_id, 0), NULLIF(product.source_tenant_id, 0), product.tenant_id)
			AND fulfillment_tenant.status = 'active' AND fulfillment_tenant.deleted_at IS NULL`).
		Joins(`JOIN tenant_capabilities AS supplier_capability ON supplier_capability.tenant_id = fulfillment_tenant.id
			AND supplier_capability.capability = 'supplier' AND supplier_capability.status = 'active' AND supplier_capability.deleted_at IS NULL`).
		Joins(`JOIN supplier_business_types AS supplier_business ON supplier_business.tenant_id = fulfillment_tenant.id
			AND supplier_business.business_type = 'scenic' AND supplier_business.status = 'active' AND supplier_business.deleted_at IS NULL`).
		Joins("JOIN xiaohongshu_product_configs AS xhs_config ON xhs_config.channel_product_mapping_id = mapping.id AND xhs_config.tenant_id = ? AND xhs_config.sync_status = ? AND xhs_config.deleted_at IS NULL", customer.TenantID, "synced").
		Joins(`LEFT JOIN scenic_areas AS scenic ON scenic.id = CASE
			WHEN product.fulfillment_scenic_area_id != 0 THEN product.fulfillment_scenic_area_id ELSE product.scenic_area_id END
			AND scenic.tenant_id = CASE WHEN product.fulfillment_tenant_id != 0 THEN product.fulfillment_tenant_id ELSE product.tenant_id END
			AND scenic.deleted_at IS NULL`).
		Joins("LEFT JOIN scenic_hotel_packages AS hotel_package ON hotel_package.product_id = product.id AND hotel_package.tenant_id = product.tenant_id AND hotel_package.deleted_at IS NULL").
		Joins("LEFT JOIN hotel_properties AS hotel ON hotel.id = hotel_package.hotel_id AND hotel.tenant_id = hotel_package.tenant_id AND hotel.deleted_at IS NULL").
		Joins("LEFT JOIN hotel_room_types AS room ON room.id = hotel_package.room_type_id AND room.hotel_id = hotel_package.hotel_id AND room.tenant_id = hotel_package.tenant_id AND room.deleted_at IS NULL").
		Joins("LEFT JOIN hotel_rate_plans AS rate ON rate.id = hotel_package.rate_plan_id AND rate.room_type_id = hotel_package.room_type_id AND rate.tenant_id = hotel_package.tenant_id AND rate.deleted_at IS NULL").
		Joins(`LEFT JOIN supplier_business_types AS hotel_business ON hotel_business.tenant_id = fulfillment_tenant.id
			AND hotel_business.business_type = 'hotel' AND hotel_business.status = 'active' AND hotel_business.deleted_at IS NULL`).
		Where("mapping.channel_account_id = ? AND mapping.status = ? AND mapping.deleted_at IS NULL AND product.status = ?", customer.ChannelAccountID, "active", "online").
		Where("hotel_package.id IS NULL OR (hotel_package.status = ? AND hotel.status = ? AND room.status = ? AND rate.status = ? AND hotel_business.id IS NOT NULL)", "online", "active", "active", "active").
		Where("supplier_capability.expires_at IS NULL OR supplier_capability.expires_at > ?", s.now()).
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
		kind := "ticket"
		if row.PackageID != 0 {
			kind = "scenic_hotel_package"
		}
		products = append(products, MiniappCatalogProduct{
			ID: row.MappingID, Name: name, ScenicAreaName: row.ScenicAreaName,
			ImageURL: row.ImageURL, Description: row.Description, ProductType: row.ProductType,
			ProductKind: kind, PriceCents: priceCents, Tags: parseProductTags(row.Tags),
			ValidityType: row.ValidityType, ValidityDays: row.ValidityDays,
			RequiresUseDate: (row.PackageID != 0 && row.BookingMode != "after_purchase") || (row.PackageID == 0 && row.StockType == "daily"),
			HotelName:       row.HotelName, RoomTypeName: row.RoomTypeName, RatePlanName: row.RatePlanName,
			Nights: row.Nights, RoomsPerPackage: row.RoomsPerPackage, BookingMode: row.BookingMode,
			VoucherValidityDays: row.VoucherValidityDays, MinAdvanceDays: row.MinAdvanceDays,
			MaxReschedules: row.MaxReschedules,
		})
	}
	catalog := &MiniappCatalog{StoreName: tenant.Name, Environment: account.Environment, Products: products}
	if account.Environment == "sandbox" {
		catalog.MaxOrderCents = 10
	}
	return catalog, nil
}

func randomXiaohongshuOrderID() (string, error) {
	value, err := randomMiniappToken()
	if err != nil {
		return "", err
	}
	return "XHS" + strings.ToUpper(value[:24]), nil
}

func miniappPathWithOrder(path, orderNo string) string {
	path = strings.TrimSpace(path)
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "order_no=" + orderNo
}

func miniappBookingExternalID(entitlementID uint, requestID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", entitlementID, strings.TrimSpace(requestID))))
	return fmt.Sprintf("XHSBOOK%d%s", entitlementID, strings.ToUpper(hex.EncodeToString(sum[:8])))
}

func firstXiaohongshuPOIID(raw string) string {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil || len(ids) == 0 {
		return ""
	}
	return strings.TrimSpace(ids[0])
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
