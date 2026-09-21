package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type commerceStorefrontServiceFixture struct {
	tenantID uint
	domain   commerceBoundaryDomainFixture
	account  model.ChannelAccount
	binding  model.CommerceStorefrontBinding
	service  *CommerceStorefrontService
	setNow   func(time.Time)
}

func createCommerceStorefrontAccountAndBinding(t *testing.T, tenantID, locationID uint, businessType, code, appID string) (model.ChannelAccount, model.CommerceStorefrontBinding) {
	t.Helper()
	account := model.ChannelAccount{
		TenantID: tenantID, Code: code, Type: "wechat_miniapp", AppID: appID,
		Status: "active", Environment: "production",
	}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatalf("create storefront channel account: %v", err)
	}
	binding := model.CommerceStorefrontBinding{
		TenantID: tenantID, ChannelAccountID: account.ID, BusinessType: businessType,
		LocationID: locationID, Status: "active",
	}
	if err := model.DB.Create(&binding).Error; err != nil {
		t.Fatalf("create storefront binding: %v", err)
	}
	return account, binding
}

func newCommerceStorefrontServiceFixture(t *testing.T) commerceStorefrontServiceFixture {
	t.Helper()
	tenantID := newCommerceTenant(t, "restaurant", "active")
	domain := createCommerceBoundaryDomainFixture(t, tenantID, "restaurant")
	account, binding := createCommerceStorefrontAccountAndBinding(
		t, tenantID, domain.location.ID, "restaurant", "storefront-account", "wx-storefront-app",
	)
	current := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	setNow := func(value time.Time) { current = value }
	storefront := &CommerceStorefrontService{
		DB: model.DB,
		LoginAdapter: WechatMiniappLoginFunc(func(_ context.Context, request WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
			return WechatMiniappLoginIdentity{Subject: request.Code}, nil
		}),
		Now:        func() time.Time { return current },
		SessionTTL: time.Hour,
	}
	delivery := &CommerceDeliveryService{DB: model.DB, Clock: storefront.Now}
	if _, err := delivery.UpsertLocationConfig(tenantID, domain.location.ID, CommerceLocationServiceConfigInput{
		BusinessType: "restaurant", PickupEnabled: true, DeliveryEnabled: true,
		MinGoodsCents: 0, PackagingFeeCents: 0,
	}); err != nil {
		t.Fatalf("configure storefront restaurant fulfillment: %v", err)
	}
	return commerceStorefrontServiceFixture{
		tenantID: tenantID, domain: domain, account: account, binding: binding,
		service: storefront, setNow: setNow,
	}
}

func storefrontPickupQuote(t *testing.T, fixture commerceStorefrontServiceFixture, token string) *CommerceCheckoutQuoteResult {
	t.Helper()
	quote, err := fixture.service.CreateCheckoutQuote(token, CommerceStorefrontQuoteInput{FulfillmentMethod: "pickup"}, "restaurant")
	if err != nil {
		t.Fatalf("create storefront pickup quote: %v", err)
	}
	return quote
}

func storefrontDeliverySetup(t *testing.T, fixture commerceStorefrontServiceFixture) (*model.CommerceDeliveryZone, *model.CommerceDeliverySlot, time.Time) {
	t.Helper()
	delivery := &CommerceDeliveryService{DB: model.DB, Clock: fixture.service.Now}
	zone, err := delivery.CreateZone(fixture.tenantID, fixture.domain.location.ID, "restaurant", CommerceDeliveryZoneInput{
		Name: "南校区配送区", Province: "南校区", City: "南校区", District: "二食堂", FeeCents: 0,
	})
	if err != nil {
		t.Fatalf("create storefront delivery zone: %v", err)
	}
	date := fixture.service.Now().Truncate(24 * time.Hour)
	slot, err := delivery.CreateSlot(fixture.tenantID, fixture.domain.location.ID, "restaurant", CommerceDeliverySlotInput{
		ZoneID: &zone.ID, DayOfWeek: int(date.Weekday()), StartMinute: 14 * 60, EndMinute: 15 * 60,
		OrderCutoffMinutes: 30, Capacity: 0,
	})
	if err != nil {
		t.Fatalf("create storefront delivery slot: %v", err)
	}
	return zone, slot, date
}

func storefrontDeliveryQuote(t *testing.T, fixture commerceStorefrontServiceFixture, token string, addressID, zoneID, slotID uint, date time.Time) *CommerceCheckoutQuoteResult {
	t.Helper()
	quote, err := fixture.service.CreateCheckoutQuote(token, CommerceStorefrontQuoteInput{
		AddressID: addressID, FulfillmentMethod: "delivery", ZoneID: zoneID, SlotID: slotID, SlotDate: date,
	}, "restaurant")
	if err != nil {
		t.Fatalf("create storefront delivery quote: %v", err)
	}
	return quote
}

