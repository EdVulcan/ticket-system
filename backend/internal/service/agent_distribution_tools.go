package service

import (
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

const agentModuleDistribution = "distribution"

var agentDistributionToolSpecs = []agentToolSpec{
	{Name: "query_distribution_partners", Description: "查询当前分销租户的合作供应商与合作状态摘要，不返回账户余额、联系人、内部编号或密钥", ModuleID: agentModuleDistribution, ActionKind: "query", Permission: authz.PermissionDistributionRead, Capability: "distributor", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"status":{"type":"string","enum":["pending","active","rejected","suspended"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_distribution_products", Description: "查询当前分销租户自己的授权商品、销售期和允许渠道摘要，不返回供应商结算底价、库存、内部规则或数据库编号", ModuleID: agentModuleDistribution, ActionKind: "query", Permission: authz.PermissionDistributionRead, Capability: "distributor", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"supplier_name":{"type":"string","maxLength":100},"status":{"type":"string","enum":["online","offline","active","suspended","expired"]},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_distribution_fulfillments", Description: "查询当前分销租户销售订单对应的供应商履约摘要，不返回游客、票码、库存、内部结算金额或数据库编号", ModuleID: agentModuleDistribution, ActionKind: "query", Permission: authz.PermissionDistributionRead, Capability: "distributor", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"status":{"type":"string","enum":["reserved","paid","fulfilled","cancelled"]},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_distribution_settlements", Description: "查询当前分销租户参与的结算单摘要，只读返回对账期间、状态和汇总金额；不能确认、付款、调整或导出结算", ModuleID: agentModuleDistribution, ActionKind: "query", Permission: authz.PermissionSettlementsRead, Capability: "distributor", ReadOnly: true, Parameters: jsonRaw(`{"type":"object","properties":{"status":{"type":"string","enum":["draft","supplier_confirmed","confirmed","paid","disputed"]},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
}

var agentDistributionModuleManifest = agentModuleManifest{
	ID:             agentModuleDistribution,
	Label:          "分销协作",
	Summary:        "分销商自己的合作关系、授权商品、履约进度和结算单只读事实",
	KnowledgeFiles: []string{"skills/agent_distribution_read.md"},
	ToolNames: []string{
		"query_distribution_partners",
		"query_distribution_products",
		"query_distribution_fulfillments",
		"query_distribution_settlements",
	},
}

// The read-only module owns its registration so distribution capabilities can
// evolve without expanding the generic Agent runtime switch.
func init() {
	agentToolSpecs = append(agentToolSpecs, agentDistributionToolSpecs...)
	agentModuleManifests = append(agentModuleManifests, agentDistributionModuleManifest)
	agentToolHandlers["query_distribution_partners"] = executeAgentDistributionPartnersQuery
	agentToolHandlers["query_distribution_products"] = executeAgentDistributionProductsQuery
	agentToolHandlers["query_distribution_fulfillments"] = executeAgentDistributionFulfillmentsQuery
	agentToolHandlers["query_distribution_settlements"] = executeAgentDistributionSettlementsQuery
}

// jsonRaw keeps the compact tool schemas readable in the registration table.
func jsonRaw(value string) []byte { return []byte(value) }

type agentDistributionPartnersArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type agentDistributionPartnerRow struct {
	SupplierName       string `json:"supplier_name"`
	RelationshipStatus string `json:"relationship_status"`
	AgentLevel         string `json:"agent_level"`
	AppliedAt          string `json:"applied_at"`
}

func executeAgentDistributionPartnersQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentDistributionPartnersArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if !agentDistributionRelationshipStatus(args.Status) {
		return agentToolExecution{}, agentInvalid("合作状态不是受支持的值")
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("distributor_relationships AS relationship").
		Joins("JOIN tenants AS supplier ON supplier.id = relationship.supplier_tenant_id AND supplier.deleted_at IS NULL").
		Where("relationship.agent_tenant_id = ? AND relationship.deleted_at IS NULL AND relationship.status <> ?", request.TenantID, "none")
	if status := strings.TrimSpace(args.Status); status != "" {
		query = query.Where("relationship.status = ?", status)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var rawRows []struct {
		SupplierName       string
		RelationshipStatus string
		AgentLevel         string
		AppliedAt          time.Time
	}
	if err := query.Select("supplier.name AS supplier_name, relationship.status AS relationship_status, relationship.agent_level AS agent_level, COALESCE(relationship.distribution_applied_at, relationship.created_at) AS applied_at").
		Order("relationship.created_at DESC, relationship.id DESC").Limit(limit).Scan(&rawRows).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentDistributionPartnerRow, 0, len(rawRows))
	for _, row := range rawRows {
		rows = append(rows, agentDistributionPartnerRow{SupplierName: row.SupplierName, RelationshipStatus: row.RelationshipStatus, AgentLevel: row.AgentLevel, AppliedAt: row.AppliedAt.UTC().Format(time.RFC3339)})
	}
	return agentQueryExecution(agentModuleDistribution, "query_distribution_partners", map[string]interface{}{"status": args.Status}, rows, len(rows), total, limit)
}

func agentDistributionRelationshipStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "pending", "active", "rejected", "suspended":
		return true
	default:
		return false
	}
}

type agentDistributionProductsArgs struct {
	SupplierName string `json:"supplier_name"`
	Status       string `json:"status"`
	Limit        int    `json:"limit"`
}

type agentDistributionProductRow struct {
	ProductName        string   `json:"product_name"`
	SupplierName       string   `json:"supplier_name"`
	ProductType        string   `json:"product_type"`
	RelationshipStatus string   `json:"relationship_status"`
	ListingStatus      string   `json:"listing_status"`
	OfferStatus        string   `json:"offer_status"`
	RetailPrice        float64  `json:"retail_price"`
	SalesStartDate     string   `json:"sales_start_date,omitempty"`
	SalesEndDate       string   `json:"sales_end_date,omitempty"`
	AllowedChannels    []string `json:"allowed_channels"`
	CurrentlySellable  bool     `json:"currently_sellable"`
}

func executeAgentDistributionProductsQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentDistributionProductsArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if len([]byte(args.SupplierName)) > 100 || !agentDistributionProductStatus(args.Status) {
		return agentToolExecution{}, agentInvalid("授权商品查询条件不是受支持的值")
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("seller_listings AS listing").
		Joins("JOIN product_offers AS offer ON offer.id = listing.product_offer_id AND offer.deleted_at IS NULL").
		Joins("JOIN products AS source_product ON source_product.id = offer.source_product_id AND source_product.tenant_id = offer.supplier_tenant_id AND source_product.deleted_at IS NULL").
		Joins("JOIN tenants AS supplier ON supplier.id = offer.supplier_tenant_id AND supplier.deleted_at IS NULL").
		Joins("LEFT JOIN distributor_relationships AS relationship ON relationship.agent_tenant_id = offer.distributor_tenant_id AND relationship.supplier_tenant_id = offer.supplier_tenant_id AND relationship.deleted_at IS NULL").
		Where("listing.seller_tenant_id = ? AND listing.deleted_at IS NULL AND offer.distributor_tenant_id = ?", request.TenantID, request.TenantID)
	if supplierName := strings.TrimSpace(args.SupplierName); supplierName != "" {
		query = query.Where("supplier.name ILIKE ?", "%"+supplierName+"%")
	}
	if status := strings.TrimSpace(args.Status); status != "" {
		switch status {
		case "online", "offline":
			query = query.Where("listing.status = ?", status)
		default:
			query = query.Where("offer.status = ?", status)
		}
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var rawRows []struct {
		ProductName        string
		SupplierName       string
		ProductType        string
		RelationshipStatus string
		ListingStatus      string
		OfferStatus        string
		RetailPrice        float64
		SalesStartAt       *time.Time
		SalesEndAt         *time.Time
		AllowedChannels    string
	}
	if err := query.Select("listing.name AS product_name, supplier.name AS supplier_name, source_product.type AS product_type, COALESCE(relationship.status, 'none') AS relationship_status, listing.status AS listing_status, offer.status AS offer_status, listing.retail_price AS retail_price, offer.sales_start_at, offer.sales_end_at, offer.allowed_channels").
		Order("supplier.name ASC, listing.name ASC, listing.id ASC").Limit(limit).Scan(&rawRows).Error; err != nil {
		return agentToolExecution{}, err
	}
	now := time.Now()
	rows := make([]agentDistributionProductRow, 0, len(rawRows))
	for _, row := range rawRows {
		view := agentDistributionProductRow{
			ProductName: row.ProductName, SupplierName: row.SupplierName, ProductType: row.ProductType,
			RelationshipStatus: row.RelationshipStatus, ListingStatus: row.ListingStatus, OfferStatus: row.OfferStatus, RetailPrice: row.RetailPrice,
			AllowedChannels: agentDistributionChannels(row.AllowedChannels),
			CurrentlySellable: row.RelationshipStatus == "active" && row.ListingStatus == "online" && row.OfferStatus == "active" &&
				(row.SalesStartAt == nil || !now.Before(*row.SalesStartAt)) && (row.SalesEndAt == nil || !now.After(*row.SalesEndAt)),
		}
		if row.SalesStartAt != nil {
			view.SalesStartDate = row.SalesStartAt.In(time.Local).Format("2006-01-02")
		}
		if row.SalesEndAt != nil {
			view.SalesEndDate = row.SalesEndAt.In(time.Local).Format("2006-01-02")
		}
		rows = append(rows, view)
	}
	filters := map[string]interface{}{"supplier_name": args.SupplierName, "status": args.Status}
	return agentQueryExecution(agentModuleDistribution, "query_distribution_products", filters, rows, len(rows), total, limit)
}

func agentDistributionProductStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "online", "offline", "active", "suspended", "expired":
		return true
	default:
		return false
	}
}

func agentDistributionChannels(value string) []string {
	values := strings.Split(value, ",")
	channels := make([]string, 0, len(values))
	for _, channel := range values {
		if channel = strings.TrimSpace(channel); channel != "" {
			channels = append(channels, channel)
		}
	}
	return channels
}

type agentDistributionFulfillmentsArgs struct {
	Status    string `json:"status"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Limit     int    `json:"limit"`
}

type agentDistributionFulfillmentRow struct {
	SalesOrderNo    string `json:"sales_order_no"`
	FulfillmentNo   string `json:"fulfillment_no"`
	SupplierName    string `json:"supplier_name"`
	ScenicAreaName  string `json:"scenic_area_name"`
	Status          string `json:"status"`
	SettlementState string `json:"settlement_state"`
	TicketCount     int64  `json:"ticket_count"`
	UsedCount       int64  `json:"used_count"`
	CreatedAt       string `json:"created_at"`
}

func executeAgentDistributionFulfillmentsQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentDistributionFulfillmentsArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if !agentDistributionFulfillmentStatus(args.Status) {
		return agentToolExecution{}, agentInvalid("履约状态不是受支持的值")
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 366, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("fulfillment_orders AS fulfillment").
		Joins("JOIN tenants AS supplier ON supplier.id = fulfillment.supplier_tenant_id AND supplier.deleted_at IS NULL").
		Joins("LEFT JOIN scenic_areas AS area ON area.id = fulfillment.scenic_area_id AND area.tenant_id = fulfillment.supplier_tenant_id AND area.deleted_at IS NULL").
		Where("fulfillment.sales_tenant_id = ? AND fulfillment.deleted_at IS NULL AND fulfillment.environment = ? AND fulfillment.created_at >= ? AND fulfillment.created_at < ?", request.TenantID, "production", start, end)
	if status := strings.TrimSpace(args.Status); status != "" {
		query = query.Where("fulfillment.status = ?", status)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var rawRows []struct {
		SalesOrderNo    string
		FulfillmentNo   string
		SupplierName    string
		ScenicAreaName  string
		Status          string
		SettlementState string
		CreatedAt       time.Time
	}
	if err := query.Select("fulfillment.sales_order_no, fulfillment.fulfillment_no, supplier.name AS supplier_name, COALESCE(area.name, '') AS scenic_area_name, fulfillment.status, fulfillment.settlement_status AS settlement_state, fulfillment.created_at").
		Order("fulfillment.created_at DESC, fulfillment.id DESC").Limit(limit).Scan(&rawRows).Error; err != nil {
		return agentToolExecution{}, err
	}
	fulfillmentNos := make([]string, 0, len(rawRows))
	for _, row := range rawRows {
		fulfillmentNos = append(fulfillmentNos, row.FulfillmentNo)
	}
	counts := make(map[string]struct{ tickets, used int64 }, len(fulfillmentNos))
	if len(fulfillmentNos) > 0 {
		var countRows []struct {
			FulfillmentNo string
			Tickets       int64
			Used          int64
		}
		if err := model.DB.Table("fulfillment_orders AS fulfillment").
			Joins("LEFT JOIN ticket_entitlements AS entitlement ON entitlement.fulfillment_order_id = fulfillment.id AND entitlement.deleted_at IS NULL").
			Where("fulfillment.sales_tenant_id = ? AND fulfillment.fulfillment_no IN ? AND fulfillment.deleted_at IS NULL", request.TenantID, fulfillmentNos).
			Select("fulfillment.fulfillment_no, COUNT(entitlement.id) AS tickets, COALESCE(SUM(CASE WHEN entitlement.status = 'used' THEN 1 ELSE 0 END), 0) AS used").
			Group("fulfillment.fulfillment_no").Scan(&countRows).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, row := range countRows {
			counts[row.FulfillmentNo] = struct{ tickets, used int64 }{tickets: row.Tickets, used: row.Used}
		}
	}
	rows := make([]agentDistributionFulfillmentRow, 0, len(rawRows))
	for _, row := range rawRows {
		count := counts[row.FulfillmentNo]
		rows = append(rows, agentDistributionFulfillmentRow{SalesOrderNo: row.SalesOrderNo, FulfillmentNo: row.FulfillmentNo, SupplierName: row.SupplierName, ScenicAreaName: row.ScenicAreaName, Status: row.Status, SettlementState: row.SettlementState, TicketCount: count.tickets, UsedCount: count.used, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)})
	}
	filters := map[string]interface{}{"status": args.Status, "start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02")}
	return agentQueryExecution(agentModuleDistribution, "query_distribution_fulfillments", filters, rows, len(rows), total, limit)
}

func agentDistributionFulfillmentStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "reserved", "paid", "fulfilled", "cancelled":
		return true
	default:
		return false
	}
}

type agentDistributionSettlementsArgs struct {
	Status    string `json:"status"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Limit     int    `json:"limit"`
}

type agentDistributionSettlementRow struct {
	StatementNo     string `json:"statement_no"`
	SupplierName    string `json:"supplier_name"`
	Status          string `json:"status"`
	PeriodStart     string `json:"period_start"`
	PeriodEnd       string `json:"period_end"`
	GrossCents      int64  `json:"gross_cents"`
	RefundCents     int64  `json:"refund_cents"`
	CommissionCents int64  `json:"commission_cents"`
	NetCents        int64  `json:"net_cents"`
	AdjustmentCents int64  `json:"adjustment_cents"`
	DueAt           string `json:"due_at,omitempty"`
}

func executeAgentDistributionSettlementsQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentDistributionSettlementsArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if !agentDistributionSettlementStatus(args.Status) {
		return agentToolExecution{}, agentInvalid("结算状态不是受支持的值")
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 366, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("settlement_statements AS statement").
		Joins("JOIN tenants AS supplier ON supplier.id = statement.supplier_tenant_id AND supplier.deleted_at IS NULL").
		Where("statement.distributor_tenant_id = ? AND statement.deleted_at IS NULL AND statement.period_start >= ? AND statement.period_start < ?", request.TenantID, start, end)
	if status := strings.TrimSpace(args.Status); status != "" {
		query = query.Where("statement.status = ?", status)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var rawRows []struct {
		StatementNo     string
		SupplierName    string
		Status          string
		PeriodStart     time.Time
		PeriodEnd       time.Time
		GrossCents      int64
		RefundCents     int64
		CommissionCents int64
		NetCents        int64
		AdjustmentCents int64
		DueAt           *time.Time
	}
	if err := query.Select("statement.statement_no, supplier.name AS supplier_name, statement.status, statement.period_start, statement.period_end, statement.gross_cents, statement.refund_cents, statement.commission_cents, statement.net_cents, statement.adjustment_cents, statement.due_at").
		Order("statement.period_start DESC, statement.id DESC").Limit(limit).Scan(&rawRows).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentDistributionSettlementRow, 0, len(rawRows))
	for _, row := range rawRows {
		view := agentDistributionSettlementRow{StatementNo: row.StatementNo, SupplierName: row.SupplierName, Status: row.Status, PeriodStart: row.PeriodStart.In(time.Local).Format("2006-01-02"), PeriodEnd: row.PeriodEnd.In(time.Local).Format("2006-01-02"), GrossCents: row.GrossCents, RefundCents: row.RefundCents, CommissionCents: row.CommissionCents, NetCents: row.NetCents, AdjustmentCents: row.AdjustmentCents}
		if row.DueAt != nil {
			view.DueAt = row.DueAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, view)
	}
	filters := map[string]interface{}{"status": args.Status, "start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02")}
	return agentQueryExecution(agentModuleDistribution, "query_distribution_settlements", filters, rows, len(rows), total, limit)
}

func agentDistributionSettlementStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "draft", "supplier_confirmed", "confirmed", "paid", "disputed":
		return true
	default:
		return false
	}
}
