package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

const agentTargetScopeVersion = 1

// AgentTargetScope is the deliberately small, versioned scope language used
// by all existing-product write tools. It describes user-facing filters only;
// the server resolves it to tenant-owned product IDs.
type AgentTargetScope struct {
	Version           int      `json:"version,omitempty"`
	Intent            string   `json:"intent,omitempty"` // single, batch
	NameTerms         []string `json:"name_terms,omitempty"`
	ScenicAreaNames   []string `json:"scenic_area_names,omitempty"`
	AllScenicAreas    bool     `json:"all_scenic_areas,omitempty"`
	ListingStatus     string   `json:"listing_status,omitempty"`     // listed, unlisted
	ProductType       string   `json:"product_type,omitempty"`       // online, window
	DistributionState string   `json:"distribution_state,omitempty"` // reserved for a later domain decision
	CandidateRefs     []string `json:"candidate_refs,omitempty"`
}

type agentTargetCandidate struct {
	Ref               string `json:"ref"`
	ProductID         uint   `json:"product_id,omitempty"`
	CurrentRevisionID uint   `json:"current_revision_id,omitempty"`
	Name              string `json:"name"`
	ScenicAreaID      uint   `json:"scenic_area_id,omitempty"`
	ScenicAreaName    string `json:"scenic_area_name,omitempty"`
	ProductType       string `json:"product_type,omitempty"`
	ListingStatus     string `json:"listing_status,omitempty"`
	IsDistributable   bool   `json:"is_distributable,omitempty"`
}

// agentTargetScopeState is kept in AgentTask.ContextJSON. IDs never leave
// the server: agentProviderContextJSON scrubs them before prompting a model.
type agentTargetScopeState struct {
	Requested       AgentTargetScope       `json:"requested_scope"`
	ResolutionState string                 `json:"resolution_state"` // unresolved, needs_clarification, resolved
	AmbiguityReason string                 `json:"ambiguity_reason,omitempty"`
	Candidates      []agentTargetCandidate `json:"candidates,omitempty"`
	SelectedTargets []agentTargetCandidate `json:"selected_targets,omitempty"`
	ScopeHash       string                 `json:"scope_hash,omitempty"`
}

type agentTargetScopeResolution struct {
	Scope   AgentTargetScope
	Targets []model.Product
	State   *agentTargetScopeState
	Missing []AgentMissingField
}

