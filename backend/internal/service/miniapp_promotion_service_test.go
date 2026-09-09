package service

import (
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type miniappPromotionFixture struct {
	tenantID uint
	account  model.ChannelAccount
	mapping  model.ChannelProductMapping
	customer model.MiniappCustomer
}

func resetMiniappPromotionData(t *testing.T) {
	t.Helper()
	if err := model.Write(func(tx *gorm.DB) error {
		for _, item := range []interface{}{
			&model.MiniappInstantDiscountGrant{}, &model.MiniappInstantDiscountActivityMapping{}, &model.MiniappInstantDiscountActivity{},
		} {
			if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(item).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("reset miniapp promotion data: %v", err)
	}
	resetBusinessData(t)
}

func seedMiniappPromotionFixture(t *testing.T) miniappPromotionFixture {
	t.Helper()
	resetMiniappPromotionData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	account := model.ChannelAccount{Code: "promotion-xhs-" + time.Now().Format("150405.000000000"), Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(tenantID, &account, "promotion-app-"+time.Now().Format("150405.000000000"), "promotion-secret"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: productID, ExternalCode: "PROMOTION-TICKET-" + time.Now().Format("150405.000000000"), Status: "active", DisplayName: "Promotion ticket", ChannelSaleCents: 100}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, ExternalSKUID: "PROMOTION-SKU", CategoryID: "opaque-category-id", ImageURL: "https://example.test/promotion.png", Description: "ticket", ProductPath: "/pages/index", OrderPath: "/pages/order", ProductType: 1, SettleType: 1, SyncStatus: "synced", AuditStatus: "approved"}).Error; err != nil {
		t.Fatal(err)
	}
	customer := model.MiniappCustomer{TenantID: tenantID, ChannelAccountID: account.ID, OpenIDHash: hashMiniappValue("promotion-customer-" + time.Now().String()), OpenIDCiphertext: "opaque", SessionKeyCiphertext: "opaque", SessionTokenHash: hashMiniappValue("promotion-token-" + time.Now().String()), SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now()}
	if err := model.DB.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	return miniappPromotionFixture{tenantID: tenantID, account: account, mapping: mapping, customer: customer}
}

func TestMiniappPromotionGrantSnapshotsExpiryAndCooldown(t *testing.T) {
	fixture := seedMiniappPromotionFixture(t)
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	service := MiniappPromotionService{Now: func() time.Time { return now }, RandomInt: func(max int64) (int64, error) { return max - 1, nil }}
	if _, err := service.SaveConfig(fixture.tenantID, fixture.account.ID, MiniappInstantDiscountConfig{Enabled: true, MinDiscountCents: 3, MaxDiscountCents: 7, ValidityMinutes: 10, CooldownDays: 1, MappingIDs: []uint{fixture.mapping.ID}}); err != nil {
		t.Fatal(err)
	}
	first, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || first.Status != "available" || first.DiscountCents != 7 || first.GrantID == 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	again, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || again.GrantID != first.GrantID || again.DiscountCents != first.DiscountCents {
		t.Fatalf("again=%+v err=%v", again, err)
	}
	now = now.Add(11 * time.Minute)
	expired, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || expired.Status != "expired" || expired.GrantID != first.GrantID {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	now = now.Add(24 * time.Hour)
	second, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || second.Status != "available" || second.GrantID == first.GrantID || second.DiscountCents != 7 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestMiniappPromotionQuoteDoesNotBlockNonparticipatingMappingAndCapsDiscount(t *testing.T) {
	fixture := seedMiniappPromotionFixture(t)
	now := time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)
	service := MiniappPromotionService{Now: func() time.Time { return now }, RandomInt: func(int64) (int64, error) { return 0, nil }}
	if _, err := service.SaveConfig(fixture.tenantID, fixture.account.ID, MiniappInstantDiscountConfig{Enabled: true, MinDiscountCents: 100, MaxDiscountCents: 100, ValidityMinutes: 30, CooldownDays: 1, MappingIDs: []uint{fixture.mapping.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireOpportunity(&fixture.customer); err != nil {
		t.Fatal(err)
	}
	quote, err := service.QuoteForCustomer(&fixture.customer, fixture.mapping.ID, 1)
	if err != nil || quote.DiscountCents != 99 || quote.AmountCents != 1 || quote.Promotion.DiscountCents != 100 {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	if err := model.DB.Model(&model.MiniappInstantDiscountActivityMapping{}).Where("activity_id IN (SELECT id FROM miniapp_instant_discount_activities WHERE tenant_id = ? AND channel_account_id = ?)", fixture.tenantID, fixture.account.ID).Delete(&model.MiniappInstantDiscountActivityMapping{}).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := service.QuoteForCustomer(&fixture.customer, fixture.mapping.ID, 1)
	if err != nil || plain.DiscountCents != 0 || plain.AmountCents != 100 || plain.Promotion.Status != "inactive" {
		t.Fatalf("plain=%+v err=%v", plain, err)
	}
}

func TestMiniappPromotionConcurrentAcquireDoesNotRedraw(t *testing.T) {
	fixture := seedMiniappPromotionFixture(t)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	service := MiniappPromotionService{Now: func() time.Time { return now }, RandomInt: func(max int64) (int64, error) { return 0, nil }}
	if _, err := service.SaveConfig(fixture.tenantID, fixture.account.ID, MiniappInstantDiscountConfig{Enabled: true, MinDiscountCents: 4, MaxDiscountCents: 8, ValidityMinutes: 10, CooldownDays: 1, MappingIDs: []uint{fixture.mapping.ID}}); err != nil {
		t.Fatal(err)
	}
	var results [2]*MiniappPromotionOpportunity
	var errs [2]error
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = service.AcquireOpportunity(&fixture.customer)
		}(i)
	}
	wait.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].GrantID == 0 || results[0].GrantID != results[1].GrantID {
		t.Fatalf("results=%+v errors=%v", results, errs)
	}
	var count int64
	if err := model.DB.Model(&model.MiniappInstantDiscountGrant{}).Where("miniapp_customer_id = ?", fixture.customer.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("grant count=%d err=%v", count, err)
	}
}

func TestMiniappPromotionReservationReleaseHonorsExpiry(t *testing.T) {
	fixture := seedMiniappPromotionFixture(t)
	now := time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC)
	service := MiniappPromotionService{Now: func() time.Time { return now }, RandomInt: func(int64) (int64, error) { return 0, nil }}
	if _, err := service.SaveConfig(fixture.tenantID, fixture.account.ID, MiniappInstantDiscountConfig{Enabled: true, MinDiscountCents: 10, MaxDiscountCents: 10, ValidityMinutes: 5, CooldownDays: 1, MappingIDs: []uint{fixture.mapping.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireOpportunity(&fixture.customer); err != nil {
		t.Fatal(err)
	}
	quote, err := service.QuoteForCustomer(&fixture.customer, fixture.mapping.ID, 1)
	if err != nil || quote.DiscountCents != 10 {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	external := "promotion-release-" + time.Now().Format("150405.000000000")
	order := model.Order{TenantID: fixture.tenantID, Channel: "xiaohongshu", ChannelAccountID: fixture.account.ID, ExternalNo: &external, Items: []model.OrderItem{{ProductID: fixture.mapping.ProductID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	order.OriginalAmountCents, order.DiscountCents, order.PromotionGrantID = quote.OriginalAmountCents, quote.DiscountCents, quote.Promotion.GrantID
	if err := model.Write(func(tx *gorm.DB) error { return service.ReserveGrantForOrderTx(tx, &order, quote) }); err != nil {
		t.Fatal(err)
	}
	reserved, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || reserved.Status != "reserved" || reserved.ReservedOrderNo != order.OrderNo {
		t.Fatalf("reserved=%+v err=%v", reserved, err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return service.ReleaseGrantForOrderTx(tx, &order) }); err != nil {
		t.Fatal(err)
	}
	released, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || released.Status != "available" || released.GrantID != quote.Promotion.GrantID {
		t.Fatalf("released=%+v err=%v", released, err)
	}
	now = now.Add(6 * time.Minute)
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.MiniappInstantDiscountGrant{}).Where("id = ?", quote.Promotion.GrantID).Updates(map[string]interface{}{"reserved_order_id": order.ID, "reserved_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return service.ReleaseGrantForOrderTx(tx, &order) }); err != nil {
		t.Fatal(err)
	}
	expired, err := service.AcquireOpportunity(&fixture.customer)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
}
