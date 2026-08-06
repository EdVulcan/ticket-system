package service

import (
	"fmt"
	"ticket-backend/internal/model"
	"time"
)

type ReportService struct{}

func reportWindow(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", startDate, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := time.ParseInLocation("2006-01-02", endDate, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end.Add(24 * time.Hour).Add(-time.Nanosecond), nil
}

func completedSalesStatuses() []string {
	return []string{"paid", "completed", "partial_refunded", "refunded"}
}

type SalesStat struct {
	Date           string  `json:"date"`
	TotalAmount    float64 `json:"total_amount"`
	RefundedAmount float64 `json:"refunded_amount"`
	NetAmount      float64 `json:"net_amount"`
	OrderCount     int     `json:"order_count"`
}

type ProductStat struct {
	ProductName string  `json:"product_name"`
	TotalSold   int     `json:"total_sold"`
	TotalAmount float64 `json:"total_amount"`
}

type ChannelStat struct {
	Channel        string  `json:"channel"`
	OrderCount     int     `json:"order_count"`
	TotalAmount    float64 `json:"total_amount"`
	RefundedAmount float64 `json:"refunded_amount"`
	NetAmount      float64 `json:"net_amount"`
}

type DailySalesFact struct {
	Date        string `json:"date"`
	OrderCount  int    `json:"order_count"`
	GrossCents  int64  `json:"gross_cents"`
	RefundCents int64  `json:"refund_cents"`
	NetCents    int64  `json:"net_cents"`
}

type DailyPaymentFact struct {
	Date         string `json:"date"`
	PaymentCount int    `json:"payment_count"`
	PaidCents    int64  `json:"paid_cents"`
	RefundCents  int64  `json:"refund_cents"`
}

type DailyVisitFact struct {
	Date        string `json:"date"`
	TicketCount int    `json:"ticket_count"`
	GrossCents  int64  `json:"gross_cents"`
}

type DailyCheckInFact struct {
	Date         string `json:"date"`
	SuccessCount int    `json:"success_count"`
	FailureCount int    `json:"failure_count"`
}

type DailySettlementFact struct {
	Date           string `json:"date"`
	StatementCount int    `json:"statement_count"`
	NetCents       int64  `json:"net_cents"`
	RefundCents    int64  `json:"refund_cents"`
}

type DailyReport struct {
	Sales       []DailySalesFact      `json:"sales"`
	Payments    []DailyPaymentFact    `json:"payments"`
	Visits      []DailyVisitFact      `json:"visits"`
	CheckIns    []DailyCheckInFact    `json:"check_ins"`
	Settlements []DailySettlementFact `json:"settlements"`
}

type OperationsReport struct {
	ChannelMismatchCount    int64 `json:"channel_mismatch_count"`
	ChannelDifferenceCents  int64 `json:"channel_difference_cents"`
	PendingRefundCount      int64 `json:"pending_refund_count"`
	ManualReviewRefundCount int64 `json:"manual_review_refund_count"`
	InventoryCapacity       int64 `json:"inventory_capacity"`
	InventorySold           int64 `json:"inventory_sold"`
	InventoryRemaining      int64 `json:"inventory_remaining"`
	SupplierReceivableCents int64 `json:"supplier_receivable_cents"`
}

