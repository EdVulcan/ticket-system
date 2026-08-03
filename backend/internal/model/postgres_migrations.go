package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CurrentPostgresSchemaVersion = 63

// PostgreSQL starts from the current domain schema. The historical migrations
// contain SQLite trigger syntax and legacy backfills, so replaying them on a
// fresh PostgreSQL database would be incorrect. SQLite remains available for
// regression tests and any future, separately verified legacy import work.
func runPostgresMigrations(db *gorm.DB) error {
	hadSettlementSource := db.Migrator().HasColumn(&SettlementLine{}, "Source")
	models := []interface{}{
		&SchemaMigration{},
		&Tenant{}, &TenantCapability{}, &ScenicArea{}, &PlatformUser{}, &User{}, &Staff{},
		&CheckPoint{}, &Device{}, &TicketRule{}, &RuleGroup{}, &RuleItem{},
		&Product{}, &ProductRevision{}, &ProductOffer{}, &SellerListing{}, &ProductInventory{},
		&BundleProduct{}, &BundleVersion{}, &BundleComponent{},
		&Order{}, &OrderItem{}, &Ticket{}, &OrderVisitor{}, &FulfillmentOrder{}, &TicketEntitlement{}, &CheckInRecord{},
		&DistributorRelationship{}, &CapitalAccount{}, &TransactionRecord{}, &LedgerEntry{},
		&Policy{}, &PaymentConfig{}, &Payment{}, &Refund{}, &PaymentReconciliationTask{}, &DigitalRefundTask{},
		&AuditLog{}, &OTANonce{}, &FinancialDocument{},
		&ChannelAccount{}, &ChannelProductMapping{}, &ChannelRequest{}, &ChannelReservation{},
		&ChannelBillRecord{}, &ChannelReconciliation{}, &ChannelReconciliationLine{},
		&TravelContract{}, &TravelAgent{}, &TourGuide{}, &TravelVehicle{}, &TourGroup{}, &TourGroupMember{},
		&TourEntryBatch{}, &TourGroupConfirmation{}, &TourGroupMemberChange{}, &TeamSettlementStatement{}, &TeamSettlementAdjustment{},
		&POSShift{}, &POSShiftCorrection{}, &PrintJob{}, &DeviceAlert{}, &POSHold{}, &POSHoldLine{},
		&SettlementStatement{}, &SettlementLine{}, &SettlementAdjustment{}, &StaffResourceScope{},
		&AfterSaleRequest{}, &AfterSaleEvent{}, &HardwareCommand{}, &HardwareEvent{}, &DeviceRequestNonce{}, &DeviceVerification{}, &MigrationAuditIssue{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("create current PostgreSQL schema: %w", err)
	}
	if err := db.Model(&Product{}).Where("gate_voice_code IS NULL OR gate_voice_code = ?", "").Update("gate_voice_code", "welcome").Error; err != nil {
		return fmt.Errorf("backfill product gate voice: %w", err)
	}
	if err := db.Model(&TeamSettlementStatement{}).Where("sequence IS NULL OR sequence = 0").Updates(map[string]interface{}{"sequence": 1, "kind": "original"}).Error; err != nil {
		return fmt.Errorf("backfill team settlement sequence: %w", err)
	}
	if !hadSettlementSource {
		if err := db.Model(&SettlementLine{}).Where("source = ?", "verification").Update("source", "legacy_sale").Error; err != nil {
			return fmt.Errorf("mark legacy sale-based settlement lines: %w", err)
		}
	}
	if err := db.Exec(`
		UPDATE platform_users
		SET is_initial_admin = TRUE
		WHERE id = (
			SELECT MIN(id) FROM platform_users
			WHERE deleted_at IS NULL AND role = 'platform_admin'
		)
		AND NOT EXISTS (
			SELECT 1 FROM platform_users WHERE is_initial_admin = TRUE
		)
	`).Error; err != nil {
		return fmt.Errorf("backfill initial platform administrator: %w", err)
	}
	if err := applyPostgresIndexes(db); err != nil {
		return err
	}
	if err := applyPostgresOwnershipGuards(db); err != nil {
		return err
	}
	if err := applyPostgresBundleGuards(db); err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaMigration{
		Version:   CurrentPostgresSchemaVersion,
		Name:      "PostgreSQL current schema baseline",
		AppliedAt: time.Now(),
	}).Error
}

