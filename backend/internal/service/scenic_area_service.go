package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type ScenicAreaService struct{}

func validateScenicArea(area *model.ScenicArea) error {
	area.Code = strings.TrimSpace(area.Code)
	area.Name = strings.TrimSpace(area.Name)
	if area.Code == "" || area.Name == "" {
		return errors.New("scenic area code and name are required")
	}
	if area.Status == "" {
		area.Status = "active"
	}
	if area.Status != "active" && area.Status != "frozen" && area.Status != "closed" {
		return errors.New("invalid scenic area status")
	}
	return nil
}

func (s *ScenicAreaService) Create(tenantID uint, area *model.ScenicArea) error {
	if tenantID == 0 {
		return errors.New("tenant is required")
	}
	if err := validateScenicArea(area); err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		area.Base = model.Base{}
		area.TenantID = tenantID
		return tx.Create(area).Error
	})
}

func (s *ScenicAreaService) Update(id, tenantID uint, area *model.ScenicArea) error {
	if err := validateScenicArea(area); err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var existing model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&existing).Error; err != nil {
			return err
		}
		// Area codes are identifiers used by devices and channels; they are
		// immutable once the area is referenced by operational data.
		return tx.Model(&existing).Updates(map[string]interface{}{
			"name": area.Name, "status": area.Status,
		}).Error
	})
}

func (s *ScenicAreaService) Delete(id, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var area model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&area).Error; err != nil {
			return err
		}
		checks := []struct {
			name  string
			model interface{}
			where string
		}{
			{"checkpoint", &model.CheckPoint{}, "scenic_area_id = ?"},
			{"device", &model.Device{}, "scenic_area_id = ?"},
			{"product", &model.Product{}, "scenic_area_id = ? OR fulfillment_scenic_area_id = ?"},
			{"product offer", &model.ProductOffer{}, "fulfillment_scenic_area_id = ?"},
			{"fulfillment order", &model.FulfillmentOrder{}, "scenic_area_id = ?"},
			{"ticket", &model.Ticket{}, "scenic_area_id = ? OR fulfillment_scenic_area_id = ?"},
			{"ticket entitlement", &model.TicketEntitlement{}, "scenic_area_id = ?"},
		}
		for _, check := range checks {
			query := tx.Model(check.model)
			var count int64
			if strings.Count(check.where, "?") == 1 {
				if err := query.Where(check.where, area.ID).Count(&count).Error; err != nil {
					return err
				}
			} else if err := query.Where(check.where, area.ID, area.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("scenic area has %s data and cannot be deleted", check.name)
			}
		}
		return tx.Delete(&area).Error
	})
}

func (s *ScenicAreaService) List(tenantID uint) ([]model.ScenicArea, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	var areas []model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("created_at asc").Find(&areas).Error; err != nil {
		return nil, err
	}
	return areas, nil
}
