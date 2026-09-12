package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

func TestXiaohongshuBookingIgnoresPlatformPayDetailForFrontDeskDifference(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 1)
	if err := model.DB.Model(&model.ScenicHotelPackage{}).Where("id = ?", fixture.packageView.ID).Updates(map[string]interface{}{
		"booking_mode": "after_purchase", "voucher_validity_days": 90,
	}).Error; err != nil {
		t.Fatal(err)
	}
	account := seedDeferredPackageXiaohongshuAccount(t, fixture, "xhs-pay-detail", "active", "production")
	secret, err := utils.EncryptAES("app-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&account).Updates(map[string]interface{}{"app_id": "app-id", "secret_ciphertext": secret}).Error; err != nil {
		t.Fatal(err)
	}

	externalOrderNo := "XHS-PAY-DETAIL-ORDER"
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: account.ID, ExternalNo: &externalOrderNo, Items: []model.OrderItem{{ProductID: fixture.productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, fixture.tenantID); err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("OPEN-PAY-DETAIL"),
		OpenIDCiphertext: "encrypted", Status: "active",
	}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := model.XiaohongshuOrderLink{TenantID: fixture.tenantID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ExternalOrderID: externalOrderNo, State: "paid"}
	if err := model.DB.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	var entitlement model.ScenicHotelPackageEntitlement
	if err := model.DB.Where("order_id = ?", order.ID).First(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	externalBookID := "XHS-PAY-DETAIL-BOOK"
	if err := model.Write(func(tx *gorm.DB) error {
		_, prepareErr := (PackageFulfillmentLifecycle{}).PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: entitlement.EntitlementNo, CheckInDate: fixture.checkIn, GuestName: "补差价游客",
			ContactPhone: "13800138000", ClientRequestID: "pay-detail-book", ExternalBookOrderID: externalBookID,
		})
		return prepareErr
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{
		OpenID: "OPEN-PAY-DETAIL", ExternalOrderID: externalOrderNo, ExternalProductID: "XHS-PAY-DETAIL-PRODUCT",
		ExternalSKUID: "XHS-PAY-DETAIL-SKU", POIID: "POI-1", VoucherCode: "VOUCHER-PAY-DETAIL",
		VoucherCodeHash: hashMiniappValue("VOUCHER-PAY-DETAIL"), CheckInDate: fixture.checkIn.Format("2006-01-02"),
		CheckOutDate: fixture.checkIn.AddDate(0, 0, 2).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	operation := model.XiaohongshuBookingOperation{
		TenantID: fixture.tenantID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "xhs:book:pay-detail", Type: "book", Status: "pending", ExternalBookOrderID: externalBookID,
		RequestPayloadCiphertext: payload, MaxAttempts: 20, NextAttemptAt: &now,
	}
	if err := model.DB.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}

	bookCalls, confirmCalls, compensationCalls := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"ACCESS","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/pre_sale/book":
			bookCalls++
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"XHS-PAY-DETAIL-ORDER","out_book_order_id":"XHS-PAY-DETAIL-BOOK","pay_detail":{"order_id":"DIFF-1","final_price":100,"pay_token":"DIFF-TOKEN","expired_time":1786349700},"book_result":[{"book_id":"PLATFORM-PAY-DETAIL","voucher_code":"VOUCHER-PAY-DETAIL"}]},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/pre_sale/sync_status":
			var request xiaohongshu.PresaleBookStatusRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			switch request.Status {
			case 1:
				confirmCalls++
			case 2:
				compensationCalls++
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

	processed, err := miniapp.ProcessPendingXiaohongshuBookingSyncs(context.Background(), 10)
	if err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	if bookCalls != 1 || confirmCalls != 1 || compensationCalls != 0 {
		t.Fatalf("book=%d confirm=%d compensation=%d", bookCalls, confirmCalls, compensationCalls)
	}
	var completed model.XiaohongshuBookingOperation
	if err := model.DB.First(&completed, operation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.CompletedAt == nil || completed.PlatformBookID != "PLATFORM-PAY-DETAIL" {
		t.Fatalf("operation=%+v", completed)
	}
	var released model.ScenicHotelPackageEntitlement
	if err := model.DB.First(&released, entitlement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if released.Status != "booked" || released.ReservationID == 0 || released.ExternalBookOrderID != externalBookID || released.PlatformBookID != "PLATFORM-PAY-DETAIL" {
		t.Fatalf("entitlement was not confirmed for front-desk difference: %+v", released)
	}
}
