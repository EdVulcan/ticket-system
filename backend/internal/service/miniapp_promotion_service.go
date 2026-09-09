package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMiniappPromotionUnavailable = errors.New("miniapp instant discount is unavailable")
	ErrMiniappPromotionMapping     = errors.New("miniapp instant discount mapping is unavailable")
	ErrMiniappPromotionQuote       = errors.New("miniapp instant discount quote is invalid")
)

type MiniappPromotionService struct {
	Now       func() time.Time
	RandomInt func(max int64) (int64, error)
}

type MiniappInstantDiscountConfig struct {
	ActorUserID      uint   `json:"-"`
	Enabled          bool   `json:"enabled"`
	MinDiscountCents int64  `json:"min_discount_cents"`
	MaxDiscountCents int64  `json:"max_discount_cents"`
	ValidityMinutes  int    `json:"validity_minutes"`
	CooldownDays     int    `json:"cooldown_days"`
	MappingIDs       []uint `json:"mapping_ids"`
}

type MiniappInstantDiscountProduct struct {
	ID         uint   `json:"id"`
	MappingID  uint   `json:"mapping_id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	Eligible   bool   `json:"eligible"`
	Reason     string `json:"reason,omitempty"`
}

type MiniappInstantDiscountAdminView struct {
	MiniappInstantDiscountConfig
	Products []MiniappInstantDiscountProduct `json:"products"`
}

type MiniappPromotionOpportunity struct {
	Status             string     `json:"status"`
	GrantID            uint       `json:"grant_id,omitempty"`
	DiscountCents      int64      `json:"discount_cents"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	NextEligibleAt     *time.Time `json:"next_eligible_at,omitempty"`
	ServerNow          time.Time  `json:"server_now"`
	EligibleMappingIDs []uint     `json:"eligible_mapping_ids"`
	ReservedOrderNo    string     `json:"reserved_order_no,omitempty"`
}

type MiniappPromotionQuote struct {
	MappingID           uint                        `json:"-"`
	OriginalAmountCents int64                       `json:"original_amount_cents"`
	DiscountCents       int64                       `json:"discount_cents"`
	AmountCents         int64                       `json:"amount_cents"`
	QuoteToken          string                      `json:"quote_token"`
	Promotion           MiniappPromotionOpportunity `json:"promotion"`
}

func (s MiniappPromotionService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s MiniappPromotionService) randomInt(max int64) (int64, error) {
	if max <= 0 {
		return 0, errors.New("random upper bound must be positive")
	}
	if s.RandomInt != nil {
		value, err := s.RandomInt(max)
		if err != nil {
			return 0, err
		}
		if value < 0 || value >= max {
			return 0, errors.New("random result is outside the requested range")
		}
		return value, nil
	}
	value, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return 0, err
	}
	return value.Int64(), nil
}

func (s MiniappPromotionService) GetConfig(tenantID, accountID uint) (*MiniappInstantDiscountAdminView, error) {
	if err := loadMiniappPromotionAccount(model.DB, tenantID, accountID, nil, false); err != nil {
		return nil, err
	}
	return s.adminView(model.DB, tenantID, accountID)
}

