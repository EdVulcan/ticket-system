package service

import (
	"errors"
	"testing"
	"time"

	"ticket-backend/internal/model"
)

func TestCommerceStorefrontCheckoutRejectsZeroPayCouponAndRollsBack(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "zero-pay-coupon-subject")
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add zero-pay cart item: %v", err)
	}
	quote := storefrontPickupQuote(t, fixture, login.Token)
	scope, err := fixture.service.ResolveCustomerScope(login.Token, "restaurant")
	if err != nil {
		t.Fatalf("resolve zero-pay customer scope: %v", err)
	}
	template := model.CommerceCouponTemplate{
		TenantID: fixture.tenantID, ChannelAccountID: fixture.account.ID, BusinessType: "restaurant",
		Name: "zero-pay-test", Version: 1, DiscountCents: quote.Quote.GoodsSubtotalCents,
		Status: "active", PerCustomerCap: 1, RefundReturnPolicy: promotionRefundReturnPolicy,
	}
	if err := model.DB.Create(&template).Error; err != nil {
		t.Fatalf("create zero-pay coupon template: %v", err)
	}
	grant := model.CommerceCouponGrant{
		TenantID: fixture.tenantID, TemplateID: template.ID, ChannelAccountID: fixture.account.ID,
		BusinessType: "restaurant", CustomerID: scope.CustomerID, Source: "manual", SourceIdentity: "zero-pay-test",
		DiscountCents: quote.Quote.GoodsSubtotalCents, RefundReturnPolicy: promotionRefundReturnPolicy,
		Status: "available", ExpiresAt: fixture.service.Now().Add(time.Hour),
	}
	if err := model.DB.Create(&grant).Error; err != nil {
		t.Fatalf("create zero-pay coupon grant: %v", err)
	}
	if _, err := fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "zero-pay-coupon-checkout", ContactName: "Guest", ContactPhone: "13800138000",
		FulfillmentMethod: "pickup", QuoteToken: quote.RawToken, CouponGrantID: grant.ID,
	}); !errors.Is(err, ErrCommerceStorefrontInvalid) {
		t.Fatalf("zero-pay checkout error=%v, want ErrCommerceStorefrontInvalid", err)
	}
	var storedGrant model.CommerceCouponGrant
	if err := model.DB.First(&storedGrant, grant.ID).Error; err != nil {
		t.Fatalf("load zero-pay coupon grant: %v", err)
	}
	if storedGrant.Status != "available" || storedGrant.ReservedOrderID != 0 {
		t.Fatalf("zero-pay coupon reservation was not rolled back: %+v", storedGrant)
	}
	var orderCount int64
	if err := model.DB.Model(&model.CommerceOrder{}).Where("tenant_id = ? AND customer_id = ? AND idempotency_key = ?", fixture.tenantID, scope.CustomerID, "zero-pay-coupon-checkout").Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("zero-pay checkout persisted %d orders", orderCount)
	}
}

func TestCommerceStorefrontQuoteRejectsEveryScopeMismatch(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "quote-scope-subject")
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add quote scope item: %v", err)
	}
	quote := storefrontPickupQuote(t, fixture, login.Token)
	scope, err := fixture.service.ResolveCustomerScope(login.Token, "restaurant")
	if err != nil {
		t.Fatalf("resolve storefront quote scope: %v", err)
	}
	base := CommerceConsumeQuoteInput{
		RawToken: quote.RawToken, TenantID: scope.TenantID, ChannelAccountID: scope.ChannelAccountID,
		BusinessType: scope.BusinessType, CustomerID: scope.CustomerID, LocationID: scope.LocationID,
		FulfillmentMethod: "pickup", GoodsSubtotalCents: quote.Quote.GoodsSubtotalCents,
		AddressSnapshotJSON: quote.Quote.AddressSnapshotJSON, OrderID: 1,
	}
	cases := []struct {
		name   string
		mutate func(*CommerceConsumeQuoteInput)
	}{
		{name: "tenant", mutate: func(input *CommerceConsumeQuoteInput) { input.TenantID++ }},
		{name: "channel account", mutate: func(input *CommerceConsumeQuoteInput) { input.ChannelAccountID++ }},
		{name: "business", mutate: func(input *CommerceConsumeQuoteInput) { input.BusinessType = "retail" }},
		{name: "customer", mutate: func(input *CommerceConsumeQuoteInput) { input.CustomerID = "another-customer" }},
		{name: "location", mutate: func(input *CommerceConsumeQuoteInput) { input.LocationID++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.mutate(&input)
			tx := model.DB.Begin()
			if tx.Error != nil {
				t.Fatalf("begin quote scope transaction: %v", tx.Error)
			}
			_, consumeErr := (&CommerceDeliveryService{DB: tx, Clock: fixture.service.Now}).ConsumeQuoteTx(tx, input)
			_ = tx.Rollback()
			if !errors.Is(consumeErr, ErrCommerceQuoteConflict) {
				t.Fatalf("scope mismatch error=%v, want %v", consumeErr, ErrCommerceQuoteConflict)
			}
		})
	}
}

func TestCommerceStorefrontCheckoutRejectsCartSubtotalChangesAfterQuote(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "quote-subtotal-subject")
	cart, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	})
	if err != nil || len(cart.Items) != 1 {
		t.Fatalf("add subtotal item: cart=%+v err=%v", cart, err)
	}
	quote := storefrontPickupQuote(t, fixture, login.Token)
	if _, err := fixture.service.UpdateCartItem(login.Token, cart.Items[0].ID, 2); err != nil {
		t.Fatalf("change cart after quote: %v", err)
	}
	_, err = fixture.service.Checkout(login.Token, CommerceStorefrontCheckoutInput{
		IdempotencyKey: "quote-subtotal-change", ContactName: "Guest", ContactPhone: "13800138000",
		FulfillmentMethod: "pickup", QuoteToken: quote.RawToken,
	})
	if !errors.Is(err, ErrCommerceQuoteConflict) {
		t.Fatalf("changed cart subtotal error=%v, want %v", err, ErrCommerceQuoteConflict)
	}
}

func TestCommerceStorefrontCheckoutRejectsChangedQuoteOnIdempotentRetry(t *testing.T) {
	fixture := newCommerceStorefrontServiceFixture(t)
	login := storefrontLogin(t, fixture.service, fixture.account.AppID, "quote-retry-subject")
	if _, err := fixture.service.AddCartItem(login.Token, CommerceStorefrontCartItemInput{
		ProductID: fixture.domain.product.ID, SKUID: fixture.domain.sku.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("add idempotency item: %v", err)
	}
	firstQuote := storefrontPickupQuote(t, fixture, login.Token)
	secondQuote := storefrontPickupQuote(t, fixture, login.Token)
	input := CommerceStorefrontCheckoutInput{
		IdempotencyKey: "quote-idempotency-conflict", ContactName: "Guest", ContactPhone: "13800138000",
		FulfillmentMethod: "pickup", QuoteToken: firstQuote.RawToken,
	}
	order, err := fixture.service.Checkout(login.Token, input)
	if err != nil {
		t.Fatalf("checkout with first quote: %v", err)
	}
	input.QuoteToken = secondQuote.RawToken
	if retry, retryErr := fixture.service.Checkout(login.Token, input); !errors.Is(retryErr, ErrCommerceIdempotencyConflict) || retry != nil {
		t.Fatalf("changed quote retry result=%+v err=%v, want idempotency conflict", retry, retryErr)
	}
	if order == nil || order.ID == 0 {
		t.Fatal("first checkout did not create an order")
	}
}
