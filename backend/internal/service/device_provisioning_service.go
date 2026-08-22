package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/deviceprovision"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProvisioningLeaseInvalid     = errors.New("设备安装绑定码无效或已失效")
	ErrProvisioningLeaseExists      = errors.New("该闸机已有未完成的安装绑定")
	ErrProvisioningDeviceOnline     = errors.New("闸机当前在线，请先停止旧客户端后再生成安装绑定")
	ErrProvisioningDeviceNotOffline = errors.New("闸机未处于明确离线状态，请先停止并确认旧客户端")
	ErrProvisioningNotReady         = errors.New("设备安装绑定服务尚未配置")
	ErrProvisioningReasonRequired   = errors.New("安装绑定操作原因不能为空")
)

type DeviceProvisioningService struct {
	DB          *gorm.DB
	Maintenance *DeviceMaintenanceService
}

type ProvisioningLeaseRequest struct {
	TenantID    uint
	DeviceID    uint
	ActorUserID uint
	Reason      string
	TTL         time.Duration
}

type ProvisioningLeaseResult struct {
	Lease          *model.DeviceProvisioningLease `json:"lease"`
	ActivationCode string                         `json:"activation_code"`
}

type ProvisioningClaimRequest struct {
	Token          string `json:"token" binding:"required"`
	InstallationID string `json:"installation_id" binding:"required"`
	PublicKey      string `json:"public_key" binding:"required"`
}

type ProvisioningClaimResult struct {
	LeaseID  uint   `json:"lease_id"`
	Status   string `json:"status"`
	Envelope string `json:"envelope"`
}

type ProvisioningConfirmRequest struct {
	InstallationID string `json:"installation_id" binding:"required"`
	Fingerprint    string `json:"fingerprint" binding:"required"`
}

func NewDeviceProvisioningService(db *gorm.DB, maintenance *DeviceMaintenanceService) *DeviceProvisioningService {
	return &DeviceProvisioningService{DB: db, Maintenance: maintenance}
}

func (s *DeviceProvisioningService) CreateLease(req ProvisioningLeaseRequest) (*ProvisioningLeaseResult, error) {
	if s == nil || s.DB == nil {
		return nil, ErrProvisioningNotReady
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.TenantID == 0 || req.DeviceID == 0 || req.ActorUserID == 0 || req.Reason == "" {
		return nil, errors.New("tenant, device, operator and reason are required")
	}
	if req.TTL <= 0 {
		req.TTL = 10 * time.Minute
	}
	if req.TTL > 15*time.Minute {
		req.TTL = 15 * time.Minute
	}
	if _, _, err := provisioningURLs(); err != nil {
		return nil, err
	}
	activationCode := utils.GenerateRandomString(64)
	lease := &model.DeviceProvisioningLease{}
	disconnectStaleClaim := false
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, req.TenantID, "supplier"); err != nil {
			return err
		}
		if err := requireActiveSupplierBusinessType(tx, req.TenantID, "scenic"); err != nil {
			return err
		}
		var device model.Device
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND type = ? AND status != ?", req.DeviceID, req.TenantID, "gate", "disabled").First(&device).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if device.ScenicAreaID == 0 {
			return errors.New("gate device has no scenic area")
		}
		if device.Status == "online" {
			return ErrProvisioningDeviceOnline
		}
		if device.Status != "offline" {
			return ErrProvisioningDeviceNotOffline
		}
		now := time.Now()
		// Do not let a stale pending/claimed row block a new installation when
		// the periodic cleanup worker has not run yet. A claimed lease has already
		// received long-lived credentials, so it must invalidate them before it is
		// marked expired; merely clearing its envelope would leave the abandoned
		// installer able to authenticate as this device.
		var staleLeases []model.DeviceProvisioningLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("device_id = ? AND status IN ? AND expires_at <= ? AND deleted_at IS NULL", req.DeviceID, []string{"pending", "claimed"}, now).
			Find(&staleLeases).Error; err != nil {
			return err
		}
		for index := range staleLeases {
			stale := &staleLeases[index]
			if stale.Status == "claimed" {
				if err := invalidateClaimedLeaseCredentialsTx(tx, stale, now); err != nil {
					return err
				}
				disconnectStaleClaim = true
			}
			if err := tx.Model(stale).Updates(map[string]interface{}{
				"status": "expired", "encrypted_bundle": "", "installer_public_key": "",
			}).Error; err != nil {
				return err
			}
		}
		var existing int64
		if err := tx.Model(&model.DeviceProvisioningLease{}).
			Where("device_id = ? AND status IN ? AND expires_at > ? AND deleted_at IS NULL", req.DeviceID, []string{"pending", "claimed"}, now).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrProvisioningLeaseExists
		}
		*lease = model.DeviceProvisioningLease{
			TenantID: req.TenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: device.ID,
			ActorUserID: req.ActorUserID, Reason: req.Reason,
			TokenHash: hashProvisioningToken(activationCode), Status: "pending",
			ExpiresAt: now.Add(req.TTL),
		}
		if err := tx.Create(lease).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, req.ActorUserID, req.TenantID, auditRoleTx(tx, req.ActorUserID), "tenant", "device.provisioning.lease.create", "device_provisioning_lease", lease.ID, req.Reason, "", fmt.Sprintf(`{"device_id":%d,"expires_at":%q}`, device.ID, lease.ExpiresAt.UTC().Format(time.RFC3339)))
	})
	if err != nil {
		if isProvisioningLeaseUniqueViolation(err) {
			return nil, ErrProvisioningLeaseExists
		}
		return nil, err
	}
	if disconnectStaleClaim {
		s.disconnectMaintenanceDevice(req.DeviceID, "过期安装绑定已吊销设备凭据")
	}
	return &ProvisioningLeaseResult{Lease: lease, ActivationCode: activationCode}, nil
}

func isProvisioningLeaseUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "idx_device_provisioning_active_device"
}

func (s *DeviceProvisioningService) Claim(req ProvisioningClaimRequest) (*ProvisioningClaimResult, error) {
	if s == nil || s.DB == nil {
		return nil, ErrProvisioningNotReady
	}
	req.Token = strings.TrimSpace(req.Token)
	req.InstallationID = strings.TrimSpace(req.InstallationID)
	req.PublicKey = strings.TrimSpace(req.PublicKey)
	publicKey, err := deviceprovision.DecodePublicKey(req.PublicKey)
	if err != nil || req.Token == "" || req.InstallationID == "" || len(req.InstallationID) > 128 {
		return nil, ErrProvisioningLeaseInvalid
	}
	fingerprint := deviceprovision.Fingerprint(publicKey)
	canonicalPublicKey := deviceprovision.EncodePublicKey(publicKey)
	lease := &model.DeviceProvisioningLease{}
	result := &ProvisioningClaimResult{}
	expiredLease := false
	claimedExpired := false
	expiredDeviceID := uint(0)
	err = model.Write(func(tx *gorm.DB) error {
		var row model.DeviceProvisioningLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ?", hashProvisioningToken(req.Token)).First(&row).Error; err != nil {
			return ErrProvisioningLeaseInvalid
		}
		now := time.Now()
		if !row.ExpiresAt.After(now) || row.Status == "expired" {
			expiredLease = true
			if row.Status == "pending" || row.Status == "claimed" {
				expiredDeviceID = row.DeviceID
				if row.Status == "claimed" {
					if err := invalidateClaimedLeaseCredentialsTx(tx, &row, now); err != nil {
						return err
					}
					claimedExpired = true
				}
				if err := tx.Model(&row).Updates(map[string]interface{}{"status": "expired", "encrypted_bundle": "", "installer_public_key": ""}).Error; err != nil {
					return err
				}
			}
			return nil
		}
		if err := requireActiveTenantCapability(tx, row.TenantID, "supplier"); err != nil {
			return ErrProvisioningLeaseInvalid
		}
		if err := requireActiveSupplierBusinessType(tx, row.TenantID, "scenic"); err != nil {
			return ErrProvisioningLeaseInvalid
		}
		var device model.Device
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND type = ? AND status != ?", row.DeviceID, row.TenantID, row.ScenicAreaID, "gate", "disabled").First(&device).Error; err != nil {
			return ErrProvisioningLeaseInvalid
		}
		if row.Status == "claimed" {
			if row.InstallationID != req.InstallationID || row.InstallerFingerprint != fingerprint || row.InstallerPublicKey != canonicalPublicKey || row.EncryptedBundle == "" {
				return ErrProvisioningLeaseInvalid
			}
			*lease = row
			result.LeaseID, result.Status, result.Envelope = row.ID, row.Status, row.EncryptedBundle
			return nil
		}
		if row.Status != "pending" {
			return ErrProvisioningLeaseInvalid
		}
		if device.Status == "online" {
			return ErrProvisioningDeviceOnline
		}
		if device.Status != "offline" {
			return ErrProvisioningDeviceNotOffline
		}
		baseURL, maintenanceURL, err := provisioningURLs()
		if err != nil {
			return err
		}
		deviceKey := utils.GenerateRandomString(40)
		maintenanceSecret := ""
		deviceCiphertext, err := utils.EncryptAES(deviceKey)
		if err != nil {
			return err
		}
		if config.GlobalConfig.Maintenance.Enabled {
			maintenanceSecret = utils.GenerateRandomString(64)
			if err := tx.Model(&model.DeviceMaintenanceCredential{}).
				Where("tenant_id = ? AND device_id = ? AND status = ?", row.TenantID, row.DeviceID, "active").
				Updates(map[string]interface{}{"status": "revoked", "revoked_at": time.Now()}).Error; err != nil {
				return err
			}
			credential := &model.DeviceMaintenanceCredential{TenantID: row.TenantID, ScenicAreaID: row.ScenicAreaID, DeviceID: row.DeviceID, SecretHash: hashMaintenanceSecret(maintenanceSecret), Status: "active"}
			if err := tx.Create(credential).Error; err != nil {
				return err
			}
		}
		var tenant model.Tenant
		if err := tx.Select("system_code").First(&tenant, row.TenantID).Error; err != nil {
			return err
		}
		var bundleEnvelope string
		bundleEnvelope, err = deviceprovision.EncryptBundle(deviceprovision.Bundle{
			ServerURL: baseURL, SystemCode: tenant.SystemCode, SerialNumber: device.SerialNumber,
			DeviceKey: deviceKey, MaintenanceSecret: maintenanceSecret, MaintenanceURL: maintenanceURL,
		}, publicKey)
		if err != nil {
			return err
		}
		claimedAt := time.Now()
		updates := map[string]interface{}{
			"auth_key_ciphertext": deviceCiphertext, "auth_key_hash": "",
		}
		if err := tx.Model(&device).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{
			"status": "claimed", "installation_id": req.InstallationID, "installer_public_key": canonicalPublicKey,
			"installer_fingerprint": fingerprint, "encrypted_bundle": bundleEnvelope, "claimed_at": claimedAt,
		}).Error; err != nil {
			return err
		}
		if err := recordAuditTx(tx, row.ActorUserID, row.TenantID, auditRoleTx(tx, row.ActorUserID), "tenant", "device.provisioning.lease.claim", "device_provisioning_lease", row.ID, row.Reason, "", fmt.Sprintf(`{"device_id":%d,"fingerprint":%q}`, row.DeviceID, fingerprint)); err != nil {
			return err
		}
		*lease = row
		result.LeaseID, result.Status, result.Envelope = row.ID, "claimed", bundleEnvelope
		return nil
	})
	if err != nil {
		return nil, err
	}
	if expiredLease {
		if claimedExpired {
			s.disconnectMaintenanceDevice(expiredDeviceID, "安装绑定已过期，设备凭据已吊销")
		}
		return nil, ErrProvisioningLeaseInvalid
	}
	if s.Maintenance != nil && s.Maintenance.Gateway != nil {
		s.Maintenance.Gateway.DisconnectDevice(lease.DeviceID, "安装绑定已轮换设备凭据")
	}
	return result, nil
}

