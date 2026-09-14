package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/zyb"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The sale snapshot also holds the small, restartable supplier workflow. Local
// orders never enter this worker; payment callbacks and verification are unchanged.
type UpstreamSupplyWorker struct {
	NewClient func(model.UpstreamConnection) (*zyb.Client, error)
	Decoder   interface {
		Decode(context.Context, []byte) (string, error)
	}
}

func newUpstreamClient(c model.UpstreamConnection) (*zyb.Client, error) {
	key, err := utils.DecryptAES(c.PrivateKeyCiphertext)
	if err != nil {
		return nil, errors.New("供应商凭据不可读取")
	}
	return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: c.CorpCode, Username: c.Username, PrivateKey: key, Timeout: 15 * time.Second, Gate: newUpstreamDispatchGate(c)}}, nil
}

func (w *UpstreamSupplyWorker) ProcessTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	processed := 0
	for processed < limit {
		leaseNow := time.Now()
		if now.After(leaseNow) {
			leaseNow = now
		}
		var snapshot model.OrderItemSupplySnapshot
		err := model.Write(func(tx *gorm.DB) error {
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Where("mode = 'upstream' AND issue_status IN ?", []string{"pending", "ready"}).
				Where("(next_attempt_at IS NULL OR next_attempt_at <= ?) AND (locked_at IS NULL OR locked_at < ?)", now, leaseNow.Add(-2*time.Minute)).
				Where("EXISTS (SELECT 1 FROM orders o WHERE o.id = order_item_supply_snapshots.order_id AND o.tenant_id = order_item_supply_snapshots.sales_tenant_id AND (o.status IN ('paid','completed','partial_refunded') OR (order_item_supply_snapshots.issue_status = 'ready' AND order_item_supply_snapshots.sync_requested_at IS NOT NULL)) AND o.deleted_at IS NULL)").
				Order("CASE WHEN issue_status = 'pending' THEN 0 WHEN sync_requested_at IS NOT NULL THEN 1 ELSE 2 END, next_attempt_at ASC NULLS FIRST, id ASC").First(&snapshot).Error
			if err != nil {
				return err
			}
			snapshot.LockedAt = &leaseNow
			return tx.Model(&snapshot).Update("locked_at", leaseNow).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return processed, err
		}
		callCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
		err = w.processSnapshot(callCtx, &snapshot)
		cancel()
		var item model.OrderItem
		if e := model.DB.Select("id", "use_date").Where("id = ?", snapshot.OrderItemID).First(&item).Error; e != nil {
			return processed, e
		}
		if err != nil {
			snapshot.SyncFailureCount++
		} else {
			snapshot.SyncFailureCount = 0
		}
		next := time.Now().Add(upstreamPollDelay(snapshot, item.UseDate, time.Now()))
		message := ""
		if err != nil {
			message = truncateChannelError(err.Error())
		}
		if retry, deferred := upstreamDispatchRetry(err); deferred {
			next = retry
			snapshot.SyncFailureCount--
			message = "等待供应商请求调度"
		}
		if updateErr := model.Write(func(tx *gorm.DB) error {
			updates := map[string]interface{}{"locked_at": nil, "next_attempt_at": next, "last_error": message, "sync_failure_count": snapshot.SyncFailureCount}
			if err == nil {
				updates["sync_requested_at"] = nil
			}
			return tx.Model(&model.OrderItemSupplySnapshot{}).Where("id = ? AND locked_at = ?", snapshot.ID, snapshot.LockedAt).Updates(updates).Error
		}); updateErr != nil {
			return processed, updateErr
		}
		processed++
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
	}
	return processed, nil
}

