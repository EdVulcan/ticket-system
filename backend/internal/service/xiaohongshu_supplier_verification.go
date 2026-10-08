package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Supplier consumption is an external fact, not a local admission. This
// coordinator never writes Ticket status, CheckInRecord, inventory or ledgers.
type XiaohongshuSupplierVerificationService struct {
	NewClient func(string, string, string) *xiaohongshu.Client
}

var errSupplierVerificationHeld = errors.New("供应商核销回传待确认，不能重复核销或退款")

// Call while holding the Ticket row lock shared by device claims,
// supplier claims and refund reservations. Do not lock the other coordinator.
func ensureSupplierVerificationAllowsRefundTx(tx *gorm.DB, ticket *model.Ticket, authorizedUsedRefund bool) error {
	var task model.XiaohongshuSupplierVerification
	err := tx.Where("ticket_id = ? AND state <> 'skipped'", ticket.ID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Preserve the existing initial-administrator exception only when the
	// coordinator adopted a real, completed device admission. A supplier-only
	// consume does not manufacture that admission evidence.
	if authorizedUsedRefund && ticket.CheckInCount > 0 && task.State == "confirmed" && task.ReviewReason == "device_confirmed" {
		return nil
	}
	return errSupplierVerificationHeld
}

func ensureSupplierVerificationAllowsDeviceTx(tx *gorm.DB, ticket *model.Ticket, saga *model.XiaohongshuVoucherVerification, preparing bool) error {
	var task model.XiaohongshuSupplierVerification
	err := tx.Where("ticket_id = ? AND state <> 'skipped'", ticket.ID).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// A device that was already prepared when discovery ran may finish its
	// original claim. No new device request can steal an uncertain supplier call.
	if task.State == "pending" && task.ReviewReason == "device_hold" && task.AttemptCount == 0 && task.ExternalStartedAt == nil && task.VerifyID == "" && saga != nil && saga.State == "prepared" && saga.TicketID == ticket.ID && saga.TenantID == task.TenantID && saga.ChannelAccountID == task.ChannelAccountID && (ticket.PendingXiaohongshuVerificationID == saga.ID || (preparing && ticket.PendingXiaohongshuVerificationID == 0)) {
		return nil
	}
	return errSupplierVerificationHeld
}

func enqueueXiaohongshuSupplierVerification(snapshotID uint, now time.Time) error {
	return model.Write(func(tx *gorm.DB) error {
		var snapshot model.OrderItemSupplySnapshot
		if err := tx.Where("id = ? AND mode = 'upstream' AND provider = 'zhiyoubao' AND issue_status = 'ready' AND provider_status IN ?", snapshotID, []string{"checked", "checking"}).First(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var order model.Order
		if err := tx.Where("id = ? AND tenant_id = ? AND channel = 'xiaohongshu' AND status IN ?", snapshot.OrderID, snapshot.SalesTenantID, []string{"paid", "completed"}).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if snapshot.CancelStatus != "" || snapshot.RefundID != 0 {
			return nil
		}
		var tickets []model.Ticket
		if err := tx.Where("order_id = ? AND order_item_id = ? AND tenant_id = ?", order.ID, snapshot.OrderItemID, order.TenantID).Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		for _, ticket := range tickets {
			if ticket.PendingRefundID != 0 || ticket.Status == "refunded" {
				continue
			}
			row := model.XiaohongshuSupplierVerification{TenantID: order.TenantID, ChannelAccountID: order.ChannelAccountID, OrderID: order.ID, OrderItemID: ticket.OrderItemID, SupplySnapshotID: snapshot.ID, TicketID: ticket.ID, State: "pending", NextAttemptAt: &now}
			if snapshot.ProviderStatus == "checking" {
				row.State, row.ReviewReason, row.LastError = "manual_review", "partial_usage", "供应商仅部分核销，无法确定对应的小红书券，请人工核查"
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticket_id"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
			// A later complete supplier response or restored issuance may unblock
			// an unsent task. Never reopen a task that might have reached Xiaohongshu.
			if snapshot.ProviderStatus == "checked" {
				if err := tx.Model(&model.XiaohongshuSupplierVerification{}).Where("ticket_id = ? AND state = 'manual_review' AND attempt_count = 0 AND external_started_at IS NULL AND review_reason IN ?", ticket.ID, []string{"partial_usage", "missing_vouchers"}).Updates(map[string]interface{}{"state": "pending", "review_reason": "", "last_error": "", "next_attempt_at": now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s XiaohongshuSupplierVerificationService) discover(now time.Time, limit int) error {
	var ids []uint
	if err := model.DB.Model(&model.OrderItemSupplySnapshot{}).
		Joins("JOIN orders o ON o.id = order_item_supply_snapshots.order_id AND o.tenant_id = order_item_supply_snapshots.sales_tenant_id AND o.deleted_at IS NULL").
		Where("o.channel = 'xiaohongshu' AND o.status IN ? AND mode = 'upstream' AND provider = 'zhiyoubao' AND issue_status = 'ready' AND provider_status IN ? AND cancel_status = '' AND refund_id = 0", []string{"paid", "completed"}, []string{"checked", "checking"}).
		Where(`EXISTS (SELECT 1 FROM tickets t WHERE t.order_item_id = order_item_supply_snapshots.order_item_id AND t.order_id = o.id AND t.tenant_id = o.tenant_id AND t.deleted_at IS NULL AND t.status <> 'refunded' AND t.pending_refund_id = 0 AND NOT EXISTS (SELECT 1 FROM xiaohongshu_supplier_verifications v WHERE v.ticket_id = t.id))`).
		Order("order_item_supply_snapshots.id").Limit(limit).Pluck("order_item_supply_snapshots.id", &ids).Error; err != nil {
		return err
	}
	var result error
	for _, id := range ids {
		result = errors.Join(result, enqueueXiaohongshuSupplierVerification(id, now))
	}
	return result
}

func (s XiaohongshuSupplierVerificationService) ProcessPending(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if err := s.discover(now, limit); err != nil {
		return 0, err
	}
	// An abandoned claim is uncertain even when the process died before HTTP.
	// It must never turn back into an automatically resendable task.
	if err := model.DB.Model(&model.XiaohongshuSupplierVerification{}).Where("state = 'in_flight' AND external_started_at < ?", now.Add(-xiaohongshuVoucherVerificationLease)).Updates(map[string]interface{}{"state": "unknown", "review_reason": "external_unknown", "last_error": "小红书核销回传结果未知，请核对渠道记录", "next_attempt_at": nil}).Error; err != nil {
		return 0, err
	}
	var rows []model.XiaohongshuSupplierVerification
	if err := model.DB.Where("state = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", now).Order("next_attempt_at ASC NULLS FIRST, id").Limit(limit).Find(&rows).Error; err != nil {
		return 0, err
	}
	processed := 0
	var result error
	for _, row := range rows {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		if err := s.process(ctx, row.ID, now); err != nil {
			result = errors.Join(result, err)
		}
		processed++
	}
	return processed, result
}

func supplierTaskUpdate(tx *gorm.DB, task *model.XiaohongshuSupplierVerification, state, reason, message string, next *time.Time) error {
	return tx.Model(task).Updates(map[string]interface{}{"state": state, "review_reason": reason, "last_error": message, "next_attempt_at": next}).Error
}

// Lock own task -> Ticket. Order and other coordinators are read without locks.
// Device claims use own saga -> Ticket, so neither locks the other's saga.
func loadSupplierVerificationOwnershipTx(tx *gorm.DB, task *model.XiaohongshuSupplierVerification) (*model.Order, *model.Ticket, *model.OrderItemSupplySnapshot, error) {
	var order model.Order
	if err := tx.Where("id = ? AND tenant_id = ? AND channel = 'xiaohongshu' AND channel_account_id = ?", task.OrderID, task.TenantID, task.ChannelAccountID).First(&order).Error; err != nil {
		return nil, nil, nil, err
	}
	var ticket model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND order_id = ? AND order_item_id = ?", task.TicketID, task.TenantID, task.OrderID, task.OrderItemID).First(&ticket).Error; err != nil {
		return nil, nil, nil, err
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := tx.Where("id = ? AND sales_tenant_id = ? AND order_id = ? AND order_item_id = ? AND mode = 'upstream' AND provider = 'zhiyoubao' AND fulfillment_tenant_id = ? AND scenic_area_id = ?", task.SupplySnapshotID, task.TenantID, task.OrderID, task.OrderItemID, ticket.FulfillmentTenantID, ticket.FulfillmentScenicAreaID).First(&snapshot).Error; err != nil {
		return nil, nil, nil, err
	}
	return &order, &ticket, &snapshot, nil
}

func (s XiaohongshuSupplierVerificationService) process(ctx context.Context, id uint, now time.Time) error {
	var task model.XiaohongshuSupplierVerification
	var request xiaohongshu.VoucherVerifyRequest
	var client *xiaohongshu.Client
	claimed := false
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&task).Error; err != nil {
			return err
		}
		if task.State != "pending" {
			return nil
		}
		order, ticket, snapshot, err := loadSupplierVerificationOwnershipTx(tx, &task)
		if err != nil {
			return err
		}
		next := now.Add(time.Minute)
		if (order.Status != "paid" && order.Status != "completed") || ticket.Status == "refunded" || snapshot.CancelStatus != "" || snapshot.RefundID != 0 || ticket.PendingRefundID != 0 {
			return supplierTaskUpdate(tx, &task, "skipped", "refund", "订单已取消、退款或正在退款，停止核销回传", nil)
		}
		if err := EnsureNoXiaohongshuRefundHoldTx(tx, order); err != nil {
			return supplierTaskUpdate(tx, &task, "pending", "refund_hold", "小红书售后处理中，暂停核销回传", &next)
		}
		if snapshot.ProviderStatus != "checked" {
			return supplierTaskUpdate(tx, &task, "manual_review", "partial_usage", "供应商未完整核销，不能自动回传全部小红书券", nil)
		}
		if snapshot.ProviderUsageQuantity == nil || snapshot.ProviderUsedQuantity == nil || snapshot.ProviderReturnedQuantity == nil {
			if err := tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND sync_requested_at IS NULL AND locked_at IS NULL", snapshot.ID).Updates(map[string]interface{}{"sync_requested_at": now, "next_attempt_at": now}).Error; err != nil {
				return err
			}
			return supplierTaskUpdate(tx, &task, "pending", "supplier_evidence", "等待供应商核销数量核查", &next)
		}
		var item model.OrderItem
		if err := tx.Where("id = ? AND order_id = ?", task.OrderItemID, task.OrderID).First(&item).Error; err != nil {
			return err
		}
		if snapshot.IssueStatus != "ready" || snapshot.Environment != order.Environment || snapshot.ProductID != item.FulfillmentProductID || snapshot.ProductRevisionID != item.ProductRevisionID || snapshot.ScenicAreaID != item.FulfillmentScenicAreaID || snapshot.FulfillmentTenantID != item.FulfillmentTenantID || *snapshot.ProviderUsageQuantity != item.Quantity || *snapshot.ProviderUsedQuantity != item.Quantity || *snapshot.ProviderReturnedQuantity != 0 {
			return supplierTaskUpdate(tx, &task, "manual_review", "quantity_mismatch", "供应商核销数量与订单不一致，请人工核查", nil)
		}
		// If a device already owns this ticket, it owns the only external call.
		var device model.XiaohongshuVoucherVerification
		deviceErr := tx.Where("ticket_id = ?", ticket.ID).First(&device).Error
		if deviceErr != nil && !errors.Is(deviceErr, gorm.ErrRecordNotFound) {
			return deviceErr
		}
		if deviceErr == nil {
			if device.State == "local_completed" && device.VerifyID != "" {
				group, err := xiaohongshuTicketVouchers(tx, ticket)
				if err != nil {
					return err
				}
				for _, member := range group {
					if member.VerifyID != device.VerifyID {
						return errors.New("已有设备核销与小红书券组不一致")
					}
				}
				return tx.Model(&task).Updates(map[string]interface{}{"state": "confirmed", "verify_id": device.VerifyID, "confirmed_at": now, "review_reason": "device_confirmed", "last_error": "", "next_attempt_at": nil}).Error
			}
			if device.State != "local_rejected" || device.AttemptCount != 0 || device.ExternalStartedAt != nil || device.VerifyID != "" {
				return supplierTaskUpdate(tx, &task, "pending", "device_hold", "设备核销处理中或结果待确认，暂停供应商回传", &next)
			}
		}
		if ticket.PendingXiaohongshuVerificationID != 0 {
			return supplierTaskUpdate(tx, &task, "pending", "device_hold", "设备核销处理中，暂停供应商回传", &next)
		}
		var link model.XiaohongshuOrderLink
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND order_id = ? AND state = 'paid' AND voucher_issuance_status = 'ready'", task.TenantID, task.ChannelAccountID, task.OrderID).First(&link).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return supplierTaskUpdate(tx, &task, "manual_review", "missing_vouchers", "小红书票券尚未完整签发，请先核查票券关联", nil)
		}
		group, err := xiaohongshuTicketVouchers(tx, ticket)
		if err != nil {
			return supplierTaskUpdate(tx, &task, "manual_review", "missing_vouchers", "小红书券组不完整，请核查票券关联", nil)
		}
		request = xiaohongshu.VoucherVerifyRequest{ExternalOrderID: link.ExternalOrderID}
		for _, member := range group {
			if member.TenantID != task.TenantID || member.ChannelAccountID != task.ChannelAccountID || member.XiaohongshuOrderLinkID != link.ID || member.Status != 1 || member.VerifyID != "" {
				return supplierTaskUpdate(tx, &task, "manual_review", "voucher_conflict", "小红书券状态或归属不一致，请人工核查", nil)
			}
			code, err := xiaohongshuVoucherPlainCode(member)
			if err != nil {
				return supplierTaskUpdate(tx, &task, "manual_review", "missing_vouchers", "小红书票券凭证不可读取，请人工核查", nil)
			}
			request.Vouchers = append(request.Vouchers, xiaohongshu.VoucherCode{Code: code})
		}
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = 'xiaohongshu' AND status IN ?", task.ChannelAccountID, task.TenantID, []string{"active", "sandbox"}).First(&account).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return supplierTaskUpdate(tx, &task, "pending", "channel_configuration", "小红书渠道未启用，等待配置恢复", &next)
		}
		if err := requireAnyActiveTenantCapability(tx, task.TenantID, "supplier", "distributor"); err != nil {
			return supplierTaskUpdate(tx, &task, "pending", "tenant_configuration", "租户业务未启用，暂停核销回传", &next)
		}
		if account.Environment != order.Environment {
			return supplierTaskUpdate(tx, &task, "manual_review", "environment_mismatch", "小红书账号环境与原订单不一致，请人工核查", nil)
		}
		secret, err := utils.DecryptAES(account.SecretCiphertext)
		if err != nil || secret == "" {
			return supplierTaskUpdate(tx, &task, "pending", "channel_configuration", "小红书渠道凭据不可用，等待配置恢复", &next)
		}
		factory := s.NewClient
		if factory == nil {
			factory = xiaohongshu.NewClient
		}
		client = factory(account.AppID, secret, account.Environment)
		// POI is a lookup hint only; account and order identities are authoritative.
		var config model.XiaohongshuProductConfig
		if err := tx.Table("xiaohongshu_product_configs AS c").Joins("JOIN channel_product_mappings m ON m.id = c.channel_product_mapping_id AND m.channel_account_id = c.channel_account_id AND m.product_id = ?", item.ProductID).Where("c.tenant_id = ? AND c.channel_account_id = ? AND c.deleted_at IS NULL AND m.deleted_at IS NULL", task.TenantID, task.ChannelAccountID).Select("c.*").First(&config).Error; err == nil {
			if pois := parseXiaohongshuPOIIDs(config.POIIDsJSON); len(pois) > 0 {
				request.POIID = pois[0]
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		cipher, err := utils.EncryptAES(string(encoded))
		if err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]interface{}{"state": "in_flight", "attempt_count": gorm.Expr("attempt_count + 1"), "external_started_at": now, "request_payload_ciphertext": cipher, "request_hash": hex.EncodeToString(digest[:]), "review_reason": "", "last_error": "", "next_attempt_at": nil}).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, remoteErr := client.VerifyVouchers(callCtx, request)
	if remoteErr != nil || response == nil || strings.TrimSpace(response.VerifyID) == "" {
		// Never expose provider messages: they may contain voucher codes.
		return model.DB.Model(&task).Where("state = 'in_flight'").Updates(map[string]interface{}{"state": "unknown", "review_reason": "external_unknown", "last_error": "小红书核销回传未获有效确认，请核对渠道结果"}).Error
	}
	// Store the received confirmation without sending again if a local write
	// briefly fails. A restart before persistence remains unknown, never resent.
	var persistErr error
	for attempt := 0; attempt < 3; attempt++ {
		persistErr = confirmSupplierVerification(task.ID, task.TenantID, response.VerifyID, time.Now())
		if persistErr == nil {
			return nil
		}
	}
	return fmt.Errorf("供应商核销回传确认落库失败，任务 %d 需核查: %w", task.ID, persistErr)
}

func confirmSupplierVerification(id, tenantID uint, verifyID string, now time.Time) error {
	verifyID = strings.TrimSpace(verifyID)
	if verifyID == "" || len(verifyID) > 100 {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	return model.Write(func(tx *gorm.DB) error {
		var task model.XiaohongshuSupplierVerification
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", id, tenantID).First(&task).Error; err != nil {
			return err
		}
		if task.State == "confirmed" && task.VerifyID == verifyID {
			return nil
		}
		if task.State != "in_flight" && task.State != "unknown" {
			return ErrXiaohongshuVoucherResolutionNotResolvable
		}
		return confirmSupplierVerificationTx(tx, &task, verifyID, now)
	})
}

func confirmSupplierVerificationTx(tx *gorm.DB, task *model.XiaohongshuSupplierVerification, verifyID string, now time.Time) error {
	_, ticket, _, err := loadSupplierVerificationOwnershipTx(tx, task)
	if err != nil {
		return err
	}
	group, err := xiaohongshuTicketVouchers(tx, ticket)
	if err != nil {
		return err
	}
	if group[0].ChannelAccountID != task.ChannelAccountID || group[0].TenantID != task.TenantID {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	plain, err := utils.DecryptAES(task.RequestPayloadCiphertext)
	if err != nil {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	digest := sha256.Sum256([]byte(plain))
	var frozen xiaohongshu.VoucherVerifyRequest
	if hex.EncodeToString(digest[:]) != task.RequestHash || json.Unmarshal([]byte(plain), &frozen) != nil || len(frozen.Vouchers) != len(group) {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	var orderLink model.XiaohongshuOrderLink
	if err := tx.Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND order_id = ?", group[0].XiaohongshuOrderLinkID, task.TenantID, task.ChannelAccountID, task.OrderID).First(&orderLink).Error; err != nil {
		return err
	}
	if frozen.ExternalOrderID != orderLink.ExternalOrderID {
		return ErrXiaohongshuVoucherResolutionInvalid
	}
	for index, member := range group {
		code, err := xiaohongshuVoucherPlainCode(member)
		if err != nil || code != frozen.Vouchers[index].Code || member.XiaohongshuOrderLinkID != orderLink.ID || member.ChannelAccountID != task.ChannelAccountID || member.TenantID != task.TenantID {
			return ErrXiaohongshuVoucherResolutionInvalid
		}
	}
	if err := updateXiaohongshuGroupVerifyID(tx, &group[0], verifyID); err != nil {
		return err
	}
	return tx.Model(task).Updates(map[string]interface{}{"state": "confirmed", "verify_id": verifyID, "confirmed_at": now, "review_reason": "", "last_error": "", "next_attempt_at": nil}).Error
}

type XiaohongshuSupplierVerificationResolutionRequest struct {
	TenantID, TaskID, ActorUserID                           uint
	ActorRole, Decision, Reason, Evidence, ExternalVerifyID string
}

func ResolveXiaohongshuSupplierVerification(request XiaohongshuSupplierVerificationResolutionRequest) (*model.XiaohongshuSupplierVerification, error) {
	request.Reason, request.Evidence, request.ExternalVerifyID = strings.TrimSpace(request.Reason), strings.TrimSpace(request.Evidence), strings.TrimSpace(request.ExternalVerifyID)
	if request.TenantID == 0 || request.TaskID == 0 || request.ActorUserID == 0 || request.Reason == "" || len(request.Reason) > 500 || request.Evidence == "" || len(request.Evidence) > 2000 || (request.Decision != "confirm_external" && request.Decision != "release_external") || (request.Decision == "confirm_external" && (request.ExternalVerifyID == "" || len(request.ExternalVerifyID) > 100)) {
		return nil, ErrXiaohongshuVoucherResolutionInvalid
	}
	if !authz.HasTenantPermission(request.ActorRole, authz.PermissionXiaohongshuVoucherResolve) {
		return nil, ErrXiaohongshuVoucherResolutionPermission
	}
	var resolved model.XiaohongshuSupplierVerification
	err := model.Write(func(tx *gorm.DB) error {
		var actor model.User
		if err := requireAnyActiveTenantCapability(tx, request.TenantID, "supplier", "distributor"); err != nil {
			return ErrXiaohongshuVoucherResolutionPermission
		}
		if err := tx.Select("id", "role").Where("id = ? AND tenant_id = ? AND role = ? AND status = 'active'", request.ActorUserID, request.TenantID, request.ActorRole).First(&actor).Error; err != nil {
			return ErrXiaohongshuVoucherResolutionPermission
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", request.TaskID, request.TenantID).First(&resolved).Error; err != nil {
			return err
		}
		before := resolved.State
		if resolved.State == "confirmed" && request.Decision == "confirm_external" && resolved.VerifyID == request.ExternalVerifyID {
			return nil
		}
		if resolved.State != "unknown" && !(resolved.State == "manual_review" && resolved.ReviewReason == "external_not_consumed") {
			return ErrXiaohongshuVoucherResolutionNotResolvable
		}
		if request.Decision == "confirm_external" {
			if err := confirmSupplierVerificationTx(tx, &resolved, request.ExternalVerifyID, time.Now()); err != nil {
				return err
			}
		} else {
			if resolved.State != "unknown" {
				return ErrXiaohongshuVoucherResolutionNotResolvable
			}
			// The supplier consumed the ticket regardless of Xiaohongshu's result.
			// Evidence of no platform consume is not permission to refund/resend.
			if err := supplierTaskUpdate(tx, &resolved, "manual_review", "external_not_consumed", "已确认平台未核销，供应商已核销票仍需人工协调；不会自动重发或解除退款限制", nil); err != nil {
				return err
			}
		}
		after, err := json.Marshal(map[string]string{"before_state": before, "decision": request.Decision, "evidence": request.Evidence, "external_verify_id": request.ExternalVerifyID})
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, request.ActorUserID, request.TenantID, request.ActorRole, "tenant", "xiaohongshu.supplier_verification.resolve", "xiaohongshu_supplier_verification", request.TaskID, request.Reason, "", string(after)); err != nil {
			return err
		}
		return tx.First(&resolved, resolved.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}
