package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func seedXiaohongshuIssuanceOrder(t *testing.T, quantity int) (*XiaohongshuOrderService, *model.XiaohongshuOrderLink, *model.Order) {
	t.Helper()
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xhs-issuance"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "xhs-issuance-app", "issuance-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "XHS-ISSUANCE-PRODUCT", ChannelSaleCents: 1}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "XHS-ISSUANCE-SKU", CategoryID: "ticket", ImageURL: "https://example.com/ticket.png", Description: "票", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail", ProductType: 1, SettleType: 1, SyncStatus: "synced", AuditStatus: "approved"}).Error; err != nil {
		t.Fatal(err)
	}
	openID, err := utils.EncryptAES("ISSUANCE-OPEN")
	if err != nil {
		t.Fatal(err)
	}
	sessionKey, err := utils.EncryptAES("ISSUANCE-SESSION")
	if err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{
		TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("ISSUANCE-OPEN"),
		OpenIDCiphertext: openID, SessionKeyCiphertext: sessionKey, SessionTokenHash: hashMiniappValue("ISSUANCE-TOKEN"),
		SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	externalOrderID := "XHS-ISSUANCE-ORDER"
	order := model.Order{
		TenantID: tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalOrderID,
		Items: []model.OrderItem{{ProductID: productID, Quantity: quantity}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{
		TenantID: tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID,
		ClientRequestID: "issuance-request", ExternalOrderID: externalOrderID, State: "unpaid", VoucherIssuanceStatus: "pending",
	}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	return &XiaohongshuOrderService{Now: time.Now}, &link, &order
}

func loadXiaohongshuIssuanceState(t *testing.T, link *model.XiaohongshuOrderLink, order *model.Order) []model.XiaohongshuVoucherLink {
	t.Helper()
	if err := model.DB.First(link, link.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	var vouchers []model.XiaohongshuVoucherLink
	if err := model.DB.Where("xiaohongshu_order_link_id = ?", link.ID).Find(&vouchers).Error; err != nil {
		t.Fatal(err)
	}
	return vouchers
}

func TestXiaohongshuVoucherIssuanceStaysPendingUntilDelayedExactResponse(t *testing.T) {
	service, link, order := seedXiaohongshuIssuanceOrder(t, 2)
	empty := &xiaohongshu.GuaranteeOrderResponse{TradeNo: "ISSUANCE-TRADE", PayChannel: 1}
	if err := service.completeXiaohongshuOrder(link, order, empty); err != nil {
		t.Fatal(err)
	}
	if vouchers := loadXiaohongshuIssuanceState(t, link, order); link.VoucherIssuanceStatus != "pending" || len(vouchers) != 0 || order.Status != "paid" {
		t.Fatalf("empty paid issuance link=%+v order=%+v vouchers=%+v", link, order, vouchers)
	}
	view, viewErr := (&ExecutionCenterService{}).List(order.TenantID, "渠道", "", 20)
	if viewErr != nil {
		t.Fatal(viewErr)
	}
	visible := false
	for _, item := range view.Items {
		if item.Source == "xiaohongshu_issuance" && item.ID == link.ID {
			visible = true
		}
	}
	if !visible {
		t.Fatal("paid pending issuance is missing from operator attention list")
	}
	result, err := service.orderResult(link, order, false)
	if err != nil || result.VoucherIssuanceStatus != "pending" || len(result.TicketCodes) != 0 {
		t.Fatalf("pending issuance exposed ticket codes result=%+v err=%v", result, err)
	}

	delayed := &xiaohongshu.GuaranteeOrderResponse{TradeNo: "ISSUANCE-TRADE", PayChannel: 1, Vouchers: []xiaohongshu.VoucherInfo{{Code: "ISSUANCE-V-1", Status: 1}, {Code: "ISSUANCE-V-2", Status: 1}}}
	if err := (&XiaohongshuOrderService{Now: time.Now}).completeXiaohongshuOrder(link, order, delayed); err != nil {
		t.Fatal(err)
	}
	vouchers := loadXiaohongshuIssuanceState(t, link, order)
	if link.VoucherIssuanceStatus != "ready" || len(vouchers) != 2 || link.VoucherIssuanceAttemptCount != 2 {
		t.Fatalf("delayed issuance link=%+v vouchers=%+v", link, vouchers)
	}
	firstHashes := []string{vouchers[0].VoucherCodeHash, vouchers[1].VoucherCodeHash}
	if err := (&XiaohongshuOrderService{Now: time.Now}).completeXiaohongshuOrder(link, order, delayed); err != nil {
		t.Fatal(err)
	}
	vouchers = loadXiaohongshuIssuanceState(t, link, order)
	if link.VoucherIssuanceStatus != "ready" || len(vouchers) != 2 || vouchers[0].VoucherCodeHash != firstHashes[0] || vouchers[1].VoucherCodeHash != firstHashes[1] {
		t.Fatalf("exact repeat changed immutable voucher bindings link=%+v vouchers=%+v", link, vouchers)
	}
	if result, err = service.orderResult(link, order, false); err != nil || result.VoucherIssuanceStatus != "ready" || len(result.TicketCodes) != 2 {
		t.Fatalf("ready issuance result=%+v err=%v", result, err)
	}
	var payments int64
	if err := model.DB.Model(&model.Payment{}).Where("order_no = ?", order.OrderNo).Count(&payments).Error; err != nil || payments != 1 {
		t.Fatalf("payment count=%d err=%v", payments, err)
	}
}

func TestXiaohongshuVoucherIssuanceFailureDoesNotRollBackConfirmedPayment(t *testing.T) {
	service, link, order := seedXiaohongshuIssuanceOrder(t, 1)
	service.EncryptVoucher = func(string) (string, error) {
		return "", errors.New("test voucher encryption failure")
	}
	err := service.completeXiaohongshuOrder(link, order, &xiaohongshu.GuaranteeOrderResponse{
		TradeNo: "ISSUANCE-ENCRYPTION-FAILURE", Vouchers: []xiaohongshu.VoucherInfo{{Code: "ISSUANCE-V-FAIL", Status: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "encryption") {
		t.Fatalf("issuance failure error=%v", err)
	}
	if vouchers := loadXiaohongshuIssuanceState(t, link, order); order.Status != "paid" || link.State != "paid" || link.VoucherIssuanceStatus != "pending" || len(vouchers) != 0 {
		t.Fatalf("known payment was rolled back link=%+v order=%+v vouchers=%+v", link, order, vouchers)
	}
	if strings.TrimSpace(link.VoucherIssuanceLastError) == "" {
		t.Fatal("issuance failure was not retained for recovery")
	}
	var payments int64
	if err := model.DB.Model(&model.Payment{}).Where("order_no = ?", order.OrderNo).Count(&payments).Error; err != nil || payments != 1 {
		t.Fatalf("payment count=%d err=%v", payments, err)
	}
}

func TestXiaohongshuVoucherIssuanceRejectsPartialAndDuplicateResponsesWithoutLinks(t *testing.T) {
	for _, test := range []struct {
		name     string
		vouchers []xiaohongshu.VoucherInfo
		status   string
	}{
		{name: "partial", vouchers: []xiaohongshu.VoucherInfo{{Code: "PARTIAL-1", Status: 1}}, status: "pending"},
		{name: "duplicate", vouchers: []xiaohongshu.VoucherInfo{{Code: "DUPLICATE", Status: 1}, {Code: "DUPLICATE", Status: 1}}, status: "manual_review"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, link, order := seedXiaohongshuIssuanceOrder(t, 2)
			if err := service.completeXiaohongshuOrder(link, order, &xiaohongshu.GuaranteeOrderResponse{TradeNo: "ISSUANCE-" + test.name, Vouchers: test.vouchers}); err != nil {
				t.Fatal(err)
			}
			if vouchers := loadXiaohongshuIssuanceState(t, link, order); link.VoucherIssuanceStatus != test.status || len(vouchers) != 0 || order.Status != "paid" {
				t.Fatalf("issuance link=%+v order=%+v vouchers=%+v", link, order, vouchers)
			}
			if strings.TrimSpace(link.VoucherIssuanceLastError) == "" {
				t.Fatal("invalid provider response was not retained for reconciliation")
			}
		})
	}
}

func TestXiaohongshuPaidPendingIssuanceRecoversThroughExistingQueryWorker(t *testing.T) {
	service, link, order := seedXiaohongshuIssuanceOrder(t, 1)
	if err := service.completeXiaohongshuOrder(link, order, &xiaohongshu.GuaranteeOrderResponse{TradeNo: "WORKER-TRADE"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"worker-token","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/gpay_order/get":
			_, _ = w.Write([]byte(`{"data":{"order_id":"WORKER-ORDER","pay_amount":1,"order_status":6,"voucher_infos":[{"voucher_code":"WORKER-VOUCHER","voucher_status":1}],"third_trade_no":"WORKER-TRADE"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service.NewXiaohongshuClient = func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}
	if processed, err := service.ProcessPendingXiaohongshuOrders(context.Background(), time.Now().Add(time.Minute), 20); err != nil || processed != 1 {
		t.Fatalf("worker processed=%d err=%v", processed, err)
	}
	if vouchers := loadXiaohongshuIssuanceState(t, link, order); link.VoucherIssuanceStatus != "ready" || len(vouchers) != 1 {
		t.Fatalf("worker recovery link=%+v vouchers=%+v", link, vouchers)
	}
	var payments int64
	if err := model.DB.Model(&model.Payment{}).Where("order_no = ?", order.OrderNo).Count(&payments).Error; err != nil || payments != 1 {
		t.Fatalf("worker duplicated payment count=%d err=%v", payments, err)
	}
}

func TestXiaohongshuOrderCodeQuantityRejectedBeforeOrderOrProviderWrite(t *testing.T) {
	service, link, order := seedXiaohongshuIssuanceOrder(t, 1)
	var item model.OrderItem
	var customer model.MiniappCustomer
	var mapping model.ChannelProductMapping
	if err := model.DB.Where("order_id = ?", order.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ?", item.ProductID).Update("code_mode", "order").Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&customer, link.MiniappCustomerID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("channel_account_id = ? AND product_id = ?", link.ChannelAccountID, item.ProductID).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	service.NewXiaohongshuClient = func(string, string, string) *xiaohongshu.Client {
		t.Fatal("unsupported quantity contacted provider")
		return nil
	}
	var before, after int64
	model.DB.Model(&model.Order{}).Where("tenant_id = ?", order.TenantID).Count(&before)
	_, err := service.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: mapping.ID, Quantity: 2, ClientRequestID: "unsupported-order-code"})
	if err == nil || !strings.Contains(err.Error(), "整单一码") {
		t.Fatalf("unsupported quantity err=%v", err)
	}
	model.DB.Model(&model.Order{}).Where("tenant_id = ?", order.TenantID).Count(&after)
	if before != after {
		t.Fatalf("unsupported quantity created order: before=%d after=%d", before, after)
	}
	// The lower-level creator also rejects it, rather than trusting a stale
	// channel-side product read. Window orders remain governed by normal rules.
	invalid := model.Order{TenantID: order.TenantID, Channel: "xiaohongshu", ChannelAccountID: link.ChannelAccountID, Items: []model.OrderItem{{ProductID: item.ProductID, Quantity: 2}}}
	if err := (&OrderService{}).Create(&invalid); err == nil {
		t.Fatal("core creator bypassed quantity restriction")
	}
	catalog, err := NewMiniappService().ListCatalog(&customer)
	if err != nil || len(catalog.Products) != 1 || catalog.Products[0].MaxQuantity != 1 {
		t.Fatalf("catalog limit=%+v err=%v", catalog, err)
	}
}
