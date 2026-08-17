package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestProductListFiltersByTypeStatusSearchAndScenicArea(t *testing.T) {
	resetBusinessData(t)
	tenantID, firstID := seedSellableProduct(t, "unlimited", 0)
	var first model.Product
	if err := model.DB.First(&first, firstID).Error; err != nil {
		t.Fatalf("load seed product: %v", err)
	}

	products := []model.Product{
		{Name: "Child Ticket", TenantID: tenantID, ScenicAreaID: first.ScenicAreaID, RuleID: first.RuleID, Type: "online", Status: "offline", Price: 50, SettlementPrice: 30, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", RefundType: "free"},
		{Name: "Window Adult Ticket", TenantID: tenantID, ScenicAreaID: first.ScenicAreaID, RuleID: first.RuleID, Type: "offline", Status: "online", Price: 80, SettlementPrice: 0, ValidityType: "days", StockType: "unlimited", CodeMode: "ticket", RefundType: "free"},
		{Name: "Calendar Hotel Room", TenantID: tenantID, ProductKind: "hotel", Type: "online", Status: "online", Price: 500, SettlementPrice: 400},
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Omit("Rule").Create(&products).Error }); err != nil {
		t.Fatalf("seed filtered products: %v", err)
	}

	service := &ProductService{}
	rows, total, err := service.ListFiltered(1, 10, tenantID, ProductListFilter{ProductType: "online", Status: "online", Search: "adult", ScenicAreaID: first.ScenicAreaID})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != firstID {
		t.Fatalf("filtered list = rows=%v total=%d, want seed adult product only", rows, total)
	}

	rows, total, err = service.ListFiltered(1, 10, tenantID, ProductListFilter{ProductType: "online", Status: "offline"})
	if err != nil {
		t.Fatalf("status list: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Name != "Child Ticket" {
		t.Fatalf("status list = rows=%v total=%d, want offline online-type product", rows, total)
	}

	rows, total, err = service.ListFiltered(1, 10, tenantID, ProductListFilter{ProductType: "online", ProductKind: "ticket"})
	if err != nil {
		t.Fatalf("product kind list: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("product kind list = rows=%v total=%d, want only ticket products", rows, total)
	}
	for _, row := range rows {
		if row.ProductKind != "ticket" {
			t.Fatalf("product kind list leaked non-ticket product: %+v", row)
		}
	}
}

func TestTicketProductServiceRejectsStandaloneHotelCreation(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var existing model.Product
	if err := model.DB.First(&existing, productID).Error; err != nil {
		t.Fatalf("load seed product: %v", err)
	}
	var checkpointID uint
	if err := model.DB.Model(&model.RuleItem{}).Where("group_id IN (SELECT id FROM rule_groups WHERE rule_id = ?)", existing.RuleID).Pluck("check_point_id", &checkpointID).Error; err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	hotel := &model.Product{
		Name: "Orphan Hotel Product", ProductKind: "hotel", TenantID: tenantID, ScenicAreaID: existing.ScenicAreaID,
		Type: "online", Status: "offline", Price: 100, SettlementPrice: 80, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", RefundType: "free",
	}
	rule := &model.TicketRule{Name: "Orphan Hotel Rule", TenantID: tenantID, ValidityType: "date", Groups: []model.RuleGroup{{GroupName: "Admission", MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: checkpointID, MaxPerCheckIn: 1}}}}}
	err := (&ProductService{}).Create(hotel, rule)
	if err == nil || !strings.Contains(err.Error(), "only ticket products") {
		t.Fatalf("hotel product creation error = %v, want ticket-service boundary rejection", err)
	}
	var count int64
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND name = ?", tenantID, hotel.Name).Count(&count).Error; err != nil {
		t.Fatalf("count rejected hotel product: %v", err)
	}
	if count != 0 {
		t.Fatalf("ticket service left an orphan hotel product in the catalog: %d", count)
	}
}
