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
	"gorm.io/gorm/clause"
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
	ID              uint     `json:"id"`
	Name            string   `json:"name"`
	ScenicAreaName  string   `json:"scenic_area_name,omitempty"`
	ImageURL        string   `json:"image_url,omitempty"`
	Description     string   `json:"description,omitempty"`
	ProductType     int      `json:"product_type"`
	ProductKind     string   `json:"product_kind"`
	PriceCents      int64    `json:"price_cents"`
	Tags            []string `json:"tags"`
	ValidityType    string   `json:"validity_type"`
	ValidityDays    int      `json:"validity_days,omitempty"`
	RequiresUseDate bool     `json:"requires_use_date"`
	HotelName       string   `json:"hotel_name,omitempty"`
	RoomTypeName    string   `json:"room_type_name,omitempty"`
	RatePlanName    string   `json:"rate_plan_name,omitempty"`
	Nights          int      `json:"nights,omitempty"`
	RoomsPerPackage int      `json:"rooms_per_package,omitempty"`
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

type MiniappOrderResult struct {
	OrderNo         string            `json:"order_no"`
	PlatformOrderID string            `json:"order_id,omitempty"`
	ProductName     string            `json:"product_name,omitempty"`
	ImageURL        string            `json:"image_url,omitempty"`
	Quantity        int               `json:"quantity"`
	PayToken        string            `json:"pay_token,omitempty"`
	AmountCents     int64             `json:"amount_cents"`
	Status          string            `json:"status"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	TicketCodes     []string          `json:"ticket_codes,omitempty"`
	ProductKind     string            `json:"product_kind"`
	HotelStay       *MiniappHotelStay `json:"hotel_stay,omitempty"`
}

type MiniappOrderSummary struct {
	OrderNo     string     `json:"order_no"`
	ProductName string     `json:"product_name"`
	ProductKind string     `json:"product_kind"`
	ImageURL    string     `json:"image_url,omitempty"`
	Quantity    int        `json:"quantity"`
	AmountCents int64      `json:"amount_cents"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
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
		MappingID        uint
		DisplayName      string
		ProductName      string
		ScenicAreaName   string
		ChannelSaleCents int64
		ProductPrice     float64
		Tags             string
		ValidityType     string
		ValidityDays     int
		ImageURL         string
		Description      string
		ProductType      int
		StockType        string
		PackageID        uint
		HotelName        string
		RoomTypeName     string
		RatePlanName     string
		Nights           int
		RoomsPerPackage  int
	}
	var rows []catalogRow
	err := model.DB.Table("channel_product_mappings AS mapping").
		Select(`mapping.id AS mapping_id, mapping.display_name, product.name AS product_name,
			scenic.name AS scenic_area_name, mapping.channel_sale_cents, product.price AS product_price,
			product.tags, product.validity_type, product.validity_days, product.stock_type,
			xhs_config.image_url, xhs_config.description, xhs_config.product_type,
			hotel_package.id AS package_id, hotel.name AS hotel_name, room.name AS room_type_name,
			rate.name AS rate_plan_name, hotel_package.nights, hotel_package.rooms_per_package`).
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
			RequiresUseDate: row.PackageID != 0 || row.StockType == "daily",
			HotelName:       row.HotelName, RoomTypeName: row.RoomTypeName, RatePlanName: row.RatePlanName,
			Nights: row.Nights, RoomsPerPackage: row.RoomsPerPackage,
		})
	}
	catalog := &MiniappCatalog{StoreName: tenant.Name, Environment: account.Environment, Products: products}
	if account.Environment == "sandbox" {
		catalog.MaxOrderCents = 10
	}
	return catalog, nil
}

