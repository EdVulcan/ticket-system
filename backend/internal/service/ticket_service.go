package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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
	return s.VerifyDeviceRequest(code, checkPointID, deviceID, tenantID, "")
}

func (s *TicketService) VerifyDeviceRequest(code string, checkPointID, deviceID, tenantID uint, deviceRequestID string) error {
	return s.verifyDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, deviceRequestID, 0, false)
}

// PrepareDeviceRequest runs the complete local admission validation and
// reserves the ticket for one external Xiaohongshu voucher verification. It
// does not create a successful check-in record or consume the ticket.
func (s *TicketService) PrepareDeviceRequest(code string, checkPointID, deviceID, tenantID, reservationID uint, deviceRequestID string) error {
	if reservationID == 0 {
		return errors.New("external verification reservation is required")
	}
	return s.verifyDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, deviceRequestID, reservationID, true)
}

// VerifyDeviceRequestReserved commits the local admission fact for a ticket
// previously reserved by PrepareDeviceRequest. The reservation id is checked
// inside the same ticket lock so another path cannot bypass the coordinator.
func (s *TicketService) VerifyDeviceRequestReserved(code string, checkPointID, deviceID, tenantID uint, deviceRequestID string, reservationID uint) error {
	if reservationID == 0 {
		return errors.New("external verification reservation is required")
	}
	return s.verifyDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, deviceRequestID, reservationID, false)
}

