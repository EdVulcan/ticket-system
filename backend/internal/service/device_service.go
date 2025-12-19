package service

import (
	"ticket-backend/internal/model"
)

type DeviceService struct{}

func (s *DeviceService) Create(device *model.Device) error {
	return model.DB.Create(device).Error
}

func (s *DeviceService) Update(id uint, device *model.Device) error {
	return model.DB.Model(&model.Device{}).Where("id = ?", id).Updates(device).Error
}

func (s *DeviceService) Delete(id uint) error {
	return model.DB.Delete(&model.Device{}, id).Error
}

func (s *DeviceService) GetByID(id uint) (*model.Device, error) {
	var device model.Device
	err := model.DB.Preload("CheckPoint").First(&device, id).Error
	return &device, err
}

func (s *DeviceService) List(page, pageSize int, tenantID uint) ([]model.Device, int64, error) {
	var devices []model.Device
	var total int64

	offset := (page - 1) * pageSize

	query := model.DB.Model(&model.Device{})
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Preload("CheckPoint").Offset(offset).Limit(pageSize).Find(&devices).Error
	return devices, total, err
}