func (s MiniappPromotionService) SaveConfig(tenantID, accountID uint, input MiniappInstantDiscountConfig) (*MiniappInstantDiscountAdminView, error) {
	input.MappingIDs = normalizedPromotionMappingIDs(input.MappingIDs)
	if err := validateMiniappPromotionConfig(input); err != nil {
		return nil, err
	}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := loadMiniappPromotionAccount(tx, tenantID, accountID, nil, true); err != nil {
			return err
		}
		var activity model.MiniappInstantDiscountActivity
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_account_id = ?", tenantID, accountID).First(&activity).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			activity = model.MiniappInstantDiscountActivity{TenantID: tenantID, ChannelAccountID: accountID}
			if err := tx.Create(&activity).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		before, err := s.adminView(tx, tenantID, accountID)
		if err != nil {
			return err
		}
		if input.Enabled {
			if err := validateMiniappPromotionMappingsTx(tx, tenantID, accountID, input.MappingIDs); err != nil {
				return err
			}
		} else {
			for _, id := range input.MappingIDs {
				var mapping model.ChannelProductMapping
				if err := tx.Where("id = ? AND channel_account_id = ?", id, accountID).First(&mapping).Error; err != nil {
					return ErrMiniappPromotionMapping
				}
			}
		}
		if err := tx.Model(&activity).Updates(map[string]interface{}{
			"enabled": input.Enabled, "min_discount_cents": input.MinDiscountCents,
			"max_discount_cents": input.MaxDiscountCents, "validity_minutes": input.ValidityMinutes,
			"cooldown_days": input.CooldownDays,
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("activity_id = ?", activity.ID).Delete(&model.MiniappInstantDiscountActivityMapping{}).Error; err != nil {
			return err
		}
		for _, mappingID := range input.MappingIDs {
			if err := tx.Create(&model.MiniappInstantDiscountActivityMapping{ActivityID: activity.ID, ChannelProductMappingID: mappingID}).Error; err != nil {
				return err
			}
		}
		beforeJSON, err := json.Marshal(before.MiniappInstantDiscountConfig)
		if err != nil {
			return err
		}
		afterJSON, err := json.Marshal(input)
		if err != nil {
			return err
		}
		return tx.Create(&model.AuditLog{ActorUserID: input.ActorUserID, Scope: "tenant", TenantID: tenantID, Action: "xiaohongshu.instant_discount.configure", TargetType: "channel_account", TargetID: accountID, BeforeJSON: string(beforeJSON), AfterJSON: string(afterJSON)}).Error
	}); err != nil {
		return nil, err
	}
	return s.adminView(model.DB, tenantID, accountID)
}

func validateMiniappPromotionConfig(input MiniappInstantDiscountConfig) error {
	if input.MinDiscountCents < 0 || input.MaxDiscountCents < input.MinDiscountCents || input.MaxDiscountCents > 9999999999 || input.ValidityMinutes < 0 || input.ValidityMinutes > 525600 || input.CooldownDays < 0 || input.CooldownDays > 3650 {
		return errors.New("请填写有效的立减金额范围、有效分钟数和领取间隔")
	}
	if input.Enabled && (input.MinDiscountCents <= 0 || input.MaxDiscountCents <= 0 || input.ValidityMinutes <= 0 || len(input.MappingIDs) == 0) {
		return errors.New("启用活动需要设置立减金额、有效期并选择参与商品")
	}
	return nil
}

