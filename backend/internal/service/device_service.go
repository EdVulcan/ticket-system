package service

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeviceService struct {
	DB            *gorm.DB
	TicketService *TicketService
	Maintenance   DeviceMaintenanceInvalidator
	// NewXiaohongshuClient is injectable for protocol tests. Production uses
	// the official merchant self-developed /mp client.
	NewXiaohongshuClient func(appID, secret, environment string) *xiaohongshu.Client
	Now                  func() time.Time
}

// DeviceMaintenanceInvalidator is the narrow control-plane seam used by the
// device service when a device lifecycle change must immediately terminate
// the maintenance data plane. Keeping the dependency small avoids coupling
// ticket verification to the WSS implementation.
type DeviceMaintenanceInvalidator interface {
	DisconnectDevice(deviceID uint, reason string)
}

// MarkOffline transitions stale devices and opens one alert per incident.
// It is safe to run from more than one scheduler because the status predicate
// and open-alert lookup make the operation idempotent.
func (s *DeviceService) MarkOffline(now time.Time, timeout time.Duration) (int, error) {
	changed := 0
	var disconnected []uint
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
			disconnected = append(disconnected, devices[i].ID)
			changed++
		}
		return nil
	})
	if err == nil && s != nil && s.Maintenance != nil {
		for _, deviceID := range disconnected {
			s.Maintenance.DisconnectDevice(deviceID, "设备心跳超时")
		}
	}
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
	return &DeviceService{DB: db, TicketService: ts, NewXiaohongshuClient: xiaohongshu.NewClient}
}

func (s *DeviceService) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
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
	VoiceCode    string `json:"voice_code"`    // device-local audio resource identifier
	OpenDuration int    `json:"open_duration"` // ms
}

type DirectVerifyRequest struct {
	TenantID     uint
	DeviceID     uint
	CheckPointID uint
	RequestID    string
	RequestHash  string
	TicketCode   string
	MediaType    string
	ScanTime     string
}

type OpenResultRequest struct {
	VerificationRequestID string `json:"verification_request_id" binding:"required"`
	Status                string `json:"status" binding:"required"` // opened, failed, unknown
	Error                 string `json:"error"`
	OccurredAt            string `json:"occurred_at"`
}

var ErrVerificationProcessing = errors.New("verification request is processing")

// ErrHardwareCommandExpired is returned when a device tries to acknowledge a
// command after its delivery window. Expired commands are terminal and must
// never become an execution acknowledgement later.
var ErrHardwareCommandExpired = errors.New("hardware command expired")

const deviceVerificationProcessingTTL = 5 * time.Minute

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

type DirectHardwareAckRequest struct {
	CommandNo string `json:"command_no" binding:"required"`
	AckToken  string `json:"ack_token" binding:"required"`
	Status    string `json:"status" binding:"required"`
	Payload   string `json:"payload"`
	Error     string `json:"error"`
}

var supportedDeviceTypes = map[string]struct{}{
	"gate":     {},
	"handheld": {},
	"pos":      {},
}

var hardwareCommandMatrix = map[string]map[string]struct{}{
	"gate": {
		"verify":    {},
		"open_gate": {},
	},
	"handheld": {
		"verify":        {},
		"read_identity": {},
	},
	"pos": {
		"print":         {},
		"read_identity": {},
	},
}

func validDeviceType(deviceType string) bool {
	_, ok := supportedDeviceTypes[strings.TrimSpace(deviceType)]
	return ok
}

