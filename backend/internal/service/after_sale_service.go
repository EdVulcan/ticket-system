package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var validAfterSaleTypes = map[string]bool{
	"refund": true, "reschedule": true, "exchange": true, "void": true, "reissue": true,
}

type AfterSaleService struct {
	RefundService *RefundService
}

func generateAfterSaleNo() string {
	return fmt.Sprintf("AS%d", time.Now().UnixNano())
}

func (s *AfterSaleService) Create(req *model.AfterSaleRequest, ticketCodes []string) error {
	if req == nil || req.TenantID == 0 || strings.TrimSpace(req.OrderNo) == "" {
		return errors.New("tenant and order are required")
	}
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if !validAfterSaleTypes[req.Type] {
		return fmt.Errorf("unsupported after-sale type %s", req.Type)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return errors.New("idempotency key is required")
	}
	codes := normalizeTicketCodes(ticketCodes)
	if len(codes) == 0 && req.Type != "void" {
		return errors.New("at least one ticket code is required")
	}
	encoded, err := json.Marshal(codes)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var existing model.AfterSaleRequest
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", req.TenantID, req.IdempotencyKey).First(&existing).Error; err == nil {
			if existing.OrderNo != req.OrderNo || existing.Type != req.Type || existing.TicketCodesJSON != string(encoded) {
				return errors.New("idempotency key was already used with different after-sale data")
			}
			*req = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var order model.Order
		if err := tx.Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", req.OrderNo, req.TenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "cancelled" || order.Status == "refunded" {
			return fmt.Errorf("order cannot enter after-sale from status %s", order.Status)
		}
		if req.Type == "refund" && req.AmountCents <= 0 {
			return errors.New("refund amount is required")
		}
		if err := validateAfterSaleTickets(&order, codes, req.Type); err != nil {
			return err
		}
		req.Base = model.Base{}
		req.RequestNo = generateAfterSaleNo()
		req.Status = "pending"
		req.TicketCodesJSON = string(encoded)
		if err := tx.Create(req).Error; err != nil {
			return err
		}
		return appendAfterSaleEvent(tx, req, "", "pending", "created", req.OperatorID, req.Reason)
	})
}

func validateAfterSaleTickets(order *model.Order, codes []string, kind string) error {
	if kind == "void" && len(codes) == 0 {
		return nil
	}
	wanted := codeSet(codes)
	seen := 0
	total := 0
	for _, item := range order.Items {
		for _, ticket := range item.Tickets {
			total++
			if _, ok := wanted[ticket.TicketCode]; !ok {
				continue
			}
			seen++
			if ticket.Status != "unused" || ticket.CheckInCount != 0 {
				return fmt.Errorf("ticket %s is already used", ticket.TicketCode)
			}
		}
	}
	if seen != len(wanted) {
		return errors.New("one or more ticket codes do not belong to the order")
	}
	if kind == "void" && seen != total {
		return errors.New("partial order void is not supported; use a ticket-scoped refund")
	}
	return nil
}

func appendAfterSaleEvent(tx *gorm.DB, req *model.AfterSaleRequest, from, to, action string, actor uint, reason string) error {
	return tx.Create(&model.AfterSaleEvent{TenantID: req.TenantID, RequestNo: req.RequestNo, FromStatus: from, ToStatus: to, Action: action, ActorID: actor, Reason: strings.TrimSpace(reason)}).Error
}

func (s *AfterSaleService) Approve(tenantID, requestID, reviewerID uint, reason string) (*model.AfterSaleRequest, error) {
	return s.ApproveWithOptions(tenantID, requestID, reviewerID, reason, false, "")
}

