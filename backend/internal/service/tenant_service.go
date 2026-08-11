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
	"gorm.io/gorm/clause"
)

type TenantService struct{}

var ErrTenantActivationBlocked = errors.New("tenant activation requires approved, unexpired qualification and contract")
var ErrSupplierCapabilityRequired = errors.New("an active supplier capability is required before enabling a supplier business type")
var ErrAuditReasonRequired = errors.New("audit reason is required")

func (s *TenantService) RevokeSessions(id uint, actorID uint, actorRole string) error {
	if id == 0 {
		return gorm.ErrRecordNotFound
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.First(&tenant, id).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("tenant_id = ?", id).UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Staff{}).Where("tenant_id = ?", id).UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.sessions.revoke", "tenant", id, "revoke all tenant sessions", "{}", "{\"revoked\":true}")
	})
}

func (s *TenantService) Create(tenant *model.Tenant, adminUsername, adminPassword string) error {
	tenant.Name = strings.TrimSpace(tenant.Name)
	tenant.SystemCode = strings.TrimSpace(tenant.SystemCode)
	adminUsername = strings.TrimSpace(adminUsername)
	if tenant.Name == "" || tenant.SystemCode == "" {
		return errors.New("tenant name and system code are required")
	}
	if tenant.QualificationStatus == "" {
		tenant.QualificationStatus = "pending"
	}
	if !validQualificationStatus(tenant.QualificationStatus) {
		return errors.New("invalid qualification status")
	}
	if tenant.Status == "" {
		// A newly provisioned tenant must be explicitly approved by the
		// platform before it can sell or operate a scenic area.
		tenant.Status = "frozen"
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
			Username:       adminUsername,
			Password:       string(hashedPassword),
			Role:           "admin", // Tenant Admin
			TenantID:       tenant.ID,
			IsInitialAdmin: true,
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

func validQualificationStatus(status string) bool {
	return status == "pending" || status == "approved" || status == "rejected" || status == "expired"
}

func qualificationAllowsActivation(tenant *model.Tenant, now time.Time) bool {
	if tenant == nil {
		return false
	}
	// Empty is a legacy value. Migration marks existing rows approved, but
	// treating an empty value as approved keeps older fixtures readable.
	if tenant.QualificationStatus != "" && tenant.QualificationStatus != "approved" {
		return false
	}
	if tenant.QualificationExpiresAt != nil && !tenant.QualificationExpiresAt.After(now) {
		return false
	}
	if tenant.ContractExpiresAt != nil && !tenant.ContractExpiresAt.After(now) {
		return false
	}
	return true
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
		if status == "active" && !qualificationAllowsActivation(&tenant, time.Now()) {
			return ErrTenantActivationBlocked
		}
		before, _ := json.Marshal(map[string]string{"status": tenant.Status})
		after, _ := json.Marshal(map[string]string{"status": status})
		updates := map[string]interface{}{"status": status}
		if status == "closed" {
			now := time.Now()
			updates["closed_at"] = now
		} else if tenant.Status == "closed" {
			updates["closed_at"] = nil
			updates["close_reason"] = ""
		}
		result := tx.Model(&tenant).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if tenant.Status != status {
			if err := tx.Model(&model.User{}).Where("tenant_id = ?", id).UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Staff{}).Where("tenant_id = ?", id).UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
				return err
			}
		}
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.status.update", "tenant", id, "platform lifecycle change", string(before), string(after))
	})
}

// TenantLifecycleUpdate is intentionally platform-scoped. Qualification,
// contract expiry and closure reason change the ability to operate and must be
// committed together with the audit record.
type TenantLifecycleUpdate struct {
	QualificationStatus    string     `json:"qualification_status"`
	QualificationNo        string     `json:"qualification_no"`
	QualificationExpiresAt *time.Time `json:"qualification_expires_at"`
	ContractExpiresAt      *time.Time `json:"contract_expires_at"`
	CloseReason            string     `json:"close_reason"`
	Reason                 string     `json:"reason"`
}

