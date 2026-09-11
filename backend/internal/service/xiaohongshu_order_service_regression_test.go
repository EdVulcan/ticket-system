package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestProcessPendingXiaohongshuOrdersRotatesPastFailedLinks(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "xhs-order-rotation", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "xhs-order-rotation-app", "xhs-order-rotation-secret"); err != nil {
		t.Fatal(err)
	}
	openIDCiphertext, err := utils.EncryptAES("xhs-order-rotation-open")
	if err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{
		TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("xhs-order-rotation-open"),
		OpenIDCiphertext: openIDCiphertext, SessionTokenHash: hashMiniappValue("xhs-order-rotation-token"),
		SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}

	baseTime := time.Date(2026, 9, 11, 12, 0, 0, 0, time.Local)
	links := make([]model.XiaohongshuOrderLink, 0, 21)
	for i := 0; i < 21; i++ {
		externalOrderID := fmt.Sprintf("XHS-ROTATION-%02d", i+1)
		order := model.Order{
			TenantID: tenantID, OrderNo: fmt.Sprintf("ROTATION-ORDER-%02d", i+1), TotalAmount: 0.01,
			Status: "unpaid", Channel: "xiaohongshu", Environment: "sandbox", ChannelAccountID: account.ID,
		}
		if err := model.DB.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		link := model.XiaohongshuOrderLink{
			TenantID: tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID,
			ClientRequestID: fmt.Sprintf("rotation-request-%02d", i+1), ExternalOrderID: externalOrderID,
			PlatformOrderID: func() string {
				if i < 20 {
					return fmt.Sprintf("platform-expected-%02d", i+1)
				}
				return ""
			}(),
			State: "unpaid", VoucherIssuanceStatus: "pending",
		}
		if err := model.DB.Create(&link).Error; err != nil {
			t.Fatal(err)
		}
		links = append(links, link)
	}

	var queryCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"rotation-token","expire_in":7200},"success":true,"code":0}`))
		case "/api/rmp/mp/deal/gpay_order/get":
			queryCalls.Add(1)
			var request xiaohongshu.GuaranteeOrderRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode query: %v", err)
				return
			}
			if request.ExternalOrderID != links[20].ExternalOrderID {
				_, _ = w.Write([]byte(`{"data":{"order_id":"different-order","order_status":0},"success":true,"code":0}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"order_status":0},"success":true,"code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := XiaohongshuOrderService{
		Now: func() time.Time { return baseTime },
		NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
			return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
		},
	}

	processed, err := service.ProcessPendingXiaohongshuOrders(context.Background(), baseTime, 20)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 {
		t.Fatalf("first batch processed=%d, want no successful refresh", processed)
	}
	if queryCalls.Load() != 20 {
		t.Fatalf("first batch provider queries=%d, want 20 failed attempts", queryCalls.Load())
	}
	for i, link := range links {
		var current model.XiaohongshuOrderLink
		if err := model.DB.First(&current, link.ID).Error; err != nil {
			t.Fatal(err)
		}
		if i < 20 && current.LastQueriedAt == nil {
			t.Fatalf("failed link %d did not record query attempt", i+1)
		}
		if i == 20 && current.LastQueriedAt != nil {
			t.Fatalf("unselected link %d was queried at %v", i+1, current.LastQueriedAt)
		}
	}

	processed, err = service.ProcessPendingXiaohongshuOrders(context.Background(), baseTime.Add(30*time.Second), 20)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("second batch processed=%d, want the previously skipped link", processed)
	}
	if queryCalls.Load() != 40 {
		t.Fatalf("provider queries=%d, want two full batches after rotation", queryCalls.Load())
	}
	var recovered model.XiaohongshuOrderLink
	if err := model.DB.First(&recovered, links[20].ID).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.LastQueriedAt == nil {
		t.Fatal("rotated link did not record query attempt")
	}
}

type xiaohongshuOrderRegressionFixture struct {
	tenantID uint
	account  model.ChannelAccount
	mapping  model.ChannelProductMapping
	customer model.MiniappCustomer
}

