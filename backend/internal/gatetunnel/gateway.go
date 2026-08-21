package gatetunnel

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

var (
	ErrGatewayDisabled    = errors.New("device maintenance gateway is disabled")
	ErrDeviceOffline      = errors.New("device maintenance tunnel is offline")
	ErrSessionNotFound    = errors.New("maintenance session not found")
	ErrSessionExpired     = errors.New("maintenance session expired")
	ErrSessionAlreadyOpen = errors.New("maintenance session already open")
)

// DeviceIdentity is the server-authoritative identity associated with a
// maintenance credential. It is deliberately independent from ticket HMAC
// request context and is never accepted from a client frame.
type DeviceIdentity struct {
	DeviceID     uint
	TenantID     uint
	ScenicAreaID uint
	SerialNumber string
}

type Authenticator func(secret string) (DeviceIdentity, error)

type Config struct {
	Enabled       bool
	Authenticator Authenticator
	MaxFrameBytes int
	Lifecycle     LifecycleCallbacks
}

// LifecycleCallbacks lets the control plane persist data-plane lease
// transitions. Callbacks run after the gateway has released its internal
// locks; they must not be used to authorize arbitrary destinations.
type LifecycleCallbacks struct {
	OnSessionActive func(SessionInfo)
	OnSessionClosed func(SessionInfo, string)
}

type SessionInfo struct {
	ID       string
	DeviceID uint
	TenantID uint
	// ActorUserID is the tenant operator whose action created or explicitly
	// closed the lease. It is callback metadata only; it is never accepted
	// from a device frame or exposed as a routing/authorization input.
	ActorUserID uint
	ExpiresAt   time.Time
	Status      string
	Connected   bool
}

type Gateway struct {
	config Config

	mu             sync.RWMutex
	devices        map[uint]*deviceConnection
	sessions       map[string]*maintenanceSession
	activeByDevice map[uint]*maintenanceSession
}

type deviceConnection struct {
	gateway  *Gateway
	identity DeviceIdentity
	ws       *websocket.Conn

	mu      sync.Mutex
	writeMu sync.Mutex
	session *maintenanceSession
	closed  chan struct{}
}

type maintenanceSession struct {
	gateway   *Gateway
	info      SessionInfo
	tokenHash [32]byte

	mu           sync.Mutex
	adminWriteMu sync.Mutex
	device       *deviceConnection
	admin        *websocket.Conn
	ready        chan error
	closed       chan struct{}
	closeOnce    sync.Once
}

func (dc *deviceConnection) sendControl(message controlMessage) error {
	dc.writeMu.Lock()
	defer dc.writeMu.Unlock()
	return sendControl(dc.ws, message)
}

func (dc *deviceConnection) sendStream(data []byte) error {
	dc.writeMu.Lock()
	defer dc.writeMu.Unlock()
	return sendStream(dc.ws, data)
}

func (s *maintenanceSession) sendAdminControl(message controlMessage) error {
	s.mu.Lock()
	admin := s.admin
	s.mu.Unlock()
	if admin == nil {
		return ErrSessionNotFound
	}
	s.adminWriteMu.Lock()
	defer s.adminWriteMu.Unlock()
	return sendControl(admin, message)
}

func (s *maintenanceSession) sendAdminStream(data []byte) error {
	s.mu.Lock()
	admin := s.admin
	s.mu.Unlock()
	if admin == nil {
		return ErrSessionNotFound
	}
	s.adminWriteMu.Lock()
	defer s.adminWriteMu.Unlock()
	return sendStream(admin, data)
}

func New(config Config) (*Gateway, error) {
	if !config.Enabled {
		return &Gateway{config: config, devices: make(map[uint]*deviceConnection), sessions: make(map[string]*maintenanceSession), activeByDevice: make(map[uint]*maintenanceSession)}, nil
	}
	if config.Authenticator == nil {
		return nil, errors.New("maintenance gateway authenticator is required")
	}
	if config.MaxFrameBytes <= 0 || config.MaxFrameBytes > 32<<20 {
		config.MaxFrameBytes = 4 << 20
	}
	return &Gateway{config: config, devices: make(map[uint]*deviceConnection), sessions: make(map[string]*maintenanceSession), activeByDevice: make(map[uint]*maintenanceSession)}, nil
}

