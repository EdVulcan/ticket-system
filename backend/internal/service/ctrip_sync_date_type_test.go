package service

import (
	"encoding/json"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/ctrip"
	"ticket-backend/internal/model"
	"time"
)

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