func (s *TicketService) verifyDeviceRequestWithReservation(code string, checkPointID, deviceID, tenantID uint, deviceRequestID string, reservationID uint, prepareOnly bool) error {
	var ticketID uint
	var recordScenicAreaID uint
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		if checkPointID == 0 || deviceID == 0 {
			return ErrAccessDenied
		}
		var checkpoint model.CheckPoint
		if err := tx.Where("id = ? AND tenant_id = ?", checkPointID, tenantID).First(&checkpoint).Error; err != nil {
			return ErrCheckpointNotFound
		}
		if checkpoint.ScenicAreaID == 0 {
			return ErrAccessDenied
		}
		recordScenicAreaID = checkpoint.ScenicAreaID
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND check_point_id = ?", deviceID, tenantID, checkpoint.ScenicAreaID, checkpoint.ID).First(&device).Error; err != nil {
			return ErrAccessDenied
		}

		var ticket model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).
			Preload("OrderItem.Product").
			Where("ticket_code = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", code, tenantID, tenantID).
			First(&ticket).Error; err != nil {
			if isTicketLockUnavailable(err) {
				return fmt.Errorf("%w: concurrent verification", ErrTicketUnavailable)
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidTicket
			}
			return err
		}
		ticketID = ticket.ID
		if reservationID == 0 && ticket.PendingXiaohongshuVerificationID != 0 {
			return fmt.Errorf("%w: external verification pending", ErrTicketUnavailable)
		}
		if reservationID != 0 && ticket.PendingXiaohongshuVerificationID != 0 && ticket.PendingXiaohongshuVerificationID != reservationID {
			return fmt.Errorf("%w: external verification pending", ErrTicketUnavailable)
		}
		if ticket.Environment == "sandbox" {
			return ErrInvalidTicket
		}

		var order model.Order
		salesTenantID := ticket.TenantID
		if salesTenantID == 0 {
			salesTenantID = ticket.OrderItem.Product.TenantID
		}
		if err := tx.Where("id = ? AND tenant_id = ?", ticket.OrderItem.OrderID, salesTenantID).First(&order).Error; err != nil {
			return ErrInvalidTicket
		}
		if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
			return ErrOrderNotPaid
		}
		if ticket.Status != "unused" && ticket.Status != "active" {
			return fmt.Errorf("%w: %s", ErrTicketUnavailable, ticket.Status)
		}
		if ticket.PendingRefundID != 0 {
			return fmt.Errorf("%w: refund pending", ErrTicketUnavailable)
		}

		now := time.Now()
		if ticket.OrderItem.ValidityStart != nil && now.Before(*ticket.OrderItem.ValidityStart) {
			return ErrTicketNotStarted
		}
		if ticket.OrderItem.ValidityEnd != nil && now.After(*ticket.OrderItem.ValidityEnd) {
			return ErrTicketExpired
		}

		fulfillmentProductID := ticket.FulfillmentProductID
		fulfillmentTenantID := ticket.FulfillmentTenantID
		if fulfillmentProductID == 0 {
			fulfillmentProductID = ticket.OrderItem.FulfillmentProductID
		}
		if fulfillmentTenantID == 0 {
			fulfillmentTenantID = ticket.OrderItem.FulfillmentTenantID
		}
		if fulfillmentProductID == 0 {
			fulfillmentProductID = ticket.OrderItem.ProductID
		}
		if fulfillmentTenantID == 0 {
			fulfillmentTenantID = salesTenantID
		}
		if fulfillmentTenantID != tenantID {
			return ErrInvalidTicket
		}
		fulfillmentScenicAreaID := ticket.FulfillmentScenicAreaID
		if fulfillmentScenicAreaID == 0 {
			fulfillmentScenicAreaID = ticket.OrderItem.FulfillmentScenicAreaID
		}
		if fulfillmentScenicAreaID == 0 || fulfillmentScenicAreaID != checkpoint.ScenicAreaID {
			return ErrInvalidTicket
		}
		var product model.Product
		if ticket.RuleSnapshot != "" {
			var rule model.TicketRule
			if err := json.Unmarshal([]byte(ticket.RuleSnapshot), &rule); err != nil {
				return ErrInvalidTicket
			}
			if rule.TenantID != 0 && rule.TenantID != fulfillmentTenantID {
				return ErrInvalidTicket
			}
			product.CodeMode = ticket.CodeMode
			if product.CodeMode == "" {
				product.CodeMode = ticket.OrderItem.Product.CodeMode
			}
			product.Rule = rule
		} else {
			// Legacy tickets predate rule snapshots. Keep the compatibility path
			// until those rows are migrated or naturally retired.
			if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").
				Where("id = ? AND tenant_id = ?", fulfillmentProductID, fulfillmentTenantID).First(&product).Error; err != nil {
				return ErrInvalidTicket
			}
		}
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
		if prepareOnly {
			if ticket.PendingXiaohongshuVerificationID != 0 && ticket.PendingXiaohongshuVerificationID != reservationID {
				return fmt.Errorf("%w: external verification pending", ErrTicketUnavailable)
			}
			return tx.Model(&ticket).Update("pending_xiaohongshu_verification_id", reservationID).Error
		}
		if reservationID != 0 && ticket.PendingXiaohongshuVerificationID != reservationID {
			return fmt.Errorf("%w: external verification reservation is missing", ErrTicketUnavailable)
		}

		record := model.CheckInRecord{
			TicketID: ticket.ID, TicketCode: code, TenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID,
			CheckPointID: checkPointID, DeviceID: deviceID, DeviceRequestID: deviceRequestID, CheckInTime: now,
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
		if reservationID != 0 {
			ticket.PendingXiaohongshuVerificationID = 0
		}
		if err := tx.Save(&ticket).Error; err != nil {
			return err
		}
		entitlementStatus := "active"
		if ticket.Status == "used" {
			entitlementStatus = "used"
		}
		if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ?", ticket.ID).Update("status", entitlementStatus).Error; err != nil {
			return err
		}

		if ticket.Status == "used" {
			var remaining int64
			if err := tx.Model(&model.Ticket{}).
				Joins("JOIN order_items ON order_items.id = tickets.order_item_id").
				Where("order_items.order_id = ? AND tickets.status IN ?", order.ID, []string{"pending_booking", "unused", "active"}).
				Count(&remaining).Error; err != nil {
				return err
			}
			if remaining == 0 {
				if err := tx.Model(&order).Update("status", "completed").Error; err != nil {
					return err
				}
				if err := updateFulfillmentOrdersTx(tx, order.ID, "fulfilled"); err != nil {
					return err
				}
			}
		}
		if _, err := enqueueCtripConsumedNoticeTx(tx, salesTenantID, order.ID); err != nil {
			return err
		}
		return nil
	})

	if err != nil && !prepareOnly {
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Create(&model.CheckInRecord{
				TicketID: ticketID, TicketCode: code, TenantID: tenantID, ScenicAreaID: recordScenicAreaID,
				CheckPointID: checkPointID, DeviceID: deviceID, DeviceRequestID: deviceRequestID, CheckInTime: time.Now(),
				Result: "deny", Message: err.Error(),
			}).Error
		})
	}
	return err
}

func isTicketLockUnavailable(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "55P03"
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