func TestCommerceStorefrontListChannelAccountsIsTenantScopedAndSecretFree(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	foreignTenantRecord := model.Tenant{
		Name: "Foreign storefront tenant", SystemCode: "FOREIGN-STOREFRONT", SecretKey: "foreign-secret", Status: "active",
	}
	if err := model.DB.Create(&foreignTenantRecord).Error; err != nil {
		t.Fatalf("create foreign storefront tenant: %v", err)
	}
	foreign := model.ChannelAccount{
		TenantID: foreignTenantRecord.ID, Code: "foreign-storefront-account", Type: "wechat_miniapp",
		AppID: "wx-foreign", SecretCiphertext: "foreign-secret", Status: "active", Environment: "production",
	}
	if err := model.DB.Create(&foreign).Error; err != nil {
		t.Fatalf("create foreign storefront account: %v", err)
	}
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("secret_ciphertext", "encrypted-secret").Error; err != nil {
		t.Fatalf("seed storefront credentials: %v", err)
	}

	rows, err := fixture.service.ListChannelAccounts(fixture.tenantID)
	if err != nil {
		t.Fatalf("list storefront channel accounts: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != fixture.account.ID || !rows[0].CredentialsReady {
		t.Fatalf("unexpected storefront channel accounts: %+v", rows)
	}
	encoded, marshalErr := json.Marshal(rows[0])
	if marshalErr != nil {
		t.Fatalf("marshal storefront channel view: %v", marshalErr)
	}
	if strings.Contains(string(encoded), "encrypted-secret") || strings.Contains(string(encoded), "secret_ciphertext") {
		t.Fatalf("storefront channel view exposed secret material: %s", encoded)
	}
}

func TestCommerceStorefrontContactIsAccountScopedAndPublicOnlyWhenActive(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	imageStore := &CommerceStorefrontContactImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	fixture.service.ContactImages = imageStore

	if _, err := fixture.service.SaveChannelContact(fixture.tenantID, fixture.account.ID, CommerceStorefrontContactInput{
		ContactType: "enterprise_wechat", ContactName: "门店客服", WechatID: "shop-service",
		Status: "active", Reason: "enable customer contact",
	}, nil, 7, "admin"); !errors.Is(err, ErrCommerceStorefrontContactInvalid) {
		t.Fatalf("active contact without QR error=%v", err)
	}

	saved, err := fixture.service.SaveChannelContact(fixture.tenantID, fixture.account.ID, CommerceStorefrontContactInput{
		ContactType: "enterprise_wechat", ContactName: "门店客服", WechatID: "shop-service",
		Status: "active", Reason: "enable customer contact",
	}, commercePNG(t), 7, "admin")
	if err != nil {
		t.Fatalf("save storefront contact: %v", err)
	}
	if !saved.Available || saved.ContactType != "enterprise_wechat" || saved.WechatID != "shop-service" || saved.QRCodeURL == "" {
		t.Fatalf("saved contact=%+v", saved)
	}

	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "contact-customer")
	public, err := fixture.service.GetContact(login.Token)
	if err != nil {
		t.Fatalf("get public contact: %v", err)
	}
	if !public.Available || public.ChannelAccountID != fixture.account.ID || public.QRCodeURL != saved.QRCodeURL {
		t.Fatalf("public contact=%+v", public)
	}

	foreignTenant := model.Tenant{Name: "Foreign contact tenant", SystemCode: "FOREIGN-CONTACT", SecretKey: "foreign-contact-secret", Status: "active"}
	if err := model.DB.Create(&foreignTenant).Error; err != nil {
		t.Fatalf("create foreign contact tenant: %v", err)
	}
	foreignTenantID := foreignTenant.ID
	if _, err := fixture.service.GetChannelContact(foreignTenantID, fixture.account.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant contact read error=%v", err)
	}

	disabled, err := fixture.service.SaveChannelContact(fixture.tenantID, fixture.account.ID, CommerceStorefrontContactInput{
		ContactType: "enterprise_wechat", ContactName: "门店客服", WechatID: "shop-service",
		Status: "disabled", Reason: "pause customer contact",
	}, nil, 7, "admin")
	if err != nil || disabled.Available {
		t.Fatalf("disable storefront contact=%+v err=%v", disabled, err)
	}
	public, err = fixture.service.GetContact(login.Token)
	if err != nil {
		t.Fatalf("get disabled public contact: %v", err)
	}
	if public.Available || public.QRCodeURL != "" || public.WechatID != "" || public.ContactName != "" {
		t.Fatalf("disabled public contact leaked details: %+v", public)
	}
}

func TestCommerceStorefrontContactPublicProjectionFailsClosedForMissingOrUnmanagedImage(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	imageStore := &CommerceStorefrontContactImageStore{Directory: t.TempDir(), PublicBaseURL: "https://tickets.example.com"}
	fixture.service.ContactImages = imageStore
	saved, err := fixture.service.SaveChannelContact(fixture.tenantID, fixture.account.ID, CommerceStorefrontContactInput{
		ContactType: "personal_wechat", ContactName: "商家客服", WechatID: "merchant-service",
		Status: "active", Reason: "configure customer contact",
	}, commercePNG(t), 7, "admin")
	if err != nil {
		t.Fatalf("save storefront contact: %v", err)
	}
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "missing-contact-image-customer")
	imagePath, err := imageStore.ownedPath(fixture.tenantID, fixture.account.ID, saved.QRCodeURL)
	if err != nil {
		t.Fatalf("resolve managed contact image: %v", err)
	}
	if err := os.Remove(imagePath); err != nil {
		t.Fatalf("remove managed contact image: %v", err)
	}
	public, err := fixture.service.GetContact(login.Token)
	if err != nil {
		t.Fatalf("get contact after image removal: %v", err)
	}
	if public.Available || public.QRCodeURL != "" || public.ContactName != "" || public.WechatID != "" {
		t.Fatalf("missing contact image leaked public configuration: %+v", public)
	}

	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).
		Updates(map[string]interface{}{"storefront_contact_qr_code_url": "https://external.example/contact.png"}).Error; err != nil {
		t.Fatalf("seed unmanaged contact URL: %v", err)
	}
	public, err = fixture.service.GetContact(login.Token)
	if err != nil {
		t.Fatalf("get contact with unmanaged URL: %v", err)
	}
	if public.Available || public.QRCodeURL != "" || public.ContactName != "" || public.WechatID != "" {
		t.Fatalf("unmanaged contact URL leaked public configuration: %+v", public)
	}
}

