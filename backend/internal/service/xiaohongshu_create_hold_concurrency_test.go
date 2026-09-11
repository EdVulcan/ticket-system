package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

// The transaction barrier uses real PostgreSQL blocking, not a timing guess.
func waitForXhsBlockedTransaction(t *testing.T, blockingPID int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := model.DB.Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", blockingPID).Scan(&blocked).Error; err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-deadline:
			t.Fatal("order did not reach the PostgreSQL transaction barrier")
		case <-ticker.C:
		}
	}
}

func xhsHoldTestEvent(t *testing.T, f miniappPromotionFixture) (model.XiaohongshuWebhookEvent, []byte) {
	t.Helper()
	payload := []byte(`{"Event":"AFTER_SALE_REFUND","OrderId":"unknown-order","AfterSaleId":"concurrent-account-hold"}`)
	event := model.XiaohongshuWebhookEvent{TenantID: f.tenantID, ChannelAccountID: f.account.ID, PayloadHash: fmt.Sprintf("hold-%d", time.Now().UnixNano()), EventType: "AFTER_SALE_REFUND", PayloadCiphertext: "test-evidence", Status: "manual_review", ReceivedAt: time.Now()}
	if err := model.DB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	return event, payload
}

func assertXhsOrderFactCounts(t *testing.T, accountID uint, want int64) {
	t.Helper()
	for _, table := range []interface{}{&model.Order{}, &model.XiaohongshuOrderLink{}, &model.XiaohongshuOrderOperation{}} {
		var count int64
		if err := model.DB.Model(table).Where("channel_account_id = ?", accountID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%T count=%d want=%d", table, count, want)
		}
	}
}

func TestXiaohongshuCreateWaitsForAccountHoldBeforeReserving(t *testing.T) {
	f, p, s, calls, _ := promotionOrderFixture(t)
	grant, err := p.AcquireOpportunity(&f.customer)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	event, payload := xhsHoldTestEvent(t, f)
	tx := model.DB.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := CreateXiaohongshuRefundCoordinationTx(tx, &f.account, &event, payload); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, createErr := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "hold-wins", QuoteToken: quote.QuoteToken, GuestName: "售后联系人", ContactPhone: "13800138000"})
		done <- createErr
	}()
	waitForXhsBlockedTransaction(t, pid)
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var outcome *MiniappOrderCreateError
		if !errors.As(err, &outcome) || outcome.Code != "order_not_created" {
			t.Fatalf("hold must reject without order: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("order remained blocked after hold commit")
	}
	assertXhsOrderFactCounts(t, f.account.ID, 0)
	if calls.Load() != 0 {
		t.Fatalf("upsert calls=%d", calls.Load())
	}
	var stored model.MiniappInstantDiscountGrant
	if err := model.DB.First(&stored, grant.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ReservedOrderID != 0 || stored.ConsumedAt != nil {
		t.Fatalf("rejected order changed grant: %+v", stored)
	}
}

func TestXiaohongshuCreateCommitWinsHoldAndReplayKeepsOriginalOrder(t *testing.T) {
	f, p, s, calls, _ := promotionOrderFixture(t)
	if _, err := p.AcquireOpportunity(&f.customer); err != nil {
		t.Fatal(err)
	}
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	event, payload := xhsHoldTestEvent(t, f)
	remoteReached, releaseRemote := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRemote) }) }
	defer release()
	client := s.NewXiaohongshuClient
	s.NewXiaohongshuClient = func(appID, secret, env string) *xiaohongshu.Client {
		close(remoteReached)
		<-releaseRemote
		return client(appID, secret, env)
	}
	input := MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "order-wins", QuoteToken: quote.QuoteToken, GuestName: "并发联系人", ContactPhone: "13800138000"}
	type completion struct {
		result *MiniappOrderResult
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		result, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
		done <- completion{result, err}
	}()
	select {
	case <-remoteReached:
	case <-time.After(10 * time.Second):
		t.Fatal("order did not reach provider after commit")
	}
	// A provider call may be slow; its account lock must already be released.
	holdDone := make(chan error, 1)
	go func() {
		holdDone <- model.Write(func(tx *gorm.DB) error { return CreateXiaohongshuRefundCoordinationTx(tx, &f.account, &event, payload) })
	}()
	select {
	case err := <-holdDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider I/O retained the order's account lock")
	}
	release()
	var first *MiniappOrderResult
	select {
	case completion := <-done:
		if completion.err != nil {
			t.Fatal(completion.err)
		}
		first = completion.result
	case <-time.After(10 * time.Second):
		t.Fatal("order did not finish")
	}
	replay, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	if err != nil || replay.OrderNo != first.OrderNo {
		t.Fatalf("recovery blocked by later hold: result=%+v err=%v", replay, err)
	}
	input.Quantity = 2
	_, err = s.CreateXiaohongshuOrder(context.Background(), &f.customer, input)
	var mismatch *MiniappOrderCreateError
	if !errors.As(err, &mismatch) || mismatch.Code != "idempotency_payload_mismatch" || mismatch.OrderNo != first.OrderNo {
		t.Fatalf("conflicting replay: %v", err)
	}
	assertXhsOrderFactCounts(t, f.account.ID, 1)
	if calls.Load() != 1 {
		t.Fatalf("recovery created a second platform order: calls=%d", calls.Load())
	}
}

func TestXiaohongshuCreateUsesSameLockOrderAsAnotherCustomerQuote(t *testing.T) {
	f, p, s, _, _ := promotionOrderFixture(t)
	quote, err := p.QuoteForCustomer(&f.customer, f.mapping.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	other := f.customer
	other.Base = model.Base{}
	other.OpenIDHash = hashMiniappValue("other-quote-customer")
	other.SessionTokenHash = hashMiniappValue("other-quote-session")
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	tx := model.DB.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if _, err := lockMiniappPromotionCustomerTx(tx, &other); err != nil {
		t.Fatal(err)
	}
	if _, err := lockMiniappPromotionActivityTx(tx, f.tenantID, f.account.ID); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateXiaohongshuOrder(context.Background(), &f.customer, MiniappOrderCreateInput{MappingID: f.mapping.ID, Quantity: 1, ClientRequestID: "quote-lock-order", QuoteToken: quote.QuoteToken, GuestName: "锁定联系人", ContactPhone: "13800138000"})
		done <- err
	}()
	waitForXhsBlockedTransaction(t, pid)
	// A waiting create must not already own the mapping the quote needs next.
	if err := tx.Exec("SET LOCAL lock_timeout = '1s'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := loadMiniappQuoteMappingTx(tx, f.tenantID, f.account.ID, f.mapping.ID, true); err != nil {
		t.Fatalf("quote/create lock order inverted: %v", err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("order stayed blocked after quote released its locks")
	}
}
