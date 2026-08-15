package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	platformAIConfigKey = "default"
	defaultAIProvider   = "deepseek"
	defaultAIBaseURL    = "https://api.deepseek.com"
	defaultAIModel      = "deepseek-chat"
)

var (
	ErrAIUnavailable    = errors.New("AI 助手当前不可用")
	ErrAIBudgetExceeded = errors.New("本租户本月 AI 用量已达到平台额度")
)

// PlatformAIConfigInput is accepted only by platform administrators. The API
// key is write-only; an empty key keeps the encrypted value already stored.
type PlatformAIConfigInput struct {
	Provider                   string  `json:"provider"`
	BaseURL                    string  `json:"base_url"`
	Model                      string  `json:"model"`
	APIKey                     string  `json:"api_key"`
	Enabled                    bool    `json:"enabled"`
	DefaultMonthlyRequestLimit int     `json:"default_monthly_request_limit"`
	DefaultMonthlyTokenLimit   int64   `json:"default_monthly_token_limit"`
	RequestTimeoutSeconds      int     `json:"request_timeout_seconds"`
	MaxOutputTokens            int     `json:"max_output_tokens"`
	Temperature                float64 `json:"temperature"`
}

type PlatformAIConfigView struct {
	ID                         uint       `json:"id"`
	ConfigKey                  string     `json:"config_key"`
	Provider                   string     `json:"provider"`
	BaseURL                    string     `json:"base_url"`
	Model                      string     `json:"model"`
	APIKeyConfigured           bool       `json:"api_key_configured"`
	Enabled                    bool       `json:"enabled"`
	DefaultMonthlyRequestLimit int        `json:"default_monthly_request_limit"`
	DefaultMonthlyTokenLimit   int64      `json:"default_monthly_token_limit"`
	RequestTimeoutSeconds      int        `json:"request_timeout_seconds"`
	MaxOutputTokens            int        `json:"max_output_tokens"`
	Temperature                float64    `json:"temperature"`
	ConfigVersion              int        `json:"config_version"`
	LastTestedAt               *time.Time `json:"last_tested_at,omitempty"`
	LastTestStatus             string     `json:"last_test_status,omitempty"`
	LastTestError              string     `json:"last_test_error,omitempty"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

type AIAvailabilityView struct {
	Enabled             bool   `json:"enabled"`
	Provider            string `json:"provider,omitempty"`
	Model               string `json:"model,omitempty"`
	RequestsRemaining   int    `json:"requests_remaining"`
	TokensRemaining     int64  `json:"tokens_remaining"`
	MonthlyRequestLimit int    `json:"monthly_request_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit"`
	Reason              string `json:"reason,omitempty"`
}

type AIUsageView struct {
	RequestCount int   `json:"request_count"`
	TokenCount   int64 `json:"token_count"`
}

type PlatformAIService struct {
	HTTPClient *http.Client
}

func (s *PlatformAIService) client() *http.Client {
	if s != nil && s.HTTPClient != nil {
		return s.HTTPClient
	}
	return &http.Client{}
}

func defaultPlatformAIConfig() model.PlatformAIConfig {
	return model.PlatformAIConfig{
		ConfigKey: platformAIConfigKey, Provider: defaultAIProvider, BaseURL: defaultAIBaseURL,
		Model: defaultAIModel, DefaultMonthlyRequestLimit: 100, DefaultMonthlyTokenLimit: 200000,
		RequestTimeoutSeconds: 30, MaxOutputTokens: 1200, Temperature: 0.1, ConfigVersion: 1,
	}
}

func platformAIConfigView(config model.PlatformAIConfig) PlatformAIConfigView {
	return PlatformAIConfigView{
		ID: config.ID, ConfigKey: config.ConfigKey, Provider: config.Provider, BaseURL: config.BaseURL,
		Model: config.Model, APIKeyConfigured: strings.TrimSpace(config.APIKeyCiphertext) != "",
		Enabled: config.Enabled, DefaultMonthlyRequestLimit: config.DefaultMonthlyRequestLimit,
		DefaultMonthlyTokenLimit: config.DefaultMonthlyTokenLimit, RequestTimeoutSeconds: config.RequestTimeoutSeconds,
		MaxOutputTokens: config.MaxOutputTokens, Temperature: config.Temperature, ConfigVersion: config.ConfigVersion,
		LastTestedAt: config.LastTestedAt, LastTestStatus: config.LastTestStatus, LastTestError: config.LastTestError,
		UpdatedAt: config.UpdatedAt,
	}
}

func (s *PlatformAIService) GetConfig() (*PlatformAIConfigView, error) {
	config := defaultPlatformAIConfig()
	err := model.DB.Where("config_key = ?", platformAIConfigKey).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		view := platformAIConfigView(config)
		return &view, nil
	}
	if err != nil {
		return nil, err
	}
	view := platformAIConfigView(config)
	return &view, nil
}

func (s *PlatformAIService) SaveConfig(input PlatformAIConfigInput, actorID uint, actorRole string) (*PlatformAIConfigView, error) {
	if err := validatePlatformAIConfigInput(input); err != nil {
		return nil, err
	}
	returnView := (*PlatformAIConfigView)(nil)
	err := model.Write(func(tx *gorm.DB) error {
		var existing model.PlatformAIConfig
		err := tx.Where("config_key = ?", platformAIConfigKey).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			existing = defaultPlatformAIConfig()
		} else if err != nil {
			return err
		}
		before := platformAIConfigView(existing)
		candidate := existing
		candidate.ConfigKey = platformAIConfigKey
		candidate.Provider = normalizeAIProvider(input.Provider)
		candidate.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
		candidate.Model = strings.TrimSpace(input.Model)
		candidate.Enabled = input.Enabled
		candidate.DefaultMonthlyRequestLimit = input.DefaultMonthlyRequestLimit
		candidate.DefaultMonthlyTokenLimit = input.DefaultMonthlyTokenLimit
		candidate.RequestTimeoutSeconds = input.RequestTimeoutSeconds
		candidate.MaxOutputTokens = input.MaxOutputTokens
		candidate.Temperature = input.Temperature
		candidate.ConfigVersion = 1
		if existing.ID != 0 {
			candidate.ConfigVersion = existing.ConfigVersion + 1
		}
		candidate.UpdatedBy = actorID
		if strings.TrimSpace(input.APIKey) != "" {
			ciphertext, err := utils.EncryptAES(strings.TrimSpace(input.APIKey))
			if err != nil {
				return fmt.Errorf("encrypt AI provider key: %w", err)
			}
			candidate.APIKeyCiphertext = ciphertext
		}
		if existing.ID == 0 {
			candidate.ID = 0
			if err := tx.Create(&candidate).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&existing).Updates(map[string]interface{}{
			"provider": candidate.Provider, "base_url": candidate.BaseURL, "model": candidate.Model,
			"api_key_ciphertext": candidate.APIKeyCiphertext, "enabled": candidate.Enabled,
			"default_monthly_request_limit": candidate.DefaultMonthlyRequestLimit,
			"default_monthly_token_limit":   candidate.DefaultMonthlyTokenLimit,
			"request_timeout_seconds":       candidate.RequestTimeoutSeconds, "max_output_tokens": candidate.MaxOutputTokens,
			"temperature": candidate.Temperature, "config_version": candidate.ConfigVersion, "updated_by": actorID,
			"last_tested_at": nil, "last_test_status": "", "last_test_error": "",
		}).Error; err != nil {
			return err
		} else {
			candidate.ID = existing.ID
			candidate.CreatedAt = existing.CreatedAt
			candidate.UpdatedAt = time.Now()
		}
		if err := recordAuditTx(tx, actorID, 0, actorRole, "platform", "platform.ai_config.update", "platform_ai_config", candidate.ID,
			"update platform AI provider configuration", mustJSON(before), mustJSON(platformAIConfigView(candidate))); err != nil {
			return err
		}
		view := platformAIConfigView(candidate)
		returnView = &view
		return nil
	})
	if err != nil {
		return nil, err
	}
	return returnView, nil
}

func (s *PlatformAIService) TestConfig(ctx context.Context, input PlatformAIConfigInput) error {
	if err := validatePlatformAIConfigInput(input); err != nil {
		return err
	}
	config := defaultPlatformAIConfig()
	if err := model.DB.Where("config_key = ?", platformAIConfigKey).First(&config).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if strings.TrimSpace(input.Provider) != "" {
		config.Provider = normalizeAIProvider(input.Provider)
	}
	if strings.TrimSpace(input.BaseURL) != "" {
		config.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	}
	if strings.TrimSpace(input.Model) != "" {
		config.Model = strings.TrimSpace(input.Model)
	}
	if input.RequestTimeoutSeconds > 0 {
		config.RequestTimeoutSeconds = input.RequestTimeoutSeconds
	}
	if input.MaxOutputTokens > 0 {
		config.MaxOutputTokens = input.MaxOutputTokens
	}
	if input.Temperature >= 0 {
		config.Temperature = input.Temperature
	}
	key := strings.TrimSpace(input.APIKey)
	if key == "" {
		if config.APIKeyCiphertext == "" {
			return ErrAIUnavailable
		}
		var err error
		key, err = utils.DecryptAES(config.APIKeyCiphertext)
		if err != nil {
			return fmt.Errorf("decrypt AI provider key: %w", err)
		}
	}
	// Keep the probe bounded: connection testing must not consume a full agent
	// response budget, while reasoning models still need room for internal work.
	testMaxTokens := config.MaxOutputTokens
	if testMaxTokens < 128 {
		testMaxTokens = 128
	}
	if testMaxTokens > 256 {
		testMaxTokens = 256
	}
	return s.probe(ctx, config, key, []AIMessage{{Role: "system", Content: "只回复 OK。"}, {Role: "user", Content: "连接测试"}}, testMaxTokens)
}

func (s *PlatformAIService) loadActiveConfig() (model.PlatformAIConfig, string, error) {
	var config model.PlatformAIConfig
	if err := model.DB.Where("config_key = ? AND enabled = ?", platformAIConfigKey, true).First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return config, "", ErrAIUnavailable
		}
		return config, "", err
	}
	if strings.TrimSpace(config.APIKeyCiphertext) == "" {
		return config, "", ErrAIUnavailable
	}
	key, err := utils.DecryptAES(config.APIKeyCiphertext)
	if err != nil || strings.TrimSpace(key) == "" {
		return config, "", ErrAIUnavailable
	}
	return config, key, nil
}

func (s *PlatformAIService) Availability(tenantID uint) (*AIAvailabilityView, error) {
	result := &AIAvailabilityView{}
	config, _, err := s.loadActiveConfig()
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Enabled = true
	result.Provider = config.Provider
	result.Model = config.Model
	result.MonthlyRequestLimit = config.DefaultMonthlyRequestLimit
	result.MonthlyTokenLimit = config.DefaultMonthlyTokenLimit
	usage, err := aiUsageForPeriod(tenantID, time.Now().Format("2006-01"))
	if err != nil {
		return nil, err
	}
	result.RequestsRemaining = nonNegativeInt(config.DefaultMonthlyRequestLimit - usage.RequestCount)
	result.TokensRemaining = nonNegativeInt64(config.DefaultMonthlyTokenLimit - usage.TokenCount)
	if result.RequestsRemaining == 0 || result.TokensRemaining == 0 {
		result.Enabled = false
		result.Reason = ErrAIBudgetExceeded.Error()
	}
	return result, nil
}

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequest struct {
	Model       string      `json:"model"`
	Messages    []AIMessage `json:"messages"`
	Temperature float64     `json:"temperature,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Stream      bool        `json:"stream"`
}

