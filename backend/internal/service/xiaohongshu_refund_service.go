package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errXiaohongshuRefundMismatch = errors.New("小红书退款查询身份、金额或凭证不匹配，需人工复核")

// prepareXiaohongshuRefundTx runs in the ordinary refund reservation
// transaction. Only unused, fully issued, ordinary group-voucher orders are
// supported here; package/calendar/mixed/used-ticket exceptions stay closed.
func prepareXiaohongshuRefundTx(tx *gorm.DB, order *model.Order, payment *model.Payment, refund *model.Refund, selected map[string]*model.Ticket) error {
	if payment.Method != xiaohongshuPaymentMethod {
		return nil
	}
	if order.Channel != xiaohongshuPaymentMethod || order.Status != "paid" || len(order.Items) != 1 ||
		refund.ParentRefundID != 0 || refund.AuthorizedUsedRefund || refund.AmountCents != payment.AmountCents ||
		refund.AmountCents != moneyCents(order.TotalAmount) || len(selected) != len(order.Items[0].Tickets) {
		return errors.New("小红书当前仅支持未核销普通门票的整单原路退款")
	}
	var account model.ChannelAccount
	var lockedTickets []model.Ticket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", order.ID).Order("id").Find(&lockedTickets).Error; err != nil {
		return err
	}
	if len(lockedTickets) != len(selected) {
		return errors.New("小红书退款票券数量不匹配")
	}
	for _, ticket := range lockedTickets {
		if ticket.PendingRefundID != refund.ID || ticket.CheckInCount != 0 || ticket.PendingXiaohongshuVerificationID != 0 || ticket.Status != "unused" {
			return errors.New("小红书退款票券占用不匹配")
		}
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", order.ChannelAccountID, order.TenantID, "xiaohongshu").First(&account).Error; err != nil {
		return err
	}
	if err := EnsureNoXiaohongshuRefundHoldTx(tx, order); err != nil {
		return err
	}
	var link model.XiaohongshuOrderLink
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND order_id = ? AND state = ? AND voucher_issuance_status = ?", order.TenantID, account.ID, order.ID, "paid", "ready").First(&link).Error; err != nil {
		return errors.New("小红书票券未完整签发，退款需人工核查")
	}
	var original model.XiaohongshuOrderOperation
	if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND status = ?", order.TenantID, account.ID, link.ID, "completed").First(&original).Error; err != nil {
		return errors.New("小红书原始下单快照缺失，退款需人工核查")
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(original.RequestPayloadCiphertext)
	if err != nil {
		return err
	}
	item := order.Items[0]
	item.Tickets = lockedTickets
	if err := requireXiaohongshuRefundProductTypeTx(tx, order, &link, &original, payload); err != nil {
		return err
	}
	if payload.Request.ExternalOrderID != link.ExternalOrderID || payload.Request.Price.OrderPrice != refund.AmountCents ||
		len(payload.Request.Products) != 1 || payload.Request.Products[0].RealPrice != refund.AmountCents ||
		payload.Request.Products[0].Count != item.Quantity {
		return errors.New("小红书原始商品或金额与退款范围不匹配")
	}
	product := payload.Request.Products[0]
	var previousFailures int64
	if err := tx.Model(&model.Refund{}).Where("tenant_id = ? AND order_no = ? AND method = ? AND status = ?", order.TenantID, order.OrderNo, "xiaohongshu", "failed").Count(&previousFailures).Error; err != nil {
		return err
	}
	if previousFailures != 0 {
		return errors.New("该订单已有失败的小红书退款，请先核查平台售后结果")
	}
	request := xiaohongshu.AfterSalesAddRequest{
		ExternalOrderID: link.ExternalOrderID, ExternalAfterSalesOrderID: refund.RefundNo, OpenID: payload.Request.OpenID,
		Type: 1, Reason: refund.Reason, BizCreateTime: refund.CreatedAt.Unix(), ProductType: 1, AutoConfirm: true, RefundRuleType: 1,
		Price:    xiaohongshu.AfterSalesPriceInfo{RefundPrice: refund.AmountCents},
		Products: []xiaohongshu.AfterSalesProductInfo{{ExternalProductID: product.ExternalProductID, ExternalSKUID: product.ExternalSKUID, Count: product.Count, Price: refund.AmountCents}},
	}
	// Iterate sale-time tickets, not map order, to freeze stable voucher allocation.
	for _, ticket := range item.Tickets {
		if ticket.CheckInCount != 0 || ticket.Status != "unused" || ticket.PendingXiaohongshuVerificationID != 0 {
			return errors.New("小红书票券已核销或核销处理中，不能退款")
		}
		var voucher model.XiaohongshuVoucherLink
		if err := tx.Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND ticket_id = ?", order.TenantID, account.ID, link.ID, ticket.ID).First(&voucher).Error; err != nil {
			return err
		}
		if voucher.VerifyID != "" || voucher.Status != 1 {
			return errors.New("小红书平台券状态不允许退款")
		}
		var active int64
		if err := tx.Model(&model.XiaohongshuVoucherVerification{}).Where("voucher_link_id = ? AND state <> ?", voucher.ID, "external_rejected").Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return errors.New("小红书核销结果待确认，不能退款")
		}
		code, err := utils.DecryptAES(voucher.VoucherCodeCiphertext)
		if err != nil || code == "" || hashMiniappValue(code) != voucher.VoucherCodeHash {
			return errors.New("小红书凭证关联无效")
		}
		value := moneyCents(item.Price)
		if ticket.CodeMode == "order" {
			value = refund.AmountCents
		}
		request.Vouchers = append(request.Vouchers, xiaohongshu.AfterSalesVoucherDetail{VoucherCode: code, RefundPrice: value})
	}
	if err := xiaohongshu.ValidateAfterSalesAdd(request); err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	encrypted, err := utils.EncryptAES(string(raw))
	if err != nil {
		return err
	}
	return tx.Create(&model.XiaohongshuRefundOperation{TenantID: order.TenantID, ChannelAccountID: account.ID, XiaohongshuOrderLinkID: link.ID, RefundID: refund.ID, ExternalAfterSalesOrderID: refund.RefundNo, RequestPayloadCiphertext: encrypted, State: "prepared"}).Error
}