func hardwareCommandAllowed(deviceType, kind string) bool {
	commands, ok := hardwareCommandMatrix[strings.TrimSpace(deviceType)]
	if !ok {
		return false
	}
	_, ok = commands[strings.TrimSpace(kind)]
	return ok
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
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", req.DeviceID, req.TenantID, "online").First(&device).Error; err != nil {
			return errors.New("device is unavailable")
		}
		if device.ScenicAreaID == 0 {
			return errors.New("device has no scenic area")
		}
		if !hardwareCommandAllowed(device.Type, strings.TrimSpace(req.Kind)) {
			return errors.New("command is not allowed for this device type")
		}
		now := s.now()
		command = model.HardwareCommand{
			TenantID: req.TenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: req.DeviceID,
			CommandNo: fmt.Sprintf("CMD%d", now.UnixNano()), Kind: strings.TrimSpace(req.Kind),
			PayloadJSON: req.PayloadJSON, Status: "queued", AckToken: utils.GenerateRandomString(32),
			QueuedAt: now, ExpiresAt: now.Add(req.TTL),
		}
		return tx.Create(&command).Error
	})
	if err != nil {
		return nil, err
	}
	return &command, nil
}

// expireHardwareCommandsTx materializes expiry instead of merely hiding old
// commands from polls. Keeping delivered commands in the predicate matters:
// a response can be lost after dispatch, but it must still become terminal
// once the command's execution window has passed.
func expireHardwareCommandsTx(tx *gorm.DB, tenantID, deviceID uint, now time.Time) error {
	return tx.Model(&model.HardwareCommand{}).
		Where("tenant_id = ? AND device_id = ? AND status IN ? AND expires_at <= ?", tenantID, deviceID, []string{"queued", "delivered"}, now).
		Updates(map[string]interface{}{"status": "expired", "last_error": ErrHardwareCommandExpired.Error()}).Error
}

// dequeueHardwareCommandTx performs a safety-first, one-time dispatch. The
// API response contains CommandNo so the device can persist it before
// physical execution and make acknowledgement retries idempotent.
func dequeueHardwareCommandTx(tx *gorm.DB, tenantID, deviceID uint, now time.Time, command *model.HardwareCommand) error {
	if err := expireHardwareCommandsTx(tx, tenantID, deviceID, now); err != nil {
		return err
	}
	result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND device_id = ? AND status = ? AND expires_at > ?", tenantID, deviceID, "queued", now).
		Order("id").
		First(command)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil
	}
	if result.Error != nil {
		return result.Error
	}
	command.Status = "delivered"
	command.AttemptCount++
	command.DeliveredAt = &now
	return tx.Model(command).Updates(map[string]interface{}{
		"status":        command.Status,
		"attempt_count": command.AttemptCount,
		"delivered_at":  now,
	}).Error
}

func acknowledgeHardwareCommandTx(tx *gorm.DB, command *model.HardwareCommand, status, errorMessage, payload string, now time.Time) error {
	if command.Status == "expired" || !command.ExpiresAt.After(now) {
		if command.Status != "expired" {
			if err := tx.Model(command).Updates(map[string]interface{}{"status": "expired", "last_error": ErrHardwareCommandExpired.Error()}).Error; err != nil {
				return err
			}
		}
		return ErrHardwareCommandExpired
	}
	if command.Status == "acknowledged" || command.Status == "failed" {
		if command.Status == status {
			return nil
		}
		return errors.New("hardware command already has a different terminal acknowledgement")
	}
	if command.Status != "delivered" {
		return errors.New("hardware command has not been delivered")
	}
	updates := map[string]interface{}{"status": status, "acked_at": now, "last_error": strings.TrimSpace(errorMessage)}
	if err := tx.Model(command).Updates(updates).Error; err != nil {
		return err
	}
	return tx.Create(&model.HardwareEvent{TenantID: command.TenantID, DeviceID: command.DeviceID, CommandNo: command.CommandNo, EventType: status, Payload: payload}).Error
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
		if device.Status != "online" {
			return errors.New("device is unavailable")
		}
		return dequeueHardwareCommandTx(tx, tenant.ID, device.ID, s.now(), &command)
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
	expired := false
	err := model.Write(func(tx *gorm.DB) error {
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
		err := acknowledgeHardwareCommandTx(tx, &command, req.Status, req.Error, req.Payload, s.now())
		if errors.Is(err, ErrHardwareCommandExpired) {
			expired = true
			// The expiry update is a durable safety fact. Commit it even though
			// the caller still needs the explicit expired error below.
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if expired {
		return ErrHardwareCommandExpired
	}
	return nil
}

func (s *DeviceService) PollHardwareCommandByDevice(tenantID, deviceID uint) (*model.HardwareCommand, error) {
	var command model.HardwareCommand
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", deviceID, tenantID, "online").First(&device).Error; err != nil {
			return errors.New("device is unavailable")
		}
		return dequeueHardwareCommandTx(tx, tenantID, deviceID, s.now(), &command)
	})
	if err != nil || command.ID == 0 {
		return nil, err
	}
	return &command, nil
}

