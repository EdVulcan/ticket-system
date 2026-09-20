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
	"regexp"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCommerceStorefrontUnavailable     = errors.New("commerce storefront is unavailable")
	ErrCommerceStorefrontUnauthenticated = errors.New("commerce storefront session is invalid or expired")
	ErrCommerceStorefrontInvalid         = errors.New("commerce storefront request is invalid")
	ErrCommerceStorefrontOwnership       = errors.New("commerce storefront resource is not owned by the customer")
	ErrCommerceStorefrontAddressInvalid  = errors.New("commerce storefront address is invalid")
)

// WechatMiniappLoginRequest is the only credential material passed to the
// injectable login adapter. The default adapter is intentionally unavailable;
// production code must provide an adapter that authenticates the provider
// response before returning an identity.
type WechatMiniappLoginRequest struct {
	AppID       string
	AppSecret   string
	Code        string
	Environment string
}

// WechatMiniappLoginIdentity contains provider-authenticated subject data.
// OpenID is preferred, while Subject permits adapters backed by a provider
// that names the same stable value differently.
type WechatMiniappLoginIdentity struct {
	Subject string
	OpenID  string
}

type WechatMiniappLoginAdapter interface {
	ExchangeCode(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error)
}

type WechatMiniappLoginFunc func(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error)

func (f WechatMiniappLoginFunc) ExchangeCode(ctx context.Context, request WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
	return f(ctx, request)
}

// Aliases make the boundary vocabulary explicit for callers that use the
// shorter WeChat naming without introducing another adapter contract.
type WechatLoginAdapter = WechatMiniappLoginAdapter
type WechatLoginIdentity = WechatMiniappLoginIdentity

type unavailableWechatMiniappLoginAdapter struct{}

func (unavailableWechatMiniappLoginAdapter) ExchangeCode(context.Context, WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
	return WechatMiniappLoginIdentity{}, ErrCommerceStorefrontUnavailable
}

type CommerceStorefrontLoginInput struct {
	AppID string `json:"app_id"`
	Code  string `json:"code"`
}

type CommerceStorefrontLoginResult struct {
	Token            string    `json:"token"`
	ExpiresAt        time.Time `json:"expires_at"`
	TenantID         uint      `json:"tenant_id"`
	ChannelAccountID uint      `json:"channel_account_id"`
	BusinessType     string    `json:"business_type"`
	LocationID       uint      `json:"location_id"`
}

type CommerceStorefrontCatalog struct {
	TenantID         uint                              `json:"tenant_id"`
	ChannelAccountID uint                              `json:"channel_account_id"`
	BusinessType     string                            `json:"business_type"`
	Location         model.CommerceFulfillmentLocation `json:"location"`
	Products         []model.CommerceProduct           `json:"products"`
}

type CommerceStorefrontCartItemInput struct {
	ProductID uint   `json:"product_id"`
	SKUID     uint   `json:"sku_id"`
	Quantity  int    `json:"quantity"`
	OptionIDs []uint `json:"option_ids"`
}

type CommerceStorefrontCheckoutInput struct {
	IdempotencyKey    string `json:"idempotency_key"`
	ContactName       string `json:"contact_name"`
	ContactPhone      string `json:"contact_phone"`
	AddressID         uint   `json:"address_id,omitempty"`
	ShippingAddress   string `json:"shipping_address,omitempty"`
	FulfillmentMethod string `json:"fulfillment_method,omitempty"`
}

// CommerceStorefrontAddressInput contains only editable customer address
// fields. Tenant and customer ownership are always derived from the bearer
// session and never accepted from this payload.
type CommerceStorefrontAddressInput struct {
	ID            uint   `json:"id,omitempty"`
	AddressType   string `json:"address_type"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province,omitempty"`
	City          string `json:"city,omitempty"`
	District      string `json:"district,omitempty"`
	CampusName    string `json:"campus_name,omitempty"`
	ZoneName      string `json:"zone_name,omitempty"`
	Building      string `json:"building,omitempty"`
	Room          string `json:"room,omitempty"`
	Detail        string `json:"detail"`
	IsDefault     bool   `json:"is_default"`
}

// CommerceStorefrontAddressView deliberately omits tenant/customer identity
// hashes from the public response while retaining the exact checkout snapshot
// fields the client needs.
type CommerceStorefrontAddressView struct {
	ID            uint   `json:"id"`
	AddressType   string `json:"address_type"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province,omitempty"`
	City          string `json:"city,omitempty"`
	District      string `json:"district,omitempty"`
	CampusName    string `json:"campus_name,omitempty"`
	ZoneName      string `json:"zone_name,omitempty"`
	Building      string `json:"building,omitempty"`
	Room          string `json:"room,omitempty"`
	Detail        string `json:"detail"`
	IsDefault     bool   `json:"is_default"`
}

type CommerceStorefrontRefundInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}

