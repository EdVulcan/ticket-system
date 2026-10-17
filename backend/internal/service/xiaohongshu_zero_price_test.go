package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

func zeroPriceMapping(t *testing.T, f xiaohongshuOrderRegressionFixture) {
	t.Helper()
	input := ChannelMappingUpdate{ExternalCode: f.mapping.ExternalCode, DisplayName: f.mapping.DisplayName, Status: "active"}
	if err := (&ChannelService{}).UpdateMapping(f.tenantID+999, f.account.ID, f.mapping.ID, input); err == nil {
		t.Fatal("another tenant changed a zero-price mapping")
	}
	if err := (&ChannelService{}).UpdateMapping(f.tenantID, f.account.ID, f.mapping.ID, input); err != nil {
		t.Fatal(err)
	}
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("channel_product_mapping_id = ?", f.mapping.ID).First(&config).Error; err != nil {
		t.Fatal(err)
	}
	if config.AuditStatus != "pending" {
		t.Fatal("price change retained stale approval")
	}
	if err := model.DB.Model(&config).Updates(map[string]interface{}{"audit_status": "approved", "sync_status": "synced"}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestXiaohongshuZeroPriceCatalogAndProductionCheckout(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	zeroPriceMapping(t, f)
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	catalog, err := NewMiniappService().ListCatalog(&f.customer)
	if err != nil || len(catalog.Products) != 1 || catalog.Products[0].PriceCents != 0 || catalog.Products[0].PurchaseBlockedReason != "" {
		t.Fatalf("zero catalog=%+v err=%v", catalog, err)
	}
	quote, err := (MiniappPromotionService{}).QuoteForCustomer(&f.customer, f.mapping.ID, 2)
	if err != nil || quote.AmountCents != 0 || quote.DiscountCents != 0 {
		t.Fatalf("zero quote=%+v err=%v", quote, err)
	}
	if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	catalog, err = NewMiniappService().ListCatalog(&f.customer)
	if err != nil || len(catalog.Products) != 1 || catalog.Products[0].PriceCents != 0 || catalog.Products[0].PurchaseBlockedReason != "" {
		t.Fatalf("production catalog=%+v err=%v", catalog, err)
	}
	created, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "zero-production", GuestName: "测试联系人", ContactPhone: "13800138000"})
	if err != nil || created.AmountCents != 0 || created.CoreOrderStatus != "unpaid" || created.PayToken == "" {
		t.Fatalf("production zero order=%v", err)
	}
	var count int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ? AND environment = 'production' AND total_amount = 0 AND status = 'unpaid'", f.tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("zero production order missing: %d", count)
	}
}