func (s MiniappService) ListXiaohongshuOrders(customer *model.MiniappCustomer, page, pageSize int) (*MiniappOrderPage, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 {
		pageSize = 10
	}

	base := model.DB.Table("xiaohongshu_order_links AS link").
		Where("link.miniapp_customer_id = ? AND link.channel_account_id = ? AND link.tenant_id = ? AND link.deleted_at IS NULL", customer.ID, customer.ChannelAccountID, customer.TenantID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, err
	}

	type orderRow struct {
		OrderNo     string
		ProductName string
		ImageURL    string
		Quantity    int
		TotalAmount float64
		Status      string
		CreatedAt   time.Time
		ExpiresAt   *time.Time
		PackageID   uint
	}
	var rows []orderRow
	err := base.
		Select(`orders.order_no, item.product_name, COALESCE(xhs_config.image_url, '') AS image_url,
			item.quantity, orders.total_amount, link.state AS status, orders.created_at, link.pay_token_expires_at AS expires_at,
			COALESCE(hotel_package.id, 0) AS package_id`).
		Joins("JOIN orders ON orders.id = link.order_id AND orders.tenant_id = link.tenant_id AND orders.deleted_at IS NULL").
		Joins("JOIN order_items AS item ON item.order_id = orders.id AND item.deleted_at IS NULL").
		Joins("LEFT JOIN channel_product_mappings AS mapping ON mapping.channel_account_id = link.channel_account_id AND mapping.product_id = item.product_id AND mapping.deleted_at IS NULL").
		Joins("LEFT JOIN xiaohongshu_product_configs AS xhs_config ON xhs_config.channel_product_mapping_id = mapping.id AND xhs_config.tenant_id = link.tenant_id AND xhs_config.deleted_at IS NULL").
		Joins("LEFT JOIN scenic_hotel_packages AS hotel_package ON hotel_package.product_id = item.product_id AND hotel_package.tenant_id = item.fulfillment_tenant_id AND hotel_package.deleted_at IS NULL").
		Order("orders.created_at DESC, orders.id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]MiniappOrderSummary, 0, len(rows))
	for _, row := range rows {
		kind := "ticket"
		if row.PackageID != 0 {
			kind = "scenic_hotel_package"
		}
		items = append(items, MiniappOrderSummary{
			OrderNo: row.OrderNo, ProductName: row.ProductName, ProductKind: kind, ImageURL: row.ImageURL,
			Quantity: row.Quantity, AmountCents: moneyCents(row.TotalAmount), Status: row.Status,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
	}
	return &MiniappOrderPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s MiniappService) CreateXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, input MiniappOrderCreateInput) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.MappingID == 0 || input.Quantity <= 0 || input.Quantity > 100 || input.ClientRequestID == "" || len(input.ClientRequestID) > 100 {
		return nil, errors.New("请选择票种、数量并提供有效的请求编号")
	}
	if existing, err := s.loadOrderResult(customer, input.ClientRequestID); err == nil {
		return existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var account model.ChannelAccount
	var mapping model.ChannelProductMapping
	var product model.Product
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", customer.ChannelAccountID, customer.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	if err := model.DB.Where("id = ? AND channel_account_id = ? AND status = ?", input.MappingID, account.ID, "active").First(&mapping).Error; err != nil {
		return nil, errors.New("票种当前不可购买")
	}
	if err := model.DB.Where("id = ? AND tenant_id = ? AND status = ?", mapping.ProductID, customer.TenantID, "online").First(&product).Error; err != nil {
		return nil, errors.New("票种当前不可购买")
	}
	if err := model.DB.Where("channel_product_mapping_id = ? AND tenant_id = ? AND channel_account_id = ? AND sync_status = ?", mapping.ID, customer.TenantID, account.ID, "synced").First(&config).Error; err != nil {
		return nil, errors.New("票种尚未完成小红书商品同步")
	}
	var hotelPackage model.ScenicHotelPackage
	hasHotelPackage := false
	if err := model.DB.Where("tenant_id = ? AND product_id = ?", customer.TenantID, product.ID).First(&hotelPackage).Error; err == nil {
		hasHotelPackage = true
		if hotelPackage.Status != "online" {
			return nil, errors.New("酒景套餐当前不可购买")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var useDate *time.Time
	if value := strings.TrimSpace(input.UseDate); value != "" {
		parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return nil, errors.New("请选择有效的使用日期")
		}
		useDate = &parsed
	}
	if (hasHotelPackage || product.StockType == "daily") && useDate == nil {
		if hasHotelPackage {
			return nil, errors.New("请选择入住日期")
		}
		return nil, errors.New("请选择游玩日期")
	}
	input.GuestName, input.ContactPhone = strings.TrimSpace(input.GuestName), strings.TrimSpace(input.ContactPhone)
	if hasHotelPackage && (input.GuestName == "" || input.ContactPhone == "" || len(input.GuestName) > 50 || len(input.ContactPhone) > 20) {
		return nil, errors.New("请填写有效的入住人和联系电话")
	}
	totalCents := mapping.ChannelSaleCents * int64(input.Quantity)
	if totalCents <= 0 {
		return nil, errors.New("票种售价无效")
	}
	if account.Environment == "sandbox" && totalCents > 10 {
		return nil, errors.New("测试小程序单笔订单金额不能超过 0.10 元")
	}
	externalID, err := randomXiaohongshuOrderID()
	if err != nil {
		return nil, err
	}
	order := model.Order{
		TenantID: customer.TenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID,
		ExternalNo: &externalID, ContactName: input.GuestName, ContactPhone: input.ContactPhone,
		Items: []model.OrderItem{{ProductID: product.ID, Quantity: input.Quantity, UseDate: useDate}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		return nil, err
	}
	link := model.XiaohongshuOrderLink{
		TenantID: customer.TenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID,
		OrderID: order.ID, ClientRequestID: input.ClientRequestID, ExternalOrderID: externalID, State: "creating",
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&link).Error }); err != nil {
		_ = (&OrderService{}).Cancel(order.OrderNo, customer.TenantID)
		if existing, findErr := s.loadOrderResult(customer, input.ClientRequestID); findErr == nil {
			return existing, nil
		}
		return nil, err
	}

	openID, err := utils.DecryptAES(customer.OpenIDCiphertext)
	if err != nil || strings.TrimSpace(openID) == "" {
		s.failXiaohongshuOrder(&link, &order, "小程序用户身份解密失败")
		return nil, ErrMiniappUnauthenticated
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		s.failXiaohongshuOrder(&link, &order, "小红书渠道密钥不可用")
		return nil, ErrMiniappUnavailable
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	expiresAt := s.now().Add(DefaultOrderReservationTTL)
	response, err := newClient(account.AppID, secret, account.Environment).UpsertOrder(ctx, xiaohongshu.OrderUpsertRequest{
		ExternalOrderID: externalID, OpenID: openID, Path: miniappPathWithOrder(config.OrderPath, order.OrderNo),
		CreatedAt: s.now().Unix(), ExpiresAt: expiresAt.Unix(),
		Products: []xiaohongshu.OrderProduct{{ExternalProductID: mapping.ExternalCode, ExternalSKUID: config.ExternalSKUID, Count: input.Quantity, SalePrice: mapping.ChannelSaleCents, RealPrice: totalCents}},
		Price:    xiaohongshu.OrderPrice{OrderPrice: totalCents},
	})
	if err != nil {
		s.failXiaohongshuOrder(&link, &order, err.Error())
		return nil, err
	}
	if response.FinalPrice != totalCents || response.OpenPayType != "life_gpay" {
		s.failXiaohongshuOrder(&link, &order, "小红书返回的金额或支付类型不匹配")
		return nil, errors.New("小红书订单金额或支付类型校验失败")
	}
	if response.ExpiresAt > 0 {
		expiresAt = time.Unix(response.ExpiresAt, 0)
	}
	payTokenCiphertext, err := utils.EncryptAES(response.PayToken)
	if err != nil {
		s.failXiaohongshuOrder(&link, &order, "支付令牌加密失败")
		return nil, err
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Order{}).Where("id = ? AND tenant_id = ? AND status = ?", order.ID, customer.TenantID, "unpaid").Update("expires_at", expiresAt).Error; err != nil {
			return err
		}
		return tx.Model(&model.XiaohongshuOrderLink{}).Where("id = ? AND state = ?", link.ID, "creating").Updates(map[string]interface{}{
			"platform_order_id": response.OrderID, "pay_token_ciphertext": payTokenCiphertext,
			"pay_token_expires_at": expiresAt, "state": "unpaid", "last_error": "",
		}).Error
	}); err != nil {
		return nil, err
	}
	return &MiniappOrderResult{OrderNo: order.OrderNo, PlatformOrderID: response.OrderID, PayToken: response.PayToken, AmountCents: totalCents, Status: "unpaid", ExpiresAt: &expiresAt}, nil
}

