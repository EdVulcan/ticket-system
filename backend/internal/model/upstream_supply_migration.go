package model

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateUpstreamSupplyFoundation applies the single schema 118 extension.
// Existing order items are backfilled as local snapshots exactly once.
func migrateUpstreamSupplyFoundation(db *gorm.DB, previous int) error {
	if previous < 118 {
		if err := db.Exec(`
			INSERT INTO order_item_supply_snapshots
			(order_item_id,sales_tenant_id,order_id,mode,fulfillment_tenant_id,scenic_area_id,product_id,product_revision_id,mapping_id,connection_id,provider,environment,external_product_code,issue_status)
			SELECT i.id,o.tenant_id,i.order_id,'local',COALESCE(i.fulfillment_tenant_id,0),COALESCE(i.fulfillment_scenic_area_id,0),COALESCE(i.fulfillment_product_id,0),COALESCE(i.product_revision_id,0),0,0,'',COALESCE(o.environment,'production'),'','local_ready'
			FROM order_items i JOIN orders o ON o.id=i.order_id
			WHERE NOT EXISTS (SELECT 1 FROM order_item_supply_snapshots s WHERE s.order_item_id=i.id)
			ON CONFLICT DO NOTHING;
		`).Error; err != nil {
			return fmt.Errorf("backfill local supply snapshots: %w", err)
		}
		var orphan int64
		if err := db.Raw(`SELECT COUNT(*) FROM order_items i LEFT JOIN order_item_supply_snapshots s ON s.order_item_id=i.id WHERE s.id IS NULL`).Scan(&orphan).Error; err != nil {
			return fmt.Errorf("verify supply snapshot backfill: %w", err)
		}
		if orphan != 0 {
			return fmt.Errorf("upstream migration: %d order items have no owning order", orphan)
		}
	}
	return db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_mapping_draft
		ON upstream_product_mappings(tenant_id,product_id) WHERE status='draft' AND deleted_at IS NULL;

		CREATE OR REPLACE FUNCTION guard_upstream_supply() RETURNS trigger AS $$
		DECLARE p products%ROWTYPE; c upstream_connections%ROWTYPE; m upstream_product_mappings%ROWTYPE;
		BEGIN
		IF TG_TABLE_NAME='upstream_connections' THEN
			IF NEW.tenant_id=0 OR NOT EXISTS(SELECT 1 FROM tenants WHERE id=NEW.tenant_id AND deleted_at IS NULL)
			OR NEW.provider<>'zhiyoubao' OR NEW.environment NOT IN ('production','sandbox')
			OR NEW.status NOT IN ('draft','active','disabled') OR btrim(NEW.name)='' THEN
				RAISE EXCEPTION 'upstream connection is invalid';
			END IF;
			IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.provider,NEW.environment) IS DISTINCT FROM (OLD.tenant_id,OLD.provider,OLD.environment) THEN
				RAISE EXCEPTION 'upstream connection identity is immutable';
			END IF;
			IF NEW.status='active' AND (btrim(NEW.endpoint)='' OR NEW.endpoint !~* '^https://' OR btrim(NEW.corp_code)='' OR btrim(NEW.username)='' OR btrim(NEW.private_key_ciphertext)='') THEN
				RAISE EXCEPTION 'active upstream connection requires complete credentials';
			END IF;
			RETURN NEW;
		END IF;
		SELECT * INTO p FROM products WHERE id=NEW.product_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL;
		IF NOT FOUND OR p.product_kind<>'ticket' OR p.scenic_area_id=0 OR COALESCE(p.source_product_id,0)<>0 OR COALESCE(p.product_offer_id,0)<>0
		OR (COALESCE(p.fulfillment_tenant_id,0)<>0 AND p.fulfillment_tenant_id<>p.tenant_id) THEN
			RAISE EXCEPTION 'upstream supply requires an owned scenic ticket product';
		END IF;
		IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.product_id) IS DISTINCT FROM (OLD.tenant_id,OLD.product_id) THEN
			RAISE EXCEPTION 'upstream product ownership is immutable';
		END IF;
		IF TG_TABLE_NAME='product_supply_configs' THEN
			IF NEW.enabled AND p.type='offline' THEN RAISE EXCEPTION 'window products cannot enable upstream supply'; END IF;
			IF NEW.enabled AND NEW.active_mapping_id IS NULL THEN RAISE EXCEPTION 'enabled supply requires a mapping'; END IF;
			IF NEW.active_mapping_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM upstream_product_mappings WHERE id=NEW.active_mapping_id AND tenant_id=NEW.tenant_id AND product_id=NEW.product_id AND status='active' AND deleted_at IS NULL) THEN
				RAISE EXCEPTION 'enabled supply mapping is invalid';
			END IF;
		ELSE
			SELECT * INTO c FROM upstream_connections WHERE id=NEW.upstream_connection_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL;
			IF NOT FOUND OR btrim(NEW.external_product_code)='' OR NEW.status NOT IN ('draft','active','disabled') THEN
				RAISE EXCEPTION 'upstream mapping is invalid';
			END IF;
			IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.product_id,NEW.upstream_connection_id) IS DISTINCT FROM (OLD.tenant_id,OLD.product_id,OLD.upstream_connection_id) AND EXISTS(SELECT 1 FROM order_item_supply_snapshots WHERE mapping_id=OLD.id) THEN
				RAISE EXCEPTION 'sold upstream mapping identity is immutable';
			END IF;
		END IF;
		RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_upstream_connection ON upstream_connections;
		CREATE TRIGGER trg_upstream_connection BEFORE INSERT OR UPDATE ON upstream_connections FOR EACH ROW EXECUTE FUNCTION guard_upstream_supply();
		DROP TRIGGER IF EXISTS trg_upstream_mapping ON upstream_product_mappings;
		CREATE TRIGGER trg_upstream_mapping BEFORE INSERT OR UPDATE ON upstream_product_mappings FOR EACH ROW EXECUTE FUNCTION guard_upstream_supply();
		DROP TRIGGER IF EXISTS trg_product_supply ON product_supply_configs;
		CREATE TRIGGER trg_product_supply BEFORE INSERT OR UPDATE ON product_supply_configs FOR EACH ROW EXECUTE FUNCTION guard_upstream_supply();

		CREATE OR REPLACE FUNCTION guard_supply_snapshot() RETURNS trigger AS $$
		BEGIN
		IF TG_OP='UPDATE' AND (
			NEW.order_item_id IS DISTINCT FROM OLD.order_item_id OR NEW.sales_tenant_id IS DISTINCT FROM OLD.sales_tenant_id OR NEW.order_id IS DISTINCT FROM OLD.order_id OR NEW.mode IS DISTINCT FROM OLD.mode OR NEW.fulfillment_tenant_id IS DISTINCT FROM OLD.fulfillment_tenant_id OR NEW.scenic_area_id IS DISTINCT FROM OLD.scenic_area_id OR NEW.product_id IS DISTINCT FROM OLD.product_id OR NEW.product_revision_id IS DISTINCT FROM OLD.product_revision_id OR NEW.mapping_id IS DISTINCT FROM OLD.mapping_id OR NEW.connection_id IS DISTINCT FROM OLD.connection_id OR NEW.provider IS DISTINCT FROM OLD.provider OR NEW.environment IS DISTINCT FROM OLD.environment OR NEW.external_product_code IS DISTINCT FROM OLD.external_product_code) THEN
			RAISE EXCEPTION 'supply snapshot identity is immutable';
		END IF;
		IF TG_OP='UPDATE' AND OLD.request_payload_ciphertext<>'' AND NEW.request_payload_ciphertext IS DISTINCT FROM OLD.request_payload_ciphertext THEN RAISE EXCEPTION 'supply request payload is immutable'; END IF;
		IF TG_OP='UPDATE' AND ((OLD.provider_order_code<>'' AND NEW.provider_order_code IS DISTINCT FROM OLD.provider_order_code) OR (OLD.provider_sub_order_code<>'' AND NEW.provider_sub_order_code IS DISTINCT FROM OLD.provider_sub_order_code)) THEN RAISE EXCEPTION 'provider order identity is immutable'; END IF;
		IF NEW.mode NOT IN ('local','upstream') OR NEW.issue_status NOT IN ('local_ready','pending','issuing','ready','failed') OR NEW.cancel_status NOT IN ('','pending','submitted','succeeded','failed','override') THEN RAISE EXCEPTION 'supply snapshot state is invalid'; END IF;
		IF NOT EXISTS(SELECT 1 FROM order_items i JOIN orders o ON o.id=i.order_id WHERE i.id=NEW.order_item_id AND i.order_id=NEW.order_id AND o.tenant_id=NEW.sales_tenant_id AND COALESCE(i.fulfillment_tenant_id,0)=NEW.fulfillment_tenant_id AND COALESCE(i.fulfillment_scenic_area_id,0)=NEW.scenic_area_id AND COALESCE(i.fulfillment_product_id,0)=NEW.product_id AND COALESCE(i.product_revision_id,0)=NEW.product_revision_id AND o.environment=NEW.environment) THEN RAISE EXCEPTION 'supply snapshot ownership mismatch'; END IF;
		RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_supply_snapshot ON order_item_supply_snapshots;
		CREATE TRIGGER trg_supply_snapshot BEFORE INSERT OR UPDATE ON order_item_supply_snapshots FOR EACH ROW EXECUTE FUNCTION guard_supply_snapshot();

		CREATE OR REPLACE FUNCTION snapshot_order_item_supply() RETURNS trigger AS $$
		DECLARE o orders%ROWTYPE; cfg product_supply_configs%ROWTYPE; map upstream_product_mappings%ROWTYPE; conn upstream_connections%ROWTYPE; mode_value text := 'local'; issue_value text := 'local_ready';
		BEGIN
		SELECT * INTO o FROM orders WHERE id=NEW.order_id;
		IF NOT FOUND THEN RAISE EXCEPTION 'supply snapshot requires owning order'; END IF;
		SELECT * INTO cfg FROM product_supply_configs WHERE o.channel<>'window' AND tenant_id=NEW.fulfillment_tenant_id AND product_id=NEW.fulfillment_product_id AND enabled=true AND active_mapping_id IS NOT NULL AND deleted_at IS NULL;
		IF FOUND THEN
			SELECT * INTO map FROM upstream_product_mappings WHERE id=cfg.active_mapping_id AND tenant_id=cfg.tenant_id AND product_id=cfg.product_id AND status='active' AND deleted_at IS NULL;
			IF NOT FOUND THEN RAISE EXCEPTION 'active upstream mapping is unavailable'; END IF;
			SELECT * INTO conn FROM upstream_connections WHERE id=map.upstream_connection_id AND tenant_id=map.tenant_id AND status='active' AND deleted_at IS NULL;
			IF NOT FOUND OR conn.environment<>o.environment OR btrim(conn.endpoint)='' OR conn.endpoint !~* '^https://' OR btrim(conn.corp_code)='' OR btrim(conn.username)='' OR btrim(conn.private_key_ciphertext)='' THEN RAISE EXCEPTION 'active upstream connection is unavailable'; END IF;
			mode_value := 'upstream'; issue_value := 'pending';
		END IF;
		INSERT INTO order_item_supply_snapshots (order_item_id,sales_tenant_id,order_id,mode,fulfillment_tenant_id,scenic_area_id,product_id,product_revision_id,mapping_id,connection_id,provider,environment,external_product_code,issue_status)
		VALUES (NEW.id,o.tenant_id,NEW.order_id,mode_value,COALESCE(NEW.fulfillment_tenant_id,0),COALESCE(NEW.fulfillment_scenic_area_id,0),COALESCE(NEW.fulfillment_product_id,0),COALESCE(NEW.product_revision_id,0),CASE WHEN mode_value='upstream' THEN map.id ELSE 0 END,CASE WHEN mode_value='upstream' THEN conn.id ELSE 0 END,CASE WHEN mode_value='upstream' THEN conn.provider ELSE '' END,o.environment,CASE WHEN mode_value='upstream' THEN map.external_product_code ELSE '' END,issue_value)
		ON CONFLICT (order_item_id) DO NOTHING;
		RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_order_item_supply_snapshot ON order_items;
		CREATE TRIGGER trg_order_item_supply_snapshot AFTER INSERT ON order_items FOR EACH ROW EXECUTE FUNCTION snapshot_order_item_supply();

		-- Preserve the historical placeholder protections. The new adapter
		-- writes the ordinary ticket code and never provisions these tables.
		CREATE UNIQUE INDEX IF NOT EXISTS idx_external_admission_scope
		ON external_admission_credentials(tenant_id,scenic_area_id,provider,environment,payload_hash);
		CREATE OR REPLACE FUNCTION guard_external_admission() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'external admission provisioning is not used; use the ordinary ticket code';
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_external_admission ON external_admission_credentials;
		CREATE TRIGGER trg_external_admission BEFORE INSERT OR UPDATE ON external_admission_credentials FOR EACH ROW EXECUTE FUNCTION guard_external_admission();
		DROP TRIGGER IF EXISTS trg_external_admission_binding ON external_admission_bindings;
		CREATE TRIGGER trg_external_admission_binding BEFORE INSERT OR UPDATE ON external_admission_bindings FOR EACH ROW EXECUTE FUNCTION guard_external_admission();
	`).Error
}
