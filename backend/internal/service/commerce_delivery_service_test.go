package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type commerceDeliveryFixture struct {
	service  *CommerceDeliveryService
	tenantID uint
	account  model.ChannelAccount
	location *model.CommerceFulfillmentLocation
	now      time.Time
}

func prepareCommerceDelivery(t *testing.T, businessType string) commerceDeliveryFixture {
	t.Helper()
	if err := model.DB.AutoMigrate(
		&model.CommerceLocationServiceConfig{},
		&model.CommerceDeliveryZone{},
		&model.CommerceDeliverySlot{},
		&model.CommerceCheckoutQuote{},
		&model.CommerceOrderAdjustment{},
	); err != nil {
		t.Fatalf("migrate commerce delivery models: %v", err)
	}
	tenant := model.Tenant{
		Name:       "Commerce delivery test tenant",
		SystemCode: fmt.Sprintf("CD-%s-%d", strings.ToUpper(businessType), time.Now().UnixNano()),
		SecretKey:  "commerce-delivery-secret",
		Status:     "active",
	}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenant.ID, BusinessType: businessType, Status: "active"}).Error; err != nil {
		t.Fatalf("create business capability: %v", err)
	}
	tenantID := tenant.ID
	account := model.ChannelAccount{
		TenantID:    tenantID,
		Code:        fmt.Sprintf("delivery-%s-%d", businessType, time.Now().UnixNano()),
		Type:        "wechat_miniapp",
		Status:      "active",
		Environment: "production",
	}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatalf("create channel account: %v", err)
	}
	ops := &CommerceOperationsService{}
	locationType := "store"
	if businessType == "retail" {
		locationType = "warehouse"
	}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{
		BusinessType: businessType,
		Name:         "Delivery test location",
		LocationType: locationType,
	})
	if err != nil {
		t.Fatalf("create fulfillment location: %v", err)
	}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC) // Monday
	return commerceDeliveryFixture{
		service:  &CommerceDeliveryService{Clock: func() time.Time { return now }},
		tenantID: tenantID,
		account:  account,
		location: location,
		now:      now,
	}
}

func activeDeliveryConfig(t *testing.T, f commerceDeliveryFixture, delivery bool) *model.CommerceLocationServiceConfig {
	t.Helper()
	config, err := f.service.UpsertLocationConfig(f.tenantID, f.location.ID, CommerceLocationServiceConfigInput{
		BusinessType:      "restaurant",
		PickupEnabled:     true,
		DeliveryEnabled:   delivery,
		MinGoodsCents:     1000,
		PackagingFeeCents: 100,
		EstimatedMinutes:  20,
	})
	if err != nil {
		t.Fatalf("configure location service: %v", err)
	}
	return config
}

func createDeliveryZoneAndSlot(t *testing.T, f commerceDeliveryFixture, capacity int) (*model.CommerceDeliveryZone, *model.CommerceDeliverySlot) {
	t.Helper()
	zone, err := f.service.CreateZone(f.tenantID, f.location.ID, "restaurant", CommerceDeliveryZoneInput{
		Name: "城区", Province: "省", City: "市", District: "区", FeeCents: 200,
		EstimatedMinutes: 30,
	})
	if err != nil {
		t.Fatalf("create delivery zone: %v", err)
	}
	slot, err := f.service.CreateSlot(f.tenantID, f.location.ID, "restaurant", CommerceDeliverySlotInput{
		ZoneID: &zone.ID, DayOfWeek: int(f.now.Weekday()), StartMinute: 12 * 60,
		EndMinute: 13 * 60, OrderCutoffMinutes: 30, Capacity: capacity,
	})
	if err != nil {
		t.Fatalf("create delivery slot: %v", err)
	}
	return zone, slot
}

