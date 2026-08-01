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
func (s *TeamService) GenerateTeamSettlement(travelTenantID, groupID uint, actorUserIDs ...uint) (*model.TeamSettlementStatement, error) {
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
		if err := tx.Model(&group).Update("settlement_status", "statement").Error; err != nil {
			return err
		}
		actorUserID := firstUint(actorUserIDs)
		return recordAuditTx(tx, actorUserID, travelTenantID, auditRoleTx(tx, actorUserID), "tenant", "team.settlement.generate", "team_settlement_statement", statement.ID,
			fmt.Sprintf("generated settlement for team %s", group.GroupNo), "", fmt.Sprintf(`{"status":"draft","net_cents":%d}`, statement.NetCents))
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
	if err := query.Preload("Adjustments", func(db *gorm.DB) *gorm.DB { return db.Order("sequence ASC") }).Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *TeamService) SetTeamSettlementStatus(tenantID, statementID uint, status, detail string, actorUserIDs ...uint) error {
	if status != "supplier_confirmed" && status != "confirmed" && status != "disputed" && status != "paid" {
		return errors.New("invalid team settlement status")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.TeamSettlementStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND (travel_tenant_id = ? OR supplier_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		beforeStatus := statement.Status
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
		if err := tx.Model(&statement).Updates(updates).Error; err != nil {
			return err
		}
		if status == "paid" {
			if err := tx.Model(&model.TourGroup{}).Where("id = ? AND tenant_id = ?", statement.GroupID, statement.TravelTenantID).Update("settlement_status", "settled").Error; err != nil {
				return err
			}
		}
		actorUserID := firstUint(actorUserIDs)
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.settlement.status", "team_settlement_statement", statement.ID,
			strings.TrimSpace(detail), fmt.Sprintf(`{"status":%q}`, beforeStatus), fmt.Sprintf(`{"status":%q}`, status))
	})
}

func (s *TeamService) AdjustTeamSettlement(tenantID, statementID, actorUserID uint, amountCents int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if tenantID == 0 || statementID == 0 || amountCents == 0 || reason == "" {
		return errors.New("statement, non-zero adjustment and reason are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		var statement model.TeamSettlementStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND (travel_tenant_id = ? OR supplier_tenant_id = ?)", statementID, tenantID, tenantID).First(&statement).Error; err != nil {
			return err
		}
		if statement.Status != "disputed" {
			return errors.New("only a disputed team settlement can be adjusted")
		}
		newAdjustment := statement.AdjustmentCents + amountCents
		if statement.NetCents+newAdjustment < 0 {
			return errors.New("adjusted team settlement payable cannot be negative")
		}
		var count int64
		if err := tx.Model(&model.TeamSettlementAdjustment{}).Where("statement_id = ?", statement.ID).Count(&count).Error; err != nil {
			return err
		}
		adjustment := model.TeamSettlementAdjustment{
			StatementID: statement.ID, Sequence: int(count) + 1, ActorTenantID: tenantID, ActorUserID: actorUserID,
			AmountCents: amountCents, PreviousAdjustmentCents: statement.AdjustmentCents, NewAdjustmentCents: newAdjustment, Reason: reason,
		}
		if err := tx.Create(&adjustment).Error; err != nil {
			return err
		}
		if err := tx.Model(&statement).Updates(map[string]interface{}{
			"adjustment_cents": newAdjustment, "status": "draft", "confirmed_at": nil,
		}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.settlement.adjust", "team_settlement_statement", statement.ID, reason,
			fmt.Sprintf(`{"status":%q,"adjustment_cents":%d}`, statement.Status, statement.AdjustmentCents),
			fmt.Sprintf(`{"status":"draft","adjustment_cents":%d}`, newAdjustment))
	})
}

func firstUint(values []uint) uint {
	if len(values) > 0 {
		return values[0]
	}
	return 0
}

func auditRoleTx(tx *gorm.DB, userID uint) string {
	if userID == 0 {
		return "system"
	}
	var user model.User
	if err := tx.Select("role").First(&user, userID).Error; err == nil && user.Role != "" {
		return user.Role
	}
	return "unknown"
}
