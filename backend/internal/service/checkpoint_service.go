package service

import (
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type CheckPointService struct{}

func (s *CheckPointService) Create(cp *model.CheckPoint) error {
	return model.Write(func(tx *gorm.DB) error { return tx.Create(cp).Error })
}

func (s *CheckPointService) Update(id, tenantID uint, cp *model.CheckPoint) error {
	cp.TenantID = tenantID
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.CheckPoint{}).Where("id = ? AND tenant_id = ?", id, tenantID).Omit("tenant_id").Updates(cp)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *CheckPointService) Delete(id, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.CheckPoint{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *CheckPointService) List(page, pageSize int, tenantID uint) ([]model.CheckPoint, int64, error) {
	var checkpoints []model.CheckPoint
	var total int64

	if tenantID == 0 {
		return nil, 0, gorm.ErrInvalidData
	}
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

	query := model.DB.Model(&model.CheckPoint{})
	query = query.Where("tenant_id = ?", tenantID)

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Offset(offset).Limit(pageSize).Find(&checkpoints).Error
	return checkpoints, total, err
}
