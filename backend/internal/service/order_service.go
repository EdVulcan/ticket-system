package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrDuplicateExternalOrder = errors.New("external order already exists")

const DefaultOrderReservationTTL = 15 * time.Minute

type OrderService struct{}

type SalesOrderFulfillmentView struct {
	ID               uint              `json:"id"`
	FulfillmentNo    string            `json:"fulfillment_no"`
	SupplierTenantID uint              `json:"supplier_tenant_id"`
	SupplierName     string            `json:"supplier_name"`
	ScenicAreaID     uint              `json:"scenic_area_id"`
	ScenicAreaName   string            `json:"scenic_area_name"`
	Status           string            `json:"status"`
	SettlementStatus string            `json:"settlement_status"`
	CanVerify        bool              `json:"can_verify"`
	TicketCount      int               `json:"ticket_count"`
	UsedCount        int               `json:"used_count"`
	RefundedCount    int               `json:"refunded_count"`
	GrossCents       int64             `json:"gross_cents"`
	RefundCents      int64             `json:"refund_cents"`
	CommissionCents  int64             `json:"commission_cents"`
	NetCents         int64             `json:"net_cents"`
	StatementID      uint              `json:"statement_id,omitempty"`
	StatementNo      string            `json:"statement_no,omitempty"`
	StatementStatus  string            `json:"statement_status,omitempty"`
	Items            []model.OrderItem `json:"items"`
}

type SalesOrderDetailView struct {
	Order        model.Order                 `json:"order"`
	Fulfillments []SalesOrderFulfillmentView `json:"fulfillments"`
}

func (s *OrderService) GenerateOrderNo() string {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("ORD%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(random)))
}

func (s *OrderService) GenerateFulfillmentNo() string {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("FUL%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(random)))
}

func (s *OrderService) GenerateTicketCode() string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return "T" + strings.ToUpper(hex.EncodeToString(random))
}

func roundMoney(value float64) float64 {
	return math.Round(value*100) / 100
}

func (s *OrderService) Create(req *model.Order) error {
	if err := validateOrder(req); err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenant(tx, req.TenantID); err != nil {
			return err
		}
		var channelReservation *model.ChannelReservation
		if req.ChannelReservationID > 0 {
			if req.ChannelAccountID == 0 || req.Channel == "online" || req.Channel == "window" || req.ExternalNo == nil {
				return errors.New("invalid channel reservation context")
			}
			var held model.ChannelReservation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
				"id = ? AND tenant_id = ? AND channel_account_id = ? AND external_no = ? AND status = ? AND expires_at > ?",
				req.ChannelReservationID, req.TenantID, req.ChannelAccountID, *req.ExternalNo, "held", time.Now(),
			).First(&held).Error; err != nil {
				return errors.New("channel reservation is unavailable")
			}
			channelReservation = &held
		}
		if req.ExternalNo != nil {
			var count int64
			if err := tx.Model(&model.Order{}).Where(
				"tenant_id = ? AND channel = ? AND external_no = ?", req.TenantID, req.Channel, *req.ExternalNo,
			).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrDuplicateExternalOrder
			}
		}
		req.Base = model.Base{}
		req.OrderNo = s.GenerateOrderNo()
		req.Status = "unpaid"
		req.TotalAmount = 0
		expiresAt := time.Now().Add(DefaultOrderReservationTTL)
		req.ExpiresAt = &expiresAt
		policyContext := newSalePolicyContext()

		for i := range req.Items {
			item := &req.Items[i]
			item.Base = model.Base{}
			item.OrderID = 0
			item.Tickets = nil

			var listing model.Product
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").
				Where("id = ? AND tenant_id = ? AND status = ?", item.ProductID, req.TenantID, "online").
				First(&listing).Error; err != nil {
				return fmt.Errorf("product %d is unavailable", item.ProductID)
			}
			fulfillment, distributed, err := resolveFulfillmentProduct(tx, &listing, req.TenantID, req.Channel)
			if err != nil {
				return fmt.Errorf("product %s: %w", listing.Name, err)
			}
			capability := "supplier"
			if distributed {
				capability = "distributor"
			}
			if err := requireActiveTenantCapability(tx, req.TenantID, capability); err != nil {
				return err
			}
			if err := requireActiveTenantCapability(tx, fulfillment.TenantID, "supplier"); err != nil {
				return fmt.Errorf("supplier is unavailable: %w", err)
			}
			if fulfillment.ScenicAreaID == 0 {
				return errors.New("fulfillment product has no scenic area")
			}
			revision, err := ensureProductRevisionTx(tx, fulfillment)
			if err != nil {
				return fmt.Errorf("product %s revision: %w", listing.Name, err)
			}
			fulfillment.CurrentRevisionID = revision.ID

			item.ProductName = listing.Name
			item.Price = roundMoney(listing.Price)
			item.SettlementPrice = roundMoney(fulfillment.SettlementPrice)
			item.ValidityType = fulfillment.ValidityType
			item.FulfillmentProductID = fulfillment.ID
			item.FulfillmentTenantID = fulfillment.TenantID
			item.FulfillmentScenicAreaID = fulfillment.ScenicAreaID
			item.ProductOfferID = listing.ProductOfferID
			item.ProductRevisionID = revision.ID
			item.CommissionBPS = fulfillment.ResolvedCommissionBPS
			if err := applyValidity(item, fulfillment); err != nil {
				return fmt.Errorf("%s: %w", listing.Name, err)
			}
			if err := validateSalePolicyTx(tx, fulfillment, req, item, policyContext); err != nil {
				return err
			}

			if distributed {
				if err := reserveOfferQuotaTx(tx, listing.ProductOfferID, item.Quantity); err != nil {
					return err
				}
				item.OfferReservedQuantity = item.Quantity
				if err := chargeDistributionAccount(tx, req, item, req.TenantID, fulfillment.TenantID, listing.Name); err != nil {
					return err
				}
			}
			if channelReservation == nil {
				if err := reserveStock(tx, fulfillment, item.UseDate, item.StockSlot, item.Quantity); err != nil {
					return err
				}
			} else {
				if len(req.Items) != 1 || channelReservation.ProductID != item.ProductID || channelReservation.Quantity != item.Quantity || !sameOptionalDate(channelReservation.UseDate, item.UseDate) || channelReservation.StockSlot != item.StockSlot {
					return errors.New("channel reservation does not match order")
				}
			}

			req.TotalAmount = roundMoney(req.TotalAmount + item.Price*float64(item.Quantity))
			item.Tickets, err = buildTickets(s, fulfillment, item.Quantity, req)
			if err != nil {
				return fmt.Errorf("%s: %w", listing.Name, err)
			}
			if err := assignTicketVisitors(item); err != nil {
				return fmt.Errorf("%s: %w", listing.Name, err)
			}
			for ticketIndex := range item.Tickets {
				if len(item.Visitors) == 0 {
					item.Tickets[ticketIndex].VisitorName = item.VisitorName
					item.Tickets[ticketIndex].VisitorPhone = item.VisitorPhone
					item.Tickets[ticketIndex].VisitorID = item.VisitorID
					item.Tickets[ticketIndex].VisitorRegion = item.VisitorRegion
				}
			}
		}

		if err := tx.Create(req).Error; err != nil {
			return err
		}
		if channelReservation != nil {
			if err := tx.Model(channelReservation).Updates(map[string]interface{}{"status": "converted", "order_no": req.OrderNo}).Error; err != nil {
				return err
			}
		}
		// GORM fills OrderItemID for nested ticket associations, but Ticket also
		// carries a denormalized OrderID used by operational queries. Backfill it
		// inside the same transaction so the two ownership paths cannot diverge.
		if err := tx.Exec("UPDATE tickets SET order_id = ? WHERE order_item_id IN (SELECT id FROM order_items WHERE order_id = ?)", req.ID, req.ID).Error; err != nil {
			return err
		}
		if err := persistOrderVisitorsTx(tx, req); err != nil {
			return err
		}
		return createFulfillmentProjections(tx, s, req)
	})
}

