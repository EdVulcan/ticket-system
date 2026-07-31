package service

import (
	"context"
	"errors"
	"strings"
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
	ReleaseReservation(context.Context, ChannelReleaseRequest) error
	CancelOrder(context.Context, ChannelCancelRequest) error
	RefundOrder(context.Context, ChannelRefundRequest) (ChannelRefundResponse, error)
}

type ChannelReservationRequest struct {
	TenantID            uint
	AccountID           uint
	Channel             string
	ExternalNo          string
	ExternalProductCode string
	Quantity            int
	UseDate             *time.Time
	StockSlot           string
	TTL                 time.Duration
}

type ChannelReservationResponse struct {
	ReservationID uint
	ExpiresAt     time.Time
}

type ChannelConfirmRequest struct {
	TenantID      uint
	AccountID     uint
	Channel       string
	ReservationID uint
	ContactName   string
	ContactPhone  string
}

type ChannelQueryRequest struct {
	TenantID   uint
	AccountID  uint
	Channel    string
	ExternalNo string
}

type ChannelCancelRequest struct {
	TenantID   uint
	AccountID  uint
	Channel    string
	ExternalNo string
	Reason     string
}

type ChannelReleaseRequest struct {
	TenantID      uint
	AccountID     uint
	Channel       string
	ReservationID uint
	Reason        string
}

type ChannelRefundRequest struct {
	TenantID    uint
	AccountID   uint
	Channel     string
	ExternalNo  string
	AmountCents int64
	Reason      string
}

type ChannelCreateOrderRequest struct {
	TenantID            uint
	AccountID           uint
	Channel             string
	ExternalNo          string
	ExternalProductCode string
	Quantity            int
	UseDate             *time.Time
	StockSlot           string
	ContactName         string
	ContactPhone        string
	TTL                 time.Duration
}

// ChannelGatewayService resolves the account's configured adapter and keeps
// protocol controllers independent from order, inventory, and refund facts.
// A missing adapter is a configuration error; callers must not fall back to a
// fake success response.
type ChannelGatewayService struct {
	Registry *ChannelAdapterRegistry
}

func (s *ChannelGatewayService) adapter(accountType string) (ChannelAdapter, error) {
	if s == nil || s.Registry == nil {
		return nil, errors.New("channel adapter registry is not configured")
	}
	adapter, err := s.Registry.Get(strings.TrimSpace(accountType))
	if err == nil {
		return adapter, nil
	}
	// These values describe the platform's built-in channel protocol, not an
	// external vendor. Vendor names must have an explicitly registered adapter.
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "", "ota", "native", "test", "sandbox":
		return s.Registry.Get("core")
	default:
		return nil, err
	}
}

func (s *ChannelGatewayService) CreateReservation(ctx context.Context, accountType string, req ChannelReservationRequest) (ChannelReservationResponse, error) {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return ChannelReservationResponse{}, err
	}
	return adapter.CreateReservation(ctx, req)
}

// CreateOrder is the one-call channel contract. The reservation is durable;
// if confirmation fails, the expiry worker owns its eventual release and a
// caller can safely retry with the same external number.
func (s *ChannelGatewayService) CreateOrder(ctx context.Context, accountType string, req ChannelCreateOrderRequest) (ChannelOrderResponse, error) {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return ChannelOrderResponse{}, err
	}
	reservation, err := adapter.CreateReservation(ctx, ChannelReservationRequest{
		TenantID: req.TenantID, AccountID: req.AccountID, Channel: req.Channel, ExternalNo: req.ExternalNo,
		ExternalProductCode: req.ExternalProductCode, Quantity: req.Quantity, UseDate: req.UseDate,
		StockSlot: req.StockSlot, TTL: req.TTL,
	})
	if err != nil {
		return ChannelOrderResponse{}, err
	}
	return adapter.ConfirmOrder(ctx, ChannelConfirmRequest{
		TenantID: req.TenantID, AccountID: req.AccountID, Channel: req.Channel, ReservationID: reservation.ReservationID,
		ContactName: req.ContactName, ContactPhone: req.ContactPhone,
	})
}

func (s *ChannelGatewayService) ConfirmOrder(ctx context.Context, accountType string, req ChannelConfirmRequest) (ChannelOrderResponse, error) {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return ChannelOrderResponse{}, err
	}
	return adapter.ConfirmOrder(ctx, req)
}

func (s *ChannelGatewayService) QueryOrder(ctx context.Context, accountType string, req ChannelQueryRequest) (ChannelOrderResponse, error) {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return ChannelOrderResponse{}, err
	}
	return adapter.QueryOrder(ctx, req)
}

func (s *ChannelGatewayService) CancelOrder(ctx context.Context, accountType string, req ChannelCancelRequest) error {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return err
	}
	return adapter.CancelOrder(ctx, req)
}

func (s *ChannelGatewayService) ReleaseReservation(ctx context.Context, accountType string, req ChannelReleaseRequest) error {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return err
	}
	return adapter.ReleaseReservation(ctx, req)
}

func (s *ChannelGatewayService) RefundOrder(ctx context.Context, accountType string, req ChannelRefundRequest) (ChannelRefundResponse, error) {
	adapter, err := s.adapter(accountType)
	if err != nil {
		return ChannelRefundResponse{}, err
	}
	return adapter.RefundOrder(ctx, req)
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