type aiChatResponse struct {
	Choices []struct {
		Message AIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *PlatformAIService) chat(ctx context.Context, config model.PlatformAIConfig, apiKey string, messages []AIMessage, maxTokens int) (string, int64, error) {
	return s.chatWithContentRequirement(ctx, config, apiKey, messages, maxTokens, true)
}

func (s *PlatformAIService) probe(ctx context.Context, config model.PlatformAIConfig, apiKey string, messages []AIMessage, maxTokens int) error {
	_, _, err := s.chatWithContentRequirement(ctx, config, apiKey, messages, maxTokens, false)
	return err
}

func (s *PlatformAIService) chatWithContentRequirement(ctx context.Context, config model.PlatformAIConfig, apiKey string, messages []AIMessage, maxTokens int, requireContent bool) (string, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint, err := aiCompletionEndpoint(config.BaseURL)
	if err != nil {
		return "", 0, err
	}
	if maxTokens <= 0 || maxTokens > config.MaxOutputTokens {
		maxTokens = config.MaxOutputTokens
	}
	body, err := json.Marshal(aiChatRequest{Model: config.Model, Messages: messages, Temperature: config.Temperature, MaxTokens: maxTokens, Stream: false})
	if err != nil {
		return "", 0, err
	}
	timeout := time.Duration(config.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.client().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("AI provider request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", 0, err
	}
	var decoded aiChatResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return "", 0, fmt.Errorf("AI provider returned invalid JSON: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := fmt.Sprintf("AI provider returned HTTP %d", response.StatusCode)
		if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			message = decoded.Error.Message
		}
		return "", decoded.Usage.TotalTokens, errors.New(message)
	}
	if len(decoded.Choices) == 0 {
		return "", decoded.Usage.TotalTokens, errors.New("AI provider returned no choices")
	}
	if requireContent && strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return "", decoded.Usage.TotalTokens, errors.New("AI provider returned an empty plan")
	}
	return decoded.Choices[0].Message.Content, decoded.Usage.TotalTokens, nil
}

func aiCompletionEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("AI provider base URL is invalid")
	}
	if parsed.Scheme != "https" && !isLocalAIHost(parsed.Hostname()) {
		return "", errors.New("AI provider base URL must use HTTPS")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/chat/completions") {
		path += "/chat/completions"
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func isLocalAIHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func validatePlatformAIConfigInput(input PlatformAIConfigInput) error {
	provider := normalizeAIProvider(input.Provider)
	if provider != "deepseek" && provider != "openai_compatible" {
		return errors.New("unsupported AI provider")
	}
	if strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.Model) == "" {
		return errors.New("AI provider base URL and model are required")
	}
	if _, err := aiCompletionEndpoint(input.BaseURL); err != nil {
		return err
	}
	if input.DefaultMonthlyRequestLimit < 1 || input.DefaultMonthlyRequestLimit > 1000000 {
		return errors.New("monthly AI request limit must be between 1 and 1000000")
	}
	if input.DefaultMonthlyTokenLimit < 1000 || input.DefaultMonthlyTokenLimit > 1000000000 {
		return errors.New("monthly AI token limit is invalid")
	}
	if input.RequestTimeoutSeconds < 5 || input.RequestTimeoutSeconds > 120 {
		return errors.New("AI request timeout must be between 5 and 120 seconds")
	}
	if input.MaxOutputTokens < 128 || input.MaxOutputTokens > 8192 {
		return errors.New("AI max output tokens must be between 128 and 8192")
	}
	if input.Temperature < 0 || input.Temperature > 2 {
		return errors.New("AI temperature must be between 0 and 2")
	}
	if input.Enabled && strings.TrimSpace(input.APIKey) == "" {
		var existing model.PlatformAIConfig
		err := model.DB.Where("config_key = ?", platformAIConfigKey).First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load existing AI provider key: %w", err)
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || existing.APIKeyCiphertext == "" {
			return errors.New("enabled AI provider requires an API key")
		}
	}
	return nil
}

func normalizeAIProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return defaultAIProvider
	}
	return provider
}

func (s *PlatformAIService) ReserveUsage(tenantID uint, config model.PlatformAIConfig, estimatedTokens int64) error {
	if tenantID == 0 {
		return errors.New("tenant is required")
	}
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	period := time.Now().Format("2006-01")
	return model.Write(func(tx *gorm.DB) error {
		var usage model.AIUsageMonth
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND period = ?", tenantID, period).First(&usage).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Seed the unique row before locking it. ON CONFLICT closes the
			// first-request race between two workers for the same tenant/month.
			seed := model.AIUsageMonth{TenantID: tenantID, Period: period}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND period = ?", tenantID, period).First(&usage).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if usage.RequestCount+1 > config.DefaultMonthlyRequestLimit || usage.TokenCount+estimatedTokens > config.DefaultMonthlyTokenLimit {
			return ErrAIBudgetExceeded
		}
		usage.RequestCount++
		usage.TokenCount += estimatedTokens
		return tx.Model(&usage).Updates(map[string]interface{}{"request_count": usage.RequestCount, "token_count": usage.TokenCount}).Error
	})
}

func (s *PlatformAIService) ReconcileUsage(tenantID uint, reservedTokens, actualTokens int64) error {
	delta := actualTokens - reservedTokens
	if delta == 0 {
		return nil
	}
	period := time.Now().Format("2006-01")
	return model.Write(func(tx *gorm.DB) error {
		var usage model.AIUsageMonth
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND period = ?", tenantID, period).First(&usage).Error; err != nil {
			return err
		}
		next := usage.TokenCount + delta
		if next < 0 {
			next = 0
		}
		return tx.Model(&usage).Update("token_count", next).Error
	})
}

func aiUsageForPeriod(tenantID uint, period string) (AIUsageView, error) {
	var usage model.AIUsageMonth
	err := model.DB.Where("tenant_id = ? AND period = ?", tenantID, period).First(&usage).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AIUsageView{}, nil
	}
	if err != nil {
		return AIUsageView{}, err
	}
	return AIUsageView{RequestCount: usage.RequestCount, TokenCount: usage.TokenCount}, nil
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func mustJSON(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
