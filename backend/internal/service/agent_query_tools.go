package service

import (
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// agentToolRequest is the narrow seam between the generic Agent runtime and a
// read-only module adapter. The runtime owns authorization, idempotency and
// audit; the adapter owns typed arguments and server-side projections.
type agentToolRequest struct {
	TenantID  uint
	ActorID   uint
	ActorRole string
	Task      model.AgentTask
	Input     string
	Config    model.PlatformAIConfig
	RawArgs   string
}

type agentToolHandler func(*AgentTaskService, agentToolRequest) (agentToolExecution, error)

var agentToolHandlers = map[string]agentToolHandler{
	"search_scenic_areas":        executeAgentScenicAreaQuery,
	"search_checkpoints":         executeAgentCheckpointQuery,
	"search_ticket_products":     executeAgentProductQuery,
	"get_ticket_product_rules":   executeAgentProductRuleQuery,
	"search_orders":              executeAgentOrderQuery,
	"query_ticket_inventory":     executeAgentTicketInventoryQuery,
	"query_sales_summary":        executeAgentSalesSummaryQuery,
	"query_verification_summary": executeAgentVerificationSummaryQuery,
}

func agentToolHandlerFor(name string) (agentToolHandler, bool) {
	handler, ok := agentToolHandlers[strings.TrimSpace(name)]
	return handler, ok
}

const agentQuerySchemaVersion = "1"

// agentQueryResult is the stable, server-owned envelope for read-only facts.
// The provider may summarize it, but the task audit retains this exact result
// and no database identifier is included in any public row.
type agentQueryResult struct {
	SchemaVersion string      `json:"schema_version"`
	Module        string      `json:"module"`
	Tool          string      `json:"tool"`
	Filters       interface{} `json:"filters,omitempty"`
	AsOf          string      `json:"as_of"`
	Data          interface{} `json:"data"`
	Returned      int         `json:"returned"`
	Total         int64       `json:"total"`
	HasMore       bool        `json:"has_more"`
}

func agentQueryExecution(module, tool string, filters interface{}, data interface{}, returned int, total int64, limit int) (agentToolExecution, error) {
	if returned < 0 {
		returned = 0
	}
	if total < int64(returned) {
		total = int64(returned)
	}
	result := agentQueryResult{
		SchemaVersion: agentQuerySchemaVersion,
		Module:        module,
		Tool:          tool,
		Filters:       filters,
		AsOf:          time.Now().UTC().Format(time.RFC3339),
		Data:          data,
		Returned:      returned,
		Total:         total,
		HasMore:       limit > 0 && total > int64(returned),
	}
	return agentToolJSON(result)
}

type agentSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type agentProductRuleArgs struct {
	ProductName string `json:"product_name"`
}

func executeAgentScenicAreaQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentSearchArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentToolLimit(args.Limit)
	query := model.DB.Model(&model.ScenicArea{}).Where("tenant_id = ? AND status = ?", request.TenantID, "active").Order("id ASC")
	if value := strings.TrimSpace(args.Query); value != "" {
		query = query.Where("name ILIKE ? OR code ILIKE ?", "%"+value+"%", "%"+value+"%")
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var areas []model.ScenicArea
	if err := query.Limit(limit).Find(&areas).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]map[string]interface{}, 0, len(areas))
	for _, area := range areas {
		rows = append(rows, map[string]interface{}{"name": area.Name, "code": area.Code, "status": area.Status})
	}
	return agentQueryExecution("catalog", "search_scenic_areas", map[string]interface{}{"query": args.Query}, rows, len(rows), total, limit)
}

func executeAgentCheckpointQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentSearchArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentToolLimit(args.Limit)
	query := model.DB.Model(&model.CheckPoint{}).Where("tenant_id = ?", request.TenantID).Order("id ASC")
	if value := strings.TrimSpace(args.Query); value != "" {
		query = query.Where("name ILIKE ? OR location ILIKE ?", "%"+value+"%", "%"+value+"%")
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var checkpoints []model.CheckPoint
	if err := query.Limit(limit).Find(&checkpoints).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]map[string]interface{}, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		rows = append(rows, map[string]interface{}{"name": checkpoint.Name, "location": checkpoint.Location})
	}
	return agentQueryExecution("catalog", "search_checkpoints", map[string]interface{}{"query": args.Query}, rows, len(rows), total, limit)
}

func executeAgentProductQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentSearchArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentToolLimit(args.Limit)
	query := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND source_product_id = 0 AND source_tenant_id = 0 AND (fulfillment_tenant_id = 0 OR fulfillment_tenant_id = tenant_id)", request.TenantID).Order("id ASC")
	if value := strings.TrimSpace(args.Query); value != "" {
		query = query.Where("name ILIKE ?", "%"+value+"%")
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var products []model.Product
	if err := query.Limit(limit).Find(&products).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]map[string]interface{}, 0, len(products))
	for _, product := range products {
		if isDistributedListing(&product) {
			continue
		}
		rows = append(rows, map[string]interface{}{"name": product.Name, "type": product.Type, "price": product.Price, "status": product.Status, "is_distributable": product.IsDistributable})
	}
	return agentQueryExecution("catalog", "search_ticket_products", map[string]interface{}{"query": args.Query}, rows, len(rows), total, limit)
}

func executeAgentProductRuleQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentProductRuleArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	name := strings.TrimSpace(args.ProductName)
	if name == "" {
		return agentToolExecution{}, agentInvalid("product_name is required")
	}
	canonicalName, err := resolveAgentAlias(model.DB, request.TenantID, agentAliasProduct, name)
	if err != nil {
		return agentToolExecution{}, err
	}
	name = canonicalName
	var products []model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("tenant_id = ? AND name = ?", request.TenantID, name).Find(&products).Error; err != nil {
		return agentToolExecution{}, err
	}
	if len(products) == 0 {
		return agentToolExecution{}, agentInvalid("当前租户不存在该票种")
	}
	if len(products) > 1 {
		return agentToolExecution{}, agentInvalid("该票种名称不唯一，请先缩小范围")
	}
	product := products[0]
	groups := make([]map[string]interface{}, 0, len(product.Rule.Groups))
	for _, group := range product.Rule.Groups {
		items := make([]map[string]interface{}, 0, len(group.Items))
		for _, item := range group.Items {
			items = append(items, map[string]interface{}{"checkpoint_name": item.CheckPoint.Name, "max_per_check_in": item.MaxPerCheckIn})
		}
		groups = append(groups, map[string]interface{}{"group_name": group.GroupName, "max_total_check_in": group.MaxTotalCheckIn, "items": items})
	}
	data := map[string]interface{}{"product_name": product.Name, "rule_name": product.Rule.Name, "groups": groups}
	return agentQueryExecution("catalog", "get_ticket_product_rules", map[string]interface{}{"product_name": name}, data, 1, 1, 1)
}

