package service

import (
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func seedPackageDistributor(t *testing.T, supplierTenantID uint) uint {
	t.Helper()
	var distributorID uint
	err := model.Write(func(tx *gorm.DB) error {
		distributor := model.Tenant{Name: "Package Distributor", SystemCode: fmt.Sprintf("PKG-DIST-%d", time.Now().UnixNano()), SecretKey: "package-distributor-secret", Status: "active"}
		if err := tx.Create(&distributor).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.TenantCapability{TenantID: distributor.ID, Capability: "distributor", Status: "active"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.DistributorRelationship{AgentTenantID: distributor.ID, SupplierTenantID: supplierTenantID, Status: "active"}).Error; err != nil {
			return err
		}
		distributorID = distributor.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return distributorID
}

func seedLegacyPackageOffer(t *testing.T, fixture packageFixture, distributorID uint, status string) model.ProductOffer {
	t.Helper()
	var offer model.ProductOffer
	err := model.Write(func(tx *gorm.DB) error {
		var source model.Product
		if err := tx.Preload("Rule").Preload("Rule.Groups").Preload("Rule.Groups.Items").First(&source, fixture.productID).Error; err != nil {
			return err
		}
		if err := tx.Model(&source).Update("is_distributable", true).Error; err != nil {
			return err
		}
		source.IsDistributable = true
		revision, err := ensureProductRevisionTx(tx, &source)
		if err != nil {
			return err
		}
		offer = model.ProductOffer{
			SupplierTenantID: fixture.tenantID, DistributorTenantID: distributorID,
			SourceProductID: fixture.productID, ProductRevisionID: revision.ID,
			FulfillmentScenicAreaID: source.ScenicAreaID, SettlementPrice: 80,
			MinimumRetailPriceCents: 8000, Status: status, AllowedChannels: "online,ota",
		}
		return tx.Create(&offer).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func seedLegacyPackageListing(t *testing.T, fixture packageFixture, distributorID uint, offer model.ProductOffer, status string) (model.Product, model.SellerListing) {
	t.Helper()
	var product model.Product
	var listing model.SellerListing
	err := model.Write(func(tx *gorm.DB) error {
		rule := model.TicketRule{Name: "Legacy package listing rule", TenantID: distributorID, ValidityType: "date"}
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		var source model.Product
		if err := tx.First(&source, fixture.productID).Error; err != nil {
			return err
		}
		product = model.Product{
			Name: "Legacy package listing", Price: 99, SettlementPrice: offer.SettlementPrice,
			TenantID: distributorID, RuleID: rule.ID, ScenicAreaID: source.ScenicAreaID,
			Type: "online", Status: status, SourceProductID: source.ID, SourceTenantID: fixture.tenantID,
			FulfillmentProductID: source.ID, FulfillmentTenantID: fixture.tenantID,
			FulfillmentScenicAreaID: source.ScenicAreaID, ProductOfferID: offer.ID,
		}
		if err := tx.Omit("Rule").Create(&product).Error; err != nil {
			return err
		}
		listing = model.SellerListing{
			SellerTenantID: distributorID, ProductOfferID: offer.ID, ProductID: product.ID,
			Name: product.Name, RetailPrice: product.Price, RetailPriceCents: 9900, Status: status,
		}
		return tx.Create(&listing).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return product, listing
}

func TestScenicHotelPackageCannotCreateDistributionOffer(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", fixture.productID).Update("is_distributable", true).Error; err != nil {
		t.Fatal(err)
	}

	_, err := (&DistributionService{}).CreateOffer(fixture.tenantID, distributorID, fixture.productID, 80, 0, "online,ota", nil, nil)
	if !errors.Is(err, ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatalf("create package distribution offer error=%v", err)
	}
	var offers int64
	if err := model.DB.Model(&model.ProductOffer{}).Where("source_product_id = ?", fixture.productID).Count(&offers).Error; err != nil {
		t.Fatal(err)
	}
	if offers != 0 {
		t.Fatalf("unsupported package left %d distribution offers", offers)
	}
}

func TestScenicHotelPackageCannotBeImportedFromLegacyOffer(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	seedLegacyPackageOffer(t, fixture, distributorID, "active")

	err := (&DistributionService{}).ImportProduct(distributorID, fixture.productID, "不完整的酒景套餐", 99, "online")
	if !errors.Is(err, ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatalf("import package from legacy offer error=%v", err)
	}
	var listings int64
	if err := model.DB.Model(&model.Product{}).Where("tenant_id = ? AND source_product_id = ?", distributorID, fixture.productID).Count(&listings).Error; err != nil {
		t.Fatal(err)
	}
	if listings != 0 {
		t.Fatalf("unsupported package left %d distributor listings", listings)
	}
}

func TestLegacyScenicHotelPackageOfferCannotBeReactivated(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	offer := seedLegacyPackageOffer(t, fixture, distributorID, "suspended")

	err := (&DistributionService{}).SetOfferStatus(fixture.tenantID, offer.ID, 1, "active", "legacy retry")
	if !errors.Is(err, ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatalf("reactivate legacy package offer error=%v", err)
	}
	if err := model.DB.First(&offer, offer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if offer.Status != "suspended" {
		t.Fatalf("legacy package offer status=%q, want suspended", offer.Status)
	}
}

func TestLegacyScenicHotelPackageIsHiddenFromDistributableProducts(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	seedLegacyPackageOffer(t, fixture, distributorID, "active")

	products, err := (&DistributionService{}).ListDistributableProducts(distributorID, fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 0 {
		t.Fatalf("legacy package remained distributable: %+v", products)
	}
}

func TestLegacyScenicHotelPackageListingCannotBePutOnlineThroughUpdate(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	offer := seedLegacyPackageOffer(t, fixture, distributorID, "active")
	product, _ := seedLegacyPackageListing(t, fixture, distributorID, offer, "offline")
	product.Status = "online"

	err := (&ProductService{}).Update(product.ID, distributorID, &product, nil)
	if !errors.Is(err, ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatalf("put legacy package listing online through product update error=%v", err)
	}
	var stored model.Product
	if err := model.DB.First(&stored, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "offline" {
		t.Fatalf("legacy package listing status=%q, want offline", stored.Status)
	}
}

func TestLegacyScenicHotelPackageListingCannotBeSyncedOnlineOrSold(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	offer := seedLegacyPackageOffer(t, fixture, distributorID, "active")
	product, listing := seedLegacyPackageListing(t, fixture, distributorID, offer, "offline")

	result, err := (&DistributionService{}).SyncListing(distributorID, listing.ID, 1, "legacy package sync")
	if err != nil {
		t.Fatal(err)
	}
	if result.Eligible || result.ListingStatus != "offline" {
		t.Fatalf("legacy package listing sync result=%+v", result)
	}
	if !errors.Is((&ProductService{}).UpdateStatus(product.ID, distributorID, "online"), ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatal("legacy package listing was manually put online")
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ?", product.ID).Update("status", "online").Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn
	order := model.Order{TenantID: distributorID, Channel: "online", ContactName: "Legacy guest", ContactPhone: "13800138000", Items: []model.OrderItem{{ProductID: product.ID, Quantity: 1, UseDate: &useDate}}}
	if !errors.Is((&OrderService{}).Create(&order), ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatal("legacy package listing produced a ticket-only order")
	}
	var orderCount int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ?", distributorID).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("unsupported legacy package produced %d orders", orderCount)
	}
}

func TestCoreChannelCannotReserveScenicHotelPackage(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	account := model.ChannelAccount{
		TenantID: fixture.tenantID, Code: fmt.Sprintf("PKG-CORE-%d", time.Now().UnixNano()),
		Type: "ota", Status: "active", Environment: "production",
	}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn

	_, err := (&ChannelWorkflowService{}).Reserve(fixture.tenantID, account.ID, "ota", fixture.productID, "PACKAGE-CORE-HOLD-1", 1, &useDate, "", time.Minute)
	if !errors.Is(err, ErrScenicHotelPackageChannelReservationUnsupported) {
		t.Fatalf("reserve package through core channel error=%v", err)
	}
	var reservations int64
	if err := model.DB.Model(&model.ChannelReservation{}).Where("channel_account_id = ?", account.ID).Count(&reservations).Error; err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("unsupported package left %d channel reservations", reservations)
	}
	for _, row := range loadPackageInventory(t, fixture) {
		if row.Reserved != 0 || row.Sold != 0 {
			t.Fatalf("unsupported channel reservation changed hotel inventory: %+v", row)
		}
	}
}

func TestCoreChannelChecksFulfillmentProductForLegacyDistributedPackage(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 2)
	distributorID := seedPackageDistributor(t, fixture.tenantID)
	offer := seedLegacyPackageOffer(t, fixture, distributorID, "active")
	product, _ := seedLegacyPackageListing(t, fixture, distributorID, offer, "online")
	account := model.ChannelAccount{
		TenantID: distributorID, Code: fmt.Sprintf("PKG-DIST-CORE-%d", time.Now().UnixNano()),
		Type: "ota", Status: "active", Environment: "production",
	}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	useDate := fixture.checkIn

	_, err := (&ChannelWorkflowService{}).Reserve(distributorID, account.ID, "ota", product.ID, "PACKAGE-DIST-CORE-HOLD-1", 1, &useDate, "", time.Minute)
	if !errors.Is(err, ErrScenicHotelPackageDistributionUnsupported) {
		t.Fatalf("reserve legacy distributed package through core channel error=%v", err)
	}
	var reservations int64
	if err := model.DB.Model(&model.ChannelReservation{}).Where("channel_account_id = ?", account.ID).Count(&reservations).Error; err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("legacy distributed package left %d channel reservations", reservations)
	}
}
