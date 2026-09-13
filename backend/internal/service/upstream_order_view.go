package service

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"ticket-backend/internal/model"
	"time"
)

type UpstreamOrderView struct {
	ProductName          string     `json:"product_name"`
	ProviderOrderCode    string     `json:"provider_order_code"`
	ExternalProductCode  string     `json:"external_product_code"`
	IssueStatus          string     `json:"issue_status"`
	ProviderStatus       string     `json:"provider_status"`
	CancelStatus         string     `json:"cancel_status"`
	LastSyncedAt         *time.Time `json:"last_synced_at"`
	FirstUsedAt          *time.Time `json:"first_used_at"`
	ProviderFirstUsedAt  *time.Time `json:"provider_first_used_at"`
	LocalFirstUsedAt     *time.Time `json:"local_first_used_at"`
	LastError            string     `json:"last_error"`
	RefundID             uint       `json:"refund_id"`
	RequiresConfirmation bool       `json:"requires_confirmation"`
	CanRecoverFunding    bool       `json:"can_recover_funding"`
	CanRecoverIssuance   bool       `json:"can_recover_issuance"`
}

func populateOrderUpstreamFlag(order *model.Order) error {
	var count int64
	if err := model.DB.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ? AND sales_tenant_id = ? AND mode = 'upstream'", order.ID, order.TenantID).Count(&count).Error; err != nil {
		return err
	}
	order.HasUpstreamSupply = count > 0
	return nil
}

func GetUpstreamOrderView(tenantID uint, orderNo string) ([]UpstreamOrderView, error) {
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, orderNo).First(&order).Error; err != nil {
		return nil, err
	}
	var snapshots []model.OrderItemSupplySnapshot
	if err := model.DB.Where("sales_tenant_id = ? AND order_id = ? AND mode = 'upstream'", tenantID, order.ID).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	rows := make([]UpstreamOrderView, 0, len(snapshots))
	for _, s := range snapshots {
		var item model.OrderItem
		if err := model.DB.Where("id = ? AND order_id = ?", s.OrderItemID, order.ID).First(&item).Error; err != nil {
			return nil, err
		}
		row := UpstreamOrderView{ProductName: item.ProductName, ProviderOrderCode: s.ProviderOrderCode, ExternalProductCode: s.ExternalProductCode, IssueStatus: s.IssueStatus, ProviderStatus: s.ProviderStatus, CancelStatus: s.CancelStatus, LastSyncedAt: s.LastSyncedAt, ProviderFirstUsedAt: s.ProviderFirstUsedAt, FirstUsedAt: s.ProviderFirstUsedAt, LastError: s.LastError}
		if s.CancelStatus == "succeeded" && s.ProviderOrderCode != "" {
			row.ProviderStatus = "refunded"
		}
		// This is a supplier-status projection, not a completion of the funds
		// workflow. Special refunds never imply that the supplier cancelled.
		if row.ProviderStatus == "refunded" && (s.CancelStatus == "submitted" || s.CancelStatus == "pending") {
			row.CancelStatus = "succeeded"
		}
		var record model.CheckInRecord
		row.RefundID = s.RefundID
		row.CanRecoverIssuance = s.IssueStatus == "pending" && s.CancelStatus == "" && s.RefundID == 0 && s.IssueAttemptedAt != nil && s.LastError != "" && (s.LockedAt == nil || s.LockedAt.Before(time.Now().Add(-2*time.Minute)))
		if s.RefundID != 0 {
			if s.CancelStatus == "succeeded" || s.CancelStatus == "override" {
				var refund model.Refund
				if err := model.DB.Where("id = ? AND tenant_id = ?", s.RefundID, tenantID).First(&refund).Error; err != nil {
					return nil, err
				}
				row.CanRecoverFunding = refund.Status == "failed" || refund.Status == "group_manual_review"
				if row.CanRecoverFunding {
					row.LastError = "款项退款待处理；票券继续锁定"
				}
			}
			var tasks []model.DigitalRefundTask
			if err := model.DB.Where("tenant_id = ? AND (refund_id = ? OR refund_id IN (SELECT id FROM refunds WHERE tenant_id = ? AND parent_refund_id = ?)) AND status = 'manual_review'", tenantID, s.RefundID, tenantID, s.RefundID).Find(&tasks).Error; err != nil {
				return nil, err
			}
			for _, task := range tasks {
				if task.FailureCode == "upstream_confirmation_required" {
					row.RequiresConfirmation = true
					row.LastError = "供应商状态需管理员确认，退款中的票券已锁定"
				}
			}
		}
		err := model.DB.Where("tenant_id = ? AND scenic_area_id = ? AND result = 'success' AND reversed_at IS NULL", s.FulfillmentTenantID, s.ScenicAreaID).
			Where("ticket_id IN (SELECT id FROM tickets WHERE order_id = ? AND order_item_id = ? AND tenant_id = ?)", order.ID, item.ID, tenantID).Order("check_in_time ASC").First(&record).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			row.LocalFirstUsedAt = &record.CheckInTime
			if row.FirstUsedAt == nil || record.CheckInTime.Before(*row.FirstUsedAt) {
				row.FirstUsedAt = &record.CheckInTime
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Refresh requests are read-only at the provider. Issuing and cancellation
// remain owned by their workers; a browser refresh cannot resend either.
func RefreshUpstreamOrder(ctx context.Context, tenantID uint, orderNo string) error {
	var order model.Order
	if err := model.DB.Where("tenant_id = ? AND order_no = ?", tenantID, orderNo).First(&order).Error; err != nil {
		return err
	}
	var snapshots []model.OrderItemSupplySnapshot
	if err := model.DB.Where("sales_tenant_id = ? AND order_id = ? AND mode = 'upstream'", tenantID, order.ID).Find(&snapshots).Error; err != nil {
		return err
	}
	for _, s := range snapshots {
		if s.IssueStatus != "ready" {
			continue
		}
		var c model.UpstreamConnection
		if err := model.DB.Where("id = ? AND tenant_id = ? AND provider = ? AND environment = ?", s.ConnectionID, s.FulfillmentTenantID, s.Provider, s.Environment).First(&c).Error; err != nil {
			return err
		}
		client, err := newUpstreamClient(c)
		if err != nil {
			return err
		}
		if s.LockedAt != nil {
			continue
		}
		// Reuse the periodic status parser without changing issuance/cancellation.
		if err = (&UpstreamSupplyWorker{}).syncStatus(ctx, client, &s, &order); err != nil {
			return err
		}
	}
	return nil
}
