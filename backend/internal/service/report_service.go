package service

import (
	"ticket-backend/internal/model"
)

type ReportService struct{}

type SalesStat struct {
	Date        string  `json:"date"`
	TotalAmount float64 `json:"total_amount"`
	OrderCount  int     `json:"order_count"`
}

type ProductStat struct {
	ProductName string  `json:"product_name"`
	TotalSold   int     `json:"total_sold"`
	TotalAmount float64 `json:"total_amount"`
}

type ChannelStat struct {
	Channel     string  `json:"channel"`
	OrderCount  int     `json:"order_count"`
	TotalAmount float64 `json:"total_amount"`
}

// GetSalesStats 销售趋势
func (s *ReportService) GetSalesStats(tenantID uint, startDate, endDate string) ([]SalesStat, error) {
	var stats []SalesStat

	// SQLite syntax for date formatting might differ (strftime).
	// Assuming SQLite given previous context, or generic SQL.
	// Using gorm's Raw for aggregation.
	// Note: For SQLite, DATE(created_at) works.

	err := model.DB.Model(&model.Order{}).
		Select("DATE(created_at) as date, SUM(total_amount) as total_amount, COUNT(id) as order_count").
		Where("tenant_id = ? AND status = ? AND created_at BETWEEN ? AND ?", tenantID, "paid", startDate, endDate).
		Group("DATE(created_at)").
		Order("date ASC").
		Scan(&stats).Error

	return stats, err
}

// GetProductStats 商品排行
func (s *ReportService) GetProductStats(tenantID uint, startDate, endDate string) ([]ProductStat, error) {
	var stats []ProductStat

	err := model.DB.Table("order_items").
		Joins("JOIN orders ON orders.id = order_items.order_id").
		Select("order_items.product_name, SUM(order_items.quantity) as total_sold, SUM(order_items.price * order_items.quantity) as total_amount").
		Where("orders.tenant_id = ? AND orders.status = ? AND orders.created_at BETWEEN ? AND ?", tenantID, "paid", startDate, endDate).
		Group("order_items.product_name").
		Order("total_sold DESC").
		Limit(10).
		Scan(&stats).Error

	return stats, err
}

// GetChannelStats 渠道占比
func (s *ReportService) GetChannelStats(tenantID uint, startDate, endDate string) ([]ChannelStat, error) {
	var stats []ChannelStat

	err := model.DB.Model(&model.Order{}).
		Select("channel, COUNT(id) as order_count, SUM(total_amount) as total_amount").
		Where("tenant_id = ? AND status = ? AND created_at BETWEEN ? AND ?", tenantID, "paid", startDate, endDate).
		Group("channel").
		Scan(&stats).Error

	return stats, err
}
