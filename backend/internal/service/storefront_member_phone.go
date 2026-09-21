package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"ticket-backend/internal/model"
)

// StorefrontPhoneVerificationInput contains only user-visible consent
// metadata and a caller-generated idempotency key. Provider credentials and
// phone values are supplied by the channel adapter after server-side
// verification.
type StorefrontPhoneVerificationInput struct {
	RequestID                string `json:"request_id"`
	MembershipConsentGranted bool   `json:"membership_consent_granted"`
	MembershipPolicyVersion  string `json:"membership_policy_version"`
}

type StorefrontPhoneVerificationResult struct {
	Verified         bool   `json:"verified"`
	PhoneMasked      string `json:"phone_masked"`
	MembershipStatus string `json:"membership_status"`
}

var (
	ErrStorefrontPhoneUnavailable = errors.New("storefront phone verification is unavailable")
	ErrStorefrontPhoneInvalid     = errors.New("storefront phone verification request is invalid")
)

// verifyStorefrontMemberPhone is the shared post-provider boundary. It is
// intentionally the only place where a channel adapter's platform evidence
// becomes a tenant member contact.
func verifyStorefrontMemberPhone(ctx context.Context, members *MemberService, tenantID, channelAccountID, memberID uint, phone, method string, input StorefrontPhoneVerificationInput) (StorefrontPhoneVerificationResult, error) {
	_ = ctx
	if members == nil || tenantID == 0 || channelAccountID == 0 || memberID == 0 {
		return StorefrontPhoneVerificationResult{}, ErrStorefrontPhoneUnavailable
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.RequestID == "" || len(input.RequestID) > 160 {
		return StorefrontPhoneVerificationResult{}, ErrStorefrontPhoneInvalid
	}
	input.MembershipPolicyVersion = strings.TrimSpace(input.MembershipPolicyVersion)
	if input.MembershipConsentGranted && input.MembershipPolicyVersion == "" {
		return StorefrontPhoneVerificationResult{}, ErrMemberConsentRequired
	}

	phoneInput := TrustedPhoneVerificationInput{
		TenantID: tenantID, MemberID: memberID, Phone: phone,
		VerificationMethod: method, MembershipConsentGranted: input.MembershipConsentGranted,
		IdempotencyKey: input.RequestID, ChannelAccountID: channelAccountID,
	}
	var member *model.TenantMember
	var err error
	if input.MembershipConsentGranted {
		member, err = members.VerifyTrustedPhoneWithConsent(phoneInput, ConsentInput{
			TenantID: tenantID, MemberID: memberID, Purpose: "membership",
			PolicyVersion: input.MembershipPolicyVersion, Decision: "grant",
			ChannelAccountID: channelAccountID, EvidenceMethod: method,
			IdempotencyKey: membershipConsentIdempotencyKey(input.RequestID),
		})
	} else {
		member, err = members.VerifyTrustedPhone(phoneInput)
	}
	if err != nil {
		return StorefrontPhoneVerificationResult{}, err
	}
	if member == nil {
		return StorefrontPhoneVerificationResult{}, ErrStorefrontPhoneUnavailable
	}
	normalized, err := normalizePhone(phone)
	if err != nil {
		return StorefrontPhoneVerificationResult{}, err
	}
	return StorefrontPhoneVerificationResult{
		Verified: true, PhoneMasked: maskMemberPhone(normalized), MembershipStatus: member.MembershipStatus,
	}, nil
}

// membershipConsentIdempotencyKey keeps the derived database key within the
// schema's 160-byte limit even when a caller supplies the maximum request ID.
// Hashing preserves collision resistance without storing provider credentials
// or user data in the key.
func membershipConsentIdempotencyKey(requestID string) string {
	const suffix = ":membership"
	if len(requestID)+len(suffix) <= 160 {
		return requestID + suffix
	}
	digest := sha256.Sum256([]byte(requestID))
	return "member-consent:" + hex.EncodeToString(digest[:])
}
