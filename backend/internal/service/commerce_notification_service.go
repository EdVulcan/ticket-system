package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCommerceNotificationInvalid = errors.New("commerce notification is invalid")

type CommerceNotificationFilter struct {
	TenantID     uint
	BusinessType string
	LocationID   uint
	UnreadOnly   bool
	AfterID      uint
	PageSize     int
}

type CommerceNotificationPage struct {
	Data     []model.CommerceMerchantNotification `json:"data"`
	Total    int64                                `json:"total"`
	Page     int                                  `json:"page"`
	PageSize int                                  `json:"page_size"`
}

type CommerceNotificationService struct {
	DB    *gorm.DB
	Clock func() time.Time
}

func (s *CommerceNotificationService) db() *gorm.DB {
	if s != nil && s.DB != nil {
		return s.DB
	}
	return model.DB
}

func (s *CommerceNotificationService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

type commercePaidOutboxPayload struct {
	TenantID         uint      `json:"tenant_id"`
	OrderID          uint      `json:"order_id"`
	OrderNo          string    `json:"order_no"`
	BusinessType     string    `json:"business_type"`
	LocationID       uint      `json:"location_id"`
	TotalAmountCents int64     `json:"total_amount_cents"`
	PaidAt           time.Time `json:"paid_at"`
}

// ProjectPending claims and projects at most limit events. It is safe to call
// repeatedly and after a process restart: each outbox key maps to one
// notification, and processing is committed only after that notification is
// inserted.
func (s *CommerceNotificationService) ProjectPending(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if ctx == nil {
		ctx = context.Background()
	}
	processed := 0
	for processed < limit {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		projected, err := s.projectOne(ctx)
		if err != nil {
			return processed, err
		}
		if !projected {
			return processed, nil
		}
		processed++
	}
	return processed, nil
}

func (s *CommerceNotificationService) projectOne(ctx context.Context) (bool, error) {
	db := s.db()
	if db == nil {
		return false, fmt.Errorf("%w: database is unavailable", ErrCommerceNotificationInvalid)
	}
	var found bool
	var projectionFailure error
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outbox model.CommerceOrderPaidOutbox
		now := s.now()
		stale := now.Add(-5 * time.Minute)
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (status = ? AND locked_at < ?)", []string{"pending", "failed"}, now, "processing", stale).
			Order("id ASC")
		if err := query.First(&outbox).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		found = true
		attempts := outbox.Attempts + 1
		if err := tx.Model(&outbox).Updates(map[string]interface{}{"status": "processing", "locked_at": now, "attempts": attempts, "last_error": ""}).Error; err != nil {
			return err
		}
		var payload commercePaidOutboxPayload
		if err := json.Unmarshal([]byte(outbox.PayloadJSON), &payload); err != nil {
			if markErr := s.failOutbox(tx, &outbox, err); markErr != nil {
				return markErr
			}
			projectionFailure = err
			return nil
		}
		if payload.TenantID != outbox.TenantID || payload.OrderID != outbox.OrderID || payload.OrderNo == "" || payload.LocationID == 0 || (payload.BusinessType != "restaurant" && payload.BusinessType != "retail") {
			cause := fmt.Errorf("%w: payment event scope is incomplete", ErrCommerceNotificationInvalid)
			if markErr := s.failOutbox(tx, &outbox, cause); markErr != nil {
				return markErr
			}
			projectionFailure = cause
			return nil
		}
		title := "新订单已支付"
		body := fmt.Sprintf("订单 %s 已支付，请及时处理", payload.OrderNo)
		if payload.BusinessType == "retail" {
			title = "新订单待发货"
			body = fmt.Sprintf("订单 %s 已支付，请及时安排发货", payload.OrderNo)
		}
		notification := &model.CommerceMerchantNotification{
			TenantID: payload.TenantID, EventKey: outbox.EventKey, EventType: outbox.EventType,
			OrderID: payload.OrderID, OrderNo: payload.OrderNo, BusinessType: payload.BusinessType,
			LocationID: payload.LocationID, Title: title, Body: body, Status: "unread",
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(notification).Error; err != nil {
			if markErr := s.failOutbox(tx, &outbox, err); markErr != nil {
				return markErr
			}
			projectionFailure = err
			return nil
		}
		return tx.Model(&outbox).Updates(map[string]interface{}{"status": "processed", "processed_at": now, "locked_at": nil, "next_attempt_at": nil}).Error
	})
	if err != nil {
		return found, err
	}
	if projectionFailure != nil {
		// The failed marker was committed so the next worker cycle can retry;
		// return the original cause for observability without rolling it back.
		return found, projectionFailure
	}
	return found, nil
}

