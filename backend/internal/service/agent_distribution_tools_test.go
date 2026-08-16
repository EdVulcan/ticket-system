package service

import (
	"encoding/json"
	"strings"
	"testing"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"
)

func TestAgentDistributionReadToolsProjectOnlyCurrentDistributorFacts(t *testing.T) {
	resetBusinessData(t)
	first := seedDistributionScenario(t)
	second := seedDistributionScenario(t)
	if err := model.DB.Model(&model.Tenant{}).Where("id = ?", second.supplierID).Update("name", "Foreign Supplier").Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{
		TenantID:     first.distributorID,
		Channel:      "online",
		ContactName:  "Private Visitor",
		ContactPhone: "13800000000",
		VisitorID:    "PRIVATE-ID-001",
		Items:        []model.OrderItem{{ProductID: first.listingID, Quantity: 1, VisitorName: "Private Visitor", VisitorPhone: "13800000000", VisitorID: "PRIVATE-ID-001"}},
	}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, first.distributorID); err != nil {
		t.Fatal(err)
	}
	foreignOrder := model.Order{TenantID: second.distributorID, Channel: "online", Items: []model.OrderItem{{ProductID: second.listingID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&foreignOrder); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(foreignOrder.OrderNo, second.distributorID); err != nil {
		t.Fatal(err)
	}
	periodStart := time.Now().AddDate(0, 0, -2).Truncate(24 * time.Hour)
	periodEnd := periodStart.AddDate(0, 0, 1)
	statement := model.SettlementStatement{
		SupplierTenantID: first.supplierID, DistributorTenantID: first.distributorID,
		StatementNo: "DIST-AGENT-STMT-1", IdempotencyKey: "agent-distribution-test",
		PeriodStart: periodStart, PeriodEnd: periodEnd, GrossCents: 8000, RefundCents: 0,
		CommissionCents: 2000, NetCents: 6000, Status: "draft",
	}
	if err := model.DB.Create(&statement).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SettlementStatement{
		SupplierTenantID: second.supplierID, DistributorTenantID: second.distributorID,
		StatementNo: "FOREIGN-DIST-AGENT-STMT", IdempotencyKey: "foreign-agent-distribution-test",
		PeriodStart: periodStart, PeriodEnd: periodEnd, GrossCents: 9900, NetCents: 9900, Status: "draft",
	}).Error; err != nil {
		t.Fatal(err)
	}

	request := func(rawArgs string) agentToolRequest {
		return agentToolRequest{TenantID: first.distributorID, ActorRole: authz.RoleViewer, RawArgs: rawArgs}
	}
	cases := []struct {
		name    string
		handler agentToolHandler
		args    string
		want    string
	}{
		{name: "partners", handler: executeAgentDistributionPartnersQuery, args: `{}`, want: "Supplier A"},
		{name: "products", handler: executeAgentDistributionProductsQuery, args: `{}`, want: "Distributed Ticket"},
		{name: "fulfillments", handler: executeAgentDistributionFulfillmentsQuery, args: `{}`, want: order.OrderNo},
		{name: "settlements", handler: executeAgentDistributionSettlementsQuery, args: `{"start_date":"` + periodStart.Format("2006-01-02") + `","end_date":"` + periodStart.Format("2006-01-02") + `"}`, want: statement.StatementNo},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			execution, err := testCase.handler(&AgentTaskService{}, request(testCase.args))
			if err != nil {
				t.Fatal(err)
			}
			var result agentQueryResult
			if err := json.Unmarshal([]byte(execution.ResultJSON), &result); err != nil {
				t.Fatal(err)
			}
			if result.Module != agentModuleDistribution || result.Returned == 0 || !strings.Contains(execution.ResultJSON, testCase.want) {
				t.Fatalf("unexpected query result: %s", execution.ResultJSON)
			}
			if strings.Contains(execution.ResultJSON, "Supplier Ticket") && testCase.name == "products" {
				t.Fatalf("source product name leaked instead of distributor listing name: %s", execution.ResultJSON)
			}
			for _, forbidden := range []string{"PRIVATE-ID-001", "13800000000", "Private Visitor", "settlement_price", "supplier_tenant_id", "product_offer_id", `\"id\"`} {
				if strings.Contains(execution.ResultJSON, forbidden) {
					t.Fatalf("forbidden value %q leaked: %s", forbidden, execution.ResultJSON)
				}
			}
			if strings.Contains(execution.ResultJSON, "Foreign Supplier") || strings.Contains(execution.ResultJSON, foreignOrder.OrderNo) || strings.Contains(execution.ResultJSON, "FOREIGN-DIST-AGENT-STMT") {
				t.Fatalf("foreign supplier facts leaked: %s", execution.ResultJSON)
			}
		})
	}
}

func TestAgentDistributionReadToolsRejectOtherRolesAndIDs(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := validateAgentCapabilityRegistry(); err != nil {
		t.Fatalf("distribution module did not register safely: %v", err)
	}
	pack, err := agentKnowledgePackForModule(agentModuleDistribution)
	if err != nil || pack.ID != agentModuleDistribution || pack.Content == "" {
		t.Fatalf("distribution knowledge pack is unavailable: %+v err=%v", pack, err)
	}
	for _, spec := range agentDistributionToolSpecs {
		if agentToolAllowed(scenario.supplierID, authz.RoleViewer, spec) {
			t.Fatalf("supplier unexpectedly received distributor tool %q", spec.Name)
		}
		if agentToolAllowed(scenario.distributorID, "seller", spec) {
			t.Fatalf("seller unexpectedly received distributor tool %q", spec.Name)
		}
		if !agentToolAllowed(scenario.distributorID, authz.RoleViewer, spec) {
			t.Fatalf("viewer should receive distributor tool %q", spec.Name)
		}
	}
	_, err = executeAgentDistributionPartnersQuery(&AgentTaskService{}, agentToolRequest{TenantID: scenario.distributorID, ActorRole: authz.RoleViewer, RawArgs: `{"supplier_tenant_id":1}`})
	if err == nil {
		t.Fatal("database identifier argument was accepted")
	}
}
