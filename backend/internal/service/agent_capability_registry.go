package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	agentModuleSystem    = "system"
	agentModuleCatalog   = "catalog"
	agentModuleOrders    = "orders"
	agentModuleInventory = "inventory"
	agentModuleReports   = "reports"
)

type agentModuleManifest struct {
	ID             string
	Label          string
	Summary        string
	KnowledgeFiles []string
	OperationTypes []string
	ToolNames      []string
}

type agentKnowledgePack struct {
	ID      string
	Version string
	Content string
	Hash    string
}

var agentModuleManifests = []agentModuleManifest{
	{
		ID:             agentModuleSystem,
		Label:          "系统能力",
		Summary:        "平台实体关系、操作等级和 AI 安全边界",
		KnowledgeFiles: []string{"skills/agent_system.md", "skills/agent_core.md", "skills/agent_product_create.md", "skills/agent_catalog_batch_change.md"},
		OperationTypes: []string{AgentOperationPending},
	},
	{
		ID:             agentModuleCatalog,
		Label:          "票种与检票规则",
		Summary:        "线上/窗口票种、产品版本、检票点和规则预览",
		KnowledgeFiles: []string{"skills/agent_system.md", "skills/agent_core.md", "skills/agent_product_create.md", "skills/agent_product_update.md", "skills/agent_product_batch_update.md", "skills/agent_catalog_batch_change.md"},
		OperationTypes: []string{AgentOperationCatalogBatchChange, AgentOperationTicketProductCreate, AgentOperationTicketProductUpdate, AgentOperationTicketProductBatchUpdate, AgentOperationCompound},
		ToolNames: []string{
			"search_scenic_areas",
			"search_checkpoints",
			"search_ticket_products",
			"get_ticket_product_rules",
			"prepare_ticket_product_create",
			"prepare_ticket_product_update",
			"prepare_ticket_product_batch_update",
			"prepare_compound_preview",
			"prepare_catalog_rule_change",
		},
	},
	{
		ID:             agentModuleOrders,
		Label:          "订单查询",
		Summary:        "销售订单、订单明细和订单状态的只读事实",
		KnowledgeFiles: []string{"skills/agent_orders_read.md"},
		ToolNames:      []string{"search_orders"},
	},
	{
		ID:             agentModuleInventory,
		Label:          "票种库存",
		Summary:        "线上票种按日期和时段的库存容量与已售事实",
		KnowledgeFiles: []string{"skills/agent_inventory_read.md"},
		ToolNames:      []string{"query_ticket_inventory"},
	},
	{
		ID:             agentModuleReports,
		Label:          "经营报表",
		Summary:        "按收款期、首次有效核销日期生成的服务器报表事实",
		KnowledgeFiles: []string{"skills/agent_reports_read.md"},
		ToolNames:      []string{"query_sales_summary", "query_verification_summary"},
	},
}

func agentModuleManifestForOperation(operationType string) (agentModuleManifest, error) {
	operationType = strings.TrimSpace(operationType)
	for _, manifest := range agentModuleManifests {
		for _, supported := range manifest.OperationTypes {
			if supported == operationType {
				return manifest, nil
			}
		}
	}
	if operationType == "" {
		return agentModuleManifests[0], nil
	}
	return agentModuleManifest{}, fmt.Errorf("no AI module manifest for operation %q", operationType)
}

func agentModuleManifestForTool(toolName string) (agentModuleManifest, bool) {
	toolName = strings.TrimSpace(toolName)
	for _, manifest := range agentModuleManifests {
		for _, registered := range manifest.ToolNames {
			if registered == toolName {
				return manifest, true
			}
		}
	}
	return agentModuleManifest{}, false
}

