package model

import (
	"testing"
	"ticket-backend/internal/testdb"
)

func TestPostgresSchema98AgentBusinessAliasOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 98 || !db.Migrator().HasTable(&AgentBusinessAlias{}) {
		t.Fatalf("schema=%d alias table missing", CurrentPostgresSchemaVersion)
	}
	tenant := Tenant{Name: "Alias Guard Tenant", SystemCode: "ALIAS-GUARD-TENANT", SecretKey: "alias", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	valid := AgentBusinessAlias{TenantID: tenant.ID, Kind: "checkpoint", Alias: "8号点", CanonicalName: "Main Gate"}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("valid alias rejected: %v", err)
	}
	invalidKind := valid
	invalidKind.ID = 0
	invalidKind.Alias = "bad-kind"
	invalidKind.Kind = "order"
	if err := db.Create(&invalidKind).Error; err == nil {
		t.Fatal("unsupported alias kind was accepted")
	}
	invalidTenant := valid
	invalidTenant.ID = 0
	invalidTenant.Alias = "foreign"
	invalidTenant.TenantID = tenant.ID + 999
	if err := db.Create(&invalidTenant).Error; err == nil {
		t.Fatal("alias for unknown tenant was accepted")
	}
}
