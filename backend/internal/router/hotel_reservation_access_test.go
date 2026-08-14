package router

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHotelReservationRoutesSeparateBackOfficeAndFrontlinePermissions(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantCapability{}, &model.SupplierBusinessType{},
		&model.User{}, &model.Staff{}, &model.Order{}, &model.OrderItem{},
		&model.Ticket{}, &model.ScenicHotelPackage{}, &model.ScenicHotelPackageEntitlement{}, &model.HotelReservation{},
		&model.ChannelAccount{}, &model.XiaohongshuBookingOperation{}, &model.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	previousDB, previousSecret := model.DB, config.GlobalConfig.Security.JWTSecret
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	config.GlobalConfig.Security.JWTSecret = "01234567890123456789012345678901"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = previousDB
		config.GlobalConfig.Security.JWTSecret = previousSecret
	})

	tenant := model.Tenant{Name: "hotel access", SystemCode: "HOTEL-ACCESS", SecretKey: "test", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.SupplierBusinessType{
		{TenantID: tenant.ID, BusinessType: "scenic", Status: "active"},
		{TenantID: tenant.ID, BusinessType: "hotel", Status: "active"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	operator := model.User{TenantID: tenant.ID, Username: "hotel-operator", Password: "unused", Role: "product_operator", TokenVersion: 1}
	admin := model.User{TenantID: tenant.ID, Username: "admin", Password: "unused", Role: "admin", TokenVersion: 1}
	viewer := model.User{TenantID: tenant.ID, Username: "viewer", Password: "unused", Role: "viewer", TokenVersion: 1}
	seller := model.Staff{TenantID: tenant.ID, JobNumber: "seller", Name: "seller", Password: "unused", Roles: "seller", Status: "active", TokenVersion: 1}
	if err := db.Create(&operator).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&seller).Error; err != nil {
		t.Fatal(err)
	}

	operatorToken, err := (&service.AuthService{}).GenerateToken(&operator)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := (&service.AuthService{}).GenerateToken(&viewer)
	if err != nil {
		t.Fatal(err)
	}
	sellerToken, err := (&service.AuthService{}).GenerateStaffToken(&seller)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := (&service.AuthService{}).GenerateToken(&admin)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitRouter(engine)
	request := func(method, path, token string, bodies ...string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		var body *bytes.Reader
		if len(bodies) > 0 {
			body = bytes.NewReader([]byte(bodies[0]))
		} else {
			body = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations", operatorToken); response.Code != http.StatusOK {
		t.Fatalf("product operator list status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations", adminToken); response.Code != http.StatusOK {
		t.Fatalf("admin list status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/booking-sync-operations/failed", operatorToken); response.Code != http.StatusOK {
		t.Fatalf("product operator failed sync list status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/scenic-hotel-packages/booking-sync-operations/999/retry", operatorToken, `{"reason":"manual recovery"}`); response.Code != http.StatusNotFound {
		t.Fatalf("product operator failed sync retry status=%d body=%s, want 404", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations?hotel_id=not-a-number", operatorToken); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid hotel filter status=%d body=%s, want 400", response.Code, response.Body.String())
	}
	for _, path := range []string{
		"/api/v1/scenic-hotel-packages/business-summary?hotel_id=not-a-number",
		"/api/v1/scenic-hotel-packages/reservations/export?hotel_id=not-a-number",
	} {
		if response := request(http.MethodGet, path, operatorToken); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid hotel filter %s status=%d body=%s, want 400", path, response.Code, response.Body.String())
		}
	}
	order := model.Order{TenantID: tenant.ID, OrderNo: "HOTEL-ACCESS-ORDER", Status: "paid"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	orderItem := model.OrderItem{OrderID: order.ID, ProductName: "hotel package", Quantity: 1, FulfillmentTenantID: tenant.ID}
	if err := db.Create(&orderItem).Error; err != nil {
		t.Fatal(err)
	}
	ticket := model.Ticket{OrderID: order.ID, OrderItemID: orderItem.ID, TenantID: tenant.ID, FulfillmentTenantID: tenant.ID, TicketCode: "HOTEL-ACCESS-TICKET", Status: "unused"}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	reservation := model.HotelReservation{ReservationNo: "HOTEL-ACCESS-RESERVATION", SalesTenantID: tenant.ID, SupplierTenantID: tenant.ID, OrderID: order.ID, OrderItemID: orderItem.ID, TicketID: ticket.ID, PackageID: 1, HotelID: 1, RoomTypeID: 1, RatePlanID: 1, HotelName: "test", RoomTypeName: "test", RatePlanName: "test", CheckInDate: order.CreatedAt, CheckOutDate: order.CreatedAt.AddDate(0, 0, 1), Rooms: 1, Status: "confirmed"}
	if err := db.Create(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPatch, "/api/v1/scenic-hotel-packages/reservations/"+strconv.FormatUint(uint64(reservation.ID), 10)+"/status", operatorToken, `{"status":"checked_in"}`); response.Code != http.StatusOK {
		t.Fatalf("product operator status update=%d body=%s, want 200", response.Code, response.Body.String())
	}
	for name, token := range map[string]string{"viewer": viewerToken, "seller": sellerToken} {
		if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages", token); response.Code != http.StatusOK {
			t.Fatalf("%s package catalog status=%d body=%s, want 200", name, response.Code, response.Body.String())
		}
		if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations", token); response.Code != http.StatusForbidden {
			t.Fatalf("%s list status=%d body=%s, want 403", name, response.Code, response.Body.String())
		}
		if response := request(http.MethodPatch, "/api/v1/scenic-hotel-packages/reservations/999/status", token, `{"status":"checked_in"}`); response.Code != http.StatusForbidden {
			t.Fatalf("%s status update=%d body=%s, want 403", name, response.Code, response.Body.String())
		}
		if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/booking-sync-operations/failed", token); response.Code != http.StatusForbidden {
			t.Fatalf("%s failed sync list=%d body=%s, want 403", name, response.Code, response.Body.String())
		}
		if response := request(http.MethodPost, "/api/v1/scenic-hotel-packages/booking-sync-operations/999/retry", token, `{"reason":"forbidden"}`); response.Code != http.StatusForbidden {
			t.Fatalf("%s failed sync retry=%d body=%s, want 403", name, response.Code, response.Body.String())
		}
	}
}

func TestHotelReservationHistoryAndExistingFulfillmentSurviveSuspension(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantCapability{}, &model.SupplierBusinessType{},
		&model.User{}, &model.Order{}, &model.OrderItem{}, &model.Ticket{},
		&model.ScenicHotelPackageEntitlement{}, &model.HotelReservation{},
		&model.ChannelAccount{}, &model.XiaohongshuBookingOperation{}, &model.AuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	previousDB, previousSecret := model.DB, config.GlobalConfig.Security.JWTSecret
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	config.GlobalConfig.Security.JWTSecret = "01234567890123456789012345678901"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = previousDB
		config.GlobalConfig.Security.JWTSecret = previousSecret
	})

	tenant := model.Tenant{Name: "suspended hotel", SystemCode: "HOTEL-SUSPENDED", SecretKey: "test", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "suspended"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.SupplierBusinessType{
		{TenantID: tenant.ID, BusinessType: "scenic", Status: "suspended"},
		{TenantID: tenant.ID, BusinessType: "hotel", Status: "suspended"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	operator := model.User{TenantID: tenant.ID, Username: "hotel-operator", Password: "unused", Role: "product_operator", TokenVersion: 1}
	if err := db.Create(&operator).Error; err != nil {
		t.Fatal(err)
	}
	token, err := (&service.AuthService{}).GenerateToken(&operator)
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitRouter(engine)
	request := func(method, path string, bodies ...string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		var body *bytes.Reader
		if len(bodies) > 0 {
			body = bytes.NewReader([]byte(bodies[0]))
		} else {
			body = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations"); response.Code != http.StatusOK {
		t.Fatalf("historical list status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/reservations/export"); response.Code != http.StatusOK {
		t.Fatalf("historical export status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/scenic-hotel-packages/booking-sync-operations/failed"); response.Code != http.StatusOK {
		t.Fatalf("suspended failed sync list status=%d body=%s, want 200", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/scenic-hotel-packages/booking-sync-operations/999/retry", `{"reason":"finish sold fulfillment"}`); response.Code != http.StatusNotFound {
		t.Fatalf("suspended failed sync retry status=%d body=%s, want 404", response.Code, response.Body.String())
	}
	order := model.Order{TenantID: tenant.ID, OrderNo: "HOTEL-SUSPENDED-ORDER", Status: "paid"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	item := model.OrderItem{OrderID: order.ID, ProductName: "historical package", Quantity: 1, FulfillmentTenantID: tenant.ID}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	ticket := model.Ticket{OrderID: order.ID, OrderItemID: item.ID, TenantID: tenant.ID, FulfillmentTenantID: tenant.ID, TicketCode: "HOTEL-SUSPENDED-TICKET", Status: "unused"}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	reservation := model.HotelReservation{
		ReservationNo: "HOTEL-SUSPENDED-RESERVATION", SalesTenantID: tenant.ID, SupplierTenantID: tenant.ID,
		OrderID: order.ID, OrderItemID: item.ID, TicketID: ticket.ID, PackageID: 1, HotelID: 1, RoomTypeID: 1, RatePlanID: 1,
		HotelName: "historical hotel", RoomTypeName: "historical room", RatePlanName: "historical plan",
		CheckInDate: time.Now(), CheckOutDate: time.Now().AddDate(0, 0, 1), Rooms: 1, Status: "confirmed",
	}
	if err := db.Create(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/scenic-hotel-packages/reservations/" + strconv.FormatUint(uint64(reservation.ID), 10) + "/status"
	if response := request(http.MethodPatch, path, `{"status":"checked_in"}`); response.Code != http.StatusOK {
		t.Fatalf("suspended existing fulfillment status=%d body=%s, want 200", response.Code, response.Body.String())
	}
}
