package service

import (
	"ticket-backend/internal/model"
)

type CheckPointService struct{}

func (s *CheckPointService) Create(cp *model.CheckPoint) error {
	return model.DB.Create(cp).Error
}

func (s *CheckPointService) Update(id uint, cp *model.CheckPoint) error {
	return model.DB.Model(&model.CheckPoint{}).Where("id = ?", id).Updates(cp).Error
}

func (s *CheckPointService) Delete(id uint) error {
	return model.DB.Delete(&model.CheckPoint{}, id).Error
}

func (s *CheckPointService) List(page, pageSize int, tenantID uint) ([]model.CheckPoint, int64, error) {
	var checkpoints []model.CheckPoint
	var total int64

	offset := (page - 1) * pageSize

	query := model.DB.Model(&model.CheckPoint{})
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Offset(offset).Limit(pageSize).Find(&checkpoints).Error
	return checkpoints, total, err
}
