package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
)

func providerTestConfig(baseURL string) model.PlatformAIConfig {
	return model.PlatformAIConfig{
		Provider:              "openai_compatible",
		BaseURL:               baseURL,
		Model:                 "test-model",
		RequestTimeoutSeconds: 5,
		Temperature:           0.1,
	}
}

func writeProviderTestSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id": "provider-test-request",
		"choices": []interface{}{map[string]interface{}{
			"message":       map[string]string{"content": `{"operation_type":"pending"}`},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"total_tokens": 7},
	})
}

func writeProviderTestError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]string{"message": message}})
}

func TestPlatformAIProviderRetriesTransientFailureOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeProviderTestError(w, http.StatusBadGateway, "temporary upstream failure")
			return
		}
		writeProviderTestSuccess(w)
	}))
	defer server.Close()

	service := &PlatformAIService{HTTPClient: server.Client()}
	content, tokens, err := service.chat(context.Background(), providerTestConfig(server.URL), "test-key", []AIMessage{{Role: "user", Content: "test"}}, 0)
	if err != nil {
		t.Fatalf("transient provider failure was not retried: %v", err)
	}
	if calls.Load() != 2 || tokens != 7 || content == "" {
		t.Fatalf("calls=%d tokens=%d content=%q, want one retry and successful response", calls.Load(), tokens, content)
	}
}

func TestPlatformAIProviderDoesNotRetryAuthenticationFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeProviderTestError(w, http.StatusUnauthorized, "invalid api key")
	}))
	defer server.Close()

	service := &PlatformAIService{HTTPClient: server.Client()}
	_, _, err := service.chat(context.Background(), providerTestConfig(server.URL), "bad-key", []AIMessage{{Role: "user", Content: "test"}}, 0)
	if err == nil {
		t.Fatal("authentication failure unexpectedly succeeded")
	}
	var providerErr *AIProviderError
	if !errors.As(err, &providerErr) || providerErr.HTTPStatus != http.StatusUnauthorized || providerErr.Retryable {
		t.Fatalf("provider error=%+v, want non-retryable HTTP 401", providerErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("authentication failure was retried %d times", calls.Load())
	}
}

func TestPlatformAIProviderCircuitOpensAfterRepeatedTransientFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeProviderTestError(w, http.StatusServiceUnavailable, "provider unavailable")
	}))
	defer server.Close()

	service := &PlatformAIService{HTTPClient: server.Client()}
	config := providerTestConfig(server.URL)
	for attempt := 0; attempt < aiProviderCircuitFailureThreshold; attempt++ {
		_, _, err := service.chat(context.Background(), config, "test-key", []AIMessage{{Role: "user", Content: "test"}}, 0)
		if err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt+1)
		}
	}
	beforeCircuit := calls.Load()
	_, _, err := service.chat(context.Background(), config, "test-key", []AIMessage{{Role: "user", Content: "test"}}, 0)
	if err == nil {
		t.Fatal("open provider circuit unexpectedly allowed a request")
	}
	var providerErr *AIProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != "circuit_open" {
		t.Fatalf("provider error=%+v, want circuit_open", providerErr)
	}
	if calls.Load() != beforeCircuit {
		t.Fatalf("open circuit sent another provider request: before=%d after=%d", beforeCircuit, calls.Load())
	}
}
