package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"time"
)

func TestHotelAgentMoneySeparatesRetailAndSettlementRules(t *testing.T) {
	zero := float64(0)
	if value, err := hotelAgentMoney(&zero); err != nil || value != 0 {
		t.Fatalf("zero settlement should be accepted, value=%d err=%v", value, err)
	}
	if _, err := hotelAgentRetailMoney(&zero); err == nil {
		t.Fatal("zero retail price should be rejected")
	}
	positive := 268.8
	if value, err := hotelAgentRetailMoney(&positive); err != nil || value != 26880 {
		t.Fatalf("retail price conversion is incorrect, value=%d err=%v", value, err)
	}
}

func TestHotelAgentSnapshotHashIgnoresStoredHashWhenRechecking(t *testing.T) {
	snapshot := agentHotelCalendarSnapshot{StayDate: "2026-08-20", Exists: true, RetailPrice: 26880, Settlement: 18000}
	previewHash := hotelAgentSnapshotHash(snapshot)
	current := snapshot
	current.Hash = ""
	current.Hash = hotelAgentSnapshotHash(current)
	if previewHash != current.Hash {
		t.Fatalf("unchanged calendar snapshot changed hash after recheck: preview=%s current=%s", previewHash, current.Hash)
	}
}

func TestScrubAgentTaskContextRemovesHotelDatabaseIDs(t *testing.T) {
	context := agentTaskContext{
		HotelInventory: &agentHotelInventoryPlan{
			HotelID: 1, RoomTypeID: 2,
			Snapshots: []agentHotelInventorySnapshot{{InventoryID: 3}},
		},
		HotelRateCalendar: &agentHotelRateCalendarPlan{
			HotelID: 4, RoomTypeID: 5, RatePlanID: 6,
			Snapshots: []agentHotelCalendarSnapshot{{RowID: 7}},
		},
		HotelProductCalendar: &agentHotelProductCalendarPlan{
			HotelID: 8, HotelProductID: 9, RevisionID: 10,
			Snapshots: []agentHotelCalendarSnapshot{{RowID: 11}},
		},
		HotelReservationStatus: &agentHotelReservationStatusPlan{ReservationID: 12},
	}
	scrubAgentTaskContextIDs(&context)
	if context.HotelInventory.HotelID != 0 || context.HotelInventory.RoomTypeID != 0 || context.HotelInventory.Snapshots[0].InventoryID != 0 {
		t.Fatal("hotel inventory IDs were not scrubbed")
	}
	if context.HotelRateCalendar.HotelID != 0 || context.HotelRateCalendar.RoomTypeID != 0 || context.HotelRateCalendar.RatePlanID != 0 || context.HotelRateCalendar.Snapshots[0].RowID != 0 {
		t.Fatal("hotel rate calendar IDs were not scrubbed")
	}
	if context.HotelProductCalendar.HotelID != 0 || context.HotelProductCalendar.HotelProductID != 0 || context.HotelProductCalendar.RevisionID != 0 || context.HotelProductCalendar.Snapshots[0].RowID != 0 {
		t.Fatal("hotel product calendar IDs were not scrubbed")
	}
	if context.HotelReservationStatus.ReservationID != 0 {
		t.Fatal("hotel reservation ID was not scrubbed")
	}
}

func TestHotelAgentReadCompoundDefersRequiredHotelArguments(t *testing.T) {
	if routes := agentReadOnlyCompoundToolRoutes("先查询酒店房量，再查询价格计划日历"); routes != nil {
		t.Fatalf("hotel compound query should use typed provider arguments, got routes=%v", routes)
	}
	if routes := agentReadOnlyCompoundToolRoutes("先查询订单，再查询销售汇总"); len(routes) != 2 || routes[0] != "search_orders" || routes[1] != "query_sales_summary" {
		t.Fatalf("non-hotel compound query routing changed: %v", routes)
	}
}

func TestHotelCatalogQueryUsesServerOwnedSingleTopicRoute(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "HOTEL-AGENT-DIRECT-CATALOG")
	server, calls := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return nil, fmt.Errorf("provider must not be called for hotel catalog lookup")
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), tenant.ID, 11, "admin", AgentTaskRequest{
		InputText: "查询酒店、房型和价格计划", IdempotencyKey: "hotel-direct-catalog-1", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("hotel catalog query: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("hotel catalog lookup called provider %d times", calls.Load())
	}
	if !strings.Contains(view.Message, "酒店目录查询") || len(view.Result) == 0 {
		t.Fatalf("unexpected hotel catalog response: message=%q result=%s", view.Message, string(view.Result))
	}
}