func (s *ReportService) GetDailyReport(tenantID uint, startDate, endDate string) (*DailyReport, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("tenant is required")
	}
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}
	report := &DailyReport{
		Sales: make([]DailySalesFact, 0), Payments: make([]DailyPaymentFact, 0),
		Visits: make([]DailyVisitFact, 0), CheckIns: make([]DailyCheckInFact, 0), Settlements: make([]DailySettlementFact, 0),
	}
	if err := model.DB.Table("orders").Select(`DATE(orders.created_at) AS date, COUNT(orders.id) AS order_count,
		COALESCE(SUM(CAST(ROUND(orders.total_amount * 100.0) AS INTEGER)), 0) AS gross_cents,
		COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) AS refund_cents,
		COALESCE(SUM(CAST(ROUND(orders.total_amount * 100.0) AS INTEGER)), 0) - COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) AS net_cents`).
		Where("orders.tenant_id = ? AND orders.environment = ? AND orders.status IN ? AND orders.created_at BETWEEN ? AND ?", tenantID, "production", completedSalesStatuses(), start, end).
		Group("DATE(orders.created_at)").Order("date ASC").Scan(&report.Sales).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("payments").Joins("JOIN orders ON orders.tenant_id = payments.tenant_id AND orders.order_no = payments.order_no").Select(`DATE(COALESCE(payments.paid_at, payments.created_at)) AS date, COUNT(payments.id) AS payment_count,
		COALESCE(SUM(CASE WHEN payments.amount_cents != 0 THEN payments.amount_cents ELSE CAST(ROUND(payments.amount * 100.0) AS INTEGER) END), 0) AS paid_cents,
		COALESCE(SUM(CASE WHEN payments.refunded_amount_cents != 0 THEN payments.refunded_amount_cents ELSE CAST(ROUND(payments.refunded_amount * 100.0) AS INTEGER) END), 0) AS refund_cents`).
		Where("payments.tenant_id = ? AND orders.environment = ? AND payments.status IN ? AND COALESCE(payments.paid_at, payments.created_at) BETWEEN ? AND ?", tenantID, "production", []string{"paid", "partial_refunded", "refunded"}, start, end).
		Group("DATE(COALESCE(payments.paid_at, payments.created_at))").Order("date ASC").Scan(&report.Payments).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("tickets").
		Joins("JOIN order_items ON order_items.id = tickets.order_item_id").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Joins("LEFT JOIN products ON products.id = order_items.product_id").
		Select(`DATE(order_items.use_date) AS date,
			COALESCE(SUM(CASE WHEN COALESCE(NULLIF(tickets.code_mode, ''), products.code_mode) = 'order' THEN order_items.quantity ELSE 1 END), 0) AS ticket_count,
			COALESCE(SUM(CAST(ROUND(order_items.price * 100.0) AS INTEGER) * CASE WHEN COALESCE(NULLIF(tickets.code_mode, ''), products.code_mode) = 'order' THEN order_items.quantity ELSE 1 END), 0) AS gross_cents`).
		Where("orders.tenant_id = ? AND orders.environment = ? AND orders.status IN ? AND tickets.status != ? AND order_items.use_date IS NOT NULL AND order_items.use_date BETWEEN ? AND ?", tenantID, "production", completedSalesStatuses(), "refunded", start, end).
		Group("DATE(order_items.use_date)").Order("date ASC").Scan(&report.Visits).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("check_in_records").Select(`DATE(check_in_time) AS date,
		SUM(CASE WHEN result = 'success' THEN 1 ELSE 0 END) AS success_count,
		SUM(CASE WHEN result != 'success' THEN 1 ELSE 0 END) AS failure_count`).
		Where("tenant_id = ? AND reversed_at IS NULL AND check_in_time BETWEEN ? AND ?", tenantID, start, end).
		Group("DATE(check_in_time)").Order("date ASC").Scan(&report.CheckIns).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("settlement_statements").Select(`DATE(COALESCE(paid_at, confirmed_at, created_at)) AS date, COUNT(id) AS statement_count,
		COALESCE(SUM(net_cents + adjustment_cents), 0) AS net_cents, COALESCE(SUM(refund_cents), 0) AS refund_cents`).
		Where("(supplier_tenant_id = ? OR distributor_tenant_id = ?) AND COALESCE(paid_at, confirmed_at, created_at) BETWEEN ? AND ?", tenantID, tenantID, start, end).
		Group("DATE(COALESCE(paid_at, confirmed_at, created_at))").Order("date ASC").Scan(&report.Settlements).Error; err != nil {
		return nil, err
	}
	return report, nil
}

