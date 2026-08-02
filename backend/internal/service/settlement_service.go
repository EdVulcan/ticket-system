package service

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SettlementService struct{}

// GenerateStatement snapshots fulfillment facts for one supplier/distributor
// pair. It never reads current product prices, so later catalog changes cannot
// rewrite an already generated statement.
func (s *SettlementService) GenerateStatement(actorTenantID, supplierTenantID, distributorTenantID uint, start, end time.Time) (*model.SettlementStatement, error) {
	if actorTenantID != supplierTenantID || supplierTenantID == 0 || distributorTenantID == 0 || start.After(end) {
		return nil, errors.New("invalid settlement scope or period")
	}
	var statement model.SettlementStatement
	periodKey := fmt.Sprintf("settlement:%d:%d:%s:%s", supplierTenantID, distributorTenantID, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	err := model.Write(func(tx *gorm.DB) error {
		var existing model.SettlementStatement
		if err := tx.Where("idempotency_key = ?", periodKey).First(&existing).Error; err == nil {
			statement = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := requireActiveTenantCapability(tx, supplierTenantID, "supplier"); err != nil {
			return err
		}
		if err := requireActiveTenantCapability(tx, distributorTenantID, "distributor"); err != nil {
			return err
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("supplier_tenant_id = ? AND agent_tenant_id = ? AND status IN ?", supplierTenantID, distributorTenantID, []string{"active", "suspended"}).First(&relationship).Error; err != nil {
			return errors.New("distribution relationship not found")
		}
		facts, err := loadSettlementAdmissionFacts(tx, supplierTenantID, distributorTenantID, start, end)
		if err != nil {
			return err
		}
		if len(facts) == 0 {
			return errors.New("no verified admission facts in settlement period")
		}
		statement = model.SettlementStatement{SupplierTenantID: supplierTenantID, DistributorTenantID: distributorTenantID, IdempotencyKey: periodKey, StatementNo: fmt.Sprintf("STL-%d-%d", time.Now().UnixNano(), supplierTenantID), PeriodStart: start, PeriodEnd: end, Status: "draft"}
		if err := tx.Create(&statement).Error; err != nil {
			return err
		}
		for fulfillmentID, fact := range facts {
			gross, refundCents, commission := settlementAmountsForAdmissionFacts(fact)
			net := gross - refundCents - commission
			line := model.SettlementLine{StatementID: statement.ID, FulfillmentOrderID: fulfillmentID, Source: "verification", GrossCents: gross, RefundCents: refundCents, CommissionCents: commission, NetCents: net, Status: "open"}
			if err := tx.Create(&line).Error; err != nil {
				return err
			}
			if err := claimSettlementAdmissionFacts(tx, &line, fact); err != nil {
				return err
			}
			if err := tx.Model(&model.FulfillmentOrder{}).Where("id = ?", fulfillmentID).Update("settlement_status", "statement").Error; err != nil {
				return err
			}
			statement.GrossCents += gross
			statement.RefundCents += refundCents
			statement.CommissionCents += commission
			statement.NetCents += net
		}
		return tx.Model(&statement).Updates(map[string]interface{}{"gross_cents": statement.GrossCents, "refund_cents": statement.RefundCents, "commission_cents": statement.CommissionCents, "net_cents": statement.NetCents}).Error
	})
	if err != nil {
		return nil, err
	}
	return &statement, nil
}

type settlementAdmissionFact struct {
	Record model.CheckInRecord
	Ticket model.Ticket
	Item   model.OrderItem
}

type settlementAdmissionGroup struct {
	Admissions []settlementAdmissionFact
	Reversals  []settlementAdmissionFact
}

func loadSettlementAdmissionFacts(tx *gorm.DB, supplierTenantID, distributorTenantID uint, start, end time.Time) (map[uint]settlementAdmissionGroup, error) {
	// Old statements were sale-based. Link their successful admissions to the
	// legacy line so later refunds can still be carried into a new statement,
	// while preventing those sales from being settled a second time.
	if err := tx.Exec(`UPDATE check_in_records SET settlement_line_id = (
		SELECT settlement_lines.id FROM settlement_lines
		JOIN fulfillment_orders ON fulfillment_orders.id = settlement_lines.fulfillment_order_id
		JOIN tickets ON tickets.fulfillment_order_id = fulfillment_orders.id
		WHERE settlement_lines.source = 'legacy_sale' AND tickets.id = check_in_records.ticket_id
		ORDER BY settlement_lines.id LIMIT 1
	) WHERE result = 'success' AND settlement_line_id = 0 AND EXISTS (
		SELECT 1 FROM settlement_lines
		JOIN fulfillment_orders ON fulfillment_orders.id = settlement_lines.fulfillment_order_id
		JOIN tickets ON tickets.fulfillment_order_id = fulfillment_orders.id
		WHERE settlement_lines.source = 'legacy_sale' AND tickets.id = check_in_records.ticket_id
	)`).Error; err != nil {
		return nil, err
	}
	base := tx.Model(&model.CheckInRecord{}).
		Joins("JOIN tickets ON tickets.id = check_in_records.ticket_id").
		Joins("JOIN fulfillment_orders ON fulfillment_orders.id = tickets.fulfillment_order_id").
		Where("check_in_records.result = ? AND fulfillment_orders.supplier_tenant_id = ? AND fulfillment_orders.sales_tenant_id = ?", "success", supplierTenantID, distributorTenantID).
		Where("NOT EXISTS (SELECT 1 FROM check_in_records earlier WHERE earlier.ticket_id = check_in_records.ticket_id AND earlier.result = 'success' AND earlier.id < check_in_records.id)").
		Where("NOT EXISTS (SELECT 1 FROM tour_groups WHERE tour_groups.sales_order_id = fulfillment_orders.sales_order_id)")
	var admissions, reversals []model.CheckInRecord
	if err := base.Session(&gorm.Session{}).
		Where("check_in_records.settlement_line_id = 0 AND check_in_records.reversed_at IS NULL AND check_in_records.check_in_time BETWEEN ? AND ?", start, end).
		Find(&admissions).Error; err != nil {
		return nil, err
	}
	if err := base.Session(&gorm.Session{}).
		Where("check_in_records.settlement_line_id != 0 AND check_in_records.reversal_settlement_line_id = 0 AND check_in_records.reversed_at BETWEEN ? AND ?", start, end).
		Find(&reversals).Error; err != nil {
		return nil, err
	}
	all := append(append([]model.CheckInRecord{}, admissions...), reversals...)
	if len(all) == 0 {
		return map[uint]settlementAdmissionGroup{}, nil
	}
	ticketIDs := make([]uint, 0, len(all))
	for i := range all {
		ticketIDs = append(ticketIDs, all[i].TicketID)
	}
	var tickets []model.Ticket
	if err := tx.Preload("OrderItem").Where("id IN ?", ticketIDs).Find(&tickets).Error; err != nil {
		return nil, err
	}
	ticketByID := make(map[uint]model.Ticket, len(tickets))
	for i := range tickets {
		ticketByID[tickets[i].ID] = tickets[i]
	}
	groups := make(map[uint]settlementAdmissionGroup)
	appendFacts := func(records []model.CheckInRecord, reversal bool) error {
		for i := range records {
			ticket, ok := ticketByID[records[i].TicketID]
			if !ok || ticket.FulfillmentOrderID == 0 || ticket.OrderItem.ID == 0 {
				return errors.New("verified ticket has no immutable fulfillment snapshot")
			}
			group := groups[ticket.FulfillmentOrderID]
			fact := settlementAdmissionFact{Record: records[i], Ticket: ticket, Item: ticket.OrderItem}
			if reversal {
				group.Reversals = append(group.Reversals, fact)
			} else {
				group.Admissions = append(group.Admissions, fact)
			}
			groups[ticket.FulfillmentOrderID] = group
		}
		return nil
	}
	if err := appendFacts(admissions, false); err != nil {
		return nil, err
	}
	if err := appendFacts(reversals, true); err != nil {
		return nil, err
	}
	return groups, nil
}

func settlementAmountForAdmission(fact settlementAdmissionFact) (int64, int64) {
	amount := moneyCents(fact.Item.SettlementPrice)
	if fact.Ticket.CodeMode == "order" {
		amount *= int64(fact.Item.Quantity)
	}
	return amount, amount * fact.Item.CommissionBPS / 10000
}

func settlementAmountsForAdmissionFacts(group settlementAdmissionGroup) (gross, refund, commission int64) {
	for i := range group.Admissions {
		amount, fee := settlementAmountForAdmission(group.Admissions[i])
		gross += amount
		commission += fee
	}
	for i := range group.Reversals {
		amount, fee := settlementAmountForAdmission(group.Reversals[i])
		refund += amount
		commission -= fee
	}
	return gross, refund, commission
}

func claimSettlementAdmissionFacts(tx *gorm.DB, line *model.SettlementLine, group settlementAdmissionGroup) error {
	claim := func(facts []settlementAdmissionFact, column string) error {
		if len(facts) == 0 {
			return nil
		}
		ids := make([]uint, 0, len(facts))
		for i := range facts {
			ids = append(ids, facts[i].Record.ID)
		}
		result := tx.Model(&model.CheckInRecord{}).Where("id IN ? AND "+column+" = 0", ids).Update(column, line.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(ids)) {
			return errors.New("settlement admission fact was already claimed")
		}
		return nil
	}
	if err := claim(group.Admissions, "settlement_line_id"); err != nil {
		return err
	}
	return claim(group.Reversals, "reversal_settlement_line_id")
}

func verifiedSettlementAmountsForFulfillment(tx *gorm.DB, fulfillment *model.FulfillmentOrder) (int64, int64, int64, error) {
	var records []model.CheckInRecord
	if err := tx.Model(&model.CheckInRecord{}).
		Joins("JOIN tickets ON tickets.id = check_in_records.ticket_id").
		Where("tickets.fulfillment_order_id = ? AND check_in_records.result = ?", fulfillment.ID, "success").
		Where("NOT EXISTS (SELECT 1 FROM check_in_records earlier WHERE earlier.ticket_id = check_in_records.ticket_id AND earlier.result = 'success' AND earlier.id < check_in_records.id)").
		Find(&records).Error; err != nil {
		return 0, 0, 0, err
	}
	if len(records) == 0 {
		return 0, 0, 0, nil
	}
	ticketIDs := make([]uint, 0, len(records))
	for i := range records {
		ticketIDs = append(ticketIDs, records[i].TicketID)
	}
	var tickets []model.Ticket
	if err := tx.Preload("OrderItem").Where("id IN ?", ticketIDs).Find(&tickets).Error; err != nil {
		return 0, 0, 0, err
	}
	ticketByID := make(map[uint]model.Ticket, len(tickets))
	for i := range tickets {
		ticketByID[tickets[i].ID] = tickets[i]
	}
	group := settlementAdmissionGroup{}
	for i := range records {
		ticket, ok := ticketByID[records[i].TicketID]
		if !ok {
			return 0, 0, 0, errors.New("verified ticket snapshot is unavailable")
		}
		fact := settlementAdmissionFact{Record: records[i], Ticket: ticket, Item: ticket.OrderItem}
		group.Admissions = append(group.Admissions, fact)
		if records[i].ReversedAt != nil {
			group.Reversals = append(group.Reversals, fact)
		}
	}
	gross, refund, commission := settlementAmountsForAdmissionFacts(group)
	return gross, refund, commission, nil
}

// settlementAmountsForFulfillment is the sale-time responsibility projection
// shown on order and fulfillment details. It is deliberately separate from
// verifiedSettlementAmountsForFulfillment, which is the supplier income basis.
func settlementAmountsForFulfillment(tx *gorm.DB, fulfillment *model.FulfillmentOrder) (int64, int64, int64, error) {
	var items []model.OrderItem
	if err := tx.Preload("Tickets").Where("fulfillment_order_id = ?", fulfillment.ID).Find(&items).Error; err != nil {
		return 0, 0, 0, err
	}
	if len(items) == 0 {
		return moneyCents(fulfillment.SettlementAmount), 0, 0, nil
	}
	var refunds []model.Refund
	if err := tx.Where("tenant_id = ? AND order_no = ? AND status = ?", fulfillment.SalesTenantID, fulfillment.SalesOrderNo, "succeeded").Find(&refunds).Error; err != nil {
		return 0, 0, 0, err
	}
	refundedCodes := make(map[string]struct{})
	for i := range refunds {
		var codes []string
		if err := json.Unmarshal([]byte(refunds[i].TicketCodesJSON), &codes); err != nil {
			return 0, 0, 0, fmt.Errorf("invalid refund ticket allocation: %w", err)
		}
		for _, code := range codes {
			refundedCodes[code] = struct{}{}
		}
	}
	var gross, refunded, commission int64
	for i := range items {
		item := &items[i]
		itemGross := moneyCents(item.SettlementPrice) * int64(item.Quantity)
		itemRefund := int64(0)
		for ticketIndex := range item.Tickets {
			ticket := &item.Tickets[ticketIndex]
			if _, ok := refundedCodes[ticket.TicketCode]; !ok {
				continue
			}
			if ticket.CodeMode == "order" {
				itemRefund = itemGross
				break
			}
			itemRefund += moneyCents(item.SettlementPrice)
		}
		if itemRefund > itemGross {
			itemRefund = itemGross
		}
		gross += itemGross
		refunded += itemRefund
		commission += (itemGross - itemRefund) * item.CommissionBPS / 10000
	}
	return gross, refunded, commission, nil
}

func (s *SettlementService) List(tenantID uint, page, pageSize int) ([]model.SettlementStatement, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.SettlementStatement{}).Where("supplier_tenant_id = ? OR distributor_tenant_id = ?", tenantID, tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.SettlementStatement
	if err := query.Preload("Lines").Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetStatement returns one settlement with immutable fulfillment lines. Both
// counterparties may inspect it, but neither can read another relationship's
// statement by guessing an ID.
func (s *SettlementService) GetStatement(tenantID, statementID uint) (*model.SettlementStatement, error) {
	if tenantID == 0 || statementID == 0 {
		return nil, errors.New("tenant and statement are required")
	}
	var statement model.SettlementStatement
	if err := model.DB.Preload("Lines").Preload("Adjustments", func(db *gorm.DB) *gorm.DB { return db.Order("sequence ASC") }).Where("id = ? AND (supplier_tenant_id = ? OR distributor_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
		return nil, err
	}
	return &statement, nil
}

func (s *SettlementService) ExportStatementCSV(tenantID, statementID uint) ([]byte, string, error) {
	statement, err := s.GetStatement(tenantID, statementID)
	if err != nil {
		return nil, "", err
	}
	var supplier, distributor model.Tenant
	if err := model.DB.Select("id", "name").First(&supplier, statement.SupplierTenantID).Error; err != nil {
		return nil, "", err
	}
	if err := model.DB.Select("id", "name").First(&distributor, statement.DistributorTenantID).Error; err != nil {
		return nil, "", err
	}
	fulfillmentIDs := make([]uint, 0, len(statement.Lines))
	for i := range statement.Lines {
		fulfillmentIDs = append(fulfillmentIDs, statement.Lines[i].FulfillmentOrderID)
	}
	var fulfillments []model.FulfillmentOrder
	if len(fulfillmentIDs) > 0 {
		if err := model.DB.Where(
			"id IN ? AND supplier_tenant_id = ? AND sales_tenant_id = ?",
			fulfillmentIDs, statement.SupplierTenantID, statement.DistributorTenantID,
		).Find(&fulfillments).Error; err != nil {
			return nil, "", err
		}
	}
	fulfillmentByID := make(map[uint]model.FulfillmentOrder, len(fulfillments))
	for i := range fulfillments {
		fulfillmentByID[fulfillments[i].ID] = fulfillments[i]
	}
	var output bytes.Buffer
	output.Write([]byte{0xEF, 0xBB, 0xBF})
	records := [][]string{
		{"结算单号", statement.StatementNo},
		{"供应商", csvSafeCell(supplier.Name)},
		{"分销商", csvSafeCell(distributor.Name)},
		{"结算周期", statement.PeriodStart.Format("2006-01-02"), statement.PeriodEnd.Format("2006-01-02")},
		{"状态", statement.Status},
		{"履约总额", formatCents(statement.GrossCents), "退款冲减", formatCents(statement.RefundCents), "佣金", formatCents(statement.CommissionCents)},
		{"追加调整", formatCents(statement.AdjustmentCents), "最终应结", formatCents(statement.NetCents + statement.AdjustmentCents)},
		{},
		{"履约单号", "销售订单号", "履约总额", "退款冲减", "佣金", "应结净额", "状态"},
	}
	for i := range statement.Lines {
		line := statement.Lines[i]
		fulfillment, ok := fulfillmentByID[line.FulfillmentOrderID]
		if !ok {
			return nil, "", errors.New("settlement fulfillment is unavailable")
		}
		records = append(records, []string{fulfillment.FulfillmentNo, fulfillment.SalesOrderNo, formatCents(line.GrossCents), formatCents(line.RefundCents), formatCents(line.CommissionCents), formatCents(line.NetCents), line.Status})
	}
	if len(statement.Adjustments) > 0 {
		records = append(records, []string{}, []string{"调整序号", "调整金额", "调整后累计", "原因", "时间"})
		for i := range statement.Adjustments {
			adjustment := statement.Adjustments[i]
			records = append(records, []string{fmt.Sprint(adjustment.Sequence), formatCents(adjustment.AmountCents), formatCents(adjustment.NewAdjustmentCents), csvSafeCell(adjustment.Reason), adjustment.CreatedAt.Format("2006-01-02 15:04:05")})
		}
	}
	writer := csv.NewWriter(&output)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		return nil, "", err
	}
	return output.Bytes(), statement.StatementNo + ".csv", nil
}

func formatCents(value int64) string {
	return fmt.Sprintf("%.2f", float64(value)/100)
}

func csvSafeCell(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (s *SettlementService) AdjustDisputed(tenantID, statementID, actorUserID uint, amountCents int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if tenantID == 0 || statementID == 0 || amountCents == 0 || reason == "" {
		return errors.New("statement, non-zero adjustment and reason are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.SettlementStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND (supplier_tenant_id = ? OR distributor_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		if statement.Status != "disputed" {
			return errors.New("only a disputed statement can be adjusted")
		}
		newAdjustment := statement.AdjustmentCents + amountCents
		if statement.NetCents+newAdjustment < 0 {
			return errors.New("adjusted settlement payable cannot be negative")
		}
		var count int64
		if err := tx.Model(&model.SettlementAdjustment{}).Where("statement_id = ?", statement.ID).Count(&count).Error; err != nil {
			return err
		}
		adjustment := model.SettlementAdjustment{
			StatementID: statement.ID, Sequence: int(count) + 1, ActorTenantID: tenantID, ActorUserID: actorUserID,
			AmountCents: amountCents, PreviousAdjustmentCents: statement.AdjustmentCents, NewAdjustmentCents: newAdjustment, Reason: reason,
		}
		if err := tx.Create(&adjustment).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"adjustment_cents": newAdjustment, "status": "draft", "supplier_confirmed_at": nil,
			"distributor_confirmed_at": nil, "confirmed_at": nil,
		}
		if err := tx.Model(&statement).Updates(updates).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, "admin", "tenant", "settlement.adjust", "settlement_statement", statement.ID, reason,
			fmt.Sprintf(`{"status":%q,"adjustment_cents":%d}`, statement.Status, statement.AdjustmentCents),
			fmt.Sprintf(`{"status":"draft","adjustment_cents":%d}`, newAdjustment))
	})
}

func (s *SettlementService) SetStatus(tenantID, statementID uint, status, detail string, actorUserIDs ...uint) error {
	if status != "supplier_confirmed" && status != "confirmed" && status != "paid" && status != "disputed" {
		return errors.New("invalid settlement status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.SettlementStatement
		if err := tx.Where("id = ? AND (supplier_tenant_id = ? OR distributor_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		now := time.Now()
		beforeStatus := statement.Status
		values := map[string]interface{}{"status": status}
		finalStatus := status
		switch status {
		case "supplier_confirmed":
			if tenantID != statement.SupplierTenantID || statement.Status != "draft" {
				return errors.New("only the supplier can confirm a draft statement")
			}
			values["supplier_confirmed_at"] = now
		case "confirmed":
			if tenantID != statement.DistributorTenantID || statement.Status != "supplier_confirmed" {
				return errors.New("only the distributor can confirm a supplier-confirmed statement")
			}
			values["distributor_confirmed_at"] = now
			values["confirmed_at"] = now
			// Refund allocations restore prepaid cash or credit immediately. A
			// non-positive statement is therefore reconciliation-only and needs
			// no second payment operation after both parties confirm it.
			if statement.NetCents+statement.AdjustmentCents <= 0 {
				finalStatus = "paid"
				values["status"] = finalStatus
				values["paid_at"] = now
				values["payment_proof"] = "退款已原路恢复账户余额"
			}
		case "disputed":
			if statement.Status != "supplier_confirmed" && statement.Status != "confirmed" {
				return errors.New("only a reviewable statement can be disputed")
			}
			if detail == "" {
				return errors.New("dispute reason is required")
			}
			values["dispute_reason"] = detail
		case "paid":
			if tenantID != statement.DistributorTenantID || statement.Status != "confirmed" {
				return errors.New("only the distributor can pay a confirmed statement")
			}
			if detail == "" {
				return errors.New("payment proof is required")
			}
			values["paid_at"] = now
			values["payment_proof"] = detail
		}
		if err := tx.Model(&statement).Updates(values).Error; err != nil {
			return err
		}
		if finalStatus == "paid" {
			if err := tx.Model(&model.SettlementLine{}).Where("statement_id = ?", statement.ID).Update("status", "paid").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.FulfillmentOrder{}).Where("id IN (SELECT fulfillment_order_id FROM settlement_lines WHERE statement_id = ?)", statement.ID).Update("settlement_status", "paid").Error; err != nil {
				return err
			}
		}
		actorUserID := uint(0)
		if len(actorUserIDs) > 0 {
			actorUserID = actorUserIDs[0]
		}
		reason := strings.TrimSpace(detail)
		if reason == "" {
			reason = "settlement status changed to " + finalStatus
		}
		return recordAuditTx(tx, actorUserID, tenantID, "admin", "tenant", "settlement.status", "settlement_statement", statement.ID, reason,
			fmt.Sprintf(`{"status":%q}`, beforeStatus), fmt.Sprintf(`{"status":%q}`, finalStatus))
	})
}
