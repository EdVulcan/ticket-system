package model

import "time"

// TenantMemberStatus values describe the lifecycle of a tenant-owned customer
// profile. A merged profile remains as an auditable alias and points at the
// canonical member through MergedIntoMemberID.
const (
	TenantMemberStatusActive          = "active"
	TenantMemberStatusFrozen          = "frozen"
	TenantMemberStatusMerged          = "merged"
	TenantMemberStatusAnonymized      = "anonymized"
	TenantMembershipStatusProvisional = "provisional"
	TenantMembershipStatusActive      = "active"
	TenantMembershipStatusWithdrawn   = "withdrawn"
	TenantMemberIdentityStatusActive  = "active"
	TenantMemberIdentityStatusRevoked = "revoked"
	TenantMemberContactTypePhone      = "phone"
	TenantMemberContactStatusActive   = "active"
	TenantMemberContactStatusRevoked  = "revoked"
	TenantMemberConsentDecisionGrant  = "grant"
	TenantMemberConsentDecisionRevoke = "revoke"
)

// TenantMember is the tenant-scoped canonical customer profile. It is an
// additive layer over existing channel customer/session records and does not
// replace those authentication facts.
type TenantMember struct {
	Base
	TenantID                     uint      `gorm:"not null;index:idx_tenant_member_no,priority:1" json:"tenant_id"`
	MemberNo                     string    `gorm:"size:64;not null;uniqueIndex:idx_tenant_member_no,priority:2" json:"member_no"`
	Status                       string    `gorm:"size:20;not null;default:'active';index;check:chk_tenant_member_status,status IN ('active','frozen','merged','anonymized')" json:"status"`
	MembershipStatus             string    `gorm:"size:20;not null;default:'provisional';index;check:chk_tenant_member_membership_status,membership_status IN ('provisional','active','withdrawn')" json:"membership_status"`
	DisplayName                  string    `gorm:"size:100" json:"display_name,omitempty"`
	AvatarURL                    string    `gorm:"size:500" json:"avatar_url,omitempty"`
	RegistrationChannelAccountID uint      `gorm:"index" json:"registration_channel_account_id,omitempty"`
	MergedIntoMemberID           *uint     `gorm:"index" json:"-"`
	FirstSeenAt                  time.Time `gorm:"not null" json:"first_seen_at"`
	LastSeenAt                   time.Time `gorm:"not null" json:"last_seen_at"`
}

// TenantMemberIdentity maps one authenticated self-owned channel identity to
// a tenant member. The subject itself is intentionally represented only by a
// keyed blind index; plaintext platform identifiers remain in channel-owned
// authentication records.
type TenantMemberIdentity struct {
	Base
	TenantID          uint       `gorm:"not null;index:idx_tenant_member_identity_scope,priority:1;uniqueIndex:idx_tenant_member_identity_unique,priority:1" json:"tenant_id"`
	MemberID          uint       `gorm:"not null;index:idx_tenant_member_identity_scope,priority:2;index:idx_tenant_member_identity_member" json:"member_id"`
	Provider          string     `gorm:"size:40;not null;uniqueIndex:idx_tenant_member_identity_unique,priority:3" json:"provider"`
	ChannelAccountID  uint       `gorm:"not null;uniqueIndex:idx_tenant_member_identity_unique,priority:2" json:"channel_account_id"`
	SubjectBlindIndex string     `gorm:"size:128;not null;uniqueIndex:idx_tenant_member_identity_unique,priority:4" json:"-"`
	BlindIndexVersion int        `gorm:"not null;default:1" json:"-"`
	Status            string     `gorm:"size:20;not null;default:'active';index;check:chk_tenant_member_identity_status,status IN ('active','revoked')" json:"status"`
	LinkMethod        string     `gorm:"size:30;not null" json:"link_method"`
	LinkedAt          time.Time  `gorm:"not null" json:"linked_at"`
	LastSeenAt        time.Time  `gorm:"not null" json:"last_seen_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

// TenantMemberVerifiedContact stores a strongly verified contact. The value
// is encrypted at rest; ValueBlindIndex is the keyed equality lookup value.
type TenantMemberVerifiedContact struct {
	Base
	TenantID             uint       `gorm:"not null;index:idx_tenant_member_contact_scope,priority:1" json:"tenant_id"`
	MemberID             uint       `gorm:"not null;index:idx_tenant_member_contact_scope,priority:2;index:idx_tenant_member_contact_member" json:"member_id"`
	ContactType          string     `gorm:"size:20;not null;default:'phone';index" json:"contact_type"`
	ValueCiphertext      string     `gorm:"type:text;not null" json:"-"`
	ValueBlindIndex      string     `gorm:"size:128;not null" json:"-"`
	BlindIndexVersion    int        `gorm:"not null;default:1" json:"-"`
	EncryptionKeyVersion int        `gorm:"not null;default:1" json:"-"`
	VerificationMethod   string     `gorm:"size:40;not null" json:"verification_method"`
	VerifiedAt           time.Time  `gorm:"not null" json:"verified_at"`
	Status               string     `gorm:"size:20;not null;default:'active';index;check:chk_tenant_member_contact_status,status IN ('active','revoked')" json:"status"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
}

// TenantMemberConsent is an append-only authorization fact. Separate rows
// are required for separate purposes such as privacy and marketing.
type TenantMemberConsent struct {
	Base
	TenantID         uint      `gorm:"not null;index:idx_tenant_member_consent_scope,priority:1" json:"tenant_id"`
	MemberID         uint      `gorm:"not null;index:idx_tenant_member_consent_scope,priority:2" json:"member_id"`
	Purpose          string    `gorm:"size:40;not null;index" json:"purpose"`
	PolicyVersion    string    `gorm:"size:40;not null" json:"policy_version"`
	Decision         string    `gorm:"size:20;not null;check:chk_tenant_member_consent_decision,decision IN ('grant','revoke')" json:"decision"`
	ChannelAccountID uint      `gorm:"index" json:"channel_account_id,omitempty"`
	EvidenceMethod   string    `gorm:"size:40;not null" json:"evidence_method"`
	IdempotencyKey   string    `gorm:"size:160;not null;index" json:"-"`
	OccurredAt       time.Time `gorm:"not null;index" json:"occurred_at"`
}

// TenantMemberEvent is the append-only security/lifecycle event stream for a
// member. MetadataJSON may contain only non-sensitive metadata.
type TenantMemberEvent struct {
	Base
	TenantID       uint      `gorm:"not null;index:idx_tenant_member_event_scope,priority:1" json:"tenant_id"`
	MemberID       uint      `gorm:"not null;index:idx_tenant_member_event_scope,priority:2" json:"member_id"`
	EventType      string    `gorm:"size:50;not null;index" json:"event_type"`
	MetadataJSON   string    `gorm:"type:text;not null;default:'{}'" json:"-"`
	IdempotencyKey string    `gorm:"size:160;not null;index" json:"-"`
	OccurredAt     time.Time `gorm:"not null;index" json:"occurred_at"`
}
