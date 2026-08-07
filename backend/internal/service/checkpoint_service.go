package service

import (
	"errors"
	"fmt"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type CheckPointService struct{}

func (s *CheckPointService) Create(cp *model.CheckPoint) error {
	return model.Write(func(tx *gorm.DB) error {
		if cp.TenantID == 0 || cp.Name == "" {
			return fmt.Errorf("tenant and checkpoint name are required")
		}
		if err := requireActiveTenantCapability(tx, cp.TenantID, "supplier"); err != nil {
			return err
		}
		areaID, err := normalizeScenicArea(tx, cp.TenantID, cp.ScenicAreaID)
		if err != nil {
			return err
		}
		cp.ScenicAreaID = areaID
		cp.Base = model.Base{}
		return tx.Create(cp).Error
	})
}

func (s *CheckPointService) Update(id, tenantID uint, cp *model.CheckPoint) error {
	cp.TenantID = tenantID
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var existing model.CheckPoint
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&existing).Error; err != nil {
			return err
		}
		cp.ScenicAreaID = existing.ScenicAreaID
		result := tx.Model(&model.CheckPoint{}).Where("id = ? AND tenant_id = ?", id, tenantID).Omit("tenant_id", "scenic_area_id").Updates(cp)
		if result.Error == nil {
			result = tx.Model(&existing).Update("scenic_area_id", cp.ScenicAreaID)
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func normalizeScenicArea(tx *gorm.DB, tenantID, requestedID uint) (uint, error) {
	if requestedID != 0 {
		var area model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", requestedID, tenantID, "active").First(&area).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, errors.New("scenic area not found")
			}
			return 0, err
		}
		return area.ID, nil
	}
	var areas []model.ScenicArea
	if err := tx.Where("tenant_id = ? AND status = ?", tenantID, "active").Find(&areas).Error; err != nil {
		return 0, err
	}
	if len(areas) == 1 {
		return areas[0].ID, nil
	}
	return 0, errors.New("an explicit scenic area is required when the tenant does not have exactly one active scenic area")
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

func (s *CheckPointService) List(page, pageSize int, tenantID, scenicAreaID uint) ([]model.CheckPoint, int64, error) {
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
	if scenicAreaID != 0 {
		query = query.Where("scenic_area_id = ?", scenicAreaID)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Offset(offset).Limit(pageSize).Find(&checkpoints).Error
	return checkpoints, total, err
}