// provisioningURLs validates the deployment boundary before any new device
// credentials are generated. A missing or non-HTTPS public URL would leave an
// installer with unusable credentials and could accidentally send them over
// plaintext HTTP, so the claim fails closed.
func provisioningURLs() (string, string, error) {
	base := strings.TrimRight(strings.TrimSpace(config.GlobalConfig.Server.PublicBaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", "", ErrProvisioningNotReady
	}
	maintenanceURL := ""
	if config.GlobalConfig.Maintenance.Enabled {
		path := strings.TrimSpace(config.GlobalConfig.Maintenance.Path)
		if path == "" || !strings.HasPrefix(path, "/") {
			return "", "", ErrProvisioningNotReady
		}
		maintenanceURL = "wss://" + parsed.Host + strings.TrimRight(path, "/")
	}
	return base, maintenanceURL, nil
}

func (s *DeviceProvisioningService) Confirm(tenantID, deviceID uint, req ProvisioningConfirmRequest) error {
	if s == nil || s.DB == nil || tenantID == 0 || deviceID == 0 {
		return ErrProvisioningLeaseInvalid
	}
	req.InstallationID = strings.TrimSpace(req.InstallationID)
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	if req.InstallationID == "" || req.Fingerprint == "" {
		return ErrProvisioningLeaseInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		var lease model.DeviceProvisioningLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND device_id = ? AND installation_id = ?", tenantID, deviceID, req.InstallationID).First(&lease).Error; err != nil {
			return ErrProvisioningLeaseInvalid
		}
		if lease.InstallerFingerprint != req.Fingerprint {
			return ErrProvisioningLeaseInvalid
		}
		if lease.Status == "completed" {
			return nil
		}
		if lease.Status != "claimed" || !lease.ExpiresAt.After(time.Now()) {
			return ErrProvisioningLeaseInvalid
		}
		if err := tx.Model(&lease).Updates(map[string]interface{}{"status": "completed", "completed_at": time.Now(), "encrypted_bundle": "", "installer_public_key": ""}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, lease.ActorUserID, lease.TenantID, auditRoleTx(tx, lease.ActorUserID), "tenant", "device.provisioning.lease.complete", "device_provisioning_lease", lease.ID, lease.Reason, "", fmt.Sprintf(`{"device_id":%d,"fingerprint":%q}`, lease.DeviceID, lease.InstallerFingerprint))
	})
}

