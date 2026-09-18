package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type commerceBoundaryDomainFixture struct {
	tenantID     uint
	businessType string
	product      *model.CommerceProduct
	sku          model.CommerceSKU
	group        *model.CommerceOptionGroup
	option       *model.CommerceOption
	location     *model.CommerceFulfillmentLocation
	stock        *model.CommerceInventory
	cart         *model.CommerceCart
	cartItemID   uint
}

// createCommerceBoundaryDomainFixture builds one complete commercial slice
// without resetting the database. The caller owns the tenant setup so the
// same tenant can be exercised with both independent business domains.
func createCommerceBoundaryDomainFixture(t *testing.T, tenantID uint, businessType string) commerceBoundaryDomainFixture {
	t.Helper()
	catalog := &CommerceCatalogService{}
	input := commerceProductInput(fmt.Sprintf("Boundary %s product", businessType))
	input.BusinessType = businessType
	input.SKUs[0].SKUCode = fmt.Sprintf("BOUNDARY-%s-SKU", businessType)
	input.SKUs[0].Name = fmt.Sprintf("Boundary %s SKU", businessType)
	product, err := catalog.CreateProduct(tenantID, input)
	if err != nil {
		t.Fatalf("create %s commerce product: %v", businessType, err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish %s commerce product: %v", businessType, err)
	}

	ops := &CommerceOperationsService{}
	group, err := ops.CreateOptionGroup(tenantID, product.ID, CreateCommerceOptionGroupInput{Name: "Boundary options", MaxSelections: 1})
	if err != nil {
		t.Fatalf("create %s option group: %v", businessType, err)
	}
	option, err := ops.CreateOption(tenantID, group.ID, CreateCommerceOptionInput{Name: "Default"})
	if err != nil {
		t.Fatalf("create %s option: %v", businessType, err)
	}

	locationType := "store"
	if businessType == "retail" {
		locationType = "warehouse"
	}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{
		BusinessType: businessType,
		Name:         fmt.Sprintf("Boundary %s location", businessType),
		LocationType: locationType,
	})
	if err != nil {
		t.Fatalf("create %s location: %v", businessType, err)
	}
	stock, err := ops.SetInventory(tenantID, CommerceInventoryInput{
		SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 20,
	})
	if err != nil {
		t.Fatalf("set %s inventory: %v", businessType, err)
	}
	cart, err := ops.GetOrCreateCart(tenantID, CommerceCartInput{
		BusinessType: businessType, CustomerID: "boundary-customer", LocationID: location.ID,
	})
	if err != nil {
		t.Fatalf("create %s cart: %v", businessType, err)
	}
	cart, err = ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{
		ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 1, OptionIDs: []uint{option.ID},
	})
	if err != nil || len(cart.Items) != 1 {
		t.Fatalf("add %s cart item: cart=%+v err=%v", businessType, cart, err)
	}

	return commerceBoundaryDomainFixture{
		tenantID: tenantID, businessType: businessType, product: product, sku: product.SKUs[0],
		group: group, option: option, location: location, stock: stock, cart: cart,
		cartItemID: cart.Items[0].ID,
	}
}

func newCommerceBoundaryDualFixture(t *testing.T) (commerceBoundaryDomainFixture, commerceBoundaryDomainFixture) {
	t.Helper()
	tenantID := newCommerceTenant(t, "restaurant", "active")
	if err := model.DB.Create(&model.TenantBusinessCapability{
		TenantID: tenantID, BusinessType: "retail", Status: "active",
	}).Error; err != nil {
		t.Fatalf("create retail capability: %v", err)
	}
	return createCommerceBoundaryDomainFixture(t, tenantID, "restaurant"),
		createCommerceBoundaryDomainFixture(t, tenantID, "retail")
}

func createCommerceBoundaryTenant(t *testing.T, businessType string) uint {
	t.Helper()
	tenant := model.Tenant{
		Name:       fmt.Sprintf("Boundary %s tenant", businessType),
		SystemCode: fmt.Sprintf("COMMERCE-BOUNDARY-%s-%d", businessType, time.Now().UnixNano()),
		SecretKey:  "boundary-secret",
		Status:     "active",
	}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatalf("create foreign tenant: %v", err)
	}
	if err := model.DB.Create(&model.TenantBusinessCapability{
		TenantID: tenant.ID, BusinessType: businessType, Status: "active",
	}).Error; err != nil {
		t.Fatalf("create foreign capability: %v", err)
	}
	return tenant.ID
}

