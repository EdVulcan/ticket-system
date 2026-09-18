package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/testdb"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func openCommerceBoundaryControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testdb.Open(t)
	if err := db.AutoMigrate(
		&model.Tenant{}, &model.TenantBusinessCapability{},
		&model.CommerceProduct{}, &model.CommerceSKU{}, &model.CommerceOptionGroup{}, &model.CommerceOption{},
		&model.CommerceFulfillmentLocation{}, &model.CommerceInventory{}, &model.CommerceCart{}, &model.CommerceCartItem{},
	); err != nil {
		t.Fatalf("migrate commerce controller tables: %v", err)
	}
	previousDB := model.DB
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = previousDB
	})
	return db
}

func createCommerceControllerTenant(t *testing.T, db *gorm.DB, name, businessType string) model.Tenant {
	t.Helper()
	tenant := model.Tenant{
		Name:       name,
		SystemCode: fmt.Sprintf("COMMERCE-CONTROLLER-%d", time.Now().UnixNano()),
		SecretKey:  "commerce-controller-secret",
		Status:     "active",
	}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create controller tenant: %v", err)
	}
	if err := db.Create(&model.TenantBusinessCapability{
		TenantID: tenant.ID, BusinessType: businessType, Status: "active",
	}).Error; err != nil {
		t.Fatalf("create controller capability: %v", err)
	}
	return tenant
}

func invokeCommerceController(t *testing.T, method, path string, tenantID uint, body interface{}, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal controller body: %v", err)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	ctx.Params = params
	ctx.Set("tenant_id", tenantID)
	ctx.Set("scope", "tenant")
	handler(ctx)
	return recorder
}

func commerceControllerProductPayload(tenantID uint, businessType, name, skuCode string) map[string]interface{} {
	return map[string]interface{}{
		// tenant_id is deliberately included to prove the controller ignores
		// client ownership fields and uses the authenticated context instead.
		"tenant_id":     tenantID,
		"business_type": businessType,
		"name":          name,
		"short_title":   name,
		"description":   "controller boundary product",
		"category_name": "boundary",
		"skus": []map[string]interface{}{{
			"sku_code":             skuCode,
			"name":                 "Default SKU",
			"original_price_cents": 1000,
			"price_cents":          900,
			"status":               "active",
		}},
	}
}

func createCommerceControllerDomainData(t *testing.T, tenantID uint, businessType string) (model.CommerceProduct, model.CommerceFulfillmentLocation, model.CommerceInventory) {
	t.Helper()
	catalog := &service.CommerceCatalogService{}
	product, err := catalog.CreateProduct(tenantID, service.CreateCommerceProductInput{
		BusinessType: businessType,
		Name:         fmt.Sprintf("Controller %s product", businessType),
		SKUs: []service.CreateCommerceSKUInput{{
			SKUCode: fmt.Sprintf("CONTROLLER-%s-SKU", businessType), Name: "Default",
			OriginalPriceCents: 1000, PriceCents: 900, Status: "active",
		}},
	})
	if err != nil {
		t.Fatalf("create controller product: %v", err)
	}
	if _, err := catalog.SetProductStatus(tenantID, product.ID, "online"); err != nil {
		t.Fatalf("publish controller product: %v", err)
	}
	ops := &service.CommerceOperationsService{}
	location, err := ops.CreateLocation(tenantID, service.CreateCommerceLocationInput{
		BusinessType: businessType, Name: fmt.Sprintf("Controller %s location", businessType),
	})
	if err != nil {
		t.Fatalf("create controller location: %v", err)
	}
	stock, err := ops.SetInventory(tenantID, service.CommerceInventoryInput{
		SkuID: product.SKUs[0].ID, LocationID: location.ID, AvailableQty: 8,
	})
	if err != nil {
		t.Fatalf("create controller inventory: %v", err)
	}
	return *product, *location, *stock
}

