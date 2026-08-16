package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// mergeProductUpdateDraft preserves server-resolved target facts while a user
// supplies missing update fields over multiple turns. A nil field means the
// provider did not receive a new value; a non-nil pointer, including false or
// an empty string, is an explicit user value.
func mergeProductUpdateDraft(previous *agentProductUpdateDraft, candidate *agentProductUpdateCandidate) *agentProductUpdateDraft {
	merged := &agentProductUpdateDraft{}
	if previous != nil {
		*merged = *previous
	}
	if candidate == nil {
		return merged
	}
	if strings.TrimSpace(candidate.ProductName) != "" {
		merged.ProductName = strings.TrimSpace(candidate.ProductName)
	}
	mergeProductUpdateChanges(&merged.Changes, candidate.Changes)
	return merged
}

func mergeProductUpdateChanges(target *agentProductUpdateChanges, source agentProductUpdateChanges) {
	if source.Name != nil {
		target.Name = source.Name
	}
	if source.Price != nil {
		target.Price = source.Price
	}
	if source.SettlementPrice != nil {
		target.SettlementPrice = source.SettlementPrice
	}
	if source.ValidityType != nil {
		target.ValidityType = source.ValidityType
	}
	if source.ValidityDays != nil {
		target.ValidityDays = source.ValidityDays
	}
	if source.ValidityStart != nil {
		target.ValidityStart = source.ValidityStart
	}
	if source.ValidityEnd != nil {
		target.ValidityEnd = source.ValidityEnd
	}
	if source.CodeMode != nil {
		target.CodeMode = source.CodeMode
	}
	if source.StockType != nil {
		target.StockType = source.StockType
	}
	if source.DailyStock != nil {
		target.DailyStock = source.DailyStock
	}
	if source.RealNameRequired != nil {
		target.RealNameRequired = source.RealNameRequired
	}
	if source.RefundType != nil {
		target.RefundType = source.RefundType
	}
	if source.RefundRule != nil {
		target.RefundRule = source.RefundRule
	}
	if source.Tags != nil {
		target.Tags = source.Tags
	}
	if source.GateVoiceCode != nil {
		target.GateVoiceCode = source.GateVoiceCode
	}
	if source.LimitPerPhone != nil {
		target.LimitPerPhone = source.LimitPerPhone
	}
	if source.LimitPerID != nil {
		target.LimitPerID = source.LimitPerID
	}
}

func resolveProductUpdateDraft(db *gorm.DB, tenantID uint, draft *agentProductUpdateDraft) (*agentProductUpdateDraft, []AgentMissingField, error) {
	if draft == nil {
		draft = &agentProductUpdateDraft{}
	}
	resolved := *draft
	resolved.ProductName = strings.TrimSpace(resolved.ProductName)
	missing := make([]AgentMissingField, 0)
	if resolved.ProductName == "" {
		options := make([]string, 0)
		var products []model.Product
		if err := db.Select("name").Where("tenant_id = ? AND deleted_at IS NULL AND source_product_id = 0 AND source_tenant_id = 0", tenantID).Order("name ASC").Limit(50).Find(&products).Error; err != nil {
			return nil, nil, err
		}
		for _, product := range products {
			if name := strings.TrimSpace(product.Name); name != "" {
				options = append(options, name)
			}
		}
		missing = append(missing, AgentMissingField{Field: "product_name", Label: "票种", Question: "请提供要修改的准确票种名称。", Options: options})
		return &resolved, missing, nil
	}

	var products []model.Product
	if err := db.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("tenant_id = ? AND name = ? AND deleted_at IS NULL", tenantID, resolved.ProductName).Find(&products).Error; err != nil {
		return nil, nil, err
	}
	owned := make([]model.Product, 0, len(products))
	for _, product := range products {
		if !isDistributedListing(&product) {
			owned = append(owned, product)
		}
	}
	if len(owned) == 0 {
		return nil, nil, agentInvalid(fmt.Sprintf("当前租户没有名为“%s”的自有票种", resolved.ProductName))
	}
	if len(owned) > 1 {
		return nil, nil, agentInvalid(fmt.Sprintf("票种名称“%s”不唯一，请先使用查询工具确认准确名称", resolved.ProductName))
	}
	product := owned[0]
	if product.Status != "offline" || product.IsDistributable {
		return nil, nil, agentConflict("只有未上架且未分销的票种可以通过 AI 修改；请在管理端确认当前状态")
	}
	if product.CurrentRevisionID == 0 {
		var revision model.ProductRevision
		if err := db.Where("product_id = ? AND tenant_id = ?", product.ID, tenantID).Order("version DESC").First(&revision).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, agentConflict("票种缺少当前版本，无法安全生成修改预览")
			}
			return nil, nil, err
		}
		product.CurrentRevisionID = revision.ID
	}
	resolved.ProductID = product.ID
	resolved.CurrentRevisionID = product.CurrentRevisionID
	if !hasProductUpdateChanges(resolved.Changes) {
		missing = append(missing, AgentMissingField{Field: "changes", Label: "修改内容", Question: "请明确要修改哪些基础字段，例如售价、有效期、标签或限购规则。检票点和核销规则请使用票规调整操作。"})
		return &resolved, missing, nil
	}
	if err := validateProductUpdateChanges(resolved.Changes); err != nil {
		return nil, nil, err
	}
	if resolved.Changes.Name != nil {
		if err := ensureProductUpdateNameAvailable(db, tenantID, product.ID, strings.TrimSpace(*resolved.Changes.Name)); err != nil {
			return nil, nil, err
		}
	}
	return &resolved, nil, nil
}