func (w *UpstreamSupplyWorker) processSnapshot(ctx context.Context, s *model.OrderItemSupplySnapshot) error {
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("id = ? AND tenant_id = ?", s.OrderID, s.SalesTenantID).First(&order).Error; err != nil {
		return err
	}
	if len(order.Items) != 1 {
		return errors.New("上游订单必须对应一个票种")
	}
	item := &order.Items[0]
	if s.Provider != "zhiyoubao" || s.OrderItemID != item.ID || s.ProductID != item.FulfillmentProductID || s.ProductRevisionID != item.ProductRevisionID || s.ScenicAreaID == 0 || s.ScenicAreaID != item.FulfillmentScenicAreaID || s.FulfillmentTenantID != item.FulfillmentTenantID || s.Environment != order.Environment {
		return errors.New("上游销售快照归属不匹配")
	}
	var connection model.UpstreamConnection
	if err := model.DB.Where("id = ? AND tenant_id = ? AND provider = ? AND environment = ?", s.ConnectionID, s.FulfillmentTenantID, s.Provider, s.Environment).First(&connection).Error; err != nil {
		return err
	}
	// Disabling new sales must not strand already-paid orders or refunds.
	factory := w.NewClient
	if factory == nil {
		factory = newUpstreamClient
	}
	client, err := factory(connection)
	if err != nil {
		return err
	}
	if s.IssueStatus == "ready" {
		priority := 0
		if s.SyncRequestedAt != nil {
			priority = 10
		}
		ctx = WithUpstreamDispatch(ctx, fmt.Sprintf("status-sync:%d", s.ID), priority, nil)
		return w.syncStatus(ctx, client, s, &order)
	}
	priority := 20
	if s.IssueAttemptedAt != nil {
		priority = 30
	}
	ctx = WithUpstreamDispatch(ctx, fmt.Sprintf("issuance:%d", s.ID), priority, nil)
	if s.CancelStatus != "" {
		return errors.New("上游出票已暂停：票券正在退款或状态已变化")
	}
	if err := upstreamTicketsPending(item, item.Tickets); err != nil {
		return err
	}
	var request zyb.SendCodeRequest
	if s.RequestPayloadCiphertext != "" {
		plain, e := utils.DecryptAES(s.RequestPayloadCiphertext)
		if e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(plain), &request); e != nil {
			return e
		}
	} else {
		date := order.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")
		if item.UseDate != nil {
			date = item.UseDate.Format("2006-01-02")
		}
		request = zyb.SendCodeRequest{ThirdPartyOrderCode: order.OrderNo, ChildOrderCode: fmt.Sprintf("%s_%d", order.OrderNo, item.ID), ContactName: order.ContactName, ContactMobile: order.ContactPhone, GoodsCode: s.ExternalProductCode, GoodsName: item.ProductName, VisitDate: date + " 00:00:00", PriceCents: moneyCents(item.Price), Quantity: item.Quantity, PayMethod: "vm"}
		encoded, e := json.Marshal(request)
		if e != nil {
			return e
		}
		cipher, e := utils.EncryptAES(string(encoded))
		if e != nil {
			return e
		}
		if e = model.Write(func(tx *gorm.DB) error {
			return tx.Model(s).Where("locked_at = ? AND request_payload_ciphertext = ''", s.LockedAt).Update("request_payload_ciphertext", cipher).Error
		}); e != nil {
			return e
		}
		s.RequestPayloadCiphertext = cipher
	}
	if request.ThirdPartyOrderCode != order.OrderNo || request.ChildOrderCode != fmt.Sprintf("%s_%d", order.OrderNo, item.ID) || request.GoodsCode != s.ExternalProductCode || request.Quantity != item.Quantity || request.PriceCents != moneyCents(item.Price) {
		return errors.New("上游出票请求与销售快照不匹配")
	}
	if s.ProviderOrderCode == "" {
		var providerOrder, providerSub string
		if s.IssueAttemptedAt == nil {
			// Persist intent before sending. An unknown response is recovered by
			// querying the SAME third-party order; it is never a second SendCode.
			attempted := time.Now()
			intent := func(tx *gorm.DB) error {
				result := tx.Model(s).Where("locked_at = ? AND issue_attempted_at IS NULL AND cancel_status = ''", s.LockedAt).Update("issue_attempted_at", attempted)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("上游出票任务已被接管")
				}
				s.IssueAttemptedAt = &attempted
				return nil
			}
			sendCtx, err := prepareUpstreamMutation(ctx, client, fmt.Sprintf("issuance:%d", s.ID), 20, intent)
			if err != nil {
				return err
			}
			result, _, e := client.SendCode(sendCtx, request)
			if e != nil {
				return e
			}
			if len(result.Order.Tickets) != 1 {
				return errors.New("供应商出票票项数量不匹配")
			}
			t := result.Order.Tickets[0]
			if e = validateUpstreamIssuedItem(t.GoodsCode, t.Quantity, t.Price, t.TotalPrice, t.VisitDate, request); e != nil {
				return e
			}
			providerOrder, providerSub = result.Order.ProviderOrderCode, t.ProviderSubOrderCode
		} else {
			result, _, e := client.QueryOrder(ctx, order.OrderNo)
			if e != nil {
				return fmt.Errorf("出票已尝试，只查原订单恢复：%w", e)
			}
			if len(result.Tickets) != 1 {
				return errors.New("供应商查单票项数量不匹配")
			}
			t := result.Tickets[0]
			if !upstreamQueryTicketMatches(s, item, order.OrderNo, t) {
				return errors.New("供应商查单的第三方子单或商品不匹配")
			}
			if e = validateUpstreamIssuedItem(t.GoodsCode, t.Quantity, t.Price, t.TotalPrice, t.VisitDate, request); e != nil {
				return e
			}
			providerOrder, providerSub = result.ProviderOrderCode, t.ProviderSubOrderCode
		}
		if providerOrder == "" {
			return errors.New("供应商订单号缺失")
		}
		// Save successful issuance BEFORE retrieving the image.
		if err = model.Write(func(tx *gorm.DB) error {
			r := tx.Model(s).Where("locked_at = ? AND provider_order_code = ''", s.LockedAt).Updates(map[string]interface{}{"provider_order_code": providerOrder, "provider_sub_order_code": providerSub})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return errors.New("上游出票任务已被接管")
			}
			return nil
		}); err != nil {
			return err
		}
		s.ProviderOrderCode, s.ProviderSubOrderCode = providerOrder, providerSub
	}
	artifacts, _, err := client.TicketImages(ctx, order.OrderNo)
	if err != nil {
		return err
	}
	decoder := w.Decoder
	if decoder == nil {
		decoder = zyb.QRCodeDecoder{}
	}
	var codes []string
	seen := make(map[string]bool)
	for _, artifact := range artifacts {
		if artifact == nil || artifact.Kind != "image" {
			return errors.New("供应商未返回二维码图片")
		}
		data, err := base64.StdEncoding.DecodeString(artifact.Value)
		if err != nil {
			return err
		}
		var decoded []string
		if multi, ok := decoder.(interface {
			DecodeAll(context.Context, []byte) ([]string, error)
		}); ok {
			decoded, err = multi.DecodeAll(ctx, data)
		} else {
			var code string
			code, err = decoder.Decode(ctx, data)
			decoded = []string{code}
		}
		if err != nil {
			return err
		}
		for _, code := range decoded {
			if !seen[code] {
				codes = append(codes, code)
				seen[code] = true
			}
		}
	}
	// Before the initial atomic binding, provider image layout/order is not a
	// ticket identity. Normalize once; ready tickets are never rebound.
	sort.Strings(codes)
	return w.finishIssueCodes(s, codes)
}

