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

type BundleService struct{}

type BundleComponentInput struct {
	SellerProductID       uint  `json:"seller_product_id"`
	Quantity              int   `json:"quantity"`
	RetailAllocationCents int64 `json:"retail_allocation_cents"`
}

type BundleUpsertInput struct {
	Name             string                 `json:"name"`
	Type             string                 `json:"type"`
	RetailPriceCents int64                  `json:"retail_price_cents"`
	Components       []BundleComponentInput `json:"components"`
}

type BundleComponentView struct {
	model.BundleComponent
	SellerProductName string `json:"seller_product_name"`
	SourceProductName string `json:"source_product_name"`
	SupplierName      string `json:"supplier_name"`
}

type BundleView struct {
	model.BundleProduct
	Version    int                   `json:"version"`
	Available  bool                  `json:"available"`
	Reason     string                `json:"reason,omitempty"`
	Components []BundleComponentView `json:"components"`
}

type BundleEligibleComponentView struct {
	SellerProductID          uint   `json:"seller_product_id"`
	SellerProductName        string `json:"seller_product_name"`
	ProductType              string `json:"product_type"`
	ProductOfferID           uint   `json:"product_offer_id"`
	SupplierTenantID         uint   `json:"supplier_tenant_id"`
	SupplierName             string `json:"supplier_name"`
	SourceProductID          uint   `json:"source_product_id"`
	SourceProductName        string `json:"source_product_name"`
	FulfillmentScenicAreaID  uint   `json:"fulfillment_scenic_area_id"`
	SettlementUnitPriceCents int64  `json:"settlement_unit_price_cents"`
	MinimumRetailPriceCents  int64  `json:"minimum_retail_price_cents"`
}

type bundleComponentFacts struct {
	component model.BundleComponent
	listing   model.Product
	offer     model.ProductOffer
	source    model.Product
}

func (s *BundleService) Create(sellerTenantID, operatorID uint, input BundleUpsertInput) (*BundleView, error) {
	var bundle model.BundleProduct
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, sellerTenantID, "distributor"); err != nil {
			return err
		}
		facts, err := validateBundleInputTx(tx, sellerTenantID, input)
		if err != nil {
			return err
		}
		bundle = model.BundleProduct{
			SellerTenantID: sellerTenantID, Name: strings.TrimSpace(input.Name), Type: input.Type,
			RetailPriceCents: input.RetailPriceCents, Status: "offline",
		}
		if err := tx.Create(&bundle).Error; err != nil {
			return err
		}
		version := model.BundleVersion{
			BundleProductID: bundle.ID, SellerTenantID: sellerTenantID, Version: 1,
			RetailPriceCents: input.RetailPriceCents, Status: "active",
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		if err := createBundleComponentsTx(tx, version.ID, facts); err != nil {
			return err
		}
		if err := tx.Model(&bundle).Update("current_version_id", version.ID).Error; err != nil {
			return err
		}
		bundle.CurrentVersionID = version.ID
		return recordAuditTx(tx, operatorID, sellerTenantID, "admin", "tenant", "distribution.bundle.create", "bundle_product", bundle.ID,
			"create cross-supplier bundle", "{}", fmt.Sprintf(`{"version":1,"retail_price_cents":%d}`, input.RetailPriceCents))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(sellerTenantID, bundle.ID)
}

func (s *BundleService) Revise(sellerTenantID, bundleID, operatorID uint, input BundleUpsertInput) (*BundleView, error) {
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, sellerTenantID, "distributor"); err != nil {
			return err
		}
		var bundle model.BundleProduct
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND seller_tenant_id = ?", bundleID, sellerTenantID).First(&bundle).Error; err != nil {
			return errors.New("bundle product not found")
		}
		facts, err := validateBundleInputTx(tx, sellerTenantID, input)
		if err != nil {
			return err
		}
		var latest model.BundleVersion
		if err := tx.Where("bundle_product_id = ? AND seller_tenant_id = ?", bundle.ID, sellerTenantID).Order("version DESC").First(&latest).Error; err != nil {
			return err
		}
		version := model.BundleVersion{
			BundleProductID: bundle.ID, SellerTenantID: sellerTenantID, Version: latest.Version + 1,
			RetailPriceCents: input.RetailPriceCents, Status: "active",
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		if err := createBundleComponentsTx(tx, version.ID, facts); err != nil {
			return err
		}
		if err := tx.Model(&model.BundleVersion{}).Where("bundle_product_id = ? AND id != ? AND status = ?", bundle.ID, version.ID, "active").Update("status", "retired").Error; err != nil {
			return err
		}
		before := fmt.Sprintf(`{"version":%d,"retail_price_cents":%d}`, latest.Version, bundle.RetailPriceCents)
		updates := map[string]interface{}{
			"name": strings.TrimSpace(input.Name), "type": input.Type, "retail_price_cents": input.RetailPriceCents,
			"current_version_id": version.ID, "status": "offline",
		}
		if err := tx.Model(&bundle).Updates(updates).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, sellerTenantID, "admin", "tenant", "distribution.bundle.revise", "bundle_product", bundle.ID,
			"revise bundle; re-enable after validation", before, fmt.Sprintf(`{"version":%d,"retail_price_cents":%d}`, version.Version, input.RetailPriceCents))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(sellerTenantID, bundleID)
}

