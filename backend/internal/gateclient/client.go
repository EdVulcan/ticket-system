package gateclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"ticket-backend/internal/deviceauth"
	"time"
)

type Config struct {
	ServerURL    string
	SystemCode   string
	SerialNumber string
	DeviceKey    string
	DriverURL    string
	Driver       GateDriver
	ListenAddr   string
	ScanToken    string
	StateFile    string
	HTTPClient   *http.Client
}

type VerifyResponse struct {
	Code         int    `json:"code"`
	Result       string `json:"result"`
	DisplayText  string `json:"display_text"`
	VoiceFile    string `json:"voice_file"`
	VoiceCode    string `json:"voice_code"`
	OpenDuration int    `json:"open_duration"`
}

type ScanRequest struct {
	TicketCode string `json:"ticket_code"`
	MediaType  string `json:"media_type"`
}

type ScanResult struct {
	RequestID      string `json:"request_id"`
	Allowed        bool   `json:"allowed"`
	Opened         bool   `json:"opened"`
	PhysicalStatus string `json:"physical_status,omitempty"` // pending, opened, failed, unknown
	DisplayText    string `json:"display_text"`
	VoiceFile      string `json:"voice_file,omitempty"`
	VoiceCode      string `json:"voice_code,omitempty"`
	Error          string `json:"error,omitempty"`
}

type pendingScan struct {
	RequestID    string            `json:"request_id"`
	Body         map[string]string `json:"body"`
	Stage        string            `json:"stage"`
	Verification VerifyResponse    `json:"verification"`
}

type Client struct {
	config  Config
	base    *url.URL
	http    *http.Client
	key     []byte
	driver  GateDriver
	mu      sync.Mutex
	stateMu sync.Mutex
	pending map[string]pendingScan
}

func New(config Config) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.ServerURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("GATE_SERVER_URL 无效")
	}
	if strings.TrimSpace(config.SystemCode) == "" || strings.TrimSpace(config.SerialNumber) == "" || strings.TrimSpace(config.DeviceKey) == "" {
		return nil, errors.New("景区系统编号、设备序列号和设备密钥不能为空")
	}
	if strings.TrimSpace(config.StateFile) == "" {
		return nil, errors.New("GATE_STATE_FILE 不能为空，闸机恢复状态必须持久化")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 8 * time.Second}
	}
	driver := config.Driver
	if driver == nil {
		driver, err = NewHTTPDriver(config.DriverURL, config.HTTPClient)
		if err != nil {
			return nil, err
		}
	}
	client := &Client{config: config, base: base, http: config.HTTPClient, key: deviceauth.DeriveKey(config.DeviceKey), driver: driver, pending: make(map[string]pendingScan)}
	if err := client.loadState(); err != nil {
		return nil, fmt.Errorf("load gate recovery state: %w", err)
	}
	return client, nil
}

func (c *Client) Scan(ctx context.Context, input ScanRequest) ScanResult {
	input.TicketCode = strings.TrimSpace(input.TicketCode)
	if input.TicketCode == "" {
		return ScanResult{Error: "票码不能为空", DisplayText: "票码不能为空"}
	}
	if input.MediaType == "" {
		input.MediaType = "qr_code"
	}
	stateKey := input.MediaType + ":" + input.TicketCode
	pending, stateErr := c.getOrCreatePending(stateKey, input)
	if stateErr != nil {
		return ScanResult{Error: stateErr.Error(), DisplayText: "闸机本地状态保存失败"}
	}
	requestID, body := pending.RequestID, pending.Body
	verification := pending.Verification
	var err error
	if pending.Stage == "verifying" {
		for attempt := 0; attempt < 5; attempt++ {
			status, callErr := c.post(ctx, "/api/v1/hardware/verify", requestID, body, &verification)
			err = callErr
			if status != http.StatusConflict || callErr == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ScanResult{RequestID: requestID, Error: ctx.Err().Error(), DisplayText: "验票超时"}
			case <-time.After(120 * time.Millisecond):
			}
		}
		if err != nil {
			return ScanResult{RequestID: requestID, Error: err.Error(), DisplayText: "无法连接验票服务"}
		}
		pending.Verification, pending.Stage = verification, "verified"
		if err := c.storePending(stateKey, pending); err != nil {
			return ScanResult{RequestID: requestID, Error: err.Error(), DisplayText: "闸机本地状态保存失败"}
		}
	}
	result := ScanResult{RequestID: requestID, Allowed: verification.Result == "allow", DisplayText: verification.DisplayText, VoiceFile: verification.VoiceFile, VoiceCode: verification.VoiceCode}
	if !result.Allowed {
		_ = c.clearPending(stateKey)
		return result
	}
	result.PhysicalStatus = "pending"

	switch pending.Stage {
	case "opening":
		result.PhysicalStatus = "unknown"
		result.Error = "上次开闸结果未知，请现场确认后处理"
		return result
	case "failed":
		result.PhysicalStatus = "failed"
		result.Error = "上次开闸失败，请先确认现场未通行后重试"
		return result
	case "opened":
		result.PhysicalStatus = "opened"
		result.Opened = true
		return c.finishOpen(ctx, stateKey, pending, result)
	case "verified":
		return c.openVerified(ctx, stateKey, pending, result)
	default:
		result.Error = "闸机本地状态无效，请检查状态文件"
		return result
	}
}

