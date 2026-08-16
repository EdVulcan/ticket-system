package service

import (
	"testing"
	"ticket-backend/internal/model"
)

func TestAgentBusinessAliasesResolveOnlyCurrentTenantObjects(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	service := &AgentTaskService{}
	checkpointAlias, err := service.SaveBusinessAlias(fixture.tenant.ID, 11, AgentBusinessAliasInput{Kind: "checkpoint", Alias: "8号点", CanonicalName: "Main Gate"})
	if err != nil {
		t.Fatalf("save checkpoint alias: %v", err)
	}
	if checkpointAlias.CanonicalName != "Main Gate" {
		t.Fatalf("checkpoint alias=%+v", checkpointAlias)
	}
	canonical, err := resolveAgentAlias(model.DB, fixture.tenant.ID, agentAliasCheckpoint, "8号点")
	if err != nil || canonical != "Main Gate" {
		t.Fatalf("resolve alias=%q err=%v", canonical, err)
	}
	if _, err := service.SaveBusinessAlias(fixture.tenant.ID, 11, AgentBusinessAliasInput{Kind: "checkpoint", Alias: "不存在的点", CanonicalName: "Missing Gate"}); err == nil {
		t.Fatal("alias to a missing checkpoint was accepted")
	}
	if _, err := service.SaveBusinessAlias(fixture.tenant.ID, 11, AgentBusinessAliasInput{Kind: "checkpoint", Alias: "Main Gate", CanonicalName: "Main Gate"}); err == nil {
		t.Fatal("alias equal to canonical checkpoint name was accepted")
	}
	if err := service.DeleteBusinessAlias(fixture.tenant.ID, checkpointAlias.ID); err != nil {
		t.Fatalf("delete alias: %v", err)
	}
	if resolved, err := resolveAgentAlias(model.DB, fixture.tenant.ID, agentAliasCheckpoint, "8号点"); err != nil || resolved != "8号点" {
		t.Fatalf("deleted alias still resolved: %q err=%v", resolved, err)
	}
}

func TestAgentBusinessAliasCanonicalizesPlannerTargets(t *testing.T) {
	fixture := seedCatalogBatchFixture(t)
	service := &AgentTaskService{}
	if _, err := service.SaveBusinessAlias(fixture.tenant.ID, 11, AgentBusinessAliasInput{Kind: "product", Alias: "成人票简称", CanonicalName: "Adult Ticket"}); err != nil {
		t.Fatalf("save product alias: %v", err)
	}
	if _, err := service.SaveBusinessAlias(fixture.tenant.ID, 11, AgentBusinessAliasInput{Kind: "checkpoint", Alias: "入口简称", CanonicalName: "Main Gate"}); err != nil {
		t.Fatalf("save checkpoint alias: %v", err)
	}
	operations, err := canonicalizeAgentCatalogAliases(model.DB, fixture.tenant.ID, []CatalogRuleOperation{{Kind: CatalogBatchOpAddCheckpoint, ProductNames: []string{"成人票简称"}, CheckpointNames: []string{"入口简称"}}})
	if err != nil {
		t.Fatalf("canonicalize catalog aliases: %v", err)
	}
	if operations[0].ProductNames[0] != "Adult Ticket" || operations[0].CheckpointNames[0] != "Main Gate" {
		t.Fatalf("canonicalized operations=%+v", operations)
	}
}
