package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

func TestXiaohongshuMiniappLoginAndCatalogAreChannelScoped(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-storefront", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-storefront", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{
		ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-TICKET-1",
		DisplayName: "小红书成人票", ChannelSaleCents: 7900,
	}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "XHS-SKU-1", CategoryID: "ticket", ImageURL: "https://example.com/ticket.png", Description: "景区门票", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "synced"}).Error; err != nil {
		t.Fatal(err)
	}

	otherTenantID, otherProductID := seedSellableProduct(t, "unlimited", 0)
	otherAccount := model.ChannelAccount{Code: "xiaohongshu-other"}
	if err := (&ChannelService{}).CreateXiaohongshu(otherTenantID, &otherAccount, "miniapp-other", "other-secret"); err != nil {
		t.Fatal(err)
	}
	if err := (&ChannelService{}).AddMapping(otherTenantID, &model.ChannelProductMapping{
		ChannelAccountID: otherAccount.ID, ProductID: otherProductID, ExternalCode: "XHS-OTHER",
		DisplayName: "其他景区票", ChannelSaleCents: 9900,
	}); err != nil {
		t.Fatal(err)
	}

	server := miniappLoginServer(t)
	defer server.Close()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	miniapp := NewMiniappService()
	miniapp.Now = func() time.Time { return now }
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		if appID != "miniapp-storefront" || secret != "app-secret" {
			t.Fatalf("appid=%q secret=%q", appID, secret)
		}
		if environment != "sandbox" {
			t.Fatalf("environment=%q", environment)
		}
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}

	login, err := miniapp.LoginXiaohongshu(context.Background(), "miniapp-storefront", "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || !login.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("login=%+v", login)
	}
	var customer model.MiniappCustomer
	if err := model.DB.First(&customer).Error; err != nil {
		t.Fatal(err)
	}
	if customer.TenantID != tenantID || customer.ChannelAccountID != account.ID ||
		strings.Contains(customer.OpenIDCiphertext, "OPEN-1") || strings.Contains(customer.SessionKeyCiphertext, "SESSION-1") ||
		customer.SessionTokenHash == login.Token {
		t.Fatalf("customer=%+v", customer)
	}
	if plain, err := utils.DecryptAES(customer.SessionKeyCiphertext); err != nil || plain != "SESSION-1" {
		t.Fatalf("session key=%q err=%v", plain, err)
	}

	authenticated, err := miniapp.Authenticate(login.Token)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := miniapp.ListCatalog(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Products) != 1 || catalog.Products[0].Name != "小红书成人票" || catalog.Products[0].PriceCents != 7900 ||
		catalog.Products[0].ImageURL != "https://example.com/ticket.png" || catalog.Products[0].Description == "" || catalog.Products[0].ProductType != 1 {
		t.Fatalf("catalog=%+v", catalog)
	}
}

func TestXiaohongshuMiniappSessionFailsClosedWhenChannelIsDisabled(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-disabled"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-disabled", "app-secret"); err != nil {
		t.Fatal(err)
	}
	server := miniappLoginServer(t)
	defer server.Close()
	miniapp := NewMiniappService()
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, ComponentAppID: "provider-app", ComponentAccessToken: "authorized-miniapp-token", BaseURL: server.URL, HTTP: server.Client()}
	}
	login, err := miniapp.LoginXiaohongshu(context.Background(), "miniapp-disabled", "login-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&ChannelService{}).SetStatus(tenantID, account.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := miniapp.Authenticate(login.Token); err != ErrMiniappUnavailable {
		t.Fatalf("error=%v", err)
	}
}

