package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const exchangeDifferencePurpose = "exchange_difference"

func (s *AfterSaleService) CollectExchangeDifference(tenantID, requestID, actor uint, payment *model.Payment) error {
	if payment == nil || strings.TrimSpace(payment.IdempotencyKey) == "" {
		return errors.New("payment and idempotency key are required")
	}
	if payment.Method == "auto" {
		if payment.PayType != "bscanc" {
			return errors.New("automatic provider detection requires a payment auth code")
		}
		payment.Method = getProviderType(strings.TrimSpace(payment.AuthCode))
	}
	if payment.Method != "cash" && payment.Method != "wechat" && payment.Method != "alipay" {
		return errors.New("unsupported payment method")
	}
	if payment.Method == "cash" {
		payment.PayType = "cash"
	} else if payment.PayType != "bscanc" && payment.PayType != "cscanb" {
		return errors.New("unsupported payment type")
	}
	if payment.PayType == "bscanc" && strings.TrimSpace(payment.AuthCode) == "" {
		return errors.New("payment auth code is required")
	}

	replayed := false
	if err := model.Write(func(tx *gorm.DB) error {
		var req model.AfterSaleRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", requestID, tenantID).First(&req).Error; err != nil {
			return err
		}
		if req.Type != "exchange" || req.Status != "processing" || req.DifferenceCents <= 0 {
			return errors.New("after-sale request is not waiting for an exchange difference payment")
		}
		if req.DifferencePaymentID > 0 {
			var active model.Payment
			if err := tx.Where("id = ? AND tenant_id = ?", req.DifferencePaymentID, tenantID).First(&active).Error; err == nil && active.Status == "pending" && active.IdempotencyKey != payment.IdempotencyKey {
				return errors.New("an exchange difference payment is already pending")
			}
		}
		var existing model.Payment
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, payment.IdempotencyKey).First(&existing).Error; err == nil {
			if existing.Purpose != exchangeDifferencePurpose || existing.ReferenceNo != req.RequestNo || existing.AmountCents != req.DifferenceCents {
				return errors.New("payment idempotency key was used with different data")
			}
			*payment = existing
			replayed = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var order model.Order
		if err := tx.Where("order_no = ? AND tenant_id = ?", req.OrderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Channel == "window" {
			if payment.ShiftID == 0 || payment.DeviceID == 0 || actor == 0 {
				return errors.New("an open POS shift, device and operator are required for window payment")
			}
			var shift model.POSShift
			if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", payment.ShiftID, tenantID, payment.DeviceID, actor, "open").First(&shift).Error; err != nil {
				return errors.New("open POS shift not found")
			}
		}
		payment.Base = model.Base{}
		payment.TenantID = tenantID
		payment.PaymentNo = generatePaymentNo()
		payment.OrderNo = req.OrderNo
		payment.Purpose = exchangeDifferencePurpose
		payment.ReferenceNo = req.RequestNo
		payment.AmountCents = req.DifferenceCents
		payment.Amount = centsMoney(req.DifferenceCents)
		payment.OperatorID = actor
		payment.Status = "pending"
		if payment.Method == "cash" {
			if payment.TenderedCents == 0 {
				payment.TenderedCents = payment.AmountCents
			}
			if payment.TenderedCents < payment.AmountCents {
				return ErrCashTenderInsufficient
			}
			payment.ChangeCents = payment.TenderedCents - payment.AmountCents
		}
		if err := tx.Create(payment).Error; err != nil {
			return err
		}
		req.DifferencePaymentID = payment.ID
		req.DifferenceStatus = "payment_pending"
		if err := tx.Model(&req).Updates(map[string]interface{}{"difference_payment_id": payment.ID, "difference_status": req.DifferenceStatus}).Error; err != nil {
			return err
		}
		return appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_payment_started", actor, fmt.Sprintf("collect %d cents by %s", payment.AmountCents, payment.Method))
	}); err != nil {
		return err
	}
	if replayed {
		return nil
	}

	var providerErr error
	switch payment.Method {
	case "cash":
		payment.Status = "paid"
		payment.TransactionID = "CASH_" + payment.PaymentNo
	case "wechat":
		providerErr = (&PaymentService{}).payWeChat(payment)
	case "alipay":
		providerErr = (&PaymentService{}).payAlipay(payment)
	}
	if providerErr != nil {
		payment.Status = "failed"
		if providerRequestMayHaveBeenAccepted(payment.Method, providerErr) {
			payment.Status = "pending"
		}
		payment.ErrorMessage = providerErr.Error()
		_ = model.Write(func(tx *gorm.DB) error {
			if err := tx.Model(payment).Updates(map[string]interface{}{"status": payment.Status, "error_message": payment.ErrorMessage}).Error; err != nil {
				return err
			}
			if payment.Status == "pending" {
				return nil
			}
			var req model.AfterSaleRequest
			if err := tx.Where("tenant_id = ? AND request_no = ? AND difference_payment_id = ?", payment.TenantID, payment.ReferenceNo, payment.ID).First(&req).Error; err != nil {
				return err
			}
			req.DifferenceStatus = "payment_required"
			if err := tx.Model(&req).Update("difference_status", req.DifferenceStatus).Error; err != nil {
				return err
			}
			return appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_payment_failed", payment.OperatorID, providerErr.Error())
		})
		if payment.Status == "pending" {
			_ = (&PaymentService{}).enqueuePaymentTask(payment)
		}
		return providerErr
	}
	if payment.Status == "paid" {
		return (&PaymentService{}).completePayment(payment)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		return tx.Model(payment).Updates(map[string]interface{}{"status": payment.Status, "transaction_id": payment.TransactionID, "code_url": payment.CodeURL}).Error
	}); err != nil {
		return err
	}
	return (&PaymentService{}).enqueuePaymentTask(payment)
}

