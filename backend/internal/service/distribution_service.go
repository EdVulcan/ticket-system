package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DistributionService struct{}

func (s *DistributionService) GetSupplierByCode(code string) (*model.Tenant, error) {
	var tenant model.Tenant
	if err := model.DB.Where("system_code = ? AND status = ?", strings.TrimSpace(code), "active").First(&tenant).Error; err != nil {
		return nil, errors.New("supplier not found")
	}
	if err := requireActiveTenantCapability(model.DB, tenant.ID, "supplier"); err != nil {
		return nil, errors.New("supplier not found")
	}
	return &tenant, nil
}

func (s *DistributionService) ApplyAgent(agentTenantID uint, supplierSystemCode string) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, agentTenantID, "distributor"); err != nil {
			return err
		}
		var supplier model.Tenant
		if err := tx.Where("system_code = ?", strings.TrimSpace(supplierSystemCode)).First(&supplier).Error; err != nil {
			return errors.New("supplier system code does not exist")
		}
		if supplier.Status != "" && supplier.Status != "active" {
			return errors.New("supplier tenant is unavailable")
		}
		if supplier.ID == agentTenantID {
			return errors.New("a tenant cannot distribute its own products")
		}
		if err := requireActiveTenantCapability(tx, supplier.ID, "supplier"); err != nil {
			return errors.New("supplier tenant is unavailable")
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
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
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
	if err := requireActiveTenantCapability(model.DB, agentTenantID, "distributor"); err != nil {
		return nil, err
	}
	if err := requireActiveTenantCapability(model.DB, supplierTenantID, "supplier"); err != nil {
		return nil, err
	}
	var relationship model.DistributorRelationship
	if err := model.DB.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", agentTenantID, supplierTenantID, "active").First(&relationship).Error; err != nil {
		return nil, errors.New("active distribution relationship not found")
	}
	var products []model.Product
	now := time.Now()
	err := model.DB.Model(&model.Product{}).
		Joins("JOIN product_offers ON product_offers.source_product_id = products.id AND product_offers.deleted_at IS NULL").
		Where("products.tenant_id = ? AND products.is_distributable = ? AND products.status = ?", supplierTenantID, true, "online").
		Where("product_offers.distributor_tenant_id = ? AND product_offers.status = ?", agentTenantID, "active").
		Where("product_offers.sales_start_at IS NULL OR product_offers.sales_start_at <= ?", now).
		Where("product_offers.sales_end_at IS NULL OR product_offers.sales_end_at >= ?", now).
		Find(&products).Error
	return products, err
}

func (s *DistributionService) CreateOffer(supplierTenantID, distributorTenantID, sourceProductID uint, settlementPrice float64, commissionBPS int64, allowedChannels string, salesStartAt, salesEndAt *time.Time) (*model.ProductOffer, error) {
	return s.CreateOfferWithPolicy(supplierTenantID, distributorTenantID, sourceProductID, settlementPrice, 0, 0, commissionBPS, allowedChannels, salesStartAt, salesEndAt)
}