func deliveryInput(f commerceDeliveryFixture, method string) CommerceCheckoutQuoteInput {
	input := CommerceCheckoutQuoteInput{
		TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant",
		CustomerID: "customer-1", LocationID: f.location.ID, FulfillmentMethod: method,
		GoodsSubtotalCents: 1500, ExpiresIn: 10 * time.Minute,
	}
	if method == "delivery" {
		zone, slot := createDeliveryZoneAndSlotForInput(f)
		input.ZoneID, input.SlotID, input.SlotDate = zone.ID, slot.ID, f.now
		input.Address = CommerceDeliveryAddress{Province: " 省 ", City: "市", District: "区", Detail: " 详细地址 "}
	}
	return input
}

func createDeliveryZoneAndSlotForInput(f commerceDeliveryFixture) (*model.CommerceDeliveryZone, *model.CommerceDeliverySlot) {
	// This helper is only used by deliveryInput in tests that already created
	// the location config; it keeps the input fixture concise.
	zone, _ := f.service.CreateZone(f.tenantID, f.location.ID, "restaurant", CommerceDeliveryZoneInput{Name: "城区", Province: "省", City: "市", District: "区", FeeCents: 200})
	slot, _ := f.service.CreateSlot(f.tenantID, f.location.ID, "restaurant", CommerceDeliverySlotInput{ZoneID: &zone.ID, DayOfWeek: int(f.now.Weekday()), StartMinute: 720, EndMinute: 780, OrderCutoffMinutes: 30, Capacity: 0})
	return zone, slot
}

func TestCommerceDeliveryQuoteScopesFeesAndSnapshots(t *testing.T) {
	f := prepareCommerceDelivery(t, "restaurant")
	config := activeDeliveryConfig(t, f, true)
	zone, slot := createDeliveryZoneAndSlot(t, f, 0)
	input := CommerceCheckoutQuoteInput{
		TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "customer-1", LocationID: f.location.ID,
		FulfillmentMethod: "delivery", Address: CommerceDeliveryAddress{Province: " 省 ", City: "市", District: "区", Detail: " 详细地址 "},
		ZoneID: zone.ID, SlotID: slot.ID, SlotDate: f.now, GoodsSubtotalCents: 1500, ExpiresIn: 10 * time.Minute,
	}
	result, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("create delivery quote: %v", err)
	}
	if result.Quote.TotalCents != 1800 || result.Quote.PackagingFeeCents != 100 || result.Quote.DeliveryFeeCents != 200 {
		t.Fatalf("unexpected quote amounts: %+v", result.Quote)
	}
	if result.Quote.ConfigVersion != config.ConfigVersion || result.RawToken == "" || strings.Contains(result.Quote.TokenHash, result.RawToken) {
		t.Fatalf("quote token/config snapshot unsafe: token=%q quote=%+v", result.RawToken, result.Quote)
	}
	if !strings.Contains(result.Quote.AddressSnapshotJSON, "详细地址") || strings.Contains(result.Quote.AddressSnapshotJSON, " 详细地址 ") {
		t.Fatalf("address was not normalized/snapshotted: %q", result.Quote.AddressSnapshotJSON)
	}

	pickup := input
	pickup.FulfillmentMethod, pickup.ZoneID, pickup.SlotID, pickup.SlotDate, pickup.Address = "pickup", 0, 0, time.Time{}, CommerceDeliveryAddress{}
	pickup.CustomerID = "customer-pickup"
	pickupResult, err := f.service.CreateQuote(pickup)
	if err != nil {
		t.Fatalf("create pickup quote: %v", err)
	}
	if pickupResult.Quote.DeliveryFeeCents != 0 || pickupResult.Quote.TotalCents != 1600 || pickupResult.Quote.AddressSnapshotJSON != "" {
		t.Fatalf("pickup quote should retain packaging only: %+v", pickupResult.Quote)
	}
}

