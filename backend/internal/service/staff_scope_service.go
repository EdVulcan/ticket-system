package service

import (
	"errors"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

var ErrResourceScopeDenied = errors.New("staff resource scope denied")

func RequireStaffResource(tenantID, staffID uint, role, resourceType string, resourceID uint) error {
	if role == "admin" || role == "super_admin" {
		return nil
	}
	var count int64
	err := model.DB.Model(&model.StaffResourceScope{}).Where("tenant_id = ? AND staff_id = ? AND resource_type = ? AND resource_id = ?", tenantID, staffID, resourceType, resourceID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrResourceScopeDenied
	}
	return nil
}

func ReplaceStaffResourceScopes(tenantID, staffID uint, scopes []model.StaffResourceScope) error {
	return model.Write(func(tx *gorm.DB) error {
		var staff model.Staff
		if err := tx.Where("id = ? AND tenant_id = ?", staffID, tenantID).First(&staff).Error; err != nil {
			return err
		}
		for i := range scopes {
			scopes[i].Base = model.Base{}
			scopes[i].TenantID = tenantID
			scopes[i].StaffID = staffID
			var count int64
			switch scopes[i].ResourceType {
			case "scenic_area":
				if err := tx.Model(&model.ScenicArea{}).Where("id = ? AND tenant_id = ?", scopes[i].ResourceID, tenantID).Count(&count).Error; err != nil {
					return err
				}
			case "checkpoint":
				if err := tx.Model(&model.CheckPoint{}).Where("id = ? AND tenant_id = ?", scopes[i].ResourceID, tenantID).Count(&count).Error; err != nil {
					return err
				}
			case "device":
				if err := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", scopes[i].ResourceID, tenantID).Count(&count).Error; err != nil {
					return err
				}
			default:
				return errors.New("unsupported resource scope")
			}
			if count == 0 {
				return ErrResourceScopeDenied
			}
		}
		if err := tx.Where("tenant_id = ? AND staff_id = ?", tenantID, staffID).Delete(&model.StaffResourceScope{}).Error; err != nil {
			return err
		}
		if len(scopes) > 0 {
			return tx.Create(&scopes).Error
		}
		return nil
	})
}
