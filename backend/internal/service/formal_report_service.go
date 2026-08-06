package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"
)

type FormalReportFilter struct {
	StartDate    string
	EndDate      string
	Channel      string
	Method       string
	OrderNo      string
	ProductName  string
	ScenicAreaID uint
}

type BusinessSummaryRow struct {
	Date         string `json:"date"`
	Channel      string `json:"channel"`
	Method       string `json:"method"`
	PaymentCount int64  `json:"payment_count"`
	RefundCount  int64  `json:"refund_count"`
	GrossCents   int64  `json:"gross_cents"`
	RefundCents  int64  `json:"refund_cents"`
	NetCents     int64  `json:"net_cents"`
}

type BusinessDetailRow struct {
	RecordID      uint   `json:"record_id"`
	OccurredAt    string `json:"occurred_at"`
	FactType      string `json:"fact_type"`
	OrderNo       string `json:"order_no"`
	TransactionNo string `json:"transaction_no"`
	Channel       string `json:"channel"`
	Method        string `json:"method"`
	AmountCents   int64  `json:"amount_cents"`
	ProductNames  string `json:"product_names"`
	ContactName   string `json:"contact_name"`
	ContactPhone  string `json:"contact_phone"`
	OperatorID    uint   `json:"operator_id"`
	Reason        string `json:"reason"`
}

type VerificationSummaryRow struct {
	Date           string `json:"date"`
	ScenicAreaID   uint   `json:"scenic_area_id"`
	ScenicAreaName string `json:"scenic_area_name"`
	ProductName    string `json:"product_name"`
	SellerName     string `json:"seller_name"`
	Channel        string `json:"channel"`
	VerifiedCount  int64  `json:"verified_count"`
	IncomeCents    int64  `json:"income_cents"`
}

type VerificationDetailRow struct {
	RecordID       uint      `json:"record_id"`
	CheckInTime    time.Time `json:"check_in_time"`
	ScenicAreaID   uint      `json:"scenic_area_id"`
	ScenicAreaName string    `json:"scenic_area_name"`
	ProductName    string    `json:"product_name"`
	TicketCode     string    `json:"ticket_code"`
	OrderNo        string    `json:"order_no"`
	SellerName     string    `json:"seller_name"`
	Channel        string    `json:"channel"`
	VerifiedCount  int64     `json:"verified_count"`
	IncomeCents    int64     `json:"income_cents"`
	VisitorName    string    `json:"visitor_name"`
	VisitorPhone   string    `json:"visitor_phone"`
	CheckPointName string    `json:"check_point_name"`
}

func validateFormalReportFilter(filter FormalReportFilter) (time.Time, time.Time, error) {
	start, end, err := reportWindow(filter.StartDate, filter.EndDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if end.Sub(start) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("report date range cannot exceed 366 days")
	}
	return start, end, nil
}

func reportPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize != 10 && pageSize != 20 && pageSize != 40 && pageSize != 10000 {
		pageSize = 20
	}
	return page, pageSize
}

func likeReportValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return "%" + value + "%"
}

