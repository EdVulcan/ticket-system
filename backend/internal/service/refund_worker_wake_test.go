package service

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func drainRefundWakeups() {
	for {
		select {
		case <-DigitalRefundWakeups():
		default:
			return
		}
	}
}

func TestRefundWakeNotificationsCoalesce(t *testing.T) {
	drainRefundWakeups()
	t.Cleanup(drainRefundWakeups)
	for i := 0; i < 100; i++ {
		notifyDigitalRefundWorker()
	}
	select {
	case <-DigitalRefundWakeups():
	default:
		t.Fatal("notification lost")
	}
	select {
	case <-DigitalRefundWakeups():
		t.Fatal("notifications were not coalesced")
	default:
	}
}

func TestRefundWebhookNotifiesOnlyAfterSuccessfulCommit(t *testing.T) {
	f := seedXiaohongshuRefundFixture(t)
	r := createXiaohongshuRefund(t, f, "immediate-wake")
	drainRefundWakeups()
	t.Cleanup(drainRefundWakeups)
	messageFor := func(id string) XiaohongshuWebhookMessage {
		payload := []byte(fmt.Sprintf(`{"Event":"REFUND_RESULT","OutAfterSalesOrderId":%q,"Status":2}`, id))
		encrypted := encryptXiaohongshuWebhookFixture(t, "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789", payload, f.account.AppID)
		stamp := time.Now().Unix()
		return XiaohongshuWebhookMessage{Nonce: "wake", Timestamp: stamp, Encrypt: encrypted,
			MsgSignature: xiaohongshu.MessageSignature("XiaohongshuRefundToken", strconv.FormatInt(stamp, 10), "wake", encrypted)}
	}
	message := messageFor(r.RefundNo)
	invalid := message
	invalid.MsgSignature = "invalid"
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), f.account.AppID, invalid); err == nil {
		t.Fatal("invalid signature accepted")
	}
	select {
	case <-DigitalRefundWakeups():
		t.Fatal("invalid callback woke worker")
	default:
	}
	if err := model.DB.Exec("ALTER TABLE xiaohongshu_webhook_events ADD CONSTRAINT test_wake_commit CHECK (status <> 'processed')").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		model.DB.Exec("ALTER TABLE xiaohongshu_webhook_events DROP CONSTRAINT IF EXISTS test_wake_commit")
	})
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), f.account.AppID, message); err == nil {
		t.Fatal("injected rollback ignored")
	}
	select {
	case <-DigitalRefundWakeups():
		t.Fatal("rolled-back callback woke worker")
	default:
	}
	if err := model.DB.Exec("ALTER TABLE xiaohongshu_webhook_events DROP CONSTRAINT test_wake_commit").Error; err != nil {
		t.Fatal(err)
	}
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), f.account.AppID, message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-DigitalRefundWakeups():
	default:
		t.Fatal("committed callback did not wake worker immediately")
	}
	var task model.DigitalRefundTask
	if err := model.DB.Where("refund_id = ?", r.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "submitted" || task.NextAttemptAt == nil || task.NextAttemptAt.After(time.Now()) {
		t.Fatalf("wake preceded committed task: %+v", task)
	}
	if err := model.DB.First(&r, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if r.Status != "pending" {
		t.Fatal("callback finalized money without provider query")
	}
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), f.account.AppID, message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-DigitalRefundWakeups():
		t.Fatal("duplicate inbox event woke worker again")
	default:
	}
	if err := (XiaohongshuWebhookService{}).Receive(context.Background(), f.account.AppID, messageFor("unlinked-refund")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-DigitalRefundWakeups():
		t.Fatal("unlinked refund woke worker")
	default:
	}
}
