package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func ensureMemberServiceSchema(t *testing.T) {
	t.Helper()
	if err := model.DB.AutoMigrate(
		&model.TenantMember{},
		&model.TenantMemberIdentity{},
		&model.TenantMemberVerifiedContact{},
		&model.TenantMemberConsent{},
		&model.TenantMemberEvent{},
	); err != nil {
		t.Fatalf("migrate member schema: %v", err)
	}
}

func newMemberServiceTenant(t *testing.T) uint {
	t.Helper()
	tenant := model.Tenant{
		Name:       fmt.Sprintf("Member tenant %d", time.Now().UnixNano()),
		SystemCode: fmt.Sprintf("MEM-%d", time.Now().UnixNano()),
		SecretKey:  "member-test-secret",
		Status:     "active",
	}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatalf("create member tenant: %v", err)
	}
	return tenant.ID
}

func newMemberServiceForTest(t *testing.T) *MemberService {
	t.Helper()
	service, err := NewMemberServiceForTest(model.DB, []byte("member-test-blind-index-key"), func(value string) (string, error) {
		return "cipher:" + value, nil
	}, func() time.Time {
		return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("new member service: %v", err)
	}
	return service
}

func TestMemberServiceSelfHostedIdentityIsTenantScopedAndIdempotent(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantA, tenantB := newMemberServiceTenant(t), newMemberServiceTenant(t)
	input := SelfHostedIdentityInput{TenantID: tenantA, ChannelAccountID: 10, Provider: "wechat_miniapp", Subject: "wx-user-1", DisplayName: "用户"}
	first, err := service.ResolveSelfHostedIdentity(input)
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	retry, err := service.ResolveSelfHostedIdentity(input)
	if err != nil || retry.ID != first.ID {
		t.Fatalf("login was not idempotent: member=%+v err=%v", retry, err)
	}
	foreign, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantB, ChannelAccountID: 10, Provider: input.Provider, Subject: input.Subject})
	if err != nil {
		t.Fatalf("same subject in another tenant should be independent: %v", err)
	}
	if foreign.ID == first.ID {
		t.Fatal("same channel subject crossed tenant boundary")
	}
	if _, err := service.ResolveCanonical(tenantB, first.ID); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("cross-tenant canonical lookup err=%v", err)
	}
	if _, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantA, ChannelAccountID: 10, Provider: "ctrip", Subject: "external-user"}); !errors.Is(err, ErrMemberExternalEntry) {
		t.Fatalf("external provider was accepted: %v", err)
	}
}

func TestMemberServiceConcurrentFirstLoginConvergesToOneIdentity(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	input := SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 11, Provider: "xiaohongshu_miniapp", Subject: "xhs-user-race"}
	const workers = 6
	results := make(chan *model.TenantMember, workers)
	errorsCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			member, err := service.ResolveSelfHostedIdentity(input)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- member
		}()
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent first login failed: %v", err)
	}
	var memberIDs []uint
	for member := range results {
		memberIDs = append(memberIDs, member.ID)
	}
	if len(memberIDs) != workers {
		t.Fatalf("got %d successful logins, want %d", len(memberIDs), workers)
	}
	for _, id := range memberIDs[1:] {
		if id != memberIDs[0] {
			t.Fatalf("concurrent login created multiple members: %v", memberIDs)
		}
	}
	var identityCount int64
	if err := model.DB.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ? AND provider = ? AND channel_account_id = ?", tenantID, input.Provider, input.ChannelAccountID).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identity: %v", err)
	}
	if identityCount != 1 {
		t.Fatalf("concurrent login created %d identities", identityCount)
	}
}

