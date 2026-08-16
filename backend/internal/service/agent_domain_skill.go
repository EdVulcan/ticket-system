package service

import (
	"embed"
)

//go:embed skills/agent_system.md skills/agent_core.md skills/agent_product_create.md skills/agent_catalog_batch_change.md
var agentSkillFiles embed.FS

const agentDomainSkillVersion = "2026-08-16.v1"

func agentDomainSkill(operationType string) (string, string, error) {
	pack, err := agentKnowledgePackForOperation(operationType)
	if err != nil {
		return "", "", err
	}
	return pack.Content, pack.Hash, nil
}
