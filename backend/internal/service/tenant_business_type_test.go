package service

import (
	"errors"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestSupplierBusinessTypesAreComposableAndRequireSupplierCapability(t *testing.T) {
	resetBusinessData(t)

	tenant := model.Tenant{Name: "Combined Resort", SystemCode: "RESORT-BUSINESS-TYPES", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	service := &TenantService{}
	if err := service.SetSupplierBusinessTypeAudited(tenant.ID, "hotel", "active", "enable hotel", 9, "platform_admin"); !errors.Is(err, ErrSupplierCapabilityRequired) {
		t.Fatalf("hotel enabled without supplier capability: %v", err)
	}
	expiredAt := time.Now().Add(-time.Hour)
	capability := model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active", ExpiresAt: &expiredAt}
	if err := model.DB.Create(&capability).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.SetSupplierBusinessTypeAudited(tenant.ID, "hotel", "active", "expired supplier", 9, "platform_admin"); !errors.Is(err, ErrSupplierCapabilityRequired) {
		t.Fatalf("hotel enabled with expired supplier capability: %v", err)
	}
	if err := model.DB.Model(&capability).Update("expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	for _, businessType := range []string{"scenic", "hotel"} {
		if err := service.SetSupplierBusinessTypeAudited(tenant.ID, businessType, "active", "platform approval", 9, "platform_admin"); err != nil {
			t.Fatalf("enable %s: %v", businessType, err)
		}
	}
	var rows []model.SupplierBusinessType
	if err := model.DB.Where("tenant_id = ?", tenant.ID).Order("business_type").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].BusinessType != "hotel" || rows[1].BusinessType != "scenic" || rows[0].Status != "active" || rows[1].Status != "active" {
		t.Fatalf("supplier business types=%+v", rows)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND action = ?", tenant.ID, "tenant.supplier_business_type.update").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("business type audit count=%d, want 2", auditCount)
	}
}

func TestSupplierBusinessTypeRequiresNonBlankAuditReason(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Reasoned Supplier", SystemCode: "REASONED-SUPPLIER-BUSINESS", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TenantService{}).SetSupplierBusinessTypeAudited(
		tenant.ID, "scenic", "active", "  \t\n", 9, "platform_admin",
	); !errors.Is(err, ErrAuditReasonRequired) {
		t.Fatalf("blank audit reason error=%v, want %v", err, ErrAuditReasonRequired)
	}
	var rows int64
	if err := model.DB.Model(&model.SupplierBusinessType{}).Where("tenant_id = ?", tenant.ID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("blank audit reason created %d supplier business rows", rows)
	}
}

func TestSupplierBusinessTypeDefaultsToSuspended(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Fail Closed Supplier", SystemCode: "FAIL-CLOSED-SUPPLIER", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	row := model.SupplierBusinessType{TenantID: tenant.ID, BusinessType: "hotel"}
	if err := model.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "suspended" {
		t.Fatalf("default supplier business type status=%q, want suspended", row.Status)
	}
}

func TestSupplierBusinessTypeFirstActivationIsConcurrentAndIdempotent(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Concurrent Supplier", SystemCode: "CONCURRENT-SUPPLIER-BUSINESS", Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}

	const requests = 8
	errorsByRequest := make(chan error, requests)
	var wait sync.WaitGroup
	for i := 0; i < requests; i++ {
		wait.Add(1)
		go func(actorID uint) {
			defer wait.Done()
			errorsByRequest <- (&TenantService{}).SetSupplierBusinessTypeAudited(
				tenant.ID, "scenic", "active", "concurrent platform approval", actorID, "platform_admin",
			)
		}(uint(i + 1))
	}
	wait.Wait()
	close(errorsByRequest)
	for err := range errorsByRequest {
		if err != nil {
			t.Fatalf("concurrent first activation failed: %v", err)
		}
	}

	var rowCount int64
	if err := model.DB.Model(&model.SupplierBusinessType{}).
		Where("tenant_id = ? AND business_type = ? AND status = ?", tenant.ID, "scenic", "active").
		Count(&rowCount).Error; err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("active scenic rows=%d, want 1", rowCount)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).
		Where("tenant_id = ? AND action = ?", tenant.ID, "tenant.supplier_business_type.update").
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != requests {
		t.Fatalf("concurrent activation audit rows=%d, want %d", auditCount, requests)
	}
}