func sameOptionalDate(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return startOfDay(*left).Equal(startOfDay(*right))
}

func createFulfillmentProjections(tx *gorm.DB, service *OrderService, order *model.Order) error {
	type fulfillmentKey struct {
		supplier uint
		area     uint
	}
	fulfillments := make(map[fulfillmentKey]*model.FulfillmentOrder)
	for i := range order.Items {
		item := &order.Items[i]
		key := fulfillmentKey{supplier: item.FulfillmentTenantID, area: item.FulfillmentScenicAreaID}
		fulfillment, ok := fulfillments[key]
		if !ok {
			fulfillment = &model.FulfillmentOrder{
				FulfillmentNo: service.GenerateFulfillmentNo(), SalesOrderID: order.ID,
				SalesOrderNo: order.OrderNo, SalesTenantID: order.TenantID,
				SupplierTenantID: item.FulfillmentTenantID, ScenicAreaID: item.FulfillmentScenicAreaID,
				Status: "reserved", SettlementStatus: "open",
			}
			fulfillments[key] = fulfillment
			if err := tx.Create(fulfillment).Error; err != nil {
				return err
			}
		}
		if fulfillment.ProductRevisionID == 0 {
			fulfillment.ProductRevisionID = item.ProductRevisionID
			if err := tx.Model(fulfillment).Update("product_revision_id", item.ProductRevisionID).Error; err != nil {
				return err
			}
		}
		fulfillment.SettlementAmount = roundMoney(fulfillment.SettlementAmount + item.SettlementPrice*float64(item.Quantity))
		if err := tx.Model(fulfillment).Update("settlement_amount", fulfillment.SettlementAmount).Error; err != nil {
			return err
		}
		item.FulfillmentOrderID = fulfillment.ID
		if err := tx.Model(&model.OrderItem{}).Where("id = ?", item.ID).Update("fulfillment_order_id", fulfillment.ID).Error; err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Where("order_item_id = ?", item.ID).Find(&tickets).Error; err != nil {
			return err
		}
		for i := range tickets {
			ticket := &tickets[i]
			ticket.FulfillmentOrderID = fulfillment.ID
			if err := tx.Model(ticket).Update("fulfillment_order_id", fulfillment.ID).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.TicketEntitlement{
				FulfillmentOrderID: fulfillment.ID, TicketID: ticket.ID, TicketCode: ticket.TicketCode,
				SalesTenantID: ticket.TenantID, SupplierTenantID: ticket.FulfillmentTenantID,
				ScenicAreaID: ticket.FulfillmentScenicAreaID, Status: "issued", RuleSnapshot: ticket.RuleSnapshot,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureProductRevisionTx(tx *gorm.DB, product *model.Product) (*model.ProductRevision, error) {
	if product == nil || product.ID == 0 || product.TenantID == 0 {
		return nil, errors.New("product is required")
	}
	var revision model.ProductRevision
	if product.CurrentRevisionID > 0 {
		if err := tx.Where("id = ? AND product_id = ? AND tenant_id = ?", product.CurrentRevisionID, product.ID, product.TenantID).First(&revision).Error; err == nil {
			return &revision, nil
		}
	}
	if err := tx.Where("product_id = ? AND tenant_id = ?", product.ID, product.TenantID).Order("version DESC").First(&revision).Error; err == nil {
		return &revision, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	snapshot, err := json.Marshal(product.Rule)
	if err != nil {
		return nil, err
	}
	if product.CreatedAt.IsZero() {
		product.CreatedAt = time.Now()
	}
	revision = model.ProductRevision{ProductID: product.ID, TenantID: product.TenantID, ScenicAreaID: product.ScenicAreaID, Version: 1, Status: "active", PriceCents: moneyCents(product.Price), SettlementCents: moneyCents(product.SettlementPrice), SnapshotJSON: string(snapshot), EffectiveFrom: product.CreatedAt}
	if err := tx.Create(&revision).Error; err != nil {
		return nil, err
	}
	_ = tx.Model(&model.Product{}).Where("id = ?", product.ID).Update("current_revision_id", revision.ID).Error
	return &revision, nil
}

// resolveFulfillmentProduct turns a seller listing into the supplier product
// that owns inventory, ticket rules, validity, and admission rights. The
// listing's legacy Source* fields are accepted only as a migration fallback;
// all authorization and pricing checks happen against the current supplier
// records inside the write transaction.
func resolveFulfillmentProduct(tx *gorm.DB, listing *model.Product, sellerTenantID uint, channel string) (*model.Product, bool, error) {
	if listing.ProductOfferID > 0 {
		var offer model.ProductOffer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"id = ? AND distributor_tenant_id = ? AND status = ?", listing.ProductOfferID, sellerTenantID, "active",
		).First(&offer).Error; err != nil {
			return nil, false, fmt.Errorf("product offer is unavailable")
		}
		now := time.Now()
		if offer.SalesStartAt != nil && now.Before(*offer.SalesStartAt) {
			return nil, false, fmt.Errorf("product offer sales period has not started")
		}
		if offer.SalesEndAt != nil && now.After(*offer.SalesEndAt) {
			return nil, false, fmt.Errorf("product offer sales period has ended")
		}
		if !offerAllowsChannel(offer.AllowedChannels, channel) {
			return nil, false, fmt.Errorf("product offer does not allow channel %s", channel)
		}
		if offer.MinimumRetailPriceCents > 0 && moneyCents(listing.Price) < offer.MinimumRetailPriceCents {
			return nil, false, fmt.Errorf("listing price is below the supplier minimum retail price")
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", sellerTenantID, offer.SupplierTenantID, "active").First(&relationship).Error; err != nil {
			return nil, false, fmt.Errorf("active distribution relationship not found")
		}
		var supplier model.Tenant
		if err := tx.Select("id", "status").First(&supplier, offer.SupplierTenantID).Error; err != nil || (supplier.Status != "" && supplier.Status != "active") {
			return nil, false, fmt.Errorf("supplier tenant is unavailable")
		}
		var source model.Product
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").
			Where("id = ? AND tenant_id = ? AND status = ?", offer.SourceProductID, offer.SupplierTenantID, "online").
			First(&source).Error; err != nil {
			return nil, false, fmt.Errorf("source product is unavailable")
		}
		if !source.IsDistributable {
			return nil, false, fmt.Errorf("source product is not available for distribution")
		}
		if offer.ProductRevisionID == 0 || source.CurrentRevisionID != offer.ProductRevisionID {
			return nil, false, fmt.Errorf("product offer revision is no longer active")
		}
		source.SettlementPrice = roundMoney(offer.SettlementPrice)
		source.ResolvedCommissionBPS = offer.CommissionBPS
		return &source, true, nil
	}
	if listing.SourceProductID == 0 && listing.SourceTenantID == 0 &&
		(listing.FulfillmentProductID == 0 || listing.FulfillmentProductID == listing.ID) &&
		(listing.FulfillmentTenantID == 0 || listing.FulfillmentTenantID == sellerTenantID) {
		return listing, false, nil
	}
	return nil, false, fmt.Errorf("distributed products require an active supplier product offer")
}

func offerAllowsChannel(allowed, channel string) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" {
		return false
	}
	for _, value := range strings.Split(allowed, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || value == channel {
			return true
		}
	}
	return false
}

func reserveOfferQuotaTx(tx *gorm.DB, offerID uint, quantity int) error {
	if offerID == 0 || quantity <= 0 {
		return nil
	}
	result := tx.Model(&model.ProductOffer{}).
		Where("id = ? AND status = ? AND (quota = 0 OR reserved_quantity + ? <= quota)", offerID, "active", quantity).
		UpdateColumn("reserved_quantity", gorm.Expr("reserved_quantity + ?", quantity))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("product offer quota is exhausted")
	}
	return nil
}

func releaseOfferQuotaTx(tx *gorm.DB, offerID uint, quantity int) error {
	if offerID == 0 || quantity <= 0 {
		return nil
	}
	result := tx.Model(&model.ProductOffer{}).
		Where("id = ? AND reserved_quantity >= ?", offerID, quantity).
		UpdateColumn("reserved_quantity", gorm.Expr("reserved_quantity - ?", quantity))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("product offer quota reservation is inconsistent")
	}
	return nil
}

