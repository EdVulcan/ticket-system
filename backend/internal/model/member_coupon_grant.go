package model

import "gorm.io/gorm"

// MemberCouponGrantBatch is an immutable successful manual issuance receipt.
// It and every recipient/grant are committed together; incomplete attempts leave no row.
type MemberCouponGrantBatch struct {
	Base
	TenantID         uint   `gorm:"not null;uniqueIndex:idx_member_coupon_batch_key,priority:1"`
	IdempotencyKey   string `gorm:"size:160;not null;uniqueIndex:idx_member_coupon_batch_key,priority:2"`
	RequestHash      string `gorm:"size:64;not null"`
	TemplateID       uint   `gorm:"not null;index"`
	ChannelAccountID uint   `gorm:"not null;index"`
	ActorUserID      uint   `gorm:"not null"`
	ActorRole        string `gorm:"size:50;not null"`
	Reason           string `gorm:"size:500;not null"`
	MemberCount      int    `gorm:"not null;check:chk_member_coupon_batch_count,member_count BETWEEN 1 AND 100"`
	Status           string `gorm:"size:20;not null;check:chk_member_coupon_batch_status,status = 'completed'"`
}

type MemberCouponGrantBatchItem struct {
	Base
	TenantID    uint   `gorm:"not null;uniqueIndex:idx_member_coupon_item_recipient,priority:1"`
	BatchID     uint   `gorm:"not null;uniqueIndex:idx_member_coupon_item_recipient,priority:2;index"`
	MemberID    uint   `gorm:"not null;uniqueIndex:idx_member_coupon_item_recipient,priority:3"`
	CustomerID  string `gorm:"size:160;not null"`
	GrantID     uint   `gorm:"not null;uniqueIndex:idx_member_coupon_item_grant"`
	MemberNo    string `gorm:"size:64;not null"`
	DisplayName string `gorm:"size:120;not null"`
}

func applyMemberCouponGrantGuards(db *gorm.DB) error {
	return db.Exec(`
 CREATE OR REPLACE FUNCTION enforce_member_coupon_grant_receipt() RETURNS trigger AS $$
 BEGIN
  IF TG_OP <> 'INSERT' THEN RAISE EXCEPTION 'member coupon receipts are immutable'; END IF;
  IF TG_TABLE_NAME = 'member_coupon_grant_batches' THEN
   IF NEW.tenant_id = 0 OR NEW.template_id = 0 OR NEW.channel_account_id = 0 OR NEW.actor_user_id = 0
    OR length(NEW.idempotency_key) = 0 OR length(NEW.request_hash) <> 64
    OR NOT EXISTS (SELECT 1 FROM commerce_coupon_templates t JOIN channel_accounts a ON a.id=t.channel_account_id AND a.tenant_id=t.tenant_id WHERE t.id=NEW.template_id AND t.tenant_id=NEW.tenant_id AND t.channel_account_id=NEW.channel_account_id AND a.type='wechat_miniapp')
   THEN RAISE EXCEPTION 'member coupon batch ownership mismatch'; END IF;
  ELSE
   IF NEW.member_id = 0 OR length(NEW.customer_id)=0 OR NOT EXISTS (
    SELECT 1 FROM member_coupon_grant_batches b
    JOIN tenant_members m ON m.id=NEW.member_id AND m.tenant_id=b.tenant_id
    JOIN commerce_coupon_grants g ON g.id=NEW.grant_id AND g.tenant_id=b.tenant_id AND g.template_id=b.template_id AND g.channel_account_id=b.channel_account_id
    WHERE b.id=NEW.batch_id AND b.tenant_id=NEW.tenant_id AND g.customer_id=NEW.customer_id AND g.source='manual' AND g.source_identity='member-batch:' || b.id::text
   ) THEN RAISE EXCEPTION 'member coupon recipient ownership mismatch'; END IF;
  END IF;
  RETURN NEW;
 END; $$ LANGUAGE plpgsql;
 DROP TRIGGER IF EXISTS member_coupon_receipt_guard ON member_coupon_grant_batches;
 CREATE TRIGGER member_coupon_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON member_coupon_grant_batches FOR EACH ROW EXECUTE FUNCTION enforce_member_coupon_grant_receipt();
 DROP TRIGGER IF EXISTS member_coupon_receipt_guard ON member_coupon_grant_batch_items;
 CREATE TRIGGER member_coupon_receipt_guard BEFORE INSERT OR UPDATE OR DELETE ON member_coupon_grant_batch_items FOR EACH ROW EXECUTE FUNCTION enforce_member_coupon_grant_receipt();
 `).Error
}
