package model

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/testdb"
	"time"

	"gorm.io/gorm"
)

func TestPostgresMigrationsReachCurrentVersionAndAreIdempotent(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest migration=%d, want %d", latest.Version, CurrentPostgresSchemaVersion)
	}
	for _, table := range []interface{}{
		&ProductRevision{}, &LedgerEntry{}, &ChannelAccount{}, &TourGroup{}, &POSShift{},
		&SettlementStatement{}, &AfterSaleRequest{}, &ChannelReservation{}, &FinancialDocument{},
		&TeamSettlementStatement{}, &ChannelReconciliation{}, &OrderVisitor{}, &BundleProduct{},
		&CtripOrderLink{}, &CtripOrderItem{}, &XiaohongshuWebhookEvent{}, &SupplierBusinessType{},
		&HotelProperty{}, &HotelRoomType{}, &HotelRatePlan{}, &HotelRoomInventory{},
		&ScenicHotelPackage{}, &ScenicHotelPackageEntitlement{}, &HotelReservation{},
		&XiaohongshuBookingOperation{}, &XiaohongshuOrderOperation{},
		&CatalogBatchChangePlan{}, &CatalogBatchChangeLine{},
		&PlatformAIConfig{}, &AIUsageMonth{},
		&AgentTask{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table for %T is missing", table)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{
		{&Payment{}, "idx_payment_idempotency"},
		{&Refund{}, "idx_refund_allocation_sequence"},
		{&PlatformUser{}, "idx_platform_users_initial_admin"},
		{&ChannelRequest{}, "idx_channel_request"},
		{&CtripOrderLink{}, "idx_ctrip_order_account_ota"},
		{&TravelContract{}, "idx_travel_contract_scope_no"},
		{&TourGroup{}, "idx_team_active_fulfillment_group"},
		{&TourGroupMember{}, "idx_team_member_active_ticket"},
		{&XiaohongshuWebhookEvent{}, "idx_xhs_webhook_payload"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s is missing", index.name)
		}
	}
	var channelRequestIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_channel_request'`).Scan(&channelRequestIndex).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(channelRequestIndex, "channel_account_id") || !strings.Contains(channelRequestIndex, "endpoint") || !strings.Contains(channelRequestIndex, "request_id") {
		t.Fatalf("channel request idempotency index has wrong scope: %s", channelRequestIndex)
	}
	defaultStatusTenant := Tenant{Name: "Default Status Tenant", SystemCode: "SUPPLIER-BUSINESS-DEFAULT", Status: "active"}
	if err := db.Create(&defaultStatusTenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, created_at, updated_at)
		VALUES (?, ?, NOW(), NOW())
	`, defaultStatusTenant.ID, "hotel").Error; err != nil {
		t.Fatal(err)
	}
	var defaultStatus string
	if err := db.Raw(`
		SELECT status FROM supplier_business_types
		WHERE tenant_id = ? AND business_type = ?
	`, defaultStatusTenant.ID, "hotel").Scan(&defaultStatus).Error; err != nil {
		t.Fatal(err)
	}
	if defaultStatus != "suspended" {
		t.Fatalf("database default supplier business status=%q, want suspended", defaultStatus)
	}
	var hotelStatusConstraint string
	if err := db.Raw(`
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'chk_hotel_reservations_status'
	`).Scan(&hotelStatusConstraint).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hotelStatusConstraint, "checked_in") || !strings.Contains(hotelStatusConstraint, "checked_out") || !strings.Contains(hotelStatusConstraint, "no_show") {
		t.Fatalf("hotel reservation status constraint=%q", hotelStatusConstraint)
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, status, created_at, updated_at)
		VALUES (?, 'restaurant', 'active', NOW(), NOW())
	`, defaultStatusTenant.ID).Error; err == nil {
		t.Fatal("database accepted unsupported supplier business type")
	}
	if err := db.Exec(`
		INSERT INTO supplier_business_types (tenant_id, business_type, status, created_at, updated_at)
		VALUES (?, 'scenic', 'pending', NOW(), NOW())
	`, defaultStatusTenant.ID).Error; err == nil {
		t.Fatal("database accepted unsupported supplier business status")
	}
}

