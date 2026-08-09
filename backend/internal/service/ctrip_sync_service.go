package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"ticket-backend/internal/ctrip"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ctripSandboxPriceEndpoint     = "https://ttdopen.ctrip.com/api/product/price.do"
	ctripSandboxInventoryEndpoint = "https://ttdopen.ctrip.com/api/product/stock.do"
	ctripSandboxOrderEndpoint     = "https://ttdopen.ctrip.com/api/order/notice.do"
	ctripProductionOrderEndpoint  = "https://ttdentry.ctrip.com/ttd-connect-orderentryapi/supplier/order/notice.do"
	ctripSyncMaxAttempts          = 5
	ctripSyncStaleAfter           = 5 * time.Minute
)

type CtripSyncService struct{}

type CtripSyncResult struct {
	Tasks []model.CtripOutboundTask `json:"tasks"`
}

func ctripSyncEndpoints(status string) (string, string, error) {
	switch status {
	case "sandbox":
		return ctripSandboxPriceEndpoint, ctripSandboxInventoryEndpoint, nil
	case "production":
		return "", "", errors.New("Ctrip production price and inventory endpoints have not been confirmed")
	default:
		return "", "", errors.New("ctrip channel is disabled")
	}
}

func ctripOrderNoticeEndpoint(environment string) (string, error) {
	switch environment {
	case "sandbox":
		return ctripSandboxOrderEndpoint, nil
	case "production":
		return ctripProductionOrderEndpoint, nil
	default:
		return "", errors.New("ctrip channel is disabled")
	}
}

func newCtripSequenceID(now time.Time) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return now.Format("20060102") + hex.EncodeToString(value[:]), nil
}

