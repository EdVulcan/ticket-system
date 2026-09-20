package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestCommerceOptionsLocationsInventoryAndCart(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, commerceProductInput("Cart meal"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish product: %v", err)
	}
	ops := &CommerceOperationsService{}
	group, err := ops.CreateOptionGroup(tenantID, product.ID, CreateCommerceOptionGroupInput{Name: "口味", Required: true, MaxSelections: 1})
	if err != nil {
		t.Fatalf("create option group: %v", err)
	}
	option, err := ops.CreateOption(tenantID, group.ID, CreateCommerceOptionInput{Name: "微辣", PriceDeltaCents: 100})
	if err != nil {
		t.Fatalf("create option: %v", err)
	}
	groups, err := ops.ListOptionGroups(tenantID, product.ID)
	if err != nil || len(groups) != 1 || len(groups[0].Options) != 1 || groups[0].Options[0].ID != option.ID {
		t.Fatalf("option groups=%+v err=%v", groups, err)
	}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{BusinessType: "restaurant", Name: "主店", LocationType: "store"})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	stock, err := ops.SetInventory(tenantID, CommerceInventoryInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 20})
	if err != nil || stock.AvailableQty != 20 {
		t.Fatalf("set inventory=%+v err=%v", stock, err)
	}
	stock, err = ops.AdjustInventory(tenantID, CommerceInventoryAdjustmentInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, Delta: -3})
	if err != nil || stock.AvailableQty != 17 {
		t.Fatalf("adjust inventory=%+v err=%v", stock, err)
	}
	if _, err := ops.AdjustInventory(tenantID, CommerceInventoryAdjustmentInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, Delta: -18}); !errors.Is(err, ErrCommerceInventoryInvalid) {
		t.Fatalf("negative inventory err=%v", err)
	}
	cart, err := ops.GetOrCreateCart(tenantID, CommerceCartInput{BusinessType: "restaurant", CustomerID: "customer-1", LocationID: location.ID})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	cart, err = ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 2, OptionIDs: []uint{option.ID}})
	if err != nil || len(cart.Items) != 1 || cart.Items[0].Quantity != 2 {
		t.Fatalf("add cart item=%+v err=%v", cart, err)
	}
	cart, err = ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 1, OptionIDs: []uint{option.ID}})
	if err != nil || len(cart.Items) != 1 || cart.Items[0].Quantity != 3 {
		t.Fatalf("merge cart item=%+v err=%v", cart, err)
	}
	if _, err := ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 1}); !errors.Is(err, ErrCommerceCartInvalid) {
		t.Fatalf("missing required option err=%v", err)
	}
	if err := ops.RemoveCartItem(tenantID, cart.ID, cart.Items[0].ID); err != nil {
		t.Fatalf("remove cart item: %v", err)
	}
	if err := ops.AbandonCart(tenantID, cart.ID); err != nil {
		t.Fatalf("abandon cart: %v", err)
	}
	newCart, err := ops.GetOrCreateCart(tenantID, CommerceCartInput{BusinessType: "restaurant", CustomerID: "customer-1", LocationID: location.ID})
	if err != nil {
		t.Fatalf("recreate cart after abandonment: %v", err)
	}
	if newCart.ID == cart.ID || newCart.Status != "active" {
		t.Fatalf("recreated cart=%+v, old cart=%+v", newCart, cart)
	}
	var abandonedCart model.CommerceCart
	if err := model.DB.Where("id = ? AND tenant_id = ?", cart.ID, tenantID).First(&abandonedCart).Error; err != nil {
		t.Fatalf("load abandoned cart: %v", err)
	}
	if abandonedCart.Status != "abandoned" {
		t.Fatalf("old cart status=%q, want abandoned", abandonedCart.Status)
	}
}

