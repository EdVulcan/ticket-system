package service

import (
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestOnlineOrderFiltersRespectSalesScopeAndLegacyChannelEquality(t *testing.T) {
	resetBusinessData(t)
	tenantID, _ := seedSellableProduct(t, "unlimited", 0)
	foreignTenantID, _ := seedSellableProduct(t, "unlimited", 0)

	xiaohongshuAccount := model.ChannelAccount{
		TenantID: tenantID, Code: "order-filter-xhs", Type: "xiaohongshu", Status: "active", Environment: "production",
	}
	ctripAccount := model.ChannelAccount{
		TenantID: tenantID, Code: "order-filter-ctrip", Type: "ctrip", Status: "active", Environment: "production",
	}
	customAccount := model.ChannelAccount{
		TenantID: tenantID, Code: "order-filter-partner", Type: "partner", Status: "active", Environment: "production",
	}
	sandboxAccount := model.ChannelAccount{
		TenantID: tenantID, Code: "order-filter-sandbox", Type: "xiaohongshu", Status: "sandbox", Environment: "sandbox",
	}
	for _, account := range []*model.ChannelAccount{&xiaohongshuAccount, &ctripAccount, &customAccount, &sandboxAccount} {
		if err := model.DB.Create(account).Error; err != nil {
			t.Fatalf("create channel account %s: %v", account.Code, err)
		}
	}

	baseDate := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	createOrder := func(order model.Order, createdAt time.Time) model.Order {
		t.Helper()
		if order.Environment == "" {
			order.Environment = "production"
		}
		if order.Status == "" {
			order.Status = "unpaid"
		}
		order.CreatedAt = createdAt
		if err := model.DB.Create(&order).Error; err != nil {
			t.Fatalf("create order %s: %v", order.OrderNo, err)
		}
		return order
	}
	createExternal := func(value string) *string { return &value }

	visibleOnline := createOrder(model.Order{
		OrderNo: "FILTER-ONLINE-PAID", TenantID: tenantID, Status: "paid", Channel: "online",
		ContactName: "线上游客", ContactPhone: "13800000001",
	}, baseDate)
	createOrder(model.Order{
		OrderNo: "FILTER-OTA-COMPLETE", TenantID: tenantID, Status: "completed", Channel: "ota",
		ExternalNo: createExternal("OTA-EXT-100"), ContactName: "OTA游客",
	}, baseDate.AddDate(0, 0, 1))
	createOrder(model.Order{
		OrderNo: "FILTER-XHS-REFUNDED", TenantID: tenantID, Status: "refunded", Channel: "xiaohongshu",
		ChannelAccountID: xiaohongshuAccount.ID, ExternalNo: createExternal("XHS-EXT-200"), ContactName: "小红书游客",
	}, baseDate.AddDate(0, 0, 2))
	ctripOrder := createOrder(model.Order{
		OrderNo: "FILTER-CTRIP-PARTIAL", TenantID: tenantID, Status: "partial_refunded", Channel: fmt.Sprintf("ctrip:%d", ctripAccount.ID),
		ChannelAccountID: ctripAccount.ID, ExternalNo: createExternal("CTRIP-EXT-300"), ContactName: "携程游客",
	}, baseDate.AddDate(0, 0, 3))
	createOrder(model.Order{
		OrderNo: "FILTER-PARTNER-UNPAID", TenantID: tenantID, Status: "unpaid", Channel: customAccount.Code,
		ChannelAccountID: customAccount.ID, ContactName: "合作方游客",
	}, baseDate.AddDate(0, 0, 4))
	createOrder(model.Order{
		OrderNo: "FILTER-ONLINE-CANCELLED", TenantID: tenantID, Status: "cancelled", Channel: "online",
		ContactName: "取消游客",
	}, baseDate.AddDate(0, 0, 5))

	createOrder(model.Order{OrderNo: "FILTER-WINDOW", TenantID: tenantID, Status: "paid", Channel: "window"}, baseDate.AddDate(0, 0, 6))
	createOrder(model.Order{OrderNo: "FILTER-OFFLINE", TenantID: tenantID, Status: "paid", Channel: "offline"}, baseDate.AddDate(0, 0, 7))
	createOrder(model.Order{OrderNo: "FILTER-TEAM-CHANNEL", TenantID: tenantID, Status: "paid", Channel: "team"}, baseDate.AddDate(0, 0, 8))
	createOrder(model.Order{OrderNo: "FILTER-SANDBOX", TenantID: tenantID, Status: "paid", Channel: "online", Environment: "sandbox"}, baseDate.AddDate(0, 0, 9))
	createOrder(model.Order{
		OrderNo: "FILTER-SANDBOX-ACCOUNT", TenantID: tenantID, Status: "paid", Channel: "xiaohongshu",
		ChannelAccountID: sandboxAccount.ID, Environment: "sandbox",
	}, baseDate.AddDate(0, 0, 10))
	teamLinked := createOrder(model.Order{OrderNo: "FILTER-TEAM-LINKED", TenantID: tenantID, Status: "paid", Channel: "team"}, baseDate.AddDate(0, 0, 11))
	var area model.ScenicArea
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&area).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.TourGroup{
		TenantID: tenantID, SupplierTenantID: tenantID, ScenicAreaID: area.ID, SalesOrderID: teamLinked.ID,
		GroupNo: "FILTER-TEAM-GROUP", Name: "线上订单团队归属", VisitDate: baseDate, ExpectedCount: 1, Status: "confirmed",
	}).Error; err != nil {
		t.Fatalf("create linked team group: %v", err)
	}
	createOrder(model.Order{OrderNo: "FILTER-FOREIGN", TenantID: foreignTenantID, Status: "paid", Channel: "online"}, baseDate)

	service := &OrderService{}
	orders, total, err := service.ListWithSalesScope(1, 100, tenantID, "", "", "online", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 6 || len(orders) != 6 {
		t.Fatalf("online scope total=%d rows=%d, want 6", total, len(orders))
	}
	visible := make(map[string]bool, len(orders))
	for _, order := range orders {
		visible[order.OrderNo] = true
	}
	for _, orderNo := range []string{
		visibleOnline.OrderNo, "FILTER-OTA-COMPLETE", "FILTER-XHS-REFUNDED", ctripOrder.OrderNo,
		"FILTER-PARTNER-UNPAID", "FILTER-ONLINE-CANCELLED",
	} {
		if !visible[orderNo] {
			t.Errorf("online scope omitted %s", orderNo)
		}
	}
	for _, orderNo := range []string{
		"FILTER-WINDOW", "FILTER-OFFLINE", "FILTER-TEAM-CHANNEL", "FILTER-SANDBOX",
		"FILTER-SANDBOX-ACCOUNT", "FILTER-TEAM-LINKED", "FILTER-FOREIGN",
	} {
		if visible[orderNo] {
			t.Errorf("online scope included excluded order %s", orderNo)
		}
	}

	pageOne, pageTotal, err := service.ListWithSalesScope(1, 2, tenantID, "", "", "online", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	pageTwo, pageTwoTotal, err := service.ListWithSalesScope(2, 2, tenantID, "", "", "online", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if pageTotal != 6 || pageTwoTotal != 6 || len(pageOne) != 2 || len(pageTwo) != 2 {
		t.Fatalf("pagination page1=%d/%d page2=%d/%d", len(pageOne), pageTotal, len(pageTwo), pageTwoTotal)
	}
	if pageOne[0].OrderNo == pageTwo[0].OrderNo || pageOne[1].OrderNo == pageTwo[1].OrderNo {
		t.Fatalf("pagination returned duplicate rows: page1=%q/%q page2=%q/%q", pageOne[0].OrderNo, pageOne[1].OrderNo, pageTwo[0].OrderNo, pageTwo[1].OrderNo)
	}

	paid, paidTotal, err := service.ListWithSalesScope(1, 100, tenantID, "paid", "", "online", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paidTotal != 1 || len(paid) != 1 || paid[0].OrderNo != visibleOnline.OrderNo {
		t.Fatalf("status filter paid total=%d rows=%+v", paidTotal, paid)
	}
	dateFiltered, dateTotal, err := service.ListWithSalesScope(1, 100, tenantID, "", "", "online", "2026-08-11", "2026-08-13", "")
	if err != nil {
		t.Fatal(err)
	}
	if dateTotal != 3 || len(dateFiltered) != 3 {
		t.Fatalf("date filter total=%d rows=%d", dateTotal, len(dateFiltered))
	}
	searchFiltered, searchTotal, err := service.ListWithSalesScope(1, 100, tenantID, "", "", "online", "", "", "XHS-EXT-200")
	if err != nil {
		t.Fatal(err)
	}
	if searchTotal != 1 || len(searchFiltered) != 1 || searchFiltered[0].OrderNo != "FILTER-XHS-REFUNDED" {
		t.Fatalf("search filter total=%d rows=%+v", searchTotal, searchFiltered)
	}

	legacyExact, legacyTotal, err := service.List(1, 100, tenantID, "", "ctrip", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if legacyTotal != 0 || len(legacyExact) != 0 {
		t.Fatalf("legacy channel filter treated ctrip as prefix: total=%d rows=%d", legacyTotal, len(legacyExact))
	}
	legacyExact, legacyTotal, err = service.List(1, 100, tenantID, "", "xiaohongshu", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if legacyTotal != 2 || len(legacyExact) != 2 {
		t.Fatalf("legacy xiaohongshu channel filter total=%d rows=%d", legacyTotal, len(legacyExact))
	}

	foreign, foreignTotal, err := service.ListWithSalesScope(1, 100, foreignTenantID, "", "", "online", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if foreignTotal != 1 || len(foreign) != 1 || foreign[0].OrderNo != "FILTER-FOREIGN" {
		t.Fatalf("tenant isolation total=%d rows=%+v", foreignTotal, foreign)
	}

	options, err := service.ListChannelOptions(tenantID, "online")
	if err != nil {
		t.Fatal(err)
	}
	optionLabels := make(map[string]string, len(options))
	for _, option := range options {
		optionLabels[option.Value] = option.Label
	}
	expectedOptions := map[string]string{
		"online":                                 "线上",
		"ota":                                    "OTA",
		"xiaohongshu":                            "小红书",
		fmt.Sprintf("ctrip:%d", ctripAccount.ID): "携程 · order-filter-ctrip",
		"order-filter-partner":                   "order-filter-partner",
	}
	if len(optionLabels) != len(expectedOptions) {
		t.Fatalf("channel options=%+v, want %d options", options, len(expectedOptions))
	}
	for value, label := range expectedOptions {
		if optionLabels[value] != label {
			t.Errorf("channel option %q label=%q, want %q (all=%+v)", value, optionLabels[value], label, options)
		}
	}

	if _, _, err := service.ListWithSalesScope(1, 10, tenantID, "", "", "offline", "", "", ""); err == nil {
		t.Fatal("unsupported sales scope was accepted")
	}
}
