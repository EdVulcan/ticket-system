package gateclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, config Config) *Client {
	t.Helper()
	if config.StateFile == "" {
		config.StateFile = t.TempDir() + "/state.json"
	}
	if config.ScanToken == "" {
		config.ScanToken = "local-token"
	}
	if config.Driver == nil && config.DriverURL == "" {
		config.Driver = NewSimulationDriver(DriverOpened)
	}
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestScanOpensAndReportsPhysicalResult(t *testing.T) {
	opened, reported, voiceCode := false, false, ""
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		voiceCode, _ = body["voice_code"].(string)
		opened = true
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened"})
	}))
	defer driver.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hardware/verify":
			_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 200, Result: "allow", DisplayText: "欢迎光临", VoiceCode: "child_ticket", OpenDuration: 3000})
		case "/api/v1/hardware/open-result":
			reported = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cloud.Close()
	client := testClient(t, Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL})
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T1"})
	if !result.Allowed || !result.Opened || result.PhysicalStatus != "opened" || !opened || !reported || result.Error != "" || voiceCode != "child_ticket" {
		t.Fatalf("unexpected result: %+v opened=%v reported=%v voice=%q", result, opened, reported, voiceCode)
	}
}

func TestScanFailsClosedWhenPhysicalOpenIsUnknown(t *testing.T) {
	reportedStatus := ""
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer driver.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/hardware/verify" {
			_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 200, Result: "allow", OpenDuration: 3000})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		reportedStatus = body["status"]
		w.WriteHeader(http.StatusOK)
	}))
	defer cloud.Close()
	client := testClient(t, Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL})
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T1"})
	if !result.Allowed || result.Opened || result.PhysicalStatus != "unknown" || result.Error == "" || reportedStatus != "" {
		t.Fatalf("unexpected result: %+v reported=%s", result, reportedStatus)
	}
	retry := client.Scan(t.Context(), ScanRequest{TicketCode: "T1"})
	if retry.RequestID != result.RequestID || retry.Opened || retry.Error != "上次开闸结果未知，请现场确认后处理" {
		t.Fatalf("ambiguous opening was not kept fail-closed: first=%+v retry=%+v", result, retry)
	}
}

func TestScanReusesRequestAfterVerificationResponseLoss(t *testing.T) {
	verifyCalls := 0
	requestIDs := make([]string, 0, 2)
	driverCalls := 0
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		driverCalls++
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened"})
	}))
	defer driver.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/hardware/verify" {
			verifyCalls++
			requestIDs = append(requestIDs, r.Header.Get("X-Device-Request-Id"))
			if verifyCalls == 1 {
				_, _ = w.Write([]byte(`{"result":`))
				return
			}
			_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 200, Result: "allow", DisplayText: "欢迎光临", OpenDuration: 3000})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer cloud.Close()
	stateFile := t.TempDir() + "/state.json"
	client := testClient(t, Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL, StateFile: stateFile})
	first := client.Scan(t.Context(), ScanRequest{TicketCode: "T-LOST"})
	if first.Error == "" {
		t.Fatalf("invalid first response was accepted: %+v", first)
	}
	second := client.Scan(t.Context(), ScanRequest{TicketCode: "T-LOST"})
	if !second.Opened || second.Error != "" || verifyCalls != 2 || driverCalls != 1 || len(requestIDs) != 2 || requestIDs[0] != requestIDs[1] {
		t.Fatalf("response-loss recovery failed: first=%+v second=%+v ids=%v verify=%d driver=%d", first, second, requestIDs, verifyCalls, driverCalls)
	}
}

func TestDeniedScanReturnsLocalVoiceInstruction(t *testing.T) {
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 410, Result: "deny", DisplayText: "门票已过期", VoiceCode: "expired", VoiceFile: "expired.mp3"})
	}))
	defer cloud.Close()
	client := testClient(t, Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K"})
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T-EXPIRED"})
	if result.Allowed || result.VoiceCode != "expired" || result.VoiceFile != "expired.mp3" || result.DisplayText != "门票已过期" {
		t.Fatalf("denied voice instruction=%+v", result)
	}
}

func TestRecoveryEndpointResolvesAmbiguousOpening(t *testing.T) {
	client := testClient(t, Config{ServerURL: "https://example.invalid", SystemCode: "S", SerialNumber: "G", DeviceKey: "K", ScanToken: "local-token"})
	client.pending["qr_code:T1"] = pendingScan{RequestID: "request-1", Stage: "opening", Body: map[string]string{"ticket_code": "T1"}, Verification: VerifyResponse{Result: "allow"}}
	body := strings.NewReader(`{"ticket_code":"T1","action":"confirm_opened"}`)
	request := httptest.NewRequest(http.MethodPost, "/recovery", body)
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	client.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || client.pending["qr_code:T1"].Stage != "opened" {
		t.Fatalf("recovery status=%d body=%s pending=%+v", response.Code, response.Body.String(), client.pending["qr_code:T1"])
	}
}