type agentOrderQueryArgs struct {
	Search    string `json:"search"`
	Status    string `json:"status"`
	Channel   string `json:"channel"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Limit     int    `json:"limit"`
}

type agentOrderQueryItem struct {
	ProductName string  `json:"product_name"`
	Quantity    int     `json:"quantity"`
	Price       float64 `json:"price"`
	UseDate     string  `json:"use_date,omitempty"`
}

type agentOrderQueryRow struct {
	OrderNo     string                `json:"order_no"`
	Status      string                `json:"status"`
	Channel     string                `json:"channel"`
	Environment string                `json:"environment"`
	TotalAmount float64               `json:"total_amount"`
	CreatedAt   string                `json:"created_at"`
	Items       []agentOrderQueryItem `json:"items"`
}

func executeAgentOrderQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentOrderQueryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 366, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	if len([]byte(args.Search)) > 100 {
		return agentToolExecution{}, agentInvalid("订单查询条件过长")
	}
	if args.Status != "" {
		switch args.Status {
		case "unpaid", "paid", "cancelled", "refunded", "partial_refunded", "completed":
		default:
			return agentToolExecution{}, agentInvalid("订单状态不是受支持的值")
		}
	}
	if len([]byte(args.Channel)) > 50 {
		return agentToolExecution{}, agentInvalid("订单渠道条件过长")
	}
	channelFilter, err := normalizeAgentOrderChannel(args.Channel)
	if err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	apply := func(query *gorm.DB) *gorm.DB {
		query = query.Where("orders.tenant_id = ? AND orders.deleted_at IS NULL AND orders.created_at >= ? AND orders.created_at < ?", request.TenantID, start, end)
		if args.Status != "" {
			query = query.Where("orders.status = ?", args.Status)
		}
		if channelFilter != "" {
			if channelFilter == "ctrip" {
				query = query.Where("orders.channel LIKE ?", "ctrip:%")
			} else {
				query = query.Where("orders.channel = ?", channelFilter)
			}
		}
		if value := strings.TrimSpace(args.Search); value != "" {
			like := "%" + value + "%"
			query = query.Where("orders.order_no ILIKE ? OR orders.external_no ILIKE ? OR orders.contact_name ILIKE ?", like, like, like)
		}
		return query
	}
	countQuery := apply(model.DB.Table("orders")).Select("COUNT(*)")
	var total int64
	if err := countQuery.Scan(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var orders []model.Order
	if err := apply(model.DB.Model(&model.Order{})).Select("orders.id, orders.order_no, orders.status, orders.total_amount, orders.channel, orders.environment, orders.created_at").Preload("Items", func(query *gorm.DB) *gorm.DB {
		return query.Select("id, order_id, product_name, price, quantity, use_date").Order("id ASC")
	}).Order("orders.created_at DESC, orders.id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentOrderQueryRow, 0, len(orders))
	for _, order := range orders {
		items := make([]agentOrderQueryItem, 0, len(order.Items))
		for _, item := range order.Items {
			row := agentOrderQueryItem{ProductName: item.ProductName, Quantity: item.Quantity, Price: item.Price}
			if item.UseDate != nil {
				row.UseDate = item.UseDate.Format("2006-01-02")
			}
			items = append(items, row)
		}
		rows = append(rows, agentOrderQueryRow{
			OrderNo: order.OrderNo, Status: order.Status, Channel: agentDisplayOrderChannel(order.Channel), Environment: order.Environment,
			TotalAmount: order.TotalAmount, CreatedAt: order.CreatedAt.UTC().Format(time.RFC3339), Items: items,
		})
	}
	filters := map[string]interface{}{"search": args.Search, "status": args.Status, "channel": args.Channel, "start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02")}
	return agentQueryExecution("orders", "search_orders", filters, rows, len(rows), total, limit)
}

func normalizeAgentOrderChannel(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, ":") {
		return "", agentInvalid("订单渠道只能使用业务名称，不能填写内部账号编号")
	}
	switch value {
	case "online", "线上":
		return "online", nil
	case "offline", "window", "窗口", "pos":
		return "window", nil
	case "ota":
		return "ota", nil
	case "ctrip", "携程":
		return "ctrip", nil
	case "xiaohongshu", "小红书":
		return "xiaohongshu", nil
	default:
		return "", agentInvalid("订单渠道不是受支持的业务名称")
	}
}

func agentDisplayOrderChannel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.HasPrefix(value, "ctrip:") {
		return "ctrip"
	}
	switch value {
	case "online":
		return "online"
	case "window":
		return "window"
	case "xiaohongshu":
		return "xiaohongshu"
	default:
		return value
	}
}

type agentTicketInventoryArgs struct {
	ProductName string `json:"product_name"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	StockSlot   string `json:"stock_slot"`
	Limit       int    `json:"limit"`
}

type agentTicketInventoryRow struct {
	ProductName   string `json:"product_name"`
	ProductStatus string `json:"product_status"`
	ScenicArea    string `json:"scenic_area"`
	StockDate     string `json:"stock_date"`
	StockSlot     string `json:"stock_slot,omitempty"`
	Capacity      int    `json:"capacity"`
	Sold          int    `json:"sold"`
	Remaining     int    `json:"remaining"`
	OverCapacity  bool   `json:"over_capacity"`
}

func executeAgentTicketInventoryQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentTicketInventoryArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if len([]byte(args.ProductName)) > 100 || len([]byte(args.StockSlot)) > 50 {
		return agentToolExecution{}, agentInvalid("库存查询条件过长")
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 93, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	if value := strings.TrimSpace(args.ProductName); value != "" {
		canonical, aliasErr := resolveAgentAlias(model.DB, request.TenantID, agentAliasProduct, value)
		if aliasErr != nil {
			return agentToolExecution{}, aliasErr
		}
		args.ProductName = canonical
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Table("product_inventories AS inventory").
		Joins("JOIN products AS product ON product.id = inventory.product_id AND product.tenant_id = inventory.tenant_id AND product.deleted_at IS NULL").
		Joins("JOIN scenic_areas AS area ON area.id = inventory.scenic_area_id AND area.tenant_id = inventory.tenant_id AND area.deleted_at IS NULL").
		Where("inventory.tenant_id = ? AND inventory.deleted_at IS NULL AND inventory.stock_date >= ? AND inventory.stock_date < ?", request.TenantID, start, end)
	if value := strings.TrimSpace(args.ProductName); value != "" {
		query = query.Where("product.name ILIKE ?", "%"+value+"%")
	}
	if value := strings.TrimSpace(args.StockSlot); value != "" {
		query = query.Where("inventory.stock_slot = ?", value)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var rawRows []struct {
		ProductName   string
		ProductStatus string
		ScenicArea    string
		StockDate     time.Time
		StockSlot     string
		Capacity      int
		Sold          int
	}
	if err := query.Select("product.name AS product_name, product.status AS product_status, area.name AS scenic_area, inventory.stock_date, inventory.stock_slot, inventory.capacity, inventory.sold").
		Order("inventory.stock_date ASC, product.name ASC, inventory.stock_slot ASC").Limit(limit).Scan(&rawRows).Error; err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentTicketInventoryRow, 0, len(rawRows))
	for _, raw := range rawRows {
		remaining := raw.Capacity - raw.Sold
		rows = append(rows, agentTicketInventoryRow{
			ProductName: raw.ProductName, ProductStatus: raw.ProductStatus, ScenicArea: raw.ScenicArea,
			StockDate: raw.StockDate.Format("2006-01-02"), StockSlot: raw.StockSlot, Capacity: raw.Capacity, Sold: raw.Sold,
			Remaining: agentMaxInt(remaining, 0), OverCapacity: remaining < 0,
		})
	}
	filters := map[string]interface{}{"product_name": args.ProductName, "stock_slot": args.StockSlot, "start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02")}
	return agentQueryExecution("inventory", "query_ticket_inventory", filters, rows, len(rows), total, limit)
}

