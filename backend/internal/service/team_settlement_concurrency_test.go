package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

type teamSettlementResult struct {
	statement *model.TeamSettlementStatement
	err       error
}

type teamRefundResult struct {
	refund *model.Refund
	err    error
}

func waitForDatabaseLockWaiters(ctx context.Context, minimum int) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int64
		if err := model.DB.Raw(`
			SELECT COUNT(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
		`).Scan(&count).Error; err != nil {
			return err
		}
		if count >= int64(minimum) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func TestGenerateTeamSettlementSerializesWithConcurrentRefund(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&model.TenantCapability{
			TenantID: scenario.distributorID, Capability: "travel_agency", Status: "active",
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.DistributorRelationship{}).
			Where("agent_tenant_id = ? AND supplier_tenant_id = ?", scenario.distributorID, scenario.supplierID).
			Update("travel_status", "active").Error
	}); err != nil {
		t.Fatal(err)
	}
	order, ticket, fulfillment, group, _ := createRefundProjectionTeamOrder(t, scenario, true)
	initial := model.User{
		TenantID: scenario.supplierID, Username: "settlement-race-initial", Password: "unused",
		Role: "super_admin", IsInitialAdmin: true,
	}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}

	sqlDB, err := model.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lockConn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	var advisoryKey int64
	if err := lockConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&advisoryKey); err != nil {
		t.Fatal(err)
	}
	functionName := fmt.Sprintf("test_team_settlement_insert_barrier_%d", advisoryKey)
	triggerName := functionName + "_trigger"
	if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		_ = model.DB.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", functionName)).Error
	}()
	defer func() {
		if locked {
			_, _ = lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if err := model.DB.Exec(fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END
		$$`, functionName, advisoryKey)).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Exec(fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE INSERT ON team_settlement_statements
		FOR EACH ROW WHEN (NEW.kind = 'original')
		EXECUTE FUNCTION %s()`, triggerName, functionName)).Error; err != nil {
		t.Fatal(err)
	}

	settlementCh := make(chan teamSettlementResult, 1)
	go func() {
		statement, generateErr := (&TeamService{}).GenerateTeamSettlement(scenario.distributorID, group.ID)
		settlementCh <- teamSettlementResult{statement: statement, err: generateErr}
	}()
	if err := waitForDatabaseLockWaiters(ctx, 1); err != nil {
		t.Fatalf("settlement generation did not reach the insert barrier: %v", err)
	}

	refundCh := make(chan teamRefundResult, 1)
	go func() {
		refund, refundErr := (&RefundService{}).CreateSupplierUsedRefund(
			RefundActor{TenantID: scenario.supplierID, UserID: initial.ID}, fulfillment.ID,
			"team-settlement-race-refund", []string{ticket.TicketCode}, "并发退款回归测试",
		)
		refundCh <- teamRefundResult{refund: refund, err: refundErr}
	}()

	// With the group row locked by settlement generation, the refund becomes
	// the second database lock waiter. The old implementation instead lets the
	// refund commit before the statement exists, which this branch also records.
	var earlyRefund *teamRefundResult
	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	waitTicker := time.NewTicker(10 * time.Millisecond)
	defer waitTicker.Stop()
waitForSerialization:
	for {
		select {
		case result := <-refundCh:
			earlyRefund = &result
			break waitForSerialization
		case <-waitTicker.C:
			var waiters int64
			if err := model.DB.Raw(`
				SELECT COUNT(*) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'
			`).Scan(&waiters).Error; err != nil {
				t.Fatal(err)
			}
			if waiters >= 2 {
				break waitForSerialization
			}
		case <-waitCtx.Done():
			t.Fatalf("refund neither completed nor waited behind settlement generation: %v", waitCtx.Err())
		}
	}
	if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false

	settlementResult := <-settlementCh
	if settlementResult.err != nil {
		t.Fatalf("generate team settlement: %v", settlementResult.err)
	}
	refundResult := earlyRefund
	if refundResult == nil {
		result := <-refundCh
		refundResult = &result
	}
	if refundResult.err != nil {
		t.Fatalf("refund team ticket: %v", refundResult.err)
	}
	if refundResult.refund == nil || refundResult.refund.OrderNo != order.OrderNo {
		t.Fatalf("unexpected refund result: %+v", refundResult.refund)
	}

	var stored model.TeamSettlementStatement
	if err := model.DB.First(&stored, settlementResult.statement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.GrossCents != 6000 || stored.RefundCents != 6000 || stored.NetCents != 0 || stored.Status != "draft" {
		t.Fatalf("settlement did not include concurrent refund: %+v", stored)
	}
}

func TestConfirmTeamSettlementPaymentSerializesWithConcurrentRefund(t *testing.T) {
	resetBusinessData(t)
	scenario := seedDistributionScenario(t)
	if err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Create(&model.TenantCapability{
			TenantID: scenario.distributorID, Capability: "travel_agency", Status: "active",
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.DistributorRelationship{}).
			Where("agent_tenant_id = ? AND supplier_tenant_id = ?", scenario.distributorID, scenario.supplierID).
			Update("travel_status", "active").Error
	}); err != nil {
		t.Fatal(err)
	}
	_, ticket, fulfillment, group, _ := createRefundProjectionTeamOrder(t, scenario, true)
	initial := model.User{
		TenantID: scenario.supplierID, Username: "settlement-paid-race-initial", Password: "unused",
		Role: "super_admin", IsInitialAdmin: true,
	}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	team := &TeamService{}
	statement, err := team.GenerateTeamSettlement(scenario.distributorID, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := team.SetTeamSettlementStatus(scenario.supplierID, statement.ID, "supplier_confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := team.SetTeamSettlementStatus(scenario.distributorID, statement.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	if err := team.SetTeamSettlementStatus(scenario.distributorID, statement.ID, "payment_submitted", "并发测试付款凭证"); err != nil {
		t.Fatal(err)
	}

	sqlDB, err := model.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lockConn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	var advisoryKey int64
	if err := lockConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&advisoryKey); err != nil {
		t.Fatal(err)
	}
	functionName := fmt.Sprintf("test_team_settlement_paid_barrier_%d", advisoryKey)
	triggerName := functionName + "_trigger"
	if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		_ = model.DB.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", functionName)).Error
	}()
	defer func() {
		if locked {
			_, _ = lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey)
		}
	}()
	if err := model.DB.Exec(fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END
		$$`, functionName, advisoryKey)).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Exec(fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE UPDATE ON team_settlement_statements
		FOR EACH ROW WHEN (NEW.status = 'paid' AND OLD.status <> 'paid')
		EXECUTE FUNCTION %s()`, triggerName, functionName)).Error; err != nil {
		t.Fatal(err)
	}

	paidCh := make(chan error, 1)
	go func() {
		paidCh <- team.SetTeamSettlementStatus(scenario.supplierID, statement.ID, "paid", "确认到账")
	}()
	if err := waitForDatabaseLockWaiters(ctx, 1); err != nil {
		t.Fatalf("payment confirmation did not reach the update barrier: %v", err)
	}

	refundCh := make(chan error, 1)
	go func() {
		_, refundErr := (&RefundService{}).CreateSupplierUsedRefund(
			RefundActor{TenantID: scenario.supplierID, UserID: initial.ID}, fulfillment.ID,
			"team-paid-race-refund", []string{ticket.TicketCode}, "确认到账并发退款回归测试",
		)
		refundCh <- refundErr
	}()
	if err := waitForDatabaseLockWaiters(ctx, 2); err != nil {
		t.Fatalf("refund did not reach the settlement serialization lock: %v", err)
	}
	if _, err := lockConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey); err != nil {
		t.Fatal(err)
	}
	locked = false

	if err := <-paidCh; err != nil {
		t.Fatalf("confirm settlement payment: %v", err)
	}
	if err := <-refundCh; err != nil {
		t.Fatalf("refund after payment confirmation: %v", err)
	}
	var statements []model.TeamSettlementStatement
	if err := model.DB.Where("group_id = ?", group.ID).Order("sequence ASC").Find(&statements).Error; err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 || statements[0].Status != "paid" || statements[1].Kind != "refund_correction" || statements[1].RefundCents != 6000 || statements[1].NetCents != -6000 {
		t.Fatalf("settlement/refund serialization result=%+v", statements)
	}
}
