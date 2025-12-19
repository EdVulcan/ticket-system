package service

import (
	"errors"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type TicketService struct{}

// Verify 核销验票
func (s *TicketService) Verify(code string, checkPointID uint, deviceID uint) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Find Ticket with associations
		var ticket model.Ticket
		if err := tx.Preload("OrderItem").Preload("OrderItem.Tickets").Where("ticket_code = ?", code).First(&ticket).Error; err != nil {
			return errors.New("无效的票码")
		}

		// 2. Check Status
		if ticket.Status != "unused" && ticket.Status != "used" {
			return errors.New("票据状态异常: " + ticket.Status)
		}

		// 3. Find Product & Rule
		var product model.Product
		if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").First(&product, ticket.OrderItemID).Error; err != nil { // Note: OrderItemID is actually ProductID in OrderItem? No, OrderItem has ProductID.
			// Wait, Ticket -> OrderItem -> Product.
			// We need to load OrderItem first.
		}

		// Reload OrderItem to get ProductID
		var orderItem model.OrderItem
		if err := tx.First(&orderItem, ticket.OrderItemID).Error; err != nil {
			return errors.New("订单明细未找到")
		}

		// Now load Product with Rule
		if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").First(&product, orderItem.ProductID).Error; err != nil {
			return errors.New("产品信息未找到")
		}

		// 4. Validate Validity (Date/Days)
		now := time.Now()
		if orderItem.ValidityStart != nil && now.Before(*orderItem.ValidityStart) {
			return errors.New("未到生效时间")
		}
		if orderItem.ValidityEnd != nil && now.After(*orderItem.ValidityEnd) {
			return errors.New("票据已过期")
		}

		// 5. Validate Rule (M-choose-N & Frequency)
		// 5.1 Find which RuleGroup/RuleItem matches this CheckPoint
		var matchedGroup *model.RuleGroup
		var matchedItem *model.RuleItem

		for i := range product.Rule.Groups {
			group := &product.Rule.Groups[i]
			for j := range group.Items {
				item := &group.Items[j]
				if item.CheckPointID == checkPointID {
					matchedGroup = group
					matchedItem = item
					break
				}
			}
			if matchedGroup != nil {
				break
			}
		}

		if matchedGroup == nil {
			return errors.New("该票据无法在此检票点使用")
		}

		// 5.2 Check Limits
		// Count previous check-ins for this ticket
		var records []model.CheckInRecord
		if err := tx.Where("ticket_id = ? AND result = 'success'", ticket.ID).Find(&records).Error; err != nil {
			return err
		}

		// A. Check Point Limit (MaxPerCheckIn)
		// If One Order One Code, the limit scales with Quantity
		limitPerPoint := matchedItem.MaxPerCheckIn
		if product.CodeMode == "order" {
			limitPerPoint = matchedItem.MaxPerCheckIn * orderItem.Quantity
		}

		pointCheckInCount := 0
		for _, r := range records {
			if r.CheckPointID == checkPointID {
				pointCheckInCount++
			}
		}
		if pointCheckInCount >= limitPerPoint {
			return errors.New("该检票点通行次数已用完")
		}

		// B. Check Group Limit (MaxTotalCheckIn - M choose N)
		// If MaxTotalCheckIn is 0, it means "All Selectable" (Unlimited within the group's items scope? No, usually means all items can be used MaxPerCheckIn times).
		// If MaxTotalCheckIn > 0, it limits the number of *distinct checkpoints* used? Or total check-ins?
		// User said: "M选N... 总核销次数应该是可核销检票点... 举个例子，我设置了检票点A可以检票2次，那我设置可核销检票点为2就不对，应该是1"
		// User implies M is "Number of Allowed Distinct Checkpoints".

		if matchedGroup.MaxTotalCheckIn > 0 {
			// Count distinct checkpoints used in this group
			usedCheckPoints := make(map[uint]bool)
			for _, r := range records {
				// We need to know if record 'r' belongs to this group.
				// Since we don't store GroupID in CheckInRecord, we have to infer or check against group.Items.
				for _, item := range matchedGroup.Items {
					if item.CheckPointID == r.CheckPointID {
						usedCheckPoints[r.CheckPointID] = true
						break
					}
				}
			}

			// If this checkpoint hasn't been used yet, check if we have room for a new one
			if !usedCheckPoints[checkPointID] {
				if len(usedCheckPoints) >= matchedGroup.MaxTotalCheckIn {
					return errors.New("已达到该规则组允许的可选点位数量上限")
				}
			}
		}

		// 6. Record Check-in
		record := model.CheckInRecord{
			TicketID:     ticket.ID,
			CheckPointID: checkPointID,
			DeviceID:     deviceID,
			CheckInTime:  now,
			Result:       "success",
			Message:      "核销成功",
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}

		// 7. Update Ticket Status & Count
		ticket.CheckInCount++
		ticket.Status = "used" // Or 'partially_used'? For now simple 'used' is fine, or maybe keep 'unused' if still valid?
		// Let's set to 'used' if it has been used at least once.
		if err := tx.Save(&ticket).Error; err != nil {
			return err
		}

		return nil
	})
}
