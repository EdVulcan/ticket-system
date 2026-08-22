package gateclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"ticket-backend/internal/gatetunnel"
	"time"

	"golang.org/x/net/websocket"
)

// RunMaintenance keeps the optional WSS maintenance channel connected. It is
// deliberately separate from the signed ticket/heartbeat client: a missing
// maintenance secret disables this feature and never affects admission.
func (c *Client) RunMaintenance(ctx context.Context, retry time.Duration) {
	if c == nil || strings.TrimSpace(c.config.MaintenanceSecret) == "" {
		return
	}
	if retry <= 0 {
		retry = 5 * time.Second
	}
	for {
		if err := c.runMaintenanceConnection(ctx); err != nil && ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

func (c *Client) runMaintenanceConnection(ctx context.Context) error {
	location, origin, err := c.maintenanceLocation()
	if err != nil {
		return err
	}
	wsConfig, err := websocket.NewConfig(location, origin)
	if err != nil {
		return err
	}
	wsConfig.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.config.MaintenanceSecret))
	ws, err := websocket.DialConfig(wsConfig)
	if err != nil {
		return err
	}
	defer ws.Close()
	// x/net/websocket's ReceiveFrame has no context parameter. Close the
	// socket from a small cancellation watcher so shutdown and credential
	// rotation cannot leave RunMaintenance blocked in a read forever.
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ws.Close()
		case <-watchDone:
		}
	}()
	defer close(watchDone)

	writer := &maintenanceWriter{ws: ws}
	if err := ws.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	kind, data, err := gatetunnel.ReceiveFrame(ws)
	if err != nil {
		return err
	}
	if kind != gatetunnel.FrameControl {
		return errors.New("maintenance gateway did not send a control handshake")
	}
	message, err := gatetunnel.DecodeControl(data)
	if err != nil || message.Type != "ready" {
		if err == nil {
			err = fmt.Errorf("unexpected maintenance handshake %q", message.Type)
		}
		return err
	}
	_ = ws.SetReadDeadline(time.Time{})

	sshMu := sync.Mutex{}
	var sshConn net.Conn
	closeSSH := func() {
		sshMu.Lock()
		conn := sshConn
		sshConn = nil
		sshMu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	}
	defer closeSSH()

	for {
		kind, data, err := gatetunnel.ReceiveFrame(ws)
		if err != nil {
			return err
		}
		if kind == gatetunnel.FrameStream {
			sshMu.Lock()
			conn := sshConn
			sshMu.Unlock()
			if conn == nil {
				_ = writer.control(gatetunnel.ControlMessage{Type: "ssh_error", Error: "本机 SSH 尚未建立"})
				continue
			}
			if _, err := conn.Write(data); err != nil {
				closeSSH()
				_ = writer.control(gatetunnel.ControlMessage{Type: "ssh_closed", Error: "写入本机 SSH 失败"})
			}
			continue
		}
		message, err := gatetunnel.DecodeControl(data)
		if err != nil {
			return err
		}
		switch message.Type {
		case "open_ssh":
			sshMu.Lock()
			alreadyOpen := sshConn != nil
			sshMu.Unlock()
			if alreadyOpen {
				_ = writer.control(gatetunnel.ControlMessage{Type: "ssh_error", SessionID: message.SessionID, Error: "本机 SSH 会话已建立"})
				continue
			}
			conn, dialErr := net.DialTimeout("tcp", "127.0.0.1:22", 5*time.Second)
			if dialErr != nil {
				_ = writer.control(gatetunnel.ControlMessage{Type: "ssh_error", SessionID: message.SessionID, Error: "无法连接闸机本机 SSH"})
				continue
			}
			sshMu.Lock()
			sshConn = conn
			sshMu.Unlock()
			if err := writer.control(gatetunnel.ControlMessage{Type: "ssh_ready", SessionID: message.SessionID}); err != nil {
				closeSSH()
				return err
			}
			go c.copySSHToCloud(ctx, conn, writer, &sshMu, &sshConn, message.SessionID)
		case "close":
			closeSSH()
		case "heartbeat_ack":
			// The cloud uses this as a liveness signal; no business state is
			// changed by a heartbeat.
		default:
			return fmt.Errorf("unsupported maintenance control message %q", message.Type)
		}
	}
}

func (c *Client) copySSHToCloud(ctx context.Context, conn net.Conn, writer *maintenanceWriter, sshMu *sync.Mutex, current *net.Conn, sessionID string) {
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := conn.Read(buffer)
		if n > 0 {
			if sendErr := writer.stream(buffer[:n]); sendErr != nil {
				return
			}
		}
		if err != nil {
			sshMu.Lock()
			if *current == conn {
				*current = nil
			}
			sshMu.Unlock()
			_ = conn.Close()
			if !errors.Is(err, io.EOF) {
				_ = writer.control(gatetunnel.ControlMessage{Type: "ssh_closed", SessionID: sessionID, Error: "本机 SSH 连接已断开"})
			}
			return
		}
	}
}

type maintenanceWriter struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (w *maintenanceWriter) control(message gatetunnel.ControlMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return gatetunnel.SendControl(w.ws, message)
}

func (w *maintenanceWriter) stream(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return gatetunnel.SendStream(w.ws, data)
}

func (c *Client) maintenanceLocation() (string, string, error) {
	raw := strings.TrimSpace(c.config.MaintenanceURL)
	if raw == "" {
		raw = c.base.String()
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", err
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			u.Scheme = "wss"
		case "http":
			u.Scheme = "ws"
		default:
			return "", "", errors.New("GATE_SERVER_URL 必须使用 http(s) 或显式配置 GATE_MAINTENANCE_URL")
		}
		u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/hardware/maintenance/ws"
		raw = u.String()
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return "", "", errors.New("GATE_MAINTENANCE_URL 必须是 ws/wss URL")
	}
	if u.Scheme == "ws" && !c.config.AllowInsecureHTTP {
		return "", "", errors.New("生产环境维护连接必须使用 wss://；本地调试请显式设置 GATE_ALLOW_INSECURE_HTTP=true")
	}
	originScheme := "http"
	if u.Scheme == "wss" {
		originScheme = "https"
	}
	origin := originScheme + "://" + u.Host
	return u.String(), origin, nil
}
