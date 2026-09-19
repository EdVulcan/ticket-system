package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"ticket-backend/internal/model"
)

func resetCommerceCatalogData(t *testing.T) {
	t.Helper()
	for _, table := range []interface{}{
		&model.CommercePaymentProviderEvent{}, &model.CommerceRefundAttempt{}, &model.CommercePaymentAttempt{},
		&model.CommercePaymentReconciliationTask{},
		&model.CommerceAfterSaleEvent{}, &model.CommerceAfterSaleRequest{},
		&model.RestaurantFulfillment{}, &model.RetailFulfillment{},
		&model.CommerceOrderItem{}, &model.CommerceOrder{}, &model.CommerceCartItem{},
		&model.CommerceCart{}, &model.CommerceInventory{}, &model.CommerceAddress{},
		&model.CommerceOption{}, &model.CommerceOptionGroup{}, &model.CommerceSKU{},
		&model.CommerceProduct{}, &model.TenantBusinessCapability{},
	} {
		if err := model.DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error; err != nil {
			t.Fatalf("reset commerce table %T: %v", table, err)
		}
	}
}

func newCommerceTenant(t *testing.T, businessType, capabilityStatus string) uint {
	t.Helper()
	resetBusinessData(t)
	resetCommerceCatalogData(t)
	tenant := model.Tenant{
		Name:       "Commerce Catalog Tenant",
		SystemCode: fmt.Sprintf("CC-%s", strings.ToUpper(businessType)),
		SecretKey:  "commerce-secret",
		Status:     "active",
	}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if capabilityStatus != "" {
		if err := model.DB.Create(&model.TenantBusinessCapability{
			TenantID: tenant.ID, BusinessType: businessType, Status: capabilityStatus,
		}).Error; err != nil {
			t.Fatalf("create business capability: %v", err)
		}
	}
	return tenant.ID
}

func commerceProductInput(name string) CreateCommerceProductInput {
	return CreateCommerceProductInput{
		BusinessType: "restaurant",
		Name:         name,
		ShortTitle:   "Catalog item",
		Description:  "Product description",
		CategoryName: "Meals",
		SKUs: []CreateCommerceSKUInput{{
			SKUCode: "SKU-001", Name: "Regular", OriginalPriceCents: 1200,
			PriceCents: 1000, Status: "active", AttributesJSON: `{"size":"regular"}`,
		}},
	}
}

