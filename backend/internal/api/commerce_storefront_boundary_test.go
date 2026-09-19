package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func migrateCommerceStorefrontOrderTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&model.ChannelAccount{}, &model.CommerceCustomerSession{}, &model.CommerceStorefrontBinding{}, &model.AuditLog{},
		&model.CommerceOrder{}, &model.CommerceOrderItem{}, &model.CommerceAfterSaleRequest{}, &model.CommerceAfterSaleEvent{},
		&model.CommercePaymentReconciliationTask{}, &model.RestaurantFulfillment{}, &model.RetailFulfillment{},
		&model.CommerceAddress{},
	); err != nil {
		t.Fatalf("migrate storefront order tables: %v", err)
	}
}

type commerceStorefrontControllerFixture struct {
	db       *gorm.DB
	tenant   model.Tenant
	product  model.CommerceProduct
	location model.CommerceFulfillmentLocation
	account  model.ChannelAccount
	setNow   func(time.Time)
	now      func() time.Time
	control  *CommerceStorefrontController
}

func newCommerceStorefrontControllerFixture(t *testing.T) commerceStorefrontControllerFixture {
	t.Helper()
	db := openCommerceBoundaryControllerDB(t)
	migrateCommerceStorefrontOrderTables(t, db)
	tenant := createCommerceControllerTenant(t, db, "storefront controller tenant", "restaurant")
	product, location, _ := createCommerceControllerDomainData(t, tenant.ID, "restaurant")
	account := model.ChannelAccount{
		TenantID: tenant.ID, Code: "api-storefront-account", Type: "wechat_miniapp",
		AppID: "wx-api-storefront", Status: "active", Environment: "production",
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create API storefront account: %v", err)
	}
	if err := db.Create(&model.CommerceStorefrontBinding{
		TenantID: tenant.ID, ChannelAccountID: account.ID, BusinessType: "restaurant",
		LocationID: location.ID, Status: "active",
	}).Error; err != nil {
		t.Fatalf("create API storefront binding: %v", err)
	}
	current := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	setNow := func(value time.Time) { current = value }
	now := func() time.Time { return current }
	storefront := service.CommerceStorefrontService{
		DB: db,
		LoginAdapter: service.WechatMiniappLoginFunc(func(_ context.Context, request service.WechatMiniappLoginRequest) (service.WechatMiniappLoginIdentity, error) {
			return service.WechatMiniappLoginIdentity{Subject: request.Code}, nil
		}),
		Now:        now,
		SessionTTL: time.Hour,
	}
	return commerceStorefrontControllerFixture{
		db: db, tenant: tenant, product: product, location: location, account: account,
		setNow: setNow, now: now, control: &CommerceStorefrontController{Service: storefront},
	}
}

func invokeCommerceStorefrontController(t *testing.T, method, path, bearer string, body interface{}, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal storefront controller body: %v", err)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	ctx.Params = params
	handler(ctx)
	return recorder
}

func loginCommerceStorefrontController(t *testing.T, fixture commerceStorefrontControllerFixture, code string) service.CommerceStorefrontLoginResult {
	t.Helper()
	response := invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/session", "", map[string]interface{}{
		"app_id": fixture.account.AppID, "code": code,
	}, nil, fixture.control.Login)
	if response.Code != http.StatusOK {
		t.Fatalf("storefront login status=%d body=%s", response.Code, response.Body.String())
	}
	var result service.CommerceStorefrontLoginResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode storefront login: %v", err)
	}
	if result.Token == "" || result.TenantID != fixture.tenant.ID || result.ChannelAccountID != fixture.account.ID || result.LocationID != fixture.location.ID || result.BusinessType != "restaurant" {
		t.Fatalf("storefront login returned wrong binding: %+v", result)
	}
	return result
}

