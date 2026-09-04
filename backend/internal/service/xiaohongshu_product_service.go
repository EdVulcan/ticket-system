package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type XiaohongshuProductService struct {
	NewClient func(appID, secret, environment string) *xiaohongshu.Client
	Now       func() time.Time
}

type XiaohongshuProductConfigInput struct {
	ExternalSKUID string   `json:"external_sku_id"`
	CategoryID    string   `json:"category_id"`
	POIIDs        []string `json:"poi_ids"`
	ImageURL      string   `json:"image_url"`
	Description   string   `json:"description"`
	ProductPath   string   `json:"product_path"`
	OrderPath     string   `json:"order_path"`
	ProductType   int      `json:"product_type"`
	SettleType    int      `json:"settle_type"`
}

type XiaohongshuProductConfigView struct {
	model.XiaohongshuProductConfig
	POIIDs []string `json:"poi_ids"`
}

type XiaohongshuDiagnosticCheck struct {
	Status       string `json:"status"` // passed, failed, skipped
	Message      string `json:"message"`
	Count        int    `json:"count,omitempty"`
	TradeCount   int    `json:"trade_count,omitempty"`
	PlatformCode int    `json:"platform_code,omitempty"`
}

type XiaohongshuDiagnostic struct {
	AccountID     uint                       `json:"account_id"`
	AppID         string                     `json:"app_id"`
	AccountStatus string                     `json:"account_status"`
	Environment   string                     `json:"environment"`
	Ready         bool                       `json:"ready"`
	Credentials   XiaohongshuDiagnosticCheck `json:"credentials"`
	Categories    XiaohongshuDiagnosticCheck `json:"categories"`
	POIs          XiaohongshuDiagnosticCheck `json:"pois"`
}

func NewXiaohongshuProductService() XiaohongshuProductService {
	return XiaohongshuProductService{NewClient: xiaohongshu.NewClient}
}

func (s XiaohongshuProductService) ListCategories(ctx context.Context, tenantID, accountID uint) ([]xiaohongshu.Category, error) {
	client, _, err := s.client(tenantID, accountID)
	if err != nil {
		return nil, err
	}
	return client.ListCategories(ctx)
}

func (s XiaohongshuProductService) ListPOIs(ctx context.Context, tenantID, accountID uint, page, pageSize int) (*xiaohongshu.POIListResponse, error) {
	client, _, err := s.client(tenantID, accountID)
	if err != nil {
		return nil, err
	}
	return client.ListPOIs(ctx, page, pageSize)
}

// Diagnose performs the minimum upstream checks needed before configuring a
// product. It deliberately reports a safe summary instead of raw provider
// responses or credentials.
func (s XiaohongshuProductService) Diagnose(ctx context.Context, tenantID, accountID uint) (*XiaohongshuDiagnostic, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ?", accountID, tenantID, "xiaohongshu").First(&account).Error; err != nil {
		return nil, err
	}
	diagnostic := &XiaohongshuDiagnostic{
		AccountID: account.ID, AppID: account.AppID, AccountStatus: account.Status,
		Environment: account.Environment,
		Credentials: XiaohongshuDiagnosticCheck{Status: "skipped", Message: "未检查"},
		Categories:  XiaohongshuDiagnosticCheck{Status: "skipped", Message: "凭据检查通过后再检查"},
		POIs:        XiaohongshuDiagnosticCheck{Status: "skipped", Message: "凭据检查通过后再检查"},
	}
	if account.Status != "active" && account.Status != "sandbox" {
		diagnostic.Credentials = XiaohongshuDiagnosticCheck{Status: "failed", Message: "渠道账号已停用"}
		return diagnostic, nil
	}
	client, _, err := s.client(tenantID, accountID)
	if err != nil {
		diagnostic.Credentials = XiaohongshuDiagnosticCheck{Status: "failed", Message: "AppID 或 AppSecret 不可用，请重新保存小红书参数"}
		return diagnostic, nil
	}
	if err := client.CheckCredentials(ctx); err != nil {
		diagnostic.Credentials = xiaohongshuDiagnosticCheckError(err, "小红书调用凭证获取失败，请检查 AppID、AppSecret 和运行环境")
		return diagnostic, nil
	}
	diagnostic.Credentials = XiaohongshuDiagnosticCheck{Status: "passed", Message: "调用凭证有效"}

	categories, categoryErr := client.ListCategories(ctx)
	if categoryErr != nil {
		diagnostic.Categories = xiaohongshuDiagnosticCheckError(categoryErr, "类目读取失败，请检查小红书交易能力和当前环境配置")
	} else {
		tradeCount := 0
		for _, category := range categories {
			if category.SupportTrade {
				tradeCount++
			}
		}
		diagnostic.Categories = XiaohongshuDiagnosticCheck{
			Status: "passed", Count: len(categories), TradeCount: tradeCount,
			Message: fmt.Sprintf("已读取 %d 个类目，其中 %d 个支持交易", len(categories), tradeCount),
		}
	}

	pois, poiErr := client.ListPOIs(ctx, 1, 100)
	if poiErr != nil {
		diagnostic.POIs = xiaohongshuDiagnosticCheckError(poiErr, "门店读取失败，请先在小红书后台认领或配置门店")
	} else {
		diagnostic.POIs = XiaohongshuDiagnosticCheck{
			Status: "passed", Count: len(pois.List),
			Message: fmt.Sprintf("已读取 %d 个可用门店", len(pois.List)),
		}
	}
	diagnostic.Ready = diagnostic.Credentials.Status == "passed" && diagnostic.Categories.Status == "passed" && diagnostic.Categories.TradeCount > 0 && diagnostic.POIs.Status == "passed" && diagnostic.POIs.Count > 0
	return diagnostic, nil
}