func (s *CommerceNotificationService) failOutbox(tx *gorm.DB, outbox *model.CommerceOrderPaidOutbox, cause error) error {
	now := s.now()
	next := now.Add(time.Minute)
	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	if err := tx.Model(outbox).Updates(map[string]interface{}{"status": "failed", "locked_at": nil, "next_attempt_at": next, "last_error": message}).Error; err != nil {
		return err
	}
	return nil
}

func (s *CommerceNotificationService) List(filter CommerceNotificationFilter) (*CommerceNotificationPage, error) {
	if filter.TenantID == 0 {
		return nil, fmt.Errorf("%w: tenant is required", ErrCommerceNotificationInvalid)
	}
	var err error
	filter.BusinessType, err = normalizeCommerceNotificationBusinessType(filter.BusinessType)
	if err != nil {
		return nil, err
	}
	if filter.PageSize <= 0 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	query := s.db().Where("tenant_id = ?", filter.TenantID)
	if filter.BusinessType != "" {
		query = query.Where("business_type = ?", filter.BusinessType)
	}
	if filter.LocationID != 0 {
		query = query.Where("location_id = ?", filter.LocationID)
	}
	if filter.UnreadOnly {
		query = query.Where("status = ?", "unread")
	}
	if filter.AfterID != 0 {
		// after_id is an incremental cursor used by the merchant console: the
		// caller has already seen this notification and asks only for newer rows.
		query = query.Where("id > ?", filter.AfterID)
	}
	var total int64
	if err := query.Model(&model.CommerceMerchantNotification{}).Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []model.CommerceMerchantNotification
	order := "id DESC"
	if filter.AfterID != 0 {
		// Return new rows oldest-first so a polling client can merge them in
		// event order. The client may still sort its bounded local view.
		order = "id ASC"
	}
	if err := query.Order(order).Limit(filter.PageSize).Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []model.CommerceMerchantNotification{}
	}
	return &CommerceNotificationPage{Data: rows, Total: total, Page: 1, PageSize: filter.PageSize}, nil
}

func (s *CommerceNotificationService) UnreadCount(filter CommerceNotificationFilter) (int64, error) {
	if filter.TenantID == 0 {
		return 0, fmt.Errorf("%w: tenant is required", ErrCommerceNotificationInvalid)
	}
	var err error
	filter.BusinessType, err = normalizeCommerceNotificationBusinessType(filter.BusinessType)
	if err != nil {
		return 0, err
	}
	query := s.db().Model(&model.CommerceMerchantNotification{}).Where("tenant_id = ? AND status = ?", filter.TenantID, "unread")
	if filter.BusinessType != "" {
		query = query.Where("business_type = ?", filter.BusinessType)
	}
	if filter.LocationID != 0 {
		query = query.Where("location_id = ?", filter.LocationID)
	}
	var count int64
	return count, query.Count(&count).Error
}

func normalizeCommerceNotificationBusinessType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" && value != "restaurant" && value != "retail" {
		return "", fmt.Errorf("%w: unsupported business type", ErrCommerceNotificationInvalid)
	}
	return value, nil
}

func (s *CommerceNotificationService) MarkRead(tenantID, notificationID uint) error {
	if tenantID == 0 || notificationID == 0 {
		return fmt.Errorf("%w: notification identity is required", ErrCommerceNotificationInvalid)
	}
	now := s.now()
	result := s.db().Model(&model.CommerceMerchantNotification{}).
		Where("id = ? AND tenant_id = ? AND status = ?", notificationID, tenantID, "unread").
		Updates(map[string]interface{}{"status": "read", "read_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var exists model.CommerceMerchantNotification
		if err := s.db().Where("id = ? AND tenant_id = ?", notificationID, tenantID).First(&exists).Error; err != nil {
			return err
		}
	}
	return nil
}
