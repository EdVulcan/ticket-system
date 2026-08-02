package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeviceService struct {
	DB            *gorm.DB
	TicketService *TicketService
}

// MarkOffline transitions stale devices and opens one alert per incident.
// It is safe to run from more than one scheduler because the status predicate
// and open-alert lookup make the operation idempotent.
func (s *DeviceService) MarkOffline(now time.Time, timeout time.Duration) (int, error) {
	changed := 0
	err := model.Write(func(tx *gorm.DB) error {
		cutoff := now.Add(-timeout)
		var devices []model.Device
		if err := tx.Where("status = ? AND last_heartbeat IS NOT NULL AND last_heartbeat < ?", "online", cutoff).Find(&devices).Error; err != nil {
			return err
		}
		for i := range devices {
			if err := tx.Model(&devices[i]).Update("status", "offline").Error; err != nil {
				return err
			}
			if err := syncDeviceAlertTx(tx, &devices[i], "offline", now); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	return changed, err
}

func syncDeviceAlertTx(tx *gorm.DB, device *model.Device, status string, now time.Time) error {
	alertType := status
	if status != "offline" && status != "fault" {
		return tx.Model(&model.DeviceAlert{}).Where("device_id = ? AND status = ?", device.ID, "open").Updates(map[string]interface{}{"status": "resolved", "resolved_at": now}).Error
	}
	var open int64
	if err := tx.Model(&model.DeviceAlert{}).Where("device_id = ? AND type = ? AND status = ?", device.ID, alertType, "open").Count(&open).Error; err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	return tx.Create(&model.DeviceAlert{TenantID: device.TenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: device.ID, Type: alertType, Status: "open", Message: "device " + alertType, OpenedAt: now}).Error
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

type HardwareCommandRequest struct {
	TenantID    uint
	DeviceID    uint
	Kind        string
	PayloadJSON string
	TTL         time.Duration
}

type HardwareAckRequest struct {
	SystemCode   string `json:"system_code"`
	SerialNumber string `json:"serial_number"`
	DeviceKey    string `json:"device_key"`
	CommandNo    string `json:"command_no"`
	AckToken     string `json:"ack_token"`
	Status       string `json:"status"` // acknowledged, failed
	Payload      string `json:"payload"`
	Error        string `json:"error"`
}

func (s *DeviceService) QueueHardwareCommand(req HardwareCommandRequest) (*model.HardwareCommand, error) {
	if req.TenantID == 0 || req.DeviceID == 0 || strings.TrimSpace(req.Kind) == "" {
		return nil, errors.New("tenant, device and command kind are required")
	}
	if req.TTL <= 0 || req.TTL > 24*time.Hour {
		req.TTL = 10 * time.Minute
	}
	var command model.HardwareCommand
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND status != ?", req.DeviceID, req.TenantID, "offline").First(&device).Error; err != nil {
			return errors.New("device is unavailable")
		}
		if device.ScenicAreaID == 0 {
			return errors.New("device has no scenic area")
		}
		command = model.HardwareCommand{
			TenantID: req.TenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: req.DeviceID,
			CommandNo: fmt.Sprintf("CMD%d", time.Now().UnixNano()), Kind: strings.TrimSpace(req.Kind),
			PayloadJSON: req.PayloadJSON, Status: "queued", AckToken: utils.GenerateRandomString(32),
			QueuedAt: time.Now(), ExpiresAt: time.Now().Add(req.TTL),
		}
		return tx.Create(&command).Error
	})
	if err != nil {
		return nil, err
	}
	return &command, nil
}

func (s *DeviceService) PollHardwareCommand(systemCode, serialNumber, deviceKey string) (*model.HardwareCommand, error) {
	var command model.HardwareCommand
	err := model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("system_code = ? AND status = ?", strings.TrimSpace(systemCode), "active").First(&tenant).Error; err != nil {
			return errors.New("invalid system_code")
		}
		if err := requireActiveTenantCapability(tx, tenant.ID, "supplier"); err != nil {
			return err
		}
		var device model.Device
		if err := tx.Where("serial_number = ? AND tenant_id = ?", serialNumber, tenant.ID).First(&device).Error; err != nil || !validDeviceKey(&device, deviceKey) {
			return errors.New("unauthorized device")
		}
		now := time.Now()
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND device_id = ? AND status IN ? AND expires_at > ?", tenant.ID, device.ID, []string{"queued", "delivered"}, now).Order("id").First(&command)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil
			}
			return result.Error
		}
		command.Status = "delivered"
		command.AttemptCount++
		command.DeliveredAt = &now
		return tx.Model(&command).Updates(map[string]interface{}{"status": command.Status, "attempt_count": command.AttemptCount, "delivered_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	if command.ID == 0 {
		return nil, nil
	}
	return &command, nil
}

