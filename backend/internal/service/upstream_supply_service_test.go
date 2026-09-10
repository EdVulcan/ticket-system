package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"
)

func TestUpstreamSupplyDraftIsolationAndLocalOrderSnapshot(t *testing.T) {
	resetBusinessData(t)
	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	otherTenantID, otherProductID := seedSellableProduct(t, "unlimited", 0)
	s := UpstreamSupplyService{}
	if _, err := s.GetProduct(otherTenantID, productID); err == nil {
		t.Fatal("cross tenant read succeeded")
	}
	c, err := s.CreateConnection(tenantID, 0, "admin", UpstreamConnectionInput{Name: "供应测试", Provider: "zhiyoubao"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "draft" {
		t.Fatal("connection activated")
	}
	if _, err := s.SetProduct(otherTenantID, otherProductID, 0, "admin", ProductSupplyInput{UpstreamConnectionID: c.ID, ExternalProductCode: "OPAQUE"}); err == nil {
		t.Fatal("cross tenant connection accepted")
	}
	if _, err := s.SetProduct(tenantID, productID, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: c.ID, ExternalProductCode: "OPAQUE"}); !errors.Is(err, ErrUpstreamSupplyNotReady) {
		t.Fatalf("live activation: %v", err)
	}
	if _, err := s.SetProduct(tenantID, productID, 0, "admin", ProductSupplyInput{UpstreamConnectionID: c.ID}); err == nil {
		t.Fatal("incomplete mapping accepted")
	}
	view, err := s.SetProduct(tenantID, productID, 0, "admin", ProductSupplyInput{UpstreamConnectionID: c.ID, ExternalProductCode: "OPAQUE"})
	if err != nil || view.Enabled || view.Ready || view.Status != "draft" {
		t.Fatalf("draft: %+v %v", view, err)
	}
	order := model.Order{TenantID: tenantID, Channel: "online", Items: []model.OrderItem{{ProductID: productID, Quantity: 1}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_item_id = ?", order.Items[0].ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != "local" || snapshot.ConnectionID != 0 || snapshot.ProductID != productID || snapshot.FulfillmentTenantID != tenantID {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	if _, err := s.SetProduct(tenantID, productID, 0, "admin", ProductSupplyInput{UpstreamConnectionID: c.ID, ExternalProductCode: "CHANGED"}); err != nil {
		t.Fatal(err)
	}
	var after model.OrderItemSupplySnapshot
	if _, err := s.SetProduct(tenantID, productID, 0, "admin", ProductSupplyInput{}); err == nil {
		t.Fatal("empty configuration incorrectly reported success")
	}
	view, err = s.GetProduct(tenantID, productID)
	if err != nil || view.ExternalProductCode != "CHANGED" {
		t.Fatalf("rejected clear changed existing draft: %+v %v", view, err)
	}
	if err := model.DB.First(&after, snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after != snapshot {
		t.Fatal("draft edit rewrote sold snapshot")
	}
	if err := model.DB.Model(&snapshot).Update("product_id", otherProductID).Error; err == nil {
		t.Fatal("snapshot was mutable")
	}
	var mapping model.UpstreamProductMapping
	if err := model.DB.Where("product_id = ?", productID).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ProductSupplyConfig{}).Where("product_id = ?", productID).Update("active_mapping_id", mapping.ID).Error; err == nil {
		t.Fatal("database accepted activation without adapter")
	}
	if err := model.DB.Model(&mapping).Update("tenant_id", otherTenantID).Error; err == nil {
		t.Fatal("database accepted cross tenant mapping")
	}
}

func TestUpstreamSupplyRejectsDistributorListing(t *testing.T) {
	resetBusinessData(t)
	f := seedDistributionScenario(t)
	_ = f
	// Distributor products cannot acquire supplier-owned configuration; the
	// ordinary supplier/distributor order regression exercises the same guard.
	var listing model.Product
	if err := model.DB.Where("product_offer_id <> 0").First(&listing).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (UpstreamSupplyService{}).GetProduct(listing.TenantID, listing.ID); err == nil {
		t.Fatal("distributor listing owns upstream configuration")
	}
}
