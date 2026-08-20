package gateclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DriverStatus describes the certainty of the physical controller result.
// An HTTP response or a local process exit is not automatically proof that the
// turnstile opened. Adapters must return unknown when they cannot establish a
// physical result safely.
type DriverStatus string

const (
	DriverOpened  DriverStatus = "opened"
	DriverFailed  DriverStatus = "failed"
	DriverUnknown DriverStatus = "unknown"
)

// OpenRequest contains only the data a hardware adapter needs after the
// server has already allowed the ticket. It deliberately carries no ticket or
// payment facts, so adapters cannot become a second ticketing engine.
type OpenRequest struct {
	RequestID      string
	OpenDurationMS int
	DisplayText    string
	VoiceCode      string
	VoiceFile      string
	Direction      string
}

type OpenResult struct {
	Status     DriverStatus
	VendorCode string
	Error      string
	ElapsedMS  int64
}

// GateDriver is the seam between the protocol-independent gate runtime and a
// concrete turnstile adapter. A real adapter may use a relay, serial port,
// RS485, TCP or a vendor SDK without changing the ticket workflow.
type GateDriver interface {
	Open(context.Context, OpenRequest) (OpenResult, error)
}

// HTTPDriver is a development and integration adapter. It intentionally
// requires an explicit JSON status from the local controller bridge. A bare
// 2xx response is treated as unknown rather than as physical success.
type HTTPDriver struct {
	URL        string
	HTTPClient *http.Client
}

type httpDriverResponse struct {
	Status     string `json:"status"`
	VendorCode string `json:"vendor_code"`
	Error      string `json:"error"`
}

func NewHTTPDriver(rawURL string, client *http.Client) (*HTTPDriver, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("GATE_DRIVER_URL 不能为空")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPDriver{URL: rawURL, HTTPClient: client}, nil
}

func (d *HTTPDriver) Open(ctx context.Context, request OpenRequest) (OpenResult, error) {
	started := time.Now()
	body, err := json.Marshal(map[string]interface{}{
		"request_id":       request.RequestID,
		"open_duration_ms": request.OpenDurationMS,
		"display_text":     request.DisplayText,
		"voice_code":       request.VoiceCode,
		"voice_file":       request.VoiceFile,
		"direction":        request.Direction,
	})
	if err != nil {
		return OpenResult{Status: DriverFailed, Error: err.Error()}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		return OpenResult{Status: DriverFailed, Error: err.Error()}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.HTTPClient.Do(req)
	if err != nil {
		// A timeout or transport error may happen after the controller acted.
		// Keep the result unknown and require the local recovery workflow.
		return OpenResult{Status: DriverUnknown, Error: err.Error(), ElapsedMS: time.Since(started).Milliseconds()}, nil
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	elapsed := time.Since(started).Milliseconds()
	if readErr != nil {
		return OpenResult{Status: DriverUnknown, Error: readErr.Error(), ElapsedMS: elapsed}, nil
	}

	var result httpDriverResponse
	if len(bytes.TrimSpace(responseBody)) > 0 {
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return OpenResult{Status: DriverUnknown, Error: "开闸驱动返回格式无法确认", ElapsedMS: elapsed}, nil
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := DriverFailed
		if resp.StatusCode >= 500 {
			status = DriverUnknown
		}
		if result.Error == "" {
			result.Error = fmt.Sprintf("开闸驱动返回 HTTP %d", resp.StatusCode)
		}
		return OpenResult{Status: status, VendorCode: result.VendorCode, Error: result.Error, ElapsedMS: elapsed}, nil
	}

	status := normalizeDriverStatus(result.Status)
	if status == DriverUnknown && result.Error == "" {
		result.Error = "开闸驱动未确认物理结果"
	}
	return OpenResult{Status: status, VendorCode: result.VendorCode, Error: result.Error, ElapsedMS: elapsed}, nil
}

func normalizeDriverStatus(value string) DriverStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(DriverOpened), "success", "ok":
		return DriverOpened
	case string(DriverFailed), "failure", "rejected":
		return DriverFailed
	default:
		return DriverUnknown
	}
}

// SimulationDriver is deliberately explicit. It is useful for automated
// tests and a future local commissioning tool, but it is never selected by
// the production command unless a caller injects it deliberately.
type SimulationDriver struct {
	Status     DriverStatus
	VendorCode string
	Error      string

	mu    sync.Mutex
	Calls []OpenRequest
}

func NewSimulationDriver(status DriverStatus) *SimulationDriver {
	if status == "" {
		status = DriverOpened
	}
	return &SimulationDriver{Status: status}
}

func (d *SimulationDriver) Open(_ context.Context, request OpenRequest) (OpenResult, error) {
	d.mu.Lock()
	d.Calls = append(d.Calls, request)
	d.mu.Unlock()
	return OpenResult{Status: d.Status, VendorCode: d.VendorCode, Error: d.Error}, nil
}
