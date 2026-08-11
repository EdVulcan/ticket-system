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
	"net/http"
	"sort"
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
	ctripProductRequestTimeout    = 5 * time.Second
)

type CtripSyncService struct {
	HTTP *http.Client
}

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
		var listing model.Product
		if err := tx.Where("id = ? AND tenant_id = ?", mapping.ProductID, tenantID).First(&listing).Error; err != nil {
			return errors.New("mapped supplier product not found")
		}
		if listing.Status != "online" {
			return errors.New("offline products cannot be synchronized to Ctrip")
		}
		product, _, err := resolveFulfillmentProduct(tx, &listing, tenantID, "ota")
		if err != nil {
			return err
		}
		if err := requireActiveScenicSupplier(tx, product.TenantID); err != nil {
			return errors.New("scenic supplier business is unavailable")
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
		remainingByDate, err := ctripRemainingInventory(tx, product, start, end)
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

type ctripSuspendedMapping struct {
	MappingID       uint
	AccountID       uint
	AccountTenantID uint
	AccountStatus   string
	ExternalCode    string
	Environment     string
	ValidityType    string
}

// enqueueCtripScenicSuspensionTasksTx closes every active Ctrip mapping whose
// inventory is fulfilled by the suspended scenic supplier. It runs in the
// same transaction as the business-type transition so a successful pause
// always leaves a durable zero-inventory task behind.
func enqueueCtripScenicSuspensionTasksTx(tx *gorm.DB, fulfillmentTenantID uint, now time.Time) error {
	if fulfillmentTenantID == 0 {
		return errors.New("fulfillment supplier is required")
	}
	var mappings []ctripSuspendedMapping
	actualTenantSQL := "COALESCE(NULLIF(product.fulfillment_tenant_id, 0), NULLIF(product.source_tenant_id, 0), product.tenant_id)"
	actualProductSQL := "COALESCE(NULLIF(product.fulfillment_product_id, 0), NULLIF(product.source_product_id, 0), product.id)"
	if err := tx.Table("channel_product_mappings AS mapping").
		Select(`mapping.id AS mapping_id, account.id AS account_id, account.tenant_id AS account_tenant_id,
			account.status AS account_status, mapping.external_code, account.environment,
			COALESCE(fulfillment.validity_type, product.validity_type) AS validity_type`).
		Joins("JOIN channel_accounts account ON account.id = mapping.channel_account_id AND account.type = 'ctrip' AND account.deleted_at IS NULL").
		Joins("JOIN products product ON product.id = mapping.product_id AND product.tenant_id = account.tenant_id").
		Joins("LEFT JOIN products fulfillment ON fulfillment.id = "+actualProductSQL+" AND fulfillment.tenant_id = "+actualTenantSQL).
		Where("mapping.status = ? AND mapping.deleted_at IS NULL", "active").
		Where(actualTenantSQL+" = ?", fulfillmentTenantID).
		Scan(&mappings).Error; err != nil {
		return err
	}
	for _, mapping := range mappings {
		var lockedMapping model.ChannelProductMapping
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND channel_account_id = ? AND status = ?", mapping.MappingID, mapping.AccountID, "active").
			First(&lockedMapping).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		mapping.ExternalCode = lockedMapping.ExternalCode
		var historical []model.CtripOutboundTask
		if err := tx.Select("payload_json").
			Where("channel_product_mapping_id = ? AND kind IN ?", mapping.MappingID, []string{"inventory", "inventory_shutdown"}).
			Find(&historical).Error; err != nil {
			return err
		}
		if err := tx.Model(&lockedMapping).Update("status", "disabled").Error; err != nil {
			return err
		}
		message := "superseded by scenic supplier suspension"
		if err := tx.Model(&model.CtripOutboundTask{}).
			Where("channel_product_mapping_id = ? AND kind IN ? AND status = ?", mapping.MappingID, []string{"price", "inventory", "inventory_shutdown"}, "pending").
			Updates(map[string]interface{}{
				"status": "failed", "next_attempt_at": nil, "locked_at": nil,
				"last_error": message, "completed_at": now,
			}).Error; err != nil {
			return err
		}

		requests, err := ctripZeroInventoryRequests(mapping.ExternalCode, mapping.ValidityType, historical, now)
		if err != nil {
			return err
		}
		_, inventoryEndpoint, endpointErr := ctripSyncEndpoints(mapping.Environment)
		for _, request := range requests {
			payload, err := json.Marshal(request)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(payload)
			nextAttemptAt := now
			task := model.CtripOutboundTask{
				TenantID: mapping.AccountTenantID, ChannelAccountID: mapping.AccountID,
				ChannelProductMappingID: mapping.MappingID, Kind: "inventory_shutdown",
				PayloadHash: hex.EncodeToString(digest[:]), Endpoint: inventoryEndpoint,
				PayloadJSON: string(payload), Status: "pending", NextAttemptAt: &nextAttemptAt,
			}
			if endpointErr != nil || mapping.AccountStatus == "disabled" {
				reason := "manual Ctrip inventory shutdown required"
				if endpointErr != nil {
					reason += ": " + endpointErr.Error()
				} else {
					reason += ": channel account is disabled"
				}
				task.Status, task.NextAttemptAt, task.LastError, task.CompletedAt = "failed", nil, reason, &now
			}
			if err := tx.Create(&task).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func ctripZeroInventoryRequests(externalCode, validityType string, historical []model.CtripOutboundTask, now time.Time) ([]ctrip.InventoryRequest, error) {
	type coverage struct {
		nonDate bool
		dates   map[string]struct{}
	}
	today := startOfDay(now)
	byExternalCode := make(map[string]*coverage)
	ensureCoverage := func(code string) *coverage {
		code = strings.TrimSpace(code)
		if code == "" {
			code = strings.TrimSpace(externalCode)
		}
		if byExternalCode[code] == nil {
			byExternalCode[code] = &coverage{dates: make(map[string]struct{})}
		}
		return byExternalCode[code]
	}
	current := ensureCoverage(externalCode)
	current.nonDate = validityType == "days"
	for _, task := range historical {
		var payload ctrip.InventoryRequest
		if json.Unmarshal([]byte(task.PayloadJSON), &payload) != nil {
			continue
		}
		target := ensureCoverage(payload.SupplierOptionID)
		if payload.DateType == "DATE_NOT_REQUIRED" {
			target.nonDate = true
		}
		for _, inventory := range payload.Inventories {
			date, err := time.ParseInLocation("2006-01-02", inventory.Date, today.Location())
			if err == nil && !date.Before(today) {
				target.dates[inventory.Date] = struct{}{}
			}
		}
	}
	if validityType != "days" {
		for offset := 0; offset < 90; offset++ {
			current.dates[today.AddDate(0, 0, offset).Format("2006-01-02")] = struct{}{}
		}
	}
	codes := make([]string, 0, len(byExternalCode))
	for code := range byExternalCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	requests := make([]ctrip.InventoryRequest, 0)
	for _, code := range codes {
		covered := byExternalCode[code]
		if covered.nonDate {
			sequence, err := newCtripSequenceID(now)
			if err != nil {
				return nil, err
			}
			requests = append(requests, ctrip.InventoryRequest{
				SequenceID: sequence + "Z", SupplierOptionID: code,
				DateType: "DATE_NOT_REQUIRED", Inventories: []ctrip.Inventory{{Quantity: 0}},
			})
		}
		orderedDates := make([]string, 0, len(covered.dates))
		for date := range covered.dates {
			orderedDates = append(orderedDates, date)
		}
		sort.Strings(orderedDates)
		for start := 0; start < len(orderedDates); start += 90 {
			end := minInt(start+90, len(orderedDates))
			inventories := make([]ctrip.Inventory, 0, end-start)
			for _, date := range orderedDates[start:end] {
				inventories = append(inventories, ctrip.Inventory{Date: date, Quantity: 0})
			}
			sequence, err := newCtripSequenceID(now)
			if err != nil {
				return nil, err
			}
			requests = append(requests, ctrip.InventoryRequest{
				SequenceID: sequence + "Z", SupplierOptionID: code,
				DateType: "DATE_REQUIRED", Inventories: inventories,
			})
		}
	}
	return requests, nil
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
	if task == nil || task.LockedAt == nil {
		return errors.New("ctrip task ownership is invalid")
	}
	if task.Kind == "price" || task.Kind == "inventory" || task.Kind == "inventory_shutdown" {
		return s.processProductTask(ctx, task, now)
	}
	if task.Kind != "consumed" {
		return errors.New("unsupported ctrip outbound task kind")
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ?", task.ChannelAccountID, task.TenantID, "ctrip").First(&account).Error; err != nil {
		return errors.New("ctrip channel account is no longer available")
	}
	if account.Status == "disabled" {
		return errors.New("ctrip channel is disabled")
	}
	expectedEndpoint, err := ctripTaskExpectedEndpoint(&account, task.Kind)
	if err != nil {
		return err
	}
	if task.Endpoint != expectedEndpoint {
		return errors.New("ctrip task environment no longer matches the channel environment; create a new synchronization")
	}
	client, err := s.ctripClient(&account)
	if err != nil {
		return err
	}
	response, err := sendCtripTask(ctx, client, task)
	if err != nil {
		return err
	}
	if response.Code != "0000" {
		return fmt.Errorf("ctrip rejected outbound request (%s): %s", response.Code, response.Message)
	}
	return model.Write(func(tx *gorm.DB) error {
		return completeCtripOutboundTaskTx(tx, task, response, now)
	})
}

func (s *CtripSyncService) processProductTask(ctx context.Context, claimed *model.CtripOutboundTask, now time.Time) error {
	return model.Write(func(tx *gorm.DB) error {
		var mapping model.ChannelProductMapping
		if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND channel_account_id = ?", claimed.ChannelProductMappingID, claimed.ChannelAccountID).
			First(&mapping).Error; err != nil {
			return errors.New("ctrip product mapping is unavailable")
		}
		var task model.CtripOutboundTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND status = ? AND locked_at = ?", claimed.ID, "processing", *claimed.LockedAt).
			First(&task).Error; err != nil {
			return errors.New("ctrip product task ownership was lost")
		}
		if task.Kind != "price" && task.Kind != "inventory" && task.Kind != "inventory_shutdown" {
			return errors.New("unsupported ctrip outbound task kind")
		}
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", task.ChannelAccountID, task.TenantID, "ctrip").First(&account).Error; err != nil {
			return errors.New("ctrip channel account is no longer available")
		}
		if account.Status == "disabled" {
			return errors.New("ctrip channel is disabled")
		}
		expectedEndpoint, err := ctripTaskExpectedEndpoint(&account, task.Kind)
		if err != nil {
			return err
		}
		if task.Endpoint != expectedEndpoint {
			return errors.New("ctrip task environment no longer matches the channel environment; create a new synchronization")
		}
		shutdownInventory := task.Kind == "inventory_shutdown"
		if shutdownInventory {
			if !ctripInventoryPayloadIsZero(task.PayloadJSON) {
				return failUnavailableCtripProductTaskTx(tx, &task, now, errors.New("Ctrip shutdown inventory task must contain only zero quantities"))
			}
			if mapping.Status != "disabled" {
				return supersedeCtripOutboundTaskTx(tx, &task, now, "mapping was reactivated before shutdown inventory was sent")
			}
		} else {
			if err := ctripOutboundProductAvailableTx(tx, &mapping, &task); err != nil {
				return failUnavailableCtripProductTaskTx(tx, &task, now, err)
			}
		}
		client, err := s.ctripClient(&account)
		if err != nil {
			return err
		}
		requestCtx, cancel := context.WithTimeout(tx.Statement.Context, ctripProductRequestTimeout)
		stopOuterCancel := context.AfterFunc(ctx, cancel)
		response, err := sendCtripTask(requestCtx, client, &task)
		stopOuterCancel()
		cancel()
		if err != nil {
			return err
		}
		if response.Code != "0000" {
			return fmt.Errorf("ctrip rejected outbound request (%s): %s", response.Code, response.Message)
		}
		return completeCtripOutboundTaskTx(tx, &task, response, now)
	})
}

func ctripTaskExpectedEndpoint(account *model.ChannelAccount, kind string) (string, error) {
	if account == nil {
		return "", errors.New("ctrip channel account is unavailable")
	}
	if kind == "consumed" {
		return ctripOrderNoticeEndpoint(account.Environment)
	}
	priceEndpoint, stockEndpoint, err := ctripSyncEndpoints(account.Environment)
	if err != nil {
		return "", err
	}
	if kind == "price" {
		return priceEndpoint, nil
	}
	if kind == "inventory" || kind == "inventory_shutdown" {
		return stockEndpoint, nil
	}
	return "", errors.New("unsupported ctrip outbound task kind")
}

func (s *CtripSyncService) ctripClient(account *model.ChannelAccount) (ctrip.Client, error) {
	signKey, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return ctrip.Client{}, fmt.Errorf("decrypt ctrip sign key: %w", err)
	}
	configText, err := utils.DecryptAES(account.ProtocolConfigCiphertext)
	if err != nil {
		return ctrip.Client{}, fmt.Errorf("decrypt ctrip protocol config: %w", err)
	}
	var config CtripChannelConfig
	if err := json.Unmarshal([]byte(configText), &config); err != nil {
		return ctrip.Client{}, fmt.Errorf("decode ctrip protocol config: %w", err)
	}
	return ctrip.Client{AccountID: account.AppID, SignKey: signKey, AESKey: config.AESKey, AESIV: config.AESIV, HTTP: s.HTTP}, nil
}

func sendCtripTask(ctx context.Context, client ctrip.Client, task *model.CtripOutboundTask) (*ctrip.Response, error) {
	var response *ctrip.Response
	var err error
	switch task.Kind {
	case "price":
		var payload ctrip.PriceRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return nil, err
		}
		response, err = client.SyncPrice(ctx, task.Endpoint, payload)
	case "inventory", "inventory_shutdown":
		var payload ctrip.InventoryRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return nil, err
		}
		response, err = client.SyncInventory(ctx, task.Endpoint, payload)
	case "consumed":
		var payload ctrip.ConsumedNoticeRequest
		if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
			return nil, err
		}
		response, err = client.NotifyConsumed(ctx, task.Endpoint, payload)
	default:
		return nil, errors.New("unsupported ctrip outbound task kind")
	}
	return response, err
}

