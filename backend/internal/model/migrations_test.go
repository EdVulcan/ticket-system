package model

import (
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
		&CtripOrderLink{}, &CtripOrderItem{}, &XiaohongshuWebhookEvent{},
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
