package service

import (
	"context"
	"gorm.io/gorm"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

type blockedQueryBudget struct{ calls int }

func (g *blockedQueryBudget) Acquire(context.Context, string, string) (zyb.RequestPermit, error) {
	g.calls++
	return nil, &zyb.DeferredError{RetryAt: time.Now().Add(time.Hour)}
}

func TestOrderSubmissionBypassesQueryCooldownButKeepsIntent(t *testing.T) {
	order := seedUpstreamWorkerOrder(t, "https://supplier.example/api")
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	budget := &blockedQueryBudget{}
	routes := upstreamRequestRoutes{queries: budget, db: model.DB}
	ctx := WithUpstreamDispatch(context.Background(), "issue-route", 20, func(tx *gorm.DB) error {
		return tx.Model(&snapshot).Update("issue_attempted_at", time.Now()).Error
	})
	p, err := routes.Acquire(ctx, "SEND_CODE_REQ", "body")
	if err != nil || budget.calls != 0 {
		t.Fatalf("order waited for query budget: %v", err)
	}
	var before model.OrderItemSupplySnapshot
	model.DB.First(&before, snapshot.ID)
	if before.IssueAttemptedAt != nil {
		t.Fatal("intent written before start")
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	model.DB.First(&before, snapshot.ID)
	if before.IssueAttemptedAt == nil {
		t.Fatal("order sent without durable intent")
	}
	if err := p.Start(ctx); err == nil {
		t.Fatal("same order permit reused")
	}
	if _, err := routes.Acquire(context.Background(), "SEND_CODE_REQ", "body"); err == nil {
		t.Fatal("order without intent hook accepted")
	}
	for _, kind := range []string{"QUERY_ORDER_NEW_REQ", "CHECK_STATUS_QUERY_REQ", "SEND_CODE_IMG_REQ", "SEND_CODE_CANCEL_NEW_REQ"} {
		if _, err := routes.Acquire(ctx, kind, "body"); err == nil {
			t.Fatalf("unconfirmed operation bypassed budget: %s", kind)
		}
	}
	if budget.calls != 4 {
		t.Fatal("query routing did not share the budget")
	}
}