func seedXiaohongshuOrderRegressionFixture(t *testing.T) xiaohongshuOrderRegressionFixture {
	t.Helper()
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: fmt.Sprintf("xhs-order-regression-%d", time.Now().UnixNano()), Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, fmt.Sprintf("xhs-order-regression-app-%d", time.Now().UnixNano()), "xhs-order-regression-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{
		ChannelAccountID: account.ID, ProductID: productID, ExternalCode: fmt.Sprintf("XHS-REGRESSION-%d", time.Now().UnixNano()),
		DisplayName: "Regression ticket", ChannelSaleCents: 1,
	}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "XHS-REGRESSION-SKU", CategoryID: "ticket", ImageURL: "https://example.test/ticket.png",
		Description: "regression ticket", ProductPath: "/pages/index/index", OrderPath: "/pages/order/detail",
		ProductType: 1, SettleType: 1, SyncStatus: "synced", AuditStatus: "approved",
	}).Error; err != nil {
		t.Fatal(err)
	}
	openIDCiphertext, err := utils.EncryptAES("xhs-order-regression-openid")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	customer := model.MiniappCustomer{
		TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("xhs-order-regression-openid"),
		OpenIDCiphertext: openIDCiphertext, SessionTokenHash: hashMiniappValue("xhs-order-regression-token"),
		SessionExpiresAt: now.Add(time.Hour), Status: "active", LastLoginAt: now,
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	return xiaohongshuOrderRegressionFixture{tenantID: tenantID, account: account, mapping: mapping, customer: customer}
}

func newXiaohongshuOrderRegressionService(t *testing.T) (XiaohongshuOrderService, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"xhs-regression-token","expire_in":7200},"success":true,"code":0}`))
		case "/api/rmp/mp/deal/order/upsert":
			var request xiaohongshu.OrderUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":{"out_order_id":%q,"order_id":"xhs-regression-platform","final_price":%d,"pay_token":"xhs-regression-pay-token","expired_time":%d,"open_pay_type":"life_gpay"},"success":true,"code":0}`, request.ExternalOrderID, request.Price.OrderPrice, time.Now().Add(15*time.Minute).Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	service := XiaohongshuOrderService{NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
	return service, server
}

func TestCreateXiaohongshuOrderRequiresOrderContact(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()

	cases := []struct {
		name  string
		input MiniappOrderCreateInput
		want  string
	}{
		{name: "missing name", input: MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "missing-name", ContactPhone: "13800138000"}, want: "请填写联系人姓名"},
		{name: "missing phone", input: MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "missing-phone", GuestName: "订单联系人"}, want: "请填写有效的手机号"},
		{name: "invalid phone", input: MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "invalid-phone", GuestName: "订单联系人", ContactPhone: "phone-number"}, want: "请填写有效的手机号"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, tc.input)
			var typed *MiniappOrderCreateError
			if !errors.As(err, &typed) || typed.Code != miniappOrderNotCreatedCode || typed.Message != tc.want {
				t.Fatalf("error=%v typed=%+v", err, typed)
			}
		})
	}
	var orders int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if orders != 0 {
		t.Fatalf("invalid contact created %d orders", orders)
	}
}

