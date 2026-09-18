package service

import (
	"encoding/json"
	"errors"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// Keep the commercial error compatible with the platform-wide capability
	// error so callers can handle either boundary without leaking details.
	ErrBusinessCapabilityInactive = ErrCapabilityInactive
	ErrBusinessCapabilityInvalid  = errors.New("invalid tenant business capability")
	ErrBusinessCapabilityStatus   = errors.New("invalid tenant business capability status")
)

var validTenantBusinessTypes = map[string]struct{}{
	"restaurant": {},
	"retail":     {},
}

func validTenantBusinessType(businessType string) bool {
	_, ok := validTenantBusinessTypes[strings.TrimSpace(businessType)]
	return ok
}

func validTenantBusinessCapabilityStatus(status string) bool {
	return status == "active" || status == "suspended"
}

// SetBusinessCapabilityAudited is a platform-only capability approval
// operation. Commercial capabilities are intentionally separate from market
// capabilities (supplier/distributor/travel_agency).
func (s *TenantService) SetBusinessCapabilityAudited(id uint, businessType, status, reason string, actorID uint, actorRole string) error {
	businessType = strings.TrimSpace(businessType)
	status = strings.TrimSpace(status)
	reason = strings.TrimSpace(reason)
	if id == 0 {
		return gorm.ErrRecordNotFound
	}
	if !validTenantBusinessType(businessType) {
		return ErrBusinessCapabilityInvalid
	}
	if !validTenantBusinessCapabilityStatus(status) {
		return ErrBusinessCapabilityStatus
	}
	if reason == "" {
		return ErrAuditReasonRequired
	}

	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&tenant, id).Error; err != nil {
			return err
		}

		var row model.TenantBusinessCapability
		err := tx.Where("tenant_id = ? AND business_type = ?", id, businessType).First(&row).Error
		before := row
		now := time.Now()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.TenantBusinessCapability{
				TenantID: id, BusinessType: businessType, Status: status,
				EnabledAt: func() *time.Time {
					if status == "active" {
						return &now
					}
					return nil
				}(),
				SuspendedAt: func() *time.Time {
					if status == "suspended" {
						return &now
					}
					return nil
				}(),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			updates := map[string]interface{}{"status": status}
			if status == "active" {
				updates["enabled_at"] = now
				updates["suspended_at"] = nil
			} else {
				updates["suspended_at"] = now
			}
			if err := tx.Model(&row).Updates(updates).Error; err != nil {
				return err
			}
			row.Status = status
			if status == "active" {
				row.EnabledAt = &now
				row.SuspendedAt = nil
			} else {
				row.SuspendedAt = &now
			}
		}

		after := model.TenantBusinessCapability{TenantID: id, BusinessType: businessType, Status: status, EnabledAt: row.EnabledAt, SuspendedAt: row.SuspendedAt}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.business_capability.update", "tenant_business_capability", row.ID, reason, string(beforeJSON), string(afterJSON))
	})
}

// RequireActiveTenantBusinessCapability is the service-layer boundary for
// commercial writes. Both the tenant lifecycle and the requested capability
// must be active; a missing row fails closed.
func RequireActiveTenantBusinessCapability(tx *gorm.DB, tenantID uint, businessType string) error {
	if tx == nil {
		return ErrTenantUnavailable
	}
	if err := requireActiveTenant(tx, tenantID); err != nil {
		return err
	}
	businessType = strings.TrimSpace(businessType)
	if !validTenantBusinessType(businessType) {
		return ErrBusinessCapabilityInactive
	}
	var row model.TenantBusinessCapability
	err := tx.Where("tenant_id = ? AND business_type = ?", tenantID, businessType).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrBusinessCapabilityInactive
	}
	if err != nil {
		return err
	}
	if row.Status != "active" {
		return ErrBusinessCapabilityInactive
	}
	return nil
}

// RequireConfiguredTenantBusinessCapability allows read-only historical
// queries while a commercial capability is suspended. It still requires an
// active tenant and never authorizes writes.
func RequireConfiguredTenantBusinessCapability(tx *gorm.DB, tenantID uint, businessType string) error {
	if tx == nil || tenantID == 0 {
		return ErrTenantUnavailable
	}
	businessType = strings.TrimSpace(businessType)
	if !validTenantBusinessType(businessType) {
		return ErrBusinessCapabilityInactive
	}
	var tenant model.Tenant
	if err := tx.Select("id", "status").First(&tenant, tenantID).Error; err != nil {
		return err
	}
	if tenant.Status != "active" {
		return ErrTenantUnavailable
	}
	var count int64
	if err := tx.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ? AND status IN ?", tenantID, businessType, []string{"active", "suspended"}).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrBusinessCapabilityInactive
	}
	return nil
}