// RevokeLease invalidates a pending/claimed lease. A claimed lease has already
// issued credentials, so revoking it also disables those credentials and
// requires a fresh installation binding. It is useful when a binding code was
// copied to the wrong workstation or the field installation is cancelled.
func (s *DeviceProvisioningService) RevokeLease(tenantID, deviceID, leaseID, actorUserID uint, reason string) error {
	if s == nil || s.DB == nil || tenantID == 0 || deviceID == 0 || leaseID == 0 || actorUserID == 0 {
		return ErrProvisioningLeaseInvalid
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ErrProvisioningReasonRequired
	}
	claimed := false
	err := model.Write(func(tx *gorm.DB) error {
		var lease model.DeviceProvisioningLease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND device_id = ? AND status IN ?", leaseID, tenantID, deviceID, []string{"pending", "claimed"}).First(&lease).Error; err != nil {
			return ErrProvisioningLeaseInvalid
		}
		now := time.Now()
		if lease.Status == "claimed" {
			if err := invalidateClaimedLeaseCredentialsTx(tx, &lease, now); err != nil {
				return err
			}
			claimed = true
		}
		if err := tx.Model(&lease).Updates(map[string]interface{}{"status": "revoked", "revoked_at": now, "encrypted_bundle": "", "installer_public_key": ""}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "device.provisioning.lease.revoke", "device_provisioning_lease", lease.ID, reason, "", fmt.Sprintf(`{"device_id":%d}`, deviceID))
	})
	if err == nil && claimed {
		s.disconnectMaintenanceDevice(deviceID, "安装绑定已撤销，设备凭据已吊销")
	}
	return err
}

