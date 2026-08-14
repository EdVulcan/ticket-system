package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

var (
	ErrTenantUnavailable            = errors.New("tenant is unavailable")
	ErrCapabilityInactive           = errors.New("tenant capability is not active")
	ErrSupplierBusinessTypeInactive = errors.New("supplier business type is not active")
)

func requireActiveTenant(tx *gorm.DB, tenantID uint) error {
	if tenantID == 0 {
		return ErrTenantUnavailable
	}
	var count int64
	if err := tx.Model(&model.Tenant{}).Where("id = ? AND status = ?", tenantID, "active").Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrTenantUnavailable
	}
	return nil
}

// requireActiveTenantCapability deliberately fails closed. Legacy tenants are
// migrated to explicit capabilities; a missing row is never authorization.
func requireActiveTenantCapability(tx *gorm.DB, tenantID uint, capability string) error {
	if err := requireActiveTenant(tx, tenantID); err != nil {
		return err
	}
	var row model.TenantCapability
	err := tx.Where("tenant_id = ? AND capability = ?", tenantID, capability).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrCapabilityInactive, capability)
	}
	if err != nil {
		return err
	}
	if row.Status != "active" || (row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now())) {
		return fmt.Errorf("%w: %s", ErrCapabilityInactive, capability)
	}
	return nil
}

func requireAnyActiveTenantCapability(tx *gorm.DB, tenantID uint, capabilities ...string) error {
	if err := requireActiveTenant(tx, tenantID); err != nil {
		return err
	}
	for _, capability := range capabilities {
		var count int64
		query := tx.Model(&model.TenantCapability{}).
			Where("tenant_id = ? AND capability = ? AND status = ?", tenantID, capability, "active").
			Where("expires_at IS NULL OR expires_at > ?", time.Now())
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count == 1 {
			return nil
		}
	}
	return ErrCapabilityInactive
}

// requireActiveSupplierBusinessType is the service-layer authorization boundary
// for fulfillment-specific operations. It deliberately checks both the active
// supplier market role and the requested business vertical so callers cannot
// bypass a suspended vertical through another role held by the same tenant.
func requireActiveSupplierBusinessType(tx *gorm.DB, tenantID uint, businessType string) error {
	if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.SupplierBusinessType{}).
		Where("tenant_id = ? AND business_type = ? AND status = ?", tenantID, strings.TrimSpace(businessType), "active").
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: %s", ErrSupplierBusinessTypeInactive, strings.TrimSpace(businessType))
	}
	return nil
}

func requireActiveScenicSupplier(tx *gorm.DB, tenantID uint) error {
	return requireActiveSupplierBusinessType(tx, tenantID, "scenic")
}

func requireActiveHotelSupplier(tx *gorm.DB, tenantID uint) error {
	return requireActiveSupplierBusinessType(tx, tenantID, "hotel")
}

// requireConfiguredSupplierBusinessType keeps already-sold fulfillment
// obligations available after a supplier role or vertical is suspended. New
// sales and configuration changes must continue to use the active variant.
func requireConfiguredSupplierBusinessType(tx *gorm.DB, tenantID uint, businessType string) error {
	if tenantID == 0 {
		return ErrTenantUnavailable
	}
	var count int64
	err := tx.Table("tenant_capabilities AS capability").
		Joins("JOIN tenants AS tenant ON tenant.id = capability.tenant_id AND tenant.deleted_at IS NULL").
		Joins("JOIN supplier_business_types AS business ON business.tenant_id = capability.tenant_id AND business.deleted_at IS NULL").
		Where("capability.tenant_id = ? AND capability.capability = ? AND capability.status IN ?", tenantID, "supplier", []string{"active", "suspended"}).
		Where("capability.deleted_at IS NULL").
		Where("tenant.status = ?", "active").
		Where("business.business_type = ? AND business.status IN ?", strings.TrimSpace(businessType), []string{"active", "suspended"}).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: %s", ErrSupplierBusinessTypeInactive, strings.TrimSpace(businessType))
	}
	return nil
}

func requireConfiguredScenicSupplier(tx *gorm.DB, tenantID uint) error {
	return requireConfiguredSupplierBusinessType(tx, tenantID, "scenic")
}

func requireConfiguredHotelSupplier(tx *gorm.DB, tenantID uint) error {
	return requireConfiguredSupplierBusinessType(tx, tenantID, "hotel")
}
