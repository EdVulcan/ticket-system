package service

import (
	"strings"
	"testing"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestAgentTargetScopeUsesExactNameUnlessUserRequestsBatch(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{
		{Base: model.Base{ID: 101}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"},
		{Base: model.Base{ID: 102}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket Package", Type: "online", Status: "online"},
	}

	single, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}}, []string{"Adult Ticket"}, "调整 Adult Ticket 的检票点", products, nil)
	if err != nil {
		t.Fatalf("single scope: %v", err)
	}
	if len(single.Targets) != 1 || single.Targets[0].Name != "Adult Ticket" {
		t.Fatalf("single exact scope expanded unexpectedly: %+v", single.Targets)
	}

	batch, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}, Intent: "single"}, []string{"Adult Ticket"}, "批量调整 Adult Ticket 的检票点", products, nil)
	if err != nil {
		t.Fatalf("batch scope: %v", err)
	}
	if len(batch.Targets) != 2 || batch.Scope.Intent != "batch" {
		t.Fatalf("explicit batch did not include exact and fuzzy matches: %+v scope=%+v", batch.Targets, batch.Scope)
	}
}

func TestAgentTargetScopeAppliesStatusBeforeAmbiguityAndRequiresScenicArea(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var secondArea model.ScenicArea
	if err := model.Write(func(tx *gorm.DB) error {
		secondArea = model.ScenicArea{TenantID: fixture.tenant.ID, Code: "SCOPE-AREA-2", Name: "Second Scenic", Status: "active"}
		return tx.Create(&secondArea).Error
	}); err != nil {
		t.Fatal(err)
	}
	products := []model.Product{
		{Base: model.Base{ID: 201}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Water Park A", Type: "online", Status: "online"},
		{Base: model.Base{ID: 202}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Water Park B", Type: "online", Status: "online"},
		{Base: model.Base{ID: 203}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Water Park C", Type: "online", Status: "offline"},
		{Base: model.Base{ID: 204}, TenantID: fixture.tenant.ID, ScenicAreaID: secondArea.ID, Name: "Water Park D", Type: "online", Status: "online"},
	}

	resolution, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		Intent: "batch", NameTerms: []string{"Water Park"}, ListingStatus: "listed",
	}, nil, "已上架的所有 Water Park 票种增加检票点", products, nil)
	if err != nil {
		t.Fatalf("status scope: %v", err)
	}
	if len(resolution.Missing) != 1 || resolution.State.AmbiguityReason != agentScopeReasonScenicArea {
		t.Fatalf("cross-area scope should ask for scenic area: %+v state=%+v", resolution.Missing, resolution.State)
	}
	for _, candidate := range resolution.State.Candidates {
		if candidate.Name == "Water Park C" {
			t.Fatal("unlisted product entered listed candidates")
		}
	}

	selected, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		Intent: "batch", NameTerms: []string{"Water Park"}, ScenicAreaNames: []string{fixture.area.Name}, ListingStatus: "listed",
	}, nil, "已上架的所有 Batch Scenic Water Park 票种增加检票点", products, nil)
	if err != nil {
		t.Fatalf("scenic-filtered scope: %v", err)
	}
	if len(selected.Targets) != 2 {
		t.Fatalf("scenic/status filters selected wrong products: %+v", selected.Targets)
	}
}

func TestAgentTargetScopeTurnsUnconfirmedAllScenicInferenceIntoClarification(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	var secondArea model.ScenicArea
	if err := model.Write(func(tx *gorm.DB) error {
		secondArea = model.ScenicArea{TenantID: fixture.tenant.ID, Code: "SCOPE-AREA-ALL", Name: "Second Scenic", Status: "active"}
		return tx.Create(&secondArea).Error
	}); err != nil {
		t.Fatal(err)
	}
	products := []model.Product{
		{Base: model.Base{ID: 211}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Water Park A", Type: "online", Status: "online"},
		{Base: model.Base{ID: 212}, TenantID: fixture.tenant.ID, ScenicAreaID: secondArea.ID, Name: "Water Park B", Type: "online", Status: "online"},
	}

	resolution, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		Version: agentTargetScopeVersion, Intent: "batch", ProductType: "online", AllScenicAreas: true,
	}, nil, "把所有线上门票设置为未核销随时退", products, nil)
	if err != nil {
		t.Fatalf("unconfirmed provider expansion should become clarification: %v", err)
	}
	if len(resolution.Missing) != 1 || resolution.State == nil || resolution.State.AmbiguityReason != agentScopeReasonScenicArea {
		t.Fatalf("expected durable scenic-area clarification, got resolution=%+v", resolution)
	}
	if resolution.Scope.AllScenicAreas {
		t.Fatal("unconfirmed all_scenic_areas inference was retained")
	}

	explicit, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		Version: agentTargetScopeVersion, Intent: "batch", ProductType: "online", AllScenicAreas: true,
	}, nil, "把所有线上门票设置为未核销随时退，全部景区", products, nil)
	if err != nil || len(explicit.Targets) != 2 || !explicit.Scope.AllScenicAreas {
		t.Fatalf("explicit all-scenic scope did not resolve: resolution=%+v err=%v", explicit, err)
	}
}

