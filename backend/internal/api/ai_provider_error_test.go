package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestAgentTaskProviderErrorUsesBadGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	writeAgentTaskError(ctx, &service.AIProviderError{Err: errors.New("AI provider did not return a final answer; increase max_output_tokens")})

	if recorder.Code != 502 {
		t.Fatalf("status=%d, want 502", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["code"] != "ai_provider_error" {
		t.Fatalf("code=%q, want ai_provider_error", body["code"])
	}
}

func TestCatalogBatchProviderErrorUsesBadGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	writeCatalogBatchError(ctx, &service.AIProviderError{Err: errors.New("AI provider returned an empty plan")})

	if recorder.Code != 502 {
		t.Fatalf("status=%d, want 502", recorder.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["code"] != "ai_provider_error" {
		t.Fatalf("code=%q, want ai_provider_error", body["code"])
	}
}
