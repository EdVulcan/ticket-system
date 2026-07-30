package service

import (
	"fmt"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type ProductService struct{}

// CreateRuleAndProduct 创建规则和产品 (事务处理)
func (s *ProductService) Create(product *model.Product, rule *model.TicketRule) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := validateProduct(tx, product.TenantID, product, rule); err != nil {
			return err
		}
		// Force clear IDs to ensure creation
		rule.ID = 0
		for i := range rule.Groups {
			rule.Groups[i].ID = 0
			rule.Groups[i].RuleID = 0
			for j := range rule.Groups[i].Items {
				rule.Groups[i].Items[j].ID = 0
				rule.Groups[i].Items[j].GroupID = 0
			}
		}

		// 1. Create Rule
		if err := tx.Create(rule).Error; err != nil {
			return err
		}

		// 2. Link Rule to Product
		product.ID = 0
		product.RuleID = rule.ID
		if err := tx.Create(product).Error; err != nil {
			return err
		}

		return nil
	})
}

// Update 更新产品及规则 (事务处理)
func (s *ProductService) Update(id, tenantID uint, product *model.Product, rule *model.TicketRule) error {
	return model.Write(func(tx *gorm.DB) error {
		// 1. Find existing product to get RuleID
		var existingProduct model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", id, tenantID).First(&existingProduct).Error; err != nil {
			return err
		}
		if err := validateProduct(tx, tenantID, product, rule); err != nil {
			return err
		}

		// 2. Update Product Fields
		product.ID = id
		product.RuleID = existingProduct.RuleID // Keep the same Rule ID
		product.TenantID = existingProduct.TenantID

		// Use Select("*") to update all fields including zero values (e.g. 0, "", false)
		// Omit protected fields
		if err := tx.Model(&existingProduct).
			Select("*").
			Omit("id", "created_at", "updated_at", "deleted_at", "tenant_id", "rule_id").
			Updates(product).Error; err != nil {
			return err
		}

		// 3. Update Rule Basic Info
		rule.ID = existingProduct.RuleID
		if err := tx.Model(&model.TicketRule{Base: model.Base{ID: rule.ID}}).Updates(rule).Error; err != nil {
			return err
		}

		// 4. Replace Rule Groups & Items (Simplest strategy for complex nested updates)
		// 4.1 Find all existing groups for this rule
		var existingGroups []model.RuleGroup
		if err := tx.Where("rule_id = ?", rule.ID).Find(&existingGroups).Error; err != nil {
			return err
		}

		// 4.2 Delete all items in these groups
		for _, g := range existingGroups {
			if err := tx.Where("group_id = ?", g.ID).Delete(&model.RuleItem{}).Error; err != nil {
				return err
			}
		}

		// 4.3 Delete all groups
		if err := tx.Where("rule_id = ?", rule.ID).Delete(&model.RuleGroup{}).Error; err != nil {
			return err
		}

		// 4.4 Re-create Groups and Items
		for _, group := range rule.Groups {
			group.RuleID = rule.ID
			// Clear IDs to force create (avoid PK conflict with soft-deleted records)
			group.ID = 0

			for i := range group.Items {
				group.Items[i].ID = 0
				group.Items[i].GroupID = 0
			}

			if err := tx.Create(&group).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *ProductService) List(page, pageSize int, tenantID uint, productType string) ([]model.Product, int64, error) {
	var products []model.Product
	var total int64

	if tenantID == 0 {
		return nil, 0, fmt.Errorf("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	query := model.DB.Model(&model.Product{}).Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint")
	query = query.Where("tenant_id = ?", tenantID)
	if productType != "" {
		query = query.Where("type = ?", productType)
	}

	err := query.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = query.Offset(offset).Limit(pageSize).Find(&products).Error
	return products, total, err
}

func validateProduct(tx *gorm.DB, tenantID uint, product *model.Product, rule *model.TicketRule) error {
	if tenantID == 0 || product.Name == "" || rule.Name == "" {
		return fmt.Errorf("product name, rule name, and tenant are required")
	}
	if product.Price < 0 || product.SettlementPrice < 0 {
		return fmt.Errorf("product prices cannot be negative")
	}
	if product.Type != "online" && product.Type != "offline" {
		return fmt.Errorf("invalid product type")
	}
	if product.Status != "online" && product.Status != "offline" {
		return fmt.Errorf("invalid product status")
	}
	if product.CodeMode != "order" && product.CodeMode != "ticket" {
		return fmt.Errorf("invalid ticket code mode")
	}
	if product.ValidityType != "date" && product.ValidityType != "days" {
		return fmt.Errorf("invalid validity type")
	}
	if product.StockType != "unlimited" && product.StockType != "daily" && product.StockType != "total" {
		return fmt.Errorf("invalid stock type")
	}
	if product.StockType != "unlimited" && product.DailyStock < 0 {
		return fmt.Errorf("stock cannot be negative")
	}
	seenCheckpoints := make(map[uint]struct{})
	for _, group := range rule.Groups {
		if len(group.Items) == 0 {
			return fmt.Errorf("every rule group must contain a checkpoint")
		}
		for _, item := range group.Items {
			if item.MaxPerCheckIn <= 0 {
				return fmt.Errorf("checkpoint admission limit must be greater than zero")
			}
			if _, duplicate := seenCheckpoints[item.CheckPointID]; duplicate {
				return fmt.Errorf("checkpoint %d appears more than once in the rule", item.CheckPointID)
			}
			seenCheckpoints[item.CheckPointID] = struct{}{}
			var count int64
			if err := tx.Model(&model.CheckPoint{}).Where("id = ? AND tenant_id = ?", item.CheckPointID, tenantID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("checkpoint %d does not belong to this tenant", item.CheckPointID)
			}
		}
		if group.MaxTotalCheckIn < 0 || (group.MaxTotalCheckIn > 0 && group.MaxTotalCheckIn > len(group.Items)) {
			return fmt.Errorf("invalid rule group limit")
		}
	}
	return nil
}

func (s *ProductService) Delete(id, tenantID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Product{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (s *ProductService) Get(id, tenantID uint) (*model.Product, error) {
	var product model.Product
	if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ?", id, tenantID).First(&product).Error; err != nil {
		return nil, err
	}
	return &product, nil
}

func (s *ProductService) UpdateStatus(id, tenantID uint, status string) error {
	if status != "online" && status != "offline" {
		return fmt.Errorf("invalid product status")
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", id, tenantID).Update("status", status)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}
