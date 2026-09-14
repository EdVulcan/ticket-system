package service

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"sync"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

func newUpstreamDispatchGate(connection model.UpstreamConnection) zyb.RequestGate {
	cfg := config.GlobalConfig.UpstreamScheduling.Effective()
	return upstreamRequestRoutes{queries: zyb.NewScheduler(model.DB, zyb.SchedulerConfig{
		ConnectionIdentity: fmt.Sprintf("connection:%d", connection.ID),
		RequestInterval:    time.Duration(cfg.RequestIntervalMS) * time.Millisecond,
		Cooldown:           time.Duration(cfg.CooldownSeconds) * time.Second,
		MaxCooldown:        time.Duration(cfg.MaxCooldownSeconds) * time.Second,
		Concurrency:        1,
	}), db: model.DB}
}

// Only order submission is confirmed to be outside the query quota. Image
// retrieval and cancellation remain paced until their provider rules are known.
type upstreamRequestRoutes struct {
	queries zyb.RequestGate
	db      *gorm.DB
}

func (g upstreamRequestRoutes) Acquire(ctx context.Context, transaction, body string) (zyb.RequestPermit, error) {
	if transaction != "SEND_CODE_REQ" {
		return g.queries.Acquire(ctx, transaction, body)
	}
	before := zyb.DispatchMetadataFromContext(ctx).BeforeStart
	if before == nil {
		return nil, errors.New("上游下单缺少持久发送标记")
	}
	return &upstreamOrderPermit{db: g.db, before: before}, nil
}

// Bypassing query pacing must never bypass durable issuance intent. Once
// Start commits, the existing order marker still forces query-only recovery.
type upstreamOrderPermit struct {
	db      *gorm.DB
	before  func(*gorm.DB) error
	mu      sync.Mutex
	started bool
}

func (p *upstreamOrderPermit) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return errors.New("上游下单请求已开始")
	}
	if p.db == nil {
		return &zyb.DeferredError{RetryAt: time.Now().Add(time.Minute), Cause: zyb.ErrRequestGateUnavailable}
	}
	if err := p.db.WithContext(ctx).Transaction(p.before); err != nil {
		return err
	}
	p.started = true
	return nil
}

func (*upstreamOrderPermit) Finish(context.Context, *zyb.RateLimitError) error { return nil }

func WithUpstreamDispatch(ctx context.Context, key string, priority int, before func(*gorm.DB) error) context.Context {
	return zyb.WithDispatchMetadata(ctx, zyb.DispatchMetadata{Key: key, Priority: priority, BeforeStart: before})
}

// Production clients defer the intent until the permit marks transport start.
// Explicit service test clients without a gate retain their ordinary fixture
// transaction; none of the production factories constructs an ungated client.
func prepareUpstreamMutation(ctx context.Context, client any, key string, priority int, intent func(*gorm.DB) error) (context.Context, error) {
	gated := false
	switch c := client.(type) {
	case *zyb.Client:
		gated = c.Config.Gate != nil
	case zyb.Client:
		gated = c.Config.Gate != nil
	}
	if !gated {
		if err := model.Write(intent); err != nil {
			return ctx, err
		}
		return ctx, nil
	}
	return WithUpstreamDispatch(ctx, key, priority, intent), nil
}

func upstreamDispatchRetry(err error) (time.Time, bool) {
	if at, ok := zyb.RetryAt(err, time.Now()); ok {
		return at, true
	}
	var limited *zyb.RateLimitError
	if errors.As(err, &limited) {
		cfg := config.GlobalConfig.UpstreamScheduling.Effective()
		return time.Now().Add(time.Duration(cfg.CooldownSeconds) * time.Second), true
	}
	return time.Time{}, false
}

func UpstreamDispatchRetryAt(err error) (time.Time, bool) { return upstreamDispatchRetry(err) }
