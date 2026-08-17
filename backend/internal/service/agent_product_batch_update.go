package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func mergeProductBatchUpdateDraft(previous *agentProductBatchUpdateDraft, candidate *agentProductBatchUpdateCandidate) *agentProductBatchUpdateDraft {
	merged := &agentProductBatchUpdateDraft{}
	if previous != nil {
		*merged = *previous
		merged.Targets = append([]agentProductBatchUpdateTarget(nil), previous.Targets...)
	}
	if candidate != nil && len(candidate.ProductNames) > 0 {
		merged.Targets = make([]agentProductBatchUpdateTarget, 0, len(candidate.ProductNames))
		for _, name := range candidate.ProductNames {
			merged.Targets = append(merged.Targets, agentProductBatchUpdateTarget{ProductName: strings.TrimSpace(name)})
		}
	}
	if candidate != nil {
		mergeProductUpdateChanges(&merged.Changes, candidate.Changes)
	}
	return merged
}

func resolveProductBatchUpdateDraft(db *gorm.DB, tenantID uint, draft *agentProductBatchUpdateDraft) (*agentProductBatchUpdateDraft, []AgentMissingField, error) {
	if draft == nil {
		draft = &agentProductBatchUpdateDraft{}
	}
	resolved := &agentProductBatchUpdateDraft{Targets: append([]agentProductBatchUpdateTarget(nil), draft.Targets...), Changes: draft.Changes}
	if len(resolved.Targets) == 0 {
		options := make([]string, 0)
		var products []model.Product
		if err := db.Select("id", "name", "source_product_id", "source_tenant_id", "fulfillment_product_id", "fulfillment_tenant_id").Where("tenant_id = ? AND deleted_at IS NULL", tenantID).Order("name ASC").Limit(100).Find(&products).Error; err != nil {
			return nil, nil, err
		}
		for _, product := range products {
			if isDistributedListing(&product) {
				continue
			}
			if name := strings.TrimSpace(product.Name); name != "" {
				options = append(options, name)
			}
		}
		return resolved, []AgentMissingField{{Field: "product_names", Label: "票种", Question: "请提供至少两个准确的当前租户自有票种名称。", Options: options}}, nil
	}
	if len(resolved.Targets) < 2 {
		return nil, nil, agentInvalid("批量修改至少需要两个准确的票种名称")
	}
	if len(resolved.Targets) > 50 {
		return nil, nil, agentInvalid("一次最多批量修改 50 个票种")
	}

	names := make([]string, 0, len(resolved.Targets))
	seen := make(map[string]struct{}, len(resolved.Targets))
	for index := range resolved.Targets {
		name := strings.TrimSpace(resolved.Targets[index].ProductName)
		if name == "" || len([]rune(name)) > 100 {
			return nil, nil, agentInvalid("批量修改中的票种名称不能为空且不能超过 100 个字符")
		}
		if _, exists := seen[name]; exists {
			return nil, nil, agentInvalid(fmt.Sprintf("批量修改中的票种名称重复：%s", name))
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	var products []model.Product
	if err := db.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("tenant_id = ? AND name IN ? AND deleted_at IS NULL", tenantID, names).Find(&products).Error; err != nil {
		return nil, nil, err
	}
	byName := make(map[string][]model.Product, len(products))
	for _, product := range products {
		byName[product.Name] = append(byName[product.Name], product)
	}
	for index := range resolved.Targets {
		name := strings.TrimSpace(resolved.Targets[index].ProductName)
		candidates := byName[name]
		owned := make([]model.Product, 0, len(candidates))
		for _, candidate := range candidates {
			if !isDistributedListing(&candidate) {
				owned = append(owned, candidate)
			}
		}
		if len(owned) == 0 {
			return nil, nil, agentInvalid(fmt.Sprintf("当前租户没有名为“%s”的自有票种", name))
		}
		if len(owned) > 1 {
			return nil, nil, agentInvalid(fmt.Sprintf("票种名称“%s”不唯一，请先使用查询工具确认准确名称", name))
		}
		product := owned[0]
		if product.CurrentRevisionID == 0 {
			var revision model.ProductRevision
			if err := db.Where("product_id = ? AND tenant_id = ?", product.ID, tenantID).Order("version DESC").First(&revision).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, nil, agentConflict(fmt.Sprintf("票种“%s”缺少当前版本，无法安全生成批量预览", name))
				}
				return nil, nil, err
			}
			product.CurrentRevisionID = revision.ID
		}
		resolved.Targets[index] = agentProductBatchUpdateTarget{ProductID: product.ID, CurrentRevisionID: product.CurrentRevisionID, ProductName: product.Name}
	}
	if resolved.Changes.Name != nil {
		return nil, nil, agentInvalid("批量修改不支持统一改名，请对单个票种单独生成预览")
	}
	if !hasProductUpdateChanges(resolved.Changes) {
		return resolved, []AgentMissingField{{Field: "changes", Label: "修改内容", Question: "请明确要批量修改哪些基础字段，例如售价、有效期、标签或限购规则。检票点和核销规则请使用票规调整操作。"}}, nil
	}
	if err := validateProductUpdateChanges(resolved.Changes); err != nil {
		return nil, nil, err
	}
	return resolved, nil, nil
}