func normalizedPromotionMappingIDs(ids []uint) []uint {
	set := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id != 0 {
			set[id] = struct{}{}
		}
	}
	result := make([]uint, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func loadMiniappPromotionAccount(tx *gorm.DB, tenantID, accountID uint, account *model.ChannelAccount, lock bool) error {
	if tenantID == 0 || accountID == 0 || requireAnyActiveTenantCapability(tx, tenantID, "supplier", "distributor") != nil {
		return ErrMiniappPromotionUnavailable
	}
	query := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"})
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var loaded model.ChannelAccount
	if err := query.First(&loaded).Error; err != nil {
		return ErrMiniappPromotionUnavailable
	}
	if account != nil {
		*account = loaded
	}
	return nil
}

func validateMiniappPromotionMappingsTx(tx *gorm.DB, tenantID, accountID uint, mappingIDs []uint) error {
	for _, mappingID := range mappingIDs {
		if _, err := loadMiniappPromotionMappingTx(tx, tenantID, accountID, mappingID, true); err != nil {
			return ErrMiniappPromotionMapping
		}
	}
	return nil
}

func loadMiniappPromotionMappingTx(tx *gorm.DB, tenantID, accountID, mappingID uint, lock bool) (*model.ChannelProductMapping, error) {
	if mappingID == 0 {
		return nil, ErrMiniappPromotionMapping
	}
	query := tx.Where("id = ? AND channel_account_id = ? AND status = ?", mappingID, accountID, "active")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var mapping model.ChannelProductMapping
	if err := query.First(&mapping).Error; err != nil || mapping.ChannelSaleCents <= 0 {
		return nil, ErrMiniappPromotionMapping
	}
	productQuery := tx.Where("id = ? AND tenant_id = ? AND status = ? AND product_kind = ?", mapping.ProductID, tenantID, "online", "ticket")
	if lock {
		productQuery = productQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var product model.Product
	if err := productQuery.First(&product).Error; err != nil {
		return nil, ErrMiniappPromotionMapping
	}
	var config model.XiaohongshuProductConfig
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND channel_product_mapping_id = ? AND sync_status IN ? AND audit_status = ?", tenantID, accountID, mapping.ID, []string{"submitted", "synced"}, "approved").First(&config).Error; err != nil || config.ProductType != 1 {
		return nil, ErrMiniappPromotionMapping
	}
	return &mapping, nil
}

// loadMiniappQuoteMappingTx covers the existing miniapp catalog surface. A
// valid, nonparticipating package remains purchasable at its ordinary channel
// price; only the promotion eligibility check is narrower.
func loadMiniappQuoteMappingTx(tx *gorm.DB, tenantID, accountID, mappingID uint, lock bool) (*model.ChannelProductMapping, error) {
	if mappingID == 0 {
		return nil, ErrMiniappPromotionQuote
	}
	query := tx.Where("id = ? AND channel_account_id = ? AND status = ?", mappingID, accountID, "active")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var mapping model.ChannelProductMapping
	if err := query.First(&mapping).Error; err != nil || mapping.ChannelSaleCents <= 0 {
		return nil, ErrMiniappPromotionQuote
	}
	productQuery := tx.Where("id = ? AND tenant_id = ? AND status = ? AND product_kind <> ?", mapping.ProductID, tenantID, "online", "hotel")
	if lock {
		productQuery = productQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var product model.Product
	if err := productQuery.First(&product).Error; err != nil {
		return nil, ErrMiniappPromotionQuote
	}
	var config model.XiaohongshuProductConfig
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND channel_product_mapping_id = ? AND sync_status IN ? AND audit_status = ?", tenantID, accountID, mapping.ID, []string{"submitted", "synced"}, "approved").First(&config).Error; err != nil {
		return nil, ErrMiniappPromotionQuote
	}
	return &mapping, nil
}

// AcquireOpportunity is the only route that creates a random offer. Reads and
// quotes never create grants, preventing redraws from page refreshes.
func (s MiniappPromotionService) AcquireOpportunity(customer *model.MiniappCustomer) (*MiniappPromotionOpportunity, error) {
	if customer == nil || customer.ID == 0 || customer.TenantID == 0 || customer.ChannelAccountID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	var result MiniappPromotionOpportunity
	err := model.Write(func(tx *gorm.DB) error {
		lockedCustomer, err := lockMiniappPromotionCustomerTx(tx, customer)
		if err != nil {
			return err
		}
		activity, err := lockMiniappPromotionActivityTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID)
		if err != nil {
			return err
		}
		result, err = s.opportunityTx(tx, lockedCustomer, activity, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// QuoteForCustomer derives the only permitted base amount from the scoped
// channel mapping. It deliberately has no price input from the storefront.
func (s MiniappPromotionService) QuoteForCustomer(customer *model.MiniappCustomer, mappingID uint, quantity int) (*MiniappPromotionQuote, error) {
	if customer == nil || customer.ID == 0 || quantity <= 0 || quantity > 100 {
		return nil, ErrMiniappPromotionQuote
	}
	var quote *MiniappPromotionQuote
	err := model.Write(func(tx *gorm.DB) error {
		lockedCustomer, err := lockMiniappPromotionCustomerTx(tx, customer)
		if err != nil {
			return err
		}
		// The quote path is intentionally read-only with respect to grants; it
		// may lock a current grant but cannot draw a new one.
		if _, err := lockMiniappPromotionActivityTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID); err != nil {
			return err
		}
		mapping, err := loadMiniappQuoteMappingTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID, mappingID, true)
		if err != nil {
			return ErrMiniappPromotionQuote
		}
		quote, err = s.LoadLockedQuoteTx(tx, lockedCustomer, mappingID, quantity, mapping.ChannelSaleCents*int64(quantity))
		return err
	})
	if err != nil {
		return nil, err
	}
	return quote, nil
}

// LoadLockedQuoteTx is used inside the core order transaction. Lock ordering
// is customer -> activity -> mapping/product -> grant.
func (s MiniappPromotionService) LoadLockedQuoteTx(tx *gorm.DB, customer *model.MiniappCustomer, mappingID uint, quantity int, baseCents int64) (*MiniappPromotionQuote, error) {
	if tx == nil || customer == nil || quantity <= 0 || baseCents <= 0 {
		return nil, ErrMiniappPromotionQuote
	}
	lockedCustomer, err := lockMiniappPromotionCustomerTx(tx, customer)
	if err != nil {
		return nil, err
	}
	activity, err := lockMiniappPromotionActivityTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID)
	if err != nil {
		return nil, err
	}
	mapping, mappingErr := loadMiniappQuoteMappingTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID, mappingID, true)
	if mappingErr != nil || mapping.ChannelSaleCents*int64(quantity) != baseCents {
		return nil, ErrMiniappPromotionQuote
	}
	opportunity, err := s.opportunityTx(tx, lockedCustomer, activity, false)
	if err != nil {
		return nil, err
	}
	quote := &MiniappPromotionQuote{MappingID: mappingID, OriginalAmountCents: baseCents, AmountCents: baseCents, Promotion: opportunity}
	if activity == nil || !activity.Enabled || !containsPromotionMapping(opportunity.EligibleMappingIDs, mappingID) || opportunity.Status != "available" {
		quote.QuoteToken = s.quoteToken(lockedCustomer, mappingID, quantity, quote)
		return quote, nil
	}
	if _, err := loadMiniappPromotionMappingTx(tx, lockedCustomer.TenantID, lockedCustomer.ChannelAccountID, mappingID, true); err != nil {
		quote.QuoteToken = s.quoteToken(lockedCustomer, mappingID, quantity, quote)
		return quote, nil
	}
	if opportunity.GrantID == 0 || opportunity.DiscountCents <= 0 {
		return nil, ErrMiniappPromotionQuote
	}
	maxDiscount := baseCents - int64(quantity)
	var listing model.Product
	if err := tx.Select("id", "product_offer_id").Where("id = ? AND tenant_id = ?", mapping.ProductID, customer.TenantID).First(&listing).Error; err != nil {
		return nil, err
	}
	if listing.ProductOfferID > 0 {
		var offer model.ProductOffer
		if err := tx.Where("id = ? AND distributor_tenant_id = ? AND status = ?", listing.ProductOfferID, customer.TenantID, "active").First(&offer).Error; err != nil {
			return nil, ErrMiniappPromotionQuote
		}
		if offer.MinimumRetailPriceCents > 1 {
			maxDiscount = baseCents - offer.MinimumRetailPriceCents*int64(quantity)
		}
	}
	if maxDiscount < 0 {
		return nil, ErrMiniappPromotionQuote
	}
	quote.DiscountCents = minPromotionCents(opportunity.DiscountCents, maxDiscount)
	quote.AmountCents = baseCents - quote.DiscountCents
	quote.QuoteToken = s.quoteToken(lockedCustomer, mappingID, quantity, quote)
	return quote, nil
}

