package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCommercePromotionInvalid   = errors.New("commerce promotion is invalid")
	ErrPromotionTemplateImmutable = errors.New("active coupon reward terms are immutable")
	ErrPromotionScopeDenied       = errors.New("promotion scope is not owned or enabled")
	ErrPromotionUnavailable       = errors.New("promotion is unavailable")
	ErrAssistSelfHelp             = errors.New("starter cannot help their own session")
	ErrAssistAlreadyHelped        = errors.New("customer already helped this session")
	ErrAssistSessionLimit         = errors.New("starter session limit reached")
	ErrCouponAlreadyReserved      = errors.New("coupon is already reserved")
	ErrCouponNotOwned             = errors.New("coupon is not owned by the customer")
	ErrCouponNotApplicable        = errors.New("coupon is not applicable to this order")
)

const promotionRefundReturnPolicy = "unfulfilled_full_refund_if_valid"

type CommercePromotionService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

// CommercePromotionScope is the server-derived ownership boundary used by
// storefront and admin promotion APIs. LocationID is retained for callers
// that resolved a fulfillment binding, but store-wide promotion rewards are
// intentionally shared by tenant, channel account, business type, and
// customer across locations of that business. It is never a cross-channel or
// cross-business authorization shortcut.
type CommercePromotionScope struct {
	TenantID         uint
	ChannelAccountID uint
	BusinessType     string
	LocationID       uint
}

func (s *CommercePromotionService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommercePromotionService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *CommercePromotionService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

func promotionBusinessType(v string) bool { return v == "restaurant" || v == "retail" }

func normalizePromotionBusinessTypes(values []string, fallback string) ([]string, error) {
	seen := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !promotionBusinessType(value) {
			return nil, fmt.Errorf("%w: unsupported business type", ErrCommercePromotionInvalid)
		}
		seen[value] = struct{}{}
	}
	if len(seen) == 0 {
		fallback = strings.TrimSpace(fallback)
		if fallback != "" {
			if !promotionBusinessType(fallback) {
				return nil, fmt.Errorf("%w: unsupported business type", ErrCommercePromotionInvalid)
			}
			seen[fallback] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("%w: at least one business type is required", ErrCommercePromotionInvalid)
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func containsPromotionBusinessType(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

// promotionScopeIncludesBusinessType validates a client-provided scope before
// an update is allowed to mutate the stored promotion. A scoped caller may
// only edit a template/campaign while retaining the business context it was
// authorized to operate in. The store-wide marketing center passes an empty
// scope and may intentionally change the complete set.
func promotionScopeIncludesBusinessType(values []string, businessType string) bool {
	if strings.TrimSpace(businessType) == "" {
		return true
	}
	// Keep legacy callers that still send only the singular business_type
	// field compatible with the normalized multi-business request.
	normalized, err := normalizePromotionBusinessTypes(values, businessType)
	return err == nil && containsPromotionBusinessType(normalized, businessType)
}

func promotionBusinessTypesForTemplate(tx *gorm.DB, row *model.CommerceCouponTemplate) ([]string, error) {
	if tx == nil || row == nil || row.ID == 0 {
		return nil, ErrCommercePromotionInvalid
	}
	var scopes []model.CommerceCouponTemplateBusinessType
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND template_id = ?", row.TenantID, row.ChannelAccountID, row.ID).Order("business_type ASC").Find(&scopes).Error; err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return []string{row.BusinessType}, nil
	}
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, scope.BusinessType)
	}
	return values, nil
}

func promotionBusinessTypesForCampaign(tx *gorm.DB, row *model.CommerceAssistCampaign) ([]string, error) {
	if tx == nil || row == nil || row.ID == 0 {
		return nil, ErrCommercePromotionInvalid
	}
	var scopes []model.CommerceAssistCampaignBusinessType
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND campaign_id = ?", row.TenantID, row.ChannelAccountID, row.ID).Order("business_type ASC").Find(&scopes).Error; err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return []string{row.BusinessType}, nil
	}
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, scope.BusinessType)
	}
	return values, nil
}

func promotionBusinessTypesForGrant(tx *gorm.DB, row *model.CommerceCouponGrant) ([]string, error) {
	if tx == nil || row == nil || row.ID == 0 {
		return nil, ErrCommercePromotionInvalid
	}
	var scopes []model.CommerceCouponGrantBusinessType
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND grant_id = ?", row.TenantID, row.ChannelAccountID, row.ID).Order("business_type ASC").Find(&scopes).Error; err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return []string{row.BusinessType}, nil
	}
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, scope.BusinessType)
	}
	return values, nil
}

func ensurePromotionTemplateScopes(tx *gorm.DB, row *model.CommerceCouponTemplate, businessTypes []string) error {
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND template_id = ? AND business_type NOT IN ?", row.TenantID, row.ChannelAccountID, row.ID, businessTypes).Delete(&model.CommerceCouponTemplateBusinessType{}).Error; err != nil {
		return err
	}
	for _, businessType := range businessTypes {
		scope := &model.CommerceCouponTemplateBusinessType{TenantID: row.TenantID, ChannelAccountID: row.ChannelAccountID, TemplateID: row.ID, BusinessType: businessType}
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND template_id = ? AND business_type = ?", row.TenantID, row.ChannelAccountID, row.ID, businessType).FirstOrCreate(scope).Error; err != nil {
			return err
		}
	}
	row.BusinessTypes = append([]string(nil), businessTypes...)
	return nil
}

func ensurePromotionCampaignScopes(tx *gorm.DB, row *model.CommerceAssistCampaign, businessTypes []string) error {
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND campaign_id = ? AND business_type NOT IN ?", row.TenantID, row.ChannelAccountID, row.ID, businessTypes).Delete(&model.CommerceAssistCampaignBusinessType{}).Error; err != nil {
		return err
	}
	for _, businessType := range businessTypes {
		scope := &model.CommerceAssistCampaignBusinessType{TenantID: row.TenantID, ChannelAccountID: row.ChannelAccountID, CampaignID: row.ID, BusinessType: businessType}
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND campaign_id = ? AND business_type = ?", row.TenantID, row.ChannelAccountID, row.ID, businessType).FirstOrCreate(scope).Error; err != nil {
			return err
		}
	}
	row.BusinessTypes = append([]string(nil), businessTypes...)
	return nil
}

func ensurePromotionGrantScopes(tx *gorm.DB, row *model.CommerceCouponGrant, businessTypes []string) error {
	for _, businessType := range businessTypes {
		scope := &model.CommerceCouponGrantBusinessType{TenantID: row.TenantID, ChannelAccountID: row.ChannelAccountID, GrantID: row.ID, BusinessType: businessType}
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND grant_id = ? AND business_type = ?", row.TenantID, row.ChannelAccountID, row.ID, businessType).FirstOrCreate(scope).Error; err != nil {
			return err
		}
	}
	row.BusinessTypes = append([]string(nil), businessTypes...)
	return nil
}

func validateCouponTemplateInput(input CreateCommerceCouponTemplateInput) error {
	input.Name = strings.TrimSpace(input.Name)
	if !promotionBusinessType(input.BusinessType) || input.Name == "" || len([]rune(input.Name)) > 120 {
		return fmt.Errorf("%w: business type and name are required", ErrCommercePromotionInvalid)
	}
	if input.DiscountCents <= 0 || input.MinGoodsSubtotalCents < 0 || input.ValidDays < 0 || input.IssuanceCap < 0 || input.PerCustomerCap <= 0 {
		return fmt.Errorf("%w: reward terms are invalid", ErrCommercePromotionInvalid)
	}
	if input.ValidDays == 0 && input.EndsAt == nil {
		return fmt.Errorf("%w: reward validity is required", ErrCommercePromotionInvalid)
	}
	if input.StartsAt != nil && input.EndsAt != nil && input.EndsAt.Before(*input.StartsAt) {
		return fmt.Errorf("%w: end precedes start", ErrCommercePromotionInvalid)
	}
	if input.RefundReturnPolicy == "" {
		return fmt.Errorf("%w: refund policy is required", ErrCommercePromotionInvalid)
	}
	if input.RefundReturnPolicy != promotionRefundReturnPolicy {
		return fmt.Errorf("%w: unsupported refund policy", ErrCommercePromotionInvalid)
	}
	return nil
}