func TestCommerceBoundarySuspensionFailsClosedForAllDomainOperations(t *testing.T) {
	for _, businessType := range []string{"restaurant", "retail"} {
		t.Run(businessType, func(t *testing.T) {
			fixture := createCommerceBoundaryDomainFixture(t, newCommerceTenant(t, businessType, "active"), businessType)
			if err := model.DB.Model(&model.TenantBusinessCapability{}).
				Where("tenant_id = ? AND business_type = ?", fixture.tenantID, businessType).
				Update("status", "suspended").Error; err != nil {
				t.Fatalf("suspend %s capability: %v", businessType, err)
			}

			catalog := &CommerceCatalogService{}
			ops := &CommerceOperationsService{}
			checks := []struct {
				name string
				call func() error
			}{
				{"list products", func() error {
					_, err := catalog.ListProducts(fixture.tenantID, businessType, "", "")
					return err
				}},
				{"unqualified product list", func() error {
					_, err := catalog.ListProducts(fixture.tenantID, "", "", "")
					return err
				}},
				{"get product", func() error {
					_, err := catalog.GetProduct(fixture.tenantID, fixture.product.ID)
					return err
				}},
				{"set product status", func() error {
					_, err := catalog.SetProductStatus(fixture.tenantID, fixture.product.ID, "offline")
					return err
				}},
				{"create product", func() error {
					input := commerceProductInput("Blocked product")
					input.BusinessType = businessType
					input.SKUs[0].SKUCode = "BLOCKED-SKU"
					_, err := catalog.CreateProduct(fixture.tenantID, input)
					return err
				}},
				{"create sku", func() error {
					_, err := catalog.CreateSKU(fixture.tenantID, fixture.product.ID, CreateCommerceSKUInput{
						SKUCode: "BLOCKED-SKU-2", Name: "Blocked", OriginalPriceCents: 100, PriceCents: 100,
					})
					return err
				}},
				{"update sku", func() error {
					_, err := catalog.UpdateSKU(fixture.tenantID, fixture.sku.ID, UpdateCommerceSKUInput{
						SKUCode: "BLOCKED-SKU-3", Name: "Blocked", OriginalPriceCents: 100, PriceCents: 100,
					})
					return err
				}},
				{"list option groups", func() error {
					_, err := ops.ListOptionGroups(fixture.tenantID, fixture.product.ID)
					return err
				}},
				{"create option group", func() error {
					_, err := ops.CreateOptionGroup(fixture.tenantID, fixture.product.ID, CreateCommerceOptionGroupInput{Name: "Blocked group"})
					return err
				}},
				{"create option", func() error {
					_, err := ops.CreateOption(fixture.tenantID, fixture.group.ID, CreateCommerceOptionInput{Name: "Blocked option"})
					return err
				}},
				{"list locations", func() error {
					_, err := ops.ListLocations(fixture.tenantID, businessType, "")
					return err
				}},
				{"unqualified location list", func() error {
					_, err := ops.ListLocations(fixture.tenantID, "", "")
					return err
				}},
				{"create location", func() error {
					_, err := ops.CreateLocation(fixture.tenantID, CreateCommerceLocationInput{
						BusinessType: businessType, Name: "Blocked location",
					})
					return err
				}},
				{"set location status", func() error {
					_, err := ops.SetLocationStatus(fixture.tenantID, fixture.location.ID, "inactive")
					return err
				}},
				{"list inventory", func() error {
					_, err := ops.ListInventory(fixture.tenantID, businessType)
					return err
				}},
				{"unqualified inventory list", func() error {
					_, err := ops.ListInventory(fixture.tenantID, "")
					return err
				}},
				{"set inventory", func() error {
					_, err := ops.SetInventory(fixture.tenantID, CommerceInventoryInput{
						SkuID: fixture.sku.ID, LocationID: fixture.location.ID, AvailableQty: 10,
					})
					return err
				}},
				{"adjust inventory", func() error {
					_, err := ops.AdjustInventory(fixture.tenantID, CommerceInventoryAdjustmentInput{
						SkuID: fixture.sku.ID, LocationID: fixture.location.ID, Delta: 1,
					})
					return err
				}},
				{"get cart", func() error {
					_, err := ops.GetCart(fixture.tenantID, fixture.cart.ID)
					return err
				}},
				{"get or create cart", func() error {
					_, err := ops.GetOrCreateCart(fixture.tenantID, CommerceCartInput{
						BusinessType: businessType, CustomerID: "blocked-customer", LocationID: fixture.location.ID,
					})
					return err
				}},
				{"add cart item", func() error {
					_, err := ops.AddCartItem(fixture.tenantID, fixture.cart.ID, CommerceCartItemInput{
						ProductID: fixture.product.ID, SkuID: fixture.sku.ID, Quantity: 1, OptionIDs: []uint{fixture.option.ID},
					})
					return err
				}},
				{"update cart item", func() error {
					_, err := ops.UpdateCartItem(fixture.tenantID, fixture.cart.ID, fixture.cartItemID, 2)
					return err
				}},
				{"remove cart item", func() error {
					return ops.RemoveCartItem(fixture.tenantID, fixture.cart.ID, fixture.cartItemID)
				}},
				{"abandon cart", func() error {
					return ops.AbandonCart(fixture.tenantID, fixture.cart.ID)
				}},
			}
			for _, check := range checks {
				t.Run(check.name, func(t *testing.T) {
					if err := check.call(); !errors.Is(err, ErrCapabilityInactive) {
						t.Fatalf("error=%v, want %v", err, ErrCapabilityInactive)
					}
				})
			}
		})
	}
}