func TestCommerceDeliveryRejectsInvalidScopeAddressWeekdayAndMinimum(t *testing.T) {
	f := prepareCommerceDelivery(t, "restaurant")
	activeDeliveryConfig(t, f, true)
	zone, slot := createDeliveryZoneAndSlot(t, f, 0)
	base := CommerceCheckoutQuoteInput{TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "customer-1", LocationID: f.location.ID, FulfillmentMethod: "delivery", Address: CommerceDeliveryAddress{Province: "省", City: "市", District: "区"}, ZoneID: zone.ID, SlotID: slot.ID, SlotDate: f.now, GoodsSubtotalCents: 900, ExpiresIn: time.Minute}
	if _, err := f.service.CreateQuote(base); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("minimum error=%v", err)
	}
	base.GoodsSubtotalCents = 1500
	base.Address.District = "其他区"
	if _, err := f.service.CreateQuote(base); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("zone mismatch error=%v", err)
	}
	base.Address.District = "区"
	base.SlotDate = f.now.Add(24 * time.Hour)
	if _, err := f.service.CreateQuote(base); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("weekday mismatch error=%v", err)
	}
	base.SlotDate = f.now
	base.ChannelAccountID = 999999
	if _, err := f.service.CreateQuote(base); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("channel scope error=%v", err)
	}
	foreign := model.Tenant{
		Name:       "Foreign delivery tenant",
		SystemCode: fmt.Sprintf("CD-FOREIGN-%d", time.Now().UnixNano()),
		SecretKey:  "foreign-secret",
		Status:     "active",
	}
	if err := model.DB.Create(&foreign).Error; err != nil {
		t.Fatalf("create foreign tenant: %v", err)
	}
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: foreign.ID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("create foreign capability: %v", err)
	}
	foreignLocation, err := (&CommerceOperationsService{}).CreateLocation(foreign.ID, CreateCommerceLocationInput{BusinessType: "restaurant", Name: "Foreign location"})
	if err != nil {
		t.Fatalf("create foreign location: %v", err)
	}
	if _, err := f.service.CreateZone(foreign.ID, f.location.ID, "restaurant", CommerceDeliveryZoneInput{Name: "越权", Province: "省", City: "市", District: "区"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant location error=%v", err)
	}
	if _, err := f.service.UpdateZone(f.tenantID, foreignLocation.ID, zone.ID, "restaurant", CommerceDeliveryZoneInput{Name: "越权", Province: "省", City: "市", District: "区"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-location zone error=%v", err)
	}
}

func TestCommerceDeliveryZonesAndSlotsRejectRetailScope(t *testing.T) {
	f := prepareCommerceDelivery(t, "retail")
	zoneInput := CommerceDeliveryZoneInput{Name: "不应创建", Province: "省", City: "市", District: "区"}
	if _, err := f.service.CreateZone(f.tenantID, f.location.ID, "retail", zoneInput); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("retail delivery zone create error=%v", err)
	}
	if _, err := f.service.ListZones(f.tenantID, f.location.ID, "retail"); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("retail delivery zone list error=%v", err)
	}
	slotInput := CommerceDeliverySlotInput{DayOfWeek: 1, StartMinute: 540, EndMinute: 600}
	if _, err := f.service.CreateSlot(f.tenantID, f.location.ID, "retail", slotInput); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("retail delivery slot create error=%v", err)
	}
	if _, err := f.service.ListSlots(f.tenantID, f.location.ID, "retail", nil); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("retail delivery slot list error=%v", err)
	}
}

