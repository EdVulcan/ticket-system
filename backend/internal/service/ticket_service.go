package service

import (
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidTicket      = errors.New("invalid ticket")
	ErrOrderNotPaid       = errors.New("order is not paid")
	ErrTicketUnavailable  = errors.New("ticket is unavailable")
	ErrTicketNotStarted   = errors.New("ticket is not valid yet")
	ErrTicketExpired      = errors.New("ticket has expired")
	ErrCheckpointNotFound = errors.New("checkpoint not found")
	ErrAccessDenied       = errors.New("ticket cannot be used at this checkpoint")
	ErrPointLimitReached  = errors.New("checkpoint admission limit reached")
	ErrGroupLimitReached  = errors.New("ticket benefit group limit reached")
)

type TicketService struct{}

func (s *TicketService) Verify(code string, checkPointID, deviceID, tenantID uint) error {
	var ticketID uint
	err := model.Write(func(tx *gorm.DB) error {
		var checkpoint model.CheckPoint
		if err := tx.Where("id = ? AND tenant_id = ?", checkPointID, tenantID).First(&checkpoint).Error; err != nil {
			return ErrCheckpointNotFound
		}

		var ticket model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("OrderItem.Product.Rule.Groups.Items").
			Where("ticket_code = ? AND tenant_id = ?", code, tenantID).
			First(&ticket).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidTicket
			}
			return err
		}
		ticketID = ticket.ID

		var order model.Order
		if err := tx.Where("id = ? AND tenant_id = ?", ticket.OrderItem.OrderID, tenantID).First(&order).Error; err != nil {
			return ErrInvalidTicket
		}
		if order.Status != "paid" && order.Status != "completed" {
			return ErrOrderNotPaid
		}
		if ticket.Status != "unused" && ticket.Status != "active" {
			return fmt.Errorf("%w: %s", ErrTicketUnavailable, ticket.Status)
		}

		now := time.Now()
		if ticket.OrderItem.ValidityStart != nil && now.Before(*ticket.OrderItem.ValidityStart) {
			return ErrTicketNotStarted
		}
		if ticket.OrderItem.ValidityEnd != nil && now.After(*ticket.OrderItem.ValidityEnd) {
			return ErrTicketExpired
		}

		product := ticket.OrderItem.Product
		matchedGroup, matchedItem := matchRule(product.Rule, checkPointID)
		if matchedGroup == nil || matchedItem == nil {
			return ErrAccessDenied
		}

		var records []model.CheckInRecord
		if err := tx.Where("ticket_id = ? AND result = ?", ticket.ID, "success").Find(&records).Error; err != nil {
			return err
		}
		limit := admissionLimit(&product, &ticket.OrderItem, matchedItem)
		if limit <= 0 || countAtCheckpoint(records, checkPointID) >= limit {
			return ErrPointLimitReached
		}
		if !groupAllowsCheckpoint(records, matchedGroup, checkPointID) {
			return ErrGroupLimitReached
		}

		record := model.CheckInRecord{
			TicketID: ticket.ID, TicketCode: code, TenantID: tenantID,
			CheckPointID: checkPointID, DeviceID: deviceID, CheckInTime: now,
			Result: "success", Message: "verified",
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		records = append(records, record)
		ticket.CheckInCount++
		if hasRemainingAdmission(&product, &ticket.OrderItem, records) {
			ticket.Status = "active"
		} else {
			ticket.Status = "used"
		}
		if err := tx.Save(&ticket).Error; err != nil {
			return err
		}

		if ticket.Status == "used" {
			var remaining int64
			if err := tx.Model(&model.Ticket{}).
				Joins("JOIN order_items ON order_items.id = tickets.order_item_id").
				Where("order_items.order_id = ? AND tickets.status IN ?", order.ID, []string{"unused", "active"}).
				Count(&remaining).Error; err != nil {
				return err
			}
			if remaining == 0 {
				if err := tx.Model(&order).Update("status", "completed").Error; err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Create(&model.CheckInRecord{
				TicketID: ticketID, TicketCode: code, TenantID: tenantID,
				CheckPointID: checkPointID, DeviceID: deviceID, CheckInTime: time.Now(),
				Result: "deny", Message: err.Error(),
			}).Error
		})
	}
	return err
}

func matchRule(rule model.TicketRule, checkpointID uint) (*model.RuleGroup, *model.RuleItem) {
	for groupIndex := range rule.Groups {
		group := &rule.Groups[groupIndex]
		for itemIndex := range group.Items {
			item := &group.Items[itemIndex]
			if item.CheckPointID == checkpointID {
				return group, item
			}
		}
	}
	return nil, nil
}

func admissionLimit(product *model.Product, item *model.OrderItem, ruleItem *model.RuleItem) int {
	limit := ruleItem.MaxPerCheckIn
	if product.CodeMode == "order" && item.Quantity > 1 {
		limit *= item.Quantity
	}
	return limit
}

func countAtCheckpoint(records []model.CheckInRecord, checkpointID uint) int {
	count := 0
	for _, record := range records {
		if record.CheckPointID == checkpointID {
			count++
		}
	}
	return count
}

func groupAllowsCheckpoint(records []model.CheckInRecord, group *model.RuleGroup, checkpointID uint) bool {
	if group.MaxTotalCheckIn <= 0 {
		return true
	}
	used := make(map[uint]struct{})
	for _, record := range records {
		for _, item := range group.Items {
			if item.CheckPointID == record.CheckPointID {
				used[record.CheckPointID] = struct{}{}
				break
			}
		}
	}
	if _, alreadyUsed := used[checkpointID]; alreadyUsed {
		return true
	}
	return len(used) < group.MaxTotalCheckIn
}

func hasRemainingAdmission(product *model.Product, orderItem *model.OrderItem, records []model.CheckInRecord) bool {
	for groupIndex := range product.Rule.Groups {
		group := &product.Rule.Groups[groupIndex]
		for itemIndex := range group.Items {
			item := &group.Items[itemIndex]
			if groupAllowsCheckpoint(records, group, item.CheckPointID) &&
				countAtCheckpoint(records, item.CheckPointID) < admissionLimit(product, orderItem, item) {
				return true
			}
		}
	}
	return false
}
