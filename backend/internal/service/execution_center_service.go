package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"ticket-backend/internal/model"
	"time"
)

// ExecutionCenterService exposes a read-only, tenant-scoped projection of
// existing durable work. It deliberately does not create a second task table
// or change any domain state; each source remains the owner of its retry,
// approval and reconciliation workflow.
type ExecutionCenterService struct{}

type ExecutionCenterItem struct {
	Source      string    `json:"source"`
	Category    string    `json:"category"`
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	Severity    string    `json:"severity"`
	Retryable   bool      `json:"retryable"`
	ActionRoute string    `json:"action_route,omitempty"`
	ActionLabel string    `json:"action_label,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ExecutionCenterSummary struct {
	Total      int            `json:"total"`
	Critical   int            `json:"critical"`
	Warning    int            `json:"warning"`
	Info       int            `json:"info"`
	ByCategory map[string]int `json:"by_category"`
}

type ExecutionCenterView struct {
	Items       []ExecutionCenterItem  `json:"items"`
	Summary     ExecutionCenterSummary `json:"summary"`
	GeneratedAt time.Time              `json:"generated_at"`
}

// List returns at most limit items per durable source. The result is an
// operational attention list, not a replacement for any source workbench.
// Source rows are always filtered by the authenticated tenant before any
// user-facing fields are projected.
func (s *ExecutionCenterService) List(tenantID uint, category, severity string, limit int) (*ExecutionCenterView, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant is required")
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	category = strings.TrimSpace(category)
	severity = strings.TrimSpace(severity)
	items := make([]ExecutionCenterItem, 0, limit)
	appendItem := func(item ExecutionCenterItem) {
		if category != "" && item.Category != category {
			return
		}
		if severity != "" && item.Severity != severity {
			return
		}
		items = append(items, item)
	}

	var alerts []model.DeviceAlert
	if err := model.DB.Where("tenant_id = ? AND status = ?", tenantID, "open").Order("updated_at DESC").Limit(limit).Find(&alerts).Error; err != nil {
		return nil, err
	}
	for _, row := range alerts {
		appendItem(ExecutionCenterItem{Source: "device_alert", Category: "现场设备", ID: row.ID,
			Title: "设备异常", Description: strings.TrimSpace(row.Message), Status: row.Status,
			Severity: "critical", ActionRoute: "/operations?tab=alerts", ActionLabel: "处理设备告警",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var printJobs []model.PrintJob
	if err := model.DB.Where("tenant_id = ? AND status IN ?", tenantID, []string{"queued", "printing", "failed"}).Order("updated_at DESC").Limit(limit).Find(&printJobs).Error; err != nil {
		return nil, err
	}
	for _, row := range printJobs {
		severity := "warning"
		if row.Status == "printing" || row.Status == "failed" {
			severity = "critical"
		}
		description := strings.TrimSpace(row.OrderNo)
		if row.TicketCode != "" {
			description += " · " + row.TicketCode
		}
		if row.LastError != "" {
			description += " · " + row.LastError
		}
		appendItem(ExecutionCenterItem{Source: "print_job", Category: "打印", ID: row.ID,
			Title: "打印任务待处理", Description: description, Status: row.Status,
			Severity: severity, Retryable: row.Status == "queued" || row.Status == "failed",
			ActionRoute: "/operations?tab=prints", ActionLabel: "处理打印任务",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var refunds []model.DigitalRefundTask
	if err := model.DB.Where("tenant_id = ? AND status <> ?", tenantID, "succeeded").Order("updated_at DESC").Limit(limit).Find(&refunds).Error; err != nil {
		return nil, err
	}
	for _, row := range refunds {
		severity := executionSeverity(row.Status)
		if row.Status == "failed" || row.Status == "manual_review" {
			severity = "critical"
		}
		description := row.PaymentNo
		if row.LastError != "" {
			description += " · " + row.LastError
		}
		appendItem(ExecutionCenterItem{Source: "digital_refund", Category: "退款", ID: row.ID,
			Title: "数字退款任务", Description: description, Status: row.Status,
			Severity: severity, Retryable: row.Status == "failed" || row.Status == "manual_review",
			ActionRoute: "/refund-tasks", ActionLabel: "处理退款任务",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var paymentTasks []model.PaymentReconciliationTask
	if err := model.DB.Where("tenant_id = ? AND status <> ?", tenantID, "completed").Order("updated_at DESC").Limit(limit).Find(&paymentTasks).Error; err != nil {
		return nil, err
	}
	for _, row := range paymentTasks {
		description := row.PaymentNo
		if row.LastError != "" {
			description += " · " + row.LastError
		}
		appendItem(ExecutionCenterItem{Source: "payment_reconciliation", Category: "支付", ID: row.ID,
			Title: "支付结果待收敛", Description: description, Status: row.Status,
			Severity: executionSeverity(row.Status), Retryable: true,
			ActionRoute: "/online-order", ActionLabel: "查看订单",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var ctripTasks []model.CtripOutboundTask
	if err := model.DB.Where("tenant_id = ? AND status NOT IN ?", tenantID, []string{"succeeded", "completed"}).Order("updated_at DESC").Limit(limit).Find(&ctripTasks).Error; err != nil {
		return nil, err
	}
	for _, row := range ctripTasks {
		description := row.ResultMessage
		if description == "" {
			description = row.LastError
		}
		appendItem(ExecutionCenterItem{Source: "ctrip_outbound", Category: "渠道", ID: row.ID,
			Title: "携程出站同步", Description: strings.TrimSpace(description), Status: row.Status,
			Severity: executionSeverity(row.Status), Retryable: row.Status == "failed" || row.Status == "pending",
			ActionRoute: "/channels", ActionLabel: "查看渠道任务",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	// ChannelRequest has no tenant column by design; ownership comes from its
	// channel account. Keep the join explicit so an ID from another tenant can
	// never enter this projection.
	var channelRequests []model.ChannelRequest
	if err := model.DB.Model(&model.ChannelRequest{}).
		Joins("JOIN channel_accounts ON channel_accounts.id = channel_requests.channel_account_id AND channel_accounts.deleted_at IS NULL").
		Where("channel_accounts.tenant_id = ? AND channel_requests.status <> ?", tenantID, "completed").
		Order("channel_requests.updated_at DESC").Limit(limit).Find(&channelRequests).Error; err != nil {
		return nil, err
	}
	for _, row := range channelRequests {
		description := row.Endpoint
		if row.ResponseStatus != 0 {
			description += fmt.Sprintf(" · HTTP %dxx", row.ResponseStatus/100)
		}
		appendItem(ExecutionCenterItem{Source: "channel_request", Category: "渠道", ID: row.ID,
			Title: "渠道请求待处理", Description: description, Status: row.Status,
			Severity: executionSeverity(row.Status), Retryable: row.Status == "failed" || row.Status == "retryable",
			ActionRoute: "/channels", ActionLabel: "查看渠道请求",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var reconciliations []model.ChannelReconciliation
	if err := model.DB.Where("tenant_id = ? AND status = ?", tenantID, "needs_review").Order("updated_at DESC").Limit(limit).Find(&reconciliations).Error; err != nil {
		return nil, err
	}
	for _, row := range reconciliations {
		appendItem(ExecutionCenterItem{Source: "channel_reconciliation", Category: "对账", ID: row.ID,
			Title: "渠道账单需要复核", Description: row.IdempotencyKey, Status: row.Status,
			Severity: "critical", ActionRoute: "/channels", ActionLabel: "查看对账",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var bookingOps []model.XiaohongshuBookingOperation
	if err := model.DB.Where("tenant_id = ? AND status <> ?", tenantID, "completed").Order("updated_at DESC").Limit(limit).Find(&bookingOps).Error; err != nil {
		return nil, err
	}
	for _, row := range bookingOps {
		severity := executionSeverity(row.Status)
		if row.Status == "failed" || row.Status == "compensation_pending" || row.Status == "confirm_pending" {
			severity = "critical"
		}
		description := row.LastError
		if description == "" {
			description = "阶段：" + row.FailedFromStage
		}
		appendItem(ExecutionCenterItem{Source: "xiaohongshu_booking", Category: "住宿预约", ID: row.ID,
			Title: "小红书预约同步", Description: strings.TrimSpace(description), Status: row.Status,
			Severity: severity, Retryable: row.Status == "failed" || row.Status == "compensation_pending" || row.Status == "confirm_pending",
			ActionRoute: "/hotel", ActionLabel: "处理预约同步",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var orderOps []model.XiaohongshuOrderOperation
	if err := model.DB.Where("tenant_id = ? AND status <> ?", tenantID, "completed").Order("updated_at DESC").Limit(limit).Find(&orderOps).Error; err != nil {
		return nil, err
	}
	for _, row := range orderOps {
		description := row.LastError
		if description == "" {
			description = "等待小红书订单状态收尾"
		}
		appendItem(ExecutionCenterItem{Source: "xiaohongshu_order", Category: "渠道", ID: row.ID,
			Title: "小红书订单同步", Description: description, Status: row.Status,
			Severity: executionSeverity(row.Status), Retryable: true,
			ActionRoute: "/channels", ActionLabel: "查看渠道任务",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var afterSales []model.AfterSaleRequest
	if err := model.DB.Where("tenant_id = ? AND status IN ?", tenantID, []string{"pending", "processing", "failed"}).Order("updated_at DESC").Limit(limit).Find(&afterSales).Error; err != nil {
		return nil, err
	}
	for _, row := range afterSales {
		severity := executionSeverity(row.Status)
		if row.Status == "failed" {
			severity = "critical"
		}
		description := row.OrderNo
		if row.ErrorMessage != "" {
			description += " · " + row.ErrorMessage
		}
		appendItem(ExecutionCenterItem{Source: "after_sale", Category: "售后", ID: row.ID,
			Title: "售后请求待处理", Description: description, Status: row.Status,
			Severity: severity, ActionRoute: "/after-sales", ActionLabel: "进入售后工作台",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	var settlements []model.SettlementStatement
	if err := model.DB.Where("(supplier_tenant_id = ? OR distributor_tenant_id = ?) AND status = ?", tenantID, tenantID, "disputed").Order("updated_at DESC").Limit(limit).Find(&settlements).Error; err != nil {
		return nil, err
	}
	for _, row := range settlements {
		appendItem(ExecutionCenterItem{Source: "settlement", Category: "结算", ID: row.ID,
			Title: "结算单存在争议", Description: row.StatementNo + " · " + row.DisputeReason, Status: row.Status,
			Severity: "critical", ActionRoute: "/operations?tab=settlements", ActionLabel: "查看结算",
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}

	sort.SliceStable(items, func(i, j int) bool {
		left, right := executionSeverityRank(items[i].Severity), executionSeverityRank(items[j].Severity)
		if left != right {
			return left < right
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	summary := ExecutionCenterSummary{ByCategory: make(map[string]int)}
	for _, item := range items {
		summary.Total++
		summary.ByCategory[item.Category]++
		switch item.Severity {
		case "critical":
			summary.Critical++
		case "warning":
			summary.Warning++
		default:
			summary.Info++
		}
	}
	return &ExecutionCenterView{Items: items, Summary: summary, GeneratedAt: time.Now()}, nil
}

func executionSeverity(status string) string {
	switch status {
	case "failed", "manual_review", "needs_review", "open", "disputed", "compensation_pending", "confirm_pending":
		return "critical"
	case "pending", "processing", "submitted", "retryable", "remote_succeeded", "queued", "printing":
		return "warning"
	default:
		return "info"
	}
}

func executionSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}
