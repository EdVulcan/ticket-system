package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChannelWorkflowService struct {
	OrderService *OrderService
}

func (s *ChannelWorkflowService) Reserve(tenantID, accountID uint, channel string, productID uint, externalNo string, quantity int, useDate *time.Time, stockSlot string, ttl time.Duration) (*model.ChannelReservation, error) {
	if tenantID == 0 || accountID == 0 || productID == 0 || strings.TrimSpace(externalNo) == "" || quantity <= 0 {
		return nil, errors.New("channel, product, external order and quantity are required")
	}
	if ttl <= 0 || ttl > 30*time.Minute {
		ttl = 10 * time.Minute
	}
	if useDate != nil {
		date := startOfDay(*useDate)
		useDate = &date
	}
	var reservation model.ChannelReservation
	err := model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND status IN ?", accountID, tenantID, []string{"active", "sandbox"}).First(&account).Error; err != nil {
			return errors.New("channel account is unavailable")
		}
		var existing model.ChannelReservation
		if err := tx.Where("channel_account_id = ? AND external_no = ?", accountID, strings.TrimSpace(externalNo)).First(&existing).Error; err == nil {
			if existing.ProductID != productID || existing.Quantity != quantity || !sameOptionalDate(existing.UseDate, useDate) || existing.StockSlot != strings.TrimSpace(stockSlot) {
				return errors.New("external reservation conflicts with existing data")
			}
			reservation = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var listing model.Product
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND status = ?", productID, tenantID, "online").First(&listing).Error; err != nil {
			return errors.New("product is unavailable")
		}
		fulfillment, _, err := resolveFulfillmentProduct(tx, &listing, tenantID, channel)
		if err != nil {
			return err
		}
		if err := applyValidity(&model.OrderItem{UseDate: useDate, StockSlot: stockSlot}, fulfillment); err != nil {
			return err
		}
		probe := &model.OrderItem{UseDate: useDate, StockSlot: strings.TrimSpace(stockSlot)}
		if err := validateTimeSlot(fulfillment.TimeSlotConfig, probe); err != nil {
			return err
		}
		if err := reserveStock(tx, fulfillment, useDate, probe.StockSlot, quantity); err != nil {
			return err
		}
		reservation = model.ChannelReservation{
			TenantID: tenantID, ChannelAccountID: accountID, ExternalNo: strings.TrimSpace(externalNo), ProductID: productID,
			UseDate: useDate, StockSlot: probe.StockSlot, Quantity: quantity, Status: "held", ExpiresAt: time.Now().Add(ttl),
		}
		return tx.Create(&reservation).Error
	})
	if err != nil {
		return nil, err
	}
	return &reservation, nil
}

func (s *ChannelWorkflowService) Confirm(tenantID, accountID uint, channel string, reservationID uint, contactName, contactPhone string) (*model.Order, error) {
	if s.OrderService == nil {
		s.OrderService = &OrderService{}
	}
	var reservation model.ChannelReservation
	if err := model.DB.Where("id = ? AND tenant_id = ? AND channel_account_id = ?", reservationID, tenantID, accountID).First(&reservation).Error; err != nil {
		return nil, err
	}
	if reservation.Status == "converted" && reservation.OrderNo != "" {
		return s.OrderService.GetByOrderNo(reservation.OrderNo, tenantID)
	}
	if reservation.Status != "held" || !time.Now().Before(reservation.ExpiresAt) {
		return nil, errors.New("channel reservation is expired or unavailable")
	}
	externalNo := reservation.ExternalNo
	order := model.Order{TenantID: tenantID, ContactName: strings.TrimSpace(contactName), ContactPhone: strings.TrimSpace(contactPhone), Channel: channel, ChannelAccountID: accountID, ExternalNo: &externalNo, ChannelReservationID: reservation.ID,
		Items: []model.OrderItem{{ProductID: reservation.ProductID, Quantity: reservation.Quantity, UseDate: reservation.UseDate, StockSlot: reservation.StockSlot}}}
	if err := s.OrderService.Create(&order); err != nil {
		return nil, err
	}
	if err := s.OrderService.MarkAsPaid(order.OrderNo, tenantID); err != nil {
		return nil, err
	}
	order.Status = "paid"
	return &order, nil
}

func (s *ChannelWorkflowService) Release(tenantID, accountID, reservationID uint, reason string) error {
	return model.Write(func(tx *gorm.DB) error {
		var reservation model.ChannelReservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND channel_account_id = ?", reservationID, tenantID, accountID).First(&reservation).Error; err != nil {
			return err
		}
		if reservation.Status != "held" {
			return nil
		}
		var listing model.Product
		if err := tx.Unscoped().Where("id = ? AND tenant_id = ?", reservation.ProductID, tenantID).First(&listing).Error; err != nil {
			return err
		}
		fulfillment, _, err := resolveFulfillmentProduct(tx, &listing, tenantID, "ota")
		if err != nil {
			// A retired listing still has enough ownership data to release its
			// reservation; use the stored source projection as a fallback.
			if listing.FulfillmentProductID == 0 || listing.FulfillmentTenantID == 0 {
				return err
			}
			fulfillment = &model.Product{Base: model.Base{ID: listing.FulfillmentProductID}, TenantID: listing.FulfillmentTenantID, StockType: listing.StockType, Name: listing.Name, DailyStock: listing.DailyStock}
		}
		if err := releaseStock(tx, fulfillment, reservation.UseDate, reservation.StockSlot, reservation.Quantity); err != nil {
			return err
		}
		return tx.Model(&reservation).Updates(map[string]interface{}{"status": "released", "order_no": "", "updated_at": time.Now()}).Error
	})
}

func (s *ChannelWorkflowService) Expire(now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	count := 0
	for count < limit {
		var reservation model.ChannelReservation
		err := model.DB.Where("status = ? AND expires_at <= ?", "held", now).Order("id").First(&reservation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		if err := s.Release(reservation.TenantID, reservation.ChannelAccountID, reservation.ID, "expired"); err != nil {
			return count, fmt.Errorf("release reservation %d: %w", reservation.ID, err)
		}
		_ = model.Write(func(tx *gorm.DB) error {
			return tx.Model(&reservation).Where("status = ?", "released").Update("status", "expired").Error
		})
		count++
	}
	return count, nil
}