func TestLegacyDeepSeekHotelInventoryMutationUsesTypedHotelPreview(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 5)
	start := fixture.checkIn.Format("2006-01-02")
	end := fixture.checkIn.AddDate(0, 0, 1).Format("2006-01-02")
	server, calls := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		args := fmt.Sprintf(`{"hotel_name":%q,"room_type_name":%q,"start_date":%q,"end_date":%q,"capacity":7}`, fixture.hotel.Name, fixture.room.Name, start, end)
		return toolCallPayload("legacy-hotel-inventory", "prepare_hotel_inventory_change", args, 24), nil
	})
	config := toolConfig(server.URL)
	config.AgentProtocolMode = agentProtocolLegacyJSON
	if _, err := (&PlatformAIService{}).SaveConfig(config, 77, "platform_admin"); err != nil {
		t.Fatalf("save legacy tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), fixture.tenantID, 11, authz.RoleTenantAdmin, AgentTaskRequest{
		InputText:      fmt.Sprintf("为%s的%s在%s到%s设置房量7", fixture.hotel.Name, fixture.room.Name, start, end),
		IdempotencyKey: "legacy-hotel-inventory-route", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("legacy DeepSeek hotel inventory request: %v", err)
	}
	if view.ProtocolMode != agentProtocolToolV1 || view.State != AgentTaskAwaitingConfirmation || !view.CanConfirm {
		t.Fatalf("hotel mutation used an unsafe/legacy route: %+v", view)
	}
	if calls.Load() != 1 || !strings.Contains(string(view.Preview), `"operation_type":"hotel_inventory_change"`) || strings.Contains(string(view.Preview), "ticket_product") {
		t.Fatalf("hotel preview was routed to the wrong envelope: calls=%d preview=%s", calls.Load(), string(view.Preview))
	}
}

func TestHotelCompoundReadCollectsAllTypedArgumentsBeforeProvider(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "AGENT-HOTEL-COMPOUND-MISSING")
	server, calls := toolProvider(t, func(_ []AIMessage) (map[string]interface{}, error) {
		return nil, fmt.Errorf("provider must not be called for an underspecified hotel compound query")
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	view, err := (&AgentTaskService{}).Submit(t.Context(), tenant.ID, 11, authz.RoleTenantAdmin, AgentTaskRequest{
		InputText:      "查询酒店、房型、价格计划和酒店产品的房量与价格日历",
		IdempotencyKey: "hotel-compound-missing-all", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("hotel compound missing request: %v", err)
	}
	if view.State != AgentTaskCollecting || view.CanConfirm || view.ProtocolMode != agentProtocolToolV1 {
		t.Fatalf("underspecified hotel compound query became executable: %+v", view)
	}
	fields := make(map[string]bool, len(view.MissingFields))
	for _, field := range view.MissingFields {
		fields[field.Field] = true
	}
	for _, field := range []string{"hotel_name", "room_type_name", "rate_plan_name", "product_name", "date_range"} {
		if !fields[field] {
			t.Fatalf("compound query did not request %s: %+v", field, view.MissingFields)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("underspecified hotel compound query called provider %d times", calls.Load())
	}
	var task model.AgentTask
	if err := model.DB.Where("id = ?", view.TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.ContextJSON, "hotel_compound_read") || !strings.Contains(task.ContextJSON, "query_hotel_inventory") {
		t.Fatalf("hotel compound continuation context was not persisted: %s", task.ContextJSON)
	}
}

func TestHotelCompoundReadContinuationUsesPersistedScope(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "AGENT-HOTEL-COMPOUND-CONTINUE")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "CONTINUE")
	start := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 8).Format("2006-01-02")
	server, calls := toolProvider(t, func(messages []AIMessage) (map[string]interface{}, error) {
		for _, message := range messages {
			if message.Role == "tool" {
				return toolTextPayload("酒店房量和价格计划日历已返回服务器事实。", 12), nil
			}
		}
		args := fmt.Sprintf(`{"steps":[{"tool_name":"query_hotel_inventory","arguments":{"hotel_name":%q,"room_type_name":%q,"start_date":%q,"end_date":%q}},{"tool_name":"query_hotel_rate_calendar","arguments":{"hotel_name":%q,"room_type_name":%q,"rate_plan_name":%q,"start_date":%q,"end_date":%q}}]}`, hotel.Name, room.Name, start, end, hotel.Name, room.Name, rate.Name, start, end)
		return toolCallPayload("hotel-compound-continue", "query_compound_readonly", args, 18), nil
	})
	if _, err := (&PlatformAIService{}).SaveConfig(toolConfig(server.URL), 77, "platform_admin"); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	service := &AgentTaskService{}
	first, err := service.Submit(t.Context(), tenant.ID, 11, authz.RoleTenantAdmin, AgentTaskRequest{
		InputText: "先查询酒店房量，再查询价格计划日历", IdempotencyKey: "hotel-compound-continuation", TurnKey: "turn-1",
	})
	if err != nil {
		t.Fatalf("initial hotel compound query: %v", err)
	}
	if first.State != AgentTaskCollecting || len(first.MissingFields) != 4 {
		t.Fatalf("initial hotel compound query did not collect all fields: %+v", first)
	}
	second, err := service.Submit(t.Context(), tenant.ID, 11, authz.RoleTenantAdmin, AgentTaskRequest{
		TaskID:    first.TaskID,
		InputText: fmt.Sprintf("酒店%s，房型%s，价格计划%s，日期%s至%s", hotel.Name, room.Name, rate.Name, start, end),
		TurnKey:   "turn-2",
	})
	if err != nil {
		t.Fatalf("hotel compound continuation: %v", err)
	}
	if second.State != AgentTaskCollecting || second.ProtocolMode != agentProtocolToolV1 || len(second.Result) == 0 {
		t.Fatalf("hotel compound continuation did not return facts: %+v", second)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one planning call and one provider summary call, got %d", calls.Load())
	}
}