func (s *AfterSaleService) ApproveWithOptions(tenantID, requestID, reviewerID uint, reason string, settlementException bool, exceptionReason string) (*model.AfterSaleRequest, error) {
	exceptionReason = strings.TrimSpace(exceptionReason)
	if settlementException && exceptionReason == "" {
		return nil, errors.New("settlement exception reason is required")
	}
	var req model.AfterSaleRequest
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error; err != nil {
			return err
		}
		if req.Status != "pending" {
			return fmt.Errorf("after-sale request cannot transition from %s", req.Status)
		}
		now := time.Now()
		updates := map[string]interface{}{
			"status": "approved", "reviewer_id": reviewerID, "reviewed_at": now,
			"settlement_exception_approved": settlementException, "settlement_exception_reason": exceptionReason,
		}
		if err := tx.Model(&req).Updates(updates).Error; err != nil {
			return err
		}
		req.Status, req.ReviewerID, req.ReviewedAt = "approved", reviewerID, &now
		req.SettlementExceptionApproved, req.SettlementExceptionReason = settlementException, exceptionReason
		if err := appendAfterSaleEvent(tx, &req, "pending", "approved", "approved", reviewerID, reason); err != nil {
			return err
		}
		if settlementException {
			return appendAfterSaleEvent(tx, &req, "approved", "approved", "settlement_exception", reviewerID, exceptionReason)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *AfterSaleService) Reject(tenantID, requestID, reviewerID uint, reason string) (*model.AfterSaleRequest, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("rejection reason is required")
	}
	return s.transition(tenantID, requestID, reviewerID, "rejected", "rejected", reason)
}

