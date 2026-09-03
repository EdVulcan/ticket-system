package model

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CurrentPostgresSchemaVersion = 108

// PostgreSQL starts from the current domain schema. Historical migrations are
// retained as source history, but are not replayed against a fresh database.
func runPostgresMigrations(db *gorm.DB) error {
	previousSchemaVersion := 0
	if db.Migrator().HasTable(&SchemaMigration{}) {
		if err := db.Model(&SchemaMigration{}).Select("COALESCE(MAX(version), 0)").Scan(&previousSchemaVersion).Error; err != nil {
			return fmt.Errorf("read current PostgreSQL schema version: %w", err)
		}
	}
	if previousSchemaVersion > CurrentPostgresSchemaVersion {
		return fmt.Errorf(
			"database schema version %d is newer than supported version %d; refusing to start",
			previousSchemaVersion,
			CurrentPostgresSchemaVersion,
		)
	}
	hadSettlementSource := db.Migrator().HasColumn(&SettlementLine{}, "Source")
	hadTravelRelationshipStatus := db.Migrator().HasColumn(&DistributorRelationship{}, "TravelStatus")
	if previousSchemaVersion > 0 && previousSchemaVersion < 76 && db.Migrator().HasIndex(&ChannelRequest{}, "idx_channel_request") {
		if err := db.Migrator().DropIndex(&ChannelRequest{}, "idx_channel_request"); err != nil {
			return fmt.Errorf("replace channel request idempotency index: %w", err)
		}
	}
	models := []interface{}{
		&SchemaMigration{},
		&Tenant{}, &TenantCapability{}, &SupplierBusinessType{}, &ScenicArea{}, &HotelProperty{}, &HotelRoomType{}, &HotelRatePlan{}, &HotelRatePlanPrice{}, &HotelRoomInventory{}, &HotelProduct{}, &HotelProductRevision{}, &HotelProductCalendarPrice{}, &HotelProductEntitlement{}, &HotelProductReservation{}, &ScenicHotelPackage{}, &ScenicHotelPackageEntitlement{}, &HotelReservation{}, &PlatformUser{}, &User{}, &Staff{},
		&CheckPoint{}, &Device{}, &TicketRule{}, &RuleGroup{}, &RuleItem{},
		&Product{}, &ProductRevision{}, &ProductOffer{}, &SellerListing{}, &ProductInventory{},
		&CatalogBatchChangePlan{}, &CatalogBatchChangeLine{},
		&PlatformAIConfig{}, &AITenantQuotaPolicy{}, &AIUsageMonth{},
		&AgentTask{}, &AgentTaskEvent{},
		&AgentBusinessAlias{},
		&BundleProduct{}, &BundleVersion{}, &BundleComponent{},
		&Order{}, &OrderItem{}, &Ticket{}, &OrderVisitor{}, &FulfillmentOrder{}, &TicketEntitlement{}, &CheckInRecord{},
		&DistributorRelationship{}, &CapitalAccount{}, &TransactionRecord{}, &LedgerEntry{},
		&Policy{}, &PaymentConfig{}, &Payment{}, &Refund{}, &PaymentReconciliationTask{}, &DigitalRefundTask{},
		&AuditLog{}, &OTANonce{}, &FinancialDocument{},
		&ChannelAccount{}, &MiniappCustomer{}, &ChannelProductMapping{}, &XiaohongshuProductConfig{}, &XiaohongshuBookingOperation{}, &XiaohongshuOrderOperation{}, &ChannelRequest{}, &ChannelNonce{}, &ChannelReservation{},
		&CtripOrderLink{}, &CtripOrderItem{}, &CtripOutboundTask{}, &XiaohongshuOrderLink{}, &XiaohongshuVoucherLink{}, &XiaohongshuVoucherVerification{}, &XiaohongshuWebhookEvent{},
		&ChannelBillRecord{}, &ChannelReconciliation{}, &ChannelReconciliationLine{},
		&TravelContract{}, &TravelAgent{}, &TourGuide{}, &TravelVehicle{}, &TourGroup{}, &TourGroupMember{},
		&TourEntryBatch{}, &TourGroupConfirmation{}, &TourGroupMemberChange{}, &TeamSettlementStatement{}, &TeamSettlementAdjustment{},
		&POSShift{}, &POSShiftCorrection{}, &PrintJob{}, &PrintTemplate{}, &PrintTemplateRevision{}, &DeviceAlert{}, &POSHold{}, &POSHoldLine{},
		&SettlementStatement{}, &SettlementLine{}, &SettlementAdjustment{}, &StaffResourceScope{},
		&AfterSaleRequest{}, &AfterSaleEvent{}, &HardwareCommand{}, &HardwareEvent{}, &DeviceRequestNonce{}, &DeviceVerification{}, &DeviceMaintenanceCredential{}, &DeviceMaintenanceSession{}, &DeviceProvisioningLease{}, &MigrationAuditIssue{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("create current PostgreSQL schema: %w", err)
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 82 {
		if err := db.Exec(`
			ALTER TABLE hotel_reservations DROP CONSTRAINT IF EXISTS chk_hotel_reservations_status;
			ALTER TABLE hotel_reservations ADD CONSTRAINT chk_hotel_reservations_status
				CHECK (status IN ('reserved','confirmed','checked_in','checked_out','no_show','cancelled','refunded'))
		`).Error; err != nil {
			return fmt.Errorf("expand hotel reservation fulfillment states: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 83 {
		if err := db.Exec(`
			DROP INDEX IF EXISTS idx_hotel_reservations_ticket_id;
			CREATE INDEX IF NOT EXISTS idx_hotel_reservations_ticket_id ON hotel_reservations(ticket_id);
			UPDATE scenic_hotel_packages
			SET voucher_validity_days = 0, min_advance_days = 0, max_reschedules = 0
			WHERE booking_mode = 'at_purchase';
		`).Error; err != nil {
			return fmt.Errorf("allow package booking history per ticket: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 84 {
		if err := db.Exec(`
			ALTER TABLE xiaohongshu_booking_operations ADD COLUMN IF NOT EXISTS failed_from_stage varchar(30) NOT NULL DEFAULT '';
			ALTER TABLE scenic_hotel_package_entitlements DROP CONSTRAINT IF EXISTS chk_scenic_hotel_package_entitlements_status;
			ALTER TABLE scenic_hotel_package_entitlements ADD CONSTRAINT chk_scenic_hotel_package_entitlements_status
				CHECK (status IN ('pending_booking','booking_pending','booked','cancel_pending','cancelled','refunded','expired'));
			ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_type;
			ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_type
				CHECK (type IN ('book','revoke','refund'));
			ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_status;
			ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_status
				CHECK (status IN ('pending','remote_succeeded','confirm_pending','completed','compensation_pending','failed'));
			ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_semantics;
			ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_semantics CHECK (
				((type = 'book' AND external_book_order_id <> '') OR (type IN ('revoke','refund') AND external_book_order_id <> '' AND platform_book_id <> ''))
				AND (type = 'book' OR status IN ('pending','remote_succeeded','completed','failed'))
				AND (status NOT IN ('remote_succeeded','confirm_pending','compensation_pending','completed') OR platform_book_id <> '')
				AND (failed_from_stage = '' OR failed_from_stage IN ('pending','remote_succeeded','confirm_pending','compensation_pending'))
				AND (failed_from_stage = '' OR status = 'failed')
				AND ((status IN ('completed','failed')) = (completed_at IS NOT NULL))
				AND ((status IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) = (next_attempt_at IS NOT NULL))
			);
		`).Error; err != nil {
			return fmt.Errorf("expand package booking operation states: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 92 {
		if err := db.Exec(`
			ALTER TABLE platform_ai_configs ADD COLUMN IF NOT EXISTS agent_protocol_mode varchar(20) NOT NULL DEFAULT 'legacy_json';
			UPDATE platform_ai_configs SET agent_protocol_mode = 'legacy_json' WHERE agent_protocol_mode IS NULL OR agent_protocol_mode = '';
			ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS protocol_mode varchar(20) NOT NULL DEFAULT 'legacy_json';
			ALTER TABLE agent_tasks ADD COLUMN IF NOT EXISTS response_text text;
			UPDATE agent_tasks SET protocol_mode = 'legacy_json' WHERE protocol_mode IS NULL OR protocol_mode = '';
			CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_event_call ON agent_task_events(task_id, tool_call_id) WHERE tool_call_id <> '';
		`).Error; err != nil {
			return fmt.Errorf("add agent tool runtime protocol and audit fields: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 93 {
		if err := db.Exec(`
			DROP INDEX IF EXISTS idx_agent_task_event_call;
			-- Legacy rows used one task/version key for every tool call. Give each
			-- existing event a stable unique identity before enforcing the new
			-- attempt-level idempotency constraint.
			UPDATE agent_task_events
			SET idempotency_key = LEFT(COALESCE(idempotency_key, ''), 88) || ':legacy:' || id::text
			WHERE COALESCE(idempotency_key, '') <> '';
		`).Error; err != nil {
			return fmt.Errorf("migrate agent tool event attempt identities: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 94 {
		if err := db.Exec(`
			ALTER TABLE platform_ai_configs
				ALTER COLUMN agent_protocol_mode SET DEFAULT 'auto';
			-- The old UI defaulted DeepSeek to legacy JSON even though the
			-- deployed tool registry only exists in the native protocol. Existing
			-- DeepSeek configurations therefore migrate to the automatic selector;
			-- explicitly selected legacy mode for other providers stays unchanged.
			UPDATE platform_ai_configs
			SET agent_protocol_mode = 'auto'
			WHERE provider = 'deepseek' AND agent_protocol_mode = 'legacy_json';
		`).Error; err != nil {
			return fmt.Errorf("migrate DeepSeek AI protocol default: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 86 {
		if err := db.Exec(`
			ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_type;
			ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_semantics;
			-- Preserve global operation-key uniqueness before converting the legacy
			-- refund prefix. A manually inserted future-format key must not make
			-- the migration overwrite or discard a durable legacy task.
			UPDATE xiaohongshu_booking_operations AS legacy
			SET operation_key = legacy.operation_key || ':legacy-' || legacy.id
			WHERE legacy.type = 'refund'
			  AND legacy.operation_key LIKE 'xhs:refund:%'
			  AND EXISTS (
				SELECT 1
				FROM xiaohongshu_booking_operations AS current
				WHERE current.operation_key = regexp_replace(legacy.operation_key, '^xhs:refund:', 'xhs:refund_status_sync:')
				  AND current.id <> legacy.id
			  );
			UPDATE xiaohongshu_booking_operations
			SET operation_key = regexp_replace(operation_key, '^xhs:refund:', 'xhs:refund_status_sync:')
			WHERE type = 'refund' AND operation_key LIKE 'xhs:refund:%';
			UPDATE xiaohongshu_booking_operations
			SET type = 'refund_status_sync'
			WHERE type = 'refund';
			ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_type
				CHECK (type IN ('book','revoke','refund_status_sync'));
			ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_semantics CHECK (
				((type = 'book' AND external_book_order_id <> '') OR (type IN ('revoke','refund_status_sync') AND external_book_order_id <> '' AND platform_book_id <> ''))
				AND (type = 'book' OR status IN ('pending','remote_succeeded','completed','failed'))
				AND (status NOT IN ('remote_succeeded','confirm_pending','compensation_pending','completed') OR platform_book_id <> '')
				AND (failed_from_stage = '' OR failed_from_stage IN ('pending','remote_succeeded','confirm_pending','compensation_pending'))
				AND (failed_from_stage = '' OR status = 'failed')
				AND ((status IN ('completed','failed')) = (completed_at IS NOT NULL))
				AND ((status IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) = (next_attempt_at IS NOT NULL))
			);
		`).Error; err != nil {
			return fmt.Errorf("rename Xiaohongshu refund status sync operations: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 90 {
		if err := db.Exec(`
			ALTER TABLE platform_ai_configs
				ALTER COLUMN max_output_tokens SET DEFAULT 0;
			UPDATE platform_ai_configs
			SET max_output_tokens = 0
			WHERE max_output_tokens = 1200;
		`).Error; err != nil {
			return fmt.Errorf("migrate AI output budget to provider default: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 91 {
		if err := db.Exec(`
			ALTER TABLE platform_ai_configs
				ALTER COLUMN request_timeout_seconds SET DEFAULT 120;
			UPDATE platform_ai_configs
			SET request_timeout_seconds = 120
			WHERE request_timeout_seconds = 30;
		`).Error; err != nil {
			return fmt.Errorf("migrate legacy AI request timeout: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 97 {
		if err := db.Exec(`
			DELETE FROM agent_business_aliases
			WHERE BTRIM(alias) = '' OR BTRIM(canonical_name) = ''
		`).Error; err != nil {
			return fmt.Errorf("clean invalid AI business aliases: %w", err)
		}
	}
	if previousSchemaVersion < 98 {
		// Schema 98 registers the durable compound-preview parent task. The
		// ownership trigger below is recreated on every upgrade, so no data
		// rewrite is required for existing tasks.
		if err := db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_business_alias_ci
			ON agent_business_aliases (tenant_id, kind, LOWER(alias))
		`).Error; err != nil {
			return fmt.Errorf("register compound AI preview task and business alias index: %w", err)
		}
	}
	if previousSchemaVersion < 99 {
		if err := db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_rate_plan_prices_scope
			ON hotel_rate_plan_prices (tenant_id, hotel_id, room_type_id, rate_plan_id, stay_date)
		`).Error; err != nil {
			return fmt.Errorf("register hotel rate plan stay-date price calendar: %w", err)
		}
	}
	if previousSchemaVersion < 100 {
		if err := db.Exec(`
			ALTER TABLE products ADD COLUMN IF NOT EXISTS product_kind varchar(30) NOT NULL DEFAULT 'ticket';
			UPDATE products
			SET product_kind = 'scenic_hotel_package'
			WHERE id IN (SELECT product_id FROM scenic_hotel_packages WHERE deleted_at IS NULL);
			UPDATE products SET product_kind = 'ticket' WHERE product_kind IS NULL OR product_kind = '';
			ALTER TABLE products DROP CONSTRAINT IF EXISTS chk_products_product_kind;
			ALTER TABLE products ADD CONSTRAINT chk_products_product_kind
				CHECK (product_kind IN ('ticket','scenic_hotel_package','hotel'));
		`).Error; err != nil {
			return fmt.Errorf("register hotel product catalog kind: %w", err)
		}
	}
	if previousSchemaVersion < 101 {
		// Agent task creation keys are scoped to the authenticated operator. The
		// old index was globally unique, so a shared tenant key could block an
		// unrelated user even though every task read/confirm path is actor-scoped.
		if err := db.Exec(`
			DROP INDEX IF EXISTS idx_agent_task_idempotency;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_idempotency
				ON agent_tasks (tenant_id, actor_user_id, idempotency_key);
		`).Error; err != nil {
			return fmt.Errorf("scope agent task idempotency index to tenant and actor: %w", err)
		}
	}
	if previousSchemaVersion < 102 {
		if err := db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_tenant_quota_policy
				ON ai_tenant_quota_policies (tenant_id);
			ALTER TABLE ai_tenant_quota_policies
				DROP CONSTRAINT IF EXISTS chk_ai_tenant_quota_policy_limits;
			ALTER TABLE ai_tenant_quota_policies
				ADD CONSTRAINT chk_ai_tenant_quota_policy_limits CHECK (
					(monthly_request_limit IS NULL OR monthly_request_limit BETWEEN 1 AND 1000000)
					AND (monthly_token_limit IS NULL OR monthly_token_limit BETWEEN 1000 AND 1000000000)
				);
		`).Error; err != nil {
			return fmt.Errorf("register tenant AI quota policies: %w", err)
		}
	}
	if previousSchemaVersion < 103 {
		if err := db.Exec(`
			-- Older model revisions accidentally created this as a global unique
			-- index on version. Recreate it as the intended per-template key.
			DROP INDEX IF EXISTS idx_print_template_revision_version;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_print_template_scope
				ON print_templates(tenant_id, scenic_area_id, product_id, product_revision_id)
				WHERE deleted_at IS NULL;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_print_template_revision_version
				ON print_template_revisions(template_id, version)
				WHERE deleted_at IS NULL;
			CREATE INDEX IF NOT EXISTS idx_print_jobs_template_revision
				ON print_jobs(tenant_id, template_revision_id);
		`).Error; err != nil {
			return fmt.Errorf("register print template revisions and print snapshots: %w", err)
		}
	}
	if previousSchemaVersion < 104 {
		if err := db.Exec(`
			ALTER TABLE print_templates ADD COLUMN IF NOT EXISTS orientation varchar(20) NOT NULL DEFAULT 'portrait';
			ALTER TABLE print_jobs ADD COLUMN IF NOT EXISTS orientation varchar(20) NOT NULL DEFAULT 'portrait';
			UPDATE print_templates SET orientation = 'portrait' WHERE orientation IS NULL OR orientation = '';
			UPDATE print_jobs SET orientation = 'portrait' WHERE orientation IS NULL OR orientation = '';
			ALTER TABLE print_templates DROP CONSTRAINT IF EXISTS chk_print_templates_orientation;
			ALTER TABLE print_templates ADD CONSTRAINT chk_print_templates_orientation
				CHECK (orientation IN ('portrait','landscape'));
			ALTER TABLE print_jobs DROP CONSTRAINT IF EXISTS chk_print_jobs_orientation;
			ALTER TABLE print_jobs ADD CONSTRAINT chk_print_jobs_orientation
				CHECK (orientation IN ('portrait','landscape'));
		`).Error; err != nil {
			return fmt.Errorf("add print template orientation: %w", err)
		}
	}
	if previousSchemaVersion < 105 {
		if err := db.Exec(`
			ALTER TABLE orders ADD COLUMN IF NOT EXISTS client_request_id varchar(100) NOT NULL DEFAULT '';
			ALTER TABLE orders ADD COLUMN IF NOT EXISTS client_request_hash varchar(64) NOT NULL DEFAULT '';
			CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_window_client_request
				ON orders(tenant_id, channel, client_request_id)
				WHERE channel = 'window' AND client_request_id <> '' AND deleted_at IS NULL;
		`).Error; err != nil {
			return fmt.Errorf("add window order client request idempotency: %w", err)
		}
	}
	if previousSchemaVersion < 106 {
		if err := db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_device_maintenance_active_credential
				ON device_maintenance_credentials(device_id)
				WHERE status = 'active' AND deleted_at IS NULL;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_device_maintenance_session_active_device
				ON device_maintenance_sessions(device_id)
				WHERE status IN ('pending','active') AND deleted_at IS NULL;
			ALTER TABLE device_maintenance_credentials
				DROP CONSTRAINT IF EXISTS chk_device_maintenance_credential_status;
			ALTER TABLE device_maintenance_credentials
				ADD CONSTRAINT chk_device_maintenance_credential_status
				CHECK (status IN ('active','revoked'));
			ALTER TABLE device_maintenance_sessions
				DROP CONSTRAINT IF EXISTS chk_device_maintenance_session_status;
			ALTER TABLE device_maintenance_sessions
				ADD CONSTRAINT chk_device_maintenance_session_status
				CHECK (status IN ('pending','active','closed','expired','interrupted'));
		`).Error; err != nil {
			return fmt.Errorf("register device maintenance credentials and sessions: %w", err)
		}
	}
	if previousSchemaVersion < 107 {
		if err := db.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS idx_device_provisioning_active_device
				ON device_provisioning_leases(device_id)
				WHERE status IN ('pending','claimed') AND deleted_at IS NULL;
			ALTER TABLE device_provisioning_leases
				DROP CONSTRAINT IF EXISTS chk_device_provisioning_lease_status;
			ALTER TABLE device_provisioning_leases
				ADD CONSTRAINT chk_device_provisioning_lease_status
				CHECK (status IN ('pending','claimed','completed','expired','revoked'));
		`).Error; err != nil {
			return fmt.Errorf("register device provisioning leases: %w", err)
		}
	}
	if previousSchemaVersion < 108 {
		if err := db.Exec(`
			ALTER TABLE tickets
				ADD COLUMN IF NOT EXISTS pending_xiaohongshu_verification_id bigint NOT NULL DEFAULT 0;
			CREATE INDEX IF NOT EXISTS idx_tickets_pending_xiaohongshu_verification
				ON tickets(pending_xiaohongshu_verification_id)
				WHERE pending_xiaohongshu_verification_id <> 0 AND deleted_at IS NULL;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_xhs_voucher_verification_link
				ON xiaohongshu_voucher_verifications(voucher_link_id)
				WHERE deleted_at IS NULL;
			DROP INDEX IF EXISTS idx_xhs_voucher_verification_request;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_xhs_voucher_verification_request
				ON xiaohongshu_voucher_verifications(device_id, request_id)
				WHERE deleted_at IS NULL;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_xhs_voucher_verification_verify
				ON xiaohongshu_voucher_verifications(channel_account_id, verify_id)
				WHERE verify_id <> '' AND deleted_at IS NULL;
			ALTER TABLE xiaohongshu_voucher_verifications
				DROP CONSTRAINT IF EXISTS chk_xhs_voucher_verification_state;
			ALTER TABLE xiaohongshu_voucher_verifications
				ADD CONSTRAINT chk_xhs_voucher_verification_state CHECK (
					state IN ('prepared','external_in_flight','external_unknown','external_confirmed','local_pending','local_completed','external_rejected','local_rejected','manual_review')
				);
		`).Error; err != nil {
			return fmt.Errorf("register xiaohongshu voucher verification coordination: %w", err)
		}
	}
	if previousSchemaVersion > 0 && previousSchemaVersion < 80 {
		if err := db.Exec(`
			INSERT INTO supplier_business_types
				(tenant_id, business_type, status, activated_at, reason, created_at, updated_at)
			SELECT capability.tenant_id, 'scenic', 'active',
			       COALESCE(capability.approved_at, capability.created_at, NOW()),
			       'existing supplier migrated to scenic ticketing', NOW(), NOW()
			FROM tenant_capabilities AS capability
			WHERE capability.capability = 'supplier'
			  AND capability.status = 'active'
			  AND (capability.expires_at IS NULL OR capability.expires_at > NOW())
			  AND capability.deleted_at IS NULL
			ON CONFLICT (tenant_id, business_type) DO NOTHING
		`).Error; err != nil {
			return fmt.Errorf("backfill existing supplier scenic business type: %w", err)
		}
	}
	if !hadTravelRelationshipStatus {
		if err := db.Exec(`
			UPDATE distributor_relationships AS relationship
			SET travel_status = CASE
				WHEN EXISTS (
					SELECT 1 FROM travel_contracts AS contract
					WHERE contract.travel_tenant_id = relationship.agent_tenant_id
					  AND contract.supplier_tenant_id = relationship.supplier_tenant_id
					  AND contract.status = 'active'
					  AND contract.deleted_at IS NULL
				) THEN 'active'
				ELSE 'suspended'
			END
			WHERE EXISTS (
				SELECT 1 FROM travel_contracts AS contract
				WHERE contract.travel_tenant_id = relationship.agent_tenant_id
				  AND contract.supplier_tenant_id = relationship.supplier_tenant_id
				  AND contract.deleted_at IS NULL
			)
		`).Error; err != nil {
			return fmt.Errorf("backfill travel partnership status: %w", err)
		}
	}
	// Schema 74 is the first version that treats distribution and team
	// partnerships as independent facts. Reapply the idempotent repair on every
	// subsequent upgrade so a persisted version 74 database is covered too.
	if previousSchemaVersion > 0 && previousSchemaVersion < CurrentPostgresSchemaVersion {
		if err := db.Exec(`
			UPDATE distributor_relationships AS relationship
			SET travel_status = 'none', travel_applied_at = NULL
			WHERE relationship.travel_status != 'none'
			  AND NOT EXISTS (
				SELECT 1 FROM travel_contracts AS contract
				WHERE contract.travel_tenant_id = relationship.agent_tenant_id
				  AND contract.supplier_tenant_id = relationship.supplier_tenant_id
				  AND contract.deleted_at IS NULL
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM audit_logs AS audit
				WHERE audit.target_type = 'supplier_relationship'
				  AND audit.target_id = relationship.id
				  AND audit.action IN ('team.partner.apply', 'team.partner.audit')
				  AND audit.deleted_at IS NULL
			  )
		`).Error; err != nil {
			return fmt.Errorf("repair inferred travel partnerships: %w", err)
		}
	}
	if err := db.Exec(`
		UPDATE distributor_relationships
		SET distribution_applied_at = created_at
		WHERE status != 'none' AND distribution_applied_at IS NULL
	`).Error; err != nil {
		return fmt.Errorf("backfill distribution partnership application time: %w", err)
	}
	if err := db.Exec(`
		UPDATE distributor_relationships AS relationship
		SET travel_applied_at = COALESCE(
			(
				SELECT MIN(audit.created_at) FROM audit_logs AS audit
				WHERE audit.target_type = 'supplier_relationship'
				  AND audit.target_id = relationship.id
				  AND audit.action = 'team.partner.apply'
				  AND audit.deleted_at IS NULL
			),
			(
				SELECT MIN(contract.created_at) FROM travel_contracts AS contract
				WHERE contract.travel_tenant_id = relationship.agent_tenant_id
				  AND contract.supplier_tenant_id = relationship.supplier_tenant_id
				  AND contract.deleted_at IS NULL
			)
		)
		WHERE travel_status != 'none' AND travel_applied_at IS NULL
	`).Error; err != nil {
		return fmt.Errorf("backfill travel partnership application time: %w", err)
	}
	if err := db.Model(&ChannelAccount{}).Where("status = ?", "sandbox").Update("environment", "sandbox").Error; err != nil {
		return fmt.Errorf("backfill channel environment: %w", err)
	}
	if err := db.Model(&Product{}).Where("gate_voice_code IS NULL OR gate_voice_code = ?", "").Update("gate_voice_code", "welcome").Error; err != nil {
		return fmt.Errorf("backfill product gate voice: %w", err)
	}
	if err := db.Model(&TeamSettlementStatement{}).Where("sequence IS NULL OR sequence = 0").Updates(map[string]interface{}{"sequence": 1, "kind": "original"}).Error; err != nil {
		return fmt.Errorf("backfill team settlement sequence: %w", err)
	}
	if err := db.Exec(`
		UPDATE team_settlement_statements AS statement
		SET due_at = statement.created_at + contract.settlement_days * INTERVAL '1 day'
		FROM tour_groups AS team, travel_contracts AS contract
		WHERE statement.due_at IS NULL
		  AND statement.kind != 'refund_correction'
		  AND statement.group_id = team.id
		  AND team.contract_id = contract.id
	`).Error; err != nil {
		return fmt.Errorf("backfill team settlement due dates: %w", err)
	}
	if err := backfillPendingRefundReservations(db); err != nil {
		return err
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
	if err := validateTeamUniquenessMigrationData(db); err != nil {
		return err
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
		Name:      "xiaohongshu voucher verification coordination",
		AppliedAt: time.Now(),
	}).Error
}

func validateTeamUniquenessMigrationData(db *gorm.DB) error {
	var fulfillmentConflicts []struct {
		SalesOrderID     uint   `gorm:"column:sales_order_id"`
		SupplierTenantID uint   `gorm:"column:supplier_tenant_id"`
		ScenicAreaID     uint   `gorm:"column:scenic_area_id"`
		GroupNos         string `gorm:"column:group_nos"`
	}
	if err := db.Raw(`
		SELECT sales_order_id, supplier_tenant_id, scenic_area_id,
		       STRING_AGG(group_no, ', ' ORDER BY id) AS group_nos
		FROM tour_groups
		WHERE sales_order_id != 0 AND status != 'cancelled' AND deleted_at IS NULL
		GROUP BY sales_order_id, supplier_tenant_id, scenic_area_id
		HAVING COUNT(*) > 1
		ORDER BY sales_order_id, supplier_tenant_id, scenic_area_id
		LIMIT 20
	`).Scan(&fulfillmentConflicts).Error; err != nil {
		return fmt.Errorf("inspect legacy team fulfillment bindings: %w", err)
	}

	var ticketConflicts []struct {
		TicketCode string `gorm:"column:ticket_code"`
		Members    string `gorm:"column:members"`
		Groups     string `gorm:"column:groups"`
	}
	if err := db.Raw(`
		SELECT member.ticket_code,
		       STRING_AGG(member.id::text, ', ' ORDER BY member.id) AS members,
		       STRING_AGG(COALESCE(team.group_no, 'group#' || member.group_id::text), ', ' ORDER BY member.id) AS groups
		FROM tour_group_members AS member
		LEFT JOIN tour_groups AS team ON team.id = member.group_id
		WHERE member.ticket_code != '' AND member.status != 'cancelled' AND member.deleted_at IS NULL
		GROUP BY member.ticket_code
		HAVING COUNT(*) > 1
		ORDER BY member.ticket_code
		LIMIT 20
	`).Scan(&ticketConflicts).Error; err != nil {
		return fmt.Errorf("inspect legacy team ticket assignments: %w", err)
	}

	if len(fulfillmentConflicts) == 0 && len(ticketConflicts) == 0 {
		return nil
	}
	details := make([]string, 0, len(fulfillmentConflicts)+len(ticketConflicts))
	for _, conflict := range fulfillmentConflicts {
		details = append(details, fmt.Sprintf(
			"duplicate fulfillment binding order=%d supplier=%d scenic=%d groups=[%s]",
			conflict.SalesOrderID, conflict.SupplierTenantID, conflict.ScenicAreaID, conflict.GroupNos,
		))
	}
	for _, conflict := range ticketConflicts {
		details = append(details, fmt.Sprintf(
			"duplicate ticket assignment ticket=%s members=[%s] groups=[%s]",
			conflict.TicketCode, conflict.Members, conflict.Groups,
		))
	}
	return fmt.Errorf(
		"team uniqueness migration blocked by legacy conflicts; resolve the listed records and restart: %s",
		strings.Join(details, "; "),
	)
}

func backfillPendingRefundReservations(db *gorm.DB) error {
	var conflicts int64
	if err := db.Raw(`
		WITH active_reservations AS (
			SELECT r.id AS refund_id, jsonb_array_elements_text(r.ticket_codes_json::jsonb) AS ticket_code
			FROM refunds r
			WHERE r.parent_refund_id = 0
			  AND r.status IN ('pending', 'group_pending')
			  AND COALESCE(r.ticket_codes_json, '') != ''
		), conflicts AS (
			SELECT ticket_code FROM active_reservations GROUP BY ticket_code HAVING COUNT(*) > 1
		)
		SELECT COUNT(*) FROM conflicts
	`).Scan(&conflicts).Error; err != nil {
		return fmt.Errorf("inspect pending refund reservations: %w", err)
	}
	if conflicts > 0 {
		return fmt.Errorf("pending refund migration found %d ticket conflicts requiring manual reconciliation", conflicts)
	}
	if err := db.Exec(`
		WITH active_reservations AS (
			SELECT r.id AS refund_id, jsonb_array_elements_text(r.ticket_codes_json::jsonb) AS ticket_code
			FROM refunds r
			WHERE r.parent_refund_id = 0
			  AND r.status IN ('pending', 'group_pending')
			  AND COALESCE(r.ticket_codes_json, '') != ''
		)
		UPDATE tickets t
		SET pending_refund_id = active_reservations.refund_id
		FROM active_reservations
		WHERE t.ticket_code = active_reservations.ticket_code
		  AND t.status != 'refunded'
	`).Error; err != nil {
		return fmt.Errorf("backfill pending refund reservations: %w", err)
	}
	return nil
}

func applyPostgresIndexes(db *gorm.DB) error {
	for _, index := range []string{"idx_settlement_fulfillment_unique", "idx_settlement_lines_fulfillment_order_id", "idx_team_settlement_statements_group_id", "idx_team_settlement_group_id", "idx_devices_serial_number", "idx_travel_contracts_contract_no", "idx_hotel_property_code", "idx_hotel_room_type_code", "idx_hotel_rate_plan_code", "idx_scenic_hotel_packages_product_id"} {
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
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_travel_contract_scope_no ON travel_contracts(supplier_tenant_id, travel_tenant_id, contract_no) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_active_fulfillment_group ON tour_groups(sales_order_id, supplier_tenant_id, scenic_area_id) WHERE sales_order_id != 0 AND status != 'cancelled' AND deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_member_active_ticket ON tour_group_members(ticket_code) WHERE ticket_code != '' AND status != 'cancelled' AND deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_settlement_group_sequence ON team_settlement_statements(group_id, sequence)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_idempotency ON payments(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_product_stock_slot ON product_inventories(tenant_id, product_id, stock_date, stock_slot)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_reservation_external ON channel_reservations(channel_account_id, external_no)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_financial_document_idempotency ON financial_documents(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_statement_idempotency ON settlement_statements(idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_distribution_pair ON distributor_relationships(agent_tenant_id, supplier_tenant_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_capital_account_pair ON capital_accounts(owner_tenant_id, manager_tenant_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_accounts_code_global ON channel_accounts(code)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_ctrip_app_id ON channel_accounts(type, app_id) WHERE type = 'ctrip' AND app_id != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_xiaohongshu_app_id ON channel_accounts(type, app_id) WHERE type = 'xiaohongshu' AND app_id != ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_settlement_statement_fulfillment ON settlement_lines(statement_id, fulfillment_order_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_product_revision_unique ON product_revisions(product_id, version)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_active_serial ON devices(serial_number) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_property_active_code ON hotel_properties(tenant_id, code) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_room_type_active_code ON hotel_room_types(tenant_id, hotel_id, code) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_rate_plan_active_code ON hotel_rate_plans(tenant_id, hotel_id, room_type_id, code) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_products_active_product ON hotel_products(product_id) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_hotel_product_calendar_prices_scope ON hotel_product_calendar_prices(tenant_id, hotel_product_id, hotel_product_revision_id, stay_date) WHERE deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_scenic_hotel_package_active_product ON scenic_hotel_packages(product_id) WHERE deleted_at IS NULL`,
		`DROP INDEX IF EXISTS idx_agent_task_event_call`,
		`CREATE INDEX IF NOT EXISTS idx_agent_task_event_call ON agent_task_events(task_id, tool_call_id) WHERE tool_call_id <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_event_idempotency ON agent_task_events(task_id, idempotency_key) WHERE idempotency_key <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_event_sequence ON agent_task_events(task_id, sequence)`,
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
		WHEN 'device_maintenance_credentials' THEN
			IF TG_OP = 'UPDATE'
			   AND NEW.tenant_id IS NOT DISTINCT FROM OLD.tenant_id
			   AND NEW.scenic_area_id IS NOT DISTINCT FROM OLD.scenic_area_id
			   AND NEW.device_id IS NOT DISTINCT FROM OLD.device_id
			   AND NEW.secret_hash IS NOT DISTINCT FROM OLD.secret_hash THEN
				IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0
				   OR NEW.status NOT IN ('active','revoked') OR COALESCE(NEW.secret_hash, '') = '' THEN
					RAISE EXCEPTION 'device maintenance credential ownership mismatch';
				END IF;
			ELSIF TG_OP = 'UPDATE'
			   AND (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
				OR NEW.scenic_area_id IS DISTINCT FROM OLD.scenic_area_id
				OR NEW.device_id IS DISTINCT FROM OLD.device_id
				OR NEW.secret_hash IS DISTINCT FROM OLD.secret_hash) THEN
				RAISE EXCEPTION 'device maintenance credential identity is immutable';
			ELSIF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0
			   OR NEW.status NOT IN ('active','revoked')
			   OR COALESCE(NEW.secret_hash, '') = ''
			   OR NOT EXISTS (
					SELECT 1 FROM devices d
					WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
					  AND d.scenic_area_id = NEW.scenic_area_id AND d.type = 'gate'
					  AND d.deleted_at IS NULL
				   ) THEN
				RAISE EXCEPTION 'device maintenance credential ownership mismatch';
			END IF;
		WHEN 'device_maintenance_sessions' THEN
			IF TG_OP = 'UPDATE'
			   AND NEW.tenant_id IS NOT DISTINCT FROM OLD.tenant_id
			   AND NEW.scenic_area_id IS NOT DISTINCT FROM OLD.scenic_area_id
			   AND NEW.device_id IS NOT DISTINCT FROM OLD.device_id
			   AND NEW.actor_user_id IS NOT DISTINCT FROM OLD.actor_user_id
			   AND NEW.mode IS NOT DISTINCT FROM OLD.mode
			   AND NEW.reason IS NOT DISTINCT FROM OLD.reason
			   AND NEW.token_hash IS NOT DISTINCT FROM OLD.token_hash
			   AND NEW.gateway_session_id IS NOT DISTINCT FROM OLD.gateway_session_id THEN
				IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0 OR NEW.actor_user_id = 0
				   OR NEW.mode <> 'ssh'
				   OR NEW.status NOT IN ('pending','active','closed','expired','interrupted')
				   OR COALESCE(NEW.reason, '') = '' OR COALESCE(NEW.token_hash, '') = '' OR COALESCE(NEW.gateway_session_id, '') = '' THEN
					RAISE EXCEPTION 'device maintenance session ownership mismatch';
				END IF;
			ELSIF TG_OP = 'UPDATE'
			   AND (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
				OR NEW.scenic_area_id IS DISTINCT FROM OLD.scenic_area_id
				OR NEW.device_id IS DISTINCT FROM OLD.device_id
				OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id
				OR NEW.mode IS DISTINCT FROM OLD.mode
				OR NEW.reason IS DISTINCT FROM OLD.reason
				OR NEW.token_hash IS DISTINCT FROM OLD.token_hash
				OR NEW.gateway_session_id IS DISTINCT FROM OLD.gateway_session_id) THEN
				RAISE EXCEPTION 'device maintenance session identity is immutable';
			ELSIF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0 OR NEW.actor_user_id = 0
			   OR NEW.mode <> 'ssh'
			   OR NEW.status NOT IN ('pending','active','closed','expired','interrupted')
			   OR COALESCE(NEW.reason, '') = '' OR COALESCE(NEW.token_hash, '') = '' OR COALESCE(NEW.gateway_session_id, '') = ''
			   OR NOT EXISTS (
					SELECT 1 FROM devices d
					WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
					  AND d.scenic_area_id = NEW.scenic_area_id AND d.type = 'gate'
					  AND d.deleted_at IS NULL
				   )
			   OR NOT EXISTS (SELECT 1 FROM users u WHERE u.id = NEW.actor_user_id AND u.tenant_id = NEW.tenant_id AND u.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'device maintenance session ownership mismatch';
			END IF;
		WHEN 'device_provisioning_leases' THEN
			IF TG_OP = 'UPDATE'
			   AND NEW.tenant_id IS NOT DISTINCT FROM OLD.tenant_id
			   AND NEW.scenic_area_id IS NOT DISTINCT FROM OLD.scenic_area_id
			   AND NEW.device_id IS NOT DISTINCT FROM OLD.device_id
			   AND NEW.actor_user_id IS NOT DISTINCT FROM OLD.actor_user_id
			   AND NEW.reason IS NOT DISTINCT FROM OLD.reason
			   AND NEW.token_hash IS NOT DISTINCT FROM OLD.token_hash THEN
				IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0 OR NEW.actor_user_id = 0
				   OR NEW.status NOT IN ('pending','claimed','completed','expired','revoked')
				   OR COALESCE(NEW.reason, '') = '' OR COALESCE(NEW.token_hash, '') = ''
				   OR NEW.expires_at IS NULL THEN
					RAISE EXCEPTION 'device provisioning lease ownership mismatch';
				END IF;
			ELSIF TG_OP = 'UPDATE'
			   AND (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
				OR NEW.scenic_area_id IS DISTINCT FROM OLD.scenic_area_id
				OR NEW.device_id IS DISTINCT FROM OLD.device_id
				OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id
				OR NEW.reason IS DISTINCT FROM OLD.reason
				OR NEW.token_hash IS DISTINCT FROM OLD.token_hash) THEN
				RAISE EXCEPTION 'device provisioning lease identity is immutable';
			ELSIF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.device_id = 0 OR NEW.actor_user_id = 0
			   OR NEW.status NOT IN ('pending','claimed','completed','expired','revoked')
			   OR COALESCE(NEW.reason, '') = '' OR COALESCE(NEW.token_hash, '') = ''
			   OR NEW.expires_at IS NULL
			   OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
				  AND d.scenic_area_id = NEW.scenic_area_id AND d.type = 'gate'
				  AND d.deleted_at IS NULL
			   )
			   OR NOT EXISTS (SELECT 1 FROM users u WHERE u.id = NEW.actor_user_id AND u.tenant_id = NEW.tenant_id AND u.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'device provisioning lease ownership mismatch';
			END IF;
		WHEN 'products' THEN
			IF NEW.product_kind NOT IN ('ticket','scenic_hotel_package','hotel')
			   OR (NEW.product_kind = 'hotel' AND (NEW.source_product_id <> 0 OR NEW.scenic_area_id <> 0))
			   OR (NEW.product_kind IN ('ticket','scenic_hotel_package') AND NEW.source_product_id = 0 AND (NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id))) THEN
				RAISE EXCEPTION 'product scenic area ownership mismatch';
			END IF;
		WHEN 'print_templates' THEN
			IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0
			   OR NEW.status NOT IN ('active','disabled')
			   OR NEW.paper_width_mm NOT IN (58,80)
			   OR NEW.orientation NOT IN ('portrait','landscape')
			   OR NOT EXISTS (SELECT 1 FROM scenic_areas s WHERE s.id = NEW.scenic_area_id AND s.tenant_id = NEW.tenant_id AND s.deleted_at IS NULL)
			   OR (NEW.product_id = 0 AND NEW.product_revision_id <> 0)
			   OR (NEW.product_id <> 0 AND NOT EXISTS (
					SELECT 1 FROM products p WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id
					  AND p.product_kind = 'ticket' AND p.scenic_area_id = NEW.scenic_area_id AND p.deleted_at IS NULL
				   ))
			   OR (NEW.product_revision_id <> 0 AND NOT EXISTS (
					SELECT 1 FROM product_revisions r WHERE r.id = NEW.product_revision_id AND r.product_id = NEW.product_id
					  AND r.tenant_id = NEW.tenant_id AND r.scenic_area_id = NEW.scenic_area_id
				   ))
			   OR (NEW.current_revision_id <> 0 AND NOT EXISTS (
					SELECT 1 FROM print_template_revisions r WHERE r.id = NEW.current_revision_id AND r.template_id = NEW.id
					  AND r.tenant_id = NEW.tenant_id AND r.status = 'published'
				   )) THEN
				RAISE EXCEPTION 'print template ownership or publication state is invalid';
			END IF;
		WHEN 'print_template_revisions' THEN
			IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NEW.template_id = 0 OR NEW.version <= 0
			   OR NEW.status NOT IN ('draft','published','retired') OR COALESCE(NEW.definition_json,'') = ''
			   OR COALESCE(NEW.definition_hash,'') = ''
			   OR NOT EXISTS (
					SELECT 1 FROM print_templates t WHERE t.id = NEW.template_id AND t.tenant_id = NEW.tenant_id
					  AND t.scenic_area_id = NEW.scenic_area_id AND t.deleted_at IS NULL
				   )
			   OR (TG_OP = 'UPDATE' AND OLD.status IN ('published','retired') AND (
					NEW.template_id IS DISTINCT FROM OLD.template_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
					OR NEW.scenic_area_id IS DISTINCT FROM OLD.scenic_area_id OR NEW.version IS DISTINCT FROM OLD.version
					OR NEW.definition_json IS DISTINCT FROM OLD.definition_json OR NEW.definition_hash IS DISTINCT FROM OLD.definition_hash
				   )) THEN
				RAISE EXCEPTION 'published print template revision is immutable';
			END IF;
		WHEN 'print_jobs' THEN
			IF NEW.tenant_id = 0 OR NEW.device_id = 0 OR NEW.shift_id = 0 OR COALESCE(NEW.order_no,'') = ''
			   OR NEW.status NOT IN ('queued','printing','printed','failed') OR NEW.attempt_count < 0 OR NEW.copy_count <= 0
			   OR NEW.orientation NOT IN ('portrait','landscape')
			   OR NOT EXISTS (SELECT 1 FROM devices d WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id AND d.type = 'pos')
			   OR NOT EXISTS (SELECT 1 FROM pos_shifts s WHERE s.id = NEW.shift_id AND s.tenant_id = NEW.tenant_id AND s.device_id = NEW.device_id)
			   OR NOT EXISTS (SELECT 1 FROM orders o WHERE o.order_no = NEW.order_no AND o.tenant_id = NEW.tenant_id)
			   OR (COALESCE(NEW.ticket_code,'') <> '' AND NOT EXISTS (
					SELECT 1 FROM tickets t JOIN orders o ON o.id = t.order_id
					WHERE t.ticket_code = NEW.ticket_code AND o.order_no = NEW.order_no AND o.tenant_id = NEW.tenant_id
				   ))
			   OR (NEW.template_revision_id <> 0 AND NOT EXISTS (
					SELECT 1 FROM print_template_revisions r WHERE r.id = NEW.template_revision_id AND r.tenant_id = NEW.tenant_id
				   ))
			   OR NEW.template_revision_id <> 0 AND COALESCE(NEW.print_document_json,'') = '' THEN
				RAISE EXCEPTION 'print job ownership or immutable snapshot is invalid';
			END IF;
		WHEN 'catalog_batch_change_plans' THEN
			IF NEW.tenant_id = 0
			   OR COALESCE(NEW.actor_role, '') = ''
			   OR COALESCE(NEW.operation_json, '') = ''
			   OR COALESCE(NEW.plan_hash, '') = ''
			   OR COALESCE(NEW.idempotency_key, '') = ''
			   OR NEW.status NOT IN ('previewed','confirmed','completed','expired','failed')
			   OR NEW.expires_at IS NULL THEN
				RAISE EXCEPTION 'catalog batch change plan ownership or state mismatch';
			END IF;
		WHEN 'catalog_batch_change_lines' THEN
			IF NEW.tenant_id = 0 OR NEW.product_id = 0 OR NEW.scenic_area_id = 0 OR NEW.before_revision_id = 0
			   OR NEW.status NOT IN ('pending','no_change','applied','failed')
			   OR NOT EXISTS (
				SELECT 1 FROM catalog_batch_change_plans p
				WHERE p.id = NEW.plan_id AND p.tenant_id = NEW.tenant_id
			   )
			   OR NOT EXISTS (
				SELECT 1 FROM products p
				WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.scenic_area_id = NEW.scenic_area_id
			   )
			   OR NOT EXISTS (
				SELECT 1 FROM product_revisions r
				WHERE r.id = NEW.before_revision_id AND r.product_id = NEW.product_id AND r.tenant_id = NEW.tenant_id
			   )
			   OR (NEW.after_revision_id != 0 AND NOT EXISTS (
				SELECT 1 FROM product_revisions r
				WHERE r.id = NEW.after_revision_id AND r.product_id = NEW.product_id AND r.tenant_id = NEW.tenant_id
			   )) THEN
				RAISE EXCEPTION 'catalog batch change line ownership mismatch';
			END IF;
		WHEN 'ai_usage_months' THEN
			IF NEW.tenant_id = 0 OR NEW.period !~ '^[0-9]{4}-(0[1-9]|1[0-2])$'
			   OR NEW.request_count < 0 OR NEW.token_count < 0
			   OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'AI usage tenant or accounting facts are invalid';
			END IF;
		WHEN 'ai_tenant_quota_policies' THEN
			IF NEW.tenant_id = 0
			   OR (NEW.monthly_request_limit IS NOT NULL AND (NEW.monthly_request_limit < 1 OR NEW.monthly_request_limit > 1000000))
			   OR (NEW.monthly_token_limit IS NOT NULL AND (NEW.monthly_token_limit < 1000 OR NEW.monthly_token_limit > 1000000000))
			   OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'AI tenant quota policy ownership or limits are invalid';
			END IF;
		WHEN 'agent_business_aliases' THEN
			IF NEW.tenant_id = 0
			   OR COALESCE(BTRIM(NEW.alias), '') = ''
			   OR COALESCE(BTRIM(NEW.canonical_name), '') = ''
			   OR NEW.kind NOT IN ('scenic_area','checkpoint','product')
			   OR LOWER(BTRIM(NEW.alias)) = LOWER(BTRIM(NEW.canonical_name))
			   OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'AI business alias ownership or shape is invalid';
			END IF;
		WHEN 'agent_tasks' THEN
			IF NEW.tenant_id = 0
			   OR NEW.actor_role = ''
			   OR COALESCE(NEW.protocol_mode, 'legacy_json') NOT IN ('legacy_json','tool_v1')
			   OR NEW.operation_type NOT IN ('pending','catalog_batch_change','ticket_product_create','ticket_product_update','ticket_product_batch_update','compound_preview','hotel_inventory_change','hotel_rate_calendar_change','hotel_product_calendar_change','hotel_reservation_status_change')
			   OR NEW.state NOT IN ('collecting','ready_for_preview','awaiting_confirmation','executing','completed','failed','expired','cancelled')
			   OR COALESCE(NEW.input_text, '') = ''
			   OR COALESCE(NEW.context_json, '') = ''
			   OR COALESCE(NEW.missing_json, '') = ''
			   OR COALESCE(NEW.idempotency_key, '') = ''
			   OR NEW.version < 1
			   OR NEW.expires_at IS NULL
			   OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL)
			   OR (NEW.linked_plan_id != 0 AND NOT EXISTS (
					SELECT 1 FROM catalog_batch_change_plans p
					WHERE p.id = NEW.linked_plan_id AND p.tenant_id = NEW.tenant_id
				)) THEN
				RAISE EXCEPTION 'agent task tenant, state, or linked plan ownership mismatch';
			END IF;
		WHEN 'agent_task_events' THEN
			IF NEW.tenant_id = 0
			   OR NEW.task_id = 0
			   OR NEW.actor_user_id = 0
			   OR COALESCE(NEW.actor_role, '') = ''
			   OR COALESCE(NEW.event_type, '') = ''
			   OR COALESCE(NEW.status, '') = ''
			   OR NEW.sequence < 1
			   OR NEW.token_count < 0
			   OR NEW.duration_ms < 0
			   OR NOT EXISTS (
					SELECT 1 FROM agent_tasks task
					WHERE task.id = NEW.task_id AND task.tenant_id = NEW.tenant_id
					  AND task.actor_user_id = NEW.actor_user_id
				)
			   OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'agent task event tenant or audit facts are invalid';
			END IF;
		WHEN 'product_inventories' THEN
			IF NEW.scenic_area_id = 0 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.scenic_area_id = NEW.scenic_area_id) THEN
				RAISE EXCEPTION 'inventory product ownership mismatch';
			END IF;
		WHEN 'hotel_properties' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'hotel property tenant ownership mismatch';
			END IF;
		WHEN 'hotel_room_types' THEN
			IF NEW.hotel_id = 0 OR NOT EXISTS (SELECT 1 FROM hotel_properties h WHERE h.id = NEW.hotel_id AND h.tenant_id = NEW.tenant_id AND h.deleted_at IS NULL) THEN
				RAISE EXCEPTION 'hotel room type ownership mismatch';
			END IF;
		WHEN 'hotel_rate_plans' THEN
			IF NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NOT EXISTS (
				SELECT 1 FROM hotel_room_types r JOIN hotel_properties h ON h.id = r.hotel_id
				WHERE r.id = NEW.room_type_id AND r.hotel_id = NEW.hotel_id AND r.tenant_id = NEW.tenant_id AND h.tenant_id = NEW.tenant_id
			) THEN RAISE EXCEPTION 'hotel rate plan ownership mismatch'; END IF;
		WHEN 'hotel_rate_plan_prices' THEN
			IF NEW.tenant_id = 0 OR NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NEW.rate_plan_id = 0
			   OR NEW.retail_price_cents <= 0 OR NEW.settlement_price_cents < 0 OR NEW.settlement_price_cents > NEW.retail_price_cents
			   OR NOT EXISTS (
				SELECT 1
				FROM hotel_rate_plans rp
				JOIN hotel_room_types r ON r.id = rp.room_type_id
				JOIN hotel_properties h ON h.id = rp.hotel_id
				WHERE rp.id = NEW.rate_plan_id AND rp.tenant_id = NEW.tenant_id
				  AND rp.hotel_id = NEW.hotel_id AND rp.room_type_id = NEW.room_type_id
				  AND r.tenant_id = NEW.tenant_id AND r.hotel_id = NEW.hotel_id
				  AND h.tenant_id = NEW.tenant_id
			   ) THEN
				RAISE EXCEPTION 'hotel rate plan price ownership mismatch';
			END IF;
		WHEN 'hotel_room_inventories' THEN
			IF NEW.capacity < 0 OR NEW.reserved < 0 OR NEW.sold < 0 OR NEW.reserved + NEW.sold > NEW.capacity THEN
				RAISE EXCEPTION 'hotel room inventory quantity is invalid';
			END IF;
			IF NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NOT EXISTS (
				SELECT 1 FROM hotel_room_types r WHERE r.id = NEW.room_type_id AND r.hotel_id = NEW.hotel_id AND r.tenant_id = NEW.tenant_id
			) THEN RAISE EXCEPTION 'hotel room inventory ownership mismatch'; END IF;
		WHEN 'hotel_products' THEN
			IF NEW.tenant_id = 0 OR NEW.product_id = 0 OR NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NEW.rate_plan_id = 0
			   OR NEW.sale_mode NOT IN ('calendar_room','presale_room') OR NEW.status NOT IN ('online','offline')
			   OR NEW.base_retail_price_cents <= 0 OR NEW.base_settlement_price_cents < 0 OR NEW.base_settlement_price_cents > NEW.base_retail_price_cents
			   OR NEW.nights <= 0 OR NEW.rooms_per_package <= 0 OR NEW.voucher_validity_days < 0 OR NEW.min_advance_days < 0 OR NEW.max_reschedules < 0
			   OR NOT EXISTS (
				SELECT 1 FROM products p
				WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.product_kind = 'hotel' AND p.source_product_id = 0 AND p.scenic_area_id = 0
			   )
			   OR NOT EXISTS (
				SELECT 1 FROM hotel_rate_plans rp
				JOIN hotel_room_types rt ON rt.id = rp.room_type_id
				JOIN hotel_properties h ON h.id = rp.hotel_id
				WHERE rp.id = NEW.rate_plan_id AND rp.tenant_id = NEW.tenant_id
				  AND rp.hotel_id = NEW.hotel_id AND rp.room_type_id = NEW.room_type_id
				  AND rt.tenant_id = NEW.tenant_id AND rt.hotel_id = NEW.hotel_id
				  AND h.tenant_id = NEW.tenant_id
			   )
			   OR (NEW.current_revision_id <> 0 AND NOT EXISTS (
				SELECT 1 FROM hotel_product_revisions r
				WHERE r.id = NEW.current_revision_id AND r.hotel_product_id = NEW.id AND r.tenant_id = NEW.tenant_id AND r.product_id = NEW.product_id
			   ))
			   OR (NEW.status = 'online' AND NEW.current_revision_id = 0) THEN
				RAISE EXCEPTION 'hotel product ownership or sales facts are invalid';
			END IF;
		WHEN 'hotel_product_revisions' THEN
			IF NEW.hotel_product_id = 0 OR NEW.tenant_id = 0 OR NEW.product_id = 0 OR NEW.version <= 0
			   OR NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NEW.rate_plan_id = 0
			   OR NEW.sale_mode NOT IN ('calendar_room','presale_room')
			   OR NEW.base_retail_price_cents <= 0 OR NEW.base_settlement_price_cents < 0 OR NEW.base_settlement_price_cents > NEW.base_retail_price_cents
			   OR NEW.nights <= 0 OR NEW.rooms_per_package <= 0 OR NEW.voucher_validity_days < 0 OR NEW.min_advance_days < 0 OR NEW.max_reschedules < 0
			   OR (TG_OP = 'UPDATE' AND (
				NEW.hotel_product_id IS DISTINCT FROM OLD.hotel_product_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
				OR NEW.product_id IS DISTINCT FROM OLD.product_id OR NEW.version IS DISTINCT FROM OLD.version
				OR NEW.hotel_id IS DISTINCT FROM OLD.hotel_id OR NEW.room_type_id IS DISTINCT FROM OLD.room_type_id OR NEW.rate_plan_id IS DISTINCT FROM OLD.rate_plan_id
				OR NEW.sale_mode IS DISTINCT FROM OLD.sale_mode OR NEW.base_retail_price_cents IS DISTINCT FROM OLD.base_retail_price_cents
				OR NEW.base_settlement_price_cents IS DISTINCT FROM OLD.base_settlement_price_cents OR NEW.nights IS DISTINCT FROM OLD.nights
				OR NEW.rooms_per_package IS DISTINCT FROM OLD.rooms_per_package OR NEW.voucher_validity_days IS DISTINCT FROM OLD.voucher_validity_days
				OR NEW.min_advance_days IS DISTINCT FROM OLD.min_advance_days OR NEW.max_reschedules IS DISTINCT FROM OLD.max_reschedules
			   ))
			   OR NOT EXISTS (
				SELECT 1 FROM hotel_products hp
				WHERE hp.id = NEW.hotel_product_id AND hp.tenant_id = NEW.tenant_id AND hp.product_id = NEW.product_id
			   )
			   OR NOT EXISTS (
				SELECT 1 FROM hotel_rate_plans rp
				JOIN hotel_room_types rt ON rt.id = rp.room_type_id
				JOIN hotel_properties h ON h.id = rp.hotel_id
				WHERE rp.id = NEW.rate_plan_id AND rp.tenant_id = NEW.tenant_id
				  AND rp.hotel_id = NEW.hotel_id AND rp.room_type_id = NEW.room_type_id
				  AND rt.tenant_id = NEW.tenant_id AND rt.hotel_id = NEW.hotel_id
				  AND h.tenant_id = NEW.tenant_id
			   ) THEN
				RAISE EXCEPTION 'hotel product revision ownership or sales facts are invalid';
			END IF;
		WHEN 'hotel_product_calendar_prices' THEN
			IF NEW.tenant_id = 0 OR NEW.hotel_product_id = 0 OR NEW.hotel_product_revision_id = 0
			   OR NEW.retail_price_cents <= 0 OR NEW.settlement_price_cents < 0 OR NEW.settlement_price_cents > NEW.retail_price_cents
			   OR (TG_OP = 'UPDATE' AND (
				NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.hotel_product_id IS DISTINCT FROM OLD.hotel_product_id
				OR NEW.hotel_product_revision_id IS DISTINCT FROM OLD.hotel_product_revision_id OR NEW.stay_date IS DISTINCT FROM OLD.stay_date
				OR NEW.retail_price_cents IS DISTINCT FROM OLD.retail_price_cents OR NEW.settlement_price_cents IS DISTINCT FROM OLD.settlement_price_cents
			   ))
			   OR NOT EXISTS (
				SELECT 1 FROM hotel_product_revisions r
				WHERE r.id = NEW.hotel_product_revision_id AND r.hotel_product_id = NEW.hotel_product_id
				  AND r.tenant_id = NEW.tenant_id AND r.sale_mode = 'calendar_room'
			   ) THEN
				RAISE EXCEPTION 'hotel product calendar price ownership or sales facts are invalid';
			END IF;
		WHEN 'hotel_product_entitlements' THEN
			IF NEW.sales_tenant_id = 0 OR NEW.supplier_tenant_id = 0 OR NEW.order_id = 0 OR NEW.order_item_id = 0
			   OR NEW.hotel_product_id = 0 OR NEW.hotel_product_revision_id = 0 OR NEW.valid_until < NEW.valid_from
			   OR NEW.rooms <= 0 OR NEW.reschedule_count < 0
			   OR NEW.retail_price_cents <= 0 OR NEW.settlement_price_cents < 0 OR NEW.settlement_price_cents > NEW.retail_price_cents
			   OR NEW.price_source NOT IN ('base','calendar')
			   OR ((NEW.check_in_date IS NULL) <> (NEW.check_out_date IS NULL))
			   OR (NEW.check_in_date IS NOT NULL AND NEW.check_out_date <= NEW.check_in_date)
			   OR NOT EXISTS (
				SELECT 1
				FROM orders o
				JOIN order_items i ON i.id = NEW.order_item_id AND i.order_id = o.id
				JOIN hotel_products hp ON hp.id = NEW.hotel_product_id
				JOIN hotel_product_revisions r ON r.id = NEW.hotel_product_revision_id
				WHERE o.id = NEW.order_id AND o.tenant_id = NEW.sales_tenant_id
				  AND i.product_id = hp.product_id AND i.fulfillment_product_id = hp.product_id
				  AND i.fulfillment_tenant_id = NEW.supplier_tenant_id AND i.fulfillment_scenic_area_id = 0
				  AND hp.tenant_id = NEW.supplier_tenant_id
				  AND r.hotel_product_id = hp.id AND r.tenant_id = NEW.supplier_tenant_id AND r.product_id = hp.product_id
			   )
			   OR (NEW.reservation_id <> 0 AND NOT EXISTS (
				SELECT 1 FROM hotel_product_reservations r
				WHERE r.id = NEW.reservation_id AND r.entitlement_id = NEW.id
				  AND r.sales_tenant_id = NEW.sales_tenant_id AND r.supplier_tenant_id = NEW.supplier_tenant_id
				  AND r.order_id = NEW.order_id AND r.order_item_id = NEW.order_item_id
				  AND r.hotel_product_id = NEW.hotel_product_id AND r.hotel_product_revision_id = NEW.hotel_product_revision_id
			   ))
			   OR (NEW.status IN ('booking_pending','booked','cancel_pending') AND NEW.reservation_id = 0) THEN
				RAISE EXCEPTION 'hotel product entitlement ownership or booking facts are invalid';
			END IF;
		WHEN 'hotel_product_reservations' THEN
			IF NEW.sales_tenant_id = 0 OR NEW.supplier_tenant_id = 0 OR NEW.order_id = 0 OR NEW.order_item_id = 0 OR NEW.entitlement_id = 0
			   OR NEW.hotel_product_id = 0 OR NEW.hotel_product_revision_id = 0 OR NEW.hotel_id = 0 OR NEW.room_type_id = 0 OR NEW.rate_plan_id = 0
			   OR NEW.check_out_date <= NEW.check_in_date OR NEW.rooms <= 0
			   OR NEW.retail_price_cents <= 0 OR NEW.settlement_price_cents < 0 OR NEW.settlement_price_cents > NEW.retail_price_cents
			   OR NEW.price_source NOT IN ('base','calendar')
			   OR NEW.status NOT IN ('reserved','confirmed','checked_in','checked_out','no_show','cancelled','refunded')
			   OR NOT EXISTS (
				SELECT 1
				FROM hotel_product_entitlements e
				WHERE e.id = NEW.entitlement_id AND e.sales_tenant_id = NEW.sales_tenant_id AND e.supplier_tenant_id = NEW.supplier_tenant_id
				  AND e.order_id = NEW.order_id AND e.order_item_id = NEW.order_item_id
				  AND e.hotel_product_id = NEW.hotel_product_id AND e.hotel_product_revision_id = NEW.hotel_product_revision_id
			   )
			   OR NOT EXISTS (
				SELECT 1
				FROM hotel_product_revisions r
				JOIN hotel_rate_plans rp ON rp.id = r.rate_plan_id
				JOIN hotel_room_types rt ON rt.id = rp.room_type_id
				JOIN hotel_properties h ON h.id = rp.hotel_id
				WHERE r.id = NEW.hotel_product_revision_id AND r.hotel_product_id = NEW.hotel_product_id AND r.tenant_id = NEW.supplier_tenant_id
				  AND r.hotel_id = NEW.hotel_id AND r.room_type_id = NEW.room_type_id AND r.rate_plan_id = NEW.rate_plan_id
				  AND rp.tenant_id = NEW.supplier_tenant_id AND rt.tenant_id = NEW.supplier_tenant_id AND h.tenant_id = NEW.supplier_tenant_id
			   ) THEN
				RAISE EXCEPTION 'hotel product reservation ownership or snapshot facts are invalid';
			END IF;
		WHEN 'scenic_hotel_packages' THEN
			IF NOT EXISTS (SELECT 1 FROM products p WHERE p.id = NEW.product_id AND p.tenant_id = NEW.tenant_id AND p.type = 'online')
			   OR NOT EXISTS (SELECT 1 FROM hotel_rate_plans rp JOIN hotel_room_types rt ON rt.id = rp.room_type_id JOIN hotel_properties h ON h.id = rp.hotel_id
			      WHERE rp.id = NEW.rate_plan_id AND rp.tenant_id = NEW.tenant_id AND rt.id = NEW.room_type_id AND rt.hotel_id = NEW.hotel_id AND h.id = NEW.hotel_id AND h.tenant_id = NEW.tenant_id) THEN
				RAISE EXCEPTION 'scenic hotel package ownership mismatch';
			END IF;
		WHEN 'scenic_hotel_package_entitlements' THEN
			IF NEW.sales_tenant_id = 0 OR NEW.supplier_tenant_id = 0
			   OR NEW.valid_until < NEW.valid_from OR NEW.reschedule_count < 0
			   OR NOT EXISTS (
				SELECT 1
				FROM orders o
				JOIN order_items i ON i.id = NEW.order_item_id AND i.order_id = o.id
				JOIN tickets t ON t.id = NEW.ticket_id AND t.order_id = o.id AND t.order_item_id = i.id
				JOIN scenic_hotel_packages p ON p.id = NEW.package_id
				WHERE o.id = NEW.order_id AND o.tenant_id = NEW.sales_tenant_id
				  AND i.fulfillment_tenant_id = NEW.supplier_tenant_id
				  AND i.fulfillment_product_id = p.product_id
				  AND t.tenant_id = NEW.sales_tenant_id
				  AND t.fulfillment_tenant_id = NEW.supplier_tenant_id
				  AND t.fulfillment_product_id = p.product_id
				  AND p.tenant_id = NEW.supplier_tenant_id AND p.booking_mode = 'after_purchase'
			   )
			   OR (NEW.reservation_id != 0 AND NOT EXISTS (
				SELECT 1 FROM hotel_reservations r
				WHERE r.id = NEW.reservation_id AND r.sales_tenant_id = NEW.sales_tenant_id
				  AND r.supplier_tenant_id = NEW.supplier_tenant_id AND r.order_id = NEW.order_id
				  AND r.order_item_id = NEW.order_item_id AND r.ticket_id = NEW.ticket_id
				  AND r.package_id = NEW.package_id
			   ))
			   OR (NEW.status = 'pending_booking' AND NEW.reservation_id != 0)
			   OR (NEW.status IN ('booking_pending','booked','cancel_pending') AND NEW.reservation_id = 0) THEN
				RAISE EXCEPTION 'scenic hotel package entitlement ownership mismatch';
			END IF;
		WHEN 'hotel_reservations' THEN
			IF NEW.sales_tenant_id = 0 OR NEW.supplier_tenant_id = 0 OR NEW.sales_tenant_id != NEW.supplier_tenant_id
			   OR NOT EXISTS (
				SELECT 1
				FROM orders o
				JOIN order_items i ON i.id = NEW.order_item_id AND i.order_id = o.id
				JOIN tickets t ON t.id = NEW.ticket_id AND t.order_id = o.id AND t.order_item_id = i.id
				JOIN scenic_hotel_packages p ON p.id = NEW.package_id
				WHERE o.id = NEW.order_id AND o.tenant_id = NEW.sales_tenant_id
				  AND i.fulfillment_tenant_id = NEW.supplier_tenant_id
				  AND i.fulfillment_product_id = p.product_id
				  AND t.tenant_id = NEW.sales_tenant_id
				  AND t.fulfillment_tenant_id = NEW.supplier_tenant_id
				  AND t.fulfillment_product_id = p.product_id
				  AND p.tenant_id = NEW.supplier_tenant_id
				  AND p.hotel_id = NEW.hotel_id AND p.room_type_id = NEW.room_type_id AND p.rate_plan_id = NEW.rate_plan_id
			   )
			   OR NEW.check_out_date <= NEW.check_in_date OR NEW.rooms <= 0 THEN
				RAISE EXCEPTION 'hotel reservation ownership mismatch';
			END IF;
		WHEN 'order_items' THEN
			IF NOT EXISTS (
				SELECT 1 FROM products p
				WHERE p.id = NEW.fulfillment_product_id AND p.tenant_id = NEW.fulfillment_tenant_id
				  AND (
					(p.product_kind = 'hotel' AND NEW.product_id = p.id AND NEW.fulfillment_scenic_area_id = 0
					 AND EXISTS (SELECT 1 FROM hotel_products hp WHERE hp.product_id = p.id AND hp.tenant_id = NEW.fulfillment_tenant_id AND hp.deleted_at IS NULL))
					OR
					(p.product_kind IN ('ticket','scenic_hotel_package') AND NEW.fulfillment_scenic_area_id <> 0 AND p.scenic_area_id = NEW.fulfillment_scenic_area_id)
				  )
			) THEN
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
		WHEN 'device_verifications' THEN
			IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
				  AND d.scenic_area_id = NEW.scenic_area_id AND d.type IN ('gate','handheld')
				  AND d.deleted_at IS NULL
			) OR (NEW.check_in_record_id <> 0 AND NOT EXISTS (
				SELECT 1 FROM check_in_records c
				WHERE c.id = NEW.check_in_record_id AND c.tenant_id = NEW.tenant_id
				  AND c.scenic_area_id = NEW.scenic_area_id AND c.device_id = NEW.device_id
			)) THEN RAISE EXCEPTION 'device verification ownership mismatch'; END IF;
		WHEN 'device_request_nonces' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id AND d.deleted_at IS NULL
			) THEN RAISE EXCEPTION 'device request nonce ownership mismatch'; END IF;
		WHEN 'hardware_commands' THEN
			IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0
			   OR NEW.kind NOT IN ('print','verify','open_gate','read_identity')
			   OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
				  AND d.scenic_area_id = NEW.scenic_area_id AND d.deleted_at IS NULL
			   ) THEN RAISE EXCEPTION 'hardware command ownership mismatch'; END IF;
		WHEN 'hardware_events' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id AND d.deleted_at IS NULL
			) OR (COALESCE(NEW.command_no, '') <> '' AND NOT (
				EXISTS (
					SELECT 1 FROM hardware_commands h
					WHERE h.command_no = NEW.command_no AND h.tenant_id = NEW.tenant_id AND h.device_id = NEW.device_id
				)
				OR (NEW.command_no LIKE 'VERIFY:%' AND EXISTS (
					SELECT 1 FROM device_verifications v
					WHERE 'VERIFY:' || v.request_id = NEW.command_no
					  AND v.tenant_id = NEW.tenant_id AND v.device_id = NEW.device_id
				))
			)) THEN RAISE EXCEPTION 'hardware event ownership mismatch'; END IF;
		WHEN 'device_alerts' THEN
			IF NEW.tenant_id = 0 OR NEW.scenic_area_id = 0 OR NOT EXISTS (
				SELECT 1 FROM devices d
				WHERE d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
				  AND d.scenic_area_id = NEW.scenic_area_id AND d.deleted_at IS NULL
			) THEN RAISE EXCEPTION 'device alert ownership mismatch'; END IF;
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
		WHEN 'ctrip_order_links' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a JOIN orders o ON o.id = NEW.order_id
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'ctrip'
				  AND o.tenant_id = NEW.tenant_id AND o.channel_account_id = NEW.channel_account_id AND o.order_no = NEW.supplier_order_id
			) THEN RAISE EXCEPTION 'ctrip order ownership mismatch'; END IF;
		WHEN 'ctrip_order_items' THEN
			IF NOT EXISTS (
				SELECT 1 FROM ctrip_order_links l JOIN order_items i ON i.id = NEW.order_item_id
				WHERE l.id = NEW.ctrip_order_link_id AND i.order_id = l.order_id
			) THEN RAISE EXCEPTION 'ctrip order item ownership mismatch'; END IF;
		WHEN 'ctrip_outbound_tasks' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a JOIN channel_product_mappings m ON m.channel_account_id = a.id
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'ctrip'
				  AND m.id = NEW.channel_product_mapping_id
			) THEN RAISE EXCEPTION 'ctrip outbound task ownership mismatch'; END IF;
		WHEN 'miniapp_customers' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
			) THEN RAISE EXCEPTION 'miniapp customer ownership mismatch'; END IF;
		WHEN 'xiaohongshu_webhook_events' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
			) THEN RAISE EXCEPTION 'xiaohongshu webhook ownership mismatch'; END IF;
		WHEN 'xiaohongshu_product_configs' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a JOIN channel_product_mappings m ON m.channel_account_id = a.id
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
				  AND m.id = NEW.channel_product_mapping_id
			) THEN RAISE EXCEPTION 'xiaohongshu product config ownership mismatch'; END IF;
		WHEN 'xiaohongshu_order_links' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM channel_accounts a JOIN miniapp_customers c ON c.channel_account_id = a.id
				JOIN orders o ON o.id = NEW.order_id
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
				  AND c.id = NEW.miniapp_customer_id AND c.tenant_id = NEW.tenant_id
				  AND o.tenant_id = NEW.tenant_id AND o.channel_account_id = NEW.channel_account_id
			) THEN RAISE EXCEPTION 'xiaohongshu order ownership mismatch'; END IF;
		WHEN 'xiaohongshu_order_operations' THEN
			IF NEW.tenant_id = 0 OR COALESCE(NEW.request_payload_ciphertext, '') = ''
			   OR NEW.attempt_count < 0
			   OR NEW.status NOT IN ('pending','remote_succeeded','completed')
			   OR (NEW.status IN ('remote_succeeded','completed') AND (COALESCE(NEW.platform_order_id, '') = '' OR COALESCE(NEW.pay_token_ciphertext, '') = '' OR NEW.pay_token_expires_at IS NULL))
			   OR ((NEW.status = 'completed') != (NEW.completed_at IS NOT NULL))
			   OR ((NEW.status IN ('pending','remote_succeeded')) != (NEW.next_attempt_at IS NOT NULL))
			   OR NOT EXISTS (
				SELECT 1 FROM xiaohongshu_order_links l
				JOIN channel_accounts a ON a.id = l.channel_account_id
				WHERE l.id = NEW.xiaohongshu_order_link_id AND l.tenant_id = NEW.tenant_id
				  AND l.channel_account_id = NEW.channel_account_id
				  AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
			) THEN RAISE EXCEPTION 'xiaohongshu order operation ownership mismatch'; END IF;
		WHEN 'xiaohongshu_booking_operations' THEN
			IF NEW.tenant_id = 0 OR COALESCE(NEW.request_payload_ciphertext, '') = ''
			   OR NEW.attempts < 0 OR NEW.max_attempts <= 0 OR NEW.attempts > NEW.max_attempts
			   OR COALESCE(NEW.failed_from_stage, '') NOT IN ('','pending','remote_succeeded','confirm_pending','compensation_pending')
			   OR (COALESCE(NEW.failed_from_stage, '') <> '' AND NEW.status <> 'failed')
			   OR (NEW.type = 'book' AND COALESCE(NEW.external_book_order_id, '') = '')
			   OR (NEW.type IN ('revoke','refund_status_sync') AND (COALESCE(NEW.external_book_order_id, '') = '' OR COALESCE(NEW.platform_book_id, '') = ''))
			   OR (NEW.type IN ('revoke','refund_status_sync') AND NEW.status NOT IN ('pending','remote_succeeded','completed','failed'))
			   OR (NEW.status IN ('remote_succeeded','confirm_pending','compensation_pending','completed') AND COALESCE(NEW.platform_book_id, '') = '')
			   OR ((NEW.status IN ('completed','failed')) != (NEW.completed_at IS NOT NULL))
			   OR ((NEW.status IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) != (NEW.next_attempt_at IS NOT NULL))
			   OR NOT EXISTS (
				SELECT 1
				FROM channel_accounts a
				JOIN xiaohongshu_order_links l ON l.channel_account_id = a.id
				JOIN scenic_hotel_package_entitlements e ON e.id = NEW.entitlement_id
				JOIN tickets t ON t.id = e.ticket_id
				WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
				  AND l.id = NEW.order_link_id AND l.tenant_id = NEW.tenant_id
				  AND e.sales_tenant_id = NEW.tenant_id AND e.order_id = l.order_id
				  AND (NEW.type <> 'refund_status_sync' OR (e.status = 'refunded' AND t.status = 'refunded'))
			   ) THEN RAISE EXCEPTION 'xiaohongshu booking operation ownership mismatch'; END IF;
		WHEN 'xiaohongshu_voucher_links' THEN
			IF NEW.tenant_id = 0 OR NOT EXISTS (
				SELECT 1 FROM xiaohongshu_order_links l JOIN tickets t ON t.id = NEW.ticket_id
				WHERE l.id = NEW.xiaohongshu_order_link_id AND l.tenant_id = NEW.tenant_id
				  AND l.channel_account_id = NEW.channel_account_id AND t.order_id = l.order_id
			) THEN RAISE EXCEPTION 'xiaohongshu voucher ownership mismatch'; END IF;
		WHEN 'xiaohongshu_voucher_verifications' THEN
			IF NEW.tenant_id = 0 OR NEW.channel_account_id = 0 OR NEW.voucher_link_id = 0
			   OR NEW.ticket_id = 0 OR NEW.device_verification_id = 0 OR NEW.device_id = 0 OR NEW.check_point_id = 0
			   OR COALESCE(NEW.request_id, '') = '' OR COALESCE(NEW.request_hash, '') = ''
			   OR NEW.attempt_count < 0
			   OR NEW.state NOT IN ('prepared','external_in_flight','external_unknown','external_confirmed','local_pending','local_completed','external_rejected','local_rejected','manual_review')
			   OR (NEW.state IN ('external_confirmed','local_pending','local_completed','manual_review') AND COALESCE(NEW.verify_id, '') = '')
			   OR (NEW.state = 'local_completed' AND (NEW.check_in_record_id = 0 OR NEW.local_completed_at IS NULL))
			   OR (NEW.state = 'manual_review' AND NEW.manual_review_at IS NULL)
			   OR NOT EXISTS (
					SELECT 1
					FROM channel_accounts a
					JOIN xiaohongshu_voucher_links l ON l.id = NEW.voucher_link_id
					JOIN xiaohongshu_order_links ol ON ol.id = l.xiaohongshu_order_link_id
					JOIN tickets t ON t.id = NEW.ticket_id
					JOIN device_verifications v ON v.id = NEW.device_verification_id
					JOIN devices d ON d.id = NEW.device_id
					JOIN check_points c ON c.id = NEW.check_point_id
					WHERE a.id = NEW.channel_account_id AND a.tenant_id = NEW.tenant_id AND a.type = 'xiaohongshu'
					  AND l.tenant_id = NEW.tenant_id AND l.channel_account_id = NEW.channel_account_id
					  AND l.ticket_id = NEW.ticket_id AND l.xiaohongshu_order_link_id = ol.id
					  AND ol.tenant_id = NEW.tenant_id AND ol.channel_account_id = NEW.channel_account_id
					  AND ol.order_id = t.order_id
					  AND t.tenant_id = NEW.tenant_id
					  AND v.tenant_id = NEW.tenant_id AND v.device_id = NEW.device_id
					  AND v.request_id = NEW.request_id AND v.ticket_code = t.ticket_code
					  AND v.scenic_area_id = c.scenic_area_id
					  AND d.id = NEW.device_id AND d.tenant_id = NEW.tenant_id
					  AND d.scenic_area_id = c.scenic_area_id AND d.check_point_id = c.id
					  AND c.tenant_id = NEW.tenant_id
				   )
			   OR (NEW.check_in_record_id <> 0 AND NOT EXISTS (
					SELECT 1 FROM check_in_records r
					WHERE r.id = NEW.check_in_record_id AND r.ticket_id = NEW.ticket_id
					  AND r.tenant_id = NEW.tenant_id AND r.device_id = NEW.device_id
					  AND r.check_point_id = NEW.check_point_id AND r.result = 'success'
				   )) THEN
				RAISE EXCEPTION 'xiaohongshu voucher verification ownership or state mismatch';
			END IF;
		END CASE;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql;`
	if err := db.Exec(function).Error; err != nil {
		return fmt.Errorf("create PostgreSQL ownership function: %w", err)
	}
	for _, table := range []string{"check_points", "devices", "device_maintenance_credentials", "device_maintenance_sessions", "device_provisioning_leases", "device_verifications", "device_request_nonces", "hardware_commands", "hardware_events", "device_alerts", "products", "product_inventories", "print_templates", "print_template_revisions", "print_jobs", "catalog_batch_change_plans", "catalog_batch_change_lines", "ai_usage_months", "ai_tenant_quota_policies", "agent_tasks", "agent_task_events", "agent_business_aliases", "hotel_properties", "hotel_room_types", "hotel_rate_plans", "hotel_rate_plan_prices", "hotel_room_inventories", "hotel_products", "hotel_product_revisions", "hotel_product_calendar_prices", "hotel_product_entitlements", "hotel_product_reservations", "scenic_hotel_packages", "scenic_hotel_package_entitlements", "hotel_reservations", "order_items", "fulfillment_orders", "tickets", "ticket_entitlements", "check_in_records", "order_visitors", "ctrip_order_links", "ctrip_order_items", "ctrip_outbound_tasks", "miniapp_customers", "xiaohongshu_product_configs", "xiaohongshu_order_links", "xiaohongshu_order_operations", "xiaohongshu_booking_operations", "xiaohongshu_voucher_links", "xiaohongshu_voucher_verifications", "xiaohongshu_webhook_events"} {
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
