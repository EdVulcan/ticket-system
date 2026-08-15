package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
)

func startCatalogAIProvider(t *testing.T, response string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/chat/completions" {
			http.Error(writer, "wrong endpoint", http.StatusNotFound)
			return
		}
		if request.Header.Get("Authorization") != "Bearer test-provider-key" {
			http.Error(writer, "wrong authorization", http.StatusUnauthorized)
			return
		}
		var body struct {
			Messages []AIMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		prompt := ""
		for _, message := range body.Messages {
			prompt += message.Content
		}
		if !strings.Contains(prompt, "Adult Ticket") || strings.Contains(prompt, "Foreign Ticket") || strings.Contains(prompt, `"scenic_area_id"`) || strings.Contains(prompt, `"checkpoint_id"`) {
			http.Error(writer, "tenant context leaked", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"total_tokens":32}}`, response)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func saveCatalogAIConfig(t *testing.T, baseURL string, requestLimit int) {
	t.Helper()
	if _, err := (&PlatformAIService{}).SaveConfig(PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: baseURL, Model: defaultAIModel, APIKey: "test-provider-key", Enabled: true,
		DefaultMonthlyRequestLimit: requestLimit, DefaultMonthlyTokenLimit: 100000,
		RequestTimeoutSeconds: 5, MaxOutputTokens: 256, Temperature: 0,
	}, 77, "platform_admin"); err != nil {
		t.Fatalf("save AI config: %v", err)
	}
}

func TestCatalogBatchAIPreviewUsesTenantContextAndDurableIdempotency(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := startCatalogAIProvider(t, `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["North Gate"],"max_per_check_in":2}]}`)
	saveCatalogAIConfig(t, server.URL, 5)

	service := &CatalogBatchChangeService{}
	result, err := service.PreviewWithAI(t.Context(), fixture.tenant.ID, 11, "admin", CatalogAIPreviewRequest{
		InputText: "给 Adult Ticket 增加 North Gate 检票点，每个点最多核销 2 次", IdempotencyKey: "catalog-ai-idempotency-1",
	})
	if err != nil {
		t.Fatalf("AI preview: %v", err)
	}
	if result.Preview == nil || len(result.Preview.Lines) != 1 || result.Preview.Operations[0].CheckpointIDs[0] != fixture.extra.ID {
		t.Fatalf("unexpected AI preview: %+v", result)
	}
	if result.Preview.Operations[0].MaxPerCheckIn == nil || *result.Preview.Operations[0].MaxPerCheckIn != 2 {
		t.Fatalf("AI limit was not normalized: %+v", result.Preview.Operations)
	}
	if result.Usage.RequestCount != 1 || calls.Load() != 1 {
		t.Fatalf("usage=%+v provider calls=%d, want one each", result.Usage, calls.Load())
	}

	repeated, err := service.PreviewWithAI(t.Context(), fixture.tenant.ID, 11, "admin", CatalogAIPreviewRequest{
		InputText: "给 Adult Ticket 增加 North Gate 检票点，每个点最多核销 2 次", IdempotencyKey: "catalog-ai-idempotency-1",
	})
	if err != nil || repeated.Preview.PlanID != result.Preview.PlanID {
		t.Fatalf("AI idempotency failed: preview=%+v err=%v", repeated, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("idempotent retry called provider %d times", calls.Load())
	}

	view, err := (&PlatformAIService{}).GetConfig()
	if err != nil {
		t.Fatalf("get AI config: %v", err)
	}
	if !view.APIKeyConfigured || strings.Contains(fmt.Sprintf("%+v", view), "test-provider-key") {
		t.Fatalf("AI key leaked from config view: %+v", view)
	}
	var stored model.PlatformAIConfig
	if err := model.DB.Where("config_key = ?", platformAIConfigKey).First(&stored).Error; err != nil {
		t.Fatalf("load stored AI config: %v", err)
	}
	if stored.APIKeyCiphertext == "" || stored.APIKeyCiphertext == "test-provider-key" {
		t.Fatal("AI provider key was not encrypted")
	}
}

func TestCatalogBatchAIPreviewEnforcesTenantRequestBudget(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, _ := startCatalogAIProvider(t, `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["North Gate"]}]}`)
	saveCatalogAIConfig(t, server.URL, 1)
	service := &CatalogBatchChangeService{}
	request := func(key string) error {
		_, err := service.PreviewWithAI(t.Context(), fixture.tenant.ID, 11, "admin", CatalogAIPreviewRequest{
			InputText: "给 Adult Ticket 增加 North Gate 检票点", IdempotencyKey: key,
		})
		return err
	}
	if err := request("catalog-ai-budget-1"); err != nil {
		t.Fatalf("first AI preview: %v", err)
	}
	if err := request("catalog-ai-budget-2"); err == nil || !strings.Contains(err.Error(), ErrAIBudgetExceeded.Error()) {
		t.Fatalf("second preview error=%v, want budget rejection", err)
	}
}

func TestCatalogBatchAIPreviewRejectsModelInventedAllProductsScope(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, _ := startCatalogAIProvider(t, `{"operations":[{"kind":"add_checkpoints","all_products":true,"checkpoint_names":["North Gate"]}]}`)
	saveCatalogAIConfig(t, server.URL, 5)
	_, err := (&CatalogBatchChangeService{}).PreviewWithAI(t.Context(), fixture.tenant.ID, 11, "admin", CatalogAIPreviewRequest{
		InputText: "给 Adult Ticket 增加 North Gate 检票点", IdempotencyKey: "catalog-ai-policy-all-products",
	})
	if err == nil || !strings.Contains(err.Error(), "全部票种") {
		t.Fatalf("model-invented broad scope was accepted: %v", err)
	}
}

func TestPlatformAITestConfigAcceptsSuccessfulEmptyProbeContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if _, present := body["max_tokens"]; present {
			http.Error(writer, "probe must use provider-default output budget", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	}))
	defer server.Close()

	input := PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: server.URL, Model: defaultAIModel, APIKey: "test-provider-key", Enabled: true,
		DefaultMonthlyRequestLimit: 5, DefaultMonthlyTokenLimit: 100000,
		RequestTimeoutSeconds: 5, MaxOutputTokens: 0, Temperature: 0,
	}
	if err := (&PlatformAIService{HTTPClient: server.Client()}).TestConfig(t.Context(), input); err != nil {
		t.Fatalf("connection test should use the provider-default output budget: %v", err)
	}
}

func TestPlatformAIChatUsesProviderDefaultOutputBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		if _, present := body["max_tokens"]; present {
			http.Error(writer, "max_tokens must be omitted in automatic mode", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{}"}}],"usage":{"total_tokens":32}}`)
	}))
	defer server.Close()

	content, actualTokens, err := (&PlatformAIService{HTTPClient: server.Client()}).chat(t.Context(), model.PlatformAIConfig{
		BaseURL: server.URL, Model: defaultAIModel, RequestTimeoutSeconds: 5, MaxOutputTokens: 0,
	}, "test-provider-key", []AIMessage{{Role: "user", Content: "生成计划"}}, 0)
	if err != nil || content != "{}" || actualTokens != 32 {
		t.Fatalf("provider-default chat content=%q tokens=%d err=%v", content, actualTokens, err)
	}
}

