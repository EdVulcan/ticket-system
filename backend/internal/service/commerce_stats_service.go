package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// ErrCommerceStatsInvalid indicates a malformed or unsupported report query.
var ErrCommerceStatsInvalid = errors.New("commerce statistics query is invalid")

// CommerceStatsQuery is intentionally server-owned: TenantID is taken from
// the authenticated context by the controller and is never decoded from the
// request body.
type CommerceStatsQuery struct {
	TenantID     uint
	BusinessType string
	LocationID   uint
	StartAt      *time.Time
	EndAt        *time.Time
}

type CommerceProductSalesStat struct {
	ProductID   uint   `json:"product_id" gorm:"column:product_id"`
	SKUId       uint   `json:"sku_id" gorm:"column:sku_id"`
	ProductName string `json:"product_name" gorm:"column:product_name"`
	SKUName     string `json:"sku_name" gorm:"column:sku_name"`
	Quantity    int64  `json:"quantity" gorm:"column:quantity"`
	AmountCents int64  `json:"amount_cents" gorm:"column:amount_cents"`
}

type CommerceFulfillmentStat struct {
	Method string `json:"method"`
	Count  int64  `json:"count"`
}

// CommerceStats is a read-only projection. Monetary fields are integer cents
// and are computed from immutable order/after-sale facts in the database.
type CommerceStats struct {
	BusinessType                 string                     `json:"business_type"`
	LocationID                   uint                       `json:"location_id,omitempty"`
	StartDate                    string                     `json:"start_date,omitempty"`
	EndDate                      string                     `json:"end_date,omitempty"`
	TotalOrders                  int64                      `json:"total_orders"`
	PaidOrders                   int64                      `json:"paid_orders"`
	PendingOrders                int64                      `json:"pending_orders"`
	CancelledOrders              int64                      `json:"cancelled_orders"`
	OriginalAmountCents          int64                      `json:"original_amount_cents"`
	DiscountCents                int64                      `json:"discount_cents"`
	TotalAmountCents             int64                      `json:"total_amount_cents"`
	RefundedAmountCents          int64                      `json:"refunded_amount_cents"`
	NetAmountCents               int64                      `json:"net_amount_cents"`
	RefundedOrderCount           int64                      `json:"refunded_order_count"`
	ProductQuantity              int64                      `json:"product_quantity"`
	FulfillmentMethods           []CommerceFulfillmentStat  `json:"fulfillment_methods"`
	AssistSuccessCount           int64                      `json:"assist_success_count"`
	NewOrderNotificationCount    int64                      `json:"new_order_notification_count"`
	UnreadOrderNotificationCount int64                      `json:"unread_order_notification_count"`
	TopProducts                  []CommerceProductSalesStat `json:"top_products"`
}

type CommerceStatsService struct{ DB *gorm.DB }

