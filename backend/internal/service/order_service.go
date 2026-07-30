package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrDuplicateExternalOrder = errors.New("external order already exists")

type OrderService struct{}

func (s *OrderService) GenerateOrderNo() string {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("ORD%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(random)))
}

func (s *OrderService) GenerateTicketCode() string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return "T" + strings.ToUpper(hex.EncodeToString(random))
}

func roundMoney(value float64) float64 {
	return math.Round(value*100) / 100
}

func (s *OrderService) Create(req *model.Order) error {
	if err := validateOrder(req); err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if req.ExternalNo != nil {
			var count int64
			if err := tx.Model(&model.Order{}).Where(
				"tenant_id = ? AND channel = ? AND external_no = ?", req.TenantID, req.Channel, *req.ExternalNo,
			).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrDuplicateExternalOrder
			}
		}
		req.Base = model.Base{}
		req.OrderNo = s.GenerateOrderNo()
		req.Status = "unpaid"
		req.TotalAmount = 0

		for i := range req.Items {
			item := &req.Items[i]
			item.Base = model.Base{}
			item.OrderID = 0
			item.Tickets = nil

			var product model.Product
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND tenant_id = ? AND status = ?", item.ProductID, req.TenantID, "online").
				First(&product).Error; err != nil {
				return fmt.Errorf("product %d is unavailable", item.ProductID)
			}

			item.ProductName = product.Name
			item.Price = roundMoney(product.Price)
			item.SettlementPrice = roundMoney(product.SettlementPrice)
			item.ValidityType = product.ValidityType
			if err := applyValidity(item, &product); err != nil {
				return fmt.Errorf("%s: %w", product.Name, err)
			}

			stockProduct := &product
			if product.SourceProductID > 0 {
				var sourceProduct model.Product
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("id = ? AND tenant_id = ? AND status = ?", product.SourceProductID, product.SourceTenantID, "online").
					First(&sourceProduct).Error; err != nil {
					return fmt.Errorf("source product for %s is unavailable", product.Name)
				}
				stockProduct = &sourceProduct
				if err := chargeDistributionAccount(tx, req, item, &product); err != nil {
					return err
				}
			}
			if err := reserveStock(tx, stockProduct, item.UseDate, item.Quantity); err != nil {
				return err
			}

			req.TotalAmount = roundMoney(req.TotalAmount + item.Price*float64(item.Quantity))
			item.Tickets = buildTickets(s, &product, item.Quantity, req)
		}

		return tx.Create(req).Error
	})
}

func validateOrder(req *model.Order) error {
	if req.TenantID == 0 {
		return fmt.Errorf("tenant is required")
	}
	if len(req.Items) == 0 {
		return fmt.Errorf("order must contain at least one item")
	}
	if len(req.ContactName) > 50 || len(req.ContactPhone) > 20 {
		return fmt.Errorf("contact information is too long")
	}
	if req.Channel == "" {
		req.Channel = "online"
	}
	if req.Channel != "online" && req.Channel != "ota" && req.Channel != "window" {
		return fmt.Errorf("invalid order channel")
	}
	if req.ExternalNo != nil {
		externalNo := strings.TrimSpace(*req.ExternalNo)
		if externalNo == "" {
			req.ExternalNo = nil
		} else if len(externalNo) > 100 {
			return fmt.Errorf("external order number is too long")
		} else {
			req.ExternalNo = &externalNo
		}
	}
	for _, item := range req.Items {
		if item.ProductID == 0 || item.Quantity <= 0 || item.Quantity > 1000 {
			return fmt.Errorf("item quantity must be between 1 and 1000")
		}
	}
	return nil
}

func applyValidity(item *model.OrderItem, product *model.Product) error {
	now := time.Now()
	if item.UseDate != nil {
		normalized := startOfDay(*item.UseDate)
		item.UseDate = &normalized
	}

	switch product.ValidityType {
	case "date":
		item.ValidityStart = product.ValidityStartDate
		item.ValidityEnd = product.ValidityEndDate
		if item.UseDate != nil {
			if product.ValidityStartDate != nil && item.UseDate.Before(startOfDay(*product.ValidityStartDate)) {
				return fmt.Errorf("visit date is before the validity period")
			}
			if product.ValidityEndDate != nil && item.UseDate.After(startOfDay(*product.ValidityEndDate)) {
				return fmt.Errorf("visit date is after the validity period")
			}
		}
	case "days":
		start := now
		if item.UseDate != nil {
			start = startOfDay(*item.UseDate)
		}
		end := start.AddDate(0, 0, product.ValidityDays)
		end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, end.Location())
		item.ValidityStart = &start
		item.ValidityEnd = &end
	default:
		return fmt.Errorf("invalid validity type")
	}

	if product.StockType == "daily" && item.UseDate == nil {
		return fmt.Errorf("visit date is required for daily stock")
	}
	return nil
}

func startOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func reserveStock(tx *gorm.DB, product *model.Product, useDate *time.Time, quantity int) error {
	switch product.StockType {
	case "", "unlimited":
		return nil
	case "total":
		result := tx.Model(&model.Product{}).
			Where("id = ? AND daily_stock >= ?", product.ID, quantity).
			UpdateColumn("daily_stock", gorm.Expr("daily_stock - ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("insufficient stock for %s", product.Name)
		}
		return nil
	case "daily":
		if useDate == nil {
			return fmt.Errorf("visit date is required for daily stock")
		}
		stockDate := startOfDay(*useDate)
		inventory := model.ProductInventory{
			TenantID: product.TenantID, ProductID: product.ID, StockDate: stockDate, Capacity: product.DailyStock,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&inventory).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ProductInventory{}).
			Where("tenant_id = ? AND product_id = ? AND stock_date = ? AND sold + ? <= capacity", product.TenantID, product.ID, stockDate, quantity).
			UpdateColumn("sold", gorm.Expr("sold + ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("insufficient stock for %s on %s", product.Name, stockDate.Format("2006-01-02"))
		}
		return nil
	default:
		return fmt.Errorf("invalid stock type for %s", product.Name)
	}
}

func releaseStock(tx *gorm.DB, product *model.Product, useDate *time.Time, quantity int) error {
	switch product.StockType {
	case "", "unlimited":
		return nil
	case "total":
		return tx.Model(&model.Product{}).Where("id = ?", product.ID).
			UpdateColumn("daily_stock", gorm.Expr("daily_stock + ?", quantity)).Error
	case "daily":
		if useDate == nil {
			return fmt.Errorf("daily stock reservation has no visit date")
		}
		result := tx.Model(&model.ProductInventory{}).
			Where("tenant_id = ? AND product_id = ? AND stock_date = ? AND sold >= ?", product.TenantID, product.ID, startOfDay(*useDate), quantity).
			UpdateColumn("sold", gorm.Expr("sold - ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("stock reservation is inconsistent")
		}
		return nil
	default:
		return fmt.Errorf("invalid stock type for %s", product.Name)
	}
}

func chargeDistributionAccount(tx *gorm.DB, order *model.Order, item *model.OrderItem, product *model.Product) error {
	cost := roundMoney(item.SettlementPrice * float64(item.Quantity))
	var account model.CapitalAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ? AND status = ?", product.TenantID, product.SourceTenantID, "active").
		First(&account).Error; err != nil {
		return fmt.Errorf("distribution capital account is unavailable")
	}
	availableCredit := account.CreditLine - account.UsedCredit
	if account.Balance+availableCredit < cost {
		return fmt.Errorf("insufficient distribution balance")
	}
	cashUsed := math.Min(account.Balance, cost)
	creditUsed := cost - cashUsed
	account.Balance = roundMoney(account.Balance - cashUsed)
	account.UsedCredit = roundMoney(account.UsedCredit + creditUsed)
	if err := tx.Save(&account).Error; err != nil {
		return err
	}
	return tx.Create(&model.TransactionRecord{
		AccountID: account.ID, Type: "payment", Amount: -cost, BalanceAfter: account.Balance,
		RelatedOrderNo: order.OrderNo, Memo: fmt.Sprintf("distribution purchase: %s x%d", product.Name, item.Quantity),
	}).Error
}

func refundDistributionAccount(tx *gorm.DB, order *model.Order, item *model.OrderItem, product *model.Product) error {
	amount := roundMoney(item.SettlementPrice * float64(item.Quantity))
	var account model.CapitalAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ?", product.TenantID, product.SourceTenantID).
		First(&account).Error; err != nil {
		return err
	}
	creditRepaid := math.Min(account.UsedCredit, amount)
	account.UsedCredit = roundMoney(account.UsedCredit - creditRepaid)
	account.Balance = roundMoney(account.Balance + amount - creditRepaid)
	if err := tx.Save(&account).Error; err != nil {
		return err
	}
	return tx.Create(&model.TransactionRecord{
		AccountID: account.ID, Type: "refund", Amount: amount, BalanceAfter: account.Balance,
		RelatedOrderNo: order.OrderNo, Memo: fmt.Sprintf("cancelled distribution purchase: %s x%d", product.Name, item.Quantity),
	}).Error
}

func buildTickets(service *OrderService, product *model.Product, quantity int, order *model.Order) []model.Ticket {
	count := quantity
	if product.CodeMode == "order" {
		count = 1
	}
	tickets := make([]model.Ticket, count)
	for i := range tickets {
		tickets[i] = model.Ticket{
			TicketCode: service.GenerateTicketCode(), Status: "unused", TenantID: order.TenantID,
			VisitorName: order.ContactName, VisitorPhone: order.ContactPhone,
		}
	}
	return tickets
}

func (s *OrderService) List(page, pageSize int, tenantID uint, status, channel, startDate, endDate, search string) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64
	if tenantID == 0 {
		return nil, 0, fmt.Errorf("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	query := model.DB.Model(&model.Order{}).Preload("Items").Preload("Items.Tickets").Where("tenant_id = ?", tenantID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if channel != "" {
		query = query.Where("channel = ?", channel)
	}
	if startDate != "" {
		query = query.Where("created_at >= ?", startDate+" 00:00:00")
	}
	if endDate != "" {
		query = query.Where("created_at <= ?", endDate+" 23:59:59")
	}
	if search != "" {
		like := "%" + search + "%"
		query = query.Where("order_no LIKE ? OR external_no LIKE ? OR contact_name LIKE ? OR contact_phone LIKE ?", like, like, like, like)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("created_at desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}

func (s *OrderService) GetByOrderNo(orderNo string, tenantID uint) (*model.Order, error) {
	var order model.Order
	err := model.DB.Preload("Items").Preload("Items.Tickets").
		Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error
	return &order, err
}

func (s *OrderService) GetByExternalNo(externalNo, channel string, tenantID uint) (*model.Order, error) {
	var order model.Order
	err := model.DB.Preload("Items").Preload("Items.Tickets").
		Where("external_no = ? AND channel = ? AND tenant_id = ?", externalNo, channel, tenantID).First(&order).Error
	return &order, err
}

func (s *OrderService) Cancel(orderNo string, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "cancelled" {
			return nil
		}
		if order.Status != "unpaid" && !(order.Status == "paid" && order.Channel == "ota") {
			return fmt.Errorf("paid orders must use the refund workflow")
		}
		for _, item := range order.Items {
			for _, ticket := range item.Tickets {
				if ticket.CheckInCount > 0 || ticket.Status == "used" {
					return fmt.Errorf("used orders cannot be cancelled")
				}
			}
		}

		for i := range order.Items {
			item := &order.Items[i]
			var product model.Product
			if err := tx.Where("id = ? AND tenant_id = ?", item.ProductID, tenantID).First(&product).Error; err != nil {
				return err
			}
			stockProduct := &product
			if product.SourceProductID > 0 {
				var source model.Product
				if err := tx.Where("id = ? AND tenant_id = ?", product.SourceProductID, product.SourceTenantID).First(&source).Error; err != nil {
					return err
				}
				stockProduct = &source
				if err := refundDistributionAccount(tx, &order, item, &product); err != nil {
					return err
				}
			}
			if err := releaseStock(tx, stockProduct, item.UseDate, item.Quantity); err != nil {
				return err
			}
		}

		if err := tx.Model(&order).Update("status", "cancelled").Error; err != nil {
			return err
		}
		var itemIDs []uint
		if err := tx.Model(&model.OrderItem{}).Where("order_id = ?", order.ID).Pluck("id", &itemIDs).Error; err != nil {
			return err
		}
		if len(itemIDs) > 0 {
			return tx.Model(&model.Ticket{}).Where("order_item_id IN ?", itemIDs).Update("status", "void").Error
		}
		return nil
	})
}

func (s *OrderService) MarkAsPaid(orderNo string, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "paid" {
			return nil
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		return tx.Model(&order).Update("status", "paid").Error
	})
}