func (s *DeviceProvisioningService) ExpireLeases(now time.Time, limit int) (int, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []model.DeviceProvisioningLease
	if err := s.DB.Where("status IN ? AND expires_at <= ?", []string{"pending", "claimed"}, now).Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		claimed := false
		expired := false
		if err := model.Write(func(tx *gorm.DB) error {
			var lease model.DeviceProvisioningLease
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status IN ? AND expires_at <= ?", row.ID, []string{"pending", "claimed"}, now).First(&lease).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if lease.Status == "claimed" {
				if err := invalidateClaimedLeaseCredentialsTx(tx, &lease, now); err != nil {
					return err
				}
				claimed = true
			}
			if err := tx.Model(&lease).Updates(map[string]interface{}{"status": "expired", "encrypted_bundle": "", "installer_public_key": ""}).Error; err != nil {
				return err
			}
			expired = true
			return nil
		}); err != nil {
			return count, err
		}
		if claimed {
			s.disconnectMaintenanceDevice(row.DeviceID, "安装绑定已过期，设备凭据已吊销")
		}
		if expired {
			count++
		}
	}
	return count, nil
}

// invalidateClaimedLeaseCredentialsTx removes the credentials issued by a
// claimed-but-unconfirmed lease. A claim rotates the device HMAC before the
// installer can confirm; if that installer is abandoned, revoked, or expires,
// retaining the replacement key would leave an untrusted controller able to
// authenticate. Pending leases have never received these credentials and must
// not call this helper.
//
// Device credentials do not retain a per-lease version in schema 107. The
// safest fail-closed behaviour for an abandoned claimed lease is therefore to
// disable the device key and every active maintenance credential for that
// device. A new one-time lease will atomically issue replacement credentials.
func invalidateClaimedLeaseCredentialsTx(tx *gorm.DB, lease *model.DeviceProvisioningLease, now time.Time) error {
	if tx == nil || lease == nil || lease.Status != "claimed" || lease.TenantID == 0 || lease.DeviceID == 0 || lease.ScenicAreaID == 0 {
		return ErrProvisioningLeaseInvalid
	}
	result := tx.Model(&model.Device{}).
		Where("id = ? AND tenant_id = ? AND scenic_area_id = ?", lease.DeviceID, lease.TenantID, lease.ScenicAreaID).
		Updates(map[string]interface{}{"auth_key_ciphertext": "", "auth_key_hash": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrProvisioningLeaseInvalid
	}
	if err := tx.Model(&model.DeviceMaintenanceCredential{}).
		Where("tenant_id = ? AND device_id = ? AND scenic_area_id = ? AND status = ?", lease.TenantID, lease.DeviceID, lease.ScenicAreaID, "active").
		Updates(map[string]interface{}{"status": "revoked", "revoked_at": now}).Error; err != nil {
		return err
	}
	return tx.Model(&model.DeviceMaintenanceSession{}).
		Where("tenant_id = ? AND device_id = ? AND status IN ?", lease.TenantID, lease.DeviceID, []string{"pending", "active"}).
		Updates(map[string]interface{}{"status": "interrupted", "closed_at": now, "closed_reason": "安装绑定已失效"}).Error
}

func (s *DeviceProvisioningService) disconnectMaintenanceDevice(deviceID uint, reason string) {
	if s == nil || s.Maintenance == nil || s.Maintenance.Gateway == nil || deviceID == 0 {
		return
	}
	s.Maintenance.Gateway.DisconnectDevice(deviceID, reason)
}

func hashProvisioningToken(token string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(digest[:])
}
