package service

import (
	"context"
	"errors"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

// XiaohongshuBookingService owns the deferred package booking entrypoints.
// The Saga implementation lives in miniapp_xiaohongshu_booking_saga.go;
// this file keeps HTTP and worker orchestration separate from the state machine.
type XiaohongshuBookingService struct {
	MiniappService
}

func (s MiniappService) bookingService() XiaohongshuBookingService {
	return XiaohongshuBookingService{MiniappService: s}
}

func (s MiniappService) ListFailedXiaohongshuBookingOperations(tenantID uint, operationType string, page, pageSize int) (*XiaohongshuFailedBookingOperationPage, error) {
	return s.bookingService().ListFailedXiaohongshuBookingOperations(tenantID, operationType, page, pageSize)
}

func (s MiniappService) RetryFailedXiaohongshuBookingOperation(tenantID, operationID, operatorID uint, operatorRole, reason string) error {
	return s.bookingService().RetryFailedXiaohongshuBookingOperation(tenantID, operationID, operatorID, operatorRole, reason)
}

// NewXiaohongshuBookingService is the worker/admin entry point for deferred
// booking operations. Keeping its construction separate makes the booking
// boundary explicit without breaking the miniapp HTTP facade.
func NewXiaohongshuBookingService() XiaohongshuBookingService {
	return XiaohongshuBookingService{MiniappService: NewMiniappService()}
}

func (s XiaohongshuBookingService) BookXiaohongshuPackage(ctx context.Context, customer *model.MiniappCustomer, orderNo string, input MiniappPackageBookingInput) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	input.EntitlementNo, input.CheckInDate = strings.TrimSpace(input.EntitlementNo), strings.TrimSpace(input.CheckInDate)
	input.GuestName, input.ContactPhone = strings.TrimSpace(input.GuestName), strings.TrimSpace(input.ContactPhone)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	checkIn, err := time.ParseInLocation("2006-01-02", input.CheckInDate, time.Local)
	if err != nil {
		return nil, errors.New("请选择有效的入住日期")
	}
	if input.EntitlementNo == "" || input.ClientRequestID == "" || len(input.ClientRequestID) > 100 {
		return nil, errors.New("预约参数不完整")
	}
	if input.GuestName == "" || input.ContactPhone == "" || len(input.GuestName) > 50 || len(input.ContactPhone) > 20 {
		return nil, errors.New("请填写有效的入住人和联系电话")
	}

	var order model.Order
	var link model.XiaohongshuOrderLink
	var operationID uint
	err = model.Write(func(tx *gorm.DB) error {
		var entitlement model.ScenicHotelPackageEntitlement
		if err := tx.Where("entitlement_no = ? AND sales_tenant_id = ?", input.EntitlementNo, customer.TenantID).First(&entitlement).Error; err != nil {
			return err
		}
		if err := tx.Where("order_id = ? AND miniapp_customer_id = ? AND channel_account_id = ?", entitlement.OrderID, customer.ID, customer.ChannelAccountID).First(&link).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("id = ? AND tenant_id = ? AND order_no = ?", entitlement.OrderID, customer.TenantID, strings.TrimSpace(orderNo)).First(&order).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		var item model.OrderItem
		if err := tx.Where("id = ? AND order_id = ?", entitlement.OrderItemID, order.ID).First(&item).Error; err != nil {
			return err
		}
		var mapping model.ChannelProductMapping
		if err := tx.Where("channel_account_id = ? AND product_id = ?", link.ChannelAccountID, item.ProductID).First(&mapping).Error; err != nil {
			return errors.New("小红书套餐映射不可用")
		}
		var config model.XiaohongshuProductConfig
		if err := tx.Where("channel_product_mapping_id = ? AND product_type = ? AND sync_status = ?", mapping.ID, xiaohongshu.ProductTypePresaleVoucher, "synced").First(&config).Error; err != nil {
			return errors.New("该套餐尚未配置为小红书预售券")
		}
		openID, decryptErr := utils.DecryptAES(customer.OpenIDCiphertext)
		if decryptErr != nil || strings.TrimSpace(openID) == "" {
			return ErrMiniappUnauthenticated
		}
		operationKey := xiaohongshuBookingOperationKey("book", entitlement.ID, input.ClientRequestID)
		if entitlement.Status == "cancel_pending" {
			return errors.New("预约正在取消，请稍后再试")
		}
		var existing model.XiaohongshuBookingOperation
		existingErr := tx.Where("operation_key = ? AND tenant_id = ? AND entitlement_id = ? AND type = ?", operationKey, customer.TenantID, entitlement.ID, "book").First(&existing).Error
		if existingErr == nil {
			if (entitlement.Status == "booking_pending" || entitlement.Status == "booked") &&
				entitlement.ClientRequestID == input.ClientRequestID && entitlement.ExternalBookOrderID == existing.ExternalBookOrderID {
				operationID = existing.ID
				return nil
			}
			return errors.New("该预约请求编号已用于历史预约，请重新提交")
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		var voucher model.XiaohongshuVoucherLink
		if err := tx.Where("xiaohongshu_order_link_id = ? AND ticket_id = ?", link.ID, entitlement.TicketID).First(&voucher).Error; err != nil {
			return errors.New("小红书预约券码尚未同步，请稍后重试")
		}
		voucherCode, decryptErr := utils.DecryptAES(voucher.VoucherCodeCiphertext)
		if decryptErr != nil || strings.TrimSpace(voucherCode) == "" {
			return errors.New("小红书预约券码不可用")
		}
		var packageRow model.ScenicHotelPackage
		if err := tx.Where("id = ? AND booking_mode = ?", entitlement.PackageID, "after_purchase").First(&packageRow).Error; err != nil {
			return errors.New("套餐预约配置不可用")
		}
		externalBookID := miniappBookingExternalID(entitlement.ID, input.ClientRequestID)
		prepared, err := (PackageFulfillmentLifecycle{}).PrepareBookingTx(tx, PackageEntitlementBookingInput{
			EntitlementNo: input.EntitlementNo, CheckInDate: checkIn, GuestName: input.GuestName,
			ContactPhone: input.ContactPhone, ClientRequestID: input.ClientRequestID,
			ExternalBookOrderID: externalBookID,
		})
		if err != nil {
			return err
		}
		if err := tx.Where("operation_key = ? AND tenant_id = ? AND entitlement_id = ? AND type = ?", operationKey, customer.TenantID, prepared.ID, "book").First(&existing).Error; err == nil {
			operationID = existing.ID
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if prepared.Status == "booked" && prepared.PlatformSyncStatus == "synced" {
			return nil
		}
		payloadCiphertext, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{
			OpenID: openID, ExternalOrderID: link.ExternalOrderID, ExternalProductID: mapping.ExternalCode,
			ExternalSKUID: config.ExternalSKUID, POIID: firstXiaohongshuPOIID(config.POIIDsJSON),
			VoucherCode: voucherCode, VoucherCodeHash: voucher.VoucherCodeHash,
			CheckInDate: checkIn.Format("2006-01-02"), CheckOutDate: checkIn.AddDate(0, 0, packageRow.Nights).Format("2006-01-02"),
		})
		if err != nil {
			return err
		}
		nextAttempt := s.now()
		operation := model.XiaohongshuBookingOperation{
			TenantID: customer.TenantID, ChannelAccountID: link.ChannelAccountID, OrderLinkID: link.ID,
			EntitlementID: prepared.ID, OperationKey: operationKey, Type: "book", Status: "pending",
			ExternalBookOrderID: externalBookID, RequestPayloadCiphertext: payloadCiphertext,
			MaxAttempts: 20, NextAttemptAt: &nextAttempt,
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}
		operationID = operation.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if operationID != 0 {
		_, _ = s.processXiaohongshuBookingOperation(ctx, operationID)
	}
	return s.orderResult(&link, &order, false)
}

func (s XiaohongshuBookingService) CancelXiaohongshuPackageBooking(ctx context.Context, customer *model.MiniappCustomer, orderNo, entitlementNo string) (*MiniappOrderResult, error) {
	if customer == nil || customer.ID == 0 {
		return nil, ErrMiniappUnauthenticated
	}
	var order model.Order
	var link model.XiaohongshuOrderLink
	var operationID uint
	err := model.Write(func(tx *gorm.DB) error {
		var entitlement model.ScenicHotelPackageEntitlement
		if err := tx.Where("entitlement_no = ? AND sales_tenant_id = ?", strings.TrimSpace(entitlementNo), customer.TenantID).First(&entitlement).Error; err != nil {
			return err
		}
		if err := tx.Where("order_id = ? AND miniapp_customer_id = ? AND channel_account_id = ?", entitlement.OrderID, customer.ID, customer.ChannelAccountID).First(&link).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("id = ? AND tenant_id = ? AND order_no = ?", entitlement.OrderID, customer.TenantID, strings.TrimSpace(orderNo)).First(&order).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		if entitlement.Status == "pending_booking" && entitlement.PlatformBookID == "" {
			return nil
		}
		if entitlement.PlatformBookID == "" || entitlement.ExternalBookOrderID == "" {
			return errors.New("小红书预约编号不完整，无法撤销")
		}
		prepared, err := (PackageFulfillmentLifecycle{}).PrepareCancelTx(tx, entitlement.EntitlementNo)
		if err != nil {
			return err
		}
		if prepared.Status == "pending_booking" && prepared.PlatformBookID == "" {
			return nil
		}
		operationKey := xiaohongshuBookingOperationKey("revoke", prepared.ID, prepared.ExternalBookOrderID)
		var existing model.XiaohongshuBookingOperation
		if err := tx.Where("operation_key = ? AND tenant_id = ? AND entitlement_id = ? AND type = ?", operationKey, customer.TenantID, prepared.ID, "revoke").First(&existing).Error; err == nil {
			operationID = existing.ID
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		payloadCiphertext, err := encryptXiaohongshuBookingPayload(xiaohongshuBookingOperationPayload{})
		if err != nil {
			return err
		}
		nextAttempt := s.now()
		operation := model.XiaohongshuBookingOperation{
			TenantID: customer.TenantID, ChannelAccountID: link.ChannelAccountID, OrderLinkID: link.ID,
			EntitlementID: prepared.ID, OperationKey: operationKey, Type: "revoke", Status: "pending",
			ExternalBookOrderID: prepared.ExternalBookOrderID, PlatformBookID: prepared.PlatformBookID,
			RequestPayloadCiphertext: payloadCiphertext, MaxAttempts: 20, NextAttemptAt: &nextAttempt,
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}
		operationID = operation.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if operationID != 0 {
		_, _ = s.processXiaohongshuBookingOperation(ctx, operationID)
	}
	return s.orderResult(&link, &order, false)
}

func (s MiniappService) BookXiaohongshuPackage(ctx context.Context, customer *model.MiniappCustomer, orderNo string, input MiniappPackageBookingInput) (*MiniappOrderResult, error) {
	return s.bookingService().BookXiaohongshuPackage(ctx, customer, orderNo, input)
}

func (s MiniappService) CancelXiaohongshuPackageBooking(ctx context.Context, customer *model.MiniappCustomer, orderNo, entitlementNo string) (*MiniappOrderResult, error) {
	return s.bookingService().CancelXiaohongshuPackageBooking(ctx, customer, orderNo, entitlementNo)
}

func (s XiaohongshuBookingService) ProcessPendingXiaohongshuBookingSyncs(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	bookingService := s.bookingService()
	if err := bookingService.ensurePendingXiaohongshuRefundOperations(limit); err != nil {
		return 0, err
	}
	var operationIDs []uint
	if err := model.DB.Model(&model.XiaohongshuBookingOperation{}).
		Where("status IN ?", []string{"pending", "remote_succeeded", "confirm_pending", "compensation_pending"}).
		Where("next_attempt_at IS NULL OR next_attempt_at <= ?", s.now()).
		Order("updated_at ASC, id ASC").Limit(limit).Pluck("id", &operationIDs).Error; err != nil {
		return 0, err
	}
	processed := 0
	var firstErr error
	for _, operationID := range operationIDs {
		completed, err := bookingService.processXiaohongshuBookingOperation(ctx, operationID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if completed {
			processed++
		}
	}
	return processed, firstErr
}

func (s MiniappService) ProcessPendingXiaohongshuBookingSyncs(ctx context.Context, limit int) (int, error) {
	return s.bookingService().ProcessPendingXiaohongshuBookingSyncs(ctx, limit)
}