func TestValidatePlatformAIConfigAllowsProviderDefaultOutputTokens(t *testing.T) {
	valid := PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: "https://api.deepseek.com", Model: defaultAIModel,
		DefaultMonthlyRequestLimit: 5, DefaultMonthlyTokenLimit: 100000,
		RequestTimeoutSeconds: 5, MaxOutputTokens: 0, Temperature: 0,
	}
	if err := validatePlatformAIConfigInput(valid); err != nil {
		t.Fatalf("provider-default output budget should be valid: %v", err)
	}
	valid.MaxOutputTokens = -1
	if err := validatePlatformAIConfigInput(valid); err == nil || !strings.Contains(err.Error(), "zero or greater") {
		t.Fatalf("negative output budget error=%v, want non-negative validation", err)
	}
}

func TestAIOutputReservationTokensKeepsMonthlyGuardInAutomaticMode(t *testing.T) {
	if got := aiOutputReservationTokens(model.PlatformAIConfig{MaxOutputTokens: 0}); got != defaultAIOutputReservationTokens {
		t.Fatalf("automatic output reservation=%d, want %d", got, defaultAIOutputReservationTokens)
	}
	if got := aiOutputReservationTokens(model.PlatformAIConfig{MaxOutputTokens: 256}); got != 256 {
		t.Fatalf("explicit output reservation=%d, want 256", got)
	}
}

