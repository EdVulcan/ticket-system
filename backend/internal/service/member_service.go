package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MemberStatusActive     = "active"
	MemberStatusFrozen     = "frozen"
	MemberStatusMerged     = "merged"
	MemberStatusAnonymized = "anonymized"

	MembershipStatusProvisional = "provisional"
	MembershipStatusActive      = "active"
	MembershipStatusWithdrawn   = "withdrawn"

	MemberIdentityActive  = "active"
	MemberIdentityRevoked = "revoked"
	MemberContactActive   = "active"
	MemberContactRevoked  = "revoked"
)

var (
	ErrMemberInvalidInput        = errors.New("member request is invalid")
	ErrMemberTenantUnavailable   = errors.New("tenant member entry is unavailable")
	ErrMemberExternalEntry       = errors.New("external or disabled entry cannot create a member")
	ErrMemberNotFound            = errors.New("member not found")
	ErrMemberMergedCycle         = errors.New("member merge cycle detected")
	ErrMemberPhoneConflict       = errors.New("verified phone belongs to another member")
	ErrMemberConsentRequired     = errors.New("membership consent is required")
	ErrMemberIdempotencyConflict = errors.New("member idempotency key was reused with different data")
	ErrMemberExportReason        = errors.New("member export reason is required")
	ErrMemberExportTooLarge      = errors.New("member export exceeds the safe row limit")
	ErrMemberIdentityRevokeAuth  = errors.New("recent strong authentication is required")
	ErrMemberLastIdentity        = errors.New("the last recoverable identity cannot be revoked")
	ErrMemberLifecycle           = errors.New("member lifecycle state does not allow this operation")
)

// MemberAdminListQuery is the tenant-admin read filter. Phone is normalized
// inside ListMembers and is never written to logs or used as a raw SQL value.
type MemberAdminListQuery struct {
	Page     int
	PageSize int
	Status   string
	Keyword  string
	Phone    string
}

type MemberAdminRecord struct {
	ID               uint
	MemberNo         string
	DisplayName      string
	Phone            string
	PhoneMasked      string
	Status           string
	MembershipStatus string
	SourceCount      int
	CreatedAt        time.Time
	LastSeenAt       *time.Time
}

type MemberAdminIdentity struct {
	Channel string
	Subject string
	Status  string
}

type MemberAdminAuthorization struct {
	PhoneVerified bool
	Consented     bool
}

type MemberAdminOrderSummary struct {
	TicketCount     int64
	HotelCount      int64
	CommerceCount   int64
	PaidOrderCount  int64
	TotalSpendCents int64
}

type MemberAdminDetail struct {
	MemberAdminRecord
	Identities    []MemberAdminIdentity
	Authorization MemberAdminAuthorization
	Orders        MemberAdminOrderSummary
}

type MemberAdminPage struct {
	Items    []MemberAdminRecord
	Page     int
	PageSize int
	Total    int64
	HasNext  bool
}

const maxMemberExportRows = 10000

// MemberSelfProfile is the small, customer-safe projection used by
// authenticated first-party storefronts. It deliberately exposes neither
// internal member IDs nor provider subjects; those values are authorization
// facts owned by the server, not client identifiers.
type MemberSelfProfile struct {
	MemberNo                 string `json:"member_no"`
	DisplayName              string `json:"display_name,omitempty"`
	AvatarURL                string `json:"avatar_url,omitempty"`
	Status                   string `json:"status"`
	MembershipStatus         string `json:"membership_status"`
	PhoneMasked              string `json:"phone_masked,omitempty"`
	PhoneVerified            bool   `json:"phone_verified"`
	MembershipConsentGranted bool   `json:"membership_consent_granted"`
	SourceCount              int    `json:"source_count"`
}

// MemberService is the only service allowed to maintain the tenant member
// identity graph. Channel adapters authenticate provider credentials before
// calling this service; the service never accepts a client-supplied member ID
// as an authorization fact.
type MemberService struct {
	db            *gorm.DB
	blindIndexKey []byte
	blindIndexVer int
	now           func() time.Time
	encrypt       func(string) (string, error)
}

func NewMemberService(db *gorm.DB, blindIndexKey []byte) (*MemberService, error) {
	if db == nil || len(blindIndexKey) < 16 {
		return nil, ErrMemberInvalidInput
	}
	key := append([]byte(nil), blindIndexKey...)
	return &MemberService{db: db, blindIndexKey: key, blindIndexVer: 1, now: time.Now, encrypt: utils.EncryptAES}, nil
}

// NewMemberServiceForTest allows tests to provide deterministic time and an
// encryption seam without weakening production construction requirements.
func NewMemberServiceForTest(db *gorm.DB, blindIndexKey []byte, encrypt func(string) (string, error), now func() time.Time) (*MemberService, error) {
	s, err := NewMemberService(db, blindIndexKey)
	if err != nil {
		return nil, err
	}
	if encrypt != nil {
		s.encrypt = encrypt
	}
	if now != nil {
		s.now = now
	}
	return s, nil
}

type SelfHostedIdentityInput struct {
	TenantID         uint
	ChannelAccountID uint
	Provider         string
	Subject          string
	DisplayName      string
	AvatarURL        string
}

type TrustedPhoneVerificationInput struct {
	TenantID                 uint
	MemberID                 uint
	Phone                    string
	VerificationMethod       string
	MembershipConsentGranted bool
	// IdempotencyKey is an optional authenticated platform-request identity.
	// It is stored only on the non-sensitive member event and never contains
	// the provider code, encrypted payload, or phone number.
	IdempotencyKey   string
	ChannelAccountID uint
}

type ConsentInput struct {
	TenantID         uint
	MemberID         uint
	Purpose          string
	PolicyVersion    string
	Decision         string
	ChannelAccountID uint
	EvidenceMethod   string
	IdempotencyKey   string
}

type AutoConvergeInput struct {
	TenantID        uint
	CurrentMemberID uint
	TargetMemberID  uint
	TrustedPhone    string
	IdempotencyKey  string
}

type RevokeIdentityInput struct {
	TenantID       uint
	IdentityID     uint
	ActorUserID    uint
	ActorRole      string
	Reason         string
	StronglyAuthn  bool
	IdempotencyKey string
}

