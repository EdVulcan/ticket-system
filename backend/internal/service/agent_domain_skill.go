package service

import (
	"embed"
)

//go:embed skills/agent_system.md skills/agent_core.md skills/agent_product_create.md skills/agent_catalog_batch_change.md skills/agent_orders_read.md skills/agent_inventory_read.md skills/agent_reports_read.md
var agentSkillFiles embed.FS

const agentDomainSkillVersion = "2026-08-16.v2"

func agentDomainSkill(operationType string) (string, string, error) {
	pack, err := agentKnowledgePackForOperation(operationType)
	if err != nil {
		return "", "", err
	}
	return pack.Content, pack.Hash, nil
}