func ensureProductUpdateNameAvailable(db *gorm.DB, tenantID, productID uint, name string) error {
	if strings.TrimSpace(name) == "" {
		return agentInvalid("修改后的票种名称不能为空")
	}
	var count int64
	if err := db.Model(&model.Product{}).Where("tenant_id = ? AND id <> ? AND name = ? AND deleted_at IS NULL", tenantID, productID, name).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return agentInvalid(fmt.Sprintf("票种名称“%s”已存在，请使用其他名称", name))
	}
	return nil
}

func hasProductUpdateChanges(changes agentProductUpdateChanges) bool {
	return changes.Name != nil || changes.Price != nil || changes.SettlementPrice != nil || changes.ValidityType != nil || changes.ValidityDays != nil || changes.ValidityStart != nil || changes.ValidityEnd != nil || changes.CodeMode != nil || changes.StockType != nil || changes.DailyStock != nil || changes.RealNameRequired != nil || changes.RefundType != nil || changes.RefundRule != nil || changes.Tags != nil || changes.GateVoiceCode != nil || changes.LimitPerPhone != nil || changes.LimitPerID != nil
}

func validateProductUpdateChanges(changes agentProductUpdateChanges) error {
	if changes.Name != nil {
		if strings.TrimSpace(*changes.Name) == "" || len([]rune(strings.TrimSpace(*changes.Name))) > 100 {
			return agentInvalid("修改后的票种名称不能为空且不能超过 100 个字符")
		}
	}
	if changes.Price != nil && *changes.Price < 0 {
		return agentInvalid("售价不能为负数")
	}
	if changes.SettlementPrice != nil && *changes.SettlementPrice < 0 {
		return agentInvalid("结算价不能为负数")
	}
	if changes.ValidityType != nil && *changes.ValidityType != "date" && *changes.ValidityType != "days" {
		return agentInvalid("有效期类型只能是 date 或 days")
	}
	if changes.CodeMode != nil && *changes.CodeMode != "order" && *changes.CodeMode != "ticket" {
		return agentInvalid("出票方式只能是 order 或 ticket")
	}
	if changes.StockType != nil && *changes.StockType != "unlimited" && *changes.StockType != "daily" && *changes.StockType != "total" {
		return agentInvalid("库存类型只能是 unlimited、daily 或 total")
	}
	if changes.RefundType != nil && *changes.RefundType != "no_refund" && *changes.RefundType != "free" && *changes.RefundType != "ladder" {
		return agentInvalid("退款类型只能是 no_refund、free 或 ladder")
	}
	for label, value := range map[string]*int{"validity_days": changes.ValidityDays, "daily_stock": changes.DailyStock, "limit_per_phone": changes.LimitPerPhone, "limit_per_id": changes.LimitPerID} {
		if value != nil && *value < 0 {
			return agentInvalid(fmt.Sprintf("%s 不能为负数", label))
		}
	}
	if changes.Tags != nil && len([]rune(*changes.Tags)) > 255 {
		return agentInvalid("标签内容不能超过 255 个字符")
	}
	if changes.GateVoiceCode != nil {
		value := strings.TrimSpace(*changes.GateVoiceCode)
		if len([]rune(value)) > 100 || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "..") {
			return agentInvalid("闸机语音编号不是受支持的本地资源标识")
		}
	}
	return nil
}

func loadAgentUpdateProduct(tx *gorm.DB, tenantID uint, draft *agentProductUpdateDraft) (*model.Product, error) {
	if draft == nil || draft.ProductID == 0 || draft.CurrentRevisionID == 0 {
		return nil, agentConflict("票种修改预览缺少当前版本事实，请重新生成任务")
	}
	var product model.Product
	if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ? AND deleted_at IS NULL", draft.ProductID, tenantID).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, agentConflict("票种已不存在，请重新生成任务")
		}
		return nil, err
	}
	if isDistributedListing(&product) || product.IsDistributable || product.Status != "offline" {
		return nil, agentConflict("票种状态已变化；只有未上架且未分销的票种可以通过 AI 修改")
	}
	if product.CurrentRevisionID != draft.CurrentRevisionID {
		return nil, agentConflict("票种版本已变化，请重新生成预览")
	}
	return &product, nil
}

