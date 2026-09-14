package model

import "time"

// UpstreamDispatchGate is the durable state for one external egress budget.
// The default scheduler uses one global key for every ZYB connection. A
// deployment may create additional keys only when it has verified that they
// represent genuinely separate egress paths.
type UpstreamDispatchGate struct {
	Base
	EgressKey     string     `gorm:"size:160;not null;uniqueIndex:idx_upstream_dispatch_gate_egress" json:"-"`
	NextAllowedAt *time.Time `gorm:"index" json:"-"`
	CooldownUntil *time.Time `gorm:"index" json:"-"`
	CooldownLevel int        `gorm:"not null;default:0" json:"-"`
	ProbeInFlight bool       `gorm:"not null;default:false" json:"-"`
}

// UpstreamDispatchWaiter is deliberately metadata-only. It does not contain
// an upstream request body, credentials, or any business payload. A retry
// with the same semantic identity reuses EnqueuedAt while it is still within
// the bounded retention window.
type UpstreamDispatchWaiter struct {
	Base
	EgressKey          string     `gorm:"size:160;not null;index:idx_upstream_dispatch_waiter_queue,priority:1" json:"-"`
	SemanticKey        string     `gorm:"size:200;not null;index:idx_upstream_dispatch_waiter_identity,priority:1" json:"-"`
	Transaction        string     `gorm:"size:80;not null;index:idx_upstream_dispatch_waiter_identity,priority:2" json:"-"`
	BodyHash           string     `gorm:"size:64;not null;index:idx_upstream_dispatch_waiter_identity,priority:3" json:"-"`
	Priority           int        `gorm:"not null;default:0;index:idx_upstream_dispatch_waiter_queue,priority:2" json:"-"`
	EnqueuedAt         time.Time  `gorm:"not null;index:idx_upstream_dispatch_waiter_queue,priority:3" json:"-"`
	Status             string     `gorm:"size:20;not null;default:'queued';index:idx_upstream_dispatch_waiter_queue,priority:4" json:"-"`
	LeaseToken         string     `gorm:"size:80;not null;default:'';index" json:"-"`
	LeaseExpiresAt     *time.Time `gorm:"index" json:"-"`
	TransportStartedAt *time.Time `gorm:"index" json:"-"`
	FinishedAt         *time.Time `json:"-"`
	ExpiredAt          *time.Time `json:"-"`
	LastError          string     `gorm:"type:text;not null;default:''" json:"-"`
	Probe              bool       `gorm:"not null;default:false" json:"-"`
}
