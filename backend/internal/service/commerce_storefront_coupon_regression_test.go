package service

import (
	"errors"
	"testing"
	"time"

	"ticket-backend/internal/model"
)

func TestCommerceStorefrontRejectsCouponWithoutRemainingGoodsDiscount(t *testing.T) {
	f := newCommerceStorefrontServiceFixture(t)
	f.service.Member = newMemberServiceForTest(t)
	login := storefrontLogin(t, f.service, f.account.AppID, "zero-coupon-benefit")
	var session model.CommerceCustomerSession
	if err := model.DB.Where("tenant_id = ?", f.tenantID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.MemberID == nil {
		t.Fatal("first-party login did not resolve member")
	}
	if err := model.DB.Model(&model.TenantMember{}).Where("id = ?", *session.MemberID).Update("membership_status", "active").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Member.SaveBenefitConfig(f.tenantID, 100); err != nil {
		t.Fatal(err)
	}
	delivery := CommerceDeliveryService{DB: model.DB, Clock: f.service.Now}
	if _, err := delivery.UpsertLocationConfig(f.tenantID, f.domain.location.ID, CommerceLocationServiceConfigInput{BusinessType: "restaurant", PickupEnabled: true, PackagingFeeCents: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{ProductID: f.domain.product.ID, SKUID: f.domain.sku.ID, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	quote := storefrontPickupQuote(t, f, login.Token)
	scope, err := f.service.ResolveCustomerScope(login.Token, "restaurant")
	if err != nil {
		t.Fatal(err)
	}
	template := model.CommerceCouponTemplate{TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", Name: "Member reward", Version: 1, DiscountCents: 200, ValidDays: 1, Status: "active", PerCustomerCap: 1, RefundReturnPolicy: promotionRefundReturnPolicy}
	if err := model.DB.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.CommerceCouponGrant{TenantID: f.tenantID, TemplateID: template.ID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: scope.CustomerID, Source: "manual", SourceIdentity: "zero-coupon-benefit", DiscountCents: 200, Status: "available", ExpiresAt: f.service.Now().Add(time.Hour), RefundReturnPolicy: promotionRefundReturnPolicy}
	if err := model.DB.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	input := CommerceStorefrontCheckoutInput{IdempotencyKey: "zero-coupon-benefit", ContactName: "Guest", ContactPhone: "13800138000", FulfillmentMethod: "pickup", QuoteToken: quote.RawToken, CouponGrantID: grant.ID}
	if order, err := f.service.Checkout(login.Token, input); !errors.Is(err, ErrCouponNotApplicable) || order != nil {
		t.Fatalf("zero-benefit coupon checkout: order=%+v err=%v", order, err)
	}
	if err := model.DB.First(&grant, grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	if grant.Status != "available" || grant.ReservedOrderID != 0 {
		t.Fatalf("rejected coupon was reserved: %+v", grant)
	}
	var count int64
	if err := model.DB.Model(&model.CommerceOrder{}).Where("tenant_id = ?", f.tenantID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected checkout persisted order: count=%d err=%v", count, err)
	}
	// The original quote and cart remain usable when the customer pays fees only.
	input.CouponGrantID = 0
	order, err := f.service.Checkout(login.Token, input)
	if err != nil || order.TotalAmountCents != 100 {
		t.Fatalf("fees-only checkout: order=%+v err=%v", order, err)
	}
	if err := model.DB.First(&grant, grant.ID).Error; err != nil || grant.Status != "available" {
		t.Fatalf("fees-only checkout consumed unrelated coupon: %+v err=%v", grant, err)
	}
	// A later purchase with remaining goods still receives the exact selected coupon.
	if _, err := f.service.Member.SaveBenefitConfig(f.tenantID, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{ProductID: f.domain.product.ID, SKUID: f.domain.sku.ID, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	quote = storefrontPickupQuote(t, f, login.Token)
	input.IdempotencyKey, input.QuoteToken, input.CouponGrantID = "partial-member-coupon", quote.RawToken, grant.ID
	order, err = f.service.Checkout(login.Token, input)
	if err != nil || order.TotalAmountCents != 400 {
		t.Fatalf("partial-member coupon checkout: order=%+v err=%v", order, err)
	}
	orders := CommerceOrderService{DB: model.DB, Clock: f.service.Now}
	if _, err := orders.ConfirmPayment(f.tenantID, order.ID); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&grant, grant.ID).Error; err != nil || grant.Status != "used" || grant.UsedOrderID != order.ID {
		t.Fatalf("coupon not consumed by its actual discounted purchase: %+v err=%v", grant, err)
	}
}