func loadAgentBatchUpdateProducts(tx *gorm.DB, tenantID uint, draft *agentProductBatchUpdateDraft) ([]model.Product, error) {
	if draft == nil || len(draft.Targets) < 2 {
		return nil, agentConflict("批量票种修改预览缺少目标，请重新生成任务")
	}
	targets := append([]agentProductBatchUpdateTarget(nil), draft.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].ProductID < targets[j].ProductID })
	products := make([]model.Product, 0, len(targets))
	seen := make(map[uint]struct{}, len(targets))
	for _, target := range targets {
		if target.ProductID == 0 || target.CurrentRevisionID == 0 {
			return nil, agentConflict("批量票种修改预览缺少当前版本事实，请重新生成任务")
		}
		if _, exists := seen[target.ProductID]; exists {
			return nil, agentConflict("批量票种修改目标重复，请重新生成任务")
		}
		seen[target.ProductID] = struct{}{}
		var product model.Product
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", target.ProductID, tenantID)
		if err := query.First(&product).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, agentConflict(fmt.Sprintf("票种“%s”已不存在，请重新生成预览", target.ProductName))
			}
			return nil, err
		}
		if product.Name != target.ProductName || isDistributedListing(&product) {
			return nil, agentConflict(fmt.Sprintf("票种“%s”的归属已变化；分销副本不能批量修改，请重新生成预览", target.ProductName))
		}
		if product.CurrentRevisionID != target.CurrentRevisionID {
			return nil, agentConflict(fmt.Sprintf("票种“%s”的版本已变化，请重新生成批量预览", target.ProductName))
		}
		products = append(products, product)
	}
	return products, nil
}

type agentProductBatchUpdatePreviewLine struct {
	ProductName    string                     `json:"product_name"`
	ScenicAreaName string                     `json:"scenic_area_name"`
	Before         agentProductPreviewProduct `json:"before"`
	After          agentProductPreviewProduct `json:"after"`
}

type agentProductBatchUpdatePreview struct {
	OperationType string                               `json:"operation_type"`
	ProductCount  int                                  `json:"product_count"`
	Changes       []string                             `json:"changes"`
	Lines         []agentProductBatchUpdatePreviewLine `json:"lines"`
	Safety        []string                             `json:"safety"`
}

func productBatchUpdatePreviewJSON(db *gorm.DB, tenantID uint, draft *agentProductBatchUpdateDraft) (string, error) {
	products, err := loadAgentBatchUpdateProducts(db, tenantID, draft)
	if err != nil {
		return "", err
	}
	lines := make([]agentProductBatchUpdatePreviewLine, 0, len(products))
	for _, product := range products {
		before := productPreviewProductFromModel(product)
		after := product
		if err := applyProductUpdateChanges(&after, draft.Changes); err != nil {
			return "", err
		}
		var area model.ScenicArea
		if err := db.Where("id = ? AND tenant_id = ?", product.ScenicAreaID, tenantID).First(&area).Error; err != nil {
			return "", err
		}
		lines = append(lines, agentProductBatchUpdatePreviewLine{ProductName: product.Name, ScenicAreaName: area.Name, Before: before, After: productPreviewProductFromModel(after)})
	}
	preview := agentProductBatchUpdatePreview{
		OperationType: AgentOperationTicketProductBatchUpdate,
		ProductCount:  len(lines),
		Changes:       productUpdateChangeLabels(draft.Changes),
		Lines:         lines,
		Safety:        []string{"确认前不会写入任何票种或产品版本。", "所有目标必须仍属于当前租户自有票种；分销副本、名称或版本变化会拒绝整批操作。", "确认后每个票种生成新 ProductRevision，已售票据历史快照不会被改写。"},
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (s *AgentTaskService) confirmProductBatchUpdateTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
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
		if locked.State != AgentTaskAwaitingConfirmation || locked.OperationType != AgentOperationTicketProductBatchUpdate {
			return agentConflict("agent task is no longer executable")
		}
		var context agentTaskContext
		if err := json.Unmarshal([]byte(locked.ContextJSON), &context); err != nil || context.ProductBatchUpdate == nil {
			return agentConflict("agent batch product update preview is invalid; create a new task")
		}
		products, err := loadAgentBatchUpdateProducts(tx, tenantID, context.ProductBatchUpdate)
		if err != nil {
			return err
		}
		before := make([]agentProductBatchUpdatePreviewLine, 0, len(products))
		for index := range products {
			before = append(before, agentProductBatchUpdatePreviewLine{ProductName: products[index].Name, Before: productPreviewProductFromModel(products[index])})
			if err := applyProductUpdateChanges(&products[index], context.ProductBatchUpdate.Changes); err != nil {
				return err
			}
		}
		confirmedAt := time.Now()
		locked.State = AgentTaskExecuting
		locked.ConfirmedAt = &confirmedAt
		locked.Version++
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		for index := range products {
			if err := (&ProductService{}).updateTx(tx, products[index].ID, tenantID, &products[index], &products[index].Rule); err != nil {
				return err
			}
		}
		resultLines := make([]agentProductBatchUpdatePreviewLine, 0, len(products))
		for index := range products {
			var updated model.Product
			if err := tx.Where("id = ? AND tenant_id = ?", products[index].ID, tenantID).First(&updated).Error; err != nil {
				return err
			}
			resultLines = append(resultLines, agentProductBatchUpdatePreviewLine{ProductName: before[index].ProductName, Before: before[index].Before, After: productPreviewProductFromModel(updated)})
		}
		resultJSON, err := json.Marshal(map[string]interface{}{"product_count": len(resultLines), "changes": productUpdateChangeLabels(context.ProductBatchUpdate.Changes), "lines": resultLines})
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "agent.task.confirm", "agent_task", locked.ID,
			"confirm AI planned batch ticket product update", locked.PreviewJSON, string(resultJSON)); err != nil {
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
