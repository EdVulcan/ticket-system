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
			"supplier_tenant_id": r.SupplierTenantID,
			"supplier_name":      r.SupplierTenant.Name,
			"supplier_code":      r.SupplierTenant.SystemCode,
			"status":             r.Status,
			"agent_level":        r.AgentLevel,
			"balance":            account.Balance,
		})
	}
	return result, nil
}

// ListMyAgents 获取申请我的分销商列表 (供应商视角)
func (s *DistributionService) ListMyAgents(supplierTenantID uint) ([]map[string]interface{}, error) {
	var rels []model.DistributorRelationship
	// Preload AgentTenant to get name/contact
	if err := model.DB.Preload("AgentTenant").Where("supplier_tenant_id = ?", supplierTenantID).Order("created_at desc").Find(&rels).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0)
	for _, r := range rels {
		result = append(result, map[string]interface{}{
			"id":            r.ID,
			"agent_name":    r.AgentTenant.Name,
			"agent_code":    r.AgentTenant.SystemCode,
			"agent_contact": r.AgentTenant.Contact,
			"status":        r.Status,
			"agent_level":   r.AgentLevel,
			"created_at":    r.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return result, nil
}

// AuditAgent 审核分销商申请
func (s *DistributionService) AuditAgent(supplierTenantID uint, relationshipID uint, status string) error {
	var rel model.DistributorRelationship
	if err := model.DB.Where("id = ? AND supplier_tenant_id = ?", relationshipID, supplierTenantID).First(&rel).Error; err != nil {
		return errors.New("申请记录不存在")
	}

	if status != "active" && status != "rejected" {
		return errors.New("无效的审核状态")
	}

	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Update Status
		rel.Status = status
		if err := tx.Save(&rel).Error; err != nil {
			return err
		}

		// 2. If Active, Create Capital Account if not exists
		if status == "active" {
			var count int64
			tx.Model(&model.CapitalAccount{}).Where("owner_tenant_id = ? AND manager_tenant_id = ?", rel.AgentTenantID, rel.SupplierTenantID).Count(&count)
			if count == 0 {
				account := model.CapitalAccount{
					OwnerTenantID:   rel.AgentTenantID,
					ManagerTenantID: rel.SupplierTenantID,
					Balance:         0,
					CreditLine:      0,
					Status:          "active",
				}
				if err := tx.Create(&account).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ListDistributableProducts 获取供应商可分销的产品
func (s *DistributionService) ListDistributableProducts(supplierTenantID uint) ([]model.Product, error) {
	var products []model.Product
	err := model.DB.Where("tenant_id = ? AND is_distributable = ? AND status = ?", supplierTenantID, true, "online").Find(&products).Error
	return products, err
}

// ImportProduct 分销商导入产品 (建立映射)
func (s *DistributionService) ImportProduct(agentTenantID uint, sourceProductID uint, name string, price float64, productType string) error {
	// 1. Check Source Product
	var source model.Product
	if err := model.DB.Preload("Rule").First(&source, sourceProductID).Error; err != nil {
		return errors.New("源产品不存在")
	}
	if !source.IsDistributable {
		return errors.New("该产品未开启分销")
	}

	// 2. Clone Operation (Transaction)
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// Clone Rule (Create a local copy so Agent can view/edit localized validity if needed,
		// though ideally it should sync. For now, simple copy).
		newRule := source.Rule
		newRule.ID = 0
		newRule.TenantID = agentTenantID
		newRule.Name = name + " (分销规则)"
		// Clear unneeded fields or relations if deep copy needed...
		// For simplicity, we just create new rule record with same values.
		if err := tx.Create(&newRule).Error; err != nil {
			return err
		}

		// Create Agent Product
		newProduct := model.Product{
			Name:            name,
			Price:           price,
			TenantID:        agentTenantID,
			RuleID:          newRule.ID, // Link to new local rule
			Type:            productType,
			Status:          "online",
			SettlementPrice: source.SettlementPrice, // Cost for Agent
			IsDistributable: false,                  // Agent can't re-distribute by default
			SourceProductID: source.ID,
			SourceTenantID:  source.TenantID,

			// Copy other configs
			ValidityType:     source.ValidityType,
			ValidityDays:     source.ValidityDays,
			StockType:        source.StockType, // Note: Stock is virtual/shared
			DailyStock:       source.DailyStock,
			RealNameRequired: source.RealNameRequired,
		}

		return tx.Create(&newProduct).Error
	})
}

// RechargeAgent 供应商给分销商充值
func (s *DistributionService) RechargeAgent(supplierTenantID uint, agentTenantID uint, amount float64) error {
	if amount <= 0 {
		return errors.New("充值金额必须大于0")
	}

	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Get Capital Account
		var account model.CapitalAccount
		if err := tx.Where("owner_tenant_id = ? AND manager_tenant_id = ?", agentTenantID, supplierTenantID).First(&account).Error; err != nil {
			return errors.New("分销资金账户不存在")
		}

		// 2. Update Balance
		account.Balance += amount
		if err := tx.Save(&account).Error; err != nil {
			return err
		}

		// 3. Record Transaction
		trans := model.TransactionRecord{
			AccountID:      account.ID,
			Type:           "deposit",
			Amount:         amount,
			BalanceAfter:   account.Balance,
			RelatedOrderNo: "", // Manual recharge has no order
			Memo:           "供应商人工充值",
			OperatorID:     0, // System/Supplier Admin
		}
		if err := tx.Create(&trans).Error; err != nil {
			return err
		}

		return nil
	})
}
