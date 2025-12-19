package service

import (
	"fmt"
	"math/rand"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type OrderService struct{}

// GenerateOrderNo 生成订单号
func (s *OrderService) GenerateOrderNo() string {
	return fmt.Sprintf("ORD%s%04d", time.Now().Format("20060102150405"), rand.Intn(10000))
}

// GenerateTicketCode 生成核销码
func (s *OrderService) GenerateTicketCode() string {
	return fmt.Sprintf("T%s%04d", time.Now().Format("0102150405"), rand.Intn(10000))
}

// Create 创建订单 (简化版，直接支付成功)
func (s *OrderService) Create(req *model.Order) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Basic Info
		req.OrderNo = s.GenerateOrderNo()
		req.Status = "paid" // Mock payment success

		// 2. Process Items & Tickets
		for i := range req.Items {
			item := &req.Items[i]

			// Fetch Product Snapshot (In real app, verify price/stock)
			var product model.Product
			if err := tx.First(&product, item.ProductID).Error; err != nil {
				return err
			}
			item.ProductName = product.Name
			item.Price = product.Price
			item.ValidityType = product.ValidityType

			// Calculate Validity
			now := time.Now()
			if product.ValidityType == "date" {
				item.ValidityStart = product.ValidityStartDate
				item.ValidityEnd = product.ValidityEndDate
			} else {
				item.ValidityStart = &now
				// Calculate end of the Nth day
				// If ValidityDays = 0 (Today), End = Today 23:59:59
				// If ValidityDays = 1 (Tomorrow), End = Tomorrow 23:59:59
				endDate := now.AddDate(0, 0, product.ValidityDays)
				endDate = time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 23, 59, 59, 0, endDate.Location())
				item.ValidityEnd = &endDate
			}

			// Generate Tickets
			// If CodeMode is 'order', all tickets share one code? Or just one ticket entry?
			// For simplicity in this MVP, we generate N tickets for Quantity N,
			// but if 'order' mode, they might share logic.
			// Let's stick to: 1 Quantity = 1 Ticket Record for now to track usage individually.
			// Or if 'order' mode, maybe we just have 1 Ticket record with Quantity?
			// Let's assume 1 Item Quantity = N Tickets.

			// Generate Tickets based on CodeMode
			if product.CodeMode == "order" {
				// One Order One Code: Create 1 Ticket
				item.Tickets = []model.Ticket{
					{
						TicketCode:   s.GenerateTicketCode(),
						Status:       "unused",
						VisitorName:  req.ContactName,
						VisitorPhone: req.ContactPhone,
					},
				}
			} else {
				// One Ticket One Code: Create N Tickets
				item.Tickets = make([]model.Ticket, item.Quantity)
				for j := 0; j < item.Quantity; j++ {
					item.Tickets[j] = model.Ticket{
						TicketCode:   s.GenerateTicketCode(),
						Status:       "unused",
						VisitorName:  req.ContactName, // Default to contact
						VisitorPhone: req.ContactPhone,
					}
				}
			}
		}

		if err := tx.Create(req).Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *OrderService) List(page, pageSize int, tenantID uint, status string) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64

	offset := (page - 1) * pageSize

	query := model.DB.Model(&model.Order{}).Preload("Items").Preload("Items.Tickets")
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Order("created_at desc").Offset(offset).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}
