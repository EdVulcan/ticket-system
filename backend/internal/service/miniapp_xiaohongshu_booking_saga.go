package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One external step can fetch a token and then call the booking API. Keep the
// lease longer than both HTTP timeouts so another worker cannot send a duplicate
// request while the first worker is still waiting on the platform.
const xiaohongshuBookingOperationLease = 90 * time.Second

const xiaohongshuBookingOperationTypeRefundStatusSync = "refund_status_sync"

func normalizeXiaohongshuBookingOperationType(operationType string) string {
	operationType = strings.TrimSpace(operationType)
	// Schema 86 migrates persisted legacy values. Keep this input alias so
	// existing admin filters do not silently lose historical failed tasks.
	if operationType == "refund" {
		return xiaohongshuBookingOperationTypeRefundStatusSync
	}
	return operationType
}

type xiaohongshuBookingOperationPayload struct {
	OpenID            string `json:"open_id"`
	ExternalOrderID   string `json:"external_order_id"`
	ExternalProductID string `json:"external_product_id"`
	ExternalSKUID     string `json:"external_sku_id"`
	POIID             string `json:"poi_id,omitempty"`
	VoucherCode       string `json:"voucher_code"`
	VoucherCodeHash   string `json:"voucher_code_hash"`
	CheckInDate       string `json:"check_in_date"`
	CheckOutDate      string `json:"check_out_date"`
}

