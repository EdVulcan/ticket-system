package service

import (
	"strings"
	"ticket-backend/internal/model"
	"time"
)

// Team query tools intentionally expose relationship-scoped operational facts
// only. Roster identity, ticket codes, device/operator data, payment proof
// and settlement negotiation details stay inside their dedicated workflows.

type agentTeamContractArgs struct {
	Search string `json:"search"`
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type agentTeamContractRow struct {
	ContractNo       string `json:"contract_no"`
	CounterpartyName string `json:"counterparty_name"`
	Status           string `json:"status"`
	SettlementDays   int    `json:"settlement_days"`
	CreditLimitCents int64  `json:"credit_limit_cents"`
	PriceRuleCount   int    `json:"price_rule_count"`
	StartsAt         string `json:"starts_at,omitempty"`
	EndsAt           string `json:"ends_at,omitempty"`
}

func executeAgentTeamContractQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentTeamContractArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if len([]byte(args.Search)) > 100 {
		return agentToolExecution{}, agentInvalid("团队合同查询条件过长")
	}
	if args.Status != "" && args.Status != "active" && args.Status != "suspended" {
		return agentToolExecution{}, agentInvalid("团队合同状态不是受支持的值")
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Model(&model.TravelContract{}).
		Where("travel_contracts.travel_tenant_id = ? OR travel_contracts.supplier_tenant_id = ?", request.TenantID, request.TenantID)
	if args.Status != "" {
		query = query.Where("travel_contracts.status = ?", args.Status)
	}
	if search := strings.TrimSpace(args.Search); search != "" {
		query = query.Where("travel_contracts.contract_no ILIKE ?", "%"+search+"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var contracts []model.TravelContract
	if err := query.Order("travel_contracts.created_at DESC, travel_contracts.id DESC").Limit(limit).Find(&contracts).Error; err != nil {
		return agentToolExecution{}, err
	}
	partnerIDs := make([]uint, 0, len(contracts))
	for _, contract := range contracts {
		if contract.TravelTenantID == request.TenantID {
			partnerIDs = append(partnerIDs, contract.SupplierTenantID)
		} else {
			partnerIDs = append(partnerIDs, contract.TravelTenantID)
		}
	}
	partnerNames, err := agentTeamTenantNames(partnerIDs)
	if err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentTeamContractRow, 0, len(contracts))
	for _, contract := range contracts {
		partnerID := contract.TravelTenantID
		if partnerID == request.TenantID {
			partnerID = contract.SupplierTenantID
		}
		rules, ruleErr := decodeTeamPriceRules(contract.PriceRulesJSON)
		if ruleErr != nil {
			return agentToolExecution{}, ruleErr
		}
		row := agentTeamContractRow{
			ContractNo: contract.ContractNo, CounterpartyName: partnerNames[partnerID], Status: contract.Status,
			SettlementDays: contract.SettlementDays, CreditLimitCents: contract.CreditLimitCents, PriceRuleCount: len(rules),
		}
		if contract.StartsAt != nil {
			row.StartsAt = contract.StartsAt.Format("2006-01-02")
		}
		if contract.EndsAt != nil {
			row.EndsAt = contract.EndsAt.Format("2006-01-02")
		}
		rows = append(rows, row)
	}
	filters := map[string]interface{}{"search": args.Search, "status": args.Status}
	return agentQueryExecution(agentModuleTeams, "query_team_contracts", filters, rows, len(rows), total, limit)
}

type agentTeamGroupArgs struct {
	Search    string `json:"search"`
	Status    string `json:"status"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Limit     int    `json:"limit"`
}

type agentTeamGroupRow struct {
	GroupNo              string `json:"group_no"`
	GroupName            string `json:"group_name"`
	CounterpartyName     string `json:"counterparty_name"`
	VisitDate            string `json:"visit_date"`
	ExpectedCount        int    `json:"expected_count"`
	Status               string `json:"status"`
	SettlementStatus     string `json:"settlement_status"`
	AdmissionBatchCount  int    `json:"admission_batch_count"`
	AdmittedCount        int    `json:"admitted_count"`
	ConfirmationCount    int    `json:"confirmation_count"`
	LatestConfirmedCount int    `json:"latest_confirmed_count,omitempty"`
	SupplierAcknowledged bool   `json:"supplier_acknowledged"`
}

type agentTeamEntryAggregate struct {
	GroupID uint
	Count   int
	Entered int
}

type agentTeamConfirmationAggregate struct {
	GroupID              uint
	Count                int
	LatestSequence       int
	LatestConfirmedCount int
	LatestAcknowledgedAt *time.Time
}

func executeAgentTeamGroupQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentTeamGroupArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if len([]byte(args.Search)) > 100 {
		return agentToolExecution{}, agentInvalid("团队计划查询条件过长")
	}
	if args.Status != "" {
		switch args.Status {
		case "draft", "confirmed", "partial_entry", "entered", "cancelled":
		default:
			return agentToolExecution{}, agentInvalid("团队计划状态不是受支持的值")
		}
	}
	start, end, err := agentTeamVisitDateRange(args.StartDate, args.EndDate)
	if err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	inclusiveEnd := end.AddDate(0, 0, -1)
	groups, total, err := (&TeamService{}).ListGroupsWithOptions(request.TenantID, TeamGroupListOptions{
		Page: 1, PageSize: limit, Keyword: args.Search, Status: args.Status, VisitStart: &start, VisitEnd: &inclusiveEnd,
	})
	if err != nil {
		return agentToolExecution{}, err
	}
	groupIDs := make([]uint, 0, len(groups))
	partnerIDs := make([]uint, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
		if group.TenantID == request.TenantID {
			partnerIDs = append(partnerIDs, group.SupplierTenantID)
		} else {
			partnerIDs = append(partnerIDs, group.TenantID)
		}
	}
	partnerNames, err := agentTeamTenantNames(partnerIDs)
	if err != nil {
		return agentToolExecution{}, err
	}
	entries, confirmations, err := agentTeamGroupAggregates(groupIDs)
	if err != nil {
		return agentToolExecution{}, err
	}
	rows := make([]agentTeamGroupRow, 0, len(groups))
	for _, group := range groups {
		partnerID := group.TenantID
		if partnerID == request.TenantID {
			partnerID = group.SupplierTenantID
		}
		entry := entries[group.ID]
		confirmation := confirmations[group.ID]
		rows = append(rows, agentTeamGroupRow{
			GroupNo: group.GroupNo, GroupName: group.Name, CounterpartyName: partnerNames[partnerID],
			VisitDate: group.VisitDate.Format("2006-01-02"), ExpectedCount: group.ExpectedCount, Status: group.Status,
			SettlementStatus: group.SettlementStatus, AdmissionBatchCount: entry.Count, AdmittedCount: entry.Entered,
			ConfirmationCount: confirmation.Count, LatestConfirmedCount: confirmation.LatestConfirmedCount,
			SupplierAcknowledged: confirmation.LatestAcknowledgedAt != nil,
		})
	}
	filters := map[string]interface{}{"search": args.Search, "status": args.Status, "start_date": start.Format("2006-01-02"), "end_date": end.AddDate(0, 0, -1).Format("2006-01-02")}
	return agentQueryExecution(agentModuleTeams, "query_team_groups", filters, rows, len(rows), total, limit)
}

// Team planning is normally forward-looking, unlike sales and verification
// reports. When the operator gives no date, show the coming 30 calendar days.
func agentTeamVisitDateRange(startDate, endDate string) (time.Time, time.Time, error) {
	if strings.TrimSpace(startDate) != "" || strings.TrimSpace(endDate) != "" {
		return agentQueryDateRange(startDate, endDate, 366, 30)
	}
	location := time.Local
	now := time.Now().In(location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	return start, start.AddDate(0, 0, 30), nil
}

func agentTeamTenantNames(ids []uint) (map[uint]string, error) {
	names := make(map[uint]string)
	if len(ids) == 0 {
		return names, nil
	}
	var tenants []model.Tenant
	if err := model.DB.Select("id", "name").Where("id IN ?", ids).Find(&tenants).Error; err != nil {
		return nil, err
	}
	for _, tenant := range tenants {
		names[tenant.ID] = tenant.Name
	}
	return names, nil
}

func agentTeamGroupAggregates(groupIDs []uint) (map[uint]agentTeamEntryAggregate, map[uint]agentTeamConfirmationAggregate, error) {
	entries := make(map[uint]agentTeamEntryAggregate)
	confirmations := make(map[uint]agentTeamConfirmationAggregate)
	if len(groupIDs) == 0 {
		return entries, confirmations, nil
	}
	var entryRows []agentTeamEntryAggregate
	if err := model.DB.Model(&model.TourEntryBatch{}).
		Select("group_id, COUNT(*) AS count, COALESCE(SUM(entered_count), 0) AS entered").
		Where("group_id IN ?", groupIDs).Group("group_id").Scan(&entryRows).Error; err != nil {
		return nil, nil, err
	}
	for _, row := range entryRows {
		entries[row.GroupID] = row
	}
	var confirmationRows []model.TourGroupConfirmation
	if err := model.DB.Select("group_id", "sequence", "confirmed_count", "supplier_acknowledged_at").
		Where("group_id IN ?", groupIDs).Order("group_id ASC, sequence DESC").Find(&confirmationRows).Error; err != nil {
		return nil, nil, err
	}
	for _, row := range confirmationRows {
		aggregate := confirmations[row.GroupID]
		aggregate.GroupID = row.GroupID
		aggregate.Count++
		if row.Sequence > aggregate.LatestSequence {
			aggregate.LatestSequence = row.Sequence
			aggregate.LatestConfirmedCount = row.ConfirmedCount
			aggregate.LatestAcknowledgedAt = row.SupplierAcknowledgedAt
		}
		confirmations[row.GroupID] = aggregate
	}
	return entries, confirmations, nil
}

type agentTeamSettlementArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

type agentTeamSettlementRow struct {
	StatementNo      string `json:"statement_no"`
	Kind             string `json:"kind"`
	GroupNo          string `json:"group_no"`
	GroupName        string `json:"group_name"`
	CounterpartyName string `json:"counterparty_name"`
	Status           string `json:"status"`
	GrossCents       int64  `json:"gross_cents"`
	RefundCents      int64  `json:"refund_cents"`
	DepositCents     int64  `json:"deposit_cents"`
	NetCents         int64  `json:"net_cents"`
	AdjustmentCents  int64  `json:"adjustment_cents"`
	PayableCents     int64  `json:"payable_cents"`
	DueAt            string `json:"due_at,omitempty"`
	ConfirmedAt      string `json:"confirmed_at,omitempty"`
	PaidAt           string `json:"paid_at,omitempty"`
}

func executeAgentTeamSettlementQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentTeamSettlementArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	if args.Status != "" {
		switch args.Status {
		case "draft", "supplier_confirmed", "confirmed", "payment_submitted", "disputed", "paid":
		default:
			return agentToolExecution{}, agentInvalid("团队结算状态不是受支持的值")
		}
	}
	limit := agentQueryLimit(args.Limit)
	query := model.DB.Model(&model.TeamSettlementStatement{}).
		Where("team_settlement_statements.travel_tenant_id = ? OR team_settlement_statements.supplier_tenant_id = ?", request.TenantID, request.TenantID)
	if args.Status != "" {
		query = query.Where("team_settlement_statements.status = ?", args.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return agentToolExecution{}, err
	}
	var statements []model.TeamSettlementStatement
	if err := query.Order("team_settlement_statements.created_at DESC, team_settlement_statements.id DESC").Limit(limit).Find(&statements).Error; err != nil {
		return agentToolExecution{}, err
	}
	groupIDs := make([]uint, 0, len(statements))
	partnerIDs := make([]uint, 0, len(statements))
	for _, statement := range statements {
		groupIDs = append(groupIDs, statement.GroupID)
		if statement.TravelTenantID == request.TenantID {
			partnerIDs = append(partnerIDs, statement.SupplierTenantID)
		} else {
			partnerIDs = append(partnerIDs, statement.TravelTenantID)
		}
	}
	partnerNames, err := agentTeamTenantNames(partnerIDs)
	if err != nil {
		return agentToolExecution{}, err
	}
	groups := make(map[uint]model.TourGroup)
	if len(groupIDs) > 0 {
		var records []model.TourGroup
		if err := model.DB.Select("id", "tenant_id", "supplier_tenant_id", "group_no", "name").Where("id IN ?", groupIDs).Find(&records).Error; err != nil {
			return agentToolExecution{}, err
		}
		for _, group := range records {
			groups[group.ID] = group
		}
	}
	rows := make([]agentTeamSettlementRow, 0, len(statements))
	for _, statement := range statements {
		group, ok := groups[statement.GroupID]
		if !ok || group.TenantID != statement.TravelTenantID || group.SupplierTenantID != statement.SupplierTenantID {
			return agentToolExecution{}, agentConflict("团队结算关联关系已变化，无法安全展示")
		}
		partnerID := statement.TravelTenantID
		if partnerID == request.TenantID {
			partnerID = statement.SupplierTenantID
		}
		row := agentTeamSettlementRow{
			StatementNo: statement.StatementNo, Kind: statement.Kind, GroupNo: group.GroupNo, GroupName: group.Name,
			CounterpartyName: partnerNames[partnerID], Status: statement.Status, GrossCents: statement.GrossCents,
			RefundCents: statement.RefundCents, DepositCents: statement.DepositCents, NetCents: statement.NetCents,
			AdjustmentCents: statement.AdjustmentCents, PayableCents: statement.NetCents + statement.AdjustmentCents,
		}
		if statement.DueAt != nil {
			row.DueAt = statement.DueAt.Format("2006-01-02")
		}
		if statement.ConfirmedAt != nil {
			row.ConfirmedAt = statement.ConfirmedAt.UTC().Format(time.RFC3339)
		}
		if statement.PaidAt != nil {
			row.PaidAt = statement.PaidAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	return agentQueryExecution(agentModuleTeams, "query_team_settlement_summary", map[string]interface{}{"status": args.Status}, rows, len(rows), total, limit)
}

type agentTeamAccountArgs struct {
	Limit int `json:"limit"`
}

type agentTeamAccountRow struct {
	CounterpartyName     string `json:"counterparty_name"`
	ActiveContractCount  int    `json:"active_contract_count"`
	CreditLimitCents     int64  `json:"credit_limit_cents"`
	GroupCount           int    `json:"group_count"`
	CreditUsedCents      int64  `json:"credit_used_cents"`
	AvailableCreditCents int64  `json:"available_credit_cents"`
	PendingCents         int64  `json:"pending_cents"`
	PaidCents            int64  `json:"paid_cents"`
	DisputedCount        int    `json:"disputed_count"`
}

func executeAgentTeamAccountQuery(_ *AgentTaskService, request agentToolRequest) (agentToolExecution, error) {
	var args agentTeamAccountArgs
	if err := decodeAgentToolArguments(request.RawArgs, &args); err != nil {
		return agentToolExecution{}, err
	}
	limit := agentQueryLimit(args.Limit)
	summaries, err := (&TeamService{}).ListTeamAccountSummaries(request.TenantID)
	if err != nil {
		return agentToolExecution{}, err
	}
	total := int64(len(summaries))
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	rows := make([]agentTeamAccountRow, 0, len(summaries))
	for _, summary := range summaries {
		partnerName := summary.TravelTenantName
		if summary.TravelTenantID == request.TenantID {
			partnerName = summary.SupplierTenantName
		}
		rows = append(rows, agentTeamAccountRow{
			CounterpartyName: partnerName, ActiveContractCount: summary.ActiveContractCount, CreditLimitCents: summary.CreditLimitCents,
			GroupCount: summary.GroupCount, CreditUsedCents: summary.CreditUsedCents, AvailableCreditCents: summary.AvailableCreditCents,
			PendingCents: summary.PendingCents, PaidCents: summary.PaidCents, DisputedCount: summary.DisputedCount,
		})
	}
	return agentQueryExecution(agentModuleTeams, "query_team_account_summary", map[string]interface{}{}, rows, len(rows), total, limit)
}