func TestCommerceDeliveryCapacityAndQuoteConsumptionAreIdempotent(t *testing.T) {
	f := prepareCommerceDelivery(t, "restaurant")
	activeDeliveryConfig(t, f, true)
	zone, slot := createDeliveryZoneAndSlot(t, f, 1)
	input := CommerceCheckoutQuoteInput{TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "customer-1", LocationID: f.location.ID, FulfillmentMethod: "delivery", Address: CommerceDeliveryAddress{Province: "省", City: "市", District: "区"}, ZoneID: zone.ID, SlotID: slot.ID, SlotDate: f.now, GoodsSubtotalCents: 1500, ExpiresIn: 10 * time.Minute}
	first, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("first quote: %v", err)
	}
	second, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("same-customer quote refresh should replace the prior hold: %v", err)
	}
	var cancelled model.CommerceCheckoutQuote
	if err := model.DB.Where("token_hash = ?", first.Quote.TokenHash).First(&cancelled).Error; err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("prior quote was not cancelled on refresh: %+v err=%v", cancelled, err)
	}
	if second.Quote.Status != "active" {
		t.Fatalf("refreshed quote status=%q", second.Quote.Status)
	}
	consume := CommerceConsumeQuoteInput{RawToken: second.RawToken, TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "customer-1", LocationID: f.location.ID, FulfillmentMethod: "delivery", GoodsSubtotalCents: 1500, AddressSnapshotJSON: `{"province":"省","city":"市","district":"区"}`, OrderID: 101}
	var consumed *model.CommerceCheckoutQuote
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		consumed, err = f.service.ConsumeQuoteTx(tx, consume)
		return err
	}); err != nil {
		t.Fatalf("consume quote: %v", err)
	}
	if consumed.Status != "consumed" || consumed.ConsumedOrderID == nil || *consumed.ConsumedOrderID != 101 {
		t.Fatalf("consumed quote=%+v", consumed)
	}
	var retry *model.CommerceCheckoutQuote
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		retry, err = f.service.ConsumeQuoteTx(tx, consume)
		return err
	}); err != nil || retry.ID != consumed.ID {
		t.Fatalf("same order retry quote=%+v err=%v", retry, err)
	}
	consume.OrderID = 102
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := f.service.ConsumeQuoteTx(tx, consume)
		return err
	}); !errors.Is(err, ErrCommerceQuoteConflict) {
		t.Fatalf("conflicting order error=%v", err)
	}
	consume.OrderID = 101
	otherCustomer := input
	otherCustomer.CustomerID = "customer-2"
	// A different time on the same selected date must use the same capacity
	// bucket, and a submitted order continues occupying that slot.
	otherCustomer.SlotDate = input.SlotDate.Add(2 * time.Hour)
	if _, err := f.service.CreateQuote(otherCustomer); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("consumed slot capacity error=%v", err)
	}
	if err := model.DB.Model(&model.CommerceCheckoutQuote{}).
		Where("id = ? AND tenant_id = ?", consumed.ID, f.tenantID).
		Update("status", "cancelled").Error; err != nil {
		t.Fatalf("release consumed capacity: %v", err)
	}
	if _, err := f.service.CreateQuote(otherCustomer); err != nil {
		t.Fatalf("released slot capacity should be reusable: %v", err)
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := f.service.ConsumeQuoteTx(tx, consume)
		return err
	}); !errors.Is(err, ErrCommerceQuoteConflict) {
		t.Fatalf("cancelled quote must not be revived by a retry: %v", err)
	}

	// Unlimited slots still represent a customer hold. Refreshing checkout
	// must replace the prior hold instead of leaving multiple active quotes.
	openZone, openSlot := createDeliveryZoneAndSlot(t, f, 0)
	openInput := CommerceCheckoutQuoteInput{
		TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "unlimited-refresh", LocationID: f.location.ID,
		FulfillmentMethod: "delivery", Address: CommerceDeliveryAddress{Province: "省", City: "市", District: "区"}, ZoneID: openZone.ID, SlotID: openSlot.ID, SlotDate: f.now,
		GoodsSubtotalCents: 1500, ExpiresIn: 10 * time.Minute,
	}
	openFirst, err := f.service.CreateQuote(openInput)
	if err != nil {
		t.Fatalf("unlimited first quote: %v", err)
	}
	if _, err := f.service.CreateQuote(openInput); err != nil {
		t.Fatalf("unlimited refreshed quote: %v", err)
	}
	var openCancelled model.CommerceCheckoutQuote
	if err := model.DB.Where("id = ?", openFirst.Quote.ID).First(&openCancelled).Error; err != nil || openCancelled.Status != "cancelled" {
		t.Fatalf("unlimited prior quote was not cancelled on refresh: %+v err=%v", openCancelled, err)
	}
}