func (s *AfterSaleService) transition(tenantID, requestID, actor uint, target, action, reason string) (*model.AfterSaleRequest, error) {
	var req model.AfterSaleRequest
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error; err != nil {
			return err
		}
		if req.Status != "pending" {
			return fmt.Errorf("after-sale request cannot transition from %s", req.Status)
		}
		now := time.Now()
		if err := tx.Model(&req).Updates(map[string]interface{}{"status": target, "reviewer_id": actor, "reviewed_at": now}).Error; err != nil {
			return err
		}
		req.Status, req.ReviewerID, req.ReviewedAt = target, actor, &now
		return appendAfterSaleEvent(tx, &req, "pending", target, action, actor, reason)
	})
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *AfterSaleService) Execute(tenantID, requestID uint, actor uint, roles ...string) (*model.AfterSaleRequest, error) {
	var req model.AfterSaleRequest
	var refund *model.Refund
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error; err != nil {
			return err
		}
		if req.Status != "approved" {
			return fmt.Errorf("after-sale request cannot execute from %s", req.Status)
		}
		if err := tx.Model(&req).Update("status", "processing").Error; err != nil {
			return err
		}
		if err := appendAfterSaleEvent(tx, &req, "approved", "processing", "execution_started", actor, req.Reason); err != nil {
			return err
		}
		switch req.Type {
		case "reschedule":
			if err := executeRescheduleTx(tx, &req); err != nil {
				return failAfterSaleTx(tx, &req, actor, err)
			}
			return completeAfterSaleTx(tx, &req, actor)
		case "void":
			var order model.Order
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", req.OrderNo, tenantID).First(&order).Error; err != nil {
				return failAfterSaleTx(tx, &req, actor, err)
			}
			if err := cancelOrderTx(tx, &order); err != nil {
				return failAfterSaleTx(tx, &req, actor, err)
			}
			return completeAfterSaleTx(tx, &req, actor)
		case "exchange":
			difference, settlementDifference, err := calculateExchangeDifferencesTx(tx, &req)
			if err != nil {
				return failAfterSaleTx(tx, &req, actor, err)
			}
			if settlementDifference != 0 && !req.SettlementExceptionApproved {
				return failAfterSaleTx(tx, &req, actor, errors.New("exchange changes supplier settlement; supervisor exception approval is required"))
			}
			req.DifferenceCents = difference
			if difference > 0 {
				req.DifferenceStatus = "payment_required"
				if err := tx.Model(&req).Updates(map[string]interface{}{"difference_cents": difference, "difference_status": req.DifferenceStatus}).Error; err != nil {
					return err
				}
				return appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_payment_required", actor, fmt.Sprintf("collect %d cents before exchange", difference))
			}
			if difference < 0 {
				req.DifferenceStatus = "refund_pending"
			}
			if err := tx.Model(&req).Updates(map[string]interface{}{"difference_cents": difference, "difference_status": req.DifferenceStatus}).Error; err != nil {
				return err
			}
			if err := executeExchangeTx(tx, &req); err != nil {
				return failAfterSaleTx(tx, &req, actor, err)
			}
			if difference < 0 {
				refund, err := createExchangeDifferenceRefundTx(tx, &req, -difference)
				if err != nil {
					return failAfterSaleTx(tx, &req, actor, err)
				}
				req.DifferenceRefundID = refund.ID
				if err := tx.Model(&req).Update("difference_refund_id", refund.ID).Error; err != nil {
					return err
				}
				if refund.Status != "group_succeeded" {
					return appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_refund_pending", actor, fmt.Sprintf("refund %d cents", -difference))
				}
				req.DifferenceStatus = "settled"
				if err := tx.Model(&req).Update("difference_status", "settled").Error; err != nil {
					return err
				}
			}
			return completeAfterSaleTx(tx, &req, actor)
		case "reissue":
			return executeReissueTx(tx, &req, actor, firstRole(roles))
		case "refund":
			// Refund provider calls happen after this transaction. Keep the request
			// in processing and link the resulting durable refund below.
			return nil
		default:
			return failAfterSaleTx(tx, &req, actor, errors.New("unsupported after-sale type"))
		}
	})
	if err != nil {
		return nil, err
	}
	if req.Type == "refund" {
		refund, err = s.executeRefund(&req)
		if err != nil {
			_ = model.Write(func(tx *gorm.DB) error { return failAfterSaleTx(tx, &req, actor, err) })
			return nil, err
		}
		if err := model.Write(func(tx *gorm.DB) error {
			if err := tx.Model(&req).Updates(map[string]interface{}{"refund_id": refund.ID}).Error; err != nil {
				return err
			}
			var storedRefund model.Refund
			if err := tx.Where("id = ? AND tenant_id = ?", refund.ID, tenantID).First(&storedRefund).Error; err != nil {
				return err
			}
			if storedRefund.Status == "succeeded" || storedRefund.Status == "group_succeeded" {
				return completeAfterSaleTx(tx, &req, actor)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return s.Get(tenantID, requestID)
}

type exchangeDifferences struct {
	RetailCents     int64
	SettlementCents int64
}

func calculateExchangeDifferencesTx(tx *gorm.DB, req *model.AfterSaleRequest) (int64, int64, error) {
	if req.TargetProductID == 0 {
		return 0, 0, errors.New("target product is required")
	}
	var order model.Order
	if err := tx.Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", req.OrderNo, req.TenantID).First(&order).Error; err != nil {
		return 0, 0, err
	}
	var codes []string
	if err := json.Unmarshal([]byte(req.TicketCodesJSON), &codes); err != nil {
		return 0, 0, err
	}
	wanted := codeSet(codes)
	var targetListing model.Product
	if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ? AND status = ?", req.TargetProductID, req.TenantID, "online").First(&targetListing).Error; err != nil {
		return 0, 0, errors.New("target product is unavailable")
	}
	target, _, err := resolveFulfillmentProduct(tx, &targetListing, req.TenantID, order.Channel)
	if err != nil {
		return 0, 0, err
	}
	var differences exchangeDifferences
	matched := 0
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		selected := 0
		for _, ticket := range item.Tickets {
			if _, ok := wanted[ticket.TicketCode]; ok {
				selected++
			}
		}
		if selected == 0 {
			continue
		}
		matched += selected
		if selected != len(item.Tickets) && len(item.Tickets) != item.Quantity {
			return 0, 0, errors.New("partial operation requires one ticket code per visitor")
		}
		if item.FulfillmentTenantID != 0 && item.FulfillmentTenantID != target.TenantID {
			return 0, 0, errors.New("exchange must stay with the same supplier")
		}
		if item.FulfillmentScenicAreaID != 0 && item.FulfillmentScenicAreaID != target.ScenicAreaID {
			return 0, 0, errors.New("exchange must stay within the same scenic area")
		}
		differences.RetailCents += (moneyCents(targetListing.Price) - moneyCents(item.Price)) * int64(selected)
		settlementDelta := (moneyCents(target.SettlementPrice) - moneyCents(item.SettlementPrice)) * int64(selected)
		differences.SettlementCents += settlementDelta
		if settlementDelta != 0 && item.FulfillmentOrderID > 0 {
			var statementLines int64
			if err := tx.Model(&model.SettlementLine{}).Where("fulfillment_order_id = ?", item.FulfillmentOrderID).Count(&statementLines).Error; err != nil {
				return 0, 0, err
			}
			if statementLines > 0 {
				return 0, 0, errors.New("exchange cannot change a fulfillment already included in a settlement statement")
			}
		}
	}
	if matched != len(wanted) {
		return 0, 0, errors.New("one or more exchange tickets do not belong to the order")
	}
	return differences.RetailCents, differences.SettlementCents, nil
}