func (s MiniappService) GetXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, orderNo string) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	var link model.XiaohongshuOrderLink
	var order model.Order
	if err := model.DB.Table("xiaohongshu_order_links AS link").Select("link.*").
		Joins("JOIN orders AS orders ON orders.id = link.order_id AND orders.tenant_id = link.tenant_id").
		Where("link.miniapp_customer_id = ? AND link.channel_account_id = ? AND orders.order_no = ?", customer.ID, customer.ChannelAccountID, strings.TrimSpace(orderNo)).
		First(&link).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", link.OrderID, customer.TenantID).First(&order).Error; err != nil {
		return nil, err
	}
	if link.State == "paid" || link.State == "cancelled" || link.State == "failed" {
		return s.orderResult(&link, &order, false)
	}
	return s.refreshXiaohongshuOrder(ctx, customer, &link, &order)
}

func (s MiniappService) refreshXiaohongshuOrder(ctx context.Context, customer *model.MiniappCustomer, link *model.XiaohongshuOrderLink, order *model.Order) (*MiniappOrderResult, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", link.ChannelAccountID, link.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	openID, err := utils.DecryptAES(customer.OpenIDCiphertext)
	if err != nil {
		return nil, ErrMiniappUnauthenticated
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return nil, ErrMiniappUnavailable
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	platform, err := newClient(account.AppID, secret, account.Environment).GetGuaranteeOrder(ctx, xiaohongshu.GuaranteeOrderRequest{ExternalOrderID: link.ExternalOrderID, OpenID: openID, OrderType: 1})
	if err != nil {
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": truncateChannelError(err.Error())}).Error
		})
		return nil, err
	}
	if link.PlatformOrderID != "" && platform.OrderID != "" && link.PlatformOrderID != platform.OrderID {
		return nil, errors.New("小红书订单编号不匹配")
	}
	switch platform.OrderStatus {
	case 6, 7:
		if platform.PayAmount != moneyCents(order.TotalAmount) {
			return nil, errors.New("小红书支付金额与本地订单不一致")
		}
		if err := s.completeXiaohongshuOrder(link, order, platform); err != nil {
			return nil, err
		}
		link.State = "paid"
		order.Status = "paid"
		return s.orderResult(link, order, false)
	case 71, 998:
		if order.Status == "unpaid" {
			if err := (&OrderService{}).Cancel(order.OrderNo, order.TenantID); err != nil {
				return nil, err
			}
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"state": "cancelled", "last_queried_at": s.now(), "last_error": ""}).Error
		})
		link.State = "cancelled"
		order.Status = "cancelled"
		return s.orderResult(link, order, false)
	default:
		if link.PayTokenExpiresAt != nil && !link.PayTokenExpiresAt.After(s.now()) && order.Status == "unpaid" {
			if err := (&OrderService{}).Cancel(order.OrderNo, order.TenantID); err != nil {
				return nil, err
			}
			_ = model.Write(func(tx *gorm.DB) error {
				return tx.Model(link).Updates(map[string]interface{}{"state": "cancelled", "last_queried_at": s.now(), "last_error": ""}).Error
			})
			link.State = "cancelled"
			order.Status = "cancelled"
			return s.orderResult(link, order, false)
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(link).Updates(map[string]interface{}{"last_queried_at": s.now(), "last_error": ""}).Error
		})
		return s.orderResult(link, order, true)
	}
}