func validateUpstreamIssuedItem(goods, quantity, price, total, date string, request zyb.SendCodeRequest) error {
	q, err := strconv.Atoi(quantity)
	if err != nil || q != request.Quantity || goods != request.GoodsCode {
		return errors.New("供应商出票商品或数量不匹配")
	}
	unit, unitErr := zyb.ParseAmountCents(price)
	sum, sumErr := zyb.ParseAmountCents(total)
	if unitErr != nil || sumErr != nil || unit != request.PriceCents || sum != request.PriceCents*int64(q) {
		return errors.New("供应商出票金额不匹配")
	}
	if len(date) < 10 || len(request.VisitDate) < 10 || date[:10] != request.VisitDate[:10] {
		return errors.New("供应商出票日期不匹配")
	}
	return nil
}

func (w *UpstreamSupplyWorker) finishIssue(s *model.OrderItemSupplySnapshot, code string) error {
	return w.finishIssueCodes(s, []string{code})
}

func (w *UpstreamSupplyWorker) finishIssueCodes(s *model.OrderItemSupplySnapshot, codes []string) error {
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" || strings.TrimSpace(code) != code || utf8.RuneCountInString(code) > 50 || seen[code] {
			return errors.New("供应商票码为空、重复或超过现有票码字段长度")
		}
		seen[code] = true
	}
	return model.Write(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", s.OrderID, s.SalesTenantID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
			return errors.New("订单已取消或退款，停止出票")
		}
		var item model.OrderItem
		if err := tx.Where("id = ? AND order_id = ?", s.OrderItemID, s.OrderID).First(&item).Error; err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ? AND order_item_id = ? AND tenant_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ?", s.OrderID, s.OrderItemID, s.SalesTenantID, s.FulfillmentTenantID, s.ScenicAreaID).Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		if err := upstreamTicketsPending(&item, tickets); err != nil {
			return err
		}
		if len(codes) != len(tickets) {
			return fmt.Errorf("供应商返回 %d 个票码，本单需要 %d 个；请核对双方票码模式", len(codes), len(tickets))
		}
		var current model.OrderItemSupplySnapshot
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND locked_at = ? AND issue_status = 'pending' AND cancel_status = ''", s.ID, s.LockedAt).First(&current).Error; err != nil {
			return err
		}
		// Bind the complete code set once, atomically. No partially bound ticket
		// is exposed, and a retry after ready never replaces this association.
		for i := range tickets {
			ticket := &tickets[i]
			if err := tx.Model(ticket).Updates(map[string]interface{}{"ticket_code": codes[i], "status": "unused"}).Error; err != nil {
				return err
			}
			result := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ? AND sales_tenant_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", ticket.ID, s.SalesTenantID, s.FulfillmentTenantID, s.ScenicAreaID).Updates(map[string]interface{}{"ticket_code": codes[i], "status": "issued"})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("票券履约记录缺失")
			}
		}
		return tx.Model(&current).Updates(map[string]interface{}{"issue_status": "ready", "last_error": ""}).Error
	})
}