func (g *Gateway) Enabled() bool { return g != nil && g.config.Enabled }

// DeviceWebSocketHandler is mounted on the HTTPS API server. The device
// authenticates with an independent maintenance credential in the bearer
// header; no ticketing DeviceKey is accepted here.
func (g *Gateway) DeviceWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	if g == nil || !g.Enabled() {
		http.Error(w, ErrGatewayDisabled.Error(), http.StatusNotImplemented)
		return
	}
	server := websocket.Server{
		Config: websocket.Config{Origin: nil},
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			// Device connections are non-browser clients. Do not use the
			// package's browser Origin check; the bearer credential is checked
			// before the connection is registered.
			if req.Method != http.MethodGet {
				return errors.New("maintenance websocket requires GET")
			}
			if len(cfg.Protocol) > 0 {
				return errors.New("unexpected device maintenance subprotocol")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			ws.MaxPayloadBytes = g.config.MaxFrameBytes
			secret := bearerSecret(ws.Request())
			identity, err := g.config.Authenticator(secret)
			if err != nil {
				_ = sendControl(ws, controlMessage{Type: "error", Error: "维护凭据无效"})
				_ = ws.Close()
				return
			}
			g.handleDevice(ws, identity)
		},
	}
	server.ServeHTTP(w, r)
}

