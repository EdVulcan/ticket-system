package service

import (
	"errors"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

// applyMiniappPromotionPriceTx runs after normal supplier, rights and inventory
// validation, before any order row is persisted. Discounts are seller-funded;
// supplier costs, commission terms and ticket rights remain their sold values.
func applyMiniappPromotionPriceTx(tx *gorm.DB, order *model.Order, quote *MiniappPromotionQuote) error {
	if order == nil || quote == nil || order.Channel != "xiaohongshu" || len(order.Items) != 1 ||
		quote.OriginalAmountCents != moneyCents(order.TotalAmount) || quote.OriginalAmountCents > 9999999999 ||
		quote.DiscountCents < 0 || quote.AmountCents <= 0 || quote.AmountCents+quote.DiscountCents != quote.OriginalAmountCents {
		return errors.New("订单价格已变化，请重新确认金额")
	}
	order.OriginalAmountCents = quote.OriginalAmountCents
	if quote.DiscountCents == 0 {
		return nil
	}
	item := &order.Items[0]
	if item.Quantity <= 0 || len(item.Tickets) == 0 || quote.Promotion.GrantID == 0 || item.BundleComponentID != 0 {
		return errors.New("该订单无法使用立减")
	}
	// A seller may fund a promotion, but may not undercut a supplier's
	// authorized retail floor or silently alter the supplier's settlement.
	if item.ProductOfferID > 0 {
		var offer model.ProductOffer
		if err := tx.Where("id = ? AND distributor_tenant_id = ? AND supplier_tenant_id = ?", item.ProductOfferID, order.TenantID, item.FulfillmentTenantID).First(&offer).Error; err != nil {
			return err
		}
		if quote.AmountCents/int64(item.Quantity) < offer.MinimumRetailPriceCents {
			return errors.New("立减后金额低于供应商允许的最低售价，请重新选择商品")
		}
	}
	order.DiscountCents = quote.DiscountCents
	order.PromotionGrantID = quote.Promotion.GrantID
	order.TotalAmount = centsMoney(quote.AmountCents)
	amount := quote.AmountCents
	item.SaleAmountCents = &amount
	units := int64(len(item.Tickets))
	for i := range item.Tickets {
		// Stable ticket order assigns remaining cents to the first tickets.
		allocation := amount / units
		if int64(i) < amount%units {
			allocation++
		}
		item.Tickets[i].SaleAmountCents = &allocation
	}
	return nil
}

// ticketSaleCents uses an immutable allocation for promoted sales and the
// historical unit-price rule for tickets sold before allocation snapshots.
func ticketSaleCents(item *model.OrderItem, ticket *model.Ticket) int64 {
	if ticket.SaleAmountCents != nil {
		return *ticket.SaleAmountCents
	}
	amount := moneyCents(item.Price)
	if ticket.CodeMode == "order" || (ticket.CodeMode == "" && item.Product.CodeMode == "order") {
		amount *= int64(item.Quantity)
	}
	return amount
}