func (s MiniappPromotionService) ValidateQuoteToken(customer *model.MiniappCustomer, mappingID uint, quantity int, quote *MiniappPromotionQuote, token string) bool {
	if customer == nil || quote == nil || strings.TrimSpace(token) == "" {
		return false
	}
	return hmac.Equal([]byte(s.quoteToken(customer, mappingID, quantity, quote)), []byte(strings.TrimSpace(token)))
}

func (s MiniappPromotionService) quoteToken(customer *model.MiniappCustomer, mappingID uint, quantity int, quote *MiniappPromotionQuote) string {
	secret := []byte(config.GlobalConfig.Security.JWTSecret)
	if len(secret) == 0 {
		secret = []byte(config.GlobalConfig.Security.EncryptionKey)
	}
	if len(secret) == 0 {
		// A quote without a configured server secret must not be accepted by
		// order creation. Production config validation supplies at least one.
		return ""
	}
	hash := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(hash, "promotion-v1|%d|%d|%d|%d|%d|%d|%d", customer.ID, customer.TenantID, customer.ChannelAccountID, mappingID, quantity, quote.OriginalAmountCents, quote.DiscountCents)
	_, _ = fmt.Fprintf(hash, "|%d", quote.Promotion.GrantID)
	return hex.EncodeToString(hash.Sum(nil))
}

