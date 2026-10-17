package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

const exactZeroPriceRejection = "xiaohongshu API rejected request (400001): 参数不合法:订单总价必须大于 0"

// Reconstruct a pre-gate sale using the same inventory, revision, ticket and
// fulfillment helpers. Every current PostgreSQL ownership constraint stays on.
func seedLegacyXiaohongshuOperation(t *testing.T, f xiaohongshuOrderRegressionFixture, now time.Time, environment string, totalCents int64, lastError string) creatingXiaohongshuReadState {
	t.Helper()
	status := "active"
	if environment == "sandbox" {
		status = "sandbox"
	}
	if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": environment, "status": status}).Error; err != nil {
		t.Fatal(err)
	}
	var order model.Order
	err := model.Write(func(tx *gorm.DB) error {
		var product model.Product
		if err := tx.Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", f.mapping.ProductID, f.tenantID).First(&product).Error; err != nil {
			return err
		}
		if product.StockType != "total" {
			if err := tx.Model(&product).Updates(map[string]interface{}{"stock_type": "total", "daily_stock": 10}).Error; err != nil {
				return err
			}
		}
		revision, err := ensureProductRevisionTx(tx, &product)
		if err != nil {
			return err
		}
		product.CurrentRevisionID = revision.ID
		externalID, err := randomXiaohongshuOrderID()
		if err != nil {
			return err
		}
		expiresAt := now.Add(time.Hour)
		order = model.Order{TenantID: f.tenantID, OrderNo: (&OrderService{}).GenerateOrderNo(), Channel: xiaohongshuPaymentMethod,
			ChannelAccountID: f.account.ID, ExternalNo: &externalID, Environment: environment, Status: "unpaid", ExpiresAt: &expiresAt,
			TotalAmount: centsMoney(totalCents), OriginalAmountCents: totalCents, ContactName: "测试联系人", ContactPhone: "13800138000"}
		tickets, err := buildTickets(&OrderService{}, &product, 2, &order)
		if err != nil {
			return err
		}
		order.Items = []model.OrderItem{{ProductID: product.ID, ProductName: product.Name, Quantity: 2, Price: centsMoney(totalCents / 2),
			FulfillmentProductID: product.ID, FulfillmentTenantID: f.tenantID, FulfillmentScenicAreaID: product.ScenicAreaID,
			ProductRevisionID: revision.ID, ReservedStockType: "total", RefundType: product.RefundType, Tickets: tickets}}
		if environment == "production" {
			if err := reserveStock(tx, &product, nil, "", 2); err != nil {
				return err
			}
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE tickets SET order_id = ? WHERE order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", order.ID, order.ID).Error; err != nil {
			return err
		}
		if err := createFulfillmentProjections(tx, &OrderService{}, &order); err != nil {
			return err
		}
		link := model.XiaohongshuOrderLink{TenantID: f.tenantID, ChannelAccountID: f.account.ID, MiniappCustomerID: f.customer.ID,
			OrderID: order.ID, ClientRequestID: "legacy-" + externalID, ExternalOrderID: externalID, State: "creating"}
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		openID, err := utils.DecryptAES(f.customer.OpenIDCiphertext)
		if err != nil {
			return err
		}
		request := xiaohongshu.OrderUpsertRequest{ExternalOrderID: externalID, OpenID: openID, Path: miniappPathWithOrder("/pages/order/detail", order.OrderNo),
			CreatedAt: order.CreatedAt.Unix(), ExpiresAt: expiresAt.Unix(), Price: xiaohongshu.OrderPrice{OrderPrice: totalCents},
			Products: []xiaohongshu.OrderProduct{{ExternalProductID: f.mapping.ExternalCode, ExternalSKUID: "XHS-REGRESSION-SKU", Count: 2, SalePrice: totalCents, RealPrice: totalCents}}}
		intent := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, GuestName: order.ContactName, ContactPhone: order.ContactPhone}
		ciphertext, err := encryptXiaohongshuOrderOperationPayloadWithFingerprint(request, miniappOrderIntentFingerprint(intent), 1)
		if err != nil {
			return err
		}
		return tx.Create(&model.XiaohongshuOrderOperation{TenantID: f.tenantID, ChannelAccountID: f.account.ID, XiaohongshuOrderLinkID: link.ID,
			RequestPayloadCiphertext: ciphertext, Status: "pending", AttemptCount: 2, LastError: lastError, NextAttemptAt: &now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return captureCreatingXiaohongshuReadState(t, f.tenantID, order.ID, f.mapping.ProductID)
}

func zeroPriceHoldTestService(t *testing.T, clock *time.Time, upserts *atomic.Int32) XiaohongshuOrderService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":{"access_token":"hold-test","expire_in":7200}}`))
		case "/api/rmp/mp/deal/order/upsert":
			upserts.Add(1)
			var request xiaohongshu.OrderUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode simulated upsert: %v", err)
				return
			}
			_, _ = fmt.Fprintf(w, `{"success":true,"code":0,"data":{"order_id":%q,"final_price":%d,"pay_token":"simulated-pay-token","open_pay_type":"life_gpay","expired_time":%d}}`, request.ExternalOrderID, request.Price.OrderPrice, request.ExpiresAt)
		case "/api/rmp/mp/deal/gpay_order/get":
			// Query errors must not prevent a completed create operation from
			// retaining its real remote-success evidence.
			_, _ = w.Write([]byte(`{"success":false,"code":400001,"msg":"simulated query unavailable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return XiaohongshuOrderService{Now: func() time.Time { return *clock }, NewXiaohongshuClient: func(appID, secret, environment string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
}

func TestXiaohongshuKnownProductionZeroPriceRejectionIsDeferredWithoutHTTP(t *testing.T) {
	f := seedXiaohongshuOrderRegressionFixture(t)
	clock := time.Now().Truncate(time.Second)
	before := seedLegacyXiaohongshuOperation(t, f, clock, "production", 0, exactZeroPriceRejection)
	var upserts atomic.Int32
	service := zeroPriceHoldTestService(t, &clock, &upserts)
	for _, advance := range []time.Duration{0, time.Hour, 25 * time.Hour} {
		clock = clock.Add(advance)
		if processed, err := service.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || processed != 0 || upserts.Load() != 0 {
			t.Fatalf("known zero-price rejection was retried: processed=%d sends=%d error=%v", processed, upserts.Load(), err)
		}
		after := captureCreatingXiaohongshuReadState(t, f.tenantID, before.order.ID, f.mapping.ProductID)
		if after.operation.NextAttemptAt == nil || !after.operation.NextAttemptAt.After(clock) || after.operation.NextAttemptAt.Sub(clock) > 24*time.Hour {
			t.Fatal("known rejection has no bounded long retry delay")
		}
		after.operation.NextAttemptAt = before.operation.NextAttemptAt
		after.operation.UpdatedAt = before.operation.UpdatedAt
		if !reflect.DeepEqual(before, after) {
			t.Fatal("holding a known rejection changed frozen facts, attempts or reserved resources")
		}
	}
	beforeReads := captureCreatingXiaohongshuReadState(t, f.tenantID, before.order.ID, f.mapping.ProductID)
	view, err := service.GetXiaohongshuOrder(context.Background(), &f.customer, before.order.OrderNo)
	if err != nil || view.OrderCreationBlockedReason != xiaohongshuExistingZeroPriceBlockedReason || view.CoreOrderStatus != "unpaid" || view.PlatformPaymentState != "creating" || view.PayToken != "" || len(view.TicketCodes) != 0 {
		t.Fatalf("safe blocked projection result=%+v error=%v", view, err)
	}
	replay, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2,
		ClientRequestID: before.link.ClientRequestID, GuestName: before.order.ContactName, ContactPhone: before.order.ContactPhone})
	if err != nil || replay.OrderNo != before.order.OrderNo || replay.OrderCreationBlockedReason != xiaohongshuExistingZeroPriceBlockedReason || upserts.Load() != 0 {
		t.Fatalf("old production request was blocked by the new-sale gate: result=%+v error=%v", replay, err)
	}
	if afterReads := captureCreatingXiaohongshuReadState(t, f.tenantID, before.order.ID, f.mapping.ProductID); !reflect.DeepEqual(beforeReads, afterReads) {
		t.Fatal("blocked projection or idempotent recovery changed the held operation")
	}
	positive := seedLegacyXiaohongshuOperation(t, f, clock, "production", 2, "temporary timeout")
	if processed, err := service.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || processed < 1 || upserts.Load() != 1 {
		t.Fatalf("held zero order blocked the next positive order: processed=%d sends=%d error=%v", processed, upserts.Load(), err)
	}
	var completed model.XiaohongshuOrderOperation
	if err := model.DB.First(&completed, positive.operation.ID).Error; err != nil || completed.Status != "completed" {
		t.Fatalf("positive task status=%s error=%v", completed.Status, err)
	}
	var product model.Product
	if err := model.DB.First(&product, f.mapping.ProductID).Error; err != nil || product.DailyStock != 6 {
		t.Fatalf("zero and positive reservations were not conserved: stock=%d error=%v", product.DailyStock, err)
	}
}

func TestXiaohongshuZeroPriceHoldDoesNotCatchOtherRecoverableStates(t *testing.T) {
	for _, test := range []struct {
		name, environment, failure string
		amount                     int64
	}{
		{"unknown_timeout", "production", "context deadline exceeded", 0},
		{"other_400001", "production", "xiaohongshu API rejected request (400001): 商品编码不合法", 0},
		{"other_api_code", "production", "xiaohongshu API rejected request (400002): 订单总价必须大于0", 0},
		{"simulated_sandbox", "sandbox", exactZeroPriceRejection, 0},
		{"positive_amount", "production", exactZeroPriceRejection, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := seedXiaohongshuOrderRegressionFixture(t)
			clock := time.Now().Truncate(time.Second)
			before := seedLegacyXiaohongshuOperation(t, f, clock, test.environment, test.amount, test.failure)
			var upserts atomic.Int32
			service := zeroPriceHoldTestService(t, &clock, &upserts)
			if processed, err := service.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || processed < 1 || upserts.Load() != 1 {
				t.Fatalf("recoverable order was held: processed=%d sends=%d error=%v", processed, upserts.Load(), err)
			}
			var operation model.XiaohongshuOrderOperation
			if err := model.DB.First(&operation, before.operation.ID).Error; err != nil || operation.Status != "completed" || operation.RequestPayloadCiphertext != before.operation.RequestPayloadCiphertext {
				t.Fatal("ordinary recovery lost its original frozen request")
			}
		})
	}
}

func TestXiaohongshuZeroPriceHoldCannotOverwriteNewerLeaseOrRemoteSuccess(t *testing.T) {
	for _, newer := range []string{"lease", "remote_succeeded"} {
		t.Run(newer, func(t *testing.T) {
			f := seedXiaohongshuOrderRegressionFixture(t)
			clock := time.Now().Truncate(time.Second)
			before := seedLegacyXiaohongshuOperation(t, f, clock, "production", 0, exactZeroPriceRejection)
			var upserts atomic.Int32
			service := zeroPriceHoldTestService(t, &clock, &upserts)
			stale, claimed, err := service.claimXiaohongshuOrderOperation(before.operation.ID)
			if err != nil || !claimed {
				t.Fatalf("claim error=%v", err)
			}
			updated := map[string]interface{}{"next_attempt_at": clock.Add(2 * time.Minute)}
			if newer == "remote_succeeded" {
				ciphertext, err := utils.EncryptAES("persisted-success-token")
				if err != nil {
					t.Fatal(err)
				}
				updated = map[string]interface{}{"status": "remote_succeeded", "platform_order_id": "persisted-platform", "pay_token_ciphertext": ciphertext,
					"pay_token_expires_at": clock.Add(time.Hour), "next_attempt_at": clock}
			}
			if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("id = ?", before.operation.ID).Updates(updated).Error; err != nil {
				t.Fatal(err)
			}
			var current model.XiaohongshuOrderOperation
			if err := model.DB.First(&current, before.operation.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.deferRejectedXiaohongshuZeroPriceOperation(stale); err != nil {
				t.Fatal(err)
			}
			var after model.XiaohongshuOrderOperation
			if err := model.DB.First(&after, before.operation.ID).Error; err != nil || !reflect.DeepEqual(current, after) {
				t.Fatal("stale hold overwrote a newer lease or persisted success")
			}
			if newer == "remote_succeeded" {
				if _, err := service.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || upserts.Load() != 0 {
					t.Fatalf("remote success was resent or blocked: sends=%d error=%v", upserts.Load(), err)
				}
				if err := model.DB.First(&after, before.operation.ID).Error; err != nil || after.Status != "completed" {
					t.Fatalf("remote success failed to finalize: status=%s error=%v", after.Status, err)
				}
			}
		})
	}
}

func TestXiaohongshuZeroPriceHoldRequiresNoPersistedSuccessEvidence(t *testing.T) {
	for _, target := range []string{"operation_platform_id", "operation_token", "operation_expiry", "link_platform_id", "link_token", "link_expiry"} {
		t.Run(target, func(t *testing.T) {
			f := seedXiaohongshuOrderRegressionFixture(t)
			clock := time.Now().Truncate(time.Second)
			before := seedLegacyXiaohongshuOperation(t, f, clock, "production", 0, exactZeroPriceRejection)
			ciphertext, err := utils.EncryptAES("persisted-partial-success")
			if err != nil {
				t.Fatal(err)
			}
			table, id := interface{}(&model.XiaohongshuOrderOperation{}), before.operation.ID
			column, value := "platform_order_id", interface{}(before.link.ExternalOrderID)
			switch target {
			case "operation_token", "link_token":
				column, value = "pay_token_ciphertext", ciphertext
			case "operation_expiry", "link_expiry":
				column, value = "pay_token_expires_at", clock.Add(time.Hour)
			}
			if target == "link_platform_id" || target == "link_token" || target == "link_expiry" {
				table, id = &model.XiaohongshuOrderLink{}, before.link.ID
			}
			if err := model.DB.Model(table).Where("id = ?", id).Update(column, value).Error; err != nil {
				t.Fatal(err)
			}
			var upserts atomic.Int32
			service := zeroPriceHoldTestService(t, &clock, &upserts)
			if _, err := service.ProcessPendingXiaohongshuOrders(context.Background(), clock, 20); err != nil || upserts.Load() != 1 {
				t.Fatalf("persisted success evidence was incorrectly held: sends=%d error=%v", upserts.Load(), err)
			}
			var operation model.XiaohongshuOrderOperation
			if err := model.DB.First(&operation, before.operation.ID).Error; err != nil || operation.Status != "completed" {
				t.Fatalf("partial success evidence did not recover: status=%s error=%v", operation.Status, err)
			}
		})
	}
}
