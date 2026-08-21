package authz

import "testing"

func TestFixedTenantRolePermissions(t *testing.T) {
	tests := []struct {
		role, allowed, denied string
	}{
		{RoleProductOperator, PermissionCatalogWrite, PermissionFinanceWrite},
		{RoleTeamOperator, PermissionTeamsWrite, PermissionTeamContractsWrite},
		{RoleSettlementOperator, PermissionTeamContractsWrite, PermissionCatalogWrite},
		{RoleViewer, PermissionOrdersRead, PermissionOrdersWrite},
	}
	for _, test := range tests {
		if !HasTenantPermission(test.role, test.allowed) {
			t.Fatalf("role %s should have %s", test.role, test.allowed)
		}
		if HasTenantPermission(test.role, test.denied) {
			t.Fatalf("role %s should not have %s", test.role, test.denied)
		}
	}
	if !HasTenantPermission(RoleTenantAdmin, PermissionTenantAccounts) || !HasTenantPermission("super_admin", PermissionPaymentConfig) {
		t.Fatal("tenant administrators should retain all tenant permissions")
	}
}

func TestAgentUseIsSeparateFromHighRiskPermissions(t *testing.T) {
	for _, role := range []string{RoleTenantAdmin, RoleProductOperator, RoleTeamOperator, RoleSettlementOperator, RoleViewer} {
		if !HasTenantPermission(role, PermissionAgentUse) {
			t.Fatalf("role %s should be allowed to open the low-risk assistant", role)
		}
	}
	for _, role := range []string{"checker", "seller,checker"} {
		if HasTenantPermission(role, PermissionAgentUse) {
			t.Fatalf("frontline checker role %s must not open the management assistant", role)
		}
	}
	for _, role := range []string{RoleProductOperator, RoleViewer, "seller"} {
		if HasTenantPermission(role, PermissionRefundsWrite) || HasTenantPermission(role, PermissionPaymentConfig) {
			t.Fatalf("agent-enabled role %s must not gain high-risk permissions", role)
		}
	}
}

func TestMaintenanceTunnelIsRestrictedToTenantAdministrator(t *testing.T) {
	if !HasTenantPermission(RoleTenantAdmin, PermissionOnsiteMaintenance) {
		t.Fatal("tenant administrator must be able to manage maintenance sessions")
	}
	for _, role := range []string{RoleProductOperator, RoleTeamOperator, RoleSettlementOperator, RoleViewer, "seller", "checker"} {
		if HasTenantPermission(role, PermissionOnsiteMaintenance) {
			t.Fatalf("role %q unexpectedly received maintenance tunnel permission", role)
		}
	}
}

func TestTeamCommercialConfigurationRequiresContractPermission(t *testing.T) {
	if HasTenantPermission(RoleTeamOperator, PermissionTeamContractsWrite) {
		t.Fatal("team operator must not change contract prices or credit limits")
	}
	if !HasTenantPermission(RoleSettlementOperator, PermissionTeamContractsWrite) {
		t.Fatal("settlement operator should manage team contract commercial terms")
	}
	for _, role := range []string{RoleTenantAdmin, "super_admin"} {
		if !HasTenantPermission(role, PermissionTeamContractsWrite) {
			t.Fatalf("tenant administrator %s should manage team contracts", role)
		}
	}
}

func TestFrontlineStaffCannotOperateAfterSalesOrRefunds(t *testing.T) {
	for _, role := range []string{"seller", "checker", "seller,checker"} {
		for _, permission := range []string{
			PermissionAfterSalesRead,
			PermissionAfterSalesWrite,
			PermissionAfterSalesApprove,
			PermissionRefundsRead,
			PermissionRefundsWrite,
		} {
			if HasTenantPermission(role, permission) {
				t.Fatalf("frontline role %s must not have %s", role, permission)
			}
		}
	}

	if !HasTenantPermission("seller", PermissionOrdersWrite) || !HasTenantPermission("seller", PermissionPaymentsWrite) {
		t.Fatal("seller should retain ticket sales and collection permissions")
	}
	for _, permission := range []string{PermissionTeamsRead, PermissionTeamsWrite, PermissionOnsiteManage} {
		if HasTenantPermission("seller", permission) {
			t.Fatalf("seller must not have management permission %s", permission)
		}
	}
	if !HasTenantPermission("checker", PermissionTicketsVerify) {
		t.Fatal("checker should retain ticket verification permission")
	}
}

func TestHotelReservationPermissionsStayOutOfCatalogAndFrontlineOperations(t *testing.T) {
	for _, role := range []string{"seller", "checker", "seller,checker", RoleViewer, RoleTeamOperator, RoleSettlementOperator} {
		for _, permission := range []string{
			PermissionHotelReservationsRead,
			PermissionHotelReservationsWrite,
			PermissionHotelReservationsExport,
		} {
			if HasTenantPermission(role, permission) {
				t.Fatalf("role %s must not have hotel reservation permission %s", role, permission)
			}
		}
	}

	for _, permission := range []string{
		PermissionHotelReservationsRead,
		PermissionHotelReservationsWrite,
		PermissionHotelReservationsExport,
	} {
		if !HasTenantPermission(RoleProductOperator, permission) {
			t.Fatalf("product operator should have hotel reservation permission %s", permission)
		}
	}
}

func TestOnlyFixedDelegatedRolesCanBeCreated(t *testing.T) {
	for _, role := range []string{RoleTenantAdmin, RoleProductOperator, RoleTeamOperator, RoleSettlementOperator, RoleViewer} {
		if !IsDelegatedTenantRole(role) {
			t.Fatalf("expected delegated role %s", role)
		}
	}
	for _, role := range []string{"super_admin", "seller", "checker", "custom"} {
		if IsDelegatedTenantRole(role) {
			t.Fatalf("unexpected delegated role %s", role)
		}
	}
}
