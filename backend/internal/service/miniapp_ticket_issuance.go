package service

import "ticket-backend/internal/model"

func populateMiniappTicketIssuance(result *MiniappOrderResult, order *model.Order) error {
	if result.RefundPending || order.Status == "refunded" || order.Status == "cancelled" {
		return nil
	}
	if len(result.TicketCodes) > 0 {
		result.TicketIssuanceStatus = "ready"
		return nil
	}
	if result.ProductKind != "ticket" && result.ProductKind != "" {
		return nil
	}
	if order.Status != "paid" {
		return nil
	}
	if result.VoucherIssuanceStatus == "manual_review" {
		result.TicketIssuanceStatus = "manual_review"
		return nil
	}
	var pending int64
	if err := model.DB.Model(&model.Ticket{}).Where("order_id = ? AND tenant_id = ? AND status = 'pending_provider'", order.ID, order.TenantID).Count(&pending).Error; err != nil {
		return err
	}
	if pending > 0 || result.VoucherIssuanceStatus == "pending" || result.VoucherIssuanceStatus == "" {
		result.TicketIssuanceStatus = "pending"
	}
	return nil
}
