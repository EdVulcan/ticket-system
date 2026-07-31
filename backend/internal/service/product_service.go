package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type ProductService struct{}

// CreateRuleAndProduct 创建规则和产品 (事务处理)
func (s *ProductService) Create(product *model.Product, rule *model.TicketRule) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, product.TenantID, "supplier"); err != nil {
			return err
		}
		if product.SourceProductID != 0 || product.SourceTenantID != 0 ||
			product.FulfillmentProductID != 0 || product.FulfillmentTenantID != 0 || product.ProductOfferID != 0 {
			return fmt.Errorf("distributed products must be created through an active product offer")
		}
		if err := validateProduct(tx, product.TenantID, product, rule); err != nil {
			return err
		}
		if err := assignProductScenicArea(tx, product.TenantID, product, rule); err != nil {
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
		product.Rule = *rule
		_, err := createProductRevisionTx(tx, product)
		return err
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
		capability := "supplier"
		if isDistributedListing(&existingProduct) {
			capability = "distributor"
		}
		if err := requireActiveTenantCapability(tx, tenantID, capability); err != nil {
			return err
		}

		// A distributor listing does not own the supplier's fulfillment rule.
		// It may change its sell-side presentation, but its source and settlement
		// ownership stay immutable and its local rule is never rewritten.
		if isDistributedListing(&existingProduct) {
			if err := validateSellerListingFields(product); err != nil {
				return err
			}
			product.ID = id
			product.RuleID = existingProduct.RuleID
			product.TenantID = existingProduct.TenantID
			product.SourceProductID = existingProduct.SourceProductID
			product.SourceTenantID = existingProduct.SourceTenantID
			product.FulfillmentProductID = existingProduct.FulfillmentProductID
			product.FulfillmentTenantID = existingProduct.FulfillmentTenantID
			product.FulfillmentScenicAreaID = existingProduct.FulfillmentScenicAreaID
			product.ProductOfferID = existingProduct.ProductOfferID
			product.ScenicAreaID = existingProduct.ScenicAreaID
			product.SettlementPrice = existingProduct.SettlementPrice
			return tx.Model(&existingProduct).
				Select("*").
				Omit("id", "created_at", "updated_at", "deleted_at", "tenant_id", "rule_id",
					"source_product_id", "source_tenant_id", "fulfillment_product_id", "fulfillment_tenant_id", "fulfillment_scenic_area_id", "product_offer_id", "settlement_price", "scenic_area_id").
				Updates(product).Error
		}

		if err := validateProduct(tx, tenantID, product, rule); err != nil {
			return err
		}
		if err := assignProductScenicArea(tx, tenantID, product, rule); err != nil {
			return err
		}

		// 2. Update Product Fields
		product.ID = id
		product.RuleID = existingProduct.RuleID // Keep the same Rule ID
		product.TenantID = existingProduct.TenantID
		product.SourceProductID = existingProduct.SourceProductID
		product.SourceTenantID = existingProduct.SourceTenantID
		product.FulfillmentProductID = existingProduct.FulfillmentProductID
		product.FulfillmentTenantID = existingProduct.FulfillmentTenantID
		product.FulfillmentScenicAreaID = existingProduct.FulfillmentScenicAreaID
		product.ProductOfferID = existingProduct.ProductOfferID
		if product.ScenicAreaID == 0 {
			product.ScenicAreaID = existingProduct.ScenicAreaID
		}

		// Use Select("*") to update all fields including zero values (e.g. 0, "", false)
		// Omit protected fields
		if err := tx.Model(&existingProduct).
			Select("*").
			Omit("id", "created_at", "updated_at", "deleted_at", "tenant_id", "rule_id",
				"source_product_id", "source_tenant_id", "fulfillment_product_id", "fulfillment_tenant_id", "fulfillment_scenic_area_id", "product_offer_id").
			Updates(product).Error; err != nil {
			return err
		}

		// 3. Update Rule Basic Info
		rule.ID = existingProduct.RuleID
		rule.TenantID = tenantID
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
		var revised model.Product
		if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ?", id, tenantID).First(&revised).Error; err != nil {
			return err
		}
		if _, err := createProductRevisionTx(tx, &revised); err != nil {
			return err
		}
		return tx.Model(&model.ProductOffer{}).
			Where("source_product_id = ? AND supplier_tenant_id = ? AND status = ? AND product_revision_id != ?", revised.ID, tenantID, "active", revised.CurrentRevisionID).
			Update("status", "suspended").Error
	})
}