// executeExchangeTx keeps the seller, supplier, scenic area, retail price and
// settlement price unchanged, so the original payment and settlement facts
// remain balanced. Ticket-level items may be split for a partial exchange.
func executeExchangeTx(tx *gorm.DB, req *model.AfterSaleRequest) error {
	if req.TargetProductID == 0 {
		return errors.New("target product is required")
	}
	var order model.Order
	if err := tx.Preload("Items.Product").Preload("Items.Tickets").
		Where("order_no = ? AND tenant_id = ?", req.OrderNo, req.TenantID).First(&order).Error; err != nil {
		return err
	}
	if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
		return fmt.Errorf("order cannot be exchanged from status %s", order.Status)
	}
	var codes []string
	if err := json.Unmarshal([]byte(req.TicketCodesJSON), &codes); err != nil {
		return err
	}
	wanted := codeSet(codes)
	if len(wanted) == 0 {
		return errors.New("exchange requires ticket codes")
	}

	var targetListing model.Product
	if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").
		Where("id = ? AND tenant_id = ? AND status = ?", req.TargetProductID, req.TenantID, "online").First(&targetListing).Error; err != nil {
		return errors.New("target product is unavailable")
	}
	target, _, err := resolveFulfillmentProduct(tx, &targetListing, req.TenantID, order.Channel)
	if err != nil {
		return err
	}
	if target.CurrentRevisionID == 0 {
		if _, err := ensureProductRevisionTx(tx, target); err != nil {
			return err
		}
	}
	if target.ScenicAreaID == 0 {
		return errors.New("target product has no scenic area")
	}
	targetDate := req.TargetDate
	if targetDate == nil {
		for _, item := range order.Items {
			for _, ticket := range item.Tickets {
				if _, ok := wanted[ticket.TicketCode]; ok {
					targetDate = item.UseDate
					break
				}
			}
			if targetDate != nil {
				break
			}
		}
	}
	if targetDate == nil {
		return errors.New("exchange requires a visit date")
	}

	matched := 0
	retailDifference := int64(0)
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		selected := 0
		for _, ticket := range item.Tickets {
			if _, ok := wanted[ticket.TicketCode]; ok {
				selected++
				if ticket.Status != "unused" || ticket.CheckInCount != 0 {
					return fmt.Errorf("ticket %s is already used", ticket.TicketCode)
				}
			}
		}
		if selected == 0 {
			continue
		}
		matched += selected
		retailDifference += (moneyCents(targetListing.Price) - moneyCents(item.Price)) * int64(selected)
		if item.FulfillmentTenantID != 0 && item.FulfillmentTenantID != target.TenantID {
			return errors.New("exchange must stay with the same supplier")
		}
		if item.FulfillmentScenicAreaID != 0 && item.FulfillmentScenicAreaID != target.ScenicAreaID {
			return errors.New("exchange must stay within the same scenic area")
		}
		if !isVisitDateValid(targetDate, target.ValidityStartDate, target.ValidityEndDate) {
			return errors.New("target product is not valid on the requested date")
		}
		stockSlot := req.TargetSlot
		if strings.TrimSpace(stockSlot) == "" {
			stockSlot = item.StockSlot
		}
		probe := &model.OrderItem{UseDate: targetDate, StockSlot: stockSlot}
		if err := validateTimeSlot(target.TimeSlotConfig, probe); err != nil {
			return err
		}
		selectedItem, err := splitOrderItemForTicketsTx(tx, item, wanted)
		if err != nil {
			return err
		}
		var oldListing model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", selectedItem.ProductID, req.TenantID).First(&oldListing).Error; err != nil {
			return err
		}
		oldProduct, _, err := loadStoredFulfillmentProduct(tx, &oldListing, selectedItem, req.TenantID)
		if err != nil {
			return err
		}
		if err := releaseStock(tx, oldProduct, selectedItem.UseDate, selectedItem.StockSlot, selectedItem.Quantity); err != nil {
			return err
		}
		if err := reserveStock(tx, target, targetDate, stockSlot, selectedItem.Quantity); err != nil {
			return err
		}
		if err := releaseOfferQuotaTx(tx, selectedItem.ProductOfferID, selectedItem.OfferReservedQuantity); err != nil {
			return err
		}
		if err := reserveOfferQuotaTx(tx, targetListing.ProductOfferID, selectedItem.Quantity); err != nil {
			return err
		}
		ruleSnapshot, err := json.Marshal(target.Rule)
		if err != nil {
			return err
		}
		updates := map[string]interface{}{
			"product_id": req.TargetProductID, "product_name": targetListing.Name,
			"price": targetListing.Price, "settlement_price": target.SettlementPrice,
			"use_date": targetDate, "stock_slot": stockSlot,
			"validity_type": target.ValidityType, "validity_start": target.ValidityStartDate,
			"validity_end": target.ValidityEndDate, "fulfillment_product_id": target.ID,
			"fulfillment_tenant_id": target.TenantID, "fulfillment_scenic_area_id": target.ScenicAreaID,
			"product_offer_id": targetListing.ProductOfferID, "product_revision_id": target.CurrentRevisionID,
			"offer_reserved_quantity": selectedItem.Quantity,
		}
		if targetListing.ProductOfferID == 0 {
			updates["offer_reserved_quantity"] = 0
		}
		if err := tx.Model(&model.OrderItem{}).Where("id = ?", selectedItem.ID).Updates(updates).Error; err != nil {
			return err
		}
		settlementDelta := (moneyCents(target.SettlementPrice) - moneyCents(selectedItem.SettlementPrice)) * int64(selectedItem.Quantity)
		if settlementDelta != 0 && selectedItem.FulfillmentOrderID > 0 {
			if err := tx.Model(&model.FulfillmentOrder{}).Where("id = ?", selectedItem.FulfillmentOrderID).UpdateColumn("settlement_amount", gorm.Expr("settlement_amount + ?", centsMoney(settlementDelta))).Error; err != nil {
				return err
			}
		}
		for ticketIndex := range selectedItem.Tickets {
			ticket := &selectedItem.Tickets[ticketIndex]
			if err := tx.Model(ticket).Updates(map[string]interface{}{
				"fulfillment_product_id": target.ID, "fulfillment_tenant_id": target.TenantID,
				"scenic_area_id": target.ScenicAreaID, "fulfillment_scenic_area_id": target.ScenicAreaID,
				"product_revision_id": target.CurrentRevisionID, "rule_snapshot": string(ruleSnapshot),
				"code_mode": target.CodeMode,
			}).Error; err != nil {
				return err
			}
		}
		if selectedItem.FulfillmentOrderID > 0 {
			var itemCount int64
			if err := tx.Model(&model.OrderItem{}).Where("fulfillment_order_id = ?", selectedItem.FulfillmentOrderID).Count(&itemCount).Error; err != nil {
				return err
			}
			if itemCount == 1 {
				if err := tx.Model(&model.FulfillmentOrder{}).Where("id = ?", selectedItem.FulfillmentOrderID).Updates(map[string]interface{}{"product_revision_id": target.CurrentRevisionID, "scenic_area_id": target.ScenicAreaID}).Error; err != nil {
					return err
				}
			}
		}
	}
	if matched != len(wanted) {
		return errors.New("one or more exchange tickets do not belong to the order")
	}
	if retailDifference != req.DifferenceCents {
		return errors.New("exchange price difference changed; review and retry the request")
	}
	if retailDifference != 0 {
		newTotalCents := moneyCents(order.TotalAmount) + retailDifference
		if newTotalCents < 0 {
			return errors.New("exchange would make the order total negative")
		}
		if err := tx.Model(&order).Update("total_amount", centsMoney(newTotalCents)).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *AfterSaleService) executeRefund(req *model.AfterSaleRequest) (*model.Refund, error) {
	if s.RefundService == nil {
		s.RefundService = &RefundService{PaymentService: &PaymentService{OrderService: &OrderService{}}}
	}
	var codes []string
	if err := json.Unmarshal([]byte(req.TicketCodesJSON), &codes); err != nil {
		return nil, err
	}
	amount := float64(req.AmountCents) / 100
	method := req.PaymentMethod
	if method == "" {
		method = "auto"
	}
	if method == "auto" || method == "mixed" {
		return s.RefundService.CreateMixedRefund(req.TenantID, req.OrderNo, "after-sale:"+req.IdempotencyKey, amount, codes, req.Reason)
	}
	if method == "cash" {
		return s.RefundService.CreateCashRefund(req.TenantID, req.OrderNo, "after-sale:"+req.IdempotencyKey, amount, codes, req.Reason)
	}
	if method == "wechat" || method == "alipay" {
		return s.RefundService.CreateDigitalRefund(req.TenantID, req.OrderNo, "after-sale:"+req.IdempotencyKey, amount, codes, req.Reason)
	}
	return nil, fmt.Errorf("unsupported refund method %s", method)
}

