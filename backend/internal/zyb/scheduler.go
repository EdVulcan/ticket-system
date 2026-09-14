package zyb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultRequestInterval = 500 * time.Millisecond
	defaultCooldownBase    = 60 * time.Second
	defaultCooldownMax     = 15 * time.Minute
	defaultGateLease       = 60 * time.Second
	defaultConcurrency     = 1
	defaultWaiterRetention = 24 * time.Hour

	dispatchEgressKey = "zyb:global"
)

var (
	ErrRequestGateUnavailable = errors.New("upstream request gate is unavailable")
	ErrRequestPermitExpired   = errors.New("upstream request permit expired")
	ErrRequestPermitStarted   = errors.New("upstream request already started")
	ErrRequestPermitFinished  = errors.New("upstream request permit already finished")
	ErrRequestPermitMismatch  = errors.New("upstream request permit lease mismatch")
)

// RequestGate reserves one durable egress slot before an upstream HTTP call.
// Acquire never performs network I/O and never writes business intent.
type RequestGate interface {
	Acquire(context.Context, string, string) (RequestPermit, error)
}

// RequestPermit is deliberately small so protocol clients and deterministic
// test fakes can share the same transport boundary.
type RequestPermit interface {
	Start(context.Context) error
	Finish(context.Context, *RateLimitError) error
}

// DeferredError means the request was certified unsent. Callers may retry the
// same semantic request at RetryAt without treating it as a provider attempt.
type DeferredError struct {
	RetryAt time.Time
	Cause   error
	Reason  string
}

func (e *DeferredError) Error() string {
	if e == nil {
		return "upstream request deferred"
	}
	message := strings.TrimSpace(e.Reason)
	if message == "" && e.Cause != nil {
		message = e.Cause.Error()
	}
	if message == "" {
		message = "upstream request deferred"
	}
	if e.RetryAt.IsZero() {
		return message
	}
	return fmt.Sprintf("%s (retry at %s)", message, e.RetryAt.UTC().Format(time.RFC3339Nano))
}

func (e *DeferredError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RateLimitError is returned only for a structurally identified HTTP 429.
// RetryAfter is parsed from Retry-After when present; zero requests the
// scheduler's exponential shared cooldown.
type RateLimitError struct {
	RetryAfter     time.Duration
	StatusCode     int
	ResponseStatus string
	HeaderValue    string
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return "upstream request rate limited"
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("upstream request rate limited (retry after %s)", e.RetryAfter)
	}
	return "upstream request rate limited"
}