func TestPostgresSchema90MigratesLegacyAIOutputDefault(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	legacy := PlatformAIConfig{
		ConfigKey: "legacy-output-budget", Provider: "deepseek", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-chat", MaxOutputTokens: 1200,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 89, Name: "pre-provider-default-output-budget", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var migrated PlatformAIConfig
	if err := db.First(&migrated, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.MaxOutputTokens != 0 {
		t.Fatalf("legacy max output tokens=%d, want provider-default sentinel 0", migrated.MaxOutputTokens)
	}
	var columnDefault string
	if err := db.Raw(`
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'platform_ai_configs'
		  AND column_name = 'max_output_tokens'
	`).Scan(&columnDefault).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(columnDefault, "0") {
		t.Fatalf("max_output_tokens column default=%q, want zero", columnDefault)
	}
}

func TestPostgresSchema89AgentTaskOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 89 {
		t.Fatalf("current schema version=%d, want at least 89", CurrentPostgresSchemaVersion)
	}
	first := Tenant{Name: "Agent Task Guard A", SystemCode: "AGENT-TASK-GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Agent Task Guard B", SystemCode: "AGENT-TASK-GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	valid := AgentTask{TenantID: first.ID, ActorUserID: 1, ActorRole: "admin", OperationType: "ticket_product_create", State: "collecting", InputText: "创建成人票", ContextJSON: `{}`, MissingJSON: `[]`, IdempotencyKey: "agent-task-guard-key", Version: 1, ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	unknownTenant := valid
	unknownTenant.ID = 0
	unknownTenant.TenantID = second.ID + 999
	unknownTenant.IdempotencyKey = "agent-task-guard-unknown"
	if err := db.Create(&unknownTenant).Error; err == nil {
		t.Fatal("agent task for unknown tenant was accepted")
	}
	invalidState := valid
	invalidState.ID = 0
	invalidState.State = "running"
	invalidState.IdempotencyKey = "agent-task-guard-state"
	if err := db.Create(&invalidState).Error; err == nil {
		t.Fatal("agent task with invalid state was accepted")
	}
	invalidOperation := valid
	invalidOperation.ID = 0
	invalidOperation.OperationType = "delete_everything"
	invalidOperation.IdempotencyKey = "agent-task-guard-operation"
	if err := db.Create(&invalidOperation).Error; err == nil {
		t.Fatal("agent task with invalid operation was accepted")
	}
}

func TestPostgresSchema87CatalogBatchChangeOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 87 {
		t.Fatalf("current schema version=%d, want at least 87", CurrentPostgresSchemaVersion)
	}
	first := Tenant{Name: "Catalog Batch Guard A", SystemCode: "CATALOG-BATCH-GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Catalog Batch Guard B", SystemCode: "CATALOG-BATCH-GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: first.ID, Code: "CATALOG-BATCH-GUARD-SCENIC", Name: "Catalog Batch Guard Scenic", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	rule := TicketRule{TenantID: first.ID, Name: "Catalog Batch Guard Rule", ValidityType: "date"}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{TenantID: first.ID, ScenicAreaID: area.ID, RuleID: rule.ID, Name: "Catalog Batch Guard Product", Type: "online", Status: "online", CodeMode: "ticket", ValidityType: "date", StockType: "unlimited", GateVoiceCode: "welcome"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	revision := ProductRevision{ProductID: product.ID, TenantID: first.ID, ScenicAreaID: area.ID, Version: 1, Status: "active", SnapshotJSON: "{}", EffectiveFrom: time.Now()}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	plan := CatalogBatchChangePlan{TenantID: first.ID, ActorRole: "admin", InputText: "guard", OperationJSON: "{}", PlanHash: strings.Repeat("a", 64), IdempotencyKey: "CATALOG-GUARD-KEY", Status: "previewed", PreviewJSON: "{}", ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	validLine := CatalogBatchChangeLine{PlanID: plan.ID, TenantID: first.ID, ProductID: product.ID, ProductName: product.Name, ScenicAreaID: area.ID, BeforeRevisionID: revision.ID, BeforeJSON: "{}", AfterJSON: "{}", Status: "pending"}
	if err := db.Create(&validLine).Error; err != nil {
		t.Fatal(err)
	}
	crossTenantLine := validLine
	crossTenantLine.ID = 0
	crossTenantLine.TenantID = second.ID
	if err := db.Create(&crossTenantLine).Error; err == nil {
		t.Fatal("cross-tenant catalog batch line was accepted")
	}
	crossProductLine := validLine
	crossProductLine.ID = 0
	crossProductLine.ProductID = product.ID
	crossProductLine.ScenicAreaID = area.ID + 999
	if err := db.Create(&crossProductLine).Error; err == nil {
		t.Fatal("catalog batch line with mismatched scenic area was accepted")
	}
	afterRevisionLine := validLine
	afterRevisionLine.ID = 0
	afterRevisionLine.Status = "applied"
	afterRevisionLine.AfterRevisionID = revision.ID + 999
	if err := db.Create(&afterRevisionLine).Error; err == nil {
		t.Fatal("catalog batch line with mismatched after revision was accepted")
	}
}

func TestPostgresSchema88AIUsageOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 88 {
		t.Fatalf("current schema version=%d, want at least 88", CurrentPostgresSchemaVersion)
	}
	first := Tenant{Name: "AI Usage Guard A", SystemCode: "AI-USAGE-GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "AI Usage Guard B", SystemCode: "AI-USAGE-GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	valid := AIUsageMonth{TenantID: first.ID, Period: "2026-08", RequestCount: 1, TokenCount: 10}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	invalidTenant := valid
	invalidTenant.ID = 0
	invalidTenant.TenantID = second.ID + 999
	if err := db.Create(&invalidTenant).Error; err == nil {
		t.Fatal("AI usage row for an unknown tenant was accepted")
	}
	invalidPeriod := valid
	invalidPeriod.ID = 0
	invalidPeriod.Period = "202608"
	if err := db.Create(&invalidPeriod).Error; err == nil {
		t.Fatal("AI usage row with invalid period was accepted")
	}
	negativeUsage := valid
	negativeUsage.ID = 0
	negativeUsage.RequestCount = -1
	if err := db.Create(&negativeUsage).Error; err == nil {
		t.Fatal("AI usage row with negative request count was accepted")
	}
}

func TestPostgresSchema84UpgradesRealSchema83BookingFacts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 84 {
		t.Fatalf("current schema version=%d, want at least 84", CurrentPostgresSchemaVersion)
	}
	if err := db.Migrator().DropTable(&XiaohongshuBookingOperation{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		DROP TRIGGER IF EXISTS ownership_guard ON scenic_hotel_package_entitlements;
		DROP TRIGGER IF EXISTS ownership_guard ON xiaohongshu_booking_operations;
		ALTER TABLE scenic_hotel_package_entitlements DROP CONSTRAINT IF EXISTS chk_scenic_hotel_package_entitlements_status;
		ALTER TABLE scenic_hotel_package_entitlements ADD CONSTRAINT chk_scenic_hotel_package_entitlements_status
			CHECK (status IN ('pending_booking','booked','cancelled','refunded','expired'));
		DELETE FROM schema_migrations WHERE version >= 84;
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES (83, 'deferred scenic hotel bookings', NOW())
		ON CONFLICT (version) DO NOTHING;
	`).Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&XiaohongshuBookingOperation{}) {
		t.Fatal("schema 83 fixture unexpectedly contains xiaohongshu booking operations")
	}

	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&XiaohongshuBookingOperation{}) {
		t.Fatal("schema 84 did not create xiaohongshu booking operations")
	}
	if !db.Migrator().HasColumn(&XiaohongshuBookingOperation{}, "FailedFromStage") {
		t.Fatal("schema 84 did not add Xiaohongshu booking failed source stage")
	}
	var triggerCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM pg_trigger
		WHERE tgname = 'ownership_guard'
		  AND tgrelid IN ('scenic_hotel_package_entitlements'::regclass, 'xiaohongshu_booking_operations'::regclass)
		  AND NOT tgisinternal
	`).Scan(&triggerCount).Error; err != nil {
		t.Fatal(err)
	}
	if triggerCount != 2 {
		t.Fatalf("schema 84 booking ownership triggers=%d, want 2", triggerCount)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("repeat schema 84 migration: %v", err)
	}
}

func TestPostgresSchema86BookingAndOrderOwnershipGuardsRejectInvalidFacts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Booking Guard A", SystemCode: "BOOKING-GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Booking Guard B", SystemCode: "BOOKING-GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	orphan := ScenicHotelPackageEntitlement{
		EntitlementNo: "ENT-GUARD-ORPHAN", SalesTenantID: first.ID, SupplierTenantID: second.ID,
		OrderID: 999, OrderItemID: 999, TicketID: 999, PackageID: 999,
		Status: "pending_booking", ValidFrom: now, ValidUntil: now.Add(24 * time.Hour),
	}
	if err := db.Create(&orphan).Error; err == nil {
		t.Fatal("orphan cross-tenant package entitlement was accepted")
	}
	invalidWindow := orphan
	invalidWindow.EntitlementNo = "ENT-GUARD-WINDOW"
	invalidWindow.ValidFrom, invalidWindow.ValidUntil = now.Add(24*time.Hour), now
	if err := db.Create(&invalidWindow).Error; err == nil {
		t.Fatal("package entitlement with inverted validity was accepted")
	}
	account := ChannelAccount{TenantID: first.ID, Code: "BOOKING-GUARD-XHS", Type: "xiaohongshu", Status: "active"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	operation := XiaohongshuBookingOperation{
		TenantID: first.ID, ChannelAccountID: account.ID, OrderLinkID: 999, EntitlementID: 999,
		OperationKey: "BOOKING-GUARD-OP", Type: "book", Status: "pending",
		RequestPayloadCiphertext: "encrypted", MaxAttempts: 20,
	}
	if err := db.Create(&operation).Error; err == nil {
		t.Fatal("orphan Xiaohongshu booking operation was accepted")
	}
	operation.OperationKey = "BOOKING-GUARD-EMPTY-CIPHER"
	operation.RequestPayloadCiphertext = ""
	if err := db.Create(&operation).Error; err == nil {
		t.Fatal("Xiaohongshu booking operation with empty ciphertext was accepted")
	}
	operation.OperationKey = "BOOKING-GUARD-UNSUPPORTED-TYPE"
	operation.RequestPayloadCiphertext = "encrypted"
	operation.Type = "reject"
	if err := db.Create(&operation).Error; err == nil {
		t.Fatal("unsupported Xiaohongshu booking operation type was accepted")
	}

	area := ScenicArea{TenantID: first.ID, Code: "BOOKING-GUARD-SCENIC", Name: "Booking Guard Scenic", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{TenantID: first.ID, ScenicAreaID: area.ID, Name: "Booking Guard Product", Type: "online", Status: "online"}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	hotel := HotelProperty{TenantID: first.ID, Code: "BOOKING-GUARD-HOTEL", Name: "Booking Guard Hotel", Status: "active"}
	if err := db.Create(&hotel).Error; err != nil {
		t.Fatal(err)
	}
	room := HotelRoomType{TenantID: first.ID, HotelID: hotel.ID, Code: "BOOKING-GUARD-ROOM", Name: "Booking Guard Room", Status: "active"}
	if err := db.Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	rate := HotelRatePlan{TenantID: first.ID, HotelID: hotel.ID, RoomTypeID: room.ID, Code: "BOOKING-GUARD-RATE", Name: "Booking Guard Rate", Status: "active"}
	if err := db.Create(&rate).Error; err != nil {
		t.Fatal(err)
	}
	packageRow := ScenicHotelPackage{
		TenantID: first.ID, ProductID: product.ID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		Nights: 1, RoomsPerPackage: 1, BookingMode: "after_purchase", VoucherValidityDays: 30, Status: "online",
	}
	if err := db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{OrderNo: "BOOKING-GUARD-ORDER", TenantID: first.ID, Status: "paid", Channel: "xiaohongshu", ChannelAccountID: account.ID}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	item := OrderItem{
		OrderID: order.ID, ProductID: product.ID, ProductName: product.Name, Quantity: 1,
		FulfillmentProductID: product.ID, FulfillmentTenantID: first.ID, FulfillmentScenicAreaID: area.ID,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	ticket := Ticket{
		OrderID: order.ID, OrderItemID: item.ID, TenantID: first.ID, ScenicAreaID: area.ID,
		FulfillmentProductID: product.ID, FulfillmentTenantID: first.ID, FulfillmentScenicAreaID: area.ID,
		TicketCode: "BOOKING-GUARD-TICKET", Status: "pending_booking",
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	entitlement := ScenicHotelPackageEntitlement{
		EntitlementNo: "BOOKING-GUARD-ENTITLEMENT", SalesTenantID: first.ID, SupplierTenantID: first.ID,
		OrderID: order.ID, OrderItemID: item.ID, TicketID: ticket.ID, PackageID: packageRow.ID,
		Status: "pending_booking", ValidFrom: now, ValidUntil: now.Add(30 * 24 * time.Hour),
	}
	if err := db.Create(&entitlement).Error; err != nil {
		t.Fatal(err)
	}
	secondArea := ScenicArea{TenantID: second.ID, Code: "BOOKING-GUARD-SCENIC-B", Name: "Booking Guard Scenic B", Status: "active"}
	if err := db.Create(&secondArea).Error; err != nil {
		t.Fatal(err)
	}
	secondProduct := Product{TenantID: second.ID, ScenicAreaID: secondArea.ID, Name: "Booking Guard Product B", Type: "online", Status: "online"}
	if err := db.Create(&secondProduct).Error; err != nil {
		t.Fatal(err)
	}
	crossSupplierItem := OrderItem{
		OrderID: order.ID, ProductID: product.ID, ProductName: secondProduct.Name, Quantity: 1,
		FulfillmentProductID: secondProduct.ID, FulfillmentTenantID: second.ID, FulfillmentScenicAreaID: secondArea.ID,
	}
	if err := db.Create(&crossSupplierItem).Error; err != nil {
		t.Fatal(err)
	}
	crossSupplierTicket := Ticket{
		OrderID: order.ID, OrderItemID: crossSupplierItem.ID, TenantID: first.ID, ScenicAreaID: secondArea.ID,
		FulfillmentProductID: secondProduct.ID, FulfillmentTenantID: second.ID, FulfillmentScenicAreaID: secondArea.ID,
		TicketCode: "BOOKING-GUARD-CROSS-SUPPLIER-TICKET", Status: "unused",
	}
	if err := db.Create(&crossSupplierTicket).Error; err != nil {
		t.Fatal(err)
	}
	invalidReservation := HotelReservation{
		ReservationNo: "BOOKING-GUARD-CROSS-SUPPLIER-RESERVATION",
		SalesTenantID: first.ID, SupplierTenantID: first.ID,
		OrderID: order.ID, OrderItemID: crossSupplierItem.ID, TicketID: crossSupplierTicket.ID,
		PackageID: packageRow.ID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		HotelName: hotel.Name, RoomTypeName: room.Name, RatePlanName: rate.Name,
		CheckInDate: now.Add(48 * time.Hour), CheckOutDate: now.Add(72 * time.Hour), Rooms: 1,
		Status: "reserved",
	}
	if err := db.Create(&invalidReservation).Error; err == nil {
		t.Fatal("hotel reservation linked to another supplier's order item and ticket was accepted")
	}
	customer := MiniappCustomer{
		TenantID: first.ID, ChannelAccountID: account.ID, OpenIDHash: "booking-guard-openid", OpenIDCiphertext: "encrypted-openid",
		SessionKeyCiphertext: "encrypted-session", SessionTokenHash: "booking-guard-session", SessionExpiresAt: now.Add(time.Hour), Status: "active", LastLoginAt: now,
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	link := XiaohongshuOrderLink{
		TenantID: first.ID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID,
		ClientRequestID: "booking-guard-order", ExternalOrderID: "BOOKING-GUARD-EXTERNAL", State: "paid",
	}
	if err := db.Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	nextAttempt := now.Add(time.Minute)
	orderOperation := XiaohongshuOrderOperation{
		TenantID: first.ID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID,
		RequestPayloadCiphertext: "encrypted-order-request", Status: "pending", NextAttemptAt: &nextAttempt,
	}
	badOrderOperation := orderOperation
	badOrderOperation.Base = Base{}
	badOrderOperation.TenantID = second.ID
	badOrderOperation.XiaohongshuOrderLinkID = link.ID
	if err := db.Create(&badOrderOperation).Error; err == nil {
		t.Fatal("cross-tenant Xiaohongshu order operation was accepted")
	}
	if err := db.Create(&orderOperation).Error; err != nil {
		t.Fatalf("valid Xiaohongshu order operation was rejected: %v", err)
	}
	if err := db.Model(&orderOperation).Updates(map[string]interface{}{"status": "remote_succeeded", "next_attempt_at": nextAttempt}).Error; err == nil {
		t.Fatal("remote-success order operation without persisted platform facts was accepted")
	}
	validOperation := XiaohongshuBookingOperation{
		TenantID: first.ID, ChannelAccountID: account.ID, OrderLinkID: link.ID, EntitlementID: entitlement.ID,
		OperationKey: "BOOKING-GUARD-VALID", Type: "book", Status: "pending", ExternalBookOrderID: "BOOKING-GUARD-BOOK",
		RequestPayloadCiphertext: "encrypted", MaxAttempts: 20, NextAttemptAt: &nextAttempt,
	}
	if err := db.Create(&validOperation).Error; err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		name string
		sql  string
	}{
		{name: "book without external id", sql: `UPDATE xiaohongshu_booking_operations SET external_book_order_id = '' WHERE id = ?`},
		{name: "remote success without platform id", sql: `UPDATE xiaohongshu_booking_operations SET status = 'remote_succeeded' WHERE id = ?`},
		{name: "active task without next attempt", sql: `UPDATE xiaohongshu_booking_operations SET next_attempt_at = NULL WHERE id = ?`},
		{name: "unsupported failed source stage", sql: `UPDATE xiaohongshu_booking_operations SET failed_from_stage = 'completed' WHERE id = ?`},
		{name: "non-failed task with failed source stage", sql: `UPDATE xiaohongshu_booking_operations SET failed_from_stage = 'pending' WHERE id = ?`},
		{name: "terminal task without completion", sql: `UPDATE xiaohongshu_booking_operations SET status = 'completed', platform_book_id = 'BOOKING-GUARD-PLATFORM', next_attempt_at = NULL WHERE id = ?`},
		{name: "non-terminal task with completion", sql: `UPDATE xiaohongshu_booking_operations SET completed_at = NOW() WHERE id = ?`},
	} {
		if err := db.Exec(mutation.sql, validOperation.ID).Error; err == nil {
			t.Fatalf("%s was accepted", mutation.name)
		}
	}
	badRevoke := validOperation
	badRevoke.Base = Base{}
	badRevoke.OperationKey = "BOOKING-GUARD-BAD-REVOKE"
	badRevoke.Type = "revoke"
	badRevoke.PlatformBookID = ""
	if err := db.Create(&badRevoke).Error; err == nil {
		t.Fatal("revoke operation without platform booking id was accepted")
	}
	badRefund := validOperation
	badRefund.Base = Base{}
	badRefund.OperationKey = "BOOKING-GUARD-BAD-REFUND"
	badRefund.Type = "refund_status_sync"
	badRefund.PlatformBookID = "BOOKING-GUARD-PLATFORM"
	if err := db.Create(&badRefund).Error; err == nil {
		t.Fatal("refund status synchronization for a non-refunded local entitlement was accepted")
	}
	if err := db.Model(&ticket).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&entitlement).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	validRefund := badRefund
	validRefund.OperationKey = "BOOKING-GUARD-VALID-REFUND"
	if err := db.Create(&validRefund).Error; err != nil {
		t.Fatalf("valid refund status synchronization was rejected: %v", err)
	}
	if err := db.Model(&validRefund).Updates(map[string]interface{}{"status": "confirm_pending", "next_attempt_at": nextAttempt}).Error; err == nil {
		t.Fatal("refund status synchronization accepted a book-only confirm_pending state")
	}
	failedAt := now.Add(2 * time.Minute)
	if err := db.Model(&validOperation).Updates(map[string]interface{}{
		"status": "failed", "failed_from_stage": "confirm_pending", "platform_book_id": "BOOKING-GUARD-PLATFORM",
		"next_attempt_at": nil, "completed_at": &failedAt,
	}).Error; err != nil {
		t.Fatalf("recoverable failed operation with source stage was rejected: %v", err)
	}
	if err := db.Model(&validOperation).Updates(map[string]interface{}{
		"failed_from_stage": "", "completed_at": &failedAt,
	}).Error; err != nil {
		t.Fatalf("successfully compensated terminal failure was rejected: %v", err)
	}
	if err := db.Model(&validOperation).Updates(map[string]interface{}{
		"failed_from_stage": "remote_succeeded", "completed_at": &failedAt,
	}).Error; err != nil {
		t.Fatalf("failed local finalization source stage was rejected: %v", err)
	}
	if CurrentPostgresSchemaVersion < 86 {
		t.Fatalf("current schema version=%d, want at least 86", CurrentPostgresSchemaVersion)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_type`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE xiaohongshu_booking_operations DROP CONSTRAINT IF EXISTS chk_xiaohongshu_booking_operations_semantics`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE xiaohongshu_booking_operations SET type = 'refund', operation_key = 'xhs:refund:legacy-migration' WHERE id = ?`, validRefund.ID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_type CHECK (type IN ('book','revoke','refund'))`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`ALTER TABLE xiaohongshu_booking_operations ADD CONSTRAINT chk_xiaohongshu_booking_operations_semantics CHECK (((type = 'book' AND external_book_order_id <> '') OR (type IN ('revoke','refund') AND external_book_order_id <> '' AND platform_book_id <> '')) AND (type = 'book' OR status IN ('pending','remote_succeeded','completed','failed')) AND (status NOT IN ('remote_succeeded','confirm_pending','compensation_pending','completed') OR platform_book_id <> '') AND (failed_from_stage = '' OR failed_from_stage IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) AND (failed_from_stage = '' OR status = 'failed') AND ((status IN ('completed','failed')) = (completed_at IS NOT NULL)) AND ((status IN ('pending','remote_succeeded','confirm_pending','compensation_pending')) = (next_attempt_at IS NOT NULL)))`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM schema_migrations WHERE version >= 86`).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (85, 'xiaohongshu order operation saga', NOW()) ON CONFLICT (version) DO NOTHING`).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("schema 86 refund status sync migration: %v", err)
	}
	var migratedRefund XiaohongshuBookingOperation
	if err := db.First(&migratedRefund, validRefund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migratedRefund.Type != "refund_status_sync" || migratedRefund.OperationKey != "xhs:refund_status_sync:legacy-migration" {
		t.Fatalf("legacy refund operation was not migrated: %+v", migratedRefund)
	}
}

func TestPostgresMigrationBackfillsOnlyActiveUnexpiredSuppliersAsScenic(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	type supplierFixture struct {
		name       string
		status     string
		expiresAt  *time.Time
		wantScenic bool
	}
	fixtures := []supplierFixture{
		{name: "active-no-expiry", status: "active", wantScenic: true},
		{name: "active-future-expiry", status: "active", expiresAt: &future, wantScenic: true},
		{name: "active-expired", status: "active", expiresAt: &past},
		{name: "pending", status: "pending"},
		{name: "rejected", status: "rejected"},
		{name: "suspended", status: "suspended"},
	}
	tenantIDs := make(map[string]uint, len(fixtures))
	for index, fixture := range fixtures {
		tenant := Tenant{
			Name: fixture.name, SystemCode: fmt.Sprintf("SCHEMA79-SUPPLIER-%d", index), Status: "active",
		}
		if err := db.Create(&tenant).Error; err != nil {
			t.Fatal(err)
		}
		tenantIDs[fixture.name] = tenant.ID
		if err := db.Create(&TenantCapability{
			TenantID: tenant.ID, Capability: "supplier", Status: fixture.status, ExpiresAt: fixture.expiresAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Schema 80 only adds supplier_business_types. Removing that table from a
	// fully migrated schema produces the exact schema 79 boundary for this change.
	if err := db.Migrator().DropTable(&SupplierBusinessType{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 79, Name: "xiaohongshu product and guarantee payment facts", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&SupplierBusinessType{}) {
		t.Fatal("schema 79 fixture unexpectedly contains supplier_business_types")
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("repeat schema 80 migration: %v", err)
	}
	for _, fixture := range fixtures {
		var count int64
		if err := db.Model(&SupplierBusinessType{}).
			Where("tenant_id = ? AND business_type = ? AND status = ?", tenantIDs[fixture.name], "scenic", "active").
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if got := count == 1; got != fixture.wantScenic {
			t.Fatalf("fixture %s scenic backfill=%v, want %v", fixture.name, got, fixture.wantScenic)
		}
	}
}

func TestPostgresMigrationRejectsNewerSchemaVersion(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	newerVersion := CurrentPostgresSchemaVersion + 1
	if err := db.Create(&SchemaMigration{
		Version: newerVersion, Name: "future application schema", AppliedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := runMigrations(db)
	if err == nil {
		t.Fatalf("application accepted newer database schema %d", newerVersion)
	}
	for _, expected := range []string{fmt.Sprint(newerVersion), fmt.Sprint(CurrentPostgresSchemaVersion), "refusing to start"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("newer-schema error %q does not contain %q", err, expected)
		}
	}
}

func TestTeamUniquenessMigrationReportsActionableLegacyConflicts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_team_active_fulfillment_group", "idx_team_member_active_ticket"} {
		if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := TourGroup{TenantID: 11, SupplierTenantID: 22, ScenicAreaID: 33, SalesOrderID: 44, GroupNo: "LEGACY-DUP-1", Name: "Legacy duplicate one", VisitDate: time.Now(), Status: "confirmed"}
	second := TourGroup{TenantID: 11, SupplierTenantID: 22, ScenicAreaID: 33, SalesOrderID: 44, GroupNo: "LEGACY-DUP-2", Name: "Legacy duplicate two", VisitDate: time.Now(), Status: "confirmed"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TourGroupMember{GroupID: first.ID, Name: "First", TicketCode: "LEGACY-TICKET-DUP", Status: "ticketed"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TourGroupMember{GroupID: second.ID, Name: "Second", TicketCode: "LEGACY-TICKET-DUP", Status: "ticketed"}).Error; err != nil {
		t.Fatal(err)
	}

	err := validateTeamUniquenessMigrationData(db)
	if err == nil {
		t.Fatal("legacy duplicate team facts were accepted before unique-index migration")
	}
	message := err.Error()
	for _, expected := range []string{"order=44", "supplier=22", "scenic=33", "LEGACY-DUP-1", "LEGACY-DUP-2", "LEGACY-TICKET-DUP"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("migration error %q does not identify %q", message, expected)
		}
	}
}

func TestPostgresMigrationDoesNotInferTravelPartnershipFromDistribution(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	supplier := Tenant{Name: "Migration Supplier", SystemCode: "MIGRATION-SUPPLIER", Status: "active"}
	partner := Tenant{Name: "Migration Partner", SystemCode: "MIGRATION-PARTNER", Status: "active"}
	if err := db.Create(&supplier).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&partner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]TenantCapability{
		{TenantID: supplier.ID, Capability: "supplier", Status: "active"},
		{TenantID: partner.ID, Capability: "distributor", Status: "active"},
		{TenantID: partner.ID, Capability: "travel_agency", Status: "active"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	relationship := DistributorRelationship{
		AgentTenantID: partner.ID, SupplierTenantID: supplier.ID,
		Status: "active", TravelStatus: "active",
	}
	if err := db.Create(&relationship).Error; err != nil {
		t.Fatal(err)
	}
	legitimatePartner := Tenant{Name: "Audited Travel Partner", SystemCode: "AUDITED-TRAVEL-PARTNER", Status: "active"}
	if err := db.Create(&legitimatePartner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TenantCapability{TenantID: legitimatePartner.ID, Capability: "travel_agency", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	legitimateRelationship := DistributorRelationship{
		AgentTenantID: legitimatePartner.ID, SupplierTenantID: supplier.ID,
		Status: "none", TravelStatus: "active",
	}
	if err := db.Create(&legitimateRelationship).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&AuditLog{
		ActorUserID: 9, ActorRole: "admin", Scope: "tenant", TenantID: supplier.ID,
		Action: "team.partner.apply", TargetType: "supplier_relationship", TargetID: legitimateRelationship.ID,
		Reason: "explicit travel partnership application",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: CurrentPostgresSchemaVersion - 1, Name: "previous schema", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&relationship, relationship.ID).Error; err != nil {
		t.Fatal(err)
	}
	if relationship.Status != "active" || relationship.TravelStatus != "none" {
		t.Fatalf("distribution relationship leaked into travel partnership: %+v", relationship)
	}
	if relationship.DistributionAppliedAt == nil || relationship.TravelAppliedAt != nil {
		t.Fatalf("relationship application timestamps were not separated: %+v", relationship)
	}
	if err := db.First(&legitimateRelationship, legitimateRelationship.ID).Error; err != nil {
		t.Fatal(err)
	}
	if legitimateRelationship.TravelStatus != "active" || legitimateRelationship.TravelAppliedAt == nil {
		t.Fatalf("audited travel partnership was not preserved: %+v", legitimateRelationship)
	}
}

func TestPostgresOwnershipGuardsRejectCrossTenantRows(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Guard A", SystemCode: "GUARD-A", SecretKey: "a", Status: "active"}
	second := Tenant{Name: "Guard B", SystemCode: "GUARD-B", SecretKey: "b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: second.ID, Code: "B", Name: "B", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CheckPoint{TenantID: first.ID, ScenicAreaID: area.ID, Name: "forbidden"}).Error; err == nil {
		t.Fatal("cross-tenant checkpoint was accepted by PostgreSQL guard")
	}
	if err := db.Create(&OrderVisitor{
		TenantID: first.ID, OrderID: 999, OrderItemID: 999, TicketID: 999,
		TicketCode: "forbidden", Sequence: 1, Name: "cross-tenant",
	}).Error; err == nil {
		t.Fatal("orphan order visitor was accepted by PostgreSQL guard")
	}
}