func executeRescheduleTx(tx *gorm.DB, req *model.AfterSaleRequest) error {
	if req.TargetDate == nil {
		return errors.New("target date is required")
	}
	var order model.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", req.OrderNo, req.TenantID).First(&order).Error; err != nil {
		return err
	}
	var codes []string
	if err := json.Unmarshal([]byte(req.TicketCodesJSON), &codes); err != nil {
		return err
	}
	wanted := codeSet(codes)
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		selected := 0
		for _, ticket := range item.Tickets {
			if _, ok := wanted[ticket.TicketCode]; ok {
				selected++
			}
		}
		if selected == 0 {
			continue
		}
		selectedItem, err := splitOrderItemForTicketsTx(tx, item, wanted)
		if err != nil {
			return err
		}
		var product model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", selectedItem.FulfillmentProductID, selectedItem.FulfillmentTenantID).First(&product).Error; err != nil {
			return err
		}
		if !isVisitDateValid(req.TargetDate, product.ValidityStartDate, product.ValidityEndDate) {
			return errors.New("target date is outside product validity")
		}
		stockSlot := req.TargetSlot
		if strings.TrimSpace(stockSlot) == "" {
			stockSlot = selectedItem.StockSlot
		}
		if err := validateTimeSlot(product.TimeSlotConfig, &model.OrderItem{UseDate: req.TargetDate, StockSlot: stockSlot}); err != nil {
			return err
		}
		if err := releaseStock(tx, &product, selectedItem.UseDate, selectedItem.StockSlot, selectedItem.Quantity); err != nil {
			return err
		}
		selectedItem.UseDate = req.TargetDate
		selectedItem.StockSlot = stockSlot
		if err := reserveStock(tx, &product, selectedItem.UseDate, selectedItem.StockSlot, selectedItem.Quantity); err != nil {
			return err
		}
		if err := tx.Model(selectedItem).Updates(map[string]interface{}{"use_date": selectedItem.UseDate, "stock_slot": selectedItem.StockSlot}).Error; err != nil {
			return err
		}
	}
	return nil
}

