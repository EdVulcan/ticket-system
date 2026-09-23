package model

import (
	"strings"
	"testing"
	"time"

	"ticket-backend/internal/testdb"

	"gorm.io/gorm"
)

// TestCommercePhaseTwoMigration139To140 is the acceptance gate for the
// second commercial-domain schema phase. It deliberately restores a disposable
// database to the 139 marker and removes only phase-two tables before rerunning
// the normal migration entry point. This keeps the test meaningful even though
// testdb starts from the current model set.
func TestCommercePhaseTwoMigration139To140(t *testing.T) {
	if CurrentPostgresSchemaVersion < 140 {
		t.Fatalf("schema 140 migration is not registered; current schema=%d", CurrentPostgresSchemaVersion)
	}

	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("create schema baseline: %v", err)
	}

	tenant := Tenant{
		Name:       "Commerce phase-two tenant",
		SystemCode: "COMMERCE-PHASE-TWO",
		SecretKey:  "phase-two-secret",
		Status:     "active",
	}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := db.Create(&TenantBusinessCapability{TenantID: tenant.ID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatalf("create retail capability: %v", err)
	}
	location := CommerceFulfillmentLocation{
		TenantID: tenant.ID, BusinessType: "retail", Name: "Phase-two warehouse", LocationType: "warehouse", Status: "active",
	}
	if err := db.Create(&location).Error; err != nil {
		t.Fatalf("create location: %v", err)
	}
	account := ChannelAccount{
		TenantID: tenant.ID, Code: "phase-two-wechat", Type: "wechat_miniapp", AppID: "wx-phase-two", Status: "active", Environment: "sandbox",
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create channel account: %v", err)
	}

	// Preserve facts from both commercial storefront and ticketing domains.
	commerceOrder := CommerceOrder{
		TenantID: tenant.ID, OrderNo: "COM-PHASE-TWO-ORDER", BusinessType: "retail", Channel: "wechat_miniapp",
		CustomerID: "customer-phase-two", LocationID: location.ID, OriginalAmountCents: 12900,
		DiscountCents: 900, TotalAmountCents: 12000, PaymentStatus: "paid", FulfillmentStatus: "pending",
		RefundStatus: "none", ContactName: "Phase Two", ContactPhone: "13800000000",
	}
	if err := db.Create(&commerceOrder).Error; err != nil {
		t.Fatalf("create commercial order: %v", err)
	}
	session := CommerceCustomerSession{
		TenantID: tenant.ID, ChannelAccountID: account.ID, SubjectHash: strings.Repeat("a", 64),
		TokenHash: strings.Repeat("b", 64), ExpiresAt: time.Now().Add(time.Hour), Status: "active",
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("create customer session: %v", err)
	}

	area := ScenicArea{TenantID: tenant.ID, Code: "PHASE-TWO-AREA", Name: "Phase-two area", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatalf("create scenic area: %v", err)
	}
	product := Product{
		TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "Phase-two ticket", Price: 80,
		ProductKind: "ticket", Type: "online", Status: "online",
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create ticket product: %v", err)
	}
	ticketOrder := Order{
		TenantID: tenant.ID, OrderNo: "TICKET-PHASE-TWO-ORDER", Status: "paid", TotalAmount: 80,
		Channel: "online", Environment: "production",
	}
	if err := db.Create(&ticketOrder).Error; err != nil {
		t.Fatalf("create ticket order: %v", err)
	}
	item := OrderItem{
		OrderID: ticketOrder.ID, ProductID: product.ID, ProductName: product.Name, Quantity: 1,
		Price: 80, FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create ticket order item: %v", err)
	}
	ticket := Ticket{
		TenantID: tenant.ID, OrderID: ticketOrder.ID, OrderItemID: item.ID, ScenicAreaID: area.ID,
		FulfillmentProductID: product.ID, FulfillmentTenantID: tenant.ID, FulfillmentScenicAreaID: area.ID,
		RuleSnapshot: `{"admission_policy":"pool_v1"}`, CodeMode: "order", TicketCode: "PHASE-TWO-TICKET-CODE",
		Status: "unused", Environment: "production",
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	historical := struct {
		commerceOrder CommerceOrder
		session       CommerceCustomerSession
		ticketOrder   Order
		item          OrderItem
		ticket        Ticket
	}{commerceOrder, session, ticketOrder, item, ticket}

	// Recreate a 139 deployment: phase-two tables are absent and the marker is
	// 139. The database is disposable, so CASCADE is safe and deterministic.
	for _, table := range phaseTwoTables() {
		if err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE").Error; err != nil {
			t.Fatalf("remove phase-two table %s: %v", table, err)
		}
	}
	if err := db.Where("version >= ?", 139).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatalf("reset schema marker: %v", err)
	}
	if err := db.Create(&SchemaMigration{Version: 139, Name: "commerce storefront multi-domain", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatalf("create schema-139 marker: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("upgrade schema 139 to %d: %v", CurrentPostgresSchemaVersion, err)
	}
	assertPhaseTwoSchema(t, db)
	assertHistoricalCommerceFacts(t, db, historical.commerceOrder, historical.session)
	assertHistoricalTicketFacts(t, db, historical.ticketOrder, historical.item, historical.ticket)
	assertPhaseTwoCompatibilityConfig(t, db, tenant.ID, location.ID, "retail")

	// A second deployment pass must be a no-op for both schema objects and
	// immutable business facts.
	if err := runMigrations(db); err != nil {
		t.Fatalf("rerun phase-two migration: %v", err)
	}
	assertPhaseTwoSchema(t, db)
	assertHistoricalCommerceFacts(t, db, historical.commerceOrder, historical.session)
	assertHistoricalTicketFacts(t, db, historical.ticketOrder, historical.item, historical.ticket)
	assertPhaseTwoCompatibilityConfig(t, db, tenant.ID, location.ID, "retail")
}

func TestCommercePhaseTwoMigration140RepairsIncompletePhysicalSchema(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("create schema baseline: %v", err)
	}
	var marker SchemaMigration
	if err := db.Order("version DESC").First(&marker).Error; err != nil || marker.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("schema marker=%+v err=%v", marker, err)
	}

	tenant := Tenant{Name: "Commerce phase-two repair tenant", SystemCode: "COMMERCE-PHASE-TWO-REPAIR", SecretKey: "phase-two-repair-secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create repair tenant: %v", err)
	}
	location := CommerceFulfillmentLocation{TenantID: tenant.ID, BusinessType: "retail", Name: "Repair warehouse", LocationType: "warehouse", Status: "active"}
	if err := db.Create(&location).Error; err != nil {
		t.Fatalf("create repair location: %v", err)
	}

	// Simulate a deployment that recorded schema 140 before its explicit
	// phase-two DDL completed. The named token index is absent, the location
	// compatibility row is missing, and one phase-two constraint is absent.
	if err := db.Exec("DROP INDEX IF EXISTS idx_commerce_checkout_quote_token").Error; err != nil {
		t.Fatalf("remove phase-two token index: %v", err)
	}
	if err := db.Exec("ALTER TABLE commerce_delivery_slots DROP CONSTRAINT IF EXISTS chk_commerce_delivery_slot_weekday").Error; err != nil {
		t.Fatalf("remove phase-two constraint: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("repair schema 140: %v", err)
	}
	var indexDefinition string
	if err := db.Raw(`SELECT COALESCE(indexdef, '') FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_commerce_checkout_quote_token'`).Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	lowerIndexDefinition := strings.ToLower(indexDefinition)
	if !strings.Contains(lowerIndexDefinition, "create unique index") || !strings.Contains(lowerIndexDefinition, "deleted_at") || !strings.Contains(lowerIndexDefinition, "is null") {
		t.Fatalf("repaired token index=%s", indexDefinition)
	}
	var constraintExists bool
	if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_commerce_delivery_slot_weekday')`).Scan(&constraintExists).Error; err != nil {
		t.Fatal(err)
	}
	if !constraintExists {
		t.Fatal("phase-two weekday constraint was not repaired")
	}
	assertPhaseTwoCompatibilityConfig(t, db, tenant.ID, location.ID, "retail")
}

