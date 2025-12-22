package service

import (
	"errors"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type DistributionService struct{}

// GetSupplierByCode 根据编号查找供应商信息 (仅用于申请前预览)
func (s *DistributionService) GetSupplierByCode(code string) (*model.Tenant, error) {
	var tenant model.Tenant
	if err := model.DB.Where("system_code = ?", code).First(&tenant).Error; err != nil {
		return nil, errors.New("未找到该编号的供应商")
	}
	return &tenant, nil
}

// ApplyAgent 申请成为分销商
func (s *DistributionService) ApplyAgent(agentTenantID uint, supplierSystemCode string) error {
	// 1. Find Supplier by Code
	var supplier model.Tenant
	if err := model.DB.Where("system_code = ?", supplierSystemCode).First(&supplier).Error; err != nil {
		return errors.New("供应商系统编号不存在")
	}

	if supplier.ID == agentTenantID {
		return errors.New("不能申请代理自己")
	}

	// 2. Check if relationship exists
	var rel model.DistributorRelationship
	err := model.DB.Where("agent_tenant_id = ? AND supplier_tenant_id = ?", agentTenantID, supplier.ID).First(&rel).Error
	if err == nil {
		if rel.Status == "pending" {
			return errors.New("申请审核中，请勿重复提交")
		}
		if rel.Status == "active" {
			return errors.New("已经是该供应商的分销商")
		}
		// If rejected, allow re-apply (update status)
		rel.Status = "pending"
		return model.DB.Save(&rel).Error
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// 3. Create new relationship
	newRel := model.DistributorRelationship{
		AgentTenantID:    agentTenantID,
		SupplierTenantID: supplier.ID,
		Status:           "pending",
		AgentLevel:       "standard",
	}
	return model.DB.Create(&newRel).Error
}

// ListSuppliers 获取我的供应商列表
func (s *DistributionService) ListSuppliers(agentTenantID uint) ([]map[string]interface{}, error) {
	var rels []model.DistributorRelationship
	if err := model.DB.Preload("SupplierTenant").Where("agent_tenant_id = ?", agentTenantID).Find(&rels).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0)
	for _, r := range rels {
		// Find Capital Account for this pair
		var account model.CapitalAccount
		model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", agentTenantID, r.SupplierTenantID).First(&account)

		result = append(result, map[string]interface{}{
			"supplier_name": r.SupplierTenant.Name,
			"supplier_code": r.SupplierTenant.SystemCode,
			"status":        r.Status,
			"agent_level":   r.AgentLevel,
			"balance":       account.Balance,
		})
	}
	return result, nil
}