func validateOrder(req *model.Order) error {
	if req.TenantID == 0 {
		return fmt.Errorf("tenant is required")
	}
	if len(req.Items) == 0 {
		return fmt.Errorf("order must contain at least one item")
	}
	if len(req.ContactName) > 50 || len(req.ContactPhone) > 20 {
		return fmt.Errorf("contact information is too long")
	}
	if req.Channel == "" {
		req.Channel = "online"
	}
	if req.ChannelAccountID == 0 && req.Channel != "online" && req.Channel != "ota" && req.Channel != "window" {
		return fmt.Errorf("invalid order channel")
	}
	if len(req.Channel) > 50 {
		return fmt.Errorf("order channel is too long")
	}
	if req.ExternalNo != nil {
		externalNo := strings.TrimSpace(*req.ExternalNo)
		if externalNo == "" {
			req.ExternalNo = nil
		} else if len(externalNo) > 100 {
			return fmt.Errorf("external order number is too long")
		} else {
			req.ExternalNo = &externalNo
		}
	}
	for _, item := range req.Items {
		if item.ProductID == 0 || item.Quantity <= 0 || item.Quantity > 1000 {
			return fmt.Errorf("item quantity must be between 1 and 1000")
		}
	}
	return nil
}

func applyValidity(item *model.OrderItem, product *model.Product) error {
	now := time.Now()
	if item.UseDate != nil {
		normalized := startOfDay(*item.UseDate)
		item.UseDate = &normalized
	}

	switch product.ValidityType {
	case "date":
		item.ValidityStart = product.ValidityStartDate
		item.ValidityEnd = product.ValidityEndDate
		if item.UseDate != nil {
			if product.ValidityStartDate != nil && item.UseDate.Before(startOfDay(*product.ValidityStartDate)) {
				return fmt.Errorf("visit date is before the validity period")
			}
			if product.ValidityEndDate != nil && item.UseDate.After(startOfDay(*product.ValidityEndDate)) {
				return fmt.Errorf("visit date is after the validity period")
			}
		}
	case "days":
		start := now
		if item.UseDate != nil {
			start = startOfDay(*item.UseDate)
		}
		end := start.AddDate(0, 0, product.ValidityDays)
		end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 0, end.Location())
		item.ValidityStart = &start
		item.ValidityEnd = &end
	default:
		return fmt.Errorf("invalid validity type")
	}

	if product.StockType == "daily" && item.UseDate == nil {
		return fmt.Errorf("visit date is required for daily stock")
	}
	return nil
}

func startOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func reserveStock(tx *gorm.DB, product *model.Product, useDate *time.Time, stockSlot string, quantity int) error {
	switch product.StockType {
	case "", "unlimited":
		return nil
	case "total":
		result := tx.Model(&model.Product{}).
			Where("id = ? AND daily_stock >= ?", product.ID, quantity).
			UpdateColumn("daily_stock", gorm.Expr("daily_stock - ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("insufficient stock for %s", product.Name)
		}
		return nil
	case "daily":
		if useDate == nil {
			return fmt.Errorf("visit date is required for daily stock")
		}
		stockDate := startOfDay(*useDate)
		capacity, err := slotCapacity(product, stockSlot)
		if err != nil {
			return err
		}
		inventory := model.ProductInventory{
			TenantID: product.TenantID, ProductID: product.ID, StockDate: stockDate, StockSlot: stockSlot, Capacity: capacity,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&inventory).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ProductInventory{}).
			Where("tenant_id = ? AND product_id = ? AND stock_date = ? AND stock_slot = ? AND sold + ? <= capacity", product.TenantID, product.ID, stockDate, stockSlot, quantity).
			UpdateColumn("sold", gorm.Expr("sold + ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("insufficient stock for %s on %s", product.Name, stockDate.Format("2006-01-02"))
		}
		return nil
	default:
		return fmt.Errorf("invalid stock type for %s", product.Name)
	}
}

func releaseStock(tx *gorm.DB, product *model.Product, useDate *time.Time, stockSlot string, quantity int) error {
	switch product.StockType {
	case "", "unlimited":
		return nil
	case "total":
		return tx.Unscoped().Model(&model.Product{}).Where("id = ?", product.ID).
			UpdateColumn("daily_stock", gorm.Expr("daily_stock + ?", quantity)).Error
	case "daily":
		if useDate == nil {
			return fmt.Errorf("daily stock reservation has no visit date")
		}
		result := tx.Model(&model.ProductInventory{}).
			Where("tenant_id = ? AND product_id = ? AND stock_date = ? AND stock_slot = ? AND sold >= ?", product.TenantID, product.ID, startOfDay(*useDate), stockSlot, quantity).
			UpdateColumn("sold", gorm.Expr("sold - ?", quantity))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("stock reservation is inconsistent")
		}
		return nil
	default:
		return fmt.Errorf("invalid stock type for %s", product.Name)
	}
}

