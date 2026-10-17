package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

type creatingXiaohongshuReadState struct {
	order        model.Order
	link         model.XiaohongshuOrderLink
	operation    model.XiaohongshuOrderOperation
	product      model.Product
	tickets      []model.Ticket
	fulfillments []model.FulfillmentOrder
	entitlements []model.TicketEntitlement
	payments     []model.Payment
	vouchers     []model.XiaohongshuVoucherLink
}

func captureCreatingXiaohongshuReadState(t *testing.T, tenantID, orderID, productID uint) creatingXiaohongshuReadState {
	t.Helper()
	var state creatingXiaohongshuReadState
	queries := []*gorm.DB{
		model.DB.Preload("Items").Where("id = ? AND tenant_id = ?", orderID, tenantID).First(&state.order),
		model.DB.Where("order_id = ? AND tenant_id = ?", orderID, tenantID).First(&state.link),
		model.DB.Where("xiaohongshu_order_link_id = ? AND tenant_id = ?", state.link.ID, tenantID).First(&state.operation),
		model.DB.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&state.product),
		model.DB.Where("order_id = ? AND tenant_id = ?", orderID, tenantID).Order("id").Find(&state.tickets),
		model.DB.Where("sales_order_id = ? AND sales_tenant_id = ?", orderID, tenantID).Order("id").Find(&state.fulfillments),
		model.DB.Where("sales_tenant_id = ? AND ticket_id IN (?)", tenantID,
			model.DB.Model(&model.Ticket{}).Select("id").Where("order_id = ? AND tenant_id = ?", orderID, tenantID)).Order("id").Find(&state.entitlements),
		model.DB.Where("order_no = ? AND tenant_id = ?", state.order.OrderNo, tenantID).Order("id").Find(&state.payments),
		model.DB.Where("xiaohongshu_order_link_id = ? AND tenant_id = ?", state.link.ID, tenantID).Order("id").Find(&state.vouchers),
	}
	for _, query := range queries {
		if query.Error != nil {
			t.Fatal(query.Error)
		}
	}
	return state
}