// CreateOfferWithPolicy creates the supplier-owned authorization. The legacy
// CreateOffer wrapper keeps existing integrations compatible while new callers
// can set a retail floor and a bounded sales quota.
func (s *DistributionService) CreateOfferWithPolicy(supplierTenantID, distributorTenantID, sourceProductID uint, settlementPrice float64, minimumRetailPrice float64, quota int, commissionBPS int64, allowedChannels string, salesStartAt, salesEndAt *time.Time) (*model.ProductOffer, error) {
	if settlementPrice <= 0 || minimumRetailPrice < 0 || quota < 0 || commissionBPS < 0 || commissionBPS > 10000 {
		return nil, errors.New("settlement price must be greater than zero")
	}
	if salesStartAt != nil && salesEndAt != nil && !salesEndAt.After(*salesStartAt) {
		return nil, errors.New("offer sales end must be after sales start")
	}
	var offer model.ProductOffer
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
		if err := requireActiveTenantCapability(tx, distributorTenantID, "distributor"); err != nil {
			return err
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", distributorTenantID, supplierTenantID, "active").First(&relationship).Error; err != nil {
			return errors.New("active distribution relationship not found")
		}
		var source model.Product
		if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").Where("id = ? AND tenant_id = ? AND status = ? AND is_distributable = ?", sourceProductID, supplierTenantID, "online", true).First(&source).Error; err != nil {
			return errors.New("source product is not available for distribution")
		}
		if source.ScenicAreaID == 0 {
			return errors.New("source product has no scenic area")
		}
		minimumCents := moneyCents(minimumRetailPrice)
		if minimumCents == 0 {
			minimumCents = moneyCents(settlementPrice)
		}
		if minimumCents < moneyCents(settlementPrice) {
			return errors.New("minimum retail price cannot be below settlement price")
		}
		revision, err := ensureProductRevisionTx(tx, &source)
		if err != nil {
			return err
		}
		offer = model.ProductOffer{
			SupplierTenantID: supplierTenantID, DistributorTenantID: distributorTenantID,
			SourceProductID: source.ID, ProductRevisionID: revision.ID, FulfillmentScenicAreaID: source.ScenicAreaID,
			SettlementPrice: roundMoney(settlementPrice), CommissionBPS: commissionBPS, Status: "active", AllowedChannels: strings.TrimSpace(allowedChannels),
			MinimumRetailPriceCents: minimumCents, Quota: quota,
			SalesStartAt: salesStartAt, SalesEndAt: salesEndAt,
		}
		var existing model.ProductOffer
		if err := tx.Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ?", supplierTenantID, distributorTenantID, source.ID).First(&existing).Error; err == nil {
			if err := tx.Model(&existing).Updates(map[string]interface{}{
				"product_revision_id": revision.ID, "fulfillment_scenic_area_id": source.ScenicAreaID,
				"settlement_price": offer.SettlementPrice, "commission_bps": commissionBPS, "status": "active", "allowed_channels": offer.AllowedChannels,
				"minimum_retail_price_cents": minimumCents, "quota": quota,
				"sales_start_at": salesStartAt, "sales_end_at": salesEndAt,
			}).Error; err != nil {
				return err
			}
			return tx.First(&offer, existing.ID).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&offer).Error
	})
	return &offer, err
}

func (s *DistributionService) ListOffers(supplierTenantID, distributorTenantID uint, status string, page, pageSize int) ([]model.ProductOffer, int64, error) {
	if supplierTenantID == 0 {
		return nil, 0, errors.New("supplier tenant is required")
	}
	if err := requireActiveTenantCapability(model.DB, supplierTenantID, "supplier"); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.ProductOffer{}).Where("supplier_tenant_id = ?", supplierTenantID)
	if distributorTenantID > 0 {
		query = query.Where("distributor_tenant_id = ?", distributorTenantID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var offers []model.ProductOffer
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&offers).Error; err != nil {
		return nil, 0, err
	}
	return offers, total, nil
}

// SetOfferStatus is the supplier-side lifecycle control. A distributor can
// only consume an active offer; it cannot reactivate or broaden authorization.
func (s *DistributionService) SetOfferStatus(supplierTenantID, offerID, operatorID uint, status, reason string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if supplierTenantID == 0 || offerID == 0 || operatorID == 0 {
		return errors.New("supplier, offer and operator are required")
	}
	if status != "active" && status != "suspended" && status != "expired" {
		return errors.New("invalid offer status")
	}
	if status != "active" && strings.TrimSpace(reason) == "" {
		return errors.New("reason is required when suspending or expiring an offer")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
		var offer model.ProductOffer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND supplier_tenant_id = ?", offerID, supplierTenantID).First(&offer).Error; err != nil {
			return err
		}
		if offer.Status == "expired" && status == "active" {
			return errors.New("expired offer cannot be reactivated")
		}
		if status == "active" {
			var source model.Product
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND is_distributable = ?", offer.SourceProductID, supplierTenantID, "online", true).First(&source).Error; err != nil {
				return errors.New("source product is unavailable")
			}
			if source.ScenicAreaID == 0 || offer.ProductRevisionID == 0 || source.CurrentRevisionID != offer.ProductRevisionID {
				return errors.New("offer revision or scenic area is no longer valid")
			}
			var relationship model.DistributorRelationship
			if err := tx.Where("supplier_tenant_id = ? AND agent_tenant_id = ? AND status = ?", supplierTenantID, offer.DistributorTenantID, "active").First(&relationship).Error; err != nil {
				return errors.New("distribution relationship is not active")
			}
		}
		before := offer.Status
		if err := tx.Model(&offer).Update("status", status).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, supplierTenantID, "admin", "tenant", "distribution.offer.status", "product_offer", offer.ID, strings.TrimSpace(reason), `{"status":"`+before+`"}`, `{"status":"`+status+`"}`)
	})
}

