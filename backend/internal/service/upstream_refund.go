package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/zyb"
	"time"

	"gorm.io/gorm"
)

var (
	ErrUpstreamRefundPending  = errors.New("upstream refund cancellation is pending")
	ErrUpstreamRefundRejected = errors.New("upstream refund cancellation was rejected")
	ErrUpstreamRefundUnknown  = errors.New("upstream refund provider status is unknown")
	ErrUpstreamRefundUsed     = errors.New("upstream ticket has been used and requires supplier confirmation")
)

// UpstreamRefundClient is deliberately smaller than the full supplier client.
// Implementations must perform network calls outside a database transaction.
type UpstreamRefundClient interface {
	QueryOrder(context.Context, string) (*zyb.QueryOrderResult, []byte, error)
	CancelOrder(context.Context, string) (*zyb.CancelOrderResult, []byte, error)
	QueryRefund(context.Context, string) (*zyb.RefundResult, []byte, error)
}

// UpstreamRefundClientFactory permits deterministic protocol fakes in service
// tests without making the refund state machine depend on HTTP.
type UpstreamRefundClientFactory func(*model.UpstreamConnection) (UpstreamRefundClient, error)

func (s *RefundService) upstreamRefundClient(conn *model.UpstreamConnection) (UpstreamRefundClient, error) {
	if s != nil && s.NewUpstreamRefundClient != nil {
		return s.NewUpstreamRefundClient(conn)
	}
	if conn == nil || conn.Provider != "zhiyoubao" {
		return nil, errors.New("upstream refund provider is not configured")
	}
	key, err := utils.DecryptAES(conn.PrivateKeyCiphertext)
	if err != nil {
		return nil, fmt.Errorf("upstream credential decrypt failed: %w", err)
	}
	return zyb.Client{Config: zyb.Config{Endpoint: conn.Endpoint, CorpCode: conn.CorpCode, Username: conn.Username, PrivateKey: key}}, nil
}

func upstreamRefundRequiredTx(tx *gorm.DB, orderID uint, selected map[string]*model.Ticket) (bool, error) {
	if tx == nil || orderID == 0 || len(selected) == 0 {
		return false, nil
	}
	itemIDs := make([]uint, 0)
	for _, ticket := range selected {
		if ticket != nil && ticket.OrderItemID != 0 {
			itemIDs = append(itemIDs, ticket.OrderItemID)
		}
	}
	if len(itemIDs) == 0 {
		return false, nil
	}
	var count int64
	if err := tx.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ? AND order_item_id IN ? AND mode = ?", orderID, itemIDs, "upstream").Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		// The provider cancellation is whole-order. Never cancel every external
		// code while refunding/locking only a subset of the local tickets.
		var tickets []model.Ticket
		if err := tx.Where("order_id = ?", orderID).Find(&tickets).Error; err != nil {
			return false, err
		}
		if len(tickets) != len(selected) {
			return false, errors.New("供应商供票订单暂仅支持整单退票，请选择全部门票")
		}
		for _, ticket := range tickets {
			if chosen := selected[ticket.TicketCode]; chosen == nil || chosen.ID != ticket.ID {
				return false, errors.New("供应商供票订单退票范围不完整")
			}
		}
	}
	return count > 0, nil
}

func authorizeUpstreamRefundTx(tx *gorm.DB, actor RefundActor, order *model.Order, selected map[string]*model.Ticket) error {
	if !actor.ConfirmUpstreamRefund {
		return errors.New("upstream used or unknown refund requires explicit confirmation")
	}
	if actor.TenantID == 0 || actor.UserID == 0 || order == nil {
		return errors.New("upstream used or unknown refund requires the scenic supplier initial administrator")
	}
	for _, ticket := range selected {
		if ticket == nil || ticket.FulfillmentTenantID == 0 {
			continue
		}
		if ticket.FulfillmentTenantID != actor.TenantID {
			return errors.New("upstream refund confirmation must be performed by the fulfillment supplier")
		}
		if actor.OrderTenantID != 0 && actor.OrderTenantID != order.TenantID {
			return errors.New("upstream refund confirmation order scope is invalid")
		}
	}
	var user model.User
	if err := tx.Where("id = ? AND tenant_id = ? AND is_initial_admin = ?", actor.UserID, actor.TenantID, true).First(&user).Error; err != nil {
		return errors.New("upstream used or unknown refund requires the scenic supplier initial administrator")
	}
	if err := requireActiveTenantCapability(tx, actor.TenantID, "supplier"); err != nil {
		return errors.New("upstream refund requires an active scenic supplier")
	}
	return nil
}