type xiaohongshuRefundProvider struct {
	newClient func(string, string, string) *xiaohongshu.Client
}

func (p *xiaohongshuRefundProvider) Process(ctx context.Context, refund *model.Refund, payment *model.Payment) (RefundProviderResult, error) {
	if refund.TenantID == 0 || refund.TenantID != payment.TenantID || refund.PaymentID != payment.ID || refund.Method != "xiaohongshu" || payment.Method != "xiaohongshu" {
		return RefundProviderResult{}, errors.New("xiaohongshu refund ownership mismatch")
	}
	var op model.XiaohongshuRefundOperation
	if err := model.DB.Where("refund_id = ? AND tenant_id = ?", refund.ID, refund.TenantID).First(&op).Error; err != nil {
		return RefundProviderResult{}, err
	}
	raw, err := utils.DecryptAES(op.RequestPayloadCiphertext)
	if err != nil {
		return RefundProviderResult{}, errors.New("xiaohongshu refund snapshot unreadable")
	}
	var request xiaohongshu.AfterSalesAddRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return RefundProviderResult{}, err
	}
	if request.ExternalAfterSalesOrderID != op.ExternalAfterSalesOrderID || request.Price.RefundPrice != refund.AmountCents {
		return RefundProviderResult{}, errors.New("xiaohongshu refund snapshot mismatch")
	}
	if err := xiaohongshu.ValidateAfterSalesAdd(request); err != nil {
		return RefundProviderResult{}, err
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ?", op.ChannelAccountID, op.TenantID, "xiaohongshu").First(&account).Error; err != nil {
		return RefundProviderResult{}, err
	}
	secret, err := utils.DecryptAES(account.SecretCiphertext)
	if err != nil {
		return RefundProviderResult{}, ErrDigitalRefundNotConfigured
	}
	factory := p.newClient
	if factory == nil {
		factory = xiaohongshu.NewClient
	}
	client := factory(account.AppID, secret, account.Environment)
	if op.State == "prepared" {
		var claimed bool
		if err := model.Write(func(tx *gorm.DB) error {
			result := tx.Model(&model.XiaohongshuRefundOperation{}).Where("id = ? AND tenant_id = ? AND state = ?", op.ID, op.TenantID, "prepared").Update("state", "querying")
			claimed = result.RowsAffected == 1
			return result.Error
		}); err != nil {
			return RefundProviderResult{}, err
		}
		if claimed {
			if err := client.AddAfterSalesOrder(ctx, request); err != nil {
				return RefundProviderResult{}, errors.New("小红书退款提交结果未确认，将查询原售后单；不会重复提交")
			}
			return RefundProviderResult{Status: "submitted", ProviderRefundID: op.ExternalAfterSalesOrderID}, nil
		}
	}
	result, err := client.GetAfterSalesOrder(ctx, xiaohongshu.AfterSalesGetRequest{ExternalOrderID: request.ExternalOrderID, ExternalAfterSalesOrderID: request.ExternalAfterSalesOrderID, OpenID: request.OpenID})
	if err != nil {
		return RefundProviderResult{}, errors.New("小红书退款结果暂无法查询，请保留原退款单重试查询")
	}
	if err := validateXiaohongshuRefundResult(request, result); err != nil {
		return RefundProviderResult{}, err
	}
	state := "submitted"
	if result.Status == 2 {
		state = "succeeded"
	}
	if result.Status == 3 {
		state = "failed"
	}
	return RefundProviderResult{Status: state, ProviderRefundID: op.ExternalAfterSalesOrderID}, nil
}

