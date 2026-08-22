package service

import (
	"errors"
	"sync"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/deviceprovision"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type provisioningFixture struct {
	service *DeviceProvisioningService
	tenant  model.Tenant
	area    model.ScenicArea
	user    model.User
	device  model.Device
}

func newProvisioningFixture(t *testing.T) provisioningFixture {
	t.Helper()
	resetBusinessData(t)
	oldURL := config.GlobalConfig.Server.PublicBaseURL
	oldMaintenance := config.GlobalConfig.Maintenance
	config.GlobalConfig.Server.PublicBaseURL = "https://tickets.example.test"
	config.GlobalConfig.Maintenance = config.MaintenanceConfig{Enabled: true, Path: "/api/v1/hardware/maintenance/ws"}
	t.Cleanup(func() {
		config.GlobalConfig.Server.PublicBaseURL = oldURL
		config.GlobalConfig.Maintenance = oldMaintenance
	})
	fixture := provisioningFixture{}
	if err := model.Write(func(tx *gorm.DB) error {
		fixture.tenant = model.Tenant{Name: "Provisioning Tenant", SystemCode: "PROVISION-SYS", SecretKey: "provision", Status: "active"}
		if err := tx.Create(&fixture.tenant).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: fixture.tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.SupplierBusinessType{TenantID: fixture.tenant.ID, BusinessType: "scenic", Status: "active"}).Error; err != nil {
			return err
		}
		fixture.area = model.ScenicArea{TenantID: fixture.tenant.ID, Code: "PROVISION-AREA", Name: "Provisioning Area", Status: "active"}
		if err := tx.Create(&fixture.area).Error; err != nil {
			return err
		}
		fixture.user = model.User{TenantID: fixture.tenant.ID, Username: "provision-admin", Password: "hash", Role: "admin"}
		if err := tx.Create(&fixture.user).Error; err != nil {
			return err
		}
		fixture.device = model.Device{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Provision Gate", SerialNumber: "PROVISION-GATE", Type: "gate", Status: "offline"}
		return tx.Create(&fixture.device).Error
	}); err != nil {
		t.Fatal(err)
	}
	fixture.service = NewDeviceProvisioningService(model.DB, nil)
	return fixture
}

func TestDeviceProvisioningClaimRetryAndConfirm(t *testing.T) {
	fixture := newProvisioningFixture(t)
	lease, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "首次部署"})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, publicKey, err := deviceprovision.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	claim := ProvisioningClaimRequest{Token: lease.ActivationCode, InstallationID: "install-1", PublicKey: deviceprovision.EncodePublicKey(publicKey)}
	first, err := fixture.service.Claim(claim)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "claimed" || first.Envelope == "" {
		t.Fatalf("claim result=%+v", first)
	}
	bundle, err := deviceprovision.DecryptBundle(first.Envelope, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ServerURL != "https://tickets.example.test" || bundle.SystemCode != fixture.tenant.SystemCode || bundle.SerialNumber != fixture.device.SerialNumber || bundle.DeviceKey == "" || bundle.MaintenanceSecret == "" {
		t.Fatalf("unexpected provisioning bundle=%+v", bundle)
	}
	retry, err := fixture.service.Claim(claim)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Envelope != first.Envelope {
		t.Fatal("same installer retry generated a different envelope")
	}
	if _, err := fixture.service.Claim(ProvisioningClaimRequest{Token: lease.ActivationCode, InstallationID: "other-install", PublicKey: deviceprovision.EncodePublicKey(publicKey)}); !errors.Is(err, ErrProvisioningLeaseInvalid) {
		t.Fatalf("different installer claim error=%v", err)
	}
	if err := fixture.service.Confirm(fixture.tenant.ID, fixture.device.ID, ProvisioningConfirmRequest{InstallationID: "install-1", Fingerprint: deviceprovision.Fingerprint(publicKey)}); err != nil {
		t.Fatal(err)
	}
	var stored model.DeviceProvisioningLease
	if err := model.DB.First(&stored, lease.Lease.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "completed" || stored.EncryptedBundle != "" || stored.InstallerPublicKey != "" {
		t.Fatalf("completed lease retained secret material=%+v", stored)
	}
	if err := fixture.service.Confirm(fixture.tenant.ID, fixture.device.ID, ProvisioningConfirmRequest{InstallationID: "install-1", Fingerprint: deviceprovision.Fingerprint(publicKey)}); err != nil {
		t.Fatalf("idempotent confirm failed: %v", err)
	}
}

func TestDeviceProvisioningFailsClosedAndSerializesClaims(t *testing.T) {
	fixture := newProvisioningFixture(t)
	lease, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "并发安装"})
	if err != nil {
		t.Fatal(err)
	}
	_, keyA, err := deviceprovision.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_, keyB, err := deviceprovision.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	requests := []ProvisioningClaimRequest{
		{Token: lease.ActivationCode, InstallationID: "parallel-a", PublicKey: deviceprovision.EncodePublicKey(keyA)},
		{Token: lease.ActivationCode, InstallationID: "parallel-b", PublicKey: deviceprovision.EncodePublicKey(keyB)},
	}
	var wg sync.WaitGroup
	results := make([]error, len(requests))
	for i := range requests {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, results[index] = fixture.service.Claim(requests[index])
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, resultErr := range results {
		if resultErr == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent claims succeeded=%d errors=%v", successes, results)
	}
	// A lease may not be claimed after the old client has come online.
	var device model.Device
	if err := model.DB.First(&device, fixture.device.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&device).Update("status", "online").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "在线设备"}); !errors.Is(err, ErrProvisioningDeviceOnline) {
		t.Fatalf("online lease error=%v", err)
	}
}

