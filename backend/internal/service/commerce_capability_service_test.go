package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func resetCommerceCapabilityData(t *testing.T) {
	t.Helper()
	resetBusinessData(t)
	if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&model.TenantBusinessCapability{}).Error; err != nil {
		t.Fatalf("reset commercial capabilities: %v", err)
	}
}

func TestBusinessCapabilityLifecycleAndAudit(t *testing.T) {
	resetCommerceCapabilityData(t)
	tenant := model.Tenant{Name: "Commerce Tenant", SystemCode: "COMMERCE-CAPABILITY", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	svc := &TenantService{}
	if err := svc.SetBusinessCapabilityAudited(tenant.ID, "restaurant", "active", "enable restaurant", 7, "platform_admin"); err != nil {
		t.Fatalf("enable capability: %v", err)
	}
	var row model.TenantBusinessCapability
	if err := model.DB.Where("tenant_id = ? AND business_type = ?", tenant.ID, "restaurant").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "active" || row.EnabledAt == nil || row.SuspendedAt != nil {
		t.Fatalf("active capability=%+v", row)
	}
	if err := svc.SetBusinessCapabilityAudited(tenant.ID, "restaurant", "suspended", "pause restaurant", 7, "platform_admin"); err != nil {
		t.Fatalf("suspend capability: %v", err)
	}
	if err := model.DB.First(&row, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "suspended" || row.SuspendedAt == nil {
		t.Fatalf("suspended capability=%+v", row)
	}
	if err := RequireActiveTenantBusinessCapability(model.DB, tenant.ID, "restaurant"); !errors.Is(err, ErrBusinessCapabilityInactive) {
		t.Fatalf("suspended write authorization=%v", err)
	}
	if err := RequireConfiguredTenantBusinessCapability(model.DB, tenant.ID, "restaurant"); err != nil {
		t.Fatalf("suspended history authorization: %v", err)
	}
	var audits int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ?", tenant.ID, "tenant.business_capability.update").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("audit rows=%d, want 2", audits)
	}
}

func TestBusinessCapabilityValidationFailsClosed(t *testing.T) {
	resetCommerceCapabilityData(t)
	tenant := model.Tenant{Name: "Commerce Validation", SystemCode: "COMMERCE-CAPABILITY-VALIDATION", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	svc := &TenantService{}
	cases := []struct {
		name string
		kind error
		args []string
	}{
		{"type", ErrBusinessCapabilityInvalid, []string{"hotel", "active", "reason"}},
		{"status", ErrBusinessCapabilityStatus, []string{"retail", "pending", "reason"}},
		{"reason", ErrAuditReasonRequired, []string{"retail", "active", " "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.SetBusinessCapabilityAudited(tenant.ID, tc.args[0], tc.args[1], tc.args[2], 1, "platform_admin")
			if !errors.Is(err, tc.kind) {
				t.Fatalf("error=%v, want %v", err, tc.kind)
			}
		})
	}
	if err := svc.SetBusinessCapabilityAudited(999999, "retail", "active", "reason", 1, "platform_admin"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing tenant error=%v", err)
	}
	if err := RequireActiveTenantBusinessCapability(model.DB, tenant.ID, "retail"); !errors.Is(err, ErrBusinessCapabilityInactive) {
		t.Fatalf("missing capability authorization=%v", err)
	}
}