func (s *BundleService) SetStatus(sellerTenantID, bundleID, operatorID uint, status, reason string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "online" && status != "offline" {
		return errors.New("invalid bundle status")
	}
	if status == "offline" && strings.TrimSpace(reason) == "" {
		return errors.New("reason is required when taking a bundle offline")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, sellerTenantID, "distributor"); err != nil {
			return err
		}
		var bundle model.BundleProduct
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND seller_tenant_id = ?", bundleID, sellerTenantID).First(&bundle).Error; err != nil {
			return errors.New("bundle product not found")
		}
		if status == "online" {
			if _, err := loadSellableBundleTx(tx, sellerTenantID, bundle.ID, bundle.Type, true); err != nil {
				return err
			}
		}
		before := bundle.Status
		if err := tx.Model(&bundle).Update("status", status).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, operatorID, sellerTenantID, "admin", "tenant", "distribution.bundle.status", "bundle_product", bundle.ID,
			strings.TrimSpace(reason), fmt.Sprintf(`{"status":%q}`, before), fmt.Sprintf(`{"status":%q}`, status))
	})
}

func (s *BundleService) List(sellerTenantID uint, productType, status string) ([]BundleView, error) {
	query := model.DB.Where("seller_tenant_id = ?", sellerTenantID)
	if productType = strings.TrimSpace(productType); productType != "" {
		query = query.Where("type = ?", productType)
	}
	if status = strings.TrimSpace(status); status != "" {
		query = query.Where("status = ?", status)
	}
	var bundles []model.BundleProduct
	if err := query.Order("updated_at DESC").Find(&bundles).Error; err != nil {
		return nil, err
	}
	views := make([]BundleView, 0, len(bundles))
	for i := range bundles {
		view, err := s.Get(sellerTenantID, bundles[i].ID)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (s *BundleService) Get(sellerTenantID, bundleID uint) (*BundleView, error) {
	var bundle model.BundleProduct
	if err := model.DB.Where("id = ? AND seller_tenant_id = ?", bundleID, sellerTenantID).First(&bundle).Error; err != nil {
		return nil, err
	}
	var version model.BundleVersion
	if err := model.DB.Where("id = ? AND bundle_product_id = ? AND seller_tenant_id = ?", bundle.CurrentVersionID, bundle.ID, sellerTenantID).First(&version).Error; err != nil {
		return nil, err
	}
	components, err := bundleComponentViews(model.DB, sellerTenantID, version.ID)
	if err != nil {
		return nil, err
	}
	view := &BundleView{BundleProduct: bundle, Version: version.Version, Available: true, Components: components}
	if _, err := loadSellableBundleTx(model.DB, sellerTenantID, bundle.ID, bundle.Type, true); err != nil {
		view.Available = false
		view.Reason = err.Error()
	}
	return view, nil
}

func (s *BundleService) ListEligibleComponents(sellerTenantID uint, productType string) ([]BundleEligibleComponentView, error) {
	if err := requireActiveTenantCapability(model.DB, sellerTenantID, "distributor"); err != nil {
		return nil, err
	}
	productType = strings.TrimSpace(productType)
	if productType != "" && productType != "online" && productType != "offline" {
		return nil, errors.New("invalid product type")
	}
	type row struct {
		SellerProductID, ProductOfferID, SupplierTenantID, SourceProductID, FulfillmentScenicAreaID uint
		SellerProductName, ProductType, SupplierName, SourceProductName, AllowedChannels            string
		SettlementPrice                                                                             float64
		MinimumRetailPriceCents                                                                     int64
	}
	var rows []row
	query := model.DB.Table("seller_listings AS l").
		Select(`p.id seller_product_id, p.name seller_product_name, p.type product_type, o.id product_offer_id,
		 o.supplier_tenant_id, supplier.name supplier_name, source.id source_product_id, source.name source_product_name,
		 o.fulfillment_scenic_area_id, o.settlement_price, o.minimum_retail_price_cents, o.allowed_channels`).
		Joins("JOIN products p ON p.id = l.product_id AND p.tenant_id = l.seller_tenant_id AND p.status = 'online'").
		Joins("JOIN product_offers o ON o.id = l.product_offer_id AND o.distributor_tenant_id = l.seller_tenant_id AND o.status = 'active'").
		Joins("JOIN products source ON source.id = o.source_product_id AND source.tenant_id = o.supplier_tenant_id AND source.status = 'online' AND source.is_distributable = 1 AND source.current_revision_id = o.product_revision_id").
		Joins("JOIN tenants supplier ON supplier.id = o.supplier_tenant_id AND supplier.status = 'active'").
		Joins("JOIN tenant_capabilities supplier_capability ON supplier_capability.tenant_id = o.supplier_tenant_id AND supplier_capability.capability = 'supplier' AND supplier_capability.status = 'active'").
		Joins("JOIN distributor_relationships r ON r.agent_tenant_id = l.seller_tenant_id AND r.supplier_tenant_id = o.supplier_tenant_id AND r.status = 'active'").
		Where("l.seller_tenant_id = ? AND l.status = ?", sellerTenantID, "online").
		Where("(o.sales_start_at IS NULL OR o.sales_start_at <= ?) AND (o.sales_end_at IS NULL OR o.sales_end_at >= ?)", time.Now(), time.Now())
	if productType != "" {
		query = query.Where("p.type = ?", productType)
	}
	if err := query.Order("supplier.name, p.name").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]BundleEligibleComponentView, 0, len(rows))
	for _, item := range rows {
		channel := "online"
		if item.ProductType == "offline" {
			channel = "window"
		}
		if !offerAllowsChannel(item.AllowedChannels, channel) {
			continue
		}
		result = append(result, BundleEligibleComponentView{
			SellerProductID: item.SellerProductID, SellerProductName: item.SellerProductName, ProductType: item.ProductType,
			ProductOfferID: item.ProductOfferID, SupplierTenantID: item.SupplierTenantID, SupplierName: item.SupplierName,
			SourceProductID: item.SourceProductID, SourceProductName: item.SourceProductName,
			FulfillmentScenicAreaID: item.FulfillmentScenicAreaID, SettlementUnitPriceCents: moneyCents(item.SettlementPrice),
			MinimumRetailPriceCents: item.MinimumRetailPriceCents,
		})
	}
	return result, nil
}