func TestCommerceStorefrontControllerLoginBearerAndExpiryBoundary(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	login := loginCommerceStorefrontController(t, fixture, "controller-subject")

	response := invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/cart", "", nil, nil, fixture.control.GetCart)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated storefront cart status=%d body=%s, want 401", response.Code, response.Body.String())
	}
	response = invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/cart", login.Token, nil, nil, fixture.control.GetCart)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated storefront cart status=%d body=%s", response.Code, response.Body.String())
	}
	var cart model.CommerceCart
	if err := json.Unmarshal(response.Body.Bytes(), &cart); err != nil {
		t.Fatalf("decode storefront cart: %v", err)
	}
	if cart.TenantID != fixture.tenant.ID || cart.ChannelAccountID != fixture.account.ID || cart.LocationID != fixture.location.ID || cart.CustomerID == "" {
		t.Fatalf("storefront cart was not session-bound: %+v", cart)
	}

	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/cart/items", login.Token, map[string]interface{}{
		"product_id": fixture.product.ID, "sku_id": fixture.product.SKUs[0].ID, "quantity": 1,
		"tenant_id": fixture.tenant.ID + 1000, "customer_id": "attacker", "location_id": 999999,
	}, nil, fixture.control.AddCartItem)
	if response.Code != http.StatusOK {
		t.Fatalf("storefront add-cart status=%d body=%s", response.Code, response.Body.String())
	}

	fixture.setNow(time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC))
	response = invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/cart", login.Token, nil, nil, fixture.control.GetCart)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired storefront cart status=%d body=%s, want 401", response.Code, response.Body.String())
	}
}

func TestCommerceStorefrontControllerFailsClosedForDisabledAccountAndCapability(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	login := loginCommerceStorefrontController(t, fixture, "disabled-account-subject")
	if err := fixture.db.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("status", "disabled").Error; err != nil {
		t.Fatalf("disable API storefront account: %v", err)
	}
	response := invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/cart", login.Token, nil, nil, fixture.control.GetCart)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled storefront account status=%d body=%s, want 503", response.Code, response.Body.String())
	}

	if err := fixture.db.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("status", "active").Error; err != nil {
		t.Fatalf("restore API storefront account: %v", err)
	}
	login = loginCommerceStorefrontController(t, fixture, "suspended-capability-subject")
	if err := fixture.db.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ?", fixture.tenant.ID, "restaurant").Update("status", "suspended").Error; err != nil {
		t.Fatalf("suspend API storefront capability: %v", err)
	}
	response = invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/cart", login.Token, nil, nil, fixture.control.GetCart)
	if response.Code != http.StatusForbidden {
		t.Fatalf("suspended storefront capability status=%d body=%s, want 403", response.Code, response.Body.String())
	}
}

func TestCommerceStorefrontControllerCheckoutAndRefundUseServerFacts(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	login := loginCommerceStorefrontController(t, fixture, "checkout-controller-subject")
	response := invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/cart/items", login.Token, map[string]interface{}{
		"product_id": fixture.product.ID, "sku_id": fixture.product.SKUs[0].ID, "quantity": 1,
	}, nil, fixture.control.AddCartItem)
	if response.Code != http.StatusOK {
		t.Fatalf("storefront controller add-cart status=%d body=%s", response.Code, response.Body.String())
	}

	checkoutBody := map[string]interface{}{
		"tenant_id": fixture.tenant.ID + 1000, "customer_id": "attacker", "business_type": "retail",
		"location_id": 999999, "amount_cents": 1, "idempotency_key": "api-storefront-checkout",
		"contact_name": "Guest", "contact_phone": "13800138000", "fulfillment_method": "pickup",
	}
	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/cart/checkout", login.Token, checkoutBody, nil, fixture.control.Checkout)
	if response.Code != http.StatusCreated {
		t.Fatalf("storefront checkout status=%d body=%s", response.Code, response.Body.String())
	}
	var order model.CommerceOrder
	if err := json.Unmarshal(response.Body.Bytes(), &order); err != nil {
		t.Fatalf("decode storefront checkout: %v", err)
	}
	if order.TenantID != fixture.tenant.ID || order.BusinessType != "restaurant" || order.Channel != "wechat_miniapp" || order.LocationID != fixture.location.ID || order.TotalAmountCents != fixture.product.SKUs[0].PriceCents || order.CustomerID == "attacker" {
		t.Fatalf("storefront controller accepted client-owned checkout facts: %+v", order)
	}

	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/cart/checkout", login.Token, checkoutBody, nil, fixture.control.Checkout)
	if response.Code != http.StatusCreated {
		t.Fatalf("repeated storefront checkout status=%d body=%s", response.Code, response.Body.String())
	}
	var retry model.CommerceOrder
	if err := json.Unmarshal(response.Body.Bytes(), &retry); err != nil {
		t.Fatalf("decode repeated storefront checkout: %v", err)
	}
	if retry.ID != order.ID {
		t.Fatalf("repeated storefront checkout returned order %d, want %d", retry.ID, order.ID)
	}

	orderService := &service.CommerceOrderService{DB: fixture.db, Clock: fixture.now}
	if _, err := orderService.ConfirmPayment(fixture.tenant.ID, order.ID); err != nil {
		t.Fatalf("confirm storefront controller payment: %v", err)
	}
	refundBody := map[string]interface{}{
		"idempotency_key": "api-storefront-refund", "reason": "customer request", "amount_cents": 1,
	}
	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/orders/"+order.OrderNo+"/refund-requests", login.Token, refundBody, gin.Params{{Key: "orderNo", Value: order.OrderNo}}, fixture.control.RequestRefund)
	if response.Code != http.StatusAccepted {
		t.Fatalf("storefront refund request status=%d body=%s, want 202", response.Code, response.Body.String())
	}
	var refund service.CommerceRefundResult
	if err := json.Unmarshal(response.Body.Bytes(), &refund); err != nil {
		t.Fatalf("decode storefront refund: %v", err)
	}
	if refund.Request == nil || refund.Request.Status != "requested" || refund.Request.AmountCents != order.TotalAmountCents {
		t.Fatalf("storefront controller accepted forged refund amount: %+v", refund)
	}
}

