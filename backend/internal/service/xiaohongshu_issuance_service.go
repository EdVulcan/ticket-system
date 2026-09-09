package service

import (
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"

	"gorm.io/gorm"
)

// applyXiaohongshuVoucherIssuanceTx records only complete provider voucher
// bindings. Payment has already been made durable before this runs, so a
// delayed or malformed voucher list must never undo payment/order truth.
func (s XiaohongshuOrderService) applyXiaohongshuVoucherIssuanceTx(tx *gorm.DB, link *model.XiaohongshuOrderLink, order *model.Order, vouchers []xiaohongshu.VoucherInfo) error {
	if link == nil || order == nil {
		return fmt.Errorf("xiaohongshu voucher issuance requires order link and order")
	}
	now := s.now()
	updates := map[string]interface{}{
		"voucher_issuance_attempt_count":   gorm.Expr("voucher_issuance_attempt_count + 1"),
		"voucher_issuance_last_attempt_at": now,
		"voucher_issuance_last_error":      "",
	}

	var tickets []model.Ticket
	if err := tx.Where("order_id = ? AND tenant_id = ?", order.ID, order.TenantID).Order("id ASC").Find(&tickets).Error; err != nil {
		return err
	}
	if len(tickets) == 0 {
		updates["voucher_issuance_status"] = "manual_review"
		updates["voucher_issuance_last_error"] = "已支付小红书订单没有本地票权"
		return tx.Model(link).Updates(updates).Error
	}

	var existing []model.XiaohongshuVoucherLink
	if err := tx.Where("xiaohongshu_order_link_id = ?", link.ID).Order("ticket_id ASC").Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 {
		if link.VoucherIssuanceStatus == "ready" && xiaohongshuVoucherIssuanceMatches(tickets, existing, vouchers) {
			// A repeated paid query may return the same vouchers. The first exact
			// binding remains authoritative; do not remap or demote it.
			updates["voucher_issuance_status"] = "ready"
			return tx.Model(link).Updates(updates).Error
		}
		// A prior partial or legacy association must never be silently repaired by
		// changing ticket/code identities. Preserve it for audited reconciliation.
		updates["voucher_issuance_status"] = "manual_review"
		updates["voucher_issuance_last_error"] = "已有小红书券绑定，拒绝覆盖不可变票券关联"
		return tx.Model(link).Updates(updates).Error
	}
	if len(vouchers) != len(tickets) {
		updates["voucher_issuance_status"] = "pending"
		updates["voucher_issuance_last_error"] = fmt.Sprintf("小红书券码数量 %d 与本地票数 %d 不一致", len(vouchers), len(tickets))
		return tx.Model(link).Updates(updates).Error
	}
	if order.DiscountCents > 0 {
		// Provider array order is not a financial identity. Match each voucher
		// to the immutable ticket allocation before recording its code binding.
		byAmount := make(map[int64][]xiaohongshu.VoucherInfo, len(vouchers))
		for _, voucher := range vouchers {
			byAmount[voucher.PayAmount] = append(byAmount[voucher.PayAmount], voucher)
		}
		matched := make([]xiaohongshu.VoucherInfo, 0, len(tickets))
		for _, ticket := range tickets {
			if ticket.SaleAmountCents == nil || len(byAmount[*ticket.SaleAmountCents]) == 0 {
				updates["voucher_issuance_status"] = "manual_review"
				updates["voucher_issuance_last_error"] = "平台券实付金额与立减订单分摊不一致，需核对后出票"
				return tx.Model(link).Updates(updates).Error
			}
			amount := *ticket.SaleAmountCents
			matched = append(matched, byAmount[amount][0])
			byAmount[amount] = byAmount[amount][1:]
		}
		vouchers = matched
	}

	rows := make([]model.XiaohongshuVoucherLink, 0, len(vouchers))
	seenCodes := make(map[string]struct{}, len(vouchers))
	encryptVoucher := s.EncryptVoucher
	if encryptVoucher == nil {
		encryptVoucher = utils.EncryptAES
	}
	for index, voucher := range vouchers {
		code := strings.TrimSpace(voucher.Code)
		if code == "" {
			updates["voucher_issuance_status"] = "manual_review"
			updates["voucher_issuance_last_error"] = "小红书返回空券码"
			return tx.Model(link).Updates(updates).Error
		}
		hash := hashMiniappValue(code)
		if _, duplicate := seenCodes[hash]; duplicate {
			updates["voucher_issuance_status"] = "manual_review"
			updates["voucher_issuance_last_error"] = "小红书返回重复券码"
			return tx.Model(link).Updates(updates).Error
		}
		seenCodes[hash] = struct{}{}
		ciphertext, err := encryptVoucher(code)
		if err != nil {
			return err
		}
		rows = append(rows, model.XiaohongshuVoucherLink{
			TenantID: link.TenantID, ChannelAccountID: link.ChannelAccountID, XiaohongshuOrderLinkID: link.ID,
			TicketID: tickets[index].ID, VoucherCodeHash: hash, VoucherCodeCiphertext: ciphertext, Status: voucher.Status,
		})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return err
	}
	updates["voucher_issuance_status"] = "ready"
	return tx.Model(link).Updates(updates).Error
}

func xiaohongshuVoucherIssuanceMatches(tickets []model.Ticket, existing []model.XiaohongshuVoucherLink, vouchers []xiaohongshu.VoucherInfo) bool {
	if len(tickets) == 0 || len(existing) != len(tickets) || len(vouchers) != len(tickets) {
		return false
	}
	ticketIDs := make(map[uint]struct{}, len(tickets))
	ticketAmounts := make(map[uint]int64, len(tickets))
	for _, ticket := range tickets {
		ticketIDs[ticket.ID] = struct{}{}
		if ticket.SaleAmountCents != nil {
			ticketAmounts[ticket.ID] = *ticket.SaleAmountCents
		}
	}
	boundCodes := make(map[string]struct{}, len(existing))
	boundAmounts := make(map[string]int64, len(existing))
	boundTicketIDs := make(map[uint]struct{}, len(existing))
	for _, voucher := range existing {
		if _, knownTicket := ticketIDs[voucher.TicketID]; !knownTicket || strings.TrimSpace(voucher.VoucherCodeHash) == "" {
			return false
		}
		if _, duplicate := boundTicketIDs[voucher.TicketID]; duplicate {
			return false
		}
		boundTicketIDs[voucher.TicketID] = struct{}{}
		if _, duplicate := boundCodes[voucher.VoucherCodeHash]; duplicate {
			return false
		}
		boundCodes[voucher.VoucherCodeHash] = struct{}{}
		if amount, allocated := ticketAmounts[voucher.TicketID]; allocated {
			boundAmounts[voucher.VoucherCodeHash] = amount
		}
	}
	for _, voucher := range vouchers {
		code := strings.TrimSpace(voucher.Code)
		if code == "" {
			return false
		}
		hash := hashMiniappValue(code)
		if _, bound := boundCodes[hash]; !bound {
			return false
		}
		if amount, allocated := boundAmounts[hash]; allocated && voucher.PayAmount != amount {
			return false
		}
		delete(boundCodes, hash)
	}
	return len(boundCodes) == 0
}
