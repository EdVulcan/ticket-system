package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
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
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	created, err := miniapp.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 2, ClientRequestID: "request-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.OrderNo == "" || created.PlatformOrderID != "XHS-PLATFORM-1" || created.PayToken != "PAY-TOKEN-1" || created.AmountCents != 2 || created.Status != "unpaid" {
		t.Fatalf("created=%+v", created)
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
	orders, err := miniapp.ListXiaohongshuOrders(&customer, 0, 99)
	if err != nil {
		t.Fatal(err)
	}
	if orders.Page != 1 || orders.PageSize != 10 || orders.Total != 1 || len(orders.Items) != 1 ||
		orders.Items[0].OrderNo != created.OrderNo || orders.Items[0].Quantity != 2 || orders.Items[0].AmountCents != 2 ||
		orders.Items[0].Status != "paid" || orders.Items[0].ImageURL != "https://example.com/ticket.png" {
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
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
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

	bookCalls, confirmCalls, cancelCalls := 0, 0, 0
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
			bookCalls++
			var request xiaohongshu.PresaleBookRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ProductType != xiaohongshu.ProductTypePresaleVoucher || request.OpenID != "OPEN-DEFERRED-PACKAGE" || request.POIID != "POI-1" || len(request.BookInfo.Details) != 1 || request.BookInfo.Details[0].VoucherCode != "VOUCHER-DEFERRED" || request.BookInfo.Details[0].CheckInDate != fixture.checkIn.Format("2006-01-02") {
				t.Fatalf("booking request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"XHS-DEFERRED","book_result":[{"book_id":"PLATFORM-BOOK-DEFERRED","voucher_code":"VOUCHER-DEFERRED"}]},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/component/deal/pre_sale/sync_status":
			var request xiaohongshu.PresaleBookStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			switch request.Status {
			case 1:
				confirmCalls++
			case 4:
				cancelCalls++
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
	miniapp.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
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
	booked, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking)
	if err != nil || len(booked.TicketCodes) != 1 || len(booked.PackageEntitlements) != 1 || booked.PackageEntitlements[0].Status != "booked" || booked.PackageEntitlements[0].GuestName != "预约游客" || bookCalls != 1 || confirmCalls != 1 {
		t.Fatalf("booked=%+v calls=%d/%d err=%v", booked, bookCalls, confirmCalls, err)
	}
	if _, err := miniapp.BookXiaohongshuPackage(context.Background(), &customer, created.OrderNo, booking); err != nil || bookCalls != 1 || confirmCalls != 1 {
		t.Fatalf("idempotent calls=%d/%d err=%v", bookCalls, confirmCalls, err)
	}
	cancelled, err := miniapp.CancelXiaohongshuPackageBooking(context.Background(), &customer, created.OrderNo, entitlementNo)
	if err != nil || len(cancelled.TicketCodes) != 0 || len(cancelled.PackageEntitlements) != 1 || cancelled.PackageEntitlements[0].Status != "pending_booking" || cancelCalls != 1 {
		t.Fatalf("cancelled=%+v calls=%d err=%v", cancelled, cancelCalls, err)
	}
	var order model.Order
	if err := model.DB.Where("order_no = ?", created.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	var reservations []model.HotelReservation
	if err := model.DB.Where("order_id = ?", order.ID).Find(&reservations).Error; err != nil || len(reservations) != 1 || reservations[0].Status != "cancelled" {
		t.Fatalf("reservations=%+v err=%v", reservations, err)
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
