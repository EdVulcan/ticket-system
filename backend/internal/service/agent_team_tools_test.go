package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func decodeAgentTeamRows[T any](t *testing.T, execution agentToolExecution) []T {
	t.Helper()
	result := decodeAgentQueryResult(t, execution)
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("encode team result data: %v", err)
	}
	var rows []T
	if err := json.Unmarshal(encoded, &rows); err != nil {
		t.Fatalf("decode team result rows: %v; data=%s", err, encoded)
	}
	return rows
}

func TestAgentTeamQueriesScopeRelationshipsAndExcludeSensitiveFields(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	group := createTeamP0Group(t, fixture, "Agent Team Group", 1)
	now := time.Now()
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Model(&model.TourGroup{}).Where("id = ?", group.ID).Updates(map[string]interface{}{"status": "confirmed", "settlement_status": "statement"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ?", group.ID).Updates(map[string]interface{}{
			"phone": "13900000000", "identity_no": "ID-SECRET-001", "ticket_code": "TICKET-SECRET-001",
		}).Error; err != nil {
			return err
		}
		batch := model.TourEntryBatch{
			GroupID: group.ID, SupplierTenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID,
			BatchNo: fmt.Sprintf("AGENT-BATCH-%d", time.Now().UnixNano()), IdempotencyKey: "agent-team-private-batch",
			MemberIDsJSON: "[1]", DeviceID: fixture.device.ID, OperatorID: fixture.operator.ID, EnteredCount: 1, EnteredAt: now,
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		confirmation := model.TourGroupConfirmation{
			GroupID: group.ID, Sequence: 1, TravelTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID, ScenicAreaID: fixture.area.ID,
			ConfirmedCount: 1, GuideName: "Guide Secret", GuidePhone: "13800000000", PlateNumber: "SECRET-PLATE", Notes: "confirmation-secret", SubmittedAt: now,
		}
		if err := tx.Create(&confirmation).Error; err != nil {
			return err
		}
		statement := model.TeamSettlementStatement{
			TravelTenantID: fixture.travel.ID, SupplierTenantID: fixture.supplier.ID, GroupID: group.ID, Sequence: 1,
			Kind: "original", StatementNo: fmt.Sprintf("AGENT-TEAM-STMT-%d", time.Now().UnixNano()), IdempotencyKey: "agent-team-private-statement",
			GrossCents: 10000, RefundCents: 1000, DepositCents: 2000, NetCents: 7000, AdjustmentCents: 200,
			Status: "disputed", DisputeReason: "dispute-secret", PaymentProof: "payment-proof-secret",
		}
		if err := tx.Create(&statement).Error; err != nil {
			return err
		}
		return tx.Create(&model.TeamSettlementAdjustment{StatementID: statement.ID, Sequence: 1, ActorTenantID: fixture.travel.ID, AmountCents: 200, PreviousAdjustmentCents: 0, NewAdjustmentCents: 200, Reason: "adjustment-secret"}).Error
	}); err != nil {
		t.Fatal(err)
	}

	contractExecution, err := executeAgentTeamContractQuery(nil, agentToolRequest{TenantID: fixture.travel.ID, RawArgs: `{"limit":20}`})
	if err != nil {
		t.Fatalf("contract query: %v", err)
	}
	contracts := decodeAgentTeamRows[agentTeamContractRow](t, contractExecution)
	if len(contracts) != 1 || contracts[0].CounterpartyName != fixture.supplier.Name || contracts[0].ContractNo != fixture.contract.ContractNo {
		t.Fatalf("unexpected contract projection: %+v", contracts)
	}

	start := now.AddDate(0, 0, -1).Format("2006-01-02")
	end := now.AddDate(0, 0, 2).Format("2006-01-02")
	groupExecution, err := executeAgentTeamGroupQuery(nil, agentToolRequest{TenantID: fixture.supplier.ID, RawArgs: fmt.Sprintf(`{"search":"Agent Team","start_date":"%s","end_date":"%s","limit":20}`, start, end)})
	if err != nil {
		t.Fatalf("group query: %v", err)
	}
	groups := decodeAgentTeamRows[agentTeamGroupRow](t, groupExecution)
	if len(groups) != 1 || groups[0].CounterpartyName != fixture.travel.Name || groups[0].AdmittedCount != 1 || groups[0].AdmissionBatchCount != 1 || groups[0].ConfirmationCount != 1 || groups[0].LatestConfirmedCount != 1 {
		t.Fatalf("unexpected group projection: %+v", groups)
	}
	if groups[0].SupplierAcknowledged {
		t.Fatalf("unacknowledged confirmation was reported as acknowledged: %+v", groups[0])
	}

	settlementExecution, err := executeAgentTeamSettlementQuery(nil, agentToolRequest{TenantID: fixture.travel.ID, RawArgs: `{"status":"disputed","limit":20}`})
	if err != nil {
		t.Fatalf("settlement query: %v", err)
	}
	settlements := decodeAgentTeamRows[agentTeamSettlementRow](t, settlementExecution)
	if len(settlements) != 1 || settlements[0].CounterpartyName != fixture.supplier.Name || settlements[0].PayableCents != 7200 || settlements[0].StatementNo == "" {
		t.Fatalf("unexpected settlement projection: %+v", settlements)
	}

	accountExecution, err := executeAgentTeamAccountQuery(nil, agentToolRequest{TenantID: fixture.travel.ID, RawArgs: `{"limit":20}`})
	if err != nil {
		t.Fatalf("account query: %v", err)
	}
	accounts := decodeAgentTeamRows[agentTeamAccountRow](t, accountExecution)
	if len(accounts) != 1 || accounts[0].CounterpartyName != fixture.supplier.Name || accounts[0].ActiveContractCount != 1 || accounts[0].PendingCents != 7200 {
		t.Fatalf("unexpected account projection: %+v", accounts)
	}

	for _, execution := range []agentToolExecution{contractExecution, groupExecution, settlementExecution, accountExecution} {
		for _, forbidden := range []string{"13900000000", "ID-SECRET", "TICKET-SECRET", "Guide Secret", "13800000000", "SECRET-PLATE", "confirmation-secret", "dispute-secret", "payment-proof-secret", "adjustment-secret", `"id"`, `"device_id"`, `"operator_id"`} {
			if strings.Contains(execution.ResultJSON, forbidden) {
				t.Fatalf("team projection leaked %q: %s", forbidden, execution.ResultJSON)
			}
		}
	}

	foreign := model.Tenant{Name: "Agent Team Query Foreign", SystemCode: fmt.Sprintf("AGENT-TEAM-QUERY-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&foreign).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantCapability{TenantID: foreign.ID, Capability: "travel_agency", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name string
		run  func(*AgentTaskService, agentToolRequest) (agentToolExecution, error)
		args string
	}{
		{name: "contracts", run: executeAgentTeamContractQuery, args: `{"limit":20}`},
		{name: "groups", run: executeAgentTeamGroupQuery, args: fmt.Sprintf(`{"start_date":"%s","end_date":"%s","limit":20}`, start, end)},
		{name: "settlements", run: executeAgentTeamSettlementQuery, args: `{"limit":20}`},
		{name: "accounts", run: executeAgentTeamAccountQuery, args: `{"limit":20}`},
	} {
		execution, queryErr := query.run(nil, agentToolRequest{TenantID: foreign.ID, RawArgs: query.args})
		if queryErr != nil {
			t.Fatalf("foreign %s query: %v", query.name, queryErr)
		}
		result := decodeAgentQueryResult(t, execution)
		if result.Returned != 0 || result.Total != 0 || strings.Contains(execution.ResultJSON, fixture.travel.Name) || strings.Contains(execution.ResultJSON, fixture.supplier.Name) {
			t.Fatalf("foreign %s query leaked relationship facts: %s", query.name, execution.ResultJSON)
		}
	}
}

