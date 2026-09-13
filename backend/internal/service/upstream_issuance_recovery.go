package service

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

// RecoverIssuance never sends a provider order itself. It checks the ORIGINAL
// order, then queues query recovery or an explicitly approved same-number send.
func (w *UpstreamSupplyWorker) RecoverIssuance(ctx context.Context, tenantID, userID uint, orderNo, reason string, confirmedAbsent bool) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("请填写出票恢复原因")
	}
	if err := requireActiveScenicSupplier(model.DB, tenantID); err != nil {
		return err
	}
	var user model.User
	if err := model.DB.Where("id = ? AND tenant_id = ? AND is_initial_admin = true", userID, tenantID).First(&user).Error; err != nil {
		return errors.New("出票恢复仅限本景区初始管理员")
	}
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND order_no = ? AND status = 'paid'", tenantID, orderNo).First(&order).Error; err != nil {
		return errors.New("只有已付款且未退票的订单可以恢复出票")
	}
	var snapshot model.OrderItemSupplySnapshot
	lease := time.Now().Truncate(time.Microsecond)
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("sales_tenant_id = ? AND fulfillment_tenant_id = ? AND order_id = ? AND mode = 'upstream' AND issue_status = 'pending' AND cancel_status = '' AND refund_id = 0", tenantID, tenantID, order.ID).First(&snapshot).Error; err != nil {
			return errors.New("此订单没有可恢复的上游出票任务")
		}
		if snapshot.LockedAt != nil && snapshot.LockedAt.After(lease.Add(-2*time.Minute)) {
			return errors.New("出票任务正在运行，请稍后操作")
		}
		return tx.Model(&snapshot).Update("locked_at", lease).Error
	})
	if err != nil {
		return err
	}
	defer model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND locked_at = ?", snapshot.ID, lease).Update("locked_at", nil).Error
	})
	var connection model.UpstreamConnection
	if err = model.DB.Where("id = ? AND tenant_id = ? AND provider = ? AND environment = ?", snapshot.ConnectionID, tenantID, snapshot.Provider, snapshot.Environment).First(&connection).Error; err != nil {
		return err
	}
	factory := w.NewClient
	if factory == nil {
		factory = newUpstreamClient
	}
	client, err := factory(connection)
	if err != nil {
		return err
	}
	_, _, queryErr := client.QueryOrder(ctx, order.OrderNo)
	resend := errors.Is(queryErr, zyb.ErrOrderNotFound)
	if queryErr != nil && !resend {
		return queryErr
	}
	if resend && (!confirmedAbsent || snapshot.ProviderOrderCode != "" || snapshot.ProviderSubOrderCode != "") {
		return errors.New("未确认供应商未成单，或已存在供应商订单身份，不能重新发码")
	}
	return model.Write(func(tx *gorm.DB) error {
		var item model.OrderItem
		if err := tx.Where("id = ? AND order_id = ?", snapshot.OrderItemID, order.ID).First(&item).Error; err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND order_item_id = ? AND tenant_id = ?", order.ID, item.ID, tenantID).Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		if err := upstreamTicketsPending(&item, tickets); err != nil {
			return err
		}
		updates := map[string]interface{}{"next_attempt_at": time.Now(), "last_error": ""}
		if resend {
			updates["issue_attempted_at"] = nil
		} else if snapshot.IssueAttemptedAt == nil {
			updates["issue_attempted_at"] = lease
		}
		r := tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND locked_at = ? AND issue_status = 'pending' AND cancel_status = '' AND refund_id = 0", snapshot.ID, lease).Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return errors.New("出票任务已变化，请刷新后重试")
		}
		return recordAuditTx(tx, userID, tenantID, user.Role, "tenant", "upstream.issue.recover", "order", order.ID, reason, "", "")
	})
}
