package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestPlatformAITenantQuotaOverridesAreScopedAndAudited(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	other := model.Tenant{Name: "Quota Other", SystemCode: fmt.Sprintf("QUOTA-OTHER-%d", time.Now().UnixNano()), SecretKey: "other", Status: "active"}
	if err := model.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	defaultRequests := 10
	defaultTokens := int64(10000)
	if _, err := (&PlatformAIService{}).SaveConfig(PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: "https://api.deepseek.com", Model: defaultAIModel,
		APIKey: "quota-test-key", Enabled: true, DefaultMonthlyRequestLimit: defaultRequests,
		DefaultMonthlyTokenLimit: defaultTokens, RequestTimeoutSeconds: 5, Temperature: 0.1,
	}, 77, "platform_admin"); err != nil {
		t.Fatal(err)
	}
	requestLimit := 2
	tokenLimit := int64(2500)
	updated, err := (&PlatformAIService{}).UpdateTenantQuotaPolicy(fixture.tenant.ID, AITenantQuotaPolicyInput{
		MonthlyRequestLimit: &requestLimit, MonthlyTokenLimit: &tokenLimit, Enabled: true, Reason: "quota isolation test",
	}, 77, "platform_admin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.MonthlyRequestLimit != requestLimit || updated.MonthlyTokenLimit != tokenLimit || updated.RequestLimitInherited || updated.TokenLimitInherited {
		t.Fatalf("unexpected updated quota view: %+v", updated)
	}
	period := time.Now().Format("2006-01")
	if err := model.DB.Create(&model.AIUsageMonth{TenantID: fixture.tenant.ID, Period: period, RequestCount: 1, TokenCount: 500}).Error; err != nil {
		t.Fatal(err)
	}
	page, err := (&PlatformAIService{}).ListTenantQuotaPolicies(period, "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	var first, second *AITenantQuotaPolicyView
	for i := range page.Data {
		row := &page.Data[i]
		if row.TenantID == fixture.tenant.ID {
			first = row
		}
		if row.TenantID == other.ID {
			second = row
		}
	}
	if first == nil || second == nil {
		t.Fatalf("quota list omitted tenants: %+v", page.Data)
	}
	if first.RequestCount != 1 || first.RequestsRemaining != 1 || first.TokensRemaining != 2000 {
		t.Fatalf("usage was not calculated against tenant override: %+v", first)
	}
	if !second.RequestLimitInherited || !second.TokenLimitInherited || second.MonthlyRequestLimit != defaultRequests || second.MonthlyTokenLimit != defaultTokens {
		t.Fatalf("other tenant did not inherit platform defaults: %+v", second)
	}
	var audit model.AuditLog
	if err := model.DB.Where("tenant_id = ? AND action = ?", fixture.tenant.ID, "platform.ai_tenant_quota.update").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.Scope != "platform" || audit.Reason != "quota isolation test" || !strings.Contains(audit.AfterJSON, `"monthly_request_limit":2`) {
		t.Fatalf("quota audit is incomplete: %+v", audit)
	}
}

func TestPlatformAITenantQuotaPauseAndLoweringDoNotRewriteUsage(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	if _, err := (&PlatformAIService{}).SaveConfig(PlatformAIConfigInput{
		Provider: defaultAIProvider, BaseURL: "https://api.deepseek.com", Model: defaultAIModel,
		APIKey: "quota-test-key", Enabled: true, DefaultMonthlyRequestLimit: 10,
		DefaultMonthlyTokenLimit: 10000, RequestTimeoutSeconds: 5, Temperature: 0.1,
	}, 77, "platform_admin"); err != nil {
		t.Fatal(err)
	}
	config := model.PlatformAIConfig{DefaultMonthlyRequestLimit: 10, DefaultMonthlyTokenLimit: 10000}
	service := &PlatformAIService{}
	if err := service.ReserveUsage(fixture.tenant.ID, config, 500); err != nil {
		t.Fatal(err)
	}
	if err := service.ReserveUsage(fixture.tenant.ID, config, 500); err != nil {
		t.Fatal(err)
	}
	requestLimit := 1
	if _, err := service.UpdateTenantQuotaPolicy(fixture.tenant.ID, AITenantQuotaPolicyInput{MonthlyRequestLimit: &requestLimit, Enabled: true, Reason: "lower without clearing ledger"}, 77, "platform_admin"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReserveUsage(fixture.tenant.ID, config, 1); !errors.Is(err, ErrAIBudgetExceeded) {
		t.Fatalf("lowered quota reservation error=%v, want budget exceeded", err)
	}
	var usage model.AIUsageMonth
	if err := model.DB.Where("tenant_id = ? AND period = ?", fixture.tenant.ID, time.Now().Format("2006-01")).First(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if usage.RequestCount != 2 || usage.TokenCount != 1000 {
		t.Fatalf("lowering quota rewrote usage: %+v", usage)
	}
	if _, err := service.UpdateTenantQuotaPolicy(fixture.tenant.ID, AITenantQuotaPolicyInput{Enabled: false, Reason: "temporarily pause tenant AI"}, 77, "platform_admin"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReserveUsage(fixture.tenant.ID, config, 1); !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("paused tenant reservation error=%v, want unavailable", err)
	}
	status, err := service.Availability(fixture.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.Reason != "本租户 AI 已暂停" {
		t.Fatalf("paused tenant availability=%+v", status)
	}
}

func TestPlatformAITenantQuotaMissingTenantIsRejected(t *testing.T) {
	_, err := (&PlatformAIService{}).UpdateTenantQuotaPolicy(999999, AITenantQuotaPolicyInput{Enabled: true, Reason: "missing tenant"}, 77, "platform_admin")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing tenant error=%v, want not found", err)
	}
}