func TestXiaohongshuZeroPriceRequiresProviderConfirmationAndSupportsClosure(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	zeroPriceMapping(t, f)
	if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	var providerState atomic.Int32
	providerState.Store(1)
	var creates atomic.Int32
	var malformed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			fmt.Fprint(w, `{"data":{"access_token":"zero-token","expire_in":7200},"success":true,"code":0}`)
		case "/api/rmp/mp/deal/order/upsert":
			creates.Add(1)
			var request xiaohongshu.OrderUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Price.OrderPrice != 0 || request.Products[0].SalePrice != 0 || request.Products[0].RealPrice != 0 {
				t.Errorf("zero price rewritten: %+v", request)
			}
			fmt.Fprintf(w, `{"data":{"order_id":%q,"final_price":0,"pay_token":"provider-zero-token","expired_time":%d,"open_pay_type":"life_gpay"},"success":true,"code":0}`, request.ExternalOrderID, time.Now().Add(time.Hour).Unix())
		case "/api/rmp/mp/deal/gpay_order/get":
			var request xiaohongshu.GuaranteeOrderRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			amountField, providerID := `,"pay_amount":0`, request.ExternalOrderID
			switch malformed.Load() {
			case 1:
				amountField = ""
			case 2:
				amountField = `,"pay_amount":null`
			case 3:
				amountField = `,"pay_amount":1`
			case 4:
				providerID = ""
			}
			fmt.Fprintf(w, `{"data":{"order_id":%q,"order_status":%d%s,"voucher_infos":[{"voucher_code":"ZERO-A","voucher_status":1,"pay_amount":0},{"voucher_code":"ZERO-B","voucher_status":1,"pay_amount":0}]},"success":true,"code":0}`, providerID, providerState.Load(), amountField)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := XiaohongshuOrderService{NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, ClientRequestID: "zero-order", GuestName: "测试联系人", ContactPhone: "13800138000"}
	created, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	var order model.Order
	if err := model.DB.Where("order_no = ?", created.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "unpaid" || order.TotalAmount != 0 {
		t.Fatalf("premature zero completion: %+v", order)
	}
	if err := model.Write(func(tx *gorm.DB) error { return settleOrderIfFullyPaidTx(tx, &order) }); err == nil {
		t.Fatal("zero settled without provider evidence")
	}
	if _, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, input); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatalf("duplicate provider order: %d", creates.Load())
	}
	providerState.Store(6)
	for mode := int32(1); mode <= 4; mode++ {
		malformed.Store(mode)
		// The public query may return its durable unpaid state on transient errors.
		_, _ = service.GetXiaohongshuOrder(context.Background(), &f.customer, order.OrderNo)
		if err := model.DB.First(&order, order.ID).Error; err != nil {
			t.Fatal(err)
		}
		var payments int64
		if err := model.DB.Model(&model.Payment{}).Where("order_no = ?", order.OrderNo).Count(&payments).Error; err != nil {
			t.Fatal(err)
		}
		if order.Status != "unpaid" || payments != 0 {
			t.Fatalf("incomplete provider evidence mode %d produced payment", mode)
		}
	}
	malformed.Store(0)
	for i := 0; i < 2; i++ {
		if _, err := service.GetXiaohongshuOrder(context.Background(), &f.customer, order.OrderNo); err != nil {
			t.Fatal(err)
		}
	}
	if err := model.DB.First(&order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" {
		t.Fatalf("provider-confirmed zero order status=%s", order.Status)
	}
	projected, err := service.GetXiaohongshuOrder(context.Background(), &f.customer, order.OrderNo)
	if err != nil || projected.CanApplyRefund || !strings.Contains(projected.RefundApplicationMessage, "零元") {
		t.Fatalf("free order refund projection=%+v err=%v", projected, err)
	}
	if _, err := NewMiniappService().ApplyXiaohongshuRefund(&f.customer, order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "zero-return", Reason: "取消行程"}); err == nil || !strings.Contains(err.Error(), "零元") {
		t.Fatalf("free return must explain current provider limitation: %v", err)
	}
	for _, target := range []interface{}{&model.Refund{}, &model.DigitalRefundTask{}, &model.AfterSaleRequest{}} {
		var count int64
		if err := model.DB.Model(target).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("free return created financial work %T: count=%d err=%v", target, count, err)
		}
	}
	var payments []model.Payment
	model.DB.Where("order_no = ?", order.OrderNo).Find(&payments)
	if len(payments) != 1 || payments[0].AmountCents != 0 {
		t.Fatalf("zero confirmation records=%+v", payments)
	}
	var vouchers int64
	model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("tenant_id = ?", f.tenantID).Count(&vouchers)
	if vouchers != 2 {
		t.Fatalf("voucher bindings=%d", vouchers)
	}
	// Missing/null/positive voucher amounts cannot overwrite established evidence.
	for _, amountField := range []string{"", `,"pay_amount":null`, `,"pay_amount":1`} {
		var invalidVouchers []xiaohongshu.VoucherInfo
		if err := json.Unmarshal([]byte(`[{"voucher_code":"ZERO-A","voucher_status":1`+amountField+`},{"voucher_code":"ZERO-B","voucher_status":1,"pay_amount":0}]`), &invalidVouchers); err != nil {
			t.Fatal(err)
		}
		var link model.XiaohongshuOrderLink
		if err := model.DB.Where("order_id = ?", order.ID).First(&link).Error; err != nil {
			t.Fatal(err)
		}
		if err := service.issueXiaohongshuVouchers(&link, &order, invalidVouchers); err != nil {
			t.Fatal(err)
		}
		if err := model.DB.First(&link, link.ID).Error; err != nil {
			t.Fatal(err)
		}
		if link.VoucherIssuanceStatus != "manual_review" {
			t.Fatal("ambiguous voucher amount treated as confirmed zero")
		}
	}
	// A separate pending free order closes only on the provider's terminal state.
	providerState.Store(1)
	input.ClientRequestID = "zero-cancel"
	cancelled, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	providerState.Store(71)
	malformed.Store(4)
	_, _ = service.GetXiaohongshuOrder(context.Background(), &f.customer, cancelled.OrderNo)
	var pending model.Order
	if err := model.DB.Where("order_no = ?", cancelled.OrderNo).First(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != "unpaid" {
		t.Fatal("missing provider order identity cancelled zero order")
	}
	malformed.Store(0)
	if _, err := service.GetXiaohongshuOrder(context.Background(), &f.customer, cancelled.OrderNo); err != nil {
		t.Fatal(err)
	}
	var closed model.Order
	model.DB.Where("order_no = ?", cancelled.OrderNo).First(&closed)
	if closed.Status != "cancelled" {
		t.Fatalf("closed status=%s", closed.Status)
	}
}

func TestXiaohongshuZeroPriceRequiresExplicitUpsertAmount(t *testing.T) {
	for _, amountField := range []string{"", `,"final_price":null`, `,"final_price":1`} {
		t.Run(amountField, func(t *testing.T) {
			f := seedXiaohongshuOrderRegressionFixture(t)
			zeroPriceMapping(t, f)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/rmp/token" {
					fmt.Fprint(w, `{"data":{"access_token":"token","expire_in":7200},"success":true,"code":0}`)
					return
				}
				fmt.Fprintf(w, `{"data":{"order_id":"ZERO-PROVIDER"%s,"pay_token":"token","expired_time":%d,"open_pay_type":"life_gpay"},"success":true,"code":0}`, amountField, time.Now().Add(time.Hour).Unix())
			}))
			defer server.Close()
			service := XiaohongshuOrderService{NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
				return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
			}}
			result, _ := service.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "ambiguous-zero", GuestName: "测试联系人", ContactPhone: "13800138000"})
			if result != nil && result.PayToken != "" {
				t.Fatal("incomplete zero upsert exposed payment token")
			}
			var count int64
			if err := model.DB.Model(&model.Payment{}).Where("tenant_id = ?", f.tenantID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("incomplete zero upsert created payment")
			}
		})
	}
}

func TestXiaohongshuZeroPriceMappingRejectsNegativeAndCrossTenant(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	if err := model.DB.Unscoped().Delete(&f.mapping).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant    uint
		price     int64
		wantError bool
	}{{f.tenantID + 999, 0, true}, {f.tenantID, -1, true}, {f.tenantID, 0, false}} {
		mapping := model.ChannelProductMapping{ChannelAccountID: f.account.ID, ProductID: f.mapping.ProductID, ExternalCode: fmt.Sprintf("ZERO-%d-%d", tc.tenant, tc.price), ChannelSaleCents: tc.price}
		err := (&ChannelService{}).AddMapping(tc.tenant, &mapping)
		if (err != nil) != tc.wantError {
			t.Fatalf("tenant=%d price=%d err=%v", tc.tenant, tc.price, err)
		}
	}
}
