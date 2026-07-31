package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
)

// CoreChannelAdapter is the local adapter used by channel accounts whose
// upstream already owns payment. It translates channel requests into the
// platform's reservation/order facts. It deliberately does not implement a
// provider refund: a real channel adapter must own that external protocol.
type CoreChannelAdapter struct {
	Workflow     *ChannelWorkflowService
	OrderService *OrderService
}

func NewCoreChannelAdapter() *CoreChannelAdapter {
	return &CoreChannelAdapter{
		Workflow:     &ChannelWorkflowService{OrderService: &OrderService{}},
		OrderService: &OrderService{},
	}
}

func (a *CoreChannelAdapter) Code() string { return "core" }

func (a *CoreChannelAdapter) CreateReservation(_ context.Context, req ChannelReservationRequest) (ChannelReservationResponse, error) {
	if a == nil || a.Workflow == nil {
		return ChannelReservationResponse{}, errors.New("core channel workflow is not configured")
	}
	if req.TenantID == 0 || req.AccountID == 0 || strings.TrimSpace(req.ExternalProductCode) == "" {
		return ChannelReservationResponse{}, errors.New("channel tenant, account and mapped product are required")
	}
	var mapping model.ChannelProductMapping
	if err := model.DB.Where("channel_account_id = ? AND external_code = ? AND status = ?", req.AccountID, strings.TrimSpace(req.ExternalProductCode), "active").First(&mapping).Error; err != nil {
		return ChannelReservationResponse{}, errors.New("external product is not mapped")
	}
	reservation, err := a.Workflow.Reserve(req.TenantID, req.AccountID, req.Channel, mapping.ProductID, req.ExternalNo, req.Quantity, req.UseDate, req.StockSlot, req.TTL)
	if err != nil {
		return ChannelReservationResponse{}, err
	}
	return ChannelReservationResponse{ReservationID: reservation.ID, ExpiresAt: reservation.ExpiresAt}, nil
}

func (a *CoreChannelAdapter) ConfirmOrder(_ context.Context, req ChannelConfirmRequest) (ChannelOrderResponse, error) {
	if a == nil || a.Workflow == nil {
		return ChannelOrderResponse{}, errors.New("core channel workflow is not configured")
	}
	order, err := a.Workflow.Confirm(req.TenantID, req.AccountID, req.Channel, req.ReservationID, req.ContactName, req.ContactPhone)
	if err != nil {
		return ChannelOrderResponse{}, err
	}
	return channelOrderResponse(order), nil
}

func (a *CoreChannelAdapter) QueryOrder(_ context.Context, req ChannelQueryRequest) (ChannelOrderResponse, error) {
	if a == nil || a.OrderService == nil {
		return ChannelOrderResponse{}, errors.New("core order service is not configured")
	}
	if req.TenantID == 0 || req.AccountID == 0 || strings.TrimSpace(req.ExternalNo) == "" {
		return ChannelOrderResponse{}, errors.New("channel tenant, account and external order are required")
	}
	var order model.Order
	if err := model.DB.Preload("Items").Preload("Items.Tickets").Where("tenant_id = ? AND channel_account_id = ? AND channel = ? AND external_no = ?", req.TenantID, req.AccountID, req.Channel, strings.TrimSpace(req.ExternalNo)).First(&order).Error; err != nil {
		return ChannelOrderResponse{}, err
	}
	return channelOrderResponse(&order), nil
}

func (a *CoreChannelAdapter) ReleaseReservation(_ context.Context, req ChannelReleaseRequest) error {
	if a == nil || a.Workflow == nil {
		return errors.New("core channel workflow is not configured")
	}
	return a.Workflow.Release(req.TenantID, req.AccountID, req.ReservationID, req.Reason)
}

func (a *CoreChannelAdapter) CancelOrder(_ context.Context, req ChannelCancelRequest) error {
	if a == nil || a.OrderService == nil {
		return errors.New("core order service is not configured")
	}
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ? AND channel = ? AND external_no = ?", req.TenantID, req.AccountID, req.Channel, strings.TrimSpace(req.ExternalNo)).First(&order).Error; err != nil {
		return err
	}
	if order.Status != "unpaid" {
		return errors.New("paid channel orders require the channel refund workflow")
	}
	return a.OrderService.Cancel(order.OrderNo, req.TenantID)
}

func (a *CoreChannelAdapter) RefundOrder(_ context.Context, req ChannelRefundRequest) (ChannelRefundResponse, error) {
	return ChannelRefundResponse{}, fmt.Errorf("channel %s refund adapter is not configured", req.Channel)
}

func channelOrderResponse(order *model.Order) ChannelOrderResponse {
	response := ChannelOrderResponse{Order: order, Status: "", ExternalNo: ""}
	if order == nil {
		return response
	}
	response.Status = order.Status
	if order.ExternalNo != nil {
		response.ExternalNo = *order.ExternalNo
	}
	for _, item := range order.Items {
		for _, ticket := range item.Tickets {
			response.TicketCodes = append(response.TicketCodes, ticket.TicketCode)
		}
	}
	return response
}
