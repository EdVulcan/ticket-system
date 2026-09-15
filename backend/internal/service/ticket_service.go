package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidTicket      = errors.New("invalid ticket")
	ErrOrderNotPaid       = errors.New("order is not paid")
	ErrTicketRefunded     = errors.New("ticket has been refunded")
	ErrTicketUnavailable  = errors.New("ticket is unavailable")
	ErrTicketNotStarted   = errors.New("ticket is not valid yet")
	ErrTicketExpired      = errors.New("ticket has expired")
	ErrCheckpointNotFound = errors.New("checkpoint not found")
	ErrAccessDenied       = errors.New("ticket cannot be used at this checkpoint")
	ErrPointLimitReached  = errors.New("checkpoint admission limit reached")
	ErrGroupLimitReached  = errors.New("ticket benefit group limit reached")
)

const ticketAdmissionPolicyPooledV1 = "pool_v1"

type TicketService struct{}

// VerifyBatchDeviceRequest atomically consumes quantity admissions for one
// shared-code ticket. It deliberately does not call VerifyDeviceRequest in a
// loop: the ticket row is locked once and all records are committed together.
func (s *TicketService) VerifyBatchDeviceRequest(code string, checkPointID, deviceID, tenantID uint, operationID string, quantity int) (int, error) {
	return s.verifyBatchDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, operationID, quantity, 0, false)
}

func (s *TicketService) PrepareBatchDeviceRequest(code string, checkPointID, deviceID, tenantID uint, operationID string, quantity int, reservationID uint) error {
	if reservationID == 0 {
		return errors.New("external verification reservation is required")
	}
	_, err := s.verifyBatchDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, operationID, quantity, reservationID, true)
	return err
}

func (s *TicketService) VerifyBatchDeviceRequestReserved(code string, checkPointID, deviceID, tenantID uint, operationID string, quantity int, reservationID uint) (int, error) {
	if reservationID == 0 {
		return 0, errors.New("external verification reservation is required")
	}
	return s.verifyBatchDeviceRequestWithReservation(code, checkPointID, deviceID, tenantID, operationID, quantity, reservationID, false)
}