type XiaohongshuFailedBookingOperationView struct {
	ID                  uint       `json:"id"`
	Type                string     `json:"type"`
	Status              string     `json:"status"`
	ChannelAccountID    uint       `json:"channel_account_id"`
	ChannelAccountCode  string     `json:"channel_account_code"`
	EntitlementNo       string     `json:"entitlement_no"`
	OrderNo             string     `json:"order_no"`
	ExternalBookOrderID string     `json:"external_book_order_id,omitempty"`
	PlatformBookID      string     `json:"platform_book_id,omitempty"`
	Attempts            int        `json:"attempts"`
	MaxAttempts         int        `json:"max_attempts"`
	LastError           string     `json:"last_error"`
	FailedFromStage     string     `json:"failed_from_stage"`
	UpdatedAt           time.Time  `json:"updated_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

type XiaohongshuFailedBookingOperationPage struct {
	Data     []XiaohongshuFailedBookingOperationView `json:"data"`
	Total    int64                                   `json:"total"`
	Page     int                                     `json:"page"`
	PageSize int                                     `json:"page_size"`
}

func (s XiaohongshuBookingService) ListFailedXiaohongshuBookingOperations(tenantID uint, operationType string, page, pageSize int) (*XiaohongshuFailedBookingOperationPage, error) {
	operationType = normalizeXiaohongshuBookingOperationType(operationType)
	if operationType != "" && operationType != "book" && operationType != "revoke" && operationType != xiaohongshuBookingOperationTypeRefundStatusSync {
		return nil, errors.New("invalid xiaohongshu booking operation type")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	base := model.DB.Table("xiaohongshu_booking_operations AS operation").
		Joins("JOIN channel_accounts AS account ON account.id = operation.channel_account_id AND account.tenant_id = operation.tenant_id").
		Joins("JOIN scenic_hotel_package_entitlements AS entitlement ON entitlement.id = operation.entitlement_id AND entitlement.sales_tenant_id = operation.tenant_id").
		Joins("JOIN orders AS orders ON orders.id = entitlement.order_id AND orders.tenant_id = operation.tenant_id").
		Where("operation.tenant_id = ? AND operation.status = ? AND operation.failed_from_stage <> '' AND operation.deleted_at IS NULL", tenantID, "failed")
	if operationType != "" {
		base = base.Where("operation.type = ?", operationType)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, err
	}
	rows := make([]XiaohongshuFailedBookingOperationView, 0)
	if err := base.Select(`operation.id, operation.type, operation.status, operation.channel_account_id,
		account.code AS channel_account_code, entitlement.entitlement_no, orders.order_no,
		operation.external_book_order_id, operation.platform_book_id, operation.attempts,
		operation.max_attempts, operation.last_error, operation.failed_from_stage, operation.updated_at, operation.completed_at`).
		Order("operation.updated_at DESC, operation.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].LastError = safeXiaohongshuBookingOperationError(rows[i].LastError)
	}
	return &XiaohongshuFailedBookingOperationPage{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s XiaohongshuBookingService) RetryFailedXiaohongshuBookingOperation(tenantID, operationID, operatorID uint, operatorRole, reason string) error {
	reason = strings.TrimSpace(reason)
	if tenantID == 0 || operationID == 0 || operatorID == 0 {
		return errors.New("tenant, operation, and operator are required")
	}
	if reason == "" || len(reason) > 255 {
		return errors.New("retry reason is required and must not exceed 255 characters")
	}
	return model.Write(func(tx *gorm.DB) error {
		var operation model.XiaohongshuBookingOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ?", operationID, tenantID).First(&operation).Error; err != nil {
			return err
		}
		if operation.Status != "failed" {
			return errors.New("only failed xiaohongshu booking operations can be retried")
		}
		var entitlement model.ScenicHotelPackageEntitlement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND sales_tenant_id = ?", operation.EntitlementID, tenantID).First(&entitlement).Error; err != nil {
			return err
		}
		if entitlement.ExternalBookOrderID != operation.ExternalBookOrderID {
			return errors.New("xiaohongshu booking operation no longer matches the entitlement")
		}
		nextStatus := strings.TrimSpace(operation.FailedFromStage)
		if nextStatus == "" {
			return errors.New("xiaohongshu booking operation has no recoverable failed stage")
		}
		switch operation.Type {
		case "book":
			if entitlement.Status != "booking_pending" || entitlement.ReservationID == 0 {
				return errors.New("failed booking operation is not recoverable from the entitlement state")
			}
			switch nextStatus {
			case "pending":
				if entitlement.PlatformBookID != "" {
					return errors.New("failed booking operation is missing the entitlement platform booking id")
				}
			case "remote_succeeded", "compensation_pending":
				if operation.PlatformBookID == "" {
					return errors.New("failed booking operation is missing its platform booking id")
				}
				if entitlement.PlatformBookID != "" && entitlement.PlatformBookID != operation.PlatformBookID {
					return errors.New("failed booking operation platform booking id does not match the entitlement")
				}
			case "confirm_pending":
				if operation.PlatformBookID == "" || entitlement.PlatformBookID != operation.PlatformBookID {
					return errors.New("failed booking operation platform booking id does not match the entitlement")
				}
			default:
				return errors.New("failed booking operation has an unsupported recovery stage")
			}
		case "revoke":
			if nextStatus != "pending" && nextStatus != "remote_succeeded" {
				return errors.New("failed cancellation operation has an unsupported recovery stage")
			}
			if entitlement.Status != "cancel_pending" || entitlement.ReservationID == 0 || entitlement.PlatformBookID != operation.PlatformBookID {
				return errors.New("failed cancellation operation is not recoverable from the entitlement state")
			}
		case xiaohongshuBookingOperationTypeRefundStatusSync, "refund": // Legacy rows are migrated by schema 86.
			if nextStatus != "pending" && nextStatus != "remote_succeeded" {
				return errors.New("failed refund status synchronization has an unsupported recovery stage")
			}
			if entitlement.Status != "refunded" || entitlement.PlatformBookID != operation.PlatformBookID {
				return errors.New("failed refund status synchronization is not recoverable from the entitlement state")
			}
			var ticket model.Ticket
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", entitlement.TicketID, tenantID, "refunded").First(&ticket).Error; err != nil {
				return errors.New("failed refund status synchronization does not have a refunded ticket fact")
			}
		default:
			return errors.New("unsupported xiaohongshu booking operation type")
		}
		before := fmt.Sprintf(`{"status":"failed","attempts":%d,"last_error":%q}`, operation.Attempts, operation.LastError)
		nextAttempt := s.now()
		if err := tx.Model(&operation).Updates(map[string]interface{}{
			"status": nextStatus, "failed_from_stage": "", "attempts": 0, "last_error": "", "next_attempt_at": nextAttempt, "completed_at": nil,
		}).Error; err != nil {
			return err
		}
		if err := (PackageFulfillmentLifecycle{}).MarkExternalBookingRetryPendingTx(tx, tenantID, operation.EntitlementID); err != nil {
			return err
		}
		if strings.TrimSpace(operatorRole) == "" {
			operatorRole = auditRoleTx(tx, operatorID)
		}
		after := fmt.Sprintf(`{"status":%q,"attempts":0}`, nextStatus)
		return recordAuditTx(tx, operatorID, tenantID, operatorRole, "tenant", "xiaohongshu.booking_sync.retry", "xiaohongshu_booking_operation", operation.ID, reason, before, after)
	})
}

func safeXiaohongshuBookingOperationError(message string) string {
	message = truncateChannelError(strings.TrimSpace(message))
	if message == "" {
		return "同步失败，请检查渠道配置后重试"
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{"open_id", "openid", "voucher", "guest", "contact", "mobile", "phone", "card_no", "id_card", "request_payload", "游客", "手机号", "券码"} {
		if strings.Contains(lower, marker) {
			return "同步失败，错误详情包含敏感信息，请由系统管理员查看审计记录"
		}
	}
	return message
}

func encryptXiaohongshuBookingPayload(payload xiaohongshuBookingOperationPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode xiaohongshu booking operation: %w", err)
	}
	return utils.EncryptAES(string(raw))
}

func decryptXiaohongshuBookingPayload(ciphertext string) (*xiaohongshuBookingOperationPayload, error) {
	raw, err := utils.DecryptAES(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt xiaohongshu booking operation: %w", err)
	}
	var payload xiaohongshuBookingOperationPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode xiaohongshu booking operation: %w", err)
	}
	return &payload, nil
}

func xiaohongshuBookingOperationKey(operationType string, entitlementID uint, stableValue string) string {
	digest := hashMiniappValue(strings.TrimSpace(stableValue))
	return fmt.Sprintf("xhs:%s:%d:%s", operationType, entitlementID, digest[:24])
}

func (s XiaohongshuBookingService) processXiaohongshuBookingOperation(ctx context.Context, operationID uint) (bool, error) {
	for step := 0; step < 5; step++ {
		operation, claimed, err := s.claimXiaohongshuBookingOperation(operationID)
		if err != nil || !claimed {
			return false, err
		}
		completed, err := s.executeXiaohongshuBookingOperationStep(ctx, operation)
		if err != nil {
			if persistErr := s.deferXiaohongshuBookingOperation(operation, err); persistErr != nil {
				return false, errors.Join(err, persistErr)
			}
			return false, nil
		}
		if completed {
			return true, nil
		}
	}
	return false, nil
}

func (s XiaohongshuBookingService) claimXiaohongshuBookingOperation(operationID uint) (*model.XiaohongshuBookingOperation, bool, error) {
	var operation model.XiaohongshuBookingOperation
	claimed := false
	now := s.now()
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operationID).First(&operation).Error; err != nil {
			return err
		}
		if operation.Status == "completed" || operation.Status == "failed" {
			return nil
		}
		if operation.NextAttemptAt != nil && operation.NextAttemptAt.After(now) {
			return nil
		}
		leaseUntil := now.Add(xiaohongshuBookingOperationLease)
		if err := tx.Model(&operation).Update("next_attempt_at", leaseUntil).Error; err != nil {
			return err
		}
		operation.NextAttemptAt = &leaseUntil
		claimed = true
		return nil
	})
	return &operation, claimed, err
}

func (s XiaohongshuBookingService) executeXiaohongshuBookingOperationStep(ctx context.Context, operation *model.XiaohongshuBookingOperation) (bool, error) {
	if operation == nil {
		return false, errors.New("xiaohongshu booking operation is required")
	}
	switch operation.Type {
	case "book":
		return s.executeXiaohongshuBookStep(ctx, operation)
	case "revoke":
		return s.executeXiaohongshuRevokeStep(ctx, operation)
	case xiaohongshuBookingOperationTypeRefundStatusSync, "refund": // Legacy rows are migrated by schema 86.
		return s.executeXiaohongshuRefundStatusSyncStep(ctx, operation)
	default:
		return false, fmt.Errorf("unsupported xiaohongshu booking operation type %q", operation.Type)
	}
}

func (s XiaohongshuBookingService) executeXiaohongshuRefundStatusSyncStep(ctx context.Context, operation *model.XiaohongshuBookingOperation) (bool, error) {
	switch operation.Status {
	case "pending":
		allowed, err := s.beginXiaohongshuExternalAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		client, err := s.xiaohongshuBookingClient(operation)
		if err != nil {
			return false, err
		}
		// Status 4 reports an already-completed, real user after-sale to the
		// presale booking component. It never calls or confirms a funds-refund
		// endpoint. Appointment cancellation and rescheduling use status 3.
		if err := client.SyncPresaleBookStatus(ctx, xiaohongshu.PresaleBookStatusRequest{
			ExternalBookOrderID: operation.ExternalBookOrderID, BookIDs: []string{operation.PlatformBookID}, Status: 4,
		}); err != nil {
			return false, err
		}
		if err := model.Write(func(tx *gorm.DB) error {
			return tx.Model(&model.XiaohongshuBookingOperation{}).Where("id = ? AND type IN ? AND status = ?", operation.ID, []string{xiaohongshuBookingOperationTypeRefundStatusSync, "refund"}, "pending").Updates(map[string]interface{}{
				"status": "remote_succeeded", "attempts": 0, "last_error": "", "next_attempt_at": s.now(),
			}).Error
		}); err != nil {
			return false, err
		}
		return false, nil

	case "remote_succeeded":
		allowed, err := s.beginXiaohongshuLocalFinalizeAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		err = model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND type IN ? AND status = ?", operation.ID, []string{xiaohongshuBookingOperationTypeRefundStatusSync, "refund"}, "remote_succeeded").First(&locked).Error; err != nil {
				return err
			}
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ? AND sales_tenant_id = ?", locked.EntitlementID, locked.TenantID).First(&entitlement).Error; err != nil {
				return err
			}
			if _, err := (PackageFulfillmentLifecycle{}).MarkRefundStatusSyncedTx(tx, locked.TenantID, entitlement.EntitlementNo, locked.ExternalBookOrderID, locked.PlatformBookID); err != nil {
				return err
			}
			now := s.now()
			return tx.Model(&locked).Updates(map[string]interface{}{
				"status": "completed", "last_error": "", "next_attempt_at": nil, "completed_at": now,
			}).Error
		})
		return err == nil, err
	}
	return operation.Status == "completed" || operation.Status == "failed", nil
}

func (s XiaohongshuBookingService) ensurePendingXiaohongshuRefundOperations(limit int) error {
	var entitlementIDs []uint
	if err := model.DB.Table("scenic_hotel_package_entitlements AS entitlement").
		Joins("JOIN xiaohongshu_order_links AS link ON link.order_id = entitlement.order_id AND link.tenant_id = entitlement.sales_tenant_id").
		Joins("LEFT JOIN xiaohongshu_booking_operations AS operation ON operation.entitlement_id = entitlement.id AND operation.tenant_id = entitlement.sales_tenant_id AND operation.type IN ? AND operation.deleted_at IS NULL", []string{xiaohongshuBookingOperationTypeRefundStatusSync, "refund"}).
		Where("entitlement.status = ? AND entitlement.platform_sync_status = ?", "refunded", "pending").
		Where("entitlement.external_book_order_id <> '' AND entitlement.platform_book_id <> '' AND operation.id IS NULL").
		Order("entitlement.updated_at ASC, entitlement.id ASC").Limit(limit).Pluck("entitlement.id", &entitlementIDs).Error; err != nil {
		return err
	}
	payloadCiphertext, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{})
	if err != nil {
		return err
	}
	for _, entitlementID := range entitlementIDs {
		if err := model.Write(func(tx *gorm.DB) error {
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND status = ? AND platform_sync_status = ? AND external_book_order_id <> '' AND platform_book_id <> ''", entitlementID, "refunded", "pending").
				First(&entitlement).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			var link model.XiaohongshuOrderLink
			if err := tx.Where("order_id = ? AND tenant_id = ?", entitlement.OrderID, entitlement.SalesTenantID).First(&link).Error; err != nil {
				return err
			}
			nextAttempt := s.now()
			operation := model.XiaohongshuBookingOperation{
				TenantID: entitlement.SalesTenantID, ChannelAccountID: link.ChannelAccountID, OrderLinkID: link.ID,
				EntitlementID: entitlement.ID, OperationKey: xiaohongshuBookingOperationKey(xiaohongshuBookingOperationTypeRefundStatusSync, entitlement.ID, entitlement.ExternalBookOrderID),
				Type: xiaohongshuBookingOperationTypeRefundStatusSync, Status: "pending", ExternalBookOrderID: entitlement.ExternalBookOrderID, PlatformBookID: entitlement.PlatformBookID,
				RequestPayloadCiphertext: payloadCiphertext, MaxAttempts: 20, NextAttemptAt: &nextAttempt,
			}
			return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "operation_key"}}, DoNothing: true}).Create(&operation).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s XiaohongshuBookingService) executeXiaohongshuBookStep(ctx context.Context, operation *model.XiaohongshuBookingOperation) (bool, error) {
	switch operation.Status {
	case "pending":
		allowed, err := s.beginXiaohongshuExternalAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		payload, err := decryptXiaohongshuBookingPayload(operation.RequestPayloadCiphertext)
		if err != nil {
			return false, err
		}
		client, err := s.xiaohongshuBookingClient(operation)
		if err != nil {
			return false, err
		}
		response, err := client.BookPresaleVoucher(ctx, xiaohongshu.PresaleBookRequest{
			ProductType: xiaohongshu.ProductTypePresaleVoucher, OpenID: payload.OpenID,
			ExternalOrderID: payload.ExternalOrderID, ExternalProductID: payload.ExternalProductID,
			ExternalSKUID: payload.ExternalSKUID, POIID: payload.POIID,
			BookInfo: xiaohongshu.PresaleBookInfo{ExternalBookOrderID: operation.ExternalBookOrderID,
				Details: []xiaohongshu.PresaleBookDetail{{VoucherCode: payload.VoucherCode, CheckInDate: payload.CheckInDate, CheckOutDate: payload.CheckOutDate}}},
		})
		if err != nil {
			return false, err
		}
		result := response.Results[0]
		voucherMismatch := result.VoucherCode != "" && hashMiniappValue(result.VoucherCode) != payload.VoucherCodeHash
		now := s.now()
		nextStatus, lastError := "remote_succeeded", ""
		if voucherMismatch {
			nextStatus, lastError = "compensation_pending", "小红书预约返回的券码不匹配"
		}
		// Persist the remote result independently of the entitlement update. If
		// the local fulfillment write is interrupted, the durable operation still
		// owns the platform booking id and can reconcile it on retry.
		if err := model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", operation.ID, "pending").First(&locked).Error; err != nil {
				return err
			}
			return tx.Model(&locked).Updates(map[string]interface{}{
				"status": nextStatus, "platform_book_id": result.BookID,
				"attempts": 0, "last_error": lastError, "next_attempt_at": now,
			}).Error
		}); err != nil {
			return false, err
		}
		return false, nil

	case "remote_succeeded":
		allowed, err := s.beginXiaohongshuLocalFinalizeAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		err = model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", operation.ID, "remote_succeeded").First(&locked).Error; err != nil {
				return err
			}
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ? AND sales_tenant_id = ?", locked.EntitlementID, locked.TenantID).First(&entitlement).Error; err != nil {
				return err
			}
			if _, err := (PackageFulfillmentLifecycle{}).RecordExternalBookingTx(tx, locked.TenantID, entitlement.EntitlementNo, locked.ExternalBookOrderID, locked.PlatformBookID); err != nil {
				return err
			}
			return tx.Model(&locked).Updates(map[string]interface{}{"status": "confirm_pending", "attempts": 0, "last_error": "", "next_attempt_at": s.now()}).Error
		})
		return false, err

	case "confirm_pending":
		allowed, err := s.beginXiaohongshuExternalAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		client, err := s.xiaohongshuBookingClient(operation)
		if err != nil {
			return false, err
		}
		if err := client.SyncPresaleBookStatus(ctx, xiaohongshu.PresaleBookStatusRequest{
			ExternalBookOrderID: operation.ExternalBookOrderID, BookIDs: []string{operation.PlatformBookID}, Status: 1,
		}); err != nil {
			return false, err
		}
		err = model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", operation.ID, "confirm_pending").First(&locked).Error; err != nil {
				return err
			}
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ? AND sales_tenant_id = ?", locked.EntitlementID, locked.TenantID).First(&entitlement).Error; err != nil {
				return err
			}
			if _, err := (PackageFulfillmentLifecycle{}).FinalizeExternalBookingTx(tx, locked.TenantID, entitlement.EntitlementNo, locked.ExternalBookOrderID, locked.PlatformBookID); err != nil {
				return err
			}
			now := s.now()
			return tx.Model(&locked).Updates(map[string]interface{}{"status": "completed", "last_error": "", "next_attempt_at": nil, "completed_at": now}).Error
		})
		return err == nil, err

	case "compensation_pending":
		allowed, err := s.beginXiaohongshuExternalAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		client, err := s.xiaohongshuBookingClient(operation)
		if err != nil {
			return false, err
		}
		if err := client.SyncPresaleBookStatus(ctx, xiaohongshu.PresaleBookStatusRequest{
			ExternalBookOrderID: operation.ExternalBookOrderID, BookIDs: []string{operation.PlatformBookID}, Status: 2,
		}); err != nil {
			return false, err
		}
		err = model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", operation.ID, "compensation_pending").First(&locked).Error; err != nil {
				return err
			}
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ? AND sales_tenant_id = ?", locked.EntitlementID, locked.TenantID).First(&entitlement).Error; err != nil {
				return err
			}
			if _, err := (PackageFulfillmentLifecycle{}).RollbackExternalBookingTx(tx, locked.TenantID, entitlement.EntitlementNo, locked.ExternalBookOrderID, locked.PlatformBookID); err != nil {
				return err
			}
			now := s.now()
			return tx.Model(&locked).Updates(map[string]interface{}{"status": "failed", "completed_at": now, "next_attempt_at": nil}).Error
		})
		return err == nil, err
	}
	return operation.Status == "completed" || operation.Status == "failed", nil
}

func (s XiaohongshuBookingService) executeXiaohongshuRevokeStep(ctx context.Context, operation *model.XiaohongshuBookingOperation) (bool, error) {
	switch operation.Status {
	case "pending":
		allowed, err := s.beginXiaohongshuExternalAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		client, err := s.xiaohongshuBookingClient(operation)
		if err != nil {
			return false, err
		}
		if err := client.SyncPresaleBookStatus(ctx, xiaohongshu.PresaleBookStatusRequest{
			ExternalBookOrderID: operation.ExternalBookOrderID, BookIDs: []string{operation.PlatformBookID}, Status: 3,
		}); err != nil {
			return false, err
		}
		if err := model.Write(func(tx *gorm.DB) error {
			return tx.Model(&model.XiaohongshuBookingOperation{}).Where("id = ? AND status = ?", operation.ID, "pending").Updates(map[string]interface{}{
				"status": "remote_succeeded", "attempts": 0, "last_error": "", "next_attempt_at": s.now(),
			}).Error
		}); err != nil {
			return false, err
		}
		return false, nil

	case "remote_succeeded":
		allowed, err := s.beginXiaohongshuLocalFinalizeAttempt(operation)
		if err != nil || !allowed {
			return !allowed, err
		}
		err = model.Write(func(tx *gorm.DB) error {
			var locked model.XiaohongshuBookingOperation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", operation.ID, "remote_succeeded").First(&locked).Error; err != nil {
				return err
			}
			var entitlement model.ScenicHotelPackageEntitlement
			if err := tx.Where("id = ? AND sales_tenant_id = ?", locked.EntitlementID, locked.TenantID).First(&entitlement).Error; err != nil {
				return err
			}
			if _, err := (PackageFulfillmentLifecycle{}).FinalizeExternalCancellationTx(tx, locked.TenantID, entitlement.EntitlementNo, locked.ExternalBookOrderID, locked.PlatformBookID); err != nil {
				return err
			}
			now := s.now()
			return tx.Model(&locked).Updates(map[string]interface{}{
				"status": "completed", "last_error": "", "next_attempt_at": nil, "completed_at": now,
			}).Error
		})
		return err == nil, err
	}
	return operation.Status == "completed" || operation.Status == "failed", nil
}

func (s XiaohongshuBookingService) beginXiaohongshuExternalAttempt(operation *model.XiaohongshuBookingOperation) (bool, error) {
	return s.beginXiaohongshuOperationAttempt(operation)
}

func (s XiaohongshuBookingService) beginXiaohongshuLocalFinalizeAttempt(operation *model.XiaohongshuBookingOperation) (bool, error) {
	return s.beginXiaohongshuOperationAttempt(operation)
}

func (s XiaohongshuBookingService) beginXiaohongshuOperationAttempt(operation *model.XiaohongshuBookingOperation) (bool, error) {
	if operation == nil {
		return false, errors.New("xiaohongshu booking operation is required")
	}
	allowed := false
	err := model.Write(func(tx *gorm.DB) error {
		var locked model.XiaohongshuBookingOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operation.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != operation.Status {
			return nil
		}
		maxAttempts := locked.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 20
		}
		if locked.Attempts >= maxAttempts {
			message := fmt.Sprintf("小红书预约同步重试已达上限（%d 次），需要人工处理", maxAttempts)
			now := s.now()
			if err := (PackageFulfillmentLifecycle{}).MarkExternalBookingSyncFailedTx(tx, locked.TenantID, locked.EntitlementID, message); err != nil {
				return err
			}
			return tx.Model(&locked).Updates(map[string]interface{}{
				"status": "failed", "failed_from_stage": locked.Status, "last_error": message, "next_attempt_at": nil, "completed_at": now,
			}).Error
		}
		if err := tx.Model(&locked).Update("attempts", locked.Attempts+1).Error; err != nil {
			return err
		}
		operation.Attempts = locked.Attempts + 1
		allowed = true
		return nil
	})
	return allowed, err
}

func (s XiaohongshuBookingService) xiaohongshuBookingClient(operation *model.XiaohongshuBookingOperation) (*xiaohongshu.Client, error) {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", operation.ChannelAccountID, operation.TenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, ErrMiniappUnavailable
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil || strings.TrimSpace(secret) == "" {
		return nil, ErrMiniappUnavailable
	}
	newClient := s.NewXiaohongshuClient
	if newClient == nil {
		newClient = xiaohongshu.NewClient
	}
	return newClient(account.AppID, secret, account.Environment), nil
}

func (s XiaohongshuBookingService) deferXiaohongshuBookingOperation(operation *model.XiaohongshuBookingOperation, operationErr error) error {
	attempts := operation.Attempts
	delay := time.Duration(1<<min(attempts, 6)) * 5 * time.Second
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	nextAttempt := s.now().Add(delay)
	return model.Write(func(tx *gorm.DB) error {
		updates := map[string]interface{}{"last_error": truncateChannelError(operationErr.Error()), "next_attempt_at": nextAttempt}
		return tx.Model(&model.XiaohongshuBookingOperation{}).
			Where("id = ? AND status = ?", operation.ID, operation.Status).Updates(updates).Error
	})
}