func TestCommerceDeliveryQuoteExpiryAndConfigHistory(t *testing.T) {
	f := prepareCommerceDelivery(t, "restaurant")
	config := activeDeliveryConfig(t, f, false)
	input := deliveryInput(f, "pickup")
	result, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("create quote: %v", err)
	}
	if _, err := f.service.UpsertLocationConfig(f.tenantID, f.location.ID, CommerceLocationServiceConfigInput{BusinessType: "restaurant", PickupEnabled: true, PackagingFeeCents: 900}); err != nil {
		t.Fatalf("update config: %v", err)
	}
	var stored model.CommerceCheckoutQuote
	if err := model.DB.First(&stored, result.Quote.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ConfigVersion != config.ConfigVersion || stored.PackagingFeeCents != config.PackagingFeeCents {
		t.Fatalf("historical quote changed after config edit: %+v", stored)
	}
	if err := f.service.CancelQuote(f.tenantID, result.Quote.ID); err != nil {
		t.Fatalf("cancel quote: %v", err)
	}

	f.service.Clock = func() time.Time { return f.now.Add(time.Hour) }
	if err := f.service.ExpireQuote(f.tenantID, result.Quote.ID); err != nil {
		t.Fatalf("expire cancelled quote: %v", err)
	}
	short := input
	short.CustomerID, short.ExpiresIn = "expires", time.Minute
	shortResult, err := f.service.CreateQuote(short)
	if err != nil {
		t.Fatalf("create expiring quote: %v", err)
	}
	f.service.Clock = func() time.Time { return f.now.Add(2 * time.Hour) }
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := f.service.ConsumeQuoteTx(tx, CommerceConsumeQuoteInput{RawToken: shortResult.RawToken, TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "expires", LocationID: f.location.ID, FulfillmentMethod: "pickup", GoodsSubtotalCents: 1500, OrderID: 404})
		return err
	}); !errors.Is(err, ErrCommerceQuoteExpired) {
		t.Fatalf("expired quote error=%v", err)
	}
	// Expired checkout consumption is rejected inside the order transaction.
	// That transaction must roll back its projection update together with the
	// order attempt; the background/explicit expiry path persists the terminal
	// projection separately.
	var active model.CommerceCheckoutQuote
	if err := model.DB.First(&active, shortResult.Quote.ID).Error; err != nil || active.Status != "active" {
		t.Fatalf("failed checkout should not commit expiry projection=%+v err=%v", active, err)
	}
	if err := f.service.ExpireQuote(f.tenantID, shortResult.Quote.ID); err != nil {
		t.Fatalf("persist expired quote: %v", err)
	}
	var expired model.CommerceCheckoutQuote
	if err := model.DB.First(&expired, shortResult.Quote.ID).Error; err != nil || expired.Status != "expired" {
		t.Fatalf("expired quote projection=%+v err=%v", expired, err)
	}
	f.service.Clock = func() time.Time { return f.now.Add(time.Hour) }
	if _, err := f.service.CreateQuote(input); err != nil {
		t.Fatalf("create second quote: %v", err)
	}
	if _, err := f.service.CreateQuote(CommerceCheckoutQuoteInput{TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: "overflow", LocationID: f.location.ID, FulfillmentMethod: "pickup", GoodsSubtotalCents: math.MaxInt64, ExpiresIn: time.Minute}); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestCommerceRetailShippingAndBusinessFulfillmentBoundaries(t *testing.T) {
	f := prepareCommerceDelivery(t, "retail")
	config, err := f.service.UpsertLocationConfig(f.tenantID, f.location.ID, CommerceLocationServiceConfigInput{
		BusinessType: "retail", ShippingEnabled: true, MinGoodsCents: 1000,
		ShippingFeeCents: 800, FreeShippingThresholdCents: 5000,
	})
	if err != nil {
		t.Fatalf("configure retail shipping: %v", err)
	}
	input := CommerceCheckoutQuoteInput{
		TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "retail", CustomerID: "retail-customer", LocationID: f.location.ID,
		FulfillmentMethod: "shipping", Address: CommerceDeliveryAddress{Province: "省", City: "市", District: "区", Detail: "街道 1 号"}, GoodsSubtotalCents: 1500, ExpiresIn: time.Minute,
	}
	result, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("create retail shipping quote: %v", err)
	}
	if result.Quote.ShippingFeeCents != 800 || result.Quote.DeliveryFeeCents != 0 || result.Quote.PackagingFeeCents != 0 || result.Quote.TotalCents != 2300 || result.Quote.FulfillmentMethod != "shipping" {
		t.Fatalf("unexpected retail quote: %+v", result.Quote)
	}
	if result.Quote.ConfigVersion != config.ConfigVersion {
		t.Fatalf("retail config version was not snapshotted: %+v", result.Quote)
	}
	free := input
	free.CustomerID, free.GoodsSubtotalCents = "retail-free", 5000
	freeResult, err := f.service.CreateQuote(free)
	if err != nil {
		t.Fatalf("create free-shipping quote: %v", err)
	}
	if freeResult.Quote.ShippingFeeCents != 0 || freeResult.Quote.TotalCents != 5000 {
		t.Fatalf("free-shipping quote: %+v", freeResult.Quote)
	}
	if _, err := f.service.CreateQuote(CommerceCheckoutQuoteInput{TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "retail", CustomerID: "bad-method", LocationID: f.location.ID, FulfillmentMethod: "delivery", GoodsSubtotalCents: 1500, ExpiresIn: time.Minute}); !errors.Is(err, ErrCommerceQuoteInvalid) {
		t.Fatalf("retail delivery method error=%v", err)
	}
	if _, err := f.service.UpsertLocationConfig(f.tenantID, f.location.ID, CommerceLocationServiceConfigInput{BusinessType: "retail", PickupEnabled: true, ShippingEnabled: true}); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("retail restaurant setting error=%v", err)
	}
	restaurant := prepareCommerceDelivery(t, "restaurant")
	if _, err := restaurant.service.UpsertLocationConfig(restaurant.tenantID, restaurant.location.ID, CommerceLocationServiceConfigInput{BusinessType: "restaurant", ShippingEnabled: true}); !errors.Is(err, ErrCommerceDeliveryInvalid) {
		t.Fatalf("restaurant shipping setting error=%v", err)
	}
}

