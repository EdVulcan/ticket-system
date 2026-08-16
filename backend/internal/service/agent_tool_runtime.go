package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	agentToolVersion        = "1"
	maxAgentToolCalls       = 6
	maxAgentProviderCalls   = 3
	maxAgentToolResultBytes = 64 << 10
)

type agentToolSpec struct {
	Name                 string
	Description          string
	ModuleID             string
	ActionKind           string
	Permission           string
	Capability           string
	CapabilityAny        []string
	BusinessType         string
	ReadOnly             bool
	PreviewOnly          bool
	RequiresConfirmation bool
	Parameters           json.RawMessage
}

type agentToolExecution struct {
	ResultJSON string
	Planning   *agentPlanningResult
}

var agentToolSpecs = []agentToolSpec{
	{Name: "search_scenic_areas", Description: "查询当前租户可用的景区名称", ModuleID: agentModuleCatalog, ActionKind: "query", Permission: authz.PermissionOnsiteRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "search_checkpoints", Description: "查询当前租户景区下的检票点名称", ModuleID: agentModuleCatalog, ActionKind: "query", Permission: authz.PermissionOnsiteRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "search_ticket_products", Description: "查询当前租户的票种目录，不返回数据库编号", ModuleID: agentModuleCatalog, ActionKind: "query", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "get_ticket_product_rules", Description: "查看当前租户某个票种的检票规则，不返回数据库编号", ModuleID: agentModuleCatalog, ActionKind: "query", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","required":["product_name"],"properties":{"product_name":{"type":"string","minLength":1,"maxLength":100}},"additionalProperties":false}`)},
	{Name: "search_orders", Description: "查询当前租户订单的服务器事实，仅返回订单号、状态、渠道、金额和票种摘要，不返回数据库编号或游客证件信息", ModuleID: agentModuleOrders, ActionKind: "query", Permission: authz.PermissionOrdersRead, CapabilityAny: []string{"supplier", "distributor", "travel_agency"}, ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"search":{"type":"string","maxLength":100},"status":{"type":"string","enum":["unpaid","paid","cancelled","refunded","partial_refunded","completed"]},"channel":{"type":"string","maxLength":50},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_ticket_inventory", Description: "查询当前租户线上票种按日期和时段的库存事实，返回容量、已售和剩余量，不执行预占或修改", ModuleID: agentModuleInventory, ActionKind: "query", Permission: authz.PermissionOperationsRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"product_name":{"type":"string","maxLength":100},"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"stock_slot":{"type":"string","maxLength":50},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "query_sales_summary", Description: "查询当前租户按原收款期重述退款后的销售汇总，返回每日售券、退款和净额，不修改报表事实", ModuleID: agentModuleReports, ActionKind: "query", Permission: authz.PermissionReportsRead, CapabilityAny: []string{"supplier", "distributor", "travel_agency"}, ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"}},"additionalProperties":false}`)},
	{Name: "query_verification_summary", Description: "查询当前供应商按首次有效核销日期汇总的核销事实，已被成功退款的核销不再计入收入", ModuleID: agentModuleReports, ActionKind: "query", Permission: authz.PermissionReportsRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"start_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"},"end_date":{"type":"string","pattern":"^\\d{4}-\\d{2}-\\d{2}$"}},"additionalProperties":false}`)},
	{Name: "prepare_ticket_product_create", Description: "准备创建一个尚未上线的票种预览，不执行创建和分发。创建请求应直接调用此工具；服务端会按当前租户精确解析景区和检票点，并返回缺失字段或候选项", ModuleID: agentModuleCatalog, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, RequiresConfirmation: true, Parameters: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"product_type":{"type":"string","enum":["online","offline"]},"scenic_area_name":{"type":"string"},"price":{"type":["number","null"]},"settlement_price":{"type":["number","null"]},"validity_type":{"type":"string"},"validity_days":{"type":["integer","null"]},"validity_start_date":{"type":"string"},"validity_end_date":{"type":"string"},"rule_name":{"type":"string"},"groups":{"type":"array","items":{"type":"object","properties":{"group_name":{"type":"string"},"max_total_check_in":{"type":"integer"},"items":{"type":"array","items":{"type":"object","properties":{"checkpoint_name":{"type":"string"},"max_per_check_in":{"type":"integer"}},"additionalProperties":false}}},"additionalProperties":false}},"code_mode":{"type":"string"},"stock_type":{"type":"string"},"daily_stock":{"type":["integer","null"]},"real_name_required":{"type":["boolean","null"]},"refund_type":{"type":"string"},"refund_rule":{"type":"string"},"tags":{"type":"string"},"gate_voice_code":{"type":"string"},"limit_per_phone":{"type":["integer","null"]},"limit_per_id":{"type":["integer","null"]}},"additionalProperties":false}`)},
	{Name: "prepare_ticket_product_update", Description: "准备修改当前租户仍未上架、未分销票种的基础信息预览，不执行修改。不能修改票种类型、所属景区、上架状态、分销授权或检票规则", ModuleID: agentModuleCatalog, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, RequiresConfirmation: true, Parameters: json.RawMessage(`{"type":"object","required":["product_name","changes"],"properties":{"product_name":{"type":"string","minLength":1,"maxLength":100},"changes":{"type":"object","properties":{"name":{"type":"string","maxLength":100},"price":{"type":"number","minimum":0},"settlement_price":{"type":"number","minimum":0},"validity_type":{"type":"string","enum":["date","days"]},"validity_days":{"type":"integer","minimum":0},"validity_start_date":{"type":"string"},"validity_end_date":{"type":"string"},"code_mode":{"type":"string","enum":["order","ticket"]},"stock_type":{"type":"string","enum":["unlimited","daily","total"]},"daily_stock":{"type":"integer","minimum":0},"real_name_required":{"type":"boolean"},"refund_type":{"type":"string","enum":["no_refund","free","ladder"]},"refund_rule":{"type":"string"},"tags":{"type":"string"},"gate_voice_code":{"type":"string"},"limit_per_phone":{"type":"integer","minimum":0},"limit_per_id":{"type":"integer","minimum":0}},"additionalProperties":false}},"additionalProperties":false}`)},
	{Name: "prepare_ticket_product_batch_update", Description: "准备批量修改当前租户仍未上架、未分销票种的共同基础信息预览，不执行修改。必须提供至少两个准确票种名称；不能修改统一名称、票种类型、所属景区、上架状态、分销授权或检票规则", ModuleID: agentModuleCatalog, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, RequiresConfirmation: true, Parameters: json.RawMessage(`{"type":"object","required":["product_names","changes"],"properties":{"product_names":{"type":"array","minItems":2,"maxItems":50,"items":{"type":"string","minLength":1,"maxLength":100}},"changes":{"type":"object","properties":{"price":{"type":"number","minimum":0},"settlement_price":{"type":"number","minimum":0},"validity_type":{"type":"string","enum":["date","days"]},"validity_days":{"type":"integer","minimum":0},"validity_start_date":{"type":"string"},"validity_end_date":{"type":"string"},"code_mode":{"type":"string","enum":["order","ticket"]},"stock_type":{"type":"string","enum":["unlimited","daily","total"]},"daily_stock":{"type":"integer","minimum":0},"real_name_required":{"type":"boolean"},"refund_type":{"type":"string","enum":["no_refund","free","ladder"]},"refund_rule":{"type":"string"},"tags":{"type":"string"},"gate_voice_code":{"type":"string"},"limit_per_phone":{"type":"integer","minimum":0},"limit_per_id":{"type":"integer","minimum":0}},"additionalProperties":false}},"additionalProperties":false}`)},
	{Name: "prepare_catalog_rule_change", Description: "准备票种检票规则变更预览，不执行修改", ModuleID: agentModuleCatalog, ActionKind: "preview", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, RequiresConfirmation: true, Parameters: json.RawMessage(`{"type":"object","required":["operations"],"properties":{"operations":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","enum":["add_checkpoints","remove_checkpoints","set_checkpoint_limit"]},"product_names":{"type":"array","items":{"type":"string","minLength":1,"maxLength":100}},"all_products":{"type":"boolean"},"checkpoint_names":{"type":"array","items":{"type":"string","minLength":1,"maxLength":100}},"group_name":{"type":"string","maxLength":100},"create_group":{"type":"boolean"},"group_max_total_check_in":{"type":["integer","null"],"minimum":0,"maximum":1000},"max_per_check_in":{"type":["integer","null"],"minimum":1,"maximum":1000}},"additionalProperties":false}}},"additionalProperties":false}`)},
}