func TestCommercePromotionScopeMigrationBackfillsLegacyRows(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("create schema baseline: %v", err)
	}
	tenant := Tenant{Name: "Promotion migration tenant", SystemCode: "PROMOTION-MIGRATION", SecretKey: "promotion-migration-secret", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	account := ChannelAccount{TenantID: tenant.ID, Code: "promotion-migration-wechat", Type: "wechat_miniapp", Status: "active"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create channel account: %v", err)
	}
	now := time.Now().UTC()
	template := CommerceCouponTemplate{
		TenantID: tenant.ID, ChannelAccountID: account.ID, BusinessType: "retail", Name: "legacy promotion",
		DiscountCents: 100, ValidDays: 1, Status: "active", PerCustomerCap: 1,
		RefundReturnPolicy: "unfulfilled_full_refund_if_valid",
	}
	if err := db.Create(&template).Error; err != nil {
		t.Fatalf("create legacy template: %v", err)
	}
	grant := CommerceCouponGrant{
		TenantID: tenant.ID, TemplateID: template.ID, ChannelAccountID: account.ID, BusinessType: "retail",
		CustomerID: "legacy-customer", Source: "manual", SourceIdentity: "legacy-grant", DiscountCents: 100,
		Status: "available", ExpiresAt: now.Add(time.Hour), RefundReturnPolicy: "unfulfilled_full_refund_if_valid",
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("create legacy grant: %v", err)
	}
	campaign := CommerceAssistCampaign{
		TenantID: tenant.ID, ChannelAccountID: account.ID, BusinessType: "retail", Title: "legacy assist",
		StarterCouponTemplateID: template.ID, HelperCouponTemplateID: template.ID,
		RequiredUniqueHelpers: 1, PerStarterSessionLimit: 1, StartsAt: now, EndsAt: now.Add(time.Hour), Status: "active",
	}
	if err := db.Create(&campaign).Error; err != nil {
		t.Fatalf("create legacy campaign: %v", err)
	}
	for _, table := range []interface{}{&CommerceCouponTemplateBusinessType{}, &CommerceCouponGrantBusinessType{}, &CommerceAssistCampaignBusinessType{}} {
		if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Where("tenant_id = ?", tenant.ID).Delete(table).Error; err != nil {
			t.Fatalf("remove normalized rows before backfill for %T: %v", table, err)
		}
	}
	if err := migrateCommercePromotionBusinessTypes(db, 143); err != nil {
		t.Fatalf("backfill normalized promotion scopes: %v", err)
	}
	if err := migrateCommercePromotionBusinessTypes(db, 143); err != nil {
		t.Fatalf("repeat normalized promotion scope migration: %v", err)
	}
	var templateScope, grantScope, campaignScope int64
	if err := db.Model(&CommerceCouponTemplateBusinessType{}).Where("tenant_id = ? AND template_id = ? AND business_type = ?", tenant.ID, template.ID, "retail").Count(&templateScope).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&CommerceCouponGrantBusinessType{}).Where("tenant_id = ? AND grant_id = ? AND business_type = ?", tenant.ID, grant.ID, "retail").Count(&grantScope).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&CommerceAssistCampaignBusinessType{}).Where("tenant_id = ? AND campaign_id = ? AND business_type = ?", tenant.ID, campaign.ID, "retail").Count(&campaignScope).Error; err != nil {
		t.Fatal(err)
	}
	if templateScope != 1 || grantScope != 1 || campaignScope != 1 {
		t.Fatalf("backfill counts template=%d grant=%d campaign=%d", templateScope, grantScope, campaignScope)
	}
}