func validateXiaohongshuRefundResult(request xiaohongshu.AfterSalesAddRequest, result *xiaohongshu.AfterSalesOrderResponse) error {
	if result == nil || result.ExternalOrderID != request.ExternalOrderID || result.ExternalAfterSalesOrderID != request.ExternalAfterSalesOrderID ||
		result.OpenID != request.OpenID || result.Type != 1 || result.ProductType != 1 || result.Price.RefundPrice != request.Price.RefundPrice ||
		len(result.Vouchers) != len(request.Vouchers) {
		return errXiaohongshuRefundMismatch
	}
	amounts := make(map[string]int64, len(request.Vouchers))
	for _, v := range request.Vouchers {
		amounts[v.VoucherCode] = v.RefundPrice
	}
	for _, v := range result.Vouchers {
		expected, ok := amounts[v.VoucherCode]
		if !ok || expected != v.RefundPrice {
			return errXiaohongshuRefundMismatch
		}
		delete(amounts, v.VoucherCode)
	}
	if len(amounts) != 0 {
		return errXiaohongshuRefundMismatch
	}
	return nil
}

// A verified callback is only a wake-up hint. Unknown refunds still follow
// the existing evidence-backed hold workflow; callbacks never finalize funds.
func wakeXiaohongshuRefundTx(tx *gorm.DB, account *model.ChannelAccount, event *model.XiaohongshuWebhookEvent, payload []byte) (bool, error) {
	var body struct {
		ExternalID string `json:"OutAfterSalesOrderId"`
		Status     int    `json:"Status"`
	}
	if json.Unmarshal(payload, &body) != nil || strings.TrimSpace(body.ExternalID) == "" {
		return false, nil
	}
	var op model.XiaohongshuRefundOperation
	err := tx.Where("tenant_id = ? AND channel_account_id = ? AND external_after_sales_order_id = ?", account.TenantID, account.ID, body.ExternalID).First(&op).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var refund model.Refund
	if err := tx.Where("id = ? AND tenant_id = ? AND method = ?", op.RefundID, op.TenantID, "xiaohongshu").First(&refund).Error; err != nil {
		return false, err
	}
	var task model.DigitalRefundTask
	if err := tx.Where("refund_id = ? AND tenant_id = ? AND provider = ?", op.RefundID, op.TenantID, "xiaohongshu").First(&task).Error; err != nil {
		return false, err
	}
	if refund.Status == "failed" && body.Status != 3 {
		return false, nil
	}
	if op.State == "prepared" {
		if err := tx.Model(&op).Update("state", "querying").Error; err != nil {
			return false, err
		}
	}
	// Preserve the lease while recording a durable follow-up hint. The active
	// worker consumes it after its current request, unless it reaches a terminal
	// result or a safety condition requiring manual review.
	if err := tx.Model(&model.DigitalRefundTask{}).Where("tenant_id = ? AND refund_id = ? AND status IN ?", op.TenantID, op.RefundID, []string{"pending", "submitted", "manual_review", "processing"}).
		Updates(map[string]interface{}{
			"status":          gorm.Expr("CASE WHEN status = 'processing' THEN status ELSE 'submitted' END"),
			"next_attempt_at": event.ReceivedAt, "attempt_count": 0,
		}).Error; err != nil {
		return false, err
	}
	return true, tx.Model(event).Updates(map[string]interface{}{"status": "processed", "last_error": ""}).Error
}
