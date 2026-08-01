package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
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
		var fulfillments []model.FulfillmentOrder
		if err := tx.Where("supplier_tenant_id = ? AND sales_tenant_id = ? AND status IN ? AND created_at BETWEEN ? AND ?", supplierTenantID, distributorTenantID, []string{"paid", "fulfilled"}, start, end).
			Where("NOT EXISTS (SELECT 1 FROM settlement_lines WHERE settlement_lines.fulfillment_order_id = fulfillment_orders.id)").Find(&fulfillments).Error; err != nil {
			return err
		}
		if len(fulfillments) == 0 {
			return errors.New("no fulfillment facts in settlement period")
		}
		statement = model.SettlementStatement{SupplierTenantID: supplierTenantID, DistributorTenantID: distributorTenantID, IdempotencyKey: periodKey, StatementNo: fmt.Sprintf("STL-%d-%d", time.Now().UnixNano(), supplierTenantID), PeriodStart: start, PeriodEnd: end, Status: "draft"}
		if err := tx.Create(&statement).Error; err != nil {
			return err
		}
		for i := range fulfillments {
			fulfillment := &fulfillments[i]
			gross, refundCents, commission, err := settlementAmountsForFulfillment(tx, fulfillment)
			if err != nil {
				return err
			}
			net := gross - refundCents - commission
			if net < 0 {
				return errors.New("settlement line would be negative")
			}
			line := model.SettlementLine{StatementID: statement.ID, FulfillmentOrderID: fulfillment.ID, GrossCents: gross, RefundCents: refundCents, CommissionCents: commission, NetCents: net, Status: "open"}
			if err := tx.Create(&line).Error; err != nil {
				return err
			}
			if err := tx.Model(fulfillment).Update("settlement_status", "statement").Error; err != nil {
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
	if err := model.DB.Preload("Lines").Where("id = ? AND (supplier_tenant_id = ? OR distributor_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
		return nil, err
	}
	return &statement, nil
}

func (s *SettlementService) SetStatus(tenantID, statementID uint, status, detail string) error {
	if status != "supplier_confirmed" && status != "confirmed" && status != "paid" && status != "disputed" {
		return errors.New("invalid settlement status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.SettlementStatement
		if err := tx.Where("id = ? AND (supplier_tenant_id = ? OR distributor_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		now := time.Now()
		values := map[string]interface{}{"status": status}
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
		return tx.Model(&statement).Updates(values).Error
	})
}
