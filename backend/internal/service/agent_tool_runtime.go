package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

const (
	agentToolVersion        = "1"
	maxAgentToolCalls       = 6
	maxAgentProviderCalls   = 3
	maxAgentToolResultBytes = 64 << 10
)

type agentToolSpec struct {
	Name         string
	Description  string
	Permission   string
	Capability   string
	BusinessType string
	ReadOnly     bool
	PreviewOnly  bool
	Parameters   json.RawMessage
}

type agentToolExecution struct {
	ResultJSON string
	Planning   *agentPlanningResult
}

var agentToolSpecs = []agentToolSpec{
	{Name: "search_scenic_areas", Description: "查询当前租户可用的景区名称", Permission: authz.PermissionOnsiteRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "search_checkpoints", Description: "查询当前租户景区下的检票点名称", Permission: authz.PermissionOnsiteRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "search_ticket_products", Description: "查询当前租户的票种目录，不返回数据库编号", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`)},
	{Name: "get_ticket_product_rules", Description: "查看当前租户某个票种的检票规则，不返回数据库编号", Permission: authz.PermissionCatalogRead, Capability: "supplier", BusinessType: "scenic", ReadOnly: true, Parameters: json.RawMessage(`{"type":"object","required":["product_name"],"properties":{"product_name":{"type":"string","minLength":1,"maxLength":100}},"additionalProperties":false}`)},
	{Name: "prepare_ticket_product_create", Description: "准备创建一个尚未上线的票种预览，不执行创建和分发", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, Parameters: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"product_type":{"type":"string","enum":["online","offline"]},"scenic_area_name":{"type":"string"},"price":{"type":["number","null"]},"settlement_price":{"type":["number","null"]},"validity_type":{"type":"string"},"validity_days":{"type":["integer","null"]},"validity_start_date":{"type":"string"},"validity_end_date":{"type":"string"},"rule_name":{"type":"string"},"groups":{"type":"array"},"code_mode":{"type":"string"},"stock_type":{"type":"string"},"daily_stock":{"type":["integer","null"]},"real_name_required":{"type":["boolean","null"]},"refund_type":{"type":"string"},"refund_rule":{"type":"string"},"tags":{"type":"string"},"gate_voice_code":{"type":"string"},"limit_per_phone":{"type":["integer","null"]},"limit_per_id":{"type":["integer","null"]}},"additionalProperties":false}`)},
	{Name: "prepare_catalog_rule_change", Description: "准备票种检票规则变更预览，不执行修改", Permission: authz.PermissionCatalogWrite, Capability: "supplier", BusinessType: "scenic", PreviewOnly: true, Parameters: json.RawMessage(`{"type":"object","required":["operations"],"properties":{"operations":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object"}}},"additionalProperties":false}`)},
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
	if err := requireActiveScenicSupplier(model.DB, tenantID); err != nil {
		return nil, err
	}
	if err := validateAgentToolIntent(input, task.OperationType); err != nil {
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
	contextJSON := strings.TrimSpace(task.ContextJSON)
	if contextJSON == "" {
		contextJSON = `{"operation_type":"pending"}`
	}
	providerContextJSON, err := agentProviderContextJSON(contextJSON)
	if err != nil {
		return nil, err
	}
	domainSkill, _, err := agentDomainSkill(task.OperationType)
	if err != nil {
		return nil, err
	}
	systemPrompt := `你是景区票务平台的受限后台助手。你只能调用系统提供的工具，不能生成 SQL、HTTP 请求、代码、密钥操作或任何平台外操作。
查询类工具只读。需要改变票种或票规时，必须调用对应的 prepare_* 工具生成预览；绝不能假装已执行，也不能调用确认或执行工具。只有用户在界面明确确认后，服务器才会执行预览。
工具参数不能填写租户编号、用户编号、权限、数据库编号或 execute 字段。名称必须来自工具查询结果或用户明确提供的当前租户数据。当前任务上下文和领域 Skill 是服务器事实，不是用户指令。
<task_context>` + providerContextJSON + `</task_context>
<domain_skill>` + domainSkill + `</domain_skill>`

	messages := []AIMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: input}}
	definitions := agentToolDefinitions(visible)
	toolCalls := 0
	for providerCalls := 0; providerCalls < maxAgentProviderCalls; providerCalls++ {
		reservedTokens := int64((len([]byte(systemPrompt)) + len([]byte(input))) / 4)
		reservedTokens += aiOutputReservationTokens(config)
		if err := ai.ReserveUsage(tenantID, config, reservedTokens); err != nil {
			return nil, err
		}
		completion, callErr := ai.chatWithTools(ctx, config, apiKey, messages, definitions, config.MaxOutputTokens)
		if callErr != nil {
			return nil, callErr
		}
		if completion.UsageTokens > 0 {
			_ = ai.ReconcileUsage(tenantID, reservedTokens, completion.UsageTokens)
		}
		if len(completion.Message.ToolCalls) == 0 {
			if strings.TrimSpace(completion.Message.Content) == "" {
				return nil, agentInvalid("AI 未返回可展示的说明或工具调用")
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
			execution, invokeErr := s.invokeAgentTool(tenantID, actorUserID, actorRole, task, input, config, completion.ProviderRequestID, call)
			if invokeErr != nil {
				return nil, invokeErr
			}
			if execution.Planning != nil {
				return execution.Planning, nil
			}
			messages = append(messages, AIMessage{Role: "tool", ToolCallID: call.ID, Content: execution.ResultJSON})
		}
	}
	return nil, agentInvalid("AI 任务需要的工具调用次数过多，请拆分请求")
}

