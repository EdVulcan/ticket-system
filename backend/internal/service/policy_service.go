package service

import (
	"ticket-backend/internal/model"
)

type PolicyService struct{}

func (s *PolicyService) Create(policy *model.Policy) error {
	return model.DB.Create(policy).Error
}

func (s *PolicyService) Update(id uint, policy *model.Policy) error {
	return model.DB.Model(&model.Policy{}).Where("id = ?", id).Updates(policy).Error
}

func (s *PolicyService) Delete(id uint) error {
	return model.DB.Delete(&model.Policy{}, id).Error
}

func (s *PolicyService) List(tenantID uint, category string) ([]model.Policy, error) {
	var policies []model.Policy
	db := model.DB.Where("tenant_id = ?", tenantID).Order("created_at desc")

	if category != "" {
		db = db.Where("category = ?", category)
	}

	err := db.Find(&policies).Error
	return policies, err
}
