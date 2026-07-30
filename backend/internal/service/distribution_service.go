package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type DistributionService struct{}

func (s *DistributionService) GetSupplierByCode(code string) (*model.Tenant, error) {
	var tenant model.Tenant
	if err := model.DB.Where("system_code = ?", strings.TrimSpace(code)).First(&tenant).Error; err != nil {
		return nil, errors.New("supplier not found")
	}
	return &tenant, nil
}

func (s *DistributionService) ApplyAgent(agentTenantID uint, supplierSystemCode string) error {
	return model.Write(func(tx *gorm.DB) error {
		var supplier model.Tenant
		if err := tx.Where("system_code = ?", strings.TrimSpace(supplierSystemCode)).First(&supplier).Error; err != nil {
			return errors.New("supplier system code does not exist")
		}
		if supplier.ID == agentTenantID {
			return errors.New("a tenant cannot distribute its own products")
		}

		var relationship model.DistributorRelationship
		err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ?", agentTenantID, supplier.ID).First(&relationship).Error
		if err == nil {
			switch relationship.Status {
			case "pending":
				return errors.New("distribution application is already pending")
			case "active":
				return errors.New("distribution relationship is already active")
			default:
				return tx.Model(&relationship).Update("status", "pending").Error
			}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&model.DistributorRelationship{
			AgentTenantID: agentTenantID, SupplierTenantID: supplier.ID,
			Status: "pending", AgentLevel: "standard",
		}).Error
	})
}

func (s *DistributionService) ListSuppliers(agentTenantID uint) ([]map[string]interface{}, error) {
	var relationships []model.DistributorRelationship
	if err := model.DB.Preload("SupplierTenant").Where("agent_tenant_id = ?", agentTenantID).Find(&relationships).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(relationships))
	for _, relationship := range relationships {
		var account model.CapitalAccount
		err := model.DB.Where("owner_tenant_id = ? AND manager_tenant_id = ?", agentTenantID, relationship.SupplierTenantID).First(&account).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		result = append(result, map[string]interface{}{
			"supplier_tenant_id": relationship.SupplierTenantID,
			"supplier_name":      relationship.SupplierTenant.Name,
			"supplier_code":      relationship.SupplierTenant.SystemCode,
			"status":             relationship.Status, "agent_level": relationship.AgentLevel,
			"balance": account.Balance,
		})
	}
	return result, nil
}

func (s *DistributionService) ListMyAgents(supplierTenantID uint) ([]map[string]interface{}, error) {
	var relationships []model.DistributorRelationship
	if err := model.DB.Preload("AgentTenant").Where("supplier_tenant_id = ?", supplierTenantID).Order("created_at desc").Find(&relationships).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(relationships))
	for _, relationship := range relationships {
		result = append(result, map[string]interface{}{
			"id": relationship.ID, "agent_tenant_id": relationship.AgentTenantID,
			"agent_name": relationship.AgentTenant.Name, "agent_code": relationship.AgentTenant.SystemCode,
			"agent_contact": relationship.AgentTenant.Contact, "status": relationship.Status,
			"agent_level": relationship.AgentLevel, "created_at": relationship.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return result, nil
}

func (s *DistributionService) AuditAgent(supplierTenantID, relationshipID uint, status string) error {
	if status != "active" && status != "rejected" {
		return errors.New("invalid distribution status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var relationship model.DistributorRelationship
		if err := tx.Where("id = ? AND supplier_tenant_id = ?", relationshipID, supplierTenantID).First(&relationship).Error; err != nil {
			return errors.New("distribution application not found")
		}
		if err := tx.Model(&relationship).Update("status", status).Error; err != nil {
			return err
		}
		if status != "active" {
			return nil
		}

		var account model.CapitalAccount
		err := tx.Where("owner_tenant_id = ? AND manager_tenant_id = ?", relationship.AgentTenantID, relationship.SupplierTenantID).First(&account).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&model.CapitalAccount{
			OwnerTenantID: relationship.AgentTenantID, ManagerTenantID: relationship.SupplierTenantID,
			Status: "active",
		}).Error
	})
}

func (s *DistributionService) ListDistributableProducts(agentTenantID, supplierTenantID uint) ([]model.Product, error) {
	var relationship model.DistributorRelationship
	if err := model.DB.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", agentTenantID, supplierTenantID, "active").First(&relationship).Error; err != nil {
		return nil, errors.New("active distribution relationship not found")
	}
	var products []model.Product
	err := model.DB.Where("tenant_id = ? AND is_distributable = ? AND status = ?", supplierTenantID, true, "online").Find(&products).Error
	return products, err
}