type CreateCommerceCouponTemplateInput struct {
	ChannelAccountID      uint       `json:"channel_account_id"`
	BusinessType          string     `json:"business_type"`
	BusinessTypes         []string   `json:"business_types"`
	Name                  string     `json:"name"`
	DiscountCents         int64      `json:"discount_cents"`
	MinGoodsSubtotalCents int64      `json:"min_goods_subtotal_cents"`
	ValidDays             int        `json:"valid_days"`
	StartsAt              *time.Time `json:"starts_at,omitempty"`
	EndsAt                *time.Time `json:"ends_at,omitempty"`
	Status                string     `json:"status"`
	IssuanceCap           int        `json:"issuance_cap"`
	PerCustomerCap        int        `json:"per_customer_cap"`
	RefundReturnPolicy    string     `json:"refund_return_policy"`
}

type UpdateCommerceCouponTemplateInput struct {
	BusinessTypes  []string   `json:"business_types,omitempty"`
	Name           string     `json:"name"`
	StartsAt       *time.Time `json:"starts_at,omitempty"`
	EndsAt         *time.Time `json:"ends_at,omitempty"`
	Status         string     `json:"status"`
	IssuanceCap    int        `json:"issuance_cap"`
	PerCustomerCap int        `json:"per_customer_cap"`
}

type CreateCommerceAssistCampaignInput struct {
	ChannelAccountID        uint      `json:"channel_account_id"`
	BusinessType            string    `json:"business_type"`
	BusinessTypes           []string  `json:"business_types"`
	Title                   string    `json:"title"`
	StarterCouponTemplateID uint      `json:"starter_coupon_template_id"`
	HelperCouponTemplateID  uint      `json:"helper_coupon_template_id"`
	RequiredUniqueHelpers   int       `json:"required_unique_helpers"`
	PerStarterSessionLimit  int       `json:"per_starter_session_limit"`
	StartsAt                time.Time `json:"starts_at"`
	EndsAt                  time.Time `json:"ends_at"`
	Status                  string    `json:"status"`
}

type UpdateCommerceAssistCampaignInput struct {
	BusinessTypes          []string  `json:"business_types,omitempty"`
	Title                  string    `json:"title"`
	StartsAt               time.Time `json:"starts_at"`
	EndsAt                 time.Time `json:"ends_at"`
	RequiredUniqueHelpers  int       `json:"required_unique_helpers"`
	PerStarterSessionLimit int       `json:"per_starter_session_limit"`
	Status                 string    `json:"status"`
}

type CommerceCouponBenefitView struct {
	DiscountCents         int64      `json:"discount_cents"`
	MinGoodsSubtotalCents int64      `json:"min_goods_subtotal_cents"`
	ValidDays             int        `json:"valid_days"`
	TemplateEndsAt        *time.Time `json:"template_ends_at,omitempty"`
	BusinessTypes         []string   `json:"business_types"`
}

type CommerceAssistCampaignView struct {
	ID                    uint                      `json:"id"`
	Title                 string                    `json:"title"`
	BusinessType          string                    `json:"business_type"`
	BusinessTypes         []string                  `json:"business_types"`
	RequiredUniqueHelpers int                       `json:"required_unique_helpers"`
	StartsAt              time.Time                 `json:"starts_at"`
	EndsAt                time.Time                 `json:"ends_at"`
	Status                string                    `json:"status"`
	StarterReward         CommerceCouponBenefitView `json:"starter_reward"`
	HelperReward          CommerceCouponBenefitView `json:"helper_reward"`
}

type CommerceAssistSessionView struct {
	ID                    uint                      `json:"id"`
	CampaignID            uint                      `json:"campaign_id"`
	Title                 string                    `json:"title"`
	BusinessType          string                    `json:"business_type"`
	BusinessTypes         []string                  `json:"business_types"`
	Status                string                    `json:"status"`
	HelperCount           int                       `json:"helper_count"`
	RequiredUniqueHelpers int                       `json:"required_unique_helpers"`
	ExpiresAt             time.Time                 `json:"expires_at"`
	SucceededAt           *time.Time                `json:"succeeded_at,omitempty"`
	StarterReward         CommerceCouponBenefitView `json:"starter_reward"`
	HelperReward          CommerceCouponBenefitView `json:"helper_reward"`
}

type CreateAssistSessionResult struct {
	ShareToken string                    `json:"share_token,omitempty"`
	View       CommerceAssistSessionView `json:"session"`
}

func (s *CommercePromotionService) checkScope(tx *gorm.DB, tenantID uint, channelID uint, businessType string) error {
	if tenantID == 0 || !promotionBusinessType(businessType) {
		return ErrPromotionScopeDenied
	}
	if err := RequireActiveTenantBusinessCapability(tx, tenantID, businessType); err != nil {
		return ErrPromotionScopeDenied
	}
	if channelID == 0 {
		return nil
	}
	return s.checkChannelScope(tx, tenantID, channelID)
}

func (s *CommercePromotionService) checkChannelScope(tx *gorm.DB, tenantID, channelID uint) error {
	if tenantID == 0 || channelID == 0 {
		return ErrPromotionScopeDenied
	}
	var account model.ChannelAccount
	if err := tx.Select("id", "tenant_id", "type", "status").First(&account, channelID).Error; err != nil {
		return ErrPromotionScopeDenied
	}
	// Commerce promotions are currently exposed only through the shared WeChat
	// storefront. Keep channel_account_id=0 available solely for legacy unit
	// tests/internal callers; every routed scope must identify this channel.
	if account.TenantID != tenantID || account.Type != "wechat_miniapp" || account.Status == "disabled" {
		return ErrPromotionScopeDenied
	}
	return nil
}

func (s *CommercePromotionService) checkBusinessTypes(tx *gorm.DB, tenantID, channelID uint, businessTypes []string) error {
	if len(businessTypes) == 0 {
		return ErrPromotionScopeDenied
	}
	for _, businessType := range businessTypes {
		if err := s.checkScope(tx, tenantID, channelID, businessType); err != nil {
			return err
		}
	}
	return nil
}

func (s *CommercePromotionService) checkPromotionScope(scope CommercePromotionScope) error {
	if scope.ChannelAccountID == 0 {
		return ErrPromotionScopeDenied
	}
	if strings.TrimSpace(scope.BusinessType) == "" {
		return s.checkChannelScope(s.db(), scope.TenantID, scope.ChannelAccountID)
	}
	return s.checkScope(s.db(), scope.TenantID, scope.ChannelAccountID, strings.TrimSpace(scope.BusinessType))
}

func normalizePromotionScope(scope CommercePromotionScope) CommercePromotionScope {
	scope.BusinessType = strings.TrimSpace(scope.BusinessType)
	return scope
}

// The ForScope variants are the only methods public storefront controllers
// should call. They overwrite request-supplied channel/business values with
// the server-derived scope and reject non-WeChat or zero-channel scopes.
func (s *CommercePromotionService) CreateCouponTemplateForScope(scope CommercePromotionScope, input CreateCommerceCouponTemplateInput) (*model.CommerceCouponTemplate, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	input.ChannelAccountID = scope.ChannelAccountID
	if len(input.BusinessTypes) == 0 && strings.TrimSpace(input.BusinessType) == "" {
		input.BusinessType = scope.BusinessType
	} else if scope.BusinessType != "" && !promotionScopeIncludesBusinessType(input.BusinessTypes, scope.BusinessType) {
		return nil, ErrPromotionScopeDenied
	}
	return s.CreateCouponTemplate(scope.TenantID, input)
}

