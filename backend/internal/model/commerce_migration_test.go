package model

import (
	"strings"
	"testing"
	"time"

	"ticket-backend/internal/testdb"
)

func TestCommerceSchema124CreatesIsolatedTablesAndConstraints(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("re-running commerce migration: %v", err)
	}

	for _, table := range []interface{}{
		&TenantBusinessCapability{}, &CommerceProduct{}, &CommerceSKU{}, &CommerceOptionGroup{}, &CommerceOption{},
		&CommerceFulfillmentLocation{}, &CommerceInventory{}, &CommerceCart{}, &CommerceCartItem{},
		&CommerceOrder{}, &CommerceOrderItem{}, &RestaurantFulfillment{}, &RetailFulfillment{},
		&CommercePaymentReconciliationTask{},
		&CommercePaymentAttempt{}, &CommerceRefundAttempt{}, &CommercePaymentProviderEvent{},
		&CommerceAddress{}, &CommerceAfterSaleRequest{}, &CommerceAfterSaleEvent{},
		&CommerceCustomerSession{}, &CommerceStorefrontBinding{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("commercial table for %T is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&TenantBusinessCapability{}, "idx_tenant_business_capability"},
		{&CommerceSKU{}, "idx_commerce_skus_tenant_code"},
		{&CommerceInventory{}, "idx_commerce_inventory_scope"},
		{&CommerceAfterSaleRequest{}, "idx_commerce_after_sales_idempotency"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("commercial index %s is missing", index.name)
		}
	}
	var cartIdentityIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_commerce_carts_active_identity'`).Scan(&cartIdentityIndex).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"tenant_id", "business_type", "customer_id", "channel_account_id", "location_id"} {
		if !strings.Contains(cartIdentityIndex, column) {
			t.Fatalf("commercial cart identity index omitted %s: %s", column, cartIdentityIndex)
		}
	}
	lowerCartIdentityIndex := strings.ToLower(cartIdentityIndex)
	if !strings.Contains(lowerCartIdentityIndex, "status") || !strings.Contains(lowerCartIdentityIndex, "active") {
		t.Fatalf("commercial cart identity index is not limited to active carts: %s", cartIdentityIndex)
	}
	for _, field := range []struct {
		model interface{}
		name  string
	}{
		{&CommerceOrder{}, "IdempotencyKey"},
		{&CommerceOrder{}, "ExpiresAt"},
		{&CommerceOrder{}, "PaidAt"},
		{&CommerceOrder{}, "PaymentReference"},
		{&CommercePaymentReconciliationTask{}, "ProviderPaidAt"},
		{&CommercePaymentReconciliationTask{}, "ProviderAmountCents"},
		{&CommercePaymentReconciliationTask{}, "ProviderReference"},
		{&CommerceOrderItem{}, "ReservationStatus"},
		{&CommerceOrderItem{}, "ReleasedAt"},
		{&CommerceAfterSaleRequest{}, "ProviderRefundReference"},
		{&CommerceAfterSaleRequest{}, "ProviderRefundAmountCents"},
		{&CommerceAddress{}, "AddressType"},
		{&CommerceAddress{}, "CampusName"},
		{&CommerceAddress{}, "Room"},
	} {
		if !db.Migrator().HasColumn(field.model, field.name) {
			t.Fatalf("commercial column %s for %T is missing", field.name, field.model)
		}
	}

	valid := TenantBusinessCapability{TenantID: 701, BusinessType: "restaurant", Status: "active", EnabledAt: ptrTime(time.Now())}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("create valid business capability: %v", err)
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: 701, BusinessType: "restaurant", Status: "active"}).Error; err == nil {
		t.Fatal("duplicate tenant/business capability was accepted")
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: 702, BusinessType: "hotel", Status: "active"}).Error; err == nil {
		t.Fatal("unsupported business capability type was accepted")
	}

	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}

	locationA := CommerceFulfillmentLocation{TenantID: 701, BusinessType: "retail", Name: "Tenant A warehouse", LocationType: "warehouse", Status: "active"}
	locationB := CommerceFulfillmentLocation{TenantID: 702, BusinessType: "retail", Name: "Tenant B warehouse", LocationType: "warehouse", Status: "active"}
	if err := db.Create(&locationA).Create(&locationB).Error; err != nil {
		t.Fatalf("create fulfillment locations: %v", err)
	}
	productA := CommerceProduct{TenantID: 701, BusinessType: "retail", Name: "Tenant A product", Status: "online"}
	if err := db.Create(&productA).Error; err != nil {
		t.Fatalf("create tenant A product: %v", err)
	}
	skuA := CommerceSKU{TenantID: 701, ProductID: productA.ID, SkuCode: "SHARED-SKU", Name: "Default", Status: "active"}
	if err := db.Create(&skuA).Error; err != nil {
		t.Fatalf("create tenant A SKU: %v", err)
	}
	orderA := CommerceOrder{
		TenantID: 701, OrderNo: "COM-ORDER-A", IdempotencyKey: "same-request", BusinessType: "retail",
		LocationID: locationA.ID, OriginalAmountCents: 100, TotalAmountCents: 100,
	}
	if err := db.Create(&orderA).Error; err != nil {
		t.Fatalf("create tenant A order: %v", err)
	}
	duplicate := orderA
	duplicate.Base = Base{}
	duplicate.OrderNo = "COM-ORDER-A-DUP"
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate idempotency key in one tenant was accepted")
	}
	orderB := CommerceOrder{
		TenantID: 702, OrderNo: "COM-ORDER-B", IdempotencyKey: "same-request", BusinessType: "retail",
		LocationID: locationB.ID, OriginalAmountCents: 100, TotalAmountCents: 100,
	}
	if err := db.Create(&orderB).Error; err != nil {
		t.Fatalf("same idempotency key across tenants was rejected: %v", err)
	}
	providerPaidAt := time.Date(2026, time.September, 17, 12, 34, 56, 0, time.UTC)
	task := CommercePaymentReconciliationTask{
		TenantID: 701, OrderID: orderA.ID, PaymentReference: "provider-payment-a",
		ProviderPaidAt: &providerPaidAt, ProviderAmountCents: 100, Status: "completed",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create payment reconciliation task: %v", err)
	}
	var loadedTask CommercePaymentReconciliationTask
	if err := db.First(&loadedTask, task.ID).Error; err != nil {
		t.Fatalf("read payment reconciliation task: %v", err)
	}
	if loadedTask.ProviderPaidAt == nil || !loadedTask.ProviderPaidAt.Equal(providerPaidAt) {
		t.Fatalf("provider paid time was not persisted: got %v", loadedTask.ProviderPaidAt)
	}
	if loadedTask.ProviderAmountCents != 100 {
		t.Fatalf("provider amount was not persisted: got %d", loadedTask.ProviderAmountCents)
	}
	invalidTask := CommercePaymentReconciliationTask{
		TenantID: 702, OrderID: orderB.ID, ProviderAmountCents: -1, Status: "pending",
	}
	if err := db.Create(&invalidTask).Error; err == nil {
		t.Fatal("negative provider amount was accepted")
	}
	for _, status := range []string{"reserved", "sold", "released", "refunded"} {
		item := CommerceOrderItem{
			TenantID: 701, OrderID: orderA.ID, ProductID: productA.ID, SkuID: skuA.ID,
			ProductNameSnapshot: "Tenant A product", SkuNameSnapshot: "Default", Quantity: 1,
			OriginalUnitPriceCents: 100, UnitPriceCents: 100, LineAmountCents: 100, ReservationStatus: status,
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("valid reservation status %q was rejected: %v", status, err)
		}
	}
	invalidItem := CommerceOrderItem{
		TenantID: 701, OrderID: orderA.ID, ProductID: productA.ID, SkuID: skuA.ID,
		ProductNameSnapshot: "Tenant A product", SkuNameSnapshot: "Default", Quantity: 1,
		OriginalUnitPriceCents: 100, UnitPriceCents: 100, LineAmountCents: 100, ReservationStatus: "invalid",
	}
	if err := db.Create(&invalidItem).Error; err == nil {
		t.Fatal("invalid reservation status was accepted")
	}
	invalidProviderFacts := CommerceAfterSaleRequest{
		TenantID: 701, OrderID: orderA.ID, RequestNo: "COM-REFUND-INVALID-FACTS", IdempotencyKey: "invalid-provider-facts",
		Type: "refund", Status: "requested", AmountCents: 100, ProviderRefundAmountCents: 1,
	}
	if err := db.Create(&invalidProviderFacts).Error; err == nil {
		t.Fatal("provider refund amount without reference was accepted")
	}
	negativeProviderAmount := CommerceAfterSaleRequest{
		TenantID: 701, OrderID: orderA.ID, RequestNo: "COM-REFUND-NEGATIVE-FACT", IdempotencyKey: "negative-provider-fact",
		Type: "refund", Status: "requested", AmountCents: 100, ProviderRefundReference: "refund-negative", ProviderRefundAmountCents: -1,
	}
	if err := db.Create(&negativeProviderAmount).Error; err == nil {
		t.Fatal("negative provider refund amount was accepted")
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