func TestCommerceStorefrontControllerAddressRoutesAreSessionScoped(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	login := loginCommerceStorefrontController(t, fixture, "address-controller-subject")

	response := invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/addresses", login.Token, nil, nil, fixture.control.ListAddresses)
	if response.Code != http.StatusOK {
		t.Fatalf("empty storefront address list status=%d body=%s", response.Code, response.Body.String())
	}
	var empty struct {
		Data []service.CommerceStorefrontAddressView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty storefront addresses: %v", err)
	}
	if empty.Data == nil || len(empty.Data) != 0 {
		t.Fatalf("empty storefront address list=%+v", empty.Data)
	}

	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/addresses", login.Token, map[string]interface{}{
		"address_type": "CAMPUS", "recipient_name": "同学", "phone": "13800138000",
		"campus_name": "南校区", "zone_name": "二食堂", "building": "3号楼", "room": "201",
		"detail": "宿舍楼下", "is_default": true,
	}, nil, fixture.control.SaveAddress)
	if response.Code != http.StatusOK {
		t.Fatalf("create storefront address status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data service.CommerceStorefrontAddressView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created storefront address: %v", err)
	}
	if created.Data.ID == 0 || created.Data.AddressType != "CAMPUS" || !created.Data.IsDefault || created.Data.CampusName != "南校区" {
		t.Fatalf("created storefront address=%+v", created.Data)
	}

	addressID := strconv.FormatUint(uint64(created.Data.ID), 10)
	response = invokeCommerceStorefrontController(t, http.MethodPut, "/storefront/wechat/addresses/"+addressID, login.Token, map[string]interface{}{
		"address_type": "CAMPUS", "recipient_name": "同学2", "phone": "13800138001",
		"campus_name": "北校区", "zone_name": "三食堂", "building": "4号楼", "room": "302", "detail": "新地址", "is_default": false,
	}, gin.Params{{Key: "addressID", Value: addressID}}, fixture.control.SaveAddress)
	if response.Code != http.StatusOK {
		t.Fatalf("update storefront address status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode updated storefront address: %v", err)
	}
	if created.Data.RecipientName != "同学2" || created.Data.Phone != "13800138001" || created.Data.CampusName != "北校区" {
		t.Fatalf("updated storefront address=%+v", created.Data)
	}

	response = invokeCommerceStorefrontController(t, http.MethodPost, "/storefront/wechat/addresses/"+addressID+"/default", login.Token, nil, gin.Params{{Key: "addressID", Value: addressID}}, fixture.control.SetDefaultAddress)
	if response.Code != http.StatusOK {
		t.Fatalf("set default storefront address status=%d body=%s", response.Code, response.Body.String())
	}
	response = invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/addresses", login.Token, nil, nil, fixture.control.ListAddresses)
	if response.Code != http.StatusOK {
		t.Fatalf("reload storefront addresses status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode reloaded storefront addresses: %v", err)
	}
	if len(empty.Data) != 1 || !empty.Data[0].IsDefault || empty.Data[0].RecipientName != "同学2" {
		t.Fatalf("reloaded storefront addresses=%+v", empty.Data)
	}

	response = invokeCommerceStorefrontController(t, http.MethodDelete, "/storefront/wechat/addresses/"+addressID, login.Token, nil, gin.Params{{Key: "addressID", Value: addressID}}, fixture.control.DeleteAddress)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete storefront address status=%d body=%s", response.Code, response.Body.String())
	}
	response = invokeCommerceStorefrontController(t, http.MethodGet, "/storefront/wechat/addresses", login.Token, nil, nil, fixture.control.ListAddresses)
	if response.Code != http.StatusOK {
		t.Fatalf("final storefront address list status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode final storefront addresses: %v", err)
	}
	if len(empty.Data) != 0 {
		t.Fatalf("deleted storefront address remained visible=%+v", empty.Data)
	}
}

