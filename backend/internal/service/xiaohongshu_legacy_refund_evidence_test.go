package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func legacyRefundSnapshot(t *testing.T, f xiaohongshuRefundFixture) model.XiaohongshuOrderOperation {
	t.Helper()
	var op model.XiaohongshuOrderOperation
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ?", f.tenantID, f.account.ID).First(&op).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(op.RequestPayloadCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the real old writer: the field is absent, not an explicit zero.
	raw, err := json.Marshal(map[string]interface{}{"request": payload.Request})
	if err != nil {
		t.Fatal(err)
	}
	op.RequestPayloadCiphertext, err = utils.EncryptAES(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&op).Update("request_payload_ciphertext", op.RequestPayloadCiphertext).Error; err != nil {
		t.Fatal(err)
	}
	return op
}

func TestXiaohongshuLegacyRefundMissingEvidenceIsSpecificAndAtomic(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	op := legacyRefundSnapshot(t, f)
	_, err := (&RefundService{}).CreateMixedRefundAs(RefundActor{TenantID: f.tenantID}, f.order.OrderNo, "legacy-missing", f.order.TotalAmount, []string{f.ticket.TicketCode}, "游客取消")
	if err == nil || !strings.Contains(err.Error(), "缺少商品类型快照") {
		t.Fatalf("want missing legacy evidence, got %v", err)
	}
	for _, table := range []interface{}{&model.Refund{}, &model.DigitalRefundTask{}, &model.XiaohongshuRefundOperation{}} {
		var count int64
		if err := model.DB.Model(table).Where("tenant_id = ?", f.tenantID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("rejection created financial work: count=%d err=%v", count, err)
		}
	}
	var ticket model.Ticket
	if err := model.DB.First(&ticket, f.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.PendingRefundID != 0 || ticket.Status != "unused" {
		t.Fatal("rejection changed ticket")
	}
	var original model.XiaohongshuOrderOperation
	if err := model.DB.First(&original, op.ID).Error; err != nil {
		t.Fatal(err)
	}
	if original.RequestPayloadCiphertext != op.RequestPayloadCiphertext {
		t.Fatal("original snapshot changed")
	}
}

type legacyRefundEvidenceFixture struct {
	refund    xiaohongshuRefundFixture
	original  model.XiaohongshuOrderOperation
	configure model.AuditLog
	sync      model.AuditLog
	initial   model.User
	ordinary  model.User
	input     LegacyXiaohongshuEvidenceInput
}

// seedLegacyRefundEvidenceFixture creates historical configuration evidence
// without changing today's config. The repair must rely on these immutable
// records, rather than infer a type from the current product configuration.
func seedLegacyRefundEvidenceFixture(t *testing.T, productType int) legacyRefundEvidenceFixture {
	t.Helper()
	f := seedXiaohongshuRefundFixture(t)
	original := legacyRefundSnapshot(t, f)

	var mapping model.ChannelProductMapping
	if err := model.DB.Where("channel_account_id = ?", f.account.ID).First(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Microsecond)
	configureAt, syncAt, orderAt := base.Add(time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour)
	if err := model.DB.Model(&mapping).Updates(map[string]interface{}{"created_at": base, "updated_at": base}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Order{}).Where("id = ?", f.order.ID).Updates(map[string]interface{}{"created_at": orderAt, "updated_at": orderAt}).Error; err != nil {
		t.Fatal(err)
	}

	initial := model.User{TenantID: f.tenantID, Username: "legacy-refund-initial", Password: "test", Role: "super_admin", IsInitialAdmin: true}
	ordinary := model.User{TenantID: f.tenantID, Username: "legacy-refund-ordinary", Password: "test", Role: "admin"}
	if err := model.DB.Create(&initial).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}

	configInput := XiaohongshuProductConfigInput{
		ExternalSKUID: "XHS-REFUND-SKU", CategoryID: "ticket", ImageURL: "https://example.com/refund.png",
		Description: "退款测试门票", ProductPath: "/pages/product", OrderPath: "/pages/order",
		ProductType: productType, SettleType: 1,
	}
	configJSON, err := json.Marshal(configInput)
	if err != nil {
		t.Fatal(err)
	}
	configure := model.AuditLog{
		Base: model.Base{CreatedAt: configureAt, UpdatedAt: configureAt}, ActorUserID: initial.ID, ActorRole: "super_admin",
		Scope: "tenant", TenantID: f.tenantID, Action: "xiaohongshu.product.configure", TargetType: "channel_product_mapping", TargetID: mapping.ID,
		Reason: "historical product configuration", AfterJSON: string(configJSON),
	}
	sync := model.AuditLog{
		Base: model.Base{CreatedAt: syncAt, UpdatedAt: syncAt}, ActorUserID: initial.ID, ActorRole: "super_admin",
		Scope: "tenant", TenantID: f.tenantID, Action: "xiaohongshu.product.sync", TargetType: "channel_product_mapping", TargetID: mapping.ID,
		Reason: "historical production sync", AfterJSON: "production",
	}
	if err := model.DB.Create(&configure).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&sync).Error; err != nil {
		t.Fatal(err)
	}
	return legacyRefundEvidenceFixture{
		refund: f, original: original, configure: configure, sync: sync, initial: initial, ordinary: ordinary,
		input: LegacyXiaohongshuEvidenceInput{
			TenantID: f.tenantID, ChannelAccountID: f.account.ID, OrderNo: f.order.OrderNo,
			ConfigureAuditID: configure.ID, SyncAuditID: sync.ID, Reason: "核对历史发布记录后确认普通团购券",
			ApprovalReference: "LEGACY-REFUND-APPROVAL-001", ApproverUserID: initial.ID, Executor: "root",
		},
	}
}