func xiaohongshuDiagnosticCheckError(err error, fallback string) XiaohongshuDiagnosticCheck {
	check := XiaohongshuDiagnosticCheck{Status: "failed", Message: fallback}
	var apiErr *xiaohongshu.APIError
	if !errors.As(err, &apiErr) {
		return check
	}
	check.PlatformCode = apiErr.Code
	switch apiErr.Code {
	case 420156:
		check.Message = "当前小程序尚未开通本地生活交易能力"
	case 12:
		check.Message = "小红书接口繁忙，请稍后重试"
	}
	return check
}

func (s XiaohongshuProductService) GetConfig(tenantID, accountID, mappingID uint) (*XiaohongshuProductConfigView, error) {
	var config model.XiaohongshuProductConfig
	if err := model.DB.Table("xiaohongshu_product_configs AS config").
		Joins("JOIN channel_accounts AS account ON account.id = config.channel_account_id").
		Joins("JOIN channel_product_mappings AS mapping ON mapping.id = config.channel_product_mapping_id AND mapping.channel_account_id = account.id").
		Where("config.channel_product_mapping_id = ? AND account.id = ? AND account.tenant_id = ? AND account.type = ?", mappingID, accountID, tenantID, "xiaohongshu").
		First(&config).Error; err != nil {
		return nil, err
	}
	return &XiaohongshuProductConfigView{XiaohongshuProductConfig: config, POIIDs: parseXiaohongshuPOIIDs(config.POIIDsJSON)}, nil
}

func (s XiaohongshuProductService) EnsureMappingAccess(tenantID, accountID, mappingID uint) error {
	return loadXiaohongshuMappingTx(model.DB, tenantID, accountID, mappingID, nil, nil)
}