func validateBundleInputTx(tx *gorm.DB, sellerTenantID uint, input BundleUpsertInput) ([]bundleComponentFacts, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 {
		return nil, errors.New("bundle name is required and must not exceed 100 characters")
	}
	if input.Type != "online" && input.Type != "offline" {
		return nil, errors.New("bundle type must be online or offline")
	}
	if input.RetailPriceCents <= 0 {
		return nil, errors.New("bundle retail price must be greater than zero")
	}
	if len(input.Components) < 2 || len(input.Components) > 20 {
		return nil, errors.New("bundle must contain between 2 and 20 components")
	}
	seenProducts := make(map[uint]struct{}, len(input.Components))
	suppliers := make(map[uint]struct{})
	var allocationTotal int64
	facts := make([]bundleComponentFacts, 0, len(input.Components))
	for _, item := range input.Components {
		if item.SellerProductID == 0 || item.Quantity <= 0 || item.Quantity > 100 || item.RetailAllocationCents <= 0 {
			return nil, errors.New("each bundle component requires a product, quantity and positive price allocation")
		}
		if _, exists := seenProducts[item.SellerProductID]; exists {
			return nil, errors.New("a seller product can appear only once in a bundle")
		}
		seenProducts[item.SellerProductID] = struct{}{}
		if item.RetailAllocationCents%int64(item.Quantity) != 0 {
			return nil, fmt.Errorf("component %d price allocation must divide evenly by quantity", item.SellerProductID)
		}
		var listing model.Product
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND type = ? AND product_offer_id > 0", item.SellerProductID, sellerTenantID, "online", input.Type).First(&listing).Error; err != nil {
			return nil, fmt.Errorf("seller product %d is unavailable or has the wrong sale type", item.SellerProductID)
		}
		var sellerListing model.SellerListing
		if err := tx.Where("seller_tenant_id = ? AND product_id = ? AND product_offer_id = ? AND status = ?", sellerTenantID, listing.ID, listing.ProductOfferID, "online").First(&sellerListing).Error; err != nil {
			return nil, fmt.Errorf("seller product %s is not an active imported listing", listing.Name)
		}
		var offer model.ProductOffer
		if err := tx.Where("id = ? AND distributor_tenant_id = ? AND status = ?", listing.ProductOfferID, sellerTenantID, "active").First(&offer).Error; err != nil {
			return nil, fmt.Errorf("supplier offer for %s is unavailable", listing.Name)
		}
		if err := validateBundleOfferTx(tx, &offer, input.Type); err != nil {
			return nil, fmt.Errorf("%s: %w", listing.Name, err)
		}
		var source model.Product
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND is_distributable = ?", offer.SourceProductID, offer.SupplierTenantID, "online", true).First(&source).Error; err != nil {
			return nil, fmt.Errorf("source product for %s is unavailable", listing.Name)
		}
		if source.CurrentRevisionID == 0 || source.CurrentRevisionID != offer.ProductRevisionID || source.ScenicAreaID != offer.FulfillmentScenicAreaID {
			return nil, fmt.Errorf("source product for %s changed; synchronize the listing first", listing.Name)
		}
		minimum := offer.MinimumRetailPriceCents * int64(item.Quantity)
		if item.RetailAllocationCents < minimum {
			return nil, fmt.Errorf("component %s allocation must be at least %.2f", listing.Name, centsMoney(minimum))
		}
		allocationTotal += item.RetailAllocationCents
		suppliers[offer.SupplierTenantID] = struct{}{}
		facts = append(facts, bundleComponentFacts{
			listing: listing, offer: offer, source: source,
			component: model.BundleComponent{
				SellerTenantID: sellerTenantID, SellerProductID: listing.ID, ProductOfferID: offer.ID,
				SupplierTenantID: offer.SupplierTenantID, SourceProductID: source.ID, ProductRevisionID: offer.ProductRevisionID,
				FulfillmentScenicAreaID: offer.FulfillmentScenicAreaID, Quantity: item.Quantity,
				RetailAllocationCents: item.RetailAllocationCents, SettlementUnitPriceCents: moneyCents(offer.SettlementPrice),
				CommissionBPS: offer.CommissionBPS,
			},
		})
	}
	if len(suppliers) < 2 {
		return nil, errors.New("cross-supplier bundle must contain products from at least two suppliers")
	}
	if allocationTotal != input.RetailPriceCents {
		return nil, fmt.Errorf("component price allocations total %d cents, expected %d cents", allocationTotal, input.RetailPriceCents)
	}
	return facts, nil
}

