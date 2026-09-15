package model

import (
	"testing"
	"time"

	"ticket-backend/internal/testdb"
)

func TestPostgresSchema120CreatesPayloadFreeUpstreamDispatchTables(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 120 {
		t.Fatalf("schema version=%d, want at least 120", CurrentPostgresSchemaVersion)
	}
	for _, table := range []interface{}{&UpstreamDispatchGate{}, &UpstreamDispatchWaiter{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table for %T is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&UpstreamDispatchGate{}, "idx_upstream_dispatch_gate_egress"},
		{&UpstreamDispatchWaiter{}, "idx_upstream_dispatch_waiter_active_identity"},
		{&UpstreamDispatchWaiter{}, "idx_upstream_dispatch_waiter_queue_order"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s for %T is missing", index.name, index.model)
		}
	}
	var waiterColumns []struct {
		Column string
	}
	if err := db.Raw(`SELECT column_name AS column FROM information_schema.columns WHERE table_name = 'upstream_dispatch_waiters'`).Scan(&waiterColumns).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range waiterColumns {
		if column.Column == "request_payload" || column.Column == "credentials" {
			t.Fatalf("dispatch waiter persisted forbidden payload column %q", column.Column)
		}
	}
	row := UpstreamDispatchWaiter{EgressKey: "zyb:test", SemanticKey: "semantic", Transaction: "QUERY", BodyHash: "hash", Priority: 10, EnqueuedAt: time.Now(), Status: "queued"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSchema119To120KeepsExistingRowsAndAddsDispatchQueue(t *testing.T) {
	db, _ := upstreamLegacyFixture(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	const factsSQL = `SELECT md5(jsonb_build_object(
		'orders', (SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM orders o),
		'tickets', (SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM tickets t),
		'payments', (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM payments p),
		'supply', (SELECT jsonb_agg(to_jsonb(s)-'sync_requested_at'-'sync_failure_count' ORDER BY id) FROM order_item_supply_snapshots s)
	)::text)`
	var beforeFacts string
	if err := db.Raw(factsSQL).Scan(&beforeFacts).Error; err != nil {
		t.Fatal(err)
	}
	var beforeOrders, beforeTickets, beforeMoney int64
	if err := db.Table("orders").Count(&beforeOrders).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tickets").Count(&beforeTickets).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ledger_entries").Count(&beforeMoney).Error; err != nil {
		t.Fatal(err)
	}
	if beforeOrders == 0 || beforeTickets == 0 {
		t.Fatal("migration fixture must contain sold business records")
	}
	if err := db.Migrator().DropTable(&UpstreamDispatchWaiter{}, &UpstreamDispatchGate{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&OrderItemSupplySnapshot{}, "SyncRequestedAt"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&OrderItemSupplySnapshot{}, "SyncFailureCount"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version >= ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 119, Name: "legacy schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var afterOrders, afterTickets, afterMoney int64
	if err := db.Table("orders").Count(&afterOrders).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tickets").Count(&afterTickets).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ledger_entries").Count(&afterMoney).Error; err != nil {
		t.Fatal(err)
	}
	if beforeOrders != afterOrders || beforeTickets != afterTickets || beforeMoney != afterMoney {
		t.Fatalf("migration changed legacy counts: orders %d->%d tickets %d->%d money %d->%d", beforeOrders, afterOrders, beforeTickets, afterTickets, beforeMoney, afterMoney)
	}
	var afterFacts string
	if err := db.Raw(factsSQL).Scan(&afterFacts).Error; err != nil {
		t.Fatal(err)
	}
	if afterFacts != beforeFacts {
		t.Fatal("scheduler upgrade changed existing orders, ticket codes, payments or supply identity")
	}
	if !db.Migrator().HasTable(&UpstreamDispatchGate{}) || !db.Migrator().HasTable(&UpstreamDispatchWaiter{}) {
		t.Fatal("schema 120 dispatch tables are missing")
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v err=%v", latest, err)
	}
}
