package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"ticket-backend/internal/model"
)

var agentScopedProductPattern = regexp.MustCompile(`(?:所有|全部)([^,，。；;\n]+?)(?:增加|添加|新增|加上|加入|删除|移除|去掉|去除|取消|设置|设为|调整|修改|开放|支持)`)
var agentNewRuleGroupPattern = regexp.MustCompile(`(?:新增|添加|新建|创建|增加)[^,，。；;\n]{0,24}规则组`)
var agentExplicitTenantPattern = regexp.MustCompile(`(?i)(?:供应商租户|租户编号|商户编号|企业码|系统编号|tenant[_ ]?id|tenant[_ ]?code|租户|商户)\s*(?:为|是)?\s*[:：=#]?\s*([a-z][a-z0-9_-]{2,63}|[0-9]{1,20})`)
var agentReverseTenantPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9_-]{2,63}|[0-9]{1,20})\s*(?:供应商租户|租户|商户)`)
var agentNumericTenantTokenPattern = regexp.MustCompile(`^[0-9]+$`)
var agentTicketProductCreatePattern = regexp.MustCompile(`(?:创建|新建|生成|建一个|做一个|需要一个|要一个)[^,，。；;\n]{0,40}(?:票种|门票|线上票|窗口票|现场票|网售|成人票|儿童票|pos)`)

// These operations deliberately have no Agent write tool. Keep the check
// lexical and before provider invocation so an unsupported request cannot be
// reinterpreted as a harmless query by a model. Product refund policy is a
// catalog field and remains allowed when the user explicitly says to change a
// ticket/product policy; an order/payment refund is a separate operation.
var agentUnsupportedCapabilityMarkers = []string{
	"支付", "退款", "退票", "资金退款", "设备控制", "下发设备", "开闸", "硬件", "凭据", "密钥", "api key", "appid", "secret",
	"分销授权", "渠道授权", "授权分销", "上架", "下架", "权限变更", "赋权", "充值", "付款", "结算确认", "确认结算", "入园",
	"创建预约", "预约创建", "取消预约", "预约取消", "改期预约", "预约改期", "修改预约", "预约入住",
	"创建酒店", "删除酒店", "修改酒店", "创建房型", "删除房型", "修改房型", "创建价格计划", "删除价格计划", "修改价格计划",
	"发布渠道", "渠道发布", "同步渠道", "pms", "webhook", "小红书", "携程", "ota",
}

var agentUnsafeInputMarkers = []string{
	"<script", "</script", "javascript:", "data:text/html", "```sql", "select * from", "drop table", "delete from", "insert into",
	"ignore previous", "ignore all rules", "忽略之前的规则", "忽略所有规则", "执行 sql", "执行shell", "powershell", "curl http", "wget http",
}