func TestCommerceBoundarySeparatesRestaurantAndRetailWithinTenant(t *testing.T) {
	restaurant, retail := newCommerceBoundaryDualFixture(t)
	ops := &CommerceOperationsService{}

	for _, check := range []struct {
		name string
		call func() error
	}{
		{"restaurant cart at retail location", func() error {
			_, err := ops.GetOrCreateCart(restaurant.tenantID, CommerceCartInput{
				BusinessType: "restaurant", CustomerID: "wrong-location", LocationID: retail.location.ID,
			})
			return err
		}},
		{"retail cart at restaurant location", func() error {
			_, err := ops.GetOrCreateCart(retail.tenantID, CommerceCartInput{
				BusinessType: "retail", CustomerID: "wrong-location", LocationID: restaurant.location.ID,
			})
			return err
		}},
		{"restaurant inventory at retail location", func() error {
			_, err := ops.SetInventory(restaurant.tenantID, CommerceInventoryInput{
				SkuID: restaurant.sku.ID, LocationID: retail.location.ID, AvailableQty: 1,
			})
			return err
		}},
		{"retail inventory at restaurant location", func() error {
			_, err := ops.SetInventory(retail.tenantID, CommerceInventoryInput{
				SkuID: retail.sku.ID, LocationID: restaurant.location.ID, AvailableQty: 1,
			})
			return err
		}},
		{"retail product in restaurant cart", func() error {
			_, err := ops.AddCartItem(restaurant.tenantID, restaurant.cart.ID, CommerceCartItemInput{
				ProductID: retail.product.ID, SkuID: retail.sku.ID, Quantity: 1, OptionIDs: []uint{retail.option.ID},
			})
			return err
		}},
		{"restaurant product in retail cart", func() error {
			_, err := ops.AddCartItem(retail.tenantID, retail.cart.ID, CommerceCartItemInput{
				ProductID: restaurant.product.ID, SkuID: restaurant.sku.ID, Quantity: 1, OptionIDs: []uint{restaurant.option.ID},
			})
			return err
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			err := check.call()
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("error=%v, want %v", err, gorm.ErrRecordNotFound)
			}
		})
	}

	restaurantCart, err := ops.GetOrCreateCart(restaurant.tenantID, CommerceCartInput{
		BusinessType: "restaurant", CustomerID: "same-customer", LocationID: restaurant.location.ID,
	})
	if err != nil {
		t.Fatalf("create restaurant cart: %v", err)
	}
	retailCart, err := ops.GetOrCreateCart(retail.tenantID, CommerceCartInput{
		BusinessType: "retail", CustomerID: "same-customer", LocationID: retail.location.ID,
	})
	if err != nil {
		t.Fatalf("create retail cart: %v", err)
	}
	if restaurantCart.ID == retailCart.ID || restaurantCart.BusinessType != "restaurant" || retailCart.BusinessType != "retail" {
		t.Fatalf("domain carts were not independent: restaurant=%+v retail=%+v", restaurantCart, retailCart)
	}
	if restaurantCart.LocationID != restaurant.location.ID || retailCart.LocationID != retail.location.ID {
		t.Fatalf("domain cart locations were mixed: restaurant=%+v retail=%+v", restaurantCart, retailCart)
	}

	locations, err := ops.ListLocations(restaurant.tenantID, "restaurant", "")
	if err != nil || len(locations) != 1 || locations[0].ID != restaurant.location.ID {
		t.Fatalf("restaurant locations=%+v err=%v", locations, err)
	}
	locations, err = ops.ListLocations(retail.tenantID, "retail", "")
	if err != nil || len(locations) != 1 || locations[0].ID != retail.location.ID {
		t.Fatalf("retail locations=%+v err=%v", locations, err)
	}
	stocks, err := ops.ListInventory(restaurant.tenantID, "restaurant")
	if err != nil || len(stocks) != 1 || stocks[0].SkuID != restaurant.sku.ID {
		t.Fatalf("restaurant inventory=%+v err=%v", stocks, err)
	}
	stocks, err = ops.ListInventory(retail.tenantID, "retail")
	if err != nil || len(stocks) != 1 || stocks[0].SkuID != retail.sku.ID {
		t.Fatalf("retail inventory=%+v err=%v", stocks, err)
	}
}