func createProductRevisionTx(tx *gorm.DB, product *model.Product) (*model.ProductRevision, error) {
	if product == nil || product.ID == 0 || product.TenantID == 0 || product.ScenicAreaID == 0 {
		return nil, errors.New("versioned product requires tenant and scenic area")
	}
	var latest model.ProductRevision
	version := 1
	if err := tx.Where("product_id = ? AND tenant_id = ?", product.ID, product.TenantID).Order("version DESC").First(&latest).Error; err == nil {
		version = latest.Version + 1
		now := time.Now()
		if err := tx.Model(&latest).Updates(map[string]interface{}{"status": "expired", "effective_to": now}).Error; err != nil {
			return nil, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	snapshot, err := json.Marshal(product.Rule)
	if err != nil {
		return nil, err
	}
	revision := model.ProductRevision{
		ProductID: product.ID, TenantID: product.TenantID, ScenicAreaID: product.ScenicAreaID,
		Version: version, Status: "active", PriceCents: moneyCents(product.Price),
		SettlementCents: moneyCents(product.SettlementPrice), SnapshotJSON: string(snapshot), EffectiveFrom: time.Now(),
	}
	if err := tx.Create(&revision).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", product.ID, product.TenantID).Update("current_revision_id", revision.ID).Error; err != nil {
		return nil, err
	}
	product.CurrentRevisionID = revision.ID
	return &revision, nil
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
	if err == nil {
		hydrateFulfillmentRules(products)
	}
	return products, total, err
}

func validateProduct(tx *gorm.DB, tenantID uint, product *model.Product, rule *model.TicketRule) error {
	if err := validateProductFields(product); err != nil {
		return err
	}
	if tenantID == 0 || rule.Name == "" {
		return fmt.Errorf("product name, rule name, and tenant are required")
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

func assignProductScenicArea(tx *gorm.DB, tenantID uint, product *model.Product, rule *model.TicketRule) error {
	var scenicAreaID uint
	for _, group := range rule.Groups {
		for _, item := range group.Items {
			var checkpoint model.CheckPoint
			if err := tx.Select("id", "tenant_id", "scenic_area_id").Where("id = ? AND tenant_id = ?", item.CheckPointID, tenantID).First(&checkpoint).Error; err != nil {
				return err
			}
			if checkpoint.ScenicAreaID == 0 {
				return fmt.Errorf("checkpoint %d has no scenic area", checkpoint.ID)
			}
			if scenicAreaID == 0 {
				scenicAreaID = checkpoint.ScenicAreaID
				continue
			}
			if scenicAreaID != checkpoint.ScenicAreaID {
				return fmt.Errorf("a product cannot combine checkpoints from different scenic areas")
			}
		}
	}
	if scenicAreaID == 0 {
		return fmt.Errorf("product must belong to a scenic area")
	}
	product.ScenicAreaID = scenicAreaID
	return nil
}

func validateProductFields(product *model.Product) error {
	if product.Name == "" {
		return fmt.Errorf("product name is required")
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
	return nil
}

func validateSellerListingFields(product *model.Product) error {
	if product.Name == "" {
		return fmt.Errorf("product name is required")
	}
	if product.Price < 0 {
		return fmt.Errorf("product price cannot be negative")
	}
	if product.Type != "online" && product.Type != "offline" {
		return fmt.Errorf("invalid product type")
	}
	if product.Status != "online" && product.Status != "offline" {
		return fmt.Errorf("invalid product status")
	}
	return nil
}

func isDistributedListing(product *model.Product) bool {
	return product.SourceProductID > 0 || product.SourceTenantID > 0 ||
		(product.FulfillmentProductID > 0 && product.FulfillmentTenantID != 0 && product.FulfillmentTenantID != product.TenantID)
}

func hydrateFulfillmentRules(products []model.Product) {
	for i := range products {
		productID := products[i].FulfillmentProductID
		tenantID := products[i].FulfillmentTenantID
		if productID == 0 && products[i].SourceProductID > 0 {
			productID = products[i].SourceProductID
			tenantID = products[i].SourceTenantID
		}
		if productID == 0 || tenantID == 0 {
			continue
		}
		var source model.Product
		if err := model.DB.
			Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").
			Where("id = ? AND tenant_id = ?", productID, tenantID).
			First(&source).Error; err == nil {
			products[i].Rule = source.Rule
		}
	}
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
	if isDistributedListing(&product) {
		var source model.Product
		productID := product.FulfillmentProductID
		sourceTenantID := product.FulfillmentTenantID
		if productID == 0 {
			productID = product.SourceProductID
			sourceTenantID = product.SourceTenantID
		}
		if err := model.DB.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Preload("Rule.Groups.Items.CheckPoint").Where("id = ? AND tenant_id = ?", productID, sourceTenantID).First(&source).Error; err == nil {
			product.Rule = source.Rule
		}
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