func (w *UpstreamSupplyWorker) syncStatus(ctx context.Context, client *zyb.Client, s *model.OrderItemSupplySnapshot, order *model.Order) error {
	result, err := client.QueryCheckStatus(ctx, order.OrderNo)
	if err != nil {
		return err
	}
	if len(result.SubOrders) == 0 {
		return errors.New("供应商状态为空")
	}
	for _, row := range result.SubOrders {
		if !upstreamCheckChildMatches(row.OrderCode, s, order.OrderNo) {
			return errors.New("供应商核销状态订单不匹配")
		}
	}
	status, used, err := upstreamUsageStatus(result.SubOrders)
	if err != nil {
		return err
	}
	updates := map[string]interface{}{"provider_status": status, "last_synced_at": time.Now()}
	if used && s.ProviderFirstUsedAt == nil {
		records, e := client.QueryCheckRecords(ctx, fmt.Sprintf("%s_%d", order.OrderNo, s.OrderItemID))
		if e == nil {
			first := s.ProviderFirstUsedAt
			for _, row := range records.SubOrders {
				if !upstreamCheckChildMatches(row.OrderCode, s, order.OrderNo) {
					return errors.New("供应商核销记录订单不匹配")
				}
				for _, record := range row.CheckRecords {
					if count, err := strconv.Atoi(record.CheckNum); err != nil || count <= 0 {
						continue
					}
					at, e := time.ParseInLocation("2006-01-02 15:04:05", record.CheckTime, time.FixedZone("CST", 8*3600))
					if e == nil && !at.After(time.Now()) && (first == nil || at.Before(*first)) {
						v := at
						first = &v
					}
				}
			}
			if first != nil {
				updates["provider_first_used_at"] = gorm.Expr("LEAST(provider_first_used_at, ?)", first)
			}
		}
	}
	return model.Write(func(tx *gorm.DB) error {
		// A check started before confirmed cancellation must not overwrite its
		// terminal supplier fact when its response arrives afterwards.
		updates["provider_status"] = gorm.Expr("CASE WHEN cancel_status = 'succeeded' AND provider_order_code <> '' THEN 'refunded' ELSE ? END", status)
		q := tx.Model(s)
		if s.LockedAt == nil {
			q = q.Where("locked_at IS NULL")
		} else {
			q = q.Where("locked_at = ?", s.LockedAt)
		}
		return q.Updates(updates).Error
	})
}

func upstreamUsageStatus(rows []zyb.CheckStatusSubOrder) (string, bool, error) {
	allReturned, allUsed := len(rows) > 0, len(rows) > 0
	anyReturned, anyUsed, ambiguous := false, false, false
	for _, row := range rows {
		total, a := strconv.Atoi(row.NeedCheckNum)
		checked, b := strconv.Atoi(row.AlreadyCheckNum)
		returned, c := strconv.Atoi(row.ReturnNum)
		if a != nil || b != nil || c != nil || total <= 0 || checked < 0 || returned < 0 || returned > total {
			return "", false, errors.New("供应商核销/退票数量不完整或无效")
		}
		allReturned = allReturned && returned == total
		allUsed = allUsed && checked >= total
		anyReturned = anyReturned || returned > 0
		anyUsed = anyUsed || checked > 0
		ambiguous = ambiguous || (checked == 0 && returned == 0 && row.CheckStatus != "un_check")
	}
	switch {
	case allReturned:
		return "refunded", anyUsed, nil
	case anyReturned:
		return "partial_refunded", anyUsed, nil
	case allUsed:
		return "checked", true, nil
	case anyUsed:
		return "checking", true, nil
	case ambiguous || len(rows) == 0:
		return "unknown", false, nil
	default:
		return "un_check", false, nil
	}
}
