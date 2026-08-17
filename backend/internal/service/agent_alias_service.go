package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

const (
	agentAliasScenicArea = "scenic_area"
	agentAliasCheckpoint = "checkpoint"
	agentAliasProduct    = "product"
)

type AgentBusinessAliasInput struct {
	Kind          string `json:"kind"`
	Alias         string `json:"alias"`
	CanonicalName string `json:"canonical_name"`
}

type AgentBusinessAliasView struct {
	ID            uint   `json:"id"`
	Kind          string `json:"kind"`
	Alias         string `json:"alias"`
	CanonicalName string `json:"canonical_name"`
}

func normalizeAgentAliasKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "area", "scenic", "scenic_area", "景区":
		return agentAliasScenicArea
	case "checkpoint", "check_point", "检票点":
		return agentAliasCheckpoint
	case "product", "ticket", "票种":
		return agentAliasProduct
	default:
		return ""
	}
}

func agentAliasView(row model.AgentBusinessAlias) AgentBusinessAliasView {
	return AgentBusinessAliasView{ID: row.ID, Kind: row.Kind, Alias: row.Alias, CanonicalName: row.CanonicalName}
}

func (s *AgentTaskService) ListBusinessAliases(tenantID uint) ([]AgentBusinessAliasView, error) {
	if tenantID == 0 {
		return nil, agentInvalid("tenant is required")
	}
	var rows []model.AgentBusinessAlias
	if err := model.DB.Where("tenant_id = ?", tenantID).Order("kind ASC, alias ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentBusinessAliasView, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentAliasView(row))
	}
	return result, nil
}

func (s *AgentTaskService) SaveBusinessAlias(tenantID, actorID uint, input AgentBusinessAliasInput) (*AgentBusinessAliasView, error) {
	kind := normalizeAgentAliasKind(input.Kind)
	alias := strings.TrimSpace(input.Alias)
	canonical := strings.TrimSpace(input.CanonicalName)
	if tenantID == 0 || actorID == 0 {
		return nil, agentInvalid("tenant and actor are required")
	}
	if kind == "" {
		return nil, agentInvalid("alias kind must be scenic_area, checkpoint, or product")
	}
	if alias == "" || canonical == "" || len([]rune(alias)) > 100 || len([]rune(canonical)) > 100 {
		return nil, agentInvalid("alias and canonical name are required and cannot exceed 100 characters")
	}
	if strings.EqualFold(alias, canonical) {
		return nil, agentInvalid("alias must be different from the canonical business name")
	}
	var result model.AgentBusinessAlias
	err := model.Write(func(tx *gorm.DB) error {
		count, err := agentAliasTargetCount(tx, tenantID, kind, canonical)
		if err != nil {
			return err
		}
		if count != 1 {
			return agentInvalid("alias target must resolve to exactly one current business object")
		}
		canonicalCount, err := agentAliasTargetCount(tx, tenantID, kind, alias)
		if err != nil {
			return err
		}
		if canonicalCount > 0 {
			return agentInvalid("alias conflicts with an existing canonical business name")
		}
		var existing model.AgentBusinessAlias
		query := tx.Where("tenant_id = ? AND kind = ? AND LOWER(alias) = LOWER(?)", tenantID, kind, alias)
		if err := query.First(&existing).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		} else if err == nil {
			existing.CanonicalName = canonical
			existing.UpdatedBy = actorID
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			result = existing
			return nil
		}
		result = model.AgentBusinessAlias{TenantID: tenantID, Kind: kind, Alias: alias, CanonicalName: canonical, UpdatedBy: actorID}
		return tx.Create(&result).Error
	})
	if err != nil {
		return nil, err
	}
	view := agentAliasView(result)
	return &view, nil
}

func (s *AgentTaskService) DeleteBusinessAlias(tenantID, aliasID uint) error {
	if tenantID == 0 || aliasID == 0 {
		return agentInvalid("tenant and alias are required")
	}
	result := model.DB.Where("id = ? AND tenant_id = ?", aliasID, tenantID).Delete(&model.AgentBusinessAlias{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return agentNotFound("business alias not found")
	}
	return nil
}

func agentAliasTargetCount(tx *gorm.DB, tenantID uint, kind, name string) (int64, error) {
	name = strings.TrimSpace(name)
	switch kind {
	case agentAliasScenicArea:
		var count int64
		err := tx.Model(&model.ScenicArea{}).Where("tenant_id = ? AND status = 'active' AND name = ?", tenantID, name).Count(&count).Error
		return count, err
	case agentAliasCheckpoint:
		var count int64
		err := tx.Model(&model.CheckPoint{}).Where("tenant_id = ? AND name = ?", tenantID, name).Count(&count).Error
		return count, err
	case agentAliasProduct:
		var products []model.Product
		if err := tx.Where("tenant_id = ? AND name = ? AND deleted_at IS NULL AND source_product_id = 0 AND source_tenant_id = 0", tenantID, name).Find(&products).Error; err != nil {
			return 0, err
		}
		count := int64(0)
		for index := range products {
			if !isDistributedListing(&products[index]) {
				count++
			}
		}
		return count, nil
	default:
		return 0, fmt.Errorf("unsupported AI alias kind %q", kind)
	}
}

func resolveAgentAlias(tx *gorm.DB, tenantID uint, kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || tx == nil || tenantID == 0 {
		return value, nil
	}
	kind = normalizeAgentAliasKind(kind)
	if kind == "" {
		return value, nil
	}
	var alias model.AgentBusinessAlias
	err := tx.Where("tenant_id = ? AND kind = ? AND LOWER(alias) = LOWER(?)", tenantID, kind, value).First(&alias).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return value, nil
	}
	if err != nil {
		return "", err
	}
	count, err := agentAliasTargetCount(tx, tenantID, kind, alias.CanonicalName)
	if err != nil {
		return "", err
	}
	if count != 1 {
		return "", agentConflict(fmt.Sprintf("业务别名“%s”对应的目标已变化，请更新别名配置", value))
	}
	return alias.CanonicalName, nil
}