func TestPlatformAIChatStillRejectsEmptyPlannerContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	}))
	defer server.Close()

	_, _, err := (&PlatformAIService{HTTPClient: server.Client()}).chat(t.Context(), model.PlatformAIConfig{
		BaseURL: server.URL, Model: defaultAIModel, RequestTimeoutSeconds: 5, MaxOutputTokens: 256,
	}, "test-provider-key", []AIMessage{{Role: "user", Content: "生成计划"}}, 128)
	if err == nil || !strings.Contains(err.Error(), "AI provider returned an empty plan") {
		t.Fatalf("planner accepted empty provider content: %v", err)
	}
}

func TestPlatformAIChatExplainsReasoningBudgetExhaustion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"模型仍在推理"}}]}`)
	}))
	defer server.Close()

	_, _, err := (&PlatformAIService{HTTPClient: server.Client()}).chat(t.Context(), model.PlatformAIConfig{
		BaseURL: server.URL, Model: "deepseek-reasoner", RequestTimeoutSeconds: 5, MaxOutputTokens: 256,
	}, "test-provider-key", []AIMessage{{Role: "user", Content: "生成计划"}}, 256)
	if err == nil || !strings.Contains(err.Error(), "final answer") {
		t.Fatalf("reasoning budget exhaustion was not explained: %v", err)
	}
	var providerErr *AIProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("reasoning budget exhaustion was not classified as a provider error: %T", err)
	}
}

func TestPlatformAITestConfigFailsClosedOnProviderUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(writer, `{"error":{"message":"Authentication Fails"}}`)
	}))
	defer server.Close()

	err := (&PlatformAIService{HTTPClient: server.Client()}).TestConfig(t.Context(), PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: server.URL, Model: defaultAIModel, APIKey: "bad-key", Enabled: true,
		DefaultMonthlyRequestLimit: 5, DefaultMonthlyTokenLimit: 100000,
		RequestTimeoutSeconds: 5, MaxOutputTokens: 256, Temperature: 0,
	})
	if err == nil || !strings.Contains(err.Error(), "Authentication Fails") {
		t.Fatalf("provider unauthorized response was not surfaced: %v", err)
	}
}

func TestPlatformAIUsageConcurrentReservationsHonorBudget(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	config := model.PlatformAIConfig{DefaultMonthlyRequestLimit: 1, DefaultMonthlyTokenLimit: 1000}
	service := &PlatformAIService{}
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- service.ReserveUsage(fixture.tenant.ID, config, 100)
		}()
	}
	wait.Wait()
	close(results)
	var succeeded, budgetRejected int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), ErrAIBudgetExceeded.Error()):
			budgetRejected++
		default:
			t.Fatalf("unexpected concurrent reservation error: %v", err)
		}
	}
	if succeeded != 1 || budgetRejected != 1 {
		t.Fatalf("concurrent reservations succeeded=%d budget_rejected=%d, want 1/1", succeeded, budgetRejected)
	}
}
