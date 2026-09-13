package service

import (
	"errors"
	"ticket-backend/internal/model"
)

// The existing sale-time ticket mode decides how many supplier codes are
// required. Mutable product settings are never consulted after ordering.
func upstreamTicketCount(item *model.OrderItem, tickets []model.Ticket) (int, error) {
	if item == nil || item.Quantity <= 0 || len(tickets) == 0 {
		return 0, errors.New("上游出票缺少本地门票")
	}
	mode := tickets[0].CodeMode
	count := item.Quantity
	if mode == "order" {
		count = 1
	}
	if len(tickets) != count {
		return 0, errors.New("本地票码数量与售出时的票码模式不一致")
	}
	for _, ticket := range tickets {
		if ticket.CodeMode != mode || ticket.OrderItemID != item.ID || ticket.OrderID != item.OrderID {
			return 0, errors.New("本地门票模式或订单归属不一致")
		}
	}
	return count, nil
}

func upstreamTicketsPending(item *model.OrderItem, tickets []model.Ticket) error {
	if _, err := upstreamTicketCount(item, tickets); err != nil {
		return err
	}
	for _, ticket := range tickets {
		if ticket.Status != "pending_provider" || ticket.PendingRefundID != 0 || ticket.CheckInCount != 0 || ticket.PendingXiaohongshuVerificationID != 0 {
			return errors.New("上游出票已暂停：票券正在退款或状态已变化")
		}
	}
	return nil
}