// RetryAt extracts a certified retry time from either a deferred unsent
// request or a response that was sent and received HTTP 429.
func RetryAt(err error, now time.Time) (time.Time, bool) {
	if err == nil {
		return time.Time{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	var deferred *DeferredError
	if errors.As(err, &deferred) && deferred != nil && !deferred.RetryAt.IsZero() {
		return deferred.RetryAt, true
	}
	var limited *RateLimitError
	if errors.As(err, &limited) && limited != nil && limited.RetryAfter > 0 {
		return now.Add(limited.RetryAfter), true
	}
	return time.Time{}, false
}

// DispatchMetadata travels with a protocol call. The scheduler persists only
// Key, Priority, transaction, and a body hash; BeforeStart remains in memory
// and executes in the same PostgreSQL transaction as transport_started_at.
type DispatchMetadata struct {
	Key         string
	Priority    int
	BeforeStart func(*gorm.DB) error
}

type dispatchMetadataContextKey struct{}

func WithDispatchMetadata(ctx context.Context, metadata DispatchMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, dispatchMetadataContextKey{}, metadata)
}

func DispatchMetadataFromContext(ctx context.Context) DispatchMetadata {
	if ctx == nil {
		return DispatchMetadata{}
	}
	metadata, _ := ctx.Value(dispatchMetadataContextKey{}).(DispatchMetadata)
	return metadata
}

// SchedulerConfig keeps deployment pacing in one explicit duration-based shape
// so the scheduler does not depend on the application config package.
type SchedulerConfig struct {
	EgressKey          string
	ConnectionIdentity string

	RequestInterval time.Duration
	Cooldown        time.Duration
	MaxCooldown     time.Duration
	Lease           time.Duration
	Concurrency     int
	WaiterRetention time.Duration
	AcquireWait     time.Duration
	Jitter          func(time.Duration) time.Duration
}

func (c SchedulerConfig) effective() SchedulerConfig {
	if c.EgressKey == "" {
		c.EgressKey = dispatchEgressKey
	}
	c.ConnectionIdentity = strings.TrimSpace(c.ConnectionIdentity)
	if c.RequestInterval == 0 {
		c.RequestInterval = defaultRequestInterval
	}
	if c.Cooldown == 0 {
		c.Cooldown = defaultCooldownBase
	}
	if c.MaxCooldown == 0 {
		c.MaxCooldown = defaultCooldownMax
	}
	if c.MaxCooldown < c.Cooldown {
		c.MaxCooldown = c.Cooldown
	}
	if c.Lease <= 0 {
		c.Lease = defaultGateLease
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultConcurrency
	}
	if c.WaiterRetention <= 0 {
		c.WaiterRetention = defaultWaiterRetention
	}
	if c.AcquireWait <= 0 {
		c.AcquireWait = 15 * time.Second
	}
	return c
}

// Scheduler is safe for use by multiple processes. All arbitration state is
// locked in PostgreSQL; the Go value contains no shared queue state.
type Scheduler struct {
	db  *gorm.DB
	cfg SchedulerConfig
}

func NewScheduler(db *gorm.DB, config SchedulerConfig) *Scheduler {
	return &Scheduler{db: db, cfg: config.effective()}
}

type postgresRequestPermit struct {
	scheduler *Scheduler
	waiterID  uint
	gateKey   string
	token     string
	retryAt   time.Time
	metadata  DispatchMetadata

	mu       sync.Mutex
	started  bool
	finished bool
	stop     context.CancelFunc
}

func (s *Scheduler) Acquire(ctx context.Context, transaction, body string) (RequestPermit, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil || s.db.Dialector == nil || s.db.Dialector.Name() != "postgres" {
		return nil, &DeferredError{RetryAt: time.Now().Add(defaultGateLease), Cause: ErrRequestGateUnavailable}
	}
	if strings.TrimSpace(transaction) == "" {
		return nil, &DeferredError{RetryAt: time.Now().Add(s.cfg.Lease), Cause: errors.New("upstream transaction is required")}
	}
	metadata := DispatchMetadataFromContext(ctx)
	egressKey := s.cfg.EgressKey
	semanticKey := semanticDispatchKey(metadata.Key, s.cfg.ConnectionIdentity, transaction, body)
	bodyHash := hashDispatchBody(body)
	priority := metadata.Priority
	if priority < 0 {
		priority = 0
	}
	if priority > 1000 {
		priority = 1000
	}
	deadline := time.Now().Add(s.cfg.AcquireWait)
	for {
		permit, deferred, wait, err := s.acquireOnce(ctx, transaction, semanticKey, bodyHash, egressKey, priority, metadata)
		if err != nil {
			return nil, s.deferredDatabaseError(err)
		}
		if permit != nil {
			return permit, nil
		}
		if deferred == nil || !wait {
			if deferred == nil {
				deferred = &DeferredError{RetryAt: time.Now().Add(s.cfg.Lease), Cause: ErrRequestGateUnavailable}
			}
			return nil, deferred
		}
		// Ordinary pacing/concurrency waits happen outside the transaction so
		// a sequence of query calls can progress without holding a DB session.
		// A shared cooldown is deliberately never slept through here.
		if deferred.RetryAt.IsZero() || deferred.RetryAt.After(deadline) {
			return nil, deferred
		}
		waitFor := time.Until(deferred.RetryAt)
		if waitFor <= 0 {
			continue
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, &DeferredError{RetryAt: time.Now().Add(s.cfg.Lease), Cause: ctx.Err()}
		case <-timer.C:
		}
	}
}

func (s *Scheduler) acquireOnce(ctx context.Context, transaction, semanticKey, bodyHash, egressKey string, priority int, metadata DispatchMetadata) (*postgresRequestPermit, *DeferredError, bool, error) {
	var permit *postgresRequestPermit
	var deferred *DeferredError
	wait := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO upstream_dispatch_gates (egress_key, created_at, updated_at)
			VALUES (?, clock_timestamp(), clock_timestamp())
			ON CONFLICT (egress_key) DO NOTHING
		`, egressKey).Error; err != nil {
			return err
		}
		var gate model.UpstreamDispatchGate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("egress_key = ?", egressKey).First(&gate).Error; err != nil {
			return err
		}
		var dbNow time.Time
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&dbNow).Error; err != nil {
			return err
		}
		if err := s.expireWaitersTx(tx, egressKey); err != nil {
			return err
		}
		if gate.ProbeInFlight {
			var probeCount int64
			if err := tx.Model(&model.UpstreamDispatchWaiter{}).
				Where("egress_key = ? AND probe = ? AND status IN ? AND lease_expires_at > clock_timestamp()", egressKey, true, []string{"leased", "started"}).Count(&probeCount).Error; err != nil {
				return err
			}
			if probeCount == 0 {
				if err := tx.Model(&gate).Update("probe_in_flight", false).Error; err != nil {
					return err
				}
				gate.ProbeInFlight = false
			}
		}

		var existing model.UpstreamDispatchWaiter
		existingErr := tx.Where("egress_key = ? AND semantic_key = ? AND \"transaction\" = ? AND body_hash = ? AND status IN ?", egressKey, semanticKey, transaction, bodyHash, []string{"started", "orphaned"}).Order("id DESC").First(&existing).Error
		if existingErr == nil {
			deferred = &DeferredError{RetryAt: dbNow.Add(s.cfg.Lease), Cause: ErrRequestPermitStarted, Reason: "upstream request already has a transport-started marker"}
			return nil
		}
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}

		// Reuse an unstarted row inside the retention window. This preserves
		// queue age while allowing a lost pre-start lease to be retried.
		var waiter model.UpstreamDispatchWaiter
		existingErr = tx.Where("egress_key = ? AND semantic_key = ? AND \"transaction\" = ? AND body_hash = ? AND status IN ?", egressKey, semanticKey, transaction, bodyHash, []string{"queued", "leased"}).Order("id DESC").First(&waiter).Error
		if errors.Is(existingErr, gorm.ErrRecordNotFound) {
			existingErr = tx.Where("egress_key = ? AND semantic_key = ? AND \"transaction\" = ? AND body_hash = ? AND status = 'expired' AND expired_at > clock_timestamp() - (? * INTERVAL '1 second') AND enqueued_at > clock_timestamp() - (? * INTERVAL '1 second')", egressKey, semanticKey, transaction, bodyHash, int64(s.cfg.WaiterRetention/time.Second), int64(s.cfg.WaiterRetention/time.Second)).Order("id DESC").First(&waiter).Error
			if existingErr == nil {
				if err := tx.Model(&waiter).Updates(map[string]interface{}{"status": "queued", "expired_at": nil, "lease_token": "", "lease_expires_at": nil, "last_error": "", "updated_at": gorm.Expr("clock_timestamp()")}).Error; err != nil {
					return err
				}
				waiter.Status, waiter.ExpiredAt, waiter.LeaseToken, waiter.LeaseExpiresAt, waiter.LastError = "queued", nil, "", nil, ""
			}
		}
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		if waiter.ID == 0 {
			if err := tx.Exec(`
				INSERT INTO upstream_dispatch_waiters
				(egress_key, semantic_key, "transaction", body_hash, priority, enqueued_at, status, lease_token, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, clock_timestamp(), 'queued', '', clock_timestamp(), clock_timestamp())
			`, egressKey, semanticKey, transaction, bodyHash, priority).Error; err != nil {
				return err
			}
			if err := tx.Where("egress_key = ? AND semantic_key = ? AND \"transaction\" = ? AND body_hash = ? AND status = 'queued'", egressKey, semanticKey, transaction, bodyHash).Order("id DESC").First(&waiter).Error; err != nil {
				return err
			}
		}

		var candidate model.UpstreamDispatchWaiter
		// Only live callers compete for a slot. A durable task that retries later
		// refreshes this timestamp while retaining its original enqueue age.
		if err := tx.Model(&waiter).Updates(map[string]interface{}{"updated_at": gorm.Expr("clock_timestamp()"), "priority": gorm.Expr("GREATEST(priority, ?)", priority)}).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("egress_key = ? AND status = 'queued' AND updated_at > clock_timestamp() - INTERVAL '10 seconds' AND (lease_expires_at IS NULL OR lease_expires_at <= clock_timestamp())", egressKey).
			Order("(priority + FLOOR(EXTRACT(EPOCH FROM (clock_timestamp() - enqueued_at)) / 30)) DESC, enqueued_at ASC, id ASC").First(&candidate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				deferred = &DeferredError{RetryAt: dbNow.Add(s.cfg.Lease), Cause: ErrRequestGateUnavailable, Reason: "upstream request queue is unavailable"}
				return nil
			}
			return err
		}

		var active int64
		if err := tx.Model(&model.UpstreamDispatchWaiter{}).Where("egress_key = ? AND status IN ? AND lease_expires_at > clock_timestamp()", egressKey, []string{"leased", "started"}).Count(&active).Error; err != nil {
			return err
		}
		retryAt := dbNow.Add(time.Second)
		if gate.NextAllowedAt != nil && gate.NextAllowedAt.After(retryAt) {
			retryAt = *gate.NextAllowedAt
		}
		if gate.CooldownUntil != nil && gate.CooldownUntil.After(retryAt) {
			retryAt = *gate.CooldownUntil
		}
		var earliestLease time.Time
		var leaseRow struct{ LeaseExpiresAt *time.Time }
		if err := tx.Model(&model.UpstreamDispatchWaiter{}).Select("lease_expires_at").Where("egress_key = ? AND status IN ? AND lease_expires_at > clock_timestamp()", egressKey, []string{"leased", "started"}).Order("lease_expires_at ASC").First(&leaseRow).Error; err == nil && leaseRow.LeaseExpiresAt != nil {
			earliestLease = *leaseRow.LeaseExpiresAt
			if earliestLease.Before(retryAt) {
				retryAt = earliestLease
			}
		}
		if gate.CooldownUntil != nil && !gate.CooldownUntil.After(dbNow) && gate.ProbeInFlight {
			deferred = &DeferredError{RetryAt: retryAt, Cause: ErrRequestGateUnavailable, Reason: "upstream cooldown probe is in flight"}
			wait = true
			return nil
		}
		if gate.CooldownUntil != nil && gate.CooldownUntil.After(dbNow) {
			deferred = &DeferredError{RetryAt: retryAt, Cause: ErrRequestGateUnavailable, Reason: "upstream provider cooldown is active"}
			return nil
		}
		if gate.NextAllowedAt != nil && gate.NextAllowedAt.After(dbNow) {
			deferred = &DeferredError{RetryAt: retryAt, Cause: ErrRequestGateUnavailable, Reason: "upstream request pacing interval is active"}
			wait = true
			return nil
		}
		if candidate.ID != waiter.ID {
			deferred = &DeferredError{RetryAt: retryAt, Cause: ErrRequestGateUnavailable, Reason: "another upstream request has queue priority"}
			wait = true
			return nil
		}
		limit := s.cfg.Concurrency
		probe := false
		if gate.CooldownUntil != nil {
			// The first request after a shared cooldown is a single probe even
			// when normal deployment concurrency is configured above one.
			probe = !gate.ProbeInFlight
			limit = 1
		}
		if active >= int64(limit) {
			deferred = &DeferredError{RetryAt: retryAt, Cause: ErrRequestGateUnavailable, Reason: "upstream request concurrency is full"}
			wait = true
			return nil
		}
		token, err := randomDispatchToken()
		if err != nil {
			return err
		}
		leaseMS := ceilMilliseconds(s.cfg.Lease)
		if err := tx.Model(&candidate).Updates(map[string]interface{}{
			"status":               "leased",
			"lease_token":          token,
			"lease_expires_at":     gorm.Expr("clock_timestamp() + (? * INTERVAL '1 millisecond')", leaseMS),
			"transport_started_at": nil,
			"probe":                probe,
			"updated_at":           gorm.Expr("clock_timestamp()"),
		}).Error; err != nil {
			return err
		}
		gateUpdates := map[string]interface{}{"updated_at": gorm.Expr("clock_timestamp()")}
		if probe {
			gateUpdates["probe_in_flight"] = true
		}
		if err := tx.Model(&gate).Updates(gateUpdates).Error; err != nil {
			return err
		}
		permit = &postgresRequestPermit{scheduler: s, waiterID: waiter.ID, gateKey: egressKey, token: token, retryAt: retryAt, metadata: metadata}
		return nil
	})
	return permit, deferred, wait, err
}

func (s *Scheduler) expireWaitersTx(tx *gorm.DB, egressKey string) error {
	// These rows are coordination metadata, not the durable order/refund
	// evidence. Retain a bounded history without growing one row per request
	// forever. Active transport and queued work are never deleted here.
	if err := tx.Exec(`DELETE FROM upstream_dispatch_waiters WHERE id IN (
		SELECT id FROM upstream_dispatch_waiters WHERE egress_key = ?
		AND status IN ('finished','expired','orphaned','cancelled')
		AND updated_at < clock_timestamp() - (? * INTERVAL '1 second')
		ORDER BY updated_at LIMIT 100)`, egressKey, int64(s.cfg.WaiterRetention/time.Second)).Error; err != nil {
		return err
	}
	if err := tx.Exec(`
		UPDATE upstream_dispatch_waiters
		SET status = 'expired', expired_at = clock_timestamp(), lease_token = '', lease_expires_at = NULL,
			probe = FALSE, updated_at = clock_timestamp()
		WHERE egress_key = ? AND status = 'leased' AND transport_started_at IS NULL
		  AND lease_expires_at IS NOT NULL AND lease_expires_at <= clock_timestamp()
	`, egressKey).Error; err != nil {
		return err
	}
	if err := tx.Exec(`
		UPDATE upstream_dispatch_waiters
		SET status = 'orphaned', expired_at = clock_timestamp(), lease_token = '', lease_expires_at = NULL,
			updated_at = clock_timestamp()
		WHERE egress_key = ? AND status = 'started' AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= clock_timestamp()
	`, egressKey).Error; err != nil {
		return err
	}
	return tx.Exec(`
		UPDATE upstream_dispatch_waiters
		SET status = 'expired', expired_at = clock_timestamp(), updated_at = clock_timestamp()
		WHERE egress_key = ? AND status = 'queued'
		  AND enqueued_at < clock_timestamp() - (? * INTERVAL '1 second')
	`, egressKey, int64(s.cfg.WaiterRetention/time.Second)).Error
}

func (s *Scheduler) deferredDatabaseError(err error) error {
	if err == nil {
		err = ErrRequestGateUnavailable
	}
	return &DeferredError{RetryAt: time.Now().Add(s.cfg.Lease), Cause: fmt.Errorf("%w: %v", ErrRequestGateUnavailable, err)}
}

func (p *postgresRequestPermit) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return &DeferredError{RetryAt: p.retryAt, Cause: ErrRequestPermitFinished}
	}
	if p.started {
		p.mu.Unlock()
		return ErrRequestPermitStarted
	}
	p.mu.Unlock()

	var callbackErr error
	err := p.scheduler.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var gate model.UpstreamDispatchGate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("egress_key = ?", p.gateKey).First(&gate).Error; err != nil {
			return err
		}
		var waiter model.UpstreamDispatchWaiter
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND egress_key = ? AND lease_token = ?", p.waiterID, p.gateKey, p.token).First(&waiter).Error; err != nil {
			return err
		}
		var now time.Time
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
			return err
		}
		if waiter.Status != "leased" || waiter.TransportStartedAt != nil || waiter.LeaseExpiresAt == nil || !waiter.LeaseExpiresAt.After(now) {
			return &DeferredError{RetryAt: now.Add(p.scheduler.cfg.Lease), Cause: ErrRequestPermitExpired}
		}
		if gate.CooldownUntil != nil && gate.CooldownUntil.After(now) {
			return &DeferredError{RetryAt: *gate.CooldownUntil, Cause: ErrRequestGateUnavailable, Reason: "upstream provider cooldown is active"}
		}
		if gate.NextAllowedAt != nil && gate.NextAllowedAt.After(now) {
			return &DeferredError{RetryAt: *gate.NextAllowedAt, Cause: ErrRequestGateUnavailable, Reason: "upstream request pacing interval is active"}
		}
		if p.metadata.BeforeStart != nil {
			if err := p.metadata.BeforeStart(tx); err != nil {
				callbackErr = err
				return err
			}
		}
		if err := tx.Model(&waiter).Where("id = ? AND egress_key = ? AND lease_token = ? AND status = 'leased' AND transport_started_at IS NULL", p.waiterID, p.gateKey, p.token).Updates(map[string]interface{}{
			"status":               "started",
			"transport_started_at": gorm.Expr("clock_timestamp()"),
			"lease_expires_at":     gorm.Expr("clock_timestamp() + (? * INTERVAL '1 millisecond')", ceilMilliseconds(p.scheduler.cfg.Lease)),
			"updated_at":           gorm.Expr("clock_timestamp()"),
		}).Error; err != nil {
			return err
		}
		return tx.Model(&gate).Updates(map[string]interface{}{
			"next_allowed_at": gorm.Expr("clock_timestamp() + (? * INTERVAL '1 millisecond')", ceilMilliseconds(p.scheduler.cfg.RequestInterval)),
			"updated_at":      gorm.Expr("clock_timestamp()"),
		}).Error
	})
	if err != nil {
		var deferred *DeferredError
		if errors.As(err, &deferred) {
			_ = p.releaseUnstarted(context.WithoutCancel(ctx))
			return deferred
		}
		if callbackErr != nil {
			_ = p.releaseUnstarted(context.WithoutCancel(ctx))
			return callbackErr
		}
		databaseDeferred := p.scheduler.deferredDatabaseError(err)
		_ = p.releaseUnstarted(context.WithoutCancel(ctx))
		return databaseDeferred
	}
	p.mu.Lock()
	p.started = true
	heartbeatCtx, cancel := context.WithCancel(context.Background())
	p.stop = cancel
	p.mu.Unlock()
	go p.heartbeatLoop(heartbeatCtx)
	return nil
}

func (p *postgresRequestPermit) releaseUnstarted(ctx context.Context) error {
	return p.scheduler.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var gate model.UpstreamDispatchGate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("egress_key = ?", p.gateKey).First(&gate).Error; err != nil {
			return err
		}
		var waiter model.UpstreamDispatchWaiter
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND egress_key = ? AND lease_token = ?", p.waiterID, p.gateKey, p.token).First(&waiter).Error; err != nil {
			return err
		}
		if waiter.Status != "leased" || waiter.TransportStartedAt != nil {
			return nil
		}
		if waiter.Probe && gate.ProbeInFlight {
			if err := tx.Model(&gate).Updates(map[string]interface{}{"probe_in_flight": false, "updated_at": gorm.Expr("clock_timestamp()")}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&waiter).Updates(map[string]interface{}{"status": "queued", "lease_token": "", "lease_expires_at": nil, "probe": false, "updated_at": gorm.Expr("clock_timestamp()")}).Error
	})
}

func (p *postgresRequestPermit) heartbeatLoop(ctx context.Context) {
	interval := p.scheduler.cfg.Lease / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.Heartbeat(context.Background())
		}
	}
}

// Heartbeat extends an active transport lease. It is exported for callers
// whose HTTP transport can outlive the scheduler's default lease interval.
func (p *postgresRequestPermit) Heartbeat(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	result := p.scheduler.db.WithContext(ctx).Model(&model.UpstreamDispatchWaiter{}).
		Where("id = ? AND egress_key = ? AND lease_token = ? AND status = 'started' AND transport_started_at IS NOT NULL AND lease_expires_at > clock_timestamp()", p.waiterID, p.gateKey, p.token).
		Updates(map[string]interface{}{"lease_expires_at": gorm.Expr("clock_timestamp() + (? * INTERVAL '1 millisecond')", ceilMilliseconds(p.scheduler.cfg.Lease)), "updated_at": gorm.Expr("clock_timestamp()")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRequestPermitExpired
	}
	return nil
}

func (p *postgresRequestPermit) Finish(ctx context.Context, limited *RateLimitError) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.finished {
		p.mu.Unlock()
		return ErrRequestPermitFinished
	}
	if !p.started {
		p.mu.Unlock()
		return &DeferredError{RetryAt: p.retryAt, Cause: ErrRequestPermitExpired}
	}
	stop := p.stop
	p.mu.Unlock()
	if stop != nil {
		stop()
	}
	err := p.scheduler.db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
		var gate model.UpstreamDispatchGate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("egress_key = ?", p.gateKey).First(&gate).Error; err != nil {
			return err
		}
		var waiter model.UpstreamDispatchWaiter
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND egress_key = ? AND lease_token = ?", p.waiterID, p.gateKey, p.token).First(&waiter).Error; err != nil {
			return err
		}
		if waiter.Status != "started" || waiter.TransportStartedAt == nil {
			return ErrRequestPermitMismatch
		}
		gateUpdates := map[string]interface{}{"updated_at": gorm.Expr("clock_timestamp()")}
		if limited != nil {
			level := gate.CooldownLevel
			if level < 0 {
				level = 0
			}
			backoff := sCooldown(p.scheduler.cfg.Cooldown, p.scheduler.cfg.MaxCooldown, level)
			if limited.RetryAfter > backoff {
				backoff = limited.RetryAfter
			}
			backoff += p.scheduler.jitter(backoff)
			limited.RetryAfter = backoff
			candidate := time.Now().Add(backoff)
			// The DB clock is authoritative for persisted pacing; this value is
			// used only as an interval endpoint and is replaced by clock_timestamp
			// below to avoid importing a wall-clock timestamp into the database.
			seconds := ceilMilliseconds(backoff)
			gateUpdates["cooldown_until"] = gorm.Expr("clock_timestamp() + (? * INTERVAL '1 millisecond')", seconds)
			gateUpdates["cooldown_level"] = level + 1
			gateUpdates["probe_in_flight"] = false
			_ = candidate
		} else if waiter.Probe {
			gateUpdates["cooldown_until"] = nil
			gateUpdates["cooldown_level"] = 0
			gateUpdates["probe_in_flight"] = false
		}
		if err := tx.Model(&gate).Updates(gateUpdates).Error; err != nil {
			return err
		}
		outcome := ""
		if limited != nil {
			outcome = "rate_limited"
		}
		return tx.Model(&waiter).Updates(map[string]interface{}{"status": "finished", "finished_at": gorm.Expr("clock_timestamp()"), "lease_token": "", "lease_expires_at": nil, "probe": false, "last_error": outcome, "updated_at": gorm.Expr("clock_timestamp()")}).Error
	})
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.finished = true
	p.mu.Unlock()
	return nil
}

func (s *Scheduler) jitter(d time.Duration) time.Duration {
	if s.cfg.Jitter != nil {
		jitter := s.cfg.Jitter(d)
		if jitter < 0 {
			return 0
		}
		return jitter
	}
	max := d / 10
	if max < time.Millisecond {
		max = time.Millisecond
	}
	if max > 5*time.Second {
		max = 5 * time.Second
	}
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return time.Millisecond
	}
	return time.Millisecond + time.Duration(binary.LittleEndian.Uint64(bytes[:])%uint64(max))
}

func sCooldown(base, maximum time.Duration, level int) time.Duration {
	if base <= 0 {
		base = defaultCooldownBase
	}
	if maximum < base {
		maximum = base
	}
	if level <= 0 {
		return base
	}
	for i := 0; i < level && base < maximum; i++ {
		if base > maximum/2 {
			base = maximum
			break
		}
		base *= 2
	}
	if base > maximum {
		return maximum
	}
	return base
}

func semanticDispatchKey(explicit, identity, transaction, body string) string {
	transaction = strings.TrimSpace(transaction)
	if key := strings.TrimSpace(explicit); key != "" {
		// A caller-supplied semantic key is still scoped to the connection
		// identity. The identity is hashed so credentials and endpoint values
		// never land in the queue row.
		return "semantic:" + hashDispatch(identity+"\x00"+key+"\x00"+transaction)
	}
	return "auto:" + hashDispatch(identity+"\x00"+transaction+"\x00"+body)
}

func hashDispatchBody(body string) string {
	return hashDispatch(body)
}

func hashDispatch(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomDispatchToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func ceilMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Millisecond - 1) / time.Millisecond)
}
