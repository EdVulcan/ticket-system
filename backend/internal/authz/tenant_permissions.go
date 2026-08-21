package authz

import "strings"

const (
	RoleTenantAdmin        = "admin"
	RoleProductOperator    = "product_operator"
	RoleTeamOperator       = "team_operator"
	RoleSettlementOperator = "settlement_operator"
	RoleViewer             = "viewer"
)

const (
	PermissionTenantAccounts          = "tenant_accounts.manage"
	PermissionCatalogRead             = "catalog.read"
	PermissionCatalogWrite            = "catalog.write"
	PermissionOrdersRead              = "orders.read"
	PermissionOrdersWrite             = "orders.write"
	PermissionAfterSalesRead          = "after_sales.read"
	PermissionAfterSalesWrite         = "after_sales.write"
	PermissionAfterSalesApprove       = "after_sales.approve"
	PermissionDistributionRead        = "distribution.read"
	PermissionDistributionWrite       = "distribution.write"
	PermissionChannelsRead            = "channels.read"
	PermissionChannelsWrite           = "channels.write"
	PermissionTeamsRead               = "teams.read"
	PermissionTeamsWrite              = "teams.write"
	PermissionTeamContractsWrite      = "teams.contracts.write"
	PermissionFinanceRead             = "finance.read"
	PermissionFinanceWrite            = "finance.write"
	PermissionSettlementsRead         = "settlements.read"
	PermissionSettlementsWrite        = "settlements.write"
	PermissionRefundsRead             = "refunds.read"
	PermissionRefundsWrite            = "refunds.write"
	PermissionReportsRead             = "reports.read"
	PermissionOperationsRead          = "operations.read"
	PermissionOperationsWrite         = "operations.write"
	PermissionOnsiteRead              = "onsite.read"
	PermissionOnsiteManage            = "onsite.manage"
	PermissionOnsiteMaintenance       = "onsite.maintenance"
	PermissionTicketsVerify           = "tickets.verify"
	PermissionPaymentsRead            = "payments.read"
	PermissionPaymentsWrite           = "payments.write"
	PermissionPaymentConfig           = "payment_config.manage"
	PermissionHotelReservationsRead   = "hotel_reservations.read"
	PermissionHotelReservationsWrite  = "hotel_reservations.write"
	PermissionHotelReservationsExport = "hotel_reservations.export"
	PermissionAgentUse                = "agent.use"
)

var rolePermissions = map[string][]string{
	RoleProductOperator: {
		PermissionCatalogRead, PermissionCatalogWrite, PermissionOrdersRead,
		PermissionAfterSalesRead, PermissionAfterSalesWrite,
		PermissionDistributionRead, PermissionDistributionWrite,
		PermissionChannelsRead, PermissionChannelsWrite,
		PermissionReportsRead, PermissionOperationsRead, PermissionOnsiteRead,
		PermissionHotelReservationsRead, PermissionHotelReservationsWrite, PermissionHotelReservationsExport,
		PermissionAgentUse,
	},
	RoleTeamOperator: {
		PermissionOrdersRead, PermissionAfterSalesRead, PermissionAfterSalesWrite,
		PermissionTeamsRead, PermissionTeamsWrite, PermissionReportsRead,
		PermissionOperationsRead, PermissionOnsiteRead, PermissionFinanceRead,
		PermissionSettlementsRead,
		PermissionAgentUse,
	},
	RoleSettlementOperator: {
		PermissionOrdersRead, PermissionAfterSalesRead, PermissionFinanceRead,
		PermissionFinanceWrite, PermissionSettlementsRead, PermissionSettlementsWrite,
		PermissionRefundsRead, PermissionRefundsWrite, PermissionReportsRead,
		PermissionOperationsRead, PermissionPaymentsRead, PermissionTeamsRead,
		PermissionTeamContractsWrite,
		PermissionAgentUse,
	},
	RoleViewer: {
		PermissionCatalogRead, PermissionOrdersRead, PermissionAfterSalesRead,
		PermissionDistributionRead, PermissionChannelsRead, PermissionTeamsRead,
		PermissionFinanceRead, PermissionSettlementsRead, PermissionRefundsRead,
		PermissionReportsRead, PermissionOperationsRead, PermissionOnsiteRead,
		PermissionPaymentsRead,
		PermissionAgentUse,
	},
	"seller": {
		PermissionCatalogRead, PermissionOrdersRead, PermissionOrdersWrite,
		PermissionOnsiteRead, PermissionOperationsRead,
		PermissionOperationsWrite, PermissionPaymentsRead, PermissionPaymentsWrite,
	},
	"checker": {PermissionCatalogRead, PermissionOnsiteRead, PermissionTicketsVerify},
}

func IsTenantAdministrator(role string) bool {
	return role == RoleTenantAdmin || role == "super_admin"
}

func IsDelegatedTenantRole(role string) bool {
	switch role {
	case RoleTenantAdmin, RoleProductOperator, RoleTeamOperator, RoleSettlementOperator, RoleViewer:
		return true
	default:
		return false
	}
}

func HasTenantPermission(role, permission string) bool {
	if strings.Contains(role, ",") {
		for _, value := range strings.Split(role, ",") {
			if HasTenantPermission(strings.TrimSpace(value), permission) {
				return true
			}
		}
		return false
	}
	if IsTenantAdministrator(role) {
		return true
	}
	for _, granted := range rolePermissions[role] {
		if granted == permission {
			return true
		}
	}
	return false
}

func PermissionsForRole(role string) []string {
	if IsTenantAdministrator(role) {
		return []string{"*"}
	}
	permissions := rolePermissions[role]
	result := make([]string, len(permissions))
	copy(result, permissions)
	return result
}
