package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

// This fixture exercises the real local order and durable provider-operation
// pipeline against an HTTP protocol double; it is not a platform acceptance test.
func promotionOrderFixture(t *testing.T) (miniappPromotionFixture, MiniappPromotionService, XiaohongshuOrderService, *atomic.Int32, *time.Time) {
	t.Helper()
	f := seedMiniappPromotionFixture(t)
	if err := model.DB.Model(&f.account).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	f.customer.OpenIDCiphertext, _ = utils.EncryptAES("promotion-openid")
	if err := model.DB.Model(&f.customer).Update("open_id_ciphertext", f.customer.OpenIDCiphertext).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := MiniappPromotionService{Now: func() time.Time { return now }}
	if _, err := p.SaveConfig(f.tenantID, f.account.ID, MiniappInstantDiscountConfig{Enabled: true, MinDiscountCents: 1, MaxDiscountCents: 1, ValidityMinutes: 1, CooldownDays: 7, MappingIDs: []uint{f.mapping.ID}}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			fmt.Fprint(w, `{"data":{"access_token":"promotion-token","expire_in":7200},"success":true,"code":0}`)
		case "/api/rmp/mp/deal/order/upsert":
			calls.Add(1)
			var request xiaohongshu.OrderUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if len(request.Products) != 1 || request.Products[0].SalePrice != int64(request.Products[0].Count)*100 || request.Products[0].RealPrice != request.Price.OrderPrice {
				t.Errorf("invalid request: %+v", request)
			}
			fmt.Fprintf(w, `{"data":{"out_order_id":%q,"order_id":%q,"final_price":%d,"pay_token":"promotion-pay","expired_time":%d,"open_pay_type":"life_gpay"},"success":true,"code":0}`, request.ExternalOrderID, "platform-"+request.ExternalOrderID, request.Price.OrderPrice, time.Now().Add(15*time.Minute).Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	s := XiaohongshuOrderService{Now: func() time.Time { return now }, NewXiaohongshuClient: func(appID, secret, env string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
	return f, p, s, &calls, &now
}

func TestMiniappPromotionOrderLocksPriceAndRefundAllocation(t *testing.T) {
	f, p, s, calls, now := promotionOrderFixture(t)
	grant, err := p.AcquireOpportunity(&f.customer)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, ClientRequestID: "promotion-create", QuoteToken: quote.QuoteToken}
	result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.AmountCents != 199 || result.OriginalAmountCents != 200 || result.DiscountCents != 1 {
		t.Fatalf("result=%+v", result)
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Where("order_no = ?", result.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	item := order.Items[0]
	if item.Price != 1 || item.SaleAmountCents == nil || *item.SaleAmountCents != 199 || *item.Tickets[0].SaleAmountCents != 100 || *item.Tickets[1].SaleAmountCents != 99 {
		t.Fatalf("allocation=%+v", item)
	}
	_, amount, err := selectRefundTickets(&order, []string{item.Tickets[0].TicketCode, item.Tickets[1].TicketCode}, false, false, 0)
	if err != nil || moneyCents(amount) != 199 {
		t.Fatalf("refund amount=%v err=%v", amount, err)
	}
	*now = now.Add(2 * time.Minute)
	retry, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil || retry.OrderNo != result.OrderNo || retry.AmountCents != 199 || calls.Load() != 1 {
		t.Fatalf("retry=%+v calls=%d err=%v", retry, calls.Load(), err)
	}
	if err := (&OrderService{}).Cancel(order.OrderNo, order.TenantID); err == nil {
		t.Fatal("cancelled an unresolved provider order")
	}
	var stored model.MiniappInstantDiscountGrant
	if err := model.DB.First(&stored, grant.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ReservedOrderID != order.ID {
		t.Fatal("expired discount lost pending order reservation")
	}
}

func TestMiniappPromotionConcurrentOrderUsesGrantOnce(t *testing.T) {
	f, p, s, calls, _ := promotionOrderFixture(t)
	if _, err := p.AcquireOpportunity(&f.customer); err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: fmt.Sprintf("promotion-concurrent-%d", i), QuoteToken: quote.QuoteToken}); err == nil {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("successes=%d external calls=%d", successes.Load(), calls.Load())
	}
}

func TestMiniappPromotionPaymentRefundAndNextOpportunity(t *testing.T) {
	f, p, s, _, now := promotionOrderFixture(t)
	grant, err := p.AcquireOpportunity(&f.customer)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, ClientRequestID: "promotion-paid", QuoteToken: quote.QuoteToken})
	if err != nil {
		t.Fatal(err)
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("order_no = ?", result.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("order_id = ?", order.ID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	// The platform returns the smaller allocation first: bind by cents, not index.
	platform := &xiaohongshu.GuaranteeOrderResponse{OrderID: result.PlatformOrderID, PayAmount: 199, TradeNo: "promotion-trade", OrderStatus: 6, Vouchers: []xiaohongshu.VoucherInfo{{Code: "promotion-v2", Status: 1, PayAmount: 99}, {Code: "promotion-v1", Status: 1, PayAmount: 100}}}
	*now = now.Add(2 * time.Minute)
	if err := s.completeXiaohongshuOrder(&link, &order, platform); err != nil {
		t.Fatal(err)
	}
	if err := s.completeXiaohongshuOrder(&link, &order, platform); err != nil {
		t.Fatalf("duplicate paid query: %v", err)
	}
	day := time.Now().Format("2006-01-02")
	stats, err := (&ReportService{}).GetProductStats(f.tenantID, day, day)
	if err != nil || len(stats) != 1 || moneyCents(stats[0].TotalAmount) != 199 || stats[0].TotalSold != 2 {
		t.Fatalf("discounted sales report=%+v err=%v", stats, err)
	}
	codes := []string{order.Items[0].Tickets[0].TicketCode, order.Items[0].Tickets[1].TicketCode}
	refund, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: f.tenantID}, order.OrderNo, "promotion-refund", 1.99, codes, "游客退票")
	if err != nil {
		t.Fatal(err)
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	refunds := &RefundService{NewXiaohongshuClient: func(appID, secret, env string) *xiaohongshu.Client {
		return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
	}}
	runXiaohongshuRefundWorker(t, refunds, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, refunds, time.Now().Add(time.Minute))
	if err := model.DB.First(refund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" || fake.addRequest.Price.RefundPrice != 199 || len(fake.addRequest.Vouchers) != 2 || fake.addRequest.Vouchers[0].RefundPrice+fake.addRequest.Vouchers[1].RefundPrice != 199 {
		t.Fatalf("refund=%+v request=%+v", refund, fake.addRequest)
	}
	for _, voucher := range fake.addRequest.Vouchers {
		if (voucher.VoucherCode == "promotion-v1" && voucher.RefundPrice != 100) || (voucher.VoucherCode == "promotion-v2" && voucher.RefundPrice != 99) {
			t.Fatalf("voucher amount binding changed: %+v", voucher)
		}
	}
	stats, err = (&ReportService{}).GetProductStats(f.tenantID, day, day)
	if err != nil || len(stats) != 1 || moneyCents(stats[0].TotalAmount) != 0 || stats[0].TotalSold != 0 {
		t.Fatalf("refunded sales report=%+v err=%v", stats, err)
	}
	after, err := p.AcquireOpportunity(&f.customer)
	if err != nil || after.GrantID != grant.GrantID || after.Status != "cooldown" {
		t.Fatalf("refund restored discount: %+v %v", after, err)
	}
	*now = now.Add(8 * 24 * time.Hour)
	after, err = p.AcquireOpportunity(&f.customer)
	if err != nil || after.GrantID == grant.GrantID || after.Status != "available" {
		t.Fatalf("next opportunity: %+v %v", after, err)
	}
}

func TestMiniappPromotionDisableReenableAndConfirmedCancel(t *testing.T) {
	f, p, s, _, _ := promotionOrderFixture(t)
	grant, err := p.AcquireOpportunity(&f.customer)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "promotion-disable", QuoteToken: quote.QuoteToken}
	settings := MiniappInstantDiscountConfig{MinDiscountCents: 5, MaxDiscountCents: 5, ValidityMinutes: 10, CooldownDays: 1, MappingIDs: []uint{f.mapping.ID}}
	if _, err := p.SaveConfig(f.tenantID, f.account.ID, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input); err == nil {
		t.Fatal("disabled activity accepted old quote")
	}
	settings.Enabled = true
	if _, err := p.SaveConfig(f.tenantID, f.account.ID, settings); err != nil {
		t.Fatal(err)
	}
	after, err := p.AcquireOpportunity(&f.customer)
	if err != nil || after.GrantID != grant.GrantID || after.DiscountCents != 1 || after.ExpiresAt.UnixMicro() != grant.ExpiresAt.UnixMicro() || after.NextEligibleAt.UnixMicro() != grant.NextEligibleAt.UnixMicro() {
		t.Fatalf("settings reset grant: %+v %v", after, err)
	}
	result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil {
		t.Fatal(err)
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("order_no = ?", result.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return cancelOrderTxProvider(tx, &order, false, true) }); err != nil {
		t.Fatal(err)
	}
	after, err = p.AcquireOpportunity(&f.customer)
	if err != nil || after.GrantID != grant.GrantID || after.Status != "available" || after.DiscountCents != 1 {
		t.Fatalf("cancel release: %+v %v", after, err)
	}
}

func TestMiniappPromotionRejectsCrossScopeAndFrozenAccount(t *testing.T) {
	f, p, _, _, _ := promotionOrderFixture(t)
	otherTenant, _ := seedSellableProduct(t, "unlimited", 0)
	forged := f.customer
	forged.TenantID = otherTenant
	if _, err := p.AcquireOpportunity(&forged); err == nil {
		t.Fatal("cross-tenant grant accepted")
	}
	if _, err := p.QuoteForCustomer(&forged, f.mapping.ID, 1); err == nil {
		t.Fatal("cross-tenant quote accepted")
	}
	if _, err := p.SaveConfig(otherTenant, f.account.ID, MiniappInstantDiscountConfig{}); err == nil {
		t.Fatal("cross-tenant config accepted")
	}
	otherAccount := model.ChannelAccount{Code: "promotion-other", Status: "sandbox"}
	if err := (&ChannelService{}).CreateXiaohongshu(otherTenant, &otherAccount, "promotion-other-app", "secret"); err != nil {
		t.Fatal(err)
	}
	forged = f.customer
	forged.ChannelAccountID = otherAccount.ID
	if _, err := p.AcquireOpportunity(&forged); err == nil {
		t.Fatal("cross-account grant accepted")
	}
	if err := model.DB.Model(&f.account).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := p.AcquireOpportunity(&f.customer); err == nil {
		t.Fatal("disabled account grant accepted")
	}
	if _, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1); err == nil {
		t.Fatal("disabled account quote accepted")
	}
}

func TestMiniappPromotionVoucherAmountMismatchHoldsIssuance(t *testing.T) {
	f, p, s, _, _ := promotionOrderFixture(t)
	if _, err := p.AcquireOpportunity(&f.customer); err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 2, ClientRequestID: "promotion-bad-voucher", QuoteToken: quote.QuoteToken})
	if err != nil {
		t.Fatal(err)
	}
	var order model.Order
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("order_no = ?", result.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("order_id = ?", order.ID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	platform := &xiaohongshu.GuaranteeOrderResponse{PayAmount: 199, TradeNo: "promotion-mismatch", Vouchers: []xiaohongshu.VoucherInfo{{Code: "bad-a", PayAmount: 100}, {Code: "bad-b", PayAmount: 100}}}
	if err := s.completeXiaohongshuOrder(&link, &order, platform); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&link, link.ID).Error; err != nil {
		t.Fatal(err)
	}
	var bindings int64
	if err := model.DB.Model(&model.XiaohongshuVoucherLink{}).Where("xiaohongshu_order_link_id = ?", link.ID).Count(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if link.State != "paid" || link.VoucherIssuanceStatus != "manual_review" || bindings != 0 {
		t.Fatalf("mismatch lost payment or issued tickets: link=%+v bindings=%d", link, bindings)
	}
}

func TestMiniappPromotionStalePaidAndClosedQueries(t *testing.T) {
	for _, discounted := range []bool{false, true} {
		for _, paidWins := range []bool{false, true} {
			t.Run(fmt.Sprintf("discount=%t/paid_wins=%t", discounted, paidWins), func(t *testing.T) {
				f, p, s, _, _ := promotionOrderFixture(t)
				if err := model.DB.Model(&model.Product{}).Where("id = ?", f.mapping.ProductID).Updates(map[string]interface{}{"stock_type": "total", "daily_stock": 2}).Error; err != nil {
					t.Fatal(err)
				}
				if discounted {
					if _, err := p.AcquireOpportunity(&f.customer); err != nil {
						t.Fatal(err)
					}
				}
				quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "stale-query", QuoteToken: quote.QuoteToken})
				if err != nil {
					t.Fatal(err)
				}
				var order model.Order
				var link model.XiaohongshuOrderLink
				if err := model.DB.Where("order_no = ?", result.OrderNo).First(&order).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.DB.Where("order_id = ?", order.ID).First(&link).Error; err != nil {
					t.Fatal(err)
				}
				started, release := make(chan struct{}), make(chan struct{})
				var queryCount atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/api/rmp/token" {
						fmt.Fprint(w, `{"data":{"access_token":"stale-token","expire_in":7200},"success":true,"code":0}`)
						return
					}
					paid := paidWins
					if queryCount.Add(1) == 1 {
						paid = !paidWins
						close(started)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					response := xiaohongshu.GuaranteeOrderResponse{OrderID: result.PlatformOrderID, OrderStatus: 71}
					if paid {
						response.OrderStatus, response.PayAmount, response.TradeNo = 6, result.AmountCents, "stale-trade"
						response.Vouchers = []xiaohongshu.VoucherInfo{{Code: "stale-voucher", Status: 1, PayAmount: result.AmountCents}}
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "code": 0, "data": response})
				}))
				defer server.Close()
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				s.NewXiaohongshuClient = func(appID, secret, env string) *xiaohongshu.Client {
					return &xiaohongshu.Client{AppID: appID, Secret: secret, BaseURL: server.URL, HTTP: server.Client()}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				oldOrder, oldLink := order, link
				loser := make(chan error, 1)
				go func() { _, err := s.refreshXiaohongshuOrder(ctx, &f.customer, &oldLink, &oldOrder); loser <- err }()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if _, err := s.refreshXiaohongshuOrder(ctx, &f.customer, &link, &order); err != nil {
					t.Fatal(err)
				}
				unblock()
				if err := <-loser; err == nil {
					t.Fatal("stale conflicting terminal response accepted")
				}
				if err := model.DB.First(&order, order.ID).Error; err != nil {
					t.Fatal(err)
				}
				var payments int64
				if err := model.DB.Model(&model.Payment{}).Where("order_no = ?", order.OrderNo).Count(&payments).Error; err != nil {
					t.Fatal(err)
				}
				var product model.Product
				if err := model.DB.First(&product, f.mapping.ProductID).Error; err != nil {
					t.Fatal(err)
				}
				if paidWins && (order.Status != "paid" || payments != 1 || product.DailyStock != 1) {
					t.Fatalf("paid winner lost facts: status=%s payments=%d stock=%d", order.Status, payments, product.DailyStock)
				}
				if !paidWins && (order.Status != "cancelled" || payments != 0 || product.DailyStock != 2) {
					t.Fatalf("closed winner resurrected: status=%s payments=%d stock=%d", order.Status, payments, product.DailyStock)
				}
				if discounted {
					var grant model.MiniappInstantDiscountGrant
					if err := model.DB.First(&grant, order.PromotionGrantID).Error; err != nil {
						t.Fatal(err)
					}
					if paidWins && (grant.ReservedOrderID != order.ID || grant.ConsumedAt == nil) {
						t.Fatalf("paid grant=%+v", grant)
					}
					if !paidWins && (grant.ReservedOrderID != 0 || grant.ConsumedAt != nil) {
						t.Fatalf("closed grant=%+v", grant)
					}
				}
			})
		}
	}
}