func TestGetCreatingXiaohongshuOrderWithoutPlatformIDIsReadOnly(t *testing.T) {
	for _, cents := range []int64{0, 1} {
		t.Run(fmt.Sprintf("price_%d", cents), func(t *testing.T) {
			fixture := seedXiaohongshuOrderRegressionFixture(t)
			if cents == 0 {
				zeroPriceMapping(t, fixture)
			}
			if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", fixture.mapping.ProductID, fixture.tenantID).
				Updates(map[string]interface{}{"stock_type": "total", "daily_stock": 10}).Error; err != nil {
				t.Fatal(err)
			}
			wantStock := 10 // Zero-price fixtures use only the simulated sandbox provider.
			if cents > 0 {
				if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ? AND tenant_id = ?", fixture.account.ID, fixture.tenantID).
					Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
					t.Fatal(err)
				}
				wantStock = 8
			}

			var acceptUpsert, rejectQuery atomic.Bool
			var queryCalls atomic.Int32
			var requestsMu sync.Mutex
			var requests []xiaohongshu.OrderUpsertRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/rmp/token":
					_, _ = w.Write([]byte(`{"data":{"access_token":"creating-test-token","expire_in":7200},"success":true,"code":0}`))
				case "/api/rmp/mp/deal/order/upsert":
					var request xiaohongshu.OrderUpsertRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode upsert: %v", err)
						http.Error(w, "invalid test request", http.StatusBadRequest)
						return
					}
					requestsMu.Lock()
					requests = append(requests, request)
					requestsMu.Unlock()
					if !acceptUpsert.Load() {
						_, _ = w.Write([]byte(`{"success":false,"code":400001,"msg":"invalid order parameters"}`))
						return
					}
					_, _ = fmt.Fprintf(w, `{"data":{"order_id":"creating-platform-order","out_order_id":%q,"final_price":%d,"pay_token":"creating-pay-token","open_pay_type":"life_gpay","expired_time":%d},"success":true,"code":0}`,
						request.ExternalOrderID, request.Price.OrderPrice, request.ExpiresAt)
				case "/api/rmp/mp/deal/gpay_order/get":
					queryCalls.Add(1)
					if !acceptUpsert.Load() || rejectQuery.Load() {
						_, _ = w.Write([]byte(`{"success":false,"code":400001,"msg":"invalid query parameters"}`))
						return
					}
					_, _ = fmt.Fprintf(w, `{"data":{"order_id":"creating-platform-order","order_status":1,"pay_amount":%d},"success":true,"code":0}`, cents*2)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			clock := time.Now().Truncate(time.Second)
			service := XiaohongshuOrderService{
				Now: func() time.Time { return clock },
				NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
					return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
				},
			}
			input := MiniappOrderCreateInput{MappingID: fixture.mapping.ID, Quantity: 2, ClientRequestID: "creating-read-only", GuestName: "测试联系人", ContactPhone: "13800138000"}
			result, err := service.CreateXiaohongshuOrder(context.Background(), &fixture.customer, input)
			var apiErr *xiaohongshu.APIError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Code != 400001 {
				t.Fatalf("upsert rejection result=%+v error=%v", result, err)
			}
			var link model.XiaohongshuOrderLink
			if err := model.DB.Where("miniapp_customer_id = ? AND tenant_id = ? AND client_request_id = ?", fixture.customer.ID, fixture.tenantID, input.ClientRequestID).First(&link).Error; err != nil {
				t.Fatal(err)
			}
			initial := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
			if initial.order.Status != "unpaid" || initial.link.State != "creating" || initial.operation.Status != "pending" || initial.operation.AttemptCount != 1 || initial.operation.NextAttemptAt == nil || initial.operation.RequestPayloadCiphertext == "" || initial.product.DailyStock != wantStock {
				t.Fatal("provider rejection did not retain the pending operation and reserved inventory")
			}
			otherMember, otherAccount, otherTenant := fixture.customer, fixture.customer, fixture.customer
			otherMember.ID++
			otherAccount.ChannelAccountID++
			otherTenant.TenantID++
			for name, customer := range map[string]*model.MiniappCustomer{"member": &otherMember, "channel": &otherAccount, "tenant": &otherTenant} {
				if got, err := service.GetXiaohongshuOrder(context.Background(), customer, initial.order.OrderNo); got != nil || !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("another %s read the creating order: result=%+v error=%v", name, got, err)
				}
			}
			for _, platformID := range []string{"", " \t "} {
				if err := model.DB.Model(&model.XiaohongshuOrderLink{}).Where("id = ?", link.ID).Update("platform_order_id", platformID).Error; err != nil {
					t.Fatal(err)
				}
				before := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
				for i := 0; i < 2; i++ {
					got, err := service.GetXiaohongshuOrder(context.Background(), &fixture.customer, " "+initial.order.OrderNo+" ")
					if err != nil {
						t.Fatalf("creating order read must not query the uncreated platform order: %v", err)
					}
					if got.Status != "unpaid" || got.CoreOrderStatus != "unpaid" || got.PlatformPaymentState != "creating" || got.AmountCents != cents*2 || strings.TrimSpace(got.PlatformOrderID) != "" || got.PayToken != "" || len(got.TicketCodes) != 0 || len(got.Tickets) != 0 {
						t.Fatalf("creating read exposed payment or ticket facts: %+v", got)
					}
				}
				after := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
				if !reflect.DeepEqual(before, after) || queryCalls.Load() != 0 || len(after.payments) != 0 || len(after.vouchers) != 0 {
					t.Fatal("reading a creating order mutated its task, inventory, payment or ticket resources")
				}
			}
			// Restore the whitespace-only legacy fixture to the genuine failed-upsert state.
			if err := model.DB.Model(&model.XiaohongshuOrderLink{}).Where("id = ?", link.ID).Update("platform_order_id", "").Error; err != nil {
				t.Fatal(err)
			}
			acceptUpsert.Store(true)
			clock = clock.Add(21 * time.Second)
			claimed, ok, err := service.claimXiaohongshuOrderOperation(initial.operation.ID)
			if err != nil || !ok {
				t.Fatalf("claim pending retry: claimed=%t error=%v", ok, err)
			}
			if completed, err := service.executeXiaohongshuOrderRemoteStep(context.Background(), claimed); err != nil || completed {
				t.Fatalf("retry remote step completed=%t error=%v", completed, err)
			}
			remoteSucceeded := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
			if remoteSucceeded.operation.Status != "remote_succeeded" || remoteSucceeded.operation.PlatformOrderID == "" || remoteSucceeded.operation.PayTokenCiphertext == "" || remoteSucceeded.link.State != "creating" || remoteSucceeded.link.PlatformOrderID != "" {
				t.Fatal("remote success was not retained independently of local finalization")
			}
			for i := 0; i < 2; i++ {
				got, err := service.GetXiaohongshuOrder(context.Background(), &fixture.customer, initial.order.OrderNo)
				if err != nil || got.CoreOrderStatus != "unpaid" || got.PlatformPaymentState != "creating" || got.PlatformOrderID != "" || got.PayToken != "" || len(got.TicketCodes) != 0 {
					t.Fatalf("unfinalized remote success exposed platform facts: result=%+v error=%v", got, err)
				}
			}
			afterRemoteReads := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
			if !reflect.DeepEqual(remoteSucceeded, afterRemoteReads) || queryCalls.Load() != 0 {
				t.Fatal("reading unfinalized remote success queried the provider or modified the durable operation")
			}
			restarted := XiaohongshuOrderService{Now: service.Now, NewXiaohongshuClient: service.NewXiaohongshuClient}
			if processed, err := restarted.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || processed < 1 {
				t.Fatalf("worker retry processed=%d error=%v", processed, err)
			}
			advanced := captureCreatingXiaohongshuReadState(t, fixture.tenantID, link.OrderID, fixture.mapping.ProductID)
			if advanced.operation.Status != "completed" || advanced.operation.RequestPayloadCiphertext != initial.operation.RequestPayloadCiphertext || advanced.operation.AttemptCount != 1 || advanced.link.State != "unpaid" || advanced.link.PlatformOrderID != "creating-platform-order" || advanced.order.Status != "unpaid" || advanced.product.DailyStock != wantStock || len(advanced.payments) != 0 || len(advanced.vouchers) != 0 {
				t.Fatal("worker retry failed to advance the original order without manufacturing payment or reserving twice")
			}
			requestsMu.Lock()
			requestCopy := append([]xiaohongshu.OrderUpsertRequest(nil), requests...)
			requestsMu.Unlock()
			if len(requestCopy) != 2 || !reflect.DeepEqual(requestCopy[0], requestCopy[1]) || requestCopy[0].ExternalOrderID != initial.link.ExternalOrderID {
				t.Fatal("worker retry did not reuse the exact persisted external request")
			}
			rejectQuery.Store(true)
			previousQueries := queryCalls.Load()
			got, err := restarted.GetXiaohongshuOrder(context.Background(), &fixture.customer, initial.order.OrderNo)
			if got != nil || !errors.As(err, &apiErr) || apiErr.Code != 400001 || queryCalls.Load() != previousQueries+1 {
				t.Fatalf("existing platform order must still query and propagate provider errors: result=%+v error=%v", got, err)
			}
		})
	}
}
