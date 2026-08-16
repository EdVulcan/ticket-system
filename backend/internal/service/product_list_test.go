package service

import (
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
}