func chargeDistributionAccount(tx *gorm.DB, order *model.Order, item *model.OrderItem, sellerTenantID, supplierTenantID uint, productName string) error {
	costCents := moneyCents(item.SettlementPrice) * int64(item.Quantity)
	if costCents <= 0 {
		return errors.New("distribution settlement cost must be positive")
	}
	var account model.CapitalAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ? AND status = ?", sellerTenantID, supplierTenantID, "active").
		First(&account).Error; err != nil {
		return fmt.Errorf("distribution capital account is unavailable")
	}
	syncCapitalAccountCents(&account)
	availableCreditCents := account.CreditLineCents - account.UsedCreditCents
	if availableCreditCents < 0 {
		return errors.New("distribution credit projection is invalid")
	}
	if account.BalanceCents+availableCreditCents < costCents {
		return fmt.Errorf("insufficient distribution balance")
	}
	cashUsedCents := account.BalanceCents
	if cashUsedCents > costCents {
		cashUsedCents = costCents
	}
	creditUsedCents := costCents - cashUsedCents
	item.CashCostCents = cashUsedCents
	item.CreditCostCents = creditUsedCents
	account.BalanceCents -= cashUsedCents
	account.UsedCreditCents += creditUsedCents
	syncCapitalAccountProjection(&account)
	if err := tx.Save(&account).Error; err != nil {
		return err
	}
	if err := tx.Create(&model.TransactionRecord{
		AccountID: account.ID, Type: "payment", Amount: -centsMoney(costCents), BalanceAfter: account.Balance,
		AmountCents: -costCents, BalanceAfterCents: account.BalanceCents,
		RelatedOrderNo: order.OrderNo, Memo: fmt.Sprintf("distribution purchase: %s x%d", productName, item.Quantity),
	}).Error; err != nil {
		return err
	}
	memo := fmt.Sprintf("distribution purchase: %s x%d", productName, item.Quantity)
	if item.CashCostCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "reservation_cash", -item.CashCostCents, "order:"+order.OrderNo+":item:"+ledgerItemKey(item)+":cash", order.OrderNo, "", memo, 0); err != nil {
			return err
		}
	}
	if item.CreditCostCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "reservation_credit", item.CreditCostCents, "order:"+order.OrderNo+":item:"+ledgerItemKey(item)+":credit", order.OrderNo, "", memo, 0); err != nil {
			return err
		}
	}
	return nil
}

func refundDistributionAccount(tx *gorm.DB, order *model.Order, item *model.OrderItem, sellerTenantID, supplierTenantID uint, productName string) error {
	amountCents := moneyCents(item.SettlementPrice) * int64(item.Quantity)
	if amountCents <= 0 {
		return errors.New("distribution refund amount must be positive")
	}
	var account model.CapitalAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ?", sellerTenantID, supplierTenantID).
		First(&account).Error; err != nil {
		return err
	}
	syncCapitalAccountCents(&account)
	creditRepaidCents := item.CreditCostCents
	if item.CreditCostCents == 0 {
		// Compatibility fallback for orders created before the original-cost
		// snapshot was migrated.
		creditRepaidCents = account.UsedCreditCents
		if creditRepaidCents > amountCents {
			creditRepaidCents = amountCents
		}
	}
	if creditRepaidCents > amountCents {
		creditRepaidCents = amountCents
	}
	account.UsedCreditCents -= creditRepaidCents
	account.BalanceCents += amountCents - creditRepaidCents
	syncCapitalAccountProjection(&account)
	if err := tx.Save(&account).Error; err != nil {
		return err
	}
	if err := tx.Create(&model.TransactionRecord{
		AccountID: account.ID, Type: "refund", Amount: centsMoney(amountCents), BalanceAfter: account.Balance,
		AmountCents: amountCents, BalanceAfterCents: account.BalanceCents,
		RelatedOrderNo: order.OrderNo, Memo: fmt.Sprintf("cancelled distribution purchase: %s x%d", productName, item.Quantity),
	}).Error; err != nil {
		return err
	}
	memo := fmt.Sprintf("cancelled distribution purchase: %s x%d", productName, item.Quantity)
	if item.CashCostCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "release_cash", item.CashCostCents, "release:"+order.OrderNo+":item:"+ledgerItemKey(item)+":cash", order.OrderNo, "", memo, 0); err != nil {
			return err
		}
	}
	if item.CreditCostCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "release_credit", -item.CreditCostCents, "release:"+order.OrderNo+":item:"+ledgerItemKey(item)+":credit", order.OrderNo, "", memo, 0); err != nil {
			return err
		}
	}
	return nil
}

