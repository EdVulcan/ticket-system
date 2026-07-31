package service

import (
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

var (
	ErrTenantUnavailable  = errors.New("tenant is unavailable")
	ErrCapabilityInactive = errors.New("tenant capability is not active")
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