// validateAgentPlannerEnvelope treats the model response as an untrusted
// proposal. The domain services remain authoritative, but this extra policy
// layer prevents an ambiguous envelope or a model-invented broad scope from
// reaching the preview path.
func validateAgentPlannerEnvelope(input string, envelope *agentAIEnvelope) error {
	if envelope == nil {
		return agentInvalid("AI 未返回任务计划")
	}
	if envelope.Compound != nil && (envelope.Product != nil || envelope.ProductUpdate != nil || envelope.ProductBatchUpdate != nil || envelope.HotelInventory != nil || envelope.HotelRateCalendar != nil || envelope.HotelProductCalendar != nil || envelope.HotelReservationStatus != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 复合计划不能同时包含顶层票种或票规操作")
	}
	if envelope.Product != nil && (envelope.ProductUpdate != nil || envelope.ProductBatchUpdate != nil || envelope.HotelInventory != nil || envelope.HotelRateCalendar != nil || envelope.HotelProductCalendar != nil || envelope.HotelReservationStatus != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 计划同时包含票种目录操作，请一次只描述一种操作")
	}
	if envelope.ProductUpdate != nil && (envelope.ProductBatchUpdate != nil || envelope.HotelInventory != nil || envelope.HotelRateCalendar != nil || envelope.HotelProductCalendar != nil || envelope.HotelReservationStatus != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 计划同时包含票种修改和票规调整，请一次只描述一种操作")
	}
	if envelope.ProductBatchUpdate != nil && (envelope.HotelInventory != nil || envelope.HotelRateCalendar != nil || envelope.HotelProductCalendar != nil || envelope.HotelReservationStatus != nil || len(envelope.Operations) > 0) {
		return agentInvalid("AI 计划同时包含批量票种修改和票规调整，请一次只描述一种操作")
	}
	hotelFields := 0
	if envelope.HotelInventory != nil {
		hotelFields++
	}
	if envelope.HotelRateCalendar != nil {
		hotelFields++
	}
	if envelope.HotelProductCalendar != nil {
		hotelFields++
	}
	if envelope.HotelReservationStatus != nil {
		hotelFields++
	}
	if hotelFields > 1 {
		return agentInvalid("AI 计划同时包含多个酒店操作，请一次只描述一种操作")
	}
	if hotelFields > 0 && (len(envelope.Operations) > 0 || envelope.Product != nil || envelope.ProductUpdate != nil || envelope.ProductBatchUpdate != nil) {
		return agentInvalid("AI 计划不能混合酒店与票务写入操作")
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
		case envelope.HotelInventory != nil:
			operationType = AgentOperationHotelInventoryChange
		case envelope.HotelRateCalendar != nil:
			operationType = AgentOperationHotelRateCalendarChange
		case envelope.HotelProductCalendar != nil:
			operationType = AgentOperationHotelProductCalendarChange
		case envelope.HotelReservationStatus != nil:
			operationType = AgentOperationHotelReservationStatusChange
		case envelope.Compound != nil:
			operationType = AgentOperationCompound
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
	if operationType == AgentOperationTicketProductUpdate && hasCatalogIntent && !hasProductUpdateIntent && agentHasCatalogRuleMutationIntent(normalizedInput) {
		return agentInvalid("模型把票规调整请求解释成了票种基础信息修改，请重新生成计划")
	}
	switch operationType {
	case AgentOperationCompound:
		if envelope.Compound == nil {
			return agentInvalid("AI 未返回复合计划内容")
		}
		if len(envelope.Compound.Steps) < 2 || len(envelope.Compound.Steps) > 5 {
			return agentInvalid("复合计划必须包含 2 到 5 个低风险操作步骤")
		}
		for index, step := range envelope.Compound.Steps {
			stepOperation := strings.TrimSpace(step.OperationType)
			if stepOperation != AgentOperationCatalogBatchChange && stepOperation != AgentOperationTicketProductCreate && stepOperation != AgentOperationTicketProductUpdate && stepOperation != AgentOperationTicketProductBatchUpdate {
				return agentInvalid(fmt.Sprintf("复合计划第 %d 步包含不受支持的操作", index+1))
			}
			child := &agentAIEnvelope{OperationType: stepOperation, Operations: step.Operations, Product: step.Product, ProductUpdate: step.ProductUpdate, ProductBatchUpdate: step.ProductBatchUpdate}
			if err := validateAgentPlannerEnvelope(compoundStepValidationInput(input, stepOperation), child); err != nil {
				return agentInvalid(fmt.Sprintf("复合计划第 %d 步无效：%s", index+1, err.Error()))
			}
		}
		return nil
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
	case AgentOperationHotelInventoryChange:
		return validateAgentHotelInventoryCandidate(input, envelope.HotelInventory)
	case AgentOperationHotelRateCalendarChange:
		return validateAgentHotelRateCalendarCandidate(input, envelope.HotelRateCalendar)
	case AgentOperationHotelProductCalendarChange:
		return validateAgentHotelProductCalendarCandidate(input, envelope.HotelProductCalendar)
	case AgentOperationHotelReservationStatusChange:
		return validateAgentHotelReservationStatusCandidate(input, envelope.HotelReservationStatus)
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
		if envelope.HotelInventory != nil {
			return validateAgentHotelInventoryCandidate(input, envelope.HotelInventory)
		}
		if envelope.HotelRateCalendar != nil {
			return validateAgentHotelRateCalendarCandidate(input, envelope.HotelRateCalendar)
		}
		if envelope.HotelProductCalendar != nil {
			return validateAgentHotelProductCalendarCandidate(input, envelope.HotelProductCalendar)
		}
		if envelope.HotelReservationStatus != nil {
			return validateAgentHotelReservationStatusCandidate(input, envelope.HotelReservationStatus)
		}
		if envelope.Compound != nil {
			return validateAgentPlannerEnvelope(input, &agentAIEnvelope{OperationType: AgentOperationCompound, Compound: envelope.Compound})
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

func compoundStepValidationInput(input, operationType string) string {
	if operationType != AgentOperationTicketProductUpdate && operationType != AgentOperationTicketProductBatchUpdate {
		return input
	}
	cleaned := input
	for _, word := range []string{"检票点", "核销规则", "规则组", "分组", "票规"} {
		cleaned = strings.ReplaceAll(cleaned, word, "")
	}
	return strings.TrimSpace(cleaned + " 修改票种")
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
	if agentHasCatalogRuleMutationIntent(normalized) {
		return agentInvalid("检票点和核销规则请使用批量票规操作")
	}
	return nil
}

func validateAgentProductBatchUpdateCandidate(input string, candidate *agentProductBatchUpdateCandidate) error {
	if candidate == nil {
		return agentInvalid("AI 未返回批量票种修改内容")
	}
	if len(candidate.ProductNames) < 2 && candidate.TargetScope == nil {
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
	productName := ""
	if len(candidate.ProductNames) > 0 {
		productName = candidate.ProductNames[0]
	}
	return validateAgentProductUpdateCandidate(input, &agentProductUpdateCandidate{ProductName: productName, TargetScope: candidate.TargetScope, Changes: candidate.Changes})
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
	if existingOperationType == AgentOperationCompound {
		return nil
	}
	if existingOperationType == AgentOperationHotelInventoryChange || existingOperationType == AgentOperationHotelRateCalendarChange || existingOperationType == AgentOperationHotelProductCalendarChange || existingOperationType == AgentOperationHotelReservationStatusChange {
		return nil
	}
	if agentHasAny(normalized, agentHotelIntentWords) {
		return nil
	}
	if agentHasAny(normalized, agentCatalogIntentWords) || agentHasAny(normalized, agentProductCreateIntentWords) || agentHasAny(normalized, agentProductUpdateIntentWords) || agentHasAny(normalized, agentProductBatchUpdateIntentWords) {
		return nil
	}
	return agentInvalid("AI 助手只处理票规调整或新票种创建，请先描述明确的业务操作")
}

// rejectExplicitForeignTenantTarget is intentionally lexical and server-side.
// A bare number or an ordinary business name is not a tenant selector; only a
// clear tenant/company qualifier is rejected. Unknown qualified values use the
// same message as known foreign values so tenant existence cannot be probed.
func rejectExplicitForeignTenantTarget(tenantID uint, input string) error {
	input = strings.TrimSpace(input)
	if tenantID == 0 || input == "" {
		return nil
	}
	values := make([]string, 0, 2)
	for _, pattern := range []*regexp.Regexp{agentExplicitTenantPattern, agentReverseTenantPattern} {
		matches := pattern.FindAllStringSubmatch(input, -1)
		for _, match := range matches {
			if len(match) == 2 && strings.TrimSpace(match[1]) != "" {
				values = append(values, strings.TrimSpace(match[1]))
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	var tenant model.Tenant
	if err := model.DB.Select("id, system_code").First(&tenant, tenantID).Error; err != nil {
		return err
	}
	for _, value := range values {
		if strings.EqualFold(value, strings.TrimSpace(tenant.SystemCode)) {
			continue
		}
		if agentNumericTenantTokenPattern.MatchString(value) {
			return agentInvalid("普通租户助手不能指定租户编号；请在当前租户内操作")
		}
		return agentInvalid("当前租户助手不能操作指定的其他租户")
	}
	return nil
}

// agentExplicitTicketProductCreateIntent distinguishes a product request from
// phrases such as “创建规则组/检票点”. Product creation may still mention its
// checkpoint requirements; the ticket noun and create verb keep that request
// on the product preview tool.
func agentExplicitTicketProductCreateIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if !agentHasAny(normalized, agentProductCreateIntentWords) {
		return false
	}
	if agentTicketProductCreatePattern.MatchString(normalized) {
		return true
	}
	return !agentHasCatalogRuleMutationIntent(normalized)
}

func agentCatalogRefundPolicyIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if !agentHasAny(normalized, []string{"票种", "门票", "产品"}) {
		return false
	}
	if !agentHasAny(normalized, []string{"退款规则", "退款类型", "退改规则", "未核销随时退", "随时退", "免费退", "无理由退", "未核销可退"}) {
		return false
	}
	return agentHasAny(normalized, agentProductCreateIntentWords) || agentHasAny(normalized, agentProductUpdateIntentWords)
}

// rejectUnsupportedAgentCapability returns a stable, user-facing boundary
// before a task row or provider request is created. It is intentionally not a
// prompt instruction: the server, rather than the model, owns this decision.
func rejectUnsupportedAgentCapability(input string) error {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" || agentCatalogRefundPolicyIntent(normalized) {
		return nil
	}
	for _, marker := range agentUnsafeInputMarkers {
		if strings.Contains(normalized, marker) {
			return agentInvalid("AI 助手不执行 SQL、脚本、代码或提示词注入内容；请直接描述受支持的票务业务操作")
		}
	}
	// Existing authorization, listing and team-entry facts are legitimate
	// read-only subjects. Only their mutations are outside the Agent boundary.
	if agentPureReadRequest(normalized) && agentHasAny(normalized, []string{"分销授权", "渠道授权", "授权分销", "上架", "下架", "入园"}) {
		return nil
	}
	creationIntent := agentExplicitTicketProductCreateIntent(normalized)
	for _, marker := range agentUnsupportedCapabilityMarkers {
		// “创建一个线上票，不上架、不分销” describes the initial state of
		// the draft. It is not a request to mutate a protected listing fact.
		// Only suppress the lexical guard for a negated marker on an explicit
		// product-creation request; an affirmative existing-product mutation
		// must continue to fail closed.
		if (creationIntent || agentHasAny(normalized, agentReadIntentWords)) && agentUnsupportedMarkerNegated(normalized, marker) {
			continue
		}
		if strings.Contains(normalized, marker) {
			return agentInvalid(agentUnsupportedCapabilityMessage(normalized))
		}
	}
	return nil
}

func agentUnsupportedMarkerNegated(input, marker string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	marker = strings.ToLower(strings.TrimSpace(marker))
	if input == "" || marker == "" {
		return false
	}
	found := false
	allNegated := true
	for start := 0; ; {
		relative := strings.Index(input[start:], marker)
		if relative < 0 {
			return found && allNegated
		}
		found = true
		index := start + relative
		prefix := strings.TrimSpace(input[max(0, index-32):index])
		// Keep a negation attached to its current clause. This handles common
		// safety wording such as “不执行任何修改、支付或外部调用” while not
		// treating a later positive clause after a comma as negated.
		for _, separator := range []string{"，", ",", "。", "；", ";", "\n"} {
			if cut := strings.LastIndex(prefix, separator); cut >= 0 {
				prefix = strings.TrimSpace(prefix[cut+len(separator):])
			}
		}
		negated := false
		for _, negation := range []string{"不执行", "无需执行", "不要执行", "不进行", "不调用", "不需要", "无需", "不要", "禁止", "不能", "不允许", "不应", "不可", "不", "未"} {
			if strings.Contains(prefix, negation) {
				negated = true
				break
			}
		}
		allNegated = allNegated && negated
		start = index + len(marker)
		if start >= len(input) {
			return found && allNegated
		}
	}
}

func agentHasAffirmativeAgentWord(input string, values []string) bool {
	input = strings.ToLower(strings.TrimSpace(input))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(input, value) && !agentUnsupportedMarkerNegated(input, value) {
			return true
		}
	}
	return false
}

func agentPureReadRequest(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if !agentHasAny(normalized, agentReadIntentWords) {
		return false
	}
	mutationWords := append([]string{}, agentCatalogIntentWords...)
	mutationWords = append(mutationWords, agentProductCreateIntentWords...)
	mutationWords = append(mutationWords, agentProductUpdateIntentWords...)
	mutationWords = append(mutationWords, agentProductBatchUpdateIntentWords...)
	mutationWords = append(mutationWords, agentHotelMutationIntentWords...)
	mutationWords = append(mutationWords, []string{"支付", "退款", "退票", "设备控制", "下发设备", "硬件", "凭据", "密钥", "api key", "appid", "secret", "付款", "充值", "结算确认", "确认结算", "权限变更", "赋权", "写入", "执行", "确认"}...)
	return !agentHasAffirmativeAgentWord(normalized, mutationWords)
}

func agentUnsupportedCapabilityMessage(input string) string {
	if agentHasAny(input, []string{"创建预约", "预约创建", "取消预约", "预约取消", "改期预约", "预约改期", "修改预约", "预约入住"}) {
		return "当前 AI 助手未开放酒店预约创建、取消或改期；请使用酒店预约页面完成操作"
	}
	if agentHasAny(input, []string{"创建酒店", "删除酒店", "修改酒店", "创建房型", "删除房型", "修改房型", "创建价格计划", "删除价格计划", "修改价格计划"}) {
		return "当前 AI 助手未开放酒店结构配置；请使用酒店基础资料页面完成操作"
	}
	if agentHasAny(input, []string{"发布渠道", "渠道发布", "同步渠道", "pms", "webhook", "小红书", "携程", "ota"}) {
		return "当前 AI 助手未开放酒店渠道、PMS、Webhook 或外部平台状态操作；请使用对应业务页面完成操作"
	}
	if agentHasAny(input, []string{"设备控制", "下发设备", "开闸", "硬件"}) {
		return "当前 AI 助手未开放设备或闸机控制；请使用现场设备管理页面完成操作"
	}
	if agentHasAny(input, []string{"凭据", "密钥", "api key", "appid", "secret"}) {
		return "当前 AI 助手未开放渠道凭据、密钥或 AppID 管理；请使用平台配置页面完成操作"
	}
	if agentHasAny(input, []string{"分销授权", "渠道授权", "授权分销", "上架", "下架"}) {
		return "当前 AI 助手未开放上架、下架、分销授权或渠道授权；请使用对应业务页面完成操作"
	}
	if agentHasAny(input, []string{"权限变更", "赋权"}) {
		return "当前 AI 助手未开放用户权限或管理员变更；请使用用户与权限页面完成操作"
	}
	if agentHasAny(input, []string{"结算确认", "确认结算", "充值", "付款", "入园"}) {
		return "当前 AI 助手未开放付款、充值、结算确认或入园操作；请使用对应业务页面完成操作"
	}
	return "当前 AI 助手未开放支付、订单退款或退票；请使用售后与支付页面完成操作"
}

func agentReadOnlyCompoundIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if !agentHasAny(normalized, []string{"只读", "查询", "查看", "统计", "报表"}) {
		return false
	}
	sequenced := agentHasAny(normalized, []string{"复合", "多步", "步骤", "依次", "分别", "先", "再", "1)", "1、", "第一步", "第二步", "然后", "接着", "随后", "同时"})
	queryTopics := []string{"订单", "库存", "销售汇总", "核销汇总", "分销关系", "授权商品", "供应商履约", "分销结算", "团队合同", "团队计划", "团队结算", "团队账户", "票种规则", "检票点", "景区"}
	queryTopics = append(queryTopics, "酒店", "住宿预订", "房量", "价格计划", "价格日历", "入住", "离店")
	topicCount := 0
	for _, topic := range queryTopics {
		if strings.Contains(normalized, topic) {
			topicCount++
		}
	}
	if !sequenced && (topicCount < 2 || !agentHasAny(normalized, []string{"、", "，", ",", "和", "以及", "及", "并"})) {
		return false
	}
	// A sequencing word alone must not turn a mixed query/write request into a
	// read-only plan. Mutation vocabulary always stays on the write policy path;
	// the provider cannot use the read adapter to smuggle a second operation.
	mutationWords := append([]string{}, agentCatalogIntentWords...)
	mutationWords = append(mutationWords, agentProductCreateIntentWords...)
	mutationWords = append(mutationWords, agentProductUpdateIntentWords...)
	mutationWords = append(mutationWords, agentProductBatchUpdateIntentWords...)
	mutationWords = append(mutationWords, agentHotelMutationIntentWords...)
	mutationWords = append(mutationWords, "支付", "退款", "退票", "设备控制", "凭据", "密钥", "分销授权", "渠道授权", "上架", "下架", "写入", "执行", "确认", "充值", "结算", "入园")
	return !agentHasAffirmativeAgentWord(normalized, mutationWords)
}

// agentReadOnlyToolRoute narrows an unambiguous single-topic query to one
// server-owned adapter. This keeps ordinary questions deterministic while
// leaving explicit multi-topic requests to query_compound_readonly.
func agentReadOnlyToolRoute(input string) []string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" || agentReadOnlyCompoundIntent(normalized) || agentHasAffirmativeAgentWord(normalized, agentCatalogIntentWords) ||
		agentHasAffirmativeAgentWord(normalized, agentProductCreateIntentWords) || agentHasAffirmativeAgentWord(normalized, agentProductUpdateIntentWords) ||
		agentHasAffirmativeAgentWord(normalized, agentProductBatchUpdateIntentWords) {
		return nil
	}
	if !agentHasAny(normalized, agentReadIntentWords) {
		return nil
	}
	// Match the most specific business noun first. For example, “景区下的
	// 检票点” is a checkpoint query, not a compound scenic-area query.
	routes := []struct {
		tool  string
		words []string
	}{
		{"query_distribution_products", []string{"分销授权"}},
		{"query_distribution_products", []string{"授权商品", "授权产品", "铺货商品"}},
		{"query_distribution_fulfillments", []string{"供应商履约", "履约进度", "履约单"}},
		{"query_distribution_settlements", []string{"分销结算", "分销对账"}},
		{"query_distribution_partners", []string{"分销关系", "合作供应商", "合作关系", "分销合作"}},
		{"query_team_settlement_summary", []string{"团队结算", "团队对账"}},
		{"query_team_account_summary", []string{"团队账户", "团队授信", "团队余额"}},
		{"query_team_groups", []string{"团队计划", "团队团期", "团队入园", "入园情况"}},
		{"query_team_contracts", []string{"团队合同", "合同摘要"}},
		{"query_verification_summary", []string{"核销汇总", "核销报表", "核销统计"}},
		{"query_sales_summary", []string{"销售汇总", "销售报表", "销售统计"}},
		{"query_hotel_business_summary", []string{"酒店经营汇总", "住宿经营汇总", "酒店业务汇总"}},
		{"query_hotel_reservations", []string{"住宿预订", "酒店预订", "入住名单"}},
		{"query_hotel_booking_entitlements", []string{"预约权益", "住宿权益", "待预约住宿"}},
		{"query_hotel_product_calendar", []string{"酒店产品价格日历", "酒店产品售价日历", "日历房售价"}},
		{"query_hotel_rate_calendar", []string{"价格计划日历", "房型价格日历", "入住日价格"}},
		{"query_hotel_inventory", []string{"酒店库存", "酒店房量", "房态", "房量"}},
		{"search_hotel_catalog", []string{"酒店目录", "酒店", "房型", "价格计划", "日历房", "预售房"}},
		{"query_ticket_inventory", []string{"库存", "房量", "余票"}},
		{"get_ticket_product_rules", []string{"票种规则", "检票规则", "核销规则"}},
		{"search_checkpoints", []string{"检票点", "闸机点"}},
		{"search_scenic_areas", []string{"景区"}},
		{"search_orders", []string{"订单", "订单号"}},
		{"search_ticket_products", []string{"票种", "门票", "商品"}},
		{"search_ticket_products", []string{"上架状态", "下架状态", "上下架"}},
	}
	for _, route := range routes {
		if agentHasAny(normalized, route.words) {
			return []string{route.tool}
		}
	}
	return nil
}

// agentReadOnlyCompoundToolRoutes resolves an explicit multi-topic read in
// the same way the UI's report shortcuts are resolved: the server owns the
// tool names and preserves the order in which the user mentioned each topic.
// This is intentionally lexical and conservative. It never invents filters
// or turns a write request into a query; callers must first pass
// agentReadOnlyCompoundIntent.
func agentReadOnlyCompoundToolRoutes(input string) []string {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" || !agentReadOnlyCompoundIntent(normalized) {
		return nil
	}
	// Hotel compound reads require typed hotel/date/name arguments that the
	// lexical adapter cannot safely infer. Let the provider use the typed
	// compound query tool instead of issuing an empty ticket-inventory query.
	if agentHasAny(normalized, agentHotelIntentWords) {
		return nil
	}
	type route struct {
		tool  string
		words []string
	}
	routes := []route{
		{"search_orders", []string{"订单", "订单号"}},
		{"query_ticket_inventory", []string{"线上库存", "库存", "房量", "余票"}},
		{"query_sales_summary", []string{"销售汇总", "销售报表", "销售统计"}},
		{"query_verification_summary", []string{"核销汇总", "核销报表", "核销统计"}},
		{"query_distribution_partners", []string{"分销关系", "合作供应商", "分销合作"}},
		{"query_distribution_products", []string{"分销授权", "授权商品", "授权产品", "铺货商品"}},
		{"query_distribution_fulfillments", []string{"供应商履约", "履约进度", "履约单"}},
		{"query_distribution_settlements", []string{"分销结算", "分销对账"}},
		{"query_team_contracts", []string{"团队合同", "合同摘要"}},
		{"query_team_groups", []string{"团队计划", "团队团期", "团队入园", "入园情况"}},
		{"query_team_settlement_summary", []string{"团队结算", "团队对账"}},
		{"query_team_account_summary", []string{"团队账户", "团队授信", "团队余额"}},
		{"get_ticket_product_rules", []string{"票种规则", "检票规则", "核销规则"}},
		{"search_checkpoints", []string{"检票点", "闸机点"}},
		{"search_scenic_areas", []string{"景区"}},
		{"search_ticket_products", []string{"票种", "门票", "商品"}},
	}
	type locatedRoute struct {
		tool string
		pos  int
		len  int
	}
	located := make([]locatedRoute, 0, len(routes))
	for _, candidate := range routes {
		bestPos := -1
		bestLen := 0
		for _, word := range candidate.words {
			if pos := strings.Index(normalized, word); pos >= 0 && (bestPos < 0 || pos < bestPos || (pos == bestPos && len(word) > bestLen)) {
				bestPos = pos
				bestLen = len(word)
			}
		}
		if bestPos >= 0 {
			located = append(located, locatedRoute{tool: candidate.tool, pos: bestPos, len: bestLen})
		}
	}
	sort.SliceStable(located, func(i, j int) bool {
		if located[i].pos == located[j].pos {
			return located[i].len > located[j].len
		}
		return located[i].pos < located[j].pos
	})
	result := make([]string, 0, len(located))
	seen := make(map[string]struct{}, len(located))
	for _, item := range located {
		// Generic nouns such as “票种” or “商品” are often contained in a
		// more specific topic (“票种规则”, “授权商品”). Do not turn one user
		// topic into two server queries merely because of that substring.
		if item.tool == "search_ticket_products" {
			overlapped := false
			for _, specific := range located {
				if specific.tool == item.tool || specific.pos > item.pos || specific.pos+specific.len <= item.pos {
					continue
				}
				if item.pos < specific.pos+specific.len {
					overlapped = true
					break
				}
			}
			if overlapped {
				continue
			}
		}
		if _, ok := seen[item.tool]; ok {
			continue
		}
		seen[item.tool] = struct{}{}
		result = append(result, item.tool)
	}
	return result
}

func validateAgentTaskInputIntent(input string, task model.AgentTask) error {
	if err := validateAgentInputIntent(input, task.OperationType); err == nil {
		return nil
	} else if task.OperationType != AgentOperationCatalogBatchChange || (!agentTaskMissingField(task, "group_name") && !agentTaskMissingField(task, "target_scope")) {
		return err
	}
	return nil
}

func validateAgentPlannerEnvelopeForTask(input string, task model.AgentTask, envelope *agentAIEnvelope) error {
	envelopeOperationType := ""
	if envelope != nil {
		envelopeOperationType = strings.TrimSpace(envelope.OperationType)
	}
	if task.OperationType == AgentOperationCatalogBatchChange && (agentTaskMissingField(task, "group_name") || agentTaskMissingField(task, "target_scope")) && envelope != nil && envelope.Product == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationCatalogBatchChange) {
		// A provider may return no structured operation for a one-token answer
		// such as "水上乐园". The durable previous operation is authoritative;
		// the planner will reconstruct it below instead of returning a 500 or
		// discarding the user's answer.
		return nil
	}
	if task.OperationType == AgentOperationTicketProductUpdate && (agentTaskMissingField(task, "target_scope") || agentTaskMissingField(task, "product_name")) && envelope != nil && envelope.Product == nil && envelope.ProductUpdate == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationTicketProductUpdate) {
		return nil
	}
	if task.OperationType == AgentOperationTicketProductBatchUpdate && (agentTaskMissingField(task, "target_scope") || agentTaskMissingField(task, "product_names")) && envelope != nil && envelope.Product == nil && envelope.ProductUpdate == nil && envelope.ProductBatchUpdate == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationTicketProductBatchUpdate) {
		return nil
	}
	if task.OperationType == AgentOperationCompound && envelope != nil && envelope.Compound == nil && envelope.Product == nil && envelope.ProductUpdate == nil && envelope.ProductBatchUpdate == nil && len(envelope.Operations) == 0 && (envelopeOperationType == "" || envelopeOperationType == AgentOperationCompound) {
		return nil
	}
	// Continuation turns often contain only a value such as “北门、每点最多
	// 1 次”. Those words can look like a rule mutation to the generic intent
	// guard even though the durable task is a product-create/update task. Keep
	// the original operation as the authoritative intent for this validation.
	validationInput := input
	switch task.OperationType {
	case AgentOperationTicketProductCreate:
		validationInput += " 创建票种"
	case AgentOperationTicketProductUpdate:
		validationInput += " 修改票种"
	case AgentOperationTicketProductBatchUpdate:
		validationInput += " 批量修改票种"
	}
	if task.OperationType == AgentOperationCatalogBatchChange && envelopeOperationType == AgentOperationTicketProductCreate && agentExplicitTicketProductCreateIntent(task.InputText) {
		validationInput += " 创建票种"
	}
	if err := validateAgentPlannerEnvelope(validationInput, envelope); err == nil {
		return nil
	} else if task.OperationType != AgentOperationCatalogBatchChange || (!agentTaskMissingField(task, "group_name") && !agentTaskMissingField(task, "target_scope")) {
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
	"修改票种", "更新票种", "调整票种", "编辑票种", "改票种", "修改售价", "调整售价", "修改价格", "调整价格", "修改结算价", "调整结算价", "修改有效期", "调整有效期", "修改标签", "调整标签", "改名", "退款类型", "退款规则", "退改规则", "随时退", "免费退", "无理由退", "未核销可退", "未核销随时退",
}

