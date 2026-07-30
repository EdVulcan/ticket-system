package service

import (
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type PolicyService struct{}

func (s *PolicyService) Create(policy *model.Policy) error {
	return model.Write(func(tx *gorm.DB) error { return tx.Create(policy).Error })
}

func (s *PolicyService) Update(id, tenantID uint, policy *model.Policy) error {
	var rows int64
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.Policy{}).Where("id = ? AND tenant_id = ?", id, tenantID).Omit("tenant_id").Updates(policy)
		rows = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *PolicyService) Delete(id, tenantID uint) error {
	var rows int64
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Policy{})
		rows = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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
