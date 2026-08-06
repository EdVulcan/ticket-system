package gateclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScanOpensAndReportsPhysicalResult(t *testing.T) {
	opened, reported, voiceCode := false, false, ""
	driver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		voiceCode, _ = body["voice_code"].(string)
		opened = true
		w.WriteHeader(http.StatusNoContent)
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
	client, err := New(Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL})
	if err != nil {
		t.Fatal(err)
	}
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T1"})
	if !result.Allowed || !result.Opened || !opened || !reported || result.Error != "" || voiceCode != "child_ticket" {
		t.Fatalf("unexpected result: %+v opened=%v reported=%v voice=%q", result, opened, reported, voiceCode)
	}
}

func TestScanFailsClosedWhenPhysicalOpenIsUnknown(t *testing.T) {
	reportedStatus := ""
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
	client, err := New(Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K"})
	if err != nil {
		t.Fatal(err)
	}
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T1"})
	if !result.Allowed || result.Opened || result.Error == "" || reportedStatus != "" {
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
		w.WriteHeader(http.StatusNoContent)
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
	client, err := New(Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K", DriverURL: driver.URL, StateFile: stateFile})
	if err != nil {
		t.Fatal(err)
	}
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
	client, err := New(Config{ServerURL: cloud.URL, SystemCode: "S", SerialNumber: "G", DeviceKey: "K"})
	if err != nil {
		t.Fatal(err)
	}
	result := client.Scan(t.Context(), ScanRequest{TicketCode: "T-EXPIRED"})
	if result.Allowed || result.VoiceCode != "expired" || result.VoiceFile != "expired.mp3" || result.DisplayText != "门票已过期" {
		t.Fatalf("denied voice instruction=%+v", result)
	}
}

func TestRecoveryEndpointResolvesAmbiguousOpening(t *testing.T) {
	client, err := New(Config{ServerURL: "https://example.invalid", SystemCode: "S", SerialNumber: "G", DeviceKey: "K", ScanToken: "local-token"})
	if err != nil {
		t.Fatal(err)
	}
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