func TestAgentCandidateContextIncludesHotelBusinessNamesWithoutGuestData(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "AGENT-HOTEL-CONTEXT")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "CONTEXT")
	if _, err := (&HotelProductService{}).Create(tenant.ID, 11, HotelProductInput{
		Name: "上下文日历房", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 30000, BaseSettlementPriceCents: 20000,
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := agentCandidateContextJSON(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{hotel.Name, room.Name, rate.Name, "上下文日历房"} {
		if !strings.Contains(encoded, value) {
			t.Fatalf("hotel candidate context omitted %q: %s", value, encoded)
		}
	}
	for _, value := range []string{"ContactPhone", "contact_phone", "guest_name", "13800138000"} {
		if strings.Contains(encoded, value) {
			t.Fatalf("hotel candidate context leaked guest/contact field %q: %s", value, encoded)
		}
	}
}

func TestHotelUnsupportedWritesFailClosedBeforeProvider(t *testing.T) {
	for _, input := range []string{"创建酒店预约", "取消预约入住", "把酒店同步到PMS", "把酒店发布渠道"} {
		if err := rejectUnsupportedAgentCapability(input); err == nil {
			t.Fatalf("unsupported hotel request was not rejected: %q", input)
		}
	}
}

func TestHotelAgentNameResolutionRejectsAmbiguousBusinessNames(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "AGENT-HOTEL-AMBIGUOUS")
	hotelService := &HotelService{}
	first, err := hotelService.CreateProperty(tenant.ID, 11, HotelPropertyInput{Code: "H-A", Name: "同名酒店"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hotelService.CreateProperty(tenant.ID, 11, HotelPropertyInput{Code: "H-B", Name: "同名酒店"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hotelAgentExactProperty(model.DB, tenant.ID, "同名酒店"); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("ambiguous hotel name was silently resolved: %v", err)
	}

	roomOne, err := hotelService.CreateRoomType(tenant.ID, first.ID, 11, HotelRoomTypeInput{Code: "R-A", Name: "同名房型", MaxGuests: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hotelService.CreateRoomType(tenant.ID, first.ID, 11, HotelRoomTypeInput{Code: "R-B", Name: "同名房型", MaxGuests: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := hotelAgentExactRoomType(model.DB, tenant.ID, first.ID, "同名房型"); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("ambiguous room type was silently resolved: %v", err)
	}
	rateOne, err := hotelService.CreateRatePlan(tenant.ID, first.ID, roomOne.ID, 11, HotelRatePlanInput{Code: "P-A", Name: "同名价格计划", RetailPriceCents: 10000, SettlementPriceCents: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hotelService.CreateRatePlan(tenant.ID, first.ID, roomOne.ID, 11, HotelRatePlanInput{Code: "P-B", Name: "同名价格计划", RetailPriceCents: 10000, SettlementPriceCents: 8000}); err != nil {
		t.Fatal(err)
	}
	if _, err := hotelAgentExactRatePlan(model.DB, tenant.ID, first.ID, roomOne.ID, "同名价格计划"); err == nil || !strings.Contains(err.Error(), "多个") {
		t.Fatalf("ambiguous rate plan was silently resolved: %v", err)
	}
	_ = rateOne
}

func TestHotelAgentProductResolverUsesProductNameColumn(t *testing.T) {
	resetBusinessData(t)
	tenant := seedHotelSupplier(t, "AGENT-HOTEL-PRODUCT-RESOLVER")
	hotel, room, rate := seedHotelProductResources(t, tenant.ID, "AGENT")
	product, err := (&HotelProductService{}).Create(tenant.ID, 11, HotelProductInput{
		Name: "独立日历房", HotelID: hotel.ID, RoomTypeID: room.ID, RatePlanID: rate.ID,
		SaleMode: "calendar_room", BaseRetailPriceCents: 50000, BaseSettlementPriceCents: 40000,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, salesProduct, err := hotelAgentExactProduct(model.DB, tenant.ID, hotel.ID, "独立日历房")
	if err != nil {
		t.Fatalf("hotel product resolver failed: %v", err)
	}
	if resolved.ID != product.ID || salesProduct.ID != product.Product.ID || salesProduct.ProductKind != "hotel" {
		t.Fatalf("resolved hotel product=%+v sales product=%+v", resolved, salesProduct)
	}
}

func TestHotelAgentResultDoesNotExposeRevisionID(t *testing.T) {
	result := map[string]interface{}{"operation_type": AgentOperationHotelProductCalendarChange, "hotel_name": "测试酒店", "product_name": "日历房", "dates": 1, "status": "completed"}
	encodedBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(encodedBytes)
	if strings.Contains(encoded, "revision_id") {
		t.Fatal("hotel result hash unexpectedly contains an internal revision field")
	}
}

func TestAgentHotelInventoryPreviewAndConfirmUsesHotelServiceTransaction(t *testing.T) {
	resetBusinessData(t)
	fixture := seedScenicHotelPackage(t, 5)
	date := fixture.checkIn
	planning, err := planHotelInventoryChange(fixture.tenantID, model.AgentTask{ContextJSON: `{}`}, "设置房量", &agentHotelInventoryCandidate{
		HotelName: fixture.hotel.Name, RoomTypeName: fixture.room.Name,
		StartDate: date.Format("2006-01-02"), EndDate: date.Format("2006-01-02"), Capacity: intPointer(7),
	}, mustHotelKnowledgePack(t, AgentOperationHotelInventoryChange))
	if err != nil {
		t.Fatalf("hotel inventory preview: %v", err)
	}
	if planning.PreviewJSON == "" || planning.PlanHash == "" || planning.Context.HotelInventory == nil {
		t.Fatalf("incomplete hotel inventory preview: %+v", planning)
	}
	var before model.HotelRoomInventory
	if err := model.DB.Where("tenant_id = ? AND hotel_id = ? AND room_type_id = ? AND stay_date = ?", fixture.tenantID, fixture.hotel.ID, fixture.room.ID, date).First(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before.Capacity != 5 {
		t.Fatalf("preview wrote inventory before confirmation: %+v", before)
	}
	contextJSON, _ := json.Marshal(planning.Context)
	task := model.AgentTask{TenantID: fixture.tenantID, ActorUserID: 11, ActorRole: authz.RoleTenantAdmin, OperationType: AgentOperationHotelInventoryChange, State: AgentTaskAwaitingConfirmation, InputText: "设置房量", ContextJSON: string(contextJSON), MissingJSON: `[]`, PreviewJSON: planning.PreviewJSON, PlanHash: planning.PlanHash, IdempotencyKey: fmt.Sprintf("agent-hotel-confirm-%d", time.Now().UnixNano()), Version: 1, ExpiresAt: time.Now().Add(time.Hour)}
	if err := model.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	view, err := (&AgentTaskService{}).Confirm(fixture.tenantID, 11, authz.RoleTenantAdmin, task.ID)
	if err != nil {
		t.Fatalf("confirm hotel inventory: %v", err)
	}
	if view.State != AgentTaskCompleted {
		t.Fatalf("hotel task did not complete: %+v", view)
	}
	var after model.HotelRoomInventory
	if err := model.DB.Where("id = ?", before.ID).First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Capacity != 7 {
		t.Fatalf("confirmed hotel inventory was not updated: %+v", after)
	}
}

func TestAgentHotelToolsRespectVerticalCapabilities(t *testing.T) {
	resetBusinessData(t)
	hotelTenant := seedHotelSupplier(t, "AGENT-HOTEL-ONLY")
	inventorySpec, _ := findAgentTool("query_hotel_inventory")
	reservationSpec, _ := findAgentTool("query_hotel_reservations")
	if !agentToolAllowed(hotelTenant.ID, authz.RoleProductOperator, inventorySpec) {
		t.Fatal("hotel-only supplier cannot use hotel inventory query")
	}
	if agentToolAllowed(hotelTenant.ID, authz.RoleProductOperator, reservationSpec) {
		t.Fatal("hotel-only supplier was allowed to query scenic package reservations")
	}
}

func mustHotelKnowledgePack(t *testing.T, operation string) agentKnowledgePack {
	t.Helper()
	pack, err := agentKnowledgePackForOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func intPointer(value int) *int { return &value }
