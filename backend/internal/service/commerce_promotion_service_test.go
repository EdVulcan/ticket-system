package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"ticket-backend/internal/model"
)

func ensureCommercePromotionSchema(t *testing.T) {
	t.Helper()
	if err := model.DB.AutoMigrate(
		&model.CommerceCouponTemplate{},
		&model.CommerceCouponTemplateBusinessType{},
		&model.CommerceCouponGrant{},
		&model.CommerceCouponGrantBusinessType{},
		&model.CommerceAssistCampaign{},
		&model.CommerceAssistCampaignBusinessType{},
		&model.CommerceAssistSession{},
		&model.CommerceAssistRecord{},
	); err != nil {
		t.Fatalf("migrate promotion schema: %v", err)
	}
}

func resetCommercePromotionData(t *testing.T) {
	t.Helper()
	for _, table := range []interface{}{
		&model.CommerceAssistRecord{}, &model.CommerceAssistSession{},
		&model.CommerceAssistCampaignBusinessType{}, &model.CommerceAssistCampaign{},
		&model.CommerceCouponGrantBusinessType{}, &model.CommerceCouponGrant{},
		&model.CommerceCouponTemplateBusinessType{}, &model.CommerceCouponTemplate{},
	} {
		if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error; err != nil {
			t.Fatalf("reset promotion table %T: %v", table, err)
		}
	}
}

