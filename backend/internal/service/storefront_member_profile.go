package service

import (
	"context"
	"errors"
)

// ErrStorefrontMemberUnavailable means that the authenticated storefront
// session predates or cannot safely resolve the additive member association.
// The legacy storefront transaction flow remains independent; clients can
// refresh their session without being given a guessed member identity.
var ErrStorefrontMemberUnavailable = errors.New("storefront member profile is unavailable")

// GetMemberProfile returns the canonical member associated with a WeChat
// commercial storefront session. The bearer token is the only client input;
// tenant, channel account and member are resolved server-side.
func (s *CommerceStorefrontService) GetMemberProfile(ctx context.Context, token string) (MemberSelfProfile, error) {
	storefront, err := s.authenticate(token)
	if err != nil {
		return MemberSelfProfile{}, err
	}
	if storefront.MemberID == nil || *storefront.MemberID == 0 || s.Member == nil {
		return MemberSelfProfile{}, ErrStorefrontMemberUnavailable
	}
	return s.Member.GetSelfProfile(ctx, storefront.Session.TenantID, *storefront.MemberID)
}

// GetXiaohongshuMemberProfile returns the canonical member associated with an
// authenticated self-owned Xiaohongshu miniapp session. Provider identifiers
// and internal member IDs never cross this public projection.
func (s MiniappService) GetXiaohongshuMemberProfile(ctx context.Context, token string) (MemberSelfProfile, error) {
	customer, err := s.Authenticate(token)
	if err != nil {
		return MemberSelfProfile{}, err
	}
	if customer.MemberID == nil || *customer.MemberID == 0 || s.Member == nil {
		return MemberSelfProfile{}, ErrStorefrontMemberUnavailable
	}
	return s.Member.GetSelfProfile(ctx, customer.TenantID, *customer.MemberID)
}
