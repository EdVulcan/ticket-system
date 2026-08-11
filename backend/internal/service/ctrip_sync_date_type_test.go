package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/ctrip"
	"ticket-backend/internal/model"
	"time"
)

type ctripRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ctripRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func ctripSuccessHTTPResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"header":{"resultCode":"0000","resultMessage":"success"}}`)),
	}
}

func createCtripSyncFixture(t *testing.T, validityType string) (uint, uint, uint) {
	t.Helper()
	previousKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.GlobalConfig.Security.EncryptionKey = previousKey })

	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", productID, tenantID).Update("validity_type", validityType).Error; err != nil {
		t.Fatal(err)
	}
	channel := model.ChannelAccount{Code: "ctrip-date-type", Type: "ctrip", Status: "sandbox", PermissionsJSON: `["products:read"]`, RateLimitPerMin: 600}
	if err := (&ChannelService{}).CreateCtrip(tenantID, &channel, "ctrip-account", "sign-key", "1234567890abcdef", "abcdef1234567890"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: channel.ID, ProductID: productID, ExternalCode: "PLU-DATE-TYPE", ChannelSaleCents: 201, ChannelCostCents: 200}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	return tenantID, channel.ID, mapping.ID
}

func TestCtripNonDateSyncOmitsDates(t *testing.T) {
	resetBusinessData(t)
	tenantID, channelID, mappingID := createCtripSyncFixture(t, "days")
	start := startOfDay(time.Now().AddDate(0, 0, 1))
	result, err := (&CtripSyncService{}).EnqueueMappingSync(tenantID, channelID, mappingID, start, start.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("tasks=%d, want price and inventory", len(result.Tasks))
	}
	var price ctrip.PriceRequest
	var inventory ctrip.InventoryRequest
	for _, task := range result.Tasks {
		switch task.Kind {
		case "price":
			if err := json.Unmarshal([]byte(task.PayloadJSON), &price); err != nil {
				t.Fatal(err)
			}
		case "inventory":
			if err := json.Unmarshal([]byte(task.PayloadJSON), &inventory); err != nil {
				t.Fatal(err)
			}
		}
	}
	if price.DateType != "DATE_NOT_REQUIRED" || len(price.Prices) != 1 || price.Prices[0].Date != "" {
		t.Fatalf("non-date price payload=%+v", price)
	}
	if inventory.DateType != "DATE_NOT_REQUIRED" || len(inventory.Inventories) != 1 || inventory.Inventories[0].Date != "" {
		t.Fatalf("non-date inventory payload=%+v", inventory)
	}
}

func TestCtripSpecifiedDateSyncKeepsDailyRows(t *testing.T) {
	resetBusinessData(t)
	tenantID, channelID, mappingID := createCtripSyncFixture(t, "date")
	start := startOfDay(time.Now().AddDate(0, 0, 1))
	result, err := (&CtripSyncService{}).EnqueueMappingSync(tenantID, channelID, mappingID, start, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var price ctrip.PriceRequest
	for _, task := range result.Tasks {
		if task.Kind == "price" {
			if err := json.Unmarshal([]byte(task.PayloadJSON), &price); err != nil {
				t.Fatal(err)
			}
		}
	}
	if price.DateType != "DATE_REQUIRED" || len(price.Prices) != 2 || price.Prices[0].Date == "" || price.Prices[1].Date == "" {
		t.Fatalf("specified-date price payload=%+v", price)
	}
}

func TestCtripNonDateSyncRejectsDailyInventory(t *testing.T) {
	resetBusinessData(t)
	tenantID, channelID, mappingID := createCtripSyncFixture(t, "days")
	var mapping model.ChannelProductMapping
	if err := model.DB.First(&mapping, mappingID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Product{}).Where("id = ? AND tenant_id = ?", mapping.ProductID, tenantID).Update("stock_type", "daily").Error; err != nil {
		t.Fatal(err)
	}
	start := startOfDay(time.Now().AddDate(0, 0, 1))
	_, err := (&CtripSyncService{}).EnqueueMappingSync(tenantID, channelID, mappingID, start, start)
	if err == nil || !strings.Contains(err.Error(), "unlimited or total inventory") {
		t.Fatalf("daily inventory error=%v", err)
	}
}

func TestCtripScenicSuspensionClosesDistributorMappingAfterInFlightSync(t *testing.T) {
	resetBusinessData(t)
	previousKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.GlobalConfig.Security.EncryptionKey = previousKey })

	scenario := seedDistributionScenario(t)
	channel := model.ChannelAccount{Code: "ctrip-distributor-suspension", Type: "ctrip", Status: "sandbox", PermissionsJSON: `["products:read","orders:create","orders:query","orders:cancel"]`, RateLimitPerMin: 600}
	if err := (&ChannelService{}).CreateCtrip(scenario.distributorID, &channel, "ctrip-account", "sign-key", "1234567890abcdef", "abcdef1234567890"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: channel.ID, ProductID: scenario.listingID, ExternalCode: "PLU-DISTRIBUTOR-SUSPEND", ChannelSaleCents: 8000, ChannelCostCents: 7000}
	if err := (&ChannelService{}).AddMapping(scenario.distributorID, &mapping); err != nil {
		t.Fatal(err)
	}
	farStart := startOfDay(time.Now().AddDate(0, 0, 150))
	result, err := (&CtripSyncService{}).EnqueueMappingSync(scenario.distributorID, channel.ID, mapping.ID, farStart, farStart.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("sync distributor listing: %v", err)
	}
	visitDate := farStart.Format("2006-01-02")
	protocol := &CtripProtocolService{OrderService: OrderService{}}
	createBody := map[string]interface{}{
		"sequenceId": "ctrip-distributor-create", "otaOrderId": "CTRIP-DISTRIBUTOR-ORDER",
		"contacts": []map[string]string{{"name": "Test Visitor", "mobile": "13800138000"}},
		"items": []map[string]interface{}{{
			"PLU": "PLU-DISTRIBUTOR-SUSPEND", "quantity": 1,
			"price": 80.00, "priceCurrency": "CNY", "salePrice": 80.00, "salePriceCurrency": "CNY", "cost": 70.00, "costCurrency": "CNY",
			"useStartDate": visitDate, "useEndDate": visitDate,
			"passengers": []map[string]string{{"passengerId": "DISTRIBUTOR-PASSENGER", "name": "Test Visitor", "mobile": "13800138000"}},
		}},
	}
	createResponse, err := protocol.Handle(buildCtripTestRequest(t, "ctrip-account", "sign-key", "1234567890abcdef", "abcdef1234567890", "CreatePreOrder", createBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	created := decodeCtripTestResponse(t, createResponse, "1234567890abcdef", "abcdef1234567890")
	if created.Code != "0000" {
		t.Fatalf("distributed Ctrip preorder=%+v", created)
	}
	supplierOrderID, _ := created.Body["supplierOrderId"].(string)
	payBody := map[string]interface{}{
		"sequenceId": "ctrip-distributor-pay", "otaOrderId": "CTRIP-DISTRIBUTOR-ORDER", "supplierOrderId": supplierOrderID,
		"confirmType": 2, "orderLastConfirmTime": time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"),
		"items": []map[string]string{{"itemId": "DISTRIBUTOR-ITEM", "PLU": "PLU-DISTRIBUTOR-SUSPEND"}},
	}
	payResponse, err := protocol.Handle(buildCtripTestRequest(t, "ctrip-account", "sign-key", "1234567890abcdef", "abcdef1234567890", "PayPreOrder", payBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if paid := decodeCtripTestResponse(t, payResponse, "1234567890abcdef", "abcdef1234567890"); paid.Code != "0000" {
		t.Fatalf("distributed Ctrip payment=%+v", paid)
	}
	var sold model.Order
	if err := model.DB.Preload("Items.Tickets").Where("tenant_id = ? AND order_no = ?", scenario.distributorID, supplierOrderID).First(&sold).Error; err != nil {
		t.Fatal(err)
	}
	if sold.Status != "paid" || len(sold.Items) != 1 || sold.Items[0].FulfillmentTenantID != scenario.supplierID || len(sold.Items[0].Tickets) != 1 || sold.Items[0].Tickets[0].FulfillmentTenantID != scenario.supplierID {
		t.Fatalf("distributed Ctrip fulfillment ownership=%+v", sold)
	}
	if err := (&ChannelService{}).UpdateMapping(scenario.distributorID, channel.ID, mapping.ID, ChannelMappingUpdate{
		ExternalCode: "PLU-DISTRIBUTOR-NEW", ChannelSaleCents: 8000, ChannelCostCents: 7000, Status: "active",
	}); err != nil {
		t.Fatalf("change mapping code: %v", err)
	}
	var originalInventory model.CtripOutboundTask
	originalIDs := make([]uint, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		originalIDs = append(originalIDs, task.ID)
		if task.Kind == "inventory" {
			originalInventory = task
		}
	}
	if originalInventory.ID == 0 {
		t.Fatal("inventory synchronization task was not created")
	}
	if err := model.DB.Model(&model.CtripOutboundTask{}).
		Where("channel_product_mapping_id = ? AND kind = ?", mapping.ID, "price").
		Updates(map[string]interface{}{"status": "failed", "next_attempt_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	lockedAt := time.Now()
	if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", originalInventory.ID).
		Updates(map[string]interface{}{"status": "processing", "locked_at": lockedAt}).Error; err != nil {
		t.Fatal(err)
	}
	originalInventory.Status = "processing"
	originalInventory.LockedAt = &lockedAt

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var requestMu sync.Mutex
	requestCount := 0
	httpClient := &http.Client{Transport: ctripRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestMu.Lock()
		requestCount++
		current := requestCount
		requestMu.Unlock()
		if current == 1 {
			close(requestStarted)
			select {
			case <-releaseRequest:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		return ctripSuccessHTTPResponse(), nil
	})}
	syncService := &CtripSyncService{HTTP: httpClient}
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- syncService.processTask(context.Background(), &originalInventory, time.Now())
	}()
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("inventory request did not start")
	}

	suspensionStarted := make(chan struct{})
	suspensionDone := make(chan error, 1)
	go func() {
		close(suspensionStarted)
		suspensionDone <- (&TenantService{}).SetSupplierBusinessTypeAudited(scenario.supplierID, "scenic", "suspended", "pause scenic sales", 9, "platform_admin")
	}()
	<-suspensionStarted
	select {
	case err := <-suspensionDone:
		t.Fatalf("suspension bypassed the in-flight inventory lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseRequest)
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatalf("finish in-flight inventory: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight inventory did not finish")
	}
	select {
	case err := <-suspensionDone:
		if err != nil {
			t.Fatalf("suspend scenic business: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("suspension did not resume after inventory completed")
	}
	if err := model.DB.First(&mapping, mapping.ID).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.Status != "disabled" {
		t.Fatalf("mapping status=%q, want disabled", mapping.Status)
	}
	var completedInventory model.CtripOutboundTask
	if err := model.DB.First(&completedInventory, originalInventory.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completedInventory.Status != "succeeded" || completedInventory.LockedAt != nil {
		t.Fatalf("in-flight inventory was not completed before suspension: %+v", completedInventory)
	}
	var zeroTasks []model.CtripOutboundTask
	if err := model.DB.Where("channel_product_mapping_id = ? AND id NOT IN ? AND kind = ?", mapping.ID, originalIDs, "inventory_shutdown").Find(&zeroTasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(zeroTasks) == 0 {
		t.Fatal("scenic suspension did not enqueue zero inventory")
	}
	foundFarDate := false
	for index := range zeroTasks {
		task := &zeroTasks[index]
		if task.Status != "pending" || task.NextAttemptAt == nil {
			t.Fatalf("zero inventory task is not pending: %+v", task)
		}
		if !ctripInventoryPayloadIsZero(task.PayloadJSON) {
			t.Fatalf("suspension task contains non-zero inventory: %s", task.PayloadJSON)
		}
		var payload ctrip.InventoryRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		for _, inventory := range payload.Inventories {
			if payload.SupplierOptionID == "PLU-DISTRIBUTOR-SUSPEND" && inventory.Date == farStart.Format("2006-01-02") {
				foundFarDate = true
			}
		}
		zeroLockedAt := time.Now().Add(time.Duration(index+1) * time.Nanosecond)
		if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", task.ID).
			Updates(map[string]interface{}{"status": "processing", "locked_at": zeroLockedAt}).Error; err != nil {
			t.Fatal(err)
		}
		task.Status = "processing"
		task.LockedAt = &zeroLockedAt
		if err := syncService.processTask(context.Background(), task, time.Now()); err != nil {
			t.Fatalf("send zero inventory: %v", err)
		}
	}
	if !foundFarDate {
		t.Fatalf("zero inventory did not cover previously synchronized date %s", farStart.Format("2006-01-02"))
	}
	requestMu.Lock()
	if requestCount != 1+len(zeroTasks) {
		t.Fatalf("Ctrip request count=%d, want one positive followed by %d zero requests", requestCount, len(zeroTasks))
	}
	requestMu.Unlock()

	staleLockedAt := time.Now()
	stalePrice := result.Tasks[0]
	for _, task := range result.Tasks {
		if task.Kind == "price" {
			stalePrice = task
			break
		}
	}
	if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", stalePrice.ID).
		Updates(map[string]interface{}{"status": "processing", "locked_at": staleLockedAt}).Error; err != nil {
		t.Fatal(err)
	}
	stalePrice.Status = "processing"
	stalePrice.LockedAt = &staleLockedAt
	if err := syncService.processTask(context.Background(), &stalePrice, time.Now()); err != nil {
		t.Fatalf("reject stale positive task after suspension: %v", err)
	}
	requestMu.Lock()
	if requestCount != 1+len(zeroTasks) {
		t.Fatal("a stale positive task was sent after zero inventory")
	}
	requestMu.Unlock()

	if err := (&TenantService{}).SetSupplierBusinessTypeAudited(scenario.supplierID, "scenic", "active", "resume scenic sales", 9, "platform_admin"); err != nil {
		t.Fatalf("resume scenic business: %v", err)
	}
	if err := (&ChannelService{}).UpdateMapping(scenario.distributorID, channel.ID, mapping.ID, ChannelMappingUpdate{
		ExternalCode: "PLU-DISTRIBUTOR-NEW", ChannelSaleCents: 8000, ChannelCostCents: 7000, Status: "active",
	}); err != nil {
		t.Fatalf("reactivate Ctrip mapping: %v", err)
	}
	reactivatedSync, err := syncService.EnqueueMappingSync(scenario.distributorID, channel.ID, mapping.ID, farStart, farStart)
	if err != nil {
		t.Fatalf("enqueue inventory after reactivation: %v", err)
	}
	var reactivatedInventory model.CtripOutboundTask
	for _, task := range reactivatedSync.Tasks {
		if task.Kind == "price" {
			if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", task.ID).
				Updates(map[string]interface{}{"status": "failed", "next_attempt_at": nil}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if task.Kind == "inventory" {
			reactivatedInventory = task
		}
	}
	if reactivatedInventory.ID == 0 {
		t.Fatal("reactivated inventory task was not created")
	}
	reactivatedLockedAt := time.Now()
	if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", reactivatedInventory.ID).
		Updates(map[string]interface{}{"status": "processing", "locked_at": reactivatedLockedAt}).Error; err != nil {
		t.Fatal(err)
	}
	reactivatedInventory.Status = "processing"
	reactivatedInventory.LockedAt = &reactivatedLockedAt
	if err := syncService.processTask(context.Background(), &reactivatedInventory, time.Now()); err != nil {
		t.Fatalf("send inventory after reactivation: %v", err)
	}

	obsoleteShutdown := zeroTasks[0]
	shutdownLockedAt := time.Now()
	if err := model.DB.Model(&model.CtripOutboundTask{}).Where("id = ?", obsoleteShutdown.ID).
		Updates(map[string]interface{}{
			"status": "processing", "locked_at": shutdownLockedAt, "next_attempt_at": nil,
			"result_code": "", "result_message": "", "completed_at": nil,
		}).Error; err != nil {
		t.Fatal(err)
	}
	obsoleteShutdown.Status = "processing"
	obsoleteShutdown.LockedAt = &shutdownLockedAt
	if err := syncService.processTask(context.Background(), &obsoleteShutdown, time.Now()); err != nil {
		t.Fatalf("supersede obsolete shutdown inventory: %v", err)
	}
	if err := model.DB.First(&obsoleteShutdown, obsoleteShutdown.ID).Error; err != nil {
		t.Fatal(err)
	}
	if obsoleteShutdown.Status != "succeeded" || obsoleteShutdown.ResultCode != "SUPERSEDED" {
		t.Fatalf("obsolete shutdown task was not superseded: %+v", obsoleteShutdown)
	}
	requestMu.Lock()
	if requestCount != 2+len(zeroTasks) {
		t.Fatal("obsolete shutdown inventory was sent after business reactivation")
	}
	requestMu.Unlock()
}

func TestCtripScenicSuspensionSurvivesUnsupportedProductionSyncEndpoint(t *testing.T) {
	resetBusinessData(t)
	tenantID, channelID, mappingID := createCtripSyncFixture(t, "days")
	start := startOfDay(time.Now().AddDate(0, 0, 1))
	result, err := (&CtripSyncService{}).EnqueueMappingSync(tenantID, channelID, mappingID, start, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", channelID).Updates(map[string]interface{}{"environment": "production", "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TenantService{}).SetSupplierBusinessTypeAudited(tenantID, "scenic", "suspended", "production pause", 9, "platform_admin"); err != nil {
		t.Fatalf("unsupported production endpoint rolled back suspension: %v", err)
	}
	originalIDs := make([]uint, 0, len(result.Tasks))
	for _, task := range result.Tasks {
		originalIDs = append(originalIDs, task.ID)
	}
	var manualTask model.CtripOutboundTask
	if err := model.DB.Where("channel_product_mapping_id = ? AND id NOT IN ? AND kind = ?", mappingID, originalIDs, "inventory_shutdown").First(&manualTask).Error; err != nil {
		t.Fatal(err)
	}
	if manualTask.Status != "failed" || !strings.Contains(manualTask.LastError, "manual Ctrip inventory shutdown required") {
		t.Fatalf("manual shutdown fact=%+v", manualTask)
	}
}