func (s *DistributionService) ImportProduct(agentTenantID, sourceProductID uint, name string, price float64, productType string) error {
	name = strings.TrimSpace(name)
	if name == "" || price < 0 || (productType != "online" && productType != "offline") {
		return fmt.Errorf("invalid imported product")
	}
	return model.Write(func(tx *gorm.DB) error {
		var source model.Product
		if err := tx.Preload("Rule.Groups.Items").First(&source, sourceProductID).Error; err != nil {
			return errors.New("source product not found")
		}
		if !source.IsDistributable || source.Status != "online" {
			return errors.New("source product is not available for distribution")
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", agentTenantID, source.TenantID, "active").First(&relationship).Error; err != nil {
			return errors.New("active distribution relationship not found")
		}

		newRule := source.Rule
		groups := newRule.Groups
		newRule.Base = model.Base{}
		newRule.TenantID = agentTenantID
		newRule.Name = name + " (distribution rule)"
		newRule.Groups = nil
		if err := tx.Omit("Groups").Create(&newRule).Error; err != nil {
			return err
		}
		for _, sourceGroup := range groups {
			items := sourceGroup.Items
			newGroup := sourceGroup
			newGroup.Base = model.Base{}
			newGroup.RuleID = newRule.ID
			newGroup.Items = nil
			if err := tx.Omit("Items").Create(&newGroup).Error; err != nil {
				return err
			}
			for _, sourceItem := range items {
				newItem := sourceItem
				newItem.Base = model.Base{}
				newItem.GroupID = newGroup.ID
				newItem.CheckPoint = model.CheckPoint{}
				if err := tx.Omit("CheckPoint").Create(&newItem).Error; err != nil {
					return err
				}
			}
		}

		newProduct := model.Product{
			Name: name, Price: price, TenantID: agentTenantID, RuleID: newRule.ID,
			Type: productType, Status: "online", SettlementPrice: source.SettlementPrice,
			SourceProductID: source.ID, SourceTenantID: source.TenantID,
			ValidityType: source.ValidityType, ValidityStartDate: source.ValidityStartDate,
			ValidityEndDate: source.ValidityEndDate, ValidityDays: source.ValidityDays,
			StockType: source.StockType, DailyStock: source.DailyStock,
			RealNameRequired: source.RealNameRequired, RegionLimit: source.RegionLimit,
			LimitPerPhone: source.LimitPerPhone, LimitPerID: source.LimitPerID,
			RefundType: source.RefundType, RefundRule: source.RefundRule, CodeMode: source.CodeMode,
		}
		return tx.Omit("Rule").Create(&newProduct).Error
	})
}

func (s *DistributionService) RechargeAgent(supplierTenantID, agentTenantID uint, amount float64) error {
	if amount <= 0 {
		return errors.New("recharge amount must be greater than zero")
	}
	return model.Write(func(tx *gorm.DB) error {
		var account model.CapitalAccount
		if err := tx.Where("owner_tenant_id = ? AND manager_tenant_id = ?", agentTenantID, supplierTenantID).First(&account).Error; err != nil {
			return errors.New("distribution capital account not found")
		}
		account.Balance += amount
		if err := tx.Model(&account).Update("balance", account.Balance).Error; err != nil {
			return err
		}
		return tx.Create(&model.TransactionRecord{
			AccountID: account.ID, Type: "deposit", Amount: amount,
			BalanceAfter: account.Balance, Memo: "supplier recharge",
		}).Error
	})
}