func TestCommerceStorefrontAdminBindingControllerUsesTenantScope(t *testing.T) {
	fixture := newCommerceStorefrontControllerFixture(t)
	if err := fixture.db.Model(&model.ChannelAccount{}).Where("id = ?", fixture.account.ID).Update("secret_ciphertext", "encrypted-secret").Error; err != nil {
		t.Fatalf("seed storefront account credentials: %v", err)
	}

	response := invokeCommerceController(t, http.MethodGet, "/commerce/storefront-bindings?business_type=restaurant", fixture.tenant.ID, nil, nil, fixture.control.ListBindings)
	if response.Code != http.StatusOK {
		t.Fatalf("list storefront bindings status=%d body=%s", response.Code, response.Body.String())
	}
	var listed struct {
		Data []service.CommerceStorefrontBindingView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode storefront bindings: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].TenantID != fixture.tenant.ID || !listed.Data[0].CredentialsReady {
		t.Fatalf("listed storefront bindings=%+v", listed.Data)
	}

	response = invokeCommerceController(t, http.MethodGet, "/commerce/storefront-channels", fixture.tenant.ID, nil, nil, fixture.control.ListChannelAccounts)
	if response.Code != http.StatusOK {
		t.Fatalf("list storefront channels status=%d body=%s", response.Code, response.Body.String())
	}
	var channels struct {
		Data []service.CommerceStorefrontChannelView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &channels); err != nil {
		t.Fatalf("decode storefront channels: %v", err)
	}
	if len(channels.Data) != 1 || channels.Data[0].ID != fixture.account.ID || !channels.Data[0].CredentialsReady {
		t.Fatalf("listed storefront channels=%+v", channels.Data)
	}

	response = invokeCommerceController(t, http.MethodPut, "/commerce/storefront-bindings/"+strconv.FormatUint(uint64(listed.Data[0].ID), 10), fixture.tenant.ID, map[string]interface{}{
		"channel_account_id": fixture.account.ID, "business_type": "restaurant", "location_id": fixture.location.ID,
		"status": "active", "reason": "confirm storefront binding",
	}, gin.Params{{Key: "bindingID", Value: strconv.FormatUint(uint64(listed.Data[0].ID), 10)}}, fixture.control.SaveBinding)
	if response.Code != http.StatusOK {
		t.Fatalf("save storefront binding status=%d body=%s", response.Code, response.Body.String())
	}
	var saved struct {
		Data service.CommerceStorefrontBindingView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved storefront binding: %v", err)
	}
	if saved.Data.ID != listed.Data[0].ID || saved.Data.LocationID != fixture.location.ID || saved.Data.Status != "active" {
		t.Fatalf("saved storefront binding=%+v", saved.Data)
	}
}