type memberEventMetadata struct {
	Provider    string `json:"provider,omitempty"`
	ChannelID   uint   `json:"channel_account_id,omitempty"`
	IdentityID  uint   `json:"identity_id,omitempty"`
	ContactType string `json:"contact_type,omitempty"`
	// ValueFingerprint binds idempotent verification evidence to the
	// encrypted/盲索引 value without persisting the raw phone number.
	ValueFingerprint string `json:"value_fingerprint,omitempty"`
	Method           string `json:"method,omitempty"`
	ConsentGranted   bool   `json:"consent_granted,omitempty"`
	PreviousState    string `json:"previous_state,omitempty"`
	TargetMember     uint   `json:"target_member_id,omitempty"`
}

var allowedMemberProviders = map[string]struct{}{
	"wechat_miniapp":      {},
	"xiaohongshu_miniapp": {},
	"app":                 {},
	"web":                 {},
}

func (s *MemberService) ResolveSelfHostedIdentity(input SelfHostedIdentityInput) (*model.TenantMember, error) {
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.Subject = strings.TrimSpace(input.Subject)
	if input.TenantID == 0 || input.ChannelAccountID == 0 || input.Subject == "" {
		return nil, ErrMemberInvalidInput
	}
	if _, ok := allowedMemberProviders[input.Provider]; !ok {
		return nil, ErrMemberExternalEntry
	}
	blind := s.identityBlindIndex(input.TenantID, input.ChannelAccountID, input.Provider, input.Subject)
	for attempt := 0; attempt < 2; attempt++ {
		var result model.TenantMember
		err := s.db.Transaction(func(tx *gorm.DB) error {
			identity := model.TenantMemberIdentity{}
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_account_id = ? AND provider = ? AND subject_blind_index = ?", input.TenantID, input.ChannelAccountID, input.Provider, blind).First(&identity).Error
			now := s.now()
			if errors.Is(err, gorm.ErrRecordNotFound) {
				member := model.TenantMember{
					TenantID: input.TenantID, MemberNo: newMemberNo(), Status: MemberStatusActive,
					MembershipStatus: MembershipStatusProvisional, DisplayName: strings.TrimSpace(input.DisplayName), AvatarURL: strings.TrimSpace(input.AvatarURL),
					RegistrationChannelAccountID: input.ChannelAccountID, FirstSeenAt: now, LastSeenAt: now,
				}
				if err := tx.Create(&member).Error; err != nil {
					return err
				}
				identity = model.TenantMemberIdentity{
					TenantID: input.TenantID, MemberID: member.ID, Provider: input.Provider, ChannelAccountID: input.ChannelAccountID,
					SubjectBlindIndex: blind, BlindIndexVersion: s.blindIndexVer, Status: MemberIdentityActive, LinkMethod: "first_login", LinkedAt: now, LastSeenAt: now,
				}
				if err := tx.Create(&identity).Error; err != nil {
					return err
				}
				if err := s.appendEventTx(tx, member.ID, input.TenantID, "member.created", memberEventMetadata{Provider: input.Provider, ChannelID: input.ChannelAccountID}); err != nil {
					return err
				}
				result = member
				return nil
			}
			if err != nil {
				return err
			}
			if identity.TenantID != input.TenantID {
				return ErrMemberExternalEntry
			}
			member, err := s.resolveCanonicalTx(tx, input.TenantID, identity.MemberID, true)
			if err != nil {
				return err
			}
			if member.Status != MemberStatusActive || member.MembershipStatus == MembershipStatusWithdrawn {
				return ErrMemberLifecycle
			}
			// A revoked channel identity is historical security state, not a
			// transient session condition. Do not silently reactivate it on a
			// later platform login; a controlled recovery flow must explicitly
			// create/link a replacement identity. Callers may keep the existing
			// channel session unassociated with the member so legacy commerce
			// access is not blocked by the additive member layer.
			if identity.Status != MemberIdentityActive {
				return ErrMemberLifecycle
			}
			identity.LastSeenAt = now
			if err := tx.Save(&identity).Error; err != nil {
				return err
			}
			if member.DisplayName == "" && strings.TrimSpace(input.DisplayName) != "" {
				member.DisplayName = strings.TrimSpace(input.DisplayName)
			}
			member.LastSeenAt = now
			if err := tx.Save(member).Error; err != nil {
				return err
			}
			result = *member
			return nil
		})
		if err == nil {
			return &result, nil
		}
		if !isUniqueViolation(err) {
			return nil, err
		}
	}
	return nil, ErrMemberInvalidInput
}

func (s *MemberService) ResolveCanonical(tenantID, memberID uint) (*model.TenantMember, error) {
	if tenantID == 0 || memberID == 0 {
		return nil, ErrMemberInvalidInput
	}
	var result *model.TenantMember
	err := s.db.Transaction(func(tx *gorm.DB) error {
		member, err := s.resolveCanonicalTx(tx, tenantID, memberID, true)
		if err == nil {
			result = member
		}
		return err
	})
	return result, err
}

func (s *MemberService) VerifyTrustedPhone(input TrustedPhoneVerificationInput) (*model.TenantMember, error) {
	phone, err := normalizePhone(input.Phone)
	if err != nil || input.TenantID == 0 || input.MemberID == 0 || strings.TrimSpace(input.VerificationMethod) == "" {
		return nil, ErrMemberInvalidInput
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	blind := s.contactBlindIndex(input.TenantID, "phone", phone)
	ciphertext, err := s.encrypt(phone)
	if err != nil {
		return nil, err
	}
	var result *model.TenantMember
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		result, txErr = s.verifyTrustedPhoneTx(tx, input, phone, blind, ciphertext, nil)
		return txErr
	})
	return result, err
}

// VerifyTrustedPhoneWithConsent atomically records a platform-verified phone
// and the explicit membership consent that accompanied it. The older
// VerifyTrustedPhone method remains available for non-storefront callers that
// do not collect consent in the same request.
func (s *MemberService) VerifyTrustedPhoneWithConsent(input TrustedPhoneVerificationInput, consent ConsentInput) (*model.TenantMember, error) {
	phone, err := normalizePhone(input.Phone)
	if err != nil || input.TenantID == 0 || input.MemberID == 0 || strings.TrimSpace(input.VerificationMethod) == "" || !input.MembershipConsentGranted {
		return nil, ErrMemberInvalidInput
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	consent.Purpose = strings.TrimSpace(consent.Purpose)
	consent.PolicyVersion = strings.TrimSpace(consent.PolicyVersion)
	consent.Decision = strings.ToLower(strings.TrimSpace(consent.Decision))
	consent.EvidenceMethod = strings.TrimSpace(consent.EvidenceMethod)
	consent.IdempotencyKey = strings.TrimSpace(consent.IdempotencyKey)
	if consent.TenantID != input.TenantID || consent.MemberID != input.MemberID || consent.Purpose != "membership" || consent.PolicyVersion == "" || consent.Decision != "grant" || consent.IdempotencyKey == "" || consent.ChannelAccountID != input.ChannelAccountID || consent.EvidenceMethod != strings.TrimSpace(input.VerificationMethod) {
		return nil, ErrMemberInvalidInput
	}
	blind := s.contactBlindIndex(input.TenantID, "phone", phone)
	ciphertext, err := s.encrypt(phone)
	if err != nil {
		return nil, err
	}
	var result *model.TenantMember
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var txErr error
		result, txErr = s.verifyTrustedPhoneTx(tx, input, phone, blind, ciphertext, &consent)
		return txErr
	})
	return result, err
}