func (s *CtripSyncService) EnqueueMappingSync(tenantID, accountID, mappingID uint, start, end time.Time) (*CtripSyncResult, error) {
	start, end = startOfDay(start), startOfDay(end)
	if tenantID == 0 || accountID == 0 || mappingID == 0 || end.Before(start) {
		return nil, errors.New("channel, mapping and valid date range are required")
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days < 1 || days > 90 {
		return nil, errors.New("ctrip synchronization date range must be between 1 and 90 days")
	}
	var result CtripSyncResult
	err := model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", accountID, tenantID, "ctrip").First(&account).Error; err != nil {
			return errors.New("ctrip channel account not found")
		}
		if account.Status == "disabled" {
			return errors.New("ctrip channel is disabled")
		}
		priceEndpoint, stockEndpoint, err := ctripSyncEndpoints(account.Environment)
		if err != nil {
			return err
		}
		if account.AppID == "" || account.SecretCiphertext == "" || account.ProtocolConfigCiphertext == "" {
			return errors.New("ctrip protocol credentials are not configured")
		}
		var mapping model.ChannelProductMapping
		if err := tx.Where("id = ? AND channel_account_id = ? AND status = ?", mappingID, accountID, "active").First(&mapping).Error; err != nil {
			return errors.New("active ctrip product mapping not found")
		}
		if mapping.ChannelSaleCents <= 0 || mapping.ChannelCostCents < 0 || mapping.ChannelCostCents > mapping.ChannelSaleCents {
			return errors.New("configure the Ctrip sale and cost prices before synchronization")
		}
		var product model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", mapping.ProductID, tenantID).First(&product).Error; err != nil {
			return errors.New("mapped supplier product not found")
		}
		if product.SourceProductID != 0 || product.FulfillmentTenantID != 0 && product.FulfillmentTenantID != tenantID {
			return errors.New("only supplier-owned products can be synchronized to Ctrip")
		}
		if product.Status != "online" {
			return errors.New("offline products cannot be synchronized to Ctrip")
		}
		if product.ValidityType == "date" {
			if product.ValidityStartDate != nil && start.Before(startOfDay(*product.ValidityStartDate)) {
				return errors.New("synchronization starts before the product validity period")
			}
			if product.ValidityEndDate != nil && end.After(startOfDay(*product.ValidityEndDate)) {
				return errors.New("synchronization ends after the product validity period")
			}
		}

		sequence := fmt.Sprintf("%d%d%d", time.Now().UnixNano(), account.ID, mapping.ID)
		prices := make([]ctrip.Price, 0, 90)
		inventories := make([]ctrip.Inventory, 0, 90)
		remainingByDate, err := ctripRemainingInventory(tx, &product, start, end)
		if err != nil {
			return err
		}
		dateType := "DATE_REQUIRED"
		if product.ValidityType == "days" {
			if product.StockType == "daily" {
				return errors.New("Ctrip non-date products require unlimited or total inventory")
			}
			dateType = "DATE_NOT_REQUIRED"
			prices = append(prices, ctrip.Price{SalePrice: float64(mapping.ChannelSaleCents) / 100, CostPrice: float64(mapping.ChannelCostCents) / 100})
			inventories = append(inventories, ctrip.Inventory{Quantity: remainingByDate[start.Format("2006-01-02")]})
		} else {
			for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
				date := day.Format("2006-01-02")
				prices = append(prices, ctrip.Price{Date: date, SalePrice: float64(mapping.ChannelSaleCents) / 100, CostPrice: float64(mapping.ChannelCostCents) / 100})
				inventories = append(inventories, ctrip.Inventory{Date: date, Quantity: remainingByDate[date]})
			}
		}
		payloads := []struct {
			kind, endpoint string
			value          interface{}
		}{
			{"price", priceEndpoint, ctrip.PriceRequest{SequenceID: sequence + "P", SupplierOptionID: mapping.ExternalCode, DateType: dateType, Prices: prices}},
			{"inventory", stockEndpoint, ctrip.InventoryRequest{SequenceID: sequence + "S", SupplierOptionID: mapping.ExternalCode, DateType: dateType, Inventories: inventories}},
		}
		for _, entry := range payloads {
			payload, err := json.Marshal(entry.value)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(payload)
			hash := hex.EncodeToString(digest[:])
			var task model.CtripOutboundTask
			err = tx.Where("channel_account_id = ? AND kind = ? AND payload_hash = ?", account.ID, entry.kind, hash).First(&task).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				now := time.Now()
				task = model.CtripOutboundTask{TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID, Kind: entry.kind, PayloadHash: hash, Endpoint: entry.endpoint, PayloadJSON: string(payload), Status: "pending", NextAttemptAt: &now}
				if err := tx.Create(&task).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else if task.Status == "failed" {
				now := time.Now()
				if err := tx.Model(&task).Updates(map[string]interface{}{"status": "pending", "attempt_count": 0, "next_attempt_at": now, "locked_at": nil, "last_error": ""}).Error; err != nil {
					return err
				}
				task.Status, task.AttemptCount, task.NextAttemptAt, task.LockedAt, task.LastError = "pending", 0, &now, nil, ""
			}
			result.Tasks = append(result.Tasks, task)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func ctripRemainingInventory(tx *gorm.DB, product *model.Product, start, end time.Time) (map[string]int, error) {
	result := make(map[string]int)
	if product.StockType == "unlimited" {
		for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
			result[day.Format("2006-01-02")] = 999999
		}
		return result, nil
	}
	if product.StockType == "total" {
		remaining := product.DailyStock
		if remaining < 0 {
			remaining = 0
		}
		for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
			result[day.Format("2006-01-02")] = remaining
		}
		return result, nil
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		result[day.Format("2006-01-02")] = maxInt(product.DailyStock, 0)
	}
	var rows []model.ProductInventory
	if err := tx.Where("tenant_id = ? AND product_id = ? AND stock_date BETWEEN ? AND ?", product.TenantID, product.ID, start, end).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, row := range rows {
		date := row.StockDate.Format("2006-01-02")
		if !seen[date] {
			result[date] = 0
			seen[date] = true
		}
		result[date] += maxInt(row.Capacity-row.Sold, 0)
	}
	return result, nil
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func enqueueCtripConsumedNoticeTx(tx *gorm.DB, tenantID, orderID uint) (*model.CtripOutboundTask, error) {
	var link model.CtripOrderLink
	if err := tx.Preload("Items").Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var account model.ChannelAccount
	if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", link.ChannelAccountID, tenantID, "ctrip").First(&account).Error; err != nil {
		return nil, err
	}
	endpoint, err := ctripOrderNoticeEndpoint(account.Environment)
	if err != nil {
		return nil, err
	}
	items := make([]ctrip.ConsumedItem, 0, len(link.Items))
	var mappingID uint
	for _, linkedItem := range link.Items {
		var orderItem model.OrderItem
		if err := tx.Preload("Tickets", func(db *gorm.DB) *gorm.DB { return db.Order("id") }).Where("id = ? AND order_id = ?", linkedItem.OrderItemID, orderID).First(&orderItem).Error; err != nil {
			return nil, err
		}
		usedTickets := make([]model.Ticket, 0, len(orderItem.Tickets))
		for _, ticket := range orderItem.Tickets {
			if ticket.Status == "used" {
				usedTickets = append(usedTickets, ticket)
			}
		}
		if len(usedTickets) == 0 {
			continue
		}
		useQuantity := len(usedTickets)
		if len(orderItem.Tickets) == 1 && orderItem.Tickets[0].CodeMode == "order" {
			useQuantity = orderItem.Quantity
		}
		item := ctrip.ConsumedItem{ItemID: linkedItem.ExternalItemID, Quantity: orderItem.Quantity, UseQuantity: useQuantity}
		if orderItem.UseDate != nil {
			item.UseStartDate = orderItem.UseDate.Format("2006-01-02")
			item.UseEndDate = item.UseStartDate
		}
		for _, ticket := range usedTickets {
			item.Vouchers = append(item.Vouchers, ctrip.ConsumedVoucher{VoucherID: ticket.TicketCode})
		}
		var passengerIDs []string
		if linkedItem.PassengerIDsJSON != "" {
			if err := json.Unmarshal([]byte(linkedItem.PassengerIDsJSON), &passengerIDs); err != nil {
				return nil, errors.New("ctrip passenger snapshot is invalid")
			}
		}
		if useQuantity < len(passengerIDs) {
			passengerIDs = passengerIDs[:useQuantity]
		}
		for _, passengerID := range passengerIDs {
			item.Passengers = append(item.Passengers, ctrip.ConsumedPassenger{PassengerID: passengerID})
		}
		items = append(items, item)
		if mappingID == 0 {
			var mapping model.ChannelProductMapping
			if err := tx.Where("channel_account_id = ? AND external_code = ?", account.ID, linkedItem.PLU).First(&mapping).Error; err == nil {
				mappingID = mapping.ID
			}
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	sequenceID, err := newCtripSequenceID(time.Now())
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(ctrip.ConsumedNoticeRequest{SequenceID: sequenceID, OTAOrderID: link.OTAOrderID, SupplierOrderID: link.SupplierOrderID, Items: items})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	now := time.Now()
	task := model.CtripOutboundTask{
		TenantID: tenantID, ChannelAccountID: account.ID, ChannelProductMappingID: mappingID,
		Kind: "consumed", PayloadHash: hex.EncodeToString(digest[:]), Endpoint: endpoint,
		PayloadJSON: string(payload), Status: "pending", NextAttemptAt: &now,
	}
	if err := tx.Create(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *CtripSyncService) SimulateSandboxConsumption(tenantID, accountID uint, supplierOrderID string, actorUserID uint, actorRole string) (*model.CtripOutboundTask, error) {
	supplierOrderID = strings.TrimSpace(supplierOrderID)
	if tenantID == 0 || accountID == 0 || supplierOrderID == "" {
		return nil, errors.New("channel and supplier order number are required")
	}
	var result *model.CtripOutboundTask
	err := model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", accountID, tenantID, "ctrip").First(&account).Error; err != nil {
			return errors.New("ctrip channel account not found")
		}
		if account.Environment != "sandbox" || account.Status != "sandbox" {
			return errors.New("simulated consumption is only available for an enabled Ctrip sandbox channel")
		}
		var link model.CtripOrderLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_account_id = ? AND supplier_order_id = ?", tenantID, accountID, supplierOrderID).First(&link).Error; err != nil {
			return errors.New("Ctrip sandbox order not found")
		}
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ? AND environment = ?", link.OrderID, tenantID, accountID, "sandbox").First(&order).Error; err != nil {
			return errors.New("Ctrip sandbox order not found")
		}
		if order.Status != "paid" {
			return errors.New("only a paid Ctrip sandbox order can be consumed")
		}
		if err := tx.Model(&model.Ticket{}).Where("order_item_id IN (SELECT id FROM order_items WHERE order_id = ?) AND status IN ?", order.ID, []string{"unused", "active", "issued"}).Updates(map[string]interface{}{"status": "used"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.TicketEntitlement{}).
			Where("ticket_id IN (SELECT tickets.id FROM tickets JOIN order_items ON order_items.id = tickets.order_item_id WHERE order_items.order_id = ?) AND status IN ?", order.ID, []string{"active", "issued"}).
			Update("status", "used").Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Update("status", "completed").Error; err != nil {
			return err
		}
		if err := updateFulfillmentOrdersTx(tx, order.ID, "fulfilled"); err != nil {
			return err
		}
		var err error
		result, err = enqueueCtripConsumedNoticeTx(tx, tenantID, order.ID)
		if err != nil {
			return err
		}
		if result == nil {
			return errors.New("Ctrip consumed notification was not created")
		}
		return recordAuditTx(tx, actorUserID, tenantID, actorRole, "tenant", "ctrip.sandbox.consume", "order", order.ID, "Ctrip sandbox traversal test", "", fmt.Sprintf(`{"supplier_order_id":%q,"task_id":%d}`, supplierOrderID, result.ID))
	})
	return result, err
}

