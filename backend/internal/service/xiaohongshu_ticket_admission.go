package service

import (
	"fmt"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

// Order provenance, not the mere presence of a voucher row, determines whether
// platform activation is required. Missing issuance data must never bypass it.
func ensureXiaohongshuTicketAdmissionTx(tx *gorm.DB, order *model.Order, ticket *model.Ticket, reservationID, deviceID, checkpointID uint, requestID string, prepareOnly bool) error {
	if order.Channel != "xiaohongshu" {
		return nil
	}
	var orderLink model.XiaohongshuOrderLink
	if err := tx.Where("tenant_id = ? AND order_id = ? AND channel_account_id = ? AND state = ? AND voucher_issuance_status = ?", order.TenantID, order.ID, order.ChannelAccountID, "paid", "ready").First(&orderLink).Error; err != nil {
		return fmt.Errorf("%w: 小红书票券尚未完整签发", ErrTicketUnavailable)
	}
	var voucher model.XiaohongshuVoucherLink
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND ticket_id = ?", order.TenantID, order.ChannelAccountID, orderLink.ID, ticket.ID).First(&voucher).Error; err != nil || voucher.VoucherCodeHash == "" || voucher.VoucherCodeCiphertext == "" {
		return fmt.Errorf("%w: 小红书票券关联缺失", ErrTicketUnavailable)
	}
	var saga model.XiaohongshuVoucherVerification
	query := tx.Where("tenant_id = ? AND channel_account_id = ? AND voucher_link_id = ? AND ticket_id = ?", order.TenantID, order.ChannelAccountID, voucher.ID, ticket.ID)
	if reservationID != 0 {
		if err := query.Where("id = ? AND device_id = ? AND check_point_id = ? AND request_id = ?", reservationID, deviceID, checkpointID, requestID).First(&saga).Error; err != nil {
			return ErrXiaohongshuVoucherRequiresDevice
		}
		if prepareOnly {
			if saga.State != "prepared" {
				return ErrXiaohongshuVoucherRequiresDevice
			}
		} else if (saga.State != "external_confirmed" && saga.State != "local_pending") || saga.VerifyID == "" || saga.VerifyID != voucher.VerifyID {
			return ErrXiaohongshuVoucherRequiresDevice
		}
		return nil
	}
	if err := query.Where("state = ? AND verify_id <> ''", "local_completed").First(&saga).Error; err != nil || voucher.VerifyID == "" || saga.VerifyID != voucher.VerifyID {
		return ErrXiaohongshuVoucherRequiresDevice
	}
	return nil
}