func (s *RefundService) upstreamRefundRequired(refund *model.Refund) (bool, error) {
	if refund == nil {
		return false, nil
	}
	var order model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", refund.OrderNo, refund.TenantID).First(&order).Error; err != nil {
		return false, err
	}
	var snapshots []model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ? AND mode = ?", order.ID, "upstream").Find(&snapshots).Error; err != nil {
		return false, err
	}
	return len(snapshots) > 0, nil
}

func updateUpstreamSnapshot(id uint, updates map[string]interface{}) error {
	if id == 0 || len(updates) == 0 {
		return nil
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND mode = ?", id, "upstream").Updates(updates).Error
	})
}

func providerState(order *zyb.QueryOrderResult) (used, known bool) {
	if order == nil {
		return false, false
	}
	if len(order.Tickets) == 0 {
		return false, false
	}
	for _, item := range order.Tickets {
		value := strings.TrimSpace(item.CheckedQuantity)
		if value == "" {
			return false, false
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return false, false
		}
		if n > 0 {
			used = true
		}
	}
	return used, true
}

func queryUpstreamUsage(ctx context.Context, client UpstreamRefundClient, orderNo string, snapshot *model.OrderItemSupplySnapshot, order *zyb.QueryOrderResult) (bool, bool) {
	checker, ok := client.(interface {
		QueryCheckStatus(context.Context, string, ...string) (*zyb.CheckStatusResult, error)
	})
	if !ok {
		return providerState(order)
	}
	result, err := checker.QueryCheckStatus(ctx, orderNo)
	if err != nil || result == nil || len(result.SubOrders) == 0 {
		return false, false
	}
	used := false
	for _, r := range result.SubOrders {
		if !upstreamCheckChildMatches(r.OrderCode, snapshot, orderNo) {
			return false, false
		}
		checked, e := strconv.Atoi(r.AlreadyCheckNum)
		returned, re := strconv.Atoi(r.ReturnNum)
		if e != nil || re != nil || checked < 0 || returned != 0 {
			return false, false
		}
		if checked > 0 || r.CheckStatus == "checked" {
			used = true
		} else if r.CheckStatus != "un_check" {
			return false, false
		}
	}
	return used, true
}

