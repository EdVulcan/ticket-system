package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/deviceauth"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
)

const mobileVerificationSessionTTL = 8 * time.Hour

var (
	ErrMobileSessionInvalid = errors.New("移动核销会话无效或已过期")
	ErrMobileTargetDenied   = errors.New("当前账号不能使用该检票点或移动设备")
)

type MobileVerificationService struct {
	DB            *gorm.DB
	DeviceService *DeviceService
	Now           func() time.Time
}

type MobileCheckpointTarget struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Location     string `json:"location"`
	ScenicAreaID uint   `json:"scenic_area_id"`
}

type MobileDeviceTarget struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	SerialNumber string `json:"serial_number"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	CheckPointID *uint  `json:"check_point_id"`
	ScenicAreaID uint   `json:"scenic_area_id"`
}

type MobileTargetsResponse struct {
	Checkpoints []MobileCheckpointTarget `json:"checkpoints"`
	Devices     []MobileDeviceTarget     `json:"devices"`
}

type MobileSessionResponse struct {
	SessionToken string                 `json:"session_token"`
	ExpiresAt    time.Time              `json:"expires_at"`
	Checkpoint   MobileCheckpointTarget `json:"checkpoint"`
	Device       MobileDeviceTarget     `json:"device"`
}

func NewMobileVerificationService(db *gorm.DB, deviceService *DeviceService) *MobileVerificationService {
	return &MobileVerificationService{DB: db, DeviceService: deviceService}
}

func (s *MobileVerificationService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func mobileTokenHash(token string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(digest[:])
}

func mobileRoleIsAdmin(role string) bool {
	for _, value := range strings.Split(role, ",") {
		if strings.TrimSpace(value) == "admin" || strings.TrimSpace(value) == "super_admin" {
			return true
		}
	}
	return false
}

func hasMobileResourceScope(db *gorm.DB, tenantID, staffID uint, role, resourceType string, resourceID uint) (bool, error) {
	if mobileRoleIsAdmin(role) {
		return true, nil
	}
	var count int64
	err := db.Model(&model.StaffResourceScope{}).
		Where("tenant_id = ? AND staff_id = ? AND resource_type = ? AND resource_id = ?", tenantID, staffID, resourceType, resourceID).
		Count(&count).Error
	return count > 0, err
}

func (s *MobileVerificationService) Targets(tenantID, staffID uint, role string) (*MobileTargetsResponse, error) {
	if tenantID == 0 || staffID == 0 || s == nil || s.DB == nil {
		return nil, ErrMobileTargetDenied
	}
	if err := requireActiveTenantCapability(s.DB, tenantID, "supplier"); err != nil {
		return nil, err
	}
	var businessType model.SupplierBusinessType
	if err := s.DB.Where("tenant_id = ? AND business_type = ? AND status = ?", tenantID, "scenic", "active").First(&businessType).Error; err != nil {
		return nil, ErrMobileTargetDenied
	}

	var checkpoints []model.CheckPoint
	if err := s.DB.Where("tenant_id = ? AND scenic_area_id != 0", tenantID).Order("id").Find(&checkpoints).Error; err != nil {
		return nil, err
	}
	var devices []model.Device
	if err := s.DB.Where("tenant_id = ? AND type = ? AND status IN ? AND scenic_area_id != 0", tenantID, "handheld", []string{"offline", "online"}).Order("id").Find(&devices).Error; err != nil {
		return nil, err
	}

	result := &MobileTargetsResponse{Checkpoints: make([]MobileCheckpointTarget, 0), Devices: make([]MobileDeviceTarget, 0)}
	for _, checkpoint := range checkpoints {
		allowed, err := hasMobileResourceScope(s.DB, tenantID, staffID, role, "checkpoint", checkpoint.ID)
		if err != nil {
			return nil, err
		}
		if allowed {
			result.Checkpoints = append(result.Checkpoints, MobileCheckpointTarget{ID: checkpoint.ID, Name: checkpoint.Name, Location: checkpoint.Location, ScenicAreaID: checkpoint.ScenicAreaID})
		}
	}
	for _, device := range devices {
		if device.CheckPointID == nil {
			continue
		}
		allowed, err := hasMobileResourceScope(s.DB, tenantID, staffID, role, "device", device.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		result.Devices = append(result.Devices, MobileDeviceTarget{ID: device.ID, Name: device.Name, SerialNumber: device.SerialNumber, Type: device.Type, Status: device.Status, CheckPointID: device.CheckPointID, ScenicAreaID: device.ScenicAreaID})
	}
	return result, nil
}

func (s *MobileVerificationService) CreateSession(tenantID, staffID uint, role string, checkPointID, deviceID uint) (*MobileSessionResponse, error) {
	if tenantID == 0 || staffID == 0 || checkPointID == 0 || deviceID == 0 || s == nil || s.DB == nil {
		return nil, ErrMobileTargetDenied
	}
	now := s.now()
	rawToken := utils.GenerateRandomString(48)
	var response MobileSessionResponse
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var businessType model.SupplierBusinessType
		if err := tx.Where("tenant_id = ? AND business_type = ? AND status = ?", tenantID, "scenic", "active").First(&businessType).Error; err != nil {
			return ErrMobileTargetDenied
		}
		var checkpoint model.CheckPoint
		if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id != 0", checkPointID, tenantID).First(&checkpoint).Error; err != nil {
			return ErrMobileTargetDenied
		}
		allowed, err := hasMobileResourceScope(tx, tenantID, staffID, role, "checkpoint", checkpoint.ID)
		if err != nil || !allowed {
			return ErrMobileTargetDenied
		}
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ? AND check_point_id = ? AND scenic_area_id = ?", deviceID, tenantID, "handheld", []string{"offline", "online"}, checkpoint.ID, checkpoint.ScenicAreaID).First(&device).Error; err != nil {
			return ErrMobileTargetDenied
		}
		allowed, err = hasMobileResourceScope(tx, tenantID, staffID, role, "device", device.ID)
		if err != nil || !allowed {
			return ErrMobileTargetDenied
		}

		// A new browser session supersedes older sessions for the same operator.
		// Keep the old handhelds from looking online after their browser session
		// has been replaced. The current device is deliberately excluded because
		// it is made online below.
		var revokedDeviceIDs []uint
		if err := tx.Model(&model.MobileVerificationSession{}).
			Where("tenant_id = ? AND staff_id = ? AND status = ?", tenantID, staffID, "active").
			Pluck("device_id", &revokedDeviceIDs).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.MobileVerificationSession{}).
			Where("tenant_id = ? AND staff_id = ? AND status = ?", tenantID, staffID, "active").
			Updates(map[string]interface{}{"status": "revoked"}).Error; err != nil {
			return err
		}
		for _, oldDeviceID := range revokedDeviceIDs {
			if oldDeviceID == 0 || oldDeviceID == device.ID {
				continue
			}
			var other int64
			if err := tx.Model(&model.MobileVerificationSession{}).
				Where("tenant_id = ? AND device_id = ? AND status = ? AND expires_at > ?", tenantID, oldDeviceID, "active", now).
				Count(&other).Error; err != nil {
				return err
			}
			if other == 0 {
				if err := tx.Model(&model.Device{}).
					Where("id = ? AND tenant_id = ? AND type = ?", oldDeviceID, tenantID, "handheld").
					Updates(map[string]interface{}{"status": "offline", "last_heartbeat": nil}).Error; err != nil {
					return err
				}
			}
		}
		expiresAt := now.Add(mobileVerificationSessionTTL)
		session := model.MobileVerificationSession{TenantID: tenantID, StaffID: staffID, DeviceID: device.ID, CheckPointID: checkpoint.ID, ScenicAreaID: checkpoint.ScenicAreaID, TokenHash: mobileTokenHash(rawToken), Status: "active", ExpiresAt: expiresAt, LastUsedAt: &now}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		// A browser does not have a hardware heartbeat. The active session is
		// that heartbeat and makes the device eligible for the shared verifier.
		if err := tx.Model(&device).Updates(map[string]interface{}{"status": "online", "last_heartbeat": now}).Error; err != nil {
			return err
		}
		response = MobileSessionResponse{
			SessionToken: rawToken,
			ExpiresAt:    expiresAt,
			Checkpoint:   MobileCheckpointTarget{ID: checkpoint.ID, Name: checkpoint.Name, Location: checkpoint.Location, ScenicAreaID: checkpoint.ScenicAreaID},
			Device:       MobileDeviceTarget{ID: device.ID, Name: device.Name, SerialNumber: device.SerialNumber, Type: device.Type, Status: "online", CheckPointID: device.CheckPointID, ScenicAreaID: device.ScenicAreaID},
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (s *MobileVerificationService) loadSession(tenantID, staffID uint, token string) (*model.MobileVerificationSession, error) {
	if tenantID == 0 || staffID == 0 || strings.TrimSpace(token) == "" || s == nil || s.DB == nil {
		return nil, ErrMobileSessionInvalid
	}
	var session model.MobileVerificationSession
	if err := s.DB.Where("tenant_id = ? AND staff_id = ? AND token_hash = ? AND status = ?", tenantID, staffID, mobileTokenHash(token), "active").First(&session).Error; err != nil {
		return nil, ErrMobileSessionInvalid
	}
	now := s.now()
	if !session.ExpiresAt.After(now) {
		_ = s.expireSession(&session, now)
		return nil, ErrMobileSessionInvalid
	}
	return &session, nil
}

func (s *MobileVerificationService) expireSession(session *model.MobileVerificationSession, now time.Time) error {
	if session == nil || session.ID == 0 {
		return ErrMobileSessionInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.MobileVerificationSession{}).
			Where("id = ? AND status = ?", session.ID, "active").
			Update("status", "expired")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMobileSessionInvalid
		}
		var other int64
		if err := tx.Model(&model.MobileVerificationSession{}).
			Where("tenant_id = ? AND device_id = ? AND status = ? AND expires_at > ?", session.TenantID, session.DeviceID, "active", now).
			Count(&other).Error; err != nil {
			return err
		}
		if other == 0 {
			return tx.Model(&model.Device{}).
				Where("id = ? AND tenant_id = ? AND type = ?", session.DeviceID, session.TenantID, "handheld").
				Updates(map[string]interface{}{"status": "offline", "last_heartbeat": nil}).Error
		}
		return nil
	})
}

func (s *MobileVerificationService) touchSession(session *model.MobileVerificationSession) error {
	now := s.now()
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.MobileVerificationSession{}).
			Where("id = ? AND status = ? AND expires_at > ?", session.ID, "active", now).
			Updates(map[string]interface{}{"last_used_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrMobileSessionInvalid
		}
		deviceResult := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", session.DeviceID, session.TenantID, "handheld", []string{"offline", "online"}).Updates(map[string]interface{}{"status": "online", "last_heartbeat": now})
		if deviceResult.Error != nil {
			return deviceResult.Error
		}
		if deviceResult.RowsAffected == 0 {
			return ErrMobileSessionInvalid
		}
		return nil
	})
}

func (s *MobileVerificationService) Heartbeat(tenantID, staffID uint, token string) error {
	session, err := s.loadSession(tenantID, staffID, token)
	if err != nil {
		return err
	}
	return s.touchSession(session)
}

func (s *MobileVerificationService) Close(tenantID, staffID uint, token string) error {
	session, err := s.loadSession(tenantID, staffID, token)
	if err != nil {
		return err
	}
	now := s.now()
	return model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(session).Where("status = ?", "active").Updates(map[string]interface{}{"status": "revoked", "last_used_at": now}).Error; err != nil {
			return err
		}
		var other int64
		if err := tx.Model(&model.MobileVerificationSession{}).Where("tenant_id = ? AND device_id = ? AND status = ? AND expires_at > ?", tenantID, session.DeviceID, "active", now).Count(&other).Error; err != nil {
			return err
		}
		if other == 0 {
			return tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", session.DeviceID, tenantID).Updates(map[string]interface{}{"status": "offline", "last_heartbeat": nil}).Error
		}
		return nil
	})
}

func (s *MobileVerificationService) Verify(tenantID, staffID uint, token, ticketCode, requestID string) (*VerifyResponse, error) {
	if strings.TrimSpace(ticketCode) == "" || strings.TrimSpace(requestID) == "" || len(requestID) > 100 {
		return nil, errors.New("票码和请求号不能为空")
	}
	session, err := s.loadSession(tenantID, staffID, token)
	if err != nil {
		return nil, err
	}
	if err := s.touchSession(session); err != nil {
		return nil, err
	}
	if s.DeviceService == nil {
		return nil, errors.New("移动核销服务未配置")
	}
	requestHash := deviceauth.HashBody([]byte(fmt.Sprintf("%d:%d:%s:%s", session.ID, session.CheckPointID, requestID, ticketCode)))
	return s.DeviceService.VerifyDirect(DirectVerifyRequest{
		TenantID: tenantID, DeviceID: session.DeviceID, CheckPointID: session.CheckPointID,
		RequestID: requestID, RequestHash: requestHash, TicketCode: strings.TrimSpace(ticketCode), MediaType: "qr_code",
	})
}
