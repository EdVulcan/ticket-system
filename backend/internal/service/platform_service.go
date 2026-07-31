package service

import (
	"sort"
	"strings"
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

type PlatformOrderView struct {
	ID          uint      `json:"id"`
	OrderNo     string    `json:"order_no"`
	TenantID    uint      `json:"tenant_id"`
	TenantName  string    `json:"tenant_name"`
	Status      string    `json:"status"`
	TotalAmount float64   `json:"total_amount"`
	Channel     string    `json:"channel"`
	CreatedAt   time.Time `json:"created_at"`
}

type PlatformIssueView struct {
	Kind        string    `json:"kind"`
	ID          uint      `json:"id"`
	TenantID    uint      `json:"tenant_id"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

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

func (s *PlatformService) ListOrders(tenantID uint, status string, page, pageSize int) ([]PlatformOrderView, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Table("orders").Joins("LEFT JOIN tenants ON tenants.id = orders.tenant_id").
		Where("orders.deleted_at IS NULL")
	if tenantID > 0 {
		query = query.Where("orders.tenant_id = ?", tenantID)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("orders.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PlatformOrderView
	if err := query.Select("orders.id, orders.order_no, orders.tenant_id, tenants.name AS tenant_name, orders.status, orders.total_amount, orders.channel, orders.created_at").
		Order("orders.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *PlatformService) ListIssues(tenantID uint, page, pageSize int) ([]PlatformIssueView, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	issues := make([]PlatformIssueView, 0)
	appendRows := func(kind string, rows []PlatformIssueView) {
		issues = append(issues, rows...)
	}
	var alerts []model.DeviceAlert
	query := model.DB.Where("status = ?", "open")
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Find(&alerts).Error; err != nil {
		return nil, 0, err
	}
	alertRows := make([]PlatformIssueView, 0, len(alerts))
	for _, row := range alerts {
		alertRows = append(alertRows, PlatformIssueView{Kind: "device_alert", ID: row.ID, TenantID: row.TenantID, Status: row.Status, Description: row.Message, CreatedAt: row.CreatedAt})
	}
	appendRows("device_alert", alertRows)

	var refunds []model.DigitalRefundTask
	query = model.DB.Where("status IN ?", []string{"pending", "submitted", "manual_review"})
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Find(&refunds).Error; err != nil {
		return nil, 0, err
	}
	refundRows := make([]PlatformIssueView, 0, len(refunds))
	for _, row := range refunds {
		refundRows = append(refundRows, PlatformIssueView{Kind: "digital_refund", ID: row.ID, TenantID: row.TenantID, Status: row.Status, Description: row.LastError, CreatedAt: row.CreatedAt})
	}
	appendRows("digital_refund", refundRows)

	var payments []model.Payment
	query = model.DB.Where("status = ?", "pending")
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Find(&payments).Error; err != nil {
		return nil, 0, err
	}
	paymentRows := make([]PlatformIssueView, 0, len(payments))
	for _, row := range payments {
		paymentRows = append(paymentRows, PlatformIssueView{Kind: "pending_payment", ID: row.ID, TenantID: row.TenantID, Status: row.Status, Description: row.OrderNo, CreatedAt: row.CreatedAt})
	}
	appendRows("pending_payment", paymentRows)

	var settlements []model.SettlementStatement
	query = model.DB.Where("status IN ?", []string{"draft", "supplier_confirmed", "confirmed", "disputed"})
	if tenantID > 0 {
		query = query.Where("supplier_tenant_id = ? OR distributor_tenant_id = ?", tenantID, tenantID)
	}
	if err := query.Order("created_at DESC").Limit(pageSize).Find(&settlements).Error; err != nil {
		return nil, 0, err
	}
	settlementRows := make([]PlatformIssueView, 0, len(settlements))
	for _, row := range settlements {
		settlementRows = append(settlementRows, PlatformIssueView{Kind: "settlement", ID: row.ID, TenantID: row.SupplierTenantID, Status: row.Status, Description: row.StatementNo, CreatedAt: row.CreatedAt})
	}
	appendRows("settlement", settlementRows)

	sort.Slice(issues, func(i, j int) bool { return issues[i].CreatedAt.After(issues[j].CreatedAt) })
	total := int64(len(issues))
	start := (page - 1) * pageSize
	if start >= len(issues) {
		return []PlatformIssueView{}, total, nil
	}
	end := start + pageSize
	if end > len(issues) {
		end = len(issues)
	}
	return issues[start:end], total, nil
}