func (s *TicketService) verifyBatchDeviceRequestWithReservation(code string, checkPointID, deviceID, tenantID uint, operationID string, quantity int, reservationID uint, prepareOnly bool) (int, error) {
	if quantity < 1 || strings.TrimSpace(code) == "" || strings.TrimSpace(operationID) == "" {
		return 0, errors.New("票码、操作号和核销数量不能为空")
	}
	remainingAfter := 0
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var checkpoint model.CheckPoint
		if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id != 0", checkPointID, tenantID).First(&checkpoint).Error; err != nil {
			return ErrAccessDenied
		}
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND check_point_id = ? AND scenic_area_id = ? AND type = ? AND status = ?", deviceID, tenantID, checkPointID, checkpoint.ScenicAreaID, "handheld", "online").First(&device).Error; err != nil {
			return ErrAccessDenied
		}
		var ticket model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).Preload("OrderItem.Product").Where("ticket_code = ? AND (fulfillment_tenant_id = ? OR (fulfillment_tenant_id = 0 AND tenant_id = ?))", strings.TrimSpace(code), tenantID, tenantID).First(&ticket).Error; err != nil {
			if isTicketLockUnavailable(err) {
				return fmt.Errorf("%w: concurrent verification", ErrTicketUnavailable)
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidTicket
			}
			return err
		}
		if !prepareOnly {
			var existingCount int64
			if err := tx.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND device_id = ? AND device_request_id = ? AND result = ?", ticket.ID, deviceID, operationID, "success").Count(&existingCount).Error; err != nil {
				return err
			}
			if existingCount > 0 {
				if existingCount == int64(quantity) {
					return nil
				}
				return errors.New("批量核销操作记录数量不一致，请人工核查")
			}
		}
		if ticket.CodeMode != "order" || ticket.OrderItem.Quantity <= 1 {
			return errors.New("该票不是可批量核销的共享码")
		}
		if ticket.Status == "refunded" {
			return ErrTicketRefunded
		}
		if ticket.PendingRefundID != 0 || (reservationID == 0 && ticket.PendingXiaohongshuVerificationID != 0) || (reservationID != 0 && ticket.PendingXiaohongshuVerificationID != 0 && ticket.PendingXiaohongshuVerificationID != reservationID) {
			return fmt.Errorf("%w: refund pending", ErrTicketUnavailable)
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
		if err := EnsureNoXiaohongshuRefundHoldTx(tx, &order); err != nil {
			return err
		}
		if err := ensureXiaohongshuTicketAdmissionTx(tx, &order, &ticket, reservationID, deviceID, checkPointID, operationID, prepareOnly); err != nil {
			return err
		}
		if ticket.OrderItem.ValidityStart != nil && time.Now().Before(*ticket.OrderItem.ValidityStart) {
			return ErrTicketNotStarted
		}
		if ticket.OrderItem.ValidityEnd != nil && time.Now().After(*ticket.OrderItem.ValidityEnd) {
			return ErrTicketExpired
		}
		fulfillmentTenantID := ticket.FulfillmentTenantID
		if fulfillmentTenantID == 0 {
			fulfillmentTenantID = ticket.OrderItem.FulfillmentTenantID
		}
		if fulfillmentTenantID == 0 {
			fulfillmentTenantID = salesTenantID
		}
		if fulfillmentTenantID != tenantID {
			return ErrInvalidTicket
		}
		if ticket.FulfillmentScenicAreaID != 0 && ticket.FulfillmentScenicAreaID != checkpoint.ScenicAreaID {
			return ErrInvalidTicket
		}
		var product model.Product
		if ticket.RuleSnapshot != "" {
			var rule model.TicketRule
			if err := json.Unmarshal([]byte(ticket.RuleSnapshot), &rule); err != nil {
				return ErrInvalidTicket
			}
			product.CodeMode = ticket.CodeMode
			product.Rule = rule
		} else {
			productID := ticket.FulfillmentProductID
			if productID == 0 {
				productID = ticket.OrderItem.FulfillmentProductID
			}
			if productID == 0 {
				productID = ticket.OrderItem.ProductID
			}
			if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", productID, fulfillmentTenantID).First(&product).Error; err != nil {
				return ErrInvalidTicket
			}
		}
		group, item := matchRule(product.Rule, checkPointID)
		if group == nil || item == nil {
			return ErrAccessDenied
		}
		var records []model.CheckInRecord
		if err := tx.Where("ticket_id = ? AND result = ?", ticket.ID, "success").Find(&records).Error; err != nil {
			return err
		}
		if quantity > admissionLimit(&product, &ticket.OrderItem, item)-countAtCheckpoint(records, checkPointID) {
			return ErrPointLimitReached
		}
		simulated := append([]model.CheckInRecord(nil), records...)
		for i := 0; i < quantity; i++ {
			if !groupAllowsCheckpointForTicket(simulated, &product, &ticket.OrderItem, group, checkPointID) {
				return ErrGroupLimitReached
			}
			if countAtCheckpoint(simulated, checkPointID) >= admissionLimit(&product, &ticket.OrderItem, item) {
				return ErrPointLimitReached
			}
			simulated = append(simulated, model.CheckInRecord{TicketID: ticket.ID, TicketCode: strings.TrimSpace(code), TenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID, CheckPointID: checkPointID, DeviceID: deviceID, DeviceRequestID: operationID, CheckInTime: time.Now(), Result: "success", Message: "verified"})
		}
		if prepareOnly {
			if ticket.PendingXiaohongshuVerificationID != 0 && ticket.PendingXiaohongshuVerificationID != reservationID {
				return fmt.Errorf("%w: external verification pending", ErrTicketUnavailable)
			}
			return tx.Model(&ticket).Update("pending_xiaohongshu_verification_id", reservationID).Error
		}
		for i := len(records); i < len(simulated); i++ {
			if err := tx.Create(&simulated[i]).Error; err != nil {
				return err
			}
		}
		records = simulated
		ticket.CheckInCount += quantity
		if reservationID != 0 {
			ticket.PendingXiaohongshuVerificationID = 0
		}
		if hasRemainingAdmission(&product, &ticket.OrderItem, records) {
			ticket.Status = "active"
		} else {
			ticket.Status = "used"
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
			if err := tx.Model(&model.Ticket{}).Joins("JOIN order_items ON order_items.id = tickets.order_item_id").Where("order_items.order_id = ? AND tickets.status IN ?", order.ID, []string{"pending_booking", "unused", "active"}).Count(&remaining).Error; err != nil {
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
		_, err := enqueueCtripConsumedNoticeTx(tx, salesTenantID, order.ID)
		remainingAfter = remainingAdmissionsAtCheckpoint(records, &product, &ticket.OrderItem, group, item, checkPointID)
		return err
	})
	return remainingAfter, err
}

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
		// A successful refund permanently removes the ticket entitlement. Check
		// this before the order/payment state so refunded orders get a precise
		// field response instead of being reported as unpaid or fully used.
		if ticket.Status == "refunded" {
			return ErrTicketRefunded
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
		if err := EnsureNoXiaohongshuRefundHoldTx(tx, &order); err != nil {
			return err
		}
		if err := ensureXiaohongshuTicketAdmissionTx(tx, &order, &ticket, reservationID, deviceID, checkPointID, deviceRequestID, prepareOnly); err != nil {
			return err
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
		if !groupAllowsCheckpointForTicket(records, &product, &ticket.OrderItem, matchedGroup, checkPointID) {
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
	if ruleItem == nil {
		return 0
	}
	return ruleItem.MaxPerCheckIn * admissionUnits(product, item)
}

// admissionUnits is the number of ticket entitlements represented by this
// QR code. An order-code QR pools all quantity units; a ticket-code QR always
// represents one unit, even when its order item has a larger quantity.
func admissionUnits(product *model.Product, item *model.OrderItem) int {
	if product != nil && item != nil && product.CodeMode == "order" && item.Quantity > 1 {
		return item.Quantity
	}
	return 1
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

func remainingAdmissionsAtCheckpoint(records []model.CheckInRecord, product *model.Product, orderItem *model.OrderItem, group *model.RuleGroup, ruleItem *model.RuleItem, checkpointID uint) int {
	limit := admissionLimit(product, orderItem, ruleItem)
	remaining := 0
	simulated := append([]model.CheckInRecord(nil), records...)
	for countAtCheckpoint(simulated, checkpointID) < limit && groupAllowsCheckpointForTicket(simulated, product, orderItem, group, checkpointID) {
		simulated = append(simulated, model.CheckInRecord{CheckPointID: checkpointID})
		remaining++
	}
	return remaining
}

func groupAllowsCheckpoint(records []model.CheckInRecord, group *model.RuleGroup, checkpointID uint) bool {
	if group == nil {
		return false
	}
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

func groupAllowsCheckpointForTicket(records []model.CheckInRecord, product *model.Product, item *model.OrderItem, group *model.RuleGroup, checkpointID uint) bool {
	if product == nil || product.Rule.AdmissionPolicy != ticketAdmissionPolicyPooledV1 {
		return groupAllowsCheckpoint(records, group, checkpointID)
	}
	return groupAllowsCheckpointWithCapacity(records, group, checkpointID, admissionUnits(product, item))
}

// groupAllowsCheckpointWithCapacity evaluates the group rule after the
// requested checkpoint admission. Each checkpoint consumes ceil(c_j/r_j)
// selections, so repeated use of a point with a larger per-point allowance
// does not consume a fresh group selection until that allowance is crossed.
func groupAllowsCheckpointWithCapacity(records []model.CheckInRecord, group *model.RuleGroup, checkpointID uint, capacity int) bool {
	if group == nil {
		return false
	}
	if group.MaxTotalCheckIn <= 0 {
		return true
	}
	if capacity < 1 {
		capacity = 1
	}
	maxUsage := int64(group.MaxTotalCheckIn) * int64(capacity)
	var usage int64
	for _, item := range group.Items {
		if item.MaxPerCheckIn <= 0 {
			return false
		}
		count := countAtCheckpoint(records, item.CheckPointID)
		if item.CheckPointID == checkpointID {
			count++
		}
		usage += int64(ceilAdmissionSelections(count, item.MaxPerCheckIn))
		if usage > maxUsage {
			return false
		}
	}
	return true
}

func ceilAdmissionSelections(count, perCheckpoint int) int {
	if count <= 0 {
		return 0
	}
	if perCheckpoint <= 0 {
		return count
	}
	return (count + perCheckpoint - 1) / perCheckpoint
}

func hasRemainingAdmission(product *model.Product, orderItem *model.OrderItem, records []model.CheckInRecord) bool {
	for groupIndex := range product.Rule.Groups {
		group := &product.Rule.Groups[groupIndex]
		for itemIndex := range group.Items {
			item := &group.Items[itemIndex]
			if groupAllowsCheckpointForTicket(records, product, orderItem, group, item.CheckPointID) &&
				countAtCheckpoint(records, item.CheckPointID) < admissionLimit(product, orderItem, item) {
				return true
			}
		}
	}
	return false
}