func (s *DeviceService) AckHardwareCommandByDevice(tenantID, deviceID uint, req DirectHardwareAckRequest) error {
	if req.Status != "acknowledged" && req.Status != "failed" {
		return errors.New("invalid hardware acknowledgement status")
	}
	expired := false
	err := model.Write(func(tx *gorm.DB) error {
		var command model.HardwareCommand
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("command_no = ? AND tenant_id = ? AND device_id = ?", req.CommandNo, tenantID, deviceID).First(&command).Error; err != nil {
			return err
		}
		if command.AckToken != req.AckToken {
			return errors.New("invalid acknowledgement token")
		}
		err := acknowledgeHardwareCommandTx(tx, &command, req.Status, req.Error, req.Payload, s.now())
		if errors.Is(err, ErrHardwareCommandExpired) {
			expired = true
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	if expired {
		return ErrHardwareCommandExpired
	}
	return nil
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

func (s *DeviceService) HeartbeatDirect(tenantID, deviceID uint, ip, status string) error {
	return model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND status != ?", deviceID, tenantID, "disabled").First(&device).Error; err != nil {
			return errors.New("设备未登记")
		}
		status = strings.TrimSpace(status)
		if status != "fault" {
			status = "online"
		}
		now := time.Now()
		updates := map[string]interface{}{"status": status, "last_heartbeat": now}
		if strings.TrimSpace(ip) != "" {
			updates["ip_address"] = strings.TrimSpace(ip)
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
	if err := s.DB.Where("serial_number = ? AND tenant_id = ? AND status = ?", req.SerialNumber, tenant.ID, "online").First(&device).Error; err != nil {
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
		return denyResponse(err), nil
	}

	return s.allowResponse(req.TicketCode), nil
}

func (s *DeviceService) VerifyDirect(req DirectVerifyRequest) (*VerifyResponse, error) {
	if req.TenantID == 0 || req.DeviceID == 0 || req.CheckPointID == 0 || strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.TicketCode) == "" {
		return nil, errors.New("设备、检票点、请求号和票码不能为空")
	}
	var device model.Device
	if err := s.DB.Where("id = ? AND tenant_id = ? AND check_point_id = ? AND scenic_area_id != 0", req.DeviceID, req.TenantID, req.CheckPointID).First(&device).Error; err != nil {
		return denyResponse(ErrAccessDenied), nil
	}
	if device.Status != "online" {
		return denyResponse(ErrAccessDenied), nil
	}
	if device.Type != "gate" && device.Type != "handheld" {
		return denyResponse(ErrAccessDenied), nil
	}
	if voucherLink, localTicketCode, found, resolveErr := s.resolveXiaohongshuVoucher(req.TenantID, req.TicketCode); resolveErr != nil {
		return nil, resolveErr
	} else if found {
		return s.verifyXiaohongshuVoucher(req, device, voucherLink, localTicketCode)
	}

	verification, replay, err := s.beginDeviceVerification(req, device.ScenicAreaID)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		return replay, nil
	}

	verifyErr := s.TicketService.VerifyDeviceRequest(req.TicketCode, req.CheckPointID, req.DeviceID, req.TenantID, req.RequestID)
	resp := denyResponse(verifyErr)
	if verifyErr == nil {
		resp = s.allowResponse(req.TicketCode)
	}
	var checkIn model.CheckInRecord
	_ = s.DB.Where("device_id = ? AND device_request_id = ?", req.DeviceID, req.RequestID).Order("id desc").First(&checkIn).Error
	if checkIn.ID != 0 {
		// A concurrent retry may have completed the ticket while this process was
		// waiting. Prefer the durable admission fact over a transient error from
		// the losing verifier so the request remains idempotent.
		resp = responseFromCheckIn(s, &checkIn)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.DeviceVerification{}).Where("id = ? AND status = ?", verification.ID, "processing").Updates(map[string]interface{}{
			"status": "completed", "response_code": resp.Code, "result": resp.Result, "display_text": resp.DisplayText,
			"voice_file": resp.VoiceFile, "voice_code": resp.VoiceCode, "open_duration": resp.OpenDuration, "check_in_record_id": checkIn.ID,
			"open_status": map[bool]string{true: "pending", false: ""}[resp.Result == "allow"],
		}).Error
	}); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *DeviceService) beginDeviceVerification(req DirectVerifyRequest, scenicAreaID uint) (*model.DeviceVerification, *VerifyResponse, error) {
	var existing model.DeviceVerification
	err := s.DB.Where("device_id = ? AND request_id = ?", req.DeviceID, req.RequestID).First(&existing).Error
	if err == nil {
		if existing.RequestHash != req.RequestHash || existing.TicketCode != req.TicketCode {
			return nil, nil, errors.New("同一请求号不能用于不同票码或请求内容")
		}
		if existing.Status == "completed" {
			return &existing, responseFromVerification(&existing), nil
		}
		var record model.CheckInRecord
		if findErr := s.DB.Where("device_id = ? AND device_request_id = ?", req.DeviceID, req.RequestID).Order("id desc").First(&record).Error; findErr == nil {
			resp := responseFromCheckIn(s, &record)
			_ = model.Write(func(tx *gorm.DB) error {
				return tx.Model(&existing).Updates(map[string]interface{}{"status": "completed", "response_code": resp.Code, "result": resp.Result, "display_text": resp.DisplayText, "voice_file": resp.VoiceFile, "voice_code": resp.VoiceCode, "open_duration": resp.OpenDuration, "check_in_record_id": record.ID, "open_status": map[bool]string{true: "pending", false: ""}[resp.Result == "allow"]}).Error
			})
			return &existing, resp, nil
		}
		// A process can die after creating the processing row but before calling
		// TicketService. A stale row may be safely taken over: the ticket service
		// still locks the ticket and the request ID remains the idempotency key.
		cutoff := time.Now().Add(-deviceVerificationProcessingTTL)
		result := s.DB.Model(&model.DeviceVerification{}).
			Where("id = ? AND status = ? AND updated_at <= ?", existing.ID, "processing", cutoff).
			Update("updated_at", time.Now())
		if result.Error != nil {
			return nil, nil, result.Error
		}
		if result.RowsAffected == 1 {
			existing.UpdatedAt = time.Now()
			return &existing, nil, nil
		}
		return &existing, nil, ErrVerificationProcessing
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	created := model.DeviceVerification{TenantID: req.TenantID, ScenicAreaID: scenicAreaID, DeviceID: req.DeviceID, RequestID: req.RequestID, RequestHash: req.RequestHash, TicketCode: req.TicketCode, Status: "processing"}
	if createErr := model.Write(func(tx *gorm.DB) error { return tx.Create(&created).Error }); createErr != nil {
		if loadErr := s.DB.Where("device_id = ? AND request_id = ?", req.DeviceID, req.RequestID).First(&existing).Error; loadErr != nil {
			return nil, nil, createErr
		}
		return s.beginDeviceVerification(req, scenicAreaID)
	}
	return &created, nil, nil
}

func (s *DeviceService) ReportOpenResult(tenantID, deviceID uint, req OpenResultRequest) error {
	req.Status = strings.TrimSpace(req.Status)
	if req.Status != "opened" && req.Status != "failed" && req.Status != "unknown" {
		return errors.New("开闸结果必须是 opened、failed 或 unknown")
	}
	return model.Write(func(tx *gorm.DB) error {
		var verification model.DeviceVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND device_id = ? AND request_id = ? AND status = ? AND result = ?", tenantID, deviceID, strings.TrimSpace(req.VerificationRequestID), "completed", "allow").First(&verification).Error; err != nil {
			return errors.New("未找到允许通行的核销请求")
		}
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", deviceID, tenantID, "gate").First(&device).Error; err != nil {
			return errors.New("只有闸机设备可以回报开闸结果")
		}
		if verification.OpenStatus == req.Status {
			return nil
		}
		if verification.OpenStatus == "opened" || (verification.OpenStatus == "failed" && req.Status != "opened") {
			return errors.New("该核销请求已经上报过不同的开闸结果")
		}
		now := time.Now()
		occurredAt := now
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.OccurredAt)); err == nil {
			occurredAt = parsed
		}
		updates := map[string]interface{}{"open_status": req.Status, "open_error": strings.TrimSpace(req.Error), "open_reported_at": now}
		if req.Status == "opened" {
			updates["opened_at"] = occurredAt
		}
		if err := tx.Model(&verification).Updates(updates).Error; err != nil {
			return err
		}
		eventType := "gate_open_failed"
		if req.Status == "unknown" {
			eventType = "gate_open_unknown"
		}
		if req.Status == "opened" {
			eventType = "gate_opened"
		}
		return tx.Create(&model.HardwareEvent{TenantID: tenantID, DeviceID: deviceID, CommandNo: "VERIFY:" + verification.RequestID, EventType: eventType, Payload: strings.TrimSpace(req.Error)}).Error
	})
}

