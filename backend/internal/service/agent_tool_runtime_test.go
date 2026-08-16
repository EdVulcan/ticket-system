package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func toolProvider(t *testing.T, response func([]AIMessage) (map[string]interface{}, error)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []AIMessage        `json:"messages"`
			Tools    []AIToolDefinition `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		if len(body.Tools) == 0 {
			http.Error(writer, "tool registry missing", http.StatusBadRequest)
			return
		}
		payload, err := response(body.Messages)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(payload)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func toolCallPayload(id, name, arguments string, tokens int) map[string]interface{} {
	return map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"finish_reason": "tool_calls",
				"message": map[string]interface{}{
					"role": "assistant", "content": "",
					"tool_calls": []interface{}{
						map[string]interface{}{"id": id, "type": "function", "function": map[string]string{"name": name, "arguments": arguments}},
					},
				},
			},
		},
		"usage": map[string]interface{}{"total_tokens": tokens},
	}
}

func toolTextPayload(content string, tokens int) map[string]interface{} {
	return map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{"finish_reason": "stop", "message": map[string]interface{}{"role": "assistant", "content": content}},
		},
		"usage": map[string]interface{}{"total_tokens": tokens},
	}
}

func TestAgentToolRegistryContainsOnlyReadOrPreviewOperations(t *testing.T) {
	for _, spec := range agentToolSpecs {
		if !spec.ReadOnly && !spec.PreviewOnly {
			t.Fatalf("unsafe executable tool registered: %s", spec.Name)
		}
		if strings.Contains(spec.Name, "refund") || strings.Contains(spec.Name, "payment") || strings.Contains(spec.Name, "settlement") || strings.Contains(spec.Name, "channel") {
			t.Fatalf("high-risk tool leaked into initial registry: %s", spec.Name)
		}
	}
}

func TestAgentToolTaskQueriesOnlyCurrentTenantAndPersistsAudit(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		for _, message := range messages {
			if message.Role == "tool" {
				if !strings.Contains(message.Content, "Adult Ticket") || strings.Contains(message.Content, "Foreign Ticket") {
					return nil, fmt.Errorf("tool result leaked another tenant")
				}
				return toolTextPayload("当前租户有 Adult Ticket。", 18), nil
			}
		}
		return toolCallPayload("call-search-1", "search_ticket_products", `{"query":"Adult Ticket","limit":10}`, 12), nil
	})
	saveCatalogAIConfig(t, server.URL, 5)
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool protocol config: %v", err)
	}
	if err := model.DB.Create(&model.Tenant{Name: "Other Tenant", SystemCode: "OTHER-TOOL-TENANT", SecretKey: "other", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "product_operator", AgentTaskRequest{InputText: "查询当前票种 Adult Ticket", IdempotencyKey: "tool-query-1", TurnKey: "turn-1"})
	if err != nil {
		t.Fatalf("tool query task: %v", err)
	}
	if view.ProtocolMode != agentProtocolToolV1 || view.Message == "" {
		t.Fatalf("unexpected tool query view: %+v", view)
	}
	if calls.Load() != 2 {
		t.Fatalf("query should use one tool call and one final response, got %d provider calls", calls.Load())
	}
	var eventCount int64
	if err := model.DB.Model(&model.AgentTaskEvent{}).Where("task_id = ?", view.TaskID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 3 {
		t.Fatalf("tool audit events=%d, want provider start/final and tool event (3)", eventCount)
	}
}

func TestDeepSeekAutoConfigUsesToolProtocolForNewTasks(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		for _, message := range messages {
			if message.Role == "tool" {
				return toolTextPayload("当前租户有 Adult Ticket。", 18), nil
			}
		}
		return toolCallPayload("call-default-protocol", "search_ticket_products", `{"query":"Adult Ticket","limit":10}`, 12), nil
	})
	config := toolConfig(server.URL)
	config.AgentProtocolMode = agentProtocolAuto
	if _, err := (&PlatformAIService{}).SaveConfig(config, 77, "platform_admin"); err != nil {
		t.Fatalf("save automatic DeepSeek config: %v", err)
	}

	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "product_operator", AgentTaskRequest{
		InputText: "查询当前票种 Adult Ticket", IdempotencyKey: "tool-default-protocol-1", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("new task should use native tools for DeepSeek: %v", err)
	}
	if view.ProtocolMode != agentProtocolToolV1 || view.Message == "" || calls.Load() != 2 {
		t.Fatalf("unexpected default protocol behavior: view=%+v provider_calls=%d", view, calls.Load())
	}
}

func TestAgentToolTaskRejectsProviderTextWithoutSupportedToolCall(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, _ := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolTextPayload("我已经完成查询。", 12), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool protocol config: %v", err)
	}
	_, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "product_operator", AgentTaskRequest{
		InputText: "查询当前票种 Adult Ticket", IdempotencyKey: "tool-text-only-1", TurnKey: "turn-1",
	})
	if err == nil || !strings.Contains(err.Error(), "未调用受支持的查询或预览工具") {
		t.Fatalf("provider text without a tool call was accepted: %v", err)
	}
}

func toolConfig(baseURL string) PlatformAIConfigInput {
	return PlatformAIConfigInput{Provider: defaultAIProvider, BaseURL: baseURL, Model: defaultAIModel, APIKey: "test-provider-key", Enabled: true, AgentProtocolMode: agentProtocolToolV1, DefaultMonthlyRequestLimit: 5, DefaultMonthlyTokenLimit: 100000, RequestTimeoutSeconds: 5, MaxOutputTokens: 256, Temperature: 0}
}

func TestAgentToolTaskPreviewDoesNotExecuteBeforeConfirmation(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-preview-1", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["North Gate"],"max_per_check_in":1}]}`, 22), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "product_operator", AgentTaskRequest{InputText: "给 Adult Ticket 增加 North Gate 检票点", IdempotencyKey: "tool-preview-1", TurnKey: "turn-1"})
	if err != nil {
		t.Fatalf("tool preview task: %v", err)
	}
	if view.ProtocolMode != agentProtocolToolV1 || view.State != AgentTaskAwaitingConfirmation || view.PlanID == 0 || !view.CanConfirm {
		t.Fatalf("unexpected tool preview view: %+v", view)
	}
	if calls.Load() != 1 {
		t.Fatalf("preview should use one provider call, got %d", calls.Load())
	}
	var event model.AgentTaskEvent
	if err := model.DB.Where("task_id = ? AND tool_call_id = ?", view.TaskID, "call-preview-1").First(&event).Error; err != nil {
		t.Fatalf("tool audit event missing: %v", err)
	}
	if event.Status != "succeeded" || event.ToolName != "prepare_catalog_rule_change" {
		t.Fatalf("unexpected audit event: %+v", event)
	}
	var revisionCount int64
	if err := model.DB.Model(&model.ProductRevision{}).Where("product_id = ?", fixture.product.ID).Count(&revisionCount).Error; err != nil {
		t.Fatal(err)
	}
	if revisionCount != 1 {
		t.Fatalf("preview unexpectedly created product revision count=%d", revisionCount)
	}
}