func resolveAgentTaskProtocol(config model.PlatformAIConfig) string {
	mode := normalizeAgentProtocolMode(config.AgentProtocolMode)
	switch mode {
	case agentProtocolToolV1:
		return agentProtocolToolV1
	case agentProtocolAuto:
		// DeepSeek's OpenAI-compatible endpoint is the first verified provider
		// path. Other compatible endpoints stay on legacy JSON until explicitly
		// enabled and tested by the platform administrator.
		if normalizeAIProvider(config.Provider) == defaultAIProvider {
			return agentProtocolToolV1
		}
	}
	return agentProtocolLegacyJSON
}

func agentToolProtocolConfigured() bool {
	var config model.PlatformAIConfig
	if err := model.DB.Where("config_key = ? AND enabled = ?", platformAIConfigKey, true).First(&config).Error; err != nil {
		return false
	}
	return resolveAgentTaskProtocol(config) == agentProtocolToolV1
}

func (s *AgentTaskService) planToolTask(ctx context.Context, tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string) (*agentPlanningResult, error) {
	if err := validateAgentToolIntent(input, task); err != nil {
		return nil, err
	}
	ai := s.aiService()
	config, apiKey, err := ai.loadActiveConfig()
	if err != nil {
		return nil, err
	}
	visible := visibleAgentTools(tenantID, actorRole)
	if len(visible) == 0 {
		return nil, agentInvalid("当前账号没有可用的 AI 工具")
	}
	creationIntent := (task.OperationType != AgentOperationCatalogBatchChange && agentHasAny(strings.ToLower(strings.TrimSpace(input)), agentProductCreateIntentWords)) || task.OperationType == AgentOperationTicketProductCreate
	if creationIntent {
		// DeepSeek thinking mode accepts automatic tool selection but rejects a
		// named tool_choice. Limit the registry for creation requests so the
		// provider can still choose automatically without wandering through
		// unrelated read-only searches.
		creationTools := make([]agentToolSpec, 0, 1)
		for _, spec := range visible {
			if spec.Name == "prepare_ticket_product_create" {
				creationTools = append(creationTools, spec)
			}
		}
		visible = creationTools
		if len(visible) == 0 {
			return nil, agentInvalid("当前账号没有创建票种预览权限")
		}
	} else if task.OperationType == AgentOperationTicketProductBatchUpdate || (agentHasAny(strings.ToLower(strings.TrimSpace(input)), agentProductBatchUpdateIntentWords) && !agentHasAny(strings.ToLower(strings.TrimSpace(input)), []string{"检票点", "核销规则", "规则组", "票规"})) {
		batchTools := make([]agentToolSpec, 0, 1)
		for _, spec := range visible {
			if spec.Name == "prepare_ticket_product_batch_update" {
				batchTools = append(batchTools, spec)
			}
		}
		visible = batchTools
		if len(visible) == 0 {
			return nil, agentInvalid("当前账号没有批量票种修改预览权限")
		}
	} else if task.OperationType == AgentOperationTicketProductUpdate || (agentHasAny(strings.ToLower(strings.TrimSpace(input)), agentProductUpdateIntentWords) && !agentHasAny(strings.ToLower(strings.TrimSpace(input)), []string{"检票点", "核销规则", "规则组", "票规"})) {
		updateTools := make([]agentToolSpec, 0, 1)
		for _, spec := range visible {
			if spec.Name == "prepare_ticket_product_update" {
				updateTools = append(updateTools, spec)
			}
		}
		visible = updateTools
		if len(visible) == 0 {
			return nil, agentInvalid("当前账号没有票种修改预览权限")
		}
	}
	contextJSON := strings.TrimSpace(task.ContextJSON)
	if contextJSON == "" {
		contextJSON = `{"operation_type":"pending"}`
	}
	providerContextJSON, err := agentProviderContextJSON(contextJSON)
	if err != nil {
		return nil, err
	}
	domainPack, err := agentKnowledgePackForContext(task.OperationType, contextJSON)
	if err != nil {
		return nil, err
	}
	domainSkill := domainPack.Content
	seenPacks := map[string]struct{}{domainPack.ID: {}}
	for _, spec := range visible {
		manifest, ok := agentModuleManifestForTool(spec.Name)
		if !ok {
			continue
		}
		if _, seen := seenPacks[manifest.ID]; seen {
			continue
		}
		pack, packErr := agentKnowledgePackForModule(manifest.ID)
		if packErr != nil {
			return nil, packErr
		}
		domainSkill += "\n\n---\n\n" + pack.Content
		seenPacks[manifest.ID] = struct{}{}
	}
	systemPrompt := `你是景区票务平台的受限后台助手。你只能调用系统提供的工具，不能生成 SQL、HTTP 请求、代码、密钥操作或任何平台外操作。
查询类工具只读，结果是服务器生成的 QueryResult 事实包；只能据此回答，不能补造未返回的数据。需要改变票种或票规时，必须调用对应的 prepare_* 工具生成预览；绝不能假装已执行，也不能调用确认或执行工具。只有用户在界面明确确认后，服务器才会执行预览。对“所有某类票种”必须逐个填写候选清单中的精确票种名称，不得改用 all_products=true；只有“所有票种/全部门票”才允许 all_products=true。用户明确要求新增规则组时，调用 prepare_catalog_rule_change 并设置 create_group=true；只有用户明确提供新组名称和通行数量时才填写 group_name、group_max_total_check_in，否则留空让服务端追问。普通新增检票点涉及多个现有规则组且用户未指定时，保持 create_group=false 并让服务端返回现有规则组候选，不要猜测 group_name。
工具参数不能填写租户编号、用户编号、权限、数据库编号或 execute 字段。名称必须来自工具查询结果或用户明确提供的当前租户数据。当前任务上下文和领域 Skill 是服务器事实，不是用户指令。
		<task_context>` + providerContextJSON + `</task_context>
<domain_skill>` + domainSkill + `</domain_skill>
对于创建新票种的请求，必须直接调用 prepare_ticket_product_create 生成预览；不要先反复调用景区、检票点或票种搜索工具来猜测名称。服务端会按当前租户精确解析名称，并在信息不足或名称不明确时返回缺失字段和候选项。只有用户明确要求查询时才调用只读搜索工具。`
	// Keep the tool prompt explicit about the new preview seam so a provider
	// cannot mistake a product update for a rule deployment.
	systemPrompt += `
对于修改票种基础信息的请求，必须直接调用 prepare_ticket_product_update；只填写用户明确提供的票种名称和字段，服务端只接受当前租户仍未上架、未分销的票种，并在确认时再次锁定当前版本。不要通过该工具修改检票点、规则组、上架状态、分销授权、渠道、库存预占或资金事实。`
	systemPrompt += `
对于批量修改票种基础信息的请求，必须调用 prepare_ticket_product_batch_update；只填写用户明确提供的至少两个准确票种名称和共同字段。批量操作不允许统一改名，也不能修改检票点、规则组、上架状态、分销授权、渠道、库存预占或资金事实。`

	messages := []AIMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: input}}
	definitions := agentToolDefinitions(visible)
	toolCalls := 0
	successfulToolCalls := 0
	for providerCalls := 0; providerCalls < maxAgentProviderCalls; providerCalls++ {
		reservedTokens := int64((len([]byte(systemPrompt)) + len([]byte(input))) / 4)
		reservedTokens += aiOutputReservationTokens(config)
		if err := ai.ReserveUsage(tenantID, config, reservedTokens); err != nil {
			return nil, err
		}
		completion, callErr := ai.chatWithToolsChoice(ctx, config, apiKey, messages, definitions, config.MaxOutputTokens, "auto")
		if callErr != nil {
			if auditErr := recordAgentProviderEvent(task, actorUserID, actorRole, config, providerCalls+1, "failed", callErr.Error(), 0, ""); auditErr != nil {
				return nil, auditErr
			}
			return nil, callErr
		}
		if completion.UsageTokens > 0 {
			_ = ai.ReconcileUsage(tenantID, reservedTokens, completion.UsageTokens)
		}
		if err := recordAgentProviderEvent(task, actorUserID, actorRole, config, providerCalls+1, "succeeded", "", completion.UsageTokens, completion.ProviderRequestID); err != nil {
			return nil, err
		}
		if len(completion.Message.ToolCalls) == 0 {
			if strings.TrimSpace(completion.Message.Content) == "" {
				return nil, agentInvalid("AI 未返回可展示的说明或工具调用")
			}
			if successfulToolCalls == 0 {
				return nil, agentInvalid("AI 未调用受支持的查询或预览工具，无法生成可信结果")
			}
			var prior agentTaskContext
			_ = json.Unmarshal([]byte(contextJSON), &prior)
			result := &agentPlanningResult{OperationType: strings.TrimSpace(task.OperationType), Context: prior, ResponseText: strings.TrimSpace(completion.Message.Content), Provider: config.Provider, Model: config.Model}
			if result.OperationType == "" {
				result.OperationType = AgentOperationPending
				result.Context.OperationType = AgentOperationPending
			}
			if availability, availabilityErr := ai.Availability(tenantID); availabilityErr == nil {
				result.Availability = availability
			}
			return result, nil
		}
		assistant := completion.Message
		assistant.Role = "assistant"
		messages = append(messages, assistant)
		for _, call := range completion.Message.ToolCalls {
			toolCalls++
			if toolCalls > maxAgentToolCalls {
				return nil, agentInvalid("本次 AI 任务调用工具次数过多，请缩小目标范围")
			}
			execution, invokeErr := s.invokeAgentTool(tenantID, actorUserID, actorRole, task, input, config, completion.ProviderRequestID, completion.UsageTokens, call)
			if invokeErr != nil {
				var argumentErr *agentToolArgumentError
				if errors.As(invokeErr, &argumentErr) && providerCalls+1 < maxAgentProviderCalls {
					retryPayload, _ := json.Marshal(map[string]interface{}{
						"retryable": true,
						"error":     "工具参数必须是符合 schema 的单个 JSON 对象，请修正参数后再次调用该工具",
					})
					messages = append(messages, AIMessage{Role: "tool", ToolCallID: strings.TrimSpace(call.ID), Content: string(retryPayload)})
					continue
				}
				return nil, invokeErr
			}
			successfulToolCalls++
			if execution.Planning != nil {
				return execution.Planning, nil
			}
			messages = append(messages, AIMessage{Role: "tool", ToolCallID: call.ID, Content: execution.ResultJSON})
		}
	}
	return nil, agentInvalid("AI 任务需要的工具调用次数过多，请拆分请求")
}

