package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"github.com/go-pay/gopay"
	"github.com/go-pay/gopay/wechat/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const staleDigitalRefundTaskAfter = 5 * time.Minute

var ErrDigitalRefundNotConfigured = errors.New("digital provider refund integration is not configured")

const defaultDigitalRefundMaxAttempts = 8

type RefundProviderResult struct {
	Status           string
	ProviderRefundID string
}

type RefundProvider interface {
	Process(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error)
}

type RefundService struct {
	PaymentService *PaymentService
	Provider       RefundProvider
}

type RefundGroupView struct {
	Root        model.Refund              `json:"root"`
	Allocations []model.Refund            `json:"allocations"`
	Tasks       []model.DigitalRefundTask `json:"tasks"`
}

// RefundActor is resolved from the authenticated request. A zero actor keeps
// the historical service API fail-closed for tests, jobs and internal callers.
type RefundActor struct {
	TenantID           uint
	UserID             uint
	OrderTenantID      uint
	FulfillmentOrderID uint
}

func refundOrderTenantID(actor RefundActor) uint {
	if actor.OrderTenantID != 0 {
		return actor.OrderTenantID
	}
	return actor.TenantID
}

func ensureRefundableEnvironment(order *model.Order) error {
	if order != nil && order.Environment == "sandbox" {
		return errors.New("sandbox orders must be cancelled through the channel workflow")
	}
	return nil
}