func TestDeviceProvisioningRequiresHTTPSPublicURL(t *testing.T) {
	fixture := newProvisioningFixture(t)
	config.GlobalConfig.Server.PublicBaseURL = ""
	if _, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "缺少公网地址"}); !errors.Is(err, ErrProvisioningNotReady) {
		t.Fatalf("missing public URL error=%v", err)
	}
}

func TestDeviceProvisioningCreateLeaseReclaimsExpiredLease(t *testing.T) {
	fixture := newProvisioningFixture(t)
	first, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "过期绑定"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.DeviceProvisioningLease{}).Where("id = ?", first.Lease.ID).Updates(map[string]interface{}{
		"expires_at": time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "重新绑定"})
	if err != nil {
		t.Fatalf("expired lease blocked replacement: %v", err)
	}
	if second.Lease.ID == first.Lease.ID {
		t.Fatal("replacement reused expired lease")
	}
	var expired model.DeviceProvisioningLease
	if err := model.DB.First(&expired, first.Lease.ID).Error; err != nil {
		t.Fatal(err)
	}
	if expired.Status != "expired" || expired.EncryptedBundle != "" || expired.InstallerPublicKey != "" {
		t.Fatalf("expired lease was not cleaned: %+v", expired)
	}
}

func TestDeviceProvisioningRevokeClearsClaimedEnvelope(t *testing.T) {
	fixture := newProvisioningFixture(t)
	lease, err := fixture.service.CreateLease(ProvisioningLeaseRequest{TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID, Reason: "撤销错误绑定"})
	if err != nil {
		t.Fatal(err)
	}
	_, publicKey, err := deviceprovision.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Claim(ProvisioningClaimRequest{Token: lease.ActivationCode, InstallationID: "revoke-install", PublicKey: deviceprovision.EncodePublicKey(publicKey)}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RevokeLease(fixture.tenant.ID, fixture.device.ID, lease.Lease.ID, fixture.user.ID, "绑定码误发"); err != nil {
		t.Fatal(err)
	}
	var stored model.DeviceProvisioningLease
	if err := model.DB.First(&stored, lease.Lease.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "revoked" || stored.EncryptedBundle != "" {
		t.Fatalf("revoked lease=%+v", stored)
	}
	if _, err := fixture.service.Claim(ProvisioningClaimRequest{Token: lease.ActivationCode, InstallationID: "revoke-install", PublicKey: deviceprovision.EncodePublicKey(publicKey)}); !errors.Is(err, ErrProvisioningLeaseInvalid) {
		t.Fatalf("revoked claim error=%v", err)
	}
}
