package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/config"
	"ticket-backend/internal/ctrip"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func TestCtripSandboxConsumptionCreatesReliableNoticeAndBlocksCancellation(t *testing.T) {
	resetBusinessData(t)
	previousKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.GlobalConfig.Security.EncryptionKey = previousKey })

	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	channel := model.ChannelAccount{Code: "ctrip-consumed-sandbox", Type: "ctrip", Status: "sandbox", PermissionsJSON: `["orders:create","orders:query","orders:cancel"]`, RateLimitPerMin: 600}
	const accountID = "ctrip-consumed-account"
	const signKey = "ctrip-consumed-sign"
	const aesKey = "1234567890abcdef"
	const aesIV = "abcdef1234567890"
	channelService := &ChannelService{}
	if err := channelService.CreateCtrip(tenantID, &channel, accountID, signKey, aesKey, aesIV); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: channel.ID, ProductID: productID, ExternalCode: "PLU-CONSUMED", ChannelSaleCents: 201, ChannelCostCents: 200}
	if err := channelService.AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	protocol := &CtripProtocolService{OrderService: OrderService{}}
	visitDate := time.Now().Format("2006-01-02")
	createBody := map[string]interface{}{
		"sequenceId": "20260809-consumed-create", "otaOrderId": "CTRIP-CONSUMED-001",
		"contacts": []map[string]string{{"name": "测试游客", "mobile": "13800138000"}},
		"items": []map[string]interface{}{{
			"PLU": "PLU-CONSUMED", "quantity": 1, "price": 2.01, "priceCurrency": "CNY", "salePrice": 2.01, "salePriceCurrency": "CNY", "cost": 2.00, "costCurrency": "CNY",
			"useStartDate": visitDate, "useEndDate": visitDate,
			"passengers": []map[string]string{{"passengerId": "PASSENGER-CONSUMED", "name": "测试游客", "mobile": "13800138000"}},
		}},
	}
	createResponse, err := protocol.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CreatePreOrder", createBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	created := decodeCtripTestResponse(t, createResponse, aesKey, aesIV)
	if created.Code != "0000" {
		t.Fatalf("create=%+v", created)
	}
	supplierOrderID := created.Body["supplierOrderId"].(string)
	payBody := map[string]interface{}{
		"sequenceId": "20260809-consumed-create", "otaOrderId": "CTRIP-CONSUMED-001", "supplierOrderId": supplierOrderID,
		"confirmType": 2, "orderLastConfirmTime": time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"),
		"items": []map[string]string{{"itemId": "ITEM-CONSUMED", "PLU": "PLU-CONSUMED"}},
	}
	payResponse, err := protocol.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "PayPreOrder", payBody), "127.0.0.1")
	if err != nil || decodeCtripTestResponse(t, payResponse, aesKey, aesIV).Code != "0000" {
		t.Fatalf("pay=%s err=%v", payResponse, err)
	}

	task, err := (&CtripSyncService{}).SimulateSandboxConsumption(tenantID, channel.ID, supplierOrderID, 42, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.Kind != "consumed" || task.Endpoint != ctripSandboxOrderEndpoint || task.Status != "pending" {
		t.Fatalf("task=%+v", task)
	}
	var payload ctrip.ConsumedNoticeRequest
	if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OTAOrderID != "CTRIP-CONSUMED-001" || payload.SupplierOrderID != supplierOrderID || len(payload.Items) != 1 || payload.Items[0].ItemID != "ITEM-CONSUMED" || payload.Items[0].UseQuantity != 1 || len(payload.Items[0].Vouchers) != 1 || payload.Items[0].Vouchers[0].VoucherID == "" {
		t.Fatalf("payload=%+v", payload)
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("order_no = ?", supplierOrderID).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "completed" || len(order.Items) != 1 || len(order.Items[0].Tickets) != 1 || order.Items[0].Tickets[0].Status != "used" {
		t.Fatalf("consumed order=%+v", order)
	}
	cancelBody := map[string]interface{}{
		"sequenceId": "20260809-consumed-cancel", "otaOrderId": "CTRIP-CONSUMED-001", "supplierOrderId": supplierOrderID, "confirmType": 2,
		"items": []map[string]interface{}{{"itemId": "ITEM-CONSUMED", "PLU": "PLU-CONSUMED", "cancelType": 0, "quantity": 1}},
	}
	cancelResponse, err := protocol.Handle(buildCtripTestRequest(t, accountID, signKey, aesKey, aesIV, "CancelOrder", cancelBody), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if result := decodeCtripTestResponse(t, cancelResponse, aesKey, aesIV); result.Code != "2002" {
		t.Fatalf("used order cancellation=%+v", result)
	}
	var auditCount int64
	if err := model.DB.Model(&model.AuditLog{}).Where("tenant_id = ? AND actor_user_id = ? AND action = ?", tenantID, 42, "ctrip.sandbox.consume").Count(&auditCount).Error; err != nil || auditCount != 1 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}

	if err := model.DB.Model(&model.ChannelAccount{}).Where("id = ?", channel.ID).Updates(map[string]interface{}{"status": "active", "environment": "production"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&CtripSyncService{}).SimulateSandboxConsumption(tenantID, channel.ID, supplierOrderID, 42, "admin"); err == nil || !strings.Contains(err.Error(), "only available") {
		t.Fatalf("production simulation was not rejected: %v", err)
	}
}

func TestTicketVerificationEnqueuesCtripConsumedNotice(t *testing.T) {
	resetBusinessData(t)
	previousKey := config.GlobalConfig.Security.EncryptionKey
	config.GlobalConfig.Security.EncryptionKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.GlobalConfig.Security.EncryptionKey = previousKey })

	tenantID, productID := seedSellableProduct(t, "unlimited", 0)
	channel := model.ChannelAccount{Code: "ctrip-consumed-production", Type: "ctrip", Status: "active", PermissionsJSON: `["orders:create","orders:query","orders:cancel"]`, RateLimitPerMin: 600}
	if err := (&ChannelService{}).CreateCtrip(tenantID, &channel, "production-account", "production-sign", "1234567890abcdef", "abcdef1234567890"); err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: channel.ID, ProductID: productID, ExternalCode: "PLU-PRODUCTION", ChannelSaleCents: 9950, ChannelCostCents: 6000}
	if err := (&ChannelService{}).AddMapping(tenantID, &mapping); err != nil {
		t.Fatal(err)
	}
	useDate := startOfDay(time.Now())
	externalNo := "CTRIP-PRODUCTION-001"
	order := model.Order{TenantID: tenantID, Channel: fmt.Sprintf("ctrip:%d", channel.ID), ChannelAccountID: channel.ID, ExternalNo: &externalNo, Items: []model.OrderItem{{ProductID: productID, Quantity: 1, UseDate: &useDate}}}
	if err := (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(order.OrderNo, tenantID); err != nil {
		t.Fatal(err)
	}
	passengers, _ := json.Marshal([]string{"PASSENGER-PRODUCTION"})
	link := model.CtripOrderLink{
		TenantID: tenantID, ChannelAccountID: channel.ID, OrderID: order.ID, OTAOrderID: externalNo, SupplierOrderID: order.OrderNo, State: "paid",
		Items: []model.CtripOrderItem{{OrderItemID: order.Items[0].ID, ExternalItemID: "ITEM-PRODUCTION", PLU: mapping.ExternalCode, PassengerIDsJSON: string(passengers)}},
	}
	if err := model.Write(func(tx *gorm.DB) error { return tx.Create(&link).Error }); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	var checkpoint model.CheckPoint
	var device model.Device
	if err := model.DB.Where("order_item_id = ?", order.Items[0].ID).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ?", tenantID).First(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Where("tenant_id = ? AND check_point_id = ?", tenantID, checkpoint.ID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&TicketService{}).Verify(ticket.TicketCode, checkpoint.ID, device.ID, tenantID); err != nil {
		t.Fatal(err)
	}
	var task model.CtripOutboundTask
	if err := model.DB.Where("channel_account_id = ? AND kind = ?", channel.ID, "consumed").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Endpoint != ctripProductionOrderEndpoint || task.Status != "pending" {
		t.Fatalf("task=%+v", task)
	}
}
