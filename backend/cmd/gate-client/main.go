package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"ticket-backend/internal/gateclient"
	"time"
)

func main() {
	serverURL := strings.TrimSpace(os.Getenv("GATE_SERVER_URL"))
	allowInsecure := os.Getenv("GATE_ALLOW_INSECURE_HTTP") == "true"
	if strings.HasPrefix(strings.ToLower(serverURL), "http://") && !allowInsecure {
		log.Fatal("生产环境 GATE_SERVER_URL 必须使用 HTTPS；本地调试可显式设置 GATE_ALLOW_INSECURE_HTTP=true")
	}
	maintenanceURL := strings.TrimSpace(os.Getenv("GATE_MAINTENANCE_URL"))
	if strings.HasPrefix(strings.ToLower(maintenanceURL), "ws://") && !allowInsecure {
		log.Fatal("生产环境维护连接必须使用 wss://；本地调试可显式设置 GATE_ALLOW_INSECURE_HTTP=true")
	}
	listen := strings.TrimSpace(os.Getenv("GATE_SCAN_LISTEN"))
	if listen == "" {
		listen = "127.0.0.1:19300"
	}
	client, err := gateclient.New(gateclient.Config{ServerURL: serverURL, SystemCode: os.Getenv("GATE_SYSTEM_CODE"), SerialNumber: os.Getenv("GATE_SERIAL_NUMBER"), DeviceKey: os.Getenv("GATE_DEVICE_KEY"), MaintenanceURL: maintenanceURL, MaintenanceSecret: os.Getenv("GATE_MAINTENANCE_SECRET"), DriverURL: os.Getenv("GATE_DRIVER_URL"), ListenAddr: listen, ScanToken: os.Getenv("GATE_SCAN_TOKEN"), StateFile: os.Getenv("GATE_STATE_FILE"), AllowInsecureHTTP: allowInsecure})
	if err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(os.Getenv("GATE_SCAN_TOKEN")) == "" {
		log.Fatal("GATE_SCAN_TOKEN 不能为空")
	}

	// BusyBox init scripts stop services with SIGTERM, while an operator at a
	// terminal normally uses SIGINT. Handle both so the same binary can run
	// under systemd, BusyBox init, or a foreground diagnostic shell and still
	// flush its HTTP server and persisted recovery state on shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go client.RunHeartbeat(ctx, 20*time.Second)
	go client.RunMaintenance(ctx, 5*time.Second)
	server := &http.Server{Addr: listen, Handler: client.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		log.Printf("闸机客户端已启动，监听 %s", listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
