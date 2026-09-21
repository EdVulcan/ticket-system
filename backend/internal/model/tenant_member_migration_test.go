package model

import (
	"testing"
	"time"

	"ticket-backend/internal/testdb"
)

func TestTenantMemberCenterMigrationAndOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("re-running member migration: %v", err)
	}

	for _, table := range []interface{}{
		&TenantMember{}, &TenantMemberIdentity{}, &TenantMemberVerifiedContact{},
		&TenantMemberConsent{}, &TenantMemberEvent{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("member table for %T is missing", table)
		}
	}
	for _, field := range []struct {
		model interface{}
		name  string
	}{
		{&Order{}, "MemberID"},
		{&CommerceOrder{}, "MemberID"},
	} {
		if !db.Migrator().HasColumn(field.model, field.name) {
			t.Fatalf("member column %s for %T is missing", field.name, field.model)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&TenantMember{}, "idx_tenant_member_no"},
		{&TenantMemberIdentity{}, "idx_tenant_member_identity_unique"},
		{&TenantMemberVerifiedContact{}, "idx_tenant_member_verified_contacts_active_value"},
		{&TenantMemberConsent{}, "idx_tenant_member_consents_idempotency"},
		{&TenantMemberEvent{}, "idx_tenant_member_events_idempotency"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("member index %s is missing", index.name)
		}
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	memberA := TenantMember{
		TenantID: 901, MemberNo: "M-901-0001", Status: TenantMemberStatusActive,
		MembershipStatus: TenantMembershipStatusProvisional, FirstSeenAt: now, LastSeenAt: now,
	}
	if err := db.Create(&memberA).Error; err != nil {
		t.Fatalf("create member A: %v", err)
	}
	if err := db.Create(&TenantMember{TenantID: 901, MemberNo: memberA.MemberNo, Status: TenantMemberStatusActive, MembershipStatus: TenantMembershipStatusProvisional, FirstSeenAt: now, LastSeenAt: now}).Error; err == nil {
		t.Fatal("duplicate member number in one tenant was accepted")
	}
	memberB := TenantMember{
		TenantID: 902, MemberNo: "M-902-0001", Status: TenantMemberStatusActive,
		MembershipStatus: TenantMembershipStatusActive, FirstSeenAt: now, LastSeenAt: now,
	}
	if err := db.Create(&memberB).Error; err != nil {
		t.Fatalf("same member number scope setup failed: %v", err)
	}

	identity := TenantMemberIdentity{
		TenantID: 901, MemberID: memberA.ID, Provider: "wechat_miniapp", ChannelAccountID: 9011,
		SubjectBlindIndex: "blind-subject-a", BlindIndexVersion: 1,
		Status: TenantMemberIdentityStatusActive, LinkMethod: "first_login", LinkedAt: now, LastSeenAt: now,
	}
	if err := db.Create(&identity).Error; err != nil {
		t.Fatalf("create member identity: %v", err)
	}
	duplicateIdentity := identity
	duplicateIdentity.Base = Base{}
	duplicateIdentity.SubjectBlindIndex = identity.SubjectBlindIndex
	if err := db.Create(&duplicateIdentity).Error; err == nil {
		t.Fatal("duplicate identity in one tenant was accepted")
	}
	crossTenantIdentity := identity
	crossTenantIdentity.Base = Base{}
	crossTenantIdentity.TenantID = memberB.TenantID
	crossTenantIdentity.MemberID = memberA.ID
	crossTenantIdentity.SubjectBlindIndex = "blind-subject-cross-tenant"
	if err := db.Create(&crossTenantIdentity).Error; err == nil {
		t.Fatal("identity was allowed to reference a member in another tenant")
	}

	contact := TenantMemberVerifiedContact{
		TenantID: 901, MemberID: memberA.ID, ContactType: TenantMemberContactTypePhone,
		ValueCiphertext: "encrypted-phone-a", ValueBlindIndex: "phone-blind-a", BlindIndexVersion: 1,
		EncryptionKeyVersion: 1, VerificationMethod: "wechat_phone", VerifiedAt: now,
		Status: TenantMemberContactStatusActive,
	}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatalf("create verified contact: %v", err)
	}
	duplicateContact := contact
	duplicateContact.Base = Base{}
	duplicateContact.MemberID = memberA.ID
	if err := db.Create(&duplicateContact).Error; err == nil {
		t.Fatal("duplicate active verified phone was accepted")
	}
	if err := db.Model(&contact).Update("status", TenantMemberContactStatusRevoked).Error; err != nil {
		t.Fatalf("revoke verified contact: %v", err)
	}
	contactReplacement := contact
	contactReplacement.Base = Base{}
	contactReplacement.TenantID = memberB.TenantID
	contactReplacement.MemberID = memberB.ID
	if err := db.Create(&contactReplacement).Error; err != nil {
		t.Fatalf("revoked phone could not be reused: %v", err)
	}

	order := Order{OrderNo: "MEMBER-MIGRATION-ORDER", TenantID: 901, MemberID: &memberA.ID}
	if err := db.Create(&order).Error; err != nil {
		t.Fatalf("create member-owned legacy order: %v", err)
	}
	crossTenantOrder := Order{OrderNo: "MEMBER-MIGRATION-CROSS-TENANT", TenantID: 902, MemberID: &memberA.ID}
	if err := db.Create(&crossTenantOrder).Error; err == nil {
		t.Fatal("cross-tenant order member reference was accepted")
	}
	legacyOrder := Order{OrderNo: "MEMBER-MIGRATION-LEGACY", TenantID: 901}
	if err := db.Create(&legacyOrder).Error; err != nil {
		t.Fatalf("legacy order without member association was rejected: %v", err)
	}

	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%+v, want 142", latest)
	}
}
