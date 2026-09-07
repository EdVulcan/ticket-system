package main

import (
	"context"
	"errors"
	"fmt"
	"ticket-backend/internal/service"
	"ticket-backend/pkg/logger"
	"time"
)

func runXiaohongshuProductAuditWorker(ctx context.Context) {
	svc := service.NewXiaohongshuProductAuditService()
	process := func() {
		batchContext, cancel := context.WithTimeout(ctx, 50*time.Second)
		defer cancel()
		if _, err := svc.ProcessProductAuditRefreshes(batchContext, 20); err != nil && !errors.Is(err, context.Canceled) {
			logger.Log.Error(fmt.Sprintf("xiaohongshu product audit reconciliation failed: %v", err))
		}
	}
	process()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}
