package service

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentCapabilityRegistryIsVersionedAndToolSafe(t *testing.T) {
	if err := validateAgentCapabilityRegistry(); err != nil {
		t.Fatalf("invalid agent capability registry: %v", err)
	}
	for _, operationType := range []string{AgentOperationPending, AgentOperationCatalogBatchChange, AgentOperationTicketProductCreate} {
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
		if !strings.Contains(definition.Function.Description, "模块：票种与检票规则") || !strings.Contains(definition.Function.Description, "模块范围：线上/窗口票种") {
			t.Fatalf("tool %q is missing module context: %q", definition.Function.Name, definition.Function.Description)
		}
	}
}