func TestXiaohongshuMiniappOrderConvergesFromOfficialPaymentQuery(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-order", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-order", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-P-1", DisplayName: "测试票", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "XHS-SKU-1", CategoryID: "ticket", ImageURL: "https://example.com/ticket.png", Description: "景区门票", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "synced"}).Error; err != nil {
		t.Fatal(err)
	}
	openID, _ := utils.EncryptAES("OPEN-ORDER")
	sessionKey, _ := utils.EncryptAES("SESSION-ORDER")
	customer := model.MiniappCustomer{TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("OPEN-ORDER"), OpenIDCiphertext: openID, SessionKeyCiphertext: sessionKey, SessionTokenHash: hashMiniappValue("TOKEN-ORDER"), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS-ORDER","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/upsert":
			if r.URL.Query().Get("app_id") != "miniapp-order" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			var request xiaohongshu.OrderUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.OpenID != "OPEN-ORDER" || request.Price.OrderPrice != 2 || len(request.Products) != 1 || request.Products[0].Count != 2 {
				t.Fatalf("request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"XHS-LOCAL","order_id":"XHS-PLATFORM-1","final_price":2,"pay_token":"PAY-TOKEN-1","expired_time":1786349700,"open_pay_type":"life_gpay"},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/gpay_order/get":
			_, _ = w.Write([]byte(`{"data":{"order_id":"XHS-PLATFORM-1","pay_amount":2,"order_status":6,"voucher_infos":[{"voucher_code":"VOUCHER-1","voucher_status":1,"pay_amount":1},{"voucher_code":"VOUCHER-2","voucher_status":1,"pay_amount":1}],"third_trade_no":"TRADE-1","pay_channel":1},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	miniapp := NewMiniappService()
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, ComponentAppID: "provider-app", ComponentAccessToken: "authorized-miniapp-token", BaseURL: server.URL, HTTP: server.Client()}
	}
	created, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 2, ClientRequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.OrderNo == "" || created.PlatformOrderID != "XHS-PLATFORM-1" || created.PayToken != "PAY-TOKEN-1" || created.AmountCents != 2 || created.Status != "unpaid" {
		t.Fatalf("created=%+v", created)
	}
	var recoveredOrder model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", created.OrderNo, tenantID).First(&recoveredOrder).Error; err != nil {
		t.Fatal(err)
	}
	var recoveredLink model.XiaohongshuOrderLink
	if err := model.DB.Where("order_id = ? AND tenant_id = ?", recoveredOrder.ID, tenantID).First(&recoveredLink).Error; err != nil {
		t.Fatal(err)
	}
	var recoveredOperation model.XiaohongshuOrderOperation
	if err := model.DB.Where("xiaohongshu_order_link_id = ? AND tenant_id = ?", recoveredLink.ID, tenantID).First(&recoveredOperation).Error; err != nil {
		t.Fatal(err)
	}
	payTokenCiphertext, err := utils.EncryptAES("PAY-TOKEN-RECOVERED")
	if err != nil {
		t.Fatal(err)
	}
	recoveredExpiry := time.Date(2026, 8, 10, 13, 0, 0, 0, time.Local)
	if err := model.DB.Model(&recoveredLink).Updates(map[string]interface{}{
		"state": "creating", "platform_order_id": "", "pay_token_ciphertext": "", "pay_token_expires_at": nil, "last_queried_at": time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&recoveredOperation).Updates(map[string]interface{}{
		"status": "remote_succeeded", "platform_order_id": "XHS-PLATFORM-1", "pay_token_ciphertext": payTokenCiphertext,
		"pay_token_expires_at": recoveredExpiry, "next_attempt_at": time.Now().Add(-time.Second), "completed_at": nil, "last_error": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if processed, err := miniapp.ProcessPendingXiaohongshuOrders(context.Background(), time.Now(), 20); err != nil || processed != 1 {
		t.Fatalf("recovered operation processed=%d err=%v", processed, err)
	}
	if err := model.DB.First(&recoveredLink, recoveredLink.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recoveredLink.State != "unpaid" || recoveredLink.PlatformOrderID != "XHS-PLATFORM-1" {
		t.Fatalf("recovered link=%+v", recoveredLink)
	}
	if err := model.DB.First(&recoveredOperation, recoveredOperation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recoveredOperation.Status != "completed" || recoveredOperation.CompletedAt == nil {
		t.Fatalf("recovered operation=%+v", recoveredOperation)
	}
	replayed, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 2, ClientRequestID: "request-1"})
	if err != nil || replayed.OrderNo != created.OrderNo {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	paid, err := miniapp.GetXiaohongshuOrder(context.Background(), &customer, created.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Status != "paid" || len(paid.TicketCodes) != 2 {
		t.Fatalf("paid=%+v", paid)
	}
	if paid.CoreOrderStatus != "paid" || paid.PlatformPaymentState != "paid" {
		t.Fatalf("paid order state contract=%+v", paid)
	}
	if _, err := (&RefundService{}).CreateDigitalRefund(tenantID, created.OrderNo, "xhs-money-refund-must-fail", 0.02, paid.TicketCodes, "customer request"); err == nil || !strings.Contains(err.Error(), "paid digital payment not found") {
		t.Fatalf("xiaohongshu money refund was not fail-closed: %v", err)
	}
	orders, err := miniapp.ListXiaohongshuOrders(&customer, 0, 99)
	if err != nil {
		t.Fatal(err)
	}
	if orders.Page != 1 || orders.PageSize != 10 || orders.Total != 1 || len(orders.Items) != 1 ||
		orders.Items[0].OrderNo != created.OrderNo || orders.Items[0].Quantity != 2 || orders.Items[0].AmountCents != 2 ||
		orders.Items[0].Status != "paid" || orders.Items[0].CoreOrderStatus != "paid" || orders.Items[0].PlatformPaymentState != "paid" || orders.Items[0].ImageURL != "https://example.com/ticket.png" {
		t.Fatalf("orders=%+v", orders)
	}
	var order model.Order
	if err := model.DB.Where("order_no = ?", created.OrderNo).First(&order).Error; err != nil || order.Status != "paid" || moneyCents(order.TotalAmount) != 2 {
		t.Fatalf("order=%+v err=%v", order, err)
	}
	var payment model.Payment
	if err := model.DB.Where("order_no = ?", order.OrderNo).First(&payment).Error; err != nil || payment.Method != "xiaohongshu" || payment.Status != "paid" || payment.AmountCents != 2 || payment.TransactionID != "TRADE-1" {
		t.Fatalf("payment=%+v err=%v", payment, err)
	}
	var vouchers []model.XiaohongshuVoucherLink
	if err := model.DB.Where("tenant_id = ?", tenantID).Find(&vouchers).Error; err != nil || len(vouchers) != 2 || strings.Contains(vouchers[0].VoucherCodeCiphertext, "VOUCHER") {
		t.Fatalf("vouchers=%+v err=%v", vouchers, err)
	}
	other := customer
	other.ID++
	otherOrders, err := miniapp.ListXiaohongshuOrders(&other, 1, 10)
	if err != nil || otherOrders.Total != 0 || len(otherOrders.Items) != 0 {
		t.Fatalf("cross customer orders=%+v err=%v", otherOrders, err)
	}
	if _, err := miniapp.GetXiaohongshuOrder(context.Background(), &other, created.OrderNo); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross customer error=%v", err)
	}
}

func TestXiaohongshuSandboxOrderLimitIsServerEnforced(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xiaohongshu-limit", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "miniapp-limit", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-LIMIT", ChannelSaleCents: 6}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "SKU", CategoryID: "ticket", ImageURL: "https://example.com/ticket.png", Description: "票", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "synced"}).Error; err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{Base: model.Base{ID: 99}, TenantID: tenantID, ChannelAccountID: account.ID}
	if _, err := (NewMiniappService()).CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 2, ClientRequestID: "too-much"}); err == nil || !strings.Contains(err.Error(), "0.10") {
		t.Fatalf("error=%v", err)
	}
	var orders int64
	if err := model.DB.Model(&model.Order{}).Where("channel_account_id = ?", account.ID).Count(&orders).Error; err != nil || orders != 0 {
		t.Fatalf("orders=%d err=%v", orders, err)
	}
}

func TestXiaohongshuMiniappScenicHotelPackageRequiresStayDateAndCreatesReservation(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 3)
	account := model.ChannelAccount{Code: "xiaohongshu-package", Status: "active"}
	if err := (&ChannelService{}).CreateXiaohongshu(fixture.tenantID, &account, "miniapp-package", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: fixture.productID, ExternalCode: "XHS-PACKAGE-1", DisplayName: "门票住宿套餐", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(fixture.tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: fixture.tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "XHS-PACKAGE-SKU-1", CategoryID: "package", ImageURL: "https://example.com/package.png", Description: "门票与住宿", ProductPath: "/pages/product/detail", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "synced"}).Error; err != nil {
		t.Fatal(err)
	}
	openID, _ := utils.EncryptAES("OPEN-PACKAGE")
	sessionKey, _ := utils.EncryptAES("SESSION-PACKAGE")
	customer := model.MiniappCustomer{TenantID: fixture.tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("OPEN-PACKAGE"), OpenIDCiphertext: openID, SessionKeyCiphertext: sessionKey, SessionTokenHash: hashMiniappValue("TOKEN-PACKAGE"), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}

	miniapp := NewMiniappService()
	catalog, err := miniapp.ListCatalog(&customer)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Products) != 1 || catalog.Products[0].ProductKind != "scenic_hotel_package" || !catalog.Products[0].RequiresUseDate || catalog.Products[0].HotelName != fixture.hotel.Name || catalog.Products[0].Nights != 2 {
		t.Fatalf("package catalog=%+v", catalog)
	}
	if _, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 1, ClientRequestID: "package-without-date"}); err == nil || !strings.Contains(err.Error(), "入住日期") {
		t.Fatalf("missing stay date error=%v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS-PACKAGE","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/upsert":
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"XHS-PACKAGE","order_id":"XHS-PACKAGE-ORDER","final_price":1,"pay_token":"PACKAGE-PAY-TOKEN","expired_time":1786349700,"open_pay_type":"life_gpay"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, ComponentAppID: "provider-app", ComponentAccessToken: "authorized-miniapp-token", BaseURL: server.URL, HTTP: server.Client()}
	}
	created, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 1, ClientRequestID: "package-with-date", UseDate: fixture.checkIn.Format("2006-01-02"), GuestName: "测试游客", ContactPhone: "13800138000"})
	if err != nil {
		t.Fatal(err)
	}
	if created.OrderNo == "" || created.PlatformOrderID != "XHS-PACKAGE-ORDER" {
		t.Fatalf("created=%+v", created)
	}
	var reservation model.HotelReservation
	if err := model.DB.Where("sales_tenant_id = ? AND reservation_no <> ''", fixture.tenantID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if reservation.Status != "reserved" || reservation.CheckInDate.Format("2006-01-02") != fixture.checkIn.Format("2006-01-02") || reservation.CheckOutDate.Sub(reservation.CheckInDate) != 48*time.Hour {
		t.Fatalf("reservation=%+v", reservation)
	}
	detail, err := miniapp.loadOrderResult(&customer, "package-with-date")
	if err != nil {
		t.Fatal(err)
	}
	if detail.ProductKind != "scenic_hotel_package" || detail.HotelStay == nil || detail.HotelStay.HotelName != fixture.hotel.Name || detail.HotelStay.Rooms != 1 || detail.HotelStay.GuestName != "测试游客" || detail.HotelStay.ContactPhone != "13800138000" {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestXiaohongshuMiniappDeferredPackageBooksIdempotentlyAndCancels(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 3)
	if err := model.DB.Model(&model.ScenicHotelPackage{}).Where("id = ?", fixture.packageView.ID).Updates(map[string]interface{}{
		"booking_mode": "after_purchase", "voucher_validity_days": 90,
		"min_advance_days": 1, "max_reschedules": 2,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := model.ChannelAccount{Code: "xiaohongshu-deferred-package", Status: "active"}
	if err := (&ChannelService{}).CreateXiaohongshu(fixture.tenantID, &account, "miniapp-deferred-package", "app-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: fixture.productID, ExternalCode: "XHS-DEFERRED-PACKAGE", DisplayName: "先购后约酒景套餐", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(fixture.tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	poiJSON, _ := json.Marshal([]string{"POI-1"})
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "XHS-DEFERRED-SKU", CategoryID: "package", POIIDsJSON: string(poiJSON),
		ImageURL: "https://example.com/package.png", Description: "购买后预约入住",
		ProductPath: "/pages/product/detail", OrderPath: "/pages/order/detail",
		ProductType: xiaohongshu.ProductTypePresaleVoucher, SettleType: 1, SyncStatus: "synced",
	}).Error; err != nil {
		t.Fatal(err)
	}
	openID, _ := utils.EncryptAES("OPEN-DEFERRED-PACKAGE")
	sessionKey, _ := utils.EncryptAES("SESSION-DEFERRED-PACKAGE")
	customer := model.MiniappCustomer{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID,
		OpenIDHash: hashMiniappValue("OPEN-DEFERRED-PACKAGE"), OpenIDCiphertext: openID,
		SessionKeyCiphertext: sessionKey, SessionTokenHash: hashMiniappValue("TOKEN-DEFERRED-PACKAGE"),
		SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}

	bookCalls, confirmCalls, cancelCalls, compensationCalls, refundCalls := 0, 0, 0, 0, 0
	var confirmExternalIDs []string
	cancelShouldFail := true
	confirmShouldFail, breakLocalFinalize, refundShouldFail := false, false, false
	bookStarted := make(chan struct{}, 1)
	releaseInitialBook := make(chan struct{})
	var callsMu sync.Mutex
	var blockBookOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS-DEFERRED","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/upsert":
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"XHS-DEFERRED","order_id":"XHS-DEFERRED-ORDER","final_price":1,"pay_token":"DEFERRED-PAY","expired_time":1786349700,"open_pay_type":"life_gpay"},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/gpay_order/get":
			_, _ = w.Write([]byte(`{"data":{"order_id":"XHS-DEFERRED-ORDER","pay_amount":1,"order_status":6,"voucher_infos":[{"voucher_code":"VOUCHER-DEFERRED","voucher_status":1,"pay_amount":1}],"third_trade_no":"TRADE-DEFERRED","pay_channel":1},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/component/deal/pre_sale/book":
			callsMu.Lock()
			bookCalls++
			callsMu.Unlock()
			var request xiaohongshu.PresaleBookRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ProductType != xiaohongshu.ProductTypePresaleVoucher || request.OpenID != "OPEN-DEFERRED-PACKAGE" || request.POIID != "POI-1" || len(request.BookInfo.Details) != 1 || request.BookInfo.Details[0].VoucherCode != "VOUCHER-DEFERRED" || request.BookInfo.Details[0].CheckInDate != fixture.checkIn.Format("2006-01-02") {
				t.Fatalf("booking request=%+v", request)
			}
			blockBookOnce.Do(func() {
				bookStarted <- struct{}{}
				<-releaseInitialBook
			})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"out_order_id": request.ExternalOrderID,
					"book_result":  []map[string]string{{"book_id": "PLATFORM-BOOK-DEFERRED", "voucher_code": "VOUCHER-DEFERRED"}},
				},
				"success": true, "msg": "success", "code": 0,
			})
		case "/api/rmp/component/deal/pre_sale/sync_status":
			var request xiaohongshu.PresaleBookStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			switch request.Status {
			case 1:
				confirmCalls++
				confirmExternalIDs = append(confirmExternalIDs, request.ExternalBookOrderID)
				if confirmShouldFail {
					_, _ = w.Write([]byte(`{"data":null,"success":false,"msg":"temporary confirm failure","code":50003}`))
					return
				}
				if breakLocalFinalize {
					var prepared model.ScenicHotelPackageEntitlement
					if err := model.DB.Where("status = ? AND external_book_order_id = ?", "booking_pending", request.ExternalBookOrderID).First(&prepared).Error; err != nil {
						t.Errorf("load prepared entitlement: %v", err)
					} else if err := model.DB.Model(&model.Ticket{}).Where("id = ?", prepared.TicketID).Update("status", "used").Error; err != nil {
						t.Errorf("break local finalize: %v", err)
					}
				}
			case 2:
				compensationCalls++
			case 3:
				cancelCalls++
				if cancelShouldFail {
					_, _ = w.Write([]byte(`{"data":null,"success":false,"msg":"temporary revoke failure","code":50001}`))
					return
				}
			case 4:
				refundCalls++
				if refundShouldFail {
					_, _ = w.Write([]byte(`{"data":null,"success":false,"msg":"temporary refund notification failure","code":50004}`))
					return
				}
			default:
				t.Fatalf("unexpected booking status=%d", request.Status)
			}
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	miniapp := NewMiniappService()
	var clockMu sync.Mutex
	clock := time.Now()
	miniapp.Now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, ComponentAppID: "provider-app", ComponentAccessToken: "authorized-miniapp-token", BaseURL: server.URL, HTTP: server.Client()}
	}

	catalog, err := miniapp.ListCatalog(&customer)
	if err != nil || len(catalog.Products) != 1 || catalog.Products[0].RequiresUseDate || catalog.Products[0].BookingMode != "after_purchase" {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	created, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 1, ClientRequestID: "deferred-order"})
	if err != nil {
		t.Fatal(err)
	}
	paid, err := miniapp.GetXiaohongshuOrder(context.Background(), &customer, created.OrderNo)
	if err != nil || paid.Status != "paid" || len(paid.TicketCodes) != 0 || len(paid.PackageEntitlements) != 1 || paid.PackageEntitlements[0].Status != "pending_booking" {
		t.Fatalf("paid=%+v err=%v", paid, err)
	}
	entitlementNo := paid.PackageEntitlements[0].EntitlementNo
	booking := MiniappPackageBookingInput{EntitlementNo: entitlementNo, CheckInDate: fixture.checkIn.Format("2006-01-02"), GuestName: "预约游客", ContactPhone: "13800138000", ClientRequestID: "booking-1"}
	type bookingResult struct {
		result *MiniappOrderResult
		err    error
	}
	firstBooking := make(chan bookingResult, 1)
	go func() {
		result, bookingErr := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking)
		firstBooking <- bookingResult{result: result, err: bookingErr}
	}()
	select {
	case <-bookStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("initial platform booking request did not start")
	}
	clockMu.Lock()
	clock = clock.Add(31 * time.Second)
	clockMu.Unlock()
	secondBooking := make(chan bookingResult, 1)
	go func() {
		result, bookingErr := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking)
		secondBooking <- bookingResult{result: result, err: bookingErr}
	}()
	select {
	case concurrent := <-secondBooking:
		if concurrent.err != nil {
			t.Fatalf("concurrent idempotent booking error=%v", concurrent.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent idempotent booking was blocked or sent a duplicate platform request")
	}
	callsMu.Lock()
	initialBookCalls := bookCalls
	callsMu.Unlock()
	if initialBookCalls != 1 {
		t.Fatalf("concurrent workers sent %d platform booking requests", initialBookCalls)
	}
	close(releaseInitialBook)
	initial := <-firstBooking
	if initial.err != nil {
		t.Fatal(initial.err)
	}
	booked, err := miniapp.GetXiaohongshuOrder(context.Background(), &customer, created.OrderNo)
	if err != nil || len(booked.TicketCodes) != 1 || len(booked.PackageEntitlements) != 1 || booked.PackageEntitlements[0].Status != "booked" || booked.PackageEntitlements[0].GuestName != "预约游客" || bookCalls != 1 || confirmCalls != 1 {
		t.Fatalf("booked=%+v calls=%d/%d err=%v", booked, bookCalls, confirmCalls, err)
	}
	if _, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking); err != nil || bookCalls != 1 || confirmCalls != 1 {
		t.Fatalf("idempotent calls=%d/%d err=%v", bookCalls, confirmCalls, err)
	}
	var bookedEntitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("entitlement_no = ?", entitlementNo).First(&bookedEntitlement).Error; err != nil {
		t.Fatal(err)
	}
	exhaustedAt := time.Now().Add(-time.Minute)
	exhausted := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: 1,
		EntitlementID: bookedEntitlement.ID, OperationKey: "xhs:book:invalid-payload-test", Type: "book", Status: "pending",
		ExternalBookOrderID: bookedEntitlement.ExternalBookOrderID, PlatformBookID: bookedEntitlement.PlatformBookID,
		RequestPayloadCiphertext: "invalid-ciphertext", Attempts: 0, MaxAttempts: 1, NextAttemptAt: &exhaustedAt,
	}
	var xhsLink model.XiaohongshuOrderLink
	if err := model.DB.Where("order_id = ?", bookedEntitlement.OrderID).First(&xhsLink).Error; err != nil {
		t.Fatal(err)
	}
	exhausted.OrderLinkID = xhsLink.ID
	if err := model.DB.Create(&exhausted).Error; err != nil {
		t.Fatal(err)
	}
	processed, err := miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 0 || cancelCalls != 0 {
		t.Fatalf("invalid payload worker processed=%d cancelCalls=%d err=%v", processed, cancelCalls, err)
	}
	if err := model.DB.First(&exhausted, exhausted.ID).Error; err != nil {
		t.Fatal(err)
	}
	if exhausted.Status != "pending" || exhausted.Attempts != 1 || exhausted.LastError == "" {
		t.Fatalf("invalid payload operation did not consume an attempt: %+v", exhausted)
	}
	if err := model.DB.Model(&exhausted).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 1 || cancelCalls != 0 {
		t.Fatalf("exhausted worker processed=%d cancelCalls=%d err=%v", processed, cancelCalls, err)
	}
	if err := model.DB.First(&exhausted, exhausted.ID).Error; err != nil {
		t.Fatal(err)
	}
	if exhausted.Status != "failed" || exhausted.Attempts != exhausted.MaxAttempts {
		t.Fatalf("exhausted operation=%+v", exhausted)
	}
	if err := model.DB.Where("id = ?", bookedEntitlement.ID).First(&bookedEntitlement).Error; err != nil {
		t.Fatal(err)
	}
	if bookedEntitlement.Status != "booked" || bookedEntitlement.PlatformBookID == "" || bookedEntitlement.ReservationID == 0 {
		t.Fatalf("retry exhaustion released remote facts: %+v", bookedEntitlement)
	}
	if err := model.DB.Delete(&exhausted).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&bookedEntitlement).Updates(map[string]interface{}{"platform_sync_status": "synced", "platform_sync_error": ""}).Error; err != nil {
		t.Fatal(err)
	}
	cancelled, err := miniapp.CancelXiaohongshuPackageBooking(context.Background(), &customer, created.OrderNo, entitlementNo)
	if err != nil || len(cancelled.TicketCodes) != 0 || len(cancelled.PackageEntitlements) != 1 || cancelled.PackageEntitlements[0].Status != "cancel_pending" || cancelCalls != 1 {
		t.Fatalf("cancelled=%+v calls=%d err=%v", cancelled, cancelCalls, err)
	}
	var cancelling model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("entitlement_no = ?", entitlementNo).First(&cancelling).Error; err != nil {
		t.Fatal(err)
	}
	if cancelling.PlatformBookID != "PLATFORM-BOOK-DEFERRED" || cancelling.ExternalBookOrderID == "" || cancelling.ReservationID == 0 {
		t.Fatalf("cancel-pending entitlement lost remote or reserved facts: %+v", cancelling)
	}
	rebook := booking
	rebook.ClientRequestID = "booking-2"
	if _, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, rebook); err == nil || !strings.Contains(err.Error(), "正在取消") {
		t.Fatalf("rebook during cancellation error=%v", err)
	}
	var stillCancelling model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("id = ?", cancelling.ID).First(&stillCancelling).Error; err != nil {
		t.Fatal(err)
	}
	if stillCancelling.PlatformBookID != cancelling.PlatformBookID || stillCancelling.ExternalBookOrderID != cancelling.ExternalBookOrderID || stillCancelling.Status != "cancel_pending" {
		t.Fatalf("rebook overwrote pending cancellation: before=%+v after=%+v", cancelling, stillCancelling)
	}

	cancelShouldFail = false
	if err := model.DB.Model(&model.XiaohongshuBookingOperation{}).Where("entitlement_id = ? AND type = ?", cancelling.ID, "revoke").Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 1 || cancelCalls != 2 {
		t.Fatalf("worker processed=%d cancelCalls=%d err=%v", processed, cancelCalls, err)
	}
	var released model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("id = ?", cancelling.ID).First(&released).Error; err != nil {
		t.Fatal(err)
	}
	if released.Status != "pending_booking" || released.ReservationID != 0 || released.PlatformBookID != "" || released.ExternalBookOrderID != "" {
		t.Fatalf("completed revoke did not release entitlement: %+v", released)
	}
	var order model.Order
	if err := model.DB.Where("order_no = ?", created.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	var reservations []model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).Find(&reservations).Error; err != nil || len(reservations) != 1 || reservations[0].Status != "cancelled" {
		t.Fatalf("reservations=%+v err=%v", reservations, err)
	}
	if _, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking); err == nil || !strings.Contains(err.Error(), "历史预约") {
		t.Fatalf("reusing a completed booking request id error=%v", err)
	}
	var unchangedAfterReplay model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("id = ?", released.ID).First(&unchangedAfterReplay).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedAfterReplay.Status != "pending_booking" || unchangedAfterReplay.ReservationID != 0 || unchangedAfterReplay.ExternalBookOrderID != "" || unchangedAfterReplay.PlatformBookID != "" || bookCalls != 1 {
		t.Fatalf("historical request replay changed booking facts: entitlement=%+v bookCalls=%d", unchangedAfterReplay, bookCalls)
	}
	var reservationCount int64
	if err := model.DB.Model(&model.HotelReservation{}).Where("order_id = ?", order.ID).Count(&reservationCount).Error; err != nil {
		t.Fatal(err)
	}
	if reservationCount != 1 {
		t.Fatalf("historical request replay created %d reservations", reservationCount)
	}

	confirmShouldFail = true
	failedConfirmBooking := booking
	failedConfirmBooking.ClientRequestID = "booking-confirm-retry"
	processing, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, failedConfirmBooking)
	if err != nil || len(processing.TicketCodes) != 0 || len(processing.PackageEntitlements) != 1 || processing.PackageEntitlements[0].Status != "booking_pending" {
		t.Fatalf("failed-confirm booking=%+v err=%v", processing, err)
	}
	var confirmPending model.XiaohongshuBookingOperation
	if err := model.DB.Where("entitlement_id = ? AND type = ? AND status = ?", released.ID, "book", "confirm_pending").Order("id DESC").First(&confirmPending).Error; err != nil {
		t.Fatal(err)
	}
	var frozen model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("id = ?", released.ID).First(&frozen).Error; err != nil {
		t.Fatal(err)
	}
	var frozenTicket model.Ticket
	if err := model.DB.Where("id = ?", frozen.TicketID).First(&frozenTicket).Error; err != nil {
		t.Fatal(err)
	}
	if frozen.Status != "booking_pending" || frozen.PlatformBookID == "" || frozen.ReservationID == 0 || frozenTicket.Status != "pending_booking" {
		t.Fatalf("failed platform confirmation exposed booking facts: entitlement=%+v ticket=%+v", frozen, frozenTicket)
	}

	confirmShouldFail = false
	if err := model.DB.Model(&confirmPending).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 1 || confirmCalls != 3 {
		t.Fatalf("confirm recovery processed=%d calls=%d err=%v", processed, confirmCalls, err)
	}
	if len(confirmExternalIDs) < 3 || confirmExternalIDs[len(confirmExternalIDs)-1] != confirmExternalIDs[len(confirmExternalIDs)-2] {
		t.Fatalf("confirmation retry changed external booking id: %v", confirmExternalIDs)
	}
	if err := model.DB.Where("id = ?", frozen.ID).First(&frozen).Error; err != nil {
		t.Fatal(err)
	}
	if frozen.Status != "booked" {
		t.Fatalf("confirmed booking was not finalized: %+v", frozen)
	}

	if _, err := miniapp.CancelXiaohongshuPackageBooking(context.Background(), &customer, created.OrderNo, entitlementNo); err != nil || cancelCalls != 3 {
		t.Fatalf("release confirmed retry booking cancelCalls=%d err=%v", cancelCalls, err)
	}

	breakLocalFinalize = true
	failedFinalizeBooking := booking
	failedFinalizeBooking.ClientRequestID = "booking-local-finalize-retry"
	processing, err = miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, failedFinalizeBooking)
	if err != nil || len(processing.TicketCodes) != 0 || len(processing.PackageEntitlements) != 1 || processing.PackageEntitlements[0].Status != "booking_pending" {
		t.Fatalf("failed-finalize booking=%+v err=%v", processing, err)
	}
	confirmPending = model.XiaohongshuBookingOperation{}
	if err := model.DB.Where("entitlement_id = ? AND type = ? AND status = ?", released.ID, "book", "confirm_pending").Order("id DESC").First(&confirmPending).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("id = ?", released.ID).First(&frozen).Error; err != nil {
		t.Fatal(err)
	}
	if frozen.Status != "booking_pending" || frozen.PlatformBookID == "" || frozen.ReservationID == 0 {
		t.Fatalf("local finalize failure lost durable booking facts: %+v", frozen)
	}

	breakLocalFinalize = false
	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", frozen.TicketID).Update("status", "pending_booking").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&confirmPending).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 1 || confirmCalls != 5 {
		t.Fatalf("local finalize recovery processed=%d calls=%d err=%v", processed, confirmCalls, err)
	}
	if len(confirmExternalIDs) < 5 || confirmExternalIDs[len(confirmExternalIDs)-1] != confirmExternalIDs[len(confirmExternalIDs)-2] {
		t.Fatalf("local finalize retry changed external booking id: %v", confirmExternalIDs)
	}
	if err := model.DB.Where("id = ?", frozen.ID).First(&frozen).Error; err != nil {
		t.Fatal(err)
	}
	if frozen.Status != "booked" || compensationCalls != 0 {
		t.Fatalf("local finalize retry result=%+v compensationCalls=%d", frozen, compensationCalls)
	}

	refundShouldFail = true
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Ticket{}).Where("id = ?", frozen.TicketID).Update("status", "refunded").Error; err != nil {
			return err
		}
		return tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("id = ?", frozen.ID).Updates(map[string]interface{}{
			"status": "refunded", "platform_sync_status": "pending", "platform_sync_error": "",
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	processed, err = miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 0 || refundCalls != 1 {
		t.Fatalf("failed refund notification processed=%d calls=%d err=%v", processed, refundCalls, err)
	}
	var refundOperation model.XiaohongshuBookingOperation
	if err := model.DB.Where("entitlement_id = ? AND type = ?", frozen.ID, "refund_status_sync").First(&refundOperation).Error; err != nil {
		t.Fatal(err)
	}
	if refundOperation.Status != "pending" || refundOperation.Attempts != 1 {
		t.Fatalf("refund retry operation=%+v", refundOperation)
	}

	refundShouldFail = false
	if err := model.DB.Model(&refundOperation).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	restarted := NewMiniappService()
	restarted.Now = miniapp.Now
	restarted.NewXiaohongshuClient = miniapp.NewXiaohongshuClient
	processed, err = restarted.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 1 || refundCalls != 2 {
		t.Fatalf("restarted refund worker processed=%d calls=%d err=%v", processed, refundCalls, err)
	}
	if err := model.DB.Where("id = ?", frozen.ID).First(&frozen).Error; err != nil {
		t.Fatal(err)
	}
	if frozen.Status != "refunded" || frozen.PlatformSyncStatus != "synced" {
		t.Fatalf("refund notification changed local refund facts: %+v", frozen)
	}
	processed, err = restarted.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 0 || refundCalls != 2 {
		t.Fatalf("completed refund notification was resent processed=%d calls=%d err=%v", processed, refundCalls, err)
	}
	if err := model.DB.Model(&frozen).Updates(map[string]interface{}{"platform_book_id": "", "platform_sync_status": "pending"}).Error; err != nil {
		t.Fatal(err)
	}
	processed, err = restarted.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 20)
	if err != nil || processed != 0 || refundCalls != 2 {
		t.Fatalf("refund without platform booking id was sent processed=%d calls=%d err=%v", processed, refundCalls, err)
	}
}