func canonicalizeAgentCatalogAliases(tx *gorm.DB, tenantID uint, operations []CatalogRuleOperation) ([]CatalogRuleOperation, error) {
	result := append([]CatalogRuleOperation(nil), operations...)
	for index := range result {
		for nameIndex, name := range result[index].ProductNames {
			canonical, err := resolveAgentAlias(tx, tenantID, agentAliasProduct, name)
			if err != nil {
				return nil, err
			}
			result[index].ProductNames[nameIndex] = canonical
		}
		for nameIndex, name := range result[index].CheckpointNames {
			canonical, err := resolveAgentAlias(tx, tenantID, agentAliasCheckpoint, name)
			if err != nil {
				return nil, err
			}
			result[index].CheckpointNames[nameIndex] = canonical
		}
		if result[index].TargetScope != nil {
			copyScope := cloneAgentTargetScope(*result[index].TargetScope)
			for nameIndex, name := range copyScope.NameTerms {
				canonical, err := resolveAgentAlias(tx, tenantID, agentAliasProduct, name)
				if err != nil {
					return nil, err
				}
				copyScope.NameTerms[nameIndex] = canonical
			}
			for nameIndex, name := range copyScope.ScenicAreaNames {
				canonical, err := resolveAgentAlias(tx, tenantID, agentAliasScenicArea, name)
				if err != nil {
					return nil, err
				}
				copyScope.ScenicAreaNames[nameIndex] = canonical
			}
			result[index].TargetScope = &copyScope
		}
	}
	return result, nil
}

func canonicalizeAgentProductCandidateAliases(tx *gorm.DB, tenantID uint, candidate *agentProductCandidate) (*agentProductCandidate, error) {
	if candidate == nil {
		return nil, nil
	}
	result := *candidate
	var err error
	result.ScenicAreaName, err = resolveAgentAlias(tx, tenantID, agentAliasScenicArea, result.ScenicAreaName)
	if err != nil {
		return nil, err
	}
	result.Groups = append([]agentRuleDraftGroup(nil), candidate.Groups...)
	for groupIndex := range result.Groups {
		result.Groups[groupIndex].Items = append([]agentRuleDraftItem(nil), candidate.Groups[groupIndex].Items...)
		for itemIndex := range result.Groups[groupIndex].Items {
			result.Groups[groupIndex].Items[itemIndex].CheckpointName, err = resolveAgentAlias(tx, tenantID, agentAliasCheckpoint, result.Groups[groupIndex].Items[itemIndex].CheckpointName)
			if err != nil {
				return nil, err
			}
		}
	}
	return &result, nil
}

func canonicalizeAgentProductUpdateCandidateAliases(tx *gorm.DB, tenantID uint, candidate *agentProductUpdateCandidate) (*agentProductUpdateCandidate, error) {
	if candidate == nil {
		return nil, nil
	}
	result := *candidate
	var err error
	result.ProductName, err = resolveAgentAlias(tx, tenantID, agentAliasProduct, result.ProductName)
	if err != nil {
		return nil, err
	}
	if candidate.TargetScope != nil {
		copyScope := cloneAgentTargetScope(*candidate.TargetScope)
		for index, name := range copyScope.NameTerms {
			canonical, aliasErr := resolveAgentAlias(tx, tenantID, agentAliasProduct, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			copyScope.NameTerms[index] = canonical
		}
		for index, name := range copyScope.ScenicAreaNames {
			canonical, aliasErr := resolveAgentAlias(tx, tenantID, agentAliasScenicArea, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			copyScope.ScenicAreaNames[index] = canonical
		}
		result.TargetScope = &copyScope
	}
	return &result, nil
}

func canonicalizeAgentProductBatchUpdateCandidateAliases(tx *gorm.DB, tenantID uint, candidate *agentProductBatchUpdateCandidate) (*agentProductBatchUpdateCandidate, error) {
	if candidate == nil {
		return nil, nil
	}
	result := *candidate
	result.ProductNames = append([]string(nil), candidate.ProductNames...)
	for index, name := range result.ProductNames {
		canonical, err := resolveAgentAlias(tx, tenantID, agentAliasProduct, name)
		if err != nil {
			return nil, err
		}
		result.ProductNames[index] = canonical
	}
	if candidate.TargetScope != nil {
		copyScope := cloneAgentTargetScope(*candidate.TargetScope)
		for index, name := range copyScope.NameTerms {
			canonical, aliasErr := resolveAgentAlias(tx, tenantID, agentAliasProduct, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			copyScope.NameTerms[index] = canonical
		}
		for index, name := range copyScope.ScenicAreaNames {
			canonical, aliasErr := resolveAgentAlias(tx, tenantID, agentAliasScenicArea, name)
			if aliasErr != nil {
				return nil, aliasErr
			}
			copyScope.ScenicAreaNames[index] = canonical
		}
		result.TargetScope = &copyScope
	}
	return &result, nil
}
