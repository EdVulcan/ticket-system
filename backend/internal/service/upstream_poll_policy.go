package service

import (
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"
)

func upstreamPollDelay(snapshot model.OrderItemSupplySnapshot, useDate *time.Time, now time.Time) time.Duration {
	cfg := config.GlobalConfig.UpstreamScheduling.Effective()
	if snapshot.SyncFailureCount > 0 {
		seconds := cfg.CooldownSeconds * (1 << minInt(snapshot.SyncFailureCount, 5))
		if seconds > cfg.MaxCooldownSeconds {
			seconds = cfg.MaxCooldownSeconds
		}
		return time.Duration(seconds)*time.Second + time.Duration(snapshot.ID%11)*time.Second
	}
	if snapshot.IssueStatus == "pending" {
		return time.Duration(cfg.RequestIntervalMS) * time.Millisecond
	}
	seconds := cfg.TodayPollSeconds
	if snapshot.ProviderStatus == "checked" || snapshot.ProviderStatus == "checking" || snapshot.ProviderStatus == "partial_refunded" {
		seconds = cfg.UsedPollSeconds
	}
	local := now.In(time.FixedZone("CST", 8*3600))
	if useDate != nil && useDate.Format("2006-01-02") > local.Format("2006-01-02") {
		seconds = cfg.FuturePollSeconds
	} else if (local.Hour() < 7 || local.Hour() >= 22) && seconds < cfg.NightPollSeconds {
		seconds = cfg.NightPollSeconds
	}
	// Stable spread avoids synchronized polling without changing ticket facts.
	jitter := time.Duration(snapshot.ID%11) * time.Second
	return time.Duration(seconds)*time.Second + jitter
}