func validateBundleOfferTx(tx *gorm.DB, offer *model.ProductOffer, productType string) error {
	if offer == nil || offer.ID == 0 || offer.Status != "active" {
		return errors.New("supplier offer is not active")
	}
	now := time.Now()
	if (offer.SalesStartAt != nil && now.Before(*offer.SalesStartAt)) || (offer.SalesEndAt != nil && now.After(*offer.SalesEndAt)) {
		return errors.New("supplier offer is outside its sales period")
	}
	channel := "online"
	if productType == "offline" {
		channel = "window"
	}
	if !offerAllowsChannel(offer.AllowedChannels, channel) {
		return fmt.Errorf("supplier offer does not allow %s sales", channel)
	}
	if err := requireActiveTenantCapability(tx, offer.SupplierTenantID, "supplier"); err != nil {
		return errors.New("supplier is unavailable")
	}
	var relationship model.DistributorRelationship
	if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", offer.DistributorTenantID, offer.SupplierTenantID, "active").First(&relationship).Error; err != nil {
		return errors.New("distribution relationship is not active")
	}
	return nil
}

func createBundleComponentsTx(tx *gorm.DB, versionID uint, facts []bundleComponentFacts) error {
	components := make([]model.BundleComponent, len(facts))
	for i := range facts {
		components[i] = facts[i].component
		components[i].BundleVersionID = versionID
	}
	return tx.Create(&components).Error
}

