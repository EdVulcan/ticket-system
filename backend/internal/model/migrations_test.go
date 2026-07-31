//go:build cgo

package model

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrationsReachCurrentVersionAndAreIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Version != 28 {
		t.Fatalf("latest migration=%d, want 28", latest.Version)
	}
	for _, table := range []string{"product_revisions", "ledger_entries", "channel_accounts", "tour_groups", "pos_shifts", "settlement_statements", "after_sale_requests", "hardware_commands", "channel_reservations", "financial_documents"} {
		var count int64
		if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
}

func TestLegacyMigrationQuarantinesOffersAndRebuildsFulfillmentTotals(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := migrateInitialSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 19; version++ {
		if err := db.Create(&SchemaMigration{Version: version, Name: "legacy", AppliedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	supplier := Tenant{Name: "Legacy Supplier", SystemCode: "LEG-S", SecretKey: "s", Status: "active"}
	distributor := Tenant{Name: "Legacy Distributor", SystemCode: "LEG-D", SecretKey: "d", Status: "active"}
	if err := db.Create(&supplier).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&distributor).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: supplier.ID, Code: "MAIN", Name: "Main", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&DistributorRelationship{AgentTenantID: distributor.ID, SupplierTenantID: supplier.ID, Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	supplierRule := TicketRule{Name: "Supplier Rule", TenantID: supplier.ID, ValidityType: "date"}
	distributorRule := TicketRule{Name: "Listing Rule", TenantID: distributor.ID, ValidityType: "date"}
	if err := db.Create(&supplierRule).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&distributorRule).Error; err != nil {
		t.Fatal(err)
	}
	sourceA := Product{Name: "A", Price: 100, SettlementPrice: 60, TenantID: supplier.ID, ScenicAreaID: area.ID, RuleID: supplierRule.ID, Type: "online", Status: "online", IsDistributable: true, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	sourceB := Product{Name: "B", Price: 50, SettlementPrice: 30, TenantID: supplier.ID, ScenicAreaID: area.ID, RuleID: supplierRule.ID, Type: "online", Status: "online", IsDistributable: true, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	if err := db.Create(&sourceA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sourceB).Error; err != nil {
		t.Fatal(err)
	}
	listingA := Product{Name: "LA", Price: 120, SettlementPrice: 0.01, TenantID: distributor.ID, ScenicAreaID: area.ID, RuleID: distributorRule.ID, Type: "online", Status: "online", SourceProductID: sourceA.ID, SourceTenantID: supplier.ID, FulfillmentProductID: sourceA.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	listingB := Product{Name: "LB", Price: 70, SettlementPrice: 0, TenantID: distributor.ID, ScenicAreaID: area.ID, RuleID: distributorRule.ID, Type: "online", Status: "online", SourceProductID: sourceB.ID, SourceTenantID: supplier.ID, FulfillmentProductID: sourceB.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	if err := db.Create(&listingA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&listingB).Error; err != nil {
		t.Fatal(err)
	}
	offerA := ProductOffer{SupplierTenantID: supplier.ID, DistributorTenantID: distributor.ID, SourceProductID: sourceA.ID, FulfillmentScenicAreaID: area.ID, SettlementPrice: listingA.SettlementPrice, Status: "active"}
	offerB := ProductOffer{SupplierTenantID: supplier.ID, DistributorTenantID: distributor.ID, SourceProductID: sourceB.ID, FulfillmentScenicAreaID: area.ID, SettlementPrice: listingB.SettlementPrice, Status: "active"}
	if err := db.Create(&offerA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&offerB).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&listingA).Update("product_offer_id", offerA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&listingB).Update("product_offer_id", offerB.ID).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{OrderNo: "LEGACY-ORDER", TenantID: distributor.ID, Status: "paid", Channel: "window", TotalAmount: 190}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	items := []OrderItem{
		{OrderID: order.ID, ProductID: listingA.ID, ProductName: listingA.Name, Price: listingA.Price, SettlementPrice: sourceA.SettlementPrice, Quantity: 1, FulfillmentProductID: sourceA.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID},
		{OrderID: order.ID, ProductID: listingB.ID, ProductName: listingB.Name, Price: listingB.Price, SettlementPrice: sourceB.SettlementPrice, Quantity: 1, FulfillmentProductID: sourceB.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatal(err)
	}
	fulfillment := FulfillmentOrder{FulfillmentNo: "FUL-LEGACY", SalesOrderID: order.ID, SalesOrderNo: order.OrderNo, SalesTenantID: distributor.ID, SupplierTenantID: supplier.ID, ScenicAreaID: area.ID, SettlementAmount: 60, SettlementStatus: "open", Status: "paid"}
	if err := db.Create(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&OrderItem{}).Where("order_id = ?", order.ID).Update("fulfillment_order_id", fulfillment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var offers []ProductOffer
	if err := db.Where("distributor_tenant_id = ?", distributor.ID).Order("source_product_id").Find(&offers).Error; err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 || offers[0].Status != "suspended" || offers[1].Status != "suspended" || offers[0].SettlementPrice != 0.01 || offers[1].SettlementPrice != 0 {
		t.Fatalf("migrated offers=%+v", offers)
	}
	if err := db.Where("sales_order_id = ?", order.ID).First(&fulfillment).Error; err != nil {
		t.Fatal(err)
	}
	if fulfillment.SettlementAmount != 90 {
		t.Fatalf("fulfillment settlement=%v, want 90", fulfillment.SettlementAmount)
	}
	var capabilities []TenantCapability
	if err := db.Find(&capabilities).Error; err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 2 {
		t.Fatalf("inferred capabilities=%+v", capabilities)
	}
}
