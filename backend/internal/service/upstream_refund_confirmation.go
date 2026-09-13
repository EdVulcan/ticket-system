package service

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"ticket-backend/internal/model"
	"time"
)

func (s *RefundService) CheckUpstreamRefund(ctx context.Context, tenantID uint, orderNo string) error {
	return s.preflightUpstreamRefund(ctx, tenantID, orderNo)
}

// ConfirmUpstreamRefund resumes the existing held refund, never creates a
// second payment request and never grants a tourist the administrator exception.
func (s *RefundService) ConfirmUpstreamRefund(actor RefundActor, refundID uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("请填写确认退款的原因")
	}
	return model.Write(func(tx *gorm.DB) error {
		var refund model.Refund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND parent_refund_id = 0", refundID, refundOrderTenantID(actor)).First(&refund).Error; err != nil {
			return err
		}
		if refund.Status != "pending" && refund.Status != "group_pending" && refund.Status != "group_manual_review" {
			return errors.New("退款已结束，请从原退款任务处理")
		}
		var order model.Order
		if err := tx.Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", refund.OrderNo, refund.TenantID).First(&order).Error; err != nil {
			return err
		}
		var codes []string
		if err := json.Unmarshal([]byte(refund.TicketCodesJSON), &codes); err != nil {
			return err
		}
		selected, _, err := selectRefundTickets(&order, codes, refund.AuthorizedUsedRefund, refund.AuthorizedPolicyOverride, refund.ID)
		if err != nil {
			return err
		}
		actor.ConfirmUpstreamRefund = true
		if err = authorizeUpstreamRefundTx(tx, actor, &order, selected); err != nil {
			return err
		}
		has, err := upstreamRefundRequiredTx(tx, order.ID, selected)
		if err != nil {
			return err
		}
		if !has {
			return errors.New("此订单没有上游退款")
		}
		var tasks []model.DigitalRefundTask
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND (refund_id = ? OR refund_id IN (SELECT id FROM refunds WHERE tenant_id = ? AND parent_refund_id = ?))", refund.TenantID, refund.ID, refund.TenantID, refund.ID).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) == 0 {
			return errors.New("退款任务不存在")
		}
		for _, task := range tasks {
			if task.Status == "processing" {
				return errors.New("退款任务处理中，请稍后确认")
			}
		}
		if err = tx.Model(&refund).Updates(map[string]interface{}{"authorized_upstream_refund": true, "authorized_by": actor.UserID}).Error; err != nil {
			return err
		}
		for _, task := range tasks {
			if task.Status == "manual_review" || task.Status == "submitted" || task.Status == "pending" {
				if err = tx.Model(&task).Updates(map[string]interface{}{"status": "pending", "locked_at": nil, "next_attempt_at": time.Now(), "last_error": "", "attempt_count": 0}).Error; err != nil {
					return err
				}
			}
		}
		if refund.Status == "group_manual_review" {
			if err = tx.Model(&refund).Update("status", "group_pending").Error; err != nil {
				return err
			}
		}
		return recordAuditTx(tx, actor.UserID, actor.TenantID, "admin", "tenant", "upstream.refund.confirm", "refund", refund.ID, reason, "", "")
	})
}
