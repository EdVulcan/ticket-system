package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func decodeAgentQueryResult(t *testing.T, execution agentToolExecution) agentQueryResult {
	t.Helper()
	var result agentQueryResult
	if err := json.Unmarshal([]byte(execution.ResultJSON), &result); err != nil {
		t.Fatalf("decode query result: %v; json=%s", err, execution.ResultJSON)
	}
	if result.SchemaVersion != agentQuerySchemaVersion || result.AsOf == "" {
		t.Fatalf("incomplete query envelope: %+v", result)
	}
	return result
}

func TestAgentOrderQueryIsTenantScopedAndReturnsProjection(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	other := model.Tenant{Name: "Agent Other Order Tenant", SystemCode: fmt.Sprintf("AGENT-ORDER-OTHER-%d", time.Now().UnixNano()), SecretKey: "agent-order-other", Status: "active"}
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: other.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().AddDate(0, 0, -1)
	orders := []model.Order{
		{OrderNo: "AGENT-ORDER-CURRENT", TenantID: fixture.tenant.ID, TotalAmount: 120, Status: "paid", Channel: "online", Environment: "production", ContactName: "Current Tenant Visitor"},
		{OrderNo: "AGENT-ORDER-FOREIGN", TenantID: other.ID, TotalAmount: 999, Status: "paid", Channel: "online", Environment: "production", ContactName: "Foreign Visitor"},
	}
	for i := range orders {
		if err := model.DB.Create(&orders[i]).Error; err != nil {
			t.Fatal(err)
		}
		if orders[i].TenantID == fixture.tenant.ID {
			if err := model.DB.Create(&model.OrderItem{
				OrderID: orders[i].ID, ProductID: fixture.product.ID, ProductName: "Adult Ticket", Price: orders[i].TotalAmount, Quantity: 1, UseDate: &start,
				FulfillmentProductID: fixture.product.ID, FulfillmentTenantID: fixture.tenant.ID, FulfillmentScenicAreaID: fixture.area.ID, ProductRevisionID: fixture.product.CurrentRevisionID,
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	queryStart := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	queryEnd := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	execution, err := executeAgentOrderQuery(nil, agentToolRequest{TenantID: fixture.tenant.ID, RawArgs: fmt.Sprintf(`{"search":"AGENT-ORDER","start_date":"%s","end_date":"%s","limit":20}`, queryStart, queryEnd)})
	if err != nil {
		t.Fatalf("order query: %v", err)
	}
	result := decodeAgentQueryResult(t, execution)
	if result.Module != agentModuleOrders || result.Tool != "search_orders" || result.Returned != 1 || result.Total != 1 {
		t.Fatalf("unexpected order query envelope: %+v", result)
	}
	if strings.Contains(execution.ResultJSON, "FOREIGN") || strings.Contains(execution.ResultJSON, "Current Tenant Visitor") || strings.Contains(execution.ResultJSON, `"id"`) {
		t.Fatalf("order projection leaked tenant or internal fields: %s", execution.ResultJSON)
	}
	var rows []agentOrderQueryRow
	encoded, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(encoded, &rows); err != nil || len(rows) != 1 || rows[0].OrderNo != "AGENT-ORDER-CURRENT" || len(rows[0].Items) != 1 {
		t.Fatalf("unexpected order rows: %s err=%v", encoded, err)
	}
}

func TestAgentTicketInventoryQueryReturnsServerOwnedFacts(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	date := time.Now().AddDate(0, 0, 2).Truncate(24 * time.Hour)
	if err := model.DB.Create(&model.ProductInventory{TenantID: fixture.tenant.ID, ProductID: fixture.product.ID, ScenicAreaID: fixture.area.ID, StockDate: date, StockSlot: "morning", Capacity: 100, Sold: 23}).Error; err != nil {
		t.Fatal(err)
	}
	execution, err := executeAgentTicketInventoryQuery(nil, agentToolRequest{TenantID: fixture.tenant.ID, RawArgs: fmt.Sprintf(`{"product_name":"Adult Ticket","start_date":"%s","end_date":"%s","limit":20}`, date.Format("2006-01-02"), date.Format("2006-01-02"))})
	if err != nil {
		t.Fatalf("inventory query: %v", err)
	}
	result := decodeAgentQueryResult(t, execution)
	if result.Module != agentModuleInventory || result.Tool != "query_ticket_inventory" || result.Returned != 1 || result.Total != 1 {
		t.Fatalf("unexpected inventory query envelope: %+v", result)
	}
	var rows []agentTicketInventoryRow
	encoded, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(encoded, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("unexpected inventory rows: %s err=%v", encoded, err)
	}
	if rows[0].Capacity != 100 || rows[0].Sold != 23 || rows[0].Remaining != 77 || rows[0].StockSlot != "morning" || rows[0].ScenicArea != fixture.area.Name {
		t.Fatalf("inventory projection is incorrect: %+v", rows[0])
	}
}

func TestAgentReportQueriesUseReadOnlyModuleAdapters(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	for _, specName := range []string{"query_sales_summary", "query_verification_summary"} {
		handler, ok := agentToolHandlerFor(specName)
		if !ok {
			t.Fatalf("missing handler for %s", specName)
		}
		execution, err := handler(nil, agentToolRequest{TenantID: fixture.tenant.ID, RawArgs: `{"start_date":"2000-01-01","end_date":"2000-01-02"}`})
		if err != nil {
			t.Fatalf("%s query: %v", specName, err)
		}
		result := decodeAgentQueryResult(t, execution)
		if result.Module != agentModuleReports || result.Tool != specName || result.Returned != 0 || result.Total != 0 {
			t.Fatalf("unexpected empty report result for %s: %+v", specName, result)
		}
		if !strings.Contains(execution.ResultJSON, "period_rule") {
			t.Fatalf("report query did not state its period rule: %s", execution.ResultJSON)
		}
	}
}

func TestAgentQueryDateRangeRejectsUnsafeWindows(t *testing.T) {
	if _, _, err := agentQueryDateRange("2020-01-01", "2021-01-01", 366, 30); err == nil {
		t.Fatal("oversized query date range was accepted")
	}
	if _, _, err := agentQueryDateRange("bad", "2020-01-01", 366, 30); err == nil {
		t.Fatal("invalid query date was accepted")
	}
}

func TestAgentOrderQuerySupportsDistributorReadScopeWithoutScenicAccess(t *testing.T) {
	resetBusinessData(t)
	distributor := model.Tenant{Name: "Agent Distributor Tenant", SystemCode: fmt.Sprintf("AGENT-DISTRIBUTOR-%d", time.Now().UnixNano()), SecretKey: "agent-distributor", Status: "active"}
	if err := model.DB.Create(&distributor).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: distributor.ID, Capability: "distributor", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	orderSpec, ok := findAgentTool("search_orders")
	if !ok || !agentToolAllowed(distributor.ID, "viewer", orderSpec) {
		t.Fatalf("distributor order query was not visible without scenic supplier capability: %+v", orderSpec)
	}
	inventorySpec, ok := findAgentTool("query_ticket_inventory")
	if !ok || agentToolAllowed(distributor.ID, "viewer", inventorySpec) {
		t.Fatal("distributor was incorrectly allowed to query supplier inventory")
	}
}
