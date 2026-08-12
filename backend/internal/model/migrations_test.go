package model

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresMigrationsReachCurrentVersionAndAreIdempotent(t *testing.T) {
	db := testdb.Open(t)
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
	if latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%d, want %d", latest.Version, CurrentPostgresSchemaVersion)
	}
	for _, table := range []interface{}{
		&ProductRevision{}, &LedgerEntry{}, &ChannelAccount{}, &TourGroup{}, &POSShift{},
		&SettlementStatement{}, &AfterSaleRequest{}, &ChannelReservation{}, &FinancialDocument{},
		&TeamSettlementStatement{}, &ChannelReconciliation{}, &OrderVisitor{}, &BundleProduct{},
		&CtripOrderLink{}, &CtripOrderItem{}, &XiaohongshuWebhookEvent{}, &SupplierBusinessType{},
		&HotelProperty{}, &HotelRoomType{}, &HotelRatePlan{}, &HotelRoomInventory{},
		&ScenicHotelPackage{}, &HotelReservation{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table for %T is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&Payment{}, "idx_payment_idempotency"},
		{&Refund{}, "idx_refund_allocation_sequence"},
		{&PlatformUser{}, "idx_platform_users_initial_admin"},
		{&ChannelRequest{}, "idx_channel_request"},
		{&CtripOrderLink{}, "idx_ctrip_order_account_ota"},
		{&TravelContract{}, "idx_travel_contract_scope_no"},
		{&TourGroup{}, "idx_team_active_fulfillment_group"},
		{&TourGroupMember{}, "idx_team_member_active_ticket"},
		{&XiaohongshuWebhookEvent{}, "idx_xhs_webhook_payload"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s is missing", index.name)
		}
	}
	var channelRequestIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_channel_request'`).Scan(&channelRequestIndex).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(channelRequestIndex, "channel_account_id") || !strings.Contains(channelRequestIndex, "endpoint") || !strings.Contains(channelRequestIndex, "request_id") {
		t.Fatalf("channel request idempotency index has wrong scope: %s", channelRequestIndex)
	}
	defaultStatusTenant := Tenant{Name: "Default Status Tenant", SystemCode: "SUPPLIER-BUSINESS-DEFAULT", Status: "active"}
	if err := db.Create(&defaultStatusTenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, created_at, updated_at)
		VALUES (?, ?, NOW(), NOW())
	`, defaultStatusTenant.ID, "hotel").Error; err != nil {
		t.Fatal(err)
	}
	var defaultStatus string
	if err := db.Raw(`
		SELECT status FROM supplier_business_types
		WHERE tenant_id = ? AND business_type = ?
	`, defaultStatusTenant.ID, "hotel").Scan(&defaultStatus).Error; err != nil {
		t.Fatal(err)
	}
	if defaultStatus != "suspended" {
		t.Fatalf("database default supplier business status=%q, want suspended", defaultStatus)
	}
	var hotelStatusConstraint string
	if err := db.Raw(`
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'chk_hotel_reservations_status'
	`).Scan(&hotelStatusConstraint).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hotelStatusConstraint, "checked_in") || !strings.Contains(hotelStatusConstraint, "checked_out") || !strings.Contains(hotelStatusConstraint, "no_show") {
		t.Fatalf("hotel reservation status constraint=%q", hotelStatusConstraint)
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, status, created_at, updated_at)
		VALUES (?, 'restaurant', 'active', NOW(), NOW())
	`, defaultStatusTenant.ID).Error; err == nil {
		t.Fatal("database accepted unsupported supplier business type")
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, status, created_at, updated_at)
		VALUES (?, 'scenic', 'pending', NOW(), NOW())
	`, defaultStatusTenant.ID).Error; err == nil {
		t.Fatal("database accepted unsupported supplier business status")
	}
}

func TestPostgresMigrationBackfillsOnlyActiveUnexpiredSuppliersAsScenic(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	type supplierFixture struct {
		name       string
		status     string
		expiresAt  *time.Time
		wantScenic bool
	}
	fixtures := []supplierFixture{
		{name: "active-no-expiry", status: "active", wantScenic: true},
		{name: "active-future-expiry", status: "active", expiresAt: &future, wantScenic: true},
		{name: "active-expired", status: "active", expiresAt: &past},
		{name: "pending", status: "pending"},
		{name: "rejected", status: "rejected"},
		{name: "suspended", status: "suspended"},
	}
	tenantIDs := make(map[string]uint, len(fixtures))
	for index, fixture := range fixtures {
		tenant := Tenant{
			Name: fixture.name, SystemCode: fmt.Sprintf("SCHEMA79-SUPPLIER-%d", index), Status: "active",
		}
		if err := db.Create(&tenant).Error; err != nil {
			t.Fatal(err)
		}
		tenantIDs[fixture.name] = tenant.ID
		if err := db.Create(&TenantCapability{
			TenantID: tenant.ID, Capability: "supplier", Status: fixture.status, ExpiresAt: fixture.expiresAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Schema 80 only adds supplier_business_types. Removing that table from a
	// fully migrated schema produces the exact schema 79 boundary for this change.
	if err := db.Migrator().DropTable(&SupplierBusinessType{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 79, Name: "xiaohongshu product and guarantee payment facts", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&SupplierBusinessType{}) {
		t.Fatal("schema 79 fixture unexpectedly contains supplier_business_types")
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("repeat schema 80 migration: %v", err)
	}
	for _, fixture := range fixtures {
		var count int64
		if err := db.Model(&SupplierBusinessType{}).
			Where("tenant_id = ? AND business_type = ? AND status = ?", tenantIDs[fixture.name], "scenic", "active").
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if got := count == 1; got != fixture.wantScenic {
			t.Fatalf("fixture %s scenic backfill=%v, want %v", fixture.name, got, fixture.wantScenic)
		}
	}
}

func TestPostgresMigrationRejectsNewerSchemaVersion(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	newerVersion := CurrentPostgresSchemaVersion + 1
	if err := db.Create(&SchemaMigration{
		Version: newerVersion, Name: "future application schema", AppliedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := runMigrations(db)
	if err == nil {
		t.Fatalf("application accepted newer database schema %d", newerVersion)
	}
	for _, expected := range []string{fmt.Sprint(newerVersion), fmt.Sprint(CurrentPostgresSchemaVersion), "refusing to start"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("newer-schema error %q does not contain %q", err, expected)
		}
	}
}

func TestTeamUniquenessMigrationReportsActionableLegacyConflicts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_team_active_fulfillment_group", "idx_team_member_active_ticket"} {
		if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := TourGroup{TenantID: 11, SupplierTenantID: 22, ScenicAreaID: 33, SalesOrderID: 44, GroupNo: "LEGACY-DUP-1", Name: "Legacy duplicate one", VisitDate: time.Now(), Status: "confirmed"}
	second := TourGroup{TenantID: 11, SupplierTenantID: 22, ScenicAreaID: 33, SalesOrderID: 44, GroupNo: "LEGACY-DUP-2", Name: "Legacy duplicate two", VisitDate: time.Now(), Status: "confirmed"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TourGroupMember{GroupID: first.ID, Name: "First", TicketCode: "LEGACY-TICKET-DUP", Status: "ticketed"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TourGroupMember{GroupID: second.ID, Name: "Second", TicketCode: "LEGACY-TICKET-DUP", Status: "ticketed"}).Error; err != nil {
		t.Fatal(err)
	}

	err := validateTeamUniquenessMigrationData(db)
	if err == nil {
		t.Fatal("legacy duplicate team facts were accepted before unique-index migration")
	}
	message := err.Error()
	for _, expected := range []string{"order=44", "supplier=22", "scenic=33", "LEGACY-DUP-1", "LEGACY-DUP-2", "LEGACY-TICKET-DUP"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("migration error %q does not identify %q", message, expected)
		}
	}
}

func TestPostgresMigrationDoesNotInferTravelPartnershipFromDistribution(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	supplier := Tenant{Name: "Migration Supplier", SystemCode: "MIGRATION-SUPPLIER", Status: "active"}
	partner := Tenant{Name: "Migration Partner", SystemCode: "MIGRATION-PARTNER", Status: "active"}
	if err := db.Create(&supplier).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&partner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]TenantCapability{
		{TenantID: supplier.ID, Capability: "supplier", Status: "active"},
		{TenantID: partner.ID, Capability: "distributor", Status: "active"},
		{TenantID: partner.ID, Capability: "travel_agency", Status: "active"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	relationship := DistributorRelationship{
		AgentTenantID: partner.ID, SupplierTenantID: supplier.ID,
		Status: "active", TravelStatus: "active",
	}
	if err := db.Create(&relationship).Error; err != nil {
		t.Fatal(err)
	}
	legitimatePartner := Tenant{Name: "Audited Travel Partner", SystemCode: "AUDITED-TRAVEL-PARTNER", Status: "active"}
	if err := db.Create(&legitimatePartner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TenantCapability{TenantID: legitimatePartner.ID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	legitimateRelationship := DistributorRelationship{
		AgentTenantID: legitimatePartner.ID, SupplierTenantID: supplier.ID,
		Status: "none", TravelStatus: "active",
	}
	if err := db.Create(&legitimateRelationship).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&AuditLog{
		ActorUserID: 9, ActorRole: "admin", Scope: "tenant", TenantID: supplier.ID,
		Action: "team.partner.apply", TargetType: "supplier_relationship", TargetID: legitimateRelationship.ID,
		Reason: "explicit travel partnership application",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion - 1, Name: "previous schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&relationship, relationship.ID).Error; err != nil {
		t.Fatal(err)
	}
	if relationship.Status != "active" || relationship.TravelStatus != "none" {
		t.Fatalf("distribution relationship leaked into travel partnership: %+v", relationship)
	}
	if relationship.DistributionAppliedAt == nil || relationship.TravelAppliedAt != nil {
		t.Fatalf("relationship application timestamps were not separated: %+v", relationship)
	}
	if err := db.First(&legitimateRelationship, legitimateRelationship.ID).Error; err != nil {
		t.Fatal(err)
	}
	if legitimateRelationship.TravelStatus != "active" || legitimateRelationship.TravelAppliedAt == nil {
		t.Fatalf("audited travel partnership was not preserved: %+v", legitimateRelationship)
	}
}

func TestPostgresOwnershipGuardsRejectCrossTenantRows(t *testing.T) {
	db := testdb.Open(t)
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
		t.Fatal("cross-tenant checkpoint was accepted by PostgreSQL guard")
	}
	if err := db.Create(&OrderVisitor{
		TenantID: first.ID, OrderID: 999, OrderItemID: 999, TicketID: 999,
		TicketCode: "forbidden", Sequence: 1, Name: "cross-tenant",
	}).Error; err == nil {
		t.Fatal("orphan order visitor was accepted by PostgreSQL guard")
	}
}
