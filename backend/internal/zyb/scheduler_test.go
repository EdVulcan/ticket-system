package zyb

import (
	"context"
	"errors"
	"testing"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
)

func TestDeferredAndRateLimitErrorsExposeRetryTimes(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	deferred := &DeferredError{RetryAt: now.Add(5 * time.Second)}
	if got, ok := RetryAt(deferred, now); !ok || !got.Equal(deferred.RetryAt) {
		t.Fatalf("deferred retry=%v ok=%v", got, ok)
	}
	limited := &RateLimitError{RetryAfter: 7 * time.Second, StatusCode: 429}
	if got, ok := RetryAt(limited, now); !ok || !got.Equal(now.Add(7*time.Second)) {
		t.Fatalf("limited retry=%v ok=%v", got, ok)
	}
	if _, ok := RetryAt(errors.New("ordinary failure"), now); ok {
		t.Fatal("ordinary errors must not claim a certified retry time")
	}
}

func TestSemanticDispatchKeyScopesExplicitKeysByConnection(t *testing.T) {
	first := semanticDispatchKey("issuance:42", "endpoint-a|corp-a|user-a", "SEND_CODE_REQ", "body")
	second := semanticDispatchKey("issuance:42", "endpoint-b|corp-b|user-b", "SEND_CODE_REQ", "body")
	if first == second {
		t.Fatal("explicit semantic keys must remain scoped to the connection identity")
	}
	if first == semanticDispatchKey("issuance:42", "endpoint-a|corp-a|user-a", "QUERY_ORDER_NEW_REQ", "body") {
		t.Fatal("transaction must be part of semantic identity")
	}
}

func TestPositiveJitterDefaultIsBounded(t *testing.T) {
	s := NewScheduler(nil, SchedulerConfig{RequestInterval: time.Millisecond})
	for i := 0; i < 20; i++ {
		jitter := s.jitter(time.Minute)
		if jitter <= 0 || jitter > 5*time.Second+time.Millisecond {
			t.Fatalf("jitter=%s is outside the positive bounded range", jitter)
		}
	}
}

// Keep the PostgreSQL integration tests in this file behind the opt-in
// environment used by CI. Local unit runs do not need to create databases.
func TestSchedulerPostgresLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("PostgreSQL scheduler integration test")
	}
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.UpstreamDispatchGate{}, &model.UpstreamDispatchWaiter{}); err != nil {
		t.Fatal(err)
	}
	config := SchedulerConfig{EgressKey: "zyb:test", ConnectionIdentity: "connection-a", RequestInterval: time.Millisecond, Cooldown: 20 * time.Millisecond, MaxCooldown: time.Second, Lease: 100 * time.Millisecond, AcquireWait: 20 * time.Millisecond, Jitter: func(time.Duration) time.Duration { return 0 }}
	scheduler := NewScheduler(db, config)
	permit, err := scheduler.Acquire(context.Background(), "SEND_CODE_REQ", "body-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var waiter model.UpstreamDispatchWaiter
	if err := db.Where("transport_started_at IS NOT NULL").First(&waiter).Error; err != nil {
		t.Fatal(err)
	}
	if waiter.Status != "started" {
		t.Fatalf("status=%q, want started", waiter.Status)
	}
	if _, err := scheduler.Acquire(context.Background(), "SEND_CODE_REQ", "body-b"); err == nil {
		t.Fatal("concurrent request unexpectedly acquired the single slot")
	} else {
		var deferred *DeferredError
		if !errors.As(err, &deferred) {
			t.Fatalf("error=%T %v, want DeferredError", err, err)
		}
	}
	if err := permit.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