func (s *CommercePromotionService) UpdateCouponTemplateForScope(scope CommercePromotionScope, templateID uint, input UpdateCommerceCouponTemplateInput) (*model.CommerceCouponTemplate, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	var existing model.CommerceCouponTemplate
	query := s.db().Where("id = ? AND tenant_id = ? AND channel_account_id = ?", templateID, scope.TenantID, scope.ChannelAccountID)
	if scope.BusinessType != "" {
		query = query.Where("business_type = ? OR EXISTS (SELECT 1 FROM commerce_coupon_template_business_types scope WHERE scope.template_id = commerce_coupon_templates.id AND scope.tenant_id = commerce_coupon_templates.tenant_id AND scope.channel_account_id = commerce_coupon_templates.channel_account_id AND scope.business_type = ?)", scope.BusinessType, scope.BusinessType)
	}
	if err := query.First(&existing).Error; err != nil {
		return nil, ErrPromotionScopeDenied
	}
	if scope.BusinessType != "" && len(input.BusinessTypes) > 0 && !promotionScopeIncludesBusinessType(input.BusinessTypes, scope.BusinessType) {
		return nil, ErrPromotionScopeDenied
	}
	row, err := s.UpdateCouponTemplate(scope.TenantID, templateID, input)
	if err != nil {
		return nil, err
	}
	if row.ChannelAccountID != scope.ChannelAccountID {
		return nil, ErrPromotionScopeDenied
	}
	if scope.BusinessType != "" {
		values, scopeErr := promotionBusinessTypesForTemplate(s.db(), row)
		if scopeErr != nil || !containsPromotionBusinessType(values, scope.BusinessType) {
			return nil, ErrPromotionScopeDenied
		}
	}
	return row, nil
}

func (s *CommercePromotionService) ListCouponTemplatesForScope(scope CommercePromotionScope, status string) ([]model.CommerceCouponTemplate, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	return s.ListCouponTemplates(scope.TenantID, scope.ChannelAccountID, scope.BusinessType, status)
}

func (s *CommercePromotionService) CreateAssistCampaignForScope(scope CommercePromotionScope, input CreateCommerceAssistCampaignInput) (*model.CommerceAssistCampaign, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	input.ChannelAccountID = scope.ChannelAccountID
	if len(input.BusinessTypes) == 0 && strings.TrimSpace(input.BusinessType) == "" {
		input.BusinessType = scope.BusinessType
	} else if scope.BusinessType != "" && !promotionScopeIncludesBusinessType(input.BusinessTypes, scope.BusinessType) {
		return nil, ErrPromotionScopeDenied
	}
	return s.CreateAssistCampaign(scope.TenantID, input)
}

func (s *CommercePromotionService) UpdateAssistCampaignForScope(scope CommercePromotionScope, campaignID uint, input UpdateCommerceAssistCampaignInput) (*model.CommerceAssistCampaign, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	var existing model.CommerceAssistCampaign
	query := s.db().Where("id = ? AND tenant_id = ? AND channel_account_id = ?", campaignID, scope.TenantID, scope.ChannelAccountID)
	if scope.BusinessType != "" {
		query = query.Where("business_type = ? OR EXISTS (SELECT 1 FROM commerce_assist_campaign_business_types scope WHERE scope.campaign_id = commerce_assist_campaigns.id AND scope.tenant_id = commerce_assist_campaigns.tenant_id AND scope.channel_account_id = commerce_assist_campaigns.channel_account_id AND scope.business_type = ?)", scope.BusinessType, scope.BusinessType)
	}
	if err := query.First(&existing).Error; err != nil {
		return nil, ErrPromotionScopeDenied
	}
	if scope.BusinessType != "" && len(input.BusinessTypes) > 0 && !promotionScopeIncludesBusinessType(input.BusinessTypes, scope.BusinessType) {
		return nil, ErrPromotionScopeDenied
	}
	row, err := s.UpdateAssistCampaign(scope.TenantID, campaignID, input)
	if err != nil {
		return nil, err
	}
	if row.ChannelAccountID != scope.ChannelAccountID {
		return nil, ErrPromotionScopeDenied
	}
	if scope.BusinessType != "" {
		values, scopeErr := promotionBusinessTypesForCampaign(s.db(), row)
		if scopeErr != nil || !containsPromotionBusinessType(values, scope.BusinessType) {
			return nil, ErrPromotionScopeDenied
		}
	}
	return row, nil
}

func (s *CommercePromotionService) ListAssistCampaignsForScope(scope CommercePromotionScope, status string) ([]model.CommerceAssistCampaign, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	return s.ListAssistCampaigns(scope.TenantID, scope.ChannelAccountID, scope.BusinessType, status)
}

