package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

// MiniappRefundApplicationInput intentionally accepts only customer-facing
// context. The order, tickets, amount and refund method always come from the
// immutable sale facts.
type MiniappRefundApplicationInput struct {
	Reason          string `json:"reason"`
	ClientRequestID string `json:"request_id"`
}

type MiniappRefundApplicationResult struct {
	RequestNo string `json:"request_no"`
	Status    string `json:"status"`
}

const miniappRefundApplicationKeyPrefix = "customer-refund:"

func (s MiniappService) ApplyXiaohongshuRefund(customer *model.MiniappCustomer, orderNo string, input MiniappRefundApplicationInput) (*MiniappRefundApplicationResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	orderNo = strings.TrimSpace(orderNo)
	input.Reason = strings.TrimSpace(input.Reason)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if orderNo == "" {
		return nil, errors.New("order not found")
	}
	if input.ClientRequestID == "" || len(input.ClientRequestID) > 80 {
		return nil, errors.New("refund application request id is invalid")
	}
	if input.Reason == "" || len([]rune(input.Reason)) > 255 {
		return nil, errors.New("refund application reason is invalid")
	}

	key := miniappRefundApplicationKey(customer.ChannelAccountID, customer.ID, input.ClientRequestID)
	// Ownership is established before any idempotency replay. These are scoped
	// reads only: the financial helper owns the payment -> ticket -> account
	// locking order inside the single write transaction below.
	ownedCustomer, err := loadMiniappRefundApplicationCustomer(customer, orderNo, s.now())
	if err != nil {
		return nil, err
	}
	result := &MiniappRefundApplicationResult{}
	err = model.Write(func(tx *gorm.DB) error {
		var request model.AfterSaleRequest
		refund, err := (&RefundService{}).createDigitalRefundAsTx(tx, RefundActor{TenantID: ownedCustomer.TenantID}, orderNo, "after-sale:"+key, float64(ownedCustomer.AmountCents)/100, ownedCustomer.TicketCodes, input.Reason,
			func(tx *gorm.DB, order *model.Order, accountPayment *model.Payment, selected map[string]*model.Ticket, amountCents int64) error {
				// Re-check the customer-scoped sale evidence inside the write
				// transaction without acquiring account/order locks ahead of the
				// canonical refund sequence.
				codes, eligibleAmount, err := validateXiaohongshuCustomerRefundApplicationTx(tx, &ownedCustomer.Customer, &ownedCustomer.Account, order, false)
				if err != nil {
					return err
				}
				if amountCents != eligibleAmount || len(codes) != len(ownedCustomer.TicketCodes) {
					return errors.New("order amount or tickets are not eligible for a refund application")
				}
				if err := ensureNoActiveXiaohongshuRefundApplicationTx(tx, order); err != nil {
					return err
				}
				encoded, err := json.Marshal(codes)
				if err != nil {
					return err
				}
				request = model.AfterSaleRequest{
					TenantID: order.TenantID, RequestNo: generateAfterSaleNo(), IdempotencyKey: key,
					OrderNo: order.OrderNo, Type: "refund", Status: "processing", Reason: input.Reason,
					TicketCodesJSON: string(encoded), AmountCents: amountCents, PaymentMethod: xiaohongshuPaymentMethod,
					OperatorID: 0, ReviewerID: 0, ReviewedAt: ptrTime(s.now()),
				}
				if err := tx.Create(&request).Error; err != nil {
					return err
				}
				if err := appendAfterSaleEvent(tx, &request, "", "processing", "customer_applied", 0, fmt.Sprintf("miniapp_customer_id=%d channel_account_id=%d", ownedCustomer.Customer.ID, ownedCustomer.Account.ID)); err != nil {
					return err
				}
				return appendAfterSaleEvent(tx, &request, "processing", "processing", "system_approved", 0, "unused whole-order automatic-refund policy")
			})
		if err != nil {
			return err
		}
		if request.ID == 0 {
			if err := tx.Where("tenant_id = ? AND idempotency_key = ?", ownedCustomer.TenantID, key).First(&request).Error; err != nil {
				return err
			}
			if request.OrderNo != orderNo || request.Type != "refund" || request.Reason != input.Reason || request.PaymentMethod != xiaohongshuPaymentMethod || request.RefundID != refund.ID {
				return errors.New("refund application request id was already used with different content")
			}
		} else if err := tx.Model(&request).Update("refund_id", refund.ID).Error; err != nil {
			return err
		}
		result.RequestNo, result.Status = request.RequestNo, request.Status
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type miniappRefundApplicationOwner struct {
	Customer    model.MiniappCustomer
	Account     model.ChannelAccount
	TenantID    uint
	AmountCents int64
	TicketCodes []string
}

func loadMiniappRefundApplicationCustomer(customer *model.MiniappCustomer, orderNo string, now time.Time) (*miniappRefundApplicationOwner, error) {
	var current model.MiniappCustomer
	if err := model.DB.Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND status = ? AND session_expires_at > ?", customer.ID, customer.TenantID, customer.ChannelAccountID, "active", now).First(&current).Error; err != nil {
		return nil, ErrMiniappUnauthenticated
	}
	var tenant model.Tenant
	if err := model.DB.Where("id = ? AND status = ?", current.TenantID, "active").First(&tenant).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	if err := requireAnyActiveTenantCapability(model.DB, current.TenantID, "supplier", "distributor"); err != nil {
		return nil, ErrMiniappUnavailable
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status = ? AND environment = ?", current.ChannelAccountID, current.TenantID, "xiaohongshu", "active", "production").First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	var order model.Order
	if err := model.DB.Preload("Items.Product").Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ? AND channel = ? AND channel_account_id = ?", orderNo, current.TenantID, xiaohongshuPaymentMethod, account.ID).First(&order).Error; err != nil {
		return nil, err
	}
	var link model.XiaohongshuOrderLink
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ? AND miniapp_customer_id = ? AND order_id = ?", current.TenantID, account.ID, current.ID, order.ID).First(&link).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	// Derive codes and money only from a scoped sale read. The atomic helper
	// repeats and locks the financial selection before it writes.
	codes := make([]string, 0)
	for _, item := range order.Items {
		for _, ticket := range item.Tickets {
			codes = append(codes, ticket.TicketCode)
		}
	}
	if len(codes) == 0 {
		return nil, errors.New("order is not eligible for a refund application")
	}
	return &miniappRefundApplicationOwner{Customer: current, Account: account, TenantID: current.TenantID, AmountCents: moneyCents(order.TotalAmount), TicketCodes: codes}, nil
}

func miniappRefundApplicationKey(accountID, customerID uint, requestID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", accountID, customerID, requestID)))
	return miniappRefundApplicationKeyPrefix + hex.EncodeToString(sum[:])
}

// validateXiaohongshuCustomerRefundApplicationTx checks customer eligibility
// using sale-time policy. Both the read-only projection and the automatic
// refund transaction use it; the shared refund helper owns reservations/tasks.
func validateXiaohongshuCustomerRefundApplicationTx(tx *gorm.DB, customer *model.MiniappCustomer, account *model.ChannelAccount, order *model.Order, lockEvidence bool) ([]string, int64, error) {
	if tx == nil || customer == nil || account == nil || order == nil || customer.ID == 0 ||
		customer.TenantID != order.TenantID || customer.ChannelAccountID != account.ID ||
		order.Channel != xiaohongshuPaymentMethod || order.ChannelAccountID != account.ID ||
		order.Environment != "production" || order.Status != "paid" || len(order.Items) != 1 {
		return nil, 0, errors.New("order is not eligible for a refund application")
	}
	item := &order.Items[0]
	if item.Product.ProductKind == "hotel" || item.Quantity <= 0 || len(item.Tickets) == 0 {
		return nil, 0, errors.New("order is not eligible for a refund application")
	}
	var packageFootprint int64
	if err := tx.Model(&model.ScenicHotelPackageEntitlement{}).Where("order_id = ? AND deleted_at IS NULL", order.ID).Count(&packageFootprint).Error; err != nil {
		return nil, 0, err
	}
	if packageFootprint != 0 {
		return nil, 0, errors.New("package order is not eligible for a refund application")
	}
	if err := tx.Table("scenic_hotel_packages").Where("product_id = ? AND deleted_at IS NULL", item.ProductID).Count(&packageFootprint).Error; err != nil {
		return nil, 0, err
	}
	if packageFootprint != 0 {
		return nil, 0, errors.New("package order is not eligible for a refund application")
	}
	if lockEvidence {
		if err := EnsureNoXiaohongshuRefundHoldTx(tx, order); err != nil {
			return nil, 0, err
		}
	} else if held, err := HasXiaohongshuRefundAccountHoldTx(tx, order.TenantID, order.ChannelAccountID); err != nil || held {
		if err != nil {
			return nil, 0, err
		}
		return nil, 0, ErrXiaohongshuRefundHold
	}
	var link model.XiaohongshuOrderLink
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND order_id = ? AND miniapp_customer_id = ? AND state = ? AND voucher_issuance_status = ?", order.TenantID, account.ID, order.ID, customer.ID, "paid", "ready").First(&link).Error; err != nil {
		return nil, 0, errors.New("order voucher issuance is not complete")
	}
	if !lockEvidence {
		var orderHoldCount int64
		if err := tx.Model(&model.XiaohongshuRefundCoordination{}).Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND scope = ? AND state IN ? AND deleted_at IS NULL", order.TenantID, account.ID, link.ID, "order", []string{"received_unmapped", "order_held", "external_refund_confirmed"}).Count(&orderHoldCount).Error; err != nil {
			return nil, 0, err
		}
		if orderHoldCount != 0 {
			return nil, 0, ErrXiaohongshuRefundHold
		}
	}
	var original model.XiaohongshuOrderOperation
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND status = ?", order.TenantID, account.ID, link.ID, "completed").First(&original).Error; err != nil {
		return nil, 0, errors.New("order refund evidence is unavailable")
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(original.RequestPayloadCiphertext)
	if err != nil || payload.ProductType != xiaohongshu.ProductTypeGroupVoucher || len(payload.Request.Products) != 1 ||
		payload.Request.ExternalOrderID != link.ExternalOrderID || payload.Request.Products[0].Count != item.Quantity {
		return nil, 0, errors.New("order is not eligible for a refund application")
	}
	var payments []model.Payment
	if err := tx.Where("tenant_id = ? AND order_no = ? AND purpose = ?", order.TenantID, order.OrderNo, "order").Find(&payments).Error; err != nil {
		return nil, 0, err
	}
	if len(payments) != 1 || payments[0].Method != xiaohongshuPaymentMethod || payments[0].Status != "paid" || payments[0].AmountCents != moneyCents(order.TotalAmount) {
		return nil, 0, errors.New("order payment is not eligible for a refund application")
	}
	var priorRefunds int64
	if err := tx.Model(&model.Refund{}).Where("tenant_id = ? AND order_no = ? AND status IN ?", order.TenantID, order.OrderNo, []string{"pending", "failed", "succeeded", "group_succeeded", "group_failed"}).Count(&priorRefunds).Error; err != nil {
		return nil, 0, err
	}
	if priorRefunds != 0 {
		return nil, 0, errors.New("order has a refund requiring review")
	}
	codes := make([]string, 0, len(item.Tickets))
	for _, ticket := range item.Tickets {
		if ticket.PendingRefundID != 0 || ticket.PendingXiaohongshuVerificationID != 0 || ticket.Status != "unused" || ticket.CheckInCount != 0 {
			return nil, 0, errors.New("ticket is already used or being verified")
		}
		var voucher model.XiaohongshuVoucherLink
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND ticket_id = ?", order.TenantID, account.ID, link.ID, ticket.ID).First(&voucher).Error; err != nil || voucher.VerifyID != "" || voucher.Status != 1 {
			return nil, 0, errors.New("ticket voucher is not eligible for a refund application")
		}
		var activeVerifications int64
		if err := tx.Model(&model.XiaohongshuVoucherVerification{}).Where("voucher_link_id = ? AND state <> ?", voucher.ID, "external_rejected").Count(&activeVerifications).Error; err != nil {
			return nil, 0, err
		}
		if activeVerifications != 0 {
			return nil, 0, errors.New("ticket verification requires review")
		}
		codes = append(codes, ticket.TicketCode)
	}
	selected, amount, err := selectRefundTickets(order, codes, false, false, 0)
	if err != nil {
		return nil, 0, err
	}
	if len(selected) != len(item.Tickets) || moneyCents(amount) != payments[0].AmountCents || payload.Request.Price.OrderPrice != payments[0].AmountCents || payload.Request.Products[0].RealPrice != payments[0].AmountCents {
		return nil, 0, errors.New("order amount or tickets are not eligible for a refund application")
	}
	return codes, payments[0].AmountCents, nil
}