func ctripOutboundProductAvailableTx(tx *gorm.DB, mapping *model.ChannelProductMapping, task *model.CtripOutboundTask) error {
	if mapping == nil || task == nil || mapping.ID == 0 || mapping.Status != "active" {
		return errors.New("ctrip product mapping is unavailable")
	}
	var product model.Product
	if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", mapping.ProductID, task.TenantID, "online").First(&product).Error; err != nil {
		return errors.New("ctrip product is unavailable")
	}
	if err := requireActiveScenicSupplier(tx, productFulfillmentTenantID(&product)); err != nil {
		return errors.New("scenic supplier business is unavailable")
	}
	return nil
}

func ctripInventoryPayloadIsZero(payloadJSON string) bool {
	var payload ctrip.InventoryRequest
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil || len(payload.Inventories) == 0 {
		return false
	}
	for _, inventory := range payload.Inventories {
		if inventory.Quantity != 0 {
			return false
		}
	}
	return true
}

func failUnavailableCtripProductTaskTx(tx *gorm.DB, task *model.CtripOutboundTask, now time.Time, cause error) error {
	message := strings.TrimSpace(cause.Error())
	result := tx.Model(&model.CtripOutboundTask{}).
		Where("id = ? AND status = ? AND locked_at = ?", task.ID, "processing", *task.LockedAt).
		Updates(map[string]interface{}{
			"status": "failed", "next_attempt_at": nil, "locked_at": nil, "last_error": message, "completed_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("ctrip product task ownership was lost")
	}
	return nil
}

func supersedeCtripOutboundTaskTx(tx *gorm.DB, task *model.CtripOutboundTask, now time.Time, reason string) error {
	result := tx.Model(&model.CtripOutboundTask{}).
		Where("id = ? AND status = ? AND locked_at = ?", task.ID, "processing", *task.LockedAt).
		Updates(map[string]interface{}{
			"status": "succeeded", "result_code": "SUPERSEDED", "result_message": strings.TrimSpace(reason),
			"last_error": "", "next_attempt_at": nil, "locked_at": nil, "completed_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("ctrip product task ownership was lost")
	}
	return nil
}

func completeCtripOutboundTaskTx(tx *gorm.DB, task *model.CtripOutboundTask, response *ctrip.Response, now time.Time) error {
	if task == nil || task.LockedAt == nil || response == nil {
		return errors.New("ctrip task completion is invalid")
	}
	result := tx.Model(&model.CtripOutboundTask{}).
		Where("id = ? AND status = ? AND locked_at = ?", task.ID, "processing", *task.LockedAt).
		Updates(map[string]interface{}{
			"status": "succeeded", "result_code": response.Code, "result_message": response.Message,
			"last_error": "", "next_attempt_at": nil, "locked_at": nil, "completed_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("ctrip task ownership was lost")
	}
	return nil
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
		if task == nil || task.LockedAt == nil {
			return errors.New("ctrip task ownership is invalid")
		}
		result := tx.Model(&model.CtripOutboundTask{}).
			Where("id = ? AND status = ? AND locked_at = ?", task.ID, "processing", *task.LockedAt).
			Updates(map[string]interface{}{"status": status, "next_attempt_at": next, "locked_at": nil, "last_error": message})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("ctrip task ownership was lost")
		}
		return nil
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