func bundleComponentViews(db *gorm.DB, sellerTenantID, versionID uint) ([]BundleComponentView, error) {
	var components []model.BundleComponent
	if err := db.Where("bundle_version_id = ? AND seller_tenant_id = ?", versionID, sellerTenantID).Order("id ASC").Find(&components).Error; err != nil {
		return nil, err
	}
	views := make([]BundleComponentView, 0, len(components))
	for i := range components {
		var listing, source model.Product
		var supplier model.Tenant
		if err := db.Unscoped().Select("id", "name").First(&listing, components[i].SellerProductID).Error; err != nil {
			return nil, err
		}
		if err := db.Unscoped().Select("id", "name").First(&source, components[i].SourceProductID).Error; err != nil {
			return nil, err
		}
		if err := db.Select("id", "name").First(&supplier, components[i].SupplierTenantID).Error; err != nil {
			return nil, err
		}
		views = append(views, BundleComponentView{BundleComponent: components[i], SellerProductName: listing.Name, SourceProductName: source.Name, SupplierName: supplier.Name})
	}
	return views, nil
}

// loadSellableBundleTx validates the current component facts. ignoreStatus is
// used by management health checks and by the transition from offline to online.
func loadSellableBundleTx(tx *gorm.DB, sellerTenantID, bundleID uint, productType string, ignoreStatus bool) (*model.BundleVersion, error) {
	var bundle model.BundleProduct
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND seller_tenant_id = ?", bundleID, sellerTenantID)
	if !ignoreStatus {
		query = query.Where("status = ?", "online")
	}
	if err := query.First(&bundle).Error; err != nil {
		return nil, errors.New("bundle product is unavailable")
	}
	if productType != "" && bundle.Type != productType {
		return nil, errors.New("bundle product is not available in this sales channel")
	}
	var version model.BundleVersion
	if err := tx.Where("id = ? AND bundle_product_id = ? AND seller_tenant_id = ? AND status = ?", bundle.CurrentVersionID, bundle.ID, sellerTenantID, "active").First(&version).Error; err != nil {
		return nil, errors.New("bundle version is unavailable")
	}
	var components []model.BundleComponent
	if err := tx.Where("bundle_version_id = ? AND seller_tenant_id = ?", version.ID, sellerTenantID).Order("id ASC").Find(&components).Error; err != nil {
		return nil, err
	}
	if len(components) < 2 {
		return nil, errors.New("bundle has no valid component set")
	}
	for i := range components {
		component := &components[i]
		var listing model.Product
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND type = ? AND product_offer_id = ?", component.SellerProductID, sellerTenantID, "online", bundle.Type, component.ProductOfferID).First(&listing).Error; err != nil {
			return nil, errors.New("a bundle component listing is unavailable")
		}
		var offer model.ProductOffer
		if err := tx.Where("id = ? AND distributor_tenant_id = ? AND supplier_tenant_id = ? AND source_product_id = ?", component.ProductOfferID, sellerTenantID, component.SupplierTenantID, component.SourceProductID).First(&offer).Error; err != nil {
			return nil, errors.New("a bundle component offer is unavailable")
		}
		if err := validateBundleOfferTx(tx, &offer, bundle.Type); err != nil {
			return nil, err
		}
		if offer.ProductRevisionID != component.ProductRevisionID || offer.FulfillmentScenicAreaID != component.FulfillmentScenicAreaID || moneyCents(offer.SettlementPrice) != component.SettlementUnitPriceCents || offer.CommissionBPS != component.CommissionBPS {
			return nil, errors.New("a supplier changed bundle terms; revise the bundle before selling it again")
		}
		var source model.Product
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND is_distributable = ? AND current_revision_id = ? AND scenic_area_id = ?", component.SourceProductID, component.SupplierTenantID, "online", true, component.ProductRevisionID, component.FulfillmentScenicAreaID).First(&source).Error; err != nil {
			return nil, errors.New("a supplier product changed; revise the bundle before selling it again")
		}
		if component.RetailAllocationCents < offer.MinimumRetailPriceCents*int64(component.Quantity) {
			return nil, errors.New("a bundle component price is below the supplier minimum retail price")
		}
	}
	version.Components = components
	return &version, nil
}