func (s *MemberService) verifyTrustedPhoneTx(tx *gorm.DB, input TrustedPhoneVerificationInput, phone, blind, ciphertext string, consent *ConsentInput) (*model.TenantMember, error) {
	member, err := s.resolveCanonicalTx(tx, input.TenantID, input.MemberID, true)
	if err != nil {
		return nil, err
	}
	if member.Status != MemberStatusActive || member.MembershipStatus == MembershipStatusWithdrawn {
		return nil, ErrMemberLifecycle
	}
	var existing model.TenantMemberVerifiedContact
	lookupErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND contact_type = ? AND value_blind_index = ? AND status = ?", input.TenantID, "phone", blind, MemberContactActive).First(&existing).Error
	if lookupErr == nil {
		owner, ownerErr := s.resolveCanonicalTx(tx, input.TenantID, existing.MemberID, true)
		if ownerErr != nil {
			return nil, ownerErr
		}
		if owner.ID != member.ID {
			return nil, ErrMemberPhoneConflict
		}
	} else if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return nil, lookupErr
	} else {
		contact := model.TenantMemberVerifiedContact{
			TenantID: input.TenantID, MemberID: member.ID, ContactType: "phone", ValueCiphertext: ciphertext, ValueBlindIndex: blind,
			BlindIndexVersion: s.blindIndexVer, EncryptionKeyVersion: 1, VerificationMethod: strings.TrimSpace(input.VerificationMethod), VerifiedAt: s.now(), Status: MemberContactActive,
		}
		if err := tx.Create(&contact).Error; err != nil {
			if isUniqueViolation(err) {
				return nil, ErrMemberPhoneConflict
			}
			return nil, err
		}
	}
	if consent != nil {
		if err := s.appendMembershipConsentTx(tx, member.ID, *consent); err != nil {
			return nil, err
		}
	}
	// Storefront verification records both facts in one transaction. The
	// legacy non-storefront method may still assert consent directly, but an
	// explicit consent record by itself never upgrades a member without a
	// trusted phone contact.
	if consent == nil && input.MembershipConsentGranted {
		member.MembershipStatus = MembershipStatusActive
	} else if err := s.refreshMembershipStatusTx(tx, member); err != nil {
		return nil, err
	}
	if err := tx.Save(member).Error; err != nil {
		return nil, err
	}
	// Keep a non-sensitive, idempotent evidence event for platform
	// verification. The platform code and phone value are deliberately
	// excluded; the encrypted contact is the only persisted phone value.
	if input.IdempotencyKey != "" {
		if err := s.appendEventTx(tx, member.ID, input.TenantID, "member.phone.verified", memberEventMetadata{ContactType: "phone", ValueFingerprint: blind, Method: input.VerificationMethod, ChannelID: input.ChannelAccountID, ConsentGranted: input.MembershipConsentGranted}, input.IdempotencyKey); err != nil {
			return nil, err
		}
	}
	return member, nil
}

