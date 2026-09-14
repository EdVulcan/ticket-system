package service

import (
	"context"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestUpstreamRefreshQueuesAndCoalescesWithoutSupplierCalls(t *testing.T) {
	order, _ := seedReadyUpstreamRefund(t)
	for i := 0; i < 5; i++ {
		if err := RefreshUpstreamOrder(context.Background(), order.TenantID, order.OrderNo); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	if snapshot.SyncRequestedAt == nil || snapshot.LastSyncedAt != nil {
		t.Fatal("refresh should queue without contacting the invalid supplier endpoint")
	}
	first := *snapshot.SyncRequestedAt
	if err := RefreshUpstreamOrder(context.Background(), order.TenantID+99, order.OrderNo); err == nil {
		t.Fatal("cross-tenant refresh accepted")
	}
	if err := RefreshUpstreamOrder(context.Background(), order.TenantID, order.OrderNo); err != nil {
		t.Fatal(err)
	}
	model.DB.First(&snapshot, snapshot.ID)
	if !snapshot.SyncRequestedAt.Equal(first) {
		t.Fatal("repeated click reset queue age")
	}
	rows, err := GetUpstreamOrderView(order.TenantID, order.OrderNo)
	if err != nil || !rows[0].SyncPending {
		t.Fatalf("queue projection: %+v %v", rows, err)
	}
}

func TestUpstreamPollingCadencePrioritizesActiveBusiness(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	snapshot := model.OrderItemSupplySnapshot{IssueStatus: "pending"}
	issue := upstreamPollDelay(snapshot, nil, now)
	snapshot.IssueStatus = "ready"
	today := upstreamPollDelay(snapshot, nil, now)
	future := now.AddDate(0, 0, 1)
	later := upstreamPollDelay(snapshot, &future, now)
	snapshot.ProviderStatus = "checked"
	used := upstreamPollDelay(snapshot, nil, now)
	if issue >= today || today >= used || today >= later {
		t.Fatalf("bad priorities %v %v %v %v", issue, today, used, later)
	}
	snapshot.IssueStatus = "pending"
	snapshot.SyncFailureCount = 3
	if upstreamPollDelay(snapshot, nil, now) <= today {
		t.Fatal("failed issuance did not back off")
	}
}
