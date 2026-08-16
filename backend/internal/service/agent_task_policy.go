package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"ticket-backend/internal/model"
)

var agentScopedProductPattern = regexp.MustCompile(`(?:所有|全部)([^,，。；;\n]+?)(?:增加|添加|新增|加上|加入|删除|移除|去掉|去除|取消|设置|设为|调整|修改|开放|支持)`)
var agentNewRuleGroupPattern = regexp.MustCompile(`(?:新增|添加|新建|创建|增加)[^,，。；;\n]{0,24}规则组`)

// validateAgentPlannerEnvelope treats the model response as an untrusted
// proposal. The domain services remain authoritative, but this extra policy
// layer prevents an ambiguous envelope or a model-invented broad scope from
// reaching the preview path.
func validateAgentPlannerEnvelope(input string, envelope *agentAIEnvelope) error {
	if envelope == nil {
		return agentInvalid("AI 未返回任务计划")
	}
	if envelope.Product != nil && (envelope.ProductUpdate != nil || envelope.ProductBatchUpdate != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 计划同时包含票种目录操作，请一次只描述一种操作")
	}
	if envelope.ProductUpdate != nil && (envelope.ProductBatchUpdate != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 计划同时包含票种修改和票规调整，请一次只描述一种操作")
	}
	if envelope.ProductBatchUpdate != nil && len(envelope.Operations) > 0 {
		return agentInvalid("AI 计划同时包含批量票种修改和票规调整，请一次只描述一种操作")
	}
	operationType := strings.TrimSpace(envelope.OperationType)
	if operationType == "" {
		switch {
		case envelope.Product != nil:
			operationType = AgentOperationTicketProductCreate
		case envelope.ProductUpdate != nil:
			operationType = AgentOperationTicketProductUpdate
		case envelope.ProductBatchUpdate != nil:
			operationType = AgentOperationTicketProductBatchUpdate
		case len(envelope.Operations) > 0:
			operationType = AgentOperationCatalogBatchChange
		}
	}
	normalizedInput := strings.ToLower(strings.TrimSpace(input))
	hasCatalogIntent := agentHasAny(normalizedInput, agentCatalogIntentWords)
	hasProductIntent := agentHasAny(normalizedInput, agentProductCreateIntentWords)
	hasProductUpdateIntent := agentHasAny(normalizedInput, agentProductUpdateIntentWords)
	if operationType == AgentOperationCatalogBatchChange && hasProductIntent && !hasCatalogIntent {
		return agentInvalid("模型把新票种创建请求解释成了票规调整，请重新生成计划")
	}
	if operationType == AgentOperationTicketProductCreate && hasCatalogIntent && !hasProductIntent {
		return agentInvalid("模型把票规调整请求解释成了新票种创建，请重新生成计划")
	}
	if operationType == AgentOperationTicketProductUpdate && hasProductIntent && !hasProductUpdateIntent {
		return agentInvalid("模型把新票种创建请求解释成了票种修改，请重新生成计划")
	}
	if operationType == AgentOperationTicketProductUpdate && hasCatalogIntent && !hasProductUpdateIntent && agentHasAny(normalizedInput, []string{"检票点", "核销", "规则组", "分组", "票规"}) {
		return agentInvalid("模型把票规调整请求解释成了票种基础信息修改，请重新生成计划")
	}
	switch operationType {
	case AgentOperationCatalogBatchChange:
		if err := validateAgentCatalogOperations(input, envelope.Operations); err != nil {
			return err
		}
		return validateAgentCatalogTargets(input, envelope.Operations)
	case AgentOperationTicketProductCreate:
		if envelope.Product == nil {
			return agentInvalid("AI 未返回票种创建内容")
		}
		return validateAgentProductCandidate(input, envelope.Product)
	case AgentOperationTicketProductUpdate:
		if envelope.ProductUpdate == nil {
			return agentInvalid("AI 未返回票种修改内容")
		}
		return validateAgentProductUpdateCandidate(input, envelope.ProductUpdate)
	case AgentOperationTicketProductBatchUpdate:
		if envelope.ProductBatchUpdate == nil {
			return agentInvalid("AI 未返回批量票种修改内容")
		}
		return validateAgentProductBatchUpdateCandidate(input, envelope.ProductBatchUpdate)
	case AgentOperationPending, "":
		if envelope.Product != nil {
			return validateAgentProductCandidate(input, envelope.Product)
		}
		if envelope.ProductUpdate != nil {
			return validateAgentProductUpdateCandidate(input, envelope.ProductUpdate)
		}
		if envelope.ProductBatchUpdate != nil {
			return validateAgentProductBatchUpdateCandidate(input, envelope.ProductBatchUpdate)
		}
		if len(envelope.Operations) > 0 {
			if err := validateAgentCatalogOperations(input, envelope.Operations); err != nil {
				return err
			}
			return validateAgentCatalogTargets(input, envelope.Operations)
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

func validateAgentProductUpdateCandidate(input string, candidate *agentProductUpdateCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回票种修改内容")
	}
	if len([]rune(strings.TrimSpace(candidate.ProductName))) > 100 {
		return agentInvalid("待修改票种名称不能超过 100 个字符")
	}
	if candidate.Changes.Name != nil && strings.TrimSpace(*candidate.Changes.Name) == "" {
		return agentInvalid("修改后的票种名称不能为空")
	}
	if candidate.Changes.Price != nil && *candidate.Changes.Price < 0 {
		return agentInvalid("售价不能为负数")
	}
	if candidate.Changes.SettlementPrice != nil && *candidate.Changes.SettlementPrice < 0 {
		return agentInvalid("结算价不能为负数")
	}
	if candidate.Changes.ValidityType != nil && *candidate.Changes.ValidityType != "date" && *candidate.Changes.ValidityType != "days" {
		return agentInvalid("有效期类型只能是 date 或 days")
	}
	if candidate.Changes.CodeMode != nil && *candidate.Changes.CodeMode != "order" && *candidate.Changes.CodeMode != "ticket" {
		return agentInvalid("出票方式只能是 order 或 ticket")
	}
	if candidate.Changes.StockType != nil && *candidate.Changes.StockType != "unlimited" && *candidate.Changes.StockType != "daily" && *candidate.Changes.StockType != "total" {
		return agentInvalid("库存类型只能是 unlimited、daily 或 total")
	}
	if candidate.Changes.RefundType != nil && *candidate.Changes.RefundType != "no_refund" && *candidate.Changes.RefundType != "free" && *candidate.Changes.RefundType != "ladder" {
		return agentInvalid("退款类型不是受支持的值")
	}
	for field, value := range map[string]*int{"validity_days": candidate.Changes.ValidityDays, "daily_stock": candidate.Changes.DailyStock, "limit_per_phone": candidate.Changes.LimitPerPhone, "limit_per_id": candidate.Changes.LimitPerID} {
		if value != nil && *value < 0 {
			return agentInvalid(fmt.Sprintf("%s 不能为负数", field))
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(input))
	if agentHasAny(normalized, agentProductCreateIntentWords) && !agentHasAny(normalized, agentProductUpdateIntentWords) {
		return agentInvalid("票种修改请求不能创建新票种，请明确使用修改或更新")
	}
	if agentHasAny(normalized, []string{"检票点", "核销规则", "规则组", "票规"}) {
		return agentInvalid("检票点和核销规则请使用批量票规操作")
	}
	return nil
}

func validateAgentProductBatchUpdateCandidate(input string, candidate *agentProductBatchUpdateCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回批量票种修改内容")
	}
	if len(candidate.ProductNames) < 2 {
		return agentInvalid("批量修改至少需要两个准确的票种名称")
	}
	if len(candidate.ProductNames) > 50 {
		return agentInvalid("一次最多批量修改 50 个票种")
	}
	seen := make(map[string]struct{}, len(candidate.ProductNames))
	for _, rawName := range candidate.ProductNames {
		name := strings.TrimSpace(rawName)
		if name == "" || len([]rune(name)) > 100 {
			return agentInvalid("批量修改中的票种名称不能为空且不能超过 100 个字符")
		}
		if _, exists := seen[name]; exists {
			return agentInvalid(fmt.Sprintf("批量修改中的票种名称重复：%s", name))
		}
		seen[name] = struct{}{}
	}
	if candidate.Changes.Name != nil {
		return agentInvalid("批量修改不支持统一改名，请对单个票种单独生成预览")
	}
	return validateAgentProductUpdateCandidate(input, &agentProductUpdateCandidate{ProductName: candidate.ProductNames[0], Changes: candidate.Changes})
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
		if agentHasAny(normalized, agentCatalogIntentWords) || agentHasAny(normalized, []string{"规则组", "分组"}) {
			return nil
		}
		return agentInvalid("请明确要增加、移除或设置哪些检票规则")
	}
	if existingOperationType == AgentOperationTicketProductUpdate {
		return nil
	}
	if existingOperationType == AgentOperationTicketProductBatchUpdate {
		return nil
	}
	if agentHasAny(normalized, agentCatalogIntentWords) || agentHasAny(normalized, agentProductCreateIntentWords) || agentHasAny(normalized, agentProductUpdateIntentWords) || agentHasAny(normalized, agentProductBatchUpdateIntentWords) {
		return nil
	}
	return agentInvalid("AI 助手只处理票规调整或新票种创建，请先描述明确的业务操作")
}

func validateAgentTaskInputIntent(input string, task model.AgentTask) error {
	if err := validateAgentInputIntent(input, task.OperationType); err == nil {
		return nil
	} else if task.OperationType != AgentOperationCatalogBatchChange || !agentTaskMissingField(task, "group_name") {
		return err
	}
	return nil
}

func validateAgentPlannerEnvelopeForTask(input string, task model.AgentTask, envelope *agentAIEnvelope) error {
	envelopeOperationType := ""
	if envelope != nil {
		envelopeOperationType = strings.TrimSpace(envelope.OperationType)
	}
	if task.OperationType == AgentOperationCatalogBatchChange && agentTaskMissingField(task, "group_name") && envelope != nil && envelope.Product == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationCatalogBatchChange) {
		// A provider may return no structured operation for a one-token answer
		// such as "水上乐园". The durable previous operation is authoritative;
		// the planner will reconstruct it below instead of returning a 500 or
		// discarding the user's answer.
		return nil
	}
	if task.OperationType == AgentOperationTicketProductUpdate && envelope != nil && envelope.Product == nil && envelope.ProductUpdate == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationTicketProductUpdate) {
		return nil
	}
	if task.OperationType == AgentOperationTicketProductBatchUpdate && envelope != nil && envelope.Product == nil && envelope.ProductUpdate == nil && envelope.ProductBatchUpdate == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationTicketProductBatchUpdate) {
		return nil
	}
	if err := validateAgentPlannerEnvelope(input, envelope); err == nil {
		return nil
	} else if task.OperationType != AgentOperationCatalogBatchChange || !agentTaskMissingField(task, "group_name") {
		return err
	}
	var previous agentTaskContext
	if err := json.Unmarshal([]byte(task.ContextJSON), &previous); err != nil {
		return err
	}
	augmented := strings.TrimSpace(input) + " 增加检票点"
	for _, operation := range previous.Operations {
		for _, name := range operation.ProductNames {
			augmented += " " + name
		}
		for _, name := range operation.CheckpointNames {
			augmented += " " + name
		}
	}
	return validateAgentPlannerEnvelope(augmented, envelope)
}