// refreshMembershipStatusTx derives membership qualification from the latest
// membership consent and the presence of an active trusted phone. Keeping this
// derivation in one transaction prevents a consent-only record from being
// mistaken for a verified membership.
func (s *MemberService) refreshMembershipStatusTx(tx *gorm.DB, member *model.TenantMember) error {
	if member == nil {
		return ErrMemberInvalidInput
	}
	desired := MembershipStatusProvisional
	var consent model.TenantMemberConsent
	err := tx.Where("tenant_id = ? AND member_id = ? AND purpose = ?", member.TenantID, member.ID, "membership").
		Order("occurred_at DESC, id DESC").First(&consent).Error
	if err == nil {
		switch consent.Decision {
		case model.TenantMemberConsentDecisionRevoke:
			desired = MembershipStatusWithdrawn
		case model.TenantMemberConsentDecisionGrant:
			var verified int64
			if err := tx.Model(&model.TenantMemberVerifiedContact{}).
				Where("tenant_id = ? AND member_id = ? AND contact_type = ? AND status = ?", member.TenantID, member.ID, model.TenantMemberContactTypePhone, MemberContactActive).
				Count(&verified).Error; err != nil {
				return err
			}
			if verified > 0 {
				desired = MembershipStatusActive
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	member.MembershipStatus = desired
	return nil
}

func (s *MemberService) appendMembershipConsentTx(tx *gorm.DB, memberID uint, input ConsentInput) error {
	var existing model.TenantMemberConsent
	err := tx.Where("tenant_id = ? AND idempotency_key = ?", input.TenantID, input.IdempotencyKey).First(&existing).Error
	if err == nil {
		if existing.MemberID != memberID || existing.Purpose != input.Purpose || existing.PolicyVersion != input.PolicyVersion || existing.Decision != input.Decision || existing.ChannelAccountID != input.ChannelAccountID || existing.EvidenceMethod != input.EvidenceMethod {
			return ErrMemberIdempotencyConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row := model.TenantMemberConsent{TenantID: input.TenantID, MemberID: memberID, Purpose: input.Purpose, PolicyVersion: input.PolicyVersion, Decision: input.Decision, ChannelAccountID: input.ChannelAccountID, EvidenceMethod: input.EvidenceMethod, IdempotencyKey: input.IdempotencyKey, OccurredAt: s.now()}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", input.TenantID, input.IdempotencyKey).First(&existing).Error; err != nil {
			return err
		}
		if existing.MemberID != memberID || existing.Purpose != input.Purpose || existing.PolicyVersion != input.PolicyVersion || existing.Decision != input.Decision || existing.ChannelAccountID != input.ChannelAccountID || existing.EvidenceMethod != input.EvidenceMethod {
			return ErrMemberIdempotencyConflict
		}
		return nil
	}
	return s.appendEventTx(tx, memberID, input.TenantID, "member.consent."+input.Decision, memberEventMetadata{Method: input.Purpose})
}

func (s *MemberService) AppendConsent(input ConsentInput) error {
	input.Purpose = strings.TrimSpace(input.Purpose)
	input.PolicyVersion = strings.TrimSpace(input.PolicyVersion)
	input.Decision = strings.ToLower(strings.TrimSpace(input.Decision))
	input.EvidenceMethod = strings.TrimSpace(input.EvidenceMethod)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.TenantID == 0 || input.MemberID == 0 || input.Purpose == "" || input.PolicyVersion == "" || (input.Decision != "grant" && input.Decision != "revoke") || input.IdempotencyKey == "" {
		return ErrMemberInvalidInput
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		member, err := s.resolveCanonicalTx(tx, input.TenantID, input.MemberID, true)
		if err != nil {
			return err
		}
		var existing model.TenantMemberConsent
		err = tx.Where("tenant_id = ? AND idempotency_key = ?", input.TenantID, input.IdempotencyKey).First(&existing).Error
		if err == nil {
			if existing.MemberID != member.ID || existing.Purpose != input.Purpose || existing.PolicyVersion != input.PolicyVersion || existing.Decision != input.Decision || existing.ChannelAccountID != input.ChannelAccountID || existing.EvidenceMethod != input.EvidenceMethod {
				return ErrMemberIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row := model.TenantMemberConsent{TenantID: input.TenantID, MemberID: member.ID, Purpose: input.Purpose, PolicyVersion: input.PolicyVersion, Decision: input.Decision, ChannelAccountID: input.ChannelAccountID, EvidenceMethod: input.EvidenceMethod, IdempotencyKey: input.IdempotencyKey, OccurredAt: s.now()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("tenant_id = ? AND idempotency_key = ?", input.TenantID, input.IdempotencyKey).First(&existing).Error; err != nil {
				return err
			}
			if existing.MemberID != member.ID || existing.Purpose != input.Purpose || existing.PolicyVersion != input.PolicyVersion || existing.Decision != input.Decision || existing.ChannelAccountID != input.ChannelAccountID || existing.EvidenceMethod != input.EvidenceMethod {
				return ErrMemberIdempotencyConflict
			}
			return nil
		}
		if input.Purpose == "membership" {
			if err := s.refreshMembershipStatusTx(tx, member); err != nil {
				return err
			}
			if err := tx.Save(member).Error; err != nil {
				return err
			}
		}
		return s.appendEventTx(tx, member.ID, input.TenantID, "member.consent."+input.Decision, memberEventMetadata{Method: input.Purpose})
	})
}

func (s *MemberService) AutoConverge(input AutoConvergeInput) (*model.TenantMember, error) {
	if input.TenantID == 0 || input.CurrentMemberID == 0 || input.TargetMemberID == 0 || input.CurrentMemberID == input.TargetMemberID || strings.TrimSpace(input.TrustedPhone) == "" || input.IdempotencyKey == "" {
		return nil, ErrMemberInvalidInput
	}
	phone, err := normalizePhone(input.TrustedPhone)
	if err != nil {
		return nil, ErrMemberInvalidInput
	}
	blind := s.contactBlindIndex(input.TenantID, "phone", phone)
	ids := []uint{input.CurrentMemberID, input.TargetMemberID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var result *model.TenantMember
	err = s.db.Transaction(func(tx *gorm.DB) error {
		rows := make(map[uint]*model.TenantMember, 2)
		for _, id := range ids {
			var row model.TenantMember
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", input.TenantID, id).First(&row).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrMemberNotFound
				}
				return err
			}
			rows[id] = &row
		}
		current, target := rows[input.CurrentMemberID], rows[input.TargetMemberID]
		var priorMerge model.TenantMemberEvent
		if err := tx.Where("tenant_id = ? AND member_id = ? AND event_type = ? AND idempotency_key = ?", input.TenantID, current.ID, "member.merged", input.IdempotencyKey).First(&priorMerge).Error; err == nil {
			canonical, resolveErr := s.resolveCanonicalTx(tx, input.TenantID, current.ID, true)
			if resolveErr != nil {
				return resolveErr
			}
			result = canonical
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if current.Status != MemberStatusActive || current.MembershipStatus != MembershipStatusProvisional || target.Status != MemberStatusActive || target.MembershipStatus != MembershipStatusActive {
			return ErrMemberLifecycle
		}
		if target.MembershipStatus == MembershipStatusWithdrawn || target.Status == MemberStatusFrozen || target.Status == MemberStatusMerged || target.Status == MemberStatusAnonymized {
			return ErrMemberLifecycle
		}
		var verified int64
		if err := tx.Model(&model.TenantMemberVerifiedContact{}).Where("tenant_id = ? AND member_id = ? AND status = ?", input.TenantID, current.ID, MemberContactActive).Count(&verified).Error; err != nil {
			return err
		}
		if verified != 0 {
			return ErrMemberLifecycle
		}
		var targetPhone int64
		if err := tx.Model(&model.TenantMemberVerifiedContact{}).Where("tenant_id = ? AND member_id = ? AND contact_type = ? AND value_blind_index = ? AND status = ?", input.TenantID, target.ID, "phone", blind, MemberContactActive).Count(&targetPhone).Error; err != nil {
			return err
		}
		if targetPhone != 1 {
			return ErrMemberPhoneConflict
		}
		current.Status = MemberStatusMerged
		targetID := target.ID
		current.MergedIntoMemberID = &targetID
		if err := tx.Save(current).Error; err != nil {
			return err
		}
		if err := s.appendEventTx(tx, current.ID, input.TenantID, "member.merged", memberEventMetadata{TargetMember: target.ID}, input.IdempotencyKey); err != nil {
			return err
		}
		result = target
		return nil
	})
	return result, err
}

// AutoConvergeByTrustedPhone resolves the already-verified owner of a phone
// and applies the narrowly-scoped provisional-to-active merge policy. It is
// used only after a current self-owned channel has supplied a fresh trusted
// phone assertion and explicit membership consent; it never transfers a phone
// or merges two active members.
func (s *MemberService) AutoConvergeByTrustedPhone(input AutoConvergeInput) (*model.TenantMember, error) {
	if input.TenantID == 0 || input.CurrentMemberID == 0 || strings.TrimSpace(input.TrustedPhone) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, ErrMemberInvalidInput
	}
	phone, err := normalizePhone(input.TrustedPhone)
	if err != nil {
		return nil, ErrMemberInvalidInput
	}
	blind := s.contactBlindIndex(input.TenantID, model.TenantMemberContactTypePhone, phone)
	var targetID uint
	if err := s.db.Model(&model.TenantMemberVerifiedContact{}).
		Where("tenant_id = ? AND contact_type = ? AND value_blind_index = ? AND status = ?", input.TenantID, model.TenantMemberContactTypePhone, blind, MemberContactActive).
		Order("id ASC").Pluck("member_id", &targetID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMemberPhoneConflict
		}
		return nil, err
	}
	if targetID == 0 || targetID == input.CurrentMemberID {
		return nil, ErrMemberPhoneConflict
	}
	target, err := s.ResolveCanonical(input.TenantID, targetID)
	if err != nil {
		return nil, err
	}
	return s.AutoConverge(AutoConvergeInput{
		TenantID: input.TenantID, CurrentMemberID: input.CurrentMemberID, TargetMemberID: target.ID,
		TrustedPhone: phone, IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
	})
}

func (s *MemberService) RevokeIdentity(input RevokeIdentityInput) error {
	if input.TenantID == 0 || input.IdentityID == 0 || !input.StronglyAuthn {
		return ErrMemberIdentityRevokeAuth
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var identity model.TenantMemberIdentity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", input.TenantID, input.IdentityID).First(&identity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMemberNotFound
			}
			return err
		}
		if identity.Status == MemberIdentityRevoked {
			return nil
		}
		canonical, err := s.resolveCanonicalTx(tx, input.TenantID, identity.MemberID, true)
		if err != nil {
			return err
		}
		var activeIdentities []model.TenantMemberIdentity
		if err := tx.Where("tenant_id = ? AND status = ?", input.TenantID, MemberIdentityActive).Find(&activeIdentities).Error; err != nil {
			return err
		}
		active := 0
		for _, candidate := range activeIdentities {
			owner, resolveErr := s.resolveCanonicalTx(tx, input.TenantID, candidate.MemberID, false)
			if resolveErr != nil {
				return resolveErr
			}
			if owner.ID == canonical.ID {
				active++
			}
		}
		if active <= 1 {
			return ErrMemberLastIdentity
		}
		now := s.now()
		identity.Status = MemberIdentityRevoked
		identity.RevokedAt = &now
		if err := tx.Save(&identity).Error; err != nil {
			return err
		}
		if err := s.appendEventTx(tx, identity.MemberID, input.TenantID, "member.identity.revoked", memberEventMetadata{IdentityID: identity.ID, Method: "strong_auth"}, input.IdempotencyKey); err != nil {
			return err
		}
		if input.ActorUserID != 0 {
			if err := recordAuditTx(tx, input.ActorUserID, input.TenantID, input.ActorRole, "tenant", "member.identity.revoke", "tenant_member_identity", identity.ID, strings.TrimSpace(input.Reason), "", "{}"); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *MemberService) Freeze(tenantID, memberID uint, frozen bool) error {
	if tenantID == 0 || memberID == 0 {
		return ErrMemberInvalidInput
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		member, err := s.resolveCanonicalTx(tx, tenantID, memberID, true)
		if err != nil {
			return err
		}
		if member.Status == MemberStatusMerged || member.Status == MemberStatusAnonymized {
			return ErrMemberLifecycle
		}
		if frozen {
			member.Status = MemberStatusFrozen
		} else if member.Status == MemberStatusFrozen {
			member.Status = MemberStatusActive
		}
		if err := tx.Save(member).Error; err != nil {
			return err
		}
		typeName := "member.unfrozen"
		if frozen {
			typeName = "member.frozen"
		}
		return s.appendEventTx(tx, member.ID, tenantID, typeName, memberEventMetadata{})
	})
}

func (s *MemberService) ListMembers(ctx context.Context, tenantID uint, query MemberAdminListQuery) (MemberAdminPage, error) {
	if tenantID == 0 || query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 {
		return MemberAdminPage{}, ErrMemberInvalidInput
	}
	_ = ctx
	query.Status = strings.TrimSpace(query.Status)
	query.Keyword = strings.TrimSpace(query.Keyword)
	query.Phone = strings.TrimSpace(query.Phone)
	if query.Status != "" {
		switch query.Status {
		case MemberStatusActive, MemberStatusFrozen, MemberStatusMerged, MemberStatusAnonymized, MembershipStatusProvisional, MembershipStatusWithdrawn:
		default:
			return MemberAdminPage{}, ErrMemberInvalidInput
		}
	}
	q := s.db.Model(&model.TenantMember{}).Where("tenant_id = ?", tenantID)
	// Normal customer views represent one row per canonical member. Merged
	// aliases remain available through the explicit "merged" audit filter so
	// historical identity records are not silently discarded or duplicated.
	includeMergedAliases := query.Status == MemberStatusMerged
	if !includeMergedAliases {
		q = q.Where("merged_into_member_id IS NULL")
	}
	if query.Status != "" {
		if query.Status == MembershipStatusProvisional || query.Status == MembershipStatusWithdrawn {
			q = q.Where("membership_status = ?", query.Status)
		} else if query.Status == MemberStatusActive {
			q = q.Where("status = ? AND membership_status = ?", MemberStatusActive, MembershipStatusActive)
		} else {
			q = q.Where("status = ?", query.Status)
		}
	}
	if query.Keyword != "" {
		like := "%" + strings.ReplaceAll(strings.ReplaceAll(query.Keyword, "%", "\\%"), "_", "\\_") + "%"
		q = q.Where("display_name ILIKE ? ESCAPE '\\' OR member_no ILIKE ? ESCAPE '\\'", like, like)
	}
	if query.Phone != "" {
		phone, err := normalizePhone(query.Phone)
		if err != nil {
			return MemberAdminPage{}, ErrMemberInvalidInput
		}
		blind := s.contactBlindIndex(tenantID, model.TenantMemberContactTypePhone, phone)
		var memberIDs []uint
		if err := s.db.Model(&model.TenantMemberVerifiedContact{}).
			Where("tenant_id = ? AND contact_type = ? AND value_blind_index = ? AND status = ?", tenantID, model.TenantMemberContactTypePhone, blind, MemberContactActive).
			Pluck("member_id", &memberIDs).Error; err != nil {
			return MemberAdminPage{}, err
		}
		if len(memberIDs) == 0 {
			return MemberAdminPage{Items: []MemberAdminRecord{}, Page: query.Page, PageSize: query.PageSize}, nil
		}
		q = q.Where("id IN ?", memberIDs)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return MemberAdminPage{}, err
	}
	var rows []model.TenantMember
	if err := q.Order("last_seen_at DESC, id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&rows).Error; err != nil {
		return MemberAdminPage{}, err
	}
	items := make([]MemberAdminRecord, 0, len(rows))
	for i := range rows {
		var record MemberAdminRecord
		var err error
		if includeMergedAliases && rows[i].MergedIntoMemberID != nil {
			// The merged filter is intentionally an alias/audit view. Project the
			// alias lifecycle state instead of replacing it with the canonical row.
			record, err = s.memberAdminRecordWithDB(s.db, tenantID, &rows[i], []uint{rows[i].ID})
		} else {
			record, err = s.memberAdminRecord(tenantID, &rows[i])
		}
		if err != nil {
			return MemberAdminPage{}, err
		}
		items = append(items, record)
	}
	return MemberAdminPage{Items: items, Page: query.Page, PageSize: query.PageSize, Total: total, HasNext: int64(query.Page*query.PageSize) < total}, nil
}

// ExportMembers reads the same tenant-scoped projection as the admin list,
// with an explicit row limit and an audit record. Sensitive phone values are
// only retained in the returned records when the caller already holds the
// separate members.sensitive.read permission.
func (s *MemberService) ExportMembers(ctx context.Context, tenantID uint, query MemberAdminListQuery, reason string, actorUserID uint, actorRole, requestID string, includeSensitive bool) ([]MemberAdminRecord, error) {
	if tenantID == 0 {
		return nil, ErrMemberInvalidInput
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 3 || len([]rune(reason)) > 200 {
		return nil, ErrMemberExportReason
	}
	query.Page = 1
	query.PageSize = 100
	items := make([]MemberAdminRecord, 0)
	for {
		page, err := s.ListMembers(ctx, tenantID, query)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if !includeSensitive {
				item.Phone = ""
			}
			items = append(items, item)
			if len(items) > maxMemberExportRows {
				return nil, ErrMemberExportTooLarge
			}
		}
		if !page.HasNext {
			break
		}
		query.Page++
	}
	after, err := json.Marshal(map[string]interface{}{
		"row_count":         len(items),
		"include_sensitive": includeSensitive,
		"status_filter":     strings.TrimSpace(query.Status),
	})
	if err != nil {
		return nil, err
	}
	if err := recordAuditTx(s.db, actorUserID, tenantID, actorRole, "tenant", "member.export", "tenant_member_export", tenantID, reason, "{}", string(after)); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *MemberService) GetMember(ctx context.Context, tenantID, memberID uint) (MemberAdminDetail, error) {
	if tenantID == 0 || memberID == 0 {
		return MemberAdminDetail{}, ErrMemberInvalidInput
	}
	_ = ctx
	var detail MemberAdminDetail
	err := s.db.Transaction(func(tx *gorm.DB) error {
		member, err := s.resolveCanonicalTx(tx, tenantID, memberID, false)
		if err != nil {
			return err
		}
		memberIDs, err := s.memberGraphIDsTx(tx, tenantID, member.ID)
		if err != nil {
			return err
		}
		record, err := s.memberAdminRecordWithDB(tx, tenantID, member, memberIDs)
		if err != nil {
			return err
		}
		detail = MemberAdminDetail{MemberAdminRecord: record, Identities: []MemberAdminIdentity{}}
		var identities []model.TenantMemberIdentity
		if err := tx.Where("tenant_id = ? AND member_id IN ?", tenantID, memberIDs).Order("id ASC").Find(&identities).Error; err != nil {
			return err
		}
		for _, identity := range identities {
			// The provider and account are useful operator context. The platform
			// subject is intentionally never returned to the admin API.
			detail.Identities = append(detail.Identities, MemberAdminIdentity{Channel: identity.Provider, Status: identity.Status})
		}
		var verified int64
		if err := tx.Model(&model.TenantMemberVerifiedContact{}).Where("tenant_id = ? AND member_id IN ? AND status = ?", tenantID, memberIDs, MemberContactActive).Count(&verified).Error; err != nil {
			return err
		}
		detail.Authorization.PhoneVerified = verified > 0
		var consent model.TenantMemberConsent
		if err := tx.Where("tenant_id = ? AND member_id IN ? AND purpose = ?", tenantID, memberIDs, "membership").Order("occurred_at DESC, id DESC").First(&consent).Error; err == nil {
			detail.Authorization.Consented = consent.Decision == model.TenantMemberConsentDecisionGrant
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Table("tickets AS ticket").
			Joins("JOIN order_items AS item ON item.id = ticket.order_item_id").
			Joins("JOIN orders AS ord ON ord.id = item.order_id AND ord.tenant_id = ?", tenantID).
			Where("ord.member_id IN ?", memberIDs).Count(&detail.Orders.TicketCount).Error; err != nil {
			return err
		}
		if err := tx.Table("hotel_reservations AS reservation").
			Joins("JOIN orders AS ord ON ord.id = reservation.order_id AND ord.tenant_id = ?", tenantID).
			Where("ord.member_id IN ?", memberIDs).Count(&detail.Orders.HotelCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.CommerceOrder{}).Where("tenant_id = ? AND member_id IN ?", tenantID, memberIDs).Count(&detail.Orders.CommerceCount).Error; err != nil {
			return err
		}
		return s.memberOrderSpendSummaryTx(tx, tenantID, memberIDs, &detail.Orders)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemberAdminDetail{}, ErrMemberNotFound
	}
	return detail, err
}

// GetSelfProfile returns only the authenticated member's own safe projection.
// The caller must already have resolved the member from a trusted storefront
// session; this method never accepts a provider subject or client identifier.
func (s *MemberService) GetSelfProfile(ctx context.Context, tenantID, memberID uint) (MemberSelfProfile, error) {
	if tenantID == 0 || memberID == 0 {
		return MemberSelfProfile{}, ErrMemberInvalidInput
	}
	_ = ctx
	var profile MemberSelfProfile
	err := s.db.Transaction(func(tx *gorm.DB) error {
		member, err := s.resolveCanonicalTx(tx, tenantID, memberID, false)
		if err != nil {
			return err
		}
		memberIDs, err := s.memberGraphIDsTx(tx, tenantID, member.ID)
		if err != nil {
			return err
		}
		profile = MemberSelfProfile{
			MemberNo:         member.MemberNo,
			DisplayName:      member.DisplayName,
			AvatarURL:        member.AvatarURL,
			Status:           member.Status,
			MembershipStatus: member.MembershipStatus,
		}
		var contact model.TenantMemberVerifiedContact
		if err := tx.Where("tenant_id = ? AND member_id IN ? AND contact_type = ? AND status = ?", tenantID, memberIDs, model.TenantMemberContactTypePhone, MemberContactActive).
			Order("verified_at DESC, id DESC").First(&contact).Error; err == nil {
			profile.PhoneVerified = true
			if phone, decryptErr := utils.DecryptAES(contact.ValueCiphertext); decryptErr == nil {
				profile.PhoneMasked = maskMemberPhone(phone)
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var consent model.TenantMemberConsent
		if err := tx.Where("tenant_id = ? AND member_id IN ? AND purpose = ?", tenantID, memberIDs, "membership").
			Order("occurred_at DESC, id DESC").First(&consent).Error; err == nil {
			profile.MembershipConsentGranted = consent.Decision == model.TenantMemberConsentDecisionGrant
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var sourceCount int64
		if err := tx.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ? AND member_id IN ?", tenantID, memberIDs).Count(&sourceCount).Error; err != nil {
			return err
		}
		profile.SourceCount = int(sourceCount)
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemberSelfProfile{}, ErrMemberNotFound
	}
	return profile, err
}

func (s *MemberService) SetMemberStatus(ctx context.Context, tenantID, memberID uint, status string, actorUserID uint, actorRole, requestID string) (MemberAdminDetail, error) {
	if tenantID == 0 || memberID == 0 {
		return MemberAdminDetail{}, ErrMemberInvalidInput
	}
	_ = ctx
	status = strings.TrimSpace(status)
	if status != MemberStatusActive && status != MemberStatusFrozen {
		return MemberAdminDetail{}, ErrMemberInvalidInput
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var member model.TenantMember
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenantID, memberID).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMemberNotFound
			}
			return err
		}
		if member.Status == MemberStatusMerged || member.Status == MemberStatusAnonymized || member.MergedIntoMemberID != nil {
			return ErrMemberLifecycle
		}
		if member.Status == status {
			return nil
		}
		previous := member.Status
		member.Status = status
		if err := tx.Save(&member).Error; err != nil {
			return err
		}
		if err := s.appendEventTx(tx, member.ID, tenantID, "member.status."+status, memberEventMetadata{PreviousState: previous}, requestID); err != nil {
			return err
		}
		if actorUserID != 0 {
			return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "member.status."+status, "tenant_member", member.ID, "", requestID, "{}")
		}
		return nil
	})
	if err != nil {
		return MemberAdminDetail{}, err
	}
	return s.GetMember(ctx, tenantID, memberID)
}

func (s *MemberService) memberAdminRecord(tenantID uint, member *model.TenantMember) (MemberAdminRecord, error) {
	canonical, err := s.resolveCanonicalTx(s.db, tenantID, member.ID, false)
	if err != nil {
		return MemberAdminRecord{}, err
	}
	memberIDs, err := s.memberGraphIDsTx(s.db, tenantID, canonical.ID)
	if err != nil {
		return MemberAdminRecord{}, err
	}
	// Always project the canonical record. Historical merged aliases remain
	// queryable for audit purposes, but must not surface a stale member number,
	// lifecycle state, or display name in the normal customer view.
	return s.memberAdminRecordWithDB(s.db, tenantID, canonical, memberIDs)
}

// memberOrderSpendSummaryTx projects the read-only customer spend summary.
// It deliberately reads immutable order/refund facts instead of maintaining a
// denormalized member balance, so refunds and historical aliases remain
// explainable and no financial workflow can be bypassed by a member update.
func (s *MemberService) memberOrderSpendSummaryTx(tx *gorm.DB, tenantID uint, memberIDs []uint, summary *MemberAdminOrderSummary) error {
	if tx == nil || summary == nil || tenantID == 0 || len(memberIDs) == 0 {
		return ErrMemberInvalidInput
	}
	type ticketOrderSpendRow struct {
		OrderID       uint
		Status        string
		TotalAmount   float64
		RefundedCents int64
	}
	var ticketOrders []ticketOrderSpendRow
	if err := tx.Table("orders AS ord").Select(`
		ord.id AS order_id,
		ord.status,
		ord.total_amount,
		COALESCE((
			SELECT SUM(CASE WHEN refund.amount_cents <> 0 THEN refund.amount_cents
				ELSE CAST(ROUND(refund.amount * 100.0) AS BIGINT) END)
			FROM refunds AS refund
			WHERE refund.tenant_id = ord.tenant_id
				AND refund.order_no = ord.order_no
				AND COALESCE(refund.parent_refund_id, 0) = 0
				AND refund.status IN ('succeeded', 'group_succeeded')
				AND refund.deleted_at IS NULL
		), 0) AS refunded_cents`).
		Where("ord.tenant_id = ? AND ord.member_id IN ? AND ord.status IN ? AND ord.deleted_at IS NULL", tenantID, memberIDs, []string{"paid", "completed", "partial_refunded", "refunded"}).
		Scan(&ticketOrders).Error; err != nil {
		return err
	}
	for _, order := range ticketOrders {
		summary.PaidOrderCount++
		net := moneyCents(order.TotalAmount) - order.RefundedCents
		// A few legacy fully-refunded rows predate a durable refund amount.
		// Their terminal status is still authoritative for customer spend.
		if order.Status == "refunded" && order.RefundedCents == 0 {
			net = 0
		}
		if net > 0 {
			summary.TotalSpendCents += net
		}
	}

	type commerceOrderSpendRow struct {
		PaymentStatus string
		TotalAmount   int64
		RefundedCents int64
	}
	var commerceOrders []commerceOrderSpendRow
	if err := tx.Model(&model.CommerceOrder{}).Select(`
		commerce_orders.payment_status,
		commerce_orders.total_amount_cents AS total_amount,
		COALESCE((
			SELECT SUM(after_sale.amount_cents)
			FROM commerce_after_sale_requests AS after_sale
			WHERE after_sale.tenant_id = commerce_orders.tenant_id
				AND after_sale.order_id = commerce_orders.id
				AND after_sale.type = 'refund'
				AND after_sale.status = 'completed'
				AND after_sale.deleted_at IS NULL
		), 0) AS refunded_cents`).
		Where("commerce_orders.tenant_id = ? AND commerce_orders.member_id IN ? AND commerce_orders.payment_status IN ? AND commerce_orders.deleted_at IS NULL", tenantID, memberIDs, []string{"paid", "refunded"}).
		Scan(&commerceOrders).Error; err != nil {
		return err
	}
	for _, order := range commerceOrders {
		summary.PaidOrderCount++
		net := order.TotalAmount - order.RefundedCents
		if order.PaymentStatus == "refunded" && order.RefundedCents == 0 {
			net = 0
		}
		if net > 0 {
			summary.TotalSpendCents += net
		}
	}
	return nil
}

func (s *MemberService) memberAdminRecordWithDB(db *gorm.DB, tenantID uint, member *model.TenantMember, memberIDs []uint) (MemberAdminRecord, error) {
	record := MemberAdminRecord{ID: member.ID, MemberNo: member.MemberNo, DisplayName: member.DisplayName, Status: member.Status, MembershipStatus: member.MembershipStatus, CreatedAt: member.CreatedAt, LastSeenAt: &member.LastSeenAt}
	var contact model.TenantMemberVerifiedContact
	if err := db.Where("tenant_id = ? AND member_id IN ? AND contact_type = ? AND status = ?", tenantID, memberIDs, model.TenantMemberContactTypePhone, MemberContactActive).Order("verified_at DESC, id DESC").First(&contact).Error; err == nil {
		if phone, decryptErr := utils.DecryptAES(contact.ValueCiphertext); decryptErr == nil {
			record.Phone = phone
			record.PhoneMasked = maskMemberPhone(phone)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return MemberAdminRecord{}, err
	}
	var sourceCount int64
	if err := db.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ? AND member_id IN ?", tenantID, memberIDs).Count(&sourceCount).Error; err != nil {
		return MemberAdminRecord{}, err
	}
	record.SourceCount = int(sourceCount)
	return record, nil
}

// memberGraphIDsTx returns the canonical member and all merged aliases that
// point to it. Historical orders retain the alias member_id, so read models
// must include this set instead of rewriting those immutable facts.
func (s *MemberService) memberGraphIDsTx(tx *gorm.DB, tenantID, canonicalID uint) ([]uint, error) {
	ids := []uint{canonicalID}
	seen := map[uint]struct{}{canonicalID: {}}
	frontier := []uint{canonicalID}
	for len(frontier) > 0 {
		var aliases []model.TenantMember
		if err := tx.Select("id").Where("tenant_id = ? AND status = ? AND merged_into_member_id IN ?", tenantID, MemberStatusMerged, frontier).Find(&aliases).Error; err != nil {
			return nil, err
		}
		frontier = frontier[:0]
		for _, alias := range aliases {
			if _, ok := seen[alias.ID]; ok {
				continue
			}
			seen[alias.ID] = struct{}{}
			ids = append(ids, alias.ID)
			frontier = append(frontier, alias.ID)
		}
	}
	return ids, nil
}

func maskMemberPhone(phone string) string {
	runes := []rune(strings.TrimSpace(phone))
	if len(runes) < 7 {
		return ""
	}
	end := len(runes) - 4
	if end <= 3 {
		// Keep the same three-leading/four-trailing convention while still
		// masking the single middle digit for the shortest accepted number.
		runes[3] = '*'
		return string(runes)
	}
	for i := 3; i < end; i++ {
		if runes[i] >= '0' && runes[i] <= '9' {
			runes[i] = '*'
		}
	}
	return string(runes)
}

func (s *MemberService) resolveCanonicalTx(tx *gorm.DB, tenantID, memberID uint, lock bool) (*model.TenantMember, error) {
	seen := make(map[uint]struct{})
	for hops := 0; hops < 32; hops++ {
		var current model.TenantMember
		q := tx
		if lock {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.Where("tenant_id = ? AND id = ?", tenantID, memberID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrMemberNotFound
			}
			return nil, err
		}
		if current.MergedIntoMemberID == nil || current.Status != MemberStatusMerged {
			return &current, nil
		}
		if _, ok := seen[current.ID]; ok {
			return nil, ErrMemberMergedCycle
		}
		seen[current.ID] = struct{}{}
		memberID = *current.MergedIntoMemberID
	}
	return nil, ErrMemberMergedCycle
}

func (s *MemberService) appendEventTx(tx *gorm.DB, memberID, tenantID uint, eventType string, metadata memberEventMetadata, idempotency ...string) error {
	key := ""
	if len(idempotency) != 0 {
		key = strings.TrimSpace(idempotency[0])
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if key != "" {
		var existing model.TenantMemberEvent
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).First(&existing).Error; err == nil {
			if existing.MemberID != memberID || existing.EventType != eventType || existing.MetadataJSON != string(encoded) {
				return ErrMemberIdempotencyConflict
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	row := model.TenantMemberEvent{TenantID: tenantID, MemberID: memberID, EventType: eventType, MetadataJSON: string(encoded), IdempotencyKey: key, OccurredAt: s.now()}
	if key == "" {
		return tx.Create(&row).Error
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var existing model.TenantMemberEvent
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).First(&existing).Error; err != nil {
			return err
		}
		if existing.MemberID != memberID || existing.EventType != eventType || existing.MetadataJSON != string(encoded) {
			return ErrMemberIdempotencyConflict
		}
	}
	return nil
}

func (s *MemberService) identityBlindIndex(tenantID, channelID uint, provider, subject string) string {
	return s.hmac(fmt.Sprintf("identity:%d:%d:%s:%s", tenantID, channelID, provider, subject))
}

func (s *MemberService) contactBlindIndex(tenantID uint, contactType, value string) string {
	return s.hmac(fmt.Sprintf("contact:%d:%s:%s", tenantID, contactType, value))
}

func (s *MemberService) hmac(value string) string {
	h := hmac.New(sha256.New, s.blindIndexKey)
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

var phoneNonDigits = regexp.MustCompile(`[^0-9+]`)

func normalizePhone(value string) (string, error) {
	value = phoneNonDigits.ReplaceAllString(strings.TrimSpace(value), "")
	if strings.HasPrefix(value, "+86") {
		value = value[3:]
	}
	if strings.HasPrefix(value, "0086") {
		value = value[4:]
	}
	if len(value) < 7 || len(value) > 15 || strings.Contains(value[1:], "+") {
		return "", ErrMemberInvalidInput
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", ErrMemberInvalidInput
		}
	}
	return value, nil
}

func newMemberNo() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("M%d", time.Now().UnixNano())
	}
	return "M" + strings.ToUpper(hex.EncodeToString(buf))
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "duplicate key") || strings.Contains(s, "unique constraint") || strings.Contains(s, "uniqueindex")
}