// ReserveGrantForOrderTx is called after core order creation in the same
// transaction. It never redraws and refuses expired, inactive, or remapped
// grants at the commit boundary.
func (s MiniappPromotionService) ReserveGrantForOrderTx(tx *gorm.DB, order *model.Order, quote *MiniappPromotionQuote) error {
	if tx == nil || order == nil || quote == nil || quote.DiscountCents <= 0 || quote.Promotion.GrantID == 0 || order.ID == 0 || order.Channel != "xiaohongshu" {
		return ErrMiniappPromotionQuote
	}
	if order.TenantID == 0 || order.ChannelAccountID == 0 || order.OriginalAmountCents != quote.OriginalAmountCents || order.DiscountCents != quote.DiscountCents || order.PromotionGrantID != quote.Promotion.GrantID {
		return ErrMiniappPromotionQuote
	}
	var grant model.MiniappInstantDiscountGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ?", quote.Promotion.GrantID, order.TenantID, order.ChannelAccountID).First(&grant).Error; err != nil {
		return ErrMiniappPromotionQuote
	}
	now := s.now()
	if grant.ReservedOrderID != 0 || grant.ConsumedAt != nil || !grant.ExpiresAt.After(now) || grant.DiscountCents != quote.Promotion.DiscountCents {
		return ErrMiniappPromotionQuote
	}
	var activity model.MiniappInstantDiscountActivity
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND enabled = ?", grant.ActivityID, order.TenantID, order.ChannelAccountID, true).First(&activity).Error; err != nil {
		return ErrMiniappPromotionQuote
	}
	if quote.MappingID == 0 || tx.Where("activity_id = ? AND channel_product_mapping_id = ?", activity.ID, quote.MappingID).First(&model.MiniappInstantDiscountActivityMapping{}).Error != nil {
		return ErrMiniappPromotionQuote
	}
	updated := tx.Model(&grant).Where("reserved_order_id = ? AND consumed_at IS NULL", 0).Updates(map[string]interface{}{"reserved_order_id": order.ID, "reserved_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrMiniappPromotionQuote
	}
	return nil
}

// ReleaseGrantForOrderTx and ConsumeGrantForOrderTx lock only order-scoped
// grant rows. They deliberately do not lock customer or activity, avoiding a
// cancellation-time inversion with order creation.
func (s MiniappPromotionService) ReleaseGrantForOrderTx(tx *gorm.DB, order *model.Order) error {
	if tx == nil || order == nil || order.ID == 0 || order.TenantID == 0 || order.ChannelAccountID == 0 {
		return ErrMiniappPromotionQuote
	}
	updated := tx.Model(&model.MiniappInstantDiscountGrant{}).
		Where("tenant_id = ? AND channel_account_id = ? AND reserved_order_id = ? AND consumed_at IS NULL", order.TenantID, order.ChannelAccountID, order.ID).
		Updates(map[string]interface{}{"reserved_order_id": 0, "reserved_at": nil})
	if updated.Error != nil {
		return updated.Error
	}
	if order.PromotionGrantID != 0 && updated.RowsAffected != 1 {
		return ErrMiniappPromotionQuote
	}
	return nil
}

func (s MiniappPromotionService) ConsumeGrantForOrderTx(tx *gorm.DB, order *model.Order) error {
	if tx == nil || order == nil || order.ID == 0 || order.TenantID == 0 || order.ChannelAccountID == 0 {
		return ErrMiniappPromotionQuote
	}
	var grant model.MiniappInstantDiscountGrant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND reserved_order_id = ?", order.PromotionGrantID, order.TenantID, order.ChannelAccountID, order.ID).First(&grant).Error; err != nil {
		return ErrMiniappPromotionQuote
	}
	if grant.ConsumedAt != nil {
		return nil
	}
	return tx.Model(&grant).Update("consumed_at", s.now()).Error
}