func (s *DistributionService) ImportProduct(agentTenantID, sourceProductID uint, name string, price float64, productType string) error {
	name = strings.TrimSpace(name)
	if name == "" || price < 0 || (productType != "online" && productType != "offline") {
		return fmt.Errorf("invalid imported product")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, agentTenantID, "distributor"); err != nil {
			return err
		}
		var source model.Product
		if err := tx.First(&source, sourceProductID).Error; err != nil {
			return errors.New("source product not found")
		}
		var supplier model.Tenant
		if err := tx.Select("id", "status").First(&supplier, source.TenantID).Error; err != nil || (supplier.Status != "" && supplier.Status != "active") {
			return errors.New("supplier tenant is unavailable")
		}
		if !source.IsDistributable || source.Status != "online" {
			return errors.New("source product is not available for distribution")
		}
		if err := requireActiveTenantCapability(tx, source.TenantID, "supplier"); err != nil {
			return errors.New("supplier tenant is unavailable")
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", agentTenantID, source.TenantID, "active").First(&relationship).Error; err != nil {
			return errors.New("active distribution relationship not found")
		}
		var offer model.ProductOffer
		if err := tx.Where("supplier_tenant_id = ? AND distributor_tenant_id = ? AND source_product_id = ? AND status = ?", source.TenantID, agentTenantID, source.ID, "active").First(&offer).Error; err != nil {
			return errors.New("active supplier product offer not found")
		}
		now := time.Now()
		if (offer.SalesStartAt != nil && now.Before(*offer.SalesStartAt)) || (offer.SalesEndAt != nil && now.After(*offer.SalesEndAt)) {
			return errors.New("supplier product offer is outside its sales period")
		}
		if offer.ProductRevisionID == 0 || source.CurrentRevisionID != offer.ProductRevisionID {
			return errors.New("supplier product offer does not match the current product revision")
		}
		if offer.MinimumRetailPriceCents > 0 && moneyCents(price) < offer.MinimumRetailPriceCents {
			return fmt.Errorf("retail price must be at least %.2f", centsMoney(offer.MinimumRetailPriceCents))
		}
		var existingListing int64
		if err := tx.Model(&model.SellerListing{}).Where("seller_tenant_id = ? AND product_offer_id = ?", agentTenantID, offer.ID).Count(&existingListing).Error; err != nil {
			return err
		}
		if existingListing > 0 {
			return errors.New("product offer already imported")
		}

		// A distributor owns a sell-side listing, not the supplier's
		// fulfillment rules or checkpoints. Keep a local placeholder rule for
		// legacy product screens; order and verification always load the source
		// product rule from the supplier tenant.
		newRule := model.TicketRule{
			Name:         name + " (distribution listing)",
			TenantID:     agentTenantID,
			ValidityType: source.ValidityType,
		}
		if err := tx.Create(&newRule).Error; err != nil {
			return err
		}

		newProduct := model.Product{
			Name: name, Price: price, TenantID: agentTenantID, RuleID: newRule.ID, ScenicAreaID: source.ScenicAreaID,
			Type: productType, Status: "online", SettlementPrice: offer.SettlementPrice,
			SourceProductID: source.ID, SourceTenantID: source.TenantID,
			FulfillmentProductID: source.ID, FulfillmentTenantID: source.TenantID, FulfillmentScenicAreaID: source.ScenicAreaID,
			ProductOfferID: offer.ID,
		}
		if err := tx.Omit("Rule").Create(&newProduct).Error; err != nil {
			return err
		}
		return tx.Create(&model.SellerListing{
			SellerTenantID: agentTenantID, ProductOfferID: offer.ID, ProductID: newProduct.ID,
			Name: name, RetailPrice: price, RetailPriceCents: moneyCents(price), Status: "online",
		}).Error
	})
}

func (s *DistributionService) RechargeAgent(supplierTenantID, agentTenantID uint, amount float64, idempotencyKey string, operatorID uint) (*model.FinancialDocument, error) {
	amountCents := moneyCents(amount)
	if amountCents <= 0 {
		return nil, errors.New("recharge amount must be greater than zero")
	}
	return (&FinanceService{}).RechargeAccount(supplierTenantID, agentTenantID, amountCents, idempotencyKey, operatorID, "supplier recharge")
}
