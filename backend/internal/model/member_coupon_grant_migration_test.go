package model

import (
	"fmt"
	"testing"
	"time"

	"ticket-backend/internal/testdb"
)

func TestMemberCouponGrantMigration154OwnershipAndImmutableReceipts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := ChannelAccount{TenantID: 901, Code: "member-coupon-migration", Type: "wechat_miniapp", Status: "active", MemberMode: ChannelMemberModeFirstParty}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	template := CommerceCouponTemplate{TenantID: 901, ChannelAccountID: account.ID, BusinessType: "retail", Name: "Existing reward", Version: 1, DiscountCents: 123, ValidDays: 2, Status: "active", PerCustomerCap: 2, RefundReturnPolicy: "unfulfilled_full_refund_if_valid"}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	historical := CommerceCouponGrant{TenantID: 901, ChannelAccountID: account.ID, TemplateID: template.ID, BusinessType: "retail", CustomerID: "existing-wallet", Source: "manual", SourceIdentity: "old-manual", DiscountCents: 123, Status: "available", ExpiresAt: now.Add(48 * time.Hour), RefundReturnPolicy: template.RefundReturnPolicy}
	if err := db.Create(&historical).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate an existing schema153 installation, preserving its historical
	// template and grant, before adding only the new receipt tables/guards.
	if err := db.Migrator().DropTable(&MemberCouponGrantBatchItem{}, &MemberCouponGrantBatch{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", 154).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 153, Name: "previous schema", AppliedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	for _, table := range []interface{}{&MemberCouponGrantBatch{}, &MemberCouponGrantBatchItem{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing %T", table)
		}
	}
	var version int
	if err := db.Model(&SchemaMigration{}).Select("MAX(version)").Scan(&version).Error; err != nil || version != 154 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var unchanged CommerceCouponGrant
	if err := db.First(&unchanged, historical.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.CustomerID != historical.CustomerID || unchanged.DiscountCents != historical.DiscountCents || unchanged.SourceIdentity != historical.SourceIdentity || !unchanged.ExpiresAt.Equal(historical.ExpiresAt) {
		t.Fatalf("historical grant rewritten: %+v", unchanged)
	}
	member := TenantMember{TenantID: 901, MemberNo: "MEMBER-901", Status: "active", MembershipStatus: "active", FirstSeenAt: now, LastSeenAt: now}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	batch := MemberCouponGrantBatch{TenantID: 901, IdempotencyKey: "batch-key", RequestHash: fmt.Sprintf("%064d", 1), TemplateID: template.ID, ChannelAccountID: account.ID, ActorUserID: 88, ActorRole: "admin", Reason: "member care", MemberCount: 1, Status: "completed"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := batch
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate tenant key accepted")
	}
	invalid := batch
	invalid.ID = 0
	invalid.IdempotencyKey = "foreign"
	invalid.TenantID = 902
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("cross-tenant template accepted")
	}
	invalid = batch
	invalid.ID = 0
	invalid.IdempotencyKey = "oversized"
	invalid.MemberCount = 101
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("oversized batch accepted")
	}
	grant := historical
	grant.ID = 0
	grant.CustomerID = "member-wallet"
	grant.SourceIdentity = fmt.Sprintf("member-batch:%d", batch.ID)
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	item := MemberCouponGrantBatchItem{TenantID: 901, BatchID: batch.ID, MemberID: member.ID, CustomerID: grant.CustomerID, GrantID: grant.ID, MemberNo: member.MemberNo, DisplayName: "Member"}
	invalidItem := item
	invalidItem.GrantID = historical.ID
	if err := db.Create(&invalidItem).Error; err == nil {
		t.Fatal("unrelated historical grant accepted")
	}
	invalidItem = item
	invalidItem.TenantID = 902
	if err := db.Create(&invalidItem).Error; err == nil {
		t.Fatal("cross-tenant receipt item accepted")
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	duplicateItem := item
	duplicateItem.ID = 0
	if err := db.Create(&duplicateItem).Error; err == nil {
		t.Fatal("duplicate member/grant receipt accepted")
	}
	if err := db.Model(&batch).Update("reason", "rewritten").Error; err == nil {
		t.Fatal("batch rewrite accepted")
	}
	if err := db.Model(&item).Update("customer_id", "other-wallet").Error; err == nil {
		t.Fatal("item rewrite accepted")
	}
	if err := db.Unscoped().Delete(&item).Error; err == nil {
		t.Fatal("receipt deletion accepted")
	}
}