// ListAvailableAssistCampaignsForScope is the customer-facing campaign list.
// It never exposes draft, inactive, not-yet-started, or expired campaigns.
func (s *CommercePromotionService) ListAvailableAssistCampaignsForScope(scope CommercePromotionScope) ([]CommerceAssistCampaignView, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	now := s.now()
	var rows []model.CommerceAssistCampaign
	err := s.db().Where(
		"tenant_id = ? AND channel_account_id = ? AND (business_type = ? OR EXISTS (SELECT 1 FROM commerce_assist_campaign_business_types scope WHERE scope.campaign_id = commerce_assist_campaigns.id AND scope.tenant_id = commerce_assist_campaigns.tenant_id AND scope.channel_account_id = commerce_assist_campaigns.channel_account_id AND scope.business_type = ?)) AND status = ? AND starts_at <= ? AND ends_at > ?",
		scope.TenantID, scope.ChannelAccountID, scope.BusinessType, scope.BusinessType, "active", now, now,
	).Order("starts_at DESC, id DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	views := make([]CommerceAssistCampaignView, 0, len(rows))
	for index := range rows {
		view, viewErr := s.assistCampaignView(s.db(), &rows[index])
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *CommercePromotionService) validateAssistTokenScope(scope CommercePromotionScope, rawToken string) error {
	var session model.CommerceAssistSession
	if err := s.db().Where("tenant_id = ? AND share_token_hash = ?", scope.TenantID, hashAssistToken(strings.TrimSpace(rawToken))).First(&session).Error; err != nil {
		return err
	}
	var campaign model.CommerceAssistCampaign
	if err := s.db().First(&campaign, session.CampaignID).Error; err != nil {
		return err
	}
	if campaign.TenantID != scope.TenantID || campaign.ChannelAccountID != scope.ChannelAccountID {
		return ErrPromotionScopeDenied
	}
	types, err := promotionBusinessTypesForCampaign(s.db(), &campaign)
	if err != nil || !containsPromotionBusinessType(types, scope.BusinessType) {
		return ErrPromotionScopeDenied
	}
	return nil
}

func (s *CommercePromotionService) CreateAssistSessionForScope(scope CommercePromotionScope, campaignID uint, starterCustomerID, idempotencyKey string) (*CreateAssistSessionResult, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	// CreateAssistSession verifies the campaign ownership and invokes the same
	// strict channel check inside its transaction.
	var campaign model.CommerceAssistCampaign
	if err := s.db().Where("id = ? AND tenant_id = ? AND channel_account_id = ?", campaignID, scope.TenantID, scope.ChannelAccountID).First(&campaign).Error; err != nil {
		return nil, ErrPromotionScopeDenied
	}
	types, err := promotionBusinessTypesForCampaign(s.db(), &campaign)
	if err != nil || !containsPromotionBusinessType(types, scope.BusinessType) {
		return nil, ErrPromotionScopeDenied
	}
	return s.CreateAssistSession(scope.TenantID, campaignID, starterCustomerID, idempotencyKey)
}

func (s *CommercePromotionService) GetAssistSessionForScope(scope CommercePromotionScope, rawToken, customerID string) (*CommerceAssistSessionView, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rawToken) == "" {
		return nil, ErrCommercePromotionInvalid
	}
	if err := s.validateAssistTokenScope(scope, rawToken); err != nil {
		return nil, err
	}
	return s.GetAssistSession(scope.TenantID, rawToken, customerID)
}

func (s *CommercePromotionService) HelpAssistForScope(scope CommercePromotionScope, rawToken, helperCustomerID string) (*AssistHelpResult, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	if err := s.validateAssistTokenScope(scope, rawToken); err != nil {
		return nil, err
	}
	return s.HelpAssist(scope.TenantID, rawToken, helperCustomerID)
}

func (s *CommercePromotionService) ListAvailableCouponsForScope(scope CommercePromotionScope, customerID string) ([]model.CommerceCouponGrant, error) {
	scope = normalizePromotionScope(scope)
	if err := s.checkPromotionScope(scope); err != nil {
		return nil, err
	}
	return s.ListAvailableCoupons(scope.TenantID, scope.ChannelAccountID, scope.BusinessType, customerID)
}

func normalizeTemplateStatus(status string) (string, error) {
	if status == "" {
		return "draft", nil
	}
	if status != "draft" && status != "active" && status != "inactive" {
		return "", ErrCommercePromotionInvalid
	}
	return status, nil
}

func (s *CommercePromotionService) CreateCouponTemplate(tenantID uint, input CreateCommerceCouponTemplateInput) (*model.CommerceCouponTemplate, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	businessTypes, err := normalizePromotionBusinessTypes(input.BusinessTypes, input.BusinessType)
	if err != nil {
		return nil, err
	}
	input.BusinessType = businessTypes[0]
	input.Name = strings.TrimSpace(input.Name)
	if input.RefundReturnPolicy == "" {
		input.RefundReturnPolicy = promotionRefundReturnPolicy
	}
	status, err := normalizeTemplateStatus(input.Status)
	if err != nil {
		return nil, err
	}
	input.Status = status
	if err := validateCouponTemplateInput(input); err != nil {
		return nil, err
	}
	var result *model.CommerceCouponTemplate
	err = s.write(func(tx *gorm.DB) error {
		if err := s.checkBusinessTypes(tx, tenantID, input.ChannelAccountID, businessTypes); err != nil {
			return err
		}
		if input.Status == "active" {
			if err := validateTemplateActiveWindow(input.StartsAt, input.EndsAt, s.now()); err != nil {
				return err
			}
		}
		row := &model.CommerceCouponTemplate{TenantID: tenantID, ChannelAccountID: input.ChannelAccountID, BusinessType: input.BusinessType, Name: input.Name, Version: 1, DiscountCents: input.DiscountCents, MinGoodsSubtotalCents: input.MinGoodsSubtotalCents, ValidDays: input.ValidDays, StartsAt: input.StartsAt, EndsAt: input.EndsAt, Status: input.Status, IssuanceCap: input.IssuanceCap, PerCustomerCap: input.PerCustomerCap, RefundReturnPolicy: input.RefundReturnPolicy}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if err := ensurePromotionTemplateScopes(tx, row, businessTypes); err != nil {
			return err
		}
		result = row
		return nil
	})
	return result, err
}

func validateTemplateActiveWindow(start, end *time.Time, now time.Time) error {
	if start != nil && end != nil && end.Before(*start) {
		return fmt.Errorf("%w: end precedes start", ErrCommercePromotionInvalid)
	}
	if end != nil && !end.After(now) {
		return fmt.Errorf("%w: template has ended", ErrCommercePromotionInvalid)
	}
	return nil
}

func (s *CommercePromotionService) UpdateCouponTemplate(tenantID, templateID uint, input UpdateCommerceCouponTemplateInput) (*model.CommerceCouponTemplate, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 || input.IssuanceCap < 0 || input.PerCustomerCap <= 0 {
		return nil, ErrCommercePromotionInvalid
	}
	status, err := normalizeTemplateStatus(input.Status)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceCouponTemplate
	err = s.write(func(tx *gorm.DB) error {
		var row model.CommerceCouponTemplate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", templateID, tenantID).First(&row).Error; err != nil {
			return err
		}
		if err := s.checkScope(tx, tenantID, row.ChannelAccountID, row.BusinessType); err != nil {
			return err
		}
		businessTypes, scopeErr := promotionBusinessTypesForTemplate(tx, &row)
		if scopeErr != nil {
			return scopeErr
		}
		if len(input.BusinessTypes) > 0 {
			businessTypes, scopeErr = normalizePromotionBusinessTypes(input.BusinessTypes, "")
			if scopeErr != nil {
				return scopeErr
			}
		}
		if err := s.checkBusinessTypes(tx, tenantID, row.ChannelAccountID, businessTypes); err != nil {
			return err
		}
		// Active reward terms are immutable. This update API only exposes
		// presentation, window, and issuance controls, so reject changes to
		// issuance controls while an active template is in use.
		if row.Status == "active" && (input.IssuanceCap != row.IssuanceCap || input.PerCustomerCap != row.PerCustomerCap) {
			return ErrPromotionTemplateImmutable
		}
		if input.EndsAt != nil && input.StartsAt != nil && input.EndsAt.Before(*input.StartsAt) {
			return ErrCommercePromotionInvalid
		}
		if status == "active" {
			if err := validateTemplateActiveWindow(input.StartsAt, input.EndsAt, s.now()); err != nil {
				return err
			}
		}
		updates := map[string]interface{}{"name": input.Name, "starts_at": input.StartsAt, "ends_at": input.EndsAt, "status": status, "issuance_cap": input.IssuanceCap, "per_customer_cap": input.PerCustomerCap, "business_type": businessTypes[0]}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		row.Name, row.StartsAt, row.EndsAt, row.Status, row.IssuanceCap, row.PerCustomerCap, row.BusinessType = input.Name, input.StartsAt, input.EndsAt, status, input.IssuanceCap, input.PerCustomerCap, businessTypes[0]
		if err := ensurePromotionTemplateScopes(tx, &row, businessTypes); err != nil {
			return err
		}
		row.BusinessTypes = businessTypes
		result = &row
		return nil
	})
	return result, err
}