func TestXiaohongshuMiniappPartialRefundKeepsRemainingDeferredEntitlementBookable(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	if err := model.DB.Model(&model.ScenicHotelPackage{}).Where("id = ?", fixture.packageView.ID).Updates(map[string]interface{}{
		"booking_mode": "after_purchase", "voucher_validity_days": 90, "max_reschedules": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-partial-refund-presentation", "active", "production")
	externalNo := "XHS-PARTIAL-REFUND-PRESENTATION"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	openID, _ := utils.EncryptAES("OPEN-PARTIAL-REFUND")
	customer := model.MiniappCustomer{TenantID: fixture.tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("OPEN-PARTIAL-REFUND"), OpenIDCiphertext: openID, Status: "active"}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID,
		OrderID: order.ID, ClientRequestID: "partial-refund", ExternalOrderID: externalNo, State: "paid",
	}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	var tickets []model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).Order("id ASC").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&tickets[0]).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ScenicHotelPackageEntitlement{}).Where("ticket_id = ?", tickets[0].ID).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&order).Update("status", "partial_refunded").Error; err != nil {
		t.Fatal(err)
	}
	result, err := (NewMiniappService()).orderResult(&link, &order, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial_refunded" || result.CoreOrderStatus != "partial_refunded" || result.PlatformPaymentState != "paid" || len(result.PackageEntitlements) != 2 || len(result.TicketCodes) != 0 {
		t.Fatalf("partial-refund presentation=%+v", result)
	}
	remaining := 0
	for _, entitlement := range result.PackageEntitlements {
		if entitlement.Status == "pending_booking" {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("remaining pending entitlements=%d result=%+v", remaining, result)
	}
}