func agentTaskMissingField(task model.AgentTask, suffix string) bool {
	var fields []AgentMissingField
	if err := json.Unmarshal([]byte(task.MissingJSON), &fields); err != nil {
		return false
	}
	for _, field := range fields {
		if strings.HasSuffix(strings.TrimSpace(field.Field), suffix) {
			return true
		}
	}
	return false
}

var agentCatalogIntentWords = []string{
	"增加", "添加", "新增", "加上", "加入", "补充", "开放", "支持", "删除", "移除", "去掉", "去除", "取消", "不要", "不允许", "不再", "剔除", "移出", "撤掉", "设置", "设为", "改为", "调整", "修改", "上限", "限额", "限次", "最多",
}

var agentProductCreateIntentWords = []string{
	"创建", "新建", "生成票种", "创建票种", "新票种", "建一个票", "做一个票", "需要一个票", "要一个票", "开一个票种",
}

var agentProductUpdateIntentWords = []string{
	"修改票种", "更新票种", "调整票种", "编辑票种", "改票种", "修改售价", "调整售价", "修改价格", "调整价格", "修改结算价", "调整结算价", "修改有效期", "调整有效期", "修改标签", "调整标签", "改名",
}

var agentProductBatchUpdateIntentWords = []string{
	"批量修改", "批量更新", "批量调整", "多个票种", "这些票种", "这几个票种", "一批票种",
}