func loadAgentTargetProducts(db *gorm.DB, tenantID uint) ([]model.Product, error) {
	var products []model.Product
	if err := db.Where("tenant_id = ? AND deleted_at IS NULL", tenantID).Order("id ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	return products, nil
}

const (
	agentScopeReasonCardinality = "cardinality"
	agentScopeReasonScenicArea  = "scenic_area"
	agentScopeReasonDuplicate   = "duplicate_name"
	agentScopeReasonNoMatch     = "no_match"
)

// resolveAgentTargetScope applies the same tenant-scoped matching rules to
// ticket-rule changes and product updates. The caller passes the current
// tenant catalog; this function never queries another tenant or writes data.
func resolveAgentTargetScope(db *gorm.DB, tenantID uint, requested *AgentTargetScope, legacyNames []string, input string, products []model.Product, previous *agentTargetScopeState) (*agentTargetScopeResolution, error) {
	if tenantID == 0 {
		return nil, agentInvalid("tenant is required")
	}
	scope, err := normalizeAgentTargetScope(db, tenantID, requested, legacyNames, input, previous)
	if err != nil {
		return nil, err
	}
	areas, err := agentScopeAreaNames(db, tenantID)
	if err != nil {
		return nil, err
	}
	owned := make([]model.Product, 0, len(products))
	for index := range products {
		product := products[index]
		if product.TenantID != tenantID || isDistributedListing(&product) || product.ScenicAreaID == 0 {
			continue
		}
		if _, ok := areas[product.ScenicAreaID]; !ok {
			continue
		}
		owned = append(owned, product)
	}
	filtered, err := filterAgentTargetProducts(scope, owned, areas)
	if err != nil {
		return nil, err
	}
	if len(scope.CandidateRefs) > 0 {
		var refErr error
		filtered, refErr = filterAgentTargetCandidatesByRef(filtered, previous, scope.CandidateRefs)
		if refErr != nil {
			return nil, refErr
		}
	}

	selected, ambiguous, duplicate := selectAgentTargetProducts(scope, input, legacyNames, filtered)
	if len(selected) == 0 && len(ambiguous) == 0 {
		options := agentTargetCandidateOptions(owned, areas, 20)
		state := buildAgentTargetScopeState(scope, agentScopeReasonNoMatch, owned, nil, areas)
		return &agentTargetScopeResolution{
			Scope: scope, State: state,
			Missing: []AgentMissingField{{Field: "target_scope", Label: "操作范围", Question: fmt.Sprintf("没有找到符合“%s”的当前租户票种，请补充准确票种名称或筛选条件。", strings.Join(scope.NameTerms, "、")), Options: options}},
		}, nil
	}
	if len(ambiguous) > 0 || duplicate {
		reason := agentScopeReasonCardinality
		if duplicate {
			reason = agentScopeReasonDuplicate
		}
		state := buildAgentTargetScopeState(scope, reason, ambiguous, nil, areas)
		question := agentTargetScopeQuestion(scope, reason, ambiguous, areas)
		return &agentTargetScopeResolution{Scope: scope, State: state, Missing: []AgentMissingField{{Field: "target_scope", Label: "操作范围", Question: question, Options: agentTargetCandidateOptions(ambiguous, areas, 50)}}}, nil
	}
	if len(selected) > 100 {
		state := buildAgentTargetScopeState(scope, agentScopeReasonCardinality, selected, nil, areas)
		return &agentTargetScopeResolution{Scope: scope, State: state, Missing: []AgentMissingField{{Field: "target_scope", Label: "操作范围", Question: "当前筛选命中超过 100 个票种，请补充景区、上架状态、票种类型或更准确的名称。", Options: agentTargetCandidateOptions(selected, areas, 20)}}}, nil
	}
	areaIDs := make(map[uint]struct{})
	for _, product := range selected {
		areaIDs[product.ScenicAreaID] = struct{}{}
	}
	if len(areaIDs) > 1 && len(scope.ScenicAreaNames) == 0 && !scope.AllScenicAreas {
		state := buildAgentTargetScopeState(scope, agentScopeReasonScenicArea, selected, nil, areas)
		return &agentTargetScopeResolution{Scope: scope, State: state, Missing: []AgentMissingField{{Field: "target_scope.scenic_area_names", Label: "景区范围", Question: agentTargetAreaQuestion(selected, areas), Options: agentTargetAreaOptions(selected, areas)}}}, nil
	}
	if scope.Intent != "batch" && len(selected) > 1 {
		state := buildAgentTargetScopeState(scope, agentScopeReasonCardinality, selected, nil, areas)
		return &agentTargetScopeResolution{Scope: scope, State: state, Missing: []AgentMissingField{{Field: "target_scope.intent", Label: "操作范围", Question: agentTargetScopeQuestion(scope, agentScopeReasonCardinality, selected, areas), Options: agentTargetCandidateOptions(selected, areas, 50)}}}, nil
	}
	state := buildAgentTargetScopeState(scope, "", selected, selected, areas)
	state.ResolutionState = "resolved"
	return &agentTargetScopeResolution{Scope: scope, Targets: selected, State: state}, nil
}

func normalizeAgentTargetScope(db *gorm.DB, tenantID uint, requested *AgentTargetScope, legacyNames []string, input string, previous *agentTargetScopeState) (AgentTargetScope, error) {
	scope := AgentTargetScope{Version: agentTargetScopeVersion}
	if previous != nil {
		scope = cloneAgentTargetScope(previous.Requested)
	}
	if requested != nil {
		mergeAgentTargetScope(&scope, *requested)
	}
	if scope.Version == 0 {
		scope.Version = agentTargetScopeVersion
	}
	if scope.Version != agentTargetScopeVersion {
		return AgentTargetScope{}, agentInvalid("AI 返回了不受支持的目标范围版本")
	}
	if len(scope.NameTerms) == 0 {
		for _, name := range legacyNames {
			name = strings.TrimSpace(name)
			if name != "" && !agentStringInList(scope.NameTerms, name) {
				scope.NameTerms = append(scope.NameTerms, name)
			}
		}
	}
	if previous != nil {
		applyAgentTargetScopeAnswer(&scope, input, previous)
	}
	batchIntent := agentExplicitBatchIntent(input)
	singleIntent := agentExplicitSingleIntent(input)
	if agentBatchMarkerPresent(input) && singleIntent {
		return AgentTargetScope{}, agentInvalid("请求同时包含单票和批量范围，请明确只操作一个还是批量操作")
	}
	// The user's current turn is authoritative for cardinality. A provider may
	// omit the field or return the wrong value, but it cannot narrow an explicit
	// batch request to one product or broaden an explicit single-product request.
	switch {
	case batchIntent:
		scope.Intent = "batch"
	case singleIntent:
		scope.Intent = "single"
	case len(scope.NameTerms) > 1 || len(legacyNames) > 1 || len(scope.ScenicAreaNames) > 1 || len(scope.CandidateRefs) > 1 || scope.AllScenicAreas:
		scope.Intent = "batch"
	case scope.Intent == "":
		scope.Intent = "single"
	}
	if scope.Intent != "single" && scope.Intent != "batch" {
		return AgentTargetScope{}, agentInvalid("目标范围 intent 只能是 single 或 batch")
	}
	if scope.Intent == "batch" && !batchIntent && previous == nil && len(scope.NameTerms) <= 1 && len(legacyNames) <= 1 && requested != nil {
		return AgentTargetScope{}, agentInvalid("模型不能自行扩大操作范围；请在请求中明确批量、全部、所有或多个票种")
	}
	if scope.Intent == "batch" && singleIntent && previous == nil {
		return AgentTargetScope{}, agentInvalid("请求同时包含单票和批量范围，请明确只操作一个还是批量操作")
	}
	if scope.AllScenicAreas && !agentExplicitAllScenicAreas(input) && previous == nil {
		return AgentTargetScope{}, agentInvalid("模型不能自行扩大到全部景区，请在请求中明确全部景区")
	}
	if strings.TrimSpace(scope.DistributionState) != "" {
		return AgentTargetScope{}, agentInvalid("分销状态筛选的业务口径尚未开放，请先使用名称、景区、上架状态或票种类型筛选")
	}
	if scope.ListingStatus != "" && scope.ListingStatus != "listed" && scope.ListingStatus != "unlisted" {
		return AgentTargetScope{}, agentInvalid("上架状态筛选只能是 listed 或 unlisted")
	}
	if scope.ProductType != "" && scope.ProductType != "online" && scope.ProductType != "window" {
		return AgentTargetScope{}, agentInvalid("票种类型筛选只能是 online 或 window")
	}
	if err := validateAgentTargetScopeEvidence(db, tenantID, scope, legacyNames, input, previous); err != nil {
		return AgentTargetScope{}, err
	}
	for index := range scope.NameTerms {
		scope.NameTerms[index] = strings.TrimSpace(scope.NameTerms[index])
		if scope.NameTerms[index] == "" || len([]rune(scope.NameTerms[index])) > 100 {
			return AgentTargetScope{}, agentInvalid("票种名称筛选不能为空且不能超过 100 个字符")
		}
	}
	for index := range scope.ScenicAreaNames {
		canonical, err := resolveAgentAlias(db, tenantID, agentAliasScenicArea, scope.ScenicAreaNames[index])
		if err != nil {
			return AgentTargetScope{}, err
		}
		scope.ScenicAreaNames[index] = strings.TrimSpace(canonical)
	}
	return scope, nil
}

func mergeAgentTargetScope(target *AgentTargetScope, source AgentTargetScope) {
	if source.Version != 0 {
		target.Version = source.Version
	}
	if strings.TrimSpace(source.Intent) != "" {
		target.Intent = strings.TrimSpace(source.Intent)
	}
	if len(source.NameTerms) > 0 {
		target.NameTerms = append([]string(nil), source.NameTerms...)
	}
	if len(source.ScenicAreaNames) > 0 {
		target.ScenicAreaNames = append([]string(nil), source.ScenicAreaNames...)
	}
	if source.AllScenicAreas {
		target.AllScenicAreas = true
	}
	if strings.TrimSpace(source.ListingStatus) != "" {
		target.ListingStatus = strings.TrimSpace(source.ListingStatus)
	}
	if strings.TrimSpace(source.ProductType) != "" {
		target.ProductType = strings.TrimSpace(source.ProductType)
	}
	if strings.TrimSpace(source.DistributionState) != "" {
		target.DistributionState = strings.TrimSpace(source.DistributionState)
	}
	if len(source.CandidateRefs) > 0 {
		target.CandidateRefs = append([]string(nil), source.CandidateRefs...)
	}
}

func cloneAgentTargetScope(source AgentTargetScope) AgentTargetScope {
	result := source
	result.NameTerms = append([]string(nil), source.NameTerms...)
	result.ScenicAreaNames = append([]string(nil), source.ScenicAreaNames...)
	result.CandidateRefs = append([]string(nil), source.CandidateRefs...)
	return result
}

func applyAgentTargetScopeAnswer(scope *AgentTargetScope, input string, previous *agentTargetScopeState) {
	if scope == nil || previous == nil {
		return
	}
	if agentExplicitBatchIntent(input) {
		scope.Intent = "batch"
	} else if agentExplicitSingleIntent(input) {
		scope.Intent = "single"
	}
	if agentExplicitAllScenicAreas(input) {
		scope.AllScenicAreas = true
		scope.ScenicAreaNames = nil
	}
	for _, candidate := range previous.Candidates {
		if agentTextContains(input, candidate.Ref) {
			scope.CandidateRefs = appendUniqueString(scope.CandidateRefs, candidate.Ref)
		}
		if candidate.ScenicAreaName != "" && agentTextContains(input, candidate.ScenicAreaName) {
			scope.ScenicAreaNames = appendUniqueString(scope.ScenicAreaNames, candidate.ScenicAreaName)
		}
	}
}

func filterAgentTargetProducts(scope AgentTargetScope, products []model.Product, areas map[uint]string) ([]model.Product, error) {
	areaSet := make(map[string]struct{}, len(scope.ScenicAreaNames))
	for _, name := range scope.ScenicAreaNames {
		matched := false
		for areaID, areaName := range areas {
			if strings.EqualFold(strings.TrimSpace(areaName), strings.TrimSpace(name)) {
				areaSet[fmt.Sprintf("%d", areaID)] = struct{}{}
				matched = true
			}
		}
		if !matched {
			return nil, agentInvalid(fmt.Sprintf("景区“%s”不属于当前租户或不存在", name))
		}
	}
	filtered := make([]model.Product, 0, len(products))
	for _, product := range products {
		if len(areaSet) > 0 {
			if _, ok := areaSet[fmt.Sprintf("%d", product.ScenicAreaID)]; !ok {
				continue
			}
		}
		if scope.ListingStatus == "listed" && product.Status != "online" {
			continue
		}
		if scope.ListingStatus == "unlisted" && product.Status != "offline" {
			continue
		}
		if scope.ProductType == "online" && product.Type != "online" {
			continue
		}
		if scope.ProductType == "window" && product.Type != "offline" {
			continue
		}
		filtered = append(filtered, product)
	}
	return filtered, nil
}

func selectAgentTargetProducts(scope AgentTargetScope, input string, legacyNames []string, products []model.Product) (selected, ambiguous []model.Product, duplicate bool) {
	terms := append([]string(nil), scope.NameTerms...)
	if len(terms) == 0 {
		if scope.Intent == "batch" {
			return uniqueAgentProducts(products), nil, false
		}
		if len(products) == 1 {
			return products, nil, false
		}
		return nil, uniqueAgentProducts(products), false
	}
	seenSelected := make(map[uint]struct{})
	seenAmbiguous := make(map[uint]struct{})
	for _, term := range terms {
		normalizedTerm := normalizeAgentCatalogName(term)
		exact := make([]model.Product, 0)
		fuzzy := make([]model.Product, 0)
		for _, product := range products {
			name := normalizeAgentCatalogName(product.Name)
			if name == normalizedTerm {
				exact = append(exact, product)
			}
			if normalizedTerm != "" && (strings.Contains(name, normalizedTerm) || strings.Contains(normalizedTerm, name)) {
				fuzzy = append(fuzzy, product)
			}
		}
		if len(exact) > 1 {
			duplicate = true
			for _, product := range exact {
				if _, ok := seenAmbiguous[product.ID]; !ok {
					ambiguous = append(ambiguous, product)
					seenAmbiguous[product.ID] = struct{}{}
				}
			}
			continue
		}
		matches := fuzzy
		if scope.Intent != "batch" && len(exact) == 1 {
			matches = exact
		}
		if len(matches) == 0 {
			continue
		}
		if scope.Intent != "batch" && len(matches) > 1 {
			for _, product := range matches {
				if _, ok := seenAmbiguous[product.ID]; !ok {
					ambiguous = append(ambiguous, product)
					seenAmbiguous[product.ID] = struct{}{}
				}
			}
			continue
		}
		for _, product := range matches {
			if _, ok := seenSelected[product.ID]; !ok {
				selected = append(selected, product)
				seenSelected[product.ID] = struct{}{}
			}
		}
	}
	if len(legacyNames) > 1 && len(ambiguous) == 0 {
		// Explicitly listed names are an unambiguous multi-target request even
		// when the natural-language sentence did not contain “batch”.
		scope.Intent = "batch"
	}
	return selected, ambiguous, duplicate
}

func filterAgentTargetCandidatesByRef(products []model.Product, previous *agentTargetScopeState, refs []string) ([]model.Product, error) {
	if previous == nil || len(refs) == 0 {
		return products, nil
	}
	ids := make(map[uint]struct{})
	knownRefs := make(map[string]struct{})
	for _, candidate := range previous.Candidates {
		knownRefs[strings.ToLower(strings.TrimSpace(candidate.Ref))] = struct{}{}
		for _, ref := range refs {
			if strings.EqualFold(strings.TrimSpace(ref), strings.TrimSpace(candidate.Ref)) && candidate.ProductID != 0 {
				ids[candidate.ProductID] = struct{}{}
			}
		}
	}
	for _, ref := range refs {
		if _, ok := knownRefs[strings.ToLower(strings.TrimSpace(ref))]; !ok {
			return nil, agentInvalid(fmt.Sprintf("候选引用“%s”不属于当前任务，请重新选择候选票种", strings.TrimSpace(ref)))
		}
	}
	filtered := make([]model.Product, 0, len(products))
	for _, product := range products {
		if _, ok := ids[product.ID]; ok {
			filtered = append(filtered, product)
		}
	}
	return filtered, nil
}

func buildAgentTargetScopeState(scope AgentTargetScope, reason string, candidates, selected []model.Product, areas map[uint]string) *agentTargetScopeState {
	state := &agentTargetScopeState{Requested: cloneAgentTargetScope(scope), ResolutionState: "needs_clarification", AmbiguityReason: reason}
	state.Candidates = agentTargetCandidates(candidates)
	state.SelectedTargets = agentTargetCandidates(selected)
	for index := range state.Candidates {
		state.Candidates[index].ScenicAreaName = areas[state.Candidates[index].ScenicAreaID]
	}
	for index := range state.SelectedTargets {
		state.SelectedTargets[index].ScenicAreaName = areas[state.SelectedTargets[index].ScenicAreaID]
	}
	state.ScopeHash = agentTargetScopeHash(scope, selected)
	if reason == "" {
		state.ResolutionState = "resolved"
	}
	return state
}

func agentTargetCandidates(products []model.Product) []agentTargetCandidate {
	sort.SliceStable(products, func(i, j int) bool {
		if products[i].ScenicAreaID != products[j].ScenicAreaID {
			return products[i].ScenicAreaID < products[j].ScenicAreaID
		}
		if products[i].Name != products[j].Name {
			return products[i].Name < products[j].Name
		}
		return products[i].ID < products[j].ID
	})
	result := make([]agentTargetCandidate, 0, len(products))
	for index, product := range products {
		result = append(result, agentTargetCandidate{Ref: fmt.Sprintf("候选%d", index+1), ProductID: product.ID, CurrentRevisionID: product.CurrentRevisionID, Name: product.Name, ScenicAreaID: product.ScenicAreaID, ProductType: product.Type, ListingStatus: product.Status, IsDistributable: product.IsDistributable})
	}
	return result
}

func agentTargetCandidateOptions(products []model.Product, areas map[uint]string, limit int) []string {
	candidates := agentTargetCandidates(append([]model.Product(nil), products...))
	if limit <= 0 || len(candidates) < limit {
		limit = len(candidates)
	}
	options := make([]string, 0, limit)
	for _, candidate := range candidates[:limit] {
		area := areas[candidate.ScenicAreaID]
		typeLabel := "窗口票"
		if candidate.ProductType == "online" {
			typeLabel = "线上票"
		}
		statusLabel := "已下架"
		if candidate.ListingStatus == "online" {
			statusLabel = "已上架"
		}
		options = append(options, fmt.Sprintf("%s｜%s｜%s｜%s｜%s", candidate.Ref, area, candidate.Name, typeLabel, statusLabel))
	}
	return options
}

func agentTargetAreaOptions(products []model.Product, areas map[uint]string) []string {
	seen := make(map[uint]struct{})
	options := make([]string, 0)
	for _, product := range products {
		if _, ok := seen[product.ScenicAreaID]; ok {
			continue
		}
		seen[product.ScenicAreaID] = struct{}{}
		if name := strings.TrimSpace(areas[product.ScenicAreaID]); name != "" {
			options = append(options, name)
		}
	}
	sort.Strings(options)
	return options
}

func agentTargetScopeQuestion(scope AgentTargetScope, reason string, products []model.Product, areas map[uint]string) string {
	if reason == agentScopeReasonDuplicate {
		return "同名票种对应多个当前租户票种，请补充所属景区或候选编号；如果要操作多个景区，请明确列出景区范围。"
	}
	if reason == agentScopeReasonScenicArea {
		return agentTargetAreaQuestion(products, areas)
	}
	if scope.Intent == "batch" {
		return "当前筛选命中多个票种，请确认要操作全部匹配票种，还是补充景区或更准确的票种名称。"
	}
	return "当前名称匹配到多个票种，请明确操作其中一个还是批量操作全部匹配票种，并可补充所属景区。"
}

func agentTargetAreaQuestion(products []model.Product, areas map[uint]string) string {
	return fmt.Sprintf("当前筛选命中多个景区，请明确操作一个或多个景区的票种（可选：%s；也可以明确说全部景区）。", strings.Join(agentTargetAreaOptions(products, areas), "、"))
}

func agentScopeAreaNames(db *gorm.DB, tenantID uint) (map[uint]string, error) {
	var rows []model.ScenicArea
	if err := db.Where("tenant_id = ? AND status = ?", tenantID, "active").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[uint]string, len(rows))
	for _, row := range rows {
		result[row.ID] = row.Name
	}
	return result, nil
}

