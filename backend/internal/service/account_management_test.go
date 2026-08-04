//go:build cgo

package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestPlatformAccountHierarchyAndRootProtection(t *testing.T) {
	resetBusinessData(t)
	hash, err := HashPassword("root-password")
	if err != nil {
		t.Fatal(err)
	}
	root := model.PlatformUser{Username: "root", Password: hash, Role: "platform_admin", Status: "active", IsInitialAdmin: true}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&root).Error }); err != nil {
		t.Fatal(err)
	}

	accounts := &PlatformAccountService{}
	operator, err := accounts.Create("operator", "operator-password", "platform_operator", root.ID, root.Role)
	if err != nil {
		t.Fatal(err)
	}
	if operator.Role != "platform_operator" || operator.IsInitialAdmin {
		t.Fatalf("unexpected operator: %+v", operator)
	}
	if err := accounts.Update(operator.ID, root.ID, root.Role, "platform_admin", "active"); err != nil {
		t.Fatal(err)
	}
	var updated model.PlatformUser
	if err := model.DB.First(&updated, operator.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Role != "platform_admin" || updated.TokenVersion < 2 {
		t.Fatalf("platform account update did not revoke sessions: %+v", updated)
	}
	if err := accounts.Update(root.ID, operator.ID, updated.Role, "platform_operator", "active"); !errors.Is(err, ErrInitialPlatformAdmin) {
		t.Fatalf("root downgrade error=%v", err)
	}
	if err := accounts.ResetPassword(root.ID, operator.ID, updated.Role, "another-password"); !errors.Is(err, ErrInitialPlatformAdmin) {
		t.Fatalf("root reset error=%v", err)
	}
	if err := accounts.Delete(root.ID, operator.ID, updated.Role); !errors.Is(err, ErrInitialPlatformAdmin) {
		t.Fatalf("root delete error=%v", err)
	}
	if err := accounts.Delete(operator.ID, operator.ID, updated.Role); !errors.Is(err, ErrOwnPlatformAccount) {
		t.Fatalf("self delete error=%v", err)
	}
}

func TestAccountsCanChangeOwnPasswordAndRevokeOldSessions(t *testing.T) {
	resetBusinessData(t)
	hash, err := HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	tenant := model.Tenant{Name: "Account Tenant", SystemCode: "ACCOUNT", SecretKey: "secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&tenant).Error }); err != nil {
		t.Fatal(err)
	}
	tenantUser := model.User{Username: "admin", Password: hash, Role: "admin", TenantID: tenant.ID, TokenVersion: 1}
	staff := model.Staff{Name: "Seller", JobNumber: "S001", Password: hash, Roles: "seller", Status: "active", TenantID: tenant.ID, TokenVersion: 1}
	platformUser := model.PlatformUser{Username: "platform", Password: hash, Role: "platform_admin", Status: "active", TokenVersion: 1}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&tenantUser).Error; err != nil {
			return err
		}
		if err := tx.Create(&staff).Error; err != nil {
			return err
		}
		return tx.Create(&platformUser).Error
	}); err != nil {
		t.Fatal(err)
	}

	accounts := &AccountService{}
	if err := accounts.ChangeOwnPassword("tenant", "", tenantUser.ID, 0, tenant.ID, "wrong-password", "new-password"); !errors.Is(err, ErrCurrentPasswordInvalid) {
		t.Fatalf("wrong current password error=%v", err)
	}
	if err := accounts.ChangeOwnPassword("tenant", "", tenantUser.ID, 0, tenant.ID, "old-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ChangeOwnPassword("tenant", "staff:S001", staff.ID, 0, tenant.ID, "old-password", "staff-password"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ChangeOwnPassword("platform", "platform:platform", 0, platformUser.ID, 0, "old-password", "platform-password"); err != nil {
		t.Fatal(err)
	}

	if err := model.DB.First(&tenantUser, tenantUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if tenantUser.TokenVersion != 2 || bcrypt.CompareHashAndPassword([]byte(tenantUser.Password), []byte("new-password")) != nil {
		t.Fatalf("tenant password was not changed safely: %+v", tenantUser)
	}
	if err := model.DB.First(&staff, staff.ID).Error; err != nil {
		t.Fatal(err)
	}
	if staff.TokenVersion != 2 || bcrypt.CompareHashAndPassword([]byte(staff.Password), []byte("staff-password")) != nil {
		t.Fatalf("staff password was not changed safely: %+v", staff)
	}
	if err := model.DB.First(&platformUser, platformUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if platformUser.TokenVersion != 2 || bcrypt.CompareHashAndPassword([]byte(platformUser.Password), []byte("platform-password")) != nil {
		t.Fatalf("platform password was not changed safely: %+v", platformUser)
	}
}

func TestTenantInitialAdminCannotBeResetByDelegatedAdmin(t *testing.T) {
	resetBusinessData(t)
	hash, err := HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	tenant := model.Tenant{Name: "Protected Tenant", SystemCode: "PROTECTED", SecretKey: "secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&tenant).Error }); err != nil {
		t.Fatal(err)
	}
	root := model.User{Username: "root", Password: hash, Role: "super_admin", TenantID: tenant.ID, IsInitialAdmin: true}
	admin := model.User{Username: "admin", Password: hash, Role: "admin", TenantID: tenant.ID}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&root).Error; err != nil {
			return err
		}
		return tx.Create(&admin).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := ResetTenantUserPassword(tenant.ID, root.ID, admin.ID, "replacement-password"); !errors.Is(err, ErrInitialTenantAdmin) {
		t.Fatalf("initial admin reset error=%v", err)
	}
	if err := ResetTenantUserPassword(tenant.ID, admin.ID, admin.ID, "replacement-password"); !errors.Is(err, ErrOwnTenantAccount) {
		t.Fatalf("self reset error=%v", err)
	}
}

func TestTenantRoleChangeRevokesSessionsAndProtectsRootAndSelf(t *testing.T) {
	resetBusinessData(t)
	tenant := model.Tenant{Name: "Role Tenant", SystemCode: "ROLE-TENANT", SecretKey: "secret", Status: "active"}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&tenant).Error }); err != nil {
		t.Fatal(err)
	}
	root := model.User{Username: "root-role", Role: "super_admin", TenantID: tenant.ID, IsInitialAdmin: true}
	admin := model.User{Username: "admin-role", Role: "admin", TenantID: tenant.ID}
	operator := model.User{Username: "operator-role", Role: "viewer", TenantID: tenant.ID, TokenVersion: 2}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&root).Error }); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&admin).Error }); err != nil {
		t.Fatal(err)
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&operator).Error }); err != nil {
		t.Fatal(err)
	}
	if err := UpdateTenantUserRole(tenant.ID, operator.ID, admin.ID, "team_operator"); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&operator, operator.ID).Error; err != nil {
		t.Fatal(err)
	}
	if operator.Role != "team_operator" || operator.TokenVersion != 3 {
		t.Fatalf("updated operator=%+v", operator)
	}
	if err := UpdateTenantUserRole(tenant.ID, root.ID, admin.ID, "viewer"); !errors.Is(err, ErrInitialTenantAdmin) {
		t.Fatalf("root role error=%v", err)
	}
	if err := UpdateTenantUserRole(tenant.ID, admin.ID, admin.ID, "viewer"); !errors.Is(err, ErrOwnTenantAccount) {
		t.Fatalf("self role error=%v", err)
	}
}
