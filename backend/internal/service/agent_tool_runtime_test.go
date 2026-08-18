package service

import (
	"encoding/json"
	"errors"
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
		if strings.Contains(spec.Name, "refund") || strings.Contains(spec.Name, "payment") || strings.Contains(spec.Name, "channel") ||
			(strings.Contains(spec.Name, "settlement") && spec.ActionKind != "query") {
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
	var persisted agentQueryResultSet
	if len(view.Result) == 0 || json.Unmarshal(view.Result, &persisted) != nil || len(persisted.QueryResults) != 1 {
		t.Fatalf("query result was not persisted on task view: %s", string(view.Result))
	}
	var result agentQueryResult
	if err := json.Unmarshal(persisted.QueryResults[0], &result); err != nil || result.Tool != "search_ticket_products" || result.Returned == 0 {
		t.Fatalf("unexpected persisted query result: %s", string(persisted.QueryResults[0]))
	}
	if calls.Load() != 2 {
		t.Fatalf("query should use one tool call and one final response, got %d provider calls", calls.Load())
	}
	var eventCount int64
	if err := model.DB.Model(&model.AgentTaskEvent{}).Where("task_id = ?", view.TaskID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 4 {
		t.Fatalf("tool audit events=%d, want planning, provider start/final and tool event (4)", eventCount)
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

func TestAgentProductCreateScopesPlannerToPrepareTool(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var providerCalls atomic.Int32
	var receivedToolChoice json.RawMessage
	var receivedTools []AIToolDefinition
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerCalls.Add(1)
		var body struct {
			ToolChoice json.RawMessage    `json:"tool_choice"`
			Tools      []AIToolDefinition `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		receivedToolChoice = append(receivedToolChoice[:0], body.ToolChoice...)
		receivedTools = append(receivedTools[:0], body.Tools...)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(toolCallPayload("call-product-prepare", "prepare_ticket_product_create", `{"name":"Child Ticket","product_type":"online","scenic_area_name":"Batch Scenic","price":55,"settlement_price":30,"groups":[{"group_name":"Admission","items":[{"checkpoint_name":"Main Gate","max_per_check_in":1}]}]}`, 24))
	}))
	defer server.Close()
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText:      "创建一个线上 Child Ticket，所属景区 Batch Scenic，售价 55 元，结算价 30 元，使用 Main Gate",
		IdempotencyKey: "tool-product-forced-prepare", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("product preview task: %v", err)
	}
	if view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm || providerCalls.Load() != 1 {
		t.Fatalf("product task did not converge to preview: view=%+v provider_calls=%d", view, providerCalls.Load())
	}
	var choice map[string]interface{}
	if err := json.Unmarshal(receivedToolChoice, &choice); err != nil {
		t.Fatalf("creation planner returned invalid tool choice: %s", string(receivedToolChoice))
	}
	if choice["type"] != "function" {
		t.Fatalf("creation planner must force its sole visible tool: %s", string(receivedToolChoice))
	}
	function, _ := choice["function"].(map[string]interface{})
	if function["name"] != "prepare_ticket_product_create" {
		t.Fatalf("creation planner forced the wrong tool: %s", string(receivedToolChoice))
	}
	if len(receivedTools) != 1 || receivedTools[0].Function.Name != "prepare_ticket_product_create" {
		t.Fatalf("creation planner exposed unrelated tools: %+v", receivedTools)
	}
}

func TestAgentDeepSeekThinkingToolChoiceFallsBackToAuto(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		if string(body.ToolChoice) != `"auto"` {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":{"message":"Thinking mode does not support this tool_choice"}}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(toolCallPayload("call-thinking-fallback", "search_ticket_products", `{"query":"Adult Ticket","limit":10}`, 18))
	}))
	defer server.Close()
	config := model.PlatformAIConfig{Provider: defaultAIProvider, BaseURL: server.URL, Model: defaultAIModel, RequestTimeoutSeconds: 5}
	choice := map[string]interface{}{"type": "function", "function": map[string]string{"name": "search_ticket_products"}}
	result, err := (&PlatformAIService{}).chatWithToolsChoice(t.Context(), config, "test-provider-key", []AIMessage{{Role: "user", Content: "查询票种"}}, []AIToolDefinition{{Type: "function", Function: AIToolFunction{Name: "search_ticket_products"}}}, 256, choice)
	if err != nil {
		t.Fatalf("thinking-mode tool choice fallback failed: %v", err)
	}
	if result == nil || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].Function.Name != "search_ticket_products" {
		t.Fatalf("unexpected fallback result: %+v", result)
	}
	if calls.Load() != 2 {
		t.Fatalf("fallback physical calls=%d, want 2", calls.Load())
	}
}

func TestAgentRejectsProviderToolOutsideTaskRegistry(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, _ := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return toolCallPayload("call-unexposed-rule", "prepare_catalog_rule_change", `{"operations":[{"kind":"add_checkpoints","product_names":["Adult Ticket"],"checkpoint_names":["Main Gate"]}]}`, 12), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	_, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "创建一个线上 Child Ticket，售价 55 元，结算价 30 元", IdempotencyKey: "tool-registry-guard", TurnKey: "turn-1",
	})
	var invalid *AgentTaskError
	if !errors.As(err, &invalid) || invalid.HTTPStatus != http.StatusBadRequest || !strings.Contains(invalid.Message, "未开放的工具") {
		t.Fatalf("provider tool outside the task registry was not rejected: %v", err)
	}
	var products int64
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND name = ?", fixture.tenant.ID, "Child Ticket").Count(&products).Error; err != nil {
		t.Fatal(err)
	}
	if products != 0 {
		t.Fatalf("unexposed provider tool changed the catalog: %d products", products)
	}
}

func TestAgentProductCreateRetriesInvalidToolArguments(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var providerCalls atomic.Int32
	var retryFeedbackSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		callNumber := providerCalls.Add(1)
		var body struct {
			Messages []AIMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		for _, message := range body.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, `"retryable":true`) {
				retryFeedbackSeen.Store(true)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		if callNumber == 1 {
			_ = json.NewEncoder(writer).Encode(toolCallPayload("call-product-invalid", "prepare_ticket_product_create", "not-json", 24))
			return
		}
		_ = json.NewEncoder(writer).Encode(toolCallPayload("call-product-retry", "prepare_ticket_product_create", `{"name":"Child Ticket","product_type":"online","scenic_area_name":"Batch Scenic","price":55,"settlement_price":30,"groups":[{"group_name":"Admission","items":[{"checkpoint_name":"Main Gate","max_per_check_in":1}]}]}`, 24))
	}))
	defer server.Close()
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText:      "创建一个线上 Child Ticket，所属景区 Batch Scenic，售价 55 元，结算价 30 元，使用 Main Gate",
		IdempotencyKey: "tool-product-invalid-retry", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("invalid tool arguments should be recoverable: %v", err)
	}
	if view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm || providerCalls.Load() != 2 || !retryFeedbackSeen.Load() {
		t.Fatalf("product task did not recover from invalid arguments: view=%+v provider_calls=%d retry_feedback=%v", view, providerCalls.Load(), retryFeedbackSeen.Load())
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

func TestAgentCreationAllowsNegatedListingConstraints(t *testing.T) {
	for _, input := range []string{
		"创建一个线上票，售价 10 元，结算价 5 元，不上架、不分销",
		"新建一个窗口票，先不上架，也不要分销",
	} {
		if err := rejectUnsupportedAgentCapability(input); err != nil {
			t.Fatalf("creation constraint %q was rejected: %v", input, err)
		}
	}
	if err := rejectUnsupportedAgentCapability("把现有成人票上架"); err == nil {
		t.Fatal("affirmative listing mutation was accepted")
	}
}

func TestAgentReadOnlyToolRouteKeepsSingleTopicDeterministic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"查询最近订单", "search_orders"},
		{"查询成人票的库存", "query_ticket_inventory"},
		{"查询水上乐园的检票点", "search_checkpoints"},
		{"查看分销关系", "query_distribution_partners"},
		{"查询分销授权状态", "query_distribution_products"},
		{"查看授权商品", "query_distribution_products"},
		{"查看团队合同", "query_team_contracts"},
		{"查询团队入园情况", "query_team_groups"},
		{"查看票种规则", "get_ticket_product_rules"},
	}
	for _, tc := range cases {
		route := agentReadOnlyToolRoute(tc.input)
		if len(route) != 1 || route[0] != tc.want {
			t.Fatalf("input %q routed to %v, want %s", tc.input, route, tc.want)
		}
	}
	if agentReadOnlyCompoundIntent("查询水上乐园的景区检票点") {
		t.Fatal("a scenic-area qualifier plus checkpoint noun must not become a compound query")
	}
	for _, input := range []string{"查询最近30天的核销汇总", "查看水上乐园的检票点", "查询票种的核销规则"} {
		if agentHasCatalogRuleMutationIntent(input) {
			t.Fatalf("read-only request was classified as a catalog mutation: %q", input)
		}
	}
	if !agentReadOnlyCompoundIntent("依次查询订单、库存和销售汇总") {
		t.Fatal("explicit multi-topic query was not recognized as compound")
	}
}

func TestAgentSingleTopicReadUsesServerAdapterWithoutProvider(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	server, calls := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return nil, fmt.Errorf("provider must not be called for a deterministic single-topic read")
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "查询最近30天的核销汇总", IdempotencyKey: "direct-verification-summary", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("deterministic verification query: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("single-topic query called provider %d times", calls.Load())
	}
	if !strings.Contains(view.Message, "核销汇总查询") {
		t.Fatalf("direct query response omitted tool label: %q", view.Message)
	}
	var resultSet agentQueryResultSet
	if len(view.Result) == 0 || json.Unmarshal(view.Result, &resultSet) != nil || len(resultSet.QueryResults) != 1 {
		t.Fatalf("direct query result was not persisted: %s", string(view.Result))
	}
	var result agentQueryResult
	if err := json.Unmarshal(resultSet.QueryResults[0], &result); err != nil || result.Tool != "query_verification_summary" {
		t.Fatalf("unexpected direct query result: %s", string(resultSet.QueryResults[0]))
	}
	if args := directAgentQueryArguments("查询核销汇总，日期 2026-08-01 至 2026-08-17"); !strings.Contains(args, `"start_date":"2026-08-01"`) || !strings.Contains(args, `"end_date":"2026-08-17"`) {
		t.Fatalf("explicit report dates were not preserved: %s", args)
	}
}

func TestAgentRejectsUnsupportedCapabilityBeforeTaskCreation(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var before int64
	if err := model.DB.Model(&model.AgentTask{}).Where("tenant_id = ?", fixture.tenant.ID).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	_, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "查询最近订单并直接退款", IdempotencyKey: "unsupported-before-task", TurnKey: "turn-1",
	})
	var taskErr *AgentTaskError
	if !errors.As(err, &taskErr) || taskErr.Code != "invalid_request" || !strings.Contains(taskErr.Message, "未开放") {
		t.Fatalf("unsupported capability returned the wrong error: %v", err)
	}
	var after int64
	if err := model.DB.Model(&model.AgentTask{}).Where("tenant_id = ?", fixture.tenant.ID).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("unsupported request created an agent task: before=%d after=%d", before, after)
	}
}

func TestAgentAllowsTicketRefundPolicyButRejectsScriptInput(t *testing.T) {
	if err := rejectUnsupportedAgentCapability("把成人票设置为未核销随时退"); err != nil {
		t.Fatalf("ticket refund policy was rejected as payment refund: %v", err)
	}
	if err := rejectUnsupportedAgentCapability("查询分销授权状态"); err != nil {
		t.Fatalf("read-only authorization status was rejected: %v", err)
	}
	if err := rejectUnsupportedAgentCapability("查询支付配置"); err == nil {
		t.Fatal("payment configuration query was accepted without a supported tool")
	}
	if err := rejectUnsupportedAgentCapability("查询订单 <script>alert(1)</script>"); err == nil || !strings.Contains(err.Error(), "SQL") {
		t.Fatalf("script input was not rejected: %v", err)
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
