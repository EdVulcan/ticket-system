package service

import (
	"ticket-backend/internal/model"
	"time"
)

type PlatformOverview struct {
	TenantTotal        int64 `json:"tenant_total"`
	TenantActive       int64 `json:"tenant_active"`
	TenantFrozen       int64 `json:"tenant_frozen"`
	OrdersToday        int64 `json:"orders_today"`
	PendingPayments    int64 `json:"pending_payments"`
	PendingRefunds     int64 `json:"pending_refunds"`
	OpenDeviceAlerts   int64 `json:"open_device_alerts"`
	OpenSettlements    int64 `json:"open_settlements"`
	ActiveChannelLinks int64 `json:"active_channel_links"`
}

type PlatformService struct{}

func (s *PlatformService) Overview() (*PlatformOverview, error) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	result := &PlatformOverview{}
	checks := []struct {
		model interface{}
		where string
		args  []interface{}
		out   *int64
	}{
		{&model.Tenant{}, "", nil, &result.TenantTotal},
		{&model.Tenant{}, "status = ?", []interface{}{"active"}, &result.TenantActive},
		{&model.Tenant{}, "status = ?", []interface{}{"frozen"}, &result.TenantFrozen},
		{&model.Order{}, "created_at >= ?", []interface{}{start}, &result.OrdersToday},
		{&model.Payment{}, "status = ?", []interface{}{"pending"}, &result.PendingPayments},
		{&model.Refund{}, "status = ?", []interface{}{"pending"}, &result.PendingRefunds},
		{&model.DeviceAlert{}, "status = ?", []interface{}{"open"}, &result.OpenDeviceAlerts},
		{&model.SettlementStatement{}, "status IN ?", []interface{}{[]string{"draft", "supplier_confirmed", "confirmed", "disputed"}}, &result.OpenSettlements},
		{&model.ChannelAccount{}, "status = ?", []interface{}{"active"}, &result.ActiveChannelLinks},
	}
	for _, check := range checks {
		query := model.DB.Model(check.model)
		if check.where != "" {
			query = query.Where(check.where, check.args...)
		}
		if err := query.Count(check.out).Error; err != nil {
			return nil, err
		}
	}
	return result, nil
}