func expandBundleOrderItemsTx(tx *gorm.DB, order *model.Order) error {
	if order == nil {
		return errors.New("order is required")
	}
	productType := "online"
	externalBundleUnsupported := false
	if order.Channel == "window" {
		productType = "offline"
	} else if order.ChannelAccountID != 0 || order.Channel == "ota" {
		externalBundleUnsupported = true
	}
	expanded := make([]model.OrderItem, 0, len(order.Items))
	for itemIndex := range order.Items {
		requested := order.Items[itemIndex]
		if requested.BundleProductID == 0 {
			expanded = append(expanded, requested)
			continue
		}
		if externalBundleUnsupported {
			return errors.New("external channel bundle sales require a bundle-aware channel mapping")
		}
		if requested.ProductID != 0 || requested.Quantity <= 0 || requested.Quantity > 100 {
			return errors.New("bundle order item is invalid")
		}
		if len(requested.Visitors) != 0 && len(requested.Visitors) != requested.Quantity {
			return errors.New("bundle visitor count must equal bundle quantity")
		}
		version, err := loadSellableBundleTx(tx, order.TenantID, requested.BundleProductID, productType, false)
		if err != nil {
			return err
		}
		var bundle model.BundleProduct
		if err := tx.Where("id = ? AND seller_tenant_id = ?", requested.BundleProductID, order.TenantID).First(&bundle).Error; err != nil {
			return err
		}
		visitorCounts := make(map[uint]int, len(version.Components))
		if len(requested.Visitors) > 0 {
			for componentIndex := range version.Components {
				component := version.Components[componentIndex]
				var source model.Product
				if err := tx.Select("id", "code_mode").Where("id = ? AND tenant_id = ?", component.SourceProductID, component.SupplierTenantID).First(&source).Error; err != nil {
					return errors.New("bundle component source product is unavailable")
				}
				visitorCounts[component.ID] = component.Quantity
				if source.CodeMode == "order" {
					visitorCounts[component.ID] = 1
				}
			}
		}
		for unit := 0; unit < requested.Quantity; unit++ {
			for componentIndex := range version.Components {
				component := version.Components[componentIndex]
				visitors := []model.VisitorInput(nil)
				if len(requested.Visitors) > 0 {
					visitors = make([]model.VisitorInput, visitorCounts[component.ID])
					for i := range visitors {
						visitors[i] = requested.Visitors[unit]
					}
				}
				expanded = append(expanded, model.OrderItem{
					ProductID: component.SellerProductID, Quantity: component.Quantity,
					UseDate: requested.UseDate, StockSlot: requested.StockSlot,
					VisitorName: requested.VisitorName, VisitorPhone: requested.VisitorPhone,
					VisitorID: requested.VisitorID, VisitorRegion: requested.VisitorRegion, Visitors: visitors,
					BundleProductID: bundle.ID, BundleVersionID: version.ID, BundleComponentID: component.ID,
					BundleName: bundle.Name, BundleUnitQuantity: 1,
				})
			}
		}
	}
	if len(expanded) > 2000 {
		return errors.New("expanded bundle order contains too many items")
	}
	order.Items = expanded
	return nil
}

func bundleComponentForOrderTx(tx *gorm.DB, sellerTenantID uint, item *model.OrderItem) (*model.BundleComponent, error) {
	if item == nil || item.BundleProductID == 0 || item.BundleVersionID == 0 || item.BundleComponentID == 0 {
		return nil, errors.New("bundle component snapshot is incomplete")
	}
	var component model.BundleComponent
	if err := tx.Where("id = ? AND bundle_version_id = ? AND seller_tenant_id = ? AND seller_product_id = ?",
		item.BundleComponentID, item.BundleVersionID, sellerTenantID, item.ProductID).First(&component).Error; err != nil {
		return nil, errors.New("bundle component no longer matches the selected bundle version")
	}
	var version model.BundleVersion
	if err := tx.Where("id = ? AND bundle_product_id = ? AND seller_tenant_id = ?", item.BundleVersionID, item.BundleProductID, sellerTenantID).First(&version).Error; err != nil {
		return nil, errors.New("bundle version ownership mismatch")
	}
	if component.Quantity <= 0 || component.RetailAllocationCents <= 0 || component.RetailAllocationCents%int64(component.Quantity) != 0 {
		return nil, errors.New("bundle component price allocation is invalid")
	}
	return &component, nil
}
