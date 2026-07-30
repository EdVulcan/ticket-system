package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"

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
	return model.Write(func(tx *gorm.DB) error {
		for _, table := range []interface{}{&model.Order{}, &model.Product{}, &model.User{}, &model.Staff{}} {
			var dependent int64
			if err := tx.Model(table).Where("tenant_id = ?", id).Count(&dependent).Error; err != nil {
				return err
			}
			if dependent > 0 {
				return fmt.Errorf("tenant contains business data and cannot be deleted")
			}
		}
		result := tx.Where("id = ?", id).Delete(&model.Tenant{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *TenantService) GetByID(id uint) (*model.Tenant, error) {
	var tenant model.Tenant
	err := model.DB.First(&tenant, id).Error
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

	err = model.DB.Offset(offset).Limit(pageSize).Find(&tenants).Error
	return tenants, total, err
}