var agentProductBatchUpdateIntentWords = []string{
	"批量修改", "批量更新", "批量调整", "多个票种", "这些票种", "这几个票种", "一批票种",
}

var agentHotelIntentWords = []string{
	"酒店", "住宿", "房型", "房量", "价格计划", "价格日历", "日历房", "预售房", "住宿预订", "入住", "离店", "未到店", "关房",
}

var agentHotelMutationIntentWords = []string{
	"设置房量", "调整房量", "增加房量", "减少房量", "关房", "开房", "设置价格", "调整价格", "清除覆盖价", "设置入住", "登记入住", "登记离店", "登记未到店",
}

var agentCompoundIntentWords = []string{"然后", "接着", "随后", "同时", "并且", "分别", "再", "第一步", "第二步", "步骤"}

func agentCompoundIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if !agentHasAny(normalized, agentCompoundIntentWords) {
		return false
	}
	return agentHasAny(normalized, agentCatalogIntentWords) || agentHasAny(normalized, agentProductCreateIntentWords) || agentHasAny(normalized, agentProductUpdateIntentWords) || agentHasAny(normalized, agentProductBatchUpdateIntentWords) || agentHasAny(normalized, agentHotelMutationIntentWords)
}

var agentReadIntentWords = []string{
	"查询", "查看", "列出", "有哪些", "统计", "报表", "库存", "订单", "票种", "检票点", "景区", "规则", "酒店", "住宿", "房型", "房量", "价格计划", "价格日历", "住宿预订",
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
				if !agentCatalogTextContains(input, productName) && !agentProductNameCoveredByExplicitScope(input, productName) &&
					!(agentHasBoundedProductScope(input) && agentCanBeBoundedProductFragment(productName)) {
					return agentInvalid(fmt.Sprintf("票种 %q 未在当前请求中明确指定，请使用当前租户的准确名称", productName))
				}
			}
		}
		for _, checkpointName := range operation.CheckpointNames {
			if !agentCatalogTextContains(input, checkpointName) {
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
		// A full catalog name in the current turn is stronger than a model's
		// shortened target_scope. Without this normalization, “【成人票】飞车
		// 套票” can be widened to every product containing “成人票” and the
		// normal resolver correctly (but inconveniently) asks for a range.
		// Explicit batch language keeps the category semantics intact.
		if !agentExplicitBatchIntent(input) {
			operationNames := explicitAgentCatalogNamesForOperation(operation, explicitNames)
			if len(operationNames) > 0 {
				// Multiple full names are an explicit multi-target request even if
				// the user omitted the word “批量”.
				operation.ProductNames = operationNames
				if operation.TargetScope != nil {
					scope := cloneAgentTargetScope(*operation.TargetScope)
					if len(operationNames) == 1 {
						scope.Intent = "single"
					} else {
						scope.Intent = "batch"
					}
					scope.NameTerms = append([]string(nil), operationNames...)
					scope.CandidateRefs = nil
					operation.TargetScope = &scope
				}
				result[index] = operation
				continue
			}
		}
		if len(explicitNames) > 1 && len(operation.ProductNames) == 0 && !agentExplicitBatchIntent(input) {
			// No provider target was returned. The full names in the user turn
			// are still safe to carry forward as one explicit batch operation.
			operation.ProductNames = append([]string(nil), explicitNames...)
			if operation.TargetScope != nil {
				scope := cloneAgentTargetScope(*operation.TargetScope)
				scope.Intent = "batch"
				scope.NameTerms = append([]string(nil), explicitNames...)
				scope.CandidateRefs = nil
				operation.TargetScope = &scope
			}
			result[index] = operation
			continue
		}
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

func explicitAgentCatalogNamesForOperation(operation CatalogRuleOperation, explicitNames []string) []string {
	if len(explicitNames) == 0 {
		return nil
	}
	if len(operation.ProductNames) == 0 {
		if len(explicitNames) == 1 {
			return append([]string(nil), explicitNames...)
		}
		return nil
	}
	selected := make([]string, 0, len(explicitNames))
	for _, requested := range operation.ProductNames {
		for _, explicit := range explicitNames {
			if agentCatalogNameCompatible(requested, explicit) {
				selected = appendUniqueAgentCatalogNames(selected, explicit)
			}
		}
	}
	if len(selected) > 0 {
		return selected
	}
	if len(explicitNames) == 1 {
		// A model may return an unrelated shorthand even though the user gave
		// exactly one full catalog name. The full name remains authoritative.
		return append([]string(nil), explicitNames...)
	}
	return nil
}

func agentCatalogNameCompatible(requested, candidate string) bool {
	requested = normalizeAgentCatalogName(requested)
	candidate = normalizeAgentCatalogName(candidate)
	return requested != "" && candidate != "" && (strings.Contains(candidate, requested) || strings.Contains(requested, candidate))
}

func normalizeAgentCatalogName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, separator := range []string{" ", "\t", "\r", "\n", "【", "】", "[", "]", "（", "）", "(", ")", "“", "”", "‘", "’", "「", "」", "『", "』", "《", "》", "〈", "〉", "\"", "'", "＂", "＇"} {
		value = strings.ReplaceAll(value, separator, "")
	}
	return value
}

// agentCatalogTextContains treats decorative quotes and whitespace as
// presentation around a catalog name. It does not broaden matching beyond
// the normalized name, so the tenant catalog resolver remains authoritative.
func agentCatalogTextContains(input, value string) bool {
	if agentTextContains(input, value) {
		return true
	}
	normalizedInput := normalizeAgentCatalogName(input)
	normalizedValue := normalizeAgentCatalogName(value)
	return normalizedValue != "" && strings.Contains(normalizedInput, normalizedValue)
}

// canonicalizeAgentCatalogCheckpointNames converts a provider's quoted or
// whitespace-variant checkpoint name to the one exact name in the current
// tenant catalog. Ambiguous normalized names are left untouched and will be
// rejected by the ordinary resolver instead of being guessed.
func canonicalizeAgentCatalogCheckpointNames(operations []CatalogRuleOperation, checkpoints []model.CheckPoint) []CatalogRuleOperation {
	byNormalizedName := make(map[string][]string, len(checkpoints))
	for _, checkpoint := range checkpoints {
		name := strings.TrimSpace(checkpoint.Name)
		if name == "" {
			continue
		}
		key := normalizeAgentCatalogName(name)
		if key != "" {
			byNormalizedName[key] = append(byNormalizedName[key], name)
		}
	}
	result := make([]CatalogRuleOperation, len(operations))
	copy(result, operations)
	for operationIndex := range result {
		for nameIndex, requested := range result[operationIndex].CheckpointNames {
			key := normalizeAgentCatalogName(requested)
			matches := byNormalizedName[key]
			if len(matches) == 1 {
				result[operationIndex].CheckpointNames[nameIndex] = matches[0]
			}
		}
	}
	return result
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

func agentHasCatalogRuleMutationIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	// A read verb with no catalog mutation verb is a lookup, even when the
	// subject contains words such as "检票点" or "核销规则". This guard must
	// run before the noun checks below so report and catalog lookups do not get
	// routed to a preview tool.
	if agentHasAny(normalized, agentReadIntentWords) && !agentHasAffirmativeAgentWord(normalized, agentCatalogIntentWords) &&
		!agentHasAffirmativeAgentWord(normalized, agentProductCreateIntentWords) && !agentHasAffirmativeAgentWord(normalized, agentProductUpdateIntentWords) {
		return false
	}
	if agentHasAny(normalized, []string{"检票点", "核销规则", "规则组", "分组", "票规"}) {
		return true
	}
	// “未核销随时退” contains the word 核销 but is a product refund policy,
	// not a request to change checkpoint admission rules. Treat standalone
	// 核销 as a rule marker only when the surrounding text is not refund-related.
	if !strings.Contains(normalized, "核销") {
		return false
	}
	return !agentHasAny(normalized, []string{"退款", "退票", "退改", "随时退", "免费退", "无理由退", "可退"})
}