func TestCommerceControllersFailClosedWhenBusinessCapabilitySuspended(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	for _, businessType := range []string{"restaurant", "retail"} {
		t.Run(businessType, func(t *testing.T) {
			tenant := createCommerceControllerTenant(t, db, fmt.Sprintf("%s controller tenant", businessType), businessType)
			product, location, stock := createCommerceControllerDomainData(t, tenant.ID, businessType)
			if err := db.Model(&model.TenantBusinessCapability{}).
				Where("tenant_id = ? AND business_type = ?", tenant.ID, businessType).
				Update("status", "suspended").Error; err != nil {
				t.Fatalf("suspend capability: %v", err)
			}

			catalogController := &CommerceCatalogController{Service: service.CommerceCatalogService{}}
			operationsController := &CommerceOperationsController{Service: service.CommerceOperationsService{}}
			productID := strconv.FormatUint(uint64(product.ID), 10)
			locationID := strconv.FormatUint(uint64(location.ID), 10)
			checks := []struct {
				name     string
				response *httptest.ResponseRecorder
			}{
				{"list products", invokeCommerceController(t, http.MethodGet, "/commerce/products?business_type="+businessType, tenant.ID, nil, nil, catalogController.ListProducts)},
				{"create product", invokeCommerceController(t, http.MethodPost, "/commerce/products", tenant.ID, commerceControllerProductPayload(tenant.ID, businessType, "Blocked", "BLOCKED-SKU"), nil, catalogController.CreateProduct)},
				{"set product status", invokeCommerceController(t, http.MethodPatch, "/commerce/products/"+productID+"/status", tenant.ID, map[string]string{"status": "offline"}, gin.Params{{Key: "id", Value: productID}}, catalogController.SetProductStatus)},
				{"list locations", invokeCommerceController(t, http.MethodGet, "/commerce/locations?business_type="+businessType, tenant.ID, nil, nil, operationsController.ListLocations)},
				{"create location", invokeCommerceController(t, http.MethodPost, "/commerce/locations", tenant.ID, map[string]interface{}{
					"business_type": businessType, "name": "Blocked location", "tenant_id": tenant.ID,
				}, nil, operationsController.CreateLocation)},
				{"set location status", invokeCommerceController(t, http.MethodPatch, "/commerce/locations/"+locationID+"/status", tenant.ID, map[string]string{"status": "inactive"}, gin.Params{{Key: "id", Value: locationID}}, operationsController.SetLocationStatus)},
				{"list inventory", invokeCommerceController(t, http.MethodGet, "/commerce/inventory?business_type="+businessType, tenant.ID, nil, nil, operationsController.ListInventory)},
				{"set inventory", invokeCommerceController(t, http.MethodPut, "/commerce/inventory", tenant.ID, map[string]interface{}{
					"sku_id": stock.SkuID, "location_id": stock.LocationID, "available_qty": 3, "tenant_id": tenant.ID,
				}, nil, operationsController.SetInventory)},
				{"adjust inventory", invokeCommerceController(t, http.MethodPatch, "/commerce/inventory", tenant.ID, map[string]interface{}{
					"sku_id": stock.SkuID, "location_id": stock.LocationID, "delta": 1, "tenant_id": tenant.ID,
				}, nil, operationsController.AdjustInventory)},
			}
			for _, check := range checks {
				if check.response.Code != http.StatusForbidden {
					t.Errorf("%s status=%d body=%s, want 403", check.name, check.response.Code, check.response.Body.String())
				}
			}
		})
	}
}

