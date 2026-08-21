package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/gatetunnel"
	"ticket-backend/internal/model"
	"time"

	"golang.org/x/net/websocket"
	"gorm.io/gorm"
)

type maintenanceFixture struct {
	service      *DeviceMaintenanceService
	tenant       model.Tenant
	area         model.ScenicArea
	device       model.Device
	user         model.User
	secret       string
	deviceConn   *websocket.Conn
	deviceServer *httptest.Server
}

func newMaintenanceFixture(t *testing.T) maintenanceFixture {
	t.Helper()
	resetBusinessData(t)
	fixture := maintenanceFixture{}
	if err := model.Write(func(tx *gorm.DB) error {
		fixture.tenant = model.Tenant{Name: "Maintenance Service Tenant", SystemCode: "MNT-SERVICE", SecretKey: "maintenance", Status: "active"}
		if err := tx.Create(&fixture.tenant).Error; err != nil {
			return err
		}
		fixture.area = model.ScenicArea{TenantID: fixture.tenant.ID, Code: "MNT-SERVICE-AREA", Name: "Maintenance Service Area", Status: "active"}
		if err := tx.Create(&fixture.area).Error; err != nil {
			return err
		}
		fixture.user = model.User{TenantID: fixture.tenant.ID, Username: "maintenance-admin", Password: "hash", Role: "admin"}
		if err := tx.Create(&fixture.user).Error; err != nil {
			return err
		}
		fixture.device = model.Device{TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Maintenance Gate", SerialNumber: "MNT-SERVICE-GATE", Type: "gate", Status: "online"}
		return tx.Create(&fixture.device).Error
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewDeviceMaintenanceService(model.DB, config.MaintenanceConfig{Enabled: true, SessionTTLSeconds: 900, MaxSessionTTL: 1800})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service = service
	credential, err := service.RotateCredential(fixture.tenant.ID, fixture.device.ID, fixture.user.ID, "initial test credential")
	if err != nil {
		t.Fatal(err)
	}
	fixture.secret = credential.Secret
	fixture.deviceServer = httptest.NewServer(http.HandlerFunc(service.Gateway.DeviceWebSocketHandler))
	deviceConfig, err := websocket.NewConfig("ws"+fixture.deviceServer.URL[len("http"):], "http://gate.local")
	if err != nil {
		t.Fatal(err)
	}
	deviceConfig.Header.Set("Authorization", "Bearer "+fixture.secret)
	deviceConn, err := websocket.DialConfig(deviceConfig)
	if err != nil {
		fixture.deviceServer.Close()
		t.Fatal(err)
	}
	fixture.deviceConn = deviceConn
	kind, data, err := gatetunnel.ReceiveFrame(deviceConn)
	if err != nil || kind != gatetunnel.FrameControl {
		t.Fatalf("maintenance device handshake kind=%d err=%v", kind, err)
	}
	message, err := gatetunnel.DecodeControl(data)
	if err != nil || message.Type != "ready" {
		t.Fatalf("maintenance device handshake message=%+v err=%v", message, err)
	}
	t.Cleanup(func() {
		service.Gateway.StopAll()
		_ = deviceConn.Close()
		fixture.deviceServer.Close()
	})
	return fixture
}

func connectMaintenanceAdmin(t *testing.T, fixture maintenanceFixture, sessionID, token string) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.service.Gateway.SessionWebSocketHandler(w, r, sessionID)
	}))
	config, err := websocket.NewConfig("ws"+server.URL[len("http"):], "http://admin.local")
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	config.Protocol = []string{"ticket-maintenance-v1." + token}
	admin, err := websocket.DialConfig(config)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return admin, server
}

func waitForMaintenanceStatus(t *testing.T, id uint, status string) model.DeviceMaintenanceSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var session model.DeviceMaintenanceSession
		if err := model.DB.First(&session, id).Error; err == nil && session.Status == status {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	var session model.DeviceMaintenanceSession
	_ = model.DB.First(&session, id).Error
	t.Fatalf("maintenance session id=%d status=%q, want %q", id, session.Status, status)
	return session
}