func TestCommerceCatalogCRUDAndLifecycle(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	service := &CommerceCatalogService{}
	product, err := service.CreateProduct(tenantID, commerceProductInput("Rice bowl"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if product.ID == 0 || len(product.SKUs) != 1 || product.SKUs[0].SkuCode != "SKU-001" {
		t.Fatalf("created product=%+v", product)
	}

	rows, err := service.ListProducts(tenantID, "restaurant", "draft", "rice")
	if err != nil || len(rows) != 1 || rows[0].ID != product.ID {
		t.Fatalf("list products rows=%v err=%v", rows, err)
	}
	detail, err := service.GetProduct(tenantID, product.ID)
	if err != nil || detail.Description != "Product description" {
		t.Fatalf("get product detail=%+v err=%v", detail, err)
	}

	updated, err := service.SetProductStatus(tenantID, product.ID, "online")
	if err != nil || updated.Status != "online" {
		t.Fatalf("online product=%+v err=%v", updated, err)
	}
	updated, err = service.SetProductStatus(tenantID, product.ID, "offline")
	if err != nil || updated.Status != "offline" {
		t.Fatalf("offline product=%+v err=%v", updated, err)
	}

	sku, err := service.CreateSKU(tenantID, product.ID, CreateCommerceSKUInput{
		SKUCode: "SKU-002", Name: "Large", OriginalPriceCents: 1500, PriceCents: 1400,
	})
	if err != nil {
		t.Fatalf("create sku: %v", err)
	}
	changed, err := service.UpdateSKU(tenantID, sku.ID, UpdateCommerceSKUInput{
		SKUCode: "SKU-002-UPDATED", Name: "Large updated", OriginalPriceCents: 1600,
		PriceCents: 1450, Status: "inactive", AttributesJSON: `{"size":"large"}`,
	})
	if err != nil || changed.SkuCode != "SKU-002-UPDATED" || changed.Status != "inactive" {
		t.Fatalf("updated sku=%+v err=%v", changed, err)
	}

	productWithInactiveSKU, err := service.CreateProduct(tenantID, CreateCommerceProductInput{
		BusinessType: "restaurant",
		Name:         "Needs active SKU",
		SKUs: []CreateCommerceSKUInput{{
			SKUCode: "SKU-INACTIVE", Name: "Unavailable", OriginalPriceCents: 100,
			PriceCents: 100, Status: "inactive",
		}},
	})
	if err != nil {
		t.Fatalf("create inactive sku product: %v", err)
	}
	if _, err := service.SetProductStatus(tenantID, productWithInactiveSKU.ID, "online"); !errors.Is(err, ErrCommerceProductInvalid) {
		t.Fatalf("online product with no active sku err=%v", err)
	}

	if _, err := service.CreateSKU(tenantID, product.ID, CreateCommerceSKUInput{
		SKUCode: "SKU-001", Name: "Duplicate", OriginalPriceCents: 100, PriceCents: 100,
	}); err == nil {
		t.Fatalf("duplicate sku err=%v", err)
	}
}

func TestCommerceCatalogRejectsInvalidInputs(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	service := &CommerceCatalogService{}
	tests := []struct {
		name  string
		input CreateCommerceProductInput
	}{
		{"unsupported business type", CreateCommerceProductInput{BusinessType: "hotel", Name: "Hotel", SKUs: []CreateCommerceSKUInput{{SKUCode: "S", Name: "Room"}}}},
		{"missing name", CreateCommerceProductInput{BusinessType: "restaurant", SKUs: []CreateCommerceSKUInput{{SKUCode: "S", Name: "Meal"}}}},
		{"empty skus", CreateCommerceProductInput{BusinessType: "restaurant", Name: "Meal"}},
		{"negative price", CreateCommerceProductInput{BusinessType: "restaurant", Name: "Meal", SKUs: []CreateCommerceSKUInput{{SKUCode: "S", Name: "Meal", OriginalPriceCents: -1, PriceCents: 0}}}},
		{"sale exceeds original", CreateCommerceProductInput{BusinessType: "restaurant", Name: "Meal", SKUs: []CreateCommerceSKUInput{{SKUCode: "S", Name: "Meal", OriginalPriceCents: 10, PriceCents: 11}}}},
		{"duplicate sku", CreateCommerceProductInput{BusinessType: "restaurant", Name: "Meal", SKUs: []CreateCommerceSKUInput{{SKUCode: "S", Name: "One"}, {SKUCode: "S", Name: "Two"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.CreateProduct(tenantID, tc.input); err == nil {
				t.Fatal("invalid product was accepted")
			}
		})
	}
	if _, err := service.SetProductStatus(tenantID, 999999, "unknown"); !errors.Is(err, ErrCommerceProductInvalid) {
		t.Fatalf("invalid status error=%v", err)
	}
}

func TestCommerceCatalogRequiresActiveCapabilityForAllOperations(t *testing.T) {
	tenantID := newCommerceTenant(t, "restaurant", "active")
	service := &CommerceCatalogService{}
	product, err := service.CreateProduct(tenantID, commerceProductInput("Capability guarded"))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := model.DB.Model(&model.TenantBusinessCapability{}).
		Where("tenant_id = ? AND business_type = ?", tenantID, "restaurant").
		Update("status", "suspended").Error; err != nil {
		t.Fatalf("suspend capability: %v", err)
	}
	if _, err := service.ListProducts(tenantID, "restaurant", "", ""); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("list with suspended capability err=%v", err)
	}
	if _, err := service.ListProducts(tenantID, "", "", ""); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("unscoped list with suspended capability err=%v", err)
	}
	if _, err := service.GetProduct(tenantID, product.ID); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("get with suspended capability err=%v", err)
	}
	if _, err := service.SetProductStatus(tenantID, product.ID, "offline"); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("status update with suspended capability err=%v", err)
	}
	if _, err := service.CreateSKU(tenantID, product.ID, CreateCommerceSKUInput{SKUCode: "S2", Name: "Second"}); !errors.Is(err, ErrCapabilityInactive) {
		t.Fatalf("sku create with suspended capability err=%v", err)
	}
}

func TestCommerceCatalogScopesProductsAndSKUsToTenant(t *testing.T) {
	firstTenant := newCommerceTenant(t, "restaurant", "active")
	service := &CommerceCatalogService{}
	product, err := service.CreateProduct(firstTenant, commerceProductInput("Tenant A product"))
	if err != nil {
		t.Fatalf("create first product: %v", err)
	}
	secondTenant := model.Tenant{Name: "Tenant B", SystemCode: "COMMERCE-CATALOG-B", SecretKey: "secret", Status: "active"}
	if err := model.DB.Create(&secondTenant).Error; err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: secondTenant.ID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("create second capability: %v", err)
	}
	if _, err := service.GetProduct(secondTenant.ID, product.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant get err=%v", err)
	}
	if _, err := service.SetProductStatus(secondTenant.ID, product.ID, "offline"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant status update err=%v", err)
	}
	if rows, err := service.ListProducts(secondTenant.ID, "restaurant", "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("cross-tenant list rows=%v err=%v", rows, err)
	}
	if _, err := service.CreateSKU(secondTenant.ID, product.ID, CreateCommerceSKUInput{SKUCode: "FOREIGN", Name: "Foreign"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant sku create err=%v", err)
	}
	if _, err := service.UpdateSKU(secondTenant.ID, product.SKUs[0].ID, UpdateCommerceSKUInput{SKUCode: "FOREIGN", Name: "Foreign"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-tenant sku update err=%v", err)
	}
}

func TestCommerceCatalogCannotUseTicketProductAsCommerceProduct(t *testing.T) {
	resetBusinessData(t)
	resetCommerceCatalogData(t)
	tenantID, ticketID := seedSellableProduct(t, "unlimited", 0)
	if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenantID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
		t.Fatalf("create commercial capability: %v", err)
	}
	service := &CommerceCatalogService{}
	if _, err := service.GetProduct(tenantID, ticketID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ticket product was returned as commerce product: %v", err)
	}
	if _, err := service.CreateSKU(tenantID, ticketID, CreateCommerceSKUInput{SKUCode: "TICKET", Name: "Invalid"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ticket product accepted as commerce parent: %v", err)
	}
}