func responseFromVerification(row *model.DeviceVerification) *VerifyResponse {
	return &VerifyResponse{Code: row.ResponseCode, Result: row.Result, DisplayText: row.DisplayText, VoiceFile: row.VoiceFile, VoiceCode: row.VoiceCode, OpenDuration: row.OpenDuration}
}

func responseFromCheckIn(s *DeviceService, record *model.CheckInRecord) *VerifyResponse {
	if record.Result == "success" {
		return s.allowResponse(record.TicketCode)
	}
	return denyResponse(errors.New(record.Message))
}

func (s *DeviceService) allowResponse(ticketCode string) *VerifyResponse {
	var ticket model.Ticket
	productName := ""
	if s.DB.Preload("OrderItem").Where("ticket_code = ?", ticketCode).First(&ticket).Error == nil {
		productName = ticket.OrderItem.ProductName
	}
	voiceCode := strings.TrimSpace(ticket.GateVoiceCode)
	if voiceCode == "" {
		voiceCode = "welcome"
	}
	return &VerifyResponse{Code: 200, Result: "allow", DisplayText: fmt.Sprintf("欢迎光临\n%s", productName), VoiceCode: voiceCode, OpenDuration: 5000}
}

func denyResponse(err error) *VerifyResponse {
	resp := &VerifyResponse{Code: 403, Result: "deny", VoiceFile: "invalid.mp3", VoiceCode: "invalid"}
	if err == nil {
		return resp
	}
	message := err.Error()
	switch {
	case errors.Is(err, ErrXiaohongshuRefundHold):
		resp.DisplayText, resp.VoiceCode = "渠道售后待核对", "manual_review"
	case errors.Is(err, ErrInvalidTicket) || strings.Contains(message, ErrInvalidTicket.Error()):
		resp.DisplayText = "无效票"
	case errors.Is(err, ErrTicketExpired) || strings.Contains(message, ErrTicketExpired.Error()):
		resp.DisplayText, resp.VoiceFile, resp.VoiceCode = "已过期", "expired.mp3", "expired"
	case errors.Is(err, ErrTicketNotStarted) || strings.Contains(message, ErrTicketNotStarted.Error()):
		resp.DisplayText, resp.VoiceFile, resp.VoiceCode = "未生效", "not_started.mp3", "not_started"
	case errors.Is(err, ErrOrderNotPaid) || strings.Contains(message, ErrOrderNotPaid.Error()):
		resp.DisplayText = "订单未支付"
	case errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrCheckpointNotFound) || strings.Contains(message, ErrAccessDenied.Error()) || strings.Contains(message, ErrCheckpointNotFound.Error()):
		resp.DisplayText = "区域无权"
	case errors.Is(err, ErrPointLimitReached) || errors.Is(err, ErrTicketUnavailable) || strings.Contains(message, ErrPointLimitReached.Error()) || strings.Contains(message, ErrTicketUnavailable.Error()):
		resp.DisplayText, resp.VoiceFile, resp.VoiceCode = "次数已满", "already_used.mp3", "already_used"
	case errors.Is(err, ErrGroupLimitReached) || strings.Contains(message, ErrGroupLimitReached.Error()):
		resp.DisplayText = "权益已尽"
	default:
		resp.DisplayText = "验证失败\n" + message
	}
	return resp
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
	if device == nil {
		return errors.New("device is required")
	}
	if !validDeviceType(device.Type) {
		return errors.New("invalid device type")
	}
	device.AuthKey = utils.GenerateRandomString(40)
	ciphertext, err := utils.EncryptAES(device.AuthKey)
	if err != nil {
		return fmt.Errorf("encrypt device key: %w", err)
	}
	device.AuthKeyCiphertext = ciphertext
	device.AuthKeyHash = ""
	device.Status = "offline"
	device.LastHeartbeat = nil
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
	if device == nil {
		return errors.New("device is required")
	}
	var disconnect bool
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var current model.Device
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&current).Error; err != nil {
			return err
		}
		requestedType := strings.TrimSpace(device.Type)
		if requestedType != "" && requestedType != current.Type {
			return errors.New("设备类型不可直接修改，请新建设备")
		}
		if device.CheckPointID != nil && current.CheckPointID != nil && *device.CheckPointID != *current.CheckPointID && current.Status != "offline" {
			return errors.New("设备在线时不能更换检票点，请先停止设备")
		}
		if device.CheckPointID == nil && current.CheckPointID != nil && current.Status != "offline" {
			return errors.New("设备在线时不能解除检票点，请先停止设备")
		}
		if err := s.validateCheckPoint(tx, tenantID, device.CheckPointID); err != nil {
			return err
		}
		areaID, err := scenicAreaForCheckpoint(tx, tenantID, device.CheckPointID, device.ScenicAreaID)
		if err != nil {
			return err
		}
		if current.ScenicAreaID != areaID && current.Status != "offline" {
			return errors.New("设备在线时不能更换所属景区，请先停止设备")
		}
		identityChanged := current.ScenicAreaID != areaID || !sameCheckpoint(current.CheckPointID, device.CheckPointID)
		if identityChanged {
			if err := revokeDeviceMaintenanceFactsTx(tx, tenantID, id, "设备履约归属已变更"); err != nil {
				return err
			}
		}
		updates := map[string]interface{}{
			"name": strings.TrimSpace(device.Name), "check_point_id": device.CheckPointID,
			"ip_address": strings.TrimSpace(device.IPAddress), "mac_address": strings.TrimSpace(device.MACAddress),
			"scenic_area_id": areaID,
		}
		result := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(updates)
		if result.Error == nil {
			result = tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).Update("scenic_area_id", areaID)
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		disconnect = identityChanged
		return nil
	})
	if err == nil && disconnect && s != nil && s.Maintenance != nil {
		s.Maintenance.DisconnectDevice(id, "设备检票点已变更")
	}
	return err
}