func TestDeviceMaintenanceServicePersistsActiveAndInterruptedSessions(t *testing.T) {
	fixture := newMaintenanceFixture(t)
	var storedCredential model.DeviceMaintenanceCredential
	if err := model.DB.Where("device_id = ?", fixture.device.ID).First(&storedCredential).Error; err != nil {
		t.Fatal(err)
	}
	if storedCredential.SecretHash == "" || storedCredential.SecretHash == fixture.secret || len(storedCredential.SecretHash) != 64 {
		t.Fatalf("maintenance secret was not stored as a one-way hash: %q", storedCredential.SecretHash)
	}

	result, err := fixture.service.CreateSession(MaintenanceSessionRequest{
		TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID,
		Reason: "验证远程维护链路", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionToken == "" || result.Session.TokenHash != "" {
		t.Fatalf("session secret exposure result=%+v", result)
	}

	go func() {
		kind, data, receiveErr := gatetunnel.ReceiveFrame(fixture.deviceConn)
		if receiveErr != nil || kind != gatetunnel.FrameControl {
			return
		}
		message, decodeErr := gatetunnel.DecodeControl(data)
		if decodeErr == nil && message.Type == "open_ssh" {
			_ = gatetunnel.SendControl(fixture.deviceConn, gatetunnel.ControlMessage{Type: "ssh_ready", SessionID: message.SessionID})
		}
	}()
	admin, adminServer := connectMaintenanceAdmin(t, fixture, result.SessionID, result.SessionToken)
	defer admin.Close()
	defer adminServer.Close()
	kind, data, err := gatetunnel.ReceiveFrame(admin)
	if err != nil || kind != gatetunnel.FrameControl {
		t.Fatalf("admin handshake kind=%d err=%v", kind, err)
	}
	message, err := gatetunnel.DecodeControl(data)
	if err != nil || message.Type != "ready" {
		t.Fatalf("admin handshake message=%+v err=%v", message, err)
	}
	active := waitForMaintenanceStatus(t, result.Session.ID, "active")
	if active.OpenedAt == nil {
		t.Fatal("active maintenance session did not persist opened_at")
	}

	fixture.service.Gateway.DisconnectDevice(fixture.device.ID, "设备网络断开")
	interrupted := waitForMaintenanceStatus(t, result.Session.ID, "interrupted")
	if interrupted.ClosedAt == nil || !strings.Contains(interrupted.ClosedReason, "设备网络断开") {
		t.Fatalf("interrupted maintenance session=%+v", interrupted)
	}
}

func TestDeviceMaintenanceServiceRejectsCrossTenantAndExpiresSessions(t *testing.T) {
	fixture := newMaintenanceFixture(t)
	otherTenant := model.Tenant{Name: "Other Maintenance Tenant", SystemCode: "MNT-OTHER", SecretKey: "other", Status: "active"}
	otherUser := model.User{Username: "other-admin", Password: "hash", Role: "admin"}
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&otherTenant).Error; err != nil {
			return err
		}
		otherUser.TenantID = otherTenant.ID
		return tx.Create(&otherUser).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateSession(MaintenanceSessionRequest{
		TenantID: otherTenant.ID, DeviceID: fixture.device.ID, ActorUserID: otherUser.ID, Reason: "跨租户测试",
	}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant maintenance session error=%v", err)
	}

	result, err := fixture.service.CreateSession(MaintenanceSessionRequest{
		TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID,
		Reason: "验证过期回收", TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ExpireSessions(time.Now().Add(time.Second), 10); err != nil {
		t.Fatal(err)
	}
	expired := waitForMaintenanceStatus(t, result.Session.ID, "expired")
	if expired.ClosedAt == nil || !strings.Contains(expired.ClosedReason, "过期") {
		t.Fatalf("expired maintenance session=%+v", expired)
	}
}

func TestDeviceMaintenanceServiceReconcileStartupInterruptsLeases(t *testing.T) {
	fixture := newMaintenanceFixture(t)
	result, err := fixture.service.CreateSession(MaintenanceSessionRequest{
		TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID,
		Reason: "验证重启恢复", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.ReconcileStartup(time.Now()); err != nil {
		t.Fatal(err)
	}
	interrupted := waitForMaintenanceStatus(t, result.Session.ID, "interrupted")
	if !strings.Contains(interrupted.ClosedReason, "重启") {
		t.Fatalf("startup reconciliation=%+v", interrupted)
	}
}

func TestDeviceMaintenanceServiceCloseAuditUsesClosingOperator(t *testing.T) {
	fixture := newMaintenanceFixture(t)
	closingOperator := model.User{TenantID: fixture.tenant.ID, Username: "maintenance-closer", Password: "hash", Role: "admin"}
	if err := model.DB.Create(&closingOperator).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.CreateSession(MaintenanceSessionRequest{
		TenantID: fixture.tenant.ID, DeviceID: fixture.device.ID, ActorUserID: fixture.user.ID,
		Reason: "验证关闭审计操作者", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.CloseSession(fixture.tenant.ID, closingOperator.ID, fixture.device.ID, result.Session.ID, "另一位管理员主动关闭"); err != nil {
		t.Fatal(err)
	}
	closed := waitForMaintenanceStatus(t, result.Session.ID, "closed")
	var audit model.AuditLog
	if err := model.DB.Where("tenant_id = ? AND action = ? AND target_type = ? AND target_id = ?", fixture.tenant.ID, "device.maintenance.session.close", "device_maintenance_session", closed.ID).Order("id DESC").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorUserID != closingOperator.ID {
		t.Fatalf("close audit actor=%d, want closing operator %d", audit.ActorUserID, closingOperator.ID)
	}
}