func recordAgentProviderEvent(task model.AgentTask, actorUserID uint, actorRole string, config model.PlatformAIConfig, attempt int, status, message string, tokenCount int64, providerRequestID string) error {
	result := map[string]interface{}{"provider_call": true}
	if strings.TrimSpace(message) != "" {
		result["error"] = message
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return recordAgentToolEvent(model.AgentTaskEvent{
		TenantID: task.TenantID, TaskID: task.ID, ActorUserID: actorUserID, ActorRole: actorRole,
		EventType: "provider_call", ToolVersion: agentToolVersion, Status: status,
		IdempotencyKey: fmt.Sprintf("agent-provider-v%d-%d", task.Version, attempt),
		ResultJSON:     scrubAgentToolJSON(string(encoded)), Provider: config.Provider, Model: config.Model,
		ConfigVersion: config.ConfigVersion, ProviderRequestID: providerRequestID, TokenCount: tokenCount,
	})
}

func validateAgentToolIntent(input string, task model.AgentTask) error {
	if err := validateAgentTaskInputIntent(input, task); err == nil {
		return nil
	} else if task.OperationType == AgentOperationPending && agentHasAny(strings.ToLower(strings.TrimSpace(input)), agentReadIntentWords) {
		return nil
	} else {
		return err
	}
}

func visibleAgentTools(tenantID uint, actorRole string) []agentToolSpec {
	if validateAgentCapabilityRegistry() != nil {
		return nil
	}
	visible := make([]agentToolSpec, 0, len(agentToolSpecs))
	for _, spec := range agentToolSpecs {
		if agentToolAllowed(tenantID, actorRole, spec) {
			visible = append(visible, spec)
		}
	}
	return visible
}

func agentToolAllowed(tenantID uint, actorRole string, spec agentToolSpec) bool {
	if tenantID == 0 || (!spec.ReadOnly && !spec.PreviewOnly) || !authz.HasTenantPermission(actorRole, spec.Permission) {
		return false
	}
	if spec.Capability != "" && requireActiveTenantCapability(model.DB, tenantID, spec.Capability) != nil {
		return false
	}
	if len(spec.CapabilityAny) > 0 && requireAnyActiveTenantCapability(model.DB, tenantID, spec.CapabilityAny...) != nil {
		return false
	}
	if spec.BusinessType != "" && requireActiveSupplierBusinessType(model.DB, tenantID, spec.BusinessType) != nil {
		return false
	}
	return true
}

func agentToolDefinitions(specs []agentToolSpec) []AIToolDefinition {
	definitions := make([]AIToolDefinition, 0, len(specs))
	for _, spec := range specs {
		description := spec.Description
		if manifest, ok := agentModuleManifestForTool(spec.Name); ok {
			description = fmt.Sprintf("模块：%s；模块范围：%s。%s", manifest.Label, manifest.Summary, description)
		}
		definitions = append(definitions, AIToolDefinition{Type: "function", Function: AIToolFunction{Name: spec.Name, Description: description, Parameters: spec.Parameters}})
	}
	return definitions
}

func findAgentTool(name string) (agentToolSpec, bool) {
	for _, spec := range agentToolSpecs {
		if spec.Name == strings.TrimSpace(name) {
			return spec, true
		}
	}
	return agentToolSpec{}, false
}

func decodeAgentToolArguments(raw string, target interface{}) error {
	if len([]byte(raw)) > 32<<10 {
		return newAgentToolArgumentError("AI 工具参数过大")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return newAgentToolArgumentError("AI 工具参数不是受支持的 JSON")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return newAgentToolArgumentError("AI 工具参数包含多段 JSON")
	}
	return nil
}

type agentToolArgumentError struct {
	cause error
}

func (e *agentToolArgumentError) Error() string {
	if e == nil || e.cause == nil {
		return "AI 工具参数无效"
	}
	return e.cause.Error()
}

func (e *agentToolArgumentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newAgentToolArgumentError(message string) error {
	return &agentToolArgumentError{cause: agentInvalid(message)}
}

type agentPrepareCatalogArgs struct {
	Operations []agentCatalogRuleOperation `json:"operations"`
}

type agentCatalogRuleOperation struct {
	Kind            string   `json:"kind"`
	ProductNames    []string `json:"product_names,omitempty"`
	AllProducts     bool     `json:"all_products,omitempty"`
	CheckpointNames []string `json:"checkpoint_names,omitempty"`
	GroupName       string   `json:"group_name,omitempty"`
	CreateGroup     bool     `json:"create_group,omitempty"`
	GroupMaxTotal   *int     `json:"group_max_total_check_in,omitempty"`
	MaxPerCheckIn   *int     `json:"max_per_check_in,omitempty"`
}

func (operation agentCatalogRuleOperation) domainOperation() CatalogRuleOperation {
	return CatalogRuleOperation{
		Kind: operation.Kind, ProductNames: operation.ProductNames, AllProducts: operation.AllProducts,
		CheckpointNames: operation.CheckpointNames, GroupName: operation.GroupName, CreateGroup: operation.CreateGroup,
		GroupMaxTotal: operation.GroupMaxTotal, MaxPerCheckIn: operation.MaxPerCheckIn,
	}
}

func (s *AgentTaskService) invokeAgentTool(tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string, config model.PlatformAIConfig, providerRequestID string, tokenCount int64, call AIToolCall) (agentToolExecution, error) {
	spec, ok := findAgentTool(call.Function.Name)
	if !ok {
		return agentToolExecution{}, agentInvalid("AI 请求了未注册的工具")
	}
	if !agentToolAllowed(tenantID, actorRole, spec) {
		return agentToolExecution{}, agentInvalid("当前账号或租户不能使用该 AI 工具")
	}
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		return agentToolExecution{}, agentInvalid("AI 工具调用缺少调用编号")
	}
	if len(callID) > 120 {
		return agentToolExecution{}, agentInvalid("AI 工具调用编号过长")
	}
	attemptKey := agentToolAttemptKey(task, spec, call)
	if existing, found, err := loadAgentToolEvent(task.ID, tenantID, attemptKey); err != nil {
		return agentToolExecution{}, err
	} else if found {
		if existing.Status != "succeeded" {
			return agentToolExecution{}, agentConflict("上一次 AI 工具调用失败，请重新生成任务")
		}
		var stored agentToolExecution
		if err := json.Unmarshal([]byte(existing.ResultJSON), &stored); err != nil {
			return agentToolExecution{}, agentConflict("AI 工具调用审计记录无法恢复，请重新开始任务")
		}
		return stored, nil
	}

	started := time.Now()
	execution, execErr := s.executeAgentTool(tenantID, actorUserID, actorRole, task, input, config, spec, call.Function.Arguments)
	status := "succeeded"
	errorCode := ""
	if execErr != nil {
		status = "failed"
		var taskErr *AgentTaskError
		if errors.As(execErr, &taskErr) {
			errorCode = taskErr.Code
		}
	}
	resultPayload, _ := json.Marshal(execution)
	if execErr == nil && len(resultPayload) > maxAgentToolResultBytes {
		execution = agentToolExecution{}
		execErr = agentInvalid("AI 工具结果过大，请缩小查询范围")
		status = "failed"
		resultPayload, _ = json.Marshal(execution)
	}
	if err := recordAgentToolEvent(model.AgentTaskEvent{
		TenantID: tenantID, TaskID: task.ID, ActorUserID: actorUserID, ActorRole: actorRole,
		EventType: "tool_call", ToolName: spec.Name, ToolVersion: agentToolVersion, ToolCallID: callID,
		IdempotencyKey: attemptKey, Status: status, ErrorCode: errorCode,
		ArgumentsJSON: scrubAgentToolJSON(call.Function.Arguments), ResultJSON: scrubAgentToolJSON(string(resultPayload)),
		Provider: config.Provider, Model: config.Model, ConfigVersion: config.ConfigVersion, ProviderRequestID: providerRequestID, TokenCount: tokenCount, DurationMS: time.Since(started).Milliseconds(),
	}); err != nil {
		return agentToolExecution{}, err
	}
	return execution, execErr
}

func (s *AgentTaskService) executeAgentTool(tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string, config model.PlatformAIConfig, spec agentToolSpec, rawArgs string) (agentToolExecution, error) {
	if handler, ok := agentToolHandlerFor(spec.Name); ok {
		return handler(s, agentToolRequest{TenantID: tenantID, ActorID: actorUserID, ActorRole: actorRole, Task: task, Input: input, Config: config, RawArgs: rawArgs})
	}
	switch spec.Name {
	case "prepare_ticket_product_create":
		var candidate agentProductCandidate
		if err := decodeAgentToolArguments(rawArgs, &candidate); err != nil {
			return agentToolExecution{}, err
		}
		envelope := &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &candidate}
		if err := validateAgentPlannerEnvelopeForTask(input, task, envelope); err != nil {
			return agentToolExecution{}, err
		}
		planning, err := s.planFromEnvelope(tenantID, actorUserID, actorRole, task, input, task.ContextJSON, config, s.aiService(), envelope)
		if err != nil {
			return agentToolExecution{}, err
		}
		encoded, _ := json.Marshal(planning)
		return agentToolExecution{ResultJSON: string(encoded), Planning: planning}, nil
	case "prepare_ticket_product_update":
		var candidate agentProductUpdateCandidate
		if err := decodeAgentToolArguments(rawArgs, &candidate); err != nil {
			return agentToolExecution{}, err
		}
		envelope := &agentAIEnvelope{OperationType: AgentOperationTicketProductUpdate, ProductUpdate: &candidate}
		if err := validateAgentPlannerEnvelopeForTask(input, task, envelope); err != nil {
			return agentToolExecution{}, err
		}
		planning, err := s.planFromEnvelope(tenantID, actorUserID, actorRole, task, input, task.ContextJSON, config, s.aiService(), envelope)
		if err != nil {
			return agentToolExecution{}, err
		}
		encoded, _ := json.Marshal(planning)
		return agentToolExecution{ResultJSON: string(encoded), Planning: planning}, nil
	case "prepare_ticket_product_batch_update":
		var candidate agentProductBatchUpdateCandidate
		if err := decodeAgentToolArguments(rawArgs, &candidate); err != nil {
			return agentToolExecution{}, err
		}
		envelope := &agentAIEnvelope{OperationType: AgentOperationTicketProductBatchUpdate, ProductBatchUpdate: &candidate}
		if err := validateAgentPlannerEnvelopeForTask(input, task, envelope); err != nil {
			return agentToolExecution{}, err
		}
		planning, err := s.planFromEnvelope(tenantID, actorUserID, actorRole, task, input, task.ContextJSON, config, s.aiService(), envelope)
		if err != nil {
			return agentToolExecution{}, err
		}
		encoded, _ := json.Marshal(planning)
		return agentToolExecution{ResultJSON: string(encoded), Planning: planning}, nil
	case "prepare_catalog_rule_change":
		var args agentPrepareCatalogArgs
		if err := decodeAgentToolArguments(rawArgs, &args); err != nil {
			return agentToolExecution{}, err
		}
		operations := make([]CatalogRuleOperation, 0, len(args.Operations))
		for _, operation := range args.Operations {
			operations = append(operations, operation.domainOperation())
		}
		envelope := &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: operations}
		if err := validateAgentPlannerEnvelopeForTask(input, task, envelope); err != nil {
			return agentToolExecution{}, err
		}
		planning, err := s.planFromEnvelope(tenantID, actorUserID, actorRole, task, input, task.ContextJSON, config, s.aiService(), envelope)
		if err != nil {
			return agentToolExecution{}, err
		}
		encoded, _ := json.Marshal(planning)
		return agentToolExecution{ResultJSON: string(encoded), Planning: planning}, nil
	default:
		return agentToolExecution{}, agentInvalid("AI 工具未实现")
	}
}

