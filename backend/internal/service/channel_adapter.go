package service

import (
	"context"
	"errors"
	"ticket-backend/internal/model"
	"time"
)

// ChannelAdapter is the contract every external channel implementation must
// satisfy. The application owns inventory, order, payment and entitlement
// facts; adapters only translate the channel's protocol into these operations.
type ChannelAdapter interface {
	Code() string
	CreateReservation(context.Context, ChannelReservationRequest) (ChannelReservationResponse, error)
	ConfirmOrder(context.Context, ChannelConfirmRequest) (ChannelOrderResponse, error)
	QueryOrder(context.Context, ChannelQueryRequest) (ChannelOrderResponse, error)
	CancelOrder(context.Context, ChannelCancelRequest) error
	RefundOrder(context.Context, ChannelRefundRequest) (ChannelRefundResponse, error)
}

type ChannelReservationRequest struct {
	AccountID           uint
	ExternalNo          string
	ExternalProductCode string
	Quantity            int
	UseDate             *time.Time
	StockSlot           string
}

type ChannelReservationResponse struct {
	ReservationID uint
	ExpiresAt     time.Time
}

type ChannelConfirmRequest struct {
	ReservationID uint
	ContactName   string
	ContactPhone  string
}

type ChannelQueryRequest struct {
	ExternalNo string
}

type ChannelCancelRequest struct {
	ExternalNo string
	Reason     string
}

type ChannelRefundRequest struct {
	ExternalNo  string
	AmountCents int64
	Reason      string
}

type ChannelOrderResponse struct {
	Order       *model.Order
	ExternalNo  string
	Status      string
	TicketCodes []string
}

type ChannelRefundResponse struct {
	Status           string
	ProviderRefundNo string
}

// ChannelAdapterRegistry keeps protocol implementations out of controllers.
// A missing adapter is an explicit configuration error, never a fake success.
type ChannelAdapterRegistry struct {
	adapters map[string]ChannelAdapter
}

func NewChannelAdapterRegistry(adapters ...ChannelAdapter) *ChannelAdapterRegistry {
	registry := &ChannelAdapterRegistry{adapters: make(map[string]ChannelAdapter, len(adapters))}
	for _, adapter := range adapters {
		if adapter != nil && adapter.Code() != "" {
			registry.adapters[adapter.Code()] = adapter
		}
	}
	return registry
}

func (r *ChannelAdapterRegistry) Register(adapter ChannelAdapter) error {
	if r == nil || adapter == nil || adapter.Code() == "" {
		return errors.New("channel adapter and code are required")
	}
	if _, exists := r.adapters[adapter.Code()]; exists {
		return errors.New("channel adapter is already registered")
	}
	r.adapters[adapter.Code()] = adapter
	return nil
}

func (r *ChannelAdapterRegistry) Get(code string) (ChannelAdapter, error) {
	if r == nil {
		return nil, errors.New("channel adapter registry is not configured")
	}
	adapter, ok := r.adapters[code]
	if !ok {
		return nil, errors.New("channel adapter is not configured")
	}
	return adapter, nil
}
