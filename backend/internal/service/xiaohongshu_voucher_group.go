package service

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
)

const maxXiaohongshuOrderCodeQuantity = 10

func xiaohongshuTicketUnits(ticket model.Ticket, item model.OrderItem) int {
	if ticket.CodeMode == "order" {
		return item.Quantity
	}
	return 1
}

// Existing one-voucher tickets and new whole-order codes share the same rows.
// The smallest member ID is the stable coordinator anchor for the whole code.
func xiaohongshuTicketVouchers(tx *gorm.DB, ticket *model.Ticket) ([]model.XiaohongshuVoucherLink, error) {
	var item model.OrderItem
	if err := tx.Where("id = ? AND order_id = ?", ticket.OrderItemID, ticket.OrderID).First(&item).Error; err != nil {
		return nil, err
	}
	var rows []model.XiaohongshuVoucherLink
	if err := tx.Where("tenant_id = ? AND ticket_id = ?", ticket.TenantID, ticket.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	count := xiaohongshuTicketUnits(*ticket, item)
	if count < 1 || count > maxXiaohongshuOrderCodeQuantity || len(rows) != count {
		return nil, errors.New("小红书券数量与本地票权不一致")
	}
	for _, r := range rows {
		if r.XiaohongshuOrderLinkID != rows[0].XiaohongshuOrderLinkID || r.ChannelAccountID != rows[0].ChannelAccountID {
			return nil, errors.New("小红书券归属不一致")
		}
	}
	return rows, nil
}

func xiaohongshuVoucherPlainCode(v model.XiaohongshuVoucherLink) (string, error) {
	code, err := utils.DecryptAES(v.VoucherCodeCiphertext)
	if err != nil || strings.TrimSpace(code) == "" || hashMiniappValue(code) != v.VoucherCodeHash {
		return "", errors.New("小红书凭证关联无效")
	}
	return code, nil
}

func updateXiaohongshuGroupVerifyID(tx *gorm.DB, link *model.XiaohongshuVoucherLink, verifyID string) error {
	var ticket model.Ticket
	if err := tx.Where("id = ? AND tenant_id = ?", link.TicketID, link.TenantID).First(&ticket).Error; err != nil {
		return err
	}
	group, err := xiaohongshuTicketVouchers(tx, &ticket)
	if err != nil {
		return err
	}
	for _, member := range group {
		if member.VerifyID != "" && member.VerifyID != verifyID {
			return errors.New("小红书券组已有不同核销编号")
		}
	}
	return tx.Model(&model.XiaohongshuVoucherLink{}).Where("tenant_id = ? AND ticket_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ?", link.TenantID, link.TicketID, link.ChannelAccountID, link.XiaohongshuOrderLinkID).Update("verify_id", verifyID).Error
}

func xiaohongshuGroupRefundDetails(group []model.XiaohongshuVoucherLink, total int64) ([]xiaohongshu.AfterSalesVoucherDetail, error) {
	var details []xiaohongshu.AfterSalesVoucherDetail
	var sum int64
	for _, v := range group {
		code, err := xiaohongshuVoucherPlainCode(v)
		if err != nil {
			return nil, err
		}
		amount := total
		if v.PayAmountCents != nil {
			amount = *v.PayAmountCents
		} else if len(group) > 1 {
			return nil, errors.New("小红书券组金额快照缺失")
		}
		if amount < 0 || amount > total {
			return nil, errors.New("小红书券金额无效")
		}
		sum += amount
		details = append(details, xiaohongshu.AfterSalesVoucherDetail{VoucherCode: code, RefundPrice: amount})
	}
	if sum != total {
		return nil, errors.New("小红书券组退款金额不守恒")
	}
	return details, nil
}