func (s *RefundService) GetRefundGroup(tenantID, refundID uint) (*RefundGroupView, error) {
	if tenantID == 0 || refundID == 0 {
		return nil, errors.New("tenant and refund are required")
	}
	var root model.Refund
	if err := model.DB.Where("id = ? AND tenant_id = ?", refundID, tenantID).First(&root).Error; err != nil {
		return nil, err
	}
	if root.ParentRefundID != 0 {
		if err := model.DB.Where("id = ? AND tenant_id = ?", root.ParentRefundID, tenantID).First(&root).Error; err != nil {
			return nil, err
		}
	}
	var allocations []model.Refund
	if err := model.DB.Where("tenant_id = ? AND parent_refund_id = ?", tenantID, root.ID).Order("allocation_seq ASC").Find(&allocations).Error; err != nil {
		return nil, err
	}
	if len(allocations) == 0 {
		allocations = []model.Refund{root}
	}
	allocationIDs := make([]uint, 0, len(allocations))
	for i := range allocations {
		allocationIDs = append(allocationIDs, allocations[i].ID)
	}
	var tasks []model.DigitalRefundTask
	if err := model.DB.Where("tenant_id = ? AND refund_id IN ?", tenantID, allocationIDs).Order("id ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return &RefundGroupView{Root: root, Allocations: allocations, Tasks: tasks}, nil
}

func generateRefundNo() string {
	bytes := make([]byte, 5)
	if _, err := rand.Read(bytes); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("REF%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(bytes)))
}

// CreateCashRefund executes a cashier refund for selected unused ticket
// entitlements. Digital payments intentionally return an explicit integration
// error until the provider refund API and reconciliation job are wired.
func (s *RefundService) CreateCashRefund(tenantID uint, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	return s.CreateCashRefundAs(RefundActor{TenantID: tenantID}, orderNo, idempotencyKey, amount, ticketCodes, reason)
}

func (s *RefundService) CreateCashRefundAs(actor RefundActor, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	tenantID := refundOrderTenantID(actor)
	if actor.TenantID == 0 || tenantID == 0 || strings.TrimSpace(orderNo) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("tenant, order and idempotency key are required")
	}
	if amount <= 0 {
		return nil, errors.New("refund amount must be greater than zero")
	}
	amountCents := moneyCents(amount)
	cleanCodes := normalizeTicketCodes(ticketCodes)
	if len(cleanCodes) == 0 {
		return nil, errors.New("at least one ticket code is required")
	}
	codesJSON, err := json.Marshal(cleanCodes)
	if err != nil {
		return nil, err
	}
	var result model.Refund
	err = model.Write(func(tx *gorm.DB) error {
		var existing model.Refund
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			existingAmountCents := existing.AmountCents
			if existingAmountCents == 0 {
				existingAmountCents = moneyCents(existing.Amount)
			}
			if existing.OrderNo != orderNo || existingAmountCents != amountCents || existing.TicketCodesJSON != string(codesJSON) {
				return errors.New("idempotency key was already used with different refund data")
			}
			result = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var payment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"tenant_id = ? AND order_no = ? AND method = ? AND status IN ?", tenantID, orderNo, "cash", []string{"paid", "partial_refunded"},
		).Order("created_at asc").First(&payment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("paid cash payment not found")
			}
			return err
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := ensureRefundableEnvironment(&order); err != nil {
			return err
		}
		if order.Status != "paid" && order.Status != "partial_refunded" && order.Status != "completed" {
			return fmt.Errorf("order cannot be refunded from status %s", order.Status)
		}

		allowUsed, err := authorizeUsedTicketRefundTx(tx, actor, &order, cleanCodes)
		if err != nil {
			return err
		}
		selected, refundableAmount, err := selectRefundTickets(&order, cleanCodes, allowUsed, 0)
		if err != nil {
			return err
		}
		if roundMoney(amount) != refundableAmount {
			return fmt.Errorf("refund amount must equal selected ticket value %.2f", refundableAmount)
		}
		paymentAmountCents := payment.AmountCents
		if paymentAmountCents == 0 {
			paymentAmountCents = moneyCents(payment.Amount)
		}
		paymentRefundedCents := payment.RefundedAmountCents
		if paymentRefundedCents == 0 {
			paymentRefundedCents = moneyCents(payment.RefundedAmount)
		}
		if paymentRefundedCents+amountCents > paymentAmountCents {
			return errors.New("refund amount exceeds paid amount")
		}

		result = model.Refund{
			TenantID: tenantID, RefundNo: generateRefundNo(), IdempotencyKey: idempotencyKey,
			OrderNo: orderNo, PaymentID: payment.ID, Amount: roundMoney(amount), AmountCents: moneyCents(amount), Method: "cash",
			Status: "succeeded", Reason: strings.TrimSpace(reason), TicketCodesJSON: string(codesJSON),
			AuthorizedUsedRefund: allowUsed, AuthorizedBy: actor.UserID,
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return applySuccessfulRefundTx(tx, &order, &payment, &result, selected)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func allocatedRefundCents(totalCents int64, previouslyRefunded, selected, totalUnits int) int64 {
	if totalCents <= 0 || selected <= 0 || totalUnits <= 0 {
		return 0
	}
	before := totalCents * int64(previouslyRefunded) / int64(totalUnits)
	afterCount := previouslyRefunded + selected
	if afterCount > totalUnits {
		afterCount = totalUnits
	}
	after := totalCents * int64(afterCount) / int64(totalUnits)
	return after - before
}

// CreateDigitalRefund records a provider refund request without claiming that
// the provider accepted it. The persisted pending task is the hand-off point
// for a real WeChat/Alipay refund adapter and survives process restarts.
func (s *RefundService) CreateDigitalRefund(tenantID uint, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	return s.CreateDigitalRefundAs(RefundActor{TenantID: tenantID}, orderNo, idempotencyKey, amount, ticketCodes, reason)
}

func (s *RefundService) CreateDigitalRefundAs(actor RefundActor, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	tenantID := refundOrderTenantID(actor)
	if actor.TenantID == 0 || tenantID == 0 || strings.TrimSpace(orderNo) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("tenant, order and idempotency key are required")
	}
	if amount <= 0 {
		return nil, errors.New("refund amount must be greater than zero")
	}
	amountCents := moneyCents(amount)
	cleanCodes := normalizeTicketCodes(ticketCodes)
	if len(cleanCodes) == 0 {
		return nil, errors.New("at least one ticket code is required")
	}
	codesJSON, err := json.Marshal(cleanCodes)
	if err != nil {
		return nil, err
	}
	var result model.Refund
	err = model.Write(func(tx *gorm.DB) error {
		var existing model.Refund
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			existingAmountCents := existing.AmountCents
			if existingAmountCents == 0 {
				existingAmountCents = moneyCents(existing.Amount)
			}
			if existing.OrderNo != orderNo || existingAmountCents != amountCents || existing.TicketCodesJSON != string(codesJSON) {
				return errors.New("idempotency key was already used with different refund data")
			}
			result = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var payment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"tenant_id = ? AND order_no = ? AND method IN ? AND status = ?", tenantID, orderNo, []string{"wechat", "alipay"}, "paid",
		).Order("created_at asc").First(&payment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("paid digital payment not found")
			}
			return err
		}
		var reservedRefundsCents int64
		if err := tx.Model(&model.Refund{}).Where("payment_id = ? AND status IN ?", payment.ID, []string{"pending", "succeeded"}).Select("COALESCE(SUM(CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS INTEGER) END), 0)").Scan(&reservedRefundsCents).Error; err != nil {
			return err
		}
		paymentAmountCents := payment.AmountCents
		if paymentAmountCents == 0 {
			paymentAmountCents = moneyCents(payment.Amount)
		}
		if reservedRefundsCents+amountCents > paymentAmountCents {
			return errors.New("refund amount exceeds paid amount")
		}
		var order model.Order
		if err := tx.Preload("Items.Product").Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := ensureRefundableEnvironment(&order); err != nil {
			return err
		}
		if order.Status != "paid" && order.Status != "partial_refunded" && order.Status != "completed" {
			return fmt.Errorf("order cannot be refunded from status %s", order.Status)
		}
		allowUsed, err := authorizeUsedTicketRefundTx(tx, actor, &order, cleanCodes)
		if err != nil {
			return err
		}
		selected, refundableAmount, err := selectRefundTickets(&order, cleanCodes, allowUsed, 0)
		if err != nil {
			return err
		}
		if len(selected) != len(cleanCodes) || amountCents != moneyCents(refundableAmount) {
			return fmt.Errorf("refund amount must equal selected ticket value %.2f", refundableAmount)
		}
		result = model.Refund{
			TenantID: tenantID, RefundNo: generateRefundNo(), IdempotencyKey: idempotencyKey,
			OrderNo: orderNo, PaymentID: payment.ID, Amount: roundMoney(amount), AmountCents: moneyCents(amount), Method: payment.Method,
			Status: "pending", Reason: strings.TrimSpace(reason), TicketCodesJSON: string(codesJSON),
			AuthorizedUsedRefund: allowUsed, AuthorizedBy: actor.UserID,
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := reserveRefundTicketsTx(tx, selected, result.ID); err != nil {
			return err
		}
		return tx.Create(&model.DigitalRefundTask{
			RefundID: result.ID, TenantID: tenantID, Provider: payment.Method,
			PaymentNo: payment.PaymentNo, Status: "pending", MaxAttempts: defaultDigitalRefundMaxAttempts,
			NextAttemptAt: ptrTime(time.Now()),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateMixedRefund allocates one business refund across the original payment
// rows. Allocation refunds carry the real cash/provider amounts; the parent
// coordinates the ticket, inventory and settlement transition exactly once.
func (s *RefundService) CreateMixedRefund(tenantID uint, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	return s.CreateMixedRefundAs(RefundActor{TenantID: tenantID}, orderNo, idempotencyKey, amount, ticketCodes, reason)
}

func (s *RefundService) CreateMixedRefundAs(actor RefundActor, orderNo, idempotencyKey string, amount float64, ticketCodes []string, reason string) (*model.Refund, error) {
	tenantID := refundOrderTenantID(actor)
	if actor.TenantID == 0 || tenantID == 0 || strings.TrimSpace(orderNo) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("tenant, order and idempotency key are required")
	}
	amountCents := moneyCents(amount)
	if amountCents <= 0 {
		return nil, errors.New("refund amount must be greater than zero")
	}
	cleanCodes := normalizeTicketCodes(ticketCodes)
	if len(cleanCodes) == 0 {
		return nil, errors.New("at least one ticket code is required")
	}
	codesJSON, err := json.Marshal(cleanCodes)
	if err != nil {
		return nil, err
	}
	var root model.Refund
	err = model.Write(func(tx *gorm.DB) error {
		var existing model.Refund
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			existingAmount := existing.AmountCents
			if existingAmount == 0 {
				existingAmount = moneyCents(existing.Amount)
			}
			if existing.ParentRefundID != 0 || existing.OrderNo != orderNo || existingAmount != amountCents || existing.TicketCodesJSON != string(codesJSON) {
				return errors.New("idempotency key was already used with different refund data")
			}
			root = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if err := ensureRefundableEnvironment(&order); err != nil {
			return err
		}
		if order.Status != "paid" && order.Status != "partial_refunded" && order.Status != "completed" {
			return fmt.Errorf("order cannot be refunded from status %s", order.Status)
		}
		allowUsed, err := authorizeUsedTicketRefundTx(tx, actor, &order, cleanCodes)
		if err != nil {
			return err
		}
		selected, refundableAmount, err := selectRefundTickets(&order, cleanCodes, allowUsed, 0)
		if err != nil {
			return err
		}
		if amountCents != moneyCents(refundableAmount) {
			return fmt.Errorf("refund amount must equal selected ticket value %.2f", refundableAmount)
		}

		var payments []model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"tenant_id = ? AND order_no = ? AND status IN ?", tenantID, orderNo, []string{"paid", "partial_refunded"},
		).Order("created_at ASC, id ASC").Find(&payments).Error; err != nil {
			return err
		}
		if len(payments) == 0 {
			return errors.New("paid payment not found")
		}

		root = model.Refund{
			TenantID: tenantID, RefundNo: generateRefundNo(), IdempotencyKey: idempotencyKey,
			OrderNo: orderNo, Amount: centsMoney(amountCents), AmountCents: amountCents,
			Method: "mixed", Status: "group_pending", Reason: strings.TrimSpace(reason), TicketCodesJSON: string(codesJSON),
			AuthorizedUsedRefund: allowUsed, AuthorizedBy: actor.UserID,
		}
		if err := tx.Create(&root).Error; err != nil {
			return err
		}

		remaining := amountCents
		digitalAllocations := 0
		allocationSeq := 0
		for i := range payments {
			payment := &payments[i]
			paymentCents := payment.AmountCents
			if paymentCents == 0 {
				paymentCents = moneyCents(payment.Amount)
			}
			var reserved int64
			if err := tx.Model(&model.Refund{}).Where("payment_id = ? AND (status IN ? OR (parent_refund_id != 0 AND status = ?))", payment.ID, []string{"pending", "succeeded"}, "failed").
				Select("COALESCE(SUM(CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS INTEGER) END), 0)").Scan(&reserved).Error; err != nil {
				return err
			}
			available := paymentCents - reserved
			if available <= 0 {
				continue
			}
			allocated := available
			if allocated > remaining {
				allocated = remaining
			}
			allocationSeq++
			status := "pending"
			if payment.Method == "cash" || payment.Method == "team_account" {
				status = "succeeded"
			} else if payment.Method != "wechat" && payment.Method != "alipay" {
				return fmt.Errorf("unsupported refund payment method %s", payment.Method)
			} else {
				digitalAllocations++
			}
			allocation := model.Refund{
				TenantID: tenantID, RefundNo: generateRefundNo(), IdempotencyKey: fmt.Sprintf("mixed:%d:%d", root.ID, allocationSeq),
				OrderNo: orderNo, PaymentID: payment.ID, ParentRefundID: root.ID, AllocationSeq: allocationSeq,
				Amount: centsMoney(allocated), AmountCents: allocated, Method: payment.Method,
				Status: status, Reason: strings.TrimSpace(reason), TicketCodesJSON: "[]",
			}
			if err := tx.Create(&allocation).Error; err != nil {
				return err
			}
			if payment.Method == "cash" || payment.Method == "team_account" {
				if err := applyRefundPaymentFactTx(tx, payment, &allocation); err != nil {
					return err
				}
			} else if err := tx.Create(&model.DigitalRefundTask{
				RefundID: allocation.ID, TenantID: tenantID, Provider: payment.Method,
				PaymentNo: payment.PaymentNo, Status: "pending", MaxAttempts: defaultDigitalRefundMaxAttempts,
				NextAttemptAt: ptrTime(time.Now()),
			}).Error; err != nil {
				return err
			}
			remaining -= allocated
			if remaining == 0 {
				break
			}
		}
		if remaining != 0 {
			return errors.New("refund amount exceeds the remaining paid balance")
		}
		if digitalAllocations > 0 {
			if err := reserveRefundTicketsTx(tx, selected, root.ID); err != nil {
				return err
			}
		}
		if digitalAllocations == 0 {
			if err := completeMixedRefundBusinessTx(tx, &root, &order, selected); err != nil {
				return err
			}
			root.Status = "group_succeeded"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &root, nil
}

func selectRefundTickets(order *model.Order, cleanCodes []string, allowUsed bool, allowedPendingRefundID uint) (map[string]*model.Ticket, float64, error) {
	wanted := codeSet(cleanCodes)
	selected := make(map[string]*model.Ticket, len(cleanCodes))
	refundableAmount := 0.0
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		policyChecked := false
		for ticketIndex := range item.Tickets {
			ticket := &item.Tickets[ticketIndex]
			if _, ok := wanted[ticket.TicketCode]; !ok {
				continue
			}
			if !policyChecked {
				refundType := strings.TrimSpace(item.RefundType)
				if refundType == "" {
					// Legacy rows predate sale-time policy snapshots. Preserve their
					// historical refundable behavior; all new orders snapshot a policy.
					refundType = "free"
				}
				switch refundType {
				case "free":
				case "no_refund":
					return nil, 0, fmt.Errorf("product %s does not allow refunds", item.ProductName)
				case "ladder":
					return nil, 0, fmt.Errorf("product %s has a ladder refund policy that is not configured", item.ProductName)
				default:
					return nil, 0, fmt.Errorf("product %s has an invalid refund policy", item.ProductName)
				}
				policyChecked = true
			}
			if ticket.PendingRefundID != 0 && ticket.PendingRefundID != allowedPendingRefundID {
				return nil, 0, fmt.Errorf("ticket %s already has a pending refund", ticket.TicketCode)
			}
			unused := ticket.Status == "unused" && ticket.CheckInCount == 0
			used := ticket.CheckInCount > 0 && (ticket.Status == "used" || ticket.Status == "active" || ticket.Status == "unused")
			if !unused && !(allowUsed && used) {
				return nil, 0, fmt.Errorf("ticket %s is already used", ticket.TicketCode)
			}
			selected[ticket.TicketCode] = ticket
			if ticket.CodeMode == "order" || (ticket.CodeMode == "" && item.Product.CodeMode == "order") {
				refundableAmount += item.Price * float64(item.Quantity)
			} else {
				refundableAmount += item.Price
			}
		}
	}
	if len(selected) != len(cleanCodes) {
		return nil, 0, errors.New("one or more ticket codes do not belong to the order")
	}
	return selected, roundMoney(refundableAmount), nil
}

func reserveRefundTicketsTx(tx *gorm.DB, selected map[string]*model.Ticket, refundID uint) error {
	if refundID == 0 {
		return errors.New("refund reservation requires a refund id")
	}
	for _, ticket := range selected {
		result := tx.Model(&model.Ticket{}).
			Where("id = ? AND status = ? AND check_in_count = ? AND pending_refund_id = 0", ticket.ID, ticket.Status, ticket.CheckInCount).
			Update("pending_refund_id", refundID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("ticket %s changed while the refund was being created", ticket.TicketCode)
		}
		ticket.PendingRefundID = refundID
	}
	return nil
}

func releaseRefundTicketsTx(tx *gorm.DB, refundID uint) error {
	if refundID == 0 {
		return nil
	}
	return tx.Model(&model.Ticket{}).Where("pending_refund_id = ?", refundID).Update("pending_refund_id", 0).Error
}

func reacquireFailedRefundTx(tx *gorm.DB, refund *model.Refund) error {
	var codes []string
	if err := json.Unmarshal([]byte(refund.TicketCodesJSON), &codes); err != nil {
		return err
	}
	codes = normalizeTicketCodes(codes)
	if len(codes) == 0 {
		return errors.New("failed refund has no ticket codes")
	}
	var payment model.Payment
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", refund.PaymentID, refund.TenantID).First(&payment).Error; err != nil {
		return err
	}
	var reserved int64
	if err := tx.Model(&model.Refund{}).
		Where("payment_id = ? AND id != ? AND status IN ?", payment.ID, refund.ID, []string{"pending", "succeeded"}).
		Select("COALESCE(SUM(CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS BIGINT) END), 0)").
		Scan(&reserved).Error; err != nil {
		return err
	}
	paymentAmount := payment.AmountCents
	if paymentAmount == 0 {
		paymentAmount = moneyCents(payment.Amount)
	}
	refundAmount := refund.AmountCents
	if refundAmount == 0 {
		refundAmount = moneyCents(refund.Amount)
	}
	if reserved+refundAmount > paymentAmount {
		return errors.New("refund payment balance is no longer available")
	}
	var order model.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").
		Where("order_no = ? AND tenant_id = ?", refund.OrderNo, refund.TenantID).First(&order).Error; err != nil {
		return err
	}
	selected, amount, err := selectRefundTickets(&order, codes, refund.AuthorizedUsedRefund, 0)
	if err != nil {
		return err
	}
	if moneyCents(amount) != refundAmount {
		return errors.New("selected ticket value changed before refund retry")
	}
	return reserveRefundTicketsTx(tx, selected, refund.ID)
}

func authorizeUsedTicketRefundTx(tx *gorm.DB, actor RefundActor, order *model.Order, cleanCodes []string) (bool, error) {
	wanted := codeSet(cleanCodes)
	hasUsed := false
	crossTenant := actor.OrderTenantID != 0 && actor.OrderTenantID != actor.TenantID
	for itemIndex := range order.Items {
		for ticketIndex := range order.Items[itemIndex].Tickets {
			ticket := &order.Items[itemIndex].Tickets[ticketIndex]
			if _, ok := wanted[ticket.TicketCode]; !ok {
				continue
			}
			if crossTenant && (ticket.CheckInCount == 0 || ticket.FulfillmentOrderID != actor.FulfillmentOrderID) {
				return false, errors.New("supplier exception only applies to used tickets in the selected fulfillment order")
			}
			if ticket.CheckInCount == 0 {
				continue
			}
			hasUsed = true
			if ticket.FulfillmentTenantID != actor.TenantID {
				return false, errors.New("used ticket refund must be operated by its scenic supplier")
			}
		}
	}
	if !hasUsed {
		return false, nil
	}
	if actor.TenantID == 0 || actor.UserID == 0 || (!crossTenant && order.TenantID != actor.TenantID) || (crossTenant && order.TenantID != actor.OrderTenantID) {
		return false, errors.New("used ticket refund requires the scenic supplier initial administrator")
	}
	var user model.User
	if err := tx.Where("id = ? AND tenant_id = ? AND is_initial_admin = ?", actor.UserID, actor.TenantID, true).First(&user).Error; err != nil {
		return false, errors.New("used ticket refund requires the scenic supplier initial administrator")
	}
	if err := requireActiveTenantCapability(tx, actor.TenantID, "supplier"); err != nil {
		return false, errors.New("used ticket refund requires an active scenic supplier")
	}
	return true, nil
}

// CreateSupplierUsedRefund lets the fulfillment supplier's initial
// administrator reverse an already verified ticket. The actor remains scoped
// to the supplier, while payment and distributor ledger facts stay owned by
// the original sales tenant.
func (s *RefundService) CreateSupplierUsedRefund(actor RefundActor, fulfillmentID uint, idempotencyKey string, ticketCodes []string, reason string) (*model.Refund, error) {
	if actor.TenantID == 0 || actor.UserID == 0 || fulfillmentID == 0 {
		return nil, errors.New("supplier, operator and fulfillment are required")
	}
	var fulfillment model.FulfillmentOrder
	if err := model.DB.Where("id = ? AND supplier_tenant_id = ?", fulfillmentID, actor.TenantID).First(&fulfillment).Error; err != nil {
		return nil, errors.New("fulfillment order not found")
	}
	cleanCodes := normalizeTicketCodes(ticketCodes)
	if len(cleanCodes) == 0 {
		return nil, errors.New("at least one ticket code is required")
	}
	codesJSON, err := json.Marshal(cleanCodes)
	if err != nil {
		return nil, err
	}
	var existing model.Refund
	if err := model.DB.Where("tenant_id = ? AND idempotency_key = ?", fulfillment.SalesTenantID, idempotencyKey).First(&existing).Error; err == nil {
		if existing.OrderNo != fulfillment.SalesOrderNo || existing.TicketCodesJSON != string(codesJSON) {
			return nil, errors.New("idempotency key was already used with different refund data")
		}
		return &existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var order model.Order
	if err := model.DB.Preload("Items.Product").Preload("Items.Tickets").Where("id = ? AND tenant_id = ?", fulfillment.SalesOrderID, fulfillment.SalesTenantID).First(&order).Error; err != nil {
		return nil, errors.New("sales order not found")
	}
	for _, code := range cleanCodes {
		matched := false
		for itemIndex := range order.Items {
			for ticketIndex := range order.Items[itemIndex].Tickets {
				ticket := &order.Items[itemIndex].Tickets[ticketIndex]
				if ticket.TicketCode == code && ticket.FulfillmentOrderID == fulfillment.ID && ticket.FulfillmentTenantID == actor.TenantID && ticket.CheckInCount > 0 {
					matched = true
				}
			}
		}
		if !matched {
			return nil, errors.New("supplier exception only applies to used tickets in the selected fulfillment order")
		}
	}
	_, amount, err := selectRefundTickets(&order, cleanCodes, true, 0)
	if err != nil {
		return nil, err
	}
	actor.OrderTenantID = fulfillment.SalesTenantID
	actor.FulfillmentOrderID = fulfillment.ID
	return s.CreateMixedRefundAs(actor, fulfillment.SalesOrderNo, idempotencyKey, amount, ticketCodes, reason)
}

func (s *RefundService) ProcessDigitalRefundTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	processed := 0
	for processed < limit {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		task, err := claimDigitalRefundTask(now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		processed++
		if err := s.processDigitalRefundTask(ctx, task.ID, now); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

// claimDigitalRefundTask changes a due task to processing in a short database
// transaction before any provider call is made. A stale processing lease is
// reclaimable after a crash, while a second worker cannot claim the same task
// during a normal provider request.
func claimDigitalRefundTask(now time.Time) (*model.DigitalRefundTask, error) {
	var task model.DigitalRefundTask
	err := model.Write(func(tx *gorm.DB) error {
		staleBefore := now.Add(-staleDigitalRefundTaskAfter)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("((status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = ? AND locked_at IS NOT NULL AND locked_at <= ?))",
				[]string{"pending", "submitted"}, now, "processing", staleBefore).
			Order("COALESCE(next_attempt_at, created_at) asc, id asc").First(&task).Error; err != nil {
			return err
		}
		lockedAt := now
		return tx.Model(&task).Updates(map[string]interface{}{
			"status": "processing", "locked_at": lockedAt,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	task.Status = "processing"
	return &task, nil
}

func updateClaimedDigitalRefundTask(tx *gorm.DB, taskID uint, lockedAt *time.Time, updates map[string]interface{}) error {
	query := tx.Model(&model.DigitalRefundTask{}).Where("id = ?", taskID)
	if lockedAt != nil {
		query = query.Where("status = ? AND locked_at = ?", "processing", *lockedAt)
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if lockedAt != nil && result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *RefundService) processDigitalRefundTask(ctx context.Context, taskID uint, now time.Time) error {
	var task model.DigitalRefundTask
	var refund model.Refund
	var payment model.Payment
	if err := model.DB.First(&task, taskID).Error; err != nil {
		return err
	}
	if task.Status == "succeeded" || task.Status == "failed" || task.Status == "manual_review" {
		return nil
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", task.RefundID, task.TenantID).First(&refund).Error; err != nil {
		return err
	}
	if err := model.DB.Where("id = ? AND tenant_id = ?", refund.PaymentID, refund.TenantID).First(&payment).Error; err != nil {
		return err
	}
	if refund.ProviderRefundID == "" {
		refund.ProviderRefundID = task.ProviderRefund
	}
	provider := s.Provider
	if provider == nil {
		provider = &gopayRefundProvider{payments: s.PaymentService}
	}
	result, err := provider.Process(ctx, &refund, &payment)
	if err != nil {
		return s.deferClaimedDigitalRefundTask(task.ID, task.LockedAt, now, err)
	}
	switch result.Status {
	case "succeeded":
		return s.completeDigitalRefund(task.ID, task.LockedAt, &refund, &payment, result.ProviderRefundID)
	case "failed":
		return model.Write(func(tx *gorm.DB) error {
			if err := tx.Model(&model.Refund{}).Where("id = ? AND status = ?", refund.ID, "pending").Updates(map[string]interface{}{"status": "failed", "provider_refund_id": result.ProviderRefundID}).Error; err != nil {
				return err
			}
			if refund.ParentRefundID == 0 {
				if err := releaseRefundTicketsTx(tx, refund.ID); err != nil {
					return err
				}
			}
			return updateClaimedDigitalRefundTask(tx, task.ID, task.LockedAt, map[string]interface{}{
				"status": "failed", "locked_at": nil, "provider_refund": result.ProviderRefundID,
				"provider_status": result.Status, "failure_code": "provider_rejected", "next_attempt_at": nil,
			})
		})
	default:
		next := now.Add(30 * time.Second)
		return model.Write(func(tx *gorm.DB) error {
			attempt := task.AttemptCount + 1
			maxAttempts := task.MaxAttempts
			if maxAttempts <= 0 {
				maxAttempts = defaultDigitalRefundMaxAttempts
			}
			if err := tx.Model(&model.Refund{}).Where("id = ?", refund.ID).Update("provider_refund_id", result.ProviderRefundID).Error; err != nil {
				return err
			}
			if attempt >= maxAttempts {
				return updateClaimedDigitalRefundTask(tx, task.ID, task.LockedAt, map[string]interface{}{
					"status": "manual_review", "provider_refund": result.ProviderRefundID,
					"attempt_count": attempt, "max_attempts": maxAttempts, "next_attempt_at": nil,
					"locked_at":  nil,
					"last_error": "provider did not reach a terminal refund state", "failure_code": "provider_pending_timeout", "manual_review_at": now,
				})
			}
			return updateClaimedDigitalRefundTask(tx, task.ID, task.LockedAt, map[string]interface{}{
				"status": "submitted", "provider_refund": result.ProviderRefundID,
				"attempt_count": attempt, "max_attempts": maxAttempts, "next_attempt_at": next, "locked_at": nil, "last_error": "",
			})
		})
	}
}

func (s *RefundService) deferDigitalRefundTask(taskID uint, now time.Time, cause error) error {
	return s.deferClaimedDigitalRefundTask(taskID, nil, now, cause)
}

func (s *RefundService) deferClaimedDigitalRefundTask(taskID uint, lockedAt *time.Time, now time.Time, cause error) error {
	return model.Write(func(tx *gorm.DB) error {
		var task model.DigitalRefundTask
		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}
		attempt := task.AttemptCount + 1
		maxAttempts := task.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = defaultDigitalRefundMaxAttempts
		}
		failureCode := refundFailureCode(cause)
		message := truncateError(cause.Error())
		updates := map[string]interface{}{}
		if lockedAt != nil {
			updates["locked_at"] = nil
		}
		if failureCode == "provider_not_configured" || failureCode == "provider_configuration" || attempt >= maxAttempts {
			updates["status"] = "manual_review"
			updates["attempt_count"] = attempt
			updates["max_attempts"] = maxAttempts
			updates["next_attempt_at"] = nil
			updates["last_error"] = message
			updates["failure_code"] = failureCode
			updates["manual_review_at"] = now
			return updateClaimedDigitalRefundTask(tx, task.ID, lockedAt, updates)
		}
		delay := time.Duration(1<<minInt(attempt, 6)) * 30 * time.Second
		next := now.Add(delay)
		updates["status"] = "pending"
		updates["attempt_count"] = attempt
		updates["max_attempts"] = maxAttempts
		updates["next_attempt_at"] = next
		updates["last_error"] = message
		updates["failure_code"] = failureCode
		return updateClaimedDigitalRefundTask(tx, task.ID, lockedAt, updates)
	})
}

func refundFailureCode(cause error) string {
	if errors.Is(cause, ErrDigitalRefundNotConfigured) {
		return "provider_not_configured"
	}
	message := strings.ToLower(cause.Error())
	for _, marker := range []string{"certificate", "credential", "private key", "not configured", "invalid app", "invalid mch"} {
		if strings.Contains(message, marker) {
			return "provider_configuration"
		}
	}
	return "provider_unavailable"
}

// ListDigitalRefundTasks returns only tasks owned by the authenticated tenant.
func (s *RefundService) ListDigitalRefundTasks(tenantID uint, status string, page, pageSize int) ([]model.DigitalRefundTask, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.DigitalRefundTask{}).Where("tenant_id = ?", tenantID)
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tasks []model.DigitalRefundTask
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// RetryDigitalRefundTask is an audited operator action for failed or parked
// refunds. It never marks a refund successful; the worker still requires
// provider confirmation before applying business facts.
func (s *RefundService) RetryDigitalRefundTask(tenantID, taskID, operatorID uint, operatorRole, reason string) error {
	if tenantID == 0 || taskID == 0 || operatorID == 0 || strings.TrimSpace(reason) == "" {
		return errors.New("tenant, task, operator and reason are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var task model.DigitalRefundTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", taskID, tenantID).First(&task).Error; err != nil {
			return err
		}
		if task.Status != "failed" && task.Status != "manual_review" {
			return fmt.Errorf("refund task cannot be retried from %s", task.Status)
		}
		var refund model.Refund
		if err := tx.Where("id = ? AND tenant_id = ?", task.RefundID, tenantID).First(&refund).Error; err != nil {
			return err
		}
		if refund.Status == "succeeded" {
			return errors.New("refund has already succeeded")
		}
		if refund.Status == "failed" && refund.ParentRefundID == 0 && strings.TrimSpace(refund.TicketCodesJSON) != "" && refund.TicketCodesJSON != "[]" {
			if err := reacquireFailedRefundTx(tx, &refund); err != nil {
				return err
			}
		}
		previousStatus := task.Status
		if err := tx.Model(&task).Updates(map[string]interface{}{
			"status": "pending", "next_attempt_at": time.Now(), "last_error": "",
			"failure_code": "", "attempt_count": 0, "locked_at": nil,
		}).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE digital_refund_tasks SET manual_review_at = NULL WHERE id = ?", task.ID).Error; err != nil {
			return err
		}
		if refund.Status == "failed" {
			if err := tx.Model(&refund).Update("status", "pending").Error; err != nil {
				return err
			}
		}
		return recordAuditTx(tx, operatorID, tenantID, operatorRole, "tenant", "payment.refund.retry", "digital_refund_task", task.ID, strings.TrimSpace(reason), `{"status":"`+previousStatus+`"}`, `{"status":"pending"}`)
	})
}

func (s *RefundService) completeDigitalRefund(taskID uint, lockedAt *time.Time, refund *model.Refund, payment *model.Payment, providerRefundID string) error {
	var codes []string
	if err := json.Unmarshal([]byte(refund.TicketCodesJSON), &codes); err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var storedRefund model.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&storedRefund, refund.ID).Error; err != nil {
			return err
		}
		if storedRefund.Status == "succeeded" {
			return updateClaimedDigitalRefundTask(tx, taskID, lockedAt, map[string]interface{}{"status": "succeeded", "next_attempt_at": nil, "locked_at": nil})
		}
		if storedRefund.Status != "pending" {
			return fmt.Errorf("refund cannot complete from status %s", storedRefund.Status)
		}
		var storedPayment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", payment.ID, payment.TenantID).First(&storedPayment).Error; err != nil {
			return err
		}
		if storedRefund.ParentRefundID != 0 {
			if err := applyRefundPaymentFactTx(tx, &storedPayment, &storedRefund); err != nil {
				return err
			}
			if err := tx.Model(&storedRefund).Updates(map[string]interface{}{"status": "succeeded", "provider_refund_id": providerRefundID}).Error; err != nil {
				return err
			}
			if err := updateClaimedDigitalRefundTask(tx, taskID, lockedAt, map[string]interface{}{"status": "succeeded", "provider_refund": providerRefundID, "next_attempt_at": nil, "locked_at": nil, "last_error": ""}); err != nil {
				return err
			}
			return tryCompleteMixedRefundTx(tx, storedRefund.TenantID, storedRefund.ParentRefundID)
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", storedRefund.OrderNo, storedRefund.TenantID).First(&order).Error; err != nil {
			return err
		}
		selected, amount, err := selectRefundTickets(&order, normalizeTicketCodes(codes), storedRefund.AuthorizedUsedRefund, storedRefund.ID)
		if err != nil {
			return err
		}
		refundAmountCents := storedRefund.AmountCents
		if refundAmountCents == 0 {
			refundAmountCents = moneyCents(storedRefund.Amount)
		}
		if moneyCents(amount) != refundAmountCents {
			return errors.New("selected ticket value changed before refund completion")
		}
		if err := applySuccessfulRefundTx(tx, &order, &storedPayment, &storedRefund, selected); err != nil {
			return err
		}
		if err := tx.Model(&storedRefund).Updates(map[string]interface{}{"status": "succeeded", "provider_refund_id": providerRefundID}).Error; err != nil {
			return err
		}
		return updateClaimedDigitalRefundTask(tx, taskID, lockedAt, map[string]interface{}{"status": "succeeded", "provider_refund": providerRefundID, "next_attempt_at": nil, "locked_at": nil, "last_error": ""})
	})
}

func applySuccessfulRefundTx(tx *gorm.DB, order *model.Order, payment *model.Payment, refund *model.Refund, selected map[string]*model.Ticket) error {
	if err := applyRefundBusinessFactsTx(tx, order, refund, selected); err != nil {
		return err
	}
	return applyRefundPaymentFactTx(tx, payment, refund)
}

func applyRefundBusinessFactsTx(tx *gorm.DB, order *model.Order, refund *model.Refund, selected map[string]*model.Ticket) error {
	if err := ensureRefundableEnvironment(order); err != nil {
		return err
	}
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		selectedUnits, refundableStockUnits, previouslyRefunded := 0, 0, 0
		for ticketIndex := range item.Tickets {
			ticket := &item.Tickets[ticketIndex]
			if _, ok := selected[ticket.TicketCode]; ok {
				selectedUnits++
				if ticket.CheckInCount == 0 {
					refundableStockUnits++
				} else if !refund.AuthorizedUsedRefund {
					return fmt.Errorf("ticket %s is already used", ticket.TicketCode)
				}
			} else if ticket.Status == "refunded" {
				previouslyRefunded++
			}
		}
		if selectedUnits == 0 {
			continue
		}
		totalUnits, stockQuantity := len(item.Tickets), refundableStockUnits
		codeMode := item.Product.CodeMode
		if len(item.Tickets) > 0 && item.Tickets[0].CodeMode != "" {
			codeMode = item.Tickets[0].CodeMode
		}
		if codeMode == "order" {
			totalUnits = 1
			if refundableStockUnits > 0 {
				stockQuantity = item.Quantity
			}
		}
		cashCents := allocatedRefundCents(item.CashCostCents, previouslyRefunded, selectedUnits, totalUnits)
		creditCents := allocatedRefundCents(item.CreditCostCents, previouslyRefunded, selectedUnits, totalUnits)
		var listing model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.ProductID, order.TenantID).First(&listing).Error; err != nil {
			return err
		}
		stockProduct, distributed, err := loadStoredFulfillmentProduct(tx, &listing, item, order.TenantID)
		if err != nil {
			return err
		}
		if distributed {
			key := "refund:" + refund.RefundNo + ":item:" + ledgerItemKey(item)
			if err := refundDistributionAllocation(tx, order, item, order.TenantID, stockProduct.TenantID, cashCents, creditCents, key, listing.Name); err != nil {
				return err
			}
			if stockQuantity > 0 {
				if err := releaseOfferQuotaTx(tx, item.ProductOfferID, stockQuantity); err != nil {
					return err
				}
			}
		}
		if stockQuantity > 0 {
			if err := releaseStock(tx, stockProductForRelease(stockProduct, item.ReservedStockType), item.UseDate, item.StockSlot, stockQuantity); err != nil {
				return err
			}
		}
	}
	for _, ticket := range selected {
		if ticket.CheckInCount > 0 {
			now := time.Now()
			if err := tx.Model(&model.CheckInRecord{}).
				Where("ticket_id = ? AND result = ? AND reversed_at IS NULL", ticket.ID, "success").
				Updates(map[string]interface{}{"reversed_at": now, "reversed_by": refund.AuthorizedBy, "reversal_refund_id": refund.ID}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(ticket).Updates(map[string]interface{}{"status": "refunded", "pending_refund_id": 0}).Error; err != nil {
			return err
		}
		ticket.Status = "refunded"
		if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ?", ticket.ID).Update("status", "refunded").Error; err != nil {
			return err
		}
	}
	if err := releaseTeamCreditAfterRefundTx(tx, order); err != nil {
		return err
	}
	if err := reconcileTeamSettlementsAfterRefundTx(tx, order, refund); err != nil {
		return err
	}
	var remainingTickets int64
	if err := tx.Model(&model.Ticket{}).Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND status != ?", order.ID, "refunded").Count(&remainingTickets).Error; err != nil {
		return err
	}
	orderStatus := "partial_refunded"
	if remainingTickets == 0 {
		orderStatus = "refunded"
	}
	if err := tx.Model(order).Update("status", orderStatus).Error; err != nil {
		return err
	}
	order.Status = orderStatus
	if orderStatus == "refunded" {
		return updateFulfillmentOrdersTx(tx, order.ID, "cancelled")
	}
	return nil
}

func releaseTeamCreditAfterRefundTx(tx *gorm.DB, order *model.Order) error {
	var groups []model.TourGroup
	if err := tx.Where("sales_order_id = ? AND status != ?", order.ID, "cancelled").Find(&groups).Error; err != nil {
		return err
	}
	for i := range groups {
		var remaining int64
		for itemIndex := range order.Items {
			item := &order.Items[itemIndex]
			if item.FulfillmentTenantID != groups[i].SupplierTenantID || item.FulfillmentScenicAreaID != groups[i].ScenicAreaID {
				continue
			}
			unit := moneyCents(item.SettlementPrice)
			if len(item.Tickets) == 1 && item.Tickets[0].CodeMode == "order" {
				if item.Tickets[0].Status != "refunded" {
					remaining += unit * int64(item.Quantity)
				}
				continue
			}
			for ticketIndex := range item.Tickets {
				if item.Tickets[ticketIndex].Status != "refunded" {
					remaining += unit
				}
			}
		}
		creditUsed := remaining - groups[i].DepositCents
		if creditUsed < 0 {
			creditUsed = 0
		}
		if err := tx.Model(&groups[i]).Update("credit_used_cents", creditUsed).Error; err != nil {
			return err
		}
	}
	return nil
}

func applyRefundPaymentFactTx(tx *gorm.DB, payment *model.Payment, refund *model.Refund) error {
	payment.RefundedAmount = roundMoney(payment.RefundedAmount + refund.Amount)
	refundAmountCents := refund.AmountCents
	if refundAmountCents == 0 {
		refundAmountCents = moneyCents(refund.Amount)
	}
	paymentRefundedCents := payment.RefundedAmountCents
	if paymentRefundedCents == 0 {
		paymentRefundedCents = moneyCents(payment.RefundedAmount - refund.Amount)
	}
	payment.RefundedAmountCents = paymentRefundedCents + refundAmountCents
	newStatus := "partial_refunded"
	paymentAmountCents := payment.AmountCents
	if paymentAmountCents == 0 {
		paymentAmountCents = moneyCents(payment.Amount)
	}
	if payment.RefundedAmountCents >= paymentAmountCents {
		newStatus = "refunded"
	}
	if err := tx.Model(payment).Updates(map[string]interface{}{"refunded_amount": payment.RefundedAmount, "refunded_amount_cents": payment.RefundedAmountCents, "amount_cents": paymentAmountCents, "status": newStatus}).Error; err != nil {
		return err
	}
	return nil
}

func tryCompleteMixedRefundTx(tx *gorm.DB, tenantID, parentRefundID uint) error {
	var remaining int64
	if err := tx.Model(&model.Refund{}).Where("tenant_id = ? AND parent_refund_id = ? AND status != ?", tenantID, parentRefundID, "succeeded").Count(&remaining).Error; err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	var root model.Refund
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND parent_refund_id = 0", parentRefundID, tenantID).First(&root).Error; err != nil {
		return err
	}
	if root.Status == "group_succeeded" {
		return nil
	}
	if root.Purpose == exchangeDifferencePurpose {
		return completeExchangeDifferenceRefundTx(tx, &root)
	}
	var order model.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Product").Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", root.OrderNo, tenantID).First(&order).Error; err != nil {
		return err
	}
	var codes []string
	if err := json.Unmarshal([]byte(root.TicketCodesJSON), &codes); err != nil {
		return err
	}
	selected, amount, err := selectRefundTickets(&order, normalizeTicketCodes(codes), root.AuthorizedUsedRefund, root.ID)
	if err != nil {
		return err
	}
	if moneyCents(amount) != root.AmountCents {
		return errors.New("selected ticket value changed before mixed refund completion")
	}
	return completeMixedRefundBusinessTx(tx, &root, &order, selected)
}

func completeMixedRefundBusinessTx(tx *gorm.DB, root *model.Refund, order *model.Order, selected map[string]*model.Ticket) error {
	if err := applyRefundBusinessFactsTx(tx, order, root, selected); err != nil {
		return err
	}
	if err := tx.Model(&model.Refund{}).Where("tenant_id = ? AND parent_refund_id = ?", root.TenantID, root.ID).Update("ticket_codes_json", root.TicketCodesJSON).Error; err != nil {
		return err
	}
	if err := tx.Model(root).Update("status", "group_succeeded").Error; err != nil {
		return err
	}
	root.Status = "group_succeeded"
	now := time.Now()
	var requests []model.AfterSaleRequest
	if err := tx.Where("tenant_id = ? AND refund_id = ? AND status = ?", root.TenantID, root.ID, "processing").Find(&requests).Error; err != nil {
		return err
	}
	for i := range requests {
		request := &requests[i]
		if err := tx.Model(request).Updates(map[string]interface{}{"status": "completed", "completed_at": now}).Error; err != nil {
			return err
		}
		request.Status = "completed"
		request.CompletedAt = &now
		if err := appendAfterSaleEvent(tx, request, "processing", "completed", "refund_completed", request.OperatorID, root.Reason); err != nil {
			return err
		}
	}
	return nil
}

type gopayRefundProvider struct{ payments *PaymentService }

func (p *gopayRefundProvider) Process(ctx context.Context, refund *model.Refund, payment *model.Payment) (RefundProviderResult, error) {
	payments := p.payments
	if payments == nil {
		payments = &PaymentService{}
	}
	switch refund.Method {
	case "wechat":
		cfg, err := payments.GetConfig(refund.TenantID, "wechat")
		if err != nil {
			return RefundProviderResult{}, err
		}
		client, err := wechat.NewClientV3(cfg.MchID, cfg.SerialNo, cfg.Key, cfg.PrivateKey)
		if err != nil {
			return RefundProviderResult{}, err
		}
		var status, providerID string
		if refund.ProviderRefundID == "" {
			bm := make(gopay.BodyMap)
			bm.Set("out_trade_no", payment.PaymentNo).Set("out_refund_no", refund.RefundNo).Set("reason", refund.Reason).
				SetBodyMap("amount", func(amount gopay.BodyMap) {
					amount.Set("refund", moneyCents(refund.Amount)).Set("total", moneyCents(payment.Amount)).Set("currency", "CNY")
				})
			res, err := client.V3Refund(ctx, bm)
			if err != nil || res.Response == nil {
				return RefundProviderResult{}, firstError(err, resError(res))
			}
			status, providerID = res.Response.Status, res.Response.RefundId
		} else {
			res, err := client.V3RefundQuery(ctx, refund.RefundNo, nil)
			if err != nil || res.Response == nil {
				return RefundProviderResult{}, firstError(err, resQueryError(res))
			}
			status, providerID = res.Response.Status, res.Response.RefundId
		}
		switch status {
		case "SUCCESS":
			return RefundProviderResult{Status: "succeeded", ProviderRefundID: providerID}, nil
		case "CLOSED", "ABNORMAL":
			return RefundProviderResult{Status: "failed", ProviderRefundID: providerID}, nil
		default:
			return RefundProviderResult{Status: "submitted", ProviderRefundID: providerID}, nil
		}
	case "alipay":
		client, err := payments.alipayClient(refund.TenantID)
		if err != nil {
			return RefundProviderResult{}, err
		}
		bm := make(gopay.BodyMap)
		bm.Set("out_trade_no", payment.PaymentNo).Set("refund_amount", fmt.Sprintf("%.2f", refund.Amount)).Set("refund_reason", refund.Reason).Set("out_request_no", refund.RefundNo)
		res, err := client.TradeRefund(ctx, bm)
		if err != nil || res.Response == nil {
			return RefundProviderResult{}, firstError(err, errors.New("empty Alipay refund response"))
		}
		if res.Response.Code != "10000" {
			return RefundProviderResult{}, fmt.Errorf("Alipay refund failed: %s", res.Response.SubMsg)
		}
		return RefundProviderResult{Status: "succeeded", ProviderRefundID: res.Response.TradeNo}, nil
	default:
		return RefundProviderResult{}, ErrDigitalRefundNotConfigured
	}
}

func resError(res *wechat.RefundRsp) error {
	if res == nil {
		return errors.New("empty WeChat refund response")
	}
	return fmt.Errorf("WeChat refund failed: %s", res.Error)
}

func resQueryError(res *wechat.RefundQueryRsp) error {
	if res == nil {
		return errors.New("empty WeChat refund query response")
	}
	return fmt.Errorf("WeChat refund query failed: %s", res.Error)
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func truncateError(value string) string {
	if len(value) > 255 {
		return value[:255]
	}
	return value
}

func ptrTime(value time.Time) *time.Time { return &value }

func normalizeTicketCodes(codes []string) []string {
	set := codeSet(nil)
	result := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, exists := set[code]; exists {
			continue
		}
		set[code] = struct{}{}
		result = append(result, code)
	}
	return result
}

func codeSet(codes []string) map[string]struct{} {
	set := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	return set
}