func TestCommerceBoundaryTenantIsolationForProductsSKUsAndInventory(t *testing.T) {
	owner := createCommerceBoundaryDomainFixture(t, newCommerceTenant(t, "retail", "active"), "retail")
	foreignTenantID := createCommerceBoundaryTenant(t, "retail")
	catalog := &CommerceCatalogService{}
	ops := &CommerceOperationsService{}
	if rows, err := catalog.ListProducts(foreignTenantID, "retail", "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("foreign product list rows=%d err=%v, want empty", len(rows), err)
	}
	if rows, err := ops.ListInventory(foreignTenantID, "retail"); err != nil || len(rows) != 0 {
		t.Fatalf("foreign inventory list rows=%d err=%v, want empty", len(rows), err)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{"get product", func() error {
			_, err := catalog.GetProduct(foreignTenantID, owner.product.ID)
			return err
		}},
		{"set product status", func() error {
			_, err := catalog.SetProductStatus(foreignTenantID, owner.product.ID, "offline")
			return err
		}},
		{"create sku under foreign product", func() error {
			_, err := catalog.CreateSKU(foreignTenantID, owner.product.ID, CreateCommerceSKUInput{
				SKUCode: "FOREIGN-SKU", Name: "Foreign", OriginalPriceCents: 100, PriceCents: 100,
			})
			return err
		}},
		{"update sku", func() error {
			_, err := catalog.UpdateSKU(foreignTenantID, owner.sku.ID, UpdateCommerceSKUInput{
				SKUCode: "FOREIGN-SKU-UPDATED", Name: "Foreign", OriginalPriceCents: 100, PriceCents: 100,
			})
			return err
		}},
		{"set inventory", func() error {
			_, err := ops.SetInventory(foreignTenantID, CommerceInventoryInput{
				SkuID: owner.sku.ID, LocationID: owner.location.ID, AvailableQty: 1,
			})
			return err
		}},
		{"adjust inventory", func() error {
			_, err := ops.AdjustInventory(foreignTenantID, CommerceInventoryAdjustmentInput{
				SkuID: owner.sku.ID, LocationID: owner.location.ID, Delta: 1,
			})
			return err
		}},
		{"get foreign cart", func() error {
			_, err := ops.GetCart(foreignTenantID, owner.cart.ID)
			return err
		}},
		{"add foreign cart item", func() error {
			_, err := ops.AddCartItem(foreignTenantID, owner.cart.ID, CommerceCartItemInput{
				ProductID: owner.product.ID, SkuID: owner.sku.ID, Quantity: 1, OptionIDs: []uint{owner.option.ID},
			})
			return err
		}},
		{"update foreign cart item", func() error {
			returnError := error(nil)
			_, returnError = ops.UpdateCartItem(foreignTenantID, owner.cart.ID, owner.cartItemID, 2)
			return returnError
		}},
		{"remove foreign cart item", func() error {
			return ops.RemoveCartItem(foreignTenantID, owner.cart.ID, owner.cartItemID)
		}},
		{"abandon foreign cart", func() error {
			return ops.AbandonCart(foreignTenantID, owner.cart.ID)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.call()
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("error=%v, want %v", err, gorm.ErrRecordNotFound)
			}
		})
	}

	var storedProduct model.CommerceProduct
	if err := model.DB.First(&storedProduct, owner.product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedProduct.TenantID != owner.tenantID || storedProduct.Status != "online" {
		t.Fatalf("owner product changed after foreign attempts: %+v", storedProduct)
	}
	var storedStock model.CommerceInventory
	if err := model.DB.Where("id = ? AND tenant_id = ?", owner.stock.ID, owner.tenantID).First(&storedStock).Error; err != nil {
		t.Fatal(err)
	}
	if storedStock.AvailableQty != owner.stock.AvailableQty {
		t.Fatalf("owner inventory changed after foreign attempts: %+v", storedStock)
	}
}