func lockMiniappPromotionCustomerTx(tx *gorm.DB, customer *model.MiniappCustomer) (*model.MiniappCustomer, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	if err := loadMiniappPromotionAccount(tx, customer.TenantID, customer.ChannelAccountID, nil, false); err != nil {
		return nil, err
	}
	var locked model.MiniappCustomer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND status = ?", customer.ID, customer.TenantID, customer.ChannelAccountID, "active").First(&locked).Error; err != nil {
		return nil, ErrMiniappUnauthenticated
	}
	return &locked, nil
}

func lockMiniappPromotionActivityTx(tx *gorm.DB, tenantID, accountID uint) (*model.MiniappInstantDiscountActivity, error) {
	var activity model.MiniappInstantDiscountActivity
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_account_id = ?", tenantID, accountID).First(&activity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &activity, nil
}

func (s MiniappPromotionService) opportunityTx(tx *gorm.DB, customer *model.MiniappCustomer, activity *model.MiniappInstantDiscountActivity, create bool) (MiniappPromotionOpportunity, error) {
	now := s.now()
	result := MiniappPromotionOpportunity{Status: "inactive", ServerNow: now}
	if activity == nil || !activity.Enabled {
		return result, nil
	}
	mappingIDs, err := promotionMappingIDsTx(tx, activity.ID)
	if err != nil {
		return result, err
	}
	result.EligibleMappingIDs = mappingIDs
	if len(mappingIDs) == 0 {
		return result, nil
	}
	var grant model.MiniappInstantDiscountGrant
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND channel_account_id = ? AND miniapp_customer_id = ?", customer.TenantID, customer.ChannelAccountID, customer.ID).
		Order("obtained_at DESC, id DESC").First(&grant).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if !create {
			return result, nil
		}
		if activity.MinDiscountCents <= 0 || activity.MaxDiscountCents < activity.MinDiscountCents || activity.ValidityMinutes <= 0 || activity.CooldownDays < 0 {
			return result, nil
		}
		offset, randomErr := s.randomInt(activity.MaxDiscountCents - activity.MinDiscountCents + 1)
		if randomErr != nil {
			return result, randomErr
		}
		expires := now.Add(time.Duration(activity.ValidityMinutes) * time.Minute)
		next := now.AddDate(0, 0, activity.CooldownDays)
		grant = model.MiniappInstantDiscountGrant{TenantID: customer.TenantID, ChannelAccountID: customer.ChannelAccountID, MiniappCustomerID: customer.ID, ActivityID: activity.ID, DiscountCents: activity.MinDiscountCents + offset, ObtainedAt: now, ExpiresAt: expires, NextEligibleAt: next}
		if err := tx.Create(&grant).Error; err != nil {
			return result, err
		}
	} else if !now.Before(grant.NextEligibleAt) && (grant.ConsumedAt != nil || (grant.ReservedOrderID == 0 && !grant.ExpiresAt.After(now))) {
		if !create {
			return result, nil
		}
		offset, randomErr := s.randomInt(activity.MaxDiscountCents - activity.MinDiscountCents + 1)
		if randomErr != nil {
			return result, randomErr
		}
		expires := now.Add(time.Duration(activity.ValidityMinutes) * time.Minute)
		next := now.AddDate(0, 0, activity.CooldownDays)
		grant = model.MiniappInstantDiscountGrant{TenantID: customer.TenantID, ChannelAccountID: customer.ChannelAccountID, MiniappCustomerID: customer.ID, ActivityID: activity.ID, DiscountCents: activity.MinDiscountCents + offset, ObtainedAt: now, ExpiresAt: expires, NextEligibleAt: next}
		if err := tx.Create(&grant).Error; err != nil {
			return result, err
		}
	}
	result.GrantID, result.DiscountCents = grant.ID, grant.DiscountCents
	result.ExpiresAt, result.NextEligibleAt = &grant.ExpiresAt, &grant.NextEligibleAt
	if grant.ReservedOrderID != 0 && grant.ConsumedAt == nil {
		result.Status = "reserved"
		var order model.Order
		if err := tx.Select("order_no").Where("id = ? AND tenant_id = ? AND channel_account_id = ?", grant.ReservedOrderID, grant.TenantID, grant.ChannelAccountID).First(&order).Error; err == nil {
			result.ReservedOrderNo = order.OrderNo
		}
		return result, nil
	}
	if grant.ConsumedAt != nil {
		result.Status = "cooldown"
		return result, nil
	}
	if !grant.ExpiresAt.After(now) {
		result.Status = "expired"
		return result, nil
	}
	result.Status = "available"
	return result, nil
}

