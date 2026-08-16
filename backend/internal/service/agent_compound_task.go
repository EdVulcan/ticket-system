package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var compoundExecutionLocks sync.Map

func compoundExecutionLock(taskID uint) *sync.Mutex {
	lock, _ := compoundExecutionLocks.LoadOrStore(taskID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func compoundPlanHash(children []agentCompoundChildPlan, preview string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(preview))
	for _, child := range children {
		_, _ = hash.Write([]byte(fmt.Sprintf("\x00%s\x00%s\x00%d", child.OperationType, child.PlanHash, child.LinkedPlanID)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func compoundChildIdempotencyKey(parentID uint, index int, planHash string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("compound:%d:%d:%s", parentID, index, planHash)))
	return fmt.Sprintf("agent-compound-%d-%d-%s", parentID, index, hex.EncodeToString(digest[:12]))
}

func (s *AgentTaskService) planCompoundFromEnvelope(tenantID, actorUserID uint, actorRole string, task model.AgentTask, input, contextJSON string, config model.PlatformAIConfig, ai *PlatformAIService, candidate *agentCompoundCandidate) (*agentPlanningResult, error) {
	result := &agentPlanningResult{OperationType: AgentOperationCompound, Provider: config.Provider, Model: config.Model}
	var previous agentTaskContext
	if strings.TrimSpace(contextJSON) != "" {
		if err := json.Unmarshal([]byte(contextJSON), &previous); err != nil {
			return nil, agentInvalid("agent task context is invalid")
		}
	}
	if candidate == nil && previous.Compound != nil {
		candidate = compoundCandidateFromDraft(previous.Compound)
	}
	if candidate == nil || len(candidate.Steps) < 2 || len(candidate.Steps) > 5 {
		result.Context = agentTaskContext{OperationType: AgentOperationCompound}
		result.Missing = []AgentMissingField{{Field: "compound.steps", Label: "操作步骤", Question: "请说明 2 到 5 个要连续预览的低风险操作步骤。"}}
		return result, nil
	}
	pack, err := agentKnowledgePackForOperation(AgentOperationCompound)
	if err != nil {
		return nil, err
	}
	compound := &agentCompoundDraft{Steps: make([]agentCompoundStepDraft, 0, len(candidate.Steps))}
	children := make([]agentCompoundChildPlan, 0, len(candidate.Steps))
	previews := make([]agentCompoundPreviewStep, 0, len(candidate.Steps))
	missing := make([]AgentMissingField, 0)
	seenTargets := make(map[string]int)
	for index, step := range candidate.Steps {
		operationType := strings.TrimSpace(step.OperationType)
		if operationType != AgentOperationCatalogBatchChange && operationType != AgentOperationTicketProductCreate && operationType != AgentOperationTicketProductUpdate && operationType != AgentOperationTicketProductBatchUpdate {
			return nil, agentInvalid(fmt.Sprintf("复合计划第 %d 步包含不受支持的操作", index+1))
		}
		previousContext := previousCompoundStepContext(previous.Compound, index)
		childCandidate := mergeCompoundStepCandidate(previousContext, step)
		childEnvelope := compoundStepEnvelope(operationType, childCandidate)
		childContextJSON := ""
		if previousContext != nil {
			encoded, marshalErr := json.Marshal(previousContext)
			if marshalErr != nil {
				return nil, marshalErr
			}
			childContextJSON = string(encoded)
		}
		childTask := task
		childTask.OperationType = operationType
		// Catalog preview idempotency is derived from task ID/version. Give each
		// synthetic planning step a distinct version so two catalog steps cannot
		// accidentally reuse one durable domain plan key.
		childTask.Version = task.Version + index + 1
		if childContextJSON == "" {
			childTask.ContextJSON = `{"operation_type":"` + operationType + `"}`
		} else {
			childTask.ContextJSON = childContextJSON
		}
		childPlanning, planErr := s.planFromEnvelope(tenantID, actorUserID, actorRole, childTask, input, childTask.ContextJSON, config, ai, childEnvelope)
		if planErr != nil {
			return nil, fmt.Errorf("compound step %d: %w", index+1, planErr)
		}
		for _, field := range childPlanning.Missing {
			field.Field = fmt.Sprintf("steps[%d].%s", index, field.Field)
			field.Label = fmt.Sprintf("步骤 %d · %s", index+1, field.Label)
			missing = append(missing, field)
		}
		stepDraft := agentCompoundStepDraft{Index: index, OperationType: operationType, Status: AgentTaskAwaitingConfirmation, Context: &childPlanning.Context}
		compound.Steps = append(compound.Steps, stepDraft)
		if len(childPlanning.Missing) == 0 && strings.TrimSpace(childPlanning.PreviewJSON) != "" {
			for _, target := range compoundStepTargetKeys(childPlanning.Context) {
				if previousIndex, exists := seenTargets[target]; exists {
					return nil, agentConflict(fmt.Sprintf("复合计划第 %d 步与第 %d 步操作同一票种，请合并为一个步骤后再预览", index+1, previousIndex+1))
				}
				seenTargets[target] = index
			}
			children = append(children, agentCompoundChildPlan{OperationType: operationType, Context: childPlanning.Context, PreviewJSON: childPlanning.PreviewJSON, LinkedPlanID: childPlanning.LinkedPlanID, PlanHash: childPlanning.PlanHash})
			previews = append(previews, agentCompoundPreviewStep{Index: index + 1, OperationType: operationType, Status: AgentTaskAwaitingConfirmation, Preview: json.RawMessage(childPlanning.PreviewJSON)})
		}
	}
	result.Context = agentTaskContext{OperationType: AgentOperationCompound, KnowledgePackID: pack.ID, SkillVersion: pack.Version, SkillHash: pack.Hash, Compound: compound}
	if len(missing) > 0 {
		result.Missing = missing
		return result, nil
	}
	preview, marshalErr := json.Marshal(agentCompoundPreview{
		OperationType: AgentOperationCompound,
		StepCount:     len(previews),
		Steps:         previews,
		Safety: []string{
			"确认前不会写入票种、票规或分销数据。",
			"确认后按步骤顺序执行，每一步仍使用现有租户归属、版本和状态校验。",
			"步骤之间不是跨域原子事务；中途失败时已完成步骤保留，重试只继续未完成步骤。",
		},
	})
	if marshalErr != nil {
		return nil, marshalErr
	}
	result.PreviewJSON = string(preview)
	result.PlanHash = compoundPlanHash(children, result.PreviewJSON)
	result.CompoundChildren = children
	return result, nil
}

func compoundCandidateFromDraft(draft *agentCompoundDraft) *agentCompoundCandidate {
	if draft == nil {
		return nil
	}
	candidate := &agentCompoundCandidate{Steps: make([]agentCompoundStepCandidate, 0, len(draft.Steps))}
	for _, step := range draft.Steps {
		value := agentCompoundStepCandidate{OperationType: step.OperationType}
		if step.Context != nil {
			value = mergeCompoundStepCandidate(step.Context, value)
		}
		candidate.Steps = append(candidate.Steps, value)
	}
	return candidate
}

func compoundStepTargetKeys(context agentTaskContext) []string {
	keys := make([]string, 0)
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	for _, operation := range context.Operations {
		for _, productID := range operation.ProductIDs {
			add(fmt.Sprintf("product:%d", productID))
		}
	}
	if context.Product != nil {
		add("name:" + strings.ToLower(strings.TrimSpace(context.Product.Name)))
	}
	if context.ProductUpdate != nil {
		if context.ProductUpdate.ProductID != 0 {
			add(fmt.Sprintf("product:%d", context.ProductUpdate.ProductID))
		} else {
			add("name:" + strings.ToLower(strings.TrimSpace(context.ProductUpdate.ProductName)))
		}
	}
	if context.ProductBatchUpdate != nil {
		for _, target := range context.ProductBatchUpdate.Targets {
			if target.ProductID != 0 {
				add(fmt.Sprintf("product:%d", target.ProductID))
			} else {
				add("name:" + strings.ToLower(strings.TrimSpace(target.ProductName)))
			}
		}
	}
	return keys
}

func compoundStepEnvelope(operationType string, candidate agentCompoundStepCandidate) *agentAIEnvelope {
	return &agentAIEnvelope{OperationType: operationType, Operations: candidate.Operations, Product: candidate.Product, ProductUpdate: candidate.ProductUpdate, ProductBatchUpdate: candidate.ProductBatchUpdate}
}

func previousCompoundStepContext(compound *agentCompoundDraft, index int) *agentTaskContext {
	if compound == nil || index < 0 || index >= len(compound.Steps) || compound.Steps[index].Context == nil {
		return nil
	}
	context := *compound.Steps[index].Context
	return &context
}

func mergeCompoundStepCandidate(previous *agentTaskContext, candidate agentCompoundStepCandidate) agentCompoundStepCandidate {
	if previous == nil {
		return candidate
	}
	if candidate.OperationType == "" {
		candidate.OperationType = previous.OperationType
	}
	if candidate.Product == nil && previous.Product != nil {
		candidate.Product = productCandidateFromDraft(previous.Product)
	}
	if candidate.ProductUpdate == nil && previous.ProductUpdate != nil {
		candidate.ProductUpdate = &agentProductUpdateCandidate{ProductName: previous.ProductUpdate.ProductName, Changes: previous.ProductUpdate.Changes}
	}
	if candidate.ProductBatchUpdate == nil && previous.ProductBatchUpdate != nil {
		names := make([]string, 0, len(previous.ProductBatchUpdate.Targets))
		for _, target := range previous.ProductBatchUpdate.Targets {
			if strings.TrimSpace(target.ProductName) != "" {
				names = append(names, target.ProductName)
			}
		}
		candidate.ProductBatchUpdate = &agentProductBatchUpdateCandidate{ProductNames: names, Changes: previous.ProductBatchUpdate.Changes}
	}
	if len(candidate.Operations) == 0 && len(previous.Operations) > 0 {
		candidate.Operations = make([]CatalogRuleOperation, len(previous.Operations))
		copy(candidate.Operations, previous.Operations)
		for index := range candidate.Operations {
			candidate.Operations[index].ProductIDs = nil
			candidate.Operations[index].CheckpointIDs = nil
		}
	}
	return candidate
}

func productCandidateFromDraft(draft *agentProductDraft) *agentProductCandidate {
	if draft == nil {
		return nil
	}
	candidate := &agentProductCandidate{
		Name: draft.Name, ProductType: draft.ProductType, ScenicAreaName: draft.ScenicAreaName,
		Price: draft.Price, SettlementPrice: draft.SettlementPrice, ValidityType: draft.ValidityType,
		ValidityDays: draft.ValidityDays, ValidityStart: draft.ValidityStart, ValidityEnd: draft.ValidityEnd,
		RuleName: draft.RuleName, CodeMode: draft.CodeMode, StockType: draft.StockType, DailyStock: draft.DailyStock,
		RealNameRequired: draft.RealNameRequired, RefundType: draft.RefundType, RefundRule: draft.RefundRule,
		Tags: draft.Tags, GateVoiceCode: draft.GateVoiceCode, LimitPerPhone: draft.LimitPerPhone, LimitPerID: draft.LimitPerID,
	}
	candidate.Groups = make([]agentRuleDraftGroup, len(draft.Groups))
	copy(candidate.Groups, draft.Groups)
	for groupIndex := range candidate.Groups {
		candidate.Groups[groupIndex].Items = append([]agentRuleDraftItem(nil), draft.Groups[groupIndex].Items...)
		for itemIndex := range candidate.Groups[groupIndex].Items {
			candidate.Groups[groupIndex].Items[itemIndex].CheckpointID = 0
		}
	}
	return candidate
}

func (s *AgentTaskService) createCompoundChildTasksTx(tx *gorm.DB, parent *model.AgentTask, planning *agentPlanningResult) error {
	if parent == nil || planning == nil || planning.OperationType != AgentOperationCompound || planning.Context.Compound == nil {
		return agentConflict("复合计划上下文无效，请重新生成预览")
	}
	if len(planning.CompoundChildren) != len(planning.Context.Compound.Steps) {
		return agentConflict("复合计划步骤不完整，请重新生成预览")
	}
	for index, childPlan := range planning.CompoundChildren {
		contextJSON, err := json.Marshal(childPlan.Context)
		if err != nil {
			return err
		}
		child := model.AgentTask{
			TenantID: parent.TenantID, ActorUserID: parent.ActorUserID, ActorRole: parent.ActorRole,
			OperationType: childPlan.OperationType, State: AgentTaskAwaitingConfirmation,
			InputText: parent.InputText, ContextJSON: string(contextJSON), MissingJSON: `[]`,
			PreviewJSON: childPlan.PreviewJSON, PlanHash: childPlan.PlanHash, LinkedPlanID: childPlan.LinkedPlanID,
			IdempotencyKey: compoundChildIdempotencyKey(parent.ID, index, childPlan.PlanHash), Version: 1,
			ExpiresAt: parent.ExpiresAt, ProtocolMode: parent.ProtocolMode,
		}
		if err := tx.Create(&child).Error; err != nil {
			return err
		}
		planning.Context.Compound.Steps[index].ChildTaskID = child.ID
	}
	return nil
}

func (s *AgentTaskService) confirmCompoundTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	lock := compoundExecutionLock(task.ID)
	if !lock.TryLock() {
		return nil, agentConflict("复合任务正在执行，请稍后重试")
	}
	defer lock.Unlock()
	var executing model.AgentTask
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&executing).Error; err != nil {
			return err
		}
		if executing.State == AgentTaskCompleted {
			return nil
		}
		if executing.State != AgentTaskAwaitingConfirmation && executing.State != AgentTaskExecuting {
			return agentConflict("复合任务已不在可执行状态")
		}
		if executing.State == AgentTaskAwaitingConfirmation {
			now := time.Now()
			executing.State = AgentTaskExecuting
			executing.ConfirmedAt = &now
			executing.Version++
			if err := tx.Save(&executing).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentNotFound("agent task not found")
		}
		return nil, err
	}
	if executing.State == AgentTaskCompleted {
		return agentTaskViewFromModel(executing), nil
	}
	return s.recoverExecutingCompoundTask(tenantID, actorUserID, actorRole, executing)
}