func applyPostgresIndexes(db *gorm.DB) error {
	for _, index := range []string{"idx_settlement_fulfillment_unique", "idx_settlement_lines_fulfillment_order_id", "idx_team_settlement_statements_group_id", "idx_team_settlement_group_id"} {
		if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			return fmt.Errorf("drop obsolete PostgreSQL index: %w", err)
		}
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_initial_admin ON users(tenant_id) WHERE is_initial_admin`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_users_initial_admin ON platform_users(is_initial_admin) WHERE is_initial_admin`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_refund_allocation_sequence ON refunds(parent_refund_id, allocation_seq) WHERE parent_refund_id != 0`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_bundle_component_product ON bundle_components(bundle_version_id, seller_product_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_entry_request ON tour_entry_batches(group_id, idempotency_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_settlement_group_sequence ON team_settlement_statements(group_id, sequence)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_idempotency ON payments(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_product_stock_slot ON product_inventories(tenant_id, product_id, stock_date, stock_slot)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_reservation_external ON channel_reservations(channel_account_id, external_no)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_financial_document_idempotency ON financial_documents(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_statement_idempotency ON settlement_statements(idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_distribution_pair ON distributor_relationships(agent_tenant_id, supplier_tenant_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_capital_account_pair ON capital_accounts(owner_tenant_id, manager_tenant_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_accounts_code_global ON channel_accounts(code)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_statement_fulfillment ON settlement_lines(statement_id, fulfillment_order_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_product_revision_unique ON product_revisions(product_id, version)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create PostgreSQL index: %w", err)
		}
	}
	return nil
}

func applyPostgresOwnershipGuards(db *gorm.DB) error {
	function := `
	CREATE OR REPLACE FUNCTION enforce_ticket_ownership() RETURNS trigger AS $$
	BEGIN
		CASE TG_TABLE_NAME
		WHEN 'check_points' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id) THEN
				RAISE EXCEPTION 'checkpoint scenic area ownership mismatch';
			END IF;
		WHEN 'devices' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id)
			   OR (NEW.check_point_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM check_points c WHERE c.id = NEW.check_point_id AND c.tenant_id = NEW.tenant_id AND c.scenic_area_id = NEW.scenic_area_id)) THEN
				RAISE EXCEPTION 'device ownership mismatch';
			END IF;
		WHEN 'products' THEN
			IF NEW.source_product_id = 0 AND (NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id)) THEN
				RAISE EXCEPTION 'product scenic area ownership mismatch';
			END IF;
		WHEN 'product_inventories' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.scenic_area_id = NEW.scenic_area_id) THEN
				RAISE EXCEPTION 'inventory product ownership mismatch';
			END IF;
		WHEN 'order_items' THEN
			IF NEW.fulfillment_scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.fulfillment_product_id AND p.tenant_id = NEW.fulfillment_tenant_id AND p.scenic_area_id = NEW.fulfillment_scenic_area_id) THEN
				RAISE EXCEPTION 'order item fulfillment ownership mismatch';
			END IF;
		WHEN 'fulfillment_orders' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM orders o WHERE o.id = NEW.sales_order_id AND o.tenant_id = NEW.sales_tenant_id)
			   OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.supplier_tenant_id) THEN
				RAISE EXCEPTION 'fulfillment order ownership mismatch';
			END IF;
		WHEN 'tickets' THEN
			IF NEW.fulfillment_scenic_area_id = 0 OR (NEW.order_id != 0 AND NOT EXISTS (
				SELECT 1 FROM order_items i JOIN orders o ON o.id = i.order_id
				WHERE i.id = NEW.order_item_id AND i.order_id = NEW.order_id AND o.tenant_id = NEW.tenant_id
				  AND i.fulfillment_tenant_id = NEW.fulfillment_tenant_id AND i.fulfillment_scenic_area_id = NEW.fulfillment_scenic_area_id
				  AND i.fulfillment_order_id = NEW.fulfillment_order_id
			)) THEN RAISE EXCEPTION 'ticket fulfillment ownership mismatch'; END IF;
		WHEN 'ticket_entitlements' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (
				SELECT 1 FROM tickets t WHERE t.id = NEW.ticket_id AND t.ticket_code = NEW.ticket_code
				  AND t.fulfillment_order_id = NEW.fulfillment_order_id AND t.tenant_id = NEW.sales_tenant_id
				  AND t.fulfillment_tenant_id = NEW.supplier_tenant_id AND t.fulfillment_scenic_area_id = NEW.scenic_area_id
			) THEN RAISE EXCEPTION 'ticket entitlement ownership mismatch'; END IF;
		WHEN 'check_in_records' THEN
			IF NEW.result = 'success' AND (NEW.scenic_area_id = 0 OR NOT EXISTS (
				SELECT 1 FROM tickets t JOIN check_points c ON c.id = NEW.check_point_id JOIN devices d ON d.id = NEW.device_id
				WHERE t.id = NEW.ticket_id AND t.ticket_code = NEW.ticket_code
				  AND t.fulfillment_tenant_id = NEW.tenant_id AND t.fulfillment_scenic_area_id = NEW.scenic_area_id
				  AND c.tenant_id = NEW.tenant_id AND c.scenic_area_id = NEW.scenic_area_id
				  AND d.tenant_id = NEW.tenant_id AND d.scenic_area_id = NEW.scenic_area_id AND d.check_point_id = c.id
			)) THEN RAISE EXCEPTION 'check-in ownership mismatch'; END IF;
		WHEN 'order_visitors' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM orders o JOIN order_items i ON i.order_id = o.id JOIN tickets t ON t.order_item_id = i.id
				WHERE o.id = NEW.order_id AND i.id = NEW.order_item_id AND t.id = NEW.ticket_id
				  AND o.tenant_id = NEW.tenant_id AND t.tenant_id = NEW.tenant_id AND t.ticket_code = NEW.ticket_code
			) THEN RAISE EXCEPTION 'order visitor ownership mismatch'; END IF;
		END CASE;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql;`
	if err := db.Exec(function).Error; err != nil {
		return fmt.Errorf("create PostgreSQL ownership function: %w", err)
	}
	for _, table := range []string{"check_points", "devices", "products", "product_inventories", "order_items", "fulfillment_orders", "tickets", "ticket_entitlements", "check_in_records", "order_visitors"} {
		if err := db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS ownership_guard ON %s; CREATE TRIGGER ownership_guard BEFORE INSERT OR UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION enforce_ticket_ownership()`, table, table)).Error; err != nil {
			return fmt.Errorf("create PostgreSQL ownership trigger on %s: %w", table, err)
		}
	}
	return nil
}