func newXiaohongshuAccountHoldEvent(t *testing.T, fixture xiaohongshuOrderRegressionFixture) (model.XiaohongshuWebhookEvent, []byte) {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{"Event":"AFTER_SALE_REFUND","OrderId":"","AfterSaleId":"xhs-regression-after-sale-%d","RefundId":"xhs-regression-refund-%d"}`, time.Now().UnixNano(), time.Now().UnixNano()))
	ciphertext, err := utils.EncryptAES(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	event := model.XiaohongshuWebhookEvent{
		TenantID: fixture.tenantID, ChannelAccountID: fixture.account.ID,
		PayloadHash: fmt.Sprintf("xhs-regression-event-%d", time.Now().UnixNano()), EventType: "AFTER_SALE_REFUND",
		PayloadCiphertext: ciphertext, Status: "manual_review", ReceivedAt: time.Now(),
	}
	if err := model.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	return event, payload
}

func createXiaohongshuAccountHold(t *testing.T, fixture xiaohongshuOrderRegressionFixture) model.XiaohongshuRefundCoordination {
	t.Helper()
	event, payload := newXiaohongshuAccountHoldEvent(t, fixture)
	if err := model.Write(func(tx *gorm.DB) error {
		return CreateXiaohongshuRefundCoordinationTx(tx, &fixture.account, &event, payload)
	}); err != nil {
		t.Fatal(err)
	}
	var coordination model.XiaohongshuRefundCoordination
	if err := model.DB.Where("webhook_event_id = ?", event.ID).First(&coordination).Error; err != nil {
		t.Fatal(err)
	}
	return coordination
}

func TestCreateXiaohongshuOrderRequestFingerprintIsIdempotentAndFailClosed(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	input := MiniappOrderCreateInput{
		MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "xhs-regression-request",
		UseDate: "2026-09-12", GuestName: "Regression Guest", ContactPhone: "13800138000",
	}
	created, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.OrderNo == "" {
		t.Fatal("created order number is empty")
	}
	var operation model.XiaohongshuOrderOperation
	if err := model.DB.Where("xiaohongshu_order_link_id = ? AND tenant_id = ?", createdOrderLinkID(t, created.OrderNo, fixture.tenantID), fixture.tenantID).First(&operation).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(operation.RequestPayloadCiphertext)
	if err != nil || payload.Fingerprint == "" {
		t.Fatalf("fingerprint payload=%+v err=%v", payload, err)
	}

	var ordersBefore, linksBefore, operationsBefore int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&ordersBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderLink{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&linksBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&operationsBefore).Error; err != nil {
		t.Fatal(err)
	}

	quoteChanged := input
	quoteChanged.QuoteToken = "a-different-current-quote"
	replayed, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, quoteChanged)
	if err != nil || replayed.OrderNo != created.OrderNo {
		t.Fatalf("exact retry with changed quote token=%+v err=%v", replayed, err)
	}

	mismatches := []struct {
		name   string
		mutate func(*MiniappOrderCreateInput)
	}{
		{name: "mapping", mutate: func(value *MiniappOrderCreateInput) { value.MappingID++ }},
		{name: "quantity", mutate: func(value *MiniappOrderCreateInput) { value.Quantity = 2 }},
		{name: "date", mutate: func(value *MiniappOrderCreateInput) { value.UseDate = "2026-09-13" }},
		{name: "guest", mutate: func(value *MiniappOrderCreateInput) { value.GuestName = "Another Guest" }},
		{name: "phone", mutate: func(value *MiniappOrderCreateInput) { value.ContactPhone = "13900139000" }},
	}
	for _, mismatch := range mismatches {
		t.Run(mismatch.name, func(t *testing.T) {
			changed := input
			mismatch.mutate(&changed)
			_, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, changed)
			var typed *MiniappOrderCreateError
			if !errors.As(err, &typed) || typed.Code != miniappOrderPayloadMismatchCode || typed.OrderNo != created.OrderNo {
				t.Fatalf("mismatch error=%v typed=%+v", err, typed)
			}
			if strings.Contains(err.Error(), "新的请求编号") || strings.Contains(err.Error(), "xhs-regression-pay-token") {
				t.Fatalf("mismatch exposed unsafe recovery guidance/token: %v", err)
			}
		})
	}

	legacyCiphertext, err := encryptXiaohongshuOrderOperationPayload(payload.Request, payload.ProductType)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&operation).Update("request_payload_ciphertext", legacyCiphertext).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
	var recovery *MiniappOrderCreateError
	if !errors.As(err, &recovery) || recovery.Code != miniappOrderRecoveryRequiredCode || recovery.OrderNo != created.OrderNo {
		t.Fatalf("legacy recovery error=%v typed=%+v", err, recovery)
	}
	if strings.Contains(err.Error(), "xhs-regression-pay-token") {
		t.Fatalf("legacy recovery exposed payment token: %v", err)
	}

	var ordersAfter, linksAfter, operationsAfter int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&ordersAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderLink{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&linksAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&operationsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if ordersAfter != ordersBefore || linksAfter != linksBefore || operationsAfter != operationsBefore {
		t.Fatalf("idempotency retries changed counts: before=(%d,%d,%d) after=(%d,%d,%d)", ordersBefore, linksBefore, operationsBefore, ordersAfter, linksAfter, operationsAfter)
	}
}

func createdOrderLinkID(t *testing.T, orderNo string, tenantID uint) uint {
	t.Helper()
	var link model.XiaohongshuOrderLink
	if err := model.DB.Joins("JOIN orders ON orders.id = xiaohongshu_order_links.order_id AND orders.tenant_id = xiaohongshu_order_links.tenant_id").Where("xiaohongshu_order_links.tenant_id = ? AND orders.order_no = ?", tenantID, orderNo).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	return link.ID
}

func TestCreateXiaohongshuOrderRejectsNewOrdersDuringAccountHold(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	createXiaohongshuAccountHold(t, fixture)
	_, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "xhs-regression-held"})
	var typed *MiniappOrderCreateError
	if !errors.As(err, &typed) || typed.Code != miniappOrderNotCreatedCode || typed.Message != "店铺订单售后核对中，暂不可购买，请稍后重试" {
		t.Fatalf("hold error=%v typed=%+v", err, typed)
	}
	if !errors.Is(err, ErrXiaohongshuRefundHold) {
		t.Fatalf("hold error lost sentinel: %v", err)
	}
	var orderCount, linkCount, operationCount int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderLink{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&linkCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("tenant_id = ? AND channel_account_id = ?", fixture.tenantID, fixture.account.ID).Count(&operationCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 || linkCount != 0 || operationCount != 0 {
		t.Fatalf("held create left facts: orders=%d links=%d operations=%d", orderCount, linkCount, operationCount)
	}
}

func TestCreateXiaohongshuOrderExactRetryPrecedesAccountHold(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	input := MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "xhs-regression-retry-held", GuestName: "重试联系人", ContactPhone: "13800138000"}
	created, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	createXiaohongshuAccountHold(t, fixture)
	retry := input
	retry.QuoteToken = "changed-after-hold"
	recovered, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, retry)
	if err != nil || recovered.OrderNo != created.OrderNo {
		t.Fatalf("exact retry under hold=%+v err=%v", recovered, err)
	}
}

func TestCreateXiaohongshuOrderWaitsForConcurrentAfterSaleHold(t *testing.T) {
	fixture := seedXiaohongshuOrderRegressionFixture(t)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	event, payload := newXiaohongshuAccountHoldEvent(t, fixture)
	callbackTx := model.DB.Begin()
	if callbackTx.Error != nil {
		t.Fatal(callbackTx.Error)
	}
	defer callbackTx.Rollback()
	var lockedAccount model.ChannelAccount
	if err := callbackTx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", fixture.account.ID, fixture.tenantID, "xiaohongshu").First(&lockedAccount).Error; err != nil {
		t.Fatal(err)
	}

	type orderOutcome struct {
		result *MiniappOrderResult
		err    error
	}
	outcomeCh := make(chan orderOutcome, 1)
	go func() {
		result, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 1, ClientRequestID: "xhs-regression-concurrent-hold", GuestName: "并发联系人", ContactPhone: "13800138000"})
		outcomeCh <- orderOutcome{result: result, err: err}
	}()
	select {
	case outcome := <-outcomeCh:
		t.Fatalf("new order bypassed account lock before callback committed: result=%+v err=%v", outcome.result, outcome.err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := CreateXiaohongshuRefundCoordinationTx(callbackTx, &lockedAccount, &event, payload); err != nil {
		t.Fatal(err)
	}
	if err := callbackTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	outcome := <-outcomeCh
	var typed *MiniappOrderCreateError
	if !errors.As(outcome.err, &typed) || typed.Code != miniappOrderNotCreatedCode || !errors.Is(outcome.err, ErrXiaohongshuRefundHold) {
		t.Fatalf("concurrent hold outcome result=%+v err=%v typed=%+v", outcome.result, outcome.err, typed)
	}
}
