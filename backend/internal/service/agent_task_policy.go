package service

import (
	"fmt"
	"strings"
)

// validateAgentPlannerEnvelope treats the model response as an untrusted
// proposal. The domain services remain authoritative, but this extra policy
// layer prevents an ambiguous envelope or a model-invented broad scope from
// reaching the preview path.
func validateAgentPlannerEnvelope(input string, envelope *agentAIEnvelope) error {
	if envelope == nil {
		return agentInvalid("AI 未返回任务计划")
	}
	if envelope.Product != nil && len(envelope.Operations) > 0 {
		return agentInvalid("AI 计划同时包含票种创建和票规调整，请一次只描述一种操作")
	}
	operationType := strings.TrimSpace(envelope.OperationType)
	if operationType == "" {
		switch {
		case envelope.Product != nil:
			operationType = AgentOperationTicketProductCreate
		case len(envelope.Operations) > 0:
			operationType = AgentOperationCatalogBatchChange
		}
	}
	normalizedInput := strings.ToLower(strings.TrimSpace(input))
	hasCatalogIntent := agentHasAny(normalizedInput, agentCatalogIntentWords)
	hasProductIntent := agentHasAny(normalizedInput, agentProductCreateIntentWords)
	if operationType == AgentOperationCatalogBatchChange && hasProductIntent && !hasCatalogIntent {
		return agentInvalid("模型把新票种创建请求解释成了票规调整，请重新生成计划")
	}
	if operationType == AgentOperationTicketProductCreate && hasCatalogIntent && !hasProductIntent {
		return agentInvalid("模型把票规调整请求解释成了新票种创建，请重新生成计划")
	}
	switch operationType {
	case AgentOperationCatalogBatchChange:
		return validateAgentCatalogOperations(input, envelope.Operations)
	case AgentOperationTicketProductCreate:
		if envelope.Product == nil {
			return agentInvalid("AI 未返回票种创建内容")
		}
		return validateAgentProductCandidate(input, envelope.Product)
	case AgentOperationPending, "":
		if envelope.Product != nil {
			return validateAgentProductCandidate(input, envelope.Product)
		}
		if len(envelope.Operations) > 0 {
			return validateAgentCatalogOperations(input, envelope.Operations)
		}
		return agentInvalid("这段内容不是受支持的票务操作")
	default:
		return agentInvalid("AI 返回了不受支持的操作类型")
	}
}

func validateAgentProductCandidate(input string, product *agentProductCandidate) error {
	productType := strings.TrimSpace(product.ProductType)
	if productType != "" && productType != "online" && productType != "offline" {
		return agentInvalid("AI 返回了不受支持的票种类型")
	}
	hasWindow := agentHasAny(strings.ToLower(input), []string{"窗口", "线下", "pos", "现场票"})
	hasOnline := agentHasAny(strings.ToLower(input), []string{"线上", "在线", "网售", "小程序票"})
	if hasWindow && hasOnline {
		return agentInvalid("一次只能创建一种票种类型，请明确选择线上票或窗口/POS 票")
	}
	if hasWindow && productType == "online" {
		return agentInvalid("模型把窗口/POS 票解释成了线上票，请重新生成计划")
	}
	if hasOnline && productType == "offline" {
		return agentInvalid("模型把线上票解释成了窗口/POS 票，请重新生成计划")
	}
	return nil
}

// validateAgentInputIntent is deliberately conservative for a new task. A
// later product turn may be a short answer such as a checkpoint name, so the
// existing task type is the authority for continuation turns.
func validateAgentInputIntent(input, existingOperationType string) error {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return agentInvalid("请输入要执行的票务操作")
	}
	if existingOperationType == AgentOperationTicketProductCreate {
		return nil
	}
	if existingOperationType == AgentOperationCatalogBatchChange {
		if agentHasAny(normalized, agentCatalogIntentWords) {
			return nil
		}
		return agentInvalid("请明确要增加、移除或设置哪些检票规则")
	}
	if agentHasAny(normalized, agentCatalogIntentWords) || agentHasAny(normalized, agentProductCreateIntentWords) {
		return nil
	}
	return agentInvalid("AI 助手只处理票规调整或新票种创建，请先描述明确的业务操作")
}

var agentCatalogIntentWords = []string{
	"增加", "添加", "新增", "加上", "加入", "补充", "开放", "支持", "删除", "移除", "去掉", "去除", "取消", "不要", "不允许", "不再", "剔除", "移出", "撤掉", "设置", "设为", "改为", "调整", "修改", "上限", "限额", "限次", "最多",
}

var agentProductCreateIntentWords = []string{
	"创建", "新建", "生成票种", "创建票种", "新票种", "建一个票", "做一个票", "需要一个票", "要一个票", "开一个票种",
}

func validateAgentCatalogOperations(input string, operations []CatalogRuleOperation) error {
	if len(operations) == 0 {
		return agentInvalid("AI 未返回票规操作")
	}
	normalized := strings.ToLower(strings.TrimSpace(input))
	for _, operation := range operations {
		kind := strings.TrimSpace(operation.Kind)
		if operation.AllProducts && !agentExplicitAllProducts(normalized) {
			return agentInvalid("模型不能自行扩大操作范围；如需处理全部票种，请在请求中明确写出“全部票种”")
		}
		switch kind {
		case CatalogBatchOpAddCheckpoint:
			if agentHasAny(normalized, []string{"删除", "移除", "去掉", "去除", "取消检票点"}) &&
				!agentHasAny(normalized, []string{"增加", "添加", "新增", "加上", "加入", "增加检票点"}) {
				return agentInvalid("模型将移除请求解释为增加操作，请重新描述意图")
			}
		case CatalogBatchOpRemoveCheckpoint:
			if agentHasAny(normalized, []string{"增加", "添加", "新增", "加上", "加入", "增加检票点"}) &&
				!agentHasAny(normalized, []string{"删除", "移除", "去掉", "去除", "取消检票点"}) {
				return agentInvalid("模型将增加请求解释为移除操作，请重新描述意图")
			}
		case CatalogBatchOpSetLimit:
			if !batchLimitPattern.MatchString(normalized) && !agentHasAny(normalized, []string{"一次", "两次", "三次", "四次", "五次"}) {
				return agentInvalid("设置检票次数时必须由请求明确给出次数")
			}
		default:
			return agentInvalid(fmt.Sprintf("AI 返回了不受支持的票规操作 %q", kind))
		}
	}
	return nil
}

func agentExplicitAllProducts(input string) bool {
	return agentHasAny(input, []string{"所有票种", "全部票种", "所有门票", "全部门票", "all products", "all tickets"})
}

func agentHasAny(input string, values []string) bool {
	for _, value := range values {
		if strings.Contains(input, strings.ToLower(value)) {
			return true
		}
	}
	return false
}
