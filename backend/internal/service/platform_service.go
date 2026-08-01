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

type PlatformFinanceOverview struct {
	LedgerEntryCount     int64 `json:"ledger_entry_count"`
	LedgerNetCents       int64 `json:"ledger_net_cents"`
	CapitalBalanceCents  int64 `json:"capital_balance_cents"`
	CreditUsedCents      int64 `json:"credit_used_cents"`
	FrozenCents          int64 `json:"frozen_cents"`
	PendingDocumentCount int64 `json:"pending_document_count"`
	PendingDocumentCents int64 `json:"pending_document_cents"`
	PendingPaymentCents  int64 `json:"pending_payment_cents"`
	PendingRefundCents   int64 `json:"pending_refund_cents"`
}

type PlatformDeviceView struct {
	ID             uint       `json:"id"`
	TenantID       uint       `json:"tenant_id"`
	TenantName     string     `json:"tenant_name"`
	ScenicAreaID   uint       `json:"scenic_area_id"`
	ScenicAreaName string     `json:"scenic_area_name"`
	CheckPointID   uint       `json:"check_point_id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	LastHeartbeat  *time.Time `json:"last_heartbeat"`
}

type PlatformSettlementView struct {
	ID                  uint      `json:"id"`
	StatementNo         string    `json:"statement_no"`
	SupplierTenantID    uint      `json:"supplier_tenant_id"`
	SupplierName        string    `json:"supplier_name"`
	DistributorTenantID uint      `json:"distributor_tenant_id"`
	DistributorName     string    `json:"distributor_name"`
	GrossCents          int64     `json:"gross_cents"`
	RefundCents         int64     `json:"refund_cents"`
	CommissionCents     int64     `json:"commission_cents"`
	AdjustmentCents     int64     `json:"adjustment_cents"`
	NetCents            int64     `json:"net_cents"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
}

type PlatformAuditView struct {
	model.AuditLog
	TenantName string `json:"tenant_name"`
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

func (s *PlatformService) FinanceOverview(tenantID uint) (*PlatformFinanceOverview, error) {
	result := &PlatformFinanceOverview{}
	ledger := model.DB.Model(&model.LedgerEntry{})
	accounts := model.DB.Model(&model.CapitalAccount{})
	documents := model.DB.Model(&model.FinancialDocument{}).Where("status IN ?", []string{"draft", "submitted", "approved"})
	payments := model.DB.Model(&model.Payment{}).Where("status = ?", "pending")
	refunds := model.DB.Model(&model.Refund{}).Where("status = ?", "pending")
	if tenantID > 0 {
		ledger = ledger.Where("owner_tenant_id = ? OR manager_tenant_id = ?", tenantID, tenantID)
		accounts = accounts.Where("owner_tenant_id = ? OR manager_tenant_id = ?", tenantID, tenantID)
		documents = documents.Where("tenant_id = ? OR counterparty_tenant_id = ?", tenantID, tenantID)
		payments = payments.Where("tenant_id = ?", tenantID)
		refunds = refunds.Where("tenant_id = ?", tenantID)
	}
	if err := ledger.Count(&result.LedgerEntryCount).Error; err != nil {
		return nil, err
	}
	if err := ledger.Select("COALESCE(SUM(amount_cents), 0)").Scan(&result.LedgerNetCents).Error; err != nil {
		return nil, err
	}
	var accountTotals struct {
		BalanceCents    int64
		UsedCreditCents int64
		FrozenCents     int64
	}
	if err := accounts.Select("COALESCE(SUM(balance_cents), 0) AS balance_cents, COALESCE(SUM(used_credit_cents), 0) AS used_credit_cents, COALESCE(SUM(frozen_cents), 0) AS frozen_cents").Scan(&accountTotals).Error; err != nil {
		return nil, err
	}
	result.CapitalBalanceCents, result.CreditUsedCents, result.FrozenCents = accountTotals.BalanceCents, accountTotals.UsedCreditCents, accountTotals.FrozenCents
	if err := documents.Count(&result.PendingDocumentCount).Error; err != nil {
		return nil, err
	}
	if err := documents.Select("COALESCE(SUM(amount_cents), 0)").Scan(&result.PendingDocumentCents).Error; err != nil {
		return nil, err
	}
	if err := payments.Select("COALESCE(SUM(amount_cents), 0)").Scan(&result.PendingPaymentCents).Error; err != nil {
		return nil, err
	}
	if err := refunds.Select("COALESCE(SUM(amount_cents), 0)").Scan(&result.PendingRefundCents).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PlatformService) ListDevices(tenantID uint, status string, page, pageSize int) ([]PlatformDeviceView, int64, error) {
	page, pageSize = normalizePlatformPage(page, pageSize)
	query := model.DB.Table("devices d").
		Joins("JOIN tenants t ON t.id = d.tenant_id").
		Joins("LEFT JOIN scenic_areas sa ON sa.id = d.scenic_area_id").
		Where("d.deleted_at IS NULL")
	if tenantID > 0 {
		query = query.Where("d.tenant_id = ?", tenantID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("d.status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]PlatformDeviceView, 0)
	err := query.Select("d.id, d.tenant_id, t.name AS tenant_name, d.scenic_area_id, sa.name AS scenic_area_name, COALESCE(d.check_point_id, 0) AS check_point_id, d.name, d.type, d.status, d.last_heartbeat").
		Order("d.updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	return rows, total, err
}

func (s *PlatformService) ListSettlements(tenantID uint, status string, page, pageSize int) ([]PlatformSettlementView, int64, error) {
	page, pageSize = normalizePlatformPage(page, pageSize)
	query := model.DB.Table("settlement_statements ss").
		Joins("JOIN tenants st ON st.id = ss.supplier_tenant_id").
		Joins("JOIN tenants dt ON dt.id = ss.distributor_tenant_id").
		Where("ss.deleted_at IS NULL")
	if tenantID > 0 {
		query = query.Where("ss.supplier_tenant_id = ? OR ss.distributor_tenant_id = ?", tenantID, tenantID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("ss.status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]PlatformSettlementView, 0)
	err := query.Select("ss.id, ss.statement_no, ss.supplier_tenant_id, st.name AS supplier_name, ss.distributor_tenant_id, dt.name AS distributor_name, ss.gross_cents, ss.refund_cents, ss.commission_cents, ss.adjustment_cents, ss.net_cents + ss.adjustment_cents AS net_cents, ss.status, ss.created_at").
		Order("ss.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	return rows, total, err
}

func (s *PlatformService) ListAuditLogs(tenantID uint, action string, page, pageSize int) ([]PlatformAuditView, int64, error) {
	page, pageSize = normalizePlatformPage(page, pageSize)
	query := model.DB.Table("audit_logs a").Joins("LEFT JOIN tenants t ON t.id = a.tenant_id").Where("a.deleted_at IS NULL")
	if tenantID > 0 {
		query = query.Where("a.tenant_id = ?", tenantID)
	}
	if strings.TrimSpace(action) != "" {
		query = query.Where("a.action = ?", strings.TrimSpace(action))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]PlatformAuditView, 0)
	err := query.Select("a.*, t.name AS tenant_name").Order("a.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error
	return rows, total, err
}

func normalizePlatformPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
