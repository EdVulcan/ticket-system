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
	RequestID   string `json:"request_id"`
	Allowed     bool   `json:"allowed"`
	Opened      bool   `json:"opened"`
	DisplayText string `json:"display_text"`
	VoiceFile   string `json:"voice_file,omitempty"`
	VoiceCode   string `json:"voice_code,omitempty"`
	Error       string `json:"error,omitempty"`
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
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 8 * time.Second}
	}
	client := &Client{config: config, base: base, http: config.HTTPClient, key: deviceauth.DeriveKey(config.DeviceKey), pending: make(map[string]pendingScan)}
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

	if pending.Stage == "opening" {
		result.Error = "上次开闸结果未知，请现场确认后处理"
		return result
	}
	if pending.Stage == "verified" {
		pending.Stage = "opening"
		if err := c.storePending(stateKey, pending); err != nil {
			result.Error = err.Error()
			return result
		}
		if openErr := c.openGate(ctx, requestID, verification); openErr != nil {
			result.Error = openErr.Error()
			return result
		}
		pending.Stage = "opened"
		if err := c.storePending(stateKey, pending); err != nil {
			return ScanResult{RequestID: requestID, Allowed: true, Opened: true, DisplayText: verification.DisplayText, VoiceFile: verification.VoiceFile, VoiceCode: verification.VoiceCode, Error: err.Error()}
		}
	}
	result.Opened = true
	report := map[string]string{"verification_request_id": requestID, "status": "opened", "occurred_at": time.Now().Format(time.RFC3339)}
	if _, err := c.post(ctx, "/api/v1/hardware/open-result", randomID(), report, nil); err != nil {
		result.Error = "开闸结果回报失败：" + err.Error()
		return result
	}
	if err := c.clearPending(stateKey); err != nil {
		result.Error = err.Error()
	}
	return result
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

func (c *Client) openGate(ctx context.Context, requestID string, verification VerifyResponse) error {
	if strings.TrimSpace(c.config.DriverURL) == "" {
		return errors.New("未配置真实开闸驱动")
	}
	body, _ := json.Marshal(map[string]interface{}{"request_id": requestID, "open_duration_ms": verification.OpenDuration, "display_text": verification.DisplayText, "voice_file": verification.VoiceFile, "voice_code": verification.VoiceCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.DriverURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("开闸驱动调用失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("开闸驱动返回 %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

func (c *Client) resolveOpeningState(key, action string) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	value, ok := c.pending[key]
	if !ok || value.Stage != "opening" {
		return errors.New("没有需要人工确认的开闸记录")
	}
	switch strings.TrimSpace(action) {
	case "confirm_opened":
		value.Stage = "opened"
	case "confirm_not_opened":
		value.Stage = "verified"
	default:
		return errors.New("恢复动作必须是确认已开或确认未开")
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