// processUpstreamRefundCancellation performs query -> whole-order cancel ->
// cancel-status confirmation. It never runs under a DB transaction. A
// submitted cancel with no batch number is intentionally parked as pending so
// an ambiguous network result cannot cause a second cancellation request.
func (s *RefundService) processUpstreamRefundCancellation(ctx context.Context, refund *model.Refund) error {
	if refund == nil || refund.ID == 0 {
		return errors.New("退款尚未持久保存，不能取消供应商订单")
	}
	root := *refund
	if refund.ParentRefundID != 0 {
		root = model.Refund{}
		if err := model.DB.Where("id = ? AND tenant_id = ?", refund.ParentRefundID, refund.TenantID).First(&root).Error; err != nil {
			return err
		}
	}
	var order model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", root.OrderNo, root.TenantID).First(&order).Error; err != nil {
		return err
	}
	var snapshots []model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ? AND sales_tenant_id = ? AND mode = 'upstream'", order.ID, order.TenantID).Find(&snapshots).Error; err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		approved := root.AuthorizedUpstreamRefund || root.AuthorizedUsedRefund
		if snapshot.CancelStatus == "succeeded" || snapshot.CancelStatus == "override" {
			if snapshot.RefundID != root.ID {
				return ErrUpstreamRefundUnknown
			}
			continue
		}
		// Refunds reserve the same ticket used by issuance before any HTTP call.
		var tickets []model.Ticket
		if err := model.DB.Where("order_id = ? AND order_item_id = ? AND tenant_id = ?", order.ID, snapshot.OrderItemID, order.TenantID).Find(&tickets).Error; err != nil {
			return err
		}
		var itemForCodes model.OrderItem
		if err := model.DB.Where("id = ? AND order_id = ?", snapshot.OrderItemID, order.ID).First(&itemForCodes).Error; err != nil {
			return err
		}
		if _, err := upstreamTicketCount(&itemForCodes, tickets); err != nil {
			return err
		}
		for _, ticket := range tickets {
			if ticket.PendingRefundID != root.ID {
				return errors.New("上游退款缺少票券锁定")
			}
		}
		if snapshot.RefundID != 0 && snapshot.RefundID != root.ID {
			return ErrUpstreamRefundUnknown
		}
		if snapshot.RefundID == 0 {
			if err := updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"refund_id": root.ID}); err != nil {
				return err
			}
		}
		if snapshot.IssueAttemptedAt == nil && snapshot.ProviderOrderCode == "" {
			var changed int64
			err := model.Write(func(tx *gorm.DB) error {
				r := tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND issue_attempted_at IS NULL AND cancel_status = ''", snapshot.ID).Updates(map[string]interface{}{"cancel_status": "succeeded", "refund_id": root.ID})
				changed = r.RowsAffected
				return r.Error
			})
			if err != nil {
				return err
			}
			if changed == 1 {
				continue
			}
			return ErrUpstreamRefundPending
		}
		bypass := func() error {
			if !approved {
				return ErrUpstreamRefundUnknown
			}
			return updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"cancel_status": "override", "refund_id": root.ID, "refund_override_approved": true, "last_error": "管理员特殊退款；不代表供应商已取消"})
		}
		var conn model.UpstreamConnection
		if err := model.DB.Where("id = ? AND tenant_id = ? AND provider = ? AND environment = ?", snapshot.ConnectionID, snapshot.FulfillmentTenantID, snapshot.Provider, snapshot.Environment).First(&conn).Error; err != nil {
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		client, err := s.upstreamRefundClient(&conn)
		if err != nil {
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		checkBatch := func(batch string) error {
			result, _, err := client.QueryRefund(ctx, batch)
			if err != nil || result == nil || result.Pending {
				return ErrUpstreamRefundPending
			}
			if result.Rejected {
				if approved {
					return bypass()
				}
				if e := updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"cancel_status": "failed", "last_error": result.Description}); e != nil {
					return e
				}
				return ErrUpstreamRefundRejected
			}
			if !result.Completed {
				return ErrUpstreamRefundPending
			}
			return updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"cancel_status": "succeeded", "provider_status": "refunded", "refund_id": root.ID, "last_synced_at": time.Now(), "last_error": ""})
		}
		if snapshot.CancelBatchNo != "" {
			if err := checkBatch(snapshot.CancelBatchNo); err != nil {
				return err
			}
			continue
		}
		query, _, err := client.QueryOrder(ctx, order.OrderNo)
		if err != nil || query == nil || len(query.Tickets) != 1 || query.Tickets[0].GoodsCode != snapshot.ExternalProductCode || (snapshot.ProviderOrderCode != "" && query.ProviderOrderCode != snapshot.ProviderOrderCode) {
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		var item model.OrderItem
		if err := model.DB.Where("id = ? AND order_id = ?", snapshot.OrderItemID, order.ID).First(&item).Error; err != nil {
			return err
		}
		if !upstreamQueryTicketMatches(&snapshot, &item, order.OrderNo, query.Tickets[0]) {
			return ErrUpstreamRefundUnknown
		}
		if snapshot.ProviderOrderCode == "" || snapshot.ProviderSubOrderCode == "" {
			if query.ProviderOrderCode == "" {
				return ErrUpstreamRefundUnknown
			}
			identityUpdates := map[string]interface{}{"provider_order_code": query.ProviderOrderCode}
			if query.Tickets[0].ProviderSubOrderCode != "" {
				identityUpdates["provider_sub_order_code"] = query.Tickets[0].ProviderSubOrderCode
			}
			if err := updateUpstreamSnapshot(snapshot.ID, identityUpdates); err != nil {
				return err
			}
			snapshot.ProviderOrderCode = query.ProviderOrderCode
			if query.Tickets[0].ProviderSubOrderCode != "" {
				snapshot.ProviderSubOrderCode = query.Tickets[0].ProviderSubOrderCode
			}
		}
		ticket := query.Tickets[0]
		quantity, qErr := strconv.Atoi(ticket.Quantity)
		returned, rErr := strconv.Atoi(ticket.ReturnedQuantity)
		if ticket.ReturnedQuantity == "" {
			returned = 0
			rErr = nil
		}
		if qErr != nil || rErr != nil || quantity <= 0 || returned < 0 || returned > quantity {
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		if returned == quantity {
			if err := updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"cancel_status": "succeeded", "provider_status": "refunded", "refund_id": root.ID, "last_synced_at": time.Now()}); err != nil {
				return err
			}
			continue
		}
		if returned != 0 {
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		used, known := queryUpstreamUsage(ctx, client, order.OrderNo, &snapshot, query)
		if used || !known {
			if !approved {
				if used {
					return ErrUpstreamRefundUsed
				}
				return ErrUpstreamRefundUnknown
			}
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		if snapshot.CancelStatus == "failed" {
			if !approved {
				return ErrUpstreamRefundRejected
			}
			if e := bypass(); e != nil {
				return e
			}
			continue
		}
		if snapshot.CancelStatus == "" {
			// A conditional durable claim also serializes mixed-payment workers.
			var claimed int64
			err := model.Write(func(tx *gorm.DB) error {
				r := tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND cancel_status = '' AND (refund_id = 0 OR refund_id = ?)", snapshot.ID, root.ID).
					Updates(map[string]interface{}{"cancel_status": "submitted", "cancel_attempted_at": time.Now(), "refund_id": root.ID})
				claimed = r.RowsAffected
				return r.Error
			})
			if err != nil {
				return err
			}
			if claimed != 1 {
				return ErrUpstreamRefundPending
			}
			cancel, _, err := client.CancelOrder(ctx, order.OrderNo)
			if err != nil || cancel == nil || cancel.RetreatBatchNo == "" {
				return ErrUpstreamRefundPending
			}
			if err = updateUpstreamSnapshot(snapshot.ID, map[string]interface{}{"cancel_batch_no": cancel.RetreatBatchNo}); err != nil {
				return err
			}
			snapshot.CancelBatchNo = cancel.RetreatBatchNo
		}
		if snapshot.CancelBatchNo == "" {
			if approved {
				if e := bypass(); e != nil {
					return e
				}
				continue
			}
			return ErrUpstreamRefundUnknown
		}
		if err := checkBatch(snapshot.CancelBatchNo); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefundService) preflightUpstreamRefund(ctx context.Context, tenantID uint, orderNo string) error {
	var snapshots []model.OrderItemSupplySnapshot
	var order model.Order
	if err := model.DB.Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
		return err
	}
	if err := model.DB.Where("order_id = ? AND mode = ?", order.ID, "upstream").Find(&snapshots).Error; err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return nil
	}
	for _, snapshot := range snapshots {
		if snapshot.IssueAttemptedAt == nil && snapshot.ProviderOrderCode == "" {
			continue
		}
		var conn model.UpstreamConnection
		if err := model.DB.Where("id = ? AND tenant_id = ? AND provider = ? AND environment = ?", snapshot.ConnectionID, snapshot.FulfillmentTenantID, snapshot.Provider, snapshot.Environment).First(&conn).Error; err != nil {
			return ErrUpstreamRefundUnknown
		}
		client, err := s.upstreamRefundClient(&conn)
		if err != nil {
			return ErrUpstreamRefundUnknown
		}
		query, _, err := client.QueryOrder(ctx, order.OrderNo)
		if err != nil {
			return fmt.Errorf("%w: 供应商查单失败：%v", ErrUpstreamRefundUnknown, err)
		}
		if query == nil || len(query.Tickets) != 1 || query.Tickets[0].GoodsCode != snapshot.ExternalProductCode || (snapshot.ProviderOrderCode != "" && query.ProviderOrderCode != snapshot.ProviderOrderCode) {
			return fmt.Errorf("%w: 供应商主订单或商品关联不匹配", ErrUpstreamRefundUnknown)
		}
		var item model.OrderItem
		if err := model.DB.Where("id = ? AND order_id = ?", snapshot.OrderItemID, order.ID).First(&item).Error; err != nil {
			return err
		}
		if !upstreamQueryTicketMatches(&snapshot, &item, order.OrderNo, query.Tickets[0]) {
			return fmt.Errorf("%w: 供应商子单或数量关联不匹配", ErrUpstreamRefundUnknown)
		}
		used, known := queryUpstreamUsage(ctx, client, order.OrderNo, &snapshot, query)
		if !known {
			return ErrUpstreamRefundUnknown
		}
		if used {
			return ErrUpstreamRefundUsed
		}
		if n, e := strconv.Atoi(query.Tickets[0].ReturnedQuantity); query.Tickets[0].ReturnedQuantity != "" && (e != nil || n != 0) {
			return ErrUpstreamRefundUnknown
		}
	}
	return nil
}
