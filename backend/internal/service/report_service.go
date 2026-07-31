package service

import (
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

// GetSalesStats 销售趋势
func (s *ReportService) GetSalesStats(tenantID uint, startDate, endDate string) ([]SalesStat, error) {
	var stats []SalesStat
	start, end, err := reportWindow(startDate, endDate)
	if err != nil {
		return nil, err
	}

	// SQLite syntax for date formatting might differ (strftime).
	// Assuming SQLite given previous context, or generic SQL.
	// Using gorm's Raw for aggregation.
	// Note: For SQLite, DATE(created_at) works.

	err = model.DB.Table("orders").
		Select(`DATE(orders.created_at) as date,
			SUM(orders.total_amount) as total_amount,
			COALESCE(SUM((SELECT COALESCE(SUM(refunds.amount), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) as refunded_amount,
			SUM(orders.total_amount) - COALESCE(SUM((SELECT COALESCE(SUM(refunds.amount), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) as net_amount,
			COUNT(orders.id) as order_count`).
		Where("orders.tenant_id = ? AND orders.status IN ? AND orders.created_at BETWEEN ? AND ?", tenantID, completedSalesStatuses(), start, end).
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
			SUM(order_items.price * (order_items.quantity - COALESCE((SELECT SUM(CASE WHEN tickets.code_mode = 'order' THEN order_items.quantity ELSE 1 END) FROM tickets WHERE tickets.order_item_id = order_items.id AND tickets.status = 'refunded'), 0))) as total_amount`).
		Where("orders.tenant_id = ? AND orders.status IN ? AND orders.created_at BETWEEN ? AND ?", tenantID, completedSalesStatuses(), start, end).
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
		Select(`channel, COUNT(id) as order_count, SUM(total_amount) as total_amount,
			COALESCE(SUM((SELECT COALESCE(SUM(refunds.amount), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) as refunded_amount,
			SUM(total_amount) - COALESCE(SUM((SELECT COALESCE(SUM(refunds.amount), 0) FROM refunds WHERE refunds.tenant_id = orders.tenant_id AND refunds.order_no = orders.order_no AND refunds.status = 'succeeded')), 0) as net_amount`).
		Where("tenant_id = ? AND status IN ? AND created_at BETWEEN ? AND ?", tenantID, completedSalesStatuses(), start, end).
		Group("channel").
		Scan(&stats).Error

	return stats, err
}