func (s *CommercePromotionService) ListCouponTemplates(tenantID uint, channelID uint, businessType, status string) ([]model.CommerceCouponTemplate, error) {
	businessType = strings.TrimSpace(businessType)
	status = strings.TrimSpace(status)
	if businessType != "" && !promotionBusinessType(businessType) {
		return nil, ErrCommercePromotionInvalid
	}
	db := s.db()
	if err := requireActiveTenant(db, tenantID); err != nil {
		return nil, err
	}
	q := db.Where("tenant_id = ?", tenantID)
	if channelID != 0 {
		q = q.Where("channel_account_id = ?", channelID)
	}
	if businessType != "" {
		q = q.Where("business_type = ? OR EXISTS (SELECT 1 FROM commerce_coupon_template_business_types scope WHERE scope.template_id = commerce_coupon_templates.id AND scope.tenant_id = commerce_coupon_templates.tenant_id AND scope.channel_account_id = commerce_coupon_templates.channel_account_id AND scope.business_type = ?)", businessType, businessType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []model.CommerceCouponTemplate
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for index := range rows {
		values, err := promotionBusinessTypesForTemplate(db, &rows[index])
		if err != nil {
			return nil, err
		}
		rows[index].BusinessTypes = values
	}
	return rows, nil
}

func (s *CommercePromotionService) CreateAssistCampaign(tenantID uint, input CreateCommerceAssistCampaignInput) (*model.CommerceAssistCampaign, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	businessTypes, err := normalizePromotionBusinessTypes(input.BusinessTypes, input.BusinessType)
	if err != nil {
		return nil, err
	}
	input.BusinessType = businessTypes[0]
	if input.Title == "" || len([]rune(input.Title)) > 160 || input.StarterCouponTemplateID == 0 || input.HelperCouponTemplateID == 0 || input.RequiredUniqueHelpers <= 0 || input.PerStarterSessionLimit <= 0 || !input.EndsAt.After(input.StartsAt) {
		return nil, ErrCommercePromotionInvalid
	}
	status, err := normalizeTemplateStatus(input.Status)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceAssistCampaign
	err = s.write(func(tx *gorm.DB) error {
		if err := s.checkBusinessTypes(tx, tenantID, input.ChannelAccountID, businessTypes); err != nil {
			return err
		}
		if err := s.validateCampaignTemplates(tx, tenantID, input.ChannelAccountID, businessTypes, input.StarterCouponTemplateID, input.HelperCouponTemplateID, status == "active"); err != nil {
			return err
		}
		row := &model.CommerceAssistCampaign{TenantID: tenantID, ChannelAccountID: input.ChannelAccountID, BusinessType: input.BusinessType, Title: input.Title, StarterCouponTemplateID: input.StarterCouponTemplateID, HelperCouponTemplateID: input.HelperCouponTemplateID, RequiredUniqueHelpers: input.RequiredUniqueHelpers, PerStarterSessionLimit: input.PerStarterSessionLimit, StartsAt: input.StartsAt, EndsAt: input.EndsAt, Status: status}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if err := ensurePromotionCampaignScopes(tx, row, businessTypes); err != nil {
			return err
		}
		result = row
		return nil
	})
	return result, err
}

func (s *CommercePromotionService) validateCampaignTemplates(tx *gorm.DB, tenantID, channelID uint, businessTypes []string, starterID, helperID uint, active bool) error {
	var rows []model.CommerceCouponTemplate
	if err := tx.Where("id IN ? AND tenant_id = ? AND channel_account_id = ?", []uint{starterID, helperID}, tenantID, channelID).Find(&rows).Error; err != nil {
		return err
	}
	expected := 2
	if starterID == helperID {
		expected = 1
	}
	if len(rows) != expected {
		return ErrPromotionScopeDenied
	}
	for index := range rows {
		types, err := promotionBusinessTypesForTemplate(tx, &rows[index])
		if err != nil {
			return err
		}
		for _, businessType := range businessTypes {
			if !containsPromotionBusinessType(types, businessType) {
				return ErrPromotionScopeDenied
			}
		}
	}
	if active {
		for _, row := range rows {
			if row.Status != "active" {
				return fmt.Errorf("%w: campaign templates must be active", ErrPromotionUnavailable)
			}
		}
	}
	return nil
}

func (s *CommercePromotionService) UpdateAssistCampaign(tenantID, campaignID uint, input UpdateCommerceAssistCampaignInput) (*model.CommerceAssistCampaign, error) {
	input.Title = strings.TrimSpace(input.Title)
	status, err := normalizeTemplateStatus(input.Status)
	if err != nil {
		return nil, err
	}
	if input.Title == "" || input.RequiredUniqueHelpers <= 0 || input.PerStarterSessionLimit <= 0 || !input.EndsAt.After(input.StartsAt) {
		return nil, ErrCommercePromotionInvalid
	}
	var result *model.CommerceAssistCampaign
	err = s.write(func(tx *gorm.DB) error {
		var row model.CommerceAssistCampaign
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", campaignID, tenantID).First(&row).Error; err != nil {
			return err
		}
		businessTypes, scopeErr := promotionBusinessTypesForCampaign(tx, &row)
		if scopeErr != nil {
			return scopeErr
		}
		if err := s.checkBusinessTypes(tx, tenantID, row.ChannelAccountID, businessTypes); err != nil {
			return err
		}
		if len(input.BusinessTypes) > 0 {
			businessTypes, scopeErr = normalizePromotionBusinessTypes(input.BusinessTypes, "")
			if scopeErr != nil {
				return scopeErr
			}
		}
		if err := s.checkBusinessTypes(tx, tenantID, row.ChannelAccountID, businessTypes); err != nil {
			return err
		}
		if status == "active" {
			if err := s.validateCampaignTemplates(tx, tenantID, row.ChannelAccountID, businessTypes, row.StarterCouponTemplateID, row.HelperCouponTemplateID, true); err != nil {
				return err
			}
		}
		if err := tx.Model(&row).Updates(map[string]interface{}{"title": input.Title, "starts_at": input.StartsAt, "ends_at": input.EndsAt, "required_unique_helpers": input.RequiredUniqueHelpers, "per_starter_session_limit": input.PerStarterSessionLimit, "status": status, "business_type": businessTypes[0]}).Error; err != nil {
			return err
		}
		row.Title, row.StartsAt, row.EndsAt, row.RequiredUniqueHelpers, row.PerStarterSessionLimit, row.Status, row.BusinessType = input.Title, input.StartsAt, input.EndsAt, input.RequiredUniqueHelpers, input.PerStarterSessionLimit, status, businessTypes[0]
		if err := ensurePromotionCampaignScopes(tx, &row, businessTypes); err != nil {
			return err
		}
		row.BusinessTypes = businessTypes
		result = &row
		return nil
	})
	return result, err
}

func (s *CommercePromotionService) ListAssistCampaigns(tenantID uint, channelID uint, businessType, status string) ([]model.CommerceAssistCampaign, error) {
	db := s.db()
	if err := requireActiveTenant(db, tenantID); err != nil {
		return nil, err
	}
	q := db.Where("tenant_id = ?", tenantID)
	if channelID != 0 {
		q = q.Where("channel_account_id = ?", channelID)
	}
	if businessType != "" {
		q = q.Where("business_type = ? OR EXISTS (SELECT 1 FROM commerce_assist_campaign_business_types scope WHERE scope.campaign_id = commerce_assist_campaigns.id AND scope.tenant_id = commerce_assist_campaigns.tenant_id AND scope.channel_account_id = commerce_assist_campaigns.channel_account_id AND scope.business_type = ?)", businessType, businessType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var rows []model.CommerceAssistCampaign
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for index := range rows {
		values, err := promotionBusinessTypesForCampaign(db, &rows[index])
		if err != nil {
			return nil, err
		}
		rows[index].BusinessTypes = values
	}
	return rows, nil
}

func hashAssistToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newAssistToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func couponBenefitView(template *model.CommerceCouponTemplate, businessTypes []string) CommerceCouponBenefitView {
	if template == nil {
		return CommerceCouponBenefitView{}
	}
	return CommerceCouponBenefitView{
		DiscountCents: template.DiscountCents, MinGoodsSubtotalCents: template.MinGoodsSubtotalCents,
		ValidDays: template.ValidDays, TemplateEndsAt: template.EndsAt,
		BusinessTypes: append([]string(nil), businessTypes...),
	}
}

func (s *CommercePromotionService) assistCampaignView(tx *gorm.DB, campaign *model.CommerceAssistCampaign) (CommerceAssistCampaignView, error) {
	if tx == nil || campaign == nil || campaign.ID == 0 {
		return CommerceAssistCampaignView{}, ErrCommercePromotionInvalid
	}
	var templates []model.CommerceCouponTemplate
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND id IN ?", campaign.TenantID, campaign.ChannelAccountID, []uint{campaign.StarterCouponTemplateID, campaign.HelperCouponTemplateID}).Find(&templates).Error; err != nil {
		return CommerceAssistCampaignView{}, err
	}
	businessTypes, err := promotionBusinessTypesForCampaign(tx, campaign)
	if err != nil {
		return CommerceAssistCampaignView{}, err
	}
	byID := make(map[uint]*model.CommerceCouponTemplate, len(templates))
	for index := range templates {
		types, typeErr := promotionBusinessTypesForTemplate(tx, &templates[index])
		if typeErr != nil {
			return CommerceAssistCampaignView{}, typeErr
		}
		for _, businessType := range businessTypes {
			if !containsPromotionBusinessType(types, businessType) {
				return CommerceAssistCampaignView{}, ErrPromotionScopeDenied
			}
		}
		byID[templates[index].ID] = &templates[index]
	}
	starter, starterOK := byID[campaign.StarterCouponTemplateID]
	helper, helperOK := byID[campaign.HelperCouponTemplateID]
	if !starterOK || !helperOK {
		return CommerceAssistCampaignView{}, ErrPromotionScopeDenied
	}
	starterTypes, err := promotionBusinessTypesForTemplate(tx, starter)
	if err != nil {
		return CommerceAssistCampaignView{}, err
	}
	helperTypes, err := promotionBusinessTypesForTemplate(tx, helper)
	if err != nil {
		return CommerceAssistCampaignView{}, err
	}
	return CommerceAssistCampaignView{
		ID: campaign.ID, Title: campaign.Title, BusinessType: campaign.BusinessType,
		BusinessTypes:         businessTypes,
		RequiredUniqueHelpers: campaign.RequiredUniqueHelpers, StartsAt: campaign.StartsAt,
		EndsAt: campaign.EndsAt, Status: campaign.Status,
		StarterReward: couponBenefitView(starter, starterTypes), HelperReward: couponBenefitView(helper, helperTypes),
	}, nil
}

func (s *CommercePromotionService) sessionView(tx *gorm.DB, row *model.CommerceAssistSession) (CommerceAssistSessionView, error) {
	var campaign model.CommerceAssistCampaign
	if err := tx.Where("id = ? AND tenant_id = ?", row.CampaignID, row.TenantID).First(&campaign).Error; err != nil {
		return CommerceAssistSessionView{}, err
	}
	view, err := s.assistCampaignView(tx, &campaign)
	if err != nil {
		return CommerceAssistSessionView{}, err
	}
	return CommerceAssistSessionView{
		ID: row.ID, CampaignID: row.CampaignID, Title: campaign.Title, BusinessType: campaign.BusinessType,
		BusinessTypes: view.BusinessTypes,
		Status:        row.Status, HelperCount: row.HelperCount, RequiredUniqueHelpers: campaign.RequiredUniqueHelpers,
		ExpiresAt: row.ExpiresAt, SucceededAt: row.SucceededAt,
		StarterReward: view.StarterReward, HelperReward: view.HelperReward,
	}, nil
}

func (s *CommercePromotionService) CreateAssistSession(tenantID, campaignID uint, starterCustomerID, idempotencyKey string) (*CreateAssistSessionResult, error) {
	starterCustomerID = strings.TrimSpace(starterCustomerID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if starterCustomerID == "" || idempotencyKey == "" {
		return nil, ErrCommercePromotionInvalid
	}
	var result *CreateAssistSessionResult
	err := s.write(func(tx *gorm.DB) error {
		var campaign model.CommerceAssistCampaign
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&campaign, campaignID).Error; err != nil {
			return err
		}
		if campaign.TenantID != tenantID {
			return ErrPromotionScopeDenied
		}
		businessTypes, scopeErr := promotionBusinessTypesForCampaign(tx, &campaign)
		if scopeErr != nil {
			return scopeErr
		}
		if err := s.checkBusinessTypes(tx, tenantID, campaign.ChannelAccountID, businessTypes); err != nil {
			return ErrPromotionScopeDenied
		}
		now := s.now()
		if campaign.Status != "active" || now.Before(campaign.StartsAt) || !now.Before(campaign.EndsAt) {
			return ErrPromotionUnavailable
		}
		var existing model.CommerceAssistSession
		if err := tx.Where("tenant_id = ? AND campaign_id = ? AND starter_customer_id = ? AND idempotency_key = ?", tenantID, campaignID, starterCustomerID, idempotencyKey).First(&existing).Error; err == nil {
			view, viewErr := s.sessionView(tx, &existing)
			if viewErr != nil {
				return viewErr
			}
			rawToken, decryptErr := utils.DecryptAES(existing.ShareTokenCiphertext)
			if decryptErr != nil || strings.TrimSpace(rawToken) == "" || hashAssistToken(rawToken) != existing.ShareTokenHash {
				return fmt.Errorf("%w: stored share token cannot be recovered", ErrCommercePromotionInvalid)
			}
			result = &CreateAssistSessionResult{ShareToken: rawToken, View: view}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err := tx.Model(&model.CommerceAssistSession{}).Where("tenant_id = ? AND campaign_id = ? AND starter_customer_id = ? AND status IN ? AND expires_at > ?", tenantID, campaignID, starterCustomerID, []string{"active", "succeeded"}, now).Count(&count).Error; err != nil {
			return err
		}
		if int(count) >= campaign.PerStarterSessionLimit {
			return ErrAssistSessionLimit
		}
		raw, err := newAssistToken()
		if err != nil {
			return err
		}
		ciphertext, err := utils.EncryptAES(raw)
		if err != nil {
			return err
		}
		expires := campaign.EndsAt
		row := &model.CommerceAssistSession{TenantID: tenantID, CampaignID: campaignID, StarterCustomerID: starterCustomerID, IdempotencyKey: idempotencyKey, ShareTokenHash: hashAssistToken(raw), ShareTokenCiphertext: ciphertext, Status: "active", ExpiresAt: expires}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		view, err := s.sessionView(tx, row)
		if err != nil {
			return err
		}
		result = &CreateAssistSessionResult{ShareToken: raw, View: view}
		return nil
	})
	return result, err
}

func (s *CommercePromotionService) GetAssistSession(tenantID uint, rawToken, customerID string) (*CommerceAssistSessionView, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil, ErrCommercePromotionInvalid
	}
	var row model.CommerceAssistSession
	db := s.db()
	if err := db.Where("tenant_id = ? AND share_token_hash = ?", tenantID, hashAssistToken(rawToken)).First(&row).Error; err != nil {
		return nil, err
	}
	if customerID != "" && strings.TrimSpace(customerID) == row.StarterCustomerID { /* starter view is allowed */
	}
	if row.Status == "active" && !s.now().Before(row.ExpiresAt) {
		_ = db.Model(&row).Updates(map[string]interface{}{"status": "expired"})
		row.Status = "expired"
	}
	view, err := s.sessionView(db, &row)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

type AssistHelpResult struct {
	View           CommerceAssistSessionView `json:"session"`
	HelperGrantID  uint                      `json:"helper_grant_id"`
	StarterGrantID uint                      `json:"starter_grant_id,omitempty"`
	AlreadyHelped  bool                      `json:"already_helped"`
}

func (s *CommercePromotionService) HelpAssist(tenantID uint, rawToken, helperCustomerID string) (*AssistHelpResult, error) {
	rawToken = strings.TrimSpace(rawToken)
	helperCustomerID = strings.TrimSpace(helperCustomerID)
	if rawToken == "" || helperCustomerID == "" {
		return nil, ErrCommercePromotionInvalid
	}
	var result *AssistHelpResult
	err := s.write(func(tx *gorm.DB) error {
		var session model.CommerceAssistSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND share_token_hash = ?", tenantID, hashAssistToken(rawToken)).First(&session).Error; err != nil {
			return err
		}
		var campaign model.CommerceAssistCampaign
		if err := tx.First(&campaign, session.CampaignID).Error; err != nil {
			return err
		}
		if campaign.TenantID != tenantID {
			return ErrPromotionScopeDenied
		}
		businessTypes, scopeErr := promotionBusinessTypesForCampaign(tx, &campaign)
		if scopeErr != nil {
			return scopeErr
		}
		if err := s.checkBusinessTypes(tx, tenantID, campaign.ChannelAccountID, businessTypes); err != nil {
			return ErrPromotionScopeDenied
		}
		now := s.now()
		if session.StarterCustomerID == helperCustomerID {
			return ErrAssistSelfHelp
		}
		var prior model.CommerceAssistRecord
		if err := tx.Where("session_id = ? AND helper_customer_id = ?", session.ID, helperCustomerID).First(&prior).Error; err == nil {
			return ErrAssistAlreadyHelped
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if session.Status != "active" || !now.Before(session.ExpiresAt) || campaign.Status != "active" || !now.Before(campaign.EndsAt) {
			return ErrPromotionUnavailable
		}
		helperGrant, err := s.issueGrantTx(tx, &campaign, campaign.HelperCouponTemplateID, helperCustomerID, "assist_helper", fmt.Sprintf("session:%d:helper:%s", session.ID, helperCustomerID), now)
		if err != nil {
			return err
		}
		record := &model.CommerceAssistRecord{TenantID: tenantID, CampaignID: campaign.ID, SessionID: session.ID, HelperCustomerID: helperCustomerID, RewardGrantID: helperGrant.ID}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if session.HelperCount+1 >= campaign.RequiredUniqueHelpers {
			starterGrant, grantErr := s.issueGrantTx(tx, &campaign, campaign.StarterCouponTemplateID, session.StarterCustomerID, "assist_starter", fmt.Sprintf("session:%d:starter", session.ID), now)
			if grantErr != nil {
				return grantErr
			}
			record.StarterGrantID = starterGrant.ID
			succeeded := now
			session.Status, session.SucceededAt, session.HelperCount = "succeeded", &succeeded, session.HelperCount+1
			if err := tx.Model(&session).Updates(map[string]interface{}{"status": session.Status, "succeeded_at": session.SucceededAt, "helper_count": session.HelperCount}).Error; err != nil {
				return err
			}
		} else {
			session.HelperCount++
			if err := tx.Model(&session).Update("helper_count", session.HelperCount).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(record).Update("starter_grant_id", record.StarterGrantID).Error; err != nil {
			return err
		}
		view, err := s.sessionView(tx, &session)
		if err != nil {
			return err
		}
		result = &AssistHelpResult{View: view, HelperGrantID: helperGrant.ID, StarterGrantID: record.StarterGrantID}
		return nil
	})
	return result, err
}

func (s *CommercePromotionService) issueGrantTx(tx *gorm.DB, campaign *model.CommerceAssistCampaign, templateID uint, customerID, source, sourceIdentity string, now time.Time) (*model.CommerceCouponGrant, error) {
	var template model.CommerceCouponTemplate
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ?", templateID, campaign.TenantID, campaign.ChannelAccountID).First(&template).Error; err != nil {
		return nil, ErrPromotionScopeDenied
	}
	campaignTypes, err := promotionBusinessTypesForCampaign(tx, campaign)
	if err != nil {
		return nil, err
	}
	templateTypes, err := promotionBusinessTypesForTemplate(tx, &template)
	if err != nil {
		return nil, err
	}
	for _, businessType := range campaignTypes {
		if !containsPromotionBusinessType(templateTypes, businessType) {
			return nil, ErrPromotionScopeDenied
		}
	}
	if template.Status != "active" || (template.StartsAt != nil && now.Before(*template.StartsAt)) || (template.EndsAt != nil && !now.Before(*template.EndsAt)) {
		return nil, ErrPromotionUnavailable
	}
	var existing model.CommerceCouponGrant
	if err := tx.Where("tenant_id = ? AND template_id = ? AND source = ? AND source_identity = ? AND customer_id = ?", campaign.TenantID, templateID, source, sourceIdentity, customerID).First(&existing).Error; err == nil {
		existingTypes, typesErr := promotionBusinessTypesForGrant(tx, &existing)
		if typesErr != nil {
			return nil, typesErr
		}
		existing.BusinessTypes = existingTypes
		return &existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if template.IssuanceCap > 0 {
		var issued int64
		if err := tx.Model(&model.CommerceCouponGrant{}).Where("tenant_id = ? AND template_id = ? AND status <> ?", campaign.TenantID, templateID, "cancelled").Count(&issued).Error; err != nil {
			return nil, err
		}
		if issued >= int64(template.IssuanceCap) {
			return nil, ErrPromotionUnavailable
		}
	}
	if template.PerCustomerCap > 0 {
		var count int64
		if err := tx.Model(&model.CommerceCouponGrant{}).Where("tenant_id = ? AND template_id = ? AND customer_id = ? AND status <> ?", campaign.TenantID, templateID, customerID, "cancelled").Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(template.PerCustomerCap) {
			return nil, ErrPromotionUnavailable
		}
	}
	expires := now.AddDate(0, 0, template.ValidDays)
	if template.ValidDays == 0 && template.EndsAt != nil {
		expires = *template.EndsAt
	}
	if template.EndsAt != nil && expires.After(*template.EndsAt) {
		expires = *template.EndsAt
	}
	// A grant is an immutable snapshot of the selected coupon template. The
	// campaign must cover its own configured scope, but it must not narrow or
	// rewrite the coupon's store-level applicability when the reward is issued.
	row := &model.CommerceCouponGrant{TenantID: campaign.TenantID, TemplateID: template.ID, ChannelAccountID: template.ChannelAccountID, BusinessType: template.BusinessType, CustomerID: customerID, Source: source, SourceIdentity: sourceIdentity, DiscountCents: template.DiscountCents, MinGoodsSubtotalCents: template.MinGoodsSubtotalCents, RefundReturnPolicy: template.RefundReturnPolicy, Status: "available", ExpiresAt: expires}
	if err := tx.Create(row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			var retry model.CommerceCouponGrant
			if retryErr := tx.Where("tenant_id = ? AND template_id = ? AND source = ? AND source_identity = ? AND customer_id = ?", campaign.TenantID, templateID, source, sourceIdentity, customerID).First(&retry).Error; retryErr == nil {
				retryTypes, typesErr := promotionBusinessTypesForGrant(tx, &retry)
				if typesErr != nil {
					return nil, typesErr
				}
				retry.BusinessTypes = retryTypes
				return &retry, nil
			}
		}
		return nil, err
	}
	if err := ensurePromotionGrantScopes(tx, row, templateTypes); err != nil {
		return nil, err
	}
	row.BusinessTypes = append([]string(nil), templateTypes...)
	return row, nil
}

func (s *CommercePromotionService) ListAvailableCoupons(tenantID uint, channelID uint, businessType, customerID string) ([]model.CommerceCouponGrant, error) {
	customerID = strings.TrimSpace(customerID)
	businessType = strings.TrimSpace(businessType)
	if customerID == "" || !promotionBusinessType(businessType) {
		return nil, ErrCommercePromotionInvalid
	}
	now := s.now()
	var rows []model.CommerceCouponGrant
	err := s.write(func(tx *gorm.DB) error {
		if err := s.checkScope(tx, tenantID, channelID, businessType); err != nil {
			return err
		}
		if err := tx.Model(&model.CommerceCouponGrant{}).Where("tenant_id = ? AND channel_account_id = ? AND customer_id = ? AND status = ? AND expires_at <= ? AND (business_type = ? OR EXISTS (SELECT 1 FROM commerce_coupon_grant_business_types scope WHERE scope.grant_id = commerce_coupon_grants.id AND scope.tenant_id = commerce_coupon_grants.tenant_id AND scope.channel_account_id = commerce_coupon_grants.channel_account_id AND scope.business_type = ?))", tenantID, channelID, customerID, "available", now, businessType, businessType).Updates(map[string]interface{}{"status": "expired"}).Error; err != nil {
			return err
		}
		q := tx.Where("tenant_id = ? AND channel_account_id = ? AND customer_id = ? AND status = ? AND expires_at > ? AND (business_type = ? OR EXISTS (SELECT 1 FROM commerce_coupon_grant_business_types scope WHERE scope.grant_id = commerce_coupon_grants.id AND scope.tenant_id = commerce_coupon_grants.tenant_id AND scope.channel_account_id = commerce_coupon_grants.channel_account_id AND scope.business_type = ?))", tenantID, channelID, customerID, "available", now, businessType, businessType)
		if err := q.Order("expires_at ASC, id ASC").Find(&rows).Error; err != nil {
			return err
		}
		for index := range rows {
			values, err := promotionBusinessTypesForGrant(tx, &rows[index])
			if err != nil {
				return err
			}
			rows[index].BusinessTypes = values
		}
		return nil
	})
	return rows, err
}

func (s *CommercePromotionService) ReserveCouponTx(tx *gorm.DB, tenantID, orderID uint, customerID, businessType string, channelID uint, goodsSubtotalCents int64) (*model.CommerceCouponGrant, error) {
	if tx == nil || tenantID == 0 || orderID == 0 || strings.TrimSpace(customerID) == "" || !promotionBusinessType(businessType) || goodsSubtotalCents < 0 {
		return nil, ErrCommercePromotionInvalid
	}
	if err := s.checkScope(tx, tenantID, channelID, businessType); err != nil {
		return nil, err
	}
	now := s.now()
	var grant model.CommerceCouponGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_account_id = ? AND customer_id = ? AND status = ? AND expires_at > ? AND (business_type = ? OR EXISTS (SELECT 1 FROM commerce_coupon_grant_business_types scope WHERE scope.grant_id = commerce_coupon_grants.id AND scope.tenant_id = commerce_coupon_grants.tenant_id AND scope.channel_account_id = commerce_coupon_grants.channel_account_id AND scope.business_type = ?))", tenantID, channelID, customerID, "available", now, businessType, businessType).Order("expires_at ASC, id ASC").First(&grant).Error; err != nil {
		return nil, ErrCouponNotOwned
	}
	return s.reserveCouponGrantTx(tx, tenantID, orderID, grant.ID, customerID, businessType, channelID, goodsSubtotalCents, now)
}

// ReserveCouponGrantTx reserves the exact customer grant selected by the
// caller. Checkout must use this method when a customer explicitly selected a
// grant_id; silently falling back to another grant would apply a different
// reward than the one quoted to the customer.
//
// The grant row is locked before any state transition. A retry for the same
// order and grant is idempotent, while a grant reserved or consumed by another
// order returns ErrCouponAlreadyReserved. Scope and customer checks happen on
// the locked row so a forged grant ID cannot cross tenant, channel, business,
// or customer boundaries.
func (s *CommercePromotionService) ReserveCouponGrantTx(tx *gorm.DB, tenantID, orderID, grantID uint, customerID, businessType string, channelID uint, goodsSubtotalCents int64) (*model.CommerceCouponGrant, error) {
	if tx == nil || tenantID == 0 || orderID == 0 || grantID == 0 || strings.TrimSpace(customerID) == "" || !promotionBusinessType(businessType) || goodsSubtotalCents < 0 {
		return nil, ErrCommercePromotionInvalid
	}
	if err := s.checkScope(tx, tenantID, channelID, strings.TrimSpace(businessType)); err != nil {
		return nil, err
	}
	return s.reserveCouponGrantTx(tx, tenantID, orderID, grantID, customerID, businessType, channelID, goodsSubtotalCents, s.now())
}

func (s *CommercePromotionService) reserveCouponGrantTx(tx *gorm.DB, tenantID, orderID, grantID uint, customerID, businessType string, channelID uint, goodsSubtotalCents int64, now time.Time) (*model.CommerceCouponGrant, error) {
	customerID = strings.TrimSpace(customerID)
	businessType = strings.TrimSpace(businessType)
	var grant model.CommerceCouponGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", grantID, tenantID).First(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCouponNotOwned
		}
		return nil, err
	}
	if grant.ChannelAccountID != channelID || grant.CustomerID != customerID {
		return nil, ErrCouponNotOwned
	}
	grantTypes, err := promotionBusinessTypesForGrant(tx, &grant)
	if err != nil || !containsPromotionBusinessType(grantTypes, businessType) {
		return nil, ErrCouponNotOwned
	}
	if grant.Status == "available" && !now.Before(grant.ExpiresAt) {
		if err := tx.Model(&grant).Update("status", "expired").Error; err != nil {
			return nil, err
		}
		return nil, ErrCouponNotOwned
	}
	if goodsSubtotalCents < grant.MinGoodsSubtotalCents {
		return nil, ErrCouponNotApplicable
	}
	discount := grant.DiscountCents
	if discount > goodsSubtotalCents {
		discount = goodsSubtotalCents
	}
	if discount <= 0 {
		return nil, ErrCouponNotApplicable
	}
	if grant.Status == "reserved" || grant.Status == "used" {
		if grant.Status == "reserved" && grant.ReservedOrderID == orderID {
			return &grant, nil
		}
		if grant.Status == "used" && grant.UsedOrderID == orderID {
			return &grant, nil
		}
		return nil, ErrCouponAlreadyReserved
	}
	if grant.Status != "available" {
		return nil, ErrCouponNotOwned
	}
	if err := tx.Model(&grant).Updates(map[string]interface{}{"status": "reserved", "reserved_order_id": orderID, "reserved_at": now}).Error; err != nil {
		return nil, err
	}
	grant.Status, grant.ReservedOrderID, grant.ReservedAt = "reserved", orderID, &now
	grant.BusinessTypes = grantTypes
	return &grant, nil
}

