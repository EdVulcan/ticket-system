package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A replacement is permitted only for a terminal failed XHS operation whose
// supplier cancellation and ticket hold have moved to this new refund atomically.
func validateUpstreamRefundReplacementTx(tx *gorm.DB, order *model.Order, payment *model.Payment, refund *model.Refund) error {
	deny := errors.New("该订单已有失败的小红书退款，请从上游订单的款项恢复入口处理")
	if !strings.HasPrefix(refund.ReferenceNo, "UPR:") {
		return deny
	}
	var previous model.Refund
	if err := tx.Where("refund_no = ? AND tenant_id = ? AND order_no = ? AND payment_id = ? AND method = 'xiaohongshu' AND status = 'failed' AND parent_refund_id = 0", strings.TrimPrefix(refund.ReferenceNo, "UPR:"), order.TenantID, order.OrderNo, payment.ID).First(&previous).Error; err != nil {
		return deny
	}
	if previous.AmountCents != refund.AmountCents || previous.TicketCodesJSON != refund.TicketCodesJSON {
		return deny
	}
	var count int64
	if err := tx.Model(&model.XiaohongshuRefundOperation{}).Where("refund_id = ? AND tenant_id = ? AND state = 'failed'", previous.ID, order.TenantID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return deny
	}
	if err := tx.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ? AND sales_tenant_id = ? AND mode = 'upstream' AND refund_id = ? AND cancel_status IN ('succeeded','override')", order.ID, order.TenantID, refund.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return deny
	}
	return nil
}

func (s *RefundService) RecoverUpstreamFunding(actor RefundActor, refundID uint, reason string) (*model.Refund, error) {
	if actor.TenantID == 0 || actor.UserID == 0 || refundID == 0 || strings.TrimSpace(reason) == "" {
		return nil, errors.New("款项恢复需要管理员及处理原因")
	}
	var user model.User
	if err := model.DB.Where("id = ? AND tenant_id = ? AND is_initial_admin = true", actor.UserID, actor.TenantID).First(&user).Error; err != nil {
		return nil, errors.New("款项恢复仅限本景区初始管理员")
	}
	var previous model.Refund
	if err := model.DB.Where("id = ? AND tenant_id = ? AND parent_refund_id = 0", refundID, refundOrderTenantID(actor)).First(&previous).Error; err != nil {
		return nil, err
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("sales_tenant_id = ? AND fulfillment_tenant_id = ? AND mode = 'upstream' AND refund_id = ? AND cancel_status IN ('succeeded','override')", previous.TenantID, actor.TenantID, previous.ID).First(&snapshot).Error; err != nil {
		// Repeated recovery returns its original successor, never a third refund.
		var successor model.Refund
		if e := model.DB.Where("tenant_id = ? AND idempotency_key = ? AND reference_no = ?", previous.TenantID, fmt.Sprintf("upstream-recover:%d", previous.ID), "UPR:"+previous.RefundNo).First(&successor).Error; e == nil {
			return &successor, nil
		}
		return nil, errors.New("未找到已完成上游退票的资金恢复记录")
	}
	if err := requireActiveScenicSupplier(model.DB, actor.TenantID); err != nil {
		return nil, err
	}
	if previous.Method == "mixed" {
		if err := s.ResolveMixedRefundGroup(previous.TenantID, previous.ID, actor.UserID, user.Role, "retry", reason); err != nil {
			return nil, err
		}
		return &previous, model.DB.First(&previous, previous.ID).Error
	}
	if previous.Method != "xiaohongshu" {
		var task model.DigitalRefundTask
		if err := model.DB.Where("tenant_id = ? AND refund_id = ?", previous.TenantID, previous.ID).First(&task).Error; err != nil {
			return nil, err
		}
		if err := s.RetryDigitalRefundTask(previous.TenantID, task.ID, actor.UserID, user.Role, reason); err != nil {
			return nil, err
		}
		return &previous, model.DB.First(&previous, previous.ID).Error
	}
	var result *model.Refund
	err := model.Write(func(tx *gorm.DB) error {
		// Same payment serializes ordinary creation, callbacks and this recovery.
		var payment model.Payment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", previous.PaymentID, previous.TenantID).First(&payment).Error; err != nil {
			return err
		}
		var successor model.Refund
		key := fmt.Sprintf("upstream-recover:%d", previous.ID)
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", previous.TenantID, key).First(&successor).Error; err == nil {
			if successor.OrderNo != previous.OrderNo || successor.PaymentID != previous.PaymentID || successor.ReferenceNo != "UPR:"+previous.RefundNo {
				return errors.New("恢复请求标识与原退款不匹配")
			}
			result = &successor
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var old model.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = 'failed'", previous.ID, previous.TenantID).First(&old).Error; err != nil {
			return errors.New("原退款未确认失败，不能创建新的退款请求")
		}
		var operation model.XiaohongshuRefundOperation
		if err := tx.Where("refund_id = ? AND tenant_id = ? AND state = 'failed'", old.ID, old.TenantID).First(&operation).Error; err != nil {
			return errors.New("小红书原售后结果尚未确认失败")
		}
		var order model.Order
		if err := tx.Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", old.OrderNo, old.TenantID).First(&order).Error; err != nil {
			return err
		}
		var codes []string
		if err := json.Unmarshal([]byte(old.TicketCodesJSON), &codes); err != nil {
			return err
		}
		selected, _, err := selectRefundTickets(&order, codes, old.AuthorizedUsedRefund, old.AuthorizedPolicyOverride, old.ID)
		if err != nil {
			return err
		}
		actor.ConfirmUpstreamRefund = true
		if err = authorizeUpstreamRefundTx(tx, actor, &order, selected); err != nil {
			return err
		}
		for _, ticket := range selected {
			if ticket.PendingRefundID != old.ID {
				return errors.New("原退款的票券锁定已变化")
			}
			r := tx.Model(&model.Ticket{}).Where("id = ? AND tenant_id = ? AND pending_refund_id = ?", ticket.ID, old.TenantID, old.ID).Update("pending_refund_id", 0)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return errors.New("票券锁定交接失败")
			}
		}
		// The temporary clear is invisible outside this transaction. Every
		// failure rolls it back; the existing helper immediately reserves anew.
		actor.upstreamRecoveryRefundID = old.ID
		actor.OverrideRefundPolicy = old.AuthorizedPolicyOverride
		actor.ConfirmUpstreamRefund = old.AuthorizedUpstreamRefund
		result, err = s.createDigitalRefundAsTx(tx, actor, old.OrderNo, key, centsMoney(old.AmountCents), codes, reason, nil)
		if err != nil {
			return err
		}
		return recordAuditTx(tx, actor.UserID, actor.TenantID, user.Role, "tenant", "upstream.refund.funding_recover", "refund", result.ID, reason, old.RefundNo, result.RefundNo)
	})
	return result, err
}