func TestCommercePromotionBusinessScopesShareStoreChannelAndSnapshotGrantScope(t *testing.T) {
	ensureCommercePromotionSchema(t)
	if !promotionScopeIncludesBusinessType(nil, "restaurant") {
		t.Fatal("legacy singular business_type should satisfy scoped validation")
	}
	if promotionScopeIncludesBusinessType([]string{"retail"}, "restaurant") {
		t.Fatal("a different normalized business scope must be rejected")
	}
	tenantID := newCommerceTenant(t, "restaurant", "active")
	resetCommercePromotionData(t)
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenantID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatalf("enable retail capability: %v", err)
	}
	channel := &model.ChannelAccount{
		TenantID: tenantID,
		Code:     fmt.Sprintf("promotion-shared-%d", time.Now().UnixNano()),
		Type:     "wechat_miniapp",
		Status:   "active",
	}
	if err := model.DB.Create(channel).Error; err != nil {
		t.Fatalf("create storefront channel: %v", err)
	}
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	end := now.Add(24 * time.Hour)
	service := &CommercePromotionService{DB: model.DB, Clock: func() time.Time { return now }}
	template, err := service.CreateCouponTemplate(tenantID, CreateCommerceCouponTemplateInput{
		ChannelAccountID: channel.ID, BusinessTypes: []string{"restaurant", "retail"}, Name: "shared reward",
		DiscountCents: 300, MinGoodsSubtotalCents: 1000, ValidDays: 1, StartsAt: &now, EndsAt: &end,
		Status: "active", PerCustomerCap: 2,
	})
	if err != nil {
		t.Fatalf("create shared template: %v", err)
	}
	if len(template.BusinessTypes) != 2 || template.BusinessTypes[0] != "restaurant" || template.BusinessTypes[1] != "retail" {
		t.Fatalf("template scope=%v", template.BusinessTypes)
	}
	campaign, err := service.CreateAssistCampaign(tenantID, CreateCommerceAssistCampaignInput{
		ChannelAccountID: channel.ID, BusinessTypes: []string{"restaurant"}, Title: "restaurant entry",
		StarterCouponTemplateID: template.ID, HelperCouponTemplateID: template.ID,
		RequiredUniqueHelpers: 1, PerStarterSessionLimit: 1, StartsAt: now, EndsAt: end, Status: "active",
	})
	if err != nil {
		t.Fatalf("create scoped campaign: %v", err)
	}
	session, err := service.CreateAssistSession(tenantID, campaign.ID, "starter", "shared-scope-request")
	if err != nil {
		t.Fatalf("create assist session: %v", err)
	}
	result, err := service.HelpAssist(tenantID, session.ShareToken, "helper")
	if err != nil {
		t.Fatalf("help assist: %v", err)
	}
	if len(result.View.HelperReward.BusinessTypes) != 2 || result.View.HelperReward.BusinessTypes[0] != "restaurant" || result.View.HelperReward.BusinessTypes[1] != "retail" {
		t.Fatalf("assist reward scope=%v, want template scope", result.View.HelperReward.BusinessTypes)
	}
	var grant model.CommerceCouponGrant
	if err := model.DB.First(&grant, result.HelperGrantID).Error; err != nil {
		t.Fatalf("load helper grant: %v", err)
	}
	grantTypes, err := promotionBusinessTypesForGrant(model.DB, &grant)
	if err != nil || len(grantTypes) != 2 || grantTypes[0] != "restaurant" || grantTypes[1] != "retail" {
		t.Fatalf("grant scope=%v err=%v, want template snapshot", grantTypes, err)
	}
	rows, err := service.ListAvailableCoupons(tenantID, channel.ID, "retail", "helper")
	if err != nil || len(rows) != 1 || rows[0].ID != grant.ID {
		t.Fatalf("cross-business coupon rows=%+v err=%v", rows, err)
	}
	if _, err := service.UpdateCouponTemplateForScope(CommercePromotionScope{
		TenantID: tenantID, ChannelAccountID: channel.ID, BusinessType: "restaurant",
	}, template.ID, UpdateCommerceCouponTemplateInput{
		BusinessTypes: []string{"retail"}, Name: template.Name, StartsAt: &now, EndsAt: &end,
		Status: "active", IssuanceCap: template.IssuanceCap, PerCustomerCap: template.PerCustomerCap,
	}); !errors.Is(err, ErrPromotionScopeDenied) {
		t.Fatalf("scoped update removing authorized business type err=%v", err)
	}
	var unchanged model.CommerceCouponTemplate
	if err := model.DB.First(&unchanged, template.ID).Error; err != nil {
		t.Fatalf("load unchanged template: %v", err)
	}
	unchangedTypes, err := promotionBusinessTypesForTemplate(model.DB, &unchanged)
	if err != nil || len(unchangedTypes) != 2 {
		t.Fatalf("template mutated after rejected update: types=%v err=%v", unchangedTypes, err)
	}
	if _, err := service.UpdateCouponTemplate(tenantID, template.ID, UpdateCommerceCouponTemplateInput{
		BusinessTypes: []string{"restaurant"}, Name: template.Name, StartsAt: &now, EndsAt: &end,
		Status: "active", IssuanceCap: template.IssuanceCap, PerCustomerCap: template.PerCustomerCap,
	}); err != nil {
		t.Fatalf("store-wide scope update: %v", err)
	}
	var retained model.CommerceCouponGrantBusinessType
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ? AND grant_id = ? AND business_type = ?", tenantID, channel.ID, grant.ID, "retail").First(&retained).Error; err != nil {
		t.Fatalf("issued grant scope was rewritten by template update: %v", err)
	}
}

