package ctrip

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSyncPriceUsesOfficialEnvelope(t *testing.T) {
	fixed := time.Date(2026, 8, 4, 12, 30, 0, 0, time.Local)
	var received struct {
		Header map[string]string `json:"header"`
		Body   string            `json:"body"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"header":{"resultCode":"0000","resultMessage":"success"}}`))
	}))
	defer server.Close()

	client := Client{AccountID: "account", SignKey: "sign", AESKey: "1234567890abcdef", AESIV: "abcdef1234567890", HTTP: server.Client(), Now: func() time.Time { return fixed }}
	result, err := client.SyncPrice(context.Background(), server.URL, PriceRequest{SequenceID: "seq", SupplierOptionID: "PLU-1", DateType: "DATE_REQUIRED", Prices: []Price{{Date: "2026-08-05", SalePrice: 100, CostPrice: 80}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != "0000" || received.Header["serviceName"] != "DatePriceModify" || received.Header["version"] != "1.0" {
		t.Fatalf("unexpected response/header: result=%+v header=%+v", result, received.Header)
	}
	want := Signature("account", "DatePriceModify", "2026-08-04 12:30:00", received.Body, "1.0", "sign")
	if received.Header["sign"] != want {
		t.Fatalf("signature = %s, want %s", received.Header["sign"], want)
	}
}

func TestClientRejectsResponseWithoutResultCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	client := Client{AccountID: "account", SignKey: "sign", AESKey: "1234567890abcdef", AESIV: "abcdef1234567890", HTTP: server.Client()}
	_, err := client.SyncInventory(context.Background(), server.URL, InventoryRequest{SequenceID: "seq", SupplierOptionID: "PLU-1", DateType: "DATE_REQUIRED", Inventories: []Inventory{{Date: "2026-08-05", Quantity: 1}}})
	if err == nil {
		t.Fatal("missing result code was accepted")
	}
}

func TestNotifyConsumedUsesOrderNoticeService(t *testing.T) {
	var serviceName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			Header map[string]string `json:"header"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		serviceName = envelope.Header["serviceName"]
		_, _ = w.Write([]byte(`{"header":{"resultCode":"0000","resultMessage":"success"}}`))
	}))
	defer server.Close()
	client := Client{AccountID: "account", SignKey: "sign", AESKey: "1234567890abcdef", AESIV: "abcdef1234567890", HTTP: server.Client()}
	response, err := client.NotifyConsumed(context.Background(), server.URL, ConsumedNoticeRequest{
		SequenceID: "20260809abcdef", OTAOrderID: "OTA-1", SupplierOrderID: "ORD-1",
		Items: []ConsumedItem{{ItemID: "ITEM-1", Quantity: 1, UseQuantity: 1, Vouchers: []ConsumedVoucher{{VoucherID: "TICKET-1"}}}},
	})
	if err != nil || response.Code != "0000" || serviceName != "OrderConsumedNotice" {
		t.Fatalf("response=%+v service=%q err=%v", response, serviceName, err)
	}
}