func (s *ReportService) GetBusinessSummary(tenantID uint, filter FormalReportFilter) ([]BusinessSummaryRow, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	start, end, err := validateFormalReportFilter(filter)
	if err != nil {
		return nil, err
	}
	rows := make([]BusinessSummaryRow, 0)
	query := `
		WITH facts AS (
			SELECT DATE(COALESCE(p.paid_at, p.created_at)) AS date, o.channel AS channel, p.method AS method,
			       1 AS payment_count, 0 AS refund_count,
			       CASE WHEN p.amount_cents != 0 THEN p.amount_cents ELSE CAST(ROUND(p.amount * 100.0) AS INTEGER) END AS gross_cents,
			       0 AS refund_cents
			FROM payments p JOIN orders o ON o.tenant_id = p.tenant_id AND o.order_no = p.order_no
			WHERE p.tenant_id = ? AND o.environment = 'production' AND p.status IN ('paid', 'partial_refunded', 'refunded')
			  AND COALESCE(p.paid_at, p.created_at) BETWEEN ? AND ?
			  AND (? = '' OR o.channel = ?) AND (? = '' OR p.method = ?)
			UNION ALL
			SELECT DATE(COALESCE(r.updated_at, r.created_at)) AS date, o.channel AS channel, r.method AS method,
			       0 AS payment_count, 1 AS refund_count, 0 AS gross_cents,
			       CASE WHEN r.amount_cents != 0 THEN r.amount_cents ELSE CAST(ROUND(r.amount * 100.0) AS INTEGER) END AS refund_cents
			FROM refunds r JOIN orders o ON o.tenant_id = r.tenant_id AND o.order_no = r.order_no
			WHERE r.tenant_id = ? AND o.environment = 'production' AND r.status = 'succeeded'
			  AND (r.parent_refund_id != 0 OR NOT EXISTS (SELECT 1 FROM refunds child WHERE child.parent_refund_id = r.id))
			  AND COALESCE(r.updated_at, r.created_at) BETWEEN ? AND ?
			  AND (? = '' OR o.channel = ?) AND (? = '' OR r.method = ?)
		)
		SELECT date, channel, method, SUM(payment_count) AS payment_count, SUM(refund_count) AS refund_count,
		       SUM(gross_cents) AS gross_cents, SUM(refund_cents) AS refund_cents,
		       SUM(gross_cents) - SUM(refund_cents) AS net_cents
		FROM facts GROUP BY date, channel, method ORDER BY date DESC, channel, method`
	args := []interface{}{tenantID, start, end, filter.Channel, filter.Channel, filter.Method, filter.Method,
		tenantID, start, end, filter.Channel, filter.Channel, filter.Method, filter.Method}
	if err := model.DB.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ReportService) GetBusinessDetails(tenantID uint, filter FormalReportFilter, page, pageSize int) ([]BusinessDetailRow, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	start, end, err := validateFormalReportFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	page, pageSize = reportPage(page, pageSize)
	orderLike := likeReportValue(filter.OrderNo)
	productNamesExpression := `GROUP_CONCAT(DISTINCT oi.product_name)`
	if model.DB.Dialector.Name() == "postgres" {
		productNamesExpression = `STRING_AGG(DISTINCT oi.product_name, ',')`
	}
	base := fmt.Sprintf(`
		WITH details AS (
			SELECT p.id AS record_id, COALESCE(p.paid_at, p.created_at) AS occurred_at, 'payment' AS fact_type,
			       o.order_no, p.payment_no AS transaction_no, o.channel, p.method,
			       CASE WHEN p.amount_cents != 0 THEN p.amount_cents ELSE CAST(ROUND(p.amount * 100.0) AS INTEGER) END AS amount_cents,
			       (SELECT %s FROM order_items oi WHERE oi.order_id = o.id) AS product_names,
			       o.contact_name, o.contact_phone, p.operator_id, '' AS reason
			FROM payments p JOIN orders o ON o.tenant_id = p.tenant_id AND o.order_no = p.order_no
			WHERE p.tenant_id = ? AND o.environment = 'production' AND p.status IN ('paid', 'partial_refunded', 'refunded')
			  AND COALESCE(p.paid_at, p.created_at) BETWEEN ? AND ?
			  AND (? = '' OR o.channel = ?) AND (? = '' OR p.method = ?) AND (? = '' OR o.order_no LIKE ?)
			UNION ALL
			SELECT r.id AS record_id, COALESCE(r.updated_at, r.created_at) AS occurred_at, 'refund' AS fact_type,
			       o.order_no, r.refund_no AS transaction_no, o.channel, r.method,
			       CASE WHEN r.amount_cents != 0 THEN r.amount_cents ELSE CAST(ROUND(r.amount * 100.0) AS INTEGER) END AS amount_cents,
			       (SELECT %s FROM order_items oi WHERE oi.order_id = o.id) AS product_names,
			       o.contact_name, o.contact_phone, r.authorized_by AS operator_id, r.reason
			FROM refunds r JOIN orders o ON o.tenant_id = r.tenant_id AND o.order_no = r.order_no
			WHERE r.tenant_id = ? AND o.environment = 'production' AND r.parent_refund_id = 0 AND r.status IN ('succeeded', 'group_succeeded')
			  AND COALESCE(r.updated_at, r.created_at) BETWEEN ? AND ?
			  AND (? = '' OR o.channel = ?) AND (? = '' OR r.method = ?) AND (? = '' OR o.order_no LIKE ?)
		)`, productNamesExpression, productNamesExpression)
	args := []interface{}{tenantID, start, end, filter.Channel, filter.Channel, filter.Method, filter.Method, orderLike, orderLike,
		tenantID, start, end, filter.Channel, filter.Channel, filter.Method, filter.Method, orderLike, orderLike}
	var total int64
	if err := model.DB.Raw(base+` SELECT COUNT(*) FROM details`, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]BusinessDetailRow, 0)
	if err := model.DB.Raw(base+` SELECT * FROM details ORDER BY occurred_at DESC, record_id DESC LIMIT ? OFFSET ?`, append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

const verificationIncomeExpression = `CASE
	WHEN o.tenant_id = oi.fulfillment_tenant_id THEN CAST(ROUND(oi.price * 100.0) AS INTEGER) *
		CASE WHEN COALESCE(NULLIF(t.code_mode, ''), p.code_mode) = 'order' THEN oi.quantity ELSE 1 END
	ELSE (CAST(ROUND(oi.settlement_price * 100.0) AS INTEGER) *
		CASE WHEN COALESCE(NULLIF(t.code_mode, ''), p.code_mode) = 'order' THEN oi.quantity ELSE 1 END *
		(10000 - oi.commission_bps)) / 10000 END`

const verificationCountExpression = `CASE WHEN COALESCE(NULLIF(t.code_mode, ''), p.code_mode) = 'order' THEN oi.quantity ELSE 1 END`

func (s *ReportService) GetVerificationSummary(tenantID uint, filter FormalReportFilter) ([]VerificationSummaryRow, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	start, end, err := validateFormalReportFilter(filter)
	if err != nil {
		return nil, err
	}
	if err := requireActiveTenantCapability(model.DB, tenantID, "supplier"); err != nil {
		return nil, err
	}
	rows := make([]VerificationSummaryRow, 0)
	query := fmt.Sprintf(`
		SELECT DATE(c.check_in_time) AS date, c.scenic_area_id, sa.name AS scenic_area_name,
		       oi.product_name, seller.name AS seller_name, o.channel,
		       SUM(%s) AS verified_count, SUM(%s) AS income_cents
		FROM check_in_records c
		JOIN tickets t ON t.id = c.ticket_id AND t.status != 'refunded'
		JOIN order_items oi ON oi.id = t.order_item_id AND oi.fulfillment_tenant_id = ?
		JOIN orders o ON o.id = oi.order_id
		LEFT JOIN products p ON p.id = oi.product_id
		JOIN scenic_areas sa ON sa.id = c.scenic_area_id AND sa.tenant_id = ?
		JOIN tenants seller ON seller.id = o.tenant_id
		WHERE c.tenant_id = ? AND o.environment = 'production' AND c.result = 'success' AND c.reversed_at IS NULL
		  AND c.check_in_time BETWEEN ? AND ?
		  AND c.id = (SELECT MIN(first.id) FROM check_in_records first
		              WHERE first.ticket_id = c.ticket_id AND first.result = 'success' AND first.reversed_at IS NULL)
		  AND (? = 0 OR c.scenic_area_id = ?) AND (? = '' OR o.channel = ?)
		  AND (? = '' OR oi.product_name LIKE ?)
		GROUP BY DATE(c.check_in_time), c.scenic_area_id, sa.name, oi.product_name, seller.name, o.channel
		ORDER BY date DESC, sa.name, oi.product_name, seller.name`, verificationCountExpression, verificationIncomeExpression)
	productLike := likeReportValue(filter.ProductName)
	if err := model.DB.Raw(query, tenantID, tenantID, tenantID, start, end,
		filter.ScenicAreaID, filter.ScenicAreaID, filter.Channel, filter.Channel, productLike, productLike).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *ReportService) GetVerificationDetails(tenantID uint, filter FormalReportFilter, page, pageSize int) ([]VerificationDetailRow, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	start, end, err := validateFormalReportFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	if err := requireActiveTenantCapability(model.DB, tenantID, "supplier"); err != nil {
		return nil, 0, err
	}
	page, pageSize = reportPage(page, pageSize)
	productLike := likeReportValue(filter.ProductName)
	base := `
		FROM check_in_records c
		JOIN tickets t ON t.id = c.ticket_id AND t.status != 'refunded'
		JOIN order_items oi ON oi.id = t.order_item_id AND oi.fulfillment_tenant_id = ?
		JOIN orders o ON o.id = oi.order_id
		LEFT JOIN products p ON p.id = oi.product_id
		JOIN scenic_areas sa ON sa.id = c.scenic_area_id AND sa.tenant_id = ?
		JOIN tenants seller ON seller.id = o.tenant_id
		LEFT JOIN check_points cp ON cp.id = c.check_point_id AND cp.tenant_id = ?
		WHERE c.tenant_id = ? AND o.environment = 'production' AND c.result = 'success' AND c.reversed_at IS NULL
		  AND c.check_in_time BETWEEN ? AND ?
		  AND c.id = (SELECT MIN(first.id) FROM check_in_records first
		              WHERE first.ticket_id = c.ticket_id AND first.result = 'success' AND first.reversed_at IS NULL)
		  AND (? = 0 OR c.scenic_area_id = ?) AND (? = '' OR o.channel = ?)
		  AND (? = '' OR oi.product_name LIKE ?)`
	args := []interface{}{tenantID, tenantID, tenantID, tenantID, start, end,
		filter.ScenicAreaID, filter.ScenicAreaID, filter.Channel, filter.Channel, productLike, productLike}
	var total int64
	if err := model.DB.Raw(`SELECT COUNT(*) `+base, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	selectQuery := fmt.Sprintf(`SELECT c.id AS record_id, c.check_in_time, c.scenic_area_id, sa.name AS scenic_area_name,
		oi.product_name, t.ticket_code, o.order_no, seller.name AS seller_name, o.channel,
		%s AS verified_count, %s AS income_cents, t.visitor_name, t.visitor_phone,
		COALESCE(cp.name, '') AS check_point_name `+base+` ORDER BY c.check_in_time DESC, c.id DESC LIMIT ? OFFSET ?`, verificationCountExpression, verificationIncomeExpression)
	rows := make([]VerificationDetailRow, 0)
	if err := model.DB.Raw(selectQuery, append(args, pageSize, (page-1)*pageSize)...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
