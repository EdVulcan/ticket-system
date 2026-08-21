// gate-ssh is a deliberately narrow stdio WebSocket bridge for an already
// authorized maintenance session. It is intended for OpenSSH ProxyCommand;
// it does not accept a target host or provide arbitrary TCP forwarding.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"ticket-backend/internal/gatetunnel"

	"golang.org/x/net/websocket"
)

func main() {
	urlFlag := flag.String("url", os.Getenv("MAINTENANCE_SESSION_WS_URL"), "maintenance session ws(s) URL")
	tokenFlag := flag.String("token", os.Getenv("MAINTENANCE_SESSION_TOKEN"), "one-time maintenance session token")
	flag.Parse()
	location := strings.TrimSpace(*urlFlag)
	token := strings.TrimSpace(*tokenFlag)
	if location == "" || token == "" {
		fatal("需要 --url/--token，或设置 MAINTENANCE_SESSION_WS_URL 与 MAINTENANCE_SESSION_TOKEN")
	}
	if !strings.HasPrefix(location, "wss://") && os.Getenv("GATE_ALLOW_INSECURE_HTTP") != "true" {
		fatal("生产环境维护连接必须使用 wss://；本地调试请显式设置 GATE_ALLOW_INSECURE_HTTP=true")
	}
	config, err := websocket.NewConfig(location, originFor(location))
	if err != nil {
		fatal(err.Error())
	}
	config.Protocol = []string{"ticket-maintenance-v1." + token}
	ws, err := websocket.DialConfig(config)
	if err != nil {
		fatal(err.Error())
	}
	defer ws.Close()

	kind, data, err := gatetunnel.ReceiveFrame(ws)
	if err != nil {
		fatal(err.Error())
	}
	message, err := gatetunnel.DecodeControl(data)
	if err != nil || kind != gatetunnel.FrameControl || message.Type != "ready" {
		if err == nil {
			err = errors.New("维护会话握手失败")
		}
		fatal(err.Error())
	}

	writer := &stdioWriter{ws: ws}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := os.Stdin.Read(buffer)
			if n > 0 {
				if err := writer.stream(buffer[:n]); err != nil {
					return
				}
			}
			if readErr != nil {
				_ = writer.control(gatetunnel.ControlMessage{Type: "close"})
				return
			}
		}
	}()

	for {
		kind, data, err := gatetunnel.ReceiveFrame(ws)
		if err != nil {
			return
		}
		if kind == gatetunnel.FrameStream {
			_, _ = os.Stdout.Write(data)
			continue
		}
		message, err := gatetunnel.DecodeControl(data)
		if err != nil {
			fatal(err.Error())
		}
		switch message.Type {
		case "error":
			fatal(message.Error)
		case "close":
			return
		case "ready":
			// The first ready is consumed above; a second one is harmless.
		default:
			fmt.Fprintf(os.Stderr, "维护会话消息：%s\n", message.Type)
		}
	}
}

type stdioWriter struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (w *stdioWriter) stream(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return gatetunnel.SendStream(w.ws, data)
}

func (w *stdioWriter) control(message gatetunnel.ControlMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return gatetunnel.SendControl(w.ws, message)
}

func originFor(location string) string {
	if strings.HasPrefix(location, "wss://") {
		return "https://" + strings.TrimPrefix(location, "wss://")
	}
	return "http://" + strings.TrimPrefix(location, "ws://")
}

func fatal(message string) {
	if strings.TrimSpace(message) == "" {
		message = "maintenance bridge failed"
	}
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