func TestCommerceStorefrontContactSaveRejectsExistingQRCodeWithoutImageStore(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Updates(map[string]interface{}{
		"storefront_contact_status":      "active",
		"storefront_contact_qr_code_url": "https://tickets.example.com/api/v1/public/commerce-storefront-contact-images/1/1/legacy.png",
		"storefront_contact_name":        "历史客服",
		"storefront_contact_type":        "personal_wechat",
		"storefront_wechat_id":           "legacy-service",
	}).Error; err != nil {
		t.Fatalf("seed legacy contact configuration: %v", err)
	}
	if _, err := fixture.service.SaveChannelContact(fixture.tenantID, fixture.account.ID, CommerceStorefrontContactInput{
		ContactType: "personal_wechat", ContactName: "历史客服", WechatID: "legacy-service",
		Status: "disabled", Reason: "disable invalid legacy contact",
	}, nil, 7, "admin"); !errors.Is(err, ErrCommerceStorefrontContactInvalid) {
		t.Fatalf("save accepted existing QR code without image store: %v", err)
	}
}

func storefrontLogin(t *testing.T, storefront *CommerceStorefrontService, appID, code string) *CommerceStorefrontLoginResult {
	t.Helper()
	result, err := storefront.Login(context.Background(), CommerceStorefrontLoginInput{AppID: appID, Code: code})
	if err != nil {
		t.Fatalf("storefront login: %v", err)
	}
	return result
}

func TestCommerceStorefrontSessionsBindAccountAndCustomerCart(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	secondAccount, _ := createCommerceStorefrontAccountAndBinding(
		t, fixture.tenantID, fixture.domain.location.ID, "restaurant", "storefront-account-2", "wx-storefront-app-2",
	)

	firstLogin := storefrontLogin(t, fixture.service, fixture.account.AppID, "same-wechat-subject")
	secondLogin := storefrontLogin(t, fixture.service, secondAccount.AppID, "same-wechat-subject")
	firstSession, err := fixture.service.Authenticate(firstLogin.Token)
	if err != nil {
		t.Fatalf("authenticate first storefront session: %v", err)
	}
	secondSession, err := fixture.service.Authenticate(secondLogin.Token)
	if err != nil {
		t.Fatalf("authenticate second storefront session: %v", err)
	}
	if firstSession.TenantID != fixture.tenantID || secondSession.TenantID != fixture.tenantID {
		t.Fatalf("storefront sessions lost tenant binding: first=%+v second=%+v", firstSession, secondSession)
	}
	if firstSession.ChannelAccountID != fixture.account.ID || secondSession.ChannelAccountID != secondAccount.ID {
		t.Fatalf("storefront sessions lost channel binding: first=%+v second=%+v", firstSession, secondSession)
	}
	if firstSession.SubjectHash != secondSession.SubjectHash {
		t.Fatalf("same provider subject was not preserved across accounts: first=%q second=%q", firstSession.SubjectHash, secondSession.SubjectHash)
	}

	firstCart, err := fixture.service.GetCart(firstLogin.Token)
	if err != nil {
		t.Fatalf("get first storefront cart: %v", err)
	}
	secondCart, err := fixture.service.GetCart(secondLogin.Token)
	if err != nil {
		t.Fatalf("get second storefront cart: %v", err)
	}
	if firstCart.ID == secondCart.ID || firstCart.CustomerID == secondCart.CustomerID {
		t.Fatalf("same provider subject shared a cart across channel accounts: first=%+v second=%+v", firstCart, secondCart)
	}
	if firstCart.TenantID != fixture.tenantID || firstCart.ChannelAccountID != fixture.account.ID || secondCart.ChannelAccountID != secondAccount.ID {
		t.Fatalf("cart scope was not derived from the session: first=%+v second=%+v", firstCart, secondCart)
	}

	if _, err := fixture.service.AddCartItem(firstLogin.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add first storefront cart item: %v", err)
	}
	secondCart, err = fixture.service.GetCart(secondLogin.Token)
	if err != nil {
		t.Fatalf("reload second storefront cart: %v", err)
	}
	if len(secondCart.Items) != 0 {
		t.Fatalf("first account cart write leaked into second account cart: %+v", secondCart.Items)
	}
	if _, err := fixture.service.AddCartItemToCart(firstLogin.Token, secondCart.ID, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); !errors.Is(err, ErrCommerceStorefrontOwnership) {
		t.Fatalf("foreign account cart access error=%v, want %v", err, ErrCommerceStorefrontOwnership)
	}
}

