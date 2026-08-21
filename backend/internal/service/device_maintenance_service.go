package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/gatetunnel"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMaintenanceReasonRequired = errors.New("maintenance reason is required")
	ErrMaintenanceCredential     = errors.New("maintenance credential is invalid")
	ErrMaintenanceNotConfigured  = errors.New("device maintenance gateway is not configured")
)

// DeviceMaintenanceService owns the control-plane facts for the optional
// maintenance tunnel. It deliberately does not share DeviceKey or any
// ticketing command path.
type DeviceMaintenanceService struct {
	DB      *gorm.DB
	Gateway *gatetunnel.Gateway
	Config  config.MaintenanceConfig
}

type MaintenanceCredentialResult struct {
	Credential *model.DeviceMaintenanceCredential `json:"credential"`
	Secret     string                             `json:"secret"`
}

type MaintenanceSessionResult struct {
	Session      *model.DeviceMaintenanceSession `json:"session"`
	SessionToken string                          `json:"session_token"`
	SessionID    string                          `json:"session_id"`
}

type MaintenanceSessionRequest struct {
	TenantID    uint
	DeviceID    uint
	ActorUserID uint
	Reason      string
	TTL         time.Duration
}

func NewDeviceMaintenanceService(db *gorm.DB, cfg config.MaintenanceConfig) (*DeviceMaintenanceService, error) {
	service := &DeviceMaintenanceService{DB: db, Config: cfg}
	gateway, err := gatetunnel.New(gatetunnel.Config{
		Enabled:       cfg.Enabled,
		MaxFrameBytes: 4 << 20,
		Authenticator: service.authenticateCredential,
		Lifecycle: gatetunnel.LifecycleCallbacks{
			OnSessionActive: service.persistGatewaySessionActive,
			OnSessionClosed: service.persistGatewaySessionClosed,
		},
	})
	if err != nil {
		return nil, err
	}
	service.Gateway = gateway
	return service, nil
}

// persistGatewaySessionActive closes the gap between the in-memory data plane
// and the durable control-plane row. A session is only reported active after
// the gate-client has successfully opened its fixed localhost SSH connection.
func (s *DeviceMaintenanceService) persistGatewaySessionActive(info gatetunnel.SessionInfo) {
	if s == nil || s.DB == nil || strings.TrimSpace(info.ID) == "" {
		return
	}
	now := time.Now()
	_ = model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.DeviceMaintenanceSession{}).
			Where("gateway_session_id = ? AND tenant_id = ? AND device_id = ? AND status = ?", info.ID, info.TenantID, info.DeviceID, "pending").
			Updates(map[string]interface{}{"status": "active", "opened_at": now}).Error
	})
}

// persistGatewaySessionClosed records disconnects, expiry and interrupted
// leases immediately. This prevents a stale pending/active row from blocking
// the next maintenance session until the periodic worker runs.
func (s *DeviceMaintenanceService) persistGatewaySessionClosed(info gatetunnel.SessionInfo, reason string) {
	if s == nil || s.DB == nil || strings.TrimSpace(info.ID) == "" {
		return
	}
	status := strings.TrimSpace(info.Status)
	if status != "closed" && status != "expired" && status != "interrupted" {
		status = "interrupted"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "维护数据通道已关闭"
	}
	_ = model.Write(func(tx *gorm.DB) error {
		var session model.DeviceMaintenanceSession
		if err := tx.Where("gateway_session_id = ? AND tenant_id = ? AND device_id = ?", info.ID, info.TenantID, info.DeviceID).First(&session).Error; err != nil {
			return err
		}
		result := tx.Model(&model.DeviceMaintenanceSession{}).
			Where("id = ? AND status IN ?", session.ID, []string{"pending", "active"}).
			Updates(map[string]interface{}{"status": status, "closed_at": time.Now(), "closed_reason": reason})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		actorUserID := info.ActorUserID
		if actorUserID == 0 {
			actorUserID = session.ActorUserID
		}
		return recordAuditTx(tx, actorUserID, session.TenantID, auditRoleTx(tx, actorUserID), "tenant", "device.maintenance.session.close", "device_maintenance_session", session.ID, reason, "", fmt.Sprintf(`{"status":%q}`, status))
	})
}

func (s *DeviceMaintenanceService) enabled() bool {
	return s != nil && s.DB != nil && s.Gateway != nil && s.Gateway.Enabled()
}