func ensureNoActiveXiaohongshuRefundApplicationTx(tx *gorm.DB, order *model.Order) error {
	if order == nil || order.Channel != xiaohongshuPaymentMethod {
		return nil
	}
	var count int64
	if err := tx.Model(&model.AfterSaleRequest{}).Where("tenant_id = ? AND order_no = ? AND type = ? AND status IN ?", order.TenantID, order.OrderNo, "refund", []string{"pending", "approved", "processing"}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("order already has a refund application in progress")
	}
	return nil
}

func miniappRefundApplicationMessage(err error) string {
	if err == nil {
		return "该订单可申请退款"
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrXiaohongshuRefundHold):
		return "订单售后状态待核查，请联系景区客服"
	case strings.Contains(message, "does not allow refunds"):
		return "该票种按购买时规则不支持退款"
	case strings.Contains(message, "already used") || strings.Contains(message, "being verified") || strings.Contains(message, "verification"):
		return "票券已核销或核销状态待确认，暂不能申请退款"
	case strings.Contains(message, "package") || strings.Contains(message, "hotel") || strings.Contains(message, "eligible"):
		return "该订单暂不支持自助退款，请联系景区客服"
	case strings.Contains(message, "refund"):
		return "订单已有退款处理记录，请联系景区客服"
	default:
		return "当前无法申请退款，请联系景区客服"
	}
}