func (s *CommerceStatsService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func validCommerceStatsBusinessType(value string) bool {
	return value == "restaurant" || value == "retail"
}

func (s *CommerceStatsService) queryScope(q CommerceStatsQuery) (*gorm.DB, error) {
	if q.TenantID == 0 || !validCommerceStatsBusinessType(strings.TrimSpace(q.BusinessType)) {
		return nil, fmt.Errorf("%w: tenant and business type are required", ErrCommerceStatsInvalid)
	}
	if q.StartAt != nil && q.EndAt != nil && !q.EndAt.After(*q.StartAt) {
		return nil, fmt.Errorf("%w: end date must be after start date", ErrCommerceStatsInvalid)
	}
	db := s.db()
	if db == nil {
		return nil, fmt.Errorf("%w: database is unavailable", ErrCommerceStatsInvalid)
	}
	if err := RequireConfiguredTenantBusinessCapability(db, q.TenantID, q.BusinessType); err != nil {
		return nil, err
	}
	base := db.Model(&model.CommerceOrder{}).
		Where("commerce_orders.tenant_id = ? AND commerce_orders.business_type = ?", q.TenantID, q.BusinessType)
	if q.LocationID != 0 {
		base = base.Where("commerce_orders.location_id = ?", q.LocationID)
	}
	if q.StartAt != nil {
		base = base.Where("commerce_orders.created_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		base = base.Where("commerce_orders.created_at < ?", *q.EndAt)
	}
	return base, nil
}

type commerceOrderAggregate struct {
	TotalOrders         int64 `gorm:"column:total_orders"`
	PaidOrders          int64 `gorm:"column:paid_orders"`
	PendingOrders       int64 `gorm:"column:pending_orders"`
	CancelledOrders     int64 `gorm:"column:cancelled_orders"`
	OriginalAmountCents int64 `gorm:"column:original_amount_cents"`
	DiscountCents       int64 `gorm:"column:discount_cents"`
	TotalAmountCents    int64 `gorm:"column:total_amount_cents"`
}

// Get returns a consistent set of report projections. Each fact family is
// queried separately so order totals cannot be multiplied by item, refund or
// notification rows.
func (s *CommerceStatsService) Get(q CommerceStatsQuery) (*CommerceStats, error) {
	q.BusinessType = strings.TrimSpace(q.BusinessType)
	base, err := s.queryScope(q)
	if err != nil {
		return nil, err
	}
	var aggregate commerceOrderAggregate
	if err := base.Select(`
		COUNT(*) AS total_orders,
		COALESCE(SUM(CASE WHEN commerce_orders.payment_status IN ('paid','refunded') THEN 1 ELSE 0 END), 0) AS paid_orders,
		COALESCE(SUM(CASE WHEN commerce_orders.payment_status = 'paid' AND commerce_orders.refund_status NOT IN ('refunded','rejected') AND commerce_orders.fulfillment_status NOT IN ('completed','cancelled') THEN 1 ELSE 0 END), 0) AS pending_orders,
		COALESCE(SUM(CASE WHEN commerce_orders.fulfillment_status = 'cancelled' OR commerce_orders.payment_status = 'failed' THEN 1 ELSE 0 END), 0) AS cancelled_orders,
		COALESCE(SUM(CASE WHEN commerce_orders.payment_status IN ('paid','refunded') THEN commerce_orders.original_amount_cents ELSE 0 END), 0) AS original_amount_cents,
		COALESCE(SUM(CASE WHEN commerce_orders.payment_status IN ('paid','refunded') THEN commerce_orders.discount_cents ELSE 0 END), 0) AS discount_cents,
		COALESCE(SUM(CASE WHEN commerce_orders.payment_status IN ('paid','refunded') THEN commerce_orders.total_amount_cents ELSE 0 END), 0) AS total_amount_cents`).Scan(&aggregate).Error; err != nil {
		return nil, err
	}
	result := &CommerceStats{BusinessType: q.BusinessType, LocationID: q.LocationID,
		TotalOrders: aggregate.TotalOrders, PaidOrders: aggregate.PaidOrders,
		PendingOrders: aggregate.PendingOrders, CancelledOrders: aggregate.CancelledOrders,
		OriginalAmountCents: aggregate.OriginalAmountCents, DiscountCents: aggregate.DiscountCents,
		TotalAmountCents:   aggregate.TotalAmountCents,
		FulfillmentMethods: []CommerceFulfillmentStat{}, TopProducts: []CommerceProductSalesStat{}}
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	if q.StartAt != nil {
		result.StartDate = q.StartAt.In(shanghai).Format("2006-01-02")
	}
	if q.EndAt != nil {
		result.EndDate = q.EndAt.Add(-time.Nanosecond).In(shanghai).Format("2006-01-02")
	}

	// Refunds are limited to completed after-sales for the same scoped orders.
	orderIDs := base.Select("commerce_orders.id")
	var refunds struct {
		Amount int64 `gorm:"column:amount"`
		Count  int64 `gorm:"column:count"`
	}
	if err := s.db().Model(&model.CommerceAfterSaleRequest{}).
		Where("commerce_after_sale_requests.tenant_id = ? AND commerce_after_sale_requests.status = 'completed' AND commerce_after_sale_requests.order_id IN (?)", q.TenantID, orderIDs).
		Select("COALESCE(SUM(CASE WHEN provider_refund_amount_cents > 0 THEN provider_refund_amount_cents ELSE amount_cents END), 0) AS amount, COUNT(DISTINCT order_id) AS count").Scan(&refunds).Error; err != nil {
		return nil, err
	}
	result.RefundedAmountCents, result.RefundedOrderCount = refunds.Amount, refunds.Count
	result.NetAmountCents = result.TotalAmountCents - result.RefundedAmountCents

	itemQuery := s.db().Table("commerce_order_items AS i").Select(`i.product_id, i.sku_id, i.product_name_snapshot AS product_name, i.sku_name_snapshot AS sku_name, SUM(i.quantity) AS quantity, SUM(i.line_amount_cents) AS amount_cents`).
		Joins("JOIN commerce_orders AS o ON o.id = i.order_id AND o.tenant_id = i.tenant_id").
		Where("i.tenant_id = ? AND o.business_type = ? AND o.payment_status IN ('paid','refunded')", q.TenantID, q.BusinessType).Group("i.product_id, i.sku_id, i.product_name_snapshot, i.sku_name_snapshot").Order("quantity DESC, amount_cents DESC").Limit(10)
	if q.LocationID != 0 {
		itemQuery = itemQuery.Where("o.location_id = ?", q.LocationID)
	}
	if q.StartAt != nil {
		itemQuery = itemQuery.Where("o.created_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		itemQuery = itemQuery.Where("o.created_at < ?", *q.EndAt)
	}
	if err := itemQuery.Scan(&result.TopProducts).Error; err != nil {
		return nil, err
	}
	var productQuantity struct {
		Quantity int64 `gorm:"column:quantity"`
	}
	quantityQuery := s.db().Table("commerce_order_items AS i").
		Joins("JOIN commerce_orders AS o ON o.id = i.order_id AND o.tenant_id = i.tenant_id").
		Where("i.tenant_id = ? AND o.business_type = ? AND o.payment_status IN ('paid','refunded')", q.TenantID, q.BusinessType)
	if q.LocationID != 0 {
		quantityQuery = quantityQuery.Where("o.location_id = ?", q.LocationID)
	}
	if q.StartAt != nil {
		quantityQuery = quantityQuery.Where("o.created_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		quantityQuery = quantityQuery.Where("o.created_at < ?", *q.EndAt)
	}
	if err := quantityQuery.Select("COALESCE(SUM(i.quantity), 0) AS quantity").Scan(&productQuantity).Error; err != nil {
		return nil, err
	}
	result.ProductQuantity = productQuantity.Quantity

	fulfillment := s.db().Table("commerce_orders AS o").Where("o.tenant_id = ? AND o.business_type = ? AND o.payment_status IN ('paid','refunded') AND o.fulfillment_status <> 'cancelled'", q.TenantID, q.BusinessType)
	if q.LocationID != 0 {
		fulfillment = fulfillment.Where("o.location_id = ?", q.LocationID)
	}
	if q.StartAt != nil {
		fulfillment = fulfillment.Where("o.created_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		fulfillment = fulfillment.Where("o.created_at < ?", *q.EndAt)
	}
	if q.BusinessType == "restaurant" {
		fulfillment = fulfillment.Joins("JOIN restaurant_fulfillments AS f ON f.order_id = o.id AND f.tenant_id = o.tenant_id").Select("f.method AS method, COUNT(*) AS count").Group("f.method")
	} else {
		fulfillment = fulfillment.Joins("JOIN retail_fulfillments AS f ON f.order_id = o.id AND f.tenant_id = o.tenant_id").Select("'shipping' AS method, COUNT(*) AS count")
	}
	if err := fulfillment.Scan(&result.FulfillmentMethods).Error; err != nil {
		return nil, err
	}
	if q.BusinessType == "retail" {
		// There is one shipment per retail order in the current model.
		if len(result.FulfillmentMethods) == 0 {
			result.FulfillmentMethods = []CommerceFulfillmentStat{{Method: "shipping", Count: 0}}
		}
	}

	// A campaign keeps business_type as a legacy primary value, while the
	// normalized scope table is authoritative for multi-business campaigns.
	// Match both representations so historical campaigns and newly scoped
	// campaigns contribute to the same business report without crossing tenant
	// or campaign/channel boundaries.
	assist := s.db().Model(&model.CommerceAssistSession{}).
		Where("tenant_id = ? AND status = 'succeeded' AND succeeded_at IS NOT NULL", q.TenantID).
		Where("campaign_id IN (?)", s.db().Model(&model.CommerceAssistCampaign{}).
			Select("id").
			Where("tenant_id = ? AND (business_type = ? OR EXISTS (SELECT 1 FROM commerce_assist_campaign_business_types scope WHERE scope.tenant_id = commerce_assist_campaigns.tenant_id AND scope.campaign_id = commerce_assist_campaigns.id AND scope.channel_account_id = commerce_assist_campaigns.channel_account_id AND scope.business_type = ?))", q.TenantID, q.BusinessType, q.BusinessType))
	if q.StartAt != nil {
		assist = assist.Where("succeeded_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		assist = assist.Where("succeeded_at < ?", *q.EndAt)
	}
	if err := assist.Count(&result.AssistSuccessCount).Error; err != nil {
		return nil, err
	}

	notifications := s.db().Model(&model.CommerceMerchantNotification{}).Where("tenant_id = ? AND business_type = ? AND event_type = 'order_paid'", q.TenantID, q.BusinessType)
	if q.LocationID != 0 {
		notifications = notifications.Where("location_id = ?", q.LocationID)
	}
	if q.StartAt != nil {
		notifications = notifications.Where("created_at >= ?", *q.StartAt)
	}
	if q.EndAt != nil {
		notifications = notifications.Where("created_at < ?", *q.EndAt)
	}
	if err := notifications.Count(&result.NewOrderNotificationCount).Error; err != nil {
		return nil, err
	}
	if err := notifications.Where("status = 'unread'").Count(&result.UnreadOrderNotificationCount).Error; err != nil {
		return nil, err
	}
	return result, nil
}