func TestCommerceOptionsCanBeEditedAndSoftDeleted(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, commerceProductInput("Editable meal"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	ops := &CommerceOperationsService{}
	group, err := ops.CreateOptionGroup(tenantID, product.ID, CreateCommerceOptionGroupInput{
		Name: "口味", Required: true, MinSelections: 1, MaxSelections: 1,
	})
	if err != nil {
		t.Fatalf("create option group: %v", err)
	}
	option, err := ops.CreateOption(tenantID, group.ID, CreateCommerceOptionInput{
		Name: "微辣", PriceDeltaCents: 100,
	})
	if err != nil {
		t.Fatalf("create option: %v", err)
	}

	updatedGroup, err := ops.UpdateOptionGroup(tenantID, group.ID, UpdateCommerceOptionGroupInput{
		Name: "辣度", Required: false, MinSelections: 0, MaxSelections: 2,
	})
	if err != nil {
		t.Fatalf("update option group: %v", err)
	}
	if updatedGroup.Name != "辣度" || updatedGroup.Required || updatedGroup.MinSelections != 0 || updatedGroup.MaxSelections != 2 {
		t.Fatalf("unexpected updated group: %+v", updatedGroup)
	}
	updatedGroup, err = ops.UpdateOptionGroup(tenantID, group.ID, UpdateCommerceOptionGroupInput{
		Name: "辣度", Required: true, MinSelections: 0, MaxSelections: 2,
	})
	if err != nil {
		t.Fatalf("make option group required: %v", err)
	}
	if !updatedGroup.Required || updatedGroup.MinSelections != 1 {
		t.Fatalf("required option group did not enforce one selection: %+v", updatedGroup)
	}
	updatedGroup, err = ops.UpdateOptionGroup(tenantID, group.ID, UpdateCommerceOptionGroupInput{
		Name: "辣度", Required: false, MinSelections: 1, MaxSelections: 2,
	})
	if err != nil {
		t.Fatalf("make option group optional: %v", err)
	}
	if updatedGroup.Required || updatedGroup.MinSelections != 0 {
		t.Fatalf("optional option group retained a mandatory minimum: %+v", updatedGroup)
	}

	updatedOption, err := ops.UpdateOption(tenantID, group.ID, option.ID, UpdateCommerceOptionInput{
		Name: "中辣", PriceDeltaCents: 200, Status: "inactive",
	})
	if err != nil {
		t.Fatalf("update option: %v", err)
	}
	if updatedOption.Name != "中辣" || updatedOption.PriceDeltaCents != 200 || updatedOption.Status != "inactive" {
		t.Fatalf("unexpected updated option: %+v", updatedOption)
	}

	if err := ops.DeleteOption(tenantID, group.ID, option.ID); err != nil {
		t.Fatalf("delete option: %v", err)
	}
	if _, err := ops.UpdateOption(tenantID, group.ID, option.ID, UpdateCommerceOptionInput{Name: "再次编辑"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted option update error=%v, want record not found", err)
	}

	if err := ops.DeleteOptionGroup(tenantID, group.ID); err != nil {
		t.Fatalf("delete option group: %v", err)
	}
	groups, err := ops.ListOptionGroups(tenantID, product.ID)
	if err != nil {
		t.Fatalf("list option groups after deletion: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("deleted option group remained visible: %+v", groups)
	}
	var deletedGroup model.CommerceOptionGroup
	if err := model.DB.Unscoped().Where("id = ? AND tenant_id = ?", group.ID, tenantID).First(&deletedGroup).Error; err != nil {
		t.Fatalf("load soft-deleted group: %v", err)
	}
	if !deletedGroup.DeletedAt.Valid {
		t.Fatal("option group was physically retained without a soft-delete timestamp")
	}
	var deletedOption model.CommerceOption
	if err := model.DB.Unscoped().Where("id = ? AND tenant_id = ?", option.ID, tenantID).First(&deletedOption).Error; err != nil {
		t.Fatalf("load soft-deleted option: %v", err)
	}
	if !deletedOption.DeletedAt.Valid {
		t.Fatal("option was physically retained without a soft-delete timestamp")
	}
}

func TestCommerceOptionEditsAndDeletesRemainTenantScoped(t *testing.T) {
	firstTenant := newCommerceTenant(t, "restaurant", "active")
	product, err := (&CommerceCatalogService{}).CreateProduct(firstTenant, commerceProductInput("Tenant scoped options"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	ops := &CommerceOperationsService{}
	group, err := ops.CreateOptionGroup(firstTenant, product.ID, CreateCommerceOptionGroupInput{Name: "口味"})
	if err != nil {
		t.Fatalf("create option group: %v", err)
	}
	option, err := ops.CreateOption(firstTenant, group.ID, CreateCommerceOptionInput{Name: "原味"})
	if err != nil {
		t.Fatalf("create option: %v", err)
	}
	foreignTenant := newCommerceTenant(t, "restaurant", "active")
	if _, err := ops.UpdateOptionGroup(foreignTenant, group.ID, UpdateCommerceOptionGroupInput{Name: "越权"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign group update error=%v, want record not found", err)
	}
	if _, err := ops.UpdateOption(foreignTenant, group.ID, option.ID, UpdateCommerceOptionInput{Name: "越权"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign option update error=%v, want record not found", err)
	}
	if err := ops.DeleteOption(foreignTenant, group.ID, option.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign option delete error=%v, want record not found", err)
	}
	if err := ops.DeleteOptionGroup(foreignTenant, group.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign group delete error=%v, want record not found", err)
	}
}

func TestCommerceCheckoutCartSerializesDifferentIdempotencyKeys(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, commerceProductInput("Concurrent checkout meal"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish product: %v", err)
	}
	ops := &CommerceOperationsService{}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{BusinessType: "restaurant", Name: "checkout store", LocationType: "store"})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	if _, err := ops.SetInventory(tenantID, CommerceInventoryInput{SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 1}); err != nil {
		t.Fatalf("set inventory: %v", err)
	}
	cart, err := ops.GetOrCreateCart(tenantID, CommerceCartInput{BusinessType: "restaurant", CustomerID: "checkout-customer", LocationID: location.ID})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if _, err := ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 1}); err != nil {
		t.Fatalf("add cart item: %v", err)
	}

	results := make(chan *model.CommerceOrder, 2)
	errorsCh := make(chan error, 2)
	var group sync.WaitGroup
	for _, key := range []string{"checkout-key-a", "checkout-key-b"} {
		key := key
		group.Add(1)
		go func() {
			defer group.Done()
			order, checkoutErr := (&CommerceOperationsService{DB: model.DB}).CheckoutCart(tenantID, cart.ID, CommerceCheckoutCartInput{IdempotencyKey: key, ContactName: "游客", ContactPhone: "13800138000"})
			results <- order
			errorsCh <- checkoutErr
		}()
	}
	group.Wait()
	close(results)
	close(errorsCh)

	var orders []*model.CommerceOrder
	for order := range results {
		if order != nil {
			orders = append(orders, order)
		}
	}
	var observedErrors []error
	for checkoutErr := range errorsCh {
		if checkoutErr != nil {
			observedErrors = append(observedErrors, checkoutErr)
		}
	}
	if len(observedErrors) != 0 || len(orders) != 2 || orders[0].ID != orders[1].ID {
		t.Fatalf("concurrent cart checkout created inconsistent results: orders=%v errors=%v", orders, observedErrors)
	}
	var orderCount int64
	if err := model.DB.Model(&model.CommerceOrder{}).Where("tenant_id = ?", tenantID).Count(&orderCount).Error; err != nil {
		t.Fatalf("count checkout orders: %v", err)
	}
	if orderCount != 1 {
		t.Fatalf("concurrent cart checkout created %d orders, want 1", orderCount)
	}
	var storedCart model.CommerceCart
	if err := model.DB.Where("id = ? AND tenant_id = ?", cart.ID, tenantID).First(&storedCart).Error; err != nil {
		t.Fatalf("load checked-out cart: %v", err)
	}
	if storedCart.Status != "checked_out" || storedCart.CheckedOutOrderID != orders[0].ID {
		t.Fatalf("cart checkout projection=%+v", storedCart)
	}
}

func TestCommerceOperationsEnforceTenantAndDomainOwnership(t *testing.T) {
	firstTenant := newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(firstTenant, commerceProductInput("Private meal"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	ops := &CommerceOperationsService{}
	if _, err := ops.CreateOptionGroup(firstTenant+999, product.ID, CreateCommerceOptionGroupInput{Name: "foreign"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign option group err=%v", err)
	}
	second := model.Tenant{Name: "Retail tenant", SystemCode: fmt.Sprintf("OPS-RETAIL-%d", product.ID), SecretKey: "secret", Status: "active"}
	if err := model.DB.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: second.ID, BusinessType: "retail", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ops.CreateLocation(second.ID, CreateCommerceLocationInput{BusinessType: "restaurant", Name: "wrong domain"}); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("foreign domain capability err=%v", err)
	}
	if _, err := ops.SetInventory(second.ID, CommerceInventoryInput{SkuID: product.SKUs[0].ID, LocationID: 1, AvailableQty: 1}); !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, ErrCommerceInventoryInvalid) {
		t.Fatalf("foreign inventory err=%v", err)
	}
}

func TestCommerceAddCartItemRejectsAtSaleEndBoundary(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	boundary := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	input := commerceProductInput("Sale boundary product")
	input.Status = "online"
	input.SaleEndsAt = &boundary
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, input)
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	ops := &CommerceOperationsService{Clock: func() time.Time { return boundary }}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{
		BusinessType: "restaurant", Name: "Sale boundary store", LocationType: "store",
	})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	cart, err := ops.GetOrCreateCart(tenantID, CommerceCartInput{
		BusinessType: "restaurant", CustomerID: "sale-boundary-customer", LocationID: location.ID,
	})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if _, err := ops.AddCartItem(tenantID, cart.ID, CommerceCartItemInput{
		ProductID: product.ID, SkuID: product.SKUs[0].ID, Quantity: 1,
	}); !errors.Is(err, ErrCommerceCartInvalid) {
		t.Fatalf("sale-end cart item error=%v, want %v", err, ErrCommerceCartInvalid)
	}
}

func commerceInventoryBoundaryFixture(t *testing.T) (uint, uint, uint, uint) {
	t.Helper()
	tenantID := newCommerceTenant(t, "restaurant", "active")
	catalog := &CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, commerceProductInput("Inventory boundary product"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	ops := &CommerceOperationsService{}
	location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{
		BusinessType: "restaurant", Name: "Inventory boundary store", LocationType: "store",
	})
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	if _, err := ops.SetInventory(tenantID, CommerceInventoryInput{
		SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 5,
	}); err != nil {
		t.Fatalf("create inventory: %v", err)
	}
	return tenantID, product.ID, product.SKUs[0].ID, location.ID
}

func TestCommerceSetInventoryRejectsVersionOverflowWithoutMutation(t *testing.T) {
	tenantID, _, skuID, locationID := commerceInventoryBoundaryFixture(t)
	if err := model.DB.Model(&model.CommerceInventory{}).
		Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).
		Update("version", commerceMaxInt64).Error; err != nil {
		t.Fatalf("set max inventory version: %v", err)
	}
	ops := &CommerceOperationsService{}
	if _, err := ops.SetInventory(tenantID, CommerceInventoryInput{SkuID: skuID, LocationID: locationID, AvailableQty: 6}); !errors.Is(err, ErrCommerceInventoryInvalid) {
		t.Fatalf("set inventory overflow error=%v, want %v", err, ErrCommerceInventoryInvalid)
	}
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatalf("reload inventory: %v", err)
	}
	if stock.AvailableQty != 5 || stock.Version != commerceMaxInt64 {
		t.Fatalf("inventory mutated after version overflow: %+v", stock)
	}
}

func TestCommerceAdjustInventoryRejectsOverflowAndMinDeltaWithoutMutation(t *testing.T) {
	tenantID, _, skuID, locationID := commerceInventoryBoundaryFixture(t)
	ops := &CommerceOperationsService{}
	if err := model.DB.Model(&model.CommerceInventory{}).
		Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).
		Update("version", commerceMaxInt64).Error; err != nil {
		t.Fatalf("set max inventory version: %v", err)
	}
	if _, err := ops.AdjustInventory(tenantID, CommerceInventoryAdjustmentInput{SkuID: skuID, LocationID: locationID, Delta: 1}); !errors.Is(err, ErrCommerceInventoryInvalid) {
		t.Fatalf("adjust inventory overflow error=%v, want %v", err, ErrCommerceInventoryInvalid)
	}
	var stock model.CommerceInventory
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatalf("reload overflow inventory: %v", err)
	}
	if stock.AvailableQty != 5 || stock.Version != commerceMaxInt64 {
		t.Fatalf("inventory mutated after adjust version overflow: %+v", stock)
	}

	if err := model.DB.Model(&stock).Updates(map[string]interface{}{"version": 1, "available_qty": 5}).Error; err != nil {
		t.Fatalf("reset inventory boundary: %v", err)
	}
	minInt := int(^uint(0) >> 1)
	minInt = -minInt - 1
	if _, err := ops.AdjustInventory(tenantID, CommerceInventoryAdjustmentInput{SkuID: skuID, LocationID: locationID, Delta: minInt}); !errors.Is(err, ErrCommerceInventoryInvalid) {
		t.Fatalf("adjust inventory min-delta error=%v, want %v", err, ErrCommerceInventoryInvalid)
	}
	if err := model.DB.Where("tenant_id = ? AND sku_id = ? AND location_id = ?", tenantID, skuID, locationID).First(&stock).Error; err != nil {
		t.Fatalf("reload min-delta inventory: %v", err)
	}
	if stock.AvailableQty != 5 || stock.Version != 1 {
		t.Fatalf("inventory mutated after min-delta rejection: %+v", stock)
	}
}
