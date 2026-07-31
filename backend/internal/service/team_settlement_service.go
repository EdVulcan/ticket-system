package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GenerateTeamSettlement snapshots one confirmed team's contract amount and
// successful refunds. A retry returns the existing statement for the same
// group, so a team cannot be paid twice by repeating the request.
func (s *TeamService) GenerateTeamSettlement(travelTenantID, groupID uint) (*model.TeamSettlementStatement, error) {
	if travelTenantID == 0 || groupID == 0 {
		return nil, errors.New("travel tenant and group are required")
	}
	var statement model.TeamSettlementStatement
	err := model.Write(func(tx *gorm.DB) error {
		var group model.TourGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, travelTenantID).First(&group).Error; err != nil {
			return errors.New("team group not found")
		}
		if group.SalesOrderID == 0 || group.Status == "cancelled" {
			return errors.New("team has no settled sales order")
		}
		key := fmt.Sprintf("team-settlement:%d", group.ID)
		if err := tx.Where("idempotency_key = ?", key).First(&statement).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var order model.Order
		if err := tx.Where("id = ? AND tenant_id = ? AND status IN ?", group.SalesOrderID, travelTenantID, []string{"paid", "completed", "partial_refunded", "refunded"}).First(&order).Error; err != nil {
			return errors.New("team sales order is not settled")
		}
		gross := group.ContractAmountCents
		if gross <= 0 {
			gross = moneyCents(order.TotalAmount)
		}
		var refundCents int64
		if err := tx.Model(&model.Refund{}).Where("tenant_id = ? AND order_no = ? AND status = ?", travelTenantID, order.OrderNo, "succeeded").Select("COALESCE(SUM(CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS INTEGER) END), 0)").Scan(&refundCents).Error; err != nil {
			return err
		}
		if refundCents > gross {
			refundCents = gross
		}
		deposit := group.DepositCents
		available := gross - refundCents
		if deposit > available {
			deposit = available
		}
		net := available - deposit
		statement = model.TeamSettlementStatement{
			TravelTenantID: travelTenantID, SupplierTenantID: group.SupplierTenantID,
			GroupID: group.ID, StatementNo: fmt.Sprintf("TST-%d-%d", time.Now().UnixNano(), group.ID),
			IdempotencyKey: key, GrossCents: gross, RefundCents: refundCents,
			DepositCents: deposit, NetCents: net, Status: "draft",
		}
		if err := tx.Create(&statement).Error; err != nil {
			return err
		}
		return tx.Model(&group).Update("settlement_status", "statement").Error
	})
	if err != nil {
		return nil, err
	}
	return &statement, nil
}

func (s *TeamService) ListTeamSettlements(tenantID uint, page, pageSize int) ([]model.TeamSettlementStatement, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.TeamSettlementStatement{}).Where("travel_tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.TeamSettlementStatement
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *TeamService) SetTeamSettlementStatus(tenantID, statementID uint, status, detail string) error {
	if status != "supplier_confirmed" && status != "confirmed" && status != "disputed" && status != "paid" {
		return errors.New("invalid team settlement status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.TeamSettlementStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND (travel_tenant_id = ? OR supplier_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"status": status}
		now := time.Now()
		switch status {
		case "supplier_confirmed":
			if tenantID != statement.SupplierTenantID || statement.Status != "draft" {
				return errors.New("only supplier can confirm a draft team settlement")
			}
		case "confirmed":
			if tenantID != statement.TravelTenantID || statement.Status != "supplier_confirmed" {
				return errors.New("only travel agency can confirm supplier-approved settlement")
			}
			updates["confirmed_at"] = now
		case "disputed":
			if statement.Status != "supplier_confirmed" && statement.Status != "confirmed" || strings.TrimSpace(detail) == "" {
				return errors.New("a reviewable settlement and dispute reason are required")
			}
			updates["dispute_reason"] = strings.TrimSpace(detail)
		case "paid":
			if tenantID != statement.TravelTenantID || statement.Status != "confirmed" || strings.TrimSpace(detail) == "" {
				return errors.New("travel agency payment requires a confirmed settlement and proof")
			}
			updates["paid_at"] = now
			updates["payment_proof"] = strings.TrimSpace(detail)
		}
		return tx.Model(&statement).Updates(updates).Error
	})
}