func (c *Client) openVerified(ctx context.Context, stateKey string, pending pendingScan, result ScanResult) ScanResult {
	pending.Stage = "opening"
	if err := c.storePending(stateKey, pending); err != nil {
		result.Error = err.Error()
		return result
	}
	driverResult, err := c.driver.Open(ctx, OpenRequest{
		RequestID: pending.RequestID, OpenDurationMS: pending.Verification.OpenDuration,
		DisplayText: pending.Verification.DisplayText, VoiceCode: pending.Verification.VoiceCode,
		VoiceFile: pending.Verification.VoiceFile, Direction: "entry",
	})
	if err != nil {
		driverResult = OpenResult{Status: DriverUnknown, Error: err.Error()}
	}
	if driverResult.Error != "" {
		result.Error = driverResult.Error
	}
	switch driverResult.Status {
	case DriverOpened:
		result.PhysicalStatus = "opened"
		pending.Stage = "opened"
		if err := c.storePending(stateKey, pending); err != nil {
			result.Opened = true
			result.Error = joinError(result.Error, err.Error())
			return result
		}
		result.Opened = true
		return c.finishOpen(ctx, stateKey, pending, result)
	case DriverFailed:
		result.PhysicalStatus = "failed"
		pending.Stage = "failed"
		if err := c.storePending(stateKey, pending); err != nil {
			result.Error = joinError(result.Error, err.Error())
			return result
		}
		if reportErr := c.reportOpenResult(ctx, pending.RequestID, "failed", driverResult.Error); reportErr != nil {
			result.Error = joinError(result.Error, "开闸失败结果回报失败："+reportErr.Error())
		} else if result.Error == "" {
			result.Error = "开闸驱动拒绝"
		}
		return result
	default:
		result.PhysicalStatus = "unknown"
		// Unknown means the command may already have reached the controller.
		// Keep the local record locked and require a physical confirmation.
		pending.Stage = "opening"
		if err := c.storePending(stateKey, pending); err != nil {
			result.Error = joinError(result.Error, err.Error())
			return result
		}
		if result.Error == "" {
			result.Error = "开闸结果未知，请现场确认后处理"
		}
		return result
	}
}

func (c *Client) finishOpen(ctx context.Context, stateKey string, pending pendingScan, result ScanResult) ScanResult {
	if err := c.reportOpenResult(ctx, pending.RequestID, "opened", ""); err != nil {
		result.Error = "开闸结果回报失败：" + err.Error()
		return result
	}
	if err := c.clearPending(stateKey); err != nil {
		result.Error = err.Error()
	}
	return result
}

func (c *Client) reportOpenResult(ctx context.Context, verificationRequestID, status, message string) error {
	_, err := c.post(ctx, "/api/v1/hardware/open-result", randomID(), map[string]string{
		"verification_request_id": verificationRequestID,
		"status":                  status,
		"error":                   strings.TrimSpace(message),
		"occurred_at":             time.Now().Format(time.RFC3339),
	}, nil)
	return err
}

func joinError(first, second string) string {
	first, second = strings.TrimSpace(first), strings.TrimSpace(second)
	switch {
	case first == "":
		return second
	case second == "":
		return first
	default:
		return first + "；" + second
	}
}

func (c *Client) getOrCreatePending(key string, input ScanRequest) (pendingScan, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if value, ok := c.pending[key]; ok {
		return value, nil
	}
	value := pendingScan{RequestID: randomID(), Stage: "verifying", Body: map[string]string{"ticket_code": input.TicketCode, "media_type": input.MediaType, "scan_time": time.Now().Format(time.RFC3339)}}
	c.pending[key] = value
	if err := c.saveStateLocked(); err != nil {
		delete(c.pending, key)
		return pendingScan{}, err
	}
	return value, nil
}

func (c *Client) storePending(key string, value pendingScan) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.pending[key] = value
	return c.saveStateLocked()
}

func (c *Client) clearPending(key string) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	delete(c.pending, key)
	return c.saveStateLocked()
}

func (c *Client) loadState() error {
	if strings.TrimSpace(c.config.StateFile) == "" {
		return nil
	}
	data, err := os.ReadFile(c.config.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &c.pending)
}

