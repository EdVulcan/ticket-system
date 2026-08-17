package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAgentCapabilityRegistryIsVersionedAndToolSafe(t *testing.T) {
	if err := validateAgentCapabilityRegistry(); err != nil {
		t.Fatalf("invalid agent capability registry: %v", err)
	}
	for _, operationType := range []string{AgentOperationPending, AgentOperationCatalogBatchChange, AgentOperationTicketProductCreate, AgentOperationTicketProductUpdate, AgentOperationTicketProductBatchUpdate, AgentOperationCompound} {
		pack, err := agentKnowledgePackForOperation(operationType)
		if err != nil {
			t.Fatalf("load knowledge pack for %q: %v", operationType, err)
		}
		if pack.ID == "" || pack.Version == "" || pack.Hash == "" || pack.Content == "" {
			t.Fatalf("incomplete knowledge pack for %q: %+v", operationType, pack)
		}
		if !strings.Contains(pack.Content, "## Module map") || !strings.Contains(pack.Content, "## Operation levels") {
			t.Fatalf("knowledge pack for %q does not contain the system contract", operationType)
		}
	}
	for _, moduleID := range []string{agentModuleOrders, agentModuleInventory, agentModuleReports, agentModuleDistribution, agentModuleTeams} {
		pack, err := agentKnowledgePackForModule(moduleID)
		if err != nil {
			t.Fatalf("load read-only module pack %q: %v", moduleID, err)
		}
		if pack.ID != moduleID || pack.Version == "" || pack.Hash == "" || pack.Content == "" {
			t.Fatalf("incomplete read-only module pack %q: %+v", moduleID, pack)
		}
	}
	for _, spec := range agentToolSpecs {
		var schema interface{}
		if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
			t.Fatalf("tool %q has invalid JSON schema: %v", spec.Name, err)
		}
	}
}

func TestAgentKnowledgePackRejectsSilentContinuationDrift(t *testing.T) {
	pack, err := agentKnowledgePackForOperation(AgentOperationCatalogBatchChange)
	if err != nil {
		t.Fatal(err)
	}
	context := agentTaskContext{OperationType: AgentOperationCatalogBatchChange, KnowledgePackID: pack.ID, SkillHash: pack.Hash}
	if _, err := agentKnowledgePackForTask(AgentOperationCatalogBatchChange, context); err != nil {
		t.Fatalf("current pack should continue: %v", err)
	}
	context.SkillHash = strings.Repeat("0", len(pack.Hash))
	_, err = agentKnowledgePackForTask(AgentOperationCatalogBatchChange, context)
	var taskErr *AgentTaskError
	if !errors.As(err, &taskErr) || taskErr.HTTPStatus != 409 {
		t.Fatalf("stale pack should require a new task, got %v", err)
	}
}

func TestAgentToolDefinitionsExposeModuleContext(t *testing.T) {
	definitions := agentToolDefinitions(agentToolSpecs)
	if len(definitions) != len(agentToolSpecs) {
		t.Fatalf("tool definitions=%d, want %d", len(definitions), len(agentToolSpecs))
	}
	for _, definition := range definitions {
		if !strings.Contains(definition.Function.Description, "模块：") || !strings.Contains(definition.Function.Description, "模块范围：") {
			t.Fatalf("tool %q is missing module context: %q", definition.Function.Name, definition.Function.Description)
		}
	}
	for _, name := range []string{"prepare_catalog_rule_change", "prepare_compound_preview", "prepare_ticket_product_update", "prepare_ticket_product_batch_update"} {
		var definition AIToolDefinition
		for _, candidate := range definitions {
			if candidate.Function.Name == name {
				definition = candidate
				break
			}
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatalf("tool %q has invalid generated schema: %v", name, err)
		}
		encoded, _ := json.Marshal(schema)
		if !strings.Contains(string(encoded), "target_scope") {
			t.Fatalf("tool %q does not expose target_scope in its schema: %s", name, string(encoded))
		}
	}
}
