package service

import (
	"errors"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type TicketService struct{}

// Verify 核销验票
// Verify 核销验票
func (s *TicketService) Verify(code string, checkPointID uint, deviceID uint, tenantID uint) error {
	// Helper to log result
	logResult := func(ticketID uint, result, message string) {
		record := model.CheckInRecord{
			TicketID:     ticketID,
			CheckPointID: checkPointID,
			DeviceID:     deviceID,
			CheckInTime:  time.Now(),
			Result:       result,
			Message:      message,
			TenantID:     tenantID,
			TicketCode:   code,
		}
		// Log independently (ignore error)
		model.DB.Create(&record)
	}

	// 1. Find Ticket with associations
	var ticket model.Ticket
	// Preload: OrderItem -> Product -> Rule -> Groups -> Items
	if err := model.DB.Preload("OrderItem.Product.Rule.Groups.Items").
		Where("ticket_code = ? AND tenant_id = ?", code, tenantID).
		First(&ticket).Error; err != nil {

		logResult(0, "deny", "Ticket Not Found")
		return errors.New("无效的票码")
	}

	// 2. Check Status
	if ticket.Status != "unused" && ticket.Status != "active" {
		msg := "票据状态异常: " + ticket.Status
		logResult(ticket.ID, "deny", msg)
		return errors.New(msg)
	}

	// 3. Find Product & Rule (Already Preloaded via OrderItem)
	orderItem := ticket.OrderItem
	product := orderItem.Product
	rule := product.Rule

	// 4. Validate Validity (Date/Days)
	now := time.Now()
	if orderItem.ValidityStart != nil && now.Before(*orderItem.ValidityStart) {
		logResult(ticket.ID, "deny", "Not yet valid")
		return errors.New("未到生效时间")
	}
	if orderItem.ValidityEnd != nil && now.After(*orderItem.ValidityEnd) {
		logResult(ticket.ID, "deny", "Expired")
		return errors.New("票据已过期")
	}

	// 5. Validate Rule (M-choose-N & Frequency)
	// 5.1 Find which RuleGroup/RuleItem matches this CheckPoint
	var matchedGroup *model.RuleGroup
	var matchedItem *model.RuleItem

	for i := range rule.Groups {
		group := &rule.Groups[i]
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
		logResult(ticket.ID, "deny", "Access Denied (No Rule)")
		return errors.New("该票据无法在此检票点使用")
	}

	// 5.2 Check Limits
	// Count previous check-ins for this ticket
	var records []model.CheckInRecord
	if err := model.DB.Where("ticket_id = ? AND result = 'success'", ticket.ID).Find(&records).Error; err != nil {
		return err
	}

	// A. Check Point Limit (MaxPerCheckIn)
	limitPerPoint := matchedItem.MaxPerCheckIn
	if product.CodeMode == "order" && orderItem.Quantity > 1 {
		limitPerPoint = matchedItem.MaxPerCheckIn * orderItem.Quantity
	}

	pointCheckInCount := 0
	for _, r := range records {
		if r.CheckPointID == checkPointID {
			pointCheckInCount++
		}
	}
	if pointCheckInCount >= limitPerPoint {
		logResult(ticket.ID, "deny", "Point Limit Reached")
		return errors.New("该检票点通行次数已用完")
	}

	// B. Check Group Limit (MaxTotalCheckIn - M choose N)
	if matchedGroup.MaxTotalCheckIn > 0 {
		usedCheckPoints := make(map[uint]bool)
		for _, r := range records {
			for _, item := range matchedGroup.Items {
				if item.CheckPointID == r.CheckPointID {
					usedCheckPoints[r.CheckPointID] = true
					break
				}
			}
		}

		if !usedCheckPoints[checkPointID] {
			if len(usedCheckPoints) >= matchedGroup.MaxTotalCheckIn {
				logResult(ticket.ID, "deny", "Group Limit Reached")
				return errors.New("已达到该规则组允许的可选点位数量上限")
			}
		}
	}

	// 6. Record Success Check-in (Transaction)
	return model.DB.Transaction(func(tx *gorm.DB) error {
		record := model.CheckInRecord{
			TicketID:     ticket.ID,
			CheckPointID: checkPointID,
			DeviceID:     deviceID,
			CheckInTime:  now,
			Result:       "success",
			Message:      "核销成功",
			TenantID:     tenantID, // Assign TenantID
			TicketCode:   code,     // Assign TicketCode
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}

		ticket.CheckInCount++
		ticket.Status = "used"
		// If needed, check if partial use is supported, but simplistic for now
		if err := tx.Save(&ticket).Error; err != nil {
			return err
		}

		return nil
	})
}