func agentTargetScopeHash(scope AgentTargetScope, products []model.Product) string {
	ids := make([]uint, 0, len(products))
	for _, product := range products {
		ids = append(ids, product.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	value, _ := json.Marshal(struct {
		Scope AgentTargetScope `json:"scope"`
		IDs   []uint           `json:"ids"`
	}{cloneAgentTargetScope(scope), ids})
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func uniqueAgentProducts(products []model.Product) []model.Product {
	seen := make(map[uint]struct{}, len(products))
	result := make([]model.Product, 0, len(products))
	for _, product := range products {
		if _, ok := seen[product.ID]; ok {
			continue
		}
		seen[product.ID] = struct{}{}
		result = append(result, product)
	}
	return result
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(value)) {
			return values
		}
	}
	return append(values, value)
}

func agentExplicitBatchIntent(input string) bool {
	return agentBatchMarkerPresent(input) && !agentExplicitSingleIntent(input)
}

func agentBatchMarkerPresent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return false
	}
	for _, phrase := range []string{"不要批量", "不批量", "不是批量", "不要全部", "不需要全部"} {
		if strings.Contains(normalized, phrase) {
			return false
		}
	}
	for _, phrase := range []string{"批量", "多个", "多种", "这批", "所有", "全部", "all", "batch", "multiple"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func agentExplicitSingleIntent(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	for _, phrase := range []string{"一个", "单个", "其中一个", "只操作一张", "只操作一个", "只改一个", "one"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func agentExplicitAllScenicAreas(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	for _, phrase := range []string{"全部景区", "所有景区", "各个景区", "all scenic areas"} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

// validateAgentTargetScopeEvidence prevents a provider from silently adding
// a product, scenic area, status, or type filter that the operator never
// requested. Existing accepted scope facts may be repeated on continuation
// turns; every new value still needs evidence in the current turn.
func validateAgentTargetScopeEvidence(db *gorm.DB, tenantID uint, scope AgentTargetScope, legacyNames []string, input string, previous *agentTargetScopeState) error {
	for _, name := range scope.NameTerms {
		if !agentTargetScopeValueHasEvidence(db, tenantID, agentAliasProduct, name, input, previous, func(previousScope AgentTargetScope) bool {
			return agentStringInList(previousScope.NameTerms, name)
		}) && !agentProductNameCoveredByExplicitScope(input, name) {
			return agentInvalid(fmt.Sprintf("票种范围“%s”未在当前请求或已确认上下文中出现", strings.TrimSpace(name)))
		}
	}
	for _, name := range legacyNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if !agentTargetScopeValueHasEvidence(db, tenantID, agentAliasProduct, name, input, previous, func(previousScope AgentTargetScope) bool {
			return agentStringInList(previousScope.NameTerms, name)
		}) && !agentProductNameCoveredByExplicitScope(input, name) {
			return agentInvalid(fmt.Sprintf("票种“%s”未在当前请求或已确认上下文中出现", strings.TrimSpace(name)))
		}
	}
	for _, name := range scope.ScenicAreaNames {
		if !agentTargetScopeValueHasEvidence(db, tenantID, agentAliasScenicArea, name, input, previous, func(previousScope AgentTargetScope) bool {
			return agentStringInList(previousScope.ScenicAreaNames, name)
		}) {
			return agentInvalid(fmt.Sprintf("景区范围“%s”未在当前请求或已确认上下文中出现", strings.TrimSpace(name)))
		}
	}
	if scope.AllScenicAreas && !agentExplicitAllScenicAreas(input) && (previous == nil || !previous.Requested.AllScenicAreas) {
		return agentInvalid("模型不能自行扩大到全部景区，请在请求中明确全部景区")
	}
	if scope.ListingStatus != "" && !agentListingStatusHasEvidence(scope.ListingStatus, input) && (previous == nil || previous.Requested.ListingStatus != scope.ListingStatus) {
		return agentInvalid("上架状态筛选未在当前请求或已确认上下文中出现")
	}
	if scope.ProductType != "" && !agentProductTypeHasEvidence(scope.ProductType, input) && (previous == nil || previous.Requested.ProductType != scope.ProductType) {
		return agentInvalid("票种类型筛选未在当前请求或已确认上下文中出现")
	}
	for _, ref := range scope.CandidateRefs {
		selectedInContext := false
		if previous != nil && previous.ResolutionState == "resolved" {
			for _, target := range previous.SelectedTargets {
				if strings.EqualFold(strings.TrimSpace(target.Ref), strings.TrimSpace(ref)) {
					selectedInContext = true
					break
				}
			}
		}
		if previous == nil || (!agentStringInList(previous.Requested.CandidateRefs, ref) && !selectedInContext && !agentTextContains(input, ref)) {
			return agentInvalid(fmt.Sprintf("候选引用“%s”未在当前任务中被选择", strings.TrimSpace(ref)))
		}
	}
	return nil
}

func agentTargetScopeValueHasEvidence(db *gorm.DB, tenantID uint, kind, value, input string, previous *agentTargetScopeState, previousMatch func(AgentTargetScope) bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if agentTextContains(input, value) || strings.Contains(normalizeAgentCatalogName(input), normalizeAgentCatalogName(value)) {
		return true
	}
	if previous != nil && previousMatch(previous.Requested) {
		return true
	}
	if db == nil || tenantID == 0 {
		return false
	}
	var aliases []model.AgentBusinessAlias
	if err := db.Where("tenant_id = ? AND kind = ? AND LOWER(canonical_name) = LOWER(?)", tenantID, kind, value).Find(&aliases).Error; err != nil {
		return false
	}
	for _, alias := range aliases {
		if agentTextContains(input, alias.Alias) {
			return true
		}
	}
	return false
}

func agentListingStatusHasEvidence(status, input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	unlisted := strings.Contains(normalized, "未上架") || strings.Contains(normalized, "下架") || strings.Contains(normalized, "未挂牌")
	// "未上架" contains the positive word "上架"; evaluate the negative
	// form first so a valid unlisted filter is not treated as contradictory.
	listed := strings.Contains(normalized, "已上架") || (strings.Contains(normalized, "上架") && !unlisted)
	if listed && unlisted {
		return false
	}
	switch status {
	case "listed":
		return listed
	case "unlisted":
		return unlisted
	default:
		return false
	}
}

func agentProductTypeHasEvidence(productType, input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	switch productType {
	case "online":
		return agentHasAny(normalized, []string{"线上", "在线", "网售", "小程序票"}) && !agentHasAny(normalized, []string{"窗口", "线下", "现场票"})
	case "window":
		return agentHasAny(normalized, []string{"窗口", "线下", "现场票", "pos"}) && !agentHasAny(normalized, []string{"线上", "在线", "网售", "小程序票"})
	default:
		return false
	}
}

// resolveAgentCatalogTargetScopes adapts the generic scope resolver to the
// existing catalog operation DSL. It returns names/IDs only after the scope
// is resolved; an unresolved scope is returned as missing_fields and no
// catalog plan is created.
func resolveAgentCatalogTargetScopes(db *gorm.DB, tenantID uint, input string, operations []CatalogRuleOperation, products []model.Product, previous *agentTargetScopeState, previousStates []*agentTargetScopeState) ([]CatalogRuleOperation, []*agentTargetScopeState, []AgentMissingField, error) {
	resolved := make([]CatalogRuleOperation, len(operations))
	copy(resolved, operations)
	states := make([]*agentTargetScopeState, len(resolved))
	missing := make([]AgentMissingField, 0)
	for index := range resolved {
		operation := &resolved[index]
		if operation.AllProducts {
			if operation.TargetScope == nil {
				operation.TargetScope = &AgentTargetScope{Version: agentTargetScopeVersion, Intent: "batch"}
			}
		}
		legacyNames := append([]string(nil), operation.ProductNames...)
		operationPrevious := (*agentTargetScopeState)(nil)
		if index < len(previousStates) {
			operationPrevious = previousStates[index]
		}
		if operationPrevious == nil && index == 0 {
			operationPrevious = previous
		}
		resolution, err := resolveAgentTargetScope(db, tenantID, operation.TargetScope, legacyNames, input, products, operationPrevious)
		if err != nil {
			return nil, nil, nil, err
		}
		states[index] = resolution.State
		if len(resolution.Missing) > 0 {
			missing = append(missing, prefixAgentTargetScopeMissing(resolution.Missing, index)...)
			continue
		}
		operation.ProductIDs = make([]uint, 0, len(resolution.Targets))
		operation.ProductNames = make([]string, 0, len(resolution.Targets))
		for _, product := range resolution.Targets {
			operation.ProductIDs = append(operation.ProductIDs, product.ID)
			operation.ProductNames = append(operation.ProductNames, product.Name)
		}
		operation.ProductIDs = uniqueSortedIDs(operation.ProductIDs)
		operation.ProductNames = uniqueSortedStrings(operation.ProductNames)
		operation.AllProducts = false
		operation.TargetScope = nil
	}
	if len(missing) > 0 {
		return resolved, states, missing, nil
	}
	for index, state := range states {
		if state == nil {
			continue
		}
		state.ResolutionState = "resolved"
		state.AmbiguityReason = ""
		if index < len(resolved) {
			state.ScopeHash = agentTargetScopeHash(state.Requested, productsForCatalogOperation(resolved[index], products))
		}
	}
	return resolved, states, nil, nil
}

func productsForCatalogOperation(operation CatalogRuleOperation, products []model.Product) []model.Product {
	ids := make(map[uint]struct{}, len(operation.ProductIDs))
	for _, id := range operation.ProductIDs {
		ids[id] = struct{}{}
	}
	selected := make([]model.Product, 0, len(ids))
	for _, product := range products {
		if _, ok := ids[product.ID]; ok {
			selected = append(selected, product)
		}
	}
	return selected
}

// legacyCatalogTargetScopeStates reconstructs per-operation continuation
// state for tasks created before target_scopes was introduced. New tasks carry
// the full candidate snapshot; old tasks at least retain the accepted product
// names, which is sufficient to continue a rule-group question safely.
func legacyCatalogTargetScopeStates(context agentTaskContext) []*agentTargetScopeState {
	if len(context.Operations) == 0 {
		return nil
	}
	states := make([]*agentTargetScopeState, len(context.Operations))
	for index, operation := range context.Operations {
		if index == 0 && context.TargetScope != nil {
			states[index] = context.TargetScope
			continue
		}
		if operation.TargetScope != nil {
			scope := cloneAgentTargetScope(*operation.TargetScope)
			states[index] = &agentTargetScopeState{Requested: scope, ResolutionState: "needs_clarification"}
			continue
		}
		scope := AgentTargetScope{Version: agentTargetScopeVersion, Intent: "single"}
		for _, name := range operation.ProductNames {
			name = strings.TrimSpace(name)
			if name != "" {
				scope.NameTerms = appendUniqueString(scope.NameTerms, name)
			}
		}
		if operation.AllProducts || len(scope.NameTerms) > 1 {
			scope.Intent = "batch"
		}
		if len(scope.NameTerms) > 0 || operation.AllProducts {
			states[index] = &agentTargetScopeState{Requested: scope, ResolutionState: "resolved"}
		}
	}
	return states
}

func legacyProductUpdateTargetScopeState(context agentTaskContext, batch bool) *agentTargetScopeState {
	if context.TargetScope != nil {
		return context.TargetScope
	}
	state := &agentTargetScopeState{Requested: AgentTargetScope{Version: agentTargetScopeVersion, Intent: "single"}, ResolutionState: "resolved"}
	if batch {
		state.Requested.Intent = "batch"
		if context.ProductBatchUpdate != nil {
			for _, target := range context.ProductBatchUpdate.Targets {
				if name := strings.TrimSpace(target.ProductName); name != "" {
					state.Requested.NameTerms = appendUniqueString(state.Requested.NameTerms, name)
				}
			}
		}
	} else if context.ProductUpdate != nil {
		if name := strings.TrimSpace(context.ProductUpdate.ProductName); name != "" {
			state.Requested.NameTerms = []string{name}
		}
	}
	if len(state.Requested.NameTerms) == 0 {
		return nil
	}
	return state
}

func scrubAgentCatalogOperationIDs(operations []CatalogRuleOperation) []CatalogRuleOperation {
	result := make([]CatalogRuleOperation, len(operations))
	copy(result, operations)
	for index := range result {
		result[index].ProductIDs = nil
		result[index].CheckpointIDs = nil
	}
	return result
}

func prefixAgentTargetScopeMissing(fields []AgentMissingField, operationIndex int) []AgentMissingField {
	result := make([]AgentMissingField, 0, len(fields))
	for _, field := range fields {
		fieldPath := strings.TrimPrefix(field.Field, "target_scope")
		fieldPath = strings.TrimPrefix(fieldPath, ".")
		field.Field = fmt.Sprintf("operations[%d]", operationIndex)
		if fieldPath != "" {
			field.Field += "." + fieldPath
		}
		field.Field = strings.TrimSuffix(field.Field, ".")
		result = append(result, field)
	}
	return result
}

func uniqueSortedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || agentStringInList(result, value) {
			continue
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