func promotionMappingIDsTx(tx *gorm.DB, activityID uint) ([]uint, error) {
	var ids []uint
	if err := tx.Model(&model.MiniappInstantDiscountActivityMapping{}).Where("activity_id = ?", activityID).Order("channel_product_mapping_id ASC").Pluck("channel_product_mapping_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func containsPromotionMapping(ids []uint, id uint) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

func minPromotionCents(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func (s MiniappPromotionService) adminView(tx *gorm.DB, tenantID, accountID uint) (*MiniappInstantDiscountAdminView, error) {
	view := &MiniappInstantDiscountAdminView{MiniappInstantDiscountConfig: MiniappInstantDiscountConfig{MappingIDs: []uint{}}, Products: []MiniappInstantDiscountProduct{}}
	var activity model.MiniappInstantDiscountActivity
	if err := tx.Where("tenant_id = ? AND channel_account_id = ?", tenantID, accountID).First(&activity).Error; err == nil {
		view.Enabled, view.MinDiscountCents, view.MaxDiscountCents = activity.Enabled, activity.MinDiscountCents, activity.MaxDiscountCents
		view.ValidityMinutes, view.CooldownDays = activity.ValidityMinutes, activity.CooldownDays
		ids, idsErr := promotionMappingIDsTx(tx, activity.ID)
		if idsErr != nil {
			return nil, idsErr
		}
		view.MappingIDs = ids
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	type row struct {
		ID               uint
		DisplayName      string
		ChannelSaleCents int64
		Status           string
		ProductID        uint
		ProductName      string
		ProductStatus    string
		ProductKind      string
		AuditStatus      string
		SyncStatus       string
		CategoryID       string
		ProductType      int
	}
	var rows []row
	if err := tx.Table("channel_product_mappings AS mapping").Select("mapping.id, mapping.display_name, mapping.channel_sale_cents, mapping.status, mapping.product_id, product.name AS product_name, product.status AS product_status, product.product_kind, config.audit_status, config.sync_status, config.category_id, config.product_type").Joins("JOIN products AS product ON product.id = mapping.product_id AND product.deleted_at IS NULL").Joins("LEFT JOIN xiaohongshu_product_configs AS config ON config.channel_product_mapping_id = mapping.id AND config.tenant_id = ? AND config.channel_account_id = ? AND config.deleted_at IS NULL", tenantID, accountID).Where("mapping.channel_account_id = ? AND mapping.deleted_at IS NULL", accountID).Order("mapping.id ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		name := strings.TrimSpace(row.DisplayName)
		if name == "" {
			name = row.ProductName
		}
		product := MiniappInstantDiscountProduct{ID: row.ID, MappingID: row.ID, Name: name, PriceCents: row.ChannelSaleCents, Eligible: true}
		switch {
		case row.Status != "active":
			product.Eligible, product.Reason = false, "商品映射已停用"
		case row.ChannelSaleCents <= 0:
			product.Eligible, product.Reason = false, "商品售价未配置"
		case row.ProductStatus != "online" || row.ProductKind != "ticket":
			product.Eligible, product.Reason = false, "当前仅支持在售的普通门票"
		case row.AuditStatus != "approved" || (row.SyncStatus != "submitted" && row.SyncStatus != "synced") || row.ProductType != 1:
			product.Eligible, product.Reason = false, "商品尚未通过审核或不是普通团购券"
		}
		view.Products = append(view.Products, product)
	}
	return view, nil
}