// splitOrderItemForTicketsTx creates a ticket-scoped item while preserving the
// exact money and quota totals of the original item. Aggregated order-code
// tickets cannot be split because there is no reliable per-visitor allocation.
func splitOrderItemForTicketsTx(tx *gorm.DB, item *model.OrderItem, wanted map[string]struct{}) (*model.OrderItem, error) {
	selectedTickets := make([]model.Ticket, 0, len(item.Tickets))
	for _, ticket := range item.Tickets {
		if _, ok := wanted[ticket.TicketCode]; !ok {
			continue
		}
		if ticket.Status != "unused" || ticket.CheckInCount != 0 {
			return nil, fmt.Errorf("ticket %s is already used", ticket.TicketCode)
		}
		selectedTickets = append(selectedTickets, ticket)
	}
	selectedQuantity := len(selectedTickets)
	if selectedQuantity == 0 {
		return nil, errors.New("no tickets selected for item split")
	}
	if selectedQuantity == len(item.Tickets) {
		return item, nil
	}
	if len(item.Tickets) != item.Quantity {
		return nil, errors.New("partial operation requires one ticket code per visitor")
	}

	originalQuantity := item.Quantity
	selectedCash := item.CashCostCents * int64(selectedQuantity) / int64(originalQuantity)
	selectedCredit := item.CreditCostCents * int64(selectedQuantity) / int64(originalQuantity)
	selectedOffer := item.OfferReservedQuantity * selectedQuantity / originalQuantity

	selectedItem := *item
	selectedItem.Base = model.Base{}
	selectedItem.Product = model.Product{}
	selectedItem.Quantity = selectedQuantity
	selectedItem.CashCostCents = selectedCash
	selectedItem.CreditCostCents = selectedCredit
	selectedItem.OfferReservedQuantity = selectedOffer
	selectedItem.Tickets = nil
	selectedItem.Visitors = nil
	selectedItem.VisitorRecords = nil
	if err := tx.Omit(clause.Associations).Create(&selectedItem).Error; err != nil {
		return nil, err
	}

	remaining := map[string]interface{}{
		"quantity":                originalQuantity - selectedQuantity,
		"cash_cost_cents":         item.CashCostCents - selectedCash,
		"credit_cost_cents":       item.CreditCostCents - selectedCredit,
		"offer_reserved_quantity": item.OfferReservedQuantity - selectedOffer,
	}
	if err := tx.Model(&model.OrderItem{}).Where("id = ?", item.ID).Updates(remaining).Error; err != nil {
		return nil, err
	}

	ticketIDs := make([]uint, 0, selectedQuantity)
	for index := range selectedTickets {
		ticketIDs = append(ticketIDs, selectedTickets[index].ID)
		selectedTickets[index].OrderItemID = selectedItem.ID
	}
	if err := tx.Model(&model.Ticket{}).Where("id IN ?", ticketIDs).Update("order_item_id", selectedItem.ID).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&model.OrderVisitor{}).Where("ticket_id IN ?", ticketIDs).Update("order_item_id", selectedItem.ID).Error; err != nil {
		return nil, err
	}
	selectedItem.Tickets = selectedTickets
	return &selectedItem, nil
}