func refundDistributionAllocation(tx *gorm.DB, order *model.Order, item *model.OrderItem, sellerTenantID, supplierTenantID uint, cashCents, creditCents int64, idempotencyPrefix, productName string) error {
	if cashCents < 0 || creditCents < 0 || cashCents+creditCents <= 0 {
		return errors.New("invalid distribution refund allocation")
	}
	var account model.CapitalAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("owner_tenant_id = ? AND manager_tenant_id = ?", sellerTenantID, supplierTenantID).
		First(&account).Error; err != nil {
		return err
	}
	syncCapitalAccountCents(&account)
	if account.UsedCreditCents < creditCents {
		return errors.New("distribution credit refund exceeds used credit")
	}
	account.UsedCreditCents -= creditCents
	account.BalanceCents += cashCents
	syncCapitalAccountProjection(&account)
	if err := tx.Save(&account).Error; err != nil {
		return err
	}
	totalCents := cashCents + creditCents
	if err := tx.Create(&model.TransactionRecord{
		AccountID: account.ID, Type: "refund", Amount: centsMoney(totalCents), BalanceAfter: account.Balance,
		AmountCents: totalCents, BalanceAfterCents: account.BalanceCents,
		RelatedOrderNo: order.OrderNo, Memo: fmt.Sprintf("refunded distribution purchase: %s", productName),
	}).Error; err != nil {
		return err
	}
	if cashCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "refund_cash", cashCents, idempotencyPrefix+":cash", order.OrderNo, "", productName, 0); err != nil {
			return err
		}
	}
	if creditCents > 0 {
		if err := appendLedgerEntryTx(tx, &account, "refund_credit", -creditCents, idempotencyPrefix+":credit", order.OrderNo, "", productName, 0); err != nil {
			return err
		}
	}
	return nil
}

func ledgerItemKey(item *model.OrderItem) string {
	if item.ID > 0 {
		return fmt.Sprint(item.ID)
	}
	return fmt.Sprintf("product-%d", item.ProductID)
}

func buildTickets(service *OrderService, product *model.Product, quantity int, order *model.Order) ([]model.Ticket, error) {
	count := quantity
	if product.CodeMode == "order" {
		count = 1
	}
	ruleSnapshot, err := json.Marshal(product.Rule)
	if err != nil {
		return nil, fmt.Errorf("snapshot ticket rule: %w", err)
	}
	tickets := make([]model.Ticket, count)
	for i := range tickets {
		tickets[i] = model.Ticket{
			TicketCode: service.GenerateTicketCode(), Status: "unused", TenantID: order.TenantID,
			FulfillmentProductID: product.ID, FulfillmentTenantID: product.TenantID,
			ScenicAreaID: product.ScenicAreaID, FulfillmentScenicAreaID: product.ScenicAreaID,
			RuleSnapshot: string(ruleSnapshot), CodeMode: product.CodeMode,
			ProductRevisionID: product.CurrentRevisionID,
			VisitorName:       order.ContactName, VisitorPhone: order.ContactPhone, VisitorID: order.VisitorID,
		}
	}
	return tickets, nil
}

func assignTicketVisitors(item *model.OrderItem) error {
	if item == nil || len(item.Visitors) == 0 {
		return nil
	}
	if len(item.Visitors) != len(item.Tickets) {
		return errors.New("visitor count does not match ticket count")
	}
	for i := range item.Tickets {
		visitor := item.Visitors[i]
		item.Tickets[i].VisitorName = visitor.Name
		item.Tickets[i].VisitorPhone = visitor.Phone
		item.Tickets[i].VisitorID = visitor.IdentityNo
		item.Tickets[i].VisitorRegion = visitor.Region
	}
	return nil
}