func agentKnowledgePackForOperation(operationType string) (agentKnowledgePack, error) {
	manifest, err := agentModuleManifestForOperation(operationType)
	if err != nil {
		return agentKnowledgePack{}, err
	}
	sections := make([]string, 0, len(manifest.KnowledgeFiles))
	for _, file := range manifest.KnowledgeFiles {
		content, readErr := agentSkillFiles.ReadFile(file)
		if readErr != nil {
			return agentKnowledgePack{}, fmt.Errorf("load agent knowledge pack %s: %w", file, readErr)
		}
		sections = append(sections, strings.TrimSpace(string(content)))
	}
	joined := strings.Join(sections, "\n\n---\n\n")
	fingerprint, fingerprintErr := agentKnowledgeRegistryFingerprint()
	if fingerprintErr != nil {
		return agentKnowledgePack{}, fingerprintErr
	}
	digest := sha256.Sum256([]byte(joined + "\n\nregistry_fingerprint:" + fingerprint))
	return agentKnowledgePack{
		ID:      manifest.ID,
		Version: agentDomainSkillVersion,
		Content: joined,
		Hash:    hex.EncodeToString(digest[:]),
	}, nil
}

func agentKnowledgePackForModule(moduleID string) (agentKnowledgePack, error) {
	moduleID = strings.TrimSpace(moduleID)
	for _, manifest := range agentModuleManifests {
		if manifest.ID != moduleID {
			continue
		}
		sections := make([]string, 0, len(manifest.KnowledgeFiles))
		for _, file := range manifest.KnowledgeFiles {
			content, readErr := agentSkillFiles.ReadFile(file)
			if readErr != nil {
				return agentKnowledgePack{}, fmt.Errorf("load agent knowledge pack %s: %w", file, readErr)
			}
			sections = append(sections, strings.TrimSpace(string(content)))
		}
		joined := strings.Join(sections, "\n\n---\n\n")
		fingerprint, fingerprintErr := agentKnowledgeRegistryFingerprint()
		if fingerprintErr != nil {
			return agentKnowledgePack{}, fingerprintErr
		}
		digest := sha256.Sum256([]byte(joined + "\n\nregistry_fingerprint:" + fingerprint))
		return agentKnowledgePack{ID: manifest.ID, Version: agentDomainSkillVersion, Content: joined, Hash: hex.EncodeToString(digest[:])}, nil
	}
	return agentKnowledgePack{}, fmt.Errorf("no AI module manifest for %q", moduleID)
}

