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
	if latest.Version != 57 {
		t.Fatalf("latest migration=%d, want 57", latest.Version)
	}
	if !db.Migrator().HasIndex(&TourEntryBatch{}, "idx_team_entry_request") {
		t.Fatal("team admission idempotency index was not created")
	}
	for _, table := range []string{"product_revisions", "ledger_entries", "channel_accounts", "tour_groups", "tour_entry_batches", "tour_group_confirmations", "tour_group_member_changes", "pos_shifts", "pos_shift_corrections", "pos_holds", "settlement_statements", "settlement_adjustments", "after_sale_requests", "hardware_commands", "channel_reservations", "financial_documents", "team_settlement_statements", "team_settlement_adjustments", "channel_bill_records", "channel_reconciliations", "channel_reconciliation_lines", "migration_audit_issues", "order_visitors", "bundle_products", "bundle_versions", "bundle_components"} {
		var count int64
		if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	var paymentIdempotencyIndex int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_payment_idempotency").Scan(&paymentIdempotencyIndex).Error; err != nil {
		t.Fatal(err)
	}
	if paymentIdempotencyIndex != 1 {
		t.Fatal("partial payment idempotency index missing")
	}
	var refundAllocationIndex int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_refund_allocation_sequence").Scan(&refundAllocationIndex).Error; err != nil {
		t.Fatal(err)
	}
	if refundAllocationIndex != 1 {
		t.Fatal("mixed refund allocation index missing")
	}
}

func TestStrictOwnershipGuardsRejectCrossTenantRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "guards.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Guard A", SystemCode: "GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Guard B", SystemCode: "GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: second.ID, Code: "B", Name: "B", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CheckPoint{TenantID: first.ID, ScenicAreaID: area.ID, Name: "forbidden"}).Error; err == nil {
		t.Fatal("cross-tenant checkpoint was accepted by database guard")
	}
	if err := db.Create(&OrderVisitor{
		TenantID: first.ID, OrderID: 999, OrderItemID: 999, TicketID: 999,
		TicketCode: "forbidden", Sequence: 1, Name: "cross-tenant",
	}).Error; err == nil {
		t.Fatal("orphan order visitor was accepted by database guard")
	}
}

func TestBundleOwnershipGuardsRejectForgedSupplierFacts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bundle-guards.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	distributor := Tenant{Name: "Bundle Seller", SystemCode: "BUNDLE-SELLER", SecretKey: "d", Status: "active"}
	supplier := Tenant{Name: "Bundle Supplier", SystemCode: "BUNDLE-SUPPLIER", SecretKey: "s", Status: "active"}
	if err := db.Create(&distributor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&supplier).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: supplier.ID, Code: "SUP", Name: "Supplier Area", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	supplierRule := TicketRule{TenantID: supplier.ID, Name: "Supplier Rule", ValidityType: "date"}
	sellerRule := TicketRule{TenantID: distributor.ID, Name: "Seller Rule", ValidityType: "date"}
	if err := db.Create(&supplierRule).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&sellerRule).Error; err != nil {
		t.Fatal(err)
	}
	source := Product{TenantID: supplier.ID, ScenicAreaID: area.ID, RuleID: supplierRule.ID, Name: "Source", Type: "online", Status: "online", IsDistributable: true, Price: 100, SettlementPrice: 60, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	offer := ProductOffer{SupplierTenantID: supplier.ID, DistributorTenantID: distributor.ID, SourceProductID: source.ID, FulfillmentScenicAreaID: area.ID, SettlementPrice: 60, Status: "active"}
	if err := db.Create(&offer).Error; err != nil {
		t.Fatal(err)
	}
	listing := Product{TenantID: distributor.ID, ScenicAreaID: area.ID, RuleID: sellerRule.ID, Name: "Listing", Type: "online", Status: "online", Price: 80, SourceProductID: source.ID, SourceTenantID: supplier.ID, FulfillmentProductID: source.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID, ProductOfferID: offer.ID, ValidityType: "date", StockType: "unlimited", CodeMode: "ticket"}
	if err := db.Create(&listing).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SellerListing{SellerTenantID: distributor.ID, ProductOfferID: offer.ID, ProductID: listing.ID, Name: listing.Name, RetailPrice: 80, RetailPriceCents: 8000, Status: "online"}).Error; err != nil {
		t.Fatal(err)
	}
	bundle := BundleProduct{SellerTenantID: distributor.ID, Name: "Bundle", Type: "online", RetailPriceCents: 8000, Status: "offline"}
	if err := db.Create(&bundle).Error; err != nil {
		t.Fatal(err)
	}
	version := BundleVersion{BundleProductID: bundle.ID, SellerTenantID: distributor.ID, Version: 1, RetailPriceCents: 8000, Status: "active"}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	valid := BundleComponent{BundleVersionID: version.ID, SellerTenantID: distributor.ID, SellerProductID: listing.ID, ProductOfferID: offer.ID, SupplierTenantID: supplier.ID, SourceProductID: source.ID, FulfillmentScenicAreaID: area.ID, Quantity: 1, RetailAllocationCents: 8000, SettlementUnitPriceCents: 6000}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("valid bundle component was rejected: %v", err)
	}
	if err := db.Model(&bundle).Update("current_version_id", version.ID).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{OrderNo: "BUNDLE-GUARD-ORDER", TenantID: distributor.ID, Channel: "online", Status: "unpaid", TotalAmount: 80, Items: []OrderItem{{
		ProductID: listing.ID, ProductName: listing.Name, Price: 80, SettlementPrice: 60, Quantity: 1,
		FulfillmentProductID: source.ID, FulfillmentTenantID: supplier.ID, FulfillmentScenicAreaID: area.ID,
		ProductOfferID: offer.ID, BundleProductID: bundle.ID, BundleVersionID: version.ID, BundleComponentID: valid.ID, BundleName: bundle.Name, BundleUnitQuantity: 1,
	}}}
	if err := db.Create(&order).Error; err != nil {
		t.Fatalf("valid nested bundle order was rejected by ownership guards: %v", err)
	}
	forged := BundleComponent{BundleVersionID: version.ID, SellerTenantID: distributor.ID, SellerProductID: listing.ID, ProductOfferID: offer.ID, SupplierTenantID: distributor.ID, SourceProductID: source.ID, FulfillmentScenicAreaID: area.ID, Quantity: 1, RetailAllocationCents: 8000, SettlementUnitPriceCents: 6000}
	if err := db.Create(&forged).Error; err == nil {
		t.Fatal("database accepted forged bundle supplier ownership")
	}
}

