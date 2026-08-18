package service

import (
	"embed"
)

//go:embed skills/agent_system.md skills/agent_core.md skills/agent_product_create.md skills/agent_product_update.md skills/agent_product_batch_update.md skills/agent_catalog_batch_change.md skills/agent_orders_read.md skills/agent_inventory_read.md skills/agent_reports_read.md skills/agent_teams_read.md skills/agent_distribution_read.md skills/agent_hotel.md
var agentSkillFiles embed.FS

const agentDomainSkillVersion = "2026-08-18.v8"

func agentDomainSkill(operationType string) (string, string, error) {
	pack, err := agentKnowledgePackForOperation(operationType)
	if err != nil {
		return "", "", err
	}
	return pack.Content, pack.Hash, nil
}