func (s XiaohongshuProductService) SaveConfig(tenantID, accountID, mappingID, actorUserID uint, actorRole string, input XiaohongshuProductConfigInput) (*XiaohongshuProductConfigView, error) {
	normalizeXiaohongshuProductInput(&input)
	poiJSON, _ := json.Marshal(input.POIIDs)
	var stored model.XiaohongshuProductConfig
	err := model.Write(func(tx *gorm.DB) error {
		if err := loadXiaohongshuMappingTx(tx, tenantID, accountID, mappingID, nil, nil); err != nil {
			return err
		}
		var mapping model.ChannelProductMapping
		if err := tx.Where("id = ? AND channel_account_id = ?", mappingID, accountID).First(&mapping).Error; err != nil {
			return err
		}
		var hotelProduct model.HotelProduct
		hotelProductErr := tx.Where("tenant_id = ? AND product_id = ?", tenantID, mapping.ProductID).First(&hotelProduct).Error
		if hotelProductErr != nil && !errors.Is(hotelProductErr, gorm.ErrRecordNotFound) {
			return hotelProductErr
		}
		if hotelProductErr == nil {
			expectedType := xiaohongshu.ProductTypePresaleVoucher
			if hotelProduct.SaleMode == "calendar_room" {
				expectedType = xiaohongshu.ProductTypeCalendar
			}
			if input.ProductType != 0 && input.ProductType != expectedType {
				return fmt.Errorf("hotel product sale mode requires Xiaohongshu product type %d", expectedType)
			}
			input.ProductType = expectedType
		}
		if err := validateXiaohongshuProductInput(input); err != nil {
			return err
		}
		var hotelPackage model.ScenicHotelPackage
		packageErr := tx.Where("tenant_id = ? AND product_id = ?", tenantID, mapping.ProductID).First(&hotelPackage).Error
		if packageErr == nil && hotelPackage.BookingMode == "after_purchase" && input.ProductType != xiaohongshu.ProductTypePresaleVoucher {
			return errors.New("book-after-purchase hotel packages must be published as a Xiaohongshu presale voucher")
		}
		if packageErr != nil && !errors.Is(packageErr, gorm.ErrRecordNotFound) {
			return packageErr
		}
		config := model.XiaohongshuProductConfig{
			TenantID: tenantID, ChannelAccountID: accountID, ChannelProductMappingID: mappingID,
			ExternalSKUID: input.ExternalSKUID, CategoryID: input.CategoryID, POIIDsJSON: string(poiJSON),
			ImageURL: input.ImageURL, Description: input.Description, ProductPath: input.ProductPath,
			OrderPath: input.OrderPath, ProductType: input.ProductType, SettleType: input.SettleType,
			SyncStatus: "pending", AuditStatus: "pending", AuditMessage: "", AuditedAt: nil,
			LastSyncError: "", LastSyncedAt: nil,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_product_mapping_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"external_sku_id", "category_id", "poi_ids_json", "image_url", "description", "product_path", "order_path", "product_type", "settle_type", "sync_status", "audit_status", "audit_message", "audited_at", "last_sync_error", "last_synced_at", "updated_at"}),
		}).Create(&config).Error; err != nil {
			return err
		}
		if err := tx.Where("channel_product_mapping_id = ?", mappingID).First(&stored).Error; err != nil {
			return err
		}
		after, _ := json.Marshal(input)
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "xiaohongshu.product.configure", "channel_product_mapping", mappingID, "配置小红书商品发布参数", "", string(after))
	})
	if err != nil {
		return nil, err
	}
	return &XiaohongshuProductConfigView{XiaohongshuProductConfig: stored, POIIDs: input.POIIDs}, nil
}

func (s XiaohongshuProductService) Sync(ctx context.Context, tenantID, accountID, mappingID, actorUserID uint, actorRole string) error {
	client, account, err := s.client(tenantID, accountID)
	if err != nil {
		return err
	}
	var mapping model.ChannelProductMapping
	var product model.Product
	if err := loadXiaohongshuMappingTx(model.DB, tenantID, accountID, mappingID, &mapping, &product); err != nil {
		return err
	}
	if product.ProductKind == "hotel" {
		// Independent hotel orders have no verified Xiaohongshu accommodation
		// protocol yet. Do not publish a remote product that the local order and
		// catalog paths intentionally refuse to sell.
		return errors.New("Xiaohongshu hotel product publishing is not enabled until the accommodation protocol is verified")
	} else if err := requireActiveScenicSupplier(model.DB, productFulfillmentTenantID(&product)); err != nil {
		return errors.New("scenic supplier business is unavailable")
	}
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ? AND channel_product_mapping_id = ?", tenantID, accountID, mappingID).First(&config).Error; err != nil {
		return errors.New("请先完成小红书商品发布配置")
	}
	// Any local change that is about to be published invalidates the previous
	// approval until Xiaohongshu explicitly reviews this version again.
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.XiaohongshuProductConfig{}).Where("id = ? AND tenant_id = ?", config.ID, tenantID).
			Updates(map[string]interface{}{"sync_status": "pending", "audit_status": "pending", "audit_message": "", "audited_at": nil, "last_sync_error": "", "last_synced_at": nil}).Error
	}); err != nil {
		return err
	}
	name := strings.TrimSpace(mapping.DisplayName)
	if name == "" {
		name = product.Name
	}
	originPrice := int64(math.Round(product.Price * 100))
	salePrice := mapping.ChannelSaleCents
	if salePrice <= 0 {
		return errors.New("Xiaohongshu product price is not configured")
	}
	if originPrice < salePrice {
		originPrice = salePrice
	}
	now := s.now()
	request := xiaohongshu.LocalLifeProductRequest{
		ExternalProductID: mapping.ExternalCode, Name: name, ShortTitle: name,
		Description: config.Description, Path: config.ProductPath, TopImage: config.ImageURL,
		CategoryID: config.CategoryID, CreatedAt: product.CreatedAt.Unix(), UpdatedAt: now.Unix(),
		POIIDs: parseXiaohongshuPOIIDs(config.POIIDsJSON), ProductType: config.ProductType, SettleType: config.SettleType,
		SKUs: []xiaohongshu.ProductSKU{{ExternalSKUID: config.ExternalSKUID, Name: name, Image: config.ImageURL, OriginPrice: originPrice, SalePrice: salePrice, Status: 1}},
	}
	err = client.UpsertLocalLifeProduct(ctx, request)
	if err != nil {
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(&model.XiaohongshuProductConfig{}).Where("id = ? AND tenant_id = ?", config.ID, tenantID).
				Updates(map[string]interface{}{"sync_status": "failed", "audit_status": "pending", "last_sync_error": truncateChannelError(err.Error())}).Error
		})
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.XiaohongshuProductConfig{}).Where("id = ? AND tenant_id = ?", config.ID, tenantID).
			Updates(map[string]interface{}{"sync_status": "submitted", "audit_status": "pending", "audit_message": "", "audited_at": nil, "last_sync_error": "", "last_synced_at": now}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "xiaohongshu.product.sync", "channel_product_mapping", mappingID, "同步小红书商品", "", account.Environment)
	})
}

