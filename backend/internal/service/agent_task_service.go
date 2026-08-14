package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentOperationPending             = "pending"
	AgentOperationCatalogBatchChange  = "catalog_batch_change"
	AgentOperationTicketProductCreate = "ticket_product_create"

	AgentTaskCollecting           = "collecting"
	AgentTaskReadyForPreview      = "ready_for_preview"
	AgentTaskAwaitingConfirmation = "awaiting_confirmation"
	AgentTaskExecuting            = "executing"
	AgentTaskCompleted            = "completed"
	AgentTaskFailed               = "failed"
	AgentTaskExpired              = "expired"
	AgentTaskCancelled            = "cancelled"
)

type AgentTaskError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *AgentTaskError) Error() string { return e.Message }

func agentInvalid(message string) error {
	return &AgentTaskError{Code: "invalid_request", Message: message, HTTPStatus: 400}
}

func agentNotFound(message string) error {
	return &AgentTaskError{Code: "not_found", Message: message, HTTPStatus: 404}
}

func agentConflict(message string) error {
	return &AgentTaskError{Code: "task_conflict", Message: message, HTTPStatus: 409}
}

type AgentTaskRequest struct {
	TaskID         uint   `json:"task_id"`
	InputText      string `json:"input_text"`
	IdempotencyKey string `json:"idempotency_key"`
	TurnKey        string `json:"turn_key"`
}