func (s *DeviceMaintenanceService) authenticateCredential(secret string) (gatetunnel.DeviceIdentity, error) {
	if s == nil || s.DB == nil || !s.enabled() {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceNotConfigured
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceCredential
	}
	hash := hashMaintenanceSecret(secret)
	var credential model.DeviceMaintenanceCredential
	if err := s.DB.Where("secret_hash = ? AND status = ?", hash, "active").First(&credential).Error; err != nil {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceCredential
	}
	now := time.Now()
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(now) {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceCredential
	}
	var device model.Device
	if err := s.DB.Select("id", "tenant_id", "scenic_area_id", "serial_number", "type", "status").
		Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND type = ? AND status != ?", credential.DeviceID, credential.TenantID, credential.ScenicAreaID, "gate", "disabled").
		First(&device).Error; err != nil {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceCredential
	}
	// Authentication remains fail-closed if the usage timestamp cannot be
	// recorded. This is an operational audit fact, not a best-effort metric.
	result := s.DB.Model(&model.DeviceMaintenanceCredential{}).
		Where("id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", credential.ID, "active", now).
		Update("last_used_at", now)
	if result.Error != nil {
		return gatetunnel.DeviceIdentity{}, fmt.Errorf("record maintenance credential use: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return gatetunnel.DeviceIdentity{}, ErrMaintenanceCredential
	}
	return gatetunnel.DeviceIdentity{DeviceID: device.ID, TenantID: device.TenantID, ScenicAreaID: device.ScenicAreaID, SerialNumber: device.SerialNumber}, nil
}

func (s *DeviceMaintenanceService) RotateCredential(tenantID, deviceID, actorUserID uint, reason string) (*MaintenanceCredentialResult, error) {
	if !s.enabled() {
		return nil, ErrMaintenanceNotConfigured
	}
	if strings.TrimSpace(reason) == "" {
		return nil, ErrMaintenanceReasonRequired
	}
	if tenantID == 0 || deviceID == 0 || actorUserID == 0 {
		return nil, errors.New("tenant, device and operator are required")
	}
	secret := utils.GenerateRandomString(64)
	hash := hashMaintenanceSecret(secret)
	credential := &model.DeviceMaintenanceCredential{}
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND type = ? AND status != ?", deviceID, tenantID, "gate", "disabled").First(&device).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if device.ScenicAreaID == 0 {
			return errors.New("gate device has no scenic area")
		}
		now := time.Now()
		if err := tx.Model(&model.DeviceMaintenanceCredential{}).
			Where("tenant_id = ? AND device_id = ? AND status = ?", tenantID, deviceID, "active").
			Updates(map[string]interface{}{"status": "revoked", "revoked_at": now}).Error; err != nil {
			return err
		}
		*credential = model.DeviceMaintenanceCredential{
			TenantID: tenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: deviceID,
			SecretHash: hash, Status: "active",
		}
		if err := tx.Create(credential).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "device.maintenance.credential.rotate", "device", deviceID, strings.TrimSpace(reason), "", fmt.Sprintf(`{"credential_id":%d}`, credential.ID))
	})
	if err != nil {
		return nil, err
	}
	// Existing connections were authenticated by the previous secret. Force a
	// reconnect so a rotation takes effect immediately.
	s.Gateway.DisconnectDevice(deviceID, "维护凭据已轮换")
	credential.SecretHash = ""
	return &MaintenanceCredentialResult{Credential: credential, Secret: secret}, nil
}

func (s *DeviceMaintenanceService) RevokeCredential(tenantID, deviceID, actorUserID uint, reason string) error {
	if !s.enabled() {
		return ErrMaintenanceNotConfigured
	}
	if strings.TrimSpace(reason) == "" {
		return ErrMaintenanceReasonRequired
	}
	err := model.Write(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.DeviceMaintenanceCredential{}).
			Where("tenant_id = ? AND device_id = ? AND status = ?", tenantID, deviceID, "active").
			Updates(map[string]interface{}{"status": "revoked", "revoked_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "device.maintenance.credential.revoke", "device", deviceID, strings.TrimSpace(reason), "", "")
	})
	if err == nil {
		s.Gateway.DisconnectDevice(deviceID, "维护凭据已吊销")
	}
	return err
}

