package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

var (
	ErrCommerceProductInvalid = errors.New("commerce product is invalid")
	ErrCommerceSKUInvalid     = errors.New("commerce sku is invalid")
)

// CommerceCatalogService owns the restaurant and retail product catalog. It
// deliberately does not use the ticket Product model: commercial products
// have independent SKU and option semantics and must never enter ticket
// fulfillment or verification.
type CommerceCatalogService struct {
	DB *gorm.DB
}

func (s *CommerceCatalogService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

// write mirrors db() so a service created with an isolated database never
// sends its mutations to the process-global writer connection.
func (s *CommerceCatalogService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

type CreateCommerceProductInput struct {
	BusinessType string                   `json:"business_type"`
	Name         string                   `json:"name"`
	ShortTitle   string                   `json:"short_title"`
	Description  string                   `json:"description"`
	CategoryName string                   `json:"category_name"`
	Status       string                   `json:"status"`
	SaleStartsAt *time.Time               `json:"sale_starts_at,omitempty"`
	SaleEndsAt   *time.Time               `json:"sale_ends_at,omitempty"`
	SKUs         []CreateCommerceSKUInput `json:"skus"`
}

type CreateCommerceSKUInput struct {
	SKUCode            string `json:"sku_code"`
	Name               string `json:"name"`
	OriginalPriceCents int64  `json:"original_price_cents"`
	PriceCents         int64  `json:"price_cents"`
	Status             string `json:"status"`
	AttributesJSON     string `json:"attributes"`
}

type UpdateCommerceSKUInput struct {
	SKUCode            string `json:"sku_code"`
	Name               string `json:"name"`
	OriginalPriceCents int64  `json:"original_price_cents"`
	PriceCents         int64  `json:"price_cents"`
	Status             string `json:"status"`
	AttributesJSON     string `json:"attributes"`
}

func normalizeCommerceProductInput(input CreateCommerceProductInput) (CreateCommerceProductInput, error) {
	input.BusinessType = strings.TrimSpace(input.BusinessType)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortTitle = strings.TrimSpace(input.ShortTitle)
	input.CategoryName = strings.TrimSpace(input.CategoryName)
	input.Status = strings.TrimSpace(input.Status)
	if input.BusinessType != "restaurant" && input.BusinessType != "retail" {
		return input, fmt.Errorf("%w: business type must be restaurant or retail", ErrCommerceProductInvalid)
	}
	if input.Name == "" || len([]rune(input.Name)) > 160 {
		return input, fmt.Errorf("%w: name is required and must be at most 160 characters", ErrCommerceProductInvalid)
	}
	if len([]rune(input.ShortTitle)) > 80 || len([]rune(input.CategoryName)) > 80 {
		return input, fmt.Errorf("%w: short title or category is too long", ErrCommerceProductInvalid)
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.Status != "draft" && input.Status != "online" && input.Status != "offline" {
		return input, fmt.Errorf("%w: status is invalid", ErrCommerceProductInvalid)
	}
	if input.SaleStartsAt != nil && input.SaleEndsAt != nil && input.SaleEndsAt.Before(*input.SaleStartsAt) {
		return input, fmt.Errorf("%w: sale end must not precede sale start", ErrCommerceProductInvalid)
	}
	if len(input.SKUs) == 0 {
		return input, fmt.Errorf("%w: at least one sku is required", ErrCommerceProductInvalid)
	}
	seenCodes := make(map[string]struct{}, len(input.SKUs))
	for i := range input.SKUs {
		sku, err := normalizeCommerceSKUInput(input.SKUs[i])
		if err != nil {
			return input, err
		}
		if _, exists := seenCodes[sku.SKUCode]; exists {
			return input, fmt.Errorf("%w: duplicate sku code", ErrCommerceSKUInvalid)
		}
		seenCodes[sku.SKUCode] = struct{}{}
		input.SKUs[i] = sku
	}
	return input, nil
}

func normalizeCommerceSKUInput(input CreateCommerceSKUInput) (CreateCommerceSKUInput, error) {
	input.SKUCode = strings.TrimSpace(input.SKUCode)
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	if input.SKUCode == "" || len([]rune(input.SKUCode)) > 80 || input.Name == "" || len([]rune(input.Name)) > 160 {
		return input, fmt.Errorf("%w: sku code and name are required and bounded", ErrCommerceSKUInvalid)
	}
	if input.OriginalPriceCents < 0 || input.PriceCents < 0 || input.PriceCents > input.OriginalPriceCents {
		return input, fmt.Errorf("%w: prices must be integer cents with sale price no greater than original price", ErrCommerceSKUInvalid)
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "inactive" {
		return input, fmt.Errorf("%w: status is invalid", ErrCommerceSKUInvalid)
	}
	return input, nil
}

func normalizeCommerceSKUUpdate(input UpdateCommerceSKUInput) (UpdateCommerceSKUInput, error) {
	created, err := normalizeCommerceSKUInput(CreateCommerceSKUInput{
		SKUCode: input.SKUCode, Name: input.Name, OriginalPriceCents: input.OriginalPriceCents,
		PriceCents: input.PriceCents, Status: input.Status, AttributesJSON: input.AttributesJSON,
	})
	if err != nil {
		return input, err
	}
	return UpdateCommerceSKUInput{
		SKUCode: created.SKUCode, Name: created.Name, OriginalPriceCents: created.OriginalPriceCents,
		PriceCents: created.PriceCents, Status: created.Status, AttributesJSON: created.AttributesJSON,
	}, nil
}

func requireActiveCommerceCapability(tx *gorm.DB, tenantID uint, businessType string) error {
	return RequireActiveTenantBusinessCapability(tx, tenantID, businessType)
}

func (s *CommerceCatalogService) CreateProduct(tenantID uint, input CreateCommerceProductInput) (*model.CommerceProduct, error) {
	input, err := normalizeCommerceProductInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceProduct
	err = s.write(func(tx *gorm.DB) error {
		if err := requireActiveCommerceCapability(tx, tenantID, input.BusinessType); err != nil {
			return err
		}
		product := model.CommerceProduct{
			TenantID: tenantID, BusinessType: input.BusinessType, Name: input.Name, ShortTitle: input.ShortTitle,
			Description: input.Description, CategoryName: input.CategoryName, Status: input.Status,
			SaleStartsAt: input.SaleStartsAt, SaleEndsAt: input.SaleEndsAt, CurrentVersion: 1,
		}
		if err := tx.Create(&product).Error; err != nil {
			return err
		}
		for _, inputSKU := range input.SKUs {
			sku := model.CommerceSKU{
				TenantID: tenantID, ProductID: product.ID, SkuCode: inputSKU.SKUCode, Name: inputSKU.Name,
				OriginalPriceCents: inputSKU.OriginalPriceCents, PriceCents: inputSKU.PriceCents,
				Status: inputSKU.Status, AttributesJSON: inputSKU.AttributesJSON,
			}
			if err := tx.Create(&sku).Error; err != nil {
				return err
			}
		}
		if err := tx.Preload("SKUs").First(&product, product.ID).Error; err != nil {
			return err
		}
		result = &product
		return nil
	})
	return result, err
}

func (s *CommerceCatalogService) ListProducts(tenantID uint, domain, status, search string) ([]model.CommerceProduct, error) {
	db := s.db()
	domain = strings.TrimSpace(domain)
	status = strings.TrimSpace(status)
	if domain != "" && domain != "restaurant" && domain != "retail" {
		return nil, fmt.Errorf("%w: business type filter is invalid", ErrCommerceProductInvalid)
	}
	if status != "" && status != "draft" && status != "online" && status != "offline" {
		return nil, fmt.Errorf("%w: status filter is invalid", ErrCommerceProductInvalid)
	}
	if domain != "" {
		if err := requireActiveCommerceCapability(db, tenantID, domain); err != nil {
			return nil, err
		}
	} else {
		if err := requireActiveTenant(db, tenantID); err != nil {
			return nil, err
		}
		// An unqualified list is still constrained to the tenant's currently
		// enabled commercial domains. Suspended-domain catalog rows remain
		// historical facts but must not leak into an active workbench.
		var enabled []string
		if err := db.Model(&model.TenantBusinessCapability{}).
			Where("tenant_id = ? AND status = ?", tenantID, "active").
			Pluck("business_type", &enabled).Error; err != nil {
			return nil, err
		}
		if len(enabled) == 0 {
			return nil, ErrBusinessCapabilityInactive
		}
		// Reuse a server-side IN predicate below rather than trusting a
		// client-supplied domain to widen the result set.
		query := db.Where("tenant_id = ? AND business_type IN ?", tenantID, enabled)
		if status != "" {
			query = query.Where("status = ?", status)
		}
		if search = strings.TrimSpace(search); search != "" {
			query = query.Where("(name ILIKE ? OR short_title ILIKE ?)", "%"+search+"%", "%"+search+"%")
		}
		var products []model.CommerceProduct
		if err := query.Preload("SKUs").Order("created_at ASC").Find(&products).Error; err != nil {
			return nil, err
		}
		if products == nil {
			products = []model.CommerceProduct{}
		}
		return products, nil
	}
	query := db.Where("tenant_id = ?", tenantID)
	if domain != "" {
		query = query.Where("business_type = ?", domain)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search = strings.TrimSpace(search); search != "" {
		query = query.Where("(name ILIKE ? OR short_title ILIKE ?)", "%"+search+"%", "%"+search+"%")
	}
	var products []model.CommerceProduct
	if err := query.Preload("SKUs").Order("created_at ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	if products == nil {
		products = []model.CommerceProduct{}
	}
	return products, nil
}

func (s *CommerceCatalogService) GetProduct(tenantID, productID uint) (*model.CommerceProduct, error) {
	db := s.db()
	var product model.CommerceProduct
	if err := db.Where("id = ? AND tenant_id = ?", productID, tenantID).Preload("SKUs").First(&product).Error; err != nil {
		return nil, err
	}
	if err := requireActiveCommerceCapability(db, tenantID, product.BusinessType); err != nil {
		return nil, err
	}
	return &product, nil
}

func (s *CommerceCatalogService) SetProductStatus(tenantID, productID uint, status string) (*model.CommerceProduct, error) {
	status = strings.TrimSpace(status)
	if status != "draft" && status != "online" && status != "offline" {
		return nil, fmt.Errorf("%w: status is invalid", ErrCommerceProductInvalid)
	}
	var result *model.CommerceProduct
	err := s.write(func(tx *gorm.DB) error {
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
			return err
		}
		if status == "online" {
			var count int64
			if err := tx.Model(&model.CommerceSKU{}).Where("tenant_id = ? AND product_id = ? AND status = ?", tenantID, productID, "active").Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return fmt.Errorf("%w: an online product needs an active sku", ErrCommerceProductInvalid)
			}
		}
		if err := tx.Model(&product).Update("status", status).Error; err != nil {
			return err
		}
		if err := tx.Preload("SKUs").First(&product, product.ID).Error; err != nil {
			return err
		}
		result = &product
		return nil
	})
	return result, err
}

func (s *CommerceCatalogService) CreateSKU(tenantID, productID uint, input CreateCommerceSKUInput) (*model.CommerceSKU, error) {
	input, err := normalizeCommerceSKUInput(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceSKU
	err = s.write(func(tx *gorm.DB) error {
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ?", productID, tenantID).First(&product).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
			return err
		}
		sku := model.CommerceSKU{TenantID: tenantID, ProductID: productID, SkuCode: input.SKUCode, Name: input.Name, OriginalPriceCents: input.OriginalPriceCents, PriceCents: input.PriceCents, Status: input.Status, AttributesJSON: input.AttributesJSON}
		if err := tx.Create(&sku).Error; err != nil {
			return err
		}
		result = &sku
		return nil
	})
	return result, err
}

func (s *CommerceCatalogService) UpdateSKU(tenantID, skuID uint, input UpdateCommerceSKUInput) (*model.CommerceSKU, error) {
	input, err := normalizeCommerceSKUUpdate(input)
	if err != nil {
		return nil, err
	}
	var result *model.CommerceSKU
	err = s.write(func(tx *gorm.DB) error {
		var sku model.CommerceSKU
		if err := tx.Where("id = ? AND tenant_id = ?", skuID, tenantID).First(&sku).Error; err != nil {
			return err
		}
		var product model.CommerceProduct
		if err := tx.Where("id = ? AND tenant_id = ?", sku.ProductID, tenantID).First(&product).Error; err != nil {
			return err
		}
		if err := requireActiveCommerceCapability(tx, tenantID, product.BusinessType); err != nil {
			return err
		}
		var duplicate int64
		if err := tx.Model(&model.CommerceSKU{}).Where("tenant_id = ? AND sku_code = ? AND id <> ?", tenantID, input.SKUCode, skuID).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate != 0 {
			return fmt.Errorf("%w: sku code already exists", ErrCommerceSKUInvalid)
		}
		if err := tx.Model(&sku).Updates(map[string]interface{}{
			"sku_code": input.SKUCode, "name": input.Name, "original_price_cents": input.OriginalPriceCents,
			"price_cents": input.PriceCents, "status": input.Status, "attributes_json": input.AttributesJSON,
		}).Error; err != nil {
			return err
		}
		if err := tx.First(&sku, sku.ID).Error; err != nil {
			return err
		}
		result = &sku
		return nil
	})
	return result, err
}
