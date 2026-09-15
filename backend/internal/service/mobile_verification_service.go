package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/deviceauth"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const mobileVerificationSessionTTL = 8 * time.Hour

var (
	ErrMobileSessionInvalid     = errors.New("移动核销会话无效或已过期")
	ErrMobileTargetDenied       = errors.New("当前账号不能使用该检票点或移动设备")
	ErrMobileRepeatConfirmation = errors.New("该票码刚刚已核销，请确认是否继续核销")
	ErrMobilePreviewExpired     = errors.New("核销预检已过期")
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

// validateSessionTarget re-checks the mutable authorization boundary on every
// mobile request. A session is intentionally short-lived, but its checkpoint
// and handheld scope can be revoked while the browser remains open.
func (s *MobileVerificationService) validateSessionTarget(session *model.MobileVerificationSession, role string) error {
	if session == nil || session.ID == 0 || s == nil || s.DB == nil {
		return ErrMobileSessionInvalid
	}
	var checkpoint model.CheckPoint
	if err := s.DB.Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND scenic_area_id != 0", session.CheckPointID, session.TenantID, session.ScenicAreaID).First(&checkpoint).Error; err != nil {
		return ErrMobileSessionInvalid
	}
	var device model.Device
	if err := s.DB.Where("id = ? AND tenant_id = ? AND type = ? AND check_point_id = ? AND scenic_area_id = ? AND status IN ?", session.DeviceID, session.TenantID, "handheld", session.CheckPointID, session.ScenicAreaID, []string{"offline", "online"}).First(&device).Error; err != nil {
		return ErrMobileSessionInvalid
	}
	if strings.TrimSpace(role) == "" {
		var staff model.Staff
		if err := s.DB.Select("roles").Where("id = ? AND tenant_id = ? AND status = ?", session.StaffID, session.TenantID, "active").First(&staff).Error; err != nil {
			return ErrMobileSessionInvalid
		}
		role = staff.Roles
	}
	if mobileRoleIsAdmin(role) {
		return nil
	}
	checkpointAllowed, err := hasMobileResourceScope(s.DB, session.TenantID, session.StaffID, role, "checkpoint", session.CheckPointID)
	if err != nil || !checkpointAllowed {
		return ErrMobileSessionInvalid
	}
	deviceAllowed, err := hasMobileResourceScope(s.DB, session.TenantID, session.StaffID, role, "device", session.DeviceID)
	if err != nil || !deviceAllowed {
		return ErrMobileSessionInvalid
	}
	return nil
}

func (s *MobileVerificationService) revokeSession(session *model.MobileVerificationSession, now time.Time) error {
	if session == nil || session.ID == 0 {
		return ErrMobileSessionInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.MobileVerificationSession{}).
			Where("id = ? AND status = ?", session.ID, "active").
			Update("status", "revoked")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var other int64
		if err := tx.Model(&model.MobileVerificationSession{}).
			Where("tenant_id = ? AND device_id = ? AND status = ? AND expires_at > ?", session.TenantID, session.DeviceID, "active", now).
			Count(&other).Error; err != nil {
			return err
		}
		if other == 0 {
			return tx.Model(&model.Device{}).
				Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", session.DeviceID, session.TenantID, "handheld", []string{"offline", "online"}).
				Updates(map[string]interface{}{"status": "offline", "last_heartbeat": nil}).Error
		}
		return nil
	})
}