func completeExchangeDifferencePaymentTx(tx *gorm.DB, payment *model.Payment) error {
	var req model.AfterSaleRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"tenant_id = ? AND request_no = ?", payment.TenantID, payment.ReferenceNo,
	).First(&req).Error; err != nil {
		return err
	}
	if req.Status == "completed" && req.DifferenceStatus == "settled" {
		return nil
	}
	if req.Status != "processing" || req.DifferencePaymentID != payment.ID || req.DifferenceCents != payment.AmountCents {
		return errors.New("exchange difference payment does not match the active after-sale request")
	}
	now := time.Now()
	if err := tx.Model(payment).Updates(map[string]interface{}{
		"status": "paid", "transaction_id": payment.TransactionID, "code_url": payment.CodeURL, "paid_at": now,
	}).Error; err != nil {
		return err
	}
	if err := executeExchangeTx(tx, &req); err != nil {
		return err
	}
	req.DifferenceStatus = "settled"
	if err := tx.Model(&req).Update("difference_status", req.DifferenceStatus).Error; err != nil {
		return err
	}
	if err := appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_payment_completed", payment.OperatorID, fmt.Sprintf("collected %d cents", payment.AmountCents)); err != nil {
		return err
	}
	return completeAfterSaleTx(tx, &req, payment.OperatorID)
}

func failExchangeDifferencePaymentTx(tx *gorm.DB, payment *model.Payment, reason string) error {
	var req model.AfterSaleRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"tenant_id = ? AND request_no = ? AND status = ?", payment.TenantID, payment.ReferenceNo, "processing",
	).First(&req).Error; err != nil {
		return err
	}
	if req.DifferencePaymentID != payment.ID {
		return nil
	}
	req.DifferenceStatus = "payment_required"
	if err := tx.Model(&req).Update("difference_status", req.DifferenceStatus).Error; err != nil {
		return err
	}
	return appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_payment_failed", payment.OperatorID, strings.TrimSpace(reason))
}