func TestCommerceBoundaryProductCannotEnterTicketOrderOrCheckIn(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	if err := model.DB.Create(&model.TenantCapability{TenantID: tenantID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatalf("create supplier capability: %v", err)
	}
	if err := model.DB.Create(&model.SupplierBusinessType{TenantID: tenantID, BusinessType: "scenic", Status: "active"}).Error; err != nil {
		t.Fatalf("create scenic business type: %v", err)
	}
	area := model.ScenicArea{TenantID: tenantID, Code: "COMMERCE-BOUNDARY-AREA", Name: "Boundary area", Status: "active"}
	if err := model.DB.Create(&area).Error; err != nil {
		t.Fatalf("create scenic area: %v", err)
	}
	checkpoint := model.CheckPoint{TenantID: tenantID, ScenicAreaID: area.ID, Name: "Boundary checkpoint"}
	if err := model.DB.Create(&checkpoint).Error; err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	device := model.Device{
		TenantID: tenantID, ScenicAreaID: area.ID, CheckPointID: &checkpoint.ID,
		Name: "Boundary handheld", SerialNumber: "COMMERCE-BOUNDARY-DEVICE", Type: "handheld", Status: "online",
	}
	if err := model.DB.Create(&device).Error; err != nil {
		t.Fatalf("create verification device: %v", err)
	}

	catalog := &CommerceCatalogService{}
	input := commerceProductInput("Commercial-only product")
	input.SKUs[0].SKUCode = "COMMERCIAL-ONLY-SKU"
	product, err := catalog.CreateProduct(tenantID, input)
	if err != nil {
		t.Fatalf("create commercial product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish commercial product: %v", err)
	}

	var ticketProductCount int64
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", product.ID, tenantID).Count(&ticketProductCount).Error; err != nil {
		t.Fatal(err)
	}
	if ticketProductCount != 0 {
		t.Fatalf("fixture unexpectedly has a ticket product with commercial id %d", product.ID)
	}

	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: product.ID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err == nil {
		t.Fatal("commercial product was accepted by ticket order service")
	}
	var orderCount int64
	if err := model.DB.Model(&model.Order{}).Where("tenant_id = ?", tenantID).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("ticket order was persisted for commercial product: %d", orderCount)
	}
	var ticketCount int64
	if err := model.DB.Model(&model.Ticket{}).Where("tenant_id = ?", tenantID).Count(&ticketCount).Error; err != nil {
		t.Fatal(err)
	}
	if ticketCount != 0 {
		t.Fatalf("ticket rows were created for commercial product: %d", ticketCount)
	}
	var entitlementCount int64
	if err := model.DB.Model(&model.TicketEntitlement{}).Where("sales_tenant_id = ?", tenantID).Count(&entitlementCount).Error; err != nil {
		t.Fatal(err)
	}
	if entitlementCount != 0 {
		t.Fatalf("ticket entitlements were created for commercial product: %d", entitlementCount)
	}

	commercialCode := fmt.Sprintf("COMMERCE-PRODUCT-%d", product.ID)
	if err := (&TicketService{}).Verify(commercialCode, checkpoint.ID, device.ID, tenantID); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("commercial identifier verification error=%v, want %v", err, ErrInvalidTicket)
	}
	var successfulCheckIns int64
	if err := model.DB.Model(&model.CheckInRecord{}).
		Where("tenant_id = ? AND ticket_code = ? AND result = ?", tenantID, commercialCode, "success").
		Count(&successfulCheckIns).Error; err != nil {
		t.Fatal(err)
	}
	if successfulCheckIns != 0 {
		t.Fatalf("commercial product produced successful check-in facts: %d", successfulCheckIns)
	}
}