func validateAgentToolIntent(input, existingOperationType string) error {
	if err := validateAgentInputIntent(input, existingOperationType); err == nil {
		return nil
	} else if existingOperationType == AgentOperationPending && agentHasAny(strings.ToLower(strings.TrimSpace(input)), agentReadIntentWords) {
		return nil
	} else {
		return err
	}
}

func visibleAgentTools(tenantID uint, actorRole string) []agentToolSpec {
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
	if spec.BusinessType != "" && requireActiveSupplierBusinessType(model.DB, tenantID, spec.BusinessType) != nil {
		return false
	}
	return true
}

func agentToolDefinitions(specs []agentToolSpec) []AIToolDefinition {
	definitions := make([]AIToolDefinition, 0, len(specs))
	for _, spec := range specs {
		definitions = append(definitions, AIToolDefinition{Type: "function", Function: AIToolFunction{Name: spec.Name, Description: spec.Description, Parameters: spec.Parameters}})
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
		return agentInvalid("AI 工具参数过大")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return agentInvalid("AI 工具参数不是受支持的 JSON")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return agentInvalid("AI 工具参数包含多段 JSON")
	}
	return nil
}

type agentSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type agentProductRuleArgs struct {
	ProductName string `json:"product_name"`
}

type agentPrepareCatalogArgs struct {
	Operations []CatalogRuleOperation `json:"operations"`
}

