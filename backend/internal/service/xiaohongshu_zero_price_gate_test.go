package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"

	"gorm.io/gorm"
)

func TestXiaohongshuProductionZeroPriceGatePreventsNewResources(t *testing.T) {
	for _, entry := range []string{"storefront", "storefront_invalid_credentials", "core", "final_amount"} {
		t.Run(entry, func(t *testing.T) {
			f := seedXiaohongshuOrderRegressionFixture(t)
			if entry != "final_amount" {
				zeroPriceMapping(t, f)
			}
			if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", f.mapping.ProductID, f.tenantID).
				Updates(map[string]interface{}{"stock_type": "total", "daily_stock": 10}).Error; err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			service := XiaohongshuOrderService{NewXiaohongshuClient: func(string, string, string) *xiaohongshu.Client {
				calls.Add(1)
				t.Error("a locally rejected zero-price order must not contact the platform")
				return nil
			}}
			var err error
			if entry == "storefront" || entry == "storefront_invalid_credentials" {
				customer := f.customer
				if entry == "storefront_invalid_credentials" {
					customer.OpenIDCiphertext = "invalid-encrypted-session"
				}
				_, err = service.CreateXiaohongshuOrder(context.Background(), &customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, ClientRequestID: "blocked-zero", GuestName: "测试联系人", ContactPhone: "13800138000"})
			} else {
				order := model.Order{TenantID: f.tenantID, Channel: "xiaohongshu", ChannelAccountID: f.account.ID, Items: []model.OrderItem{{ProductID: f.mapping.ProductID, Quantity: 2}}}
				if entry == "core" {
					err = (&OrderService{}).Create(&order)
				} else {
					err = model.Write(func(tx *gorm.DB) error {
						return (&OrderService{}).createTx(tx, &order, func(_ *gorm.DB, priced *model.Order) error {
							priced.TotalAmount = 0
							return nil
						})
					})
				}
			}
			var rejected *MiniappOrderCreateError
			if !errors.As(err, &rejected) || rejected.Code != miniappOrderNotCreatedCode || rejected.Message != xiaohongshuZeroPriceBlockedReason || rejected.OrderNo != "" || calls.Load() != 0 {
				t.Fatalf("zero-price gate error=%v", err)
			}
			for _, table := range []interface{}{&model.Order{}, &model.XiaohongshuOrderLink{}, &model.XiaohongshuOrderOperation{}, &model.Ticket{}, &model.FulfillmentOrder{}, &model.Payment{}} {
				var count int64
				ownerColumn := "tenant_id"
				if _, ok := table.(*model.FulfillmentOrder); ok {
					ownerColumn = "sales_tenant_id"
				}
				if err := model.DB.Model(table).Where(ownerColumn+" = ?", f.tenantID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("rejected order created %T rows: %d", table, count)
				}
			}
			var product model.Product
			if err := model.DB.First(&product, f.mapping.ProductID).Error; err != nil || product.DailyStock != 10 {
				t.Fatalf("rejected order consumed inventory: stock=%d error=%v", product.DailyStock, err)
			}
		})
	}
}

func TestXiaohongshuExistingZeroPriceRequestRecoversBeforeNewProductionGate(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	zeroPriceMapping(t, f)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/rmp/token" {
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":{"access_token":"legacy-test","expire_in":7200}}`))
			return
		}
		calls.Add(1)
		_, _ = w.Write([]byte(`{"success":false,"code":400001,"msg":"订单总价必须大于0"}`))
	}))
	defer server.Close()
	service := XiaohongshuOrderService{NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "existing-zero", GuestName: "测试联系人", ContactPhone: "13800138000"}
	if _, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, input); err == nil {
		t.Fatal("simulated provider rejection missing")
	}
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("tenant_id = ? AND miniapp_customer_id = ?", f.tenantID, f.customer.ID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	before := captureCreatingXiaohongshuReadState(t, f.tenantID, link.OrderID, f.mapping.ProductID)
	got, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil || got.OrderNo != before.order.OrderNo || got.PlatformPaymentState != "creating" || got.PayToken != "" || calls.Load() != 1 {
		t.Fatalf("existing request recovery result=%+v error=%v", got, err)
	}
	after := captureCreatingXiaohongshuReadState(t, f.tenantID, link.OrderID, f.mapping.ProductID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("recovering an existing request rewrote the frozen order or task")
	}
	input.ClientRequestID = "new-production-zero"
	_, err = service.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	var rejected *MiniappOrderCreateError
	if !errors.As(err, &rejected) || rejected.Code != miniappOrderNotCreatedCode || calls.Load() != 1 {
		t.Fatalf("new zero request was not locally rejected: %v", err)
	}
}