func (s *CtripSyncService) ProcessTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	processed := 0
	for processed < limit {
		task, err := claimCtripOutboundTask(now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		processed++
		if err := s.processTask(ctx, task, now); err != nil {
			if deferErr := deferCtripOutboundTask(task, now, err); deferErr != nil {
				return processed, deferErr
			}
		}
	}
	return processed, nil
}

func claimCtripOutboundTask(now time.Time) (*model.CtripOutboundTask, error) {
	var task model.CtripOutboundTask
	err := model.Write(func(tx *gorm.DB) error {
		staleBefore := now.Add(-ctripSyncStaleAfter)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? AND next_attempt_at <= ?) OR (status = ? AND locked_at <= ?)", "pending", now, "processing", staleBefore).
			Order("next_attempt_at, id").First(&task).Error; err != nil {
			return err
		}
		return tx.Model(&task).Updates(map[string]interface{}{"status": "processing", "attempt_count": gorm.Expr("attempt_count + 1"), "locked_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	task.Status, task.AttemptCount, task.LockedAt = "processing", task.AttemptCount+1, &now
	return &task, nil
}

func (s *CtripSyncService) processTask(ctx context.Context, task *model.CtripOutboundTask, now time.Time) error {
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ?", task.ChannelAccountID, task.TenantID, "ctrip").First(&account).Error; err != nil {
		return errors.New("ctrip channel account is no longer available")
	}
	if account.Status == "disabled" {
		return errors.New("ctrip channel is disabled")
	}
	var expectedEndpoint string
	switch task.Kind {
	case "price", "inventory":
		priceEndpoint, stockEndpoint, endpointErr := ctripSyncEndpoints(account.Environment)
		if endpointErr != nil {
			return endpointErr
		}
		expectedEndpoint = stockEndpoint
		if task.Kind == "price" {
			expectedEndpoint = priceEndpoint
		}
	case "consumed":
		var endpointErr error
		expectedEndpoint, endpointErr = ctripOrderNoticeEndpoint(account.Environment)
		if endpointErr != nil {
			return endpointErr
		}
	default:
		return errors.New("unsupported ctrip outbound task kind")
	}
	if task.Endpoint != expectedEndpoint {
		return errors.New("ctrip task environment no longer matches the channel environment; create a new synchronization")
	}
	signKey, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt ctrip sign key: %w", err)
	}
	configText, err := utils.DecryptAES(account.ProtocolConfigCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt ctrip protocol config: %w", err)
	}
	var config CtripChannelConfig
	if err := json.Unmarshal([]byte(configText), &config); err != nil {
		return fmt.Errorf("decode ctrip protocol config: %w", err)
	}
	client := ctrip.Client{AccountID: account.AppID, SignKey: signKey, AESKey: config.AESKey, AESIV: config.AESIV}
	var response *ctrip.Response
	switch task.Kind {
	case "price":
		var payload ctrip.PriceRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return err
		}
		response, err = client.SyncPrice(ctx, task.Endpoint, payload)
	case "inventory":
		var payload ctrip.InventoryRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return err
		}
		response, err = client.SyncInventory(ctx, task.Endpoint, payload)
	case "consumed":
		var payload ctrip.ConsumedNoticeRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return err
		}
		response, err = client.NotifyConsumed(ctx, task.Endpoint, payload)
	default:
		return errors.New("unsupported ctrip outbound task kind")
	}
	if err != nil {
		return err
	}
	if response.Code != "0000" {
		return fmt.Errorf("ctrip rejected outbound request (%s): %s", response.Code, response.Message)
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.CtripOutboundTask{}).Where("id = ? AND locked_at = ?", task.ID, task.LockedAt).Updates(map[string]interface{}{
			"status": "succeeded", "result_code": response.Code, "result_message": response.Message, "last_error": "", "next_attempt_at": nil, "locked_at": nil, "completed_at": now,
		}).Error
	})
}

func deferCtripOutboundTask(task *model.CtripOutboundTask, now time.Time, cause error) error {
	status := "pending"
	var next interface{} = now.Add(time.Duration(math.Pow(2, float64(minInt(task.AttemptCount, 10)))) * time.Second)
	if task.AttemptCount >= ctripSyncMaxAttempts {
		status, next = "failed", nil
	}
	message := strings.TrimSpace(cause.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.CtripOutboundTask{}).Where("id = ? AND locked_at = ?", task.ID, task.LockedAt).Updates(map[string]interface{}{"status": status, "next_attempt_at": next, "locked_at": nil, "last_error": message}).Error
	})
}

func (s *CtripSyncService) ListTasks(tenantID, accountID uint, limit int) ([]model.CtripOutboundTask, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	var rows []model.CtripOutboundTask
	err := model.DB.Where("tenant_id = ? AND channel_account_id = ?", tenantID, accountID).Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