func TestMemberServiceTrustedPhoneConflictAndConsent(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	first, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 12, Provider: "web", Subject: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 13, Provider: "app", Subject: "app-1"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.VerifyTrustedPhone(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: first.ID, Phone: "+86 138-0013-8000", VerificationMethod: "sms", MembershipConsentGranted: false})
	if err != nil {
		t.Fatalf("verify phone without membership consent: %v", err)
	}
	if updated.MembershipStatus != MembershipStatusProvisional {
		t.Fatalf("phone verification implicitly granted membership: %q", updated.MembershipStatus)
	}
	if _, err := service.VerifyTrustedPhone(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: second.ID, Phone: "13800138000", VerificationMethod: "sms", MembershipConsentGranted: true}); !errors.Is(err, ErrMemberPhoneConflict) {
		t.Fatalf("duplicate verified phone was accepted: %v", err)
	}
	if err := service.AppendConsent(ConsentInput{TenantID: tenantID, MemberID: first.ID, Purpose: "membership", PolicyVersion: "v1", Decision: "grant", EvidenceMethod: "checkbox", IdempotencyKey: "membership-grant-1"}); err != nil {
		t.Fatalf("grant membership consent: %v", err)
	}
	if err := service.AppendConsent(ConsentInput{TenantID: tenantID, MemberID: first.ID, Purpose: "membership", PolicyVersion: "v1", Decision: "grant", EvidenceMethod: "checkbox", IdempotencyKey: "membership-grant-1"}); err != nil {
		t.Fatalf("idempotent consent retry: %v", err)
	}
	if err := service.AppendConsent(ConsentInput{TenantID: tenantID, MemberID: first.ID, Purpose: "marketing", PolicyVersion: "v1", Decision: "grant", EvidenceMethod: "checkbox", IdempotencyKey: "membership-grant-1"}); !errors.Is(err, ErrMemberIdempotencyConflict) {
		t.Fatalf("consent idempotency key payload reuse was accepted: %v", err)
	}
	if err := service.AppendConsent(ConsentInput{TenantID: tenantID + 1, MemberID: first.ID, Purpose: "membership", PolicyVersion: "v1", Decision: "grant", EvidenceMethod: "checkbox", IdempotencyKey: "membership-grant-1"}); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("cross-tenant consent unexpectedly succeeded: %v", err)
	}
	var consentCount int64
	if err := model.DB.Model(&model.TenantMemberConsent{}).Where("tenant_id = ? AND member_id = ? AND idempotency_key = ?", tenantID, first.ID, "membership-grant-1").Count(&consentCount).Error; err != nil {
		t.Fatal(err)
	}
	if consentCount != 1 {
		t.Fatalf("consent retry created %d rows", consentCount)
	}
}

func TestMemberServiceTrustedPhoneWithConsentDoesNotLeaveConsentOnPhoneConflict(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	owner, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 18, Provider: "web", Subject: "phone-owner"})
	if err != nil {
		t.Fatal(err)
	}
	claimant, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 19, Provider: "app", Subject: "phone-claimant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyTrustedPhone(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: owner.ID, Phone: "13800138000", VerificationMethod: "wechat_phone_authorization"}); err != nil {
		t.Fatalf("owner phone verification: %v", err)
	}
	_, err = service.VerifyTrustedPhoneWithConsent(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: claimant.ID, Phone: "13800138000", VerificationMethod: "wechat_phone_authorization", MembershipConsentGranted: true, IdempotencyKey: "phone-conflict", ChannelAccountID: 19}, ConsentInput{TenantID: tenantID, MemberID: claimant.ID, Purpose: "membership", PolicyVersion: "member-phone-v1", Decision: "grant", ChannelAccountID: 19, EvidenceMethod: "wechat_phone_authorization", IdempotencyKey: "phone-conflict:membership"})
	if !errors.Is(err, ErrMemberPhoneConflict) {
		t.Fatalf("phone conflict error=%v", err)
	}
	var consentCount int64
	if err := model.DB.Model(&model.TenantMemberConsent{}).Where("tenant_id = ? AND member_id = ? AND idempotency_key = ?", tenantID, claimant.ID, "phone-conflict:membership").Count(&consentCount).Error; err != nil {
		t.Fatal(err)
	}
	if consentCount != 0 {
		t.Fatalf("phone conflict left %d consent rows", consentCount)
	}
	var reloaded model.TenantMember
	if err := model.DB.Where("id = ? AND tenant_id = ?", claimant.ID, tenantID).First(&reloaded).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.MembershipStatus != MembershipStatusProvisional {
		t.Fatalf("phone conflict changed membership status=%q", reloaded.MembershipStatus)
	}
}

func TestMemberServicePhoneVerificationBindsRequestToPhoneAndBoundsConsentKey(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	member, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 31, Provider: "wechat_miniapp", Subject: "long-request-user"})
	if err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("r", 160)
	input := StorefrontPhoneVerificationInput{RequestID: requestID, MembershipConsentGranted: true, MembershipPolicyVersion: "member-phone-v1"}
	if _, err := verifyStorefrontMemberPhone(context.Background(), service, tenantID, 31, member.ID, "13800138000", "wechat_phone_authorization", input); err != nil {
		t.Fatalf("first phone verification: %v", err)
	}
	var consent model.TenantMemberConsent
	if err := model.DB.Where("tenant_id = ? AND member_id = ?", tenantID, member.ID).First(&consent).Error; err != nil {
		t.Fatal(err)
	}
	if len(consent.IdempotencyKey) > 160 {
		t.Fatalf("consent idempotency key length=%d", len(consent.IdempotencyKey))
	}
	if _, err := verifyStorefrontMemberPhone(context.Background(), service, tenantID, 31, member.ID, "13900139000", "wechat_phone_authorization", input); !errors.Is(err, ErrMemberIdempotencyConflict) {
		t.Fatalf("request id reused for a different phone error=%v, want idempotency conflict", err)
	}
	var contactCount int64
	if err := model.DB.Model(&model.TenantMemberVerifiedContact{}).Where("tenant_id = ? AND member_id = ? AND status = ?", tenantID, member.ID, MemberContactActive).Count(&contactCount).Error; err != nil {
		t.Fatal(err)
	}
	if contactCount != 1 {
		t.Fatalf("replayed request created %d active contacts", contactCount)
	}
}