func (s *AgentTaskService) recoverExecutingCompoundTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
	var context agentTaskContext
	if err := json.Unmarshal([]byte(task.ContextJSON), &context); err != nil || context.Compound == nil {
		return nil, agentConflict("复合任务上下文无效，请重新生成预览")
	}
	for index := range context.Compound.Steps {
		step := &context.Compound.Steps[index]
		if step.ChildTaskID == 0 {
			return nil, s.failCompoundAndConflict(tenantID, actorUserID, task.ID, fmt.Sprintf("步骤 %d 缺少持久化子任务", index+1))
		}
		var child model.AgentTask
		if err := model.DB.Where("id = ? AND tenant_id = ? AND actor_user_id = ?", step.ChildTaskID, tenantID, actorUserID).First(&child).Error; err != nil {
			return nil, err
		}
		if child.State == AgentTaskCompleted {
			step.Status = AgentTaskCompleted
			if err := s.saveCompoundProgress(tenantID, actorUserID, task.ID, context); err != nil {
				return nil, err
			}
			continue
		}
		if child.State == AgentTaskFailed || child.State == AgentTaskCancelled || child.State == AgentTaskExpired {
			message := child.ErrorMessage
			if strings.TrimSpace(message) == "" {
				message = "子任务已不可执行"
			}
			return nil, s.failCompoundAndConflict(tenantID, actorUserID, task.ID, fmt.Sprintf("步骤 %d 未完成：%s", index+1, message))
		}
		step.Status = AgentTaskExecuting
		if err := s.saveCompoundProgress(tenantID, actorUserID, task.ID, context); err != nil {
			return nil, err
		}
		_, confirmErr := s.Confirm(tenantID, actorUserID, actorRole, child.ID)
		if confirmErr != nil {
			var taskErr *AgentTaskError
			if errors.As(confirmErr, &taskErr) {
				return nil, s.failCompoundAndConflict(tenantID, actorUserID, task.ID, fmt.Sprintf("步骤 %d 未完成：%s", index+1, taskErr.Message))
			}
			return nil, confirmErr
		}
		step.Status = AgentTaskCompleted
		if err := s.saveCompoundProgress(tenantID, actorUserID, task.ID, context); err != nil {
			return nil, err
		}
	}
	return s.completeCompoundTask(tenantID, actorUserID, task, context)
}

