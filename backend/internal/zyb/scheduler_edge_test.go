package zyb

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"time"
)

func schedulerEdgeDB(t *testing.T) *gorm.DB {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.UpstreamDispatchGate{}, &model.UpstreamDispatchWaiter{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSchedulerPacesActualStartAfterDelayedPermit(t *testing.T) {
	db := schedulerEdgeDB(t)
	s := NewScheduler(db, SchedulerConfig{RequestInterval: time.Second, AcquireWait: time.Millisecond})
	p, err := s.Acquire(context.Background(), "READ", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.UpstreamDispatchGate{}).Where("egress_key = ?", dispatchEgressKey).Update("next_allowed_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer p.Finish(context.Background(), nil)
	var row model.UpstreamDispatchWaiter
	db.Where("status='started'").First(&row)
	var gate model.UpstreamDispatchGate
	db.Where("egress_key = ?", dispatchEgressKey).First(&gate)
	if gate.NextAllowedAt == nil || row.TransportStartedAt == nil || gate.NextAllowedAt.Before(row.TransportStartedAt.Add(900*time.Millisecond)) {
		t.Fatal("delayed transport did not reserve its actual start interval")
	}
}

func TestAbandonedInteractiveWaiterDoesNotBlockAnotherOrder(t *testing.T) {
	db := schedulerEdgeDB(t)
	stale := model.UpstreamDispatchWaiter{EgressKey: dispatchEgressKey, SemanticKey: "abandoned", Transaction: "READ", BodyHash: "hash", Priority: 30, EnqueuedAt: time.Now().Add(-time.Hour), Status: "queued"}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&stale).UpdateColumn("updated_at", time.Now().Add(-2*time.Minute))
	s := NewScheduler(db, SchedulerConfig{RequestInterval: time.Millisecond, AcquireWait: 5 * time.Millisecond})
	p, err := s.Acquire(context.Background(), "READ", "live")
	if err != nil {
		t.Fatalf("abandoned caller blocks all requests: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerIntentAndTransportStartAreAtomic(t *testing.T) {
	db := schedulerEdgeDB(t)
	db.Exec("CREATE TABLE intent_probe (id integer PRIMARY KEY)")
	s := NewScheduler(db, SchedulerConfig{RequestInterval: time.Millisecond, AcquireWait: time.Millisecond})
	ctx := WithDispatchMetadata(context.Background(), DispatchMetadata{Key: "intent", BeforeStart: func(tx *gorm.DB) error { return tx.Exec("INSERT INTO intent_probe VALUES (1)").Error }})
	p, err := s.Acquire(ctx, "SEND_CODE_REQ", "body")
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Table("intent_probe").Count(&count)
	if count != 0 {
		t.Fatal("acquisition wrote a send marker")
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	db.Table("intent_probe").Count(&count)
	if count != 1 {
		t.Fatal("transport did not persist intent")
	}
	if err := p.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	bad := WithDispatchMetadata(context.Background(), DispatchMetadata{Key: "failed-intent", BeforeStart: func(tx *gorm.DB) error {
		tx.Exec("INSERT INTO intent_probe VALUES (2)")
		return errors.New("abort before send")
	}})
	p, err = s.Acquire(bad, "SEND_CODE_REQ", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(bad); err == nil {
		t.Fatal("failed intent allowed HTTP start")
	}
	db.Table("intent_probe").Count(&count)
	if count != 1 {
		t.Fatal("failed intent did not roll back")
	}
}

func TestSchedulerCooldownSurvivesAnotherInstance(t *testing.T) {
	db := schedulerEdgeDB(t)
	cfg := SchedulerConfig{RequestInterval: time.Millisecond, Cooldown: time.Minute, AcquireWait: time.Millisecond, Jitter: func(time.Duration) time.Duration { return 0 }}
	s := NewScheduler(db, cfg)
	p, err := s.Acquire(context.Background(), "READ", "before-limit")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	limited := &RateLimitError{RetryAfter: 2 * time.Minute, StatusCode: 429}
	if err := p.Finish(context.Background(), limited); err != nil {
		t.Fatal(err)
	}
	other := NewScheduler(db, cfg)
	_, err = other.Acquire(context.Background(), "READ", "another-connection")
	var deferred *DeferredError
	if !errors.As(err, &deferred) {
		t.Fatalf("cooldown bypassed: %v", err)
	}
	var gate model.UpstreamDispatchGate
	db.Where("egress_key = ?", dispatchEgressKey).First(&gate)
	if gate.CooldownUntil == nil || deferred.RetryAt.Before(*gate.CooldownUntil) {
		t.Fatal("retry ignored shared Retry-After")
	}
}

func TestAgedBackgroundWaiterOutranksNewCriticalWork(t *testing.T) {
	db := schedulerEdgeDB(t)
	future := time.Now().Add(time.Hour)
	if err := db.Create(&model.UpstreamDispatchGate{EgressKey: dispatchEgressKey, NextAllowedAt: &future}).Error; err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(db, SchedulerConfig{RequestInterval: time.Millisecond, AcquireWait: time.Millisecond})
	bg := WithDispatchMetadata(context.Background(), DispatchMetadata{Key: "background", Priority: 0})
	if _, err := s.Acquire(bg, "READ", "background"); err == nil {
		t.Fatal("pacing bypassed")
	}
	db.Model(&model.UpstreamDispatchWaiter{}).Where("priority = 0").UpdateColumn("enqueued_at", time.Now().Add(-20*time.Minute))
	db.Model(&model.UpstreamDispatchGate{}).Where("egress_key = ?", dispatchEgressKey).UpdateColumn("next_allowed_at", time.Now().Add(-time.Hour))
	urgent := WithDispatchMetadata(context.Background(), DispatchMetadata{Key: "urgent", Priority: 30})
	if _, err := s.Acquire(urgent, "READ", "urgent"); err == nil {
		t.Fatal("new urgent work starved old background work")
	}
	var active int64
	db.Model(&model.UpstreamDispatchWaiter{}).Where("status IN ?", []string{"leased", "started"}).Count(&active)
	if active != 0 {
		t.Fatal("caller created a permit for an absent caller")
	}
	p, err := s.Acquire(bg, "READ", "background")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(bg); err != nil {
		t.Fatal(err)
	}
	if err := p.Finish(bg, nil); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentSchedulersShareOneConcurrentSlot(t *testing.T) {
	db := schedulerEdgeDB(t)
	start := make(chan struct{})
	type result struct {
		permit RequestPermit
		err    error
	}
	results := make(chan result, 2)
	for _, body := range []string{"instance-a", "instance-b"} {
		go func(body string) {
			s := NewScheduler(db, SchedulerConfig{RequestInterval: time.Millisecond, AcquireWait: time.Millisecond})
			<-start
			p, err := s.Acquire(context.Background(), "READ", body)
			results <- result{p, err}
		}(body)
	}
	close(start)
	var winner RequestPermit
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			if winner != nil {
				t.Fatal("two instances acquired the same egress concurrently")
			}
			winner = r.permit
		} else {
			var deferred *DeferredError
			if !errors.As(r.err, &deferred) {
				t.Fatal(r.err)
			}
		}
	}
	if winner == nil {
		t.Fatal("no instance could acquire an idle egress")
	}
	if err := winner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := winner.Finish(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
