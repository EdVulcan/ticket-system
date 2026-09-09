package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type callbackRaceProvider func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error)

func (p callbackRaceProvider) Process(ctx context.Context, r *model.Refund, payment *model.Payment) (RefundProviderResult, error) {
	return p(ctx, r, payment)
}

func TestRefundCallbackDuringQueryPreservesLeaseAndPromptFollowup(t *testing.T) {
	for _, outcome := range []string{"pending", "transient", "mismatch", "succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			f := seedXiaohongshuRefundFixture(t)
			r := createXiaohongshuRefund(t, f, "inflight-"+outcome)
			status := 1
			if outcome == "succeeded" {
				status = 2
			}
			if outcome == "failed" {
				status = 3
			}
			fake, server := newXiaohongshuRefundFake(t, status, false)
			defer server.Close()
			svc := xiaohongshuRefundServiceForTest(t, server)
			runXiaohongshuRefundWorker(t, svc, time.Now().Add(time.Second))
			if err := model.DB.Model(&model.DigitalRefundTask{}).Where("refund_id = ?", r.ID).Update("next_attempt_at", time.Now()).Error; err != nil {
				t.Fatal(err)
			}
			base := &xiaohongshuRefundProvider{newClient: svc.NewXiaohongshuClient}
			started, release := make(chan struct{}), make(chan struct{})
			svc.Provider = callbackRaceProvider(func(ctx context.Context, refund *model.Refund, payment *model.Payment) (RefundProviderResult, error) {
				result, err := base.Process(ctx, refund, payment)
				close(started)
				<-release
				if outcome == "transient" {
					return RefundProviderResult{}, errors.New("temporary connection failure")
				}
				if outcome == "mismatch" {
					return RefundProviderResult{}, errXiaohongshuRefundMismatch
				}
				return result, err
			})
			done := make(chan error, 1)
			go func() { _, err := svc.ProcessDigitalRefundTasks(context.Background(), time.Now(), 1); done <- err }()
			<-started
			var before, after model.DigitalRefundTask
			if err := model.DB.Where("refund_id = ?", r.ID).First(&before).Error; err != nil {
				close(release)
				<-done
				t.Fatal(err)
			}
			event := model.XiaohongshuWebhookEvent{TenantID: f.tenantID, ChannelAccountID: f.account.ID, PayloadHash: "inflight-" + outcome, EventType: "REFUND_RESULT", PayloadCiphertext: "authenticated-fixture", ReceivedAt: time.Now()}
			err := model.Write(func(tx *gorm.DB) error {
				if err := tx.Create(&event).Error; err != nil {
					return err
				}
				_, err := wakeXiaohongshuRefundTx(tx, &f.account, &event, []byte(fmt.Sprintf(`{"OutAfterSalesOrderId":%q,"Status":2}`, r.RefundNo)))
				return err
			})
			if err != nil {
				close(release)
				<-done
				t.Fatal(err)
			}
			if err := model.DB.First(&after, before.ID).Error; err != nil {
				close(release)
				<-done
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if after.Status != "processing" || before.LockedAt == nil || after.LockedAt == nil || !before.LockedAt.Equal(*after.LockedAt) {
				t.Fatal("callback preempted active lease")
			}
			after = model.DigitalRefundTask{}
			if err := model.DB.First(&after, before.ID).Error; err != nil {
				t.Fatal(err)
			}
			if outcome == "succeeded" || outcome == "failed" || outcome == "mismatch" {
				want := outcome
				if outcome == "mismatch" {
					want = "manual_review"
				}
				if after.Status != want || after.NextAttemptAt != nil {
					t.Fatalf("callback overrode terminal/safety outcome: %+v", after)
				}
				return
			}
			if after.NextAttemptAt == nil || after.NextAttemptAt.After(time.Now()) {
				t.Fatalf("callback hint lost during provider completion: %+v", after)
			}
			if after.AttemptCount != 0 {
				t.Fatal("active request overwrote callback retry reset")
			}
			svc.Provider = base
			runXiaohongshuRefundWorker(t, svc, time.Now())
			if fake.addCalls.Load() != 1 || fake.getCalls.Load() != 2 {
				t.Fatalf("prompt followup missing or add repeated: add=%d get=%d", fake.addCalls.Load(), fake.getCalls.Load())
			}
			after = model.DigitalRefundTask{}
			if err := model.DB.First(&after, before.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.NextAttemptAt == nil || !after.NextAttemptAt.After(time.Now().Add(20*time.Second)) {
				t.Fatal("consumed callback caused repeated immediate polling")
			}
		})
	}
}

func TestRefundStalePendingResultCannotOverwriteNewLeaseCompletion(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	r := createXiaohongshuRefund(t, f, "stale-callback-query")
	release := make(chan struct{})
	oldStarted, newStarted := make(chan struct{}), make(chan struct{})
	oldService := &RefundService{Provider: callbackRaceProvider(func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error) {
		close(oldStarted)
		<-release
		return RefundProviderResult{Status: "submitted", ProviderRefundID: "stale-result"}, nil
	})}
	newService := &RefundService{Provider: callbackRaceProvider(func(context.Context, *model.Refund, *model.Payment) (RefundProviderResult, error) {
		close(newStarted)
		<-release
		return RefundProviderResult{Status: "succeeded", ProviderRefundID: r.RefundNo}, nil
	})}
	now := time.Now().Add(time.Second)
	oldDone, newDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := oldService.ProcessDigitalRefundTasks(context.Background(), now, 1)
		oldDone <- err
	}()
	<-oldStarted
	go func() {
		_, err := newService.ProcessDigitalRefundTasks(context.Background(), now.Add(staleDigitalRefundTaskAfter+time.Second), 1)
		newDone <- err
	}()
	<-newStarted
	close(release)
	oldErr, newErr := <-oldDone, <-newDone
	if !errors.Is(oldErr, gorm.ErrRecordNotFound) || newErr != nil {
		t.Fatalf("stale lease must roll back without blocking completion: old=%v new=%v", oldErr, newErr)
	}
	var stored model.Refund
	if err := model.DB.First(&stored, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || stored.ProviderRefundID != r.RefundNo {
		t.Fatal("stale worker overwrote completed refund")
	}
}