func TestCommerceDeliveryQuoteConsumptionBindsCheckoutFacts(t *testing.T) {
	f := prepareCommerceDelivery(t, "restaurant")
	activeDeliveryConfig(t, f, true)
	input := deliveryInput(f, "delivery")
	result, err := f.service.CreateQuote(input)
	if err != nil {
		t.Fatalf("create quote: %v", err)
	}
	base := CommerceConsumeQuoteInput{RawToken: result.RawToken, TenantID: f.tenantID, ChannelAccountID: f.account.ID, BusinessType: "restaurant", CustomerID: input.CustomerID, LocationID: f.location.ID, FulfillmentMethod: "delivery", GoodsSubtotalCents: input.GoodsSubtotalCents, AddressSnapshotJSON: result.Quote.AddressSnapshotJSON, OrderID: 500}
	for name, mutate := range map[string]func(*CommerceConsumeQuoteInput){
		"method":   func(v *CommerceConsumeQuoteInput) { v.FulfillmentMethod = "pickup" },
		"subtotal": func(v *CommerceConsumeQuoteInput) { v.GoodsSubtotalCents++ },
		"address": func(v *CommerceConsumeQuoteInput) {
			v.AddressSnapshotJSON = `{"province":"省","city":"市","district":"区","detail":"另一个地址"}`
		},
	} {
		candidate := base
		candidate.RawToken = result.RawToken
		mutate(&candidate)
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := f.service.ConsumeQuoteTx(tx, candidate)
			return err
		}); !errors.Is(err, ErrCommerceQuoteConflict) {
			t.Fatalf("%s mismatch error=%v", name, err)
		}
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := f.service.ConsumeQuoteTx(tx, base)
		return err
	}); err != nil {
		t.Fatalf("matching quote consumption: %v", err)
	}
}