func (s *MobileVerificationService) loadSession(tenantID, staffID uint, token string, role ...string) (*model.MobileVerificationSession, error) {
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
	currentRole := ""
	if len(role) > 0 {
		currentRole = strings.TrimSpace(role[0])
	}
	if err := s.validateSessionTarget(&session, currentRole); err != nil {
		_ = s.revokeSession(&session, now)
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

func (s *MobileVerificationService) Heartbeat(tenantID, staffID uint, token string, role ...string) error {
	session, err := s.loadSession(tenantID, staffID, token, role...)
	if err != nil {
		return err
	}
	return s.touchSession(session)
}

func (s *MobileVerificationService) Close(tenantID, staffID uint, token string, role ...string) error {
	session, err := s.loadSession(tenantID, staffID, token, role...)
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

func (s *MobileVerificationService) Verify(tenantID, staffID uint, token, ticketCode, requestID string, role ...string) (*VerifyResponse, error) {
	ticketCode = strings.TrimSpace(ticketCode)
	requestID = strings.TrimSpace(requestID)
	if ticketCode == "" || requestID == "" || len(requestID) > 100 {
		return nil, errors.New("票码和请求号不能为空")
	}
	session, err := s.loadSession(tenantID, staffID, token, role...)
	if err != nil {
		return nil, err
	}
	if err := s.touchSession(session); err != nil {
		return nil, err
	}
	// Keep the legacy one-step endpoint compatible, but prevent a different
	// request from immediately consuming the same shared code again. Replays of
	// the same request ID remain idempotent in DeviceService.
	var recent model.CheckInRecord
	if err := s.DB.Where("tenant_id = ? AND device_id = ? AND check_point_id = ? AND ticket_code = ? AND result = ? AND device_request_id <> ? AND check_in_time > ?", tenantID, session.DeviceID, session.CheckPointID, ticketCode, "success", requestID, s.now().Add(-10*time.Second)).Order("check_in_time desc").First(&recent).Error; err == nil {
		return nil, ErrMobileRepeatConfirmation
	}
	if s.DeviceService == nil {
		return nil, errors.New("移动核销服务未配置")
	}
	// The request hash is the client-visible idempotency contract. It must stay
	// stable when an operator has to sign in again after a lost response, while
	// still binding a request to the authenticated tenant, device, checkpoint,
	// request ID and normalized ticket code. Do not include the short-lived
	// browser session row ID here.
	requestHash := deviceauth.HashBody([]byte(fmt.Sprintf("%d:%d:%d:%s:%s", tenantID, session.DeviceID, session.CheckPointID, requestID, ticketCode)))
	return s.DeviceService.VerifyDirect(DirectVerifyRequest{
		TenantID: tenantID, DeviceID: session.DeviceID, CheckPointID: session.CheckPointID,
		RequestID: requestID, RequestHash: requestHash, TicketCode: ticketCode, MediaType: "qr_code",
	})
}

type MobileVerificationPreviewResponse struct {
	PreviewID                  string                 `json:"preview_id"`
	ExpiresAt                  time.Time              `json:"expires_at"`
	ProductName                string                 `json:"product_name"`
	CodeMode                   string                 `json:"code_mode"`
	BatchAllowed               bool                   `json:"batch_allowed"`
	MaxQuantity                int                    `json:"max_quantity"`
	PointUsed                  int                    `json:"point_used"`
	PointRemaining             int                    `json:"point_remaining"`
	RequiresRepeatConfirmation bool                   `json:"requires_repeat_confirmation"`
	RecentOperation            *MobileRecentOperation `json:"recent_operation,omitempty"`
}

type MobileRecentOperation struct {
	OperationID string    `json:"operation_id"`
	Quantity    int       `json:"quantity"`
	CompletedAt time.Time `json:"completed_at"`
}

type MobileVerificationOperationResponse struct {
	OperationID    string     `json:"operation_id"`
	Status         string     `json:"status"`
	Result         string     `json:"result,omitempty"`
	ReasonCode     string     `json:"reason_code,omitempty"`
	DisplayText    string     `json:"display_text,omitempty"`
	Quantity       int        `json:"quantity"`
	PointRemaining int        `json:"point_remaining"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

func (s *MobileVerificationService) VerificationPreview(tenantID, staffID uint, token, ticketCode string, role ...string) (*MobileVerificationPreviewResponse, error) {
	session, err := s.loadSession(tenantID, staffID, token, role...)
	if err != nil {
		return nil, err
	}
	ticketCode = strings.TrimSpace(ticketCode)
	if ticketCode == "" {
		return nil, errors.New("票码不能为空")
	}
	var ticket model.Ticket
	if err := s.DB.Preload("OrderItem.Product").Where("ticket_code = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", ticketCode, tenantID, tenantID).First(&ticket).Error; err != nil {
		return nil, ErrInvalidTicket
	}
	if ticket.Status == "refunded" {
		return nil, ErrTicketRefunded
	}
	if ticket.PendingRefundID != 0 || ticket.PendingXiaohongshuVerificationID != 0 {
		return nil, ErrTicketUnavailable
	}
	batchAllowed := ticket.CodeMode == "order" && ticket.OrderItem.Quantity > 1
	var order model.Order
	salesTenantID := ticket.TenantID
	if salesTenantID == 0 {
		salesTenantID = ticket.OrderItem.Product.TenantID
	}
	if err := s.DB.Where("id = ? AND tenant_id = ?", ticket.OrderItem.OrderID, salesTenantID).First(&order).Error; err != nil {
		return nil, ErrInvalidTicket
	}
	if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
		return nil, ErrOrderNotPaid
	}
	if err := EnsureNoXiaohongshuRefundHoldTx(s.DB, &order); err != nil {
		return nil, err
	}
	if ticket.OrderItem.ValidityStart != nil && s.now().Before(*ticket.OrderItem.ValidityStart) {
		return nil, ErrTicketNotStarted
	}
	if ticket.OrderItem.ValidityEnd != nil && s.now().After(*ticket.OrderItem.ValidityEnd) {
		return nil, ErrTicketExpired
	}
	var product model.Product
	if ticket.RuleSnapshot != "" {
		var rule model.TicketRule
		if json.Unmarshal([]byte(ticket.RuleSnapshot), &rule) != nil {
			return nil, ErrInvalidTicket
		}
		product.CodeMode = ticket.CodeMode
		product.Rule = rule
	} else {
		productID := ticket.FulfillmentProductID
		if productID == 0 {
			productID = ticket.OrderItem.FulfillmentProductID
		}
		if productID == 0 {
			productID = ticket.OrderItem.ProductID
		}
		if err := s.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return nil, ErrInvalidTicket
		}
	}
	group, item := matchRule(product.Rule, session.CheckPointID)
	if group == nil || item == nil {
		return nil, ErrAccessDenied
	}
	var records []model.CheckInRecord
	if err := s.DB.Where("ticket_id = ? AND result = ?", ticket.ID, "success").Find(&records).Error; err != nil {
		return nil, err
	}
	used := countAtCheckpoint(records, session.CheckPointID)
	limit := admissionLimit(&product, &ticket.OrderItem, item)
	if limit <= used {
		return nil, ErrPointLimitReached
	}
	max := limit - used
	if !batchAllowed {
		max = 1
	}
	if batchAllowed {
		max = 0
		simulated := append([]model.CheckInRecord(nil), records...)
		for max < limit-used {
			if !groupAllowsCheckpointForTicket(simulated, &product, &ticket.OrderItem, group, session.CheckPointID) {
				break
			}
			simulated = append(simulated, model.CheckInRecord{CheckPointID: session.CheckPointID})
			max++
		}
	}
	if max < 1 {
		return nil, ErrPointLimitReached
	}
	now := s.now()
	var recent model.MobileVerificationOperation
	resp := &MobileVerificationPreviewResponse{PreviewID: utils.GenerateRandomString(32), ExpiresAt: now.Add(2 * time.Minute), ProductName: ticket.OrderItem.ProductName, CodeMode: ticket.CodeMode, BatchAllowed: batchAllowed, MaxQuantity: max, PointUsed: used, PointRemaining: max}
	if resp.ProductName == "" {
		resp.ProductName = ticket.OrderItem.Product.Name
	}
	if err := s.DB.Where("tenant_id = ? AND device_id = ? AND check_point_id = ? AND ticket_code = ? AND status = ? AND completed_at > ?", tenantID, session.DeviceID, session.CheckPointID, ticketCode, "completed", now.Add(-10*time.Second)).Order("completed_at desc").First(&recent).Error; err == nil {
		resp.RequiresRepeatConfirmation = true
		resp.RecentOperation = &MobileRecentOperation{OperationID: recent.OperationID, Quantity: recent.Quantity, CompletedAt: *recent.CompletedAt}
	}
	preview := model.MobileVerificationPreview{PreviewID: resp.PreviewID, TenantID: tenantID, StaffID: staffID, SessionID: session.ID, DeviceID: session.DeviceID, CheckPointID: session.CheckPointID, ScenicAreaID: session.ScenicAreaID, TicketCode: ticketCode, ProductName: resp.ProductName, CodeMode: resp.CodeMode, BatchAllowed: batchAllowed, MaxQuantity: max, PointUsed: used, PointRemaining: max, RequiresRepeatConfirmation: resp.RequiresRepeatConfirmation, ExpiresAt: resp.ExpiresAt, Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&preview).Error }); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *MobileVerificationService) VerificationOperation(tenantID, staffID uint, token, previewID, operationID string, quantity int, continuationOf string, role ...string) (*MobileVerificationOperationResponse, error) {
	session, err := s.loadSession(tenantID, staffID, token, role...)
	if err != nil {
		return nil, err
	}
	operationID = strings.TrimSpace(operationID)
	previewID = strings.TrimSpace(previewID)
	continuationOf = strings.TrimSpace(continuationOf)
	if operationID == "" || previewID == "" || quantity < 1 {
		return nil, errors.New("操作号和核销数量不能为空")
	}
	if s.DeviceService == nil {
		return nil, errors.New("移动核销服务未配置")
	}
	var op model.MobileVerificationOperation
	findErr := s.DB.Where("operation_id = ?", operationID).First(&op).Error
	if findErr == nil {
		if op.TenantID != tenantID || op.StaffID != staffID || op.Quantity != quantity || op.DeviceID != session.DeviceID || op.CheckPointID != session.CheckPointID || op.ContinuationOf != continuationOf {
			return nil, errors.New("同一操作号不能用于不同核销内容")
		}
		if op.Status == "completed" || op.Status == "denied" {
			return mobileOperationResponse(&op), nil
		}
		if op.UpdatedAt.After(s.now().Add(-30 * time.Second)) {
			return mobileOperationResponse(&op), nil
		}
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, findErr
	}
	var preview model.MobileVerificationPreview
	if op.ID != 0 {
		if err := s.DB.Where("preview_id = ? AND tenant_id = ? AND staff_id = ? AND operation_id = ?", previewID, tenantID, staffID, operationID).First(&preview).Error; err != nil {
			return nil, ErrMobilePreviewExpired
		}
	} else {
		err := model.Write(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("preview_id = ? AND tenant_id = ? AND staff_id = ? AND session_id = ? AND status = ?", previewID, tenantID, staffID, session.ID, "active").First(&preview).Error; err != nil {
				return ErrMobilePreviewExpired
			}
			if !preview.ExpiresAt.After(s.now()) {
				if err := tx.Model(&preview).Update("status", "expired").Error; err != nil {
					return err
				}
				return ErrMobilePreviewExpired
			}
			if quantity > preview.MaxQuantity || (!preview.BatchAllowed && quantity != 1) {
				return errors.New("核销数量超出预检范围")
			}
			if preview.RequiresRepeatConfirmation && continuationOf != latestMobileOperationID(tx, &preview) {
				return ErrMobileRepeatConfirmation
			}
			hash := deviceauth.HashBody([]byte(fmt.Sprintf("%d:%d:%d:%s:%d:%s", tenantID, session.DeviceID, session.CheckPointID, preview.TicketCode, quantity, continuationOf)))
			op = model.MobileVerificationOperation{OperationID: operationID, TenantID: tenantID, StaffID: staffID, SessionID: session.ID, DeviceID: session.DeviceID, CheckPointID: session.CheckPointID, ScenicAreaID: session.ScenicAreaID, TicketCode: preview.TicketCode, Quantity: quantity, ContinuationOf: continuationOf, RequestHash: hash, Status: "processing"}
			result := tx.Model(&model.MobileVerificationPreview{}).Where("id = ? AND status = ?", preview.ID, "active").Updates(map[string]interface{}{"status": "consuming", "operation_id": operationID})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrMobilePreviewExpired
			}
			return tx.Create(&op).Error
		})
		if err != nil {
			return nil, err
		}
	}
	response, verifyErr := s.DeviceService.VerifyDirect(DirectVerifyRequest{TenantID: tenantID, DeviceID: session.DeviceID, CheckPointID: session.CheckPointID, RequestID: operationID, RequestHash: op.RequestHash, TicketCode: preview.TicketCode, MediaType: "qr_code", Quantity: quantity})
	if errors.Is(verifyErr, ErrVerificationProcessing) || (verifyErr == nil && response != nil && response.ReasonCode == "processing") {
		if err := s.DB.Model(&model.MobileVerificationOperation{}).Where("operation_id = ? AND status = ?", operationID, "processing").Update("updated_at", s.now()).Error; err != nil {
			return nil, err
		}
		op.Status = "processing"
		return mobileOperationResponse(&op), nil
	}
	now := s.now()
	status, result, reason, display := "completed", "allow", "verified", "核销成功"
	if verifyErr != nil {
		status, result, reason, display = "denied", "deny", reasonCodeForError(verifyErr), verifyErr.Error()
	} else if response != nil {
		result, reason, display = response.Result, response.ReasonCode, response.DisplayText
		if result != "allow" {
			status = "denied"
		}
	}
	pointRemaining := preview.PointRemaining
	if result == "allow" {
		if current, err := currentMobilePointRemaining(s.DB, tenantID, preview.TicketCode, session.CheckPointID); err == nil {
			pointRemaining = current
		} else {
			pointRemaining -= quantity
			if pointRemaining < 0 {
				pointRemaining = 0
			}
		}
	}
	if err := model.Write(func(tx *gorm.DB) error {
		update := tx.Model(&model.MobileVerificationOperation{}).Where("operation_id = ? AND tenant_id = ? AND staff_id = ? AND status = ?", operationID, tenantID, staffID, "processing").Updates(map[string]interface{}{"status": status, "result": result, "reason_code": reason, "display_text": display, "point_remaining": pointRemaining, "completed_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return errors.New("移动核销操作状态已变化")
		}
		return tx.Model(&model.MobileVerificationPreview{}).Where("id = ? AND operation_id = ? AND status = ?", preview.ID, operationID, "consuming").Update("status", "consumed").Error
	}); err != nil {
		return nil, err
	}
	op.Status, op.Result, op.ReasonCode, op.DisplayText, op.PointRemaining, op.CompletedAt = status, result, reason, display, pointRemaining, &now
	return mobileOperationResponse(&op), nil
}

func latestMobileOperationID(db *gorm.DB, p *model.MobileVerificationPreview) string {
	var op model.MobileVerificationOperation
	if db.Where("tenant_id = ? AND device_id = ? AND check_point_id = ? AND ticket_code = ? AND status = ?", p.TenantID, p.DeviceID, p.CheckPointID, p.TicketCode, "completed").Order("completed_at desc").First(&op).Error == nil {
		return op.OperationID
	}
	return ""
}

func currentMobilePointRemaining(db *gorm.DB, tenantID uint, ticketCode string, checkpointID uint) (int, error) {
	var ticket model.Ticket
	if err := db.Preload("OrderItem.Product").Where("ticket_code = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", ticketCode, tenantID, tenantID).First(&ticket).Error; err != nil {
		return 0, err
	}
	var product model.Product
	if ticket.RuleSnapshot != "" {
		var rule model.TicketRule
		if err := json.Unmarshal([]byte(ticket.RuleSnapshot), &rule); err != nil {
			return 0, err
		}
		product.CodeMode = ticket.CodeMode
		product.Rule = rule
	} else {
		productID := ticket.FulfillmentProductID
		if productID == 0 {
			productID = ticket.OrderItem.FulfillmentProductID
		}
		if productID == 0 {
			productID = ticket.OrderItem.ProductID
		}
		if err := db.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return 0, err
		}
	}
	group, item := matchRule(product.Rule, checkpointID)
	if group == nil || item == nil {
		return 0, ErrAccessDenied
	}
	var records []model.CheckInRecord
	if err := db.Where("ticket_id = ? AND result = ?", ticket.ID, "success").Find(&records).Error; err != nil {
		return 0, err
	}
	return remainingAdmissionsAtCheckpoint(records, &product, &ticket.OrderItem, group, item, checkpointID), nil
}

func mobileOperationResponse(op *model.MobileVerificationOperation) *MobileVerificationOperationResponse {
	return &MobileVerificationOperationResponse{OperationID: op.OperationID, Status: op.Status, Result: op.Result, ReasonCode: op.ReasonCode, DisplayText: op.DisplayText, Quantity: op.Quantity, PointRemaining: op.PointRemaining, CompletedAt: op.CompletedAt}
}
func (s *MobileVerificationService) GetVerificationOperation(tenantID, staffID uint, token, operationID string, role ...string) (*MobileVerificationOperationResponse, error) {
	if _, err := s.loadSession(tenantID, staffID, token, role...); err != nil {
		return nil, err
	}
	var op model.MobileVerificationOperation
	if err := s.DB.Where("operation_id = ? AND tenant_id = ? AND staff_id = ?", operationID, tenantID, staffID).First(&op).Error; err != nil {
		return nil, err
	}
	return mobileOperationResponse(&op), nil
}