func (s XiaohongshuProductService) client(tenantID, accountID uint) (*xiaohongshu.Client, *model.ChannelAccount, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, nil, errors.New("小红书渠道账号不可用")
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		return nil, nil, errors.New("小红书渠道密钥不可用")
	}
	newClient := s.NewClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	return newClient(account.AppID, secret, account.Environment), &account, nil
}

func loadXiaohongshuMappingTx(tx *gorm.DB, tenantID, accountID, mappingID uint, mapping *model.ChannelProductMapping, product *model.Product) error {
	var account model.ChannelAccount
	if err := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return errors.New("小红书渠道账号不可用")
	}
	var current model.ChannelProductMapping
	if err := tx.Where("id = ? AND channel_account_id = ?", mappingID, accountID).First(&current).Error; err != nil {
		return errors.New("小红书商品映射不存在")
	}
	var currentProduct model.Product
	if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", current.ProductID, tenantID, "online").First(&currentProduct).Error; err != nil {
		return errors.New("映射的本地产品不可售")
	}
	if current.Status != "active" || (current.ChannelSaleCents <= 0 && currentProduct.ProductKind != "hotel") {
		return errors.New("小红书商品映射未启用或售价无效")
	}
	if mapping != nil {
		*mapping = current
	}
	if product != nil {
		*product = currentProduct
	}
	return nil
}

func normalizeXiaohongshuProductInput(input *XiaohongshuProductConfigInput) {
	input.ExternalSKUID = strings.TrimSpace(input.ExternalSKUID)
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.ImageURL = strings.TrimSpace(input.ImageURL)
	input.Description = strings.TrimSpace(input.Description)
	input.ProductPath = strings.TrimSpace(input.ProductPath)
	input.OrderPath = strings.TrimSpace(input.OrderPath)
	seen := make(map[string]struct{})
	pois := make([]string, 0, len(input.POIIDs))
	for _, id := range input.POIIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				pois = append(pois, id)
			}
		}
	}
	input.POIIDs = pois
}

func validateXiaohongshuProductInput(input XiaohongshuProductConfigInput) error {
	image, err := url.Parse(input.ImageURL)
	if input.ExternalSKUID == "" || input.CategoryID == "" || input.Description == "" || err != nil || image.Scheme != "https" || image.Host == "" {
		return errors.New("请完整填写 SKU、类目、说明和 HTTPS 商品图片")
	}
	if !strings.HasPrefix(input.ProductPath, "/") || !strings.HasPrefix(input.OrderPath, "/") {
		return errors.New("小程序商品页和订单页路径必须以 / 开头")
	}
	if input.ProductType < xiaohongshu.ProductTypeGroupVoucher || input.ProductType > xiaohongshu.ProductTypeCalendar {
		return errors.New("小红书商品类型无效")
	}
	if input.SettleType < xiaohongshu.SettleAtHeadOffice || input.SettleType > xiaohongshu.SettleByRegion {
		return errors.New("小红书结算方式无效")
	}
	return nil
}

func parseXiaohongshuPOIIDs(raw string) []string {
	var result []string
	if json.Unmarshal([]byte(raw), &result) != nil {
		return []string{}
	}
	return result
}

func truncateChannelError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}

func (s XiaohongshuProductService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
