package model

import (
	"fmt"
	"gorm.io/gorm"
)

func migrateUpstreamSupplyFoundation(db *gorm.DB, previous int) error {
	if previous < 117 {
		// This release is the first upstream foundation: every pre-existing
		// item was fulfilled locally, including downstream Xiaohongshu sales.
		if err := db.Exec(`
			INSERT INTO order_item_supply_snapshots
			(order_item_id,sales_tenant_id,order_id,mode,fulfillment_tenant_id,scenic_area_id,product_id,product_revision_id,mapping_id,connection_id,provider,environment,external_product_code)
			SELECT i.id,o.tenant_id,i.order_id,'local',COALESCE(i.fulfillment_tenant_id,0),COALESCE(i.fulfillment_scenic_area_id,0),COALESCE(i.fulfillment_product_id,0),COALESCE(i.product_revision_id,0),0,0,'',o.environment,''
			FROM order_items i JOIN orders o ON o.id=i.order_id
			ON CONFLICT DO NOTHING;
			DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM order_items i LEFT JOIN order_item_supply_snapshots s ON s.order_item_id=i.id WHERE s.id IS NULL) THEN
				RAISE EXCEPTION 'upstream migration: order item has no owning order; migration aborted';
			END IF;
			END $$;
		`).Error; err != nil {
			return fmt.Errorf("backfill local supply snapshots: %w", err)
		}
	}
	return db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_external_admission_scope
		ON external_admission_credentials(tenant_id,scenic_area_id,provider,environment,payload_hash);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_mapping_draft
		ON upstream_product_mappings(tenant_id,product_id) WHERE status='draft' AND deleted_at IS NULL;
		CREATE OR REPLACE FUNCTION guard_upstream_supply() RETURNS trigger AS $$
		DECLARE p products%ROWTYPE; c upstream_connections%ROWTYPE; m upstream_product_mappings%ROWTYPE;
		BEGIN
		IF TG_TABLE_NAME='upstream_connections' THEN
			IF NEW.tenant_id=0 OR NOT EXISTS(SELECT 1 FROM tenants WHERE id=NEW.tenant_id)
			OR NEW.provider<>'zhiyoubao' OR NEW.environment NOT IN ('production','sandbox')
			OR NEW.status NOT IN ('draft','disabled') OR btrim(NEW.name)='' THEN
				RAISE EXCEPTION 'upstream connection is invalid or live activation is unavailable'; END IF;
			IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.provider,NEW.environment) IS DISTINCT FROM (OLD.tenant_id,OLD.provider,OLD.environment) THEN
				RAISE EXCEPTION 'upstream connection identity is immutable'; END IF;
			RETURN NEW;
		END IF;
		SELECT * INTO p FROM products WHERE id=NEW.product_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL;
		IF NOT FOUND OR p.product_kind<>'ticket' OR p.scenic_area_id=0 OR COALESCE(p.source_product_id,0)<>0 OR COALESCE(p.product_offer_id,0)<>0
		OR (COALESCE(p.fulfillment_tenant_id,0)<>0 AND p.fulfillment_tenant_id<>p.tenant_id) THEN
			RAISE EXCEPTION 'upstream supply requires an owned scenic ticket product'; END IF;
		IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.product_id) IS DISTINCT FROM (OLD.tenant_id,OLD.product_id) THEN
			RAISE EXCEPTION 'upstream product ownership is immutable'; END IF;
		IF TG_TABLE_NAME='product_supply_configs' THEN
			IF NEW.active_mapping_id IS NOT NULL THEN RAISE EXCEPTION 'upstream activation awaits verified adapter'; END IF;
		ELSE
			SELECT * INTO c FROM upstream_connections WHERE id=NEW.upstream_connection_id AND tenant_id=NEW.tenant_id AND deleted_at IS NULL;
			IF NOT FOUND OR c.status<>'draft' OR NEW.status<>'draft' OR btrim(NEW.external_product_code)='' THEN
				RAISE EXCEPTION 'upstream draft mapping is invalid'; END IF;
			IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM order_item_supply_snapshots WHERE mapping_id=OLD.id) THEN
				RAISE EXCEPTION 'sold upstream mapping is immutable'; END IF;
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
		IF TG_OP='UPDATE' AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'supply snapshot is immutable'; END IF;
		IF NEW.mode<>'local' OR NEW.mapping_id<>0 OR NEW.connection_id<>0 OR NEW.provider<>'' OR NEW.external_product_code<>'' THEN
			RAISE EXCEPTION 'upstream issuance is not enabled'; END IF;
		IF NOT EXISTS(SELECT 1 FROM order_items i JOIN orders o ON o.id=i.order_id
			WHERE i.id=NEW.order_item_id AND i.order_id=NEW.order_id AND o.tenant_id=NEW.sales_tenant_id
			AND COALESCE(i.fulfillment_tenant_id,0)=NEW.fulfillment_tenant_id AND COALESCE(i.fulfillment_scenic_area_id,0)=NEW.scenic_area_id
			AND COALESCE(i.fulfillment_product_id,0)=NEW.product_id AND COALESCE(i.product_revision_id,0)=NEW.product_revision_id
			AND o.environment=NEW.environment) THEN RAISE EXCEPTION 'supply snapshot ownership mismatch'; END IF;
		RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_supply_snapshot ON order_item_supply_snapshots;
		CREATE TRIGGER trg_supply_snapshot BEFORE INSERT OR UPDATE ON order_item_supply_snapshots FOR EACH ROW EXECUTE FUNCTION guard_supply_snapshot();

		CREATE OR REPLACE FUNCTION snapshot_local_order_supply() RETURNS trigger AS $$
		BEGIN
		IF EXISTS(SELECT 1 FROM product_supply_configs WHERE product_id=NEW.fulfillment_product_id AND tenant_id=NEW.fulfillment_tenant_id AND active_mapping_id IS NOT NULL) THEN
			RAISE EXCEPTION 'upstream supply is not enabled; local fallback denied'; END IF;
		INSERT INTO order_item_supply_snapshots
		(order_item_id,sales_tenant_id,order_id,mode,fulfillment_tenant_id,scenic_area_id,product_id,product_revision_id,mapping_id,connection_id,provider,environment,external_product_code)
		SELECT NEW.id,o.tenant_id,NEW.order_id,'local',COALESCE(NEW.fulfillment_tenant_id,0),COALESCE(NEW.fulfillment_scenic_area_id,0),COALESCE(NEW.fulfillment_product_id,0),COALESCE(NEW.product_revision_id,0),0,0,'',o.environment,''
		FROM orders o WHERE o.id=NEW.order_id;
		IF NOT FOUND THEN RAISE EXCEPTION 'supply snapshot requires owning order'; END IF;
		RETURN NEW;
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_order_item_supply_snapshot ON order_items;
		CREATE TRIGGER trg_order_item_supply_snapshot AFTER INSERT ON order_items FOR EACH ROW EXECUTE FUNCTION snapshot_local_order_supply();

		CREATE OR REPLACE FUNCTION guard_external_admission() RETURNS trigger AS $$
		BEGIN
		-- No vendor credential may be provisioned before the verified adapter.
		-- This prevents a test/manual binding from bypassing ordinary issuance.
		RAISE EXCEPTION 'external admission provisioning awaits verified adapter';
		END; $$ LANGUAGE plpgsql;
		DROP TRIGGER IF EXISTS trg_external_admission ON external_admission_credentials;
		CREATE TRIGGER trg_external_admission BEFORE INSERT OR UPDATE ON external_admission_credentials FOR EACH ROW EXECUTE FUNCTION guard_external_admission();
		DROP TRIGGER IF EXISTS trg_external_admission_binding ON external_admission_bindings;
		CREATE TRIGGER trg_external_admission_binding BEFORE INSERT OR UPDATE ON external_admission_bindings FOR EACH ROW EXECUTE FUNCTION guard_external_admission();
	`).Error
}
