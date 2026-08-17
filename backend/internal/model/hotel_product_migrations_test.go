package model

import (
	"testing"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm"
)

type hotelProductModelFixture struct {
	tenant   Tenant
	product  Product
	hotel    HotelProperty
	room     HotelRoomType
	rate     HotelRatePlan
	profile  HotelProduct
	revision HotelProductRevision
}

func newHotelProductModelFixture(t *testing.T, db *gorm.DB, dbTenant *Tenant, suffix, saleMode string) hotelProductModelFixture {
	t.Helper()
	product := Product{
		TenantID: dbTenant.ID, Name: "Hotel Product " + suffix, ProductKind: "hotel",
		Type: "online", Status: "offline",
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	hotel := HotelProperty{TenantID: dbTenant.ID, Code: "HP-HOTEL-" + suffix, Name: "Hotel " + suffix, Status: "active"}
	if err := db.Create(&hotel).Error; err != nil {
		t.Fatal(err)
	}
	room := HotelRoomType{TenantID: dbTenant.ID, HotelID: hotel.ID, Code: "HP-ROOM-" + suffix, Name: "Room " + suffix, Status: "active"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	rate := HotelRatePlan{TenantID: dbTenant.ID, HotelID: hotel.ID, RoomTypeID: room.ID, Code: "HP-RATE-" + suffix, Name: "Rate " + suffix, RetailPriceCents: 12800, SettlementPriceCents: 10000, Status: "active"}
	if err := db.Create(&rate).Error; err != nil {
		t.Fatal(err)
	}
	profile := HotelProduct{
		ProductID: product.ID, TenantID: dbTenant.ID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: saleMode, BaseRetailPriceCents: 12800, BaseSettlementPriceCents: 10000,
		Nights: 1, RoomsPerPackage: 1, VoucherValidityDays: 30, Status: "offline",
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	revision := HotelProductRevision{
		HotelProductID: profile.ID, TenantID: dbTenant.ID, ProductID: product.ID, Version: 1,
		HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID, SaleMode: saleMode,
		BaseRetailPriceCents: 12800, BaseSettlementPriceCents: 10000, Nights: 1, RoomsPerPackage: 1,
		VoucherValidityDays: 30,
	}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&profile).Update("current_revision_id", revision.ID).Error; err != nil {
		t.Fatal(err)
	}
	profile.CurrentRevisionID = revision.ID
	return hotelProductModelFixture{tenant: *dbTenant, product: product, hotel: hotel, room: room, rate: rate, profile: profile, revision: revision}
}

func TestPostgresSchema100HotelProductOwnershipAndCalendarGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Hotel Product Guard A", SystemCode: "HOTEL-PRODUCT-GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Hotel Product Guard B", SystemCode: "HOTEL-PRODUCT-GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	fixture := newHotelProductModelFixture(t, db, &first, "A", "calendar_room")
	stayDate := time.Now().AddDate(0, 0, 7)
	price := HotelProductCalendarPrice{
		TenantID: first.ID, HotelProductID: fixture.profile.ID, HotelProductRevisionID: fixture.revision.ID,
		StayDate: stayDate, RetailPriceCents: 15800, SettlementPriceCents: 12000,
	}
	if err := db.Create(&price).Error; err != nil {
		t.Fatalf("valid hotel product calendar price was rejected: %v", err)
	}
	duplicate := price
	duplicate.Base = Base{}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate hotel product calendar price was accepted")
	}
	if err := db.Model(&price).Update("retail_price_cents", 16800).Error; err == nil {
		t.Fatal("hotel product calendar price snapshot was mutable")
	}
	if err := db.Model(&fixture.revision).Update("base_retail_price_cents", 16800).Error; err == nil {
		t.Fatal("hotel product revision snapshot was mutable")
	}
	wrongTenantProduct := Product{TenantID: second.ID, Name: "Hotel Product B", ProductKind: "hotel", Type: "online", Status: "offline"}
	if err := db.Create(&wrongTenantProduct).Error; err != nil {
		t.Fatal(err)
	}
	wrongTenant := HotelProduct{
		ProductID: wrongTenantProduct.ID, TenantID: second.ID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 12800, BaseSettlementPriceCents: 10000, Nights: 1, RoomsPerPackage: 1, Status: "offline",
	}
	if err := db.Create(&wrongTenant).Error; err == nil {
		t.Fatal("cross-tenant hotel product resource binding was accepted")
	}
	invalidModeProduct := Product{TenantID: first.ID, Name: "Invalid Sale Mode", ProductKind: "hotel", Type: "online", Status: "offline"}
	if err := db.Create(&invalidModeProduct).Error; err != nil {
		t.Fatal(err)
	}
	invalidMode := HotelProduct{
		ProductID: invalidModeProduct.ID, TenantID: first.ID, HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID,
		SaleMode: "walk_in", BaseRetailPriceCents: 12800, BaseSettlementPriceCents: 10000, Nights: 1, RoomsPerPackage: 1, Status: "offline",
	}
	if err := db.Create(&invalidMode).Error; err == nil {
		t.Fatal("unsupported hotel product sale mode was accepted")
	}
	presale := newHotelProductModelFixture(t, db, &first, "PRESALE", "presale_room")
	presaleCalendar := HotelProductCalendarPrice{
		TenantID: first.ID, HotelProductID: presale.profile.ID, HotelProductRevisionID: presale.revision.ID,
		StayDate: stayDate, RetailPriceCents: 15800, SettlementPriceCents: 12000,
	}
	if err := db.Create(&presaleCalendar).Error; err == nil {
		t.Fatal("presale-room revision accepted a stay-date calendar price")
	}
	wrongRevision := newHotelProductModelFixture(t, db, &first, "OTHER", "calendar_room")
	wrongCalendar := HotelProductCalendarPrice{
		TenantID: first.ID, HotelProductID: fixture.profile.ID, HotelProductRevisionID: wrongRevision.revision.ID,
		StayDate: stayDate.AddDate(0, 0, 1), RetailPriceCents: 15800, SettlementPriceCents: 12000,
	}
	if err := db.Create(&wrongCalendar).Error; err == nil {
		t.Fatal("calendar price with another hotel product revision was accepted")
	}
	order := Order{OrderNo: "HOTEL-PRODUCT-GUARD-ORDER", TenantID: first.ID, Status: "paid", Channel: "online"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	item := OrderItem{
		OrderID: order.ID, ProductID: fixture.product.ID, ProductName: fixture.product.Name, Quantity: 1,
		FulfillmentProductID: fixture.product.ID, FulfillmentTenantID: first.ID, FulfillmentScenicAreaID: 0,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("hotel product order item was rejected: %v", err)
	}
	checkIn := stayDate
	checkOut := stayDate.AddDate(0, 0, 1)
	entitlement := HotelProductEntitlement{
		EntitlementNo: "HOTEL-PRODUCT-GUARD-ENTITLEMENT", SalesTenantID: first.ID, SupplierTenantID: first.ID,
		OrderID: order.ID, OrderItemID: item.ID, HotelProductID: fixture.profile.ID, HotelProductRevisionID: fixture.revision.ID,
		CheckInDate: &checkIn, CheckOutDate: &checkOut, Rooms: 1,
		HotelName: fixture.hotel.Name, RoomTypeName: fixture.room.Name, RatePlanName: fixture.rate.Name,
		GuestName: "Guest", ContactPhone: "13800138000", RetailPriceCents: 15800, SettlementPriceCents: 12000, PriceSource: "calendar",
		Status: "pending_booking", ValidFrom: time.Now(), ValidUntil: time.Now().AddDate(0, 1, 0),
	}
	if err := db.Create(&entitlement).Error; err != nil {
		t.Fatalf("valid hotel product entitlement was rejected: %v", err)
	}
	reservation := HotelProductReservation{
		ReservationNo: "HOTEL-PRODUCT-GUARD-RESERVATION", SalesTenantID: first.ID, SupplierTenantID: first.ID,
		OrderID: order.ID, OrderItemID: item.ID, EntitlementID: entitlement.ID, HotelProductID: fixture.profile.ID, HotelProductRevisionID: fixture.revision.ID,
		HotelID: fixture.hotel.ID, RoomTypeID: fixture.room.ID, RatePlanID: fixture.rate.ID,
		HotelName: fixture.hotel.Name, RoomTypeName: fixture.room.Name, RatePlanName: fixture.rate.Name,
		CheckInDate: checkIn, CheckOutDate: checkOut, Rooms: 1, GuestName: "Guest", ContactPhone: "13800138000",
		RetailPriceCents: 15800, SettlementPriceCents: 12000, PriceSource: "calendar", Status: "reserved",
	}
	if err := db.Create(&reservation).Error; err != nil {
		t.Fatalf("valid hotel product reservation was rejected: %v", err)
	}
	if err := db.Model(&entitlement).Updates(map[string]interface{}{"reservation_id": reservation.ID, "status": "booked"}).Error; err != nil {
		t.Fatal(err)
	}
	crossTenantEntitlement := entitlement
	crossTenantEntitlement.Base = Base{}
	crossTenantEntitlement.EntitlementNo = "HOTEL-PRODUCT-GUARD-CROSS-TENANT"
	crossTenantEntitlement.SalesTenantID = second.ID
	if err := db.Create(&crossTenantEntitlement).Error; err == nil {
		t.Fatal("cross-tenant hotel product entitlement was accepted")
	}
}

func TestPostgresSchema100BackfillsProductKindsWithoutCreatingHotelProducts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{Name: "Hotel Product Migration", SystemCode: "HOTEL-PRODUCT-MIGRATION", SecretKey: "m", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: tenant.ID, Code: "HP-MIGRATION-SCENIC", Name: "Migration Scenic", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	plain := Product{TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "Legacy Ticket", Type: "online", Status: "offline"}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	packageProduct := Product{TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "Legacy Package", Type: "online", Status: "offline"}
	if err := db.Create(&packageProduct).Error; err != nil {
		t.Fatal(err)
	}
	hotel := HotelProperty{TenantID: tenant.ID, Code: "HP-MIGRATION-HOTEL", Name: "Migration Hotel", Status: "active"}
	if err := db.Create(&hotel).Error; err != nil {
		t.Fatal(err)
	}
	room := HotelRoomType{TenantID: tenant.ID, HotelID: hotel.ID, Code: "HP-MIGRATION-ROOM", Name: "Migration Room", Status: "active"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	rate := HotelRatePlan{TenantID: tenant.ID, HotelID: hotel.ID, RoomTypeID: room.ID, Code: "HP-MIGRATION-RATE", Name: "Migration Rate", RetailPriceCents: 12800, SettlementPriceCents: 10000, Status: "active"}
	if err := db.Create(&rate).Error; err != nil {
		t.Fatal(err)
	}
	legacyPackage := ScenicHotelPackage{TenantID: tenant.ID, ProductID: packageProduct.ID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID, Nights: 1, RoomsPerPackage: 1, BookingMode: "at_purchase", Status: "offline"}
	if err := db.Create(&legacyPackage).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		DROP TRIGGER IF EXISTS ownership_guard ON products;
		ALTER TABLE products DROP CONSTRAINT IF EXISTS chk_products_product_kind;
		ALTER TABLE products DROP COLUMN product_kind;
		DROP TABLE IF EXISTS hotel_product_reservations;
		DROP TABLE IF EXISTS hotel_product_entitlements;
		DROP TABLE IF EXISTS hotel_product_calendar_prices;
		DROP TABLE IF EXISTS hotel_product_revisions;
		DROP TABLE IF EXISTS hotel_products;
		DELETE FROM schema_migrations WHERE version >= 100;
		INSERT INTO schema_migrations (version, name, applied_at) VALUES (99, 'hotel rate plan stay-date price calendar', NOW()) ON CONFLICT (version) DO NOTHING;
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var migratedPlain, migratedPackage Product
	if err := db.First(&migratedPlain, plain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&migratedPackage, packageProduct.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migratedPlain.ProductKind != "ticket" || migratedPackage.ProductKind != "scenic_hotel_package" {
		t.Fatalf("unexpected product kind backfill: ticket=%q package=%q", migratedPlain.ProductKind, migratedPackage.ProductKind)
	}
	var profiles int64
	if err := db.Model(&HotelProduct{}).Count(&profiles).Error; err != nil {
		t.Fatal(err)
	}
	if profiles != 0 {
		t.Fatalf("schema 100 unexpectedly backfilled %d standalone hotel products", profiles)
	}
}