func TestFailedXiaohongshuBookingOperationsAreTenantScopedAndRecoverCorrectPhase(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	if err := model.DB.Model(&model.ScenicHotelPackage{}).Where("id = ?", fixture.packageView.ID).Updates(map[string]interface{}{
		"booking_mode": "after_purchase", "voucher_validity_days": 90, "min_advance_days": 0, "max_reschedules": 2,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-recovery", "active", "production")
	externalOrderNo := "XHS-RECOVERY-ORDER"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalOrderNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{TenantID: fixture.tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("RECOVERY-OPEN"), OpenIDCiphertext: "encrypted", Status: "active"}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{TenantID: fixture.tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ClientRequestID: "recovery-order", ExternalOrderID: externalOrderNo, State: "paid"}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	externalBookID := "XHS-RECOVERY-BOOK"
	if err := model.Write(func(tx *gorm.DB) error {
		_, err := (PackageFulfillmentLifecycle{}).PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn, GuestName: "恢复测试游客",
			ContactPhone: "13800138000", ClientRequestID: "recovery-book", ExternalBookOrderID: externalBookID,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("id = ?", entitlement.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{OpenID: "sensitive-open-id", VoucherCode: "sensitive-voucher"})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now()
	bookOperation := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "xhs:book:manual-recovery", Type: "book", Status: "failed", ExternalBookOrderID: externalBookID,
		FailedFromStage: "pending", RequestPayloadCiphertext: payload, Attempts: 20, MaxAttempts: 20, LastError: "permanent provider error", CompletedAt: &completedAt,
	}
	if err := model.DB.Create(&bookOperation).Error; err != nil {
		t.Fatal(err)
	}
	completedCompensation := bookOperation
	completedCompensation.Base = model.Base{}
	completedCompensation.OperationKey = "xhs:book:completed-compensation"
	completedCompensation.FailedFromStage = ""
	completedCompensation.LastError = "voucher mismatch was already compensated"
	if err := model.DB.Create(&completedCompensation).Error; err != nil {
		t.Fatal(err)
	}
	miniapp := NewMiniappService()
	if err := model.DB.Model(&bookOperation).Update("last_error", "voucher sensitive-voucher for mobile 13800138000").Error; err != nil {
		t.Fatal(err)
	}
	page, err := miniapp.ListFailedXiaohongshuBookingOperations(fixture.tenantID, "book", 1, 20)
	if err != nil || page.Total != 1 || len(page.Data) != 1 || page.Data[0].EntitlementNo != entitlement.EntitlementNo || page.Data[0].OrderNo != order.OrderNo || page.Data[0].FailedFromStage != "pending" {
		t.Fatalf("failed operation page=%+v err=%v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), payload) || strings.Contains(string(encoded), "sensitive-open-id") || strings.Contains(string(encoded), "sensitive-voucher") || strings.Contains(string(encoded), "13800138000") || strings.Contains(string(encoded), "恢复测试游客") {
		t.Fatalf("failed operation response leaked sensitive data: %s", encoded)
	}
	foreign, err := miniapp.ListFailedXiaohongshuBookingOperations(fixture.tenantID+9999, "", 1, 20)
	if err != nil || foreign.Total != 0 || len(foreign.Data) != 0 {
		t.Fatalf("cross-tenant failed operations=%+v err=%v", foreign, err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID+9999, bookOperation.ID, 77, "admin", "cross tenant"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant retry error=%v", err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, bookOperation.ID, 77, "admin", " "); err == nil {
		t.Fatal("blank retry reason was accepted")
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, bookOperation.ID, 77, "admin", "retry initial booking request"); err != nil {
		t.Fatal(err)
	}
	bookOperationID := bookOperation.ID
	bookOperation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&bookOperation, bookOperationID).Error; err != nil {
		t.Fatal(err)
	}
	if bookOperation.Status != "pending" || bookOperation.FailedFromStage != "" || bookOperation.Attempts != 0 || bookOperation.NextAttemptAt == nil || bookOperation.CompletedAt != nil || bookOperation.LastError != "" {
		t.Fatalf("book operation without platform id recovered incorrectly: %+v", bookOperation)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, bookOperation.ID, 77, "admin", "retry twice"); err == nil || !strings.Contains(err.Error(), "only failed") {
		t.Fatalf("non-failed operation retry error=%v", err)
	}
	// A remote booking result may be durable before the entitlement row gets
	// its platform id. Recovery must reconcile that local gap without sending
	// the booking request again.
	remoteOnlyID := "PLATFORM-REMOTE-ONLY"
	completedAt = time.Now()
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{
		"status": "booking_pending", "platform_book_id": "", "platform_sync_status": "failed", "platform_sync_error": "local persistence interrupted",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&bookOperation).Updates(map[string]interface{}{
		"status": "failed", "failed_from_stage": "remote_succeeded", "platform_book_id": remoteOnlyID,
		"attempts": 20, "max_attempts": 20, "last_error": "local persistence interrupted", "next_attempt_at": nil, "completed_at": completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, bookOperation.ID, 78, "admin", "reconcile remote booking result"); err != nil {
		t.Fatal(err)
	}
	if completed, err := miniapp.bookingService().processXiaohongshuBookingOperation(context.Background(), bookOperation.ID); err != nil || completed {
		t.Fatalf("remote-only booking reconciliation completed=%v err=%v", completed, err)
	}
	var reconciledEntitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.First(&reconciledEntitlement, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reconciledEntitlement.Status != "booking_pending" || reconciledEntitlement.PlatformBookID != remoteOnlyID || reconciledEntitlement.PlatformSyncStatus != "pending" {
		t.Fatalf("remote-only booking result was not reconciled: %+v", reconciledEntitlement)
	}
	var reconciledOperation model.XiaohongshuBookingOperation
	if err := model.DB.First(&reconciledOperation, bookOperation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reconciledOperation.Status != "confirm_pending" || reconciledOperation.PlatformBookID != remoteOnlyID {
		t.Fatalf("remote-only booking operation stage=%+v", reconciledOperation)
	}

	platformBookID := "PLATFORM-RECOVERY-BOOK"
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"platform_book_id": platformBookID, "platform_sync_status": "failed", "platform_sync_error": "confirm failed"}).Error; err != nil {
		t.Fatal(err)
	}
	completedAt = time.Now()
	if err := model.DB.Model(&bookOperation).Updates(map[string]interface{}{
		"status": "failed", "failed_from_stage": "confirm_pending", "platform_book_id": platformBookID, "attempts": 20, "last_error": "confirm failed", "next_attempt_at": nil, "completed_at": completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, bookOperation.ID, 77, "admin", "retry platform confirmation"); err != nil {
		t.Fatal(err)
	}
	bookOperationID = bookOperation.ID
	bookOperation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&bookOperation, bookOperationID).Error; err != nil {
		t.Fatal(err)
	}
	if bookOperation.Status != "confirm_pending" || bookOperation.FailedFromStage != "" || bookOperation.Attempts != 0 || bookOperation.CompletedAt != nil {
		t.Fatalf("book operation with platform id recovered incorrectly: %+v", bookOperation)
	}

	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", entitlement.TicketID).Update("status", "pending_booking").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"status": "cancel_pending", "platform_sync_status": "failed", "platform_sync_error": "cancel failed"}).Error; err != nil {
		t.Fatal(err)
	}
	revokeOperation := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "xhs:revoke:manual-recovery", Type: "revoke", Status: "failed", ExternalBookOrderID: externalBookID,
		PlatformBookID: platformBookID, FailedFromStage: "pending", RequestPayloadCiphertext: payload, Attempts: 20, MaxAttempts: 20, LastError: "cancel failed", CompletedAt: &completedAt,
	}
	if err := model.DB.Create(&revokeOperation).Error; err != nil {
		t.Fatal(err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, revokeOperation.ID, 77, "admin", "retry cancellation"); err != nil {
		t.Fatal(err)
	}
	revokeOperationID := revokeOperation.ID
	revokeOperation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&revokeOperation, revokeOperationID).Error; err != nil || revokeOperation.Status != "pending" || revokeOperation.CompletedAt != nil {
		t.Fatalf("revoke operation=%+v err=%v", revokeOperation, err)
	}

	if err := model.DB.Model(&model.Ticket{}).Where("id = ?", entitlement.TicketID).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&entitlement).Updates(map[string]interface{}{"status": "refunded", "platform_sync_status": "failed", "platform_sync_error": "refund sync failed"}).Error; err != nil {
		t.Fatal(err)
	}
	refundOperation := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "xhs:refund_status_sync:manual-recovery", Type: "refund_status_sync", Status: "failed", ExternalBookOrderID: externalBookID,
		PlatformBookID: platformBookID, FailedFromStage: "pending", RequestPayloadCiphertext: payload, Attempts: 20, MaxAttempts: 20, LastError: "refund sync failed", CompletedAt: &completedAt,
	}
	if err := model.DB.Create(&refundOperation).Error; err != nil {
		t.Fatal(err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, refundOperation.ID, 77, "admin", "retry refund notification"); err != nil {
		t.Fatal(err)
	}
	refundOperationID := refundOperation.ID
	refundOperation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&refundOperation, refundOperationID).Error; err != nil || refundOperation.Status != "pending" || refundOperation.CompletedAt != nil {
		t.Fatalf("refund operation=%+v err=%v", refundOperation, err)
	}
	// Simulate a platform refund notice that already succeeded while the local
	// finalization fact is temporarily inconsistent. Exhaustion must remember
	// remote_succeeded, and a manual retry must only finish local work without
	// sending status 4 again.
	past := time.Now().Add(-time.Minute)
	if err := model.DB.Model(&refundOperation).Updates(map[string]interface{}{
		"status": "remote_succeeded", "platform_book_id": "WRONG-PLATFORM-ID", "attempts": 0,
		"max_attempts": 1, "next_attempt_at": past,
	}).Error; err != nil {
		t.Fatal(err)
	}
	completed, err := miniapp.bookingService().processXiaohongshuBookingOperation(context.Background(), refundOperation.ID)
	if err != nil || completed {
		t.Fatalf("local refund finalize first attempt completed=%v err=%v", completed, err)
	}
	if err := model.DB.Model(&refundOperation).Update("next_attempt_at", past).Error; err != nil {
		t.Fatal(err)
	}
	completed, err = miniapp.bookingService().processXiaohongshuBookingOperation(context.Background(), refundOperation.ID)
	if err != nil || !completed {
		t.Fatalf("local refund finalize exhaustion completed=%v err=%v", completed, err)
	}
	refundOperation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&refundOperation, refundOperationID).Error; err != nil {
		t.Fatal(err)
	}
	if refundOperation.Status != "failed" || refundOperation.FailedFromStage != "remote_succeeded" || refundOperation.Attempts != 1 {
		t.Fatalf("local refund finalize exhaustion lost stage: %+v", refundOperation)
	}
	if err := model.DB.Model(&refundOperation).Update("platform_book_id", platformBookID).Error; err != nil {
		t.Fatal(err)
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, refundOperation.ID, 77, "admin", "finish local refund finalization"); err != nil {
		t.Fatal(err)
	}
	completed, err = miniapp.bookingService().processXiaohongshuBookingOperation(context.Background(), refundOperation.ID)
	if err != nil || !completed {
		t.Fatalf("local refund finalize recovery completed=%v err=%v", completed, err)
	}
	var syncedEntitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.First(&syncedEntitlement, entitlement.ID).Error; err != nil || syncedEntitlement.Status != "refunded" || syncedEntitlement.PlatformSyncStatus != "synced" {
		t.Fatalf("local refund finalize recovery entitlement=%+v err=%v", syncedEntitlement, err)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ?", fixture.tenantID, "xiaohongshu.booking_sync.retry").Count(&auditCount).Error; err != nil || auditCount != 6 {
		t.Fatalf("retry audits=%d err=%v", auditCount, err)
	}
}