func (s *TenantService) UpdateLifecycleAudited(id uint, update TenantLifecycleUpdate, actorID uint, actorRole string) error {
	if id == 0 {
		return gorm.ErrRecordNotFound
	}
	if update.QualificationStatus != "" && !validQualificationStatus(update.QualificationStatus) {
		return errors.New("invalid qualification status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("id = ?", id).First(&tenant).Error; err != nil {
			return err
		}
		before, _ := json.Marshal(tenant)
		values := map[string]interface{}{}
		if update.QualificationStatus != "" {
			values["qualification_status"] = update.QualificationStatus
		}
		if strings.TrimSpace(update.QualificationNo) != "" {
			values["qualification_no"] = strings.TrimSpace(update.QualificationNo)
		}
		if update.QualificationExpiresAt != nil {
			values["qualification_expires_at"] = update.QualificationExpiresAt
		}
		if update.ContractExpiresAt != nil {
			values["contract_expires_at"] = update.ContractExpiresAt
		}
		if strings.TrimSpace(update.CloseReason) != "" {
			values["close_reason"] = strings.TrimSpace(update.CloseReason)
		}
		if len(values) == 0 {
			return errors.New("lifecycle update is empty")
		}
		if err := tx.Model(&tenant).Updates(values).Error; err != nil {
			return err
		}
		if err := tx.First(&tenant, id).Error; err != nil {
			return err
		}
		if tenant.Status == "active" && !qualificationAllowsActivation(&tenant, time.Now()) {
			return errors.New("cannot keep tenant active with expired or unapproved qualification")
		}
		after, _ := json.Marshal(tenant)
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.lifecycle.update", "tenant", id, strings.TrimSpace(update.Reason), string(before), string(after))
	})
}

var validTenantCapabilities = map[string]struct{}{
	"supplier":      {},
	"distributor":   {},
	"travel_agency": {},
}

var validSupplierBusinessTypes = map[string]struct{}{
	"scenic": {},
	"hotel":  {},
}

func validTenantCapability(capability string) bool {
	_, ok := validTenantCapabilities[capability]
	return ok
}

func validSupplierBusinessType(businessType string) bool {
	_, ok := validSupplierBusinessTypes[businessType]
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&tenant, id).Error; err != nil {
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

// SetSupplierBusinessTypeAudited configures a supplier's fulfillment vertical
// without changing its market role. Scenic ticketing and hotel accommodation
// can be enabled together for the same tenant.
func (s *TenantService) SetSupplierBusinessTypeAudited(id uint, businessType, status, reason string, actorID uint, actorRole string) error {
	if !validSupplierBusinessType(businessType) {
		return errors.New("invalid supplier business type")
	}
	if status != "active" && status != "suspended" {
		return errors.New("invalid supplier business type status")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrAuditReasonRequired
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&tenant, id).Error; err != nil {
			return err
		}
		if status == "active" {
			var supplierCount int64
			if err := tx.Model(&model.TenantCapability{}).
				Where("tenant_id = ? AND capability = ? AND status = ?", id, "supplier", "active").
				Where("expires_at IS NULL OR expires_at > ?", time.Now()).
				Count(&supplierCount).Error; err != nil {
				return err
			}
			if supplierCount == 0 {
				return ErrSupplierCapabilityRequired
			}
		}

		var row model.SupplierBusinessType
		err := tx.Where("tenant_id = ? AND business_type = ?", id, businessType).First(&row).Error
		before, _ := json.Marshal(row)
		previousStatus := row.Status
		activatedAt := row.ActivatedAt
		if status == "active" {
			now := time.Now()
			activatedAt = &now
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.SupplierBusinessType{
				TenantID: id, BusinessType: businessType, Status: status,
				ActivatedAt: activatedAt, Reason: reason,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			if err := tx.Model(&row).Updates(map[string]interface{}{
				"status": status, "activated_at": activatedAt, "reason": reason,
			}).Error; err != nil {
				return err
			}
		}
		if businessType == "scenic" && previousStatus == "active" && status == "suspended" {
			if err := enqueueCtripScenicSuspensionTasksTx(tx, id, time.Now()); err != nil {
				return err
			}
		}
		after, _ := json.Marshal(map[string]interface{}{"business_type": businessType, "status": status, "reason": reason})
		return recordAuditTx(tx, actorID, id, actorRole, "platform", "tenant.supplier_business_type.update", "supplier_business_type", row.ID, reason, string(before), string(after))
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
	err := model.DB.Preload("Capabilities").Preload("SupplierBusinessTypes").First(&tenant, id).Error
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

	err = model.DB.Preload("Capabilities").Preload("SupplierBusinessTypes").Offset(offset).Limit(pageSize).Find(&tenants).Error
	return tenants, total, err
}