func (s *CommercePromotionService) ConsumeCouponTx(tx *gorm.DB, tenantID, orderID uint) error {
	return s.transitionCouponTx(tx, tenantID, orderID, "reserved", "used")
}
func (s *CommercePromotionService) ReleaseCouponTx(tx *gorm.DB, tenantID, orderID uint) error {
	return s.transitionCouponTx(tx, tenantID, orderID, "reserved", "available")
}

func (s *CommercePromotionService) transitionCouponTx(tx *gorm.DB, tenantID, orderID uint, from, to string) error {
	if tx == nil || tenantID == 0 || orderID == 0 {
		return ErrCommercePromotionInvalid
	}
	now := s.now()
	var grant model.CommerceCouponGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND reserved_order_id = ?", tenantID, orderID).First(&grant).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if grant.Status != from {
		if grant.Status == to {
			return nil
		}
		return ErrCouponAlreadyReserved
	}
	updates := map[string]interface{}{"status": to}
	if to == "available" {
		updates["reserved_order_id"] = 0
		updates["reserved_at"] = nil
	}
	if to == "used" {
		updates["used_order_id"] = orderID
		updates["used_at"] = now
	}
	return tx.Model(&grant).Updates(updates).Error
}

func (s *CommercePromotionService) ReturnCouponAfterFullUnfulfilledRefundTx(tx *gorm.DB, tenantID, orderID uint) error {
	if tx == nil || tenantID == 0 || orderID == 0 {
		return ErrCommercePromotionInvalid
	}
	now := s.now()
	var grant model.CommerceCouponGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND (reserved_order_id = ? OR used_order_id = ?)", tenantID, orderID, orderID).First(&grant).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if grant.RefundReturnPolicy != promotionRefundReturnPolicy || !now.Before(grant.ExpiresAt) {
		return nil
	}
	if grant.Status != "used" && grant.Status != "reserved" {
		return nil
	}
	return tx.Model(&grant).Updates(map[string]interface{}{"status": "available", "reserved_order_id": 0, "used_order_id": 0, "reserved_at": nil, "used_at": nil}).Error
}