func bearerSecret(req *http.Request) string {
	if req == nil {
		return ""
	}
	value := strings.TrimSpace(req.Header.Get("Authorization"))
	if len(value) >= len("Bearer ") && strings.EqualFold(value[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(value[len("Bearer "):])
	}
	return ""
}

func (g *Gateway) handleDevice(ws *websocket.Conn, identity DeviceIdentity) {
	dc := &deviceConnection{gateway: g, identity: identity, ws: ws, closed: make(chan struct{})}
	g.registerDevice(dc)
	defer func() {
		g.unregisterDevice(dc)
		_ = ws.Close()
	}()
	if err := dc.sendControl(controlMessage{Type: "ready"}); err != nil {
		return
	}
	for {
		kind, data, err := receiveFrame(ws)
		if err != nil {
			return
		}
		if kind == frameStream {
			dc.forwardToAdmin(data)
			continue
		}
		message, err := decodeControl(data)
		if err != nil {
			_ = dc.sendControl(controlMessage{Type: "error", Error: "维护控制帧格式错误"})
			continue
		}
		dc.handleControl(message)
	}
}

func (g *Gateway) registerDevice(dc *deviceConnection) {
	g.mu.Lock()
	previous := g.devices[dc.identity.DeviceID]
	g.devices[dc.identity.DeviceID] = dc
	g.mu.Unlock()
	if previous != nil && previous != dc {
		previous.close("device maintenance connection replaced")
	}
}

func (g *Gateway) unregisterDevice(dc *deviceConnection) {
	g.mu.Lock()
	if current := g.devices[dc.identity.DeviceID]; current == dc {
		delete(g.devices, dc.identity.DeviceID)
	}
	g.mu.Unlock()
	dc.close("device maintenance connection closed")
}

func (dc *deviceConnection) close(reason string) {
	dc.mu.Lock()
	session := dc.session
	dc.session = nil
	select {
	case <-dc.closed:
	default:
		close(dc.closed)
	}
	dc.mu.Unlock()
	// Do not call session.close while holding dc.mu. Session cleanup detaches
	// itself from the device, so the opposite lock order would deadlock when a
	// device disconnect races with an administrator close.
	if session != nil {
		session.closeWithStatus(reason, "interrupted")
	}
}

func (dc *deviceConnection) handleControl(message controlMessage) {
	dc.mu.Lock()
	session := dc.session
	dc.mu.Unlock()
	if session == nil {
		return
	}
	switch message.Type {
	case "ssh_ready":
		select {
		case session.ready <- nil:
		default:
		}
	case "ssh_error":
		select {
		case session.ready <- errors.New(message.Error):
		default:
		}
	case "ssh_closed":
		session.closeWithStatus(message.Error, "interrupted")
	case "heartbeat":
		_ = dc.sendControl(controlMessage{Type: "heartbeat_ack", SessionID: session.info.ID})
	}
}

func (dc *deviceConnection) forwardToAdmin(data []byte) {
	dc.mu.Lock()
	session := dc.session
	dc.mu.Unlock()
	if session == nil {
		return
	}
	session.mu.Lock()
	admin := session.admin
	session.mu.Unlock()
	if admin == nil {
		return
	}
	if err := session.sendAdminStream(data); err != nil {
		session.closeWithStatus("向维护端转发失败", "interrupted")
	}
}

// CreateSession reserves one short-lived maintenance lease. The caller must
// have already enforced tenant permissions and recorded the business audit;
// the gateway only handles the in-memory data plane.
func (g *Gateway) CreateSession(deviceID, tenantID uint, ttl time.Duration, tokenHash [32]byte, sessionID string, actorUserID ...uint) (SessionInfo, error) {
	if g == nil || !g.Enabled() {
		return SessionInfo{}, ErrGatewayDisabled
	}
	if deviceID == 0 || tenantID == 0 || strings.TrimSpace(sessionID) == "" {
		return SessionInfo{}, errors.New("maintenance session identity is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if ttl > 30*time.Minute {
		ttl = 30 * time.Minute
	}
	g.mu.Lock()
	dc := g.devices[deviceID]
	if dc == nil {
		g.mu.Unlock()
		return SessionInfo{}, ErrDeviceOffline
	}
	if dc.identity.TenantID != tenantID {
		g.mu.Unlock()
		return SessionInfo{}, errors.New("maintenance device tenant mismatch")
	}
	if existing := g.activeByDevice[deviceID]; existing != nil {
		g.mu.Unlock()
		return SessionInfo{}, ErrSessionAlreadyOpen
	}
	var actorID uint
	if len(actorUserID) > 0 {
		actorID = actorUserID[0]
	}
	info := SessionInfo{ID: sessionID, DeviceID: deviceID, TenantID: tenantID, ActorUserID: actorID, ExpiresAt: time.Now().Add(ttl), Status: "pending", Connected: true}
	session := &maintenanceSession{gateway: g, info: info, tokenHash: tokenHash, device: dc, ready: make(chan error, 1), closed: make(chan struct{})}
	g.sessions[sessionID] = session
	g.activeByDevice[deviceID] = session
	g.mu.Unlock()
	dc.mu.Lock()
	dc.session = session
	dc.mu.Unlock()
	return info, nil
}

func (g *Gateway) OpenSession(sessionID string) error {
	g.mu.RLock()
	session := g.sessions[sessionID]
	g.mu.RUnlock()
	if session == nil {
		return ErrSessionNotFound
	}
	session.mu.Lock()
	expiresAt := session.info.ExpiresAt
	status := session.info.Status
	session.mu.Unlock()
	if status == "closed" || status == "expired" || time.Now().After(expiresAt) {
		session.closeWithStatus("维护会话已过期", "expired")
		return ErrSessionExpired
	}
	dc := session.device
	if dc == nil {
		return ErrDeviceOffline
	}
	if err := dc.sendControl(controlMessage{Type: "open_ssh", SessionID: sessionID}); err != nil {
		session.closeWithStatus("通知设备建立 SSH 失败", "interrupted")
		return err
	}
	select {
	case err := <-session.ready:
		if err != nil {
			session.closeWithStatus("设备无法建立本机 SSH: "+err.Error(), "interrupted")
			return err
		}
		var info SessionInfo
		session.mu.Lock()
		if session.info.Status != "pending" || time.Now().After(session.info.ExpiresAt) {
			session.mu.Unlock()
			return errors.New("maintenance session closed")
		}
		session.info.Status = "active"
		session.info.Connected = true
		info = session.info
		session.mu.Unlock()
		if callback := session.gateway.config.Lifecycle.OnSessionActive; callback != nil {
			callback(info)
		}
		return nil
	case <-time.After(15 * time.Second):
		session.closeWithStatus("设备建立 SSH 超时", "interrupted")
		return errors.New("device SSH bridge timeout")
	case <-session.closed:
		return errors.New("maintenance session closed")
	}
}

func (g *Gateway) SessionWebSocketHandler(w http.ResponseWriter, r *http.Request, sessionID string) {
	if g == nil || !g.Enabled() {
		http.Error(w, ErrGatewayDisabled.Error(), http.StatusNotImplemented)
		return
	}
	server := websocket.Server{
		Config: websocket.Config{Origin: nil},
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			if req.Method != http.MethodGet {
				return errors.New("maintenance websocket requires GET")
			}
			if len(cfg.Protocol) != 1 || !strings.HasPrefix(cfg.Protocol[0], "ticket-maintenance-v1.") {
				return errors.New("maintenance session subprotocol is required")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			ws.MaxPayloadBytes = g.config.MaxFrameBytes
			token := ""
			if ws.Config() != nil {
				token = sessionTokenFromProtocols(ws.Config().Protocol)
			}
			if token == "" {
				token = sessionToken(ws.Request())
			}
			g.handleAdmin(ws, sessionID, token)
		},
	}
	server.ServeHTTP(w, r)
}

func sessionToken(req *http.Request) string {
	if req == nil {
		return ""
	}
	if value := strings.TrimSpace(req.Header.Get("X-Maintenance-Session-Token")); value != "" {
		return value
	}
	// Browser WebSocket clients cannot set arbitrary headers. The
	// subprotocol still stays out of URLs and is visible only to the TLS peer.
	return sessionTokenFromProtocols(req.Header.Values("Sec-WebSocket-Protocol"))
}

func sessionTokenFromProtocols(values []string) string {
	for _, value := range values {
		for _, protocol := range strings.Split(value, ",") {
			protocol = strings.TrimSpace(protocol)
			if strings.HasPrefix(protocol, "ticket-maintenance-v1.") {
				return strings.TrimPrefix(protocol, "ticket-maintenance-v1.")
			}
		}
	}
	return ""
}

func (g *Gateway) handleAdmin(ws *websocket.Conn, sessionID, token string) {
	g.mu.RLock()
	session := g.sessions[sessionID]
	g.mu.RUnlock()
	if session == nil || !secureTokenMatch(session.tokenHash, token) {
		_ = sendControl(ws, controlMessage{Type: "error", Error: "维护会话令牌无效"})
		_ = ws.Close()
		return
	}
	session.mu.Lock()
	expiresAt := session.info.ExpiresAt
	session.mu.Unlock()
	if time.Now().After(expiresAt) {
		session.closeWithStatus("维护会话已过期", "expired")
		_ = sendControl(ws, controlMessage{Type: "error", Error: "维护会话已过期"})
		_ = ws.Close()
		return
	}
	session.mu.Lock()
	if session.admin != nil {
		session.mu.Unlock()
		_ = sendControl(ws, controlMessage{Type: "error", Error: "维护会话已被占用"})
		_ = ws.Close()
		return
	}
	session.admin = ws
	session.mu.Unlock()
	explicitClose := false
	defer func() {
		session.mu.Lock()
		if session.admin == ws {
			session.admin = nil
		}
		session.mu.Unlock()
		if explicitClose {
			session.closeWithStatus("维护端主动关闭", "closed")
		} else {
			session.closeWithStatus("维护端连接中断", "interrupted")
		}
		_ = ws.Close()
	}()
	if err := g.OpenSession(sessionID); err != nil {
		_ = sendControl(ws, controlMessage{Type: "error", Error: err.Error()})
		return
	}
	if err := session.sendAdminControl(controlMessage{Type: "ready", SessionID: sessionID}); err != nil {
		return
	}
	// The admin side is the only reader for this WebSocket. Device data is
	// written by forwardToAdmin; admin bytes are written to the device here.
	for {
		kind, data, err := receiveFrame(ws)
		if err != nil {
			return
		}
		if kind == frameStream {
			dc := session.device
			if dc == nil || dc.sendStream(data) != nil {
				return
			}
			continue
		}
		message, err := decodeControl(data)
		if err != nil {
			return
		}
		if message.Type == "close" {
			explicitClose = true
			return
		}
	}
}

func secureTokenMatch(expected [32]byte, token string) bool {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	if strings.TrimSpace(token) == "" {
		return false
	}
	return subtle.ConstantTimeCompare(expected[:], digest[:]) == 1
}

func (s *maintenanceSession) close(reason string) {
	s.closeWithStatus(reason, "closed")
}

func (s *maintenanceSession) closeWithStatus(reason, status string, actorUserID ...uint) {
	status = strings.TrimSpace(status)
	if status != "closed" && status != "expired" && status != "interrupted" {
		status = "closed"
	}
	s.closeOnce.Do(func() {
		var device *deviceConnection
		var info SessionInfo
		if s.gateway != nil {
			s.gateway.mu.Lock()
			s.mu.Lock()
			if len(actorUserID) > 0 && actorUserID[0] != 0 {
				s.info.ActorUserID = actorUserID[0]
			}
			s.info.Status = status
			s.info.Connected = false
			s.info.ExpiresAt = time.Now()
			device = s.device
			info = s.info
			close(s.closed)
			if current := s.gateway.sessions[s.info.ID]; current == s {
				delete(s.gateway.sessions, s.info.ID)
			}
			if current := s.gateway.activeByDevice[s.info.DeviceID]; current == s {
				delete(s.gateway.activeByDevice, s.info.DeviceID)
			}
			s.mu.Unlock()
			s.gateway.mu.Unlock()
		} else {
			s.mu.Lock()
			if len(actorUserID) > 0 && actorUserID[0] != 0 {
				s.info.ActorUserID = actorUserID[0]
			}
			s.info.Status = status
			s.info.Connected = false
			s.info.ExpiresAt = time.Now()
			device = s.device
			info = s.info
			close(s.closed)
			s.mu.Unlock()
		}
		if device != nil {
			device.mu.Lock()
			if device.session == s {
				device.session = nil
			}
			device.mu.Unlock()
			_ = device.sendControl(controlMessage{Type: "close", SessionID: info.ID, Error: reason})
		}
		if s.gateway != nil {
			if callback := s.gateway.config.Lifecycle.OnSessionClosed; callback != nil {
				callback(info, reason)
			}
		}
	})
}

func (g *Gateway) CloseSession(sessionID, reason string) error {
	return g.CloseSessionWithStatus(sessionID, reason, "closed")
}

// CloseSessionWithStatus is used by the control plane when a lease is
// interrupted or expires. Ordinary administrator closes remain "closed";
// preserving the distinction makes recovery and audit unambiguous.
func (g *Gateway) CloseSessionWithStatus(sessionID, reason, status string) error {
	return g.CloseSessionWithStatusForActor(sessionID, reason, status, 0)
}

// CloseSessionWithStatusForActor is used by the control plane when an
// authenticated administrator explicitly closes a lease. The actor is
// callback metadata for the audit trail; it does not grant any additional
// data-plane authority.
func (g *Gateway) CloseSessionWithStatusForActor(sessionID, reason, status string, actorUserID uint) error {
	g.mu.RLock()
	session := g.sessions[sessionID]
	g.mu.RUnlock()
	if session == nil {
		return ErrSessionNotFound
	}
	if actorUserID > 0 {
		session.closeWithStatus(reason, status, actorUserID)
	} else {
		session.closeWithStatus(reason, status)
	}
	return nil
}

func (g *Gateway) GetSession(sessionID string) (SessionInfo, error) {
	g.mu.RLock()
	session := g.sessions[sessionID]
	g.mu.RUnlock()
	if session == nil {
		return SessionInfo{}, ErrSessionNotFound
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	info := session.info
	if info.Status != "closed" && time.Now().After(info.ExpiresAt) {
		go session.closeWithStatus("维护会话已过期", "expired")
		info.Status = "expired"
		info.Connected = false
	}
	return info, nil
}

// DisconnectDevice drops the maintenance data-plane connection without
// affecting ticket verification or the device's normal signed API path.
func (g *Gateway) DisconnectDevice(deviceID uint, reason string) {
	if g == nil {
		return
	}
	g.mu.RLock()
	device := g.devices[deviceID]
	g.mu.RUnlock()
	if device != nil {
		device.close(reason)
		_ = device.ws.Close()
	}
}

func (g *Gateway) DeviceConnected(deviceID uint) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.devices[deviceID] != nil
}

// StopAll closes in-memory connections. Database session rows are marked
// interrupted by the service layer during the next startup reconciliation.
func (g *Gateway) StopAll() {
	if g == nil {
		return
	}
	g.mu.Lock()
	devices := make([]*deviceConnection, 0, len(g.devices))
	for _, device := range g.devices {
		devices = append(devices, device)
	}
	g.mu.Unlock()
	for _, device := range devices {
		device.close("服务器维护网关停止")
		_ = device.ws.Close()
	}
}

func copyStream(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return err
}
