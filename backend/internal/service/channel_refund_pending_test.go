package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestChannelRefundPendingProjectionTracksTicketReservation(t *testing.T) {
	fixture := seedXiaohongshuRefundFixture(t)
	channel := &ChannelService{}
	check := func(wantPending bool, wantStatus string) {
		t.Helper()
		rows, total, err := channel.ListOrders(fixture.tenantID, fixture.account.ID, fixture.order.OrderNo, "", 1, 20)
		if err != nil || total != 1 || len(rows) != 1 || rows[0].RefundPending != wantPending || rows[0].Status != wantStatus {
			t.Fatalf("rows=%+v total=%d error=%v", rows, total, err)
		}
	}
	check(false, "paid")
	createXiaohongshuRefund(t, fixture, "channel-refund-pending")
	check(true, "paid")
	other := model.ChannelAccount{TenantID: fixture.tenantID, Code: "other-pending-channel", Type: "ota", Status: "active"}
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ tenant, account uint }{{fixture.tenantID, other.ID}, {fixture.tenantID + 10000, fixture.account.ID}} {
		rows, total, err := channel.ListOrders(scope.tenant, scope.account, "", "", 1, 20)
		if err != nil || total != 0 || len(rows) != 0 {
			t.Fatalf("scope leaked rows: %+v %d %v", rows, total, err)
		}
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	service := xiaohongshuRefundServiceForTest(t, server)
	now := time.Now().Add(time.Second)
	runXiaohongshuRefundWorker(t, service, now)
	runXiaohongshuRefundWorker(t, service, now.Add(3*time.Second))
	if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 0 {
		t.Fatal("faster worker ignored the provider query due time")
	}
	check(true, "paid")
	runXiaohongshuRefundWorker(t, service, now.Add(time.Minute))
	check(false, "refunded")
}