func TestAgentTeamToolsRejectDistributorAndCrossTenantAccess(t *testing.T) {
	fixture := seedTeamP0Fixture(t, 100000)
	foreign := model.Tenant{Name: "Agent Team Foreign", SystemCode: fmt.Sprintf("AGENT-TEAM-FOREIGN-%d", time.Now().UnixNano()), SecretKey: "foreign", Status: "active"}
	distributor := model.Tenant{Name: "Agent Team Distributor", SystemCode: fmt.Sprintf("AGENT-TEAM-DIST-%d", time.Now().UnixNano()), SecretKey: "dist", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&foreign).Error; err != nil {
			return err
		}
		if err := tx.Create(&distributor).Error; err != nil {
			return err
		}
		return tx.Create(&model.TenantCapability{TenantID: distributor.ID, Capability: "distributor", Status: "active"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"query_team_contracts", "query_team_groups", "query_team_settlement_summary", "query_team_account_summary"} {
		spec, ok := findAgentTool(name)
		if !ok {
			t.Fatalf("team tool %q was not registered", name)
		}
		if agentToolAllowed(distributor.ID, "admin", spec) {
			t.Fatalf("distributor was allowed to use team-only tool %q", name)
		}
	}

	contracts, err := executeAgentTeamContractQuery(nil, agentToolRequest{TenantID: foreign.ID, RawArgs: `{"limit":20}`})
	if err != nil {
		t.Fatalf("foreign contract query: %v", err)
	}
	if rows := decodeAgentTeamRows[agentTeamContractRow](t, contracts); len(rows) != 0 {
		t.Fatalf("foreign tenant received team contracts: %+v", rows)
	}
	groups, err := executeAgentTeamGroupQuery(nil, agentToolRequest{TenantID: foreign.ID, RawArgs: `{"limit":20}`})
	if err != nil {
		t.Fatalf("foreign group query: %v", err)
	}
	if rows := decodeAgentTeamRows[agentTeamGroupRow](t, groups); len(rows) != 0 {
		t.Fatalf("foreign tenant received team groups: %+v", rows)
	}

	teamSpec, _ := findAgentTool("query_team_groups")
	if !agentToolAllowed(fixture.travel.ID, "team_operator", teamSpec) || agentToolAllowed(fixture.travel.ID, "product_operator", teamSpec) {
		t.Fatalf("team query permission matrix is incorrect")
	}
	settlementSpec, _ := findAgentTool("query_team_settlement_summary")
	if !agentToolAllowed(fixture.travel.ID, "settlement_operator", settlementSpec) || !agentToolAllowed(fixture.travel.ID, "team_operator", settlementSpec) || agentToolAllowed(fixture.travel.ID, "product_operator", settlementSpec) {
		t.Fatalf("team settlement query permission matrix is incorrect")
	}
}

func TestAgentTeamVisitDateRangeDefaultsForwardAndKeepsExplicitBounds(t *testing.T) {
	start, end, err := agentTeamVisitDateRange("", "")
	if err != nil || !end.After(start) || end.Sub(start) != 30*24*time.Hour {
		t.Fatalf("unexpected forward team default range start=%s end=%s err=%v", start, end, err)
	}
	explicitStart, explicitEnd, err := agentTeamVisitDateRange("2026-08-01", "2026-08-02")
	if err != nil || explicitStart.Format("2006-01-02") != "2026-08-01" || explicitEnd.Format("2006-01-02") != "2026-08-03" {
		t.Fatalf("unexpected explicit team range start=%s end=%s err=%v", explicitStart, explicitEnd, err)
	}
}