func TestAgentTargetScopeRecognizesUnlistedStatus(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{
		{Base: model.Base{ID: 151}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "offline"},
		{Base: model.Base{ID: 152}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket Package", Type: "online", Status: "online"},
	}

	resolution, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		NameTerms:     []string{"Adult Ticket"},
		ListingStatus: "unlisted",
	}, []string{"Adult Ticket"}, "调整未上架的 Adult Ticket", products, nil)
	if err != nil {
		t.Fatalf("resolve unlisted scope: %v", err)
	}
	if len(resolution.Missing) != 0 || len(resolution.Targets) != 1 || resolution.Targets[0].Name != "Adult Ticket" {
		t.Fatalf("expected only the unlisted exact product, got %#v missing=%#v", resolution.Targets, resolution.Missing)
	}
}

func TestAgentTargetScopeRejectsInventedFiltersAndForeignTenantProducts(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{
		{Base: model.Base{ID: 301}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"},
		{Base: model.Base{ID: 302}, TenantID: fixture.tenant.ID + 1000, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"},
	}

	resolution, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}, ListingStatus: "unlisted"}, []string{"Adult Ticket"}, "调整 Adult Ticket 的检票点", products, nil)
	if err == nil || !strings.Contains(err.Error(), "上架状态筛选未在当前请求") {
		t.Fatalf("invented listing filter was accepted: resolution=%+v err=%v", resolution, err)
	}

	resolution, err = resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}}, []string{"Adult Ticket"}, "调整 Adult Ticket 的检票点", products, nil)
	if err != nil {
		t.Fatalf("tenant-scoped resolution: %v", err)
	}
	if len(resolution.Targets) != 1 || resolution.Targets[0].TenantID != fixture.tenant.ID {
		t.Fatalf("foreign tenant product entered scope: %+v", resolution.Targets)
	}
}

func TestAgentTargetScopeCandidateReferenceCannotCrossTask(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{{Base: model.Base{ID: 401}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"}}
	previous := &agentTargetScopeState{Candidates: []agentTargetCandidate{{Ref: "候选1", ProductID: 401, Name: "Adult Ticket"}}}
	if _, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}, CandidateRefs: []string{"候选2"}}, nil, "选择候选2", products, previous); err == nil {
		t.Fatal("candidate reference from another task was accepted")
	}
}

func TestAgentTargetScopeIgnoresUnmentionedProviderCandidateOnExplicitBatch(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{
		{Base: model.Base{ID: 405}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"},
		{Base: model.Base{ID: 406}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket Package", Type: "online", Status: "offline"},
	}
	resolution, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{
		Intent: "batch", NameTerms: []string{"Adult Ticket"}, ScenicAreaNames: []string{fixture.area.Name}, CandidateRefs: []string{"候选1"},
	}, nil, "批量操作全部匹配的 Adult Ticket，景区为 "+fixture.area.Name, products, nil)
	if err != nil {
		t.Fatalf("explicit batch should not fail on an echoed candidate ref: %v", err)
	}
	if len(resolution.Targets) != 2 || len(resolution.Scope.CandidateRefs) != 0 {
		t.Fatalf("provider candidate ref narrowed explicit batch: targets=%+v scope=%+v", resolution.Targets, resolution.Scope)
	}
}

func TestAgentTargetScopeAllowsProviderRepeatOfResolvedTarget(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{{Base: model.Base{ID: 402}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"}}

	initial, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{NameTerms: []string{"Adult Ticket"}}, nil, "调整 Adult Ticket 的检票点", products, nil)
	if err != nil || len(initial.Targets) != 1 || initial.State == nil || initial.State.ResolutionState != "resolved" {
		t.Fatalf("initial target was not resolved: resolution=%+v err=%v", initial, err)
	}

	continued, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{CandidateRefs: []string{"候选1"}}, nil, "放入云上飞车二选一规则组", products, initial.State)
	if err != nil {
		t.Fatalf("provider repeat of the already resolved candidate was rejected: %v", err)
	}
	if len(continued.Targets) != 1 || continued.Targets[0].ID != products[0].ID {
		t.Fatalf("continuation selected the wrong target: %+v", continued.Targets)
	}
}

func TestAgentTargetScopeRejectsUnselectedCandidateOnResolvedContinuation(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	products := []model.Product{
		{Base: model.Base{ID: 403}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket", Type: "online", Status: "online"},
		{Base: model.Base{ID: 404}, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Name: "Adult Ticket Package", Type: "online", Status: "online"},
	}
	previous := &agentTargetScopeState{
		Requested:       AgentTargetScope{Version: agentTargetScopeVersion, Intent: "single", NameTerms: []string{"Adult Ticket"}},
		ResolutionState: "resolved",
		Candidates:      []agentTargetCandidate{{Ref: "候选1", ProductID: 403, Name: "Adult Ticket"}, {Ref: "候选2", ProductID: 404, Name: "Adult Ticket Package"}},
		SelectedTargets: []agentTargetCandidate{{Ref: "候选1", ProductID: 403, Name: "Adult Ticket"}},
	}

	if _, err := resolveAgentTargetScope(model.DB, fixture.tenant.ID, &AgentTargetScope{CandidateRefs: []string{"候选2"}}, nil, "继续设置规则组", products, previous); err == nil || !strings.Contains(err.Error(), "候选引用") {
		t.Fatalf("unselected candidate was accepted: %v", err)
	}
}