func (s MiniappService) completeXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, platform *xiaohongshu.GuaranteeOrderResponse) error {
	return model.Write(func(tx *gorm.DB) error {
		var lockedLink model.XiaohongshuOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", link.ID, link.TenantID).First(&lockedLink).Error; err != nil {
			return err
		}
		var lockedOrder model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(&lockedOrder).Error; err != nil {
			return err
		}
		if lockedLink.State == "paid" && lockedOrder.Status == "paid" {
			return nil
		}
		amountCents := moneyCents(lockedOrder.TotalAmount)
		var payment model.Payment
		err := tx.Where("tenant_id = ? AND idempotency_key = ?", lockedOrder.TenantID, fmt.Sprintf("xiaohongshu:%d", lockedLink.ID)).First(&payment).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			now := s.now()
			payment = model.Payment{TenantID: lockedOrder.TenantID, PaymentNo: generatePaymentNo(), IdempotencyKey: fmt.Sprintf("xiaohongshu:%d", lockedLink.ID), OrderNo: lockedOrder.OrderNo, Amount: centsMoney(amountCents), AmountCents: amountCents, Method: "xiaohongshu", PayType: "life_gpay", Status: "paid", TransactionID: platform.TradeNo, PaidAt: &now}
			if err := tx.Create(&payment).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if payment.AmountCents != amountCents || payment.Status != "paid" || (payment.TransactionID != "" && platform.TradeNo != "" && payment.TransactionID != platform.TradeNo) {
			return errors.New("小红书支付流水与本地记录不一致")
		}
		if err := settleOrderIfFullyPaidTx(tx, &lockedOrder); err != nil {
			return err
		}
		voucherError := ""
		if len(platform.Vouchers) > 0 {
			var tickets []model.Ticket
			if err := tx.Where("order_id = ? AND tenant_id = ?", lockedOrder.ID, lockedOrder.TenantID).Order("id ASC").Find(&tickets).Error; err != nil {
				return err
			}
			if len(tickets) != len(platform.Vouchers) {
				voucherError = fmt.Sprintf("小红书券码数量 %d 与本地票数 %d 不一致", len(platform.Vouchers), len(tickets))
			} else {
				for index, voucher := range platform.Vouchers {
					ciphertext, err := utils.EncryptAES(voucher.Code)
					if err != nil {
						return err
					}
					row := model.XiaohongshuVoucherLink{TenantID: lockedOrder.TenantID, ChannelAccountID: lockedLink.ChannelAccountID, XiaohongshuOrderLinkID: lockedLink.ID, TicketID: tickets[index].ID, VoucherCodeHash: hashMiniappValue(voucher.Code), VoucherCodeCiphertext: ciphertext, Status: voucher.Status}
					if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticket_id"}}, DoUpdates: clause.AssignmentColumns([]string{"voucher_code_hash", "voucher_code_ciphertext", "status", "updated_at"})}).Create(&row).Error; err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&lockedLink).Updates(map[string]interface{}{"state": "paid", "trade_no": platform.TradeNo, "pay_channel": platform.PayChannel, "last_queried_at": s.now(), "last_error": voucherError}).Error
	})
}

func (s MiniappService) ProcessPendingXiaohongshuOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var links []model.XiaohongshuOrderLink
	if err := model.DB.Where("state IN ?", []string{"creating", "unpaid"}).Where("last_queried_at IS NULL OR last_queried_at < ?", now.Add(-20*time.Second)).Order("id ASC").Limit(limit).Find(&links).Error; err != nil {
		return 0, err
	}
	processed := 0
	for i := range links {
		if links[i].State == "creating" {
			if links[i].CreatedAt.Before(now.Add(-5 * time.Minute)) {
				var order model.Order
				if model.DB.Where("id = ? AND tenant_id = ?", links[i].OrderID, links[i].TenantID).First(&order).Error == nil && order.Status == "unpaid" {
					_ = (&OrderService{}).Cancel(order.OrderNo, order.TenantID)
				}
				_ = model.Write(func(tx *gorm.DB) error {
					return tx.Model(&links[i]).Updates(map[string]interface{}{"state": "failed", "last_error": "创建小红书订单超时"}).Error
				})
			}
			continue
		}
		var customer model.MiniappCustomer
		var order model.Order
		if model.DB.Where("id = ? AND tenant_id = ?", links[i].MiniappCustomerID, links[i].TenantID).First(&customer).Error != nil || model.DB.Where("id = ? AND tenant_id = ?", links[i].OrderID, links[i].TenantID).First(&order).Error != nil {
			continue
		}
		if _, err := s.refreshXiaohongshuOrder(ctx, &customer, &links[i], &order); err != nil {
			continue
		}
		processed++
	}
	return processed, nil
}

func (s MiniappService) loadOrderResult(customer *model.MiniappCustomer, requestID string) (*MiniappOrderResult, error) {
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("miniapp_customer_id = ? AND channel_account_id = ? AND client_request_id = ?", customer.ID, customer.ChannelAccountID, requestID).First(&link).Error; err != nil {
		return nil, err
	}
	var order model.Order
	if err := model.DB.Where("id = ? AND tenant_id = ?", link.OrderID, customer.TenantID).First(&order).Error; err != nil {
		return nil, err
	}
	return s.orderResult(&link, &order, link.State == "unpaid")
}

func (s MiniappService) orderResult(link *model.XiaohongshuOrderLink, order *model.Order, includePayToken bool) (*MiniappOrderResult, error) {
	result := &MiniappOrderResult{OrderNo: order.OrderNo, PlatformOrderID: link.PlatformOrderID, AmountCents: moneyCents(order.TotalAmount), Status: link.State, ExpiresAt: link.PayTokenExpiresAt}
	type presentationRow struct {
		ProductName string
		ImageURL    string
		Quantity    int
		PackageID   uint
	}
	var presentation presentationRow
	if err := model.DB.Table("order_items AS item").
		Select("item.product_name, item.quantity, COALESCE(xhs_config.image_url, '') AS image_url, COALESCE(hotel_package.id, 0) AS package_id").
		Joins("LEFT JOIN channel_product_mappings AS mapping ON mapping.channel_account_id = ? AND mapping.product_id = item.product_id AND mapping.deleted_at IS NULL", link.ChannelAccountID).
		Joins("LEFT JOIN xiaohongshu_product_configs AS xhs_config ON xhs_config.channel_product_mapping_id = mapping.id AND xhs_config.tenant_id = ? AND xhs_config.deleted_at IS NULL", link.TenantID).
		Joins("LEFT JOIN scenic_hotel_packages AS hotel_package ON hotel_package.product_id = item.product_id AND hotel_package.tenant_id = item.fulfillment_tenant_id AND hotel_package.deleted_at IS NULL").
		Where("item.order_id = ? AND item.deleted_at IS NULL", order.ID).
		Order("item.id ASC").Limit(1).Scan(&presentation).Error; err != nil {
		return nil, err
	}
	result.ProductName = presentation.ProductName
	result.ImageURL = presentation.ImageURL
	result.Quantity = presentation.Quantity
	result.ProductKind = "ticket"
	if presentation.PackageID != 0 {
		result.ProductKind = "scenic_hotel_package"
		var stay MiniappHotelStay
		if err := model.DB.Model(&model.HotelReservation{}).
			Select("MIN(hotel_name) AS hotel_name, MIN(room_type_name) AS room_type_name, MIN(rate_plan_name) AS rate_plan_name, MIN(check_in_date) AS check_in_date, MAX(check_out_date) AS check_out_date, SUM(rooms) AS rooms").
			Where("order_id = ? AND sales_tenant_id = ?", order.ID, order.TenantID).
			Scan(&stay).Error; err != nil {
			return nil, err
		}
		if stay.HotelName != "" {
			stay.GuestName, stay.ContactPhone = order.ContactName, order.ContactPhone
			result.HotelStay = &stay
		}
	}
	if includePayToken && link.PayTokenCiphertext != "" {
		payToken, err := utils.DecryptAES(link.PayTokenCiphertext)
		if err != nil {
			return nil, err
		}
		result.PayToken = payToken
	}
	if link.State == "paid" {
		if err := model.DB.Model(&model.Ticket{}).Where("order_id = ? AND tenant_id = ?", order.ID, order.TenantID).Order("id ASC").Pluck("ticket_code", &result.TicketCodes).Error; err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s MiniappService) failXiaohongshuOrder(link *model.XiaohongshuOrderLink, order *model.Order, message string) {
	_ = model.Write(func(tx *gorm.DB) error {
		return tx.Model(link).Updates(map[string]interface{}{"state": "failed", "last_error": truncateChannelError(message)}).Error
	})
	_ = (&OrderService{}).Cancel(order.OrderNo, order.TenantID)
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
