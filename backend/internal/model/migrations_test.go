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
		&CtripOrderLink{}, &CtripOrderItem{}, &XiaohongshuVoucherVerification{}, &XiaohongshuWebhookEvent{}, &SupplierBusinessType{},
		&HotelProperty{}, &HotelRoomType{}, &HotelRatePlan{}, &HotelRatePlanPrice{}, &HotelRoomInventory{},
		&HotelProduct{}, &HotelProductRevision{}, &HotelProductCalendarPrice{}, &HotelProductEntitlement{}, &HotelProductReservation{},
		&ScenicHotelPackage{}, &ScenicHotelPackageEntitlement{}, &HotelReservation{},
		&XiaohongshuBookingOperation{}, &XiaohongshuOrderOperation{},
		&CatalogBatchChangePlan{}, &CatalogBatchChangeLine{},
		&PlatformAIConfig{}, &AITenantQuotaPolicy{}, &AIUsageMonth{},
		&AgentTask{}, &AgentTaskEvent{},
		&PrintTemplate{}, &PrintTemplateRevision{},
		&DeviceMaintenanceCredential{}, &DeviceMaintenanceSession{}, &DeviceProvisioningLease{},
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
		{&HotelRatePlanPrice{}, "idx_hotel_rate_plan_prices_scope"},
		{&HotelProduct{}, "idx_hotel_products_active_product"},
		{&HotelProductCalendarPrice{}, "idx_hotel_product_calendar_prices_scope"},
		{&AgentTask{}, "idx_agent_task_idempotency"},
		{&AITenantQuotaPolicy{}, "idx_ai_tenant_quota_policy"},
		{&PrintTemplate{}, "idx_print_template_scope"},
		{&PrintTemplateRevision{}, "idx_print_template_revision_version"},
		{&PrintJob{}, "idx_print_jobs_template_revision"},
		{&DeviceMaintenanceCredential{}, "idx_device_maintenance_active_credential"},
		{&DeviceMaintenanceSession{}, "idx_device_maintenance_session_active_device"},
		{&DeviceProvisioningLease{}, "idx_device_provisioning_active_device"},
		{&XiaohongshuVoucherVerification{}, "idx_xhs_voucher_verification_link"},
		{&XiaohongshuVoucherVerification{}, "idx_xhs_voucher_verification_request"},
		{&XiaohongshuVoucherVerification{}, "idx_xhs_voucher_verification_verify"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("index %s is missing", index.name)
		}
	}
	var voucherRequestIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_xhs_voucher_verification_request'`).Scan(&voucherRequestIndex).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"device_id", "request_id"} {
		if !strings.Contains(voucherRequestIndex, column) {
			t.Fatalf("xiaohongshu voucher request index omitted %s: %s", column, voucherRequestIndex)
		}
	}
	var channelRequestIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_channel_request'`).Scan(&channelRequestIndex).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(channelRequestIndex, "channel_account_id") || !strings.Contains(channelRequestIndex, "endpoint") || !strings.Contains(channelRequestIndex, "request_id") {
		t.Fatalf("channel request idempotency index has wrong scope: %s", channelRequestIndex)
	}
	var agentTaskIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_agent_task_idempotency'`).Scan(&agentTaskIndex).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"tenant_id", "actor_user_id", "idempotency_key"} {
		if !strings.Contains(agentTaskIndex, column) {
			t.Fatalf("agent task idempotency index omitted %s: %s", column, agentTaskIndex)
		}
	}
	var printRevisionIndex string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_print_template_revision_version'`).Scan(&printRevisionIndex).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"template_id", "version"} {
		if !strings.Contains(printRevisionIndex, column) {
			t.Fatalf("print template revision index omitted %s: %s", column, printRevisionIndex)
		}
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