func TestFailedXiaohongshuBookingOperationResumesOriginalCompensationStage(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	if err := model.DB.Model(&model.ScenicHotelPackage{}).Where("id = ?", fixture.packageView.ID).Updates(map[string]interface{}{
		"booking_mode": "after_purchase", "voucher_validity_days": 90, "max_reschedules": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-compensation-recovery", "active", "production")
	secret, err := utils.EncryptAES("app-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&account).Updates(map[string]interface{}{"app_id": "app-id", "secret_ciphertext": secret}).Error; err != nil {
		t.Fatal(err)
	}
	externalOrderNo := "XHS-COMPENSATION-RECOVERY"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalOrderNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{TenantID: fixture.tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("COMPENSATION-OPEN"), OpenIDCiphertext: "encrypted", Status: "active"}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{TenantID: fixture.tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ClientRequestID: "compensation-order", ExternalOrderID: externalOrderNo, State: "paid"}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	externalBookID, platformBookID := "XHS-COMPENSATION-BOOK", "PLATFORM-COMPENSATION-BOOK"
	if err := model.Write(func(tx *gorm.DB) error {
		_, prepareErr := (PackageFulfillmentLifecycle{}).PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn, GuestName: "补偿恢复游客",
			ContactPhone: "13800138000", ClientRequestID: "compensation-book", ExternalBookOrderID: externalBookID,
		})
		return prepareErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ScenicHotelPackageEntitlement{}).Where("id = ?", entitlement.ID).Updates(map[string]interface{}{
		"platform_book_id": platformBookID, "platform_sync_status": "failed", "platform_sync_error": "compensation retry exhausted",
	}).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{})
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now()
	operation := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "xhs:book:compensation-recovery", Type: "book", Status: "failed", FailedFromStage: "compensation_pending",
		ExternalBookOrderID: externalBookID, PlatformBookID: platformBookID, RequestPayloadCiphertext: payload,
		Attempts: 20, MaxAttempts: 20, LastError: "compensation retry exhausted", CompletedAt: &completedAt,
	}
	if err := model.DB.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}
	miniapp := NewMiniappService()
	confirmCalls, compensationCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/component/deal/pre_sale/sync_status":
			var request xiaohongshu.PresaleBookStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Status == 1 {
				confirmCalls++
			}
			if request.Status == 2 {
				compensationCalls++
			}
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, ComponentAppID: "provider-app", ComponentAccessToken: "authorized-miniapp-token", BaseURL: server.URL, HTTP: server.Client()}
	}
	if err := miniapp.RetryFailedXiaohongshuBookingOperation(fixture.tenantID, operation.ID, 88, "admin", "continue compensation"); err != nil {
		t.Fatal(err)
	}
	operationID := operation.ID
	operation = model.XiaohongshuBookingOperation{}
	if err := model.DB.First(&operation, operationID).Error; err != nil {
		t.Fatal(err)
	}
	if operation.Status != "compensation_pending" || operation.FailedFromStage != "" || operation.Attempts != 0 {
		t.Fatalf("compensation operation restored incorrectly: %+v", operation)
	}
	processed, err := miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 10)
	if err != nil || processed != 1 || compensationCalls != 1 || confirmCalls != 0 {
		t.Fatalf("compensation recovery processed=%d compensation=%d confirm=%d err=%v", processed, compensationCalls, confirmCalls, err)
	}
	var released model.ScenicHotelPackageEntitlement
	if err := model.DB.First(&released, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if released.Status != "pending_booking" || released.ReservationID != 0 || released.PlatformBookID != "" {
		t.Fatalf("compensation recovery did not release prepared booking: %+v", released)
	}
}

func miniappLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/session":
			if r.URL.Query().Get("code") != "login-code" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"openid":"OPEN-1","session_key":"SESSION-1"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
