package service

import (
	"context"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"
	"time"
)

func TestXiaohongshuAuditWorkerSchedulesFailuresAndResumesAfterRestart(t *testing.T) {
	resetBusinessData(t)
	_, failedAccountID, failedMappingID, failing := seedXiaohongshuAuditTarget(t, "PASS", 500)
	_, _, goodMappingID, working := seedXiaohongshuAuditTarget(t, "PASS", 0)
	var failedAccount model.ChannelAccount
	if err := model.DB.First(&failedAccount, failedAccountID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	newClient := working.Products.NewClient
	working.Products.NewClient = func(appID, secret, environment string) *xiaohongshu.Client {
		if appID == failedAccount.AppID {
			return failing.Products.NewClient(appID, secret, environment)
		}
		return newClient(appID, secret, environment)
	}
	working.Now = func() time.Time { return now }
	if processed, err := working.ProcessProductAuditRefreshes(context.Background(), 20); processed != 2 || err == nil {
		t.Fatalf("first batch processed=%d err=%v", processed, err)
	}
	for _, mappingID := range []uint{failedMappingID, goodMappingID} {
		var config model.XiaohongshuProductConfig
		if err := model.DB.Where("channel_product_mapping_id = ?", mappingID).First(&config).Error; err != nil {
			t.Fatal(err)
		}
		if config.AuditCheckedAt == nil {
			t.Fatalf("mapping %d missing persisted schedule", mappingID)
		}
		if mappingID == failedMappingID && (config.AuditStatus != "pending" || config.AuditCheckError == "") {
			t.Fatalf("failed mapping state=%+v", config)
		}
		if mappingID == goodMappingID && config.AuditStatus != "approved" {
			t.Fatalf("healthy mapping blocked by previous failure: %+v", config)
		}
	}
	// A fresh service has no process-local scheduler state; persisted timestamps
	// suppress early repeats and still make both products due after five minutes.
	restarted := XiaohongshuProductAuditService{Products: working.Products, Now: func() time.Time { return now.Add(4 * time.Minute) }}
	if processed, err := restarted.ProcessProductAuditRefreshes(context.Background(), 20); processed != 0 || err != nil {
		t.Fatalf("early restart processed=%d err=%v", processed, err)
	}
	restarted.Now = func() time.Time { return now.Add(6 * time.Minute) }
	if processed, err := restarted.ProcessProductAuditRefreshes(context.Background(), 20); processed != 2 || err == nil {
		t.Fatalf("due restart processed=%d err=%v", processed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := restarted.ProcessProductAuditRefreshes(ctx, 20); err == nil {
		t.Fatal("cancelled batch succeeded")
	}
}
