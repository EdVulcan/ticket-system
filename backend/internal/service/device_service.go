package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
)

type DeviceService struct {
	DB            *gorm.DB
	TicketService *TicketService
}

func NewDeviceService(db *gorm.DB, ts *TicketService) *DeviceService {
	return &DeviceService{DB: db, TicketService: ts}
}

// --- Request/Response Types ---

type HeartbeatRequest struct {
	SystemCode   string `json:"system_code"`
	SerialNumber string `json:"serial_number"`
	IP           string `json:"ip"`
	Status       string `json:"status"` // online, fault, etc.
	DeviceKey    string `json:"device_key"`
}

type VerifyRequest struct {
	SystemCode   string `json:"system_code"`
	SerialNumber string `json:"serial_number"`
	TicketCode   string `json:"ticket_code"`
	MediaType    string `json:"media_type"` // qr_code, id_card
	ScanTime     string `json:"scan_time"`
	DeviceKey    string `json:"device_key"`
}

type VerifyResponse struct {
	Code         int    `json:"code"`
	Result       string `json:"result"`        // allow, deny
	DisplayText  string `json:"display_text"`  // Message for screen
	VoiceFile    string `json:"voice_file"`    // e.g., welcome.mp3
	OpenDuration int    `json:"open_duration"` // ms
}

// --- Hardware API Methods ---

func (s *DeviceService) Heartbeat(req HeartbeatRequest) error {
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("system_code = ?", req.SystemCode).First(&tenant).Error; err != nil {
			return errors.New("invalid system_code")
		}

		var device model.Device
		if err := tx.Where("serial_number = ? AND tenant_id = ?", req.SerialNumber, tenant.ID).First(&device).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("device not registered")
			}
			return err
		}
		if !validDeviceKey(&device, req.DeviceKey) {
			return errors.New("invalid device key")
		}

		updates := map[string]interface{}{"status": "online", "last_heartbeat": time.Now()}
		if req.IP != "" {
			updates["ip_address"] = req.IP
		}
		return tx.Model(&device).Updates(updates).Error
	})
}

func (s *DeviceService) Verify(req VerifyRequest) (*VerifyResponse, error) {
	// 1. Validate Tenant
	var tenant model.Tenant
	if err := s.DB.Where("system_code = ?", req.SystemCode).First(&tenant).Error; err != nil {
		return &VerifyResponse{Code: 400, Result: "deny", DisplayText: "Invalid System Code"}, nil
	}

	// 2. Validate Device
	var device model.Device
	if err := s.DB.Where("serial_number = ? AND tenant_id = ?", req.SerialNumber, tenant.ID).First(&device).Error; err != nil {
		return &VerifyResponse{Code: 403, Result: "deny", DisplayText: "Unauthorized Device"}, nil
	}
	if !validDeviceKey(&device, req.DeviceKey) {
		return &VerifyResponse{Code: 403, Result: "deny", DisplayText: "Unauthorized Device"}, nil
	}

	// 3. Delegate Verification to TicketService (Common Logic)
	// Determines CheckPointID (if bound)
	var checkPointID uint
	if device.CheckPointID != nil {
		checkPointID = *device.CheckPointID
	}

	err := s.TicketService.Verify(req.TicketCode, checkPointID, device.ID, tenant.ID)
	if err != nil {
		// Map error to Voice/Display
		resp := &VerifyResponse{Code: 403, Result: "deny", VoiceFile: "invalid.mp3"}

		if errors.Is(err, ErrInvalidTicket) {
			resp.DisplayText = "无效票\nInvalid Ticket"
		} else if errors.Is(err, ErrTicketExpired) {
			resp.DisplayText = "已过期\nExpired"
		} else if errors.Is(err, ErrTicketNotStarted) {
			resp.DisplayText = "未生效\nNot Started"
		} else if errors.Is(err, ErrOrderNotPaid) {
			resp.DisplayText = "订单未支付\nUnpaid Order"
		} else if errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrCheckpointNotFound) {
			resp.DisplayText = "区域无权\nAccess Denied"
		} else if errors.Is(err, ErrPointLimitReached) || errors.Is(err, ErrTicketUnavailable) {
			resp.DisplayText = "次数已满\nLimit Reached"
			resp.VoiceFile = "already_used.mp3"
		} else if errors.Is(err, ErrGroupLimitReached) {
			resp.DisplayText = "权益已尽\nNo Quota"
		} else {
			resp.DisplayText = "验证失败\n" + err.Error()
		}
		return resp, nil
	}

	// 4. Success Response
	// Fetch ticket productName for display
	var ticket model.Ticket
	s.DB.Preload("OrderItem").Where("ticket_code = ?", req.TicketCode).First(&ticket)
	productName := ticket.OrderItem.ProductName

	return &VerifyResponse{
		Code:         200,
		Result:       "allow",
		DisplayText:  fmt.Sprintf("欢迎光临\n%s", productName),
		VoiceFile:    "welcome.mp3",
		OpenDuration: 5000,
	}, nil
}

// --- CRUD Methods (Admin UI) ---

func (s *DeviceService) validateCheckPoint(db *gorm.DB, tenantID uint, checkPointID *uint) error {
	if checkPointID == nil {
		return nil
	}
	var count int64
	if err := db.Model(&model.CheckPoint{}).Where("id = ? AND tenant_id = ?", *checkPointID, tenantID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return errors.New("checkpoint not found")
	}
	return nil
}

func (s *DeviceService) Create(device *model.Device, tenantID uint) error {
	device.AuthKey = utils.GenerateRandomString(40)
	device.AuthKeyHash = hashDeviceKey(device.AuthKey)
	return model.Write(func(tx *gorm.DB) error {
		if err := s.validateCheckPoint(tx, tenantID, device.CheckPointID); err != nil {
			return err
		}
		device.Base = model.Base{}
		device.TenantID = tenantID
		return tx.Omit("CheckPoint").Create(device).Error
	})
}

func (s *DeviceService) Update(id, tenantID uint, device *model.Device) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := s.validateCheckPoint(tx, tenantID, device.CheckPointID); err != nil {
			return err
		}
		result := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).
			Omit("tenant_id", "serial_number", "auth_key_hash").Updates(device)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *DeviceService) RotateKey(id, tenantID uint) (string, error) {
	key := utils.GenerateRandomString(40)
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).Update("auth_key_hash", hashDeviceKey(key))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	return key, err
}

func hashDeviceKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func validDeviceKey(device *model.Device, key string) bool {
	if device.AuthKeyHash == "" || key == "" {
		return false
	}
	provided := hashDeviceKey(key)
	return subtle.ConstantTimeCompare([]byte(device.AuthKeyHash), []byte(provided)) == 1
}

func (s *DeviceService) Delete(id, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Device{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *DeviceService) GetByID(id, tenantID uint) (*model.Device, error) {
	var device model.Device
	err := s.DB.Preload("CheckPoint").Where("id = ? AND tenant_id = ?", id, tenantID).First(&device).Error
	return &device, err
}

func (s *DeviceService) List(page, pageSize int, tenantID uint) ([]model.Device, int64, error) {
	var devices []model.Device
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

	query := s.DB.Model(&model.Device{})
	query = query.Where("tenant_id = ?", tenantID)

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Preload("CheckPoint").Offset(offset).Limit(pageSize).Find(&devices).Error
	return devices, total, err
}