type agentReportDateArgs struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type agentVerificationSummaryRow struct {
	Date           string `json:"date"`
	ScenicAreaName string `json:"scenic_area_name"`
	ProductName    string `json:"product_name"`
	SellerName     string `json:"seller_name"`
	Channel        string `json:"channel"`
	VerifiedCount  int64  `json:"verified_count"`
	IncomeCents    int64  `json:"income_cents"`
}

func executeAgentSalesSummaryQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentReportDateArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 366, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	stats, err := (&ReportService{}).GetSalesStats(request.TenantID, start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		return agentToolExecution{}, err
	}
	filters := map[string]interface{}{"start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02"), "period_rule": "refunds restated to original payment date"}
	return agentQueryExecution("reports", "query_sales_summary", filters, stats, len(stats), int64(len(stats)), len(stats))
}

func executeAgentVerificationSummaryQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentReportDateArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	start, end, err := agentQueryDateRange(args.StartDate, args.EndDate, 366, 30)
	if err != nil {
		return agentToolExecution{}, err
	}
	stats, err := (&ReportService{}).GetVerificationSummary(request.TenantID, FormalReportFilter{
		StartDate: start.Format("2006-01-02"), EndDate: end.AddDate(0, 0, -1).Format("2006-01-02"),
	})
	if err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentVerificationSummaryRow, 0, len(stats))
	for _, stat := range stats {
		rows = append(rows, agentVerificationSummaryRow{
			Date: stat.Date, ScenicAreaName: stat.ScenicAreaName, ProductName: stat.ProductName,
			SellerName: stat.SellerName, Channel: stat.Channel, VerifiedCount: stat.VerifiedCount, IncomeCents: stat.IncomeCents,
		})
	}
	filters := map[string]interface{}{"start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02"), "period_rule": "first effective verification date; refunded verification is excluded"}
	return agentQueryExecution("reports", "query_verification_summary", filters, rows, len(rows), int64(len(rows)), len(rows))
}

func agentQueryDateRange(startDate, endDate string, maxDays, defaultDays int) (time.Time, time.Time, error) {
	location := time.Local
	today := time.Now().In(location)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -defaultDays+1)
	end := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	var err error
	if strings.TrimSpace(startDate) != "" {
		start, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(startDate), location)
		if err != nil {
			return time.Time{}, time.Time{}, agentInvalid("开始日期必须使用 YYYY-MM-DD")
		}
	}
	if strings.TrimSpace(endDate) != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02", strings.TrimSpace(endDate), location)
		if parseErr != nil {
			return time.Time{}, time.Time{}, agentInvalid("结束日期必须使用 YYYY-MM-DD")
		}
		end = parsed.AddDate(0, 0, 1)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, agentInvalid("日期范围必须至少包含一天")
	}
	if end.Sub(start) > time.Duration(maxDays)*24*time.Hour {
		return time.Time{}, time.Time{}, agentInvalid(fmt.Sprintf("查询日期范围不能超过 %d 天", maxDays))
	}
	return start, end, nil
}

func agentQueryLimit(value int) int {
	if value <= 0 || value > 50 {
		return 20
	}
	return value
}

func agentMaxInt(value, fallback int) int {
	if value > fallback {
		return value
	}
	return fallback
}