func (s *DeviceService) AckHardwareCommand(req HardwareAckRequest) error {
	if strings.TrimSpace(req.CommandNo) == "" || strings.TrimSpace(req.AckToken) == "" {
		return errors.New("command and ack token are required")
	}
	if req.Status != "acknowledged" && req.Status != "failed" {
		return errors.New("invalid hardware acknowledgement status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("system_code = ? AND status = ?", strings.TrimSpace(req.SystemCode), "active").First(&tenant).Error; err != nil {
			return errors.New("invalid system_code")
		}
		var device model.Device
		if err := tx.Where("serial_number = ? AND tenant_id = ?", req.SerialNumber, tenant.ID).First(&device).Error; err != nil || !validDeviceKey(&device, req.DeviceKey) {
			return errors.New("unauthorized device")
		}
		var command model.HardwareCommand
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("command_no = ? AND tenant_id = ? AND device_id = ?", req.CommandNo, tenant.ID, device.ID).First(&command).Error; err != nil {
			return err
		}
		if command.AckToken != req.AckToken {
			return errors.New("invalid acknowledgement token")
		}
		if command.Status == "acknowledged" || command.Status == "failed" {
			return nil
		}
		now := time.Now()
		updates := map[string]interface{}{"status": req.Status, "acked_at": now, "last_error": strings.TrimSpace(req.Error)}
		if err := tx.Model(&command).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Create(&model.HardwareEvent{TenantID: tenant.ID, DeviceID: device.ID, CommandNo: command.CommandNo, EventType: req.Status, Payload: req.Payload}).Error
	})
}

// --- Hardware API Methods ---

func (s *DeviceService) Heartbeat(req HeartbeatRequest) error {
	return model.Write(func(tx *gorm.DB) error {
		var tenant model.Tenant
		if err := tx.Where("system_code = ? AND status = ?", req.SystemCode, "active").First(&tenant).Error; err != nil {
			return errors.New("invalid system_code")
		}
		if err := requireActiveTenantCapability(tx, tenant.ID, "supplier"); err != nil {
			return err
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

		status := strings.TrimSpace(req.Status)
		if status != "fault" {
			status = "online"
		}
		now := time.Now()
		updates := map[string]interface{}{"status": status, "last_heartbeat": now}
		if req.IP != "" {
			updates["ip_address"] = req.IP
		}
		if err := tx.Model(&device).Updates(updates).Error; err != nil {
			return err
		}
		return syncDeviceAlertTx(tx, &device, status, now)
	})
}

func (s *DeviceService) Verify(req VerifyRequest) (*VerifyResponse, error) {
	// 1. Validate Tenant
	var tenant model.Tenant
	if err := s.DB.Where("system_code = ? AND status = ?", req.SystemCode, "active").First(&tenant).Error; err != nil {
		return &VerifyResponse{Code: 400, Result: "deny", DisplayText: "Invalid System Code"}, nil
	}
	if err := requireActiveTenantCapability(s.DB, tenant.ID, "supplier"); err != nil {
		return &VerifyResponse{Code: 403, Result: "deny", DisplayText: "Tenant Unavailable"}, nil
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
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		if err := s.validateCheckPoint(tx, tenantID, device.CheckPointID); err != nil {
			return err
		}
		areaID, err := scenicAreaForCheckpoint(tx, tenantID, device.CheckPointID, device.ScenicAreaID)
		if err != nil {
			return err
		}
		device.Base = model.Base{}
		device.TenantID = tenantID
		device.ScenicAreaID = areaID
		return tx.Omit("CheckPoint").Create(device).Error
	})
}

func (s *DeviceService) Update(id, tenantID uint, device *model.Device) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		if err := s.validateCheckPoint(tx, tenantID, device.CheckPointID); err != nil {
			return err
		}
		areaID, err := scenicAreaForCheckpoint(tx, tenantID, device.CheckPointID, device.ScenicAreaID)
		if err != nil {
			return err
		}
		device.ScenicAreaID = areaID
		result := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).
			Omit("tenant_id", "serial_number", "auth_key_hash", "ScenicAreaID").Updates(device)
		if result.Error == nil {
			result = tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).Update("scenic_area_id", areaID)
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

func scenicAreaForCheckpoint(db *gorm.DB, tenantID uint, checkpointID *uint, requestedAreaID uint) (uint, error) {
	if checkpointID == nil {
		return normalizeScenicArea(db, tenantID, requestedAreaID)
	}
	var checkpoint model.CheckPoint
	if err := db.Select("id", "tenant_id", "scenic_area_id").Where("id = ? AND tenant_id = ?", *checkpointID, tenantID).First(&checkpoint).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errors.New("checkpoint not found")
		}
		return 0, err
	}
	if checkpoint.ScenicAreaID == 0 {
		return 0, errors.New("checkpoint has no scenic area")
	}
	if requestedAreaID != 0 && requestedAreaID != checkpoint.ScenicAreaID {
		return 0, errors.New("device scenic area does not match checkpoint")
	}
	return checkpoint.ScenicAreaID, nil
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