func (c *Client) saveStateLocked() error {
	path := strings.TrimSpace(c.config.StateFile)
	if path == "" {
		return nil
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c.pending)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (c *Client) Heartbeat(ctx context.Context, status string) error {
	if status == "" {
		status = "online"
	}
	_, err := c.post(ctx, "/api/v1/hardware/heartbeat", randomID(), map[string]string{"status": status}, nil)
	return err
}

func (c *Client) RunHeartbeat(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_ = c.Heartbeat(ctx, "online")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Client) post(ctx context.Context, path, requestID string, payload interface{}, output interface{}) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), randomID()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(deviceauth.HeaderSystemCode, c.config.SystemCode)
	req.Header.Set(deviceauth.HeaderSerial, c.config.SerialNumber)
	req.Header.Set(deviceauth.HeaderRequestID, requestID)
	req.Header.Set(deviceauth.HeaderTimestamp, timestamp)
	req.Header.Set(deviceauth.HeaderNonce, nonce)
	req.Header.Set(deviceauth.HeaderSignature, deviceauth.Sign(c.key, http.MethodPost, target.Path, timestamp, nonce, requestID, body))
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &failure)
		if failure.Error == "" {
			failure.Error = strings.TrimSpace(string(responseBody))
		}
		return resp.StatusCode, fmt.Errorf("服务返回 %d：%s", resp.StatusCode, failure.Error)
	}
	if output != nil && len(responseBody) > 0 {
		return resp.StatusCode, json.Unmarshal(responseBody, output)
	}
	return resp.StatusCode, nil
}

func (c *Client) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 GET"})
			return
		}
		if c.config.ScanToken == "" || req.Header.Get("Authorization") != "Bearer "+c.config.ScanToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "本机状态接口认证失败"})
			return
		}
		writeJSON(w, http.StatusOK, c.statusSnapshot())
	})
	mux.HandleFunc("/scan", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 POST"})
			return
		}
		if c.config.ScanToken == "" || req.Header.Get("Authorization") != "Bearer "+c.config.ScanToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "本机扫码接口认证失败"})
			return
		}
		var input ScanRequest
		if err := json.NewDecoder(io.LimitReader(req.Body, 16<<10)).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "扫码请求格式错误"})
			return
		}
		c.mu.Lock()
		result := c.Scan(req.Context(), input)
		c.mu.Unlock()
		status := http.StatusOK
		if result.Error != "" && !result.Opened {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, result)
	})
	mux.HandleFunc("/recovery", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 POST"})
			return
		}
		if c.config.ScanToken == "" || req.Header.Get("Authorization") != "Bearer "+c.config.ScanToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "本机恢复接口认证失败"})
			return
		}
		var input struct {
			TicketCode string `json:"ticket_code"`
			MediaType  string `json:"media_type"`
			Action     string `json:"action"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 16<<10)).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "恢复请求格式错误"})
			return
		}
		input.TicketCode = strings.TrimSpace(input.TicketCode)
		if input.MediaType == "" {
			input.MediaType = "qr_code"
		}
		key := input.MediaType + ":" + input.TicketCode
		if err := c.resolveOpeningState(key, input.Action); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "已更新，请重新扫描原票码以继续"})
	})
	return mux
}

func (c *Client) statusSnapshot() map[string]interface{} {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	byStage := make(map[string]int)
	for _, pending := range c.pending {
		byStage[pending.Stage]++
	}
	return map[string]interface{}{
		"status":                "ok",
		"system_code":           c.config.SystemCode,
		"serial_number":         c.config.SerialNumber,
		"pending_count":         len(c.pending),
		"pending_by_stage":      byStage,
		"state_file_configured": strings.TrimSpace(c.config.StateFile) != "",
		"driver_configured":     c.driver != nil,
		"offline_mode_enabled":  false,
	}
}

func (c *Client) resolveOpeningState(key, action string) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	value, ok := c.pending[key]
	if !ok {
		return errors.New("没有需要人工确认的开闸记录")
	}
	switch strings.TrimSpace(action) {
	case "confirm_opened":
		if value.Stage != "opening" {
			return errors.New("只有开闸结果未知时才能确认已开")
		}
		value.Stage = "opened"
	case "confirm_not_opened":
		if value.Stage != "opening" {
			return errors.New("只有开闸结果未知时才能确认未开")
		}
		value.Stage = "verified"
	case "retry_open":
		if value.Stage != "failed" {
			return errors.New("只有开闸失败时才能重试")
		}
		value.Stage = "verified"
	default:
		return errors.New("恢复动作必须是确认已开、确认未开或重试开闸")
	}
	c.pending[key] = value
	return c.saveStateLocked()
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(value)
}
