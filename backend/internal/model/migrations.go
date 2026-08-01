package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

type SchemaMigration struct {
	Version   int       `gorm:"primaryKey"`
	Name      string    `gorm:"size:100;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

type migration struct {
	version int
	name    string
	apply   func(*gorm.DB) error
}

func runMigrations(db *gorm.DB) error {
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return err
	}
	migrations := []migration{
		{version: 1, name: "initial embedded schema", apply: migrateInitialSchema},
		{version: 2, name: "distribution uniqueness", apply: migrateDistributionUniqueness},
		{version: 3, name: "fulfillment ownership snapshots", apply: migrateFulfillmentOwnership},
		{version: 4, name: "order reservation expiry", apply: migrateOrderReservationExpiry},
		{version: 5, name: "payment callback credentials", apply: migratePaymentCallbackCredentials},
		{version: 6, name: "payment reconciliation tasks", apply: migratePaymentReconciliationTasks},
		{version: 7, name: "tenant status and capabilities", apply: migrateTenantStatusAndCapabilities},
		{version: 8, name: "scenic area fulfillment boundary", apply: migrateScenicAreaBoundary},
		{version: 9, name: "cash refund records", apply: migrateCashRefundRecords},
		{version: 10, name: "audit log", apply: migrateAuditLog},
		{version: 11, name: "platform identity", apply: migratePlatformIdentity},
		{version: 12, name: "product offers and seller listings", apply: migrateProductOffers},
		{version: 13, name: "supplier fulfillment projections", apply: migrateFulfillmentProjections},
		{version: 14, name: "immutable cent ledger and digital refunds", apply: migrateLedger},
		{version: 15, name: "independent external channels", apply: migrateChannels},
		{version: 16, name: "travel agency team operations", apply: migrateTravelTeams},
		{version: 17, name: "pos shifts and device alerts", apply: migratePOSOperations},
		{version: 18, name: "product revisions and original refund composition", apply: migrateProductRevisions},
		{version: 19, name: "settlement statements", apply: migrateSettlements},
		{version: 20, name: "phase zero authorization and conservation hardening", apply: migratePhaseZeroHardening},
		{version: 21, name: "composite ownership and POS operation facts", apply: migrateCompositeOwnershipAndPOSFacts},
		{version: 22, name: "staff resource scopes", apply: migrateStaffResourceScopes},
		{version: 23, name: "check-in ownership constraints", apply: migrateCheckInOwnership},
		{version: 24, name: "after-sales policy slots and durable external workflows", apply: migrateAfterSalesAndWorkflows},
		{version: 25, name: "channel reservation conversion and uniqueness", apply: migrateChannelReservationHardening},
		{version: 26, name: "financial document idempotency", apply: migrateFinancialDocumentIdempotency},
		{version: 27, name: "session token revocation versions", apply: migrateSessionTokenVersions},
		{version: 28, name: "settlement period idempotency", apply: migrateSettlementPeriodIdempotency},
		{version: 29, name: "channel request policy facts", apply: migrateChannelRequestPolicyFacts},
		{version: 30, name: "team contract settlement facts", apply: migrateTeamSettlementFacts},
		{version: 31, name: "integer capital account projections", apply: migrateIntegerCapitalAccountProjections},
		{version: 32, name: "channel bill reconciliation facts", apply: migrateChannelBillReconciliation},
		{version: 33, name: "digital refund operations state", apply: migrateDigitalRefundOperations},
		{version: 34, name: "transaction record cent projections", apply: migrateTransactionRecordCentProjections},
		{version: 35, name: "offer pricing floors and quotas", apply: migrateOfferPricingAndQuota},
		{version: 36, name: "legacy migration audit quarantine", apply: migrateMigrationAudit},
		{version: 37, name: "payment success timestamps", apply: migratePaymentSuccessTimestamps},
		{version: 38, name: "durable POS holds", apply: migratePOSHolds},
		{version: 39, name: "strict ownership database guards", apply: migrateStrictOwnershipGuards},
		{version: 40, name: "payment and refund cent facts", apply: migratePaymentCentFacts},
		{version: 41, name: "digital refund task leases", apply: migrateDigitalRefundTaskLeases},
		{version: 42, name: "channel request leases", apply: migrateChannelRequestLeases},
		{version: 43, name: "tenant qualification and lifecycle facts", apply: migrateTenantLifecycleFacts},
		{version: 44, name: "per-ticket visitor snapshots", apply: migrateVisitorSnapshots},
		{version: 45, name: "visitor snapshot ownership guards", apply: migrateVisitorSnapshotOwnership},
		{version: 46, name: "POS shift reconciliation facts", apply: migratePOSShiftReconciliation},
		{version: 47, name: "POS cash tender and shift corrections", apply: migratePOSCashAndShiftCorrections},
		{version: 48, name: "POS split payment idempotency", apply: migratePOSSplitPaymentIdempotency},
		{version: 49, name: "mixed payment refund allocations", apply: migrateMixedRefundAllocations},
		{version: 50, name: "append-only settlement adjustments", apply: migrateSettlementAdjustments},
	}
	for _, item := range migrations {
		var count int64
		if err := db.Model(&SchemaMigration{}).Where("version = ?", item.version).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.apply(tx); err != nil {
				return err
			}
			return tx.Create(&SchemaMigration{Version: item.version, Name: item.name, AppliedAt: time.Now()}).Error
		}); err != nil {
			return fmt.Errorf("migration %d (%s): %w", item.version, item.name, err)
		}
	}
	return nil
}

func migrateDigitalRefundTaskLeases(db *gorm.DB) error {
	return db.AutoMigrate(&DigitalRefundTask{})
}

func migrateChannelRequestLeases(db *gorm.DB) error {
	return db.AutoMigrate(&ChannelRequest{})
}

func migrateTenantLifecycleFacts(db *gorm.DB) error {
	if err := db.AutoMigrate(&Tenant{}); err != nil {
		return err
	}
	// Existing tenants were provisioned before platform qualification existed.
	// Preserve their current ability to operate, while making every new tenant
	// explicitly qualify before a platform operator can reactivate it.
	return db.Exec("UPDATE tenants SET qualification_status = 'approved' WHERE qualification_status IS NULL OR qualification_status = ''").Error
}

func migrateVisitorSnapshots(db *gorm.DB) error {
	// Use explicit idempotent DDL here. GORM AutoMigrate may rebuild related
	// SQLite tables; upgraded databases already contain ownership triggers on
	// orders/products/tickets and those triggers must never be detached during
	// a metadata-only migration.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS order_visitors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME,
		tenant_id INTEGER NOT NULL,
		order_id INTEGER NOT NULL,
		order_item_id INTEGER NOT NULL,
		ticket_id INTEGER NOT NULL,
		ticket_code TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		name TEXT NOT NULL,
		phone TEXT,
		identity_no TEXT,
		region TEXT
	)`).Error; err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_order_visitors_tenant_id ON order_visitors(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_visitors_order_id ON order_visitors(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_visitors_order_item_id ON order_visitors(order_item_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_order_visitors_ticket_id ON order_visitors(ticket_id)`,
		`CREATE INDEX IF NOT EXISTS idx_order_visitors_ticket_code ON order_visitors(ticket_code)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	// Ticket already has ownership triggers in upgraded databases. Calling
	// GORM AutoMigrate on it would rebuild the table and temporarily detach
	// those triggers, so add this compatibility column with a guarded DDL.
	return addColumnIfMissing(db, "tickets", "visitor_region", `ALTER TABLE tickets ADD COLUMN visitor_region TEXT`)
}

func migrateVisitorSnapshotOwnership(db *gorm.DB) error {
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS order_visitors_owner_insert BEFORE INSERT ON order_visitors
			WHEN NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM orders o
				JOIN order_items oi ON oi.order_id = o.id
				JOIN tickets t ON t.order_item_id = oi.id
				WHERE o.id = NEW.order_id AND oi.id = NEW.order_item_id AND t.id = NEW.ticket_id
				  AND o.tenant_id = NEW.tenant_id AND t.tenant_id = NEW.tenant_id
				  AND t.ticket_code = NEW.ticket_code
			)
			BEGIN SELECT RAISE(ABORT, 'order visitor ownership mismatch'); END`,
		`CREATE TRIGGER IF NOT EXISTS order_visitors_owner_update BEFORE UPDATE OF tenant_id, order_id, order_item_id, ticket_id, ticket_code ON order_visitors
			WHEN NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM orders o
				JOIN order_items oi ON oi.order_id = o.id
				JOIN tickets t ON t.order_item_id = oi.id
				WHERE o.id = NEW.order_id AND oi.id = NEW.order_item_id AND t.id = NEW.ticket_id
				  AND o.tenant_id = NEW.tenant_id AND t.tenant_id = NEW.tenant_id
				  AND t.ticket_code = NEW.ticket_code
			)
			BEGIN SELECT RAISE(ABORT, 'order visitor ownership mismatch'); END`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePOSShiftReconciliation(db *gorm.DB) error {
	return db.AutoMigrate(&POSShift{})
}

func migratePOSCashAndShiftCorrections(db *gorm.DB) error {
	return db.AutoMigrate(&Payment{}, &POSShift{}, &POSShiftCorrection{})
}

func migratePOSSplitPaymentIdempotency(db *gorm.DB) error {
	if err := db.AutoMigrate(&Payment{}); err != nil {
		return err
	}
	return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_idempotency
		ON payments(tenant_id, idempotency_key) WHERE idempotency_key <> ''`).Error
}