func (s *AgentTaskService) saveCompoundProgress(tenantID, actorUserID, taskID uint, context agentTaskContext) error {
	encoded, err := json.Marshal(context)
	if err != nil {
		return err
	}
	return model.Write(func(tx *gorm.DB) error {
		var task model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", taskID, tenantID, actorUserID).First(&task).Error; err != nil {
			return err
		}
		if task.State == AgentTaskCompleted || task.State == AgentTaskFailed {
			return nil
		}
		task.ContextJSON = string(encoded)
		task.Version++
		return tx.Save(&task).Error
	})
}

func compoundResult(context agentTaskContext) string {
	steps := make([]map[string]interface{}, 0, len(context.Compound.Steps))
	for index, step := range context.Compound.Steps {
		steps = append(steps, map[string]interface{}{"index": index + 1, "operation_type": step.OperationType, "status": step.Status})
	}
	encoded, _ := json.Marshal(map[string]interface{}{"operation_type": AgentOperationCompound, "steps": steps, "partial": false})
	return string(encoded)
}

func (s *AgentTaskService) completeCompoundTask(tenantID, actorUserID uint, task model.AgentTask, context agentTaskContext) (*AgentTaskView, error) {
	resultJSON := compoundResult(context)
	var response *AgentTaskView
	err := model.Write(func(tx *gorm.DB) error {
		var completed model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", task.ID, tenantID, actorUserID).First(&completed).Error; err != nil {
			return err
		}
		if completed.State == AgentTaskCompleted {
			response = agentTaskViewFromModel(completed)
			return nil
		}
		if completed.State != AgentTaskExecuting || completed.OperationType != AgentOperationCompound {
			return agentConflict("复合任务已不在执行状态")
		}
		now := time.Now()
		completed.State = AgentTaskCompleted
		completed.CompletedAt = &now
		completed.ResultJSON = resultJSON
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

func (s *AgentTaskService) failCompoundTask(tenantID, actorUserID, taskID uint, message string) error {
	return model.Write(func(tx *gorm.DB) error {
		var task model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND actor_user_id = ?", taskID, tenantID, actorUserID).First(&task).Error; err != nil {
			return err
		}
		if task.State == AgentTaskCompleted || task.State == AgentTaskFailed {
			return agentConflict(strings.TrimSpace(message))
		}
		task.State = AgentTaskFailed
		task.ErrorMessage = strings.TrimSpace(message)
		task.Version++
		return tx.Save(&task).Error
	})
}

func (s *AgentTaskService) failCompoundAndConflict(tenantID, actorUserID, taskID uint, message string) error {
	if err := s.failCompoundTask(tenantID, actorUserID, taskID, message); err != nil {
		return err
	}
	return agentConflict(strings.TrimSpace(message))
}