func TestPostgresSchema91MigratesLegacyAIRequestTimeout(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	legacy := PlatformAIConfig{
		ConfigKey: "legacy-request-timeout", Provider: "deepseek", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-chat", RequestTimeoutSeconds: 30,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 90, Name: "pre-extended-ai-request-timeout", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var migrated PlatformAIConfig
	if err := db.First(&migrated, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.RequestTimeoutSeconds != 120 {
		t.Fatalf("legacy AI request timeout=%d, want 120", migrated.RequestTimeoutSeconds)
	}
	var columnDefault string
	if err := db.Raw(`
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'platform_ai_configs'
		  AND column_name = 'request_timeout_seconds'
	`).Scan(&columnDefault).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(columnDefault, "120") {
		t.Fatalf("request_timeout_seconds column default=%q, want 120", columnDefault)
	}
}

func TestPostgresSchema96AgentTaskOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 96 {
		t.Fatalf("current schema version=%d, want at least 96", CurrentPostgresSchemaVersion)
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
	validUpdate := valid
	validUpdate.ID = 0
	validUpdate.OperationType = "ticket_product_update"
	validUpdate.IdempotencyKey = "agent-task-guard-update-key"
	if err := db.Create(&validUpdate).Error; err != nil {
		t.Fatalf("agent product update task was rejected: %v", err)
	}
	validBatchUpdate := valid
	validBatchUpdate.ID = 0
	validBatchUpdate.OperationType = "ticket_product_batch_update"
	validBatchUpdate.IdempotencyKey = "agent-task-guard-batch-update-key"
	if err := db.Create(&validBatchUpdate).Error; err != nil {
		t.Fatalf("agent batch product update task was rejected: %v", err)
	}
	validCompound := valid
	validCompound.ID = 0
	validCompound.OperationType = "compound_preview"
	validCompound.IdempotencyKey = "agent-task-guard-compound-key"
	if err := db.Create(&validCompound).Error; err != nil {
		t.Fatalf("agent compound preview task was rejected: %v", err)
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

func TestPostgresSchema93AgentTaskEventOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 93 {
		t.Fatalf("current schema version=%d, want at least 93", CurrentPostgresSchemaVersion)
	}
	tenant := Tenant{Name: "Agent Event Guard", SystemCode: "AGENT-EVENT-GUARD", SecretKey: "event", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	task := AgentTask{TenantID: tenant.ID, ActorUserID: 11, ActorRole: "admin", OperationType: "pending", State: "collecting", InputText: "查询票种", ContextJSON: `{}`, MissingJSON: `[]`, IdempotencyKey: "agent-event-task", Version: 1, ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	valid := AgentTaskEvent{TenantID: tenant.ID, TaskID: task.ID, ActorUserID: task.ActorUserID, ActorRole: task.ActorRole, Sequence: 1, EventType: "tool_call", ToolName: "search_ticket_products", ToolVersion: "1", ToolCallID: "event-call-1", IdempotencyKey: "event-attempt-1", Status: "succeeded"}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	invalidTenant := valid
	invalidTenant.ID = 0
	invalidTenant.TenantID++
	invalidTenant.ToolCallID = "event-call-2"
	if err := db.Create(&invalidTenant).Error; err == nil {
		t.Fatal("agent task event from another tenant was accepted")
	}
	invalidActor := valid
	invalidActor.ID = 0
	invalidActor.ActorUserID++
	invalidActor.ToolCallID = "event-call-3"
	if err := db.Create(&invalidActor).Error; err == nil {
		t.Fatal("agent task event from another actor was accepted")
	}
	duplicateAttempt := valid
	duplicateAttempt.ID = 0
	duplicateAttempt.Sequence = 2
	duplicateAttempt.ToolCallID = "event-call-2"
	if err := db.Create(&duplicateAttempt).Error; err == nil {
		t.Fatal("duplicate agent task event idempotency key was accepted")
	}
}

func TestPostgresSchema94MigratesDeepSeekProtocolDefault(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	legacy := PlatformAIConfig{
		ConfigKey: "legacy-deepseek-protocol", Provider: "deepseek", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-chat", AgentProtocolMode: "legacy_json",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	other := PlatformAIConfig{
		ConfigKey: "legacy-compatible-protocol", Provider: "openai_compatible", BaseURL: "https://example.com",
		Model: "compatible", AgentProtocolMode: "legacy_json",
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 93, Name: "pre-deepseek-protocol-default", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var migrated PlatformAIConfig
	if err := db.First(&migrated, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.AgentProtocolMode != "auto" {
		t.Fatalf("DeepSeek protocol=%q, want auto", migrated.AgentProtocolMode)
	}
	var untouched PlatformAIConfig
	if err := db.First(&untouched, other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.AgentProtocolMode != "legacy_json" {
		t.Fatalf("compatible protocol=%q, want legacy_json", untouched.AgentProtocolMode)
	}
	var columnDefault string
	if err := db.Raw(`
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'platform_ai_configs'
		  AND column_name = 'agent_protocol_mode'
	`).Scan(&columnDefault).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(columnDefault, "auto") {
		t.Fatalf("agent_protocol_mode column default=%q, want auto", columnDefault)
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

func TestPostgresSchema102AITenantQuotaOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 102 {
		t.Fatalf("current schema version=%d, want at least 102", CurrentPostgresSchemaVersion)
	}
	tenant := Tenant{Name: "AI Quota Guard", SystemCode: "AI-QUOTA-GUARD", SecretKey: "quota", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	requestLimit := 25
	valid := AITenantQuotaPolicy{TenantID: tenant.ID, MonthlyRequestLimit: &requestLimit, Enabled: true, LastUpdatedReason: "test"}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	unknownTenant := valid
	unknownTenant.ID = 0
	unknownTenant.TenantID = tenant.ID + 999
	if err := db.Create(&unknownTenant).Error; err == nil {
		t.Fatal("AI tenant quota policy for an unknown tenant was accepted")
	}
	invalidRequest := valid
	invalidRequest.ID = 0
	invalidRequest.TenantID = tenant.ID
	tooMany := 1000001
	invalidRequest.MonthlyRequestLimit = &tooMany
	if err := db.Create(&invalidRequest).Error; err == nil {
		t.Fatal("AI tenant quota policy with an invalid request limit was accepted")
	}
	invalidToken := valid
	invalidToken.ID = 0
	invalidToken.TenantID = tenant.ID
	tooFew := int64(999)
	invalidToken.MonthlyRequestLimit = nil
	invalidToken.MonthlyTokenLimit = &tooFew
	if err := db.Create(&invalidToken).Error; err == nil {
		t.Fatal("AI tenant quota policy with an invalid token limit was accepted")
	}
}

func TestPostgresSchema102AddsTenantQuotaPolicyToSchema101Database(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&AITenantQuotaPolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 101, Name: "schema 101 fixture", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&AITenantQuotaPolicy{}) || !db.Migrator().HasIndex(&AITenantQuotaPolicy{}, "idx_ai_tenant_quota_policy") {
		t.Fatal("schema 102 did not recreate tenant AI quota policy table and index")
	}
}

func TestPostgresSchema104PrintTemplateOrientationAndOwnershipGuard(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 104 {
		t.Fatalf("current schema version=%d, want at least 104", CurrentPostgresSchemaVersion)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := Tenant{Name: "Print Template Tenant", SystemCode: "PRINT-TEMPLATE-" + suffix, SecretKey: "print", Status: "active"}
	other := Tenant{Name: "Other Print Tenant", SystemCode: "OTHER-PRINT-" + suffix, SecretKey: "other", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: tenant.ID, Code: "PRINT-AREA-" + suffix, Name: "打印景区", Status: "active"}
	otherArea := ScenicArea{TenantID: other.ID, Code: "OTHER-AREA-" + suffix, Name: "其他景区", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherArea).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{Name: "打印测试票", TenantID: tenant.ID, ScenicAreaID: area.ID, ProductKind: "ticket", Type: "online", Status: "online", Price: 10, SettlementPrice: 8}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	template := PrintTemplate{TenantID: tenant.ID, ScenicAreaID: area.ID, ProductID: product.ID, Name: "有效模板", Status: "active", PaperWidthMM: 58, Orientation: "landscape"}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	if template.Orientation != "landscape" {
		t.Fatalf("template orientation was not persisted: %q", template.Orientation)
	}
	invalidOrientation := PrintTemplate{TenantID: tenant.ID, ScenicAreaID: area.ID, Name: "非法方向", Status: "active", PaperWidthMM: 58, Orientation: "diagonal"}
	if err := db.Create(&invalidOrientation).Error; err == nil {
		t.Fatal("invalid print template orientation was accepted")
	}
	foreignTemplate := PrintTemplate{TenantID: tenant.ID, ScenicAreaID: otherArea.ID, Name: "越权模板", Status: "active", PaperWidthMM: 58}
	if err := db.Create(&foreignTemplate).Error; err == nil {
		t.Fatal("cross-tenant print template was accepted")
	}
	revision := PrintTemplateRevision{TenantID: tenant.ID, ScenicAreaID: area.ID, TemplateID: template.ID, Version: 1, Status: "published", DefinitionJSON: `{"schema_version":1,"paper_width_mm":58,"blocks":[{"kind":"ticket_code"}]}`, DefinitionHash: "revision-hash", CreatedBy: 1, PublishedBy: 1}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&template).Update("current_revision_id", revision.ID).Error; err != nil {
		t.Fatal(err)
	}
	foreignRevision := PrintTemplateRevision{TenantID: other.ID, ScenicAreaID: otherArea.ID, TemplateID: template.ID, Version: 2, Status: "draft", DefinitionJSON: revision.DefinitionJSON, DefinitionHash: "foreign-hash", CreatedBy: 1}
	if err := db.Create(&foreignRevision).Error; err == nil {
		t.Fatal("cross-tenant print template revision was accepted")
	}
	if err := db.Model(&revision).Update("definition_json", `{"schema_version":1}`).Error; err == nil {
		t.Fatal("published print template revision was mutable")
	}
}

func TestPostgresSchema104AddsPrintOrientationToSchema103(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&PrintTemplate{}, "Orientation"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&PrintJob{}, "Orientation"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 103, Name: "schema 103 fixture", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&PrintTemplate{}, "Orientation") || !db.Migrator().HasColumn(&PrintJob{}, "Orientation") {
		t.Fatal("schema 104 did not add print orientation columns")
	}
	var templateOrientation, jobOrientation string
	if err := db.Raw(`SELECT column_default FROM information_schema.columns WHERE table_schema = CURRENT_SCHEMA() AND table_name = 'print_templates' AND column_name = 'orientation'`).Scan(&templateOrientation).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(templateOrientation, "portrait") {
		t.Fatalf("print template orientation default=%q, want portrait", templateOrientation)
	}
	if err := db.Raw(`SELECT column_default FROM information_schema.columns WHERE table_schema = CURRENT_SCHEMA() AND table_name = 'print_jobs' AND column_name = 'orientation'`).Scan(&jobOrientation).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jobOrientation, "portrait") {
		t.Fatalf("print job orientation default=%q, want portrait", jobOrientation)
	}
}

func TestPostgresSchema105AddsWindowOrderIdempotency(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if CurrentPostgresSchemaVersion < 105 {
		t.Fatalf("current schema version=%d, want at least 105", CurrentPostgresSchemaVersion)
	}
	if err := db.Migrator().DropColumn(&Order{}, "ClientRequestID"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&Order{}, "ClientRequestHash"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 104, Name: "schema 104 fixture", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&Order{}, "ClientRequestID") || !db.Migrator().HasColumn(&Order{}, "ClientRequestHash") {
		t.Fatal("schema 105 did not add window order idempotency columns")
	}
	var indexDefinition string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = CURRENT_SCHEMA() AND indexname = 'idx_orders_window_client_request'`).Scan(&indexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexDefinition, "client_request_id") || !strings.Contains(indexDefinition, "channel") || !strings.Contains(indexDefinition, "deleted_at") {
		t.Fatalf("window order idempotency index=%q", indexDefinition)
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
	calendarPrice := HotelRatePlanPrice{
		TenantID: first.ID, HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		StayDate: now.AddDate(0, 0, 3), RetailPriceCents: 58800, SettlementPriceCents: 50000,
	}
	if err := db.Create(&calendarPrice).Error; err != nil {
		t.Fatalf("valid hotel rate plan calendar price was rejected: %v", err)
	}
	badCalendarPrice := calendarPrice
	badCalendarPrice.Base = Base{}
	badCalendarPrice.StayDate = now.AddDate(0, 0, 4)
	badCalendarPrice.TenantID = second.ID
	if err := db.Create(&badCalendarPrice).Error; err == nil {
		t.Fatal("cross-tenant hotel rate plan calendar price was accepted")
	}
	badCalendarPrice = calendarPrice
	badCalendarPrice.Base = Base{}
	badCalendarPrice.StayDate = now.AddDate(0, 0, 5)
	badCalendarPrice.SettlementPriceCents = badCalendarPrice.RetailPriceCents + 1
	if err := db.Create(&badCalendarPrice).Error; err == nil {
		t.Fatal("hotel rate plan calendar price above retail was accepted")
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

func TestPostgresHardwareOwnershipGuardsRejectCrossTenantFacts(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Hardware Guard A", SystemCode: "HARDWARE-GUARD-A", SecretKey: "hardware-a", Status: "active"}
	second := Tenant{Name: "Hardware Guard B", SystemCode: "HARDWARE-GUARD-B", SecretKey: "hardware-b", Status: "active"}
	if err := db.Create(&first).Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	firstArea := ScenicArea{TenantID: first.ID, Code: "HARDWARE-A", Name: "Hardware A", Status: "active"}
	secondArea := ScenicArea{TenantID: second.ID, Code: "HARDWARE-B", Name: "Hardware B", Status: "active"}
	if err := db.Create(&firstArea).Create(&secondArea).Error; err != nil {
		t.Fatal(err)
	}
	firstDevice := Device{TenantID: first.ID, ScenicAreaID: firstArea.ID, Name: "Hardware Gate A", SerialNumber: "HARDWARE-GATE-A", Type: "gate", Status: "online"}
	secondDevice := Device{TenantID: second.ID, ScenicAreaID: secondArea.ID, Name: "Hardware Gate B", SerialNumber: "HARDWARE-GATE-B", Type: "gate", Status: "online"}
	if err := db.Create(&firstDevice).Create(&secondDevice).Error; err != nil {
		t.Fatal(err)
	}
	validVerification := DeviceVerification{TenantID: first.ID, ScenicAreaID: firstArea.ID, DeviceID: firstDevice.ID, RequestID: "hardware-scan", RequestHash: strings.Repeat("a", 64), TicketCode: "HARDWARE-TICKET", Status: "completed", Result: "allow", OpenStatus: "pending"}
	if err := db.Create(&validVerification).Error; err != nil {
		t.Fatalf("valid device verification rejected: %v", err)
	}
	invalidVerification := validVerification
	invalidVerification.Base = Base{}
	invalidVerification.RequestID = "hardware-cross-tenant"
	invalidVerification.DeviceID = secondDevice.ID
	invalidVerification.ScenicAreaID = secondArea.ID
	if err := db.Create(&invalidVerification).Error; err == nil {
		t.Fatal("cross-tenant device verification was accepted")
	}
	validNonce := DeviceRequestNonce{TenantID: first.ID, DeviceID: firstDevice.ID, Nonce: "hardware-nonce", RequestID: "hardware-request", Path: "/api/v1/hardware/verify", ExpiresAt: time.Now().Add(time.Minute)}
	if err := db.Create(&validNonce).Error; err != nil {
		t.Fatalf("valid device nonce rejected: %v", err)
	}
	invalidNonce := validNonce
	invalidNonce.Base = Base{}
	invalidNonce.Nonce = "hardware-cross-nonce"
	invalidNonce.TenantID = first.ID
	invalidNonce.DeviceID = secondDevice.ID
	if err := db.Create(&invalidNonce).Error; err == nil {
		t.Fatal("cross-tenant device nonce was accepted")
	}
	validCommand := HardwareCommand{TenantID: first.ID, ScenicAreaID: firstArea.ID, DeviceID: firstDevice.ID, CommandNo: "HARDWARE-CMD-1", Kind: "open_gate", AckToken: "ack", QueuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	if err := db.Create(&validCommand).Error; err != nil {
		t.Fatalf("valid hardware command rejected: %v", err)
	}
	invalidCommand := validCommand
	invalidCommand.Base = Base{}
	invalidCommand.CommandNo = "HARDWARE-CMD-CROSS"
	invalidCommand.ScenicAreaID = secondArea.ID
	invalidCommand.DeviceID = secondDevice.ID
	if err := db.Create(&invalidCommand).Error; err == nil {
		t.Fatal("cross-tenant hardware command was accepted")
	}
	if err := db.Create(&HardwareEvent{TenantID: first.ID, DeviceID: firstDevice.ID, CommandNo: "VERIFY:" + validVerification.RequestID, EventType: "gate_open_unknown", Payload: "response lost"}).Error; err != nil {
		t.Fatalf("verification hardware event rejected: %v", err)
	}
	if err := db.Create(&HardwareEvent{TenantID: first.ID, DeviceID: firstDevice.ID, CommandNo: "HARDWARE-UNKNOWN", EventType: "gate_opened"}).Error; err == nil {
		t.Fatal("hardware event without an owned command or verification was accepted")
	}
	if err := db.Create(&DeviceAlert{TenantID: first.ID, ScenicAreaID: secondArea.ID, DeviceID: secondDevice.ID, Type: "offline", Status: "open", Message: "cross-tenant"}).Error; err == nil {
		t.Fatal("cross-tenant device alert was accepted")
	}
}

func TestPostgresSchema106MaintenanceOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Maintenance Guard A", SystemCode: "MNT-GUARD-A", SecretKey: "mnt-a", Status: "active"}
	second := Tenant{Name: "Maintenance Guard B", SystemCode: "MNT-GUARD-B", SecretKey: "mnt-b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	firstArea := ScenicArea{TenantID: first.ID, Code: "MNT-A", Name: "Maintenance A", Status: "active"}
	secondArea := ScenicArea{TenantID: second.ID, Code: "MNT-B", Name: "Maintenance B", Status: "active"}
	if err := db.Create(&firstArea).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondArea).Error; err != nil {
		t.Fatal(err)
	}
	firstUser := User{TenantID: first.ID, Username: "mnt-admin-a", Password: "hash", Role: "admin"}
	if err := db.Create(&firstUser).Error; err != nil {
		t.Fatal(err)
	}
	secondUser := User{TenantID: second.ID, Username: "mnt-admin-b", Password: "hash", Role: "admin"}
	if err := db.Create(&secondUser).Error; err != nil {
		t.Fatal(err)
	}
	firstDevice := Device{TenantID: first.ID, ScenicAreaID: firstArea.ID, Name: "Gate A", SerialNumber: "MNT-GATE-A", Type: "gate", Status: "online"}
	secondDevice := Device{TenantID: second.ID, ScenicAreaID: secondArea.ID, Name: "Gate B", SerialNumber: "MNT-GATE-B", Type: "gate", Status: "online"}
	if err := db.Create(&firstDevice).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&secondDevice).Error; err != nil {
		t.Fatal(err)
	}
	validCredential := DeviceMaintenanceCredential{TenantID: first.ID, ScenicAreaID: firstArea.ID, DeviceID: firstDevice.ID, SecretHash: strings.Repeat("a", 64), Status: "active"}
	if err := db.Create(&validCredential).Error; err != nil {
		t.Fatalf("valid maintenance credential rejected: %v", err)
	}
	invalidCredential := validCredential
	invalidCredential.Base = Base{}
	invalidCredential.SecretHash = strings.Repeat("b", 64)
	invalidCredential.TenantID = first.ID
	invalidCredential.ScenicAreaID = secondArea.ID
	invalidCredential.DeviceID = secondDevice.ID
	if err := db.Create(&invalidCredential).Error; err == nil {
		t.Fatal("cross-tenant maintenance credential was accepted")
	}
	validSession := DeviceMaintenanceSession{
		TenantID: first.ID, ScenicAreaID: firstArea.ID, DeviceID: firstDevice.ID, ActorUserID: firstUser.ID,
		Reason: "ownership test", Mode: "ssh", Status: "pending", TokenHash: strings.Repeat("c", 64),
		GatewaySessionID: "MNT-GUARD-SESSION", ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := db.Create(&validSession).Error; err != nil {
		t.Fatalf("valid maintenance session rejected: %v", err)
	}
	if err := db.Model(&validCredential).Update("secret_hash", strings.Repeat("e", 64)).Error; err == nil {
		t.Fatal("maintenance credential identity was mutable")
	}
	if err := db.Model(&validSession).Update("tenant_id", second.ID).Error; err == nil {
		t.Fatal("maintenance session tenant identity was mutable")
	}
	invalidSession := validSession
	invalidSession.Base = Base{}
	invalidSession.TokenHash = strings.Repeat("d", 64)
	invalidSession.GatewaySessionID = "MNT-GUARD-CROSS"
	invalidSession.ScenicAreaID = secondArea.ID
	invalidSession.DeviceID = secondDevice.ID
	if err := db.Create(&invalidSession).Error; err == nil {
		t.Fatal("cross-tenant maintenance session was accepted")
	}
	// Closing historical maintenance facts must remain possible after the
	// device or operator is soft-deleted; the immutable ownership columns are
	// still checked by the trigger.
	if err := db.Delete(&firstDevice).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&firstUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&validCredential).Updates(map[string]interface{}{"status": "revoked", "revoked_at": time.Now()}).Error; err != nil {
		t.Fatalf("historical credential could not be revoked after device deletion: %v", err)
	}
	if err := db.Model(&validSession).Updates(map[string]interface{}{"status": "interrupted", "closed_at": time.Now(), "closed_reason": "device removed"}).Error; err != nil {
		t.Fatalf("historical session could not be closed after owner deletion: %v", err)
	}
}

func TestPostgresSchema106UpgradeAddsMaintenanceFactsToSchema105(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version >= ?", CurrentPostgresSchemaVersion).Delete(&SchemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SchemaMigration{Version: 105, Name: "window order idempotency and print template orientation", AppliedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("schema 105 to 106 upgrade failed: %v", err)
	}
	for _, field := range []struct {
		model interface{}
		name  string
	}{{&DeviceMaintenanceCredential{}, "SecretHash"}, {&DeviceMaintenanceSession{}, "GatewaySessionID"}, {&DeviceMaintenanceSession{}, "Status"}} {
		if !db.Migrator().HasColumn(field.model, field.name) {
			t.Fatalf("schema 106 column %s for %T is missing", field.name, field.model)
		}
	}
	for _, index := range []struct {
		model interface{}
		name  string
	}{{&DeviceMaintenanceCredential{}, "idx_device_maintenance_active_credential"}, {&DeviceMaintenanceSession{}, "idx_device_maintenance_session_active_device"}} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("schema 106 index %s is missing", index.name)
		}
	}
	var latest SchemaMigration
	if err := db.Order("version DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Version != CurrentPostgresSchemaVersion {
		t.Fatalf("latest schema=%d, want %d", latest.Version, CurrentPostgresSchemaVersion)
	}
}

func TestPostgresSchema107ProvisioningOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "Provision Guard A", SystemCode: "PROV-GUARD-A", SecretKey: "prov-a", Status: "active"}
	second := Tenant{Name: "Provision Guard B", SystemCode: "PROV-GUARD-B", SecretKey: "prov-b", Status: "active"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	firstArea := ScenicArea{TenantID: first.ID, Code: "PROV-A", Name: "Provision A", Status: "active"}
	secondArea := ScenicArea{TenantID: second.ID, Code: "PROV-B", Name: "Provision B", Status: "active"}
	if err := db.Create(&firstArea).Create(&secondArea).Error; err != nil {
		t.Fatal(err)
	}
	firstUser := User{TenantID: first.ID, Username: "prov-admin-a", Password: "hash", Role: "admin"}
	secondUser := User{TenantID: second.ID, Username: "prov-admin-b", Password: "hash", Role: "admin"}
	if err := db.Create(&firstUser).Create(&secondUser).Error; err != nil {
		t.Fatal(err)
	}
	firstDevice := Device{TenantID: first.ID, ScenicAreaID: firstArea.ID, Name: "Provision Gate A", SerialNumber: "PROV-GATE-A", Type: "gate", Status: "offline"}
	secondDevice := Device{TenantID: second.ID, ScenicAreaID: secondArea.ID, Name: "Provision Gate B", SerialNumber: "PROV-GATE-B", Type: "gate", Status: "offline"}
	if err := db.Create(&firstDevice).Create(&secondDevice).Error; err != nil {
		t.Fatal(err)
	}
	valid := DeviceProvisioningLease{TenantID: first.ID, ScenicAreaID: firstArea.ID, DeviceID: firstDevice.ID, ActorUserID: firstUser.ID, Reason: "ownership", TokenHash: strings.Repeat("a", 64), Status: "pending", ExpiresAt: time.Now().Add(time.Minute)}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("valid provisioning lease rejected: %v", err)
	}
	invalid := valid
	invalid.Base = Base{}
	invalid.TokenHash = strings.Repeat("b", 64)
	invalid.ScenicAreaID = secondArea.ID
	invalid.DeviceID = secondDevice.ID
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("cross-tenant provisioning lease was accepted")
	}
	if err := db.Model(&valid).Update("tenant_id", second.ID).Error; err == nil {
		t.Fatal("provisioning lease identity was mutable")
	}
	if err := db.Model(&valid).Updates(map[string]interface{}{"status": "claimed", "installation_id": "install-guard", "installer_public_key": "pub", "installer_fingerprint": strings.Repeat("c", 64), "encrypted_bundle": "sealed", "claimed_at": time.Now()}).Error; err != nil {
		t.Fatalf("valid provisioning state update rejected: %v", err)
	}
	if err := db.Delete(&firstDevice).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&firstUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&valid).Updates(map[string]interface{}{"status": "expired", "encrypted_bundle": "", "installer_public_key": ""}).Error; err != nil {
		t.Fatalf("historical provisioning lease could not expire: %v", err)
	}
}

func TestPostgresSchema108XiaohongshuVoucherVerificationOwnershipGuards(t *testing.T) {
	db := testdb.Open(t)
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	first := Tenant{Name: "XHS Verification Guard A", SystemCode: "XHS-VERIFY-GUARD-A", SecretKey: "xhs-a", Status: "active"}
	second := Tenant{Name: "XHS Verification Guard B", SystemCode: "XHS-VERIFY-GUARD-B", SecretKey: "xhs-b", Status: "active"}
	if err := db.Create(&first).Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	area := ScenicArea{TenantID: first.ID, Code: "XHS-VERIFY-A", Name: "XHS Verify A", Status: "active"}
	if err := db.Create(&area).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckPoint{TenantID: first.ID, ScenicAreaID: area.ID, Name: "XHS Verify Entry"}
	if err := db.Create(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	checkpointID := checkpoint.ID
	device := Device{TenantID: first.ID, ScenicAreaID: area.ID, CheckPointID: &checkpointID, Name: "XHS Verify Gate", SerialNumber: "XHS-VERIFY-GATE", Type: "gate", Status: "online"}
	if err := db.Create(&device).Error; err != nil {
		t.Fatal(err)
	}
	account := ChannelAccount{TenantID: first.ID, Code: "xhs-verify-guard", Type: "xiaohongshu", AppID: "xhs-verify-app", Status: "sandbox", Environment: "sandbox"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	customer := MiniappCustomer{
		TenantID: first.ID, ChannelAccountID: account.ID, OpenIDHash: strings.Repeat("a", 64),
		OpenIDCiphertext: "sealed-open-id", SessionKeyCiphertext: "sealed-session", SessionTokenHash: strings.Repeat("b", 64),
		SessionExpiresAt: time.Now().Add(time.Hour), Status: "active", LastLoginAt: time.Now(),
	}
	if err := db.Create(&customer).Error; err != nil {
		t.Fatal(err)
	}
	product := Product{TenantID: first.ID, ScenicAreaID: area.ID, Name: "XHS Verify Ticket", ProductKind: "ticket", Type: "online", Status: "online", Price: 1}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	order := Order{TenantID: first.ID, OrderNo: "XHS-VERIFY-ORDER", Channel: "xiaohongshu", ChannelAccountID: account.ID, Status: "paid", Environment: "production"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	orderItem := OrderItem{OrderID: order.ID, ProductID: product.ID, ProductName: product.Name, Quantity: 1, Price: 1, FulfillmentProductID: product.ID, FulfillmentTenantID: first.ID, FulfillmentScenicAreaID: area.ID}
	if err := db.Create(&orderItem).Error; err != nil {
		t.Fatal(err)
	}
	ticket := Ticket{OrderItemID: orderItem.ID, OrderID: order.ID, TenantID: first.ID, ScenicAreaID: area.ID, FulfillmentProductID: product.ID, FulfillmentTenantID: first.ID, FulfillmentScenicAreaID: area.ID, TicketCode: "XHS-VERIFY-TICKET", Status: "unused", Environment: "production"}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	orderLink := XiaohongshuOrderLink{TenantID: first.ID, ChannelAccountID: account.ID, MiniappCustomerID: customer.ID, OrderID: order.ID, ClientRequestID: "xhs-verify-request", ExternalOrderID: "XHS-VERIFY-EXTERNAL", State: "paid"}
	if err := db.Create(&orderLink).Error; err != nil {
		t.Fatal(err)
	}
	voucherLink := XiaohongshuVoucherLink{TenantID: first.ID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: orderLink.ID, TicketID: ticket.ID, VoucherCodeHash: strings.Repeat("c", 64), VoucherCodeCiphertext: "ciphertext", Status: 1}
	if err := db.Create(&voucherLink).Error; err != nil {
		t.Fatal(err)
	}
	verification := DeviceVerification{TenantID: first.ID, ScenicAreaID: area.ID, DeviceID: device.ID, RequestID: "xhs-verify-scan", RequestHash: strings.Repeat("d", 64), TicketCode: ticket.TicketCode, Status: "processing"}
	if err := db.Create(&verification).Error; err != nil {
		t.Fatal(err)
	}
	saga := XiaohongshuVoucherVerification{
		TenantID: first.ID, ChannelAccountID: account.ID, VoucherLinkID: voucherLink.ID, TicketID: ticket.ID,
		DeviceVerificationID: verification.ID, DeviceID: device.ID, CheckPointID: checkpoint.ID, RequestID: verification.RequestID,
		RequestHash: verification.RequestHash, State: "prepared",
	}
	if err := db.Create(&saga).Error; err != nil {
		t.Fatalf("valid xiaohongshu voucher verification rejected: %v", err)
	}
	if err := db.Model(&saga).Update("tenant_id", second.ID).Error; err == nil {
		t.Fatal("cross-tenant xiaohongshu voucher verification was accepted")
	}
	checkIn := CheckInRecord{TenantID: first.ID, ScenicAreaID: area.ID, TicketID: ticket.ID, TicketCode: ticket.TicketCode, CheckPointID: checkpoint.ID, DeviceID: device.ID, DeviceRequestID: verification.RequestID, CheckInTime: time.Now(), Result: "success", Message: "verified"}
	if err := db.Create(&checkIn).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saga).Updates(map[string]interface{}{"state": "local_completed", "verify_id": "XHS-VERIFY-ID", "check_in_record_id": checkIn.ID, "local_completed_at": time.Now()}).Error; err != nil {
		t.Fatalf("valid local completion rejected: %v", err)
	}
	if err := db.Model(&saga).Update("check_in_record_id", 999999).Error; err == nil {
		t.Fatal("voucher verification accepted an unrelated check-in record")
	}
}
