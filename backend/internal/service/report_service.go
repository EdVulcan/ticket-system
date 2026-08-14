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

// paidOrdersCTE keeps all sales-facing reports on the same attribution rule:
// a sale belongs to the latest successful order payment, with the order
// creation time as a legacy fallback when the payment fact is absent.
const paidOrdersCTE = `
WITH paid_orders AS (
	SELECT orders.id AS order_id, orders.tenant_id, orders.order_no, orders.channel, orders.total_amount,
	       COALESCE(MAX(COALESCE(payments.paid_at, payments.created_at)), orders.created_at) AS sold_at
	FROM orders
	LEFT JOIN payments ON payments.tenant_id = orders.tenant_id
	 AND payments.order_no = orders.order_no
	 AND payments.status IN ('paid','partial_refunded','refunded')
	 AND payments.purpose IN ('','order')
	WHERE orders.deleted_at IS NULL AND orders.environment = 'production'
	  AND orders.status IN ('paid','completed','partial_refunded','refunded')
	GROUP BY orders.id, orders.tenant_id, orders.order_no, orders.channel, orders.total_amount, orders.created_at
)`

const salesFactsCTE = paidOrdersCTE + `,
sales_facts AS (
	SELECT paid_orders.*,
	       CAST(ROUND(paid_orders.total_amount * 100.0) AS BIGINT) AS gross_cents,
	       COALESCE((
		SELECT SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS BIGINT) END)
		FROM refunds
		WHERE refunds.tenant_id = paid_orders.tenant_id AND refunds.order_no = paid_orders.order_no
		  AND refunds.status IN ('succeeded','group_succeeded')
		  AND (refunds.parent_refund_id != 0 OR NOT EXISTS (
			SELECT 1 FROM refunds child WHERE child.parent_refund_id = refunds.id
		  ))
	       ), 0) AS refund_cents
	FROM paid_orders
)`

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
	if err := model.DB.Raw(salesFactsCTE+` SELECT DATE(sold_at) AS date, COUNT(order_id) AS order_count,
		COALESCE(SUM(gross_cents), 0) AS gross_cents,
		COALESCE(SUM(refund_cents), 0) AS refund_cents,
		COALESCE(SUM(gross_cents - refund_cents), 0) AS net_cents
		FROM sales_facts
		WHERE tenant_id = ? AND sold_at BETWEEN ? AND ?
		GROUP BY DATE(sold_at) ORDER BY date ASC`, tenantID, start, end).Scan(&report.Sales).Error; err != nil {
		return nil, err
	}
	if err := model.DB.Table("payments").Joins("JOIN orders ON orders.tenant_id = payments.tenant_id AND orders.order_no = payments.order_no").Select(`DATE(COALESCE(payments.paid_at, payments.created_at)) AS date, COUNT(payments.id) AS payment_count,
		COALESCE(SUM(CASE WHEN payments.amount_cents != 0 THEN payments.amount_cents ELSE CAST(ROUND(payments.amount * 100.0) AS INTEGER) END), 0) AS paid_cents,
		COALESCE(SUM(CASE WHEN payments.refunded_amount_cents != 0 THEN payments.refunded_amount_cents ELSE CAST(ROUND(payments.refunded_amount * 100.0) AS INTEGER) END), 0) AS refund_cents`).
		Where("payments.tenant_id = ? AND orders.environment = ? AND payments.status IN ? AND payments.purpose IN ? AND COALESCE(payments.paid_at, payments.created_at) BETWEEN ? AND ?", tenantID, "production", []string{"paid", "partial_refunded", "refunded"}, []string{"", "order"}, start, end).
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

	// Sales-facing reports aggregate on the original successful payment date.

	err = model.DB.Raw(salesFactsCTE+` SELECT DATE(sold_at) AS date,
			SUM(gross_cents) / 100.0 AS total_amount,
			SUM(refund_cents) / 100.0 AS refunded_amount,
			SUM(gross_cents - refund_cents) / 100.0 AS net_amount,
			COUNT(order_id) AS order_count
		FROM sales_facts
		WHERE tenant_id = ? AND sold_at BETWEEN ? AND ?
		GROUP BY DATE(sold_at) ORDER BY date ASC`, tenantID, start, end).Scan(&stats).Error

	return stats, err
}

// GetProductStats 商品排行
func (s *ReportService) GetProductStats(tenantID uint, startDate, endDate string) ([]ProductStat, error) {
	var stats []ProductStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	err = model.DB.Raw(paidOrdersCTE+` SELECT order_items.product_name,
			SUM(order_items.quantity - COALESCE((SELECT SUM(CASE WHEN tickets.code_mode = 'order' THEN order_items.quantity ELSE 1 END) FROM tickets WHERE tickets.order_item_id = order_items.id AND tickets.status = 'refunded'), 0)) as total_sold,
			SUM(CAST(ROUND(order_items.price * 100.0) AS INTEGER) * (order_items.quantity - COALESCE((SELECT SUM(CASE WHEN tickets.code_mode = 'order' THEN order_items.quantity ELSE 1 END) FROM tickets WHERE tickets.order_item_id = order_items.id AND tickets.status = 'refunded'), 0))) / 100.0 as total_amount
		FROM paid_orders
		JOIN order_items ON order_items.order_id = paid_orders.order_id AND order_items.deleted_at IS NULL
		WHERE paid_orders.tenant_id = ? AND paid_orders.sold_at BETWEEN ? AND ?
		GROUP BY order_items.product_name
		ORDER BY total_sold DESC
		LIMIT 10`, tenantID, start, end).Scan(&stats).Error

	return stats, err
}

// GetChannelStats 渠道占比
func (s *ReportService) GetChannelStats(tenantID uint, startDate, endDate string) ([]ChannelStat, error) {
	var stats []ChannelStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	err = model.DB.Raw(salesFactsCTE+` SELECT channel, COUNT(order_id) AS order_count,
			SUM(gross_cents) / 100.0 AS total_amount,
			SUM(refund_cents) / 100.0 AS refunded_amount,
			SUM(gross_cents - refund_cents) / 100.0 AS net_amount
		FROM sales_facts
		WHERE tenant_id = ? AND sold_at BETWEEN ? AND ?
		GROUP BY channel ORDER BY channel`, tenantID, start, end).Scan(&stats).Error

	return stats, err
}