func TestCommerceStorefrontSameAccountUsesOneSessionAcrossIsolatedDomains(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	if err := model.DB.Create(&model.TenantBusinessCapability{
		TenantID: fixture.tenantID, BusinessType: "retail", Status: "active",
	}).Error; err != nil {
		t.Fatalf("create retail storefront capability: %v", err)
	}
	retailDomain := createCommerceBoundaryDomainFixture(t, fixture.tenantID, "retail")
	retailBinding := model.CommerceStorefrontBinding{
		TenantID: fixture.tenantID, ChannelAccountID: fixture.account.ID, BusinessType: "retail",
		LocationID: retailDomain.location.ID, Status: "active",
	}
	if err := model.DB.Create(&retailBinding).Error; err != nil {
		t.Fatalf("create second storefront binding on same account: %v", err)
	}
	if retailBinding.ID == 0 {
		t.Fatal("second storefront binding did not receive an id")
	}

	adapterCalls := 0
	fixture.service.LoginAdapter = WechatMiniappLoginFunc(func(_ context.Context, request WechatMiniappLoginRequest) (WechatMiniappLoginIdentity, error) {
		adapterCalls++
		return WechatMiniappLoginIdentity{Subject: request.Code}, nil
	})
	login, err := fixture.service.Login(context.Background(), CommerceStorefrontLoginInput{
		AppID: fixture.account.AppID, Code: "same-subject",
	})
	if err != nil {
		t.Fatalf("shared-account login: %v", err)
	}
	if adapterCalls != 1 {
		t.Fatalf("shared-account login exchanged provider code %d times, want 1", adapterCalls)
	}
	if login.BusinessType != "" || login.LocationID != 0 || len(login.Businesses) != 2 {
		t.Fatalf("shared-account login returned wrong businesses: %+v", login)
	}
	if login.Businesses[0].BusinessType != "restaurant" || login.Businesses[0].Location.ID != fixture.domain.location.ID ||
		login.Businesses[1].BusinessType != "retail" || login.Businesses[1].Location.ID != retailDomain.location.ID {
		t.Fatalf("shared-account business routes=%+v", login.Businesses)
	}
	if _, err := fixture.service.GetCart(login.Token); !errors.Is(err, ErrCommerceStorefrontAmbiguous) {
		t.Fatalf("multi-domain cart without selector error=%v, want ambiguous", err)
	}
	restaurantCart, err := fixture.service.GetCart(login.Token, "restaurant")
	if err != nil {
		t.Fatalf("get restaurant cart: %v", err)
	}
	retailCart, err := fixture.service.GetCart(login.Token, "retail")
	if err != nil {
		t.Fatalf("get retail cart: %v", err)
	}
	if restaurantCart.ID == retailCart.ID || restaurantCart.BusinessType != "restaurant" || retailCart.BusinessType != "retail" {
		t.Fatalf("shared-account carts crossed domains: restaurant=%+v retail=%+v", restaurantCart, retailCart)
	}
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}, "retail"); err == nil {
		t.Fatal("retail route accepted restaurant product")
	}

	retry, err := fixture.service.Login(context.Background(), CommerceStorefrontLoginInput{
		AppID: fixture.account.AppID, Code: "same-subject",
	})
	if err != nil {
		t.Fatalf("repeat shared-account login: %v", err)
	}
	if _, err := fixture.service.Authenticate(login.Token); !errors.Is(err, ErrCommerceStorefrontUnauthenticated) {
		t.Fatalf("old account session error=%v, want revoked", err)
	}
	if _, err := fixture.service.GetCart(retry.Token, "retail"); err != nil {
		t.Fatalf("new account session could not access retail: %v", err)
	}
	if err := model.DB.Model(&model.CommerceStorefrontBinding{}).
		Where("id = ? AND tenant_id = ?", retailBinding.ID, fixture.tenantID).
		Update("status", "disabled").Error; err != nil {
		t.Fatalf("disable retail publication: %v", err)
	}
	if _, err := fixture.service.Authenticate(retry.Token); err != nil {
		t.Fatalf("disabling retail invalidated account session: %v", err)
	}
	if _, err := fixture.service.GetCart(retry.Token, "restaurant"); err != nil {
		t.Fatalf("disabling retail blocked restaurant: %v", err)
	}
	if _, err := fixture.service.GetCart(retry.Token, "retail"); !errors.Is(err, ErrCommerceStorefrontUnavailable) {
		t.Fatalf("disabled retail route error=%v, want unavailable", err)
	}
}

func TestCommerceStorefrontSandboxAccountCanLoginAndAuthenticate(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).
		Updates(map[string]interface{}{"status": "sandbox", "environment": "sandbox"}).Error; err != nil {
		t.Fatalf("set storefront account to sandbox: %v", err)
	}

	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "sandbox-subject")
	context, err := fixture.service.authenticate(login.Token)
	if err != nil {
		t.Fatalf("authenticate sandbox storefront session: %v", err)
	}
	if context.Account.Status != "sandbox" || context.Account.Environment != "sandbox" {
		t.Fatalf("sandbox storefront account lost environment: %+v", context.Account)
	}
}

func TestCommerceStorefrontSessionFailsClosedAcrossTenantsAndLifecycle(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	foreignTenantID := createCommerceBoundaryTenant(t, "restaurant")
	foreignDomain := createCommerceBoundaryDomainFixture(t, foreignTenantID, "restaurant")
	foreignAccount, _ := createCommerceStorefrontAccountAndBinding(
		t, foreignTenantID, foreignDomain.location.ID, "restaurant", "storefront-foreign-account", "wx-storefront-foreign-app",
	)
	ownerLogin := storefrontLogin(t, fixture.service, fixture.account.AppID, "owner-subject")
	foreignLogin := storefrontLogin(t, fixture.service, foreignAccount.AppID, "foreign-subject")
	foreignCart, err := fixture.service.GetCart(foreignLogin.Token)
	if err != nil {
		t.Fatalf("get foreign storefront cart: %v", err)
	}
	if _, err := fixture.service.AddCartItemToCart(ownerLogin.Token, foreignCart.ID, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant cart access error=%v, want %v", err, gorm.ErrRecordNotFound)
	}

	fixture.setNow(time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC))
	if _, err := fixture.service.Authenticate(ownerLogin.Token); !errors.Is(err, ErrCommerceStorefrontUnauthenticated) {
		t.Fatalf("expired storefront session error=%v, want %v", err, ErrCommerceStorefrontUnauthenticated)
	}
	var expired model.CommerceCustomerSession
	if err := model.DB.Where("token_hash = ?", storefrontHash(ownerLogin.Token)).First(&expired).Error; err != nil {
		t.Fatalf("load expired storefront session: %v", err)
	}
	if expired.Status != "expired" {
		t.Fatalf("expired storefront session status=%q, want expired", expired.Status)
	}

	fixture.setNow(time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	revokedLogin := storefrontLogin(t, fixture.service, fixture.account.AppID, "revoked-subject")
	if err := fixture.service.RevokeSession(revokedLogin.Token); err != nil {
		t.Fatalf("revoke storefront session: %v", err)
	}
	if _, err := fixture.service.Authenticate(revokedLogin.Token); !errors.Is(err, ErrCommerceStorefrontUnauthenticated) {
		t.Fatalf("revoked storefront session error=%v, want %v", err, ErrCommerceStorefrontUnauthenticated)
	}

	disabledLogin := storefrontLogin(t, fixture.service, fixture.account.AppID, "disabled-account-subject")
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("status", "disabled").Error; err != nil {
		t.Fatalf("disable storefront channel account: %v", err)
	}
	if _, err := fixture.service.Authenticate(disabledLogin.Token); !errors.Is(err, ErrCommerceStorefrontUnavailable) {
		t.Fatalf("disabled storefront account error=%v, want %v", err, ErrCommerceStorefrontUnavailable)
	}
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("status", "active").Error; err != nil {
		t.Fatalf("restore storefront channel account: %v", err)
	}
	if err := model.DB.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ?", fixture.tenantID, "restaurant").Update("status", "suspended").Error; err != nil {
		t.Fatalf("suspend storefront capability: %v", err)
	}
	if _, err := fixture.service.Authenticate(disabledLogin.Token); err != nil {
		t.Fatalf("one suspended business invalidated account session: %v", err)
	}
	if _, err := fixture.service.GetCart(disabledLogin.Token, "restaurant"); !errors.Is(err, ErrBusinessCapabilityInactive) && !errors.Is(err, ErrCommerceStorefrontUnavailable) {
		t.Fatalf("suspended storefront business error=%v, want capability denial", err)
	}
}

