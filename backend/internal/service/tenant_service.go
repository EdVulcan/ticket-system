package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type TenantService struct{}

func (s *TenantService) Create(tenant *model.Tenant, adminUsername, adminPassword string) error {
	tenant.Name = strings.TrimSpace(tenant.Name)
	tenant.SystemCode = strings.TrimSpace(tenant.SystemCode)
	adminUsername = strings.TrimSpace(adminUsername)
	if tenant.Name == "" || tenant.SystemCode == "" {
		return errors.New("tenant name and system code are required")
	}
	if tenant.Status == "" {
		tenant.Status = "active"
	}
	if !validTenantStatus(tenant.Status) {
		return errors.New("invalid tenant status")
	}
	if len(adminPassword) < 8 {
		return errors.New("administrator password must be at least 8 characters")
	}
	return model.Write(func(tx *gorm.DB) error {
		// 1. Generate SecretKey if missing
		if tenant.SecretKey == "" {
			tenant.SecretKey = utils.GenerateRandomString(32)
		}

		// 2. Create Tenant
		if err := tx.Create(tenant).Error; err != nil {
			return err
		}

		// 2. Create Default Admin User
		// Use provided username/password or defaults
		if adminUsername == "" {
			adminUsername = "admin"
		}
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		adminUser := model.User{
			Username: adminUsername,
			Password: string(hashedPassword),
			Role:     "admin", // Tenant Admin
			TenantID: tenant.ID,
		}

		if err := tx.Create(&adminUser).Error; err != nil {
			return err
		}

		return nil
	})
}

func validTenantStatus(status string) bool {
	return status == "active" || status == "frozen" || status == "closed"
}

// UpdateStatus is a platform-only lifecycle transition. Ordinary tenant
// routes never accept a status field, so a tenant cannot unfreeze itself.
func (s *TenantService) UpdateStatus(id uint, status string) error {
	return s.UpdateStatusAudited(id, status, 0, "system")
}

func (s *TenantService) UpdateStatusAudited(id uint, status string, actorID uint, actorRole string) error {
	if !validTenantStatus(status) {
		return errors.New("invalid tenant status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("id = ?", id).First(&tenant).Error; err != nil {
			return err
		}
		before, _ := json.Marshal(map[string]string{"status": tenant.Status})
		after, _ := json.Marshal(map[string]string{"status": status})
		result := tx.Model(&tenant).Update("status", status)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.status.update", "tenant", id, "platform lifecycle change", string(before), string(after))
	})
}

var validTenantCapabilities = map[string]struct{}{
	"supplier":      {},
	"distributor":   {},
	"travel_agency": {},
}

func validTenantCapability(capability string) bool {
	_, ok := validTenantCapabilities[capability]
	return ok
}

// SetCapability is a platform-only capability approval operation. Capability
// status is kept separately from the tenant lifecycle so mixed-role tenants
// remain possible.
func (s *TenantService) SetCapability(id uint, capability, status, reason string) error {
	return s.SetCapabilityAudited(id, capability, status, reason, 0, "system")
}

func (s *TenantService) SetCapabilityAudited(id uint, capability, status, reason string, actorID uint, actorRole string) error {
	if !validTenantCapability(capability) {
		return errors.New("invalid tenant capability")
	}
	if status != "pending" && status != "active" && status != "suspended" && status != "rejected" {
		return errors.New("invalid capability status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Select("id").First(&tenant, id).Error; err != nil {
			return err
		}
		var capabilityRow model.TenantCapability
		err := tx.Where("tenant_id = ? AND capability = ?", id, capability).First(&capabilityRow).Error
		approvedAt := capabilityRow.ApprovedAt
		if status == "active" {
			now := time.Now()
			approvedAt = &now
		}
		before, _ := json.Marshal(capabilityRow)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			capabilityRow = model.TenantCapability{
				TenantID: id, Capability: capability, Status: status,
				ApprovedAt: approvedAt, Reason: strings.TrimSpace(reason),
			}
			if err := tx.Create(&capabilityRow).Error; err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			if err := tx.Model(&capabilityRow).Updates(map[string]interface{}{
				"status": status, "approved_at": approvedAt, "reason": strings.TrimSpace(reason),
			}).Error; err != nil {
				return err
			}
		}
		after, _ := json.Marshal(map[string]interface{}{"capability": capability, "status": status, "reason": strings.TrimSpace(reason)})
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.capability.update", "tenant_capability", capabilityRow.ID, reason, string(before), string(after))
	})
}

func (s *TenantService) Update(id uint, tenant *model.Tenant) error {
	tenant.Name = strings.TrimSpace(tenant.Name)
	if tenant.Name == "" {
		return errors.New("tenant name is required")
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.Tenant{}).Where("id = ?", id).Updates(map[string]interface{}{
			"name": tenant.Name, "contact": tenant.Contact, "phone": tenant.Phone, "address": tenant.Address,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *TenantService) Delete(id uint) error {
	if id == 0 {
		return gorm.ErrRecordNotFound
	}
	return fmt.Errorf("tenant deletion is disabled; set tenant status to closed to preserve business and audit data")
}

func (s *TenantService) GetByID(id uint) (*model.Tenant, error) {
	var tenant model.Tenant
	err := model.DB.Preload("Capabilities").First(&tenant, id).Error
	return &tenant, err
}

func (s *TenantService) List(page, pageSize int) ([]model.Tenant, int64, error) {
	var tenants []model.Tenant
	var total int64

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	err := model.DB.Model(&model.Tenant{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = model.DB.Preload("Capabilities").Offset(offset).Limit(pageSize).Find(&tenants).Error
	return tenants, total, err
}
