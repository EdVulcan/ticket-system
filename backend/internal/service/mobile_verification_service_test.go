package service

import (
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestMobileVerificationSessionScopesTargetsAndReplaysWithoutGateTask(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	checkpointID := checkpoint.ID
	handheld := model.Device{Name: "手机终端", SerialNumber: fmt.Sprintf("HANDHELD-%d", time.Now().UnixNano()), Type: "handheld", Status: "offline", TenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID, CheckPointID: &checkpointID}
	if err := model.DB.Create(&handheld).Error; err != nil {
		t.Fatal(err)
	}
	staff := model.Staff{Name: "验票员", JobNumber: fmt.Sprintf("CHECKER-%d", time.Now().UnixNano()), Roles: "checker", Status: "active", TenantID: tenantID, TokenVersion: 1}
	if err := model.DB.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.StaffResourceScope{TenantID: tenantID, StaffID: staff.ID, ResourceType: "checkpoint", ResourceID: checkpoint.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.StaffResourceScope{TenantID: tenantID, StaffID: staff.ID, ResourceType: "device", ResourceID: handheld.ID}).Error; err != nil {
		t.Fatal(err)
	}
	order := model.Order{TenantID: tenantID, Channel: "window", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	if err := model.DB.Where("order_id = ?", order.ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}

	deviceService := NewDeviceService(model.DB, &TicketService{})
	service := NewMobileVerificationService(model.DB, deviceService)
	targets, err := service.Targets(tenantID, staff.ID, staff.Roles)
	if err != nil || len(targets.Checkpoints) != 1 || len(targets.Devices) != 1 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	session, err := service.CreateSession(tenantID, staff.ID, staff.Roles, checkpoint.ID, handheld.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Verify(tenantID, staff.ID, session.SessionToken, ticket.TicketCode, "mobile-scan-1")
	if err != nil || first.Result != "allow" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.Verify(tenantID, staff.ID, session.SessionToken, ticket.TicketCode, "mobile-scan-1")
	if err != nil || second.Result != "allow" {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	var verification model.DeviceVerification
	if err := model.DB.Where("device_id = ? AND request_id = ?", handheld.ID, "mobile-scan-1").First(&verification).Error; err != nil {
		t.Fatal(err)
	}
	if verification.OpenStatus != "not_required" {
		t.Fatalf("mobile verification open status=%q, want not_required", verification.OpenStatus)
	}
	var successful int64
	if err := model.DB.Model(&model.CheckInRecord{}).Where("ticket_id = ? AND result = ?", ticket.ID, "success").Count(&successful).Error; err != nil || successful != 1 {
		t.Fatalf("successful check-ins=%d err=%v", successful, err)
	}
	if err := service.Close(tenantID, staff.ID, session.SessionToken); err != nil {
		t.Fatal(err)
	}
	var storedDevice model.Device
	if err := model.DB.First(&storedDevice, handheld.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedDevice.Status != "offline" {
		t.Fatalf("device status=%q after close, want offline", storedDevice.Status)
	}
}

func TestMobileVerificationRejectsUnscopedDevice(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	checkpointID := checkpoint.ID
	device := model.Device{Name: "未授权手机", SerialNumber: fmt.Sprintf("HANDHELD-%d", time.Now().UnixNano()), Type: "handheld", Status: "offline", TenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID, CheckPointID: &checkpointID}
	if err := model.DB.Create(&device).Error; err != nil {
		t.Fatal(err)
	}
	staff := model.Staff{Name: "验票员", JobNumber: fmt.Sprintf("CHECKER-%d", time.Now().UnixNano()), Roles: "checker", Status: "active", TenantID: tenantID, TokenVersion: 1}
	if err := model.DB.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMobileVerificationService(model.DB, NewDeviceService(model.DB, &TicketService{}))
	if _, err := service.CreateSession(tenantID, staff.ID, staff.Roles, checkpoint.ID, device.ID); err == nil || !errors.Is(err, ErrMobileTargetDenied) {
		t.Fatalf("unscoped session err=%v, want ErrMobileTargetDenied", err)
	}
}

func TestMobileSessionCannotBeUsedAfterRevocationOrExpiry(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	var checkpoint model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	checkpointID := checkpoint.ID
	device := model.Device{Name: "会话生命周期终端", SerialNumber: fmt.Sprintf("HANDHELD-%d", time.Now().UnixNano()), Type: "handheld", Status: "offline", TenantID: tenantID, ScenicAreaID: checkpoint.ScenicAreaID, CheckPointID: &checkpointID}
	if err := model.DB.Create(&device).Error; err != nil {
		t.Fatal(err)
	}
	staff := model.Staff{Name: "验票员", JobNumber: fmt.Sprintf("CHECKER-%d", time.Now().UnixNano()), Roles: "checker", Status: "active", TenantID: tenantID, TokenVersion: 1}
	if err := model.DB.Create(&staff).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.StaffResourceScope{TenantID: tenantID, StaffID: staff.ID, ResourceType: "checkpoint", ResourceID: checkpoint.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.StaffResourceScope{TenantID: tenantID, StaffID: staff.ID, ResourceType: "device", ResourceID: device.ID}).Error; err != nil {
		t.Fatal(err)
	}

	service := NewMobileVerificationService(model.DB, NewDeviceService(model.DB, &TicketService{}))
	session, err := service.CreateSession(tenantID, staff.ID, staff.Roles, checkpoint.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored model.MobileVerificationSession
	if err := model.DB.Where("tenant_id = ? AND staff_id = ?", tenantID, staff.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.MobileVerificationSession{}).Where("id = ?", stored.ID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	var revoked model.MobileVerificationSession
	if err := model.DB.First(&revoked, stored.ID).Error; err != nil {
		t.Fatal(err)
	}
	if revoked.Status != "revoked" {
		t.Fatalf("session status=%q, want revoked", revoked.Status)
	}
	if err := service.Heartbeat(tenantID, staff.ID, session.SessionToken); !errors.Is(err, ErrMobileSessionInvalid) {
		t.Fatalf("revoked heartbeat err=%v, want ErrMobileSessionInvalid", err)
	}

	// A new session can be expired server-side; expiry must also release the
	// synthetic online state when no other mobile session remains.
	second, err := service.CreateSession(tenantID, staff.ID, staff.Roles, checkpoint.ID, device.ID)
	if err != nil {
		t.Fatal(err)
	}
	var active model.MobileVerificationSession
	if err := model.DB.Where("tenant_id = ? AND staff_id = ? AND status = ?", tenantID, staff.ID, "active").First(&active).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&active).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Heartbeat(tenantID, staff.ID, second.SessionToken); !errors.Is(err, ErrMobileSessionInvalid) {
		t.Fatalf("expired heartbeat err=%v, want ErrMobileSessionInvalid", err)
	}
	var storedDevice model.Device
	if err := model.DB.First(&storedDevice, device.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedDevice.Status != "offline" {
		t.Fatalf("device status=%q after expired session, want offline", storedDevice.Status)
	}
}