func TestStatusEndpointDoesNotExposeDeviceSecret(t *testing.T) {
	client := testClient(t, Config{ServerURL: "https://example.invalid", SystemCode: "S", SerialNumber: "G", DeviceKey: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	client.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), "pending_count") {
		t.Fatalf("unexpected status response: code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKnownDriverFailureIsReportedAndCanBeRetried(t *testing.T) {
	reported := make([]string, 0, 2)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/hardware/verify" {
			_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 200, Result: "allow", DisplayText: "欢迎光临", OpenDuration: 3000})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		reported = append(reported, body["status"])
		w.WriteHeader(http.StatusOK)
	}))
	defer cloud.Close()
	driver := NewSimulationDriver(DriverFailed)
	client := testClient(t, Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", Driver: driver})
	first := client.Scan(t.Context(), ScanRequest{TicketCode: "T-FAIL"})
	if !first.Allowed || first.Opened || first.PhysicalStatus != "failed" || first.Error == "" || len(reported) != 1 || reported[0] != "failed" {
		t.Fatalf("known failure was not persisted: result=%+v reported=%v", first, reported)
	}
	request := httptest.NewRequest(http.MethodPost, "/recovery", strings.NewReader(`{"ticket_code":"T-FAIL","action":"retry_open"}`))
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	client.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("retry recovery status=%d body=%s", response.Code, response.Body.String())
	}
	driver.Status = DriverOpened
	second := client.Scan(t.Context(), ScanRequest{TicketCode: "T-FAIL"})
	if !second.Opened || second.PhysicalStatus != "opened" || second.Error != "" || len(reported) != 2 || reported[1] != "opened" {
		t.Fatalf("failed opening could not be retried: result=%+v reported=%v", second, reported)
	}
}

func TestStateFileIsRequired(t *testing.T) {
	_, err := New(Config{ServerURL: "https://example.invalid", SystemCode: "S", SerialNumber: "G", DeviceKey: "K", Driver: NewSimulationDriver(DriverOpened)})
	if err == nil || !strings.Contains(err.Error(), "GATE_STATE_FILE") {
		t.Fatalf("missing state file was accepted: %v", err)
	}
}

func TestScanReloadsRecoveryStateAfterProcessRestart(t *testing.T) {
	verifyCalls, openReports, driverCalls := 0, 0, 0
	driverStatus := "unknown"
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		driverCalls++
		if driverStatus == "opened" {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "opened"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer driver.Close()
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hardware/verify":
			verifyCalls++
			_ = json.NewEncoder(w).Encode(VerifyResponse{Code: 200, Result: "allow", DisplayText: "欢迎光临", OpenDuration: 3000})
		case "/api/v1/hardware/open-result":
			openReports++
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cloud.Close()
	stateFile := t.TempDir() + "/state.json"
	config := Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL, StateFile: stateFile, ScanToken: "local-token"}
	firstClient := testClient(t, config)
	first := firstClient.Scan(t.Context(), ScanRequest{TicketCode: "T-RESTART"})
	if !first.Allowed || first.Opened || first.PhysicalStatus != "unknown" || first.Error == "" || verifyCalls != 1 || driverCalls != 1 {
		t.Fatalf("initial unknown result=%+v verify=%d driver=%d", first, verifyCalls, driverCalls)
	}

	secondClient := testClient(t, config)
	restarted := secondClient.Scan(t.Context(), ScanRequest{TicketCode: "T-RESTART"})
	if restarted.RequestID != first.RequestID || restarted.Opened || verifyCalls != 1 || driverCalls != 1 {
		t.Fatalf("restart lost local recovery state: first=%+v restarted=%+v verify=%d driver=%d", first, restarted, verifyCalls, driverCalls)
	}

	request := httptest.NewRequest(http.MethodPost, "/recovery", strings.NewReader(`{"ticket_code":"T-RESTART","action":"confirm_not_opened"}`))
	request.Header.Set("Authorization", "Bearer local-token")
	response := httptest.NewRecorder()
	secondClient.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("restart recovery status=%d body=%s", response.Code, response.Body.String())
	}
	driverStatus = "opened"
	final := secondClient.Scan(t.Context(), ScanRequest{TicketCode: "T-RESTART"})
	if !final.Opened || final.PhysicalStatus != "opened" || final.Error != "" || verifyCalls != 1 || driverCalls != 2 || openReports != 1 {
		t.Fatalf("restart recovery did not complete: final=%+v verify=%d driver=%d reports=%d", final, verifyCalls, driverCalls, openReports)
	}
}
