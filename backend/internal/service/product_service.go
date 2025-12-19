package service

import (
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type ProductService struct{}

// CreateRuleAndProduct 创建规则和产品 (事务处理)
func (s *ProductService) Create(product *model.Product, rule *model.TicketRule) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Create Rule
		if err := tx.Create(rule).Error; err != nil {
			return err
		}

		// 2. Link Rule to Product
		product.RuleID = rule.ID
		if err := tx.Create(product).Error; err != nil {
			return err
		}

		return nil
	})
}

// Update 更新产品及规则 (事务处理)
func (s *ProductService) Update(id uint, product *model.Product, rule *model.TicketRule) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Find existing product to get RuleID
		var existingProduct model.Product
		if err := tx.First(&existingProduct, id).Error; err != nil {
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

	offset := (page - 1) * pageSize

	query := model.DB.Model(&model.Product{}).Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint")
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
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

func (s *ProductService) Delete(id uint) error {
	return model.DB.Delete(&model.Product{}, id).Error
}

func (s *ProductService) UpdateStatus(id uint, status string) error {
	return model.DB.Model(&model.Product{}).Where("id = ?", id).Update("status", status).Error
}
