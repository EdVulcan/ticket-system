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
		&CommerceOrder{}, &CommerceOrderItem{}, &RestaurantFulfillment{}, &RetailFulfillment{}, &CommerceProductMedia{},
		&CommerceOrderPaidOutbox{}, &CommerceMerchantNotification{},
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
		{&CommerceOrderPaidOutbox{}, "idx_commerce_paid_outbox_identity"},
		{&CommerceMerchantNotification{}, "idx_commerce_notification_event"},
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
		{&CommercePaymentReconciliationTask{}, "LockedAt"},
		{&CommerceOrderItem{}, "ReservationStatus"},
		{&CommerceOrderItem{}, "ReleasedAt"},
		{&CommerceAfterSaleRequest{}, "ProviderRefundReference"},
		{&CommerceAfterSaleRequest{}, "ProviderRefundAmountCents"},
		{&CommerceOrderItem{}, "MediaSnapshotJSON"},
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

func TestCommerceStorefrontMultiDomainMigrationPreservesAccountSessionsAndRebuildsIndexes(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}

	singleTenant := Tenant{Name: "Storefront migration single tenant", SystemCode: "STOREFRONT-MIGRATION-SINGLE", SecretKey: "migration-secret", Status: "active"}
	if err := db.Create(&singleTenant).Error; err != nil {
		t.Fatalf("create single-binding tenant: %v", err)
	}
	dualTenant := Tenant{Name: "Storefront migration dual tenant", SystemCode: "STOREFRONT-MIGRATION-DUAL", SecretKey: "migration-secret", Status: "active"}
	if err := db.Create(&dualTenant).Error; err != nil {
		t.Fatalf("create dual-binding tenant: %v", err)
	}
	newAccount := func(tenantID uint, code string) ChannelAccount {
		account := ChannelAccount{TenantID: tenantID, Code: code, Type: "wechat_miniapp", AppID: code + "-app", Status: "active", Environment: "production"}
		if err := db.Create(&account).Error; err != nil {
			t.Fatalf("create storefront account %s: %v", code, err)
		}
		return account
	}
	newLocation := func(tenantID uint, businessType, name string) CommerceFulfillmentLocation {
		locationType := "store"
		if businessType == "retail" {
			locationType = "warehouse"
		}
		location := CommerceFulfillmentLocation{TenantID: tenantID, BusinessType: businessType, Name: name, LocationType: locationType, Status: "active"}
		if err := db.Create(&location).Error; err != nil {
			t.Fatalf("create storefront location %s: %v", name, err)
		}
		return location
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: singleTenant.ID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("create single capability: %v", err)
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: dualTenant.ID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("create dual capability: %v", err)
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: dualTenant.ID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatalf("create dual retail capability: %v", err)
	}
	singleAccount := newAccount(singleTenant.ID, "migration-single")
	dualAccount := newAccount(dualTenant.ID, "migration-dual")
	singleLocation := newLocation(singleTenant.ID, "restaurant", "single location")
	dualLocationA := newLocation(dualTenant.ID, "restaurant", "dual restaurant location")
	dualLocationB := newLocation(dualTenant.ID, "retail", "dual retail location")
	singleBinding := CommerceStorefrontBinding{TenantID: singleTenant.ID, ChannelAccountID: singleAccount.ID, BusinessType: "restaurant", LocationID: singleLocation.ID, Status: "active"}
	if err := db.Create(&singleBinding).Error; err != nil {
		t.Fatalf("create single binding: %v", err)
	}
	dualBindingA := CommerceStorefrontBinding{TenantID: dualTenant.ID, ChannelAccountID: dualAccount.ID, BusinessType: "restaurant", LocationID: dualLocationA.ID, Status: "active"}
	if err := db.Create(&dualBindingA).Error; err != nil {
		t.Fatalf("create dual restaurant binding: %v", err)
	}
	legacySession := func(tenantID, accountID uint, subject string) CommerceCustomerSession {
		session := CommerceCustomerSession{
			TenantID: tenantID, ChannelAccountID: accountID,
			SubjectHash: subject, TokenHash: subject + "-token", ExpiresAt: time.Now().Add(time.Hour), Status: "active",
		}
		if err := db.Create(&session).Error; err != nil {
			t.Fatalf("create legacy session %s: %v", subject, err)
		}
		return session
	}
	singleSession := legacySession(singleTenant.ID, singleAccount.ID, "single-subject")
	dualSession := legacySession(dualTenant.ID, dualAccount.ID, "dual-subject")

	// Recreate the schema shape produced by version 137: no experimental
	// binding column, account-only binding uniqueness, and a non-partial
	// account-session uniqueness index. This keeps the test meaningful even
	// though testdb starts from the current model.
	if err := db.Exec(`
		DROP INDEX IF EXISTS idx_commerce_storefront_bindings_account_domain;
		CREATE UNIQUE INDEX idx_commerce_storefront_bindings_account
			ON commerce_storefront_bindings (tenant_id, channel_account_id);
		DROP INDEX IF EXISTS idx_commerce_customer_sessions_subject;
		CREATE UNIQUE INDEX idx_commerce_customer_sessions_subject
			ON commerce_customer_sessions (channel_account_id, subject_hash);
		ALTER TABLE commerce_customer_sessions DROP COLUMN IF EXISTS storefront_binding_id;
	`).Error; err != nil {
		t.Fatalf("restore schema-137 storefront shape: %v", err)
	}
	if err := migrateCommerceStorefrontMultiDomain(db, CurrentPostgresSchemaVersion-1); err != nil {
		t.Fatalf("run storefront multi-domain migration: %v", err)
	}
	var migratedSingle CommerceCustomerSession
	if err := db.First(&migratedSingle, singleSession.ID).Error; err != nil {
		t.Fatalf("load migrated single session: %v", err)
	}
	if migratedSingle.Status != "active" || migratedSingle.ChannelAccountID != singleAccount.ID || migratedSingle.SubjectHash != "single-subject" {
		t.Fatalf("single-binding legacy session=%+v, want account-scoped active session", migratedSingle)
	}
	var migratedDual CommerceCustomerSession
	if err := db.First(&migratedDual, dualSession.ID).Error; err != nil {
		t.Fatalf("load migrated dual session: %v", err)
	}
	if migratedDual.Status != "active" || migratedDual.ChannelAccountID != dualAccount.ID || migratedDual.SubjectHash != "dual-subject" {
		t.Fatalf("dual-binding legacy session=%+v, want account-scoped active session", migratedDual)
	}
	if db.Migrator().HasColumn(&CommerceCustomerSession{}, "storefront_binding_id") {
		t.Fatal("experimental storefront_binding_id column remains after account-session migration")
	}

	var bindingIndex, sessionIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_commerce_storefront_bindings_account_domain'`).Scan(&bindingIndex).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(bindingIndex), "deleted_at is null") || !strings.Contains(bindingIndex, "business_type") {
		t.Fatalf("binding index does not scope soft-deleted rows and business type: %s", bindingIndex)
	}
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_commerce_customer_sessions_subject'`).Scan(&sessionIndex).Error; err != nil {
		t.Fatal(err)
	}
	lowerSessionIndex := strings.ToLower(sessionIndex)
	if !strings.Contains(lowerSessionIndex, "channel_account_id") || !strings.Contains(lowerSessionIndex, "subject_hash") || !strings.Contains(lowerSessionIndex, "status") || !strings.Contains(lowerSessionIndex, "active") || strings.Contains(sessionIndex, "storefront_binding_id") {
		t.Fatalf("session index does not scope active account sessions: %s", sessionIndex)
	}
	dualBindingB := CommerceStorefrontBinding{TenantID: dualTenant.ID, ChannelAccountID: dualAccount.ID, BusinessType: "retail", LocationID: dualLocationB.ID, Status: "active"}
	if err := db.Create(&dualBindingB).Error; err != nil {
		t.Fatalf("same AppID could not add second business binding after migration: %v", err)
	}

	// Rerunning the correction against an already repaired 138 database is a
	// no-op and must not disturb existing sessions.
	if err := migrateCommerceStorefrontMultiDomain(db, CurrentPostgresSchemaVersion); err != nil {
		t.Fatalf("rerun storefront multi-domain migration: %v", err)
	}
	var rerunSession CommerceCustomerSession
	if err := db.First(&rerunSession, dualSession.ID).Error; err != nil || rerunSession.Status != "active" {
		t.Fatalf("rerun changed account session=%+v err=%v", rerunSession, err)
	}

	// A deployment may have recorded schema 138 while the short-lived
	// binding-scoped session experiment was still present. The repair path must
	// inspect the physical column, not only the schema marker, and preserve the
	// account-scoped session facts while removing that stale shape.
	if err := db.Exec("ALTER TABLE commerce_customer_sessions ADD COLUMN storefront_binding_id bigint").Error; err != nil {
		t.Fatalf("add experimental storefront_binding_id column: %v", err)
	}
	if !db.Migrator().HasColumn(&CommerceCustomerSession{}, "storefront_binding_id") {
		t.Fatal("experimental storefront_binding_id column was not added to repair fixture")
	}
	if err := migrateCommerceStorefrontMultiDomain(db, CurrentPostgresSchemaVersion); err != nil {
		t.Fatalf("repair experimental schema-138 storefront shape: %v", err)
	}
	if db.Migrator().HasColumn(&CommerceCustomerSession{}, "storefront_binding_id") {
		t.Fatal("schema-138 repair left experimental storefront_binding_id column")
	}
	var repairedSession CommerceCustomerSession
	if err := db.First(&repairedSession, dualSession.ID).Error; err != nil || repairedSession.Status != "active" || repairedSession.ChannelAccountID != dualAccount.ID {
		t.Fatalf("schema-138 repair changed account session=%+v err=%v", repairedSession, err)
	}
}

func TestCommercePaymentReconciliationLockMigrationRepairsMissingColumn(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE commerce_payment_reconciliation_tasks DROP COLUMN IF EXISTS locked_at").Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasColumn(&CommercePaymentReconciliationTask{}, "LockedAt") {
		t.Fatal("locked_at column was not removed from migration fixture")
	}
	if err := migrateCommercePaymentReconciliationLock(db, 136); err != nil {
		t.Fatalf("repair missing locked_at column: %v", err)
	}
	if !db.Migrator().HasColumn(&CommercePaymentReconciliationTask{}, "LockedAt") {
		t.Fatal("locked_at column was not restored")
	}
	if !db.Migrator().HasIndex(&CommercePaymentReconciliationTask{}, "idx_commerce_payment_reconciliation_claim") {
		t.Fatal("reconciliation claim index was not restored")
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
