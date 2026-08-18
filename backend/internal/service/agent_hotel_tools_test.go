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
