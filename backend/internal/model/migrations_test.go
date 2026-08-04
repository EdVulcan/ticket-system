package model

import (
	"testing"
	"ticket-backend/internal/testdb"
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
		&CtripOrderLink{}, &CtripOrderItem{},
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
		{&CtripOrderLink{}, "idx_ctrip_order_account_ota"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s is missing", index.name)
		}
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