// agentKnowledgeRegistryFingerprint makes a frozen task sensitive to a
// change in any registered module, including a read-only module loaded only
// for the provider prompt. It prevents continuation from silently mixing old
// task context with newly published module knowledge.
func agentKnowledgeRegistryFingerprint() (string, error) {
	hash := sha256.New()
	for _, manifest := range agentModuleManifests {
		if _, err := hash.Write([]byte(manifest.ID + "\x00" + agentDomainSkillVersion + "\x00")); err != nil {
			return "", err
		}
		for _, file := range manifest.KnowledgeFiles {
			content, err := agentSkillFiles.ReadFile(file)
			if err != nil {
				return "", fmt.Errorf("load agent knowledge pack %s: %w", file, err)
			}
			if _, err := hash.Write([]byte(file + "\x00")); err != nil {
				return "", err
			}
			if _, err := hash.Write(content); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// agentKnowledgePackForTask freezes continuation behaviour. A task created
// with an older pack must not silently switch rules after a deployment; the
// operator can start a new task against the new pack instead.
func agentKnowledgePackForTask(operationType string, context agentTaskContext) (agentKnowledgePack, error) {
	pack, err := agentKnowledgePackForOperation(operationType)
	if err != nil {
		return agentKnowledgePack{}, err
	}
	if strings.TrimSpace(context.OperationType) != strings.TrimSpace(operationType) || strings.TrimSpace(context.SkillHash) == "" {
		return pack, nil
	}
	if strings.TrimSpace(context.KnowledgePackID) != "" && context.KnowledgePackID != pack.ID {
		return agentKnowledgePack{}, agentConflict("当前任务使用的 AI 知识包已不适用于此业务，请新建任务")
	}
	if context.SkillHash != pack.Hash {
		return agentKnowledgePack{}, agentConflict("系统知识已更新，当前任务已冻结；请新建任务重新生成计划")
	}
	return pack, nil
}

func agentKnowledgePackForContext(operationType, contextJSON string) (agentKnowledgePack, error) {
	var context agentTaskContext
	if strings.TrimSpace(contextJSON) != "" {
		if err := json.Unmarshal([]byte(contextJSON), &context); err != nil {
			return agentKnowledgePack{}, agentInvalid("agent task context is invalid")
		}
	}
	return agentKnowledgePackForTask(operationType, context)
}

func validateAgentCapabilityRegistry() error {
	seenModules := make(map[string]struct{}, len(agentModuleManifests))
	seenTools := make(map[string]struct{})
	for _, manifest := range agentModuleManifests {
		if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Label) == "" {
			return fmt.Errorf("AI module manifest must have an id and label")
		}
		if _, exists := seenModules[manifest.ID]; exists {
			return fmt.Errorf("duplicate AI module manifest %q", manifest.ID)
		}
		seenModules[manifest.ID] = struct{}{}
		if len(manifest.KnowledgeFiles) == 0 || (len(manifest.OperationTypes) == 0 && len(manifest.ToolNames) == 0) {
			return fmt.Errorf("AI module manifest %q is incomplete", manifest.ID)
		}
		for _, operationType := range manifest.OperationTypes {
			resolved, err := agentModuleManifestForOperation(operationType)
			if err != nil || resolved.ID != manifest.ID {
				return fmt.Errorf("operation %q is not owned by module %q", operationType, manifest.ID)
			}
		}
	}
	for _, spec := range agentToolSpecs {
		if _, exists := seenTools[spec.Name]; exists {
			return fmt.Errorf("duplicate AI tool %q", spec.Name)
		}
		seenTools[spec.Name] = struct{}{}
		manifest, ok := agentModuleManifestForTool(spec.Name)
		if !ok || manifest.ID != spec.ModuleID {
			return fmt.Errorf("tool %q is not mapped to its declared AI module", spec.Name)
		}
		if spec.ReadOnly == spec.PreviewOnly {
			return fmt.Errorf("tool %q must be exactly read-only or preview-only", spec.Name)
		}
		if spec.ActionKind != "query" && spec.ActionKind != "preview" {
			return fmt.Errorf("tool %q has unsupported action kind %q", spec.Name, spec.ActionKind)
		}
		if spec.ReadOnly && spec.ActionKind != "query" {
			return fmt.Errorf("read-only tool %q must be a query action", spec.Name)
		}
		if spec.PreviewOnly && spec.ActionKind != "preview" {
			return fmt.Errorf("preview tool %q must be a preview action", spec.Name)
		}
		if spec.PreviewOnly && !spec.RequiresConfirmation {
			return fmt.Errorf("preview tool %q must require confirmation", spec.Name)
		}
		if spec.ReadOnly && spec.RequiresConfirmation {
			return fmt.Errorf("read-only tool %q cannot require confirmation", spec.Name)
		}
		if spec.Capability != "" && len(spec.CapabilityAny) > 0 {
			return fmt.Errorf("tool %q cannot declare both one capability and any-capability access", spec.Name)
		}
		if spec.ReadOnly {
			if _, ok := agentToolHandlerFor(spec.Name); !ok {
				return fmt.Errorf("read-only tool %q has no handler adapter", spec.Name)
			}
		}
	}
	for _, manifest := range agentModuleManifests {
		for _, toolName := range manifest.ToolNames {
			if _, exists := seenTools[toolName]; !exists {
				return fmt.Errorf("AI module %q references missing tool %q", manifest.ID, toolName)
			}
		}
	}
	return nil
}
