package service

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"
)

//go:embed skills/agent_core.md skills/agent_product_create.md skills/agent_catalog_batch_change.md
var agentSkillFiles embed.FS

const agentDomainSkillVersion = "2026-08-15.v1"

func agentDomainSkill(operationType string) (string, string, error) {
	files := []string{"skills/agent_core.md"}
	switch operationType {
	case AgentOperationTicketProductCreate:
		files = append(files, "skills/agent_product_create.md")
	case AgentOperationCatalogBatchChange:
		files = append(files, "skills/agent_catalog_batch_change.md")
	default:
		files = append(files, "skills/agent_product_create.md", "skills/agent_catalog_batch_change.md")
	}
	var sections []string
	for _, file := range files {
		content, err := agentSkillFiles.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("load agent domain skill %s: %w", file, err)
		}
		sections = append(sections, strings.TrimSpace(string(content)))
	}
	joined := strings.Join(sections, "\n\n---\n\n")
	digest := sha256.Sum256([]byte(joined))
	return joined, hex.EncodeToString(digest[:]), nil
}