func TestCommercePromotionAssistIdempotencyAndIsolation(t *testing.T) {
	ensureCommercePromotionSchema(t)
	tenantID := newCommerceTenant(t, "restaurant", "active")
	resetCommercePromotionData(t)
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	service := &CommercePromotionService{Clock: func() time.Time { return now }}
	windowStart, windowEnd := now.Add(-time.Hour), now.Add(time.Hour)
	starter, err := service.CreateCouponTemplate(tenantID, CreateCommerceCouponTemplateInput{
		BusinessType: "restaurant", Name: "starter", DiscountCents: 100,
		ValidDays: 1, PerCustomerCap: 1, StartsAt: &windowStart, EndsAt: &windowEnd, Status: "active",
	})
	if err != nil {
		t.Fatalf("create starter template: %v", err)
	}
	helper, err := service.CreateCouponTemplate(tenantID, CreateCommerceCouponTemplateInput{
		BusinessType: "restaurant", Name: "helper", DiscountCents: 80,
		ValidDays: 1, PerCustomerCap: 1, StartsAt: &windowStart, EndsAt: &windowEnd, Status: "active",
	})
	if err != nil {
		t.Fatalf("create helper template: %v", err)
	}
	campaign, err := service.CreateAssistCampaign(tenantID, CreateCommerceAssistCampaignInput{
		BusinessType: "restaurant", Title: "share", StarterCouponTemplateID: starter.ID,
		HelperCouponTemplateID: helper.ID, RequiredUniqueHelpers: 1,
		PerStarterSessionLimit: 1, StartsAt: windowStart, EndsAt: windowEnd, Status: "active",
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	first, err := service.CreateAssistSession(tenantID, campaign.ID, "starter-customer", "request-1")
	if err != nil || first.ShareToken == "" {
		t.Fatalf("create session result=%+v err=%v", first, err)
	}
	if first.View.Title != campaign.Title || first.View.BusinessType != "restaurant" || first.View.StarterReward.DiscountCents != 100 || first.View.HelperReward.DiscountCents != 80 {
		t.Fatalf("assist session did not expose authoritative reward terms: %+v", first.View)
	}
	retry, err := service.CreateAssistSession(tenantID, campaign.ID, "starter-customer", "request-1")
	if err != nil || retry.ShareToken != first.ShareToken || retry.View.ID != first.View.ID {
		t.Fatalf("idempotent retry result=%+v err=%v", retry, err)
	}
	if _, err := service.HelpAssist(tenantID, first.ShareToken, "starter-customer"); !errors.Is(err, ErrAssistSelfHelp) {
		t.Fatalf("self help err=%v", err)
	}
	if _, err := service.HelpAssist(tenantID, first.ShareToken, "helper-customer"); err != nil {
		t.Fatalf("help session: %v", err)
	}
	if _, err := service.HelpAssist(tenantID, first.ShareToken, "helper-customer"); !errors.Is(err, ErrAssistAlreadyHelped) {
		t.Fatalf("duplicate help err=%v", err)
	}
	foreignTenant := newCommerceTenant(t, "restaurant", "active")
	if _, err := service.GetAssistSession(foreignTenant, first.ShareToken, "helper-customer"); err == nil {
		t.Fatal("foreign tenant read unexpectedly succeeded")
	}
}

func TestCommercePromotionCouponLifecycleAndRefundReturn(t *testing.T) {
	ensureCommercePromotionSchema(t)
	tenantID := newCommerceTenant(t, "retail", "active")
	resetCommercePromotionData(t)
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	service := &CommercePromotionService{DB: model.DB, Clock: func() time.Time { return now }}
	end := now.Add(24 * time.Hour)
	template, err := service.CreateCouponTemplate(tenantID, CreateCommerceCouponTemplateInput{
		BusinessType: "retail", Name: "welcome", DiscountCents: 300,
		MinGoodsSubtotalCents: 1000, ValidDays: 1, PerCustomerCap: 1, StartsAt: &now, EndsAt: &end, Status: "active",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	grant := &model.CommerceCouponGrant{TenantID: tenantID, TemplateID: template.ID, BusinessType: "retail", CustomerID: "customer", Source: "manual", SourceIdentity: "manual-1", DiscountCents: 300, MinGoodsSubtotalCents: 1000, RefundReturnPolicy: promotionRefundReturnPolicy, Status: "available", ExpiresAt: end}
	if err := model.DB.Create(grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		reserved, reserveErr := service.ReserveCouponTx(tx, tenantID, 101, "customer", "retail", 0, 1200)
		if reserveErr != nil || reserved.ID != grant.ID || reserved.Status != "reserved" {
			return errors.Join(reserveErr, errors.New("coupon was not reserved"))
		}
		return service.ConsumeCouponTx(tx, tenantID, 101)
	}); err != nil {
		t.Fatalf("reserve and consume: %v", err)
	}
	var consumed model.CommerceCouponGrant
	if err := model.DB.First(&consumed, grant.ID).Error; err != nil || consumed.Status != "used" {
		t.Fatalf("consumed grant=%+v err=%v", consumed, err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return service.ReturnCouponAfterFullUnfulfilledRefundTx(tx, tenantID, 101)
	}); err != nil {
		t.Fatalf("refund return: %v", err)
	}
	var returned model.CommerceCouponGrant
	if err := model.DB.First(&returned, grant.ID).Error; err != nil || returned.Status != "available" || returned.UsedOrderID != 0 {
		t.Fatalf("returned grant=%+v err=%v", returned, err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return service.ReturnCouponAfterFullUnfulfilledRefundTx(tx, tenantID, 101)
	}); err != nil {
		t.Fatalf("idempotent refund return: %v", err)
	}
	if err := model.DB.First(&returned, grant.ID).Error; err != nil || returned.Status != "available" {
		t.Fatalf("repeat returned grant=%+v err=%v", returned, err)
	}
}

func TestCommercePromotionExplicitGrantReservationIsScopedAndIdempotent(t *testing.T) {
	ensureCommercePromotionSchema(t)
	tenantID := newCommerceTenant(t, "retail", "active")
	resetCommercePromotionData(t)
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenantID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("enable second business capability: %v", err)
	}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	service := &CommercePromotionService{DB: model.DB, Clock: func() time.Time { return now }}
	end := now.Add(time.Hour)
	template, err := service.CreateCouponTemplate(tenantID, CreateCommerceCouponTemplateInput{
		BusinessType: "retail", Name: "explicit", DiscountCents: 100,
		ValidDays: 1, PerCustomerCap: 1, StartsAt: &now, EndsAt: &end, Status: "active",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	grant := &model.CommerceCouponGrant{
		TenantID: tenantID, TemplateID: template.ID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "explicit-1", DiscountCents: 100,
		RefundReturnPolicy: promotionRefundReturnPolicy, Status: "available", ExpiresAt: end,
	}
	otherGrant := &model.CommerceCouponGrant{
		TenantID: tenantID, TemplateID: template.ID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "explicit-2", DiscountCents: 100,
		RefundReturnPolicy: promotionRefundReturnPolicy, Status: "available", ExpiresAt: end,
	}
	if err := model.DB.Create(grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if err := model.DB.Create(otherGrant).Error; err != nil {
		t.Fatalf("create other grant: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		reserved, reserveErr := service.ReserveCouponGrantTx(tx, tenantID, 201, grant.ID, "customer", "retail", 0, 500)
		if reserveErr != nil || reserved.ID != grant.ID || reserved.ReservedOrderID != 201 {
			return errors.Join(reserveErr, errors.New("explicit grant was not reserved"))
		}
		retry, retryErr := service.ReserveCouponGrantTx(tx, tenantID, 201, grant.ID, "customer", "retail", 0, 500)
		if retryErr != nil || retry.ID != grant.ID || retry.ReservedOrderID != 201 {
			return errors.Join(retryErr, errors.New("same order retry was not idempotent"))
		}
		return nil
	}); err != nil {
		t.Fatalf("reserve explicit grant: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, reserveErr := service.ReserveCouponGrantTx(tx, tenantID, 202, grant.ID, "customer", "retail", 0, 500)
		if !errors.Is(reserveErr, ErrCouponAlreadyReserved) {
			return errors.Join(reserveErr, errors.New("grant reserved by another order was accepted"))
		}
		return nil
	}); err != nil {
		t.Fatalf("occupied grant guard: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, reserveErr := service.ReserveCouponGrantTx(tx, tenantID, 203, otherGrant.ID, "other-customer", "retail", 0, 500)
		if !errors.Is(reserveErr, ErrCouponNotOwned) {
			return errors.Join(reserveErr, errors.New("foreign customer selected grant"))
		}
		_, reserveErr = service.ReserveCouponGrantTx(tx, tenantID, 203, otherGrant.ID, "customer", "restaurant", 0, 500)
		if !errors.Is(reserveErr, ErrCouponNotOwned) {
			return errors.Join(reserveErr, errors.New("foreign business selected grant"))
		}
		return nil
	}); err != nil {
		t.Fatalf("scope guard: %v", err)
	}
}

func TestCommercePromotionListAvailableCouponsExpiresOnlyCurrentScope(t *testing.T) {
	ensureCommercePromotionSchema(t)
	tenantID := newCommerceTenant(t, "restaurant", "active")
	resetCommercePromotionData(t)
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	service := &CommercePromotionService{DB: model.DB, Clock: func() time.Time { return now }}
	currentChannel := &model.ChannelAccount{TenantID: tenantID, Code: "promotion-scope-current-" + time.Now().Format("150405.000000000"), Type: "wechat_miniapp", Status: "active"}
	otherChannelAccount := &model.ChannelAccount{TenantID: tenantID, Code: "promotion-scope-other-" + time.Now().Format("150405.000000000"), Type: "wechat_miniapp", Status: "active"}
	if err := model.DB.Create(currentChannel).Error; err != nil {
		t.Fatalf("create current channel: %v", err)
	}
	if err := model.DB.Create(otherChannelAccount).Error; err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	expired := now.Add(-time.Minute)
	current := &model.CommerceCouponGrant{
		TenantID: tenantID, ChannelAccountID: currentChannel.ID, BusinessType: "restaurant", CustomerID: "customer",
		Source: "manual", SourceIdentity: "current", DiscountCents: 100, Status: "available", ExpiresAt: expired,
	}
	otherChannel := &model.CommerceCouponGrant{
		TenantID: tenantID, ChannelAccountID: otherChannelAccount.ID, BusinessType: "restaurant", CustomerID: "customer",
		Source: "manual", SourceIdentity: "other-channel", DiscountCents: 100, Status: "available", ExpiresAt: expired,
	}
	otherBusiness := &model.CommerceCouponGrant{
		TenantID: tenantID, ChannelAccountID: currentChannel.ID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "other-business", DiscountCents: 100, Status: "available", ExpiresAt: expired,
	}
	for _, grant := range []*model.CommerceCouponGrant{current, otherChannel, otherBusiness} {
		if err := model.DB.Create(grant).Error; err != nil {
			t.Fatalf("create grant %s: %v", grant.SourceIdentity, err)
		}
	}
	if _, err := service.ListAvailableCoupons(tenantID, currentChannel.ID, "restaurant", "customer"); err != nil {
		t.Fatalf("list current scope: %v", err)
	}
	for _, grant := range []*model.CommerceCouponGrant{current, otherChannel, otherBusiness} {
		var stored model.CommerceCouponGrant
		if err := model.DB.First(&stored, grant.ID).Error; err != nil {
			t.Fatalf("load grant %d: %v", grant.ID, err)
		}
		want := "available"
		if grant == current {
			want = "expired"
		}
		if stored.Status != want {
			t.Fatalf("grant %d status=%q want %q", grant.ID, stored.Status, want)
		}
	}
	shared := &model.CommerceCouponGrant{
		TenantID: tenantID, ChannelAccountID: currentChannel.ID, BusinessType: "restaurant", CustomerID: "customer",
		Source: "manual", SourceIdentity: "shared-location", DiscountCents: 100, Status: "available", ExpiresAt: now.Add(time.Hour),
	}
	if err := model.DB.Create(shared).Error; err != nil {
		t.Fatalf("create shared grant: %v", err)
	}
	for _, locationID := range []uint{101, 202} {
		rows, err := service.ListAvailableCouponsForScope(CommercePromotionScope{
			TenantID: tenantID, ChannelAccountID: currentChannel.ID, BusinessType: "restaurant", LocationID: locationID,
		}, "customer")
		if err != nil || len(rows) != 1 || rows[0].ID != shared.ID {
			t.Fatalf("location %d shared rows=%+v err=%v", locationID, rows, err)
		}
	}
}

func TestCommercePromotionReservationReleaseAndExpiry(t *testing.T) {
	ensureCommercePromotionSchema(t)
	tenantID := newCommerceTenant(t, "retail", "active")
	resetCommercePromotionData(t)
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	service := &CommercePromotionService{DB: model.DB, Clock: func() time.Time { return now }}

	activeGrant := &model.CommerceCouponGrant{
		TenantID: tenantID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "release-1", DiscountCents: 200,
		MinGoodsSubtotalCents: 1000, RefundReturnPolicy: promotionRefundReturnPolicy, Status: "available", ExpiresAt: now.Add(time.Hour),
	}
	if err := model.DB.Create(activeGrant).Error; err != nil {
		t.Fatalf("create active grant: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := service.ReserveCouponTx(tx, tenantID, 301, "customer", "retail", 0, 500); !errors.Is(err, ErrCouponNotApplicable) {
			return errors.Join(err, errors.New("below-minimum order was accepted"))
		}
		reserved, err := service.ReserveCouponTx(tx, tenantID, 301, "customer", "retail", 0, 1200)
		if err != nil || reserved.ID != activeGrant.ID || reserved.Status != "reserved" {
			return errors.Join(err, errors.New("grant was not reserved"))
		}
		return service.ReleaseCouponTx(tx, tenantID, 301)
	}); err != nil {
		t.Fatalf("release unpaid reservation: %v", err)
	}
	var released model.CommerceCouponGrant
	if err := model.DB.First(&released, activeGrant.ID).Error; err != nil {
		t.Fatalf("load released grant: %v", err)
	}
	if released.Status != "available" || released.ReservedOrderID != 0 || released.ReservedAt != nil {
		t.Fatalf("released grant=%+v", released)
	}

	expiredGrant := &model.CommerceCouponGrant{
		TenantID: tenantID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "expired-1", DiscountCents: 200,
		RefundReturnPolicy: promotionRefundReturnPolicy, Status: "available", ExpiresAt: now.Add(-time.Minute),
	}
	if err := model.DB.Create(expiredGrant).Error; err != nil {
		t.Fatalf("create expired grant: %v", err)
	}
	if _, err := service.ListAvailableCoupons(tenantID, 0, "retail", "customer"); err != nil {
		t.Fatalf("expire available grants: %v", err)
	}
	var expired model.CommerceCouponGrant
	if err := model.DB.First(&expired, expiredGrant.ID).Error; err != nil {
		t.Fatalf("load expired grant: %v", err)
	}
	if expired.Status != "expired" {
		t.Fatalf("expired grant status=%q", expired.Status)
	}
	if _, err := service.ReserveCouponGrantTx(model.DB, tenantID, 302, expiredGrant.ID, "customer", "retail", 0, 500); !errors.Is(err, ErrCouponNotOwned) {
		t.Fatalf("expired grant reservation err=%v, want ErrCouponNotOwned", err)
	}
	expiredUsed := &model.CommerceCouponGrant{
		TenantID: tenantID, BusinessType: "retail", CustomerID: "customer",
		Source: "manual", SourceIdentity: "expired-used-1", DiscountCents: 200,
		RefundReturnPolicy: promotionRefundReturnPolicy, Status: "used", UsedOrderID: 303, ExpiresAt: now.Add(-time.Minute),
	}
	if err := model.DB.Create(expiredUsed).Error; err != nil {
		t.Fatalf("create expired used grant: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return service.ReturnCouponAfterFullUnfulfilledRefundTx(tx, tenantID, 303)
	}); err != nil {
		t.Fatalf("expired refund return: %v", err)
	}
	var stillUsed model.CommerceCouponGrant
	if err := model.DB.First(&stillUsed, expiredUsed.ID).Error; err != nil {
		t.Fatalf("load expired used grant: %v", err)
	}
	if stillUsed.Status != "used" {
		t.Fatalf("expired used grant status=%q, want used", stillUsed.Status)
	}
}
