package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultPaymentTaskLimit = 20
	stalePaymentTaskAfter   = 5 * time.Minute
)

// enqueuePaymentTask creates the durable query task after a provider payment
// request has been accepted. The unique payment_id constraint makes retries
// safe even if the caller repeats this operation.
func (s *PaymentService) enqueuePaymentTask(payment *model.Payment) error {
	if payment == nil || payment.ID == 0 || payment.Status != "pending" {
		return nil
	}
	return model.Write(func(tx *gorm.DB) error {
		var existing model.PaymentReconciliationTask
		err := tx.Where("payment_id = ?", payment.ID).First(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&model.PaymentReconciliationTask{
			TenantID:  payment.TenantID,
			PaymentID: payment.ID,
			PaymentNo: payment.PaymentNo,
			Status:    "pending",
			NextRunAt: time.Now(),
		}).Error
	})
}

// EnsurePaymentReconciliationTasks repairs the small crash window between a
// pending payment being stored and its task being written. It is intentionally
// idempotent and only schedules providers that support active order queries.
func (s *PaymentService) EnsurePaymentReconciliationTasks(now time.Time) error {
	return model.Write(func(tx *gorm.DB) error {
		var payments []model.Payment
		if err := tx.Where("status = ? AND method IN ?", "pending", []string{"wechat", "alipay"}).Find(&payments).Error; err != nil {
			return err
		}
		for i := range payments {
			var count int64
			if err := tx.Model(&model.PaymentReconciliationTask{}).Where("payment_id = ?", payments[i].ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&model.PaymentReconciliationTask{
				TenantID: payments[i].TenantID, PaymentID: payments[i].ID,
				PaymentNo: payments[i].PaymentNo, Status: "pending", NextRunAt: now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ProcessPaymentReconciliationTasks claims due tasks one at a time, performs
// the provider query outside the write transaction, and records a bounded
// exponential retry. A processing task left behind by a crash is reclaimable
// after stalePaymentTaskAfter.
func (s *PaymentService) ProcessPaymentReconciliationTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = defaultPaymentTaskLimit
	}
	if err := s.EnsurePaymentReconciliationTasks(now); err != nil {
		return 0, err
	}
	processed := 0
	for processed < limit {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		task, err := claimPaymentTask(now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		processed++
		if err := s.processPaymentTask(task); err != nil {
			if rescheduleErr := reschedulePaymentTask(task, err, now); rescheduleErr != nil {
				return processed, rescheduleErr
			}
		}
	}
	return processed, nil
}

func claimPaymentTask(now time.Time) (*model.PaymentReconciliationTask, error) {
	var task model.PaymentReconciliationTask
	err := model.Write(func(tx *gorm.DB) error {
		staleBefore := now.Add(-stalePaymentTaskAfter)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("(status = ? AND next_run_at <= ?) OR (status = ? AND locked_at IS NOT NULL AND locked_at <= ?)",
				"pending", now, "processing", staleBefore).
			Order("next_run_at asc, id asc").First(&task).Error; err != nil {
			return err
		}
		lockedAt := now
		return tx.Model(&task).Updates(map[string]interface{}{
			"status": "processing", "attempts": gorm.Expr("attempts + 1"), "locked_at": lockedAt,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	task.Attempts++
	return &task, nil
}

func (s *PaymentService) processPaymentTask(task *model.PaymentReconciliationTask) error {
	var payment model.Payment
	if err := model.DB.Where("id = ? AND tenant_id = ?", task.PaymentID, task.TenantID).First(&payment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return completePaymentTask(task.ID, "payment not found")
		}
		return err
	}
	if payment.Status != "pending" {
		return completePaymentTask(task.ID, "")
	}
	if err := s.refreshProviderStatus(&payment); err != nil {
		return err
	}
	switch payment.Status {
	case "paid":
		if err := s.completePayment(&payment); err != nil {
			return err
		}
		return completePaymentTask(task.ID, "")
	case "failed":
		if payment.Purpose == exchangeDifferencePurpose {
			if err := model.Write(func(tx *gorm.DB) error { return failExchangeDifferencePaymentTx(tx, &payment, payment.ErrorMessage) }); err != nil {
				return err
			}
			return completePaymentTask(task.ID, payment.ErrorMessage)
		}
		if s.OrderService != nil {
			if err := s.OrderService.Cancel(payment.OrderNo, payment.TenantID); err != nil {
				return err
			}
		}
		return completePaymentTask(task.ID, "")
	default:
		return fmt.Errorf("provider payment remains pending")
	}
}

func completePaymentTask(taskID uint, message string) error {
	return model.Write(func(tx *gorm.DB) error {
		updates := map[string]interface{}{"status": "completed", "locked_at": nil}
		if message != "" {
			updates["last_error"] = message
		}
		return tx.Model(&model.PaymentReconciliationTask{}).Where("id = ?", taskID).Updates(updates).Error
	})
}

func reschedulePaymentTask(task *model.PaymentReconciliationTask, cause error, now time.Time) error {
	attempt := task.Attempts
	if attempt < 1 {
		attempt = 1
	}
	// 2s, 4s ... up to 30m. The task remains durable and observable while a
	// provider is unavailable, rather than blocking the SQLite writer.
	seconds := math.Pow(2, float64(minInt(attempt, 14)))
	delay := time.Duration(seconds) * time.Second
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	nextRunAt := now.Add(delay)
	message := cause.Error()
	return model.Write(func(tx *gorm.DB) error {
		return tx.Model(&model.PaymentReconciliationTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
			"status": "pending", "next_run_at": nextRunAt, "locked_at": nil, "last_error": message,
		}).Error
	})
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
