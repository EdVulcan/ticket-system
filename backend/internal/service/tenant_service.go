package service

import (
	"errors"
	"ticket-backend/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type TenantService struct{}

func (s *TenantService) Create(tenant *model.Tenant, adminUsername, adminPassword string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Create Tenant
		if err := tx.Create(tenant).Error; err != nil {
			return err
		}

		// 2. Create Default Admin User
		// Use provided username/password or defaults
		if adminUsername == "" {
			adminUsername = "admin"
		}
		if adminPassword == "" {
			return errors.New("必须设置管理员初始密码")
		}

		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)

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
	return model.DB.Model(&model.Tenant{}).Where("id = ?", id).Updates(tenant).Error
}

func (s *TenantService) Delete(id uint) error {
	return model.DB.Delete(&model.Tenant{}, id).Error
}

func (s *TenantService) GetByID(id uint) (*model.Tenant, error) {
	var tenant model.Tenant
	err := model.DB.First(&tenant, id).Error
	return &tenant, err
}

func (s *TenantService) List(page, pageSize int) ([]model.Tenant, int64, error) {
	var tenants []model.Tenant
	var total int64

	offset := (page - 1) * pageSize

	err := model.DB.Model(&model.Tenant{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = model.DB.Offset(offset).Limit(pageSize).Find(&tenants).Error
	return tenants, total, err
}
