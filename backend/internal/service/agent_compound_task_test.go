package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestAgentCompoundEnvelopeAcceptsOnlyLowRiskSteps(t *testing.T) {
	price := 120.0
	envelope := &agentAIEnvelope{
		OperationType: AgentOperationCompound,
		Compound: &agentCompoundCandidate{Steps: []agentCompoundStepCandidate{
			{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}}}},
			{OperationType: AgentOperationTicketProductUpdate, ProductUpdate: &agentProductUpdateCandidate{ProductName: "Adult Ticket", Changes: agentProductUpdateChanges{Price: &price}}},
		}},
	}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate 检票点，然后修改售价为 120 元", envelope); err != nil {
		t.Fatalf("valid compound envelope rejected: %v", err)
	}
	tooMany := *envelope.Compound
	tooMany.Steps = append(tooMany.Steps, tooMany.Steps...)
	tooMany.Steps = append(tooMany.Steps, tooMany.Steps[0], tooMany.Steps[1])
	if err := validateAgentPlannerEnvelope("先做多个步骤", &agentAIEnvelope{OperationType: AgentOperationCompound, Compound: &tooMany}); err == nil || !strings.Contains(err.Error(), "2 到 5") {
		t.Fatalf("compound step limit was not enforced: %v", err)
	}
	unsafe := &agentAIEnvelope{OperationType: AgentOperationCompound, Compound: &agentCompoundCandidate{Steps: []agentCompoundStepCandidate{
		{OperationType: "refund_payment"},
		{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{"Adult Ticket"}, CheckpointNames: []string{"North Gate"}}}},
	}}}
	if err := validateAgentPlannerEnvelope("给 Adult Ticket 增加 North Gate，然后退款", unsafe); err == nil || !strings.Contains(err.Error(), "不受支持") {
		t.Fatalf("unsafe compound step was accepted: %v", err)
	}
}

func TestAgentProviderContextStripsCompoundIdentifiers(t *testing.T) {
	price := 80.0
	context := agentTaskContext{OperationType: AgentOperationCompound, Compound: &agentCompoundDraft{Steps: []agentCompoundStepDraft{{
		Index: 0, OperationType: AgentOperationTicketProductUpdate, ChildTaskID: 77,
		Context: &agentTaskContext{OperationType: AgentOperationTicketProductUpdate, ProductUpdate: &agentProductUpdateDraft{ProductID: 9, CurrentRevisionID: 11, ProductName: "Adult Ticket", Changes: agentProductUpdateChanges{Price: &price}}},
	}}}}
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	scrubbed, err := agentProviderContextJSON(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scrubbed, "77") || strings.Contains(scrubbed, "\"product_id\":9") || strings.Contains(scrubbed, "\"current_revision_id\":11") {
		t.Fatalf("compound provider context leaked identifiers: %s", scrubbed)
	}
}

func TestAgentCompoundPlanPersistsChildrenAndConfirmsInOrder(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	child := model.Product{Name: "Child Ticket", Price: 80, SettlementPrice: 40, TenantID: fixture.tenant.ID, ScenicAreaID: fixture.area.ID, Type: "online", Status: "online", ValidityType: "date", StockType: "unlimited", CodeMode: "ticket", RefundType: "free", GateVoiceCode: "welcome"}
	childRule := model.TicketRule{Name: "Child Rule", TenantID: fixture.tenant.ID, ValidityType: "date", Groups: []model.RuleGroup{{GroupName: "Admission", MaxTotalCheckIn: 1, Items: []model.RuleItem{{CheckPointID: fixture.checkpoint.ID, MaxPerCheckIn: 1}}}}}
	if err := (&ProductService{}).Create(&child, &childRule); err != nil {
		t.Fatal(err)
	}
	service := &AgentTaskService{}
	parent := model.AgentTask{TenantID: fixture.tenant.ID, ActorUserID: 11, ActorRole: "admin", OperationType: AgentOperationCompound, State: AgentTaskCollecting, InputText: "给 Adult Ticket 和 Child Ticket 分别增加 North Gate", ContextJSON: `{}`, MissingJSON: `[]`, IdempotencyKey: fmt.Sprintf("compound-parent-%d", time.Now().UnixNano()), Version: 1, ExpiresAt: time.Now().Add(time.Hour), ProtocolMode: agentProtocolLegacyJSON}
	if err := model.DB.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	candidate := &agentCompoundCandidate{Steps: []agentCompoundStepCandidate{
		{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{fixture.product.Name}, CheckpointNames: []string{fixture.extra.Name}}}},
		{OperationType: AgentOperationCatalogBatchChange, Operations: []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{child.Name}, CheckpointNames: []string{fixture.extra.Name}}}},
	}}
	planning, err := service.planCompoundFromEnvelope(fixture.tenant.ID, 11, "admin", parent, parent.InputText, parent.ContextJSON, model.PlatformAIConfig{Provider: defaultAIProvider, Model: defaultAIModel}, &PlatformAIService{}, candidate)
	if err != nil {
		t.Fatalf("compound planning: %v", err)
	}
	if len(planning.CompoundChildren) != 2 || len(planning.Missing) != 0 || planning.PreviewJSON == "" {
		t.Fatalf("unexpected compound planning: %+v", planning)
	}
	if err := model.Write(func(tx *gorm.DB) error {
		var locked model.AgentTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, parent.ID).Error; err != nil {
			return err
		}
		if err := service.createCompoundChildTasksTx(tx, &locked, planning); err != nil {
			return err
		}
		contextJSON, err := json.Marshal(planning.Context)
		if err != nil {
			return err
		}
		locked.OperationType = AgentOperationCompound
		locked.State = AgentTaskAwaitingConfirmation
		locked.ContextJSON = string(contextJSON)
		locked.PreviewJSON = planning.PreviewJSON
		locked.PlanHash = planning.PlanHash
		return tx.Save(&locked).Error
	}); err != nil {
		t.Fatal(err)
	}
	var childCount int64
	if err := model.DB.Model(&model.AgentTask{}).Where("tenant_id = ? AND idempotency_key LIKE ?", fixture.tenant.ID, fmt.Sprintf("agent-compound-%d-%%", parent.ID)).Count(&childCount).Error; err != nil {
		t.Fatal(err)
	}
	if childCount != 2 {
		t.Fatalf("child task count=%d, want 2", childCount)
	}
	completed, err := service.Confirm(fixture.tenant.ID, 11, "admin", parent.ID)
	if err != nil {
		t.Fatalf("confirm compound task: %v", err)
	}
	if completed.State != AgentTaskCompleted {
		t.Fatalf("compound task state=%s, want completed", completed.State)
	}
	var revised []model.Product
	if err := model.DB.Where("tenant_id = ? AND name IN ?", fixture.tenant.ID, []string{fixture.product.Name, child.Name}).Find(&revised).Error; err != nil {
		t.Fatal(err)
	}
	if len(revised) != 2 {
		t.Fatalf("revised products=%d, want 2", len(revised))
	}
}
