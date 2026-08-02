package service

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *TeamService) ExportTeamSettlementCSV(tenantID, statementID uint) ([]byte, string, error) {
	var statement model.TeamSettlementStatement
	if err := model.DB.Preload("Adjustments", func(db *gorm.DB) *gorm.DB { return db.Order("sequence ASC") }).
		Where("id = ? AND (travel_tenant_id = ? OR supplier_tenant_id = ?)", statementID, tenantID, tenantID).
		First(&statement).Error; err != nil {
		return nil, "", err
	}
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND tenant_id = ? AND supplier_tenant_id = ?", statement.GroupID, statement.TravelTenantID, statement.SupplierTenantID).First(&group).Error; err != nil {
		return nil, "", err
	}
	var travel, supplier model.Tenant
	if err := model.DB.Select("id", "name").First(&travel, statement.TravelTenantID).Error; err != nil {
		return nil, "", err
	}
	if err := model.DB.Select("id", "name").First(&supplier, statement.SupplierTenantID).Error; err != nil {
		return nil, "", err
	}
	var scenic model.ScenicArea
	if err := model.DB.Select("id", "name").Where("id = ? AND tenant_id = ?", group.ScenicAreaID, statement.SupplierTenantID).First(&scenic).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}

	var output bytes.Buffer
	output.Write([]byte{0xEF, 0xBB, 0xBF})
	records := [][]string{
		{"团队结算单号", statement.StatementNo},
		{"团队", csvSafeCell(group.GroupNo), csvSafeCell(group.Name)},
		{"游玩日期", group.VisitDate.Format("2006-01-02")},
		{"旅行社", csvSafeCell(travel.Name)},
		{"供应商", csvSafeCell(supplier.Name)},
		{"景区", csvSafeCell(scenic.Name)},
		{"状态", statement.Status},
		{"履约总额", formatCents(statement.GrossCents), "退款冲减", formatCents(statement.RefundCents), "已付预款", formatCents(statement.DepositCents)},
		{"追加调整", formatCents(statement.AdjustmentCents), "最终应付", formatCents(statement.NetCents + statement.AdjustmentCents)},
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

// GenerateTeamSettlement snapshots one fully admitted team's effective
// verification amount. A retry returns the existing statement for the same
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
		if group.SalesOrderID == 0 || group.Status != "entered" {
			return errors.New("team must complete admission before settlement")
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
		gross, refundCents, err := teamVerifiedSettlementAmounts(tx, &group)
		if err != nil {
			return err
		}
		if gross <= 0 {
			return errors.New("team has no verified admission amount")
		}
		deposit := group.DepositCents
		available := gross - refundCents
		if deposit > available {
			deposit = available
		}
		net := available - deposit
		statement = model.TeamSettlementStatement{
			TravelTenantID: travelTenantID, SupplierTenantID: group.SupplierTenantID,
			GroupID: group.ID, Sequence: 1, Kind: "original", StatementNo: fmt.Sprintf("TST-%d-%d", time.Now().UnixNano(), group.ID),
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

// reconcileTeamSettlementsAfterRefundTx keeps team settlement projections in
// step with a successful ticket refund. Before final payment the open statement
// is refreshed and sent back for confirmation. After payment, a separate
// negative statement records the reconciliation correction; funds or credit
// were already restored by the refund transaction and are not moved again.
func reconcileTeamSettlementsAfterRefundTx(tx *gorm.DB, order *model.Order, refund *model.Refund) error {
	var groups []model.TourGroup
	if err := tx.Where("sales_order_id = ? AND status != ?", order.ID, "cancelled").Find(&groups).Error; err != nil {
		return err
	}
	for i := range groups {
		group := &groups[i]
		var statements []model.TeamSettlementStatement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ?", group.ID).Order("sequence ASC, id ASC").Find(&statements).Error; err != nil {
			return err
		}
		if len(statements) == 0 {
			continue
		}
		_, currentRefundCents, err := teamVerifiedSettlementAmounts(tx, group)
		if err != nil {
			return err
		}
		var accountedRefundCents int64
		paid := false
		maxSequence := 0
		for j := range statements {
			accountedRefundCents += statements[j].RefundCents
			paid = paid || statements[j].Status == "paid"
			if statements[j].Sequence > maxSequence {
				maxSequence = statements[j].Sequence
			}
		}
		delta := currentRefundCents - accountedRefundCents
		if delta <= 0 {
			continue
		}
		if !paid {
			statement := &statements[len(statements)-1]
			available := statement.GrossCents - (statement.RefundCents + delta)
			deposit := group.DepositCents
			if deposit > available {
				deposit = available
			}
			if deposit < 0 {
				deposit = 0
			}
			updates := map[string]interface{}{
				"refund_cents":  statement.RefundCents + delta,
				"deposit_cents": deposit,
				"net_cents":     available - deposit,
				"status":        "draft", "confirmed_at": nil,
				"dispute_reason": "", "payment_proof": "", "paid_at": nil,
			}
			if err := tx.Model(statement).Updates(updates).Error; err != nil {
				return err
			}
			if err := recordAuditTx(tx, refund.AuthorizedBy, group.SupplierTenantID, "super_admin", "tenant", "team.settlement.refund.refresh", "team_settlement_statement", statement.ID,
				refund.Reason, fmt.Sprintf(`{"refund_cents":%d,"status":%q}`, statement.RefundCents, statement.Status),
				fmt.Sprintf(`{"refund_cents":%d,"status":"draft"}`, statement.RefundCents+delta)); err != nil {
				return err
			}
			continue
		}

		key := fmt.Sprintf("team-settlement:%d:refund:%d", group.ID, refund.ID)
		var existing model.TeamSettlementStatement
		if err := tx.Where("idempotency_key = ?", key).First(&existing).Error; err == nil {
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now()
		correction := model.TeamSettlementStatement{
			TravelTenantID: group.TenantID, SupplierTenantID: group.SupplierTenantID,
			GroupID: group.ID, Sequence: maxSequence + 1, Kind: "refund_correction",
			StatementNo: fmt.Sprintf("TST-R-%d-%d", time.Now().UnixNano(), group.ID), IdempotencyKey: key,
			GrossCents: 0, RefundCents: delta, DepositCents: 0, NetCents: -delta,
			Status: "paid", PaymentProof: "退款已原路恢复账户余额", ConfirmedAt: &now, PaidAt: &now,
		}
		if err := tx.Create(&correction).Error; err != nil {
			return err
		}
		if err := recordAuditTx(tx, refund.AuthorizedBy, group.SupplierTenantID, "super_admin", "tenant", "team.settlement.refund.correction", "team_settlement_statement", correction.ID,
			refund.Reason, "", fmt.Sprintf(`{"refund_cents":%d,"net_cents":%d,"status":"paid"}`, delta, -delta)); err != nil {
			return err
		}
	}
	return nil
}

func teamVerifiedSettlementAmounts(tx *gorm.DB, group *model.TourGroup) (int64, int64, error) {
	var fulfillments []model.FulfillmentOrder
	if err := tx.Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", group.SalesOrderID, group.SupplierTenantID, group.ScenicAreaID).Find(&fulfillments).Error; err != nil {
		return 0, 0, err
	}
	var gross, refund int64
	for i := range fulfillments {
		fulfillmentGross, fulfillmentRefund, _, err := verifiedSettlementAmountsForFulfillment(tx, &fulfillments[i])
		if err != nil {
			return 0, 0, err
		}
		gross += fulfillmentGross
		refund += fulfillmentRefund
	}
	return gross, refund, nil
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