func applyPostgresBundleGuards(db *gorm.DB) error {
	statements := []string{
		`CREATE OR REPLACE FUNCTION enforce_bundle_version() RETURNS trigger AS $$
		 BEGIN
		  IF NOT EXISTS (SELECT 1 FROM bundle_products b WHERE b.id = NEW.bundle_product_id AND b.seller_tenant_id = NEW.seller_tenant_id) THEN RAISE EXCEPTION 'bundle version ownership mismatch'; END IF;
		  IF TG_OP = 'UPDATE' AND (NEW.bundle_product_id IS DISTINCT FROM OLD.bundle_product_id OR NEW.seller_tenant_id IS DISTINCT FROM OLD.seller_tenant_id OR NEW.version IS DISTINCT FROM OLD.version OR NEW.retail_price_cents IS DISTINCT FROM OLD.retail_price_cents) THEN RAISE EXCEPTION 'bundle version facts are immutable'; END IF;
		  RETURN NEW;
		 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS bundle_version_guard ON bundle_versions; CREATE TRIGGER bundle_version_guard BEFORE INSERT OR UPDATE ON bundle_versions FOR EACH ROW EXECUTE FUNCTION enforce_bundle_version()`,
		`CREATE OR REPLACE FUNCTION enforce_bundle_component() RETURNS trigger AS $$
		 BEGIN
		  IF TG_OP = 'UPDATE' THEN RAISE EXCEPTION 'bundle component facts are immutable'; END IF;
		  IF NOT EXISTS (
		   SELECT 1 FROM bundle_versions v
		   JOIN products p ON p.id = NEW.seller_product_id AND p.tenant_id = NEW.seller_tenant_id
		   JOIN seller_listings l ON l.product_id = p.id AND l.seller_tenant_id = NEW.seller_tenant_id AND l.product_offer_id = NEW.product_offer_id
		   JOIN product_offers o ON o.id = NEW.product_offer_id AND o.distributor_tenant_id = NEW.seller_tenant_id
		   WHERE v.id = NEW.bundle_version_id AND v.seller_tenant_id = NEW.seller_tenant_id
		     AND o.supplier_tenant_id = NEW.supplier_tenant_id AND o.source_product_id = NEW.source_product_id
		     AND o.product_revision_id = NEW.product_revision_id AND o.fulfillment_scenic_area_id = NEW.fulfillment_scenic_area_id
		  ) THEN RAISE EXCEPTION 'bundle component ownership mismatch'; END IF;
		  RETURN NEW;
		 END; $$ LANGUAGE plpgsql`,
		`DROP TRIGGER IF EXISTS bundle_component_guard ON bundle_components; CREATE TRIGGER bundle_component_guard BEFORE INSERT OR UPDATE ON bundle_components FOR EACH ROW EXECUTE FUNCTION enforce_bundle_component()`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create PostgreSQL bundle guard: %w", err)
		}
	}
	return nil
}
