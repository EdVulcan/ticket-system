package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"sync"
	"testing"
	"time"

	"ticket-backend/internal/model"
)

type memberCouponFixture struct {
	tenant   uint
	account  model.ChannelAccount
	template *model.CommerceCouponTemplate
	service  *MemberCouponGrantService
	members  []uint
	now      time.Time
}

func newMemberCouponFixture(t *testing.T, count int) memberCouponFixture {
	t.Helper()
	tenant := newMemberServiceTenant(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, business := range []string{"restaurant", "retail"} {
		if err := model.DB.Create(&model.TenantBusinessCapability{TenantID: tenant, BusinessType: business, Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	account := model.ChannelAccount{TenantID: tenant, Code: fmt.Sprintf("member-coupon-%d", time.Now().UnixNano()), Type: "wechat_miniapp", Status: "active", MemberMode: model.ChannelMemberModeFirstParty}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	member := newMemberServiceForTest(t)
	service := &MemberCouponGrantService{DB: model.DB, Member: member, Clock: func() time.Time { return now }}
	promo := &CommercePromotionService{DB: model.DB, Clock: service.Clock}
	template, err := promo.CreateCouponTemplate(tenant, CreateCommerceCouponTemplateInput{ChannelAccountID: account.ID, BusinessTypes: []string{"restaurant", "retail"}, Name: "Member welcome", DiscountCents: 200, MinGoodsSubtotalCents: 1000, ValidDays: 3, Status: "active", PerCustomerCap: 3})
	if err != nil {
		t.Fatal(err)
	}
	f := memberCouponFixture{tenant: tenant, account: account, template: template, service: service, now: now}
	for i := 0; i < count; i++ {
		subject := fmt.Sprintf("member-subject-%d-%d", tenant, i)
		row, err := member.ResolveSelfHostedIdentity(SelfHostedIdentityInput{TenantID: tenant, ChannelAccountID: account.ID, Provider: "wechat_miniapp", Subject: subject, DisplayName: fmt.Sprintf("Member %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err = model.DB.Model(row).Update("membership_status", "active").Error; err != nil {
			t.Fatal(err)
		}
		// This historical token is deliberately expired/revoked: identity, not token
		// freshness, proves the member link and fixed shop wallet.
		session := model.CommerceCustomerSession{TenantID: tenant, ChannelAccountID: account.ID, MemberID: &row.ID, SubjectHash: storefrontHash(subject), TokenHash: storefrontHash("token:" + subject), ExpiresAt: now.Add(-time.Hour), Status: "revoked"}
		if err = model.DB.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		f.members = append(f.members, row.ID)
	}
	return f
}
func (f memberCouponFixture) input(key string) MemberCouponGrantIssueInput {
	return MemberCouponGrantIssueInput{TemplateID: f.template.ID, MemberIDs: append([]uint(nil), f.members...), IdempotencyKey: key, Reason: "会员关怀", ActorUserID: 88, ActorRole: "admin"}
}
func memberCouponCount(t *testing.T, tenant uint, table interface{}) int64 {
	t.Helper()
	var count int64
	if err := model.DB.Model(table).Where("tenant_id = ?", tenant).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMemberCouponGrantRejectsUnsafeRecipientsAndChannels(t *testing.T) {
	for _, kind := range []string{"cross_tenant", "frozen", "provisional", "withdrawn", "revoked_identity", "missing_identity", "multiple_identities", "missing_session", "ambiguous_session", "empty_session", "wrong_account", "disabled_member_mode", "disabled_account", "other_provider", "duplicate_member"} {
		t.Run(kind, func(t *testing.T) {
			f := newMemberCouponFixture(t, 1)
			input := f.input("negative")
			memberUpdate := map[string]interface{}{}
			var mutation *gorm.DB
			switch kind {
			case "cross_tenant":
				input.MemberIDs = []uint{f.members[0] + 999999}
			case "frozen":
				memberUpdate["status"] = "frozen"
			case "provisional":
				memberUpdate["membership_status"] = "provisional"
			case "withdrawn":
				memberUpdate["membership_status"] = "withdrawn"
			case "revoked_identity":
				mutation = model.DB.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ?", f.tenant).Update("status", "revoked")
			case "missing_identity":
				mutation = model.DB.Where("tenant_id = ?", f.tenant).Delete(&model.TenantMemberIdentity{})
			case "multiple_identities":
				mutation = model.DB.Create(&model.TenantMemberIdentity{TenantID: f.tenant, MemberID: f.members[0], ChannelAccountID: f.account.ID, Provider: "wechat_miniapp", SubjectBlindIndex: "second", Status: "active", LinkMethod: "first_login", LinkedAt: f.now, LastSeenAt: f.now})
			case "missing_session":
				mutation = model.DB.Where("tenant_id = ?", f.tenant).Unscoped().Delete(&model.CommerceCustomerSession{})
			case "ambiguous_session":
				mutation = model.DB.Create(&model.CommerceCustomerSession{TenantID: f.tenant, ChannelAccountID: f.account.ID, MemberID: &f.members[0], SubjectHash: strings.Repeat("a", 64), TokenHash: fmt.Sprint("second-", f.tenant), ExpiresAt: f.now, Status: "expired"})
			case "empty_session":
				mutation = model.DB.Model(&model.CommerceCustomerSession{}).Where("tenant_id = ?", f.tenant).Update("subject_hash", "")
			case "wrong_account":
				mutation = model.DB.Model(&model.ChannelAccount{}).Where("id = ?", f.account.ID).Update("tenant_id", f.tenant+999999)
			case "disabled_member_mode":
				mutation = model.DB.Model(&model.ChannelAccount{}).Where("id = ?", f.account.ID).Update("member_mode", "disabled")
			case "disabled_account":
				mutation = model.DB.Model(&model.ChannelAccount{}).Where("id = ?", f.account.ID).Update("status", "disabled")
			case "other_provider":
				mutation = model.DB.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ?", f.tenant).Update("provider", "xiaohongshu_miniapp")
			case "duplicate_member":
				input.MemberIDs = append(input.MemberIDs, input.MemberIDs...)
			}
			if len(memberUpdate) > 0 {
				mutation = model.DB.Model(&model.TenantMember{}).Where("id = ?", f.members[0]).Updates(memberUpdate)
			}
			if mutation != nil && mutation.Error != nil {
				t.Fatal(mutation.Error)
			}
			preview, err := f.service.Preview(context.Background(), f.tenant, input.TemplateID, input.MemberIDs)
			if err == nil && preview.Eligible {
				t.Fatalf("unsafe preview accepted: %+v", preview)
			}
			if _, err = f.service.Issue(context.Background(), f.tenant, input); err == nil {
				t.Fatal("unsafe issue accepted")
			}
			if memberCouponCount(t, f.tenant, &model.CommerceCouponGrant{}) != 0 || memberCouponCount(t, f.tenant, &model.MemberCouponGrantBatch{}) != 0 {
				t.Fatal("rejected attempt left grants or receipt")
			}
		})
	}
	if _, err := (&MemberCouponGrantService{DB: model.DB}).Preview(context.Background(), 1, 1, []uint{1}); !errors.Is(err, ErrMemberCouponGrantUnavailable) {
		t.Fatalf("missing member dependency: %v", err)
	}
}

func TestMemberCouponGrantOptionalReasonDefaultsAndReplays(t *testing.T) {
	f := newMemberCouponFixture(t, 1)
	input := f.input("optional-reason")
	input.Reason = ""
	first, err := f.service.Issue(context.Background(), f.tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"  \t\n", "会员优惠券发放"} {
		input.Reason = reason
		retry, err := f.service.Issue(context.Background(), f.tenant, input)
		if err != nil || retry.Items[0].GrantID != first.Items[0].GrantID {
			t.Fatalf("default reason replay: %+v, %v", retry, err)
		}
	}
	var batch model.MemberCouponGrantBatch
	if err := model.DB.Where("tenant_id = ?", f.tenant).First(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if batch.Reason != "会员优惠券发放" || memberCouponCount(t, f.tenant, &model.CommerceCouponGrant{}) != 1 {
		t.Fatalf("default reason or duplicate grant: %+v", batch)
	}
}

func TestMemberCouponGrantSnapshotsWalletScopeAndReplaysAfterChanges(t *testing.T) {
	f := newMemberCouponFixture(t, 2)
	ctx := context.Background()
	preview, err := f.service.Preview(ctx, f.tenant, f.template.ID, f.members)
	if err != nil || !preview.Eligible {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if memberCouponCount(t, f.tenant, &model.CommerceCouponGrant{}) != 0 {
		t.Fatal("preview issued grants")
	}
	input := f.input("stable-batch")
	input.MemberIDs = []uint{f.members[1], f.members[0]}
	result, err := f.service.Issue(ctx, f.tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || len(result.Items) != 2 || result.MemberCount != 2 {
		t.Fatalf("receipt=%+v", result)
	}
	var grants []model.CommerceCouponGrant
	if err = model.DB.Where("tenant_id = ?", f.tenant).Find(&grants).Error; err != nil {
		t.Fatal(err)
	}
	promo := &CommercePromotionService{DB: model.DB, Clock: f.service.Clock}
	for _, grant := range grants {
		if grant.Source != "manual" || grant.DiscountCents != 200 || grant.MinGoodsSubtotalCents != 1000 || !grant.ExpiresAt.Equal(f.now.AddDate(0, 0, 3)) {
			t.Fatalf("snapshot=%+v", grant)
		}
		for _, business := range []string{"restaurant", "retail"} {
			wallet, err := promo.ListAvailableCoupons(f.tenant, f.account.ID, business, grant.CustomerID)
			if err != nil || len(wallet) != 1 {
				t.Fatalf("wallet scope %s: %v %v", business, wallet, err)
			}
		}
		if err = model.DB.Transaction(func(tx *gorm.DB) error {
			_, err := promo.ReserveCouponGrantTx(tx, f.tenant, 999, grant.ID, grant.CustomerID, "retail", f.account.ID+1, 2000)
			if !errors.Is(err, ErrCouponNotOwned) && !errors.Is(err, ErrPromotionScopeDenied) {
				return fmt.Errorf("cross account reservation: %v", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = model.DB.Model(&model.CommerceCouponTemplate{}).Where("id = ?", f.template.ID).Update("status", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	if err = model.DB.Model(&model.TenantMember{}).Where("tenant_id = ?", f.tenant).Update("status", "frozen").Error; err != nil {
		t.Fatal(err)
	}
	input.MemberIDs = f.members
	retry, err := f.service.Issue(ctx, f.tenant, input)
	if err != nil || retry.Items[0].GrantID != result.Items[0].GrantID {
		t.Fatalf("replay after disable: %+v %v", retry, err)
	}
	got, err := f.service.Get(ctx, f.tenant, input.IdempotencyKey)
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("get=%+v %v", got, err)
	}
	if _, err = f.service.Get(ctx, f.tenant+999999, input.IdempotencyKey); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross tenant get=%v", err)
	}
	input.Reason = "different intent"
	if _, err = f.service.Issue(ctx, f.tenant, input); !errors.Is(err, ErrMemberCouponGrantConflict) {
		t.Fatalf("payload conflict=%v", err)
	}
}

func TestMemberCouponGrantMergedAliasWalletAndDuplicateAliasRejection(t *testing.T) {
	f := newMemberCouponFixture(t, 1)
	alias := model.TenantMember{TenantID: f.tenant, MemberNo: fmt.Sprintf("ALIAS-%d", f.tenant), Status: "merged", MembershipStatus: "provisional", MergedIntoMemberID: &f.members[0], FirstSeenAt: f.now, LastSeenAt: f.now}
	if err := model.DB.Create(&alias).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.TenantMemberIdentity{}).Where("tenant_id = ?", f.tenant).Update("member_id", alias.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.CommerceCustomerSession{}).Where("tenant_id = ?", f.tenant).Update("member_id", alias.ID).Error; err != nil {
		t.Fatal(err)
	}
	input := f.input("alias-wallet")
	result, err := f.service.Issue(context.Background(), f.tenant, input)
	if err != nil || result.Items[0].MemberID != f.members[0] {
		t.Fatalf("alias wallet=%+v %v", result, err)
	}
	input.IdempotencyKey = "alias-duplicate"
	input.MemberIDs = append(input.MemberIDs, alias.ID)
	if _, err = f.service.Issue(context.Background(), f.tenant, input); !errors.Is(err, ErrMemberCouponGrantEligibility) {
		t.Fatalf("alias duplicate accepted: %v", err)
	}
	// Existing alias-first lifecycle locking must not deadlock issuance's sorted
	// graph locks. A conflicting held alias causes an immediate safe retry.
	tx := model.DB.Begin()
	defer tx.Rollback()
	var locked model.TenantMember
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, alias.ID).Error; err != nil {
		t.Fatal(err)
	}
	input = f.input("alias-concurrent-freeze")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = f.service.Issue(ctx, f.tenant, input); !errors.Is(err, ErrMemberCouponGrantEligibility) {
		t.Fatalf("locked alias should fail fast: %v", err)
	}
}

func TestMemberCouponGrantAtomicAuditRollbackAndCapsConcurrency(t *testing.T) {
	f := newMemberCouponFixture(t, 2)
	failure := errors.New("audit unavailable")
	callback := "test:member-coupon-audit-failure"
	if err := model.DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_logs" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Issue(context.Background(), f.tenant, f.input("rollback"))
	model.DB.Callback().Create().Remove(callback)
	if !errors.Is(err, failure) {
		t.Fatalf("audit failure=%v", err)
	}
	for _, table := range []interface{}{&model.MemberCouponGrantBatch{}, &model.MemberCouponGrantBatchItem{}, &model.CommerceCouponGrant{}, &model.CommerceCouponGrantBusinessType{}} {
		if memberCouponCount(t, f.tenant, table) != 0 {
			t.Fatalf("partial issuance: %T", table)
		}
	}
	if err = model.DB.Model(&model.CommerceCouponTemplate{}).Where("id = ?", f.template.ID).Update("issuance_cap", 2).Error; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := f.service.Issue(context.Background(), f.tenant, f.input(fmt.Sprintf("cap-%d", i)))
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrMemberCouponGrantEligibility) {
			t.Fatal(err)
		}
	}
	if success != 1 || memberCouponCount(t, f.tenant, &model.CommerceCouponGrant{}) != 2 || memberCouponCount(t, f.tenant, &model.MemberCouponGrantBatch{}) != 1 {
		t.Fatalf("cap concurrency successes=%d", success)
	}
}

func TestMemberCouponGrantUsesExistingCheckoutAndPerCustomerCaps(t *testing.T) {
	f := newMemberCouponFixture(t, 1)
	ctx := context.Background()
	if err := model.DB.Model(&model.CommerceCouponTemplate{}).Where("id = ?", f.template.ID).Update("per_customer_cap", 1).Error; err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Issue(ctx, f.tenant, f.input("one-per-customer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Issue(ctx, f.tenant, f.input("second-attempt")); !errors.Is(err, ErrMemberCouponGrantEligibility) {
		t.Fatalf("customer cap bypass: %v", err)
	}
	var grant model.CommerceCouponGrant
	if err = model.DB.First(&grant, result.Items[0].GrantID).Error; err != nil {
		t.Fatal(err)
	}
	location := model.CommerceFulfillmentLocation{TenantID: f.tenant, BusinessType: "retail", Name: "Member coupon warehouse", LocationType: "warehouse", Status: "active"}
	if err = model.DB.Create(&location).Error; err != nil {
		t.Fatal(err)
	}
	order := model.CommerceOrder{TenantID: f.tenant, LocationID: location.ID, OrderNo: fmt.Sprintf("MEMBER-COUPON-%d", f.tenant), BusinessType: "retail", CustomerID: grant.CustomerID, PaymentStatus: "unpaid", FulfillmentStatus: "pending_shipment", RefundStatus: "none", TotalAmountCents: 1800}
	if err = model.DB.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	promo := &CommercePromotionService{DB: model.DB, Clock: f.service.Clock}
	if err = model.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := promo.ReserveCouponGrantTx(tx, f.tenant, order.ID, grant.ID, "another-customer", "retail", f.account.ID, 2000); !errors.Is(err, ErrCouponNotOwned) {
			return fmt.Errorf("forged customer: %v", err)
		}
		reserved, err := promo.ReserveCouponGrantTx(tx, f.tenant, order.ID, grant.ID, grant.CustomerID, "retail", f.account.ID, 2000)
		if err != nil {
			return err
		}
		if reserved.Status != "reserved" {
			return fmt.Errorf("coupon not reserved")
		}
		return promo.ConsumeCouponTx(tx, f.tenant, order.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err = model.DB.First(&grant, grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	if grant.Status != "used" || grant.UsedOrderID != order.ID {
		t.Fatalf("coupon not consumed by normal checkout: %+v", grant)
	}
}

func TestMemberCouponGrantSameKeyConcurrentReplay(t *testing.T) {
	f := newMemberCouponFixture(t, 1)
	var wg sync.WaitGroup
	results := make(chan *MemberCouponGrantResult, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.service.Issue(context.Background(), f.tenant, f.input("same-key"))
			if err != nil {
				failures <- err
			} else {
				results <- result
			}
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	grantID := uint(0)
	count := 0
	for result := range results {
		count++
		if grantID != 0 && grantID != result.Items[0].GrantID {
			t.Fatal("same key issued different grants")
		}
		grantID = result.Items[0].GrantID
	}
	if count != 2 || memberCouponCount(t, f.tenant, &model.CommerceCouponGrant{}) != 1 || memberCouponCount(t, f.tenant, &model.MemberCouponGrantBatch{}) != 1 {
		t.Fatalf("same key count=%d", count)
	}
}
