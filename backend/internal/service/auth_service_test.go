package service

import (
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestTenantLoginRequiresActiveTenant(t *testing.T) {
	hash, err := HashPassword("tenant-login-password")
	if err != nil {
		t.Fatal(err)
	}
	previousSecret := config.GlobalConfig.Security.JWTSecret
	config.GlobalConfig.Security.JWTSecret = "auth-service-test-secret"
	t.Cleanup(func() { config.GlobalConfig.Security.JWTSecret = previousSecret })

	cases := []struct {
		name        string
		status      string
		wantAllowed bool
	}{
		{name: "active", status: "active", wantAllowed: true},
		{name: "empty status", status: "", wantAllowed: false},
		{name: "frozen", status: "frozen", wantAllowed: false},
		{name: "closed", status: "closed", wantAllowed: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetBusinessData(t)
			tenant := model.Tenant{
				Name:       "Auth Test Tenant",
				SystemCode: "AUTH-" + tc.name,
				SecretKey:  "auth-secret",
				Status:     "active",
			}
			if err := model.Write(func(tx *gorm.DB) error {
				if err := tx.Create(&tenant).Error; err != nil {
					return err
				}
				if tc.status == "" {
					return tx.Model(&tenant).Update("status", "").Error
				}
				return tx.Model(&tenant).Update("status", tc.status).Error
			}); err != nil {
				t.Fatal(err)
			}

			user := model.User{
				Username: "tenant-admin",
				Password: hash,
				Role:     "admin",
				TenantID: tenant.ID,
			}
			staff := model.Staff{
				Name:      "Auth Staff",
				JobNumber: "AUTH-STAFF",
				Password:  hash,
				Roles:     "checker",
				Status:    "active",
				TenantID:  tenant.ID,
			}
			if err := model.Write(func(tx *gorm.DB) error {
				if err := tx.Create(&user).Error; err != nil {
					return err
				}
				return tx.Create(&staff).Error
			}); err != nil {
				t.Fatal(err)
			}

			auth := &AuthService{}
			tenantToken, gotUser, loginErr := auth.Login(tenant.SystemCode, user.Username, "tenant-login-password")
			staffToken, gotStaff, staffErr := auth.StaffLogin(tenant.SystemCode, staff.JobNumber, "tenant-login-password")
			if tc.wantAllowed {
				if loginErr != nil || tenantToken == "" || gotUser == nil || gotUser.ID != user.ID {
					t.Fatalf("tenant login failed: token=%q user=%+v err=%v", tenantToken, gotUser, loginErr)
				}
				if staffErr != nil || staffToken == "" || gotStaff == nil || gotStaff.ID != staff.ID {
					t.Fatalf("staff login failed: token=%q staff=%+v err=%v", staffToken, gotStaff, staffErr)
				}
				return
			}

			if loginErr == nil || tenantToken != "" || gotUser != nil || loginErr.Error() != "租户已被停用" {
				t.Fatalf("inactive tenant login was not rejected: token=%q user=%+v err=%v", tenantToken, gotUser, loginErr)
			}
			if staffErr == nil || staffToken != "" || gotStaff != nil || staffErr.Error() != "租户已被停用" {
				t.Fatalf("inactive tenant staff login was not rejected: token=%q staff=%+v err=%v", staffToken, gotStaff, staffErr)
			}
		})
	}
}