func (s *DeviceMaintenanceService) CreateSession(req MaintenanceSessionRequest) (*MaintenanceSessionResult, error) {
	if !s.enabled() {
		return nil, ErrMaintenanceNotConfigured
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		return nil, ErrMaintenanceReasonRequired
	}
	if req.TenantID == 0 || req.DeviceID == 0 || req.ActorUserID == 0 {
		return nil, errors.New("tenant, device and operator are required")
	}
	defaultTTL := time.Duration(s.Config.SessionTTLSeconds) * time.Second
	maxTTL := time.Duration(s.Config.MaxSessionTTL) * time.Second
	if defaultTTL <= 0 {
		defaultTTL = 15 * time.Minute
	}
	if maxTTL <= 0 {
		maxTTL = 30 * time.Minute
	}
	if req.TTL <= 0 {
		req.TTL = defaultTTL
	}
	if req.TTL > maxTTL {
		req.TTL = maxTTL
	}
	if req.TTL > 30*time.Minute {
		req.TTL = 30 * time.Minute
	}
	var credential model.DeviceMaintenanceCredential
	if err := s.DB.Where("tenant_id = ? AND device_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", req.TenantID, req.DeviceID, "active", time.Now()).First(&credential).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if !s.Gateway.DeviceConnected(req.DeviceID) {
		return nil, gatetunnel.ErrDeviceOffline
	}

	token := utils.GenerateRandomString(64)
	tokenHashBytes := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(tokenHashBytes[:])
	sessionID := fmt.Sprintf("MNT-%d-%s", time.Now().UnixNano(), utils.GenerateRandomString(8))
	expiresAt := time.Now().Add(req.TTL)
	session := &model.DeviceMaintenanceSession{}
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Select("id", "tenant_id", "scenic_area_id", "type", "status").
			Where("id = ? AND tenant_id = ? AND type = ? AND status != ?", req.DeviceID, req.TenantID, "gate", "disabled").First(&device).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if device.ScenicAreaID == 0 {
			return errors.New("gate device has no scenic area")
		}
		var credential model.DeviceMaintenanceCredential
		if err := tx.Where("tenant_id = ? AND device_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", req.TenantID, req.DeviceID, "active", time.Now()).First(&credential).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		*session = model.DeviceMaintenanceSession{
			TenantID: req.TenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: req.DeviceID,
			ActorUserID: req.ActorUserID, Reason: req.Reason, Mode: "ssh", Status: "pending",
			TokenHash: tokenHash, GatewaySessionID: sessionID, ExpiresAt: expiresAt,
		}
		if err := tx.Create(session).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, req.ActorUserID, req.TenantID, auditRoleTx(tx, req.ActorUserID), "tenant", "device.maintenance.session.create", "device_maintenance_session", session.ID, req.Reason, "", fmt.Sprintf(`{"session_id":%q,"device_id":%d}`, sessionID, req.DeviceID))
	})
	if err != nil {
		return nil, err
	}

	if _, err := s.Gateway.CreateSession(req.DeviceID, req.TenantID, req.TTL, tokenHashBytes, sessionID, req.ActorUserID); err != nil {
		_ = s.markSessionClosed(session.ID, req.TenantID, req.ActorUserID, "网关无法建立维护租约: "+err.Error(), "interrupted")
		return nil, err
	}
	// Credential rotation disconnects the old device connection after its
	// transaction commits. Re-check here so a race cannot turn that stale
	// connection into a usable maintenance lease.
	if err := s.DB.Where("tenant_id = ? AND device_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", req.TenantID, req.DeviceID, "active", time.Now()).First(&credential).Error; err != nil {
		_ = s.Gateway.CloseSessionWithStatus(sessionID, "维护凭据已轮换或吊销", "interrupted")
		_ = s.markSessionClosed(session.ID, req.TenantID, req.ActorUserID, "维护凭据已轮换或吊销", "interrupted")
		return nil, ErrMaintenanceCredential
	}
	// The in-memory gateway uses the public session ID as its lookup key. Keep
	// the DB primary key and public opaque ID separate to avoid exposing IDs in
	// the token or allowing cross-tenant guessing.
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(session).Where("id = ? AND tenant_id = ?", session.ID, req.TenantID).Update("connection_version", time.Now().UnixNano()).Error
	}); err != nil {
		_ = s.Gateway.CloseSessionWithStatus(sessionID, "维护会话状态写入失败", "interrupted")
		_ = s.markSessionClosed(session.ID, req.TenantID, req.ActorUserID, "维护会话状态写入失败", "interrupted")
		return nil, err
	}
	session.TokenHash = ""
	return &MaintenanceSessionResult{Session: session, SessionToken: token, SessionID: sessionID}, nil
}

