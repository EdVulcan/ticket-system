package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestOrderControllerListUsesOnlineSalesScopeAndTenantScopedFilters(t *testing.T) {
	db := openOrderControllerDB(t)
	tenantID := uint(101)
	foreignTenantID := uint(202)

	xiaohongshuAccount := model.ChannelAccount{TenantID: tenantID, Code: "api-order-filter-xhs", Type: "xiaohongshu", Status: "active", Environment: "production"}
	ctripAccount := model.ChannelAccount{TenantID: tenantID, Code: "api-order-filter-ctrip", Type: "ctrip", Status: "active", Environment: "production"}
	partnerAccount := model.ChannelAccount{TenantID: tenantID, Code: "api-order-filter-partner", Type: "partner", Status: "active", Environment: "production"}
	sandboxAccount := model.ChannelAccount{TenantID: tenantID, Code: "api-order-filter-sandbox", Type: "xiaohongshu", Status: "sandbox", Environment: "sandbox"}
	for _, account := range []*model.ChannelAccount{&xiaohongshuAccount, &ctripAccount, &partnerAccount, &sandboxAccount} {
		if err := db.Create(account).Error; err != nil {
			t.Fatalf("create channel account %s: %v", account.Code, err)
		}
	}

	baseDate := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	createOrder := func(order model.Order, createdAt time.Time) {
		t.Helper()
		order.CreatedAt = createdAt
		if order.Environment == "" {
			order.Environment = "production"
		}
		if order.Status == "" {
			order.Status = "unpaid"
		}
		if err := db.Create(&order).Error; err != nil {
			t.Fatalf("create order %s: %v", order.OrderNo, err)
		}
	}
	stringPtr := func(value string) *string { return &value }

	createOrder(model.Order{OrderNo: "API-FILTER-ONLINE-PAID", TenantID: tenantID, Status: "paid", Channel: "online", ContactName: "线上游客"}, baseDate)
	createOrder(model.Order{OrderNo: "API-FILTER-OTA-COMPLETE", TenantID: tenantID, Status: "completed", Channel: "ota", ExternalNo: stringPtr("API-OTA-EXT"), ContactName: "OTA游客"}, baseDate.AddDate(0, 0, 1))
	createOrder(model.Order{OrderNo: "API-FILTER-XHS-REFUNDED", TenantID: tenantID, Status: "refunded", Channel: "xiaohongshu", ChannelAccountID: xiaohongshuAccount.ID, ExternalNo: stringPtr("API-XHS-EXT"), ContactName: "小红书游客"}, baseDate.AddDate(0, 0, 2))
	ctripChannel := fmt.Sprintf("ctrip:%d", ctripAccount.ID)
	createOrder(model.Order{OrderNo: "API-FILTER-CTRIP-PARTIAL", TenantID: tenantID, Status: "partial_refunded", Channel: ctripChannel, ChannelAccountID: ctripAccount.ID, ExternalNo: stringPtr("API-CTRIP-EXT"), ContactName: "携程游客"}, baseDate.AddDate(0, 0, 3))
	createOrder(model.Order{OrderNo: "API-FILTER-PARTNER-UNPAID", TenantID: tenantID, Status: "unpaid", Channel: partnerAccount.Code, ChannelAccountID: partnerAccount.ID, ContactName: "合作方游客"}, baseDate.AddDate(0, 0, 4))
	createOrder(model.Order{OrderNo: "API-FILTER-WINDOW", TenantID: tenantID, Status: "paid", Channel: "window"}, baseDate.AddDate(0, 0, 5))
	createOrder(model.Order{OrderNo: "API-FILTER-SANDBOX", TenantID: tenantID, Status: "paid", Channel: "online", Environment: "sandbox"}, baseDate.AddDate(0, 0, 6))
	createOrder(model.Order{OrderNo: "API-FILTER-SANDBOX-ACCOUNT", TenantID: tenantID, Status: "paid", Channel: "xiaohongshu", ChannelAccountID: sandboxAccount.ID}, baseDate.AddDate(0, 0, 7))
	createOrder(model.Order{OrderNo: "API-FILTER-TEAM-LINKED", TenantID: tenantID, Status: "paid", Channel: "online"}, baseDate.AddDate(0, 0, 8))
	var linkedOrder model.Order
	if err := db.Where("order_no = ?", "API-FILTER-TEAM-LINKED").First(&linkedOrder).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TourGroup{
		TenantID: tenantID, SupplierTenantID: tenantID, ScenicAreaID: 1, SalesOrderID: linkedOrder.ID,
		GroupNo: "API-FILTER-TEAM-GROUP", Name: "团队归属订单", VisitDate: baseDate, ExpectedCount: 1, Status: "confirmed",
	}).Error; err != nil {
		t.Fatalf("create linked team group: %v", err)
	}
	createOrder(model.Order{OrderNo: "API-FILTER-FOREIGN", TenantID: foreignTenantID, Status: "paid", Channel: "online"}, baseDate)

	query := url.Values{"sales_scope": {"online"}, "page": {"1"}, "page_size": {"2"}}
	status, body := invokeOrderList(t, tenantID, query)
	if status != http.StatusOK {
		t.Fatalf("online list status=%d body=%s", status, body)
	}
	var response struct {
		Data           []model.Order                `json:"data"`
		Total          int64                        `json:"total"`
		Page           int                          `json:"page"`
		ChannelOptions []service.OrderChannelOption `json:"channel_options"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	// Historical production sales stay visible after account mode changes or
	// later team association. Neither changes the original sales source.
	if response.Total != 7 || len(response.Data) != 2 || response.Page != 1 {
		t.Fatalf("online response total=%d rows=%d page=%d", response.Total, len(response.Data), response.Page)
	}
	for _, order := range response.Data {
		if order.OrderNo == "API-FILTER-WINDOW" || order.OrderNo == "API-FILTER-SANDBOX" || order.OrderNo == "API-FILTER-FOREIGN" {
			t.Errorf("online response included excluded order %s", order.OrderNo)
		}
	}
	optionLabels := make(map[string]string, len(response.ChannelOptions))
	for _, option := range response.ChannelOptions {
		optionLabels[option.Value] = option.Label
	}
	expectedOptions := map[string]string{
		"online": "线上", "ota": "OTA", "xiaohongshu": "小红书",
		ctripChannel: "携程 · api-order-filter-ctrip", partnerAccount.Code: partnerAccount.Code,
	}
	if len(optionLabels) != len(expectedOptions) {
		t.Fatalf("channel options=%+v", response.ChannelOptions)
	}
	for value, label := range expectedOptions {
		if optionLabels[value] != label {
			t.Errorf("channel option %q label=%q, want %q", value, optionLabels[value], label)
		}
	}

	status = 0
	body = nil
	status, body = invokeOrderList(t, tenantID, url.Values{"sales_scope": {"online"}, "status": {"refunded"}})
	if status != http.StatusOK {
		t.Fatalf("status filter response=%d body=%s", status, body)
	}
	response = struct {
		Data           []model.Order                `json:"data"`
		Total          int64                        `json:"total"`
		Page           int                          `json:"page"`
		ChannelOptions []service.OrderChannelOption `json:"channel_options"`
	}{}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].OrderNo != "API-FILTER-XHS-REFUNDED" {
		t.Fatalf("status filter total=%d rows=%+v", response.Total, response.Data)
	}

	status, body = invokeOrderList(t, tenantID, url.Values{"sales_scope": {"online"}, "start_date": {"2026-08-11"}, "end_date": {"2026-08-13"}})
	if status != http.StatusOK {
		t.Fatalf("date filter response=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 3 || len(response.Data) != 3 {
		t.Fatalf("date filter total=%d rows=%d", response.Total, len(response.Data))
	}

	status, body = invokeOrderList(t, tenantID, url.Values{"sales_scope": {"online"}, "search": {"API-CTRIP-EXT"}})
	if status != http.StatusOK {
		t.Fatalf("search filter response=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].OrderNo != "API-FILTER-CTRIP-PARTIAL" {
		t.Fatalf("search filter total=%d rows=%+v", response.Total, response.Data)
	}

	status, body = invokeOrderList(t, tenantID, url.Values{"sales_scope": {"online"}, "channel": {ctripChannel}})
	if status != http.StatusOK {
		t.Fatalf("exact channel response=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].Channel != ctripChannel {
		t.Fatalf("exact channel total=%d rows=%+v", response.Total, response.Data)
	}

	status, body = invokeOrderList(t, tenantID, url.Values{"sales_scope": {"online"}, "channel": {"ctrip"}})
	if status != http.StatusOK {
		t.Fatalf("non-exact channel response=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 0 || len(response.Data) != 0 {
		t.Fatalf("channel filter used prefix semantics: total=%d rows=%d", response.Total, len(response.Data))
	}

	status, body = invokeOrderList(t, foreignTenantID, url.Values{"sales_scope": {"online"}})
	if status != http.StatusOK {
		t.Fatalf("foreign tenant response=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Data) != 1 || response.Data[0].OrderNo != "API-FILTER-FOREIGN" {
		t.Fatalf("foreign tenant isolation total=%d rows=%+v", response.Total, response.Data)
	}
}

func TestOrderControllerListRequiresTenantAndRejectsUnknownSalesScope(t *testing.T) {
	status, body := invokeOrderList(t, 0, url.Values{})
	if status != http.StatusBadRequest || string(body) != `{"error":"tenant is required"}` {
		t.Fatalf("missing tenant response=%d body=%s", status, body)
	}
	status, body = invokeOrderList(t, 101, url.Values{"sales_scope": {"offline"}})
	if status != http.StatusBadRequest || string(body) != `{"error":"unsupported sales scope"}` {
		t.Fatalf("unknown scope response=%d body=%s", status, body)
	}
}

func invokeOrderList(t *testing.T, tenantID uint, query url.Values) (int, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/orders?"+query.Encode(), nil)
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	if tenantID != 0 {
		ctx.Set("tenant_id", tenantID)
	}
	(&OrderController{Service: service.OrderService{}}).List(ctx)
	return recorder.Code, recorder.Body.Bytes()
}

func openOrderControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.Order{}, &model.OrderItem{}, &model.Ticket{}, &model.OrderVisitor{}, &model.ChannelAccount{}, &model.TourGroup{}); err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB = nil
	})
	return db
}
