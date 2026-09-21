package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func migrateCommerceNotificationControllerTables(t *testing.T, db interface{ AutoMigrate(...interface{}) error }) {
	t.Helper()
	if err := db.AutoMigrate(&model.CommerceMerchantNotification{}); err != nil {
		t.Fatalf("migrate commerce notification controller tables: %v", err)
	}
}

func invokeCommerceNotificationController(t *testing.T, method, path string, tenantID uint, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(nil))
	request.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	ctx.Params = params
	ctx.Set("tenant_id", tenantID)
	ctx.Set("scope", "tenant")
	handler(ctx)
	ctx.Writer.WriteHeaderNow()
	return recorder
}

func TestCommerceNotificationControllerScopesQueriesAndMarkRead(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	migrateCommerceNotificationControllerTables(t, db)
	owner := createCommerceControllerTenant(t, db, "notification controller owner", "restaurant")
	foreign := createCommerceControllerTenant(t, db, "notification controller foreign", "restaurant")
	ownerRows := []model.CommerceMerchantNotification{
		{TenantID: owner.ID, EventKey: "controller-owner-1", EventType: "order.paid", OrderID: 901,
			OrderNo: "COM-CTRL-901", BusinessType: "restaurant", LocationID: 41,
			Title: "新订单已支付", Body: "请及时处理", Status: "unread"},
		{TenantID: owner.ID, EventKey: "controller-owner-2", EventType: "order.paid", OrderID: 902,
			OrderNo: "COM-CTRL-902", BusinessType: "retail", LocationID: 42,
			Title: "新订单待发货", Body: "请及时发货", Status: "unread"},
	}
	foreignRow := model.CommerceMerchantNotification{
		TenantID: foreign.ID, EventKey: "controller-foreign-1", EventType: "order.paid", OrderID: 903,
		OrderNo: "COM-CTRL-903", BusinessType: "restaurant", LocationID: 41,
		Title: "其他租户", Body: "不得读取", Status: "unread",
	}
	if err := db.Create(&ownerRows).Error; err != nil {
		t.Fatalf("create owner notifications: %v", err)
	}
	if err := db.Create(&foreignRow).Error; err != nil {
		t.Fatalf("create foreign notification: %v", err)
	}
	controller := &CommerceNotificationController{Service: service.CommerceNotificationService{DB: db}}

	response := invokeCommerceNotificationController(t, http.MethodGet, "/commerce/notifications?business_type=restaurant&location_id=41&page_size=10", owner.ID, nil, controller.List)
	if response.Code != http.StatusOK {
		t.Fatalf("owner list status=%d body=%s", response.Code, response.Body.String())
	}
	var page service.CommerceNotificationPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode owner notification page: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].TenantID != owner.ID || page.Data[0].BusinessType != "restaurant" || page.Data[0].LocationID != 41 {
		t.Fatalf("owner list crossed scope: %+v", page)
	}

	response = invokeCommerceNotificationController(t, http.MethodGet, "/commerce/notifications?business_type=not-a-business", owner.ID, nil, controller.List)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid business status=%d body=%s", response.Code, response.Body.String())
	}
	response = invokeCommerceNotificationController(t, http.MethodGet, "/commerce/notifications?after_id=bad", owner.ID, nil, controller.List)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid after_id status=%d body=%s", response.Code, response.Body.String())
	}

	response = invokeCommerceNotificationController(t, http.MethodGet, "/commerce/notifications/unread-count?business_type=restaurant&location_id=41", owner.ID, nil, controller.UnreadCount)
	if response.Code != http.StatusOK || response.Body.String() == "" {
		t.Fatalf("owner unread count status=%d body=%s", response.Code, response.Body.String())
	}

	ownerID := strconv.FormatUint(uint64(ownerRows[0].ID), 10)
	response = invokeCommerceNotificationController(t, http.MethodPost, "/commerce/notifications/"+ownerID+"/read", owner.ID, gin.Params{{Key: "id", Value: ownerID}}, controller.MarkRead)
	if response.Code != http.StatusNoContent {
		t.Fatalf("owner mark read status=%d body=%s", response.Code, response.Body.String())
	}
	var read model.CommerceMerchantNotification
	if err := db.First(&read, ownerRows[0].ID).Error; err != nil {
		t.Fatalf("load marked notification: %v", err)
	}
	if read.Status != "read" || read.ReadAt == nil {
		t.Fatalf("notification was not marked read: %+v", read)
	}

	foreignID := strconv.FormatUint(uint64(foreignRow.ID), 10)
	response = invokeCommerceNotificationController(t, http.MethodPost, "/commerce/notifications/"+foreignID+"/read", owner.ID, gin.Params{{Key: "id", Value: foreignID}}, controller.MarkRead)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant mark read status=%d body=%s, want 404", response.Code, response.Body.String())
	}

	response = invokeCommerceNotificationController(t, http.MethodPost, "/commerce/notifications/not-a-number/read", owner.ID, gin.Params{{Key: "id", Value: "not-a-number"}}, controller.MarkRead)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid notification ID status=%d body=%s", response.Code, response.Body.String())
	}

	if ownerRows[0].ID == 0 || foreignRow.ID == 0 {
		t.Fatalf("test fixture IDs were not assigned: owner=%d foreign=%d", ownerRows[0].ID, foreignRow.ID)
	}
}
