package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMemberCouponGrantScopeDenied = errors.New("member coupon grant scope denied")
	ErrMemberCouponGrantInvalid     = errors.New("invalid member coupon grant request")
	ErrMemberCouponGrantEligibility = errors.New("member coupon grant recipient is ineligible")
	ErrMemberCouponGrantConflict    = errors.New("member coupon grant idempotency conflict")
	ErrMemberCouponGrantUnavailable = errors.New("member coupon grant unavailable")
)

type MemberCouponGrantService struct {
	DB     *gorm.DB
	Member *MemberService
	Clock  func() time.Time
}
type MemberCouponGrantIssueInput struct {
	TemplateID     uint   `json:"template_id"`
	MemberIDs      []uint `json:"member_ids"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
	ActorUserID    uint   `json:"-"`
	ActorRole      string `json:"-"`
}
type MemberCouponGrantRecipient struct {
	MemberID    uint   `json:"member_id"`
	MemberNo    string `json:"member_no"`
	DisplayName string `json:"display_name"`
	Eligible    bool   `json:"eligible"`
	Reason      string `json:"reason"`
}
type MemberCouponGrantPreviewResult struct {
	Eligible    bool                          `json:"eligible"`
	Reason      string                        `json:"reason"`
	Template    *model.CommerceCouponTemplate `json:"template"`
	MemberCount int                           `json:"member_count"`
	Recipients  []MemberCouponGrantRecipient  `json:"recipients"`
}
type MemberCouponGrantItem struct {
	MemberID    uint   `json:"member_id"`
	MemberNo    string `json:"member_no"`
	DisplayName string `json:"display_name"`
	GrantID     uint   `json:"grant_id"`
}
type MemberCouponGrantResult struct {
	Status           string                  `json:"status"`
	IdempotencyKey   string                  `json:"idempotency_key"`
	TemplateID       uint                    `json:"template_id"`
	ChannelAccountID uint                    `json:"channel_account_id"`
	MemberCount      int                     `json:"member_count"`
	Items            []MemberCouponGrantItem `json:"items"`
	CreatedAt        time.Time               `json:"created_at"`
}

func (s *MemberCouponGrantService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}
func (s *MemberCouponGrantService) available() bool {
	return s != nil && s.Member != nil && s.Member.db != nil && s.db() != nil
}
func (s *MemberCouponGrantService) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}
func normalizeMemberCouponIDs(ids []uint) ([]uint, error) {
	if len(ids) < 1 || len(ids) > 100 {
		return nil, ErrMemberCouponGrantInvalid
	}
	out := append([]uint(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for i, id := range out {
		if id == 0 || i > 0 && out[i-1] == id {
			return nil, fmt.Errorf("%w: duplicate or empty member", ErrMemberCouponGrantInvalid)
		}
	}
	return out, nil
}
func memberCouponRequestHash(input MemberCouponGrantIssueInput) string {
	payload, _ := json.Marshal(struct {
		Template uint
		Members  []uint
		Reason   string
	}{input.TemplateID, input.MemberIDs, input.Reason})
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
func (s *MemberCouponGrantService) Get(ctx context.Context, tenantID uint, key string) (*MemberCouponGrantResult, error) {
	if !s.available() {
		return nil, ErrMemberCouponGrantUnavailable
	}
	key = strings.TrimSpace(key)
	if tenantID == 0 || key == "" || len(key) > 160 {
		return nil, ErrMemberCouponGrantInvalid
	}
	return memberCouponResultTx(s.db().WithContext(ctx), tenantID, key)
}
func memberCouponResultTx(tx *gorm.DB, tenantID uint, key string) (*MemberCouponGrantResult, error) {
	var batch model.MemberCouponGrantBatch
	if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).First(&batch).Error; err != nil {
		return nil, err
	}
	var items []model.MemberCouponGrantBatchItem
	if err := tx.Where("tenant_id = ? AND batch_id = ?", tenantID, batch.ID).Order("member_id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	result := &MemberCouponGrantResult{Status: batch.Status, IdempotencyKey: key, TemplateID: batch.TemplateID, ChannelAccountID: batch.ChannelAccountID, MemberCount: batch.MemberCount, CreatedAt: batch.CreatedAt, Items: make([]MemberCouponGrantItem, 0, len(items))}
	for _, item := range items {
		result.Items = append(result.Items, MemberCouponGrantItem{item.MemberID, item.MemberNo, item.DisplayName, item.GrantID})
	}
	return result, nil
}
func (s *MemberCouponGrantService) Preview(ctx context.Context, tenantID, templateID uint, memberIDs []uint) (*MemberCouponGrantPreviewResult, error) {
	if !s.available() {
		return nil, ErrMemberCouponGrantUnavailable
	}
	ids, err := normalizeMemberCouponIDs(memberIDs)
	if err != nil || tenantID == 0 || templateID == 0 {
		return nil, ErrMemberCouponGrantInvalid
	}
	var result *MemberCouponGrantPreviewResult
	err = s.db().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		result, _, e = s.prepareTx(tx, tenantID, templateID, ids, false)
		return e
	})
	return result, err
}

// prepareTx reads the graph before locking, then follows the identity service's
// account -> identities -> ascending members -> template order. Re-reading after
// member locks rejects graph/identity drift instead of guessing a wallet.
func (s *MemberCouponGrantService) prepareTx(tx *gorm.DB, tenantID, templateID uint, ids []uint, lock bool) (*MemberCouponGrantPreviewResult, map[uint]string, error) {
	result := &MemberCouponGrantPreviewResult{Eligible: true, MemberCount: len(ids), Recipients: make([]MemberCouponGrantRecipient, 0, len(ids))}
	wallets := map[uint]string{}
	var template model.CommerceCouponTemplate
	if err := tx.Where("tenant_id = ? AND id = ?", tenantID, templateID).First(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrMemberCouponGrantScopeDenied
		}
		return nil, nil, err
	}
	var account model.ChannelAccount
	q := tx
	if lock {
		q = q.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := q.Where("tenant_id = ? AND id = ?", tenantID, template.ChannelAccountID).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrMemberCouponGrantScopeDenied
		}
		return nil, nil, err
	}
	if account.Type != "wechat_miniapp" || (account.Status != "active" && account.Status != "sandbox") || !ChannelAllowsMemberIdentity(&account) {
		return nil, nil, fmt.Errorf("%w: 店铺账号未启用自营微信会员发券", ErrMemberCouponGrantEligibility)
	}
	var tenant model.Tenant
	if err := tx.Select("id").Where("id = ? AND status = ?", tenantID, "active").First(&tenant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrMemberCouponGrantScopeDenied
		}
		return nil, nil, err
	}
	graphs := map[uint][]uint{}
	all := []uint{}
	seen := map[uint]bool{}
	for _, id := range ids {
		graph, err := s.Member.memberGraphIDsTx(tx, tenantID, id)
		if err != nil {
			return nil, nil, err
		}
		sort.Slice(graph, func(i, j int) bool { return graph[i] < graph[j] })
		graphs[id] = graph
		for _, mid := range graph {
			if !seen[mid] {
				seen[mid] = true
				all = append(all, mid)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	var identities []model.TenantMemberIdentity
	q = tx
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Unscoped().Where("tenant_id = ? AND channel_account_id = ? AND member_id IN ?", tenantID, account.ID, all).Order("id ASC").Find(&identities).Error; err != nil {
		return nil, nil, err
	}
	var members []model.TenantMember
	q = tx
	// Existing canonical resolution can lock an alias before its older root.
	// Do not wait while holding identity locks: abort safely on contention
	// instead of introducing an alias/root lock inversion.
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"})
	}
	if err := q.Where("tenant_id = ? AND id IN ?", tenantID, all).Order("id ASC").Find(&members).Error; err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "55P03" {
			return nil, nil, fmt.Errorf("%w: member is changing; retry", ErrMemberCouponGrantEligibility)
		}
		return nil, nil, err
	}
	memberByID := map[uint]model.TenantMember{}
	for _, member := range members {
		memberByID[member.ID] = member
	}
	if lock {
		for _, id := range ids {
			fresh, err := s.Member.memberGraphIDsTx(tx, tenantID, id)
			if err != nil {
				return nil, nil, err
			}
			sort.Slice(fresh, func(i, j int) bool { return fresh[i] < fresh[j] })
			if !reflect.DeepEqual(fresh, graphs[id]) {
				return nil, nil, fmt.Errorf("%w: membership changed; retry", ErrMemberCouponGrantEligibility)
			}
		}
		var fresh []model.TenantMemberIdentity
		if err := tx.Unscoped().Where("tenant_id = ? AND channel_account_id = ? AND member_id IN ?", tenantID, account.ID, all).Order("id ASC").Find(&fresh).Error; err != nil {
			return nil, nil, err
		}
		if !reflect.DeepEqual(identities, fresh) {
			return nil, nil, fmt.Errorf("%w: identity changed; retry", ErrMemberCouponGrantEligibility)
		}
	}
	q = tx
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Where("tenant_id = ? AND id = ? AND channel_account_id = ?", tenantID, templateID, account.ID).First(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrMemberCouponGrantScopeDenied
		}
		return nil, nil, err
	}
	types, err := promotionBusinessTypesForTemplate(tx, &template)
	if err != nil {
		return nil, nil, err
	}
	template.BusinessTypes = types
	result.Template = &template
	promo := &CommercePromotionService{}
	if err := promo.checkBusinessTypes(tx, tenantID, account.ID, types); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrMemberCouponGrantScopeDenied
		}
		return nil, nil, err
	}
	now := s.now()
	if template.Status != "active" || template.StartsAt != nil && now.Before(*template.StartsAt) || template.EndsAt != nil && !now.Before(*template.EndsAt) {
		result.Eligible = false
		result.Reason = "优惠券模板未生效或已停用"
	}
	var issued int64
	if template.IssuanceCap > 0 {
		if err := tx.Model(&model.CommerceCouponGrant{}).Where("tenant_id = ? AND template_id = ? AND status <> ?", tenantID, templateID, "cancelled").Count(&issued).Error; err != nil {
			return nil, nil, err
		}
		if issued+int64(len(ids)) > int64(template.IssuanceCap) {
			result.Eligible = false
			result.Reason = "优惠券剩余发放额度不足"
		}
	}
	usedWallet := map[string]bool{}
	for _, id := range ids {
		recipient := MemberCouponGrantRecipient{MemberID: id, Eligible: true}
		member, ok := memberByID[id]
		recipient.MemberNo = member.MemberNo
		recipient.DisplayName = member.DisplayName
		deny := func(reason string) { recipient.Eligible = false; recipient.Reason = reason }
		if !ok || member.Status != model.TenantMemberStatusActive || member.MembershipStatus != model.TenantMembershipStatusActive || member.MergedIntoMemberID != nil {
			deny("仅可向本租户状态正常的正式会员发券")
		}
		inGraph := map[uint]bool{}
		for _, mid := range graphs[id] {
			inGraph[mid] = true
		}
		matches := []model.TenantMemberIdentity{}
		for _, identity := range identities {
			if inGraph[identity.MemberID] {
				matches = append(matches, identity)
			}
		}
		if recipient.Eligible && (len(matches) != 1 || matches[0].Provider != "wechat_miniapp" || matches[0].Status != model.TenantMemberIdentityStatusActive || matches[0].DeletedAt.Valid) {
			deny("该店铺的微信会员身份缺失、撤销或不唯一")
		}
		if recipient.Eligible {
			var hashes []string
			// Session expiry/token rotation does not revoke the member identity. Empty
			// historical subjects are unsafe; no phone-based or cross-account fallback.
			if err := tx.Unscoped().Model(&model.CommerceCustomerSession{}).Distinct("subject_hash").Where("tenant_id = ? AND channel_account_id = ? AND member_id IN ?", tenantID, account.ID, graphs[id]).Pluck("subject_hash", &hashes).Error; err != nil {
				return nil, nil, err
			}
			if len(hashes) != 1 || strings.TrimSpace(hashes[0]) == "" {
				deny("无法唯一确定该店铺的历史微信券包")
			} else {
				customer := storefrontCustomerID(account.ID, hashes[0])
				wallets[id] = customer
				if usedWallet[customer] {
					deny("选择的会员对应重复券包")
				} else {
					usedWallet[customer] = true
				}
				var count int64
				if err := tx.Model(&model.CommerceCouponGrant{}).Where("tenant_id = ? AND template_id = ? AND customer_id = ? AND status <> ?", tenantID, templateID, customer, "cancelled").Count(&count).Error; err != nil {
					return nil, nil, err
				}
				if template.PerCustomerCap > 0 && count >= int64(template.PerCustomerCap) {
					deny("该客户已达到此优惠券领取上限")
				}
			}
		}
		if !recipient.Eligible {
			result.Eligible = false
			if result.Reason == "" {
				result.Reason = "部分会员不符合发券条件"
			}
		}
		result.Recipients = append(result.Recipients, recipient)
	}
	return result, wallets, nil
}

func (s *MemberCouponGrantService) Issue(ctx context.Context, tenantID uint, input MemberCouponGrantIssueInput) (*MemberCouponGrantResult, error) {
	if !s.available() {
		return nil, ErrMemberCouponGrantUnavailable
	}
	ids, err := normalizeMemberCouponIDs(input.MemberIDs)
	if err != nil {
		return nil, err
	}
	input.MemberIDs = ids
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		input.Reason = "会员优惠券发放"
	}
	input.ActorRole = strings.TrimSpace(input.ActorRole)
	if tenantID == 0 || input.TemplateID == 0 || input.ActorUserID == 0 || input.ActorRole == "" || len(input.ActorRole) > 50 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 160 || input.Reason == "" || len([]rune(input.Reason)) > 500 {
		return nil, ErrMemberCouponGrantInvalid
	}
	hash := memberCouponRequestHash(input)
	var result *MemberCouponGrantResult
	err = s.db().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Same-key concurrent transactions serialize before reading eligibility.
		// An advisory transaction lock claims only this tenant's request identity;
		// account/identity/member/template row locks still protect domain facts.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", fmt.Sprintf("member-coupon:%d:%s", tenantID, input.IdempotencyKey)).Error; err != nil {
			return err
		}
		var existing model.MemberCouponGrantBatch
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, input.IdempotencyKey).First(&existing).Error; err == nil {
			if existing.RequestHash != hash {
				return ErrMemberCouponGrantConflict
			}
			var e error
			result, e = memberCouponResultTx(tx, tenantID, input.IdempotencyKey)
			return e
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		preview, wallets, err := s.prepareTx(tx, tenantID, input.TemplateID, ids, true)
		if err != nil {
			return err
		}
		if !preview.Eligible {
			return fmt.Errorf("%w: %s", ErrMemberCouponGrantEligibility, preview.Reason)
		}
		now := s.now()
		batch := model.MemberCouponGrantBatch{TenantID: tenantID, IdempotencyKey: input.IdempotencyKey, RequestHash: hash, TemplateID: input.TemplateID, ChannelAccountID: preview.Template.ChannelAccountID, ActorUserID: input.ActorUserID, ActorRole: input.ActorRole, Reason: input.Reason, MemberCount: len(ids), Status: "completed"}
		batch.CreatedAt = now
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		promo := &CommercePromotionService{}
		for _, recipient := range preview.Recipients {
			grant, err := promo.issueCouponGrantTx(tx, tenantID, batch.ChannelAccountID, batch.TemplateID, wallets[recipient.MemberID], "manual", fmt.Sprintf("member-batch:%d", batch.ID), now)
			if err != nil {
				if errors.Is(err, ErrPromotionUnavailable) {
					return ErrMemberCouponGrantEligibility
				}
				return err
			}
			item := model.MemberCouponGrantBatchItem{TenantID: tenantID, BatchID: batch.ID, MemberID: recipient.MemberID, CustomerID: wallets[recipient.MemberID], GrantID: grant.ID, MemberNo: recipient.MemberNo, DisplayName: recipient.DisplayName}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		result, err = memberCouponResultTx(tx, tenantID, input.IdempotencyKey)
		if err != nil {
			return err
		}
		after, _ := json.Marshal(struct {
			Result *MemberCouponGrantResult `json:"result"`
			Reason string                   `json:"reason"`
		}{result, input.Reason})
		auditReason := []rune(input.Reason)
		if len(auditReason) > 255 {
			auditReason = auditReason[:255]
		}
		return recordAuditTx(tx, input.ActorUserID, tenantID, input.ActorRole, "tenant", "member.coupons.granted", "member_coupon_grant_batch", batch.ID, string(auditReason), "", string(after))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