func sameCheckpoint(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
	ciphertext, encryptErr := utils.EncryptAES(key)
	if encryptErr != nil {
		return "", fmt.Errorf("encrypt device key: %w", encryptErr)
	}
	err := model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.Device{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(map[string]interface{}{
			"auth_key_ciphertext": ciphertext, "auth_key_hash": "", "status": "offline", "last_heartbeat": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := revokeDeviceMaintenanceFactsTx(tx, tenantID, id, "设备接入密钥已轮换"); err != nil {
			return err
		}
		return nil
	})
	if err == nil && s != nil && s.Maintenance != nil {
		s.Maintenance.DisconnectDevice(id, "设备接入密钥已轮换")
	}
	return key, err
}

func validDeviceKey(device *model.Device, key string) bool {
	if device.AuthKeyCiphertext == "" || key == "" {
		return false
	}
	stored, err := utils.DecryptAES(device.AuthKeyCiphertext)
	if err != nil || len(stored) != len(key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(key)) == 1
}

func (s *DeviceService) Delete(id, tenantID uint) error {
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&device).Error; err != nil {
			return err
		}
		if err := revokeDeviceMaintenanceFactsTx(tx, tenantID, id, "设备已删除"); err != nil {
			return err
		}
		if err := tx.Model(&device).Updates(map[string]interface{}{"status": "disabled", "auth_key_ciphertext": "", "auth_key_hash": "", "last_heartbeat": nil}).Error; err != nil {
			return err
		}
		result := tx.Delete(&device)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err == nil && s != nil && s.Maintenance != nil {
		s.Maintenance.DisconnectDevice(id, "设备已删除")
	}
	return err
}