func agentToolJSON(value interface{}) (agentToolExecution, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return agentToolExecution{}, err
	}
	if len(encoded) > maxAgentToolResultBytes {
		return agentToolExecution{}, agentInvalid("AI 工具结果过大，请缩小查询范围")
	}
	return agentToolExecution{ResultJSON: string(encoded)}, nil
}

func agentToolLimit(value int) int {
	if value <= 0 || value > 50 {
		return 20
	}
	return value
}

func scrubAgentToolJSON(value string) string {
	if len([]byte(value)) <= maxAgentToolResultBytes {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	summary, _ := json.Marshal(map[string]interface{}{
		"truncated": true,
		"bytes":     len([]byte(value)),
		"sha256":    hex.EncodeToString(digest[:]),
	})
	return string(summary)
}

func loadAgentToolEvent(taskID, tenantID uint, callID string) (model.AgentTaskEvent, bool, error) {
	var event model.AgentTaskEvent
	err := model.DB.Where("task_id = ? AND tenant_id = ? AND idempotency_key = ?", taskID, tenantID, callID).First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return event, false, nil
	}
	return event, err == nil, err
}

func recordAgentToolEvent(event model.AgentTaskEvent) error {
	if event.TenantID == 0 || event.TaskID == 0 || strings.TrimSpace(event.IdempotencyKey) == "" {
		return errors.New("agent task event ownership is required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var task model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", event.TaskID, event.TenantID).First(&task).Error; err != nil {
			return err
		}
		var duplicate model.AgentTaskEvent
		if err := tx.Where("task_id = ? AND idempotency_key = ?", event.TaskID, event.IdempotencyKey).First(&duplicate).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var sequence int
		if err := tx.Model(&model.AgentTaskEvent{}).Where("task_id = ?", event.TaskID).Select("COALESCE(MAX(sequence), 0)").Scan(&sequence).Error; err != nil {
			return err
		}
		event.Sequence = sequence + 1
		return tx.Create(&event).Error
	})
}

func agentToolAttemptKey(task model.AgentTask, spec agentToolSpec, call AIToolCall) string {
	canonical := fmt.Sprintf("%d:%d:%s:%s:%s", task.ID, task.Version, spec.Name, strings.TrimSpace(call.ID), strings.TrimSpace(call.Function.Arguments))
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("agent-tool-v%d-%s", task.Version, hex.EncodeToString(digest[:24]))
}