func applyProductUpdateChanges(product *model.Product, changes agentProductUpdateChanges) error {
	if product == nil {
		return agentInvalid("票种事实为空")
	}
	if err := validateProductUpdateChanges(changes); err != nil {
		return err
	}
	if changes.Name != nil {
		product.Name = strings.TrimSpace(*changes.Name)
	}
	if changes.Price != nil {
		product.Price = *changes.Price
	}
	if changes.SettlementPrice != nil {
		product.SettlementPrice = *changes.SettlementPrice
	}
	if changes.ValidityType != nil {
		product.ValidityType = *changes.ValidityType
	}
	if changes.ValidityDays != nil {
		product.ValidityDays = *changes.ValidityDays
	}
	if changes.ValidityStart != nil {
		value, err := parseAgentUpdateDate(*changes.ValidityStart)
		if err != nil {
			return err
		}
		product.ValidityStartDate = value
	}
	if changes.ValidityEnd != nil {
		value, err := parseAgentUpdateDate(*changes.ValidityEnd)
		if err != nil {
			return err
		}
		product.ValidityEndDate = value
	}
	if product.ValidityType == "days" && product.ValidityDays <= 0 {
		return agentInvalid("购买后有效天数必须大于 0")
	}
	if product.ValidityStartDate != nil && product.ValidityEndDate != nil && product.ValidityEndDate.Before(*product.ValidityStartDate) {
		return agentInvalid("有效期结束日期不能早于开始日期")
	}
	if changes.CodeMode != nil {
		product.CodeMode = *changes.CodeMode
	}
	if changes.StockType != nil {
		product.StockType = *changes.StockType
	}
	if changes.DailyStock != nil {
		product.DailyStock = *changes.DailyStock
	}
	if changes.RealNameRequired != nil {
		product.RealNameRequired = *changes.RealNameRequired
	}
	if changes.RefundType != nil {
		product.RefundType = *changes.RefundType
	}
	if changes.RefundRule != nil {
		product.RefundRule = *changes.RefundRule
	}
	if changes.Tags != nil {
		product.Tags = *changes.Tags
	}
	if changes.GateVoiceCode != nil {
		product.GateVoiceCode = strings.TrimSpace(*changes.GateVoiceCode)
	}
	if changes.LimitPerPhone != nil {
		product.LimitPerPhone = *changes.LimitPerPhone
	}
	if changes.LimitPerID != nil {
		product.LimitPerID = *changes.LimitPerID
	}
	product.Rule.ValidityType = product.ValidityType
	return nil
}

func parseAgentUpdateDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return nil, agentInvalid("有效期日期必须使用 YYYY-MM-DD，留空可清除日期")
	}
	return &parsed, nil
}

type agentProductUpdatePreview struct {
	OperationType  string                     `json:"operation_type"`
	ProductName    string                     `json:"product_name"`
	ScenicAreaName string                     `json:"scenic_area_name"`
	Before         agentProductPreviewProduct `json:"before"`
	After          agentProductPreviewProduct `json:"after"`
	Changes        []string                   `json:"changes"`
	Safety         []string                   `json:"safety"`
}

