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
		if req.Channel == "" {
			req.Channel = "online"
		}
		req.Status = "unpaid"

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

			// --- B2B Logic Start ---
			if product.SourceProductID > 0 {
				// 1. Calculate Cost
				cost := product.SettlementPrice * float64(item.Quantity)

				// 2. Load Capital Account
				var account model.CapitalAccount
				// Owner = Agent(Product.TenantID), Manager = Supplier(Product.SourceTenantID)
				if err := tx.Where("owner_tenant_id = ? AND manager_tenant_id = ?", product.TenantID, product.SourceTenantID).First(&account).Error; err != nil {
					return fmt.Errorf("分销资金账户不存在或异常")
				}

				// 3. Check Balance (Balance + CreditLine >= Cost)
				if account.Balance+account.CreditLine < cost {
					return fmt.Errorf("余额不足，无法采购 (需: %.2f, 余额: %.2f)", cost, account.Balance)
				}

				// 4. Deduct Balance
				account.Balance -= cost
				if account.Balance < 0 {
					// Use Credit
					account.UsedCredit += (0 - account.Balance)
					// account.Balance = 0 // Keep negative? Or handle logically.
					// Simplified: Allow balance to be negative if within credit line?
					// Better: Split logic.
					// MVP: Just subtract. If Balance becomes negative, it means we used credit (implicit).
					// But usually Balance should floor at 0.
					// Let's keep it simple: Balance is Cash. If Cost > Balance, deduct all Balance, rest adds to Usage?
					// For MVP, just subtract from Balance. If negative, it means owed.
					// wait, credit line check was (Balance + CreditLine < Cost).
					// yes, so simple subtraction works.
				}
				if err := tx.Save(&account).Error; err != nil {
					return err
				}

				// 5. Record Transaction
				trans := model.TransactionRecord{
					AccountID:      account.ID,
					Type:           "payment",
					Amount:         -cost,
					BalanceAfter:   account.Balance,
					RelatedOrderNo: req.OrderNo,
					Memo:           fmt.Sprintf("分销采购: %s x%d", product.Name, item.Quantity),
					OperatorID:     0, // System
				}
				if err := tx.Create(&trans).Error; err != nil {
					return err
				}

				// 6. Decrement Source Stock (Optimistic Lock ideally, simplistic here)
				// We need to load Source Product to update its stock?
				// Or assume SourceProduct.StockType is handled?
				var sourceProduct model.Product
				if err := tx.First(&sourceProduct, product.SourceProductID).Error; err != nil {
					return err
				}
				if sourceProduct.StockType != "unlimited" {
					if sourceProduct.DailyStock < item.Quantity {
						return fmt.Errorf("供应商库存不足")
					}
					sourceProduct.DailyStock -= item.Quantity
					if err := tx.Save(&sourceProduct).Error; err != nil {
						return err
					}
				}
			}
			// --- B2B Logic End ---

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

func (s *OrderService) List(page, pageSize int, tenantID uint, status string, channel string, startDate string, endDate string, search string) ([]model.Order, int64, error) {
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
	if channel != "" {
		if channel == "online" {
			// Treat empty or null channel as online for backward compatibility
			query = query.Where("channel = ? OR channel = '' OR channel IS NULL", "online")
		} else {
			query = query.Where("channel = ?", channel)
		}
	}
	if startDate != "" {
		query = query.Where("created_at >= ?", startDate+" 00:00:00")
	}
	if endDate != "" {
		query = query.Where("created_at <= ?", endDate+" 23:59:59")
	}
	if search != "" {
		query = query.Where("order_no LIKE ? OR contact_name LIKE ? OR contact_phone LIKE ?", "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Order("created_at desc").Offset(offset).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}

func (s *OrderService) GetByOrderNo(orderNo string) (*model.Order, error) {
	var order model.Order
	err := model.DB.Preload("Items").Preload("Items.Tickets").Where("order_no = ?", orderNo).First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *OrderService) Cancel(orderNo string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
			return err
		}

		if order.Status == "cancelled" {
			return fmt.Errorf("订单已取消")
		}
		if order.Status == "used" {
			return fmt.Errorf("订单已核销，无法取消")
		}

		// Update Order Status
		if err := tx.Model(&order).Update("status", "cancelled").Error; err != nil {
			return err
		}

		// Invalidate Tickets
		// Find all tickets for this order?
		// Order -> Items -> Tickets.
		// Use raw query or load associations.
		// Update tickets set status = 'void' where item_id in (select id from order_items where order_id = ?)
		// Or simpler logic:
		var itemIDs []uint
		tx.Model(&model.OrderItem{}).Where("order_id = ?", order.ID).Pluck("id", &itemIDs)

		if len(itemIDs) > 0 {
			if err := tx.Model(&model.Ticket{}).Where("order_item_id IN ?", itemIDs).Update("status", "void").Error; err != nil {
				return err
			}
		}

		// TODO: Refund Balance for B2B Orders (Future Scope)

		return nil
	})
}

// MarkAsPaid 标记订单为已支付
func (s *OrderService) MarkAsPaid(orderNo string) error {
	var order model.Order
	if err := model.DB.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		return err
	}

	if order.Status == "paid" {
		return nil // Already paid
	}

	if order.Status != "unpaid" {
		return fmt.Errorf("订单状态异常，无法支付: %s", order.Status)
	}

	order.Status = "paid"
	if err := model.DB.Save(&order).Error; err != nil {
		return err
	}

	// Future: Trigger ticket activation or external notification here
	return nil
}
