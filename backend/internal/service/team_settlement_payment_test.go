package service

import (
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

func seedTeamSettlementPaymentTest(t *testing.T, status string) (supplier model.Tenant, travel model.Tenant, supplierUser model.User, travelUser model.User, group model.TourGroup, statement model.TeamSettlementStatement) {
	t.Helper()
	resetBusinessData(t)
	err := model.Write(func(tx *gorm.DB) error {
		supplier = model.Tenant{Name: "结算测试景区", SystemCode: "TEAM-SETTLE-SUP", SecretKey: "secret", Status: "active"}
		travel = model.Tenant{Name: "结算测试旅行社", SystemCode: "TEAM-SETTLE-TRAVEL", SecretKey: "secret", Status: "active"}
		if err := tx.Create(&supplier).Error; err != nil {
			return err
		}
		if err := tx.Create(&travel).Error; err != nil {
			return err
		}
		if err := tx.Create(&[]model.TenantCapability{
			{TenantID: supplier.ID, Capability: "supplier", Status: "active"},
			{TenantID: travel.ID, Capability: "travel_agency", Status: "active"},
		}).Error; err != nil {
			return err
		}
		supplierUser = model.User{Username: "team-settle-supplier", Password: "unused", Role: "settlement_operator", TenantID: supplier.ID}
		travelUser = model.User{Username: "team-settle-travel", Password: "unused", Role: "settlement_operator", TenantID: travel.ID}
		if err := tx.Create(&supplierUser).Error; err != nil {
			return err
		}
		if err := tx.Create(&travelUser).Error; err != nil {
			return err
		}
		area := model.ScenicArea{TenantID: supplier.ID, Name: "结算测试景区", Code: "TEAM-SETTLE-AREA", Status: "active"}
		if err := tx.Create(&area).Error; err != nil {
			return err
		}
		group = model.TourGroup{
			TenantID: travel.ID, SupplierTenantID: supplier.ID, ScenicAreaID: area.ID,
			GroupNo: "TEAM-SETTLE-GROUP", Name: "结算测试团队", VisitDate: time.Now(),
			ExpectedCount: 1, Status: "entered", CreditUsedCents: 8000, SettlementStatus: "statement",
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		statement = model.TeamSettlementStatement{
			TravelTenantID: travel.ID, SupplierTenantID: supplier.ID, GroupID: group.ID,
			Sequence: 1, Kind: "original", StatementNo: "TEAM-SETTLE-STMT", IdempotencyKey: "team-settle-stmt",
			GrossCents: 8000, NetCents: 8000, Status: status,
		}
		return tx.Create(&statement).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestTeamSettlementPaymentRequiresSupplierReceiptConfirmation(t *testing.T) {
	supplier, travel, supplierUser, travelUser, group, statement := seedTeamSettlementPaymentTest(t, "confirmed")
	team := &TeamService{}

	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "paid", "银行回单-001", travelUser.ID); err == nil {
		t.Fatal("travel agency marked its own payment as received")
	}
	if err := team.SetTeamSettlementStatus(supplier.ID, statement.ID, "payment_submitted", "银行回单-001", supplierUser.ID); err == nil {
		t.Fatal("supplier submitted payment on behalf of travel agency")
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "payment_submitted", "银行回单-001", travelUser.ID); err != nil {
		t.Fatal(err)
	}

	var submitted model.TeamSettlementStatement
	if err := model.DB.First(&submitted, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if submitted.Status != "payment_submitted" || submitted.PaymentProof != "银行回单-001" || submitted.PaidAt != nil {
		t.Fatalf("submitted settlement=%+v", submitted)
	}
	if err := model.DB.First(&group, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if group.SettlementStatus == "settled" {
		t.Fatal("payment proof prematurely settled the team")
	}
	submittedAccounts, err := team.ListTeamAccountSummaries(travel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(submittedAccounts) != 1 || submittedAccounts[0].CreditUsedCents != 8000 || submittedAccounts[0].PendingCents != 8000 || submittedAccounts[0].PaidCents != 0 {
		t.Fatalf("accounts after payment submission=%+v", submittedAccounts)
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "paid", "", travelUser.ID); err == nil {
		t.Fatal("travel agency confirmed receipt of its own payment")
	}
	if err := team.SetTeamSettlementStatus(supplier.ID, statement.ID, "paid", "确认到账", supplierUser.ID); err != nil {
		t.Fatal(err)
	}

	var paid model.TeamSettlementStatement
	if err := model.DB.First(&paid, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if paid.Status != "paid" || paid.PaidAt == nil || paid.PaymentProof != "银行回单-001" {
		t.Fatalf("paid settlement=%+v", paid)
	}
	if err := model.DB.First(&group, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if group.SettlementStatus != "settled" {
		t.Fatalf("group settlement status=%s", group.SettlementStatus)
	}
	paidAccounts, err := team.ListTeamAccountSummaries(travel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(paidAccounts) != 1 || paidAccounts[0].CreditUsedCents != 0 || paidAccounts[0].PendingCents != 0 || paidAccounts[0].PaidCents != 8000 {
		t.Fatalf("accounts after supplier receipt confirmation=%+v", paidAccounts)
	}
	var audits []model.AuditLog
	if err := model.DB.Where("target_type = ? AND target_id = ?", "team_settlement_statement", statement.ID).Order("id ASC").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 || audits[0].TenantID != travel.ID || audits[1].TenantID != supplier.ID {
		t.Fatalf("payment audits=%+v", audits)
	}
}

func TestZeroPayableTeamSettlementClosesAfterBothSidesConfirm(t *testing.T) {
	_, travel, _, travelUser, group, statement := seedTeamSettlementPaymentTest(t, "supplier_confirmed")
	if err := model.DB.Model(&statement).Updates(map[string]interface{}{
		"deposit_cents": 8000, "net_cents": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&group).Updates(map[string]interface{}{
		"deposit_cents": 8000, "credit_used_cents": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}

	team := &TeamService{}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "confirmed", "", travelUser.ID); err != nil {
		t.Fatalf("confirm zero-payable settlement: %v", err)
	}
	var settled model.TeamSettlementStatement
	if err := model.DB.First(&settled, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if settled.Status != "paid" || settled.ConfirmedAt == nil || settled.PaidAt == nil || settled.PaymentProof != "" {
		t.Fatalf("zero-payable settlement=%+v", settled)
	}
	if err := model.DB.First(&group, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if group.SettlementStatus != "settled" {
		t.Fatalf("group settlement status=%s, want settled", group.SettlementStatus)
	}
	if err := team.SetTeamSettlementStatus(travel.ID, statement.ID, "payment_submitted", "虚假付款凭证", travelUser.ID); err == nil {
		t.Fatal("settled zero-payable statement accepted a payment proof")
	}
}

func TestTeamSettlementAdjustmentClearsSubmittedPaymentProof(t *testing.T) {
	supplier, _, supplierUser, _, _, statement := seedTeamSettlementPaymentTest(t, "payment_submitted")
	if err := model.DB.Model(&statement).Update("payment_proof", "旧付款凭证").Error; err != nil {
		t.Fatal(err)
	}
	team := &TeamService{}
	if err := team.SetTeamSettlementStatus(supplier.ID, statement.ID, "disputed", "到账金额不符", supplierUser.ID); err != nil {
		t.Fatal(err)
	}
	if err := team.AdjustTeamSettlement(supplier.ID, statement.ID, supplierUser.ID, -100, "扣除银行手续费"); err != nil {
		t.Fatal(err)
	}
	var adjusted model.TeamSettlementStatement
	if err := model.DB.First(&adjusted, statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if adjusted.Status != "draft" || adjusted.AdjustmentCents != -100 || adjusted.PaymentProof != "" || adjusted.DisputeReason != "" || adjusted.PaidAt != nil {
		t.Fatalf("adjusted settlement=%+v", adjusted)
	}
}
