package gateclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestScanFailsClosedWithoutDriverAndReportsFailure(t *testing.T) {
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
	if !result.Allowed || result.Opened || result.Error == "" || reportedStatus != "failed" {
		t.Fatalf("unexpected result: %+v reported=%s", result, reportedStatus)
	}
}