type CommerceStorefrontOrderPage struct {
	Data     []model.CommerceOrder `json:"data"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

// CommerceStorefrontService is the public customer boundary for the
// independent restaurant/retail domain. Tenant, channel, customer, business
// and fulfillment-location facts are resolved from the session and binding;
// callers cannot supply them as authority.
type CommerceStorefrontService struct {
	DB           *gorm.DB
	Catalog      CommerceCatalogService
	Operations   CommerceOperationsService
	Orders       CommerceOrderService
	LoginAdapter WechatMiniappLoginAdapter
	// WechatLoginAdapter is an alias field for dependency injection in callers
	// that use the shorter name. LoginAdapter takes precedence when both exist.
	WechatLoginAdapter WechatMiniappLoginAdapter
	Now                func() time.Time
	SessionTTL         time.Duration
}

type commerceStorefrontContext struct {
	Session    model.CommerceCustomerSession
	Account    model.ChannelAccount
	Binding    model.CommerceStorefrontBinding
	Location   model.CommerceFulfillmentLocation
	CustomerID string
}

func (s *CommerceStorefrontService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommerceStorefrontService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *CommerceStorefrontService) sessionTTL() time.Duration {
	if s != nil && s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return 7 * 24 * time.Hour
}

func (s *CommerceStorefrontService) catalogService() *CommerceCatalogService {
	service := s.Catalog
	if service.DB == nil {
		service.DB = s.db()
	}
	return &service
}

func (s *CommerceStorefrontService) operationsService() *CommerceOperationsService {
	service := s.Operations
	if service.DB == nil {
		service.DB = s.db()
	}
	if service.Clock == nil {
		service.Clock = s.Now
	}
	return &service
}

func (s *CommerceStorefrontService) ordersService() *CommerceOrderService {
	service := s.Orders
	if service.DB == nil {
		service.DB = s.db()
	}
	if service.Clock == nil {
		service.Clock = s.Now
	}
	return &service
}

func storefrontHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func storefrontCustomerID(accountID uint, subjectHash string) string {
	return storefrontHash(fmt.Sprintf("wechat-miniapp:%d:%s", accountID, subjectHash))
}

func randomStorefrontToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *CommerceStorefrontService) loginAdapter() WechatMiniappLoginAdapter {
	if s != nil && s.LoginAdapter != nil {
		return s.LoginAdapter
	}
	if s != nil && s.WechatLoginAdapter != nil {
		return s.WechatLoginAdapter
	}
	return unavailableWechatMiniappLoginAdapter{}
}

func (s *CommerceStorefrontService) loadActiveWechatAccount(appID string) (*model.ChannelAccount, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, fmt.Errorf("%w: app id is required", ErrCommerceStorefrontInvalid)
	}
	// A sandbox channel is still a deliberately published storefront account:
	// WeChat test mini programs use the same jscode2session endpoint and need to
	// exercise the complete SaaS session/binding path. Disabled accounts remain
	// excluded, and the binding/capability/location checks below still apply.
	query := s.db().Where("type = ? AND app_id = ? AND status IN ?", "wechat_miniapp", appID, []string{"active", "sandbox"})
	var count int64
	if err := query.Model(&model.ChannelAccount{}).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("%w: channel account lookup failed: %v", ErrCommerceStorefrontUnavailable, err)
	}
	if count != 1 {
		return nil, fmt.Errorf("%w: expected one enabled wechat channel account for app id, found %d", ErrCommerceStorefrontUnavailable, count)
	}
	var account model.ChannelAccount
	if err := query.First(&account).Error; err != nil {
		return nil, fmt.Errorf("%w: channel account load failed: %v", ErrCommerceStorefrontUnavailable, err)
	}
	return &account, nil
}

func (s *CommerceStorefrontService) loadBinding(tx *gorm.DB, account *model.ChannelAccount) (*model.CommerceStorefrontBinding, *model.CommerceFulfillmentLocation, error) {
	if account == nil || account.ID == 0 || account.TenantID == 0 {
		return nil, nil, fmt.Errorf("%w: storefront account identity is incomplete", ErrCommerceStorefrontUnavailable)
	}
	var tenant model.Tenant
	if err := tx.Select("id", "status").Where("id = ? AND status = ?", account.TenantID, "active").First(&tenant).Error; err != nil {
		return nil, nil, fmt.Errorf("%w: tenant is not active: %v", ErrCommerceStorefrontUnavailable, err)
	}
	var binding model.CommerceStorefrontBinding
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND status = ?", account.TenantID, account.ID, "active").First(&binding).Error; err != nil {
		return nil, nil, fmt.Errorf("%w: active storefront binding is missing: %v", ErrCommerceStorefrontUnavailable, err)
	}
	if err := RequireActiveTenantBusinessCapability(tx, account.TenantID, binding.BusinessType); err != nil {
		return nil, nil, err
	}
	var location model.CommerceFulfillmentLocation
	if err := tx.Where("id = ? AND tenant_id = ? AND business_type = ? AND status = ?", binding.LocationID, account.TenantID, binding.BusinessType, "active").First(&location).Error; err != nil {
		return nil, nil, fmt.Errorf("%w: active fulfillment location is missing: %v", ErrCommerceStorefrontUnavailable, err)
	}
	return &binding, &location, nil
}

func (s *CommerceStorefrontService) Login(ctx context.Context, input CommerceStorefrontLoginInput) (*CommerceStorefrontLoginResult, error) {
	input.AppID = strings.TrimSpace(input.AppID)
	input.Code = strings.TrimSpace(input.Code)
	if input.AppID == "" || input.Code == "" {
		return nil, fmt.Errorf("%w: app id and code are required", ErrCommerceStorefrontInvalid)
	}
	account, err := s.loadActiveWechatAccount(input.AppID)
	if err != nil {
		return nil, err
	}
	secret := ""
	if strings.TrimSpace(account.SecretCiphertext) != "" {
		secret, err = utils.DecryptAES(account.SecretCiphertext)
		if err != nil || strings.TrimSpace(secret) == "" {
			if err == nil {
				err = errors.New("decrypted app secret is empty")
			}
			return nil, fmt.Errorf("%w: app secret decryption failed: %v", ErrCommerceStorefrontUnavailable, err)
		}
	}
	identity, err := s.loginAdapter().ExchangeCode(ctx, WechatMiniappLoginRequest{
		AppID: account.AppID, AppSecret: secret, Code: input.Code, Environment: account.Environment,
	})
	if err != nil {
		if errors.Is(err, ErrCommerceStorefrontUnavailable) {
			return nil, err
		}
		// Keep the provider's safe error text in the server-side error chain so
		// the API layer can log the actual rejection/transport cause. The
		// controller still maps this to the generic 503 response for callers.
		return nil, fmt.Errorf("%w: provider login failed: %v", ErrCommerceStorefrontUnavailable, err)
	}
	subject := strings.TrimSpace(identity.Subject)
	if subject == "" {
		subject = strings.TrimSpace(identity.OpenID)
	}
	if subject == "" {
		return nil, fmt.Errorf("%w: provider identity is missing", ErrCommerceStorefrontUnavailable)
	}
	subjectHash := storefrontHash(subject)
	token, err := randomStorefrontToken()
	if err != nil {
		return nil, err
	}
	tokenHash := storefrontHash(token)
	now := s.now()
	expiresAt := now.Add(s.sessionTTL())

	var binding *model.CommerceStorefrontBinding
	var location *model.CommerceFulfillmentLocation
	err = s.db().Transaction(func(tx *gorm.DB) error {
		var current model.ChannelAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ? AND app_id = ? AND status IN ?", account.ID, account.TenantID, "wechat_miniapp", account.AppID, []string{"active", "sandbox"}).First(&current).Error; err != nil {
			return fmt.Errorf("%w: channel account changed during login: %v", ErrCommerceStorefrontUnavailable, err)
		}
		var loadErr error
		binding, location, loadErr = s.loadBinding(tx, &current)
		if loadErr != nil {
			return loadErr
		}
		if err := tx.Model(&model.CommerceCustomerSession{}).
			Where("tenant_id = ? AND channel_account_id = ? AND subject_hash = ? AND status = ?", current.TenantID, current.ID, subjectHash, "active").
			Updates(map[string]interface{}{"status": "revoked", "revoked_at": now}).Error; err != nil {
			return err
		}
		session := model.CommerceCustomerSession{
			TenantID: current.TenantID, ChannelAccountID: current.ID, SubjectHash: subjectHash,
			TokenHash: tokenHash, ExpiresAt: expiresAt, Status: "active", LastSeenAt: &now,
		}
		return tx.Create(&session).Error
	})
	if err != nil {
		return nil, err
	}
	return &CommerceStorefrontLoginResult{
		Token: token, ExpiresAt: expiresAt, TenantID: account.TenantID, ChannelAccountID: account.ID,
		BusinessType: binding.BusinessType, LocationID: location.ID,
	}, nil
}

// LoginWithCode is a concise alias for adapters and tests that use the
// provider terminology directly.
func (s *CommerceStorefrontService) LoginWithCode(ctx context.Context, appID, code string) (*CommerceStorefrontLoginResult, error) {
	return s.Login(ctx, CommerceStorefrontLoginInput{AppID: appID, Code: code})
}

func (s *CommerceStorefrontService) authenticate(token string) (*commerceStorefrontContext, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	now := s.now()
	var session model.CommerceCustomerSession
	if err := s.db().Where("token_hash = ?", storefrontHash(token)).First(&session).Error; err != nil {
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	if session.Status != "active" {
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	if !session.ExpiresAt.After(now) {
		_ = s.db().Model(&session).Where("status = ?", "active").Updates(map[string]interface{}{"status": "expired"})
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	var account model.ChannelAccount
	if err := s.db().Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", session.ChannelAccountID, session.TenantID, "wechat_miniapp", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrCommerceStorefrontUnavailable
	}
	binding, location, err := s.loadBinding(s.db(), &account)
	if err != nil {
		return nil, err
	}
	if err := s.db().Model(&session).Where("status = ? AND expires_at > ?", "active", now).Update("last_seen_at", now).Error; err != nil {
		return nil, err
	}
	session.LastSeenAt = &now
	return &commerceStorefrontContext{
		Session: session, Account: account, Binding: *binding, Location: *location,
		CustomerID: storefrontCustomerID(session.ChannelAccountID, session.SubjectHash),
	}, nil
}

// Authenticate validates the opaque bearer token and returns only the bound
// session record. Account, tenant, capability and binding checks happen before
// the session is accepted.
func (s *CommerceStorefrontService) Authenticate(token string) (*model.CommerceCustomerSession, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	return &context.Session, nil
}

func (s *CommerceStorefrontService) GetSession(token string) (*model.CommerceCustomerSession, error) {
	return s.Authenticate(token)
}

func (s *CommerceStorefrontService) ListCatalog(token string) (*CommerceStorefrontCatalog, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	products, err := s.catalogService().ListProducts(context.Session.TenantID, context.Binding.BusinessType, "online", "")
	if err != nil {
		return nil, err
	}
	now := s.now()
	filtered := make([]model.CommerceProduct, 0, len(products))
	for _, product := range products {
		if product.SaleStartsAt != nil && product.SaleStartsAt.After(now) {
			continue
		}
		if product.SaleEndsAt != nil && !now.Before(*product.SaleEndsAt) {
			continue
		}
		activeSKUs := product.SKUs[:0]
		for _, sku := range product.SKUs {
			if sku.Status == "active" {
				activeSKUs = append(activeSKUs, sku)
			}
		}
		if len(activeSKUs) == 0 {
			continue
		}
		product.SKUs = activeSKUs
		filtered = append(filtered, product)
	}
	if filtered == nil {
		filtered = []model.CommerceProduct{}
	}
	return &CommerceStorefrontCatalog{
		TenantID: context.Session.TenantID, ChannelAccountID: context.Session.ChannelAccountID,
		BusinessType: context.Binding.BusinessType, Location: context.Location, Products: filtered,
	}, nil
}

func (s *CommerceStorefrontService) RevokeSession(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrCommerceStorefrontUnauthenticated
	}
	now := s.now()
	result := s.db().Model(&model.CommerceCustomerSession{}).Where("token_hash = ? AND status = ?", storefrontHash(token), "active").Updates(map[string]interface{}{"status": "revoked", "revoked_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCommerceStorefrontUnauthenticated
	}
	return nil
}

func (s *CommerceStorefrontService) currentCart(context *commerceStorefrontContext) (*model.CommerceCart, error) {
	if context == nil {
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	operations := s.operationsService()
	return operations.GetOrCreateCart(context.Session.TenantID, CommerceCartInput{
		BusinessType: context.Binding.BusinessType, CustomerID: context.CustomerID,
		ChannelAccountID: context.Session.ChannelAccountID, LocationID: context.Binding.LocationID,
	})
}

var storefrontPhonePattern = regexp.MustCompile(`^[0-9+() -]{6,30}$`)

func normalizeStorefrontAddressInput(input CommerceStorefrontAddressInput) (CommerceStorefrontAddressInput, error) {
	input.AddressType = strings.ToUpper(strings.TrimSpace(input.AddressType))
	if input.AddressType == "" {
		input.AddressType = "SHIPPING"
	}
	if input.AddressType != "CAMPUS" && input.AddressType != "SHIPPING" {
		return input, fmt.Errorf("%w: unsupported address type", ErrCommerceStorefrontAddressInvalid)
	}
	input.RecipientName = strings.TrimSpace(input.RecipientName)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Province = strings.TrimSpace(input.Province)
	input.City = strings.TrimSpace(input.City)
	input.District = strings.TrimSpace(input.District)
	input.CampusName = strings.TrimSpace(input.CampusName)
	input.ZoneName = strings.TrimSpace(input.ZoneName)
	input.Building = strings.TrimSpace(input.Building)
	input.Room = strings.TrimSpace(input.Room)
	input.Detail = strings.TrimSpace(input.Detail)
	if input.RecipientName == "" || len([]rune(input.RecipientName)) > 80 || !storefrontPhonePattern.MatchString(input.Phone) ||
		len([]rune(input.Phone)) > 30 || len([]rune(input.Province)) > 40 || len([]rune(input.City)) > 40 ||
		len([]rune(input.District)) > 40 || len([]rune(input.CampusName)) > 80 || len([]rune(input.ZoneName)) > 80 ||
		len([]rune(input.Building)) > 80 || len([]rune(input.Room)) > 80 || len([]rune(input.Detail)) > 255 {
		return input, fmt.Errorf("%w: address fields are invalid", ErrCommerceStorefrontAddressInvalid)
	}
	if input.Detail == "" {
		return input, fmt.Errorf("%w: address detail is required", ErrCommerceStorefrontAddressInvalid)
	}
	if input.AddressType == "SHIPPING" && (input.Province == "" || input.City == "") {
		return input, fmt.Errorf("%w: shipping province and city are required", ErrCommerceStorefrontAddressInvalid)
	}
	if input.AddressType == "CAMPUS" && (input.CampusName == "" || input.ZoneName == "" || input.Building == "" || input.Room == "") {
		return input, fmt.Errorf("%w: campus address fields are required", ErrCommerceStorefrontAddressInvalid)
	}
	return input, nil
}

func storefrontAddressView(address *model.CommerceAddress) *CommerceStorefrontAddressView {
	if address == nil {
		return nil
	}
	return &CommerceStorefrontAddressView{
		ID: address.ID, AddressType: address.AddressType, RecipientName: address.RecipientName,
		Phone: address.Phone, Province: address.Province, City: address.City, District: address.District,
		CampusName: address.CampusName, ZoneName: address.ZoneName, Building: address.Building,
		Room: address.Room, Detail: address.Detail, IsDefault: address.IsDefault,
	}
}

func storefrontAddressSnapshot(address *model.CommerceAddress) (string, error) {
	view := storefrontAddressView(address)
	if view == nil {
		return "", fmt.Errorf("%w: address is required", ErrCommerceStorefrontAddressInvalid)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return "", fmt.Errorf("%w: address snapshot could not be encoded", ErrCommerceStorefrontAddressInvalid)
	}
	return string(encoded), nil
}

func storefrontAddressModel(input CommerceStorefrontAddressInput, tenantID uint, customerID string) model.CommerceAddress {
	return model.CommerceAddress{
		TenantID: tenantID, CustomerID: customerID, AddressType: input.AddressType,
		RecipientName: input.RecipientName, Phone: input.Phone, Province: input.Province,
		City: input.City, District: input.District, CampusName: input.CampusName,
		ZoneName: input.ZoneName, Building: input.Building, Room: input.Room,
		Detail: input.Detail, IsDefault: input.IsDefault,
	}
}

// ListAddresses returns only addresses owned by the current storefront
// customer. The tenant and customer hash never cross this boundary.
func (s *CommerceStorefrontService) ListAddresses(token string) ([]CommerceStorefrontAddressView, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	var rows []model.CommerceAddress
	if err := s.db().Where("tenant_id = ? AND customer_id = ?", context.Session.TenantID, context.CustomerID).
		Order("is_default DESC, created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]CommerceStorefrontAddressView, 0, len(rows))
	for index := range rows {
		if view := storefrontAddressView(&rows[index]); view != nil {
			result = append(result, *view)
		}
	}
	return result, nil
}

func (s *CommerceStorefrontService) saveAddress(token string, raw CommerceStorefrontAddressInput) (*CommerceStorefrontAddressView, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	input, err := normalizeStorefrontAddressInput(raw)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceAddress
	err = s.db().Transaction(func(tx *gorm.DB) error {
		var address model.CommerceAddress
		if input.ID != 0 {
			if err := tx.Where("id = ? AND tenant_id = ? AND customer_id = ?", input.ID, context.Session.TenantID, context.CustomerID).First(&address).Error; err != nil {
				return err
			}
			updates := storefrontAddressModel(input, context.Session.TenantID, context.CustomerID)
			if err := tx.Model(&address).Updates(map[string]interface{}{
				"address_type": updates.AddressType, "recipient_name": updates.RecipientName, "phone": updates.Phone,
				"province": updates.Province, "city": updates.City, "district": updates.District,
				"campus_name": updates.CampusName, "zone_name": updates.ZoneName, "building": updates.Building,
				"room": updates.Room, "detail": updates.Detail, "is_default": updates.IsDefault,
			}).Error; err != nil {
				return err
			}
			address = updates
			address.ID = input.ID
		} else {
			address = storefrontAddressModel(input, context.Session.TenantID, context.CustomerID)
			if err := tx.Create(&address).Error; err != nil {
				return err
			}
		}
		if address.IsDefault {
			if err := tx.Model(&model.CommerceAddress{}).
				Where("tenant_id = ? AND customer_id = ? AND address_type = ? AND id <> ?", context.Session.TenantID, context.CustomerID, address.AddressType, address.ID).
				Update("is_default", false).Error; err != nil {
				return err
			}
		} else {
			var count int64
			if err := tx.Model(&model.CommerceAddress{}).Where("tenant_id = ? AND customer_id = ? AND address_type = ?", context.Session.TenantID, context.CustomerID, address.AddressType).Count(&count).Error; err != nil {
				return err
			}
			if count == 1 {
				if err := tx.Model(&address).Update("is_default", true).Error; err != nil {
					return err
				}
				address.IsDefault = true
			}
		}
		result = &address
		return nil
	})
	if err != nil {
		return nil, err
	}
	return storefrontAddressView(result), nil
}

func (s *CommerceStorefrontService) SaveAddress(token string, input CommerceStorefrontAddressInput) (*CommerceStorefrontAddressView, error) {
	return s.saveAddress(token, input)
}

func (s *CommerceStorefrontService) DeleteAddress(token string, addressID uint) error {
	context, err := s.authenticate(token)
	if err != nil {
		return err
	}
	if addressID == 0 {
		return fmt.Errorf("%w: address id is required", ErrCommerceStorefrontAddressInvalid)
	}
	result := s.db().Where("id = ? AND tenant_id = ? AND customer_id = ?", addressID, context.Session.TenantID, context.CustomerID).Delete(&model.CommerceAddress{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *CommerceStorefrontService) SetDefaultAddress(token string, addressID uint) (*CommerceStorefrontAddressView, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if addressID == 0 {
		return nil, fmt.Errorf("%w: address id is required", ErrCommerceStorefrontAddressInvalid)
	}
	var result *model.CommerceAddress
	err = s.db().Transaction(func(tx *gorm.DB) error {
		var address model.CommerceAddress
		if err := tx.Where("id = ? AND tenant_id = ? AND customer_id = ?", addressID, context.Session.TenantID, context.CustomerID).First(&address).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.CommerceAddress{}).
			Where("tenant_id = ? AND customer_id = ? AND address_type = ?", context.Session.TenantID, context.CustomerID, address.AddressType).
			Update("is_default", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&address).Update("is_default", true).Error; err != nil {
			return err
		}
		address.IsDefault = true
		result = &address
		return nil
	})
	if err != nil {
		return nil, err
	}
	return storefrontAddressView(result), nil
}

func (s *CommerceStorefrontService) loadAddressForCheckout(tx *gorm.DB, context *commerceStorefrontContext, addressID uint) (*model.CommerceAddress, error) {
	if context == nil || addressID == 0 {
		return nil, fmt.Errorf("%w: address is required", ErrCommerceStorefrontAddressInvalid)
	}
	var address model.CommerceAddress
	if err := tx.Where("id = ? AND tenant_id = ? AND customer_id = ?", addressID, context.Session.TenantID, context.CustomerID).First(&address).Error; err != nil {
		return nil, err
	}
	return &address, nil
}

func (s *CommerceStorefrontService) ownedCart(context *commerceStorefrontContext, cartID uint) (*model.CommerceCart, error) {
	if cartID == 0 {
		return nil, fmt.Errorf("%w: cart id is required", ErrCommerceStorefrontInvalid)
	}
	cart, err := s.operationsService().GetCart(context.Session.TenantID, cartID)
	if err != nil {
		return nil, err
	}
	if cart.TenantID != context.Session.TenantID || cart.BusinessType != context.Binding.BusinessType || cart.CustomerID != context.CustomerID || cart.ChannelAccountID != context.Session.ChannelAccountID || cart.LocationID != context.Binding.LocationID {
		return nil, ErrCommerceStorefrontOwnership
	}
	return cart, nil
}

func (s *CommerceStorefrontService) GetCart(token string) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	return s.currentCart(context)
}

func (s *CommerceStorefrontService) GetCartByID(token string, cartID uint) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	return s.ownedCart(context, cartID)
}

func (s *CommerceStorefrontService) AddCartItem(token string, input CommerceStorefrontCartItemInput) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	cart, err := s.currentCart(context)
	if err != nil {
		return nil, err
	}
	return s.operationsService().AddCartItem(context.Session.TenantID, cart.ID, CommerceCartItemInput{ProductID: input.ProductID, SkuID: input.SKUID, Quantity: input.Quantity, OptionIDs: input.OptionIDs})
}

func (s *CommerceStorefrontService) AddCartItemToCart(token string, cartID uint, input CommerceStorefrontCartItemInput) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if _, err := s.ownedCart(context, cartID); err != nil {
		return nil, err
	}
	return s.operationsService().AddCartItem(context.Session.TenantID, cartID, CommerceCartItemInput{ProductID: input.ProductID, SkuID: input.SKUID, Quantity: input.Quantity, OptionIDs: input.OptionIDs})
}

func (s *CommerceStorefrontService) UpdateCartItem(token string, itemID uint, quantity int) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	cart, err := s.currentCart(context)
	if err != nil {
		return nil, err
	}
	if _, err := s.operationsService().UpdateCartItem(context.Session.TenantID, cart.ID, itemID, quantity); err != nil {
		return nil, err
	}
	return s.operationsService().GetCart(context.Session.TenantID, cart.ID)
}

func (s *CommerceStorefrontService) UpdateCartItemToCart(token string, cartID, itemID uint, quantity int) (*model.CommerceCart, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if _, err := s.ownedCart(context, cartID); err != nil {
		return nil, err
	}
	if _, err := s.operationsService().UpdateCartItem(context.Session.TenantID, cartID, itemID, quantity); err != nil {
		return nil, err
	}
	return s.operationsService().GetCart(context.Session.TenantID, cartID)
}

func (s *CommerceStorefrontService) RemoveCartItem(token string, itemID uint) error {
	context, err := s.authenticate(token)
	if err != nil {
		return err
	}
	cart, err := s.currentCart(context)
	if err != nil {
		return err
	}
	return s.operationsService().RemoveCartItem(context.Session.TenantID, cart.ID, itemID)
}

func (s *CommerceStorefrontService) RemoveCartItemFromCart(token string, cartID, itemID uint) error {
	context, err := s.authenticate(token)
	if err != nil {
		return err
	}
	if _, err := s.ownedCart(context, cartID); err != nil {
		return err
	}
	return s.operationsService().RemoveCartItem(context.Session.TenantID, cartID, itemID)
}

func normalizeStorefrontCheckout(input CommerceStorefrontCheckoutInput) (CommerceStorefrontCheckoutInput, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactPhone = strings.TrimSpace(input.ContactPhone)
	input.ShippingAddress = strings.TrimSpace(input.ShippingAddress)
	input.FulfillmentMethod = strings.TrimSpace(input.FulfillmentMethod)
	if input.FulfillmentMethod != "" {
		input.FulfillmentMethod = strings.ToLower(input.FulfillmentMethod)
	}
	if len([]rune(input.IdempotencyKey)) > 100 || len([]rune(input.ContactName)) > 80 || len([]rune(input.ContactPhone)) > 30 || len([]rune(input.ShippingAddress)) > 2000 || len([]rune(input.FulfillmentMethod)) > 20 {
		return input, fmt.Errorf("%w: checkout fields are too long", ErrCommerceStorefrontInvalid)
	}
	return input, nil
}

func storefrontOrderMatchesCheckout(tx *gorm.DB, order *model.CommerceOrder, context *commerceStorefrontContext, input CommerceStorefrontCheckoutInput, idempotencyKey string) (bool, error) {
	if order == nil || context == nil || order.TenantID != context.Session.TenantID || order.Channel != "wechat_miniapp" || order.CustomerID != context.CustomerID || order.BusinessType != context.Binding.BusinessType || order.LocationID != context.Binding.LocationID || order.IdempotencyKey != idempotencyKey || order.ContactName != input.ContactName || order.ContactPhone != input.ContactPhone {
		return false, nil
	}
	// A delivery retry carries the address ID, not the mutable client-side
	// address text. Compare that ID with the immutable order snapshot so a
	// valid retry still converges after the customer edits or deletes the
	// address. Free-form text is only relevant to legacy callers that did not
	// provide an address ID.
	requiresAddress := context.Binding.BusinessType != "restaurant" || input.FulfillmentMethod == "delivery"
	if requiresAddress && input.AddressID != 0 {
		var snapshot CommerceStorefrontAddressView
		if err := json.Unmarshal([]byte(order.ShippingAddressJSON), &snapshot); err != nil || snapshot.ID != input.AddressID {
			return false, nil
		}
	} else if !requiresAddress && order.ShippingAddressJSON != "" {
		return false, nil
	} else if requiresAddress && input.AddressID == 0 && order.ShippingAddressJSON != input.ShippingAddress {
		return false, nil
	}
	if context.Binding.BusinessType == "restaurant" {
		var fulfillment model.RestaurantFulfillment
		if err := tx.Where("tenant_id = ? AND order_id = ?", order.TenantID, order.ID).First(&fulfillment).Error; err != nil {
			return false, err
		}
		return fulfillment.Method == input.FulfillmentMethod, nil
	}
	return input.FulfillmentMethod == "", nil
}

func (s *CommerceStorefrontService) Checkout(token string, raw CommerceStorefrontCheckoutInput) (*model.CommerceOrder, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	input, err := normalizeStorefrontCheckout(raw)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency key is required", ErrCommerceStorefrontInvalid)
	}
	// Keep the idempotency lookup's interpretation identical to the actual
	// checkout path. Restaurant pickup is the default when the client omits
	// the method, so a retry compares against "pickup" rather than an empty
	// request value.
	if context.Binding.BusinessType == "restaurant" && input.FulfillmentMethod == "" {
		input.FulfillmentMethod = "pickup"
	}
	// A retry after the cart was marked checked_out may no longer find an
	// active cart. Resolve the tenant/customer/channel-scoped idempotency fact
	// first so the same request still returns its original order.
	var existing model.CommerceOrder
	lookupErr := s.db().Where("tenant_id = ? AND business_type = ? AND channel = ? AND customer_id = ? AND idempotency_key = ?", context.Session.TenantID, context.Binding.BusinessType, "wechat_miniapp", context.CustomerID, input.IdempotencyKey).First(&existing).Error
	if lookupErr == nil {
		order, getErr := s.ordersService().GetOrder(context.Session.TenantID, existing.ID)
		if getErr != nil {
			return nil, getErr
		}
		matches, matchErr := storefrontOrderMatchesCheckout(s.db(), order, context, input, input.IdempotencyKey)
		if matchErr != nil {
			return nil, matchErr
		}
		if !matches {
			return nil, ErrCommerceIdempotencyConflict
		}
		return order, nil
	}
	if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return nil, lookupErr
	}
	cart, err := s.currentCart(context)
	if err != nil {
		return nil, err
	}
	return s.checkoutOwnedCart(context, cart.ID, input)
}

func (s *CommerceStorefrontService) CheckoutCartByID(token string, cartID uint, raw CommerceStorefrontCheckoutInput) (*model.CommerceOrder, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if _, err := s.ownedCart(context, cartID); err != nil {
		return nil, err
	}
	return s.checkoutOwnedCart(context, cartID, raw)
}

func (s *CommerceStorefrontService) checkoutOwnedCart(context *commerceStorefrontContext, cartID uint, raw CommerceStorefrontCheckoutInput) (*model.CommerceOrder, error) {
	if context == nil || cartID == 0 {
		return nil, ErrCommerceStorefrontUnauthenticated
	}
	input, err := normalizeStorefrontCheckout(raw)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency key is required", ErrCommerceStorefrontInvalid)
	}
	if context.Binding.BusinessType == "restaurant" && input.FulfillmentMethod == "" {
		input.FulfillmentMethod = "pickup"
	}
	if context.Binding.BusinessType == "restaurant" && input.FulfillmentMethod != "pickup" && input.FulfillmentMethod != "delivery" {
		return nil, fmt.Errorf("%w: restaurant fulfillment method is invalid", ErrCommerceStorefrontInvalid)
	}
	if context.Binding.BusinessType == "retail" && input.FulfillmentMethod != "" {
		return nil, fmt.Errorf("%w: retail does not accept restaurant fulfillment method", ErrCommerceStorefrontInvalid)
	}
	var result *model.CommerceOrder
	err = s.db().Transaction(func(tx *gorm.DB) error {
		var locked model.CommerceCart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", cartID, context.Session.TenantID).Preload("Items").First(&locked).Error; err != nil {
			return err
		}
		if locked.TenantID != context.Session.TenantID || locked.BusinessType != context.Binding.BusinessType || locked.CustomerID != context.CustomerID || locked.ChannelAccountID != context.Session.ChannelAccountID || locked.LocationID != context.Binding.LocationID {
			return ErrCommerceStorefrontOwnership
		}
		orders := s.ordersService()
		orders.DB = tx
		if locked.Status == "checked_out" {
			if locked.CheckedOutOrderID == 0 {
				return fmt.Errorf("%w: checked-out cart is missing its order", ErrCommerceStorefrontInvalid)
			}
			order, err := orders.GetOrder(context.Session.TenantID, locked.CheckedOutOrderID)
			if err != nil {
				return err
			}
			// Replays must remain idempotent even if the customer has since
			// edited or deleted the address. The immutable order snapshot is the
			// comparison source; the address id is checked against its snapshot
			// when one was supplied by the caller.
			if input.FulfillmentMethod == "pickup" {
				input.ShippingAddress = ""
			} else {
				var snapshot CommerceStorefrontAddressView
				if err := json.Unmarshal([]byte(order.ShippingAddressJSON), &snapshot); err != nil || input.AddressID == 0 || snapshot.ID != input.AddressID {
					return ErrCommerceIdempotencyConflict
				}
				input.ShippingAddress = order.ShippingAddressJSON
			}
			matches, err := storefrontOrderMatchesCheckout(tx, order, context, input, input.IdempotencyKey)
			if err != nil {
				return err
			}
			if !matches {
				return ErrCommerceIdempotencyConflict
			}
			result = order
			return nil
		}
		if locked.Status != "active" || len(locked.Items) == 0 {
			return fmt.Errorf("%w: cart is empty or no longer active", ErrCommerceStorefrontInvalid)
		}
		if context.Binding.BusinessType == "restaurant" && input.FulfillmentMethod == "pickup" {
			input.ShippingAddress = ""
		} else {
			address, addressErr := s.loadAddressForCheckout(tx, context, input.AddressID)
			if addressErr != nil {
				return addressErr
			}
			if context.Binding.BusinessType == "retail" && address.AddressType != "SHIPPING" {
				return fmt.Errorf("%w: retail checkout requires a shipping address", ErrCommerceStorefrontAddressInvalid)
			}
			if context.Binding.BusinessType == "restaurant" && address.AddressType != "CAMPUS" {
				return fmt.Errorf("%w: restaurant delivery requires a campus address", ErrCommerceStorefrontAddressInvalid)
			}
			snapshot, snapshotErr := storefrontAddressSnapshot(address)
			if snapshotErr != nil {
				return snapshotErr
			}
			input.ShippingAddress = snapshot
		}
		items := make([]CommerceOrderItemInput, 0, len(locked.Items))
		for _, item := range locked.Items {
			items = append(items, CommerceOrderItemInput{ProductID: item.ProductID, SKUID: item.SkuID, Quantity: item.Quantity, OptionsSnapshotJSON: item.OptionsSnapshotJSON})
		}
		order, err := orders.CreateOrder(context.Session.TenantID, CreateCommerceOrderInput{
			IdempotencyKey: input.IdempotencyKey, Channel: "wechat_miniapp", CustomerID: context.CustomerID,
			BusinessType: context.Binding.BusinessType, LocationID: context.Binding.LocationID,
			ContactName: input.ContactName, ContactPhone: input.ContactPhone,
			ShippingAddressJSON: input.ShippingAddress, FulfillmentMethod: input.FulfillmentMethod, Items: items,
		})
		if err != nil {
			return err
		}
		if err := tx.Model(&locked).Updates(map[string]interface{}{"status": "checked_out", "checked_out_order_id": order.ID}).Error; err != nil {
			return err
		}
		result = order
		return nil
	})
	return result, err
}

func (s *CommerceStorefrontService) CheckoutCart(token string, input CommerceStorefrontCheckoutInput) (*model.CommerceOrder, error) {
	return s.Checkout(token, input)
}

func (s *CommerceStorefrontService) ListOrders(token string, page, pageSize int) (*CommerceStorefrontOrderPage, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := s.db().Where("tenant_id = ? AND business_type = ? AND channel = ? AND customer_id = ?", context.Session.TenantID, context.Binding.BusinessType, "wechat_miniapp", context.CustomerID)
	var total int64
	if err := query.Model(&model.CommerceOrder{}).Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []model.CommerceOrder
	if err := query.Preload("Items").Preload("AfterSales", "tenant_id = ?", context.Session.TenantID, func(db *gorm.DB) *gorm.DB { return db.Order("created_at DESC") }).Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []model.CommerceOrder{}
	}
	return &CommerceStorefrontOrderPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *CommerceStorefrontService) GetOrder(token string, orderID uint) (*model.CommerceOrder, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	if orderID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var candidate model.CommerceOrder
	if err := s.db().Where("id = ? AND tenant_id = ? AND business_type = ? AND channel = ? AND customer_id = ?", orderID, context.Session.TenantID, context.Binding.BusinessType, "wechat_miniapp", context.CustomerID).First(&candidate).Error; err != nil {
		return nil, err
	}
	return s.ordersService().GetOrder(context.Session.TenantID, candidate.ID)
}

func (s *CommerceStorefrontService) GetOrderByNo(token, orderNo string) (*model.CommerceOrder, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	orderNo = strings.TrimSpace(orderNo)
	if orderNo == "" || len([]rune(orderNo)) > 50 {
		return nil, gorm.ErrRecordNotFound
	}
	var candidate model.CommerceOrder
	if err := s.db().Where("order_no = ? AND tenant_id = ? AND business_type = ? AND channel = ? AND customer_id = ?", orderNo, context.Session.TenantID, context.Binding.BusinessType, "wechat_miniapp", context.CustomerID).First(&candidate).Error; err != nil {
		return nil, err
	}
	return s.ordersService().GetOrder(context.Session.TenantID, candidate.ID)
}

func (s *CommerceStorefrontService) RequestRefund(token, orderNo string, raw CommerceStorefrontRefundInput) (*CommerceRefundResult, error) {
	context, err := s.authenticate(token)
	if err != nil {
		return nil, err
	}
	raw.IdempotencyKey = strings.TrimSpace(raw.IdempotencyKey)
	raw.Reason = strings.TrimSpace(raw.Reason)
	if raw.IdempotencyKey == "" || len([]rune(raw.IdempotencyKey)) > 100 || len([]rune(raw.Reason)) > 255 {
		return nil, fmt.Errorf("%w: refund request fields are invalid", ErrCommerceStorefrontInvalid)
	}
	order, err := s.GetOrderByNo(token, orderNo)
	if err != nil {
		return nil, err
	}
	return s.ordersService().RequestRefund(context.Session.TenantID, order.ID, raw.IdempotencyKey, raw.Reason)
}