func (s *DeviceMaintenanceService) markSessionClosed(id, tenantID, actorUserID uint, reason, status string) error {
	if status == "" {
		status = "closed"
	}
	return model.Write(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.DeviceMaintenanceSession{}).
			Where("id = ? AND tenant_id = ? AND status IN ?", id, tenantID, []string{"pending", "active"}).
			Updates(map[string]interface{}{"status": status, "closed_at": now, "closed_reason": reason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var session model.DeviceMaintenanceSession
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&session).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "device.maintenance.session.close", "device_maintenance_session", id, reason, "", fmt.Sprintf(`{"status":%q}`, status))
	})
}

func (s *DeviceMaintenanceService) CloseSession(tenantID, actorUserID, deviceID, sessionDBID uint, reason string) error {
	if !s.enabled() {
		return ErrMaintenanceNotConfigured
	}
	if strings.TrimSpace(reason) == "" {
		return ErrMaintenanceReasonRequired
	}
	var session model.DeviceMaintenanceSession
	if deviceID == 0 {
		return gorm.ErrRecordNotFound
	}
	if err := s.DB.Where("id = ? AND tenant_id = ? AND device_id = ?", sessionDBID, tenantID, deviceID).First(&session).Error; err != nil {
		return err
	}
	if err := s.Gateway.CloseSessionWithStatusForActor(session.GatewaySessionID, reason, "closed", actorUserID); err != nil && !errors.Is(err, gatetunnel.ErrSessionNotFound) {
		return err
	}
	err := model.Write(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&model.DeviceMaintenanceSession{}).
			Where("id = ? AND tenant_id = ? AND device_id = ? AND status IN ?", session.ID, tenantID, deviceID, []string{"pending", "active"}).
			Updates(map[string]interface{}{"status": "closed", "closed_at": now, "closed_reason": strings.TrimSpace(reason)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "device.maintenance.session.close", "device_maintenance_session", session.ID, strings.TrimSpace(reason), "", "")
	})
	return err
}

func (s *DeviceMaintenanceService) GetSession(tenantID, id uint) (*model.DeviceMaintenanceSession, error) {
	if tenantID == 0 || id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var session model.DeviceMaintenanceSession
	err := s.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *DeviceMaintenanceService) CredentialStatus(tenantID, deviceID uint) (*model.DeviceMaintenanceCredential, error) {
	if tenantID == 0 || deviceID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var credential model.DeviceMaintenanceCredential
	err := s.DB.Select("id", "tenant_id", "scenic_area_id", "device_id", "status", "expires_at", "revoked_at", "last_used_at").
		Where("tenant_id = ? AND device_id = ? AND status = ?", tenantID, deviceID, "active").First(&credential).Error
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

func (s *DeviceMaintenanceService) ListSessions(tenantID, deviceID uint, page, pageSize int) ([]model.DeviceMaintenanceSession, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := s.DB.Model(&model.DeviceMaintenanceSession{}).Where("tenant_id = ?", tenantID)
	if deviceID > 0 {
		query = query.Where("device_id = ?", deviceID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.DeviceMaintenanceSession
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ReconcileStartup makes a process restart visible instead of leaving an
// apparently active lease in the workbench.
func (s *DeviceMaintenanceService) ReconcileStartup(now time.Time) error {
	if s == nil || s.DB == nil {
		return nil
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.DeviceMaintenanceSession{}).
			Where("status IN ?", []string{"pending", "active"}).
			Updates(map[string]interface{}{"status": "interrupted", "closed_at": now, "closed_reason": "服务进程重启，维护会话已中断"}).Error
	})
}

// ExpireSessions is called by the worker and is idempotent. The gateway is
// closed first so no byte stream remains usable after the database state is
// changed.
func (s *DeviceMaintenanceService) ExpireSessions(now time.Time, limit int) (int, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var sessions []model.DeviceMaintenanceSession
	if err := s.DB.Where("status IN ? AND expires_at <= ?", []string{"pending", "active"}, now).Order("id").Limit(limit).Find(&sessions).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, session := range sessions {
		if s.Gateway != nil {
			_ = s.Gateway.CloseSessionWithStatus(session.GatewaySessionID, "维护会话已过期", "expired")
		}
		if err := model.Write(func(tx *gorm.DB) error {
			result := tx.Model(&model.DeviceMaintenanceSession{}).
				Where("id = ? AND status IN ? AND expires_at <= ?", session.ID, []string{"pending", "active"}, now).
				Updates(map[string]interface{}{"status": "expired", "closed_at": now, "closed_reason": "维护会话已过期"})
			return result.Error
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func hashMaintenanceSecret(secret string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return hex.EncodeToString(digest[:])
}