func productUpdatePreviewJSON(db *gorm.DB, tenantID uint, draft *agentProductUpdateDraft) (string, error) {
	product, err := loadAgentUpdateProduct(db, tenantID, draft)
	if err != nil {
		return "", err
	}
	before := *product
	after := *product
	if err := applyProductUpdateChanges(&after, draft.Changes); err != nil {
		return "", err
	}
	var area model.ScenicArea
	if err := db.Where("id = ? AND tenant_id = ?", product.ScenicAreaID, tenantID).First(&area).Error; err != nil {
		return "", err
	}
	preview := agentProductUpdatePreview{
		OperationType: AgentOperationTicketProductUpdate,
		ProductName:   product.Name, ScenicAreaName: area.Name,
		Before: productPreviewProductFromModel(before), After: productPreviewProductFromModel(after),
		Changes: productUpdateChangeLabels(draft.Changes),
		Safety:  []string{"确认前不会写入产品或产品版本。", "仅修改仍未上架且未分销的票种；票种类型、所属景区、分销和渠道事实不会改变。", "确认后沿用现有产品事务并生成新 ProductRevision，已售票据的历史快照不会被改写。"},
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func productPreviewProductFromModel(product model.Product) agentProductPreviewProduct {
	validityStart := ""
	if product.ValidityStartDate != nil {
		validityStart = product.ValidityStartDate.Format("2006-01-02")
	}
	validityEnd := ""
	if product.ValidityEndDate != nil {
		validityEnd = product.ValidityEndDate.Format("2006-01-02")
	}
	return agentProductPreviewProduct{
		Name: product.Name, Type: product.Type, TypeLabel: productTypeLabel(product.Type), Price: product.Price, SettlementPrice: product.SettlementPrice,
		Status: product.Status, StatusLabel: productStatusLabel(product.Status), IsDistributable: product.IsDistributable,
		ValidityType: product.ValidityType, ValidityDays: product.ValidityDays, ValidityStart: validityStart, ValidityEnd: validityEnd,
		CodeMode: product.CodeMode, StockType: product.StockType, DailyStock: product.DailyStock, RealNameRequired: product.RealNameRequired,
		LimitPerPhone: product.LimitPerPhone, LimitPerID: product.LimitPerID, RefundType: product.RefundType, RefundRule: product.RefundRule,
		Tags: product.Tags, GateVoiceCode: product.GateVoiceCode,
	}
}

func productStatusLabel(status string) string {
	if status == "offline" {
		return "未上架"
	}
	if status == "online" {
		return "已上架"
	}
	return status
}

func productUpdateChangeLabels(changes agentProductUpdateChanges) []string {
	labels := make([]string, 0, 17)
	if changes.Name != nil {
		labels = append(labels, "票种名称")
	}
	if changes.Price != nil {
		labels = append(labels, "售价")
	}
	if changes.SettlementPrice != nil {
		labels = append(labels, "结算价")
	}
	if changes.ValidityType != nil {
		labels = append(labels, "有效期类型")
	}
	if changes.ValidityDays != nil {
		labels = append(labels, "有效天数")
	}
	if changes.ValidityStart != nil {
		labels = append(labels, "有效期开始")
	}
	if changes.ValidityEnd != nil {
		labels = append(labels, "有效期结束")
	}
	if changes.CodeMode != nil {
		labels = append(labels, "出票方式")
	}
	if changes.StockType != nil {
		labels = append(labels, "库存类型")
	}
	if changes.DailyStock != nil {
		labels = append(labels, "每日库存")
	}
	if changes.RealNameRequired != nil {
		labels = append(labels, "实名要求")
	}
	if changes.RefundType != nil {
		labels = append(labels, "退款类型")
	}
	if changes.RefundRule != nil {
		labels = append(labels, "退款说明")
	}
	if changes.Tags != nil {
		labels = append(labels, "标签")
	}
	if changes.GateVoiceCode != nil {
		labels = append(labels, "闸机语音")
	}
	if changes.LimitPerPhone != nil {
		labels = append(labels, "手机号限购")
	}
	if changes.LimitPerID != nil {
		labels = append(labels, "证件限购")
	}
	return labels
}

func (s *AgentTaskService) confirmProductUpdateTask(tenantID, actorUserID uint, actorRole string, task model.AgentTask) (*AgentTaskView, error) {
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
		if locked.State != AgentTaskAwaitingConfirmation || locked.OperationType != AgentOperationTicketProductUpdate {
			return agentConflict("agent task is no longer executable")
		}
		var context agentTaskContext
		if err := json.Unmarshal([]byte(locked.ContextJSON), &context); err != nil || context.ProductUpdate == nil {
			return agentConflict("agent task product update preview is invalid; create a new task")
		}
		product, err := loadAgentUpdateProduct(tx, tenantID, context.ProductUpdate)
		if err != nil {
			return err
		}
		before := productPreviewProductFromModel(*product)
		if err := applyProductUpdateChanges(product, context.ProductUpdate.Changes); err != nil {
			return err
		}
		if context.ProductUpdate.Changes.Name != nil {
			if err := ensureProductUpdateNameAvailable(tx, tenantID, product.ID, strings.TrimSpace(*context.ProductUpdate.Changes.Name)); err != nil {
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
		if err := (&ProductService{}).updateTx(tx, product.ID, tenantID, product, &product.Rule); err != nil {
			return err
		}
		var updated model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", product.ID, tenantID).First(&updated).Error; err != nil {
			return err
		}
		resultJSON, err := json.Marshal(map[string]interface{}{"product_id": updated.ID, "name": updated.Name, "before": before, "after": productPreviewProductFromModel(updated)})
		if err != nil {
			return err
		}
		if err := recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "agent.task.confirm", "agent_task", locked.ID,
			"confirm AI planned unpublished ticket product update", locked.PreviewJSON, string(resultJSON)); err != nil {
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
