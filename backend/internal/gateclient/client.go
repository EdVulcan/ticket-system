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
	Error       string `json:"error,omitempty"`
}

type Client struct {
	config Config
	base   *url.URL
	http   *http.Client
	key    []byte
	mu     sync.Mutex
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
	return &Client{config: config, base: base, http: config.HTTPClient, key: deviceauth.DeriveKey(config.DeviceKey)}, nil
}

func (c *Client) Scan(ctx context.Context, input ScanRequest) ScanResult {
	input.TicketCode = strings.TrimSpace(input.TicketCode)
	if input.TicketCode == "" {
		return ScanResult{Error: "票码不能为空", DisplayText: "票码不能为空"}
	}
	if input.MediaType == "" {
		input.MediaType = "qr_code"
	}
	requestID := randomID()
	body := map[string]string{"ticket_code": input.TicketCode, "media_type": input.MediaType, "scan_time": time.Now().Format(time.RFC3339)}
	var verification VerifyResponse
	var err error
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
	result := ScanResult{RequestID: requestID, Allowed: verification.Result == "allow", DisplayText: verification.DisplayText}
	if !result.Allowed {
		return result
	}

	openErr := c.openGate(ctx, requestID, verification)
	openStatus := "opened"
	if openErr != nil {
		openStatus = "failed"
		result.Error = openErr.Error()
	} else {
		result.Opened = true
	}
	report := map[string]string{"verification_request_id": requestID, "status": openStatus, "occurred_at": time.Now().Format(time.RFC3339)}
	if openErr != nil {
		report["error"] = openErr.Error()
	}
	if _, err := c.post(ctx, "/api/v1/hardware/open-result", randomID(), report, nil); err != nil {
		if result.Error == "" {
			result.Error = "开闸结果回报失败：" + err.Error()
		} else {
			result.Error += "；开闸结果回报失败：" + err.Error()
		}
	}
	return result
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
	return mux
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