type AgentMissingField struct {
	Field    string   `json:"field"`
	Label    string   `json:"label"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

type AgentTaskView struct {
	TaskID        uint                `json:"task_id"`
	OperationType string              `json:"operation_type"`
	State         string              `json:"state"`
	InputText     string              `json:"input_text"`
	MissingFields []AgentMissingField `json:"missing_fields,omitempty"`
	Preview       json.RawMessage     `json:"preview,omitempty"`
	Result        json.RawMessage     `json:"result,omitempty"`
	PlanID        uint                `json:"plan_id,omitempty"`
	PlanHash      string              `json:"plan_hash,omitempty"`
	CanConfirm    bool                `json:"can_confirm"`
	Version       int                 `json:"version"`
	ExpiresAt     time.Time           `json:"expires_at"`
	ConfirmedAt   *time.Time          `json:"confirmed_at,omitempty"`
	CompletedAt   *time.Time          `json:"completed_at,omitempty"`
	ErrorMessage  string              `json:"error_message,omitempty"`
	Provider      string              `json:"provider,omitempty"`
	Model         string              `json:"model,omitempty"`
	Availability  *AIAvailabilityView `json:"availability,omitempty"`
}

type agentTaskContext struct {
	OperationType string                 `json:"operation_type"`
	Operations    []CatalogRuleOperation `json:"operations,omitempty"`
	Product       *agentProductDraft     `json:"product,omitempty"`
	Assumptions   []string               `json:"assumptions,omitempty"`
}

type agentProductDraft struct {
	Name             string                `json:"name,omitempty"`
	ScenicAreaName   string                `json:"scenic_area_name,omitempty"`
	ScenicAreaID     uint                  `json:"scenic_area_id,omitempty"`
	Price            *float64              `json:"price,omitempty"`
	SettlementPrice  *float64              `json:"settlement_price,omitempty"`
	ValidityType     string                `json:"validity_type,omitempty"`
	ValidityDays     *int                  `json:"validity_days,omitempty"`
	ValidityStart    string                `json:"validity_start_date,omitempty"`
	ValidityEnd      string                `json:"validity_end_date,omitempty"`
	RuleName         string                `json:"rule_name,omitempty"`
	Groups           []agentRuleDraftGroup `json:"groups,omitempty"`
	CodeMode         string                `json:"code_mode,omitempty"`
	StockType        string                `json:"stock_type,omitempty"`
	DailyStock       *int                  `json:"daily_stock,omitempty"`
	RealNameRequired *bool                 `json:"real_name_required,omitempty"`
	RefundType       string                `json:"refund_type,omitempty"`
	RefundRule       string                `json:"refund_rule,omitempty"`
	Tags             string                `json:"tags,omitempty"`
	GateVoiceCode    string                `json:"gate_voice_code,omitempty"`
	LimitPerPhone    *int                  `json:"limit_per_phone,omitempty"`
	LimitPerID       *int                  `json:"limit_per_id,omitempty"`
}

type agentRuleDraftGroup struct {
	GroupName       string               `json:"group_name,omitempty"`
	MaxTotalCheckIn int                  `json:"max_total_check_in,omitempty"`
	Items           []agentRuleDraftItem `json:"items,omitempty"`
}

type agentRuleDraftItem struct {
	CheckpointName string `json:"checkpoint_name,omitempty"`
	CheckpointID   uint   `json:"checkpoint_id,omitempty"`
	MaxPerCheckIn  int    `json:"max_per_check_in,omitempty"`
}

type agentProductCandidate struct {
	Name             string                `json:"name,omitempty"`
	ScenicAreaName   string                `json:"scenic_area_name,omitempty"`
	Price            *float64              `json:"price,omitempty"`
	SettlementPrice  *float64              `json:"settlement_price,omitempty"`
	ValidityType     string                `json:"validity_type,omitempty"`
	ValidityDays     *int                  `json:"validity_days,omitempty"`
	ValidityStart    string                `json:"validity_start_date,omitempty"`
	ValidityEnd      string                `json:"validity_end_date,omitempty"`
	RuleName         string                `json:"rule_name,omitempty"`
	Groups           []agentRuleDraftGroup `json:"groups,omitempty"`
	CodeMode         string                `json:"code_mode,omitempty"`
	StockType        string                `json:"stock_type,omitempty"`
	DailyStock       *int                  `json:"daily_stock,omitempty"`
	RealNameRequired *bool                 `json:"real_name_required,omitempty"`
	RefundType       string                `json:"refund_type,omitempty"`
	RefundRule       string                `json:"refund_rule,omitempty"`
	Tags             string                `json:"tags,omitempty"`
	GateVoiceCode    string                `json:"gate_voice_code,omitempty"`
	LimitPerPhone    *int                  `json:"limit_per_phone,omitempty"`
	LimitPerID       *int                  `json:"limit_per_id,omitempty"`
}

type agentAIEnvelope struct {
	OperationType string                 `json:"operation_type"`
	Operations    []CatalogRuleOperation `json:"operations,omitempty"`
	Product       *agentProductCandidate `json:"product,omitempty"`
}

type agentPlanningResult struct {
	OperationType string
	Context       agentTaskContext
	Missing       []AgentMissingField
	PreviewJSON   string
	LinkedPlanID  uint
	PlanHash      string
	Provider      string
	Model         string
	Availability  *AIAvailabilityView
}

type AgentTaskService struct {
	AI *PlatformAIService
}

func (s *AgentTaskService) aiService() *PlatformAIService {
	if s != nil && s.AI != nil {
		return s.AI
	}
	return &PlatformAIService{}
}

func (s *AgentTaskService) Submit(ctx context.Context, tenantID, actorUserID uint, actorRole string, req AgentTaskRequest) (*AgentTaskView, error) {
	input := strings.TrimSpace(req.InputText)
	if tenantID == 0 {
		return nil, agentInvalid("tenant is required")
	}
	if input == "" {
		return nil, agentInvalid("input_text is required")
	}
	if len([]rune(input)) > 2000 {
		return nil, agentInvalid("input_text cannot exceed 2000 characters")
	}
	if actorRole == "" {
		actorRole = "admin"
	}
	turnKey := strings.TrimSpace(req.TurnKey)
	if turnKey == "" {
		turnKey = fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}
	if len(turnKey) > 120 {
		return nil, agentInvalid("turn_key is too long")
	}

	task, err := s.loadOrCreateTask(tenantID, actorUserID, actorRole, req, input)
	if err != nil {
		return nil, err
	}
	if task.ActorUserID != actorUserID {
		return nil, agentNotFound("agent task not found")
	}
	if task.LastTurnKey == turnKey && strings.TrimSpace(task.LastResponseJSON) != "" {
		return decodeAgentTaskView(task.LastResponseJSON)
	}
	if time.Now().After(task.ExpiresAt) && task.State != AgentTaskCompleted {
		_ = model.DB.Model(&task).Updates(map[string]interface{}{"state": AgentTaskExpired, "error_message": "agent task expired; start a new task"}).Error
		return nil, agentConflict("agent task expired; start a new task")
	}
	if task.State != AgentTaskCollecting && task.State != AgentTaskReadyForPreview {
		return nil, agentConflict("agent task is already awaiting confirmation or has completed")
	}

	planning, err := s.plan(ctx, tenantID, actorUserID, actorRole, task, input)
	if err != nil {
		return nil, err
	}
	var response *AgentTaskView
	err = model.Write(func(tx *gorm.DB) error {
		var locked model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return agentNotFound("agent task not found")
			}
			return err
		}
		if locked.LastTurnKey == turnKey && strings.TrimSpace(locked.LastResponseJSON) != "" {
			return decodeJSONInto(locked.LastResponseJSON, &response)
		}
		if locked.State != AgentTaskCollecting && locked.State != AgentTaskReadyForPreview {
			return agentConflict("agent task changed while it was being planned")
		}
		contextJSON, err := json.Marshal(planning.Context)
		if err != nil {
			return err
		}
		missingJSON, err := json.Marshal(planning.Missing)
		if err != nil {
			return err
		}
		locked.OperationType = planning.OperationType
		locked.State = AgentTaskCollecting
		if len(planning.Missing) == 0 && planning.PreviewJSON != "" {
			locked.State = AgentTaskAwaitingConfirmation
		}
		locked.InputText = input
		locked.ContextJSON = string(contextJSON)
		locked.MissingJSON = string(missingJSON)
		locked.PreviewJSON = planning.PreviewJSON
		locked.PlanHash = planning.PlanHash
		locked.LinkedPlanID = planning.LinkedPlanID
		locked.ErrorMessage = ""
		locked.LastTurnKey = turnKey
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		view := agentTaskViewFromModel(locked)
		view.Provider = planning.Provider
		view.Model = planning.Model
		view.Availability = planning.Availability
		response = view
		stored, err := json.Marshal(view)
		if err != nil {
			return err
		}
		return tx.Model(&locked).Update("last_response_json", string(stored)).Error
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *AgentTaskService) Get(tenantID, actorUserID, taskID uint) (*AgentTaskView, error) {
	if tenantID == 0 || taskID == 0 {
		return nil, agentInvalid("tenant and task are required")
	}
	var task model.AgentTask
	if err := model.DB.Where("id = ? AND tenant_id = ? AND actor_user_id = ?", taskID, tenantID, actorUserID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentNotFound("agent task not found")
		}
		return nil, err
	}
	return agentTaskViewFromModel(task), nil
}

func (s *AgentTaskService) Confirm(tenantID, actorUserID uint, actorRole string, taskID uint) (*AgentTaskView, error) {
	if tenantID == 0 || taskID == 0 {
		return nil, agentInvalid("tenant and task are required")
	}
	if actorRole == "" {
		actorRole = "admin"
	}
	var task model.AgentTask
	if err := model.DB.Where("id = ? AND tenant_id = ? AND actor_user_id = ?", taskID, tenantID, actorUserID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentNotFound("agent task not found")
		}
		return nil, err
	}
	if task.State == AgentTaskCompleted {
		return agentTaskViewFromModel(task), nil
	}
	if task.State != AgentTaskAwaitingConfirmation {
		return nil, agentConflict("agent task is not ready for confirmation")
	}
	if time.Now().After(task.ExpiresAt) {
		_ = model.DB.Model(&task).Updates(map[string]interface{}{"state": AgentTaskExpired, "error_message": "agent task expired; start a new task"}).Error
		return nil, agentConflict("agent task expired; start a new task")
	}

	if task.OperationType == AgentOperationTicketProductCreate {
		return s.confirmProductTask(tenantID, actorUserID, actorRole, task)
	}
	if task.OperationType != AgentOperationCatalogBatchChange {
		return nil, agentConflict("agent task has no executable operation")
	}

	// The existing batch plan is independently idempotent and locks its own
	// rows. Marking the task as executing first prevents a second UI click from
	// starting a second provider-side execution.
	var executing model.AgentTask
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&executing).Error; err != nil {
			return err
		}
		if executing.State == AgentTaskCompleted {
			return nil
		}
		if executing.State != AgentTaskAwaitingConfirmation {
			return agentConflict("agent task is no longer executable")
		}
		now := time.Now()
		executing.State = AgentTaskExecuting
		executing.ConfirmedAt = &now
		executing.Version++
		return tx.Save(&executing).Error
	}); err != nil {
		return nil, err
	}
	if executing.State == AgentTaskCompleted {
		return agentTaskViewFromModel(executing), nil
	}
	preview, err := (&CatalogBatchChangeService{}).Confirm(tenantID, actorUserID, actorRole, executing.LinkedPlanID, executing.PlanHash)
	if err != nil {
		_ = model.DB.Model(&model.AgentTask{}).Where("id = ? AND tenant_id = ?", task.ID, tenantID).Updates(map[string]interface{}{"state": AgentTaskFailed, "error_message": err.Error()}).Error
		return nil, err
	}
	previewJSON, _ := json.Marshal(preview)
	var response *AgentTaskView
	err = model.Write(func(tx *gorm.DB) error {
		var completed model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&completed).Error; err != nil {
			return err
		}
		now := time.Now()
		completed.State = AgentTaskCompleted
		completed.CompletedAt = &now
		completed.ResultJSON = string(previewJSON)
		completed.PreviewJSON = string(previewJSON)
		completed.ErrorMessage = ""
		completed.Version++
		if err := tx.Save(&completed).Error; err != nil {
			return err
		}
		response = agentTaskViewFromModel(completed)
		stored, _ := json.Marshal(response)
		return tx.Model(&completed).Update("last_response_json", string(stored)).Error
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *AgentTaskService) loadOrCreateTask(tenantID, actorUserID uint, actorRole string, req AgentTaskRequest, input string) (model.AgentTask, error) {
	if req.TaskID != 0 {
		var task model.AgentTask
		if err := model.DB.Where("id = ? AND tenant_id = ? AND actor_user_id = ?", req.TaskID, tenantID, actorUserID).First(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return task, agentNotFound("agent task not found")
			}
			return task, err
		}
		return task, nil
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("agent-%d", time.Now().UnixNano())
	}
	if len(key) > 120 {
		return model.AgentTask{}, agentInvalid("idempotency_key is too long")
	}
	var task model.AgentTask
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", tenantID, key).First(&task).Error; err == nil {
			if task.ActorUserID != actorUserID || task.InputText != input {
				return agentConflict("idempotency key already belongs to another agent task")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		expiresAt := time.Now().Add(30 * time.Minute)
		task = model.AgentTask{
			TenantID: tenantID, ActorUserID: actorUserID, ActorRole: actorRole,
			OperationType: AgentOperationPending, State: AgentTaskCollecting,
			InputText: input, ContextJSON: `{"operation_type":"pending"}`,
			MissingJSON: `[]`, IdempotencyKey: key, Version: 1, ExpiresAt: expiresAt,
		}
		return tx.Create(&task).Error
	})
	return task, err
}

func (s *AgentTaskService) plan(ctx context.Context, tenantID, actorUserID uint, actorRole string, task model.AgentTask, input string) (*agentPlanningResult, error) {
	if err := requireActiveScenicSupplier(model.DB, tenantID); err != nil {
		return nil, err
	}
	ai := s.aiService()
	config, apiKey, err := ai.loadActiveConfig()
	if err != nil {
		return nil, err
	}
	contextJSON := strings.TrimSpace(task.ContextJSON)
	if contextJSON == "" {
		contextJSON = `{"operation_type":"pending"}`
	}
	providerContextJSON, err := agentProviderContextJSON(contextJSON)
	if err != nil {
		return nil, err
	}
	promptContext, err := agentCandidateContextJSON(tenantID)
	if err != nil {
		return nil, err
	}
	systemPrompt := `你是景区票务平台的受限操作规划器。你只能输出严格 JSON，不能解释、不能调用工具、不能生成 SQL，也不能直接修改数据。
输出格式必须是：{"operation_type":"catalog_batch_change|ticket_product_create","operations":[...],"product":{...}}。
catalog_batch_change 只能使用 add_checkpoints、remove_checkpoints、set_checkpoint_limit；票种和检票点只能填写候选清单中的精确名称，不要输出 product_ids 或 checkpoint_ids。无法确定时输出空 operations。
ticket_product_create 用于创建尚未上线的本租户票种。product 只填写用户明确提供的字段，价格缺失必须输出 null，不能猜测价格；分销字段、status、product_id、tenant_id 都不能输出。groups 的每个 item 只填写 checkpoint_name 和可选 max_per_check_in。
当前任务上下文是服务器保存的规范化事实，用户的新输入用于补充或修正它。不要丢失已有事实，也不要编造未提供的业务数字。
候选景区、检票点和票种如下：` + promptContext + `
服务器上下文如下：` + providerContextJSON
	if ctx == nil {
		ctx = context.Background()
	}
	reservedTokens := int64((len([]byte(systemPrompt)) + len([]byte(input))) / 4)
	reservedTokens += int64(config.MaxOutputTokens)
	if err := ai.ReserveUsage(tenantID, config, reservedTokens); err != nil {
		return nil, err
	}
	content, actualTokens, err := ai.chat(ctx, config, apiKey, []AIMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: input}}, config.MaxOutputTokens)
	if actualTokens > 0 {
		_ = ai.ReconcileUsage(tenantID, reservedTokens, actualTokens)
	}
	if err != nil {
		return nil, err
	}
	envelope, err := decodeAgentAIEnvelope(content)
	if err != nil {
		return nil, err
	}
	result := &agentPlanningResult{Provider: config.Provider, Model: config.Model}
	availability, availabilityErr := ai.Availability(tenantID)
	if availabilityErr == nil {
		result.Availability = availability
	}
	operationType := strings.TrimSpace(envelope.OperationType)
	if operationType == "" {
		if envelope.Product != nil {
			operationType = AgentOperationTicketProductCreate
		} else if len(envelope.Operations) > 0 {
			operationType = AgentOperationCatalogBatchChange
		}
	}
	switch operationType {
	case AgentOperationCatalogBatchChange:
		result.OperationType = operationType
		if len(envelope.Operations) == 0 {
			result.Context = agentTaskContext{OperationType: operationType}
			result.Missing = []AgentMissingField{{Field: "operations", Label: "操作内容", Question: "请说明要操作哪些票种、检票点以及增加、移除或设置次数。"}}
			return result, nil
		}
		products, checkpoints, err := loadCatalogBatchCandidates(model.DB, tenantID)
		if err != nil {
			return nil, err
		}
		operations, err := resolveCatalogBatchOperations(model.DB, tenantID, envelope.Operations, products, checkpoints)
		if err != nil {
			return nil, err
		}
		idempotencyKey := fmt.Sprintf("agent-task-%d-v%d", task.ID, task.Version)
		preview, err := (&CatalogBatchChangeService{}).Preview(tenantID, actorUserID, actorRole, CatalogBatchChangePreviewRequest{InputText: input, IdempotencyKey: idempotencyKey, Operations: operations})
		if err != nil {
			return nil, err
		}
		previewJSON, err := json.Marshal(preview)
		if err != nil {
			return nil, err
		}
		result.Context = agentTaskContext{OperationType: operationType, Operations: operations}
		result.PreviewJSON = string(previewJSON)
		result.LinkedPlanID = preview.PlanID
		result.PlanHash = preview.PlanHash
		return result, nil
	case AgentOperationTicketProductCreate:
		result.OperationType = operationType
		candidate := envelope.Product
		if candidate == nil {
			result.Context = agentTaskContext{OperationType: operationType, Product: &agentProductDraft{}}
			result.Missing = []AgentMissingField{{Field: "product", Label: "票种信息", Question: "请提供票种名称、所属景区、售价、结算价和至少一个检票点。"}}
			return result, nil
		}
		var previous agentTaskContext
		if err := json.Unmarshal([]byte(contextJSON), &previous); err != nil && !errors.Is(err, io.EOF) {
			return nil, agentInvalid("agent task context is invalid")
		}
		merged := mergeProductDraft(previous.Product, candidate)
		resolved, missing, err := resolveProductDraft(model.DB, tenantID, merged)
		if err != nil {
			return nil, err
		}
		result.Context = agentTaskContext{OperationType: operationType, Product: resolved, Assumptions: productAssumptions(resolved)}
		result.Missing = missing
		if len(missing) == 0 {
			preview, err := productPreviewJSON(model.DB, tenantID, resolved, result.Context.Assumptions)
			if err != nil {
				return nil, err
			}
			result.PreviewJSON = preview
		}
		return result, nil
	default:
		return nil, agentInvalid("AI 无法识别要执行的业务类型，请明确是批量调整票规还是创建新票种")
	}
}

func (s *AgentTaskService) confirmProductTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	var response *AgentTaskView
	err := model.Write(func(tx *gorm.DB) error {
		var locked model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&locked).Error; err != nil {
			return err
		}
		if locked.State == AgentTaskCompleted {
			response = agentTaskViewFromModel(locked)
			return nil
		}
		if locked.State != AgentTaskAwaitingConfirmation || locked.OperationType != AgentOperationTicketProductCreate {
			return agentConflict("agent task is no longer executable")
		}
		var stored agentTaskContext
		if err := json.Unmarshal([]byte(locked.ContextJSON), &stored); err != nil || stored.Product == nil {
			return agentConflict("agent task product preview is invalid; create a new task")
		}
		product, rule, missing, err := productFromDraft(tx, tenantID, stored.Product)
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			return agentConflict("product details changed; provide the missing information again")
		}
		confirmedAt := time.Now()
		locked.State = AgentTaskExecuting
		locked.ConfirmedAt = &confirmedAt
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		if err := (&ProductService{}).createTx(tx, product, rule); err != nil {
			return err
		}
		resultJSON, err := json.Marshal(map[string]interface{}{"product_id": product.ID, "name": product.Name, "status": product.Status, "is_distributable": product.IsDistributable})
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "agent.task.confirm", "agent_task", locked.ID,
			"confirm AI planned ticket product creation", locked.PreviewJSON, string(resultJSON)); err != nil {
			return err
		}
		now := time.Now()
		locked.State = AgentTaskCompleted
		locked.ResultJSON = string(resultJSON)
		locked.CompletedAt = &now
		locked.ErrorMessage = ""
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		response = agentTaskViewFromModel(locked)
		storedResponse, _ := json.Marshal(response)
		return tx.Model(&locked).Update("last_response_json", string(storedResponse)).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentNotFound("agent task not found")
		}
		return nil, err
	}
	return response, nil
}

func agentTaskViewFromModel(task model.AgentTask) *AgentTaskView {
	view := &AgentTaskView{
		TaskID: task.ID, OperationType: task.OperationType, State: task.State,
		InputText: task.InputText, PlanID: task.LinkedPlanID, PlanHash: task.PlanHash,
		Version: task.Version, ExpiresAt: task.ExpiresAt, ConfirmedAt: task.ConfirmedAt,
		CompletedAt: task.CompletedAt, ErrorMessage: task.ErrorMessage,
		CanConfirm: task.State == AgentTaskAwaitingConfirmation && strings.TrimSpace(task.PreviewJSON) != "",
	}
	_ = json.Unmarshal([]byte(task.MissingJSON), &view.MissingFields)
	if strings.TrimSpace(task.PreviewJSON) != "" {
		view.Preview = json.RawMessage(task.PreviewJSON)
	}
	if strings.TrimSpace(task.ResultJSON) != "" {
		view.Result = json.RawMessage(task.ResultJSON)
	}
	return view
}

func decodeAgentTaskView(value string) (*AgentTaskView, error) {
	var view AgentTaskView
	if err := json.Unmarshal([]byte(value), &view); err != nil {
		return nil, err
	}
	return &view, nil
}

func decodeJSONInto(value string, target interface{}) error {
	return json.Unmarshal([]byte(value), target)
}

func decodeAgentAIEnvelope(content string) (*agentAIEnvelope, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			content = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	var envelope agentAIEnvelope
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, agentInvalid("AI 返回的任务计划不是受支持的 JSON")
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, agentInvalid("AI 返回了多段任务计划")
	}
	for _, operation := range envelope.Operations {
		if len(operation.ProductIDs) > 0 || len(operation.CheckpointIDs) > 0 {
			return nil, agentInvalid("AI 不能直接提交数据库对象编号")
		}
	}
	return &envelope, nil
}

// agentProviderContextJSON deliberately strips server-resolved IDs before a
// task context is sent to the model. IDs are retained in the database for
// confirmation-time revalidation, but the provider only needs names and
// business values.
func agentProviderContextJSON(value string) (string, error) {
	var contextValue agentTaskContext
	if err := json.Unmarshal([]byte(value), &contextValue); err != nil {
		return "", agentInvalid("agent task context is invalid")
	}
	if contextValue.Product != nil {
		contextValue.Product.ScenicAreaID = 0
		for groupIndex := range contextValue.Product.Groups {
			for itemIndex := range contextValue.Product.Groups[groupIndex].Items {
				contextValue.Product.Groups[groupIndex].Items[itemIndex].CheckpointID = 0
			}
		}
	}
	for operationIndex := range contextValue.Operations {
		contextValue.Operations[operationIndex].ProductIDs = nil
		contextValue.Operations[operationIndex].CheckpointIDs = nil
	}
	encoded, err := json.Marshal(contextValue)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func agentCandidateContextJSON(tenantID uint) (string, error) {
	var areas []model.ScenicArea
	if err := model.DB.Where("tenant_id = ? AND status = ?", tenantID, "active").Order("id ASC").Find(&areas).Error; err != nil {
		return "", err
	}
	var checkpoints []model.CheckPoint
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&checkpoints).Error; err != nil {
		return "", err
	}
	var products []model.Product
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&products).Error; err != nil {
		return "", err
	}
	type candidate struct {
		ScenicAreas []string `json:"scenic_areas"`
		Checkpoints []struct {
			Name           string `json:"name"`
			ScenicAreaName string `json:"scenic_area_name"`
		} `json:"checkpoints"`
		Products []string `json:"products"`
	}
	value := candidate{ScenicAreas: make([]string, 0, len(areas)), Products: make([]string, 0, len(products))}
	for _, area := range areas {
		value.ScenicAreas = append(value.ScenicAreas, area.Name)
	}
	for _, checkpoint := range checkpoints {
		value.Checkpoints = append(value.Checkpoints, struct {
			Name           string `json:"name"`
			ScenicAreaName string `json:"scenic_area_name"`
		}{Name: checkpoint.Name, ScenicAreaName: scenicAreaNameForID(areas, checkpoint.ScenicAreaID)})
	}
	for _, product := range products {
		if !isDistributedListing(&product) {
			value.Products = append(value.Products, product.Name)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("build agent candidate context: %w", err)
	}
	if len(encoded) > 2<<20 {
		return "", agentInvalid("当前租户目录过大，请先使用结构化选择缩小范围")
	}
	return string(encoded), nil
}

func scenicAreaNameForID(areas []model.ScenicArea, id uint) string {
	for _, area := range areas {
		if area.ID == id {
			return area.Name
		}
	}
	return ""
}

func mergeProductDraft(previous *agentProductDraft, candidate *agentProductCandidate) *agentProductDraft {
	result := &agentProductDraft{}
	if previous != nil {
		encoded, _ := json.Marshal(previous)
		_ = json.Unmarshal(encoded, result)
	}
	if candidate == nil {
		return result
	}
	if strings.TrimSpace(candidate.Name) != "" {
		result.Name = strings.TrimSpace(candidate.Name)
	}
	if strings.TrimSpace(candidate.ScenicAreaName) != "" {
		result.ScenicAreaName = strings.TrimSpace(candidate.ScenicAreaName)
	}
	if candidate.Price != nil {
		result.Price = candidate.Price
	}
	if candidate.SettlementPrice != nil {
		result.SettlementPrice = candidate.SettlementPrice
	}
	if strings.TrimSpace(candidate.ValidityType) != "" {
		result.ValidityType = strings.TrimSpace(candidate.ValidityType)
	}
	if candidate.ValidityDays != nil {
		result.ValidityDays = candidate.ValidityDays
	}
	if strings.TrimSpace(candidate.ValidityStart) != "" {
		result.ValidityStart = strings.TrimSpace(candidate.ValidityStart)
	}
	if strings.TrimSpace(candidate.ValidityEnd) != "" {
		result.ValidityEnd = strings.TrimSpace(candidate.ValidityEnd)
	}
	if strings.TrimSpace(candidate.RuleName) != "" {
		result.RuleName = strings.TrimSpace(candidate.RuleName)
	}
	if candidate.Groups != nil && len(candidate.Groups) > 0 {
		result.Groups = candidate.Groups
	}
	if strings.TrimSpace(candidate.CodeMode) != "" {
		result.CodeMode = strings.TrimSpace(candidate.CodeMode)
	}
	if strings.TrimSpace(candidate.StockType) != "" {
		result.StockType = strings.TrimSpace(candidate.StockType)
	}
	if candidate.DailyStock != nil {
		result.DailyStock = candidate.DailyStock
	}
	if candidate.RealNameRequired != nil {
		result.RealNameRequired = candidate.RealNameRequired
	}
	if strings.TrimSpace(candidate.RefundType) != "" {
		result.RefundType = strings.TrimSpace(candidate.RefundType)
	}
	if strings.TrimSpace(candidate.RefundRule) != "" {
		result.RefundRule = strings.TrimSpace(candidate.RefundRule)
	}
	if strings.TrimSpace(candidate.Tags) != "" {
		result.Tags = strings.TrimSpace(candidate.Tags)
	}
	if strings.TrimSpace(candidate.GateVoiceCode) != "" {
		result.GateVoiceCode = strings.TrimSpace(candidate.GateVoiceCode)
	}
	if candidate.LimitPerPhone != nil {
		result.LimitPerPhone = candidate.LimitPerPhone
	}
	if candidate.LimitPerID != nil {
		result.LimitPerID = candidate.LimitPerID
	}
	return result
}

func resolveProductDraft(tx *gorm.DB, tenantID uint, draft *agentProductDraft) (*agentProductDraft, []AgentMissingField, error) {
	if draft == nil {
		draft = &agentProductDraft{}
	}
	result := *draft
	missing := make([]AgentMissingField, 0)
	if strings.TrimSpace(result.Name) == "" {
		missing = append(missing, AgentMissingField{Field: "name", Label: "票种名称", Question: "请提供要创建的票种名称。"})
	}
	if result.Price == nil {
		missing = append(missing, AgentMissingField{Field: "price", Label: "售价", Question: "请提供票面售价，例如 120 元。"})
	} else if *result.Price < 0 {
		return nil, nil, agentInvalid("售价不能为负数")
	}
	if result.SettlementPrice == nil {
		missing = append(missing, AgentMissingField{Field: "settlement_price", Label: "结算价", Question: "请提供供应商结算价，例如 80 元。系统不会猜测或代填。"})
	} else if *result.SettlementPrice < 0 {
		return nil, nil, agentInvalid("结算价不能为负数")
	}
	var areas []model.ScenicArea
	if err := tx.Where("tenant_id = ? AND status = ?", tenantID, "active").Order("id ASC").Find(&areas).Error; err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(result.ScenicAreaName) == "" {
		if len(areas) == 1 {
			result.ScenicAreaName = areas[0].Name
			result.ScenicAreaID = areas[0].ID
		} else {
			options := make([]string, 0, len(areas))
			for _, area := range areas {
				options = append(options, area.Name)
			}
			missing = append(missing, AgentMissingField{Field: "scenic_area_name", Label: "所属景区", Question: "请指定票种所属景区。", Options: options})
		}
	} else {
		matches := make([]model.ScenicArea, 0, 1)
		for _, area := range areas {
			if strings.TrimSpace(area.Name) == strings.TrimSpace(result.ScenicAreaName) {
				matches = append(matches, area)
			}
		}
		if len(matches) == 0 {
			missing = append(missing, AgentMissingField{Field: "scenic_area_name", Label: "所属景区", Question: fmt.Sprintf("景区 %q 不属于当前租户，请从候选景区中选择。", result.ScenicAreaName), Options: scenicAreaNames(areas)})
		} else {
			result.ScenicAreaID = matches[0].ID
		}
	}
	if len(result.Groups) == 0 {
		missing = append(missing, AgentMissingField{Field: "groups", Label: "检票规则", Question: "请至少提供一个检票点，例如“北门，每点最多 1 次”。"})
	}
	if result.ValidityType == "" {
		result.ValidityType = "date"
	}
	if result.ValidityType != "date" && result.ValidityType != "days" {
		return nil, nil, agentInvalid("有效期类型只能是 date 或 days")
	}
	if result.ValidityType == "days" && (result.ValidityDays == nil || *result.ValidityDays <= 0) {
		missing = append(missing, AgentMissingField{Field: "validity_days", Label: "有效天数", Question: "请提供购买后有效天数。"})
	}
	if result.RuleName == "" && result.Name != "" {
		result.RuleName = result.Name
	}
	if result.CodeMode == "" {
		result.CodeMode = "order"
	}
	if result.StockType == "" {
		result.StockType = "unlimited"
	}
	if result.RefundType == "" {
		result.RefundType = "no_refund"
	}
	if result.GateVoiceCode == "" {
		result.GateVoiceCode = "welcome"
	}
	if result.ScenicAreaID != 0 {
		var checkpoints []model.CheckPoint
		if err := tx.Where("tenant_id = ? AND scenic_area_id = ?", tenantID, result.ScenicAreaID).Order("id ASC").Find(&checkpoints).Error; err != nil {
			return nil, nil, err
		}
		seen := make(map[uint]struct{})
		for gi := range result.Groups {
			group := &result.Groups[gi]
			if strings.TrimSpace(group.GroupName) == "" {
				group.GroupName = "默认分组"
			}
			if len(group.Items) == 0 {
				missing = append(missing, AgentMissingField{Field: fmt.Sprintf("groups[%d].items", gi), Label: "检票点", Question: "请为每个规则分组提供至少一个检票点。"})
				continue
			}
			for ii := range group.Items {
				item := &group.Items[ii]
				if item.MaxPerCheckIn == 0 {
					item.MaxPerCheckIn = 1
				}
				if item.MaxPerCheckIn < 1 || item.MaxPerCheckIn > 1000 {
					return nil, nil, agentInvalid("单个检票点次数必须在 1 到 1000 之间")
				}
				matches := make([]model.CheckPoint, 0, 1)
				for _, checkpoint := range checkpoints {
					if strings.TrimSpace(checkpoint.Name) == strings.TrimSpace(item.CheckpointName) {
						matches = append(matches, checkpoint)
					}
				}
				if strings.TrimSpace(item.CheckpointName) == "" || len(matches) == 0 {
					missing = append(missing, AgentMissingField{Field: fmt.Sprintf("groups[%d].items[%d].checkpoint_name", gi, ii), Label: "检票点", Question: "请提供当前景区内的准确检票点名称。", Options: checkpointNames(checkpoints)})
					continue
				}
				item.CheckpointID = matches[0].ID
				if _, exists := seen[item.CheckpointID]; exists {
					return nil, nil, agentInvalid("同一个检票点不能重复出现在新票种规则中")
				}
				seen[item.CheckpointID] = struct{}{}
				if group.MaxTotalCheckIn < 0 || (group.MaxTotalCheckIn > 0 && group.MaxTotalCheckIn > len(group.Items)) {
					return nil, nil, agentInvalid("规则分组总次数无效")
				}
			}
		}
	}
	return &result, missing, nil
}

func productFromDraft(tx *gorm.DB, tenantID uint, draft *agentProductDraft) (*model.Product, *model.TicketRule, []AgentMissingField, error) {
	resolved, missing, err := resolveProductDraft(tx, tenantID, draft)
	if err != nil || len(missing) > 0 {
		return nil, nil, missing, err
	}
	product := &model.Product{
		TenantID: tenantID, Name: resolved.Name, Price: *resolved.Price, SettlementPrice: *resolved.SettlementPrice,
		ScenicAreaID: resolved.ScenicAreaID, Type: "online", Status: "offline", IsDistributable: false,
		ValidityType: resolved.ValidityType, CodeMode: resolved.CodeMode, StockType: resolved.StockType,
		RefundType: resolved.RefundType, RefundRule: resolved.RefundRule, Tags: resolved.Tags,
		GateVoiceCode: resolved.GateVoiceCode,
	}
	if resolved.ValidityDays != nil {
		product.ValidityDays = *resolved.ValidityDays
	}
	if resolved.DailyStock != nil {
		product.DailyStock = *resolved.DailyStock
	}
	if resolved.RealNameRequired != nil {
		product.RealNameRequired = *resolved.RealNameRequired
	}
	if resolved.LimitPerPhone != nil {
		product.LimitPerPhone = *resolved.LimitPerPhone
	}
	if resolved.LimitPerID != nil {
		product.LimitPerID = *resolved.LimitPerID
	}
	if resolved.ValidityStart != "" {
		value, parseErr := time.Parse("2006-01-02", resolved.ValidityStart)
		if parseErr != nil {
			return nil, nil, nil, agentInvalid("有效期开始日期必须使用 YYYY-MM-DD")
		}
		product.ValidityStartDate = &value
	}
	if resolved.ValidityEnd != "" {
		value, parseErr := time.Parse("2006-01-02", resolved.ValidityEnd)
		if parseErr != nil {
			return nil, nil, nil, agentInvalid("有效期结束日期必须使用 YYYY-MM-DD")
		}
		product.ValidityEndDate = &value
	}
	rule := &model.TicketRule{TenantID: tenantID, Name: resolved.RuleName, ValidityType: resolved.ValidityType}
	for _, group := range resolved.Groups {
		ruleGroup := model.RuleGroup{GroupName: group.GroupName, MaxTotalCheckIn: group.MaxTotalCheckIn}
		for _, item := range group.Items {
			ruleGroup.Items = append(ruleGroup.Items, model.RuleItem{CheckPointID: item.CheckpointID, MaxPerCheckIn: item.MaxPerCheckIn})
		}
		rule.Groups = append(rule.Groups, ruleGroup)
	}
	return product, rule, nil, nil
}

func productPreviewJSON(tx *gorm.DB, tenantID uint, draft *agentProductDraft, assumptions []string) (string, error) {
	product, rule, missing, err := productFromDraft(tx, tenantID, draft)
	if err != nil {
		return "", err
	}
	if len(missing) > 0 {
		return "", agentInvalid("product preview is missing required fields")
	}
	preview := struct {
		OperationType  string                `json:"operation_type"`
		Product        *model.Product        `json:"product"`
		Rule           *model.TicketRule     `json:"rule"`
		ScenicAreaName string                `json:"scenic_area_name"`
		RuleGroups     []agentRuleDraftGroup `json:"rule_groups"`
		Assumptions    []string              `json:"assumptions,omitempty"`
		Safety         []string              `json:"safety"`
	}{
		OperationType:  AgentOperationTicketProductCreate,
		Product:        product,
		Rule:           rule,
		ScenicAreaName: draft.ScenicAreaName,
		RuleGroups:     draft.Groups,
		Assumptions:    assumptions,
		Safety:         []string{"确认前不会写入产品、规则、版本或渠道映射。", "确认后产品状态固定为 offline，is_distributable 固定为 false。"},
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func productAssumptions(draft *agentProductDraft) []string {
	assumptions := []string{"系统将以 offline 创建，且不会创建任何分销授权、渠道商品或上架映射。", "未特别说明时使用订单码、库存不限、不可退款、默认闸机语音 welcome。"}
	if draft != nil && draft.RuleName == draft.Name {
		assumptions = append(assumptions, "规则名称未单独提供，使用票种名称。")
	}
	if draft != nil && draft.ValidityType == "date" && draft.ValidityStart == "" && draft.ValidityEnd == "" {
		assumptions = append(assumptions, "未提供固定日期，先按 date 类型保存，后续可在管理端补充有效期。")
	}
	return assumptions
}

func scenicAreaNames(values []model.ScenicArea) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	return result
}
func checkpointNames(values []model.CheckPoint) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	return result
}