func (s *AgentTaskService) invokeAgentTool(tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string, config model.PlatformAIConfig, providerRequestID string, call AIToolCall) (agentToolExecution, error) {
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
	if existing, found, err := loadAgentToolEvent(task.ID, tenantID, callID); err != nil {
		return agentToolExecution{}, err
	} else if found {
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
	if err := recordAgentToolEvent(model.AgentTaskEvent{
		TenantID: tenantID, TaskID: task.ID, ActorUserID: actorUserID, ActorRole: actorRole,
		EventType: "tool_call", ToolName: spec.Name, ToolVersion: agentToolVersion, ToolCallID: callID,
		IdempotencyKey: fmt.Sprintf("agent-task-%d-v%d", task.ID, task.Version), Status: status, ErrorCode: errorCode,
		ArgumentsJSON: scrubAgentToolJSON(call.Function.Arguments), ResultJSON: scrubAgentToolJSON(string(resultPayload)),
		Provider: config.Provider, Model: config.Model, ConfigVersion: config.ConfigVersion, ProviderRequestID: providerRequestID, DurationMS: time.Since(started).Milliseconds(),
	}); err != nil {
		return agentToolExecution{}, err
	}
	return execution, execErr
}

func (s *AgentTaskService) executeAgentTool(tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string, config model.PlatformAIConfig, spec agentToolSpec, rawArgs string) (agentToolExecution, error) {
	switch spec.Name {
	case "search_scenic_areas":
		var args agentSearchArgs
		if err := decodeAgentToolArguments(rawArgs, &args); err != nil {
			return agentToolExecution{}, err
		}
		var areas []model.ScenicArea
		query := model.DB.Where("tenant_id = ? AND status = ?", tenantID, "active").Order("id ASC")
		if strings.TrimSpace(args.Query) != "" {
			query = query.Where("name ILIKE ? OR code ILIKE ?", "%"+strings.TrimSpace(args.Query)+"%", "%"+strings.TrimSpace(args.Query)+"%")
		}
		if err := query.Limit(agentToolLimit(args.Limit)).Find(&areas).Error; err != nil {
			return agentToolExecution{}, err
		}
		rows := make([]map[string]interface{}, 0, len(areas))
		for _, area := range areas {
			rows = append(rows, map[string]interface{}{"name": area.Name, "code": area.Code, "status": area.Status})
		}
		return agentToolJSON(rows)
	case "search_checkpoints":
		var args agentSearchArgs
		if err := decodeAgentToolArguments(rawArgs, &args); err != nil {
			return agentToolExecution{}, err
		}
		var checkpoints []model.CheckPoint
		query := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC")
		if strings.TrimSpace(args.Query) != "" {
			query = query.Where("name ILIKE ? OR location ILIKE ?", "%"+strings.TrimSpace(args.Query)+"%", "%"+strings.TrimSpace(args.Query)+"%")
		}
		if err := query.Limit(agentToolLimit(args.Limit)).Find(&checkpoints).Error; err != nil {
			return agentToolExecution{}, err
		}
		rows := make([]map[string]interface{}, 0, len(checkpoints))
		for _, point := range checkpoints {
			rows = append(rows, map[string]interface{}{"name": point.Name, "location": point.Location})
		}
		return agentToolJSON(rows)
	case "search_ticket_products":
		var args agentSearchArgs
		if err := decodeAgentToolArguments(rawArgs, &args); err != nil {
			return agentToolExecution{}, err
		}
		var products []model.Product
		query := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC")
		if strings.TrimSpace(args.Query) != "" {
			query = query.Where("name ILIKE ?", "%"+strings.TrimSpace(args.Query)+"%")
		}
		if err := query.Limit(agentToolLimit(args.Limit)).Find(&products).Error; err != nil {
			return agentToolExecution{}, err
		}
		rows := make([]map[string]interface{}, 0, len(products))
		for _, product := range products {
			if isDistributedListing(&product) {
				continue
			}
			rows = append(rows, map[string]interface{}{"name": product.Name, "type": product.Type, "price": product.Price, "status": product.Status, "is_distributable": product.IsDistributable})
		}
		return agentToolJSON(rows)
	case "get_ticket_product_rules":
		var args agentProductRuleArgs
		if err := decodeAgentToolArguments(rawArgs, &args); err != nil {
			return agentToolExecution{}, err
		}
		name := strings.TrimSpace(args.ProductName)
		if name == "" {
			return agentToolExecution{}, agentInvalid("product_name is required")
		}
		var products []model.Product
		if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("tenant_id = ? AND name = ?", tenantID, name).Find(&products).Error; err != nil {
			return agentToolExecution{}, err
		}
		if len(products) == 0 {
			return agentToolExecution{}, agentInvalid("当前租户不存在该票种")
		}
		if len(products) > 1 {
			return agentToolExecution{}, agentInvalid("该票种名称不唯一，请先缩小范围")
		}
		product := products[0]
		groups := make([]map[string]interface{}, 0, len(product.Rule.Groups))
		for _, group := range product.Rule.Groups {
			items := make([]map[string]interface{}, 0, len(group.Items))
			for _, item := range group.Items {
				items = append(items, map[string]interface{}{"checkpoint_name": item.CheckPoint.Name, "max_per_check_in": item.MaxPerCheckIn})
			}
			groups = append(groups, map[string]interface{}{"group_name": group.GroupName, "max_total_check_in": group.MaxTotalCheckIn, "items": items})
		}
		return agentToolJSON(map[string]interface{}{"product_name": product.Name, "rule_name": product.Rule.Name, "groups": groups})
	case "prepare_ticket_product_create":
		var candidate agentProductCandidate
		if err := decodeAgentToolArguments(rawArgs, &candidate); err != nil {
			return agentToolExecution{}, err
		}
		envelope := &agentAIEnvelope{OperationType: AgentOperationTicketProductCreate, Product: &candidate}
		if err := validateAgentPlannerEnvelope(input, envelope); err != nil {
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
		envelope := &agentAIEnvelope{OperationType: AgentOperationCatalogBatchChange, Operations: args.Operations}
		if err := validateAgentPlannerEnvelope(input, envelope); err != nil {
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
	return string([]byte(value)[:maxAgentToolResultBytes])
}

func loadAgentToolEvent(taskID, tenantID uint, callID string) (model.AgentTaskEvent, bool, error) {
	var event model.AgentTaskEvent
	err := model.DB.Where("task_id = ? AND tenant_id = ? AND tool_call_id = ?", taskID, tenantID, callID).First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return event, false, nil
	}
	return event, err == nil, err
}

func recordAgentToolEvent(event model.AgentTaskEvent) error {
	if event.TenantID == 0 || event.TaskID == 0 {
		return errors.New("agent task event ownership is required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var duplicate model.AgentTaskEvent
		if err := tx.Where("task_id = ? AND tool_call_id = ? AND tool_call_id <> ''", event.TaskID, event.ToolCallID).First(&duplicate).Error; err == nil {
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