func TestPaymentCentMigrationBackfillsLegacyAmounts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "payment-cents.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := db.AutoMigrate(&Payment{}, &Refund{}); err != nil {
		t.Fatal(err)
	}
	payment := Payment{TenantID: 1, PaymentNo: "PAY-CENTS", OrderNo: "ORD-CENTS", Amount: 12.34, RefundedAmount: 1.23, Method: "cash", Status: "paid"}
	refund := Refund{TenantID: 1, RefundNo: "REF-CENTS", IdempotencyKey: "REF-CENTS", OrderNo: payment.OrderNo, PaymentID: 1, Amount: 1.23, Method: "cash", Status: "succeeded"}
	if err := db.Create(&payment).Error; err != nil {
		t.Fatal(err)
	}
	refund.PaymentID = payment.ID
	if err := db.Create(&refund).Error; err != nil {
		t.Fatal(err)
	}
	if err := migratePaymentCentFacts(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&payment, payment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if payment.AmountCents != 1234 || payment.RefundedAmountCents != 123 {
		t.Fatalf("payment cent backfill=%+v", payment)
	}
	if err := db.First(&refund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if refund.AmountCents != 123 {
		t.Fatalf("refund cent backfill=%+v", refund)
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
	legacyAccount := CapitalAccount{OwnerTenantID: distributor.ID, ManagerTenantID: supplier.ID, Balance: 12.34, CreditLine: 5.67, UsedCredit: 1.23, FrozenAmount: 0.45, Status: "active"}
	if err := db.Create(&legacyAccount).Error; err != nil {
		t.Fatal(err)
	}
	legacyTransaction := TransactionRecord{AccountID: legacyAccount.ID, Type: "payment", Amount: 0.29, BalanceAfter: 12.34, Memo: "legacy cents"}
	if err := db.Create(&legacyTransaction).Error; err != nil {
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
	if err := db.First(&legacyAccount, legacyAccount.ID).Error; err != nil {
		t.Fatal(err)
	}
	if legacyAccount.BalanceCents != 1234 || legacyAccount.CreditLineCents != 567 || legacyAccount.UsedCreditCents != 123 || legacyAccount.FrozenCents != 45 {
		t.Fatalf("legacy account cent projection=%+v", legacyAccount)
	}
	if err := db.First(&legacyTransaction, legacyTransaction.ID).Error; err != nil {
		t.Fatal(err)
	}
	if legacyTransaction.AmountCents != 29 || legacyTransaction.BalanceAfterCents != 1234 {
		t.Fatalf("legacy transaction cent projection=%+v", legacyTransaction)
	}
	var capabilities []TenantCapability
	if err := db.Find(&capabilities).Error; err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 2 {
		t.Fatalf("inferred capabilities=%+v", capabilities)
	}
}

func TestLegacyMigrationAuditReportsAndQuarantinesUnsafeRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := migrateInitialSchema(db); err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{Name: "Audit Tenant", SystemCode: "AUDIT", SecretKey: "secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	rule := TicketRule{Name: "Audit rule", TenantID: tenant.ID, ValidityType: "date"}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	unsafe := Product{Name: "Unassigned", TenantID: tenant.ID, RuleID: rule.ID, Status: "online", Type: "online", StockType: "unlimited", CodeMode: "ticket"}
	if err := db.Create(&unsafe).Error; err != nil {
		t.Fatal(err)
	}
	report, err := AuditLegacyMigration(db)
	if err != nil {
		t.Fatal(err)
	}
	if report.SafeToMigrate || len(report.Issues) != 1 || report.Issues[0].Code != "zero_scenic_area" {
		t.Fatalf("unexpected migration report=%+v", report)
	}
	if err := PersistMigrationAudit(db, report); err != nil {
		t.Fatal(err)
	}
	var stored []MigrationAuditIssue
	if err := db.Where("run_id = ?", report.RunID).Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Status != "open" || stored[0].EntityID != unsafe.ID {
		t.Fatalf("stored migration issues=%+v", stored)
	}
}

func TestLegacyMigrationAuditReportsCrossTenantOwnership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ownership-audit.db")+"?_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	if err := migrateInitialSchema(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "First", SystemCode: "OWN-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Second", SystemCode: "OWN-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: second.ID, Code: "SECOND", Name: "Second Park", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	// This row can exist in a legacy database because the old schema only had
	// independent integer columns. The audit must reject it before migration.
	if err := db.Create(&CheckPoint{TenantID: first.ID, ScenicAreaID: area.ID, Name: "wrong owner"}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := AuditLegacyMigration(db)
	if err != nil {
		t.Fatal(err)
	}
	if report.SafeToMigrate {
		t.Fatal("cross-tenant checkpoint was considered safe")
	}
	found := false
	for _, issue := range report.Issues {
		if issue.Code == "ownership_mismatch" && issue.EntityType == "checkpoint" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ownership mismatch was not reported: %+v", report.Issues)
	}
}
