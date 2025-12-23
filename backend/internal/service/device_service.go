package service

import (
	"errors"
	"fmt"
	"ticket-backend/internal/model"
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
}

type VerifyRequest struct {
	SystemCode   string `json:"system_code"`
	SerialNumber string `json:"serial_number"`
	TicketCode   string `json:"ticket_code"`
	MediaType    string `json:"media_type"` // qr_code, id_card
	ScanTime     string `json:"scan_time"`
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
	var tenant model.Tenant
	if err := s.DB.Where("system_code = ?", req.SystemCode).First(&tenant).Error; err != nil {
		return errors.New("invalid system_code")
	}

	var device model.Device
	// Find device by SN and Tenant
	if err := s.DB.Where("serial_number = ? AND tenant_id = ?", req.SerialNumber, tenant.ID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("device not registered")
		}
		return err
	}

	// Update status
	device.Status = "online"
	now := time.Now()
	device.LastHeartbeat = &now
	if req.IP != "" {
		device.IPAddress = req.IP
	}

	return s.DB.Save(&device).Error
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

	// 3. Delegate Verification to TicketService (Common Logic)
	// Determines CheckPointID (if bound)
	var checkPointID uint
	if device.CheckPointID != nil {
		checkPointID = *device.CheckPointID
	}

	err := s.TicketService.Verify(req.TicketCode, checkPointID, device.ID, tenant.ID)
	if err != nil {
		// Map error to Voice/Display
		errMsg := err.Error()
		resp := &VerifyResponse{Code: 403, Result: "deny", VoiceFile: "invalid.mp3"}

		if errMsg == "无效的票码" || errMsg == "Ticket Not Found" {
			resp.DisplayText = "无效票\nInvalid Ticket"
		} else if errMsg == "票据已过期" || errMsg == "Expired" {
			resp.DisplayText = "已过期\nExpired"
		} else if errMsg == "未到生效时间" {
			resp.DisplayText = "未生效\nNot Started"
		} else if errMsg == "该票据无法在此检票点使用" || errMsg == "Access Denied (No Rule)" {
			resp.DisplayText = "区域无权\nAccess Denied"
		} else if errMsg == "该检票点通行次数已用完" || errMsg == "Point Limit Reached" {
			resp.DisplayText = "次数已满\nLimit Reached"
			resp.VoiceFile = "already_used.mp3"
		} else if errMsg == "已达到该规则组允许的可选点位数量上限" || errMsg == "Group Limit Reached" {
			resp.DisplayText = "权益已尽\nNo Quota"
		} else {
			resp.DisplayText = "验证失败\n" + errMsg
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

func (s *DeviceService) Create(device *model.Device) error {
	return s.DB.Create(device).Error
}

func (s *DeviceService) Update(id uint, device *model.Device) error {
	return s.DB.Model(&model.Device{}).Where("id = ?", id).Updates(device).Error
}

func (s *DeviceService) Delete(id uint) error {
	return s.DB.Delete(&model.Device{}, id).Error
}

func (s *DeviceService) GetByID(id uint) (*model.Device, error) {
	var device model.Device
	err := s.DB.Preload("CheckPoint").First(&device, id).Error
	return &device, err
}

func (s *DeviceService) List(page, pageSize int, tenantID uint) ([]model.Device, int64, error) {
	var devices []model.Device
	var total int64

	offset := (page - 1) * pageSize

	query := s.DB.Model(&model.Device{})
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
