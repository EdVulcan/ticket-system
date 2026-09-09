package model

import (
	"strings"
	"testing"
	"ticket-backend/internal/testdb"
	"time"
)

func TestPostgresSchema116MiniappPromotionUpgradeRerunAndOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	// Recreate the precise pre-116 boundary in this isolated database. The
	// current-model AutoMigrate pass restores tables/columns; the version-gated
	// migration must then restore checks, partial indexes, and ownership guards.
	for _, table := range []interface{}{
		&MiniappInstantDiscountGrant{}, &MiniappInstantDiscountActivityMapping{}, &MiniappInstantDiscountActivity{},
	} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"original_amount_cents", "discount_cents", "promotion_grant_id"} {
		if err := db.Exec("ALTER TABLE orders DROP COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"order_items", "tickets"} {
		if err := db.Exec("ALTER TABLE " + table + " DROP COLUMN sale_amount_cents").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version >= ?", 116).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 115, Name: "xiaohongshu voucher issuance readiness", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []interface{}{
		&MiniappInstantDiscountActivity{}, &MiniappInstantDiscountActivityMapping{}, &MiniappInstantDiscountGrant{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table for %T", table)
		}
	}
	for _, column := range []string{"original_amount_cents", "discount_cents", "promotion_grant_id"} {
		if !db.Migrator().HasColumn(&Order{}, column) {
			t.Fatalf("orders missing %s", column)
		}
	}
	for _, target := range []struct {
		model interface{}
		name  string
	}{
		{&Order{}, "idx_orders_promotion_grant_id"},
		{&MiniappInstantDiscountActivity{}, "idx_miniapp_discount_account"},
		{&MiniappInstantDiscountActivityMapping{}, "idx_miniapp_discount_activity_mapping"},
		{&MiniappInstantDiscountGrant{}, "idx_miniapp_discount_reserved_order"},
		{&MiniappInstantDiscountGrant{}, "idx_miniapp_discount_grant_customer"},
	} {
		if !db.Migrator().HasIndex(target.model, target.name) {
			t.Fatalf("missing index %s", target.name)
		}
	}

	first := Tenant{Name: "Promotion Guard A", SystemCode: "PROMOTION-GUARD-A", SecretKey: "promotion-a", Status: "active"}
	second := Tenant{Name: "Promotion Guard B", SystemCode: "PROMOTION-GUARD-B", SecretKey: "promotion-b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	firstAccount := ChannelAccount{TenantID: first.ID, Code: "promotion-guard-a", Type: "xiaohongshu", AppID: "promotion-guard-app-a", Status: "sandbox", Environment: "sandbox"}
	secondAccount := ChannelAccount{TenantID: second.ID, Code: "promotion-guard-b", Type: "xiaohongshu", AppID: "promotion-guard-app-b", Status: "sandbox", Environment: "sandbox"}
	if err := db.Create(&firstAccount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondAccount).Error; err != nil {
		t.Fatal(err)
	}
	customer := MiniappCustomer{TenantID: first.ID, ChannelAccountID: firstAccount.ID, OpenIDHash: strings.Repeat("a", 64), OpenIDCiphertext: "sealed-open", SessionKeyCiphertext: "sealed-session", SessionTokenHash: strings.Repeat("b", 64), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	activity := MiniappInstantDiscountActivity{TenantID: first.ID, ChannelAccountID: firstAccount.ID, Enabled: true, MinDiscountCents: 1, MaxDiscountCents: 3, ValidityMinutes: 30, CooldownDays: 1}
	if err := db.Create(&activity).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	grant := MiniappInstantDiscountGrant{TenantID: first.ID, ChannelAccountID: firstAccount.ID, MiniappCustomerID: customer.ID, ActivityID: activity.ID, DiscountCents: 2, ObtainedAt: now, ExpiresAt: now.Add(30 * time.Minute), NextEligibleAt: now.Add(24 * time.Hour)}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Create(&MiniappInstantDiscountActivity{TenantID: first.ID, ChannelAccountID: secondAccount.ID, Enabled: true, MinDiscountCents: 1, MaxDiscountCents: 1, ValidityMinutes: 1, CooldownDays: 0}).Error; err == nil {
		t.Fatal("cross-tenant channel activity was accepted")
	}
	if err := db.Create(&MiniappInstantDiscountActivityMapping{ActivityID: activity.ID, ChannelProductMappingID: 999999}).Error; err == nil {
		t.Fatal("orphan promotion mapping was accepted")
	}
	if err := db.Create(&MiniappInstantDiscountGrant{TenantID: first.ID, ChannelAccountID: secondAccount.ID, MiniappCustomerID: customer.ID, ActivityID: activity.ID, DiscountCents: 1, ObtainedAt: now, ExpiresAt: now.Add(time.Minute), NextEligibleAt: now.Add(time.Hour)}).Error; err == nil {
		t.Fatal("cross-account promotion grant was accepted")
	}
	if err := db.Model(&grant).Update("discount_cents", 3).Error; err == nil {
		t.Fatal("promotion grant snapshot mutation was accepted")
	}
	if err := db.Create(&MiniappInstantDiscountActivity{TenantID: first.ID, ChannelAccountID: firstAccount.ID, Enabled: true, MinDiscountCents: 4, MaxDiscountCents: 3, ValidityMinutes: 1, CooldownDays: 0}).Error; err == nil {
		t.Fatal("invalid activity discount range was accepted")
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil || latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
