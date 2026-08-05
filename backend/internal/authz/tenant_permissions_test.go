package authz

import "testing"

func TestFixedTenantRolePermissions(t *testing.T) {
	tests := []struct {
		role, allowed, denied string
	}{
		{RoleProductOperator, PermissionCatalogWrite, PermissionFinanceWrite},
		{RoleTeamOperator, PermissionTeamsWrite, PermissionDistributionWrite},
		{RoleSettlementOperator, PermissionSettlementsWrite, PermissionCatalogWrite},
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
	if !HasTenantPermission("checker", PermissionTicketsVerify) {
		t.Fatal("checker should retain ticket verification permission")
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
