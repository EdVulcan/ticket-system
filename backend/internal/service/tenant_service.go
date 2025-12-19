package service

import (
	"ticket-backend/internal/model"
)

type TenantService struct{}

func (s *TenantService) Create(tenant *model.Tenant) error {
	return model.DB.Create(tenant).Error
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