func assertPhaseTwoCompatibilityConfig(t *testing.T, db *gorm.DB, tenantID, locationID uint, businessType string) {
	t.Helper()
	var rows []CommerceLocationServiceConfig
	if err := db.Where("tenant_id = ? AND location_id = ? AND business_type = ?", tenantID, locationID, businessType).Find(&rows).Error; err != nil {
		t.Fatalf("read compatibility service config: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("compatibility service config count=%d want 1", len(rows))
	}
	config := rows[0]
	if config.Status != "active" || config.PickupEnabled || config.DeliveryEnabled || !config.ShippingEnabled || config.ShippingFeeCents != 0 {
		t.Fatalf("unexpected retail compatibility service config: %+v", config)
	}
}

func phaseTwoTables() []string {
	return []string{
		"commerce_location_service_configs",
		"commerce_delivery_zones",
		"commerce_delivery_slots",
		"commerce_checkout_quotes",
		"commerce_order_adjustments",
		"commerce_shipments",
		"commerce_shipment_events",
		"commerce_coupon_templates",
		"commerce_coupon_template_business_types",
		"commerce_coupon_grants",
		"commerce_coupon_grant_business_types",
		"commerce_assist_campaigns",
		"commerce_assist_campaign_business_types",
		"commerce_assist_sessions",
		"commerce_assist_records",
	}
}

func assertPhaseTwoSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range phaseTwoTables() {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("phase-two table %s is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&CommerceLocationServiceConfig{}, "idx_commerce_location_service_scope"},
		{&CommerceShipment{}, "idx_commerce_shipments_tenant_no"},
		{&CommerceCouponTemplate{}, "idx_commerce_coupon_templates_scope_name"},
		{&CommerceCouponTemplateBusinessType{}, "idx_commerce_coupon_template_business_types_unique"},
		{&CommerceCouponGrant{}, "idx_commerce_coupon_grants_issue"},
		{&CommerceCouponGrantBusinessType{}, "idx_commerce_coupon_grant_business_types_unique"},
		{&CommerceAssistCampaign{}, "idx_commerce_assist_campaigns_scope_name"},
		{&CommerceAssistCampaignBusinessType{}, "idx_commerce_assist_campaign_business_types_unique"},
		{&CommerceAssistSession{}, "idx_commerce_assist_sessions_idempotency"},
		{&CommerceAssistRecord{}, "idx_commerce_assist_records_session_helper"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("phase-two index %s is missing", index.name)
		}
	}
	for _, constraint := range []string{
		"chk_commerce_location_service_business_type",
		"chk_commerce_delivery_slot_weekday",
		"chk_commerce_shipments_status",
		"chk_commerce_coupon_templates_discount",
		"chk_commerce_coupon_template_business_types_type",
		"chk_commerce_coupon_grants_status",
		"chk_commerce_coupon_grant_business_types_type",
		"chk_commerce_assist_campaigns_status",
		"chk_commerce_assist_campaign_business_types_type",
		"chk_commerce_assist_sessions_status",
	} {
		var exists bool
		if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = ?)`, constraint).Scan(&exists).Error; err != nil {
			t.Fatalf("check constraint %s lookup failed: %v", constraint, err)
		}
		if !exists {
			t.Fatalf("phase-two CHECK constraint %s is missing", constraint)
		}
	}
}

func assertHistoricalCommerceFacts(t *testing.T, db *gorm.DB, wantOrder CommerceOrder, wantSession CommerceCustomerSession) {
	t.Helper()
	var gotOrder CommerceOrder
	if err := db.First(&gotOrder, wantOrder.ID).Error; err != nil {
		t.Fatalf("read historical commercial order: %v", err)
	}
	if gotOrder.TenantID != wantOrder.TenantID || gotOrder.OrderNo != wantOrder.OrderNo || gotOrder.BusinessType != wantOrder.BusinessType || gotOrder.TotalAmountCents != wantOrder.TotalAmountCents || gotOrder.PaymentStatus != wantOrder.PaymentStatus {
		t.Fatalf("historical commercial order changed: got=%+v want=%+v", gotOrder, wantOrder)
	}
	var gotSession CommerceCustomerSession
	if err := db.First(&gotSession, wantSession.ID).Error; err != nil {
		t.Fatalf("read historical customer session: %v", err)
	}
	if gotSession.TenantID != wantSession.TenantID || gotSession.ChannelAccountID != wantSession.ChannelAccountID || gotSession.SubjectHash != wantSession.SubjectHash || gotSession.TokenHash != wantSession.TokenHash || gotSession.Status != wantSession.Status {
		t.Fatalf("historical customer session changed: got=%+v want=%+v", gotSession, wantSession)
	}
}

func assertHistoricalTicketFacts(t *testing.T, db *gorm.DB, wantOrder Order, wantItem OrderItem, wantTicket Ticket) {
	t.Helper()
	var gotOrder Order
	if err := db.First(&gotOrder, wantOrder.ID).Error; err != nil {
		t.Fatalf("read historical ticket order: %v", err)
	}
	if gotOrder.TenantID != wantOrder.TenantID || gotOrder.OrderNo != wantOrder.OrderNo || gotOrder.Status != wantOrder.Status || gotOrder.TotalAmount != wantOrder.TotalAmount {
		t.Fatalf("historical ticket order changed: got=%+v want=%+v", gotOrder, wantOrder)
	}
	var gotItem OrderItem
	if err := db.First(&gotItem, wantItem.ID).Error; err != nil {
		t.Fatalf("read historical ticket order item: %v", err)
	}
	if gotItem.OrderID != wantItem.OrderID || gotItem.ProductID != wantItem.ProductID || gotItem.Quantity != wantItem.Quantity || gotItem.ProductName != wantItem.ProductName || gotItem.Price != wantItem.Price {
		t.Fatalf("historical ticket order item changed: got=%+v want=%+v", gotItem, wantItem)
	}
	var gotTicket Ticket
	if err := db.First(&gotTicket, wantTicket.ID).Error; err != nil {
		t.Fatalf("read historical ticket: %v", err)
	}
	if gotTicket.OrderID != wantTicket.OrderID || gotTicket.OrderItemID != wantTicket.OrderItemID || gotTicket.TenantID != wantTicket.TenantID || gotTicket.ScenicAreaID != wantTicket.ScenicAreaID || gotTicket.TicketCode != wantTicket.TicketCode || gotTicket.Status != wantTicket.Status || gotTicket.RuleSnapshot != wantTicket.RuleSnapshot {
		t.Fatalf("historical ticket changed: got=%+v want=%+v", gotTicket, wantTicket)
	}
}
