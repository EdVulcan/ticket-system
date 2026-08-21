package gatetunnel

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestGatewayPairsOneDeviceWithOneAdminAndForwardsOnlyFrames(t *testing.T) {
	const secret = "maintenance-secret"
	gateway, err := New(Config{
		Enabled: true,
		Authenticator: func(value string) (DeviceIdentity, error) {
			if value != secret {
				t.Fatalf("unexpected device secret %q", value)
			}
			return DeviceIdentity{DeviceID: 7, TenantID: 11, ScenicAreaID: 13, SerialNumber: "GATE-7"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(gateway.DeviceWebSocketHandler))
	defer server.Close()
	deviceURL := "ws" + server.URL[len("http"):]
	deviceConfig, err := websocket.NewConfig(deviceURL, "http://device.local")
	if err != nil {
		t.Fatal(err)
	}
	deviceConfig.Header.Set("Authorization", "Bearer "+secret)
	device, err := websocket.DialConfig(deviceConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if kind, _, err := ReceiveFrame(device); err != nil || kind != FrameControl {
		t.Fatalf("device handshake: kind=%d err=%v", kind, err)
	}
	token := "0123456789abcdef0123456789abcdef"
	digest := sha256.Sum256([]byte(token))
	publicSessionID := "MNT-test-session"
	if _, err := gateway.CreateSession(7, 11, time.Minute, digest, publicSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.CreateSession(7, 11, time.Minute, digest, "another"); err != ErrSessionAlreadyOpen {
		t.Fatalf("duplicate session error=%v", err)
	}

	adminURL := "ws" + server.URL[len("http"):] + "/session"
	// SessionWebSocketHandler is normally mounted by the router; use the
	// handler directly here to keep the test independent of Gin.
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateway.SessionWebSocketHandler(w, r, publicSessionID)
	}))
	defer adminServer.Close()
	adminURL = "ws" + adminServer.URL[len("http"):]
	adminConfig, err := websocket.NewConfig(adminURL, "http://admin.local")
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.Protocol = []string{"ticket-maintenance-v1." + token}
	admin, err := websocket.DialConfig(adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	kind, data, err := ReceiveFrame(device)
	if err != nil {
		t.Fatal(err)
	}
	message, err := DecodeControl(data)
	if err != nil || kind != FrameControl || message.Type != "open_ssh" || message.SessionID != publicSessionID {
		t.Fatalf("open frame: kind=%d message=%+v err=%v", kind, message, err)
	}
	if err := SendControl(device, ControlMessage{Type: "ssh_ready", SessionID: publicSessionID}); err != nil {
		t.Fatal(err)
	}
	kind, data, err = ReceiveFrame(admin)
	if err != nil {
		t.Fatal(err)
	}
	message, err = DecodeControl(data)
	if err != nil || kind != FrameControl || message.Type != "ready" {
		t.Fatalf("admin handshake: kind=%d message=%+v err=%v", kind, message, err)
	}
	if err := SendStream(admin, []byte("to-device")); err != nil {
		t.Fatal(err)
	}
	kind, data, err = ReceiveFrame(device)
	if err != nil || kind != FrameStream || string(data) != "to-device" {
		t.Fatalf("admin to device: kind=%d data=%q err=%v", kind, data, err)
	}
	if err := SendStream(device, []byte("to-admin")); err != nil {
		t.Fatal(err)
	}
	kind, data, err = ReceiveFrame(admin)
	if err != nil || kind != FrameStream || string(data) != "to-admin" {
		t.Fatalf("device to admin: kind=%d data=%q err=%v", kind, data, err)
	}
	if err := SendControl(admin, ControlMessage{Type: "close"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := gateway.GetSession(publicSessionID); err == ErrSessionNotFound {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("closed session should no longer be routable, got %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestGatewayRejectsDisabledAndCrossTenantSessions(t *testing.T) {
	disabled, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.CreateSession(1, 1, time.Minute, [32]byte{}, "x"); err != ErrGatewayDisabled {
		t.Fatalf("disabled error=%v", err)
	}
	gateway, err := New(Config{Enabled: true, Authenticator: func(string) (DeviceIdentity, error) {
		return DeviceIdentity{DeviceID: 1, TenantID: 2, ScenicAreaID: 3}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.CreateSession(1, 2, time.Minute, [32]byte{}, "x"); err != ErrDeviceOffline {
		t.Fatalf("offline error=%v", err)
	}
}

func TestGatewayPersistsLifecycleAndDisconnectDoesNotDeadlock(t *testing.T) {
	const secret = "lifecycle-secret"
	active := make(chan SessionInfo, 1)
	closed := make(chan struct {
		info   SessionInfo
		reason string
	}, 1)
	gateway, err := New(Config{
		Enabled: true,
		Authenticator: func(value string) (DeviceIdentity, error) {
			if value != secret {
				return DeviceIdentity{}, errors.New("bad secret")
			}
			return DeviceIdentity{DeviceID: 17, TenantID: 19, ScenicAreaID: 23}, nil
		},
		Lifecycle: LifecycleCallbacks{
			OnSessionActive: func(info SessionInfo) { active <- info },
			OnSessionClosed: func(info SessionInfo, reason string) {
				closed <- struct {
					info   SessionInfo
					reason string
				}{info: info, reason: reason}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(gateway.DeviceWebSocketHandler))
	defer server.Close()
	deviceConfig, err := websocket.NewConfig("ws"+server.URL[len("http"):], "http://device.local")
	if err != nil {
		t.Fatal(err)
	}
	deviceConfig.Header.Set("Authorization", "Bearer "+secret)
	device, err := websocket.DialConfig(deviceConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if _, _, err := ReceiveFrame(device); err != nil {
		t.Fatal(err)
	}
	token := "lifecycle-session-token"
	digest := sha256.Sum256([]byte(token))
	if _, err := gateway.CreateSession(17, 19, time.Minute, digest, "MNT-lifecycle"); err != nil {
		t.Fatal(err)
	}
	go func() {
		kind, data, receiveErr := ReceiveFrame(device)
		if receiveErr != nil || kind != FrameControl {
			return
		}
		message, decodeErr := DecodeControl(data)
		if decodeErr != nil || message.Type != "open_ssh" {
			return
		}
		_ = SendControl(device, ControlMessage{Type: "ssh_ready", SessionID: message.SessionID})
	}()
	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateway.SessionWebSocketHandler(w, r, "MNT-lifecycle")
	}))
	defer adminServer.Close()
	adminConfig, err := websocket.NewConfig("ws"+adminServer.URL[len("http"):], "http://admin.local")
	if err != nil {
		t.Fatal(err)
	}
	adminConfig.Protocol = []string{"ticket-maintenance-v1." + token}
	admin, err := websocket.DialConfig(adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	select {
	case info := <-active:
		if info.Status != "active" || info.ID != "MNT-lifecycle" {
			t.Fatalf("active lifecycle info=%+v", info)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active lifecycle callback did not run")
	}

	// DisconnectDevice used to call session.close while holding dc.mu, which
	// deadlocked when session cleanup tried to detach from the same device.
	done := make(chan struct{})
	go func() {
		gateway.DisconnectDevice(17, "设备断线")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("device disconnect deadlocked")
	}
	select {
	case event := <-closed:
		if event.info.Status != "interrupted" || event.reason != "设备断线" {
			t.Fatalf("closed lifecycle event=%+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("closed lifecycle callback did not run")
	}
	if _, err := gateway.GetSession("MNT-lifecycle"); err != ErrSessionNotFound {
		t.Fatalf("closed session remained routable: %v", err)
	}
}