func executeReissueTx(tx *gorm.DB, req *model.AfterSaleRequest, actor uint, role string) error {
	if req.DeviceID == 0 || req.ShiftID == 0 {
		return errors.New("reissue requires an active POS device and shift")
	}
	var device model.Device
	if err := tx.Where("id = ? AND tenant_id = ? AND type = ? AND status = ?", req.DeviceID, req.TenantID, "pos", "online").First(&device).Error; err != nil {
		return errors.New("reissue device is unavailable")
	}
	var shift model.POSShift
	if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND status = ?", req.ShiftID, req.TenantID, req.DeviceID, "open").First(&shift).Error; err != nil {
		return errors.New("reissue shift is not open for this device")
	}
	isSupervisor := role == "admin" || role == "super_admin"
	if shift.OperatorID != actor && !isSupervisor {
		return errors.New("reissue shift is not open for this operator and device")
	}
	if shift.OperatorID != actor && strings.TrimSpace(req.Reason) == "" {
		return errors.New("supervisor proxy reissue requires a reason")
	}
	var order model.Order
	if err := tx.Where("order_no = ? AND tenant_id = ?", req.OrderNo, req.TenantID).First(&order).Error; err != nil {
		return err
	}
	if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
		return fmt.Errorf("order cannot be reissued from status %s", order.Status)
	}
	var codes []string
	if err := json.Unmarshal([]byte(req.TicketCodesJSON), &codes); err != nil {
		return err
	}
	for _, code := range codes {
		if err := tx.Create(&model.PrintJob{TenantID: req.TenantID, DeviceID: req.DeviceID, OperatorID: shift.OperatorID, ShiftID: req.ShiftID, OrderNo: req.OrderNo, TicketCode: code, AfterSaleRequestNo: req.RequestNo, Status: "queued"}).Error; err != nil {
			return err
		}
	}
	if shift.OperatorID != actor {
		if err := appendAfterSaleEvent(tx, req, "processing", "processing", "proxy_print_queued", actor, fmt.Sprintf("%s; shift operator=%d", strings.TrimSpace(req.Reason), shift.OperatorID)); err != nil {
			return err
		}
	}
	return appendAfterSaleEvent(tx, req, "processing", "processing", "print_queued", actor, req.Reason)
}