func TestCommerceStorefrontCheckoutDerivesFactsAndIsIdempotent(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "checkout-subject")
	cart, err := fixture.service.GetCart(login.Token)
	if err != nil {
		t.Fatalf("get storefront checkout cart: %v", err)
	}
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add storefront checkout item: %v", err)
	}

	var input CommerceStorefrontCheckoutInput
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"tenant_id":%d,"customer_id":"attacker","business_type":"retail","location_id":999999,"amount_cents":1,"idempotency_key":"storefront-checkout-1","contact_name":"Guest","contact_phone":"13800138000","fulfillment_method":"pickup"}`, fixture.tenantID)), &input); err != nil {
		t.Fatalf("decode storefront checkout input: %v", err)
	}
	input.QuoteToken = storefrontPickupQuote(t, fixture, login.Token).RawToken
	order, err := fixture.service.Checkout(login.Token, input)
	if err != nil {
		t.Fatalf("storefront checkout: %v", err)
	}
	if order.TenantID != fixture.tenantID || order.BusinessType != "restaurant" || order.Channel != "wechat_miniapp" || order.CustomerID != cart.CustomerID || order.LocationID != fixture.domain.location.ID {
		t.Fatalf("storefront checkout accepted client-owned facts: %+v", order)
	}
	if order.TotalAmountCents != fixture.domain.sku.PriceCents || len(order.Items) != 1 || order.Items[0].LineAmountCents != fixture.domain.sku.PriceCents {
		t.Fatalf("storefront checkout amount was not derived from SKU: %+v", order)
	}
	var fulfillment model.RestaurantFulfillment
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", fixture.tenantID, order.ID).First(&fulfillment).Error; err != nil {
		t.Fatalf("load storefront fulfillment: %v", err)
	}
	if fulfillment.LocationID != fixture.domain.location.ID || fulfillment.Method != "pickup" {
		t.Fatalf("storefront fulfillment accepted client-owned facts: %+v", fulfillment)
	}

	retry, err := fixture.service.Checkout(login.Token, input)
	if err != nil || retry.ID != order.ID {
		t.Fatalf("repeated storefront checkout result=%+v err=%v, want original order %d", retry, err, order.ID)
	}
	var orderCount int64
	if err := model.DB.Model(&model.CommerceOrder{}).
		Where("tenant_id = ? AND channel = ? AND customer_id = ?", fixture.tenantID, "wechat_miniapp", cart.CustomerID).
		Count(&orderCount).Error; err != nil {
		t.Fatalf("count storefront checkout orders: %v", err)
	}
	if orderCount != 1 {
		t.Fatalf("repeated storefront checkout created %d orders", orderCount)
	}
	var ticketOrders int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ?", fixture.tenantID).Count(&ticketOrders).Error; err != nil {
		t.Fatalf("count ticket orders after storefront checkout: %v", err)
	}
	var tickets int64
	if err := model.DB.Model(&model.Ticket{}).Where("tenant_id = ?", fixture.tenantID).Count(&tickets).Error; err != nil {
		t.Fatalf("count tickets after storefront checkout: %v", err)
	}
	if ticketOrders != 0 || tickets != 0 {
		t.Fatalf("commercial storefront checkout entered ticket domain: ticket_orders=%d tickets=%d", ticketOrders, tickets)
	}
}

func TestCommerceStorefrontCustomerCancellationAndReceiptConfirmation(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "customer-actions-subject")
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add cancellable storefront item: %v", err)
	}
	cancelQuote := storefrontPickupQuote(t, fixture, login.Token)
	cancellable, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "customer-cancel-order", ContactName: "Guest", ContactPhone: "13800138000",
		FulfillmentMethod: "pickup", QuoteToken: cancelQuote.RawToken,
	})
	if err != nil {
		t.Fatalf("create cancellable storefront order: %v", err)
	}
	cancelled, err := fixture.service.CancelOrder(login.Token, cancellable.OrderNo)
	if err != nil {
		t.Fatalf("cancel unpaid storefront order: %v", err)
	}
	if cancelled.PaymentStatus != "failed" || cancelled.FulfillmentStatus != "cancelled" {
		t.Fatalf("cancelled storefront order=%+v", cancelled)
	}
	if retry, retryErr := fixture.service.CancelOrder(login.Token, cancellable.OrderNo); retryErr != nil || retry.ID != cancellable.ID {
		t.Fatalf("repeat unpaid cancellation order=%+v err=%v", retry, retryErr)
	}
	var cancelledQuote model.CommerceCheckoutQuote
	if err := model.DB.Where("id = ? AND tenant_id = ?", cancelQuote.Quote.ID, fixture.tenantID).First(&cancelledQuote).Error; err != nil {
		t.Fatalf("load cancelled checkout quote: %v", err)
	}
	if cancelledQuote.Status != "cancelled" {
		t.Fatalf("cancelled order retained delivery capacity: %+v", cancelledQuote)
	}
	var cancelledItem model.CommerceOrderItem
	if err := model.DB.Where("tenant_id = ? AND order_id = ?", fixture.tenantID, cancellable.ID).First(&cancelledItem).Error; err != nil {
		t.Fatalf("load cancelled order item: %v", err)
	}
	if cancelledItem.ReservationStatus != "released" {
		t.Fatalf("cancelled order retained inventory reservation: %+v", cancelledItem)
	}

	address, err := fixture.service.SaveAddress(login.Token, CommerceStorefrontAddressInput{
		AddressType: "DELIVERY", RecipientName: "Guest", Phone: "13800138000",
		Province: "省", City: "市", District: "区", Detail: "街道 1 号", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("save delivery address: %v", err)
	}
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add delivery storefront item: %v", err)
	}
	delivery := &CommerceDeliveryService{DB: model.DB, Clock: fixture.service.Now}
	zone, err := delivery.CreateZone(fixture.tenantID, fixture.domain.location.ID, "restaurant", CommerceDeliveryZoneInput{
		Name: "城区配送", Province: "省", City: "市", District: "区", FeeCents: 0,
	})
	if err != nil {
		t.Fatalf("create delivery zone: %v", err)
	}
	slotDate := fixture.service.Now().Truncate(24 * time.Hour)
	slot, err := delivery.CreateSlot(fixture.tenantID, fixture.domain.location.ID, "restaurant", CommerceDeliverySlotInput{
		ZoneID: &zone.ID, DayOfWeek: int(slotDate.Weekday()), StartMinute: 14 * 60, EndMinute: 15 * 60,
		OrderCutoffMinutes: 30, Capacity: 0,
	})
	if err != nil {
		t.Fatalf("create delivery slot: %v", err)
	}
	deliveryQuote := storefrontDeliveryQuote(t, fixture, login.Token, address.ID, zone.ID, slot.ID, slotDate)
	delivered, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "customer-confirm-receipt-order", ContactName: "Guest", ContactPhone: "13800138000",
		AddressID: address.ID, FulfillmentMethod: "delivery", QuoteToken: deliveryQuote.RawToken,
	})
	if err != nil {
		t.Fatalf("create delivery storefront order: %v", err)
	}
	orders := &CommerceOrderService{DB: model.DB, Clock: fixture.service.Now}
	if _, err := orders.ConfirmPayment(fixture.tenantID, delivered.ID); err != nil {
		t.Fatalf("confirm delivery payment: %v", err)
	}
	if _, err := fixture.service.ConfirmReceipt(login.Token, delivered.OrderNo); !errors.Is(err, ErrCommerceOrderState) {
		t.Fatalf("premature receipt confirmation error=%v", err)
	}
	for _, status := range []string{"accepted", "preparing", "ready", "delivering"} {
		if _, err := orders.TransitionRestaurantFulfillment(fixture.tenantID, delivered.ID, RestaurantFulfillmentTransitionInput{
			Status: status, ActorUserID: 101, ActorRole: "admin", Reason: "customer receipt test",
		}); err != nil {
			t.Fatalf("transition restaurant delivery to %s: %v", status, err)
		}
	}
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ? AND tenant_id = ?", delivered.ID, fixture.tenantID).Update("refund_status", "requested").Error; err != nil {
		t.Fatalf("lock delivery with refund: %v", err)
	}
	if _, err := fixture.service.ConfirmReceipt(login.Token, delivered.OrderNo); !errors.Is(err, ErrCommerceOrderState) {
		t.Fatalf("refund-locked receipt confirmation error=%v", err)
	}
	if err := model.DB.Model(&model.CommerceOrder{}).Where("id = ? AND tenant_id = ?", delivered.ID, fixture.tenantID).Update("refund_status", "rejected").Error; err != nil {
		t.Fatalf("release delivery refund lock: %v", err)
	}
	confirmed, err := fixture.service.ConfirmReceipt(login.Token, delivered.OrderNo)
	if err != nil {
		t.Fatalf("confirm delivered storefront order: %v", err)
	}
	if confirmed.FulfillmentStatus != "completed" {
		t.Fatalf("confirmed storefront order=%+v", confirmed)
	}
	if retry, retryErr := fixture.service.ConfirmReceipt(login.Token, delivered.OrderNo); retryErr != nil || retry.FulfillmentStatus != "completed" {
		t.Fatalf("repeat receipt confirmation order=%+v err=%v", retry, retryErr)
	}
}

func TestCommerceStorefrontBindingMoveCannotDriftExistingCart(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "binding-move-subject")
	cart, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("seed cart before binding move: %v", err)
	}
	replacement := model.CommerceFulfillmentLocation{
		TenantID: fixture.tenantID, BusinessType: "restaurant", Name: "Replacement restaurant",
		LocationType: "store", Status: "active",
	}
	if err := model.DB.Create(&replacement).Error; err != nil {
		t.Fatalf("create replacement location: %v", err)
	}
	if err := model.DB.Model(&model.CommerceStorefrontBinding{}).
		Where("id = ? AND tenant_id = ?", fixture.binding.ID, fixture.tenantID).
		Update("location_id", replacement.ID).Error; err != nil {
		t.Fatalf("move storefront binding: %v", err)
	}
	if _, err := fixture.service.CheckoutCartByID(login.Token, cart.ID, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "binding-move-checkout", ContactName: "Guest", ContactPhone: "13800138000", FulfillmentMethod: "pickup", QuoteToken: "binding-move-quote",
	}, "restaurant"); !errors.Is(err, ErrCommerceStorefrontOwnership) {
		t.Fatalf("moved binding checkout error=%v, want ownership rejection", err)
	}
	var orders int64
	if err := model.DB.Model(&model.CommerceOrder{}).
		Where("tenant_id = ? AND customer_id = ?", fixture.tenantID, cart.CustomerID).
		Count(&orders).Error; err != nil {
		t.Fatalf("count orders after rejected binding move: %v", err)
	}
	if orders != 0 {
		t.Fatalf("binding move created %d orders from old cart", orders)
	}
}

func TestCommerceStorefrontAddressesAreSessionScopedAndCheckoutSnapshotsServerAddress(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "address-subject")
	foreignTenantID := createCommerceBoundaryTenant(t, "restaurant")
	foreignDomain := createCommerceBoundaryDomainFixture(t, foreignTenantID, "restaurant")
	foreignAccount, _ := createCommerceStorefrontAccountAndBinding(t, foreignTenantID, foreignDomain.location.ID, "restaurant", "address-foreign-account", "wx-address-foreign")
	foreignLogin := storefrontLogin(t, fixture.service, foreignAccount.AppID, "address-foreign-subject")

	address, err := fixture.service.SaveAddress(login.Token, CommerceStorefrontAddressInput{
		AddressType: "CAMPUS", RecipientName: "同学", Phone: "13800138000", CampusName: "南校区",
		ZoneName: "二食堂", Building: "3号楼", Room: "201", Detail: "宿舍楼下", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("save storefront address: %v", err)
	}
	if address.ID == 0 || address.AddressType != "CAMPUS" || !address.IsDefault {
		t.Fatalf("saved storefront address=%+v", address)
	}
	addresses, err := fixture.service.ListAddresses(login.Token)
	if err != nil || len(addresses) != 1 || addresses[0].Phone != "13800138000" {
		t.Fatalf("list storefront addresses=%+v err=%v", addresses, err)
	}
	if _, err := fixture.service.SetDefaultAddress(foreignLogin.Token, address.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign default-address access error=%v, want not found", err)
	}
	if err := fixture.service.DeleteAddress(foreignLogin.Token, address.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign delete-address access error=%v, want not found", err)
	}

	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add address checkout item: %v", err)
	}
	zone, slot, slotDate := storefrontDeliverySetup(t, fixture)
	quote := storefrontDeliveryQuote(t, fixture, login.Token, address.ID, zone.ID, slot.ID, slotDate)
	if _, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "address-checkout-missing-address", ContactName: "同学", ContactPhone: "13800138000",
		ShippingAddress: "forged", FulfillmentMethod: "delivery", QuoteToken: quote.RawToken,
	}); !errors.Is(err, ErrCommerceStorefrontAddressInvalid) {
		t.Fatalf("checkout without address error=%v, want address validation", err)
	}
	order, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "address-checkout-1", ContactName: "同学", ContactPhone: "13800138000",
		AddressID: address.ID, ShippingAddress: "attacker-controlled-address", FulfillmentMethod: "delivery", QuoteToken: quote.RawToken,
	})
	if err != nil {
		t.Fatalf("checkout with owned address: %v", err)
	}
	if strings.Contains(order.ShippingAddressJSON, "attacker-controlled-address") || !strings.Contains(order.ShippingAddressJSON, "南校区") {
		t.Fatalf("checkout did not snapshot server-owned address: %s", order.ShippingAddressJSON)
	}
	addressRetry, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "address-checkout-1", ContactName: "同学", ContactPhone: "13800138000",
		AddressID: address.ID, ShippingAddress: "different-client-text", FulfillmentMethod: "delivery", QuoteToken: quote.RawToken,
	})
	if err != nil || addressRetry.ID != order.ID {
		t.Fatalf("delivery checkout retry result=%+v err=%v, want original order %d", addressRetry, err, order.ID)
	}
}

func TestCommerceStorefrontRefundStaysRequestedUntilProviderConfirmation(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "refund-subject")
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add storefront refund item: %v", err)
	}
	order, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "storefront-refund-order", ContactName: "Guest", ContactPhone: "13800138000", FulfillmentMethod: "pickup", QuoteToken: storefrontPickupQuote(t, fixture, login.Token).RawToken,
	})
	if err != nil {
		t.Fatalf("create storefront refund order: %v", err)
	}
	orderService := &CommerceOrderService{DB: model.DB, Clock: fixture.service.Now}
	if _, err := orderService.ConfirmPayment(fixture.tenantID, order.ID); err != nil {
		t.Fatalf("confirm storefront refund payment: %v", err)
	}
	if err := model.DB.Model(&model.CommerceStorefrontBinding{}).
		Where("id = ? AND tenant_id = ?", fixture.binding.ID, fixture.tenantID).
		Update("status", "disabled").Error; err != nil {
		t.Fatalf("disable storefront binding before historical refund: %v", err)
	}
	if err := model.DB.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ?", fixture.tenantID, "restaurant").
		Update("status", "suspended").Error; err != nil {
		t.Fatalf("suspend storefront capability before historical refund: %v", err)
	}
	if historical, err := fixture.service.GetOrderByNo(login.Token, order.OrderNo); err != nil || historical.ID != order.ID {
		t.Fatalf("historical order disappeared after publication stopped: order=%+v err=%v", historical, err)
	}
	payment := &CommercePaymentService{DB: model.DB, Storefront: fixture.service, Clock: fixture.service.Now}
	if status, err := payment.GetPaymentStatus(context.Background(), login.Token, order.OrderNo); err != nil || status.OrderNo != order.OrderNo || status.Status != "paid" {
		t.Fatalf("historical payment status disappeared after publication stopped: status=%+v err=%v", status, err)
	}

	var refundInput CommerceStorefrontRefundInput
	if err := json.Unmarshal([]byte(`{"idempotency_key":"storefront-refund-request","reason":"customer request","amount_cents":1}`), &refundInput); err != nil {
		t.Fatalf("decode storefront refund input: %v", err)
	}
	result, err := fixture.service.RequestRefund(login.Token, order.OrderNo, refundInput)
	if err != nil {
		t.Fatalf("request storefront refund: %v", err)
	}
	if result.Request == nil || result.Request.Status != "requested" || result.Request.AmountCents != order.TotalAmountCents {
		t.Fatalf("storefront refund did not derive server amount: %+v", result)
	}
	if _, err := orderService.CompleteRefund(fixture.tenantID, result.Request.ID); !errors.Is(err, ErrCommercePaymentUnavailable) {
		t.Fatalf("ordinary storefront refund completion error=%v, want provider-unavailable", err)
	}

	var persistedRequest model.CommerceAfterSaleRequest
	if err := model.DB.Where("id = ? AND tenant_id = ?", result.Request.ID, fixture.tenantID).First(&persistedRequest).Error; err != nil {
		t.Fatalf("load storefront refund request: %v", err)
	}
	if persistedRequest.Status != "requested" || persistedRequest.ProviderRefundReference != "" || persistedRequest.ProviderRefundAmountCents != 0 {
		t.Fatalf("ordinary refund completion changed provider facts: %+v", persistedRequest)
	}
	var persistedOrder model.CommerceOrder
	if err := model.DB.Where("id = ? AND tenant_id = ?", order.ID, fixture.tenantID).First(&persistedOrder).Error; err != nil {
		t.Fatalf("load storefront refund order: %v", err)
	}
	if persistedOrder.PaymentStatus != "paid" || persistedOrder.RefundStatus != "requested" {
		t.Fatalf("ordinary refund completion changed order state: %+v", persistedOrder)
	}
}

func TestCommerceStorefrontBindingAdminBoundaryValidatesAccountCapabilityAndLocation(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: fixture.tenantID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatalf("seed second storefront capability: %v", err)
	}
	retailDomain := createCommerceBoundaryDomainFixture(t, fixture.tenantID, "retail")
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("secret_ciphertext", "encrypted-secret").Error; err != nil {
		t.Fatalf("seed storefront account credentials: %v", err)
	}

	rows, err := fixture.service.ListBindings(fixture.tenantID, "restaurant")
	if err != nil || len(rows) != 1 || rows[0].ChannelAccountID != fixture.account.ID || !rows[0].CredentialsReady {
		t.Fatalf("list storefront bindings=%+v err=%v", rows, err)
	}
	updated, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ID: fixture.binding.ID, ChannelAccountID: fixture.account.ID, BusinessType: "restaurant",
		LocationID: fixture.domain.location.ID, Status: "active", Reason: "publish campus storefront",
	}, 101, "admin")
	if err != nil {
		t.Fatalf("save storefront binding: %v", err)
	}
	if updated.ID != fixture.binding.ID || updated.Status != "active" || updated.LocationName != fixture.domain.location.Name {
		t.Fatalf("saved storefront binding=%+v", updated)
	}
	var audit model.AuditLog
	if err := model.DB.Where("tenant_id = ? AND action = ? AND target_id = ?", fixture.tenantID, "commerce.storefront_binding.save", fixture.binding.ID).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load storefront binding audit: %v", err)
	}
	if audit.ActorUserID != 101 || audit.Reason != "publish campus storefront" {
		t.Fatalf("storefront binding audit=%+v", audit)
	}

	second, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ChannelAccountID: fixture.account.ID, BusinessType: "retail", LocationID: retailDomain.location.ID,
		Status: "active", Reason: "publish retail storefront on shared miniapp",
	}, 101, "admin")
	if err != nil {
		t.Fatalf("same account retail binding was rejected: %v", err)
	}
	if second.BusinessType != "retail" || second.ChannelAccountID != fixture.account.ID || second.LocationID != retailDomain.location.ID {
		t.Fatalf("same account retail binding=%+v", second)
	}
	if _, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ChannelAccountID: fixture.account.ID, BusinessType: "retail", LocationID: retailDomain.location.ID,
		Status: "active", Reason: "duplicate retail storefront binding",
	}, 101, "admin"); !errors.Is(err, ErrCommerceStorefrontBindingInvalid) {
		t.Fatalf("duplicate same-domain binding error=%v, want invalid binding", err)
	}

	if _, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ID: fixture.binding.ID, ChannelAccountID: fixture.account.ID, BusinessType: "retail",
		LocationID: fixture.domain.location.ID, Status: "active", Reason: "cross-domain attempt",
	}, 101, "admin"); !errors.Is(err, ErrCommerceStorefrontBindingInvalid) {
		t.Fatalf("cross-domain storefront binding error=%v, want invalid binding", err)
	}
	if _, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ID: fixture.binding.ID, ChannelAccountID: fixture.account.ID, BusinessType: "restaurant",
		LocationID: fixture.domain.location.ID, Status: "active", Reason: "missing credentials",
	}, 101, "admin"); err != nil {
		t.Fatalf("existing credentials unexpectedly rejected after seed: %v", err)
	}
	if _, err := fixture.service.SaveBinding(fixture.tenantID, CommerceStorefrontBindingInput{
		ID: fixture.binding.ID, ChannelAccountID: fixture.account.ID, BusinessType: "restaurant",
		LocationID: fixture.domain.location.ID, Status: "active", Reason: "",
	}, 101, "admin"); !errors.Is(err, ErrCommerceStorefrontBindingInvalid) {
		t.Fatalf("empty binding reason error=%v, want invalid binding", err)
	}
}