func TestMemberPhoneMaskCoversShortestAcceptedNumber(t *testing.T) {
	if got := maskMemberPhone("1234567"); got != "123*567" {
		t.Fatalf("maskMemberPhone(7 digits)=%q, want 123*567", got)
	}
}

func TestMemberServiceAutoConvergeAndCanonicalCycleProtection(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	current, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 14, Provider: "web", Subject: "provisional"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 15, Provider: "app", Subject: "canonical"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyTrustedPhone(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: target.ID, Phone: "13900139000", VerificationMethod: "sms", MembershipConsentGranted: true}); err != nil {
		t.Fatal(err)
	}
	canonical, err := service.AutoConverge(AutoConvergeInput{TenantID: tenantID, CurrentMemberID: current.ID, TargetMemberID: target.ID, TrustedPhone: "13900139000", IdempotencyKey: "merge-1"})
	if err != nil || canonical.ID != target.ID {
		t.Fatalf("auto converge result=%+v err=%v", canonical, err)
	}
	retried, err := service.AutoConverge(AutoConvergeInput{TenantID: tenantID, CurrentMemberID: current.ID, TargetMemberID: target.ID, TrustedPhone: "13900139000", IdempotencyKey: "merge-1"})
	if err != nil || retried.ID != target.ID {
		t.Fatalf("auto converge retry was not idempotent: %+v err=%v", retried, err)
	}
	resolved, err := service.ResolveCanonical(tenantID, current.ID)
	if err != nil || resolved.ID != target.ID {
		t.Fatalf("merged member did not resolve to canonical: %+v err=%v", resolved, err)
	}
	detail, err := service.GetMember(context.Background(), tenantID, current.ID)
	if err != nil {
		t.Fatalf("merged member detail lookup failed: %v", err)
	}
	if detail.ID != target.ID || detail.SourceCount != 2 || len(detail.Identities) != 2 {
		t.Fatalf("merged member detail did not aggregate canonical graph: %+v identities=%d", detail, len(detail.Identities))
	}
	if _, err := service.AutoConverge(AutoConvergeInput{TenantID: tenantID, CurrentMemberID: target.ID, TargetMemberID: current.ID, TrustedPhone: "13900139000", IdempotencyKey: "merge-cycle"}); !errors.Is(err, ErrMemberLifecycle) {
		t.Fatalf("merged target was accepted for another convergence: %v", err)
	}
	cycleTarget := current.ID
	if err := model.DB.Model(&model.TenantMember{}).Where("id = ? AND tenant_id = ?", target.ID, tenantID).Updates(map[string]interface{}{"status": MemberStatusMerged, "merged_into_member_id": cycleTarget}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveCanonical(tenantID, current.ID); !errors.Is(err, ErrMemberMergedCycle) {
		t.Fatalf("merge cycle was not rejected: %v", err)
	}
}

func TestMemberServiceRevokeProtectsLastIdentityPerMember(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	member, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 16, Provider: "web", Subject: "only-identity"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 17, Provider: "web", Subject: "other-member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeIdentity(RevokeIdentityInput{TenantID: tenantID, IdentityID: mustMemberIdentityID(t, member.ID), StronglyAuthn: true, IdempotencyKey: "revoke-last"}); !errors.Is(err, ErrMemberLastIdentity) {
		t.Fatalf("last identity revocation was allowed with another member present: %v", err)
	}
	var secondIdentity = model.TenantMemberIdentity{TenantID: tenantID, MemberID: member.ID, Provider: "app", ChannelAccountID: 18, SubjectBlindIndex: service.identityBlindIndex(tenantID, 18, "app", "recovery"), BlindIndexVersion: 1, Status: MemberIdentityActive, LinkMethod: "controlled_recovery", LinkedAt: service.now(), LastSeenAt: service.now()}
	if err := model.DB.Create(&secondIdentity).Error; err != nil {
		t.Fatalf("create recovery identity: %v", err)
	}
	if err := service.RevokeIdentity(RevokeIdentityInput{TenantID: tenantID, IdentityID: mustMemberIdentityID(t, member.ID), StronglyAuthn: true, IdempotencyKey: "revoke-first"}); err != nil {
		t.Fatalf("revoke one of two identities: %v", err)
	}
	if other.ID == member.ID {
		t.Fatal("test members unexpectedly share identity")
	}
}