func (s *ReportService) GetOperationsReport(tenantID uint, startDate, endDate string) (*OperationsReport, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("tenant is required")
	}
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}
	report := &OperationsReport{}
	if err := model.DB.Table("channel_bill_records").Where("tenant_id = ? AND created_at BETWEEN ? AND ? AND status != ?", tenantID, start, end, "matched").Count(&report.ChannelMismatchCount).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("channel_bill_records").Where("tenant_id = ? AND created_at BETWEEN ? AND ?", tenantID, start, end).Select("COALESCE(SUM(ABS(difference_cents)), 0)").Scan(&report.ChannelDifferenceCents).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("digital_refund_tasks").Where("tenant_id = ? AND status IN ?", tenantID, []string{"pending", "processing", "submitted"}).Count(&report.PendingRefundCount).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("digital_refund_tasks").Where("tenant_id = ? AND status = ?", tenantID, "manual_review").Count(&report.ManualReviewRefundCount).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("product_inventories").Where("tenant_id = ? AND stock_date BETWEEN ? AND ?", tenantID, start, end).Select("COALESCE(SUM(capacity), 0)").Scan(&report.InventoryCapacity).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("product_inventories").Where("tenant_id = ? AND stock_date BETWEEN ? AND ?", tenantID, start, end).Select("COALESCE(SUM(sold), 0)").Scan(&report.InventorySold).Error; err != nil {
		return nil, err
	}
	report.InventoryRemaining = report.InventoryCapacity - report.InventorySold
	if report.InventoryRemaining < 0 {
		report.InventoryRemaining = 0
	}
	if err := model.DB.Table("settlement_statements").
		Where("supplier_tenant_id = ? AND status != ? AND created_at BETWEEN ? AND ?", tenantID, "paid", start, end).
		Select("COALESCE(SUM(net_cents + adjustment_cents), 0)").Scan(&report.SupplierReceivableCents).Error; err != nil {
		return nil, err
	}
	return report, nil
}

// GetSalesStats 销售趋势
func (s *ReportService) GetSalesStats(tenantID uint, startDate, endDate string) ([]SalesStat, error) {
	var stats []SalesStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	// PostgreSQL DATE(created_at) keeps the aggregation on business dates.

	err = model.DB.Table("orders").
		Select(`DATE(orders.created_at) as date,
			SUM(CAST(ROUND(orders.total_amount * 100.0) AS INTEGER)) / 100.0 as total_amount,
			COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) / 100.0 as refunded_amount,
			(SUM(CAST(ROUND(orders.total_amount * 100.0) AS INTEGER)) - COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0)) / 100.0 as net_amount,
			COUNT(orders.id) as order_count`).
		Where("orders.tenant_id = ? AND orders.environment = ? AND orders.status IN ? AND orders.created_at BETWEEN ? AND ?", tenantID, "production", completedSalesStatuses(), start, end).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&stats).Error

	return stats, err
}

// GetProductStats 商品排行
func (s *ReportService) GetProductStats(tenantID uint, startDate, endDate string) ([]ProductStat, error) {
	var stats []ProductStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	err = model.DB.Table("order_items").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Select(`order_items.product_name,
			SUM(order_items.quantity - COALESCE((SELECT SUM(CASE WHEN tickets.code_mode = 'order' THEN order_items.quantity ELSE 1 END) FROM tickets WHERE tickets.order_item_id = order_items.id AND tickets.status = 'refunded'), 0)) as total_sold,
			SUM(CAST(ROUND(order_items.price * 100.0) AS INTEGER) * (order_items.quantity - COALESCE((SELECT SUM(CASE WHEN tickets.code_mode = 'order' THEN order_items.quantity ELSE 1 END) FROM tickets WHERE tickets.order_item_id = order_items.id AND tickets.status = 'refunded'), 0))) / 100.0 as total_amount`).
		Where("orders.tenant_id = ? AND orders.environment = ? AND orders.status IN ? AND orders.created_at BETWEEN ? AND ?", tenantID, "production", completedSalesStatuses(), start, end).
		Group("order_items.product_name").
		Order("total_sold DESC").
		Limit(10).
		Scan(&stats).Error

	return stats, err
}

// GetChannelStats 渠道占比
func (s *ReportService) GetChannelStats(tenantID uint, startDate, endDate string) ([]ChannelStat, error) {
	var stats []ChannelStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	err = model.DB.Model(&model.Order{}).
		Select(`channel, COUNT(id) as order_count, SUM(CAST(ROUND(total_amount * 100.0) AS INTEGER)) / 100.0 as total_amount,
			COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) / 100.0 as refunded_amount,
			(SUM(CAST(ROUND(total_amount * 100.0) AS INTEGER)) - COALESCE(SUM((SELECT COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0)) / 100.0 as net_amount`).
		Where("tenant_id = ? AND environment = ? AND status IN ? AND created_at BETWEEN ? AND ?", tenantID, "production", completedSalesStatuses(), start, end).
		Group("channel").
		Scan(&stats).Error

	return stats, err
}
