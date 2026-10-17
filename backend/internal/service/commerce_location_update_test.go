package service

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func TestCommerceLocationUpdateRejectsInvalidScopeOrInputWithoutMutation(t *testing.T) {
	checks := []struct {
		name    string
		prepare func(*testing.T, uint) uint
		input   CreateCommerceLocationInput
		wantErr error
	}{
		{
			name: "another tenant cannot edit the location",
			prepare: func(t *testing.T, _ uint) uint {
				return createCommerceBoundaryTenant(t, "retail")
			},
			wantErr: gorm.ErrRecordNotFound,
		},
		{
			name: "another enabled business cannot move the location",
			prepare: func(t *testing.T, tenantID uint) uint {
				if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenantID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
					t.Fatal(err)
				}
				return tenantID
			},
			input:   CreateCommerceLocationInput{BusinessType: "restaurant", Name: "Moved", LocationType: "store", Status: "inactive"},
			wantErr: gorm.ErrRecordNotFound,
		},
		{
			name: "frozen tenant cannot edit",
			prepare: func(t *testing.T, tenantID uint) uint {
				if err := model.DB.Model(&model.Tenant{}).Where("id = ?", tenantID).Update("status", "frozen").Error; err != nil {
					t.Fatal(err)
				}
				return tenantID
			},
			wantErr: ErrTenantUnavailable,
		},
		{
			name: "another active business cannot bypass suspended capability",
			prepare: func(t *testing.T, tenantID uint) uint {
				if err := model.DB.Model(&model.TenantBusinessCapability{}).Where("tenant_id = ? AND business_type = ?", tenantID, "retail").Update("status", "suspended").Error; err != nil {
					t.Fatal(err)
				}
				if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenantID, BusinessType: "restaurant", Status: "active"}).Error; err != nil {
					t.Fatal(err)
				}
				return tenantID
			},
			wantErr: ErrCapabilityInactive,
		},
		{name: "business is required", input: CreateCommerceLocationInput{Name: "Invalid"}, wantErr: ErrCommerceLocationInvalid},
		{name: "unsupported business", input: CreateCommerceLocationInput{BusinessType: "scenic", Name: "Invalid"}, wantErr: ErrCommerceLocationInvalid},
		{name: "name is required", input: CreateCommerceLocationInput{BusinessType: "retail", Name: "  "}, wantErr: ErrCommerceLocationInvalid},
		{name: "name is bounded", input: CreateCommerceLocationInput{BusinessType: "retail", Name: strings.Repeat("仓", 121)}, wantErr: ErrCommerceLocationInvalid},
		{name: "unsupported location type", input: CreateCommerceLocationInput{BusinessType: "retail", Name: "Invalid", LocationType: "scenic"}, wantErr: ErrCommerceLocationInvalid},
		{name: "unsupported status", input: CreateCommerceLocationInput{BusinessType: "retail", Name: "Invalid", Status: "deleted"}, wantErr: ErrCommerceLocationInvalid},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			tenantID := newCommerceTenant(t, "retail", "active")
			ops := &CommerceOperationsService{}
			location, err := ops.CreateLocation(tenantID, CreateCommerceLocationInput{BusinessType: "retail", Name: "Original warehouse", LocationType: "warehouse"})
			if err != nil {
				t.Fatal(err)
			}
			if err := model.DB.First(location, location.ID).Error; err != nil {
				t.Fatal(err)
			}
			actorTenantID := tenantID
			if check.prepare != nil {
				actorTenantID = check.prepare(t, tenantID)
			}
			input := check.input
			if input == (CreateCommerceLocationInput{}) {
				input = CreateCommerceLocationInput{BusinessType: "retail", Name: "Edited", LocationType: "pickup", Status: "inactive"}
			}
			if _, err := ops.UpdateLocation(actorTenantID, location.ID, input); !errors.Is(err, check.wantErr) {
				t.Fatalf("update error=%v, want %v", err, check.wantErr)
			}
			var stored model.CommerceFulfillmentLocation
			if err := model.DB.Where("id = ? AND tenant_id = ?", location.ID, tenantID).First(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, *location) {
				t.Fatalf("rejected update changed location: before=%+v after=%+v", *location, stored)
			}
		})
	}
}

func TestCommerceLocationUpdateKeepsIdentityInventoryCartAndOrder(t *testing.T) {
	for _, businessType := range []string{"restaurant", "retail"} {
		t.Run(businessType, func(t *testing.T) {
			fixture := createCommerceBoundaryDomainFixture(t, newCommerceTenant(t, businessType, "active"), businessType)
			ops := &CommerceOperationsService{}
			order, err := ops.CheckoutCart(fixture.tenantID, fixture.cart.ID, CommerceCheckoutCartInput{
				IdempotencyKey: "location-edit-order", ContactName: "Customer", ContactPhone: "13800138000",
				ShippingAddress: `{"recipient":"Customer","detail":"Main street 1"}`,
			})
			if err != nil {
				t.Fatal(err)
			}
			stock := loadCommerceInventory(t, fixture.tenantID, fixture.sku.ID, fixture.location.ID)
			var cart model.CommerceCart
			var beforeOrder model.CommerceOrder
			if err := model.DB.First(&cart, fixture.cart.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Preload("Items").First(&beforeOrder, order.ID).Error; err != nil {
				t.Fatal(err)
			}

			updated, err := ops.UpdateLocation(fixture.tenantID, fixture.location.ID, CreateCommerceLocationInput{
				BusinessType: " " + businessType + " ", Name: " 精选发货点 ", LocationType: " pickup ", Status: " inactive ",
			})
			if err != nil {
				t.Fatal(err)
			}
			if updated.ID != fixture.location.ID || updated.TenantID != fixture.tenantID || updated.BusinessType != businessType || updated.Name != "精选发货点" || updated.LocationType != "pickup" || updated.Status != "inactive" {
				t.Fatalf("updated location=%+v", updated)
			}
			var stored model.CommerceFulfillmentLocation
			if err := model.DB.First(&stored, fixture.location.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.ID != updated.ID || stored.TenantID != updated.TenantID || stored.BusinessType != updated.BusinessType || stored.Name != updated.Name || stored.LocationType != updated.LocationType || stored.Status != updated.Status {
				t.Fatalf("update response does not match stored location: response=%+v stored=%+v", updated, stored)
			}
			if got := loadCommerceInventory(t, fixture.tenantID, fixture.sku.ID, fixture.location.ID); !reflect.DeepEqual(got, stock) {
				t.Fatalf("metadata edit changed inventory: before=%+v after=%+v", stock, got)
			}
			var afterCart model.CommerceCart
			var afterOrder model.CommerceOrder
			if err := model.DB.First(&afterCart, fixture.cart.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.Preload("Items").First(&afterOrder, order.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterCart, cart) || !reflect.DeepEqual(afterOrder, beforeOrder) {
				t.Fatal("metadata edit changed the existing cart, order or item snapshots")
			}
			rows, err := ops.ListLocations(fixture.tenantID, businessType, "")
			if err != nil || len(rows) != 1 || rows[0].ID != updated.ID || rows[0].Name != updated.Name {
				t.Fatalf("updated location list=%+v err=%v", rows, err)
			}
		})
	}
}