func TestAgentToolEventsAllowSameProviderCallIDAcrossTaskAttempts(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	task := model.AgentTask{
		TenantID: fixture.tenant.ID, ActorUserID: 11, ActorRole: "product_operator",
		OperationType: AgentOperationPending, State: AgentTaskCollecting, InputText: "查询票种",
		ContextJSON: `{}`, MissingJSON: `[]`, IdempotencyKey: "tool-event-attempt-task", Version: 1,
		ExpiresAt: time.Now().Add(time.Hour), ProtocolMode: agentProtocolToolV1,
	}
	if err := model.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	base := model.AgentTaskEvent{TenantID: fixture.tenant.ID, TaskID: task.ID, ActorUserID: 11, ActorRole: task.ActorRole,
		EventType: "tool_call", ToolName: "search_ticket_products", ToolVersion: agentToolVersion, ToolCallID: "provider-call-reused",
		Status: "succeeded", ResultJSON: `{}`}
	first := base
	first.IdempotencyKey = "attempt-v1"
	if err := recordAgentToolEvent(first); err != nil {
		t.Fatalf("record first attempt: %v", err)
	}
	second := base
	second.IdempotencyKey = "attempt-v2"
	if err := recordAgentToolEvent(second); err != nil {
		t.Fatalf("record second attempt with reused provider call id: %v", err)
	}
	var count int64
	if err := model.DB.Model(&model.AgentTaskEvent{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count=%d, want 2", count)
	}
}