func firstRole(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	return strings.TrimSpace(roles[0])
}

func completeAfterSaleTx(tx *gorm.DB, req *model.AfterSaleRequest, actor uint) error {
	now := time.Now()
	if err := tx.Model(req).Updates(map[string]interface{}{"status": "completed", "completed_at": now, "error_message": ""}).Error; err != nil {
		return err
	}
	req.Status, req.CompletedAt = "completed", &now
	return appendAfterSaleEvent(tx, req, "processing", "completed", "execution_completed", actor, req.Reason)
}

func failAfterSaleTx(tx *gorm.DB, req *model.AfterSaleRequest, actor uint, cause error) error {
	message := cause.Error()
	if err := tx.Model(req).Updates(map[string]interface{}{"status": "failed", "error_message": message}).Error; err != nil {
		return err
	}
	req.Status, req.ErrorMessage = "failed", message
	return appendAfterSaleEvent(tx, req, "processing", "failed", "execution_failed", actor, message)
}

func (s *AfterSaleService) Get(tenantID, requestID uint) (*model.AfterSaleRequest, error) {
	var req model.AfterSaleRequest
	err := model.DB.Preload("Events", func(db *gorm.DB) *gorm.DB { return db.Order("id ASC") }).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error
	return &req, err
}

func (s *AfterSaleService) List(tenantID uint, status, orderNo string, page, pageSize int) ([]model.AfterSaleRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.AfterSaleRequest{}).Where("tenant_id = ?", tenantID)
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", status)
	}
	if strings.TrimSpace(orderNo) != "" {
		query = query.Where("order_no = ?", strings.TrimSpace(orderNo))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.AfterSaleRequest
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// ReconcileRefunds closes after-sale requests after the durable refund worker
// reaches a terminal provider state. It is safe to run on every worker tick.
func (s *AfterSaleService) ReconcileRefunds() error {
	return model.Write(func(tx *gorm.DB) error {
		var requests []model.AfterSaleRequest
		if err := tx.Where("type = ? AND status = ? AND refund_id > 0", "refund", "processing").Find(&requests).Error; err != nil {
			return err
		}
		for i := range requests {
			var refund model.Refund
			if err := tx.Where("id = ? AND tenant_id = ?", requests[i].RefundID, requests[i].TenantID).First(&refund).Error; err != nil {
				continue
			}
			switch refund.Status {
			case "succeeded":
				if err := completeAfterSaleTx(tx, &requests[i], requests[i].OperatorID); err != nil {
					return err
				}
			case "failed":
				if err := failAfterSaleTx(tx, &requests[i], requests[i].OperatorID, errors.New(refund.Reason)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
