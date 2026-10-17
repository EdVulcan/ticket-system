package model

import (
	"fmt"

	"gorm.io/gorm"
)

// AutoMigrate adds the default-false policies and empty ciphertext columns.
// Historical revisions and orders are deliberately not inferred from current
// products. Only new orders may collect contact identity.
func applyMiniappOrderContactGuards(db *gorm.DB) error {
	if err := db.Exec(`CREATE OR REPLACE FUNCTION enforce_miniapp_order_contact_snapshot() RETURNS trigger AS $$
	BEGIN
		IF TG_TABLE_NAME = 'orders' THEN
			IF (NEW.contact_real_name_required AND (NEW.channel <> 'xiaohongshu' OR NEW.contact_id_card_ciphertext = ''))
				OR (NOT NEW.contact_real_name_required AND NEW.contact_id_card_ciphertext <> '') THEN
				RAISE EXCEPTION 'invalid order contact identity snapshot';
			END IF;
			IF TG_OP = 'UPDATE' AND (
				NEW.contact_real_name_required IS DISTINCT FROM OLD.contact_real_name_required
				OR NEW.contact_id_card_ciphertext IS DISTINCT FROM OLD.contact_id_card_ciphertext
			) THEN
				RAISE EXCEPTION 'order contact identity snapshot is immutable';
			END IF;
		ELSIF TG_TABLE_NAME = 'product_revisions' AND TG_OP = 'UPDATE' THEN
			IF NEW.contact_real_name_required IS DISTINCT FROM OLD.contact_real_name_required THEN
				RAISE EXCEPTION 'product revision contact policy is immutable';
			END IF;
		END IF;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql`).Error; err != nil {
		return fmt.Errorf("create order contact snapshot guard: %w", err)
	}
	for _, table := range []string{"orders", "product_revisions"} {
		if err := db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS miniapp_order_contact_snapshot_guard ON %s;
			CREATE TRIGGER miniapp_order_contact_snapshot_guard BEFORE INSERT OR UPDATE ON %s
			FOR EACH ROW EXECUTE FUNCTION enforce_miniapp_order_contact_snapshot()`, table, table)).Error; err != nil {
			return fmt.Errorf("install contact snapshot guard on %s: %w", table, err)
		}
	}
	return nil
}