// revokeDeviceMaintenanceFactsTx closes durable maintenance state before a
// device lifecycle change commits. The in-memory gateway is disconnected by
// the caller after commit, while these rows make a restart fail closed too.
func revokeDeviceMaintenanceFactsTx(tx *gorm.DB, tenantID, deviceID uint, reason string) error {
	if tx == nil || tenantID == 0 || deviceID == 0 {
		return errors.New("device maintenance identity is required")
	}
	now := time.Now()
	if err := tx.Model(&model.DeviceMaintenanceCredential{}).
		Where("tenant_id = ? AND device_id = ? AND status = ?", tenantID, deviceID, "active").
		Updates(map[string]interface{}{"status": "revoked", "revoked_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.DeviceMaintenanceSession{}).
		Where("tenant_id = ? AND device_id = ? AND status IN ?", tenantID, deviceID, []string{"pending", "active"}).
		Updates(map[string]interface{}{"status": "interrupted", "closed_at": now, "closed_reason": strings.TrimSpace(reason)}).Error; err != nil {
		return err
	}
	return tx.Model(&model.DeviceProvisioningLease{}).
		Where("tenant_id = ? AND device_id = ? AND status IN ?", tenantID, deviceID, []string{"pending", "claimed"}).
		Updates(map[string]interface{}{"status": "revoked", "revoked_at": now, "encrypted_bundle": "", "installer_public_key": ""}).Error
}

func (s *DeviceService) GetByID(id, tenantID uint) (*model.Device, error) {
	var device model.Device
	err := s.DB.Preload("CheckPoint").Where("id = ? AND tenant_id = ?", id, tenantID).First(&device).Error
	return &device, err
}

func (s *DeviceService) List(page, pageSize int, tenantID uint, deviceTypes ...string) ([]model.Device, int64, error) {
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
	if len(deviceTypes) > 0 && strings.TrimSpace(deviceTypes[0]) != "" {
		query = query.Where("type = ?", strings.TrimSpace(deviceTypes[0]))
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Preload("CheckPoint").Offset(offset).Limit(pageSize).Find(&devices).Error
	return devices, total, err
}
