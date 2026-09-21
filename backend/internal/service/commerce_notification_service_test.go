package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func resetCommerceNotificationData(t *testing.T) {
	t.Helper()
	for _, table := range []interface{}{
		&model.CommerceMerchantNotification{}, &model.CommerceOrderPaidOutbox{},
	} {
		if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error; err != nil {
			t.Fatalf("reset notification table %T: %v", table, err)
		}
	}
}

func createCommerceNotificationTenant(t *testing.T, label string) uint {
	t.Helper()
	tenant := &model.Tenant{
		Name:       "Commerce notification " + label,
		SystemCode: fmt.Sprintf("CN-%s-%d", label, time.Now().UnixNano()),
		SecretKey:  "notification-secret",
		Status:     "active",
	}
	if err := model.DB.Create(tenant).Error; err != nil {
		t.Fatalf("create notification tenant: %v", err)
	}
	return tenant.ID
}

func TestCommerceNotificationProjectionIsIdempotent(t *testing.T) {
	resetCommerceNotificationData(t)
	tenantID := createCommerceNotificationTenant(t, "projection")
	payload, err := json.Marshal(commercePaidOutboxPayload{
		TenantID: tenantID, OrderID: 901, OrderNo: "COM-901", BusinessType: "restaurant",
		LocationID: 21, TotalAmountCents: 1200, PaidAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.CommerceOrderPaidOutbox{
		TenantID: tenantID, OrderID: 901, EventType: "order.paid", EventKey: "order.paid:901",
		PayloadJSON: string(payload), Status: "pending",
	}).Error; err != nil {
		t.Fatalf("create paid outbox: %v", err)
	}

	service := &CommerceNotificationService{}
	projected, err := service.ProjectPending(context.Background(), 20)
	if err != nil || projected != 1 {
		t.Fatalf("first projection count=%d err=%v", projected, err)
	}
	projected, err = service.ProjectPending(context.Background(), 20)
	if err != nil || projected != 0 {
		t.Fatalf("repeated projection count=%d err=%v", projected, err)
	}
	var count int64
	if err := model.DB.Model(&model.CommerceMerchantNotification{}).Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notification count=%d, want 1", count)
	}
	var outbox model.CommerceOrderPaidOutbox
	if err := model.DB.Where("tenant_id = ? AND event_key = ?", tenantID, "order.paid:901").First(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if outbox.Status != "processed" || outbox.Attempts != 1 {
		t.Fatalf("outbox=%+v, want processed once", outbox)
	}
}

func TestCommerceNotificationProjectionCommitsRetryableFailure(t *testing.T) {
	resetCommerceNotificationData(t)
	tenantID := createCommerceNotificationTenant(t, "failure")
	if err := model.DB.Create(&model.CommerceOrderPaidOutbox{
		TenantID: tenantID, OrderID: 902, EventType: "order.paid", EventKey: "order.paid:902",
		PayloadJSON: "{broken", Status: "pending",
	}).Error; err != nil {
		t.Fatalf("create malformed outbox: %v", err)
	}
	service := &CommerceNotificationService{}
	if _, err := service.ProjectPending(context.Background(), 20); err == nil {
		t.Fatal("malformed outbox did not report projection failure")
	}
	var outbox model.CommerceOrderPaidOutbox
	if err := model.DB.Where("event_key = ?", "order.paid:902").First(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if outbox.Status != "failed" || outbox.NextAttemptAt == nil || outbox.LastError == "" {
		t.Fatalf("failed outbox=%+v, want committed retry marker", outbox)
	}
}

func TestCommerceNotificationIncrementalCursorAndTenantIsolation(t *testing.T) {
	resetCommerceNotificationData(t)
	tenantID := createCommerceNotificationTenant(t, "cursor")
	otherTenantID := createCommerceNotificationTenant(t, "other")
	rows := make([]model.CommerceMerchantNotification, 0, 4)
	for index := 1; index <= 3; index++ {
		rows = append(rows, model.CommerceMerchantNotification{
			TenantID: tenantID, EventKey: fmt.Sprintf("event:%d", index), EventType: "order.paid",
			OrderID: uint(100 + index), OrderNo: fmt.Sprintf("COM-%d", index), BusinessType: "restaurant",
			LocationID: 21, Title: "新订单已支付", Body: "请处理", Status: "unread",
		})
	}
	rows = append(rows, model.CommerceMerchantNotification{
		TenantID: otherTenantID, EventKey: "event:other", EventType: "order.paid", OrderID: 999,
		OrderNo: "OTHER", BusinessType: "restaurant", LocationID: 21, Title: "其他租户", Body: "隔离", Status: "unread",
	})
	if err := model.DB.Create(&rows).Error; err != nil {
		t.Fatalf("create notifications: %v", err)
	}

	var all *CommerceNotificationPage
	var err error
	if all, err = (&CommerceNotificationService{}).List(CommerceNotificationFilter{TenantID: tenantID, PageSize: 20}); err != nil {
		t.Fatal(err)
	}
	if len(all.Data) != 3 || all.Data[0].ID < all.Data[1].ID || all.Data[1].ID < all.Data[2].ID {
		t.Fatalf("initial notification order=%v, want tenant rows newest first", all.Data)
	}
	latestSeen := all.Data[1].ID
	incremental, err := (&CommerceNotificationService{}).List(CommerceNotificationFilter{TenantID: tenantID, AfterID: latestSeen, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(incremental.Data) != 1 || incremental.Data[0].ID <= latestSeen || incremental.Data[0].TenantID != tenantID {
		t.Fatalf("incremental rows=%v, want only id > %d for tenant %d", incremental.Data, latestSeen, tenantID)
	}

	if err := (&CommerceNotificationService{}).MarkRead(tenantID, all.Data[0].ID); err != nil {
		t.Fatalf("mark own notification read: %v", err)
	}
	var read model.CommerceMerchantNotification
	if err := model.DB.First(&read, all.Data[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if read.Status != "read" || read.ReadAt == nil {
		t.Fatalf("read notification=%+v", read)
	}
	if err := (&CommerceNotificationService{}).MarkRead(tenantID, rows[3].ID); err == nil {
		t.Fatal("cross-tenant notification was marked read")
	}
	count, err := (&CommerceNotificationService{}).UnreadCount(CommerceNotificationFilter{TenantID: tenantID})
	if err != nil || count != 2 {
		t.Fatalf("tenant unread count=%d err=%v, want 2", count, err)
	}
}

func TestCommerceNotificationUnreadCountRejectsUnsupportedBusinessType(t *testing.T) {
	resetCommerceNotificationData(t)
	tenantID := createCommerceNotificationTenant(t, "invalid-business")
	if _, err := (&CommerceNotificationService{}).UnreadCount(CommerceNotificationFilter{
		TenantID: tenantID, BusinessType: "campus",
	}); err == nil {
		t.Fatal("unsupported business type was accepted by unread count")
	}
}
