package service

import (
	"errors"
	"sort"
	"ticket-backend/internal/model"
)

// TeamAccountSummary is a relationship-level operational reconciliation view.
// It intentionally covers only already-funded amounts, contract credit use and
// settlement facts.
type TeamAccountSummary struct {
	TravelTenantID       uint   `json:"travel_tenant_id"`
	TravelTenantName     string `json:"travel_tenant_name"`
	SupplierTenantID     uint   `json:"supplier_tenant_id"`
	SupplierTenantName   string `json:"supplier_tenant_name"`
	ActiveContractCount  int    `json:"active_contract_count"`
	CreditLimitCents     int64  `json:"credit_limit_cents"`
	GroupCount           int    `json:"group_count"`
	ContractAmountCents  int64  `json:"contract_amount_cents"`
	DepositCents         int64  `json:"deposit_cents"`
	CreditUsedCents      int64  `json:"credit_used_cents"`
	AvailableCreditCents int64  `json:"available_credit_cents"`
	PendingCents         int64  `json:"pending_cents"`
	PaidCents            int64  `json:"paid_cents"`
	DisputedCount        int    `json:"disputed_count"`
}

type teamAccountKey struct {
	travel   uint
	supplier uint
}

func (s *TeamService) ListTeamAccountSummaries(tenantID uint) ([]TeamAccountSummary, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	rows := make(map[teamAccountKey]*TeamAccountSummary)
	ensure := func(travelTenantID, supplierTenantID uint) *TeamAccountSummary {
		key := teamAccountKey{travel: travelTenantID, supplier: supplierTenantID}
		if rows[key] == nil {
			rows[key] = &TeamAccountSummary{TravelTenantID: travelTenantID, SupplierTenantID: supplierTenantID}
		}
		return rows[key]
	}

	var contracts []model.TravelContract
	if err := model.DB.Where("travel_tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID).Find(&contracts).Error; err != nil {
		return nil, err
	}
	for _, contract := range contracts {
		row := ensure(contract.TravelTenantID, contract.SupplierTenantID)
		if contract.Status == "active" {
			row.ActiveContractCount++
			row.CreditLimitCents += contract.CreditLimitCents
		}
	}

	var groups []model.TourGroup
	if err := model.DB.Where("(tenant_id = ? OR supplier_tenant_id = ?) AND status != ?", tenantID, tenantID, "cancelled").Find(&groups).Error; err != nil {
		return nil, err
	}
	for _, group := range groups {
		row := ensure(group.TenantID, group.SupplierTenantID)
		row.GroupCount++
		row.ContractAmountCents += group.ContractAmountCents
		row.DepositCents += group.DepositCents
		if group.SettlementStatus != "settled" {
			row.CreditUsedCents += group.CreditUsedCents
		}
	}

	var statements []model.TeamSettlementStatement
	if err := model.DB.Where("travel_tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID).Find(&statements).Error; err != nil {
		return nil, err
	}
	for _, statement := range statements {
		row := ensure(statement.TravelTenantID, statement.SupplierTenantID)
		payable := statement.NetCents + statement.AdjustmentCents
		if statement.Status == "paid" {
			row.PaidCents += payable
		} else {
			row.PendingCents += payable
		}
		if statement.Status == "disputed" {
			row.DisputedCount++
		}
	}

	result := make([]TeamAccountSummary, 0, len(rows))
	tenantIDs := make([]uint, 0, len(rows)*2)
	seenTenants := make(map[uint]struct{}, len(rows)*2)
	for key := range rows {
		for _, tenantID := range []uint{key.travel, key.supplier} {
			if _, ok := seenTenants[tenantID]; tenantID != 0 && !ok {
				seenTenants[tenantID] = struct{}{}
				tenantIDs = append(tenantIDs, tenantID)
			}
		}
	}
	var tenants []model.Tenant
	if err := model.DB.Select("id", "name").Where("id IN ?", tenantIDs).Find(&tenants).Error; err != nil {
		return nil, err
	}
	tenantNames := make(map[uint]string, len(tenants))
	for i := range tenants {
		tenantNames[tenants[i].ID] = tenants[i].Name
	}
	for _, row := range rows {
		row.TravelTenantName = tenantNames[row.TravelTenantID]
		row.SupplierTenantName = tenantNames[row.SupplierTenantID]
		row.AvailableCreditCents = row.CreditLimitCents - row.CreditUsedCents
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SupplierTenantID == result[j].SupplierTenantID {
			return result[i].TravelTenantID < result[j].TravelTenantID
		}
		return result[i].SupplierTenantID < result[j].SupplierTenantID
	})
	return result, nil
}
