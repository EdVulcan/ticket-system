package service

import (
	"fmt"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
)

func upstreamThirdPartyChild(orderNo string, itemID uint) string {
	return fmt.Sprintf("%s_%d", orderNo, itemID)
}

func upstreamQueryTicketMatches(s *model.OrderItemSupplySnapshot, item *model.OrderItem, orderNo string, t zyb.QueryOrderTicket) bool {
	if t.GoodsCode != s.ExternalProductCode || t.Quantity != strconv.Itoa(item.Quantity) {
		return false
	}
	if t.ScenicThirdCode != "" && t.ScenicThirdCode != upstreamThirdPartyChild(orderNo, item.ID) {
		return false
	}
	if t.ProviderSubOrderCode != "" && s.ProviderSubOrderCode != "" && t.ProviderSubOrderCode != s.ProviderSubOrderCode {
		return false
	}
	return t.ScenicThirdCode != "" || t.ProviderSubOrderCode != ""
}

func upstreamCheckChildMatches(code string, s *model.OrderItemSupplySnapshot, orderNo string) bool {
	for _, root := range []string{upstreamThirdPartyChild(orderNo, s.OrderItemID), s.ProviderSubOrderCode} {
		if root != "" && (code == root || strings.HasPrefix(code, root+"_")) {
			return true
		}
	}
	return false
}