func TestMemberServiceDoesNotReactivateRevokedIdentityOnLogin(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	input := SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 22001, Provider: "wechat_miniapp", Subject: "revoked-login"}
	member, err := service.ResolveSelfHostedIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	identityID := mustMemberIdentityID(t, member.ID)
	// A member needs another recoverable identity before the first one can be
	// revoked; this mirrors the production last-identity guard.
	second := model.TenantMemberIdentity{
		TenantID: tenantID, MemberID: member.ID, Provider: "app", ChannelAccountID: 22002,
		SubjectBlindIndex: service.identityBlindIndex(tenantID, 22002, "app", "recovery"), BlindIndexVersion: 1,
		Status: MemberIdentityActive, LinkMethod: "controlled_recovery", LinkedAt: service.now(), LastSeenAt: service.now(),
	}
	if err := model.DB.Create(&second).Error; err != nil {
		t.Fatalf("create recovery identity: %v", err)
	}
	if err := service.RevokeIdentity(RevokeIdentityInput{TenantID: tenantID, IdentityID: identityID, StronglyAuthn: true, IdempotencyKey: "revoke-login-identity"}); err != nil {
		t.Fatalf("revoke identity: %v", err)
	}
	if _, err := service.ResolveSelfHostedIdentity(input); !errors.Is(err, ErrMemberLifecycle) {
		t.Fatalf("revoked identity was reactivated on login: %v", err)
	}
	var identity model.TenantMemberIdentity
	if err := model.DB.Where("id = ? AND tenant_id = ?", identityID, tenantID).First(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if identity.Status != MemberIdentityRevoked || identity.RevokedAt == nil {
		t.Fatalf("revoked identity changed after login: %+v", identity)
	}
}

func mustMemberIdentityID(t *testing.T, memberID uint) uint {
	t.Helper()
	var identity model.TenantMemberIdentity
	if err := model.DB.Where("member_id = ? AND status = ?", memberID, MemberIdentityActive).Order("id ASC").First(&identity).Error; err != nil {
		t.Fatalf("load member identity: %v", err)
	}
	return identity.ID
}

func TestMemberServiceEventsDoNotPersistRawSecrets(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	member, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 19, Provider: "wechat_miniapp", Subject: "secret-subject"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyTrustedPhone(TrustedPhoneVerificationInput{TenantID: tenantID, MemberID: member.ID, Phone: "13700137000", VerificationMethod: "sms", MembershipConsentGranted: true}); err != nil {
		t.Fatal(err)
	}
	var events []model.TenantMemberEvent
	if err := model.DB.Where("tenant_id = ? AND member_id = ?", tenantID, member.ID).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(event.MetadataJSON, "secret-subject") || strings.Contains(event.MetadataJSON, "13700137000") {
			t.Fatalf("event persisted raw secret: %s", event.MetadataJSON)
		}
	}
}

func TestMemberServiceEventIdempotencyIsTenantScopedAndPayloadBound(t *testing.T) {
	ensureMemberServiceSchema(t)
	service := newMemberServiceForTest(t)
	tenantID := newMemberServiceTenant(t)
	first, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 20, Provider: "web", Subject: "event-owner-1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenantID, ChannelAccountID: 21, Provider: "web", Subject: "event-owner-2"})
	if err != nil {
		t.Fatal(err)
	}
	metadata := memberEventMetadata{Method: "strong_auth"}
	appendEvent := func(memberID uint, eventType string, eventMetadata memberEventMetadata) error {
		return model.DB.Transaction(func(tx *gorm.DB) error {
			return service.appendEventTx(tx, memberID, tenantID, eventType, eventMetadata, "event-idempotency-1")
		})
	}
	if err := appendEvent(first.ID, "member.test", metadata); err != nil {
		t.Fatalf("first event append: %v", err)
	}
	if err := appendEvent(first.ID, "member.test", metadata); err != nil {
		t.Fatalf("same event retry was not idempotent: %v", err)
	}
	if err := appendEvent(second.ID, "member.test", metadata); !errors.Is(err, ErrMemberIdempotencyConflict) {
		t.Fatalf("same tenant key on another member was not rejected: %v", err)
	}
	if err := appendEvent(first.ID, "member.other", metadata); !errors.Is(err, ErrMemberIdempotencyConflict) {
		t.Fatalf("same key with another event type was not rejected: %v", err)
	}
	if err := appendEvent(first.ID, "member.test", memberEventMetadata{Method: "different"}); !errors.Is(err, ErrMemberIdempotencyConflict) {
		t.Fatalf("same key with another payload was not rejected: %v", err)
	}
	var count int64
	if err := model.DB.Model(&model.TenantMemberEvent{}).Where("tenant_id = ? AND idempotency_key = ?", tenantID, "event-idempotency-1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent event retry created %d rows", count)
	}
}
