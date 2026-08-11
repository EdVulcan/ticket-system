package service

import (
	"errors"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func seedHotelSupplier(t *testing.T, code string) model.Tenant {
	t.Helper()
	tenant := model.Tenant{Name: code, SystemCode: code, Status: "active"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TenantCapability{TenantID: tenant.ID, Capability: "supplier", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.SupplierBusinessType{TenantID: tenant.ID, BusinessType: "hotel", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	return tenant
}

func TestHotelCatalogAndInventoryLifecycle(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-LIFECYCLE")
	service := &HotelService{}

	hotel, err := service.CreateProperty(tenant.ID, 11, HotelPropertyInput{Code: " h001 ", Name: " 云门山酒店 ", Address: "景区东门"})
	if err != nil {
		t.Fatal(err)
	}
	if hotel.Code != "H001" || hotel.CheckInTime != "14:00" || hotel.CheckOutTime != "12:00" {
		t.Fatalf("normalized hotel=%+v", hotel)
	}
	room, err := service.CreateRoomType(tenant.ID, hotel.ID, 11, HotelRoomTypeInput{Code: "queen", Name: "大床房", MaxGuests: 2, BedType: "1张大床"})
	if err != nil {
		t.Fatal(err)
	}
	rate, err := service.CreateRatePlan(tenant.ID, hotel.ID, room.ID, 11, HotelRatePlanInput{Code: "breakfast", Name: "含双早", RetailPriceCents: 58800, SettlementPriceCents: 50000, BreakfastCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rate.RetailPriceCents != 58800 || rate.BreakfastCount != 2 {
		t.Fatalf("rate plan=%+v", rate)
	}

	date := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	if err := service.SetInventory(tenant.ID, hotel.ID, room.ID, 11, []HotelInventoryInput{{StayDate: date, Capacity: 8}}); err != nil {
		t.Fatal(err)
	}
	rows, err := service.ListInventory(tenant.ID, hotel.ID, room.ID, date, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Capacity != 8 || rows[0].Reserved != 0 || rows[0].Sold != 0 {
		t.Fatalf("inventory=%+v", rows)
	}
	if err := model.DB.Model(&rows[0]).Updates(map[string]interface{}{"reserved": 2, "sold": 3}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.SetInventory(tenant.ID, hotel.ID, room.ID, 11, []HotelInventoryInput{{StayDate: date, Capacity: 4}}); err == nil {
		t.Fatal("inventory capacity was reduced below reserved plus sold")
	}
	if err := service.SetInventory(tenant.ID, hotel.ID, room.ID, 11, []HotelInventoryInput{{StayDate: date, Capacity: 5, Closed: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestHotelCatalogRejectsCrossTenantOwnership(t *testing.T) {
	resetBusinessData(t)
	owner := seedHotelSupplier(t, "HOTEL-OWNER")
	other := seedHotelSupplier(t, "HOTEL-OTHER")
	service := &HotelService{}
	hotel, err := service.CreateProperty(owner.ID, 1, HotelPropertyInput{Code: "OWN", Name: "Owner Hotel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRoomType(other.ID, hotel.ID, 2, HotelRoomTypeInput{Code: "CROSS", Name: "Cross Tenant", MaxGuests: 2}); err == nil {
		t.Fatal("cross-tenant room type was accepted")
	}

	rogue := model.HotelRoomType{TenantID: other.ID, HotelID: hotel.ID, Code: "RAW", Name: "Raw Cross Tenant", MaxGuests: 2, Status: "active"}
	if err := model.DB.Create(&rogue).Error; err == nil {
		t.Fatal("database ownership guard accepted cross-tenant hotel room type")
	}
	if err := service.DeleteProperty(owner.ID, hotel.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProperty(owner.ID, 1, HotelPropertyInput{Code: "OWN", Name: "Recreated Hotel"}); err != nil {
		t.Fatalf("reuse soft-deleted hotel code: %v", err)
	}
}

func TestHotelWritesRequireActiveHotelBusiness(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-SUSPENDED")
	service := &HotelService{}
	hotel, err := service.CreateProperty(tenant.ID, 1, HotelPropertyInput{Code: "SUSP", Name: "Suspend Hotel"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.SupplierBusinessType{}).Where("tenant_id = ? AND business_type = ?", tenant.ID, "hotel").Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateRoomType(tenant.ID, hotel.ID, 1, HotelRoomTypeInput{Code: "BLOCK", Name: "Blocked", MaxGuests: 2}); !errors.Is(err, ErrSupplierBusinessTypeInactive) {
		t.Fatalf("suspended hotel create error=%v", err)
	}
	rows, err := service.ListProperties(tenant.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("historical hotel read rows=%+v err=%v", rows, err)
	}
}

func TestHotelInventoryInputValidation(t *testing.T) {
	service := &HotelService{}
	if err := service.SetInventory(1, 1, 1, 1, nil); err == nil {
		t.Fatal("empty inventory update was accepted")
	}
	if _, _, err := parseHotelInventoryRange("2026-08-02", "2026-12-31"); err == nil {
		t.Fatal("oversized inventory query range was accepted")
	}
	if _, err := normalizeRatePlanInput(HotelRatePlanInput{Code: "X", Name: "X", RetailPriceCents: 100, SettlementPriceCents: 101}); err == nil {
		t.Fatal("settlement price above retail was accepted")
	}
	if _, err := normalizeRoomTypeInput(HotelRoomTypeInput{Code: "X", Name: "X", MaxGuests: 0}); err == nil {
		t.Fatal("zero guest room type was accepted")
	}
}