func migrateMixedRefundAllocations(db *gorm.DB) error {
	if err := db.AutoMigrate(&Refund{}); err != nil {
		return err
	}
	return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_refund_allocation_sequence
		ON refunds(parent_refund_id, allocation_seq) WHERE parent_refund_id != 0`).Error
}

func migrateSettlementAdjustments(db *gorm.DB) error {
	return db.AutoMigrate(&SettlementStatement{}, &SettlementAdjustment{})
}

func migrateTeamSettlementFacts(db *gorm.DB) error {
	return db.AutoMigrate(&TourGroup{}, &TeamSettlementStatement{})
}

func migrateIntegerCapitalAccountProjections(db *gorm.DB) error {
	if err := db.AutoMigrate(&CapitalAccount{}); err != nil {
		return err
	}
	// Legacy balances are decimal columns. Backfill the cent projections once;
	// all subsequent service writes update both representations atomically.
	return db.Exec(`
		UPDATE capital_accounts
		SET balance_cents = CAST(ROUND(balance * 100.0) AS INTEGER),
		    credit_line_cents = CAST(ROUND(credit_line * 100.0) AS INTEGER),
		    used_credit_cents = CAST(ROUND(used_credit * 100.0) AS INTEGER),
		    frozen_cents = CAST(ROUND(frozen_amount * 100.0) AS INTEGER)
	`).Error
}

func migrateChannelBillReconciliation(db *gorm.DB) error {
	return db.AutoMigrate(&ChannelBillRecord{}, &ChannelReconciliation{})
}

func migrateDigitalRefundOperations(db *gorm.DB) error {
	if err := db.AutoMigrate(&DigitalRefundTask{}); err != nil {
		return err
	}
	return db.Exec("UPDATE digital_refund_tasks SET max_attempts = 8 WHERE max_attempts IS NULL OR max_attempts <= 0").Error
}

func migrateTransactionRecordCentProjections(db *gorm.DB) error {
	if err := db.AutoMigrate(&TransactionRecord{}); err != nil {
		return err
	}
	return db.Exec(`
		UPDATE transaction_records
		SET amount_cents = CAST(ROUND(amount * 100.0) AS INTEGER),
		    balance_after_cents = CAST(ROUND(balance_after * 100.0) AS INTEGER)
		WHERE amount_cents = 0 AND (amount <> 0 OR balance_after <> 0)
	`).Error
}

func migrateOfferPricingAndQuota(db *gorm.DB) error {
	for _, column := range []struct {
		table string
		name  string
		ddl   string
	}{
		{"product_offers", "minimum_retail_price_cents", `ALTER TABLE product_offers ADD COLUMN minimum_retail_price_cents INTEGER NOT NULL DEFAULT 0`},
		{"product_offers", "quota", `ALTER TABLE product_offers ADD COLUMN quota INTEGER NOT NULL DEFAULT 0`},
		{"product_offers", "reserved_quantity", `ALTER TABLE product_offers ADD COLUMN reserved_quantity INTEGER NOT NULL DEFAULT 0`},
		{"seller_listings", "retail_price_cents", `ALTER TABLE seller_listings ADD COLUMN retail_price_cents INTEGER NOT NULL DEFAULT 0`},
		{"order_items", "offer_reserved_quantity", `ALTER TABLE order_items ADD COLUMN offer_reserved_quantity INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := addColumnIfMissing(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

func migrateMigrationAudit(db *gorm.DB) error {
	return db.AutoMigrate(&MigrationAuditIssue{})
}

func migratePaymentSuccessTimestamps(db *gorm.DB) error {
	if err := db.AutoMigrate(&Payment{}); err != nil {
		return err
	}
	return db.Exec("UPDATE payments SET paid_at = created_at WHERE status IN ('paid', 'refunded') AND paid_at IS NULL").Error
}

func migratePOSHolds(db *gorm.DB) error {
	return db.AutoMigrate(&POSHold{})
}

// migrateStrictOwnershipGuards adds database-level checks for relationships
// whose ownership cannot be represented by a single foreign key in the
// legacy schema. Existing rows must pass the migration-audit command before
// strict mode is enabled; the guards then prevent new drift. Distributed
// seller listings intentionally remain exempt from the
// supplier scenic-area check because their Product row belongs to the seller
// while fulfillment ownership is stored in the explicit snapshot columns.
func migrateStrictOwnershipGuards(db *gorm.DB) error {
	checks := []struct {
		name string
		sql  string
	}{
		{"checkpoints_owner_insert", `CREATE TRIGGER IF NOT EXISTS checkpoints_owner_insert BEFORE INSERT ON check_points
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas WHERE id = NEW.scenic_area_id AND tenant_id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'checkpoint scenic area ownership mismatch'); END`},
		{"checkpoints_owner_update", `CREATE TRIGGER IF NOT EXISTS checkpoints_owner_update BEFORE UPDATE OF tenant_id, scenic_area_id ON check_points
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas WHERE id = NEW.scenic_area_id AND tenant_id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'checkpoint scenic area ownership mismatch'); END`},
		{"devices_owner_insert", `CREATE TRIGGER IF NOT EXISTS devices_owner_insert BEFORE INSERT ON devices
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas WHERE id = NEW.scenic_area_id AND tenant_id = NEW.tenant_id)
			   OR (NEW.check_point_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM check_points WHERE id = NEW.check_point_id AND tenant_id = NEW.tenant_id AND scenic_area_id = NEW.scenic_area_id))
			BEGIN SELECT RAISE(ABORT, 'device ownership mismatch'); END`},
		{"devices_owner_update", `CREATE TRIGGER IF NOT EXISTS devices_owner_update BEFORE UPDATE OF tenant_id, scenic_area_id, check_point_id ON devices
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas WHERE id = NEW.scenic_area_id AND tenant_id = NEW.tenant_id)
			   OR (NEW.check_point_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM check_points WHERE id = NEW.check_point_id AND tenant_id = NEW.tenant_id AND scenic_area_id = NEW.scenic_area_id))
			BEGIN SELECT RAISE(ABORT, 'device ownership mismatch'); END`},
		{"fulfillment_orders_owner_insert", `CREATE TRIGGER IF NOT EXISTS fulfillment_orders_owner_insert BEFORE INSERT ON fulfillment_orders
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM orders WHERE id = NEW.sales_order_id AND tenant_id = NEW.sales_tenant_id)
			BEGIN SELECT RAISE(ABORT, 'fulfillment order ownership mismatch'); END`},
		{"fulfillment_orders_owner_update", `CREATE TRIGGER IF NOT EXISTS fulfillment_orders_owner_update BEFORE UPDATE OF sales_order_id, sales_tenant_id, scenic_area_id ON fulfillment_orders
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM orders WHERE id = NEW.sales_order_id AND tenant_id = NEW.sales_tenant_id)
			BEGIN SELECT RAISE(ABORT, 'fulfillment order ownership mismatch'); END`},
		{"tickets_owner_insert", `CREATE TRIGGER IF NOT EXISTS tickets_owner_insert BEFORE INSERT ON tickets
			WHEN NEW.fulfillment_scenic_area_id = 0 OR (NEW.order_id != 0 AND NOT EXISTS (SELECT 1 FROM order_items oi JOIN orders o ON o.id = oi.order_id WHERE oi.id = NEW.order_item_id AND oi.order_id = NEW.order_id AND o.tenant_id = NEW.tenant_id))
			BEGIN SELECT RAISE(ABORT, 'ticket ownership mismatch'); END`},
		{"tickets_owner_update", `CREATE TRIGGER IF NOT EXISTS tickets_owner_update BEFORE UPDATE OF order_item_id, order_id, tenant_id, fulfillment_scenic_area_id ON tickets
			WHEN NEW.fulfillment_scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM order_items oi JOIN orders o ON o.id = oi.order_id WHERE oi.id = NEW.order_item_id AND oi.order_id = NEW.order_id AND o.tenant_id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'ticket ownership mismatch'); END`},
		{"entitlements_owner_insert", `CREATE TRIGGER IF NOT EXISTS entitlements_owner_insert BEFORE INSERT ON ticket_entitlements
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM fulfillment_orders f WHERE f.id = NEW.fulfillment_order_id AND f.supplier_tenant_id = NEW.supplier_tenant_id AND f.scenic_area_id = NEW.scenic_area_id)
			   OR NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id = NEW.ticket_id AND t.ticket_code = NEW.ticket_code AND t.fulfillment_scenic_area_id = NEW.scenic_area_id)
			BEGIN SELECT RAISE(ABORT, 'ticket entitlement ownership mismatch'); END`},
		{"entitlements_owner_update", `CREATE TRIGGER IF NOT EXISTS entitlements_owner_update BEFORE UPDATE OF fulfillment_order_id, ticket_id, ticket_code, supplier_tenant_id, scenic_area_id ON ticket_entitlements
			WHEN NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM fulfillment_orders f WHERE f.id = NEW.fulfillment_order_id AND f.supplier_tenant_id = NEW.supplier_tenant_id AND f.scenic_area_id = NEW.scenic_area_id)
			   OR NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id = NEW.ticket_id AND t.ticket_code = NEW.ticket_code AND t.fulfillment_scenic_area_id = NEW.scenic_area_id)
			BEGIN SELECT RAISE(ABORT, 'ticket entitlement ownership mismatch'); END`},
	}
	for _, check := range checks {
		if err := db.Exec(check.sql).Error; err != nil {
			return fmt.Errorf("create ownership trigger %s: %w", check.name, err)
		}
	}
	return nil
}

func migratePaymentCentFacts(db *gorm.DB) error {
	if err := db.AutoMigrate(&Payment{}, &Refund{}); err != nil {
		return err
	}
	if err := db.Exec(`
		UPDATE payments
		SET amount_cents = CAST(ROUND(amount * 100.0) AS INTEGER),
		    refunded_amount_cents = CAST(ROUND(refunded_amount * 100.0) AS INTEGER)
		WHERE amount_cents = 0 AND (amount != 0 OR refunded_amount != 0)
	`).Error; err != nil {
		return err
	}
	return db.Exec(`
		UPDATE refunds
		SET amount_cents = CAST(ROUND(amount * 100.0) AS INTEGER)
		WHERE amount_cents = 0 AND amount != 0
	`).Error
}

func migrateAfterSalesAndWorkflows(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&AfterSaleRequest{}, &AfterSaleEvent{}, &HardwareCommand{}, &HardwareEvent{},
		&ChannelReservation{}, &FinancialDocument{},
	); err != nil {
		return err
	}
	for _, column := range []struct {
		table string
		name  string
		ddl   string
	}{
		{"orders", "visitor_id", `ALTER TABLE orders ADD COLUMN visitor_id TEXT`},
		{"orders", "visitor_region", `ALTER TABLE orders ADD COLUMN visitor_region TEXT`},
		{"orders", "channel_reservation_id", `ALTER TABLE orders ADD COLUMN channel_reservation_id INTEGER`},
		{"order_items", "stock_slot", `ALTER TABLE order_items ADD COLUMN stock_slot TEXT`},
		{"order_items", "visitor_name", `ALTER TABLE order_items ADD COLUMN visitor_name TEXT`},
		{"order_items", "visitor_phone", `ALTER TABLE order_items ADD COLUMN visitor_phone TEXT`},
		{"order_items", "visitor_id", `ALTER TABLE order_items ADD COLUMN visitor_id TEXT`},
		{"order_items", "visitor_region", `ALTER TABLE order_items ADD COLUMN visitor_region TEXT`},
		{"product_inventories", "stock_slot", `ALTER TABLE product_inventories ADD COLUMN stock_slot TEXT`},
		{"print_jobs", "after_sale_request_no", `ALTER TABLE print_jobs ADD COLUMN after_sale_request_no TEXT`},
	} {
		if err := addColumnIfMissing(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	// The pre-v24 index did not include a slot, so it would reject two valid
	// reservations for different sessions on the same date. Rebuild it using
	// the normalized empty-string slot for legacy rows.
	if err := db.Exec("DROP INDEX IF EXISTS idx_product_stock_date").Error; err != nil {
		return err
	}
	if err := db.Exec("UPDATE product_inventories SET stock_slot = '' WHERE stock_slot IS NULL").Error; err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_product_stock_slot ON product_inventories(tenant_id, product_id, stock_date, stock_slot)").Error
}

func addColumnIfMissing(db *gorm.DB, table, column, ddl string) error {
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Exec(ddl).Error
}

func migrateChannelReservationHardening(db *gorm.DB) error {
	if err := addColumnIfMissing(db, "orders", "channel_reservation_id", `ALTER TABLE orders ADD COLUMN channel_reservation_id INTEGER`); err != nil {
		return err
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_channel_reservation_external").Error; err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_reservation_external ON channel_reservations(channel_account_id, external_no)").Error
}

func migrateFinancialDocumentIdempotency(db *gorm.DB) error {
	if err := addColumnIfMissing(db, "financial_documents", "idempotency_key", `ALTER TABLE financial_documents ADD COLUMN idempotency_key TEXT`); err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_financial_document_idempotency ON financial_documents(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''").Error
}

func migrateSessionTokenVersions(db *gorm.DB) error {
	for _, column := range []struct {
		table string
		name  string
		ddl   string
	}{
		{"users", "token_version", `ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 1`},
		{"staffs", "token_version", `ALTER TABLE staffs ADD COLUMN token_version INTEGER NOT NULL DEFAULT 1`},
		{"platform_users", "token_version", `ALTER TABLE platform_users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 1`},
	} {
		if err := addColumnIfMissing(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

func migrateSettlementPeriodIdempotency(db *gorm.DB) error {
	if err := addColumnIfMissing(db, "settlement_statements", "idempotency_key", `ALTER TABLE settlement_statements ADD COLUMN idempotency_key TEXT`); err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_statement_idempotency ON settlement_statements(idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''").Error
}

func migrateChannelRequestPolicyFacts(db *gorm.DB) error {
	for _, column := range []struct {
		table string
		name  string
		ddl   string
	}{
		{"channel_accounts", "allowed_ips_json", `ALTER TABLE channel_accounts ADD COLUMN allowed_ips_json TEXT`},
		{"channel_requests", "response_status", `ALTER TABLE channel_requests ADD COLUMN response_status INTEGER NOT NULL DEFAULT 200`},
		{"channel_requests", "remote_ip", `ALTER TABLE channel_requests ADD COLUMN remote_ip TEXT`},
	} {
		if err := addColumnIfMissing(db, column.table, column.name, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

func migrateCheckInOwnership(db *gorm.DB) error {
	when := `NEW.result = 'success' AND (NEW.scenic_area_id = 0 OR NOT EXISTS (
		SELECT 1 FROM tickets t
		JOIN check_points c ON c.id = NEW.check_point_id
		JOIN devices d ON d.id = NEW.device_id
		WHERE t.id = NEW.ticket_id AND t.ticket_code = NEW.ticket_code
		AND t.fulfillment_tenant_id = NEW.tenant_id AND t.fulfillment_scenic_area_id = NEW.scenic_area_id
		AND c.tenant_id = NEW.tenant_id AND c.scenic_area_id = NEW.scenic_area_id
		AND d.tenant_id = NEW.tenant_id AND d.scenic_area_id = NEW.scenic_area_id AND d.check_point_id = c.id
	))`
	for _, operation := range []string{"INSERT", "UPDATE"} {
		statement := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS checkin_owner_%s BEFORE %s ON check_in_records WHEN %s BEGIN SELECT RAISE(ABORT, 'check-in ownership mismatch'); END", strings.ToLower(operation), operation, when)
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateStaffResourceScopes(db *gorm.DB) error {
	if err := db.AutoMigrate(&StaffResourceScope{}); err != nil {
		return err
	}
	statements := []string{
		`INSERT OR IGNORE INTO staff_resource_scopes (created_at, updated_at, tenant_id, staff_id, resource_type, resource_id)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, staff.tenant_id, staff.id, 'scenic_area', scenic.id FROM staffs staff JOIN scenic_areas scenic ON scenic.tenant_id = staff.tenant_id`,
		`INSERT OR IGNORE INTO staff_resource_scopes (created_at, updated_at, tenant_id, staff_id, resource_type, resource_id)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, staff.tenant_id, staff.id, 'checkpoint', checkpoint.id FROM staffs staff JOIN check_points checkpoint ON checkpoint.tenant_id = staff.tenant_id`,
		`INSERT OR IGNORE INTO staff_resource_scopes (created_at, updated_at, tenant_id, staff_id, resource_type, resource_id)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, staff.tenant_id, staff.id, 'device', device.id FROM staffs staff JOIN devices device ON device.tenant_id = staff.tenant_id`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateCompositeOwnershipAndPOSFacts(db *gorm.DB) error {
	if err := db.AutoMigrate(&Payment{}, &PrintJob{}); err != nil {
		return err
	}
	checks := []struct {
		name  string
		table string
		when  string
		msg   string
	}{
		{"checkpoint_owner", "check_points", "NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id)", "checkpoint scenic area ownership mismatch"},
		{"device_owner", "devices", "NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM check_points c WHERE c.id = NEW.check_point_id AND c.tenant_id = NEW.tenant_id AND c.scenic_area_id = NEW.scenic_area_id)", "device checkpoint ownership mismatch"},
		{"product_owner", "products", "NEW.source_product_id = 0 AND (NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id))", "product scenic area ownership mismatch"},
		{"inventory_owner", "product_inventories", "NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.scenic_area_id = NEW.scenic_area_id)", "inventory product ownership mismatch"},
		{"order_item_fulfillment", "order_items", "NEW.fulfillment_scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.fulfillment_product_id AND p.tenant_id = NEW.fulfillment_tenant_id AND p.scenic_area_id = NEW.fulfillment_scenic_area_id)", "order item fulfillment ownership mismatch"},
		{"ticket_fulfillment", "tickets", "NEW.fulfillment_scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM order_items i JOIN orders o ON o.id = i.order_id WHERE i.id = NEW.order_item_id AND i.order_id = NEW.order_id AND o.tenant_id = NEW.tenant_id AND i.fulfillment_tenant_id = NEW.fulfillment_tenant_id AND i.fulfillment_scenic_area_id = NEW.fulfillment_scenic_area_id AND i.fulfillment_order_id = NEW.fulfillment_order_id)", "ticket fulfillment ownership mismatch"},
		{"entitlement_fulfillment", "ticket_entitlements", "NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM tickets t WHERE t.id = NEW.ticket_id AND t.fulfillment_order_id = NEW.fulfillment_order_id AND t.tenant_id = NEW.sales_tenant_id AND t.fulfillment_tenant_id = NEW.supplier_tenant_id AND t.fulfillment_scenic_area_id = NEW.scenic_area_id)", "entitlement fulfillment ownership mismatch"},
	}
	for _, check := range checks {
		for _, operation := range []string{"INSERT", "UPDATE"} {
			name := fmt.Sprintf("%s_%s", check.name, strings.ToLower(operation))
			statement := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS %s BEFORE %s ON %s WHEN %s BEGIN SELECT RAISE(ABORT, '%s'); END", name, operation, check.table, check.when, check.msg)
			if err := db.Exec(statement).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateDistributionUniqueness(db *gorm.DB) error {
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_distribution_pair ON distributor_relationships(agent_tenant_id, supplier_tenant_id)").Error; err != nil {
		return err
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_capital_account_pair ON capital_accounts(owner_tenant_id, manager_tenant_id)").Error
}

func migrateFulfillmentOwnership(db *gorm.DB) error {
	if err := db.AutoMigrate(&Product{}, &OrderItem{}, &Ticket{}); err != nil {
		return err
	}

	// Existing distributed listings already carry Source* fields. Copy those
	// values into the server-controlled fulfillment snapshot before new code
	// starts relying on it. Direct products point to themselves.
	if err := db.Exec(`
		UPDATE products
		SET fulfillment_product_id = CASE
			WHEN source_product_id > 0 THEN source_product_id
			ELSE id
		END,
		fulfillment_tenant_id = CASE
			WHEN source_tenant_id > 0 THEN source_tenant_id
			ELSE tenant_id
		END
		WHERE fulfillment_product_id = 0 OR fulfillment_tenant_id = 0
	`).Error; err != nil {
		return err
	}

	if err := db.Exec(`
		UPDATE order_items
		SET fulfillment_product_id = COALESCE(NULLIF((
			SELECT fulfillment_product_id FROM products WHERE products.id = order_items.product_id
		), 0), product_id),
		fulfillment_tenant_id = COALESCE(NULLIF((
			SELECT fulfillment_tenant_id FROM products WHERE products.id = order_items.product_id
		), 0), (
			SELECT tenant_id FROM products WHERE products.id = order_items.product_id
		))
		WHERE fulfillment_product_id = 0 OR fulfillment_tenant_id = 0
	`).Error; err != nil {
		return err
	}

	return db.Exec(`
		UPDATE tickets
		SET fulfillment_product_id = (
			SELECT fulfillment_product_id FROM order_items WHERE order_items.id = tickets.order_item_id
		),
		fulfillment_tenant_id = (
			SELECT fulfillment_tenant_id FROM order_items WHERE order_items.id = tickets.order_item_id
		)
		WHERE fulfillment_product_id = 0 OR fulfillment_tenant_id = 0
	`).Error
}

func migrateOrderReservationExpiry(db *gorm.DB) error {
	if err := db.AutoMigrate(&Order{}); err != nil {
		return err
	}
	// Existing unpaid orders did not have a deadline. Give them the same
	// bounded reservation window from their creation time so they cannot hold
	// inventory or distributor funds forever after the upgrade.
	return db.Exec(`
		UPDATE orders
		SET expires_at = datetime(created_at, '+15 minutes')
		WHERE status = 'unpaid' AND expires_at IS NULL
	`).Error
}

func migratePaymentCallbackCredentials(db *gorm.DB) error {
	return db.AutoMigrate(&PaymentConfig{})
}

func migratePaymentReconciliationTasks(db *gorm.DB) error {
	return db.AutoMigrate(&PaymentReconciliationTask{})
}

func migrateTenantStatusAndCapabilities(db *gorm.DB) error {
	if err := db.AutoMigrate(&Tenant{}, &TenantCapability{}); err != nil {
		return err
	}
	return db.Exec("UPDATE tenants SET status = 'active' WHERE status IS NULL OR status = ''").Error
}

func migrateScenicAreaBoundary(db *gorm.DB) error {
	if err := db.AutoMigrate(&ScenicArea{}, &CheckPoint{}, &Device{}, &Product{}, &ProductInventory{}, &OrderItem{}, &Ticket{}); err != nil {
		return err
	}
	var tenants []Tenant
	if err := db.Find(&tenants).Error; err != nil {
		return err
	}
	for i := range tenants {
		var area ScenicArea
		err := db.Where("tenant_id = ? AND code = ?", tenants[i].ID, "DEFAULT").First(&area).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			area = ScenicArea{TenantID: tenants[i].ID, Code: "DEFAULT", Name: tenants[i].Name, Status: "active"}
			if err := db.Create(&area).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := db.Model(&CheckPoint{}).Where("tenant_id = ? AND scenic_area_id = 0", tenants[i].ID).Update("scenic_area_id", area.ID).Error; err != nil {
			return err
		}
		if err := db.Model(&Product{}).Where("tenant_id = ? AND scenic_area_id = 0 AND source_product_id = 0", tenants[i].ID).Update("scenic_area_id", area.ID).Error; err != nil {
			return err
		}
	}
	if err := db.Exec(`
		UPDATE products
		SET fulfillment_scenic_area_id = COALESCE((
			SELECT scenic_area_id FROM products AS source
			WHERE source.id = products.fulfillment_product_id
		), scenic_area_id)
		WHERE fulfillment_scenic_area_id = 0
	`).Error; err != nil {
		return err
	}
	if err := db.Exec(`
		UPDATE product_inventories
		SET scenic_area_id = COALESCE((SELECT scenic_area_id FROM products WHERE products.id = product_inventories.product_id), 0)
		WHERE scenic_area_id = 0
	`).Error; err != nil {
		return err
	}
	if err := db.Exec(`
		UPDATE devices
		SET scenic_area_id = COALESCE((SELECT scenic_area_id FROM check_points WHERE check_points.id = devices.check_point_id), 0)
		WHERE scenic_area_id = 0
	`).Error; err != nil {
		return err
	}
	if err := db.Exec(`
		UPDATE order_items
		SET fulfillment_scenic_area_id = COALESCE((SELECT fulfillment_scenic_area_id FROM products WHERE products.id = order_items.fulfillment_product_id), 0)
		WHERE fulfillment_scenic_area_id = 0
	`).Error; err != nil {
		return err
	}
	return db.Exec(`
		UPDATE tickets
		SET scenic_area_id = COALESCE((SELECT fulfillment_scenic_area_id FROM order_items WHERE order_items.id = tickets.order_item_id), 0),
			fulfillment_scenic_area_id = COALESCE((SELECT fulfillment_scenic_area_id FROM order_items WHERE order_items.id = tickets.order_item_id), 0)
		WHERE scenic_area_id = 0 OR fulfillment_scenic_area_id = 0
	`).Error
}

func migrateCashRefundRecords(db *gorm.DB) error {
	return db.AutoMigrate(&Payment{}, &Refund{})
}

func migrateAuditLog(db *gorm.DB) error {
	return db.AutoMigrate(&AuditLog{})
}

func migratePlatformIdentity(db *gorm.DB) error {
	return db.AutoMigrate(&PlatformUser{})
}

func migrateProductOffers(db *gorm.DB) error {
	if err := db.AutoMigrate(&ProductOffer{}, &SellerListing{}, &Product{}, &OrderItem{}); err != nil {
		return err
	}
	var listings []Product
	if err := db.Where("source_product_id > 0 AND product_offer_id = 0").Find(&listings).Error; err != nil {
		return err
	}
	for i := range listings {
		var source Product
		if err := db.Unscoped().Where("id = ? AND tenant_id = ?", listings[i].SourceProductID, listings[i].SourceTenantID).First(&source).Error; err != nil {
			continue
		}
		var offer ProductOffer
		err := db.Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ?", source.TenantID, listings[i].TenantID, source.ID).First(&offer).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			offer = ProductOffer{
				SupplierTenantID: source.TenantID, DistributorTenantID: listings[i].TenantID,
				SourceProductID: source.ID, FulfillmentScenicAreaID: source.ScenicAreaID,
				SettlementPrice: source.SettlementPrice, Status: "suspended",
			}
			if err := db.Create(&offer).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := db.Unscoped().Model(&listings[i]).Update("product_offer_id", offer.ID).Error; err != nil {
			return err
		}
		var count int64
		if err := db.Model(&SellerListing{}).Where("product_id = ?", listings[i].ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := db.Create(&SellerListing{
				SellerTenantID: listings[i].TenantID, ProductOfferID: offer.ID, ProductID: listings[i].ID,
				Name: listings[i].Name, RetailPrice: listings[i].Price, Status: listings[i].Status,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateFulfillmentProjections(db *gorm.DB) error {
	if err := db.AutoMigrate(&FulfillmentOrder{}, &TicketEntitlement{}, &OrderItem{}, &Ticket{}); err != nil {
		return err
	}
	var orders []Order
	if err := db.Preload("Items.Tickets").Find(&orders).Error; err != nil {
		return err
	}
	for i := range orders {
		sequence := 0
		for itemIndex := range orders[i].Items {
			item := &orders[i].Items[itemIndex]
			var fulfillment FulfillmentOrder
			err := db.Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", orders[i].ID, item.FulfillmentTenantID, item.FulfillmentScenicAreaID).First(&fulfillment).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				sequence++
				fulfillment = FulfillmentOrder{
					FulfillmentNo: fmt.Sprintf("FUL-MIG-%d-%d", orders[i].ID, sequence),
					SalesOrderID:  orders[i].ID, SalesOrderNo: orders[i].OrderNo, SalesTenantID: orders[i].TenantID,
					SupplierTenantID: item.FulfillmentTenantID, ScenicAreaID: item.FulfillmentScenicAreaID,
					SettlementAmount: item.SettlementPrice * float64(item.Quantity), Status: fulfillmentStatusForOrder(orders[i].Status),
				}
				if err := db.Create(&fulfillment).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				amount := item.SettlementPrice * float64(item.Quantity)
				if err := db.Model(&fulfillment).Update("settlement_amount", fulfillment.SettlementAmount+amount).Error; err != nil {
					return err
				}
				fulfillment.SettlementAmount += amount
			}
			if err := db.Model(item).Update("fulfillment_order_id", fulfillment.ID).Error; err != nil {
				return err
			}
			for ticketIndex := range item.Tickets {
				ticket := &item.Tickets[ticketIndex]
				if err := db.Model(ticket).Update("fulfillment_order_id", fulfillment.ID).Error; err != nil {
					return err
				}
				var count int64
				if err := db.Model(&TicketEntitlement{}).Where("ticket_id = ?", ticket.ID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					if err := db.Create(&TicketEntitlement{
						FulfillmentOrderID: fulfillment.ID, TicketID: ticket.ID, TicketCode: ticket.TicketCode,
						SalesTenantID: ticket.TenantID, SupplierTenantID: ticket.FulfillmentTenantID,
						ScenicAreaID: ticket.FulfillmentScenicAreaID, Status: entitlementStatusForTicket(ticket.Status), RuleSnapshot: ticket.RuleSnapshot,
					}).Error; err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func migrateLedger(db *gorm.DB) error {
	if err := db.AutoMigrate(&LedgerEntry{}, &DigitalRefundTask{}); err != nil {
		return err
	}
	// Seed a single opening fact for legacy account balances so the new ledger
	// starts from a known cent-based point without rewriting historical rows.
	var accounts []CapitalAccount
	if err := db.Find(&accounts).Error; err != nil {
		return err
	}
	for i := range accounts {
		var count int64
		if err := db.Model(&LedgerEntry{}).Where("account_id = ?", accounts[i].ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := db.Create(&LedgerEntry{
			AccountID: accounts[i].ID, OwnerTenantID: accounts[i].OwnerTenantID, ManagerTenantID: accounts[i].ManagerTenantID,
			EntryType: "opening_balance", AmountCents: legacyMoneyCents(accounts[i].Balance),
			BalanceCents: legacyMoneyCents(accounts[i].Balance), UsedCreditCents: legacyMoneyCents(accounts[i].UsedCredit),
			FrozenCents: legacyMoneyCents(accounts[i].FrozenAmount), IdempotencyKey: fmt.Sprintf("opening:%d", accounts[i].ID),
			Memo: "legacy capital account opening balance",
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func legacyMoneyCents(value float64) int64 {
	return int64(math.Round(value * 100))
}

func migrateChannels(db *gorm.DB) error {
	return db.AutoMigrate(&ChannelAccount{}, &ChannelProductMapping{}, &ChannelRequest{}, &Order{})
}

func migrateTravelTeams(db *gorm.DB) error {
	return db.AutoMigrate(&TravelContract{}, &TravelAgent{}, &TourGuide{}, &TravelVehicle{}, &TourGroup{}, &TourGroupMember{}, &TourEntryBatch{})
}

func migratePOSOperations(db *gorm.DB) error {
	return db.AutoMigrate(&POSShift{}, &PrintJob{}, &DeviceAlert{})
}

func migrateProductRevisions(db *gorm.DB) error {
	if err := db.AutoMigrate(&ProductRevision{}, &Product{}, &ProductOffer{}, &OrderItem{}, &Ticket{}, &FulfillmentOrder{}); err != nil {
		return err
	}
	var products []Product
	if err := db.Preload("Rule").Where("current_revision_id = 0").Find(&products).Error; err != nil {
		return err
	}
	for i := range products {
		var revision ProductRevision
		err := db.Where("product_id = ?", products[i].ID).Order("version DESC").First(&revision).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			snapshot, marshalErr := json.Marshal(products[i].Rule)
			if marshalErr != nil {
				return marshalErr
			}
			revision = ProductRevision{ProductID: products[i].ID, TenantID: products[i].TenantID, ScenicAreaID: products[i].ScenicAreaID, Version: 1, Status: "active", PriceCents: legacyMoneyCents(products[i].Price), SettlementCents: legacyMoneyCents(products[i].SettlementPrice), SnapshotJSON: string(snapshot), EffectiveFrom: products[i].CreatedAt}
			if err := db.Create(&revision).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := db.Model(&Product{}).Where("id = ?", products[i].ID).Update("current_revision_id", revision.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateSettlements(db *gorm.DB) error {
	return db.AutoMigrate(&SettlementStatement{}, &SettlementLine{}, &FulfillmentOrder{})
}

func migratePhaseZeroHardening(db *gorm.DB) error {
	columns := []struct {
		model interface{}
		name  string
	}{
		{&ProductOffer{}, "CommissionBPS"}, {&OrderItem{}, "CommissionBPS"},
		{&SettlementStatement{}, "SupplierConfirmedAt"}, {&SettlementStatement{}, "DistributorConfirmedAt"},
		{&SettlementStatement{}, "PaymentProof"}, {&SettlementStatement{}, "DisputeReason"},
	}
	for _, column := range columns {
		if !db.Migrator().HasColumn(column.model, column.name) {
			if err := db.Migrator().AddColumn(column.model, column.name); err != nil {
				return err
			}
		}
	}
	capabilityInferences := []string{
		`INSERT OR IGNORE INTO tenant_capabilities (created_at, updated_at, tenant_id, capability, status)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, tenants.id, 'supplier', 'active' FROM tenants
		 WHERE EXISTS (SELECT 1 FROM products WHERE products.tenant_id = tenants.id AND products.source_product_id = 0)
		    OR EXISTS (SELECT 1 FROM check_points WHERE check_points.tenant_id = tenants.id)
		    OR EXISTS (SELECT 1 FROM devices WHERE devices.tenant_id = tenants.id)`,
		`INSERT OR IGNORE INTO tenant_capabilities (created_at, updated_at, tenant_id, capability, status)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, tenants.id, 'distributor', 'active' FROM tenants
		 WHERE EXISTS (SELECT 1 FROM distributor_relationships WHERE distributor_relationships.agent_tenant_id = tenants.id)
		    OR EXISTS (SELECT 1 FROM seller_listings WHERE seller_listings.seller_tenant_id = tenants.id)
		    OR EXISTS (SELECT 1 FROM capital_accounts WHERE capital_accounts.owner_tenant_id = tenants.id)`,
		`INSERT OR IGNORE INTO tenant_capabilities (created_at, updated_at, tenant_id, capability, status)
		 SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, tenants.id, 'travel_agency', 'active' FROM tenants
		 WHERE EXISTS (SELECT 1 FROM travel_contracts WHERE travel_contracts.travel_tenant_id = tenants.id)
		    OR EXISTS (SELECT 1 FROM tour_groups WHERE tour_groups.tenant_id = tenants.id)`,
	}
	for _, statement := range capabilityInferences {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	backfills := []string{
		`UPDATE check_points SET scenic_area_id = (SELECT id FROM scenic_areas WHERE scenic_areas.tenant_id = check_points.tenant_id AND scenic_areas.status = 'active' ORDER BY id LIMIT 1) WHERE scenic_area_id = 0`,
		`UPDATE devices SET scenic_area_id = COALESCE((SELECT scenic_area_id FROM check_points WHERE check_points.id = devices.check_point_id), (SELECT id FROM scenic_areas WHERE scenic_areas.tenant_id = devices.tenant_id AND scenic_areas.status = 'active' ORDER BY id LIMIT 1)) WHERE scenic_area_id = 0`,
		`UPDATE products SET scenic_area_id = (SELECT id FROM scenic_areas WHERE scenic_areas.tenant_id = products.tenant_id AND scenic_areas.status = 'active' ORDER BY id LIMIT 1) WHERE scenic_area_id = 0 AND source_product_id = 0`,
		`UPDATE order_items SET fulfillment_scenic_area_id = (SELECT scenic_area_id FROM products WHERE products.id = order_items.fulfillment_product_id) WHERE fulfillment_scenic_area_id = 0`,
		`UPDATE tickets SET scenic_area_id = (SELECT fulfillment_scenic_area_id FROM order_items WHERE order_items.id = tickets.order_item_id), fulfillment_scenic_area_id = (SELECT fulfillment_scenic_area_id FROM order_items WHERE order_items.id = tickets.order_item_id) WHERE scenic_area_id = 0 OR fulfillment_scenic_area_id = 0`,
		`UPDATE fulfillment_orders SET scenic_area_id = (SELECT fulfillment_scenic_area_id FROM order_items WHERE order_items.fulfillment_order_id = fulfillment_orders.id LIMIT 1) WHERE scenic_area_id = 0`,
		`UPDATE ticket_entitlements SET scenic_area_id = (SELECT fulfillment_scenic_area_id FROM tickets WHERE tickets.id = ticket_entitlements.ticket_id) WHERE scenic_area_id = 0`,
		`UPDATE fulfillment_orders SET settlement_amount = COALESCE((SELECT SUM(order_items.settlement_price * order_items.quantity) FROM order_items WHERE order_items.fulfillment_order_id = fulfillment_orders.id), settlement_amount)`,
		`UPDATE product_offers SET status = 'suspended' WHERE product_revision_id = 0 OR fulfillment_scenic_area_id = 0 OR settlement_price <= 0`,
	}
	for _, statement := range backfills {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	zeroChecks := []struct {
		table string
		where string
	}{
		{"check_points", "scenic_area_id = 0"}, {"devices", "scenic_area_id = 0"},
		{"products", "source_product_id = 0 AND scenic_area_id = 0"}, {"order_items", "fulfillment_scenic_area_id = 0"},
		{"tickets", "fulfillment_scenic_area_id = 0"}, {"fulfillment_orders", "scenic_area_id = 0"},
		{"ticket_entitlements", "scenic_area_id = 0"},
	}
	for _, check := range zeroChecks {
		var count int64
		if err := db.Table(check.table).Where(check.where).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("legacy migration left %d zero scenic-area rows in %s", count, check.table)
		}
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_channel_code").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_accounts_code_global ON channel_accounts(code)").Error; err != nil {
		return fmt.Errorf("duplicate global channel codes must be resolved before migration: %w", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_fulfillment_unique ON settlement_lines(fulfillment_order_id)").Error; err != nil {
		return fmt.Errorf("duplicate fulfillment settlement lines must be resolved before migration: %w", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_product_revision_unique ON product_revisions(product_id, version)").Error; err != nil {
		return err
	}
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS checkpoints_nonzero_scenic_insert BEFORE INSERT ON check_points WHEN NEW.scenic_area_id = 0 BEGIN SELECT RAISE(ABORT, 'checkpoint scenic area is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS devices_nonzero_scenic_insert BEFORE INSERT ON devices WHEN NEW.scenic_area_id = 0 BEGIN SELECT RAISE(ABORT, 'device scenic area is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS tickets_nonzero_scenic_insert BEFORE INSERT ON tickets WHEN NEW.fulfillment_scenic_area_id = 0 BEGIN SELECT RAISE(ABORT, 'ticket scenic area is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS entitlements_nonzero_scenic_insert BEFORE INSERT ON ticket_entitlements WHEN NEW.scenic_area_id = 0 BEGIN SELECT RAISE(ABORT, 'entitlement scenic area is required'); END`,
	}
	for _, statement := range triggers {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func fulfillmentStatusForOrder(status string) string {
	switch status {
	case "paid", "partial_refunded":
		return "paid"
	case "completed":
		return "fulfilled"
	case "cancelled", "refunded":
		return "cancelled"
	default:
		return "reserved"
	}
}

func entitlementStatusForTicket(status string) string {
	switch status {
	case "used":
		return "used"
	case "active":
		return "active"
	case "refunded":
		return "refunded"
	case "void":
		return "void"
	default:
		return "issued"
	}
}

func migrateInitialSchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&Tenant{}, &TenantCapability{}, &ScenicArea{}, &PlatformUser{}, &User{}, &Staff{}, &CheckPoint{}, &Device{},
		&TicketRule{}, &RuleGroup{}, &RuleItem{}, &Product{}, &ProductOffer{}, &SellerListing{}, &ProductInventory{},
		&Order{}, &OrderItem{}, &Ticket{}, &FulfillmentOrder{}, &TicketEntitlement{}, &CheckInRecord{},
		&OrderVisitor{},
		&DistributorRelationship{}, &CapitalAccount{}, &TransactionRecord{}, &LedgerEntry{}, &ProductRevision{},
		&Policy{}, &PaymentConfig{}, &Payment{}, &Refund{}, &PaymentReconciliationTask{}, &AuditLog{}, &OTANonce{},
		&DigitalRefundTask{}, &ChannelAccount{}, &ChannelProductMapping{}, &ChannelRequest{},
		&TravelContract{}, &TravelAgent{}, &TourGuide{}, &TravelVehicle{}, &TourGroup{}, &TourGroupMember{}, &TourEntryBatch{},
		&POSShift{}, &PrintJob{}, &DeviceAlert{}, &POSHold{}, &SettlementStatement{}, &SettlementLine{}, &StaffResourceScope{},
		&MigrationAuditIssue{},
	)
}