func persistOrderVisitorsTx(tx *gorm.DB, order *model.Order) error {
	if order == nil || order.ID == 0 {
		return errors.New("order is required")
	}
	for itemIndex := range order.Items {
		item := &order.Items[itemIndex]
		if len(item.Visitors) == 0 {
			continue
		}
		if len(item.Visitors) != len(item.Tickets) {
			return errors.New("visitor count does not match ticket count")
		}
		for index, visitor := range item.Visitors {
			ticket := item.Tickets[index]
			if err := tx.Create(&model.OrderVisitor{
				TenantID: order.TenantID, OrderID: order.ID, OrderItemID: item.ID,
				TicketID: ticket.ID, TicketCode: ticket.TicketCode, Sequence: index + 1,
				Name: visitor.Name, Phone: visitor.Phone, IdentityNo: visitor.IdentityNo, Region: visitor.Region,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *OrderService) List(page, pageSize int, tenantID uint, status, channel, startDate, endDate, search string) ([]model.Order, int64, error) {
	var orders []model.Order
	var total int64
	if tenantID == 0 {
		return nil, 0, fmt.Errorf("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	query := model.DB.Model(&model.Order{}).Preload("Items").Preload("Items.Tickets").Preload("Items.VisitorRecords").Where("tenant_id = ?", tenantID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if channel != "" {
		query = query.Where("channel = ?", channel)
	}
	if startDate != "" {
		query = query.Where("created_at >= ?", startDate+" 00:00:00")
	}
	if endDate != "" {
		query = query.Where("created_at <= ?", endDate+" 23:59:59")
	}
	if search != "" {
		like := "%" + search + "%"
		query = query.Where("order_no LIKE ? OR external_no LIKE ? OR contact_name LIKE ? OR contact_phone LIKE ?", like, like, like, like)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("created_at desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}

func (s *OrderService) GetByOrderNo(orderNo string, tenantID uint) (*model.Order, error) {
	var order model.Order
	err := model.DB.Preload("Items").Preload("Items.Tickets").Preload("Items.VisitorRecords").
		Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error
	return &order, err
}

// GetDetail returns the seller-owned order together with its supplier
// responsibilities. The sales tenant may see every fulfillment created from
// its order, while supplier-only operational details remain on the scoped
// distribution fulfillment endpoint.
func (s *OrderService) GetDetail(orderNo string, tenantID uint) (*SalesOrderDetailView, error) {
	if tenantID == 0 || strings.TrimSpace(orderNo) == "" {
		return nil, errors.New("tenant and order number are required")
	}
	order, err := s.GetByOrderNo(orderNo, tenantID)
	if err != nil {
		return nil, err
	}
	var fulfillments []model.FulfillmentOrder
	if err := model.DB.Where("sales_order_id = ? AND sales_tenant_id = ?", order.ID, tenantID).Order("id ASC").Find(&fulfillments).Error; err != nil {
		return nil, err
	}
	itemsByFulfillment := make(map[uint][]model.OrderItem)
	for i := range order.Items {
		item := order.Items[i]
		itemsByFulfillment[item.FulfillmentOrderID] = append(itemsByFulfillment[item.FulfillmentOrderID], item)
	}
	views := make([]SalesOrderFulfillmentView, 0, len(fulfillments))
	for i := range fulfillments {
		fulfillment := &fulfillments[i]
		var supplier model.Tenant
		if err := model.DB.Select("id", "name").Where("id = ?", fulfillment.SupplierTenantID).First(&supplier).Error; err != nil {
			return nil, err
		}
		var area model.ScenicArea
		if err := model.DB.Select("id", "name").Where("id = ? AND tenant_id = ?", fulfillment.ScenicAreaID, fulfillment.SupplierTenantID).First(&area).Error; err != nil {
			return nil, err
		}
		items := itemsByFulfillment[fulfillment.ID]
		view := SalesOrderFulfillmentView{
			ID: fulfillment.ID, FulfillmentNo: fulfillment.FulfillmentNo,
			SupplierTenantID: fulfillment.SupplierTenantID, SupplierName: supplier.Name,
			ScenicAreaID: fulfillment.ScenicAreaID, ScenicAreaName: area.Name,
			Status: fulfillment.Status, SettlementStatus: fulfillment.SettlementStatus,
			CanVerify: fulfillment.SupplierTenantID == tenantID, Items: items,
		}
		for itemIndex := range items {
			for ticketIndex := range items[itemIndex].Tickets {
				view.TicketCount++
				switch items[itemIndex].Tickets[ticketIndex].Status {
				case "used":
					view.UsedCount++
				case "refunded":
					view.RefundedCount++
				}
			}
		}
		gross, refunded, commission, err := settlementAmountsForFulfillment(model.DB, fulfillment)
		if err != nil {
			return nil, err
		}
		view.GrossCents, view.RefundCents, view.CommissionCents = gross, refunded, commission
		view.NetCents = gross - refunded - commission
		var line model.SettlementLine
		if err := model.DB.Where("fulfillment_order_id = ?", fulfillment.ID).First(&line).Error; err == nil {
			view.StatementID = line.StatementID
			var statement model.SettlementStatement
			if err := model.DB.Where(
				"id = ? AND supplier_tenant_id = ? AND distributor_tenant_id = ?",
				line.StatementID, fulfillment.SupplierTenantID, tenantID,
			).First(&statement).Error; err != nil {
				return nil, err
			}
			view.StatementNo, view.StatementStatus = statement.StatementNo, statement.Status
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		views = append(views, view)
	}
	return &SalesOrderDetailView{Order: *order, Fulfillments: views}, nil
}

func (s *OrderService) GetByExternalNo(externalNo, channel string, tenantID uint) (*model.Order, error) {
	var order model.Order
	err := model.DB.Preload("Items").Preload("Items.Tickets").Preload("Items.VisitorRecords").
		Where("external_no = ? AND channel = ? AND tenant_id = ?", externalNo, channel, tenantID).First(&order).Error
	return &order, err
}

func (s *OrderService) Cancel(orderNo string, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items.Tickets").
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		return cancelOrderTx(tx, &order)
	})
}

// ExpireUnpaid releases every due reservation in one serialized write
// transaction. The status transition is the idempotency key: a retry sees a
// cancelled order and cannot release the same stock or funds twice.
func (s *OrderService) ExpireUnpaid(now time.Time) (int, error) {
	count := 0
	err := model.Write(func(tx *gorm.DB) error {
		var orders []model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items.Tickets").
			Where("orders.status = ? AND orders.expires_at IS NOT NULL AND orders.expires_at <= ?", "unpaid", now).
			Where("NOT EXISTS (SELECT 1 FROM payments WHERE payments.tenant_id = orders.tenant_id AND payments.order_no = orders.order_no AND payments.status IN ?)", []string{"pending", "paid", "partial_refunded"}).
			Find(&orders).Error; err != nil {
			return err
		}
		for i := range orders {
			if err := cancelOrderTx(tx, &orders[i]); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func cancelOrderTx(tx *gorm.DB, order *model.Order) error {
	if order.Status == "cancelled" {
		return nil
	}
	if order.Status != "unpaid" && !(order.Status == "paid" && order.Channel == "ota") {
		return fmt.Errorf("paid orders must use the refund workflow")
	}
	var pendingPayments int64
	if err := tx.Model(&model.Payment{}).Where("tenant_id = ? AND order_no = ? AND status = ?", order.TenantID, order.OrderNo, "pending").Count(&pendingPayments).Error; err != nil {
		return err
	}
	if pendingPayments > 0 {
		return errors.New("order has a pending provider payment and cannot be cancelled")
	}
	var collectedPayments int64
	if err := tx.Model(&model.Payment{}).Where("tenant_id = ? AND order_no = ? AND status IN ?", order.TenantID, order.OrderNo, []string{"paid", "partial_refunded"}).Count(&collectedPayments).Error; err != nil {
		return err
	}
	if collectedPayments > 0 {
		return errors.New("order has collected payments and must use the refund workflow")
	}
	for _, item := range order.Items {
		for _, ticket := range item.Tickets {
			if ticket.CheckInCount > 0 || ticket.Status == "used" {
				return fmt.Errorf("used orders cannot be cancelled")
			}
		}
	}

	for i := range order.Items {
		item := &order.Items[i]
		var listing model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", item.ProductID, order.TenantID).First(&listing).Error; err != nil {
			return err
		}
		stockProduct, distributed, err := loadStoredFulfillmentProduct(tx, &listing, item, order.TenantID)
		if err != nil {
			return err
		}
		if distributed {
			if err := refundDistributionAccount(tx, order, item, order.TenantID, stockProduct.TenantID, listing.Name); err != nil {
				return err
			}
			quotaQuantity := item.OfferReservedQuantity
			if quotaQuantity == 0 {
				quotaQuantity = item.Quantity
			}
			if err := releaseOfferQuotaTx(tx, item.ProductOfferID, quotaQuantity); err != nil {
				return err
			}
		}
		if err := releaseStock(tx, stockProduct, item.UseDate, item.StockSlot, item.Quantity); err != nil {
			return err
		}
	}

	if err := tx.Model(order).Update("status", "cancelled").Error; err != nil {
		return err
	}
	var itemIDs []uint
	if err := tx.Model(&model.OrderItem{}).Where("order_id = ?", order.ID).Pluck("id", &itemIDs).Error; err != nil {
		return err
	}
	if len(itemIDs) > 0 {
		if err := tx.Model(&model.Ticket{}).Where("order_item_id IN ?", itemIDs).Update("status", "void").Error; err != nil {
			return err
		}
		if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id IN (SELECT id FROM tickets WHERE order_item_id IN ?)", itemIDs).Update("status", "void").Error; err != nil {
			return err
		}
	}
	return updateFulfillmentOrdersTx(tx, order.ID, "cancelled")
}

func updateFulfillmentOrdersTx(tx *gorm.DB, salesOrderID uint, status string) error {
	return tx.Model(&model.FulfillmentOrder{}).Where("sales_order_id = ?", salesOrderID).Update("status", status).Error
}

// loadStoredFulfillmentProduct resolves cancellation/refund using the
// ownership captured on the order item. It intentionally does not require the
// source product to remain online or the relationship to remain active: a
// previously reserved supplier stock unit and supplier account still need to
// be released when a listing is retired.
func loadStoredFulfillmentProduct(tx *gorm.DB, listing *model.Product, item *model.OrderItem, sellerTenantID uint) (*model.Product, bool, error) {
	productID := item.FulfillmentProductID
	supplierTenantID := item.FulfillmentTenantID
	if productID == 0 && listing.FulfillmentProductID > 0 {
		productID = listing.FulfillmentProductID
		supplierTenantID = listing.FulfillmentTenantID
	}
	if productID == 0 && listing.SourceProductID > 0 {
		productID = listing.SourceProductID
		supplierTenantID = listing.SourceTenantID
	}
	if productID == 0 && supplierTenantID == 0 {
		return listing, false, nil
	}
	if productID == listing.ID && supplierTenantID == sellerTenantID {
		return listing, false, nil
	}
	if productID == 0 || supplierTenantID == 0 || supplierTenantID == sellerTenantID {
		return nil, false, fmt.Errorf("invalid stored fulfillment ownership")
	}
	var source model.Product
	if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", productID, supplierTenantID).First(&source).Error; err != nil {
		return nil, false, fmt.Errorf("stored fulfillment product is unavailable")
	}
	return &source, true, nil
}

func (s *OrderService) MarkAsPaid(orderNo string, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status == "paid" {
			return nil
		}
		if order.Status != "unpaid" {
			return fmt.Errorf("order cannot be paid from status %s", order.Status)
		}
		if err := tx.Model(&order).Update("status", "paid").Error; err != nil {
			return err
		}
		return updateFulfillmentOrdersTx(tx, order.ID, "paid")
	})
}
