package api

import (
	"net/http"
	"testing"

	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"gorm.io/gorm"
)

func openCommerceStatsControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openCommerceBoundaryControllerDB(t)
	if err := db.AutoMigrate(
		&model.CommerceOrder{}, &model.CommerceOrderItem{},
		&model.CommerceAfterSaleRequest{}, &model.RestaurantFulfillment{}, &model.RetailFulfillment{},
		&model.CommerceAssistCampaign{}, &model.CommerceAssistSession{}, &model.CommerceMerchantNotification{},
	); err != nil {
		t.Fatalf("migrate commerce stats controller tables: %v", err)
	}
	return db
}

func TestCommerceStatsControllerRejectsMalformedQueries(t *testing.T) {
	db := openCommerceStatsControllerDB(t)
	tenant := createCommerceControllerTenant(t, db, "stats-controller", "restaurant")
	controller := &CommerceStatsController{Service: service.CommerceStatsService{DB: db}}

	cases := []struct {
		name string
		path string
	}{
		{name: "missing business type", path: "/commerce/stats"},
		{name: "unsupported business type", path: "/commerce/stats?business_type=hotel"},
		{name: "zero location", path: "/commerce/stats?business_type=restaurant&location_id=0"},
		{name: "invalid location", path: "/commerce/stats?business_type=restaurant&location_id=abc"},
		{name: "invalid date", path: "/commerce/stats?business_type=restaurant&start_date=2026/09/01"},
		{name: "reversed dates", path: "/commerce/stats?business_type=restaurant&start_date=2026-09-03&end_date=2026-09-02"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := invokeCommerceController(t, http.MethodGet, tc.path, tenant.ID, nil, nil, controller.Get)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCommerceStatsControllerAllowsConfiguredSuspendedHistory(t *testing.T) {
	db := openCommerceStatsControllerDB(t)
	tenant := createCommerceControllerTenant(t, db, "suspended-stats-controller", "retail")
	if err := db.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ?", tenant.ID, "retail").
		Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	controller := &CommerceStatsController{Service: service.CommerceStatsService{DB: db}}
	response := invokeCommerceController(t, http.MethodGet, "/commerce/stats?business_type=retail&start_date=2026-09-01&end_date=2026-09-02", tenant.ID, nil, nil, controller.Get)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