func legacyRefundEvidenceCounts(t *testing.T, tenantID uint) (audits, refunds, tasks, operations int64) {
	t.Helper()
	for _, target := range []struct {
		model interface{}
		out   *int64
	}{
		{&model.AuditLog{}, &audits}, {&model.Refund{}, &refunds}, {&model.DigitalRefundTask{}, &tasks}, {&model.XiaohongshuRefundOperation{}, &operations},
	} {
		if err := model.DB.Model(target.model).Where("tenant_id = ?", tenantID).Count(target.out).Error; err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestXiaohongshuLegacyRefundEvidenceCheckApplyAndManualRefund(t *testing.T) {
	e := seedLegacyRefundEvidenceFixture(t, xiaohongshu.ProductTypeGroupVoucher)
	beforeAudit, beforeRefund, beforeTask, beforeOperation := legacyRefundEvidenceCounts(t, e.refund.tenantID)

	crossTenant := e.input
	crossTenant.TenantID++
	if _, err := CheckLegacyXiaohongshuRefundEvidence(crossTenant); err == nil {
		t.Fatal("cross-tenant evidence check was accepted")
	}
	nonInitial := e.input
	nonInitial.ApproverUserID = e.ordinary.ID
	if _, err := CheckLegacyXiaohongshuRefundEvidence(nonInitial); err == nil {
		t.Fatal("ordinary administrator approved legacy repair")
	}
	plan, err := CheckLegacyXiaohongshuRefundEvidence(e.input)
	if err != nil || plan.Digest == "" {
		t.Fatalf("check plan=%+v err=%v", plan, err)
	}
	if audits, refunds, tasks, operations := legacyRefundEvidenceCounts(t, e.refund.tenantID); audits != beforeAudit || refunds != beforeRefund || tasks != beforeTask || operations != beforeOperation {
		t.Fatalf("check/rejection wrote state: audits=%d refunds=%d tasks=%d operations=%d", audits, refunds, tasks, operations)
	}

	if _, err := ApplyLegacyXiaohongshuRefundEvidence(e.input, "wrong-digest"); err == nil {
		t.Fatal("apply accepted a digest that was not checked")
	}
	applyInput := e.input
	applied, err := ApplyLegacyXiaohongshuRefundEvidence(applyInput, plan.Digest)
	if err != nil || applied.AuditID == 0 || applied.Digest != plan.Digest {
		t.Fatalf("apply plan=%+v checked=%+v err=%v", applied, plan, err)
	}
	replayed, err := ApplyLegacyXiaohongshuRefundEvidence(applyInput, plan.Digest)
	if err != nil || replayed.AuditID != applied.AuditID || replayed.Digest != applied.Digest {
		t.Fatalf("matching apply was not idempotent: replay=%+v err=%v", replayed, err)
	}
	if audits, refunds, tasks, operations := legacyRefundEvidenceCounts(t, e.refund.tenantID); audits != beforeAudit+1 || refunds != 0 || tasks != 0 || operations != beforeOperation {
		t.Fatalf("attestation wrote more than its audit: audits=%d refunds=%d tasks=%d operations=%d", audits, refunds, tasks, operations)
	}
	var attestation model.AuditLog
	if err := model.DB.First(&attestation, applied.AuditID).Error; err != nil {
		t.Fatal(err)
	}
	if attestation.Action != "xiaohongshu.order.legacy_type_confirmed" || attestation.TargetType != "xiaohongshu_order_operation" || attestation.TargetID != e.original.ID || attestation.ActorUserID == e.initial.ID ||
		!strings.Contains(attestation.AfterJSON, "root") || !strings.Contains(attestation.AfterJSON, e.input.ApprovalReference) || !strings.Contains(attestation.AfterJSON, fmt.Sprintf("%d", e.initial.ID)) {
		t.Fatalf("attestation must record the CLI executor and independent initial-admin approval: %+v", attestation)
	}
	var original model.XiaohongshuOrderOperation
	if err := model.DB.First(&original, e.original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if original.RequestPayloadCiphertext != e.original.RequestPayloadCiphertext {
		t.Fatal("legacy confirmation rewrote the original encrypted snapshot")
	}

	customer := xiaohongshuRefundCustomer(t, e.refund)
	projection, err := NewMiniappService().GetXiaohongshuOrder(t.Context(), &customer, e.refund.order.OrderNo)
	if err != nil || projection.CanApplyRefund || projection.RefundApplicationMessage == "" {
		t.Fatalf("legacy repair must not open the miniapp refund path: projection=%+v err=%v", projection, err)
	}
	if _, err := NewMiniappService().ApplyXiaohongshuRefund(&customer, e.refund.order.OrderNo, MiniappRefundApplicationInput{ClientRequestID: "legacy-customer", Reason: "游客申请"}); err == nil {
		t.Fatal("legacy confirmation opened an automatic miniapp refund")
	}

	if err := model.DB.Model(&model.OrderItem{}).Where("order_id = ?", e.refund.order.ID).Update("refund_type", "no_refund").Error; err != nil {
		t.Fatal(err)
	}
	withoutOverride := RefundActor{TenantID: e.refund.tenantID, UserID: e.initial.ID}
	if _, err := (&RefundService{}).CreateMixedRefundAs(withoutOverride, e.refund.order.OrderNo, "legacy-policy-rejected", e.refund.order.TotalAmount, []string{e.refund.ticket.TicketCode}, "历史确认后人工退款"); err == nil {
		t.Fatal("legacy confirmation bypassed explicit no-refund policy override")
	}
	actor := withoutOverride
	actor.OverrideRefundPolicy = true
	refund, err := (&RefundService{}).CreateMixedRefundAs(actor, e.refund.order.OrderNo, "legacy-policy-approved", e.refund.order.TotalAmount, []string{e.refund.ticket.TicketCode}, "历史确认后人工退款")
	if err != nil || refund.Status != "pending" {
		t.Fatalf("manual refund after confirmed evidence=%+v err=%v", refund, err)
	}
	fake, server := newXiaohongshuRefundFake(t, 2, false)
	defer server.Close()
	worker := xiaohongshuRefundServiceForTest(t, server)
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Second))
	runXiaohongshuRefundWorker(t, worker, time.Now().Add(time.Minute))
	if err := model.DB.First(refund, refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" || fake.addCalls.Load() != 1 || fake.getCalls.Load() != 1 {
		t.Fatalf("manual refund must retain the existing provider chain: refund=%+v add=%d get=%d", refund, fake.addCalls.Load(), fake.getCalls.Load())
	}
}

func TestXiaohongshuLegacyRefundEvidenceRejectsIncompleteOrChangedHistory(t *testing.T) {
	t.Run("current_config_is_not_attestation", func(t *testing.T) {
		f := seedXiaohongshuRefundFixture(t)
		legacyRefundSnapshot(t, f)
		initial := model.User{TenantID: f.tenantID, Username: "legacy-current-config-initial", Password: "test", Role: "super_admin", IsInitialAdmin: true}
		if err := model.DB.Create(&initial).Error; err != nil {
			t.Fatal(err)
		}
		input := LegacyXiaohongshuEvidenceInput{TenantID: f.tenantID, ChannelAccountID: f.account.ID, OrderNo: f.order.OrderNo, Reason: "current config", ApprovalReference: "CURRENT-CONFIG", ApproverUserID: initial.ID, Executor: "root"}
		if _, err := CheckLegacyXiaohongshuRefundEvidence(input); err == nil {
			t.Fatal("current type-1 config authorized a legacy order without historical attestations")
		}
	})
	t.Run("non_group_historical_product", func(t *testing.T) {
		e := seedLegacyRefundEvidenceFixture(t, xiaohongshu.ProductTypePresaleVoucher)
		if _, err := CheckLegacyXiaohongshuRefundEvidence(e.input); err == nil {
			t.Fatal("non-group historical product type was accepted")
		}
	})
	t.Run("original_cipher_changed_after_check", func(t *testing.T) {
		e := seedLegacyRefundEvidenceFixture(t, xiaohongshu.ProductTypeGroupVoucher)
		plan, err := CheckLegacyXiaohongshuRefundEvidence(e.input)
		if err != nil {
			t.Fatal(err)
		}
		cipher, err := utils.EncryptAES(`{"unexpected":"replacement"}`)
		if err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&model.XiaohongshuOrderOperation{}).Where("id = ?", e.original.ID).Update("request_payload_ciphertext", cipher).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyLegacyXiaohongshuRefundEvidence(e.input, plan.Digest); err == nil {
			t.Fatal("apply accepted a changed original snapshot")
		}
	})
	t.Run("source_audit_changed_after_check", func(t *testing.T) {
		e := seedLegacyRefundEvidenceFixture(t, xiaohongshu.ProductTypeGroupVoucher)
		plan, err := CheckLegacyXiaohongshuRefundEvidence(e.input)
		if err != nil {
			t.Fatal(err)
		}
		if err := model.DB.Model(&model.AuditLog{}).Where("id = ?", e.configure.ID).Update("after_json", `{}`).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyLegacyXiaohongshuRefundEvidence(e.input, plan.Digest); err == nil {
			t.Fatal("apply accepted a changed source audit")
		}
	})
	t.Run("conflicting_or_duplicate_attestation", func(t *testing.T) {
		e := seedLegacyRefundEvidenceFixture(t, xiaohongshu.ProductTypeGroupVoucher)
		plan, err := CheckLegacyXiaohongshuRefundEvidence(e.input)
		if err != nil {
			t.Fatal(err)
		}
		applied, err := ApplyLegacyXiaohongshuRefundEvidence(e.input, plan.Digest)
		if err != nil {
			t.Fatal(err)
		}
		changedApproval := e.input
		changedApproval.ApprovalReference = "DIFFERENT-APPROVAL"
		if _, err := ApplyLegacyXiaohongshuRefundEvidence(changedApproval, plan.Digest); err == nil {
			t.Fatal("different duplicate legacy attestation was accepted")
		}
		conflict := model.AuditLog{Scope: "tenant", TenantID: e.refund.tenantID, Action: "xiaohongshu.order.legacy_type_confirmed", TargetType: "xiaohongshu_order_operation", TargetID: e.original.ID, Reason: "conflicting injected attestation", AfterJSON: `{"digest":"different"}`}
		if err := model.DB.Create(&conflict).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyLegacyXiaohongshuRefundEvidence(e.input, plan.Digest); err == nil {
			t.Fatalf("conflicting attestation was accepted after audit %d", applied.AuditID)
		}
	})
}
