package model

import "time"

// DeviceMaintenanceCredential is independent from the ticketing HMAC key.
// The raw secret is returned only when an administrator rotates it; the
// server stores a hash for authentication and never reuses it for hardware
// verification requests.
type DeviceMaintenanceCredential struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	SecretHash   string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Status       string     `gorm:"size:20;not null;default:'active';index" json:"status"` // active, revoked
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}

// DeviceMaintenanceSession is a short-lived, audited maintenance lease. It
// authorizes exactly one device and one fixed localhost SSH bridge; it is not
// a generic TCP proxy or a second ticketing workflow.
type DeviceMaintenanceSession struct {
	Base
	TenantID          uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID      uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID          uint       `gorm:"index;not null" json:"device_id"`
	ActorUserID       uint       `gorm:"index;not null" json:"actor_user_id"`
	Reason            string     `gorm:"size:255;not null" json:"reason"`
	Mode              string     `gorm:"size:20;not null;default:'ssh'" json:"mode"`             // ssh
	Status            string     `gorm:"size:20;not null;default:'pending';index" json:"status"` // pending, active, closed, expired, interrupted
	TokenHash         string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	GatewaySessionID  string     `gorm:"size:80;uniqueIndex;not null" json:"-"`
	ConnectionVersion int64      `gorm:"not null;default:0" json:"connection_version"`
	ExpiresAt         time.Time  `gorm:"index;not null" json:"expires_at"`
	OpenedAt          *time.Time `json:"opened_at,omitempty"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
	ClosedReason      string     `gorm:"size:255" json:"closed_reason,omitempty"`
}

// DeviceProvisioningLease is a short-lived, single-use installation lease.
// It binds an installer to a server-selected tenant/device identity without
// placing long-lived credentials in a package, URL, or command line. The
// encrypted bundle is retained only long enough for the same installer to
// retry a lost HTTP response and is cleared after confirmation.
type DeviceProvisioningLease struct {
	Base
	TenantID             uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID         uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID             uint       `gorm:"index;not null" json:"device_id"`
	ActorUserID          uint       `gorm:"index;not null" json:"actor_user_id"`
	Reason               string     `gorm:"size:255;not null" json:"reason"`
	TokenHash            string     `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Status               string     `gorm:"size:20;not null;default:'pending';index" json:"status"` // pending, claimed, completed, expired, revoked
	ExpiresAt            time.Time  `gorm:"index;not null" json:"expires_at"`
	InstallationID       string     `gorm:"size:128" json:"-"`
	InstallerPublicKey   string     `gorm:"type:text" json:"-"`
	InstallerFingerprint string     `gorm:"size:64" json:"-"`
	EncryptedBundle      string     `gorm:"type:text" json:"-"`
	ClaimedAt            *time.Time `json:"claimed_at,omitempty"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
}

// HardwareCommand is a durable command sent to a printer, gate, scanner, or
// identity reader. A command is never considered successful merely because a
// browser requested it; the device must acknowledge it with the same token.
//
// Delivery is intentionally one-way: a poll atomically changes queued to
// delivered and must not dispatch that command number again. Devices must
// treat CommandNo as their execution idempotency key and acknowledge the
// original command after a lost acknowledgement response. This favors a
// missed command that can be investigated over a duplicate physical action
// such as opening a gate twice.
type HardwareCommand struct {
	Base
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID     uint       `gorm:"index;not null" json:"device_id"`
	CommandNo    string     `gorm:"size:50;uniqueIndex;not null" json:"command_no"`
	Kind         string     `gorm:"size:30;not null" json:"kind"` // print, verify, open_gate, read_identity
	PayloadJSON  string     `gorm:"type:text" json:"payload_json"`
	Status       string     `gorm:"size:20;not null;default:'queued';index" json:"status"` // queued, delivered, acknowledged, failed, expired
	AttemptCount int        `json:"attempt_count"`
	LastError    string     `gorm:"size:500" json:"last_error,omitempty"`
	AckToken     string     `gorm:"size:100;not null" json:"-"`
	QueuedAt     time.Time  `json:"queued_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	AckedAt      *time.Time `json:"acked_at,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
}

// HardwareEvent is the immutable device-side acknowledgement/audit fact.
type HardwareEvent struct {
	Base
	TenantID  uint   `gorm:"index;not null" json:"tenant_id"`
	DeviceID  uint   `gorm:"index;not null" json:"device_id"`
	CommandNo string `gorm:"size:50;index;not null" json:"command_no"`
	EventType string `gorm:"size:30;not null" json:"event_type"`
	Payload   string `gorm:"type:text" json:"payload,omitempty"`
}

// DeviceRequestNonce is a short-lived replay guard for signed device calls.
type DeviceRequestNonce struct {
	Base
	TenantID  uint      `gorm:"index;not null" json:"tenant_id"`
	DeviceID  uint      `gorm:"uniqueIndex:idx_device_nonce,priority:1;not null" json:"device_id"`
	Nonce     string    `gorm:"size:100;uniqueIndex:idx_device_nonce,priority:2;not null" json:"-"`
	RequestID string    `gorm:"size:100;index;not null" json:"request_id"`
	Path      string    `gorm:"size:160;not null" json:"path"`
	ExpiresAt time.Time `gorm:"index;not null" json:"expires_at"`
}

// DeviceVerification persists the exact response returned for one physical
// scan. It separates ticket verification from the later physical open result.
type DeviceVerification struct {
	Base
	TenantID        uint       `gorm:"index;not null" json:"tenant_id"`
	ScenicAreaID    uint       `gorm:"index;not null" json:"scenic_area_id"`
	DeviceID        uint       `gorm:"uniqueIndex:idx_device_verify_request,priority:1;not null" json:"device_id"`
	RequestID       string     `gorm:"size:100;uniqueIndex:idx_device_verify_request,priority:2;not null" json:"request_id"`
	RequestHash     string     `gorm:"size:64;not null" json:"-"`
	TicketCode      string     `gorm:"size:100;index;not null" json:"ticket_code"`
	Status          string     `gorm:"size:20;index;not null" json:"status"` // processing, completed
	ResponseCode    int        `json:"response_code"`
	Result          string     `gorm:"size:20" json:"result"`
	DisplayText     string     `gorm:"size:255" json:"display_text"`
	VoiceFile       string     `gorm:"size:100" json:"voice_file"`
	VoiceCode       string     `gorm:"size:100" json:"voice_code"`
	OpenDuration    int        `json:"open_duration"`
	CheckInRecordID uint       `gorm:"index" json:"check_in_record_id"`
	OpenStatus      string     `gorm:"size:20;index" json:"open_status"` // pending, opened, failed, unknown, not_required (handheld)
	OpenError       string     `gorm:"size:500" json:"open_error,omitempty"`
	OpenedAt        *time.Time `json:"opened_at,omitempty"`
	OpenReportedAt  *time.Time `json:"open_reported_at,omitempty"`
}