func createExchangeDifferenceRefundTx(tx *gorm.DB, req *model.AfterSaleRequest, amountCents int64) (*model.Refund, error) {
	if amountCents <= 0 {
		return nil, errors.New("exchange refund difference must be positive")
	}
	idempotencyKey := "exchange-difference:" + req.RequestNo
	var existing model.Refund
	if err := tx.Where("tenant_id = ? AND idempotency_key = ?", req.TenantID, idempotencyKey).First(&existing).Error; err == nil {
		if existing.AmountCents != amountCents || existing.ReferenceNo != req.RequestNo {
			return nil, errors.New("exchange difference refund conflicts with existing facts")
		}
		return &existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var payments []model.Payment
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"tenant_id = ? AND order_no = ? AND status IN ? AND (purpose = ? OR purpose = '')",
		req.TenantID, req.OrderNo, []string{"paid", "partial_refunded"}, "order",
	).Order("created_at ASC, id ASC").Find(&payments).Error; err != nil {
		return nil, err
	}
	if len(payments) == 0 {
		return nil, errors.New("paid payment not found for exchange difference refund")
	}

	root := model.Refund{
		TenantID: req.TenantID, RefundNo: generateRefundNo(), IdempotencyKey: idempotencyKey,
		OrderNo: req.OrderNo, Purpose: exchangeDifferencePurpose, ReferenceNo: req.RequestNo,
		Amount: centsMoney(amountCents), AmountCents: amountCents, Method: "mixed", Status: "group_pending",
		Reason: strings.TrimSpace(req.Reason), TicketCodesJSON: "[]",
	}
	if err := tx.Create(&root).Error; err != nil {
		return nil, err
	}

	remaining := amountCents
	digitalAllocations := 0
	allocationSeq := 0
	for index := range payments {
		payment := &payments[index]
		paymentCents := payment.AmountCents
		if paymentCents == 0 {
			paymentCents = moneyCents(payment.Amount)
		}
		refundedCents := payment.RefundedAmountCents
		if refundedCents == 0 {
			refundedCents = moneyCents(payment.RefundedAmount)
		}
		var pendingCents int64
		if err := tx.Model(&model.Refund{}).Where("payment_id = ? AND status = ?", payment.ID, "pending").Select("COALESCE(SUM(amount_cents), 0)").Scan(&pendingCents).Error; err != nil {
			return nil, err
		}
		available := paymentCents - refundedCents - pendingCents
		if available <= 0 {
			continue
		}
		allocated := available
		if allocated > remaining {
			allocated = remaining
		}
		allocationSeq++
		status := "pending"
		if payment.Method == "cash" {
			status = "succeeded"
		} else if payment.Method == "wechat" || payment.Method == "alipay" {
			digitalAllocations++
		} else {
			return nil, fmt.Errorf("unsupported refund payment method %s", payment.Method)
		}
		allocation := model.Refund{
			TenantID: req.TenantID, RefundNo: generateRefundNo(), IdempotencyKey: fmt.Sprintf("exchange-difference:%d:%d", root.ID, allocationSeq),
			OrderNo: req.OrderNo, Purpose: exchangeDifferencePurpose, ReferenceNo: req.RequestNo,
			PaymentID: payment.ID, ParentRefundID: root.ID, AllocationSeq: allocationSeq,
			Amount: centsMoney(allocated), AmountCents: allocated, Method: payment.Method, Status: status,
			Reason: strings.TrimSpace(req.Reason), TicketCodesJSON: "[]",
		}
		if err := tx.Create(&allocation).Error; err != nil {
			return nil, err
		}
		if payment.Method == "cash" {
			if err := applyRefundPaymentFactTx(tx, payment, &allocation); err != nil {
				return nil, err
			}
		} else if err := tx.Create(&model.DigitalRefundTask{
			RefundID: allocation.ID, TenantID: req.TenantID, Provider: payment.Method,
			PaymentNo: payment.PaymentNo, Status: "pending", MaxAttempts: defaultDigitalRefundMaxAttempts,
			NextAttemptAt: ptrTime(time.Now()),
		}).Error; err != nil {
			return nil, err
		}
		remaining -= allocated
		if remaining == 0 {
			break
		}
	}
	if remaining != 0 {
		return nil, errors.New("exchange difference refund exceeds the remaining paid balance")
	}
	if digitalAllocations == 0 {
		root.Status = "group_succeeded"
		if err := tx.Model(&root).Update("status", root.Status).Error; err != nil {
			return nil, err
		}
	}
	return &root, nil
}

func completeExchangeDifferenceRefundTx(tx *gorm.DB, root *model.Refund) error {
	if err := tx.Model(root).Update("status", "group_succeeded").Error; err != nil {
		return err
	}
	var req model.AfterSaleRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"tenant_id = ? AND request_no = ? AND status = ?", root.TenantID, root.ReferenceNo, "processing",
	).First(&req).Error; err != nil {
		return err
	}
	req.DifferenceStatus = "settled"
	if err := tx.Model(&req).Update("difference_status", req.DifferenceStatus).Error; err != nil {
		return err
	}
	if err := appendAfterSaleEvent(tx, &req, "processing", "processing", "difference_refund_completed", req.OperatorID, root.Reason); err != nil {
		return err
	}
	return completeAfterSaleTx(tx, &req, req.OperatorID)
}