func TestCommerceControllerTenantIDInRequestBodyCannotWidenAuthorization(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	owner := createCommerceControllerTenant(t, db, "owner controller tenant", "restaurant")
	foreign := createCommerceControllerTenant(t, db, "foreign controller tenant", "retail")
	catalogController := &CommerceCatalogController{Service: service.CommerceCatalogService{}}
	operationsController := &CommerceOperationsController{Service: service.CommerceOperationsService{}}

	ownerPayload := commerceControllerProductPayload(foreign.ID, "restaurant", "Owner product", "OWNER-SKU")
	response := invokeCommerceController(t, http.MethodPost, "/commerce/products", owner.ID, ownerPayload, nil, catalogController.CreateProduct)
	if response.Code != http.StatusCreated {
		t.Fatalf("owner product status=%d body=%s, want 201", response.Code, response.Body.String())
	}
	var created model.CommerceProduct
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode owner product: %v", err)
	}
	if created.TenantID != owner.ID {
		t.Fatalf("request tenant_id changed product owner: got %d want %d", created.TenantID, owner.ID)
	}
	var stored model.CommerceProduct
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TenantID != owner.ID {
		t.Fatalf("stored product tenant=%d, want %d", stored.TenantID, owner.ID)
	}

	// Tenant A has no retail capability. Supplying tenant B's ID and retail
	// domain must not let A borrow B's capability.
	foreignDomainPayload := commerceControllerProductPayload(foreign.ID, "retail", "Borrowed retail product", "BORROWED-RETAIL-SKU")
	response = invokeCommerceController(t, http.MethodPost, "/commerce/products", owner.ID, foreignDomainPayload, nil, catalogController.CreateProduct)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign capability status=%d body=%s, want 403", response.Code, response.Body.String())
	}
	var foreignProductCount int64
	if err := db.Model(&model.CommerceProduct{}).Where("tenant_id = ? AND name = ?", foreign.ID, "Borrowed retail product").Count(&foreignProductCount).Error; err != nil {
		t.Fatal(err)
	}
	if foreignProductCount != 0 {
		t.Fatalf("foreign product was created by body tenant_id: %d", foreignProductCount)
	}

	locationResponse := invokeCommerceController(t, http.MethodPost, "/commerce/locations", owner.ID, map[string]interface{}{
		"tenant_id": foreign.ID, "business_type": "restaurant", "name": "Owner location",
	}, nil, operationsController.CreateLocation)
	if locationResponse.Code != http.StatusCreated {
		t.Fatalf("owner location status=%d body=%s, want 201", locationResponse.Code, locationResponse.Body.String())
	}
	var location model.CommerceFulfillmentLocation
	if err := json.Unmarshal(locationResponse.Body.Bytes(), &location); err != nil {
		t.Fatalf("decode owner location: %v", err)
	}
	if location.TenantID != owner.ID {
		t.Fatalf("request tenant_id changed location owner: got %d want %d", location.TenantID, owner.ID)
	}

	stockResponse := invokeCommerceController(t, http.MethodPut, "/commerce/inventory", owner.ID, map[string]interface{}{
		"tenant_id": foreign.ID, "sku_id": created.SKUs[0].ID, "location_id": location.ID, "available_qty": 6,
	}, nil, operationsController.SetInventory)
	if stockResponse.Code != http.StatusOK {
		t.Fatalf("owner inventory status=%d body=%s, want 200", stockResponse.Code, stockResponse.Body.String())
	}
	var storedStock model.CommerceInventory
	if err := db.Where("sku_id = ? AND location_id = ?", created.SKUs[0].ID, location.ID).First(&storedStock).Error; err != nil {
		t.Fatal(err)
	}
	if storedStock.TenantID != owner.ID || storedStock.AvailableQty != 6 {
		t.Fatalf("request tenant_id changed inventory owner/facts: %+v", storedStock)
	}
}

func TestCommerceControllerCrossTenantProductIsNotReadableOrWritable(t *testing.T) {
	db := openCommerceBoundaryControllerDB(t)
	owner := createCommerceControllerTenant(t, db, "owner query tenant", "restaurant")
	foreign := createCommerceControllerTenant(t, db, "foreign query tenant", "restaurant")
	product, _, _ := createCommerceControllerDomainData(t, owner.ID, "restaurant")
	catalogController := &CommerceCatalogController{Service: service.CommerceCatalogService{}}
	productID := strconv.FormatUint(uint64(product.ID), 10)

	response := invokeCommerceController(t, http.MethodGet, "/commerce/products/"+productID, foreign.ID, nil, gin.Params{{Key: "id", Value: productID}}, catalogController.GetProduct)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status=%d body=%s, want 404", response.Code, response.Body.String())
	}
	response = invokeCommerceController(t, http.MethodPatch, "/commerce/products/"+productID+"/status", foreign.ID, map[string]string{"status": "offline"}, gin.Params{{Key: "id", Value: productID}}, catalogController.SetProductStatus)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant update status=%d body=%s, want 404", response.Code, response.Body.String())
	}
	var stored model.CommerceProduct
	if err := db.First(&stored, product.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TenantID != owner.ID || stored.Status != "online" {
		t.Fatalf("cross-tenant update changed owner product: %+v", stored)
	}
}
