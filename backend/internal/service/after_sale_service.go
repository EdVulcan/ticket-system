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
	for _, item := range order.Items {
		for _, ticket := range item.Tickets {
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
	return nil
}

func appendAfterSaleEvent(tx *gorm.DB, req *model.AfterSaleRequest, from, to, action string, actor uint, reason string) error {
	return tx.Create(&model.AfterSaleEvent{TenantID: req.TenantID, RequestNo: req.RequestNo, FromStatus: from, ToStatus: to, Action: action, ActorID: actor, Reason: strings.TrimSpace(reason)}).Error
}

func (s *AfterSaleService) Approve(tenantID, requestID, reviewerID uint, reason string) (*model.AfterSaleRequest, error) {
	return s.transition(tenantID, requestID, reviewerID, "approved", "approved", reason)
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

func (s *AfterSaleService) Execute(tenantID, requestID uint, actor uint) (*model.AfterSaleRequest, error) {
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
			return failAfterSaleTx(tx, &req, actor, errors.New("exchange requires a supplier-approved target product workflow"))
		case "reissue":
			return executeReissueTx(tx, &req, actor)
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
			if refund.Status == "succeeded" {
				return completeAfterSaleTx(tx, &req, actor)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return s.Get(tenantID, requestID)
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
		method = "cash"
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
		selected := false
		for _, ticket := range item.Tickets {
			if _, ok := wanted[ticket.TicketCode]; ok {
				selected = true
			}
		}
		if !selected {
			continue
		}
		var product model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.FulfillmentProductID, item.FulfillmentTenantID).First(&product).Error; err != nil {
			return err
		}
		if !isVisitDateValid(req.TargetDate, product.ValidityStartDate, product.ValidityEndDate) {
			return errors.New("target date is outside product validity")
		}
		if err := validateTimeSlot(product.TimeSlotConfig, &model.OrderItem{UseDate: req.TargetDate, StockSlot: req.TargetSlot}); err != nil {
			return err
		}
		if err := releaseStock(tx, &product, item.UseDate, item.StockSlot, item.Quantity); err != nil {
			return err
		}
		item.UseDate = req.TargetDate
		item.StockSlot = req.TargetSlot
		if err := reserveStock(tx, &product, item.UseDate, item.StockSlot, item.Quantity); err != nil {
			return err
		}
		if err := tx.Model(item).Updates(map[string]interface{}{"use_date": item.UseDate, "stock_slot": item.StockSlot}).Error; err != nil {
			return err
		}
	}
	return nil
}

func executeReissueTx(tx *gorm.DB, req *model.AfterSaleRequest, actor uint) error {
	if req.DeviceID == 0 || req.ShiftID == 0 {
		return errors.New("reissue requires an active POS device and shift")
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
		if err := tx.Create(&model.PrintJob{TenantID: req.TenantID, DeviceID: req.DeviceID, OperatorID: actor, ShiftID: req.ShiftID, OrderNo: req.OrderNo, TicketCode: code, AfterSaleRequestNo: req.RequestNo, Status: "queued"}).Error; err != nil {
			return err
		}
	}
	return appendAfterSaleEvent(tx, req, "processing", "processing", "print_queued", actor, req.Reason)
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
	err := model.DB.Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error
	return &req, err
}

func (s *AfterSaleService) List(tenantID uint, status string, page, pageSize int) ([]model.AfterSaleRequest, int64, error) {
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