func populateMiniappRefundApplicationProjection(result *MiniappOrderResult, link *model.XiaohongshuOrderLink, order *model.Order) error {
	if result == nil || link == nil || order == nil || order.Channel != xiaohongshuPaymentMethod {
		return nil
	}
	var request model.AfterSaleRequest
	err := model.DB.Where("tenant_id = ? AND order_no = ? AND type = ?", order.TenantID, order.OrderNo, "refund").Order("id DESC").First(&request).Error
	if err == nil && (request.Status == "pending" || request.Status == "approved" || request.Status == "processing") {
		result.RefundApplicationNo, result.RefundApplicationStatus = request.RequestNo, request.Status
		switch request.Status {
		case "pending":
			result.RefundApplicationMessage = "退款申请已提交，等待景区审核"
		case "approved":
			result.RefundApplicationMessage = "退款申请已通过，正在等待退款处理"
		default:
			result.RefundApplicationMessage = "退款正在处理中，请留意订单状态"
		}
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		result.RefundApplicationNo, result.RefundApplicationStatus = request.RequestNo, request.Status
		switch request.Status {
		case "rejected":
			result.RefundApplicationMessage = "退款申请未通过，可修改原因后重新申请"
		case "completed":
			result.RefundApplicationMessage = "退款已完成"
		case "failed":
			result.RefundApplicationMessage = "退款处理失败，请联系景区客服"
		default:
			result.RefundApplicationMessage = "退款申请已结束，请联系景区客服"
		}
	}
	if link.State != "paid" || link.VoucherIssuanceStatus != "ready" || order.Status != "paid" {
		if result.RefundApplicationMessage == "" {
			result.RefundApplicationMessage = "订单支付或票券签发尚未完成，暂不能申请退款"
		}
		return nil
	}

	// This is a read-only eligibility projection. Submission repeats the same
	// checks inside the shared payment-serialized refund transaction.
	var customer model.MiniappCustomer
	if err := model.DB.Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND status = ?", link.MiniappCustomerID, link.TenantID, link.ChannelAccountID, "active").First(&customer).Error; err != nil {
		result.RefundApplicationMessage = miniappRefundApplicationMessage(err)
		return nil
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status = ? AND environment = ?", link.ChannelAccountID, link.TenantID, "xiaohongshu", "active", "production").First(&account).Error; err != nil {
		result.RefundApplicationMessage = miniappRefundApplicationMessage(err)
		return nil
	}
	var detailed model.Order
	if err := model.DB.Preload("Items.Product").Preload("Items.Tickets").Where("id = ? AND tenant_id = ?", order.ID, order.TenantID).First(&detailed).Error; err != nil {
		return err
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		_, _, validationErr := validateXiaohongshuCustomerRefundApplicationTx(tx, &customer, &account, &detailed, false)
		return validationErr
	})
	result.CanApplyRefund = err == nil
	if result.RefundApplicationMessage == "" {
		result.RefundApplicationMessage = miniappRefundApplicationMessage(err)
	}
	return nil
}
