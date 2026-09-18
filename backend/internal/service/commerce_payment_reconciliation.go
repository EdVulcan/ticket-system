package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultCommercePaymentTaskLimit = 20
	staleCommercePaymentTaskAfter   = 5 * time.Minute
)

// CommercePaymentQuery is implemented by a trusted payment adapter. The
// adapter must authenticate the provider response before returning it. Keeping
// the query behind this callback lets the durable worker run even when a
// tenant has not configured a provider: such tasks are moved to manual review
// instead of remaining locked forever.
type CommercePaymentQuery func(context.Context, uint, *model.CommerceOrder, *model.CommercePaymentReconciliationTask) (CommercePaymentOutcome, error)

// CommercePaymentReconciliationService owns only the durable task lifecycle.
// Payment state and inventory facts are still changed by CommerceOrderService,
// which is the single transaction boundary for payment outcomes.
type CommercePaymentReconciliationService struct {
	DB    *gorm.DB
	Clock func() time.Time
	Query CommercePaymentQuery
}

func NewCommercePaymentReconciliationService(query CommercePaymentQuery) *CommercePaymentReconciliationService {
	return &CommercePaymentReconciliationService{Query: query}
}

func (s *CommercePaymentReconciliationService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommercePaymentReconciliationService) write(apply func(*gorm.DB) error) error {
	if s != nil && s.DB != nil {
		return s.DB.Transaction(apply)
	}
	return model.Write(apply)
}

func (s *CommercePaymentReconciliationService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// EnsureTasks repairs the process-restart window for pending commercial
// payments. It is safe to call repeatedly and does not alter order or stock
// state.
func (s *CommercePaymentReconciliationService) EnsureTasks(now time.Time) error {
	return s.write(func(tx *gorm.DB) error {
		var orders []model.CommerceOrder
		if err := tx.Where("payment_status = ?", "pending").Find(&orders).Error; err != nil {
			return err
		}
		for i := range orders {
			next := now
			task := model.CommercePaymentReconciliationTask{
				TenantID: orders[i].TenantID, OrderID: orders[i].ID,
				PaymentReference: orders[i].PaymentReference, Status: "pending",
				NextAttemptAt: &next,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&task).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *CommercePaymentReconciliationService) claimTask(now time.Time) (*model.CommercePaymentReconciliationTask, error) {
	var task model.CommercePaymentReconciliationTask
	err := s.write(func(tx *gorm.DB) error {
		staleBefore := now.Add(-staleCommercePaymentTaskAfter)
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"(status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = ? AND locked_at IS NOT NULL AND locked_at <= ?)",
			"pending", now, "processing", staleBefore,
		).Order("COALESCE(next_attempt_at, created_at) ASC, id ASC")
		if err := query.First(&task).Error; err != nil {
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

func (s *CommercePaymentReconciliationService) completeTask(taskID uint, message string) error {
	updates := map[string]interface{}{"status": "completed", "locked_at": nil, "next_attempt_at": nil}
	if strings.TrimSpace(message) != "" {
		updates["last_error"] = strings.TrimSpace(message)
	}
	return s.write(func(tx *gorm.DB) error {
		return tx.Model(&model.CommercePaymentReconciliationTask{}).Where("id = ?", taskID).Updates(updates).Error
	})
}

func (s *CommercePaymentReconciliationService) manualReviewTask(taskID uint, message string, now time.Time) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "商业支付渠道未配置，等待适配器或人工复核"
	}
	return s.write(func(tx *gorm.DB) error {
		return tx.Model(&model.CommercePaymentReconciliationTask{}).Where("id = ?", taskID).Updates(map[string]interface{}{
			"status": "manual_review", "locked_at": nil, "next_attempt_at": nil,
			"last_attempt_at": now, "last_error": message,
		}).Error
	})
}

func (s *CommercePaymentReconciliationService) rescheduleTask(task *model.CommercePaymentReconciliationTask, cause error, now time.Time) error {
	if task == nil {
		return errors.New("commerce payment task is nil")
	}
	attempt := task.Attempts
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(math.Pow(2, float64(minInt(attempt, 14)))) * time.Second
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	next := now.Add(delay)
	message := "商业支付对账失败"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	return s.write(func(tx *gorm.DB) error {
		return tx.Model(&model.CommercePaymentReconciliationTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
			"status": "pending", "next_attempt_at": next, "locked_at": nil,
			"last_attempt_at": now, "last_error": message,
		}).Error
	})
}

// ProcessTasks claims due tasks one at a time and performs the provider query
// outside the claim transaction. A crashed process leaves a stale processing
// row that is reclaimed on the next run.
func (s *CommercePaymentReconciliationService) ProcessTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = defaultCommercePaymentTaskLimit
	}
	if err := s.EnsureTasks(now); err != nil {
		return 0, err
	}
	processed := 0
	for processed < limit {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		task, err := s.claimTask(now)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		processed++
		var order model.CommerceOrder
		if err := s.db().Where("id = ? AND tenant_id = ?", task.OrderID, task.TenantID).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if completeErr := s.completeTask(task.ID, "商业订单不存在"); completeErr != nil {
					return processed, completeErr
				}
				continue
			}
			if rescheduleErr := s.rescheduleTask(task, err, now); rescheduleErr != nil {
				return processed, rescheduleErr
			}
			continue
		}
		if order.PaymentStatus != "pending" {
			if completeErr := s.completeTask(task.ID, "订单支付状态已收敛"); completeErr != nil {
				return processed, completeErr
			}
			continue
		}
		if s.Query == nil {
			if reviewErr := s.manualReviewTask(task.ID, "商业支付渠道未配置，等待适配器或人工复核", now); reviewErr != nil {
				return processed, reviewErr
			}
			continue
		}
		outcome, queryErr := s.Query(ctx, task.TenantID, &order, task)
		if queryErr != nil {
			if rescheduleErr := s.rescheduleTask(task, queryErr, now); rescheduleErr != nil {
				return processed, rescheduleErr
			}
			continue
		}
		_, applyErr := (&CommerceOrderService{DB: s.db(), Clock: s.Clock}).ApplyPaymentOutcome(task.TenantID, task.OrderID, outcome)
		if errors.Is(applyErr, ErrCommercePaymentManualReview) {
			continue
		}
		if applyErr != nil {
			if rescheduleErr := s.rescheduleTask(task, applyErr, now); rescheduleErr != nil {
				return processed, rescheduleErr
			}
		}
	}
	return processed, nil
}

// Retry requeues a task after an operator or a trusted adapter has resolved
// the reason it was placed in manual review.
func (s *CommercePaymentReconciliationService) Retry(tenantID, orderID uint) error {
	if tenantID == 0 || orderID == 0 {
		return ErrCommerceOrderInvalid
	}
	now := s.now()
	return s.write(func(tx *gorm.DB) error {
		var task model.CommercePaymentReconciliationTask
		if err := tx.Where("tenant_id = ? AND order_id = ?", tenantID, orderID).First(&task).Error; err != nil {
			return err
		}
		return tx.Model(&task).Updates(map[string]interface{}{"status": "pending", "next_attempt_at": now, "locked_at": nil, "last_error": "", "completed_at": nil}).Error
	})
}