var agentReadIntentWords = []string{
	"查询", "查看", "列出", "有哪些", "统计", "报表", "库存", "订单", "票种", "检票点", "景区", "规则",
}

func validateAgentCatalogOperations(input string, operations []CatalogRuleOperation) error {
	if len(operations) == 0 {
		return agentInvalid("AI 未返回票规操作")
	}
	normalized := strings.ToLower(strings.TrimSpace(input))
	for _, operation := range operations {
		kind := strings.TrimSpace(operation.Kind)
		if operation.CreateGroup && kind != CatalogBatchOpAddCheckpoint {
			return agentInvalid("新增规则组只能和增加检票点一起使用")
		}
		if !operation.CreateGroup && operation.GroupMaxTotal != nil {
			return agentInvalid("新规则组通行数量只能用于新增规则组")
		}
		if operation.GroupMaxTotal != nil && (*operation.GroupMaxTotal < 0 || *operation.GroupMaxTotal > 1000) {
			return agentInvalid("新规则组通行数量必须在 0 到 1000 之间")
		}
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

func normalizeAgentCatalogGroupIntent(input string, operations []CatalogRuleOperation) []CatalogRuleOperation {
	if !agentRequestsNewRuleGroup(input) {
		return operations
	}
	result := make([]CatalogRuleOperation, len(operations))
	copy(result, operations)
	for index, operation := range result {
		if operation.Kind != CatalogBatchOpAddCheckpoint {
			continue
		}
		operation.CreateGroup = true
		if !agentExplicitNewRuleGroupName(input, operation.GroupName) {
			operation.GroupName = ""
		}
		result[index] = operation
	}
	return result
}

func agentRequestsNewRuleGroup(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if agentNewRuleGroupPattern.MatchString(normalized) {
		return true
	}
	for _, phrase := range []string{"新增一个规则组", "新增规则组", "添加一个规则组", "添加规则组", "新建一个规则组", "新建规则组", "创建一个规则组", "创建规则组", "增加一个规则组", "增加规则组", "新规则组"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func agentExplicitNewRuleGroupName(input, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || agentGenericNewRuleGroupName(candidate) || !agentTextContains(input, candidate) {
		return false
	}
	normalized := strings.ToLower(input)
	for _, marker := range []string{"规则组名称", "规则组名", "规则组叫", "规则组为", "规则组命名", "命名为", "名称为", "叫做"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return strings.Contains(input, candidate+"规则组") || strings.Contains(input, "规则组"+candidate)
}

func agentGenericNewRuleGroupName(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "新规则组", "新增规则组", "添加规则组", "默认分组", "new group", "new rule group":
		return true
	default:
		return false
	}
}

func agentNewRuleGroupNameAnswer(input string) string {
	value := strings.Trim(strings.TrimSpace(input), " \t\r\n，。；;：:、")
	if value == "" {
		return ""
	}
	normalized := strings.ToLower(value)
	for _, marker := range []string{"规则组名称", "规则组名", "规则组叫", "规则组为", "规则组命名", "命名为", "名称为", "叫做"} {
		if index := strings.Index(normalized, marker); index >= 0 {
			value = strings.Trim(strings.TrimSpace(value[index+len(marker):]), " \t\r\n，。；;：:、")
			break
		}
	}
	value = strings.TrimSpace(strings.TrimSuffix(value, "规则组"))
	if value == "" || agentGenericNewRuleGroupName(value) || len([]rune(value)) > 100 || agentHasAny(strings.ToLower(value), []string{"增加", "添加", "新增", "新建", "创建", "删除", "移除", "设置"}) {
		return ""
	}
	return value
}

func validateAgentCatalogTargets(input string, operations []CatalogRuleOperation) error {
	for _, operation := range operations {
		if !operation.AllProducts {
			for _, productName := range operation.ProductNames {
				if !agentTextContains(input, productName) && !agentProductNameCoveredByExplicitScope(input, productName) &&
					!(agentHasBoundedProductScope(input) && agentCanBeBoundedProductFragment(productName)) {
					return agentInvalid(fmt.Sprintf("票种 %q 未在当前请求中明确指定，请使用当前租户的准确名称", productName))
				}
			}
		}
		for _, checkpointName := range operation.CheckpointNames {
			if !agentTextContains(input, checkpointName) {
				return agentInvalid(fmt.Sprintf("检票点 %q 未在当前请求中明确指定，请使用当前租户的准确名称", checkpointName))
			}
		}
	}
	return nil
}

// canonicalizeAgentCatalogProductNames keeps natural-language planning
// flexible without making the provider authoritative for tenant-owned
// references. A user may write a full catalog name while a provider returns
// a shortened fragment; when that fragment maps to one explicit catalog
// candidate, use the server-owned canonical name. Explicit bounded scopes
// such as "所有飞车套票" are expanded only to matching current-tenant
// products. Ambiguous fragments are left untouched so the normal resolver
// returns a clarification/error instead of guessing.
func canonicalizeAgentCatalogProductNames(input string, operations []CatalogRuleOperation, products []model.Product) []CatalogRuleOperation {
	byLowerName := make(map[string][]string, len(products))
	scopedNames := make([]string, 0)
	for _, product := range products {
		if isDistributedListing(&product) {
			continue
		}
		name := strings.TrimSpace(product.Name)
		if name == "" {
			continue
		}
		byLowerName[strings.ToLower(name)] = append(byLowerName[strings.ToLower(name)], name)
		if agentProductNameCoveredByExplicitScope(input, name) {
			scopedNames = append(scopedNames, name)
		}
	}
	explicitCatalogNames := make(map[string]string, len(byLowerName))
	for key, names := range byLowerName {
		if len(names) == 1 {
			explicitCatalogNames[key] = names[0]
		}
	}
	explicitNames := explicitAgentCatalogNamesInText(input, explicitCatalogNames)
	hasBoundedScope := agentHasBoundedProductScope(input)
	result := make([]CatalogRuleOperation, len(operations))
	copy(result, operations)
	for index, operation := range result {
		if operation.AllProducts || len(operation.ProductNames) == 0 {
			if !operation.AllProducts && len(operation.ProductNames) == 0 {
				switch {
				case len(explicitNames) == 1:
					operation.ProductNames = append([]string(nil), explicitNames...)
				case len(scopedNames) > 0:
					operation.ProductNames = append([]string(nil), scopedNames...)
				}
				result[index] = operation
			}
			continue
		}

		canonicalNames := make([]string, 0, len(operation.ProductNames))
		for _, requested := range operation.ProductNames {
			requested = strings.TrimSpace(requested)
			if requested == "" {
				continue
			}
			matches := make([]string, 0, 1)
			if len(scopedNames) > 0 {
				for _, scopedName := range scopedNames {
					if agentCatalogNameCompatible(requested, scopedName) {
						matches = append(matches, scopedNames...)
						break
					}
				}
				if len(matches) == 0 && agentProductNameCoveredByExplicitScope(input, requested) {
					matches = append(matches, scopedNames...)
				}
			}
			if len(matches) == 0 {
				for _, explicitName := range explicitNames {
					if agentCatalogNameCompatible(requested, explicitName) {
						matches = append(matches, explicitName)
					}
				}
				if len(matches) == 0 && !hasBoundedScope {
					if exact, ok := byLowerName[strings.ToLower(requested)]; ok && len(exact) == 1 {
						matches = append(matches, exact[0])
					} else if !ok {
						for _, product := range products {
							if isDistributedListing(&product) {
								continue
							}
							name := strings.TrimSpace(product.Name)
							if name != "" && agentCatalogNameCompatible(requested, name) {
								matches = append(matches, name)
							}
						}
					}
				}
			}
			if len(matches) == 1 {
				canonicalNames = appendUniqueAgentCatalogNames(canonicalNames, matches[0])
				continue
			}
			if len(matches) > 1 && len(scopedNames) > 0 {
				for _, match := range matches {
					canonicalNames = appendUniqueAgentCatalogNames(canonicalNames, match)
				}
				continue
			}
			// Preserve an ambiguous or unknown name. The existing resolver will
			// produce a tenant-safe error instead of silently selecting one.
			canonicalNames = appendUniqueAgentCatalogNames(canonicalNames, requested)
		}
		operation.ProductNames = canonicalNames
		result[index] = operation
	}
	return result
}

func agentCatalogNameCompatible(requested, candidate string) bool {
	requested = normalizeAgentCatalogName(requested)
	candidate = normalizeAgentCatalogName(candidate)
	return requested != "" && candidate != "" && (strings.Contains(candidate, requested) || strings.Contains(requested, candidate))
}

func normalizeAgentCatalogName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, separator := range []string{" ", "\t", "\r", "\n", "【", "】", "[", "]", "（", "）", "(", ")"} {
		value = strings.ReplaceAll(value, separator, "")
	}
	return value
}

func appendUniqueAgentCatalogNames(values []string, name string) []string {
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
			return values
		}
	}
	return append(values, name)
}

func agentHasBoundedProductScope(input string) bool {
	for _, match := range agentScopedProductPattern.FindAllStringSubmatch(input, -1) {
		if len(match) != 2 {
			continue
		}
		scope := strings.TrimSpace(match[1])
		if scope != "" && scope != "票种" && scope != "门票" && scope != "产品" && scope != "票" && scope != "all products" && scope != "all tickets" {
			return true
		}
	}
	return false
}

func agentCanBeBoundedProductFragment(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "票", "票种", "门票", "产品", "所有", "全部", "所有票种", "全部票种":
		return false
	default:
		return true
	}
}

func agentExplicitAllProducts(input string) bool {
	return agentHasAny(input, []string{"所有票种", "全部票种", "所有门票", "全部门票", "all products", "all tickets"})
}

// agentProductNameCoveredByExplicitScope accepts a bounded category such as
// "所有飞车套票" while keeping generic all-products language on the strict
// all_products path. The model still has to return an exact current-tenant
// product name, and the domain resolver remains authoritative afterwards.
func agentProductNameCoveredByExplicitScope(input, productName string) bool {
	productName = strings.TrimSpace(productName)
	if productName == "" {
		return false
	}
	for _, match := range agentScopedProductPattern.FindAllStringSubmatch(input, -1) {
		if len(match) != 2 {
			continue
		}
		scope := strings.TrimSpace(match[1])
		switch scope {
		case "", "票种", "门票", "产品", "票", "all products", "all tickets":
			continue
		}
		if strings.Contains(productName, scope) {
			return true
		}
	}
	return false
}

func agentHasAny(input string, values []string) bool {
	for _, value := range values {
		if strings.Contains(input, strings.ToLower(value)) {
			return true
		}
	}
	return false
}
