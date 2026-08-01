package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type ChannelBillInput struct {
	ExternalNo          string     `json:"external_no"`
	Operation           string     `json:"operation"`
	ExternalProductCode string     `json:"external_product_code,omitempty"`
	AmountCents         int64      `json:"amount_cents"`
	Currency            string     `json:"currency,omitempty"`
	ExternalOccurredAt  *time.Time `json:"external_occurred_at,omitempty"`
	RawJSON             string     `json:"raw_json,omitempty"`
}

// ImportBill stores an external bill batch and immediately attempts a
// read-only match against internal order/payment/refund facts. Repeating the
// idempotency key returns the original report; conflicting rows are rejected.
func (s *ChannelService) ImportBill(tenantID, accountID uint, idempotencyKey string, records []ChannelBillInput) (*model.ChannelReconciliation, error) {
	if tenantID == 0 || accountID == 0 || strings.TrimSpace(idempotencyKey) == "" || len(records) == 0 {
		return nil, errors.New("tenant, channel, idempotency key and bill records are required")
	}
	var result model.ChannelReconciliation
	err := model.Write(func(tx *gorm.DB) error {
		var account model.ChannelAccount
		if err := tx.Where("id = ? AND tenant_id = ? AND status != ?", accountID, tenantID, "disabled").First(&account).Error; err != nil {
			return errors.New("channel account is unavailable")
		}
		normalized, start, end, err := normalizeChannelBillInputs(records)
		if err != nil {
			return err
		}
		encodedInput, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(encodedInput)
		inputHash := hex.EncodeToString(hash[:])
		if err := tx.Preload("Lines").Where("tenant_id = ? AND channel_account_id = ? AND idempotency_key = ?", tenantID, accountID, idempotencyKey).First(&result).Error; err == nil {
			if result.InputHash != "" && result.InputHash != inputHash {
				return errors.New("bill idempotency key was used with different records")
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result = model.ChannelReconciliation{
			TenantID: tenantID, ChannelAccountID: accountID, IdempotencyKey: strings.TrimSpace(idempotencyKey),
			InputHash: inputHash, PeriodStart: start, PeriodEnd: end, RecordCount: len(normalized), Status: "completed",
		}
		lines := make([]model.ChannelReconciliationLine, 0, len(normalized))
		for _, input := range normalized {
			match, err := matchChannelBill(tx, tenantID, accountID, input)
			if err != nil {
				return err
			}
			var existing model.ChannelBillRecord
			var billRecordID uint
			err = tx.Where("channel_account_id = ? AND external_no = ? AND operation = ?", accountID, input.ExternalNo, input.Operation).First(&existing).Error
			if err == nil {
				if existing.AmountCents != input.AmountCents || existing.Currency != input.Currency {
					return fmt.Errorf("bill fact %s/%s conflicts with an earlier import", input.ExternalNo, input.Operation)
				}
				match.Status = existing.Status
				match.MatchedOrderNo = existing.MatchedOrderNo
				match.MatchedPaymentNo = existing.MatchedPaymentNo
				match.MatchedRefundNo = existing.MatchedRefundNo
				match.DifferenceCents = existing.DifferenceCents
				billRecordID = existing.ID
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				fact := model.ChannelBillRecord{
					TenantID: tenantID, ChannelAccountID: accountID, ExternalNo: input.ExternalNo, Operation: input.Operation,
					ExternalProductCode: input.ExternalProductCode, AmountCents: input.AmountCents, Currency: input.Currency,
					ExternalOccurredAt: input.ExternalOccurredAt, Status: match.Status, MatchedOrderNo: match.MatchedOrderNo,
					MatchedPaymentNo: match.MatchedPaymentNo, MatchedRefundNo: match.MatchedRefundNo, DifferenceCents: match.DifferenceCents,
					RawJSON: input.RawJSON,
				}
				if err := tx.Create(&fact).Error; err != nil {
					return err
				}
				billRecordID = fact.ID
			} else {
				return err
			}
			if match.Status == "matched" {
				result.MatchedCount++
			}
			result.DifferenceCents += match.DifferenceCents
			lines = append(lines, model.ChannelReconciliationLine{
				BillRecordID: billRecordID, ExternalNo: input.ExternalNo, Operation: input.Operation, AmountCents: input.AmountCents,
				Status: match.Status, MatchedOrderNo: match.MatchedOrderNo, MatchedPaymentNo: match.MatchedPaymentNo,
				MatchedRefundNo: match.MatchedRefundNo, DifferenceCents: match.DifferenceCents,
			})
		}
		if result.DifferenceCents != 0 || result.MatchedCount != result.RecordCount {
			result.Status = "needs_review"
		}
		summary, err := json.Marshal(map[string]interface{}{"matched": result.MatchedCount, "records": result.RecordCount, "difference_cents": result.DifferenceCents})
		if err != nil {
			return err
		}
		result.SummaryJSON = string(summary)
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		for i := range lines {
			lines[i].ReconciliationID = result.ID
		}
		if err := tx.Create(&lines).Error; err != nil {
			return err
		}
		result.Lines = lines
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *ChannelService) GetReconciliation(tenantID, accountID, reconciliationID uint) (*model.ChannelReconciliation, error) {
	if tenantID == 0 || accountID == 0 || reconciliationID == 0 {
		return nil, errors.New("tenant, channel and reconciliation are required")
	}
	var result model.ChannelReconciliation
	if err := model.DB.Preload("Lines", func(db *gorm.DB) *gorm.DB { return db.Order("id ASC") }).
		Where("id = ? AND tenant_id = ? AND channel_account_id = ?", reconciliationID, tenantID, accountID).First(&result).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func normalizeChannelBillInputs(records []ChannelBillInput) ([]ChannelBillInput, time.Time, time.Time, error) {
	normalized := make([]ChannelBillInput, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	var start, end time.Time
	for _, input := range records {
		input.ExternalNo = strings.TrimSpace(input.ExternalNo)
		input.Operation = strings.ToLower(strings.TrimSpace(input.Operation))
		input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
		if input.Currency == "" {
			input.Currency = "CNY"
		}
		if input.ExternalNo == "" || input.AmountCents < 0 {
			return nil, time.Time{}, time.Time{}, errors.New("bill external number and non-negative amount are required")
		}
		switch input.Operation {
		case "sale", "payment", "cancel", "refund":
		default:
			return nil, time.Time{}, time.Time{}, fmt.Errorf("unsupported bill operation %q", input.Operation)
		}
		key := input.ExternalNo + "\x00" + input.Operation
		if _, ok := seen[key]; ok {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("duplicate bill fact %s/%s", input.ExternalNo, input.Operation)
		}
		seen[key] = struct{}{}
		if input.ExternalOccurredAt != nil {
			if start.IsZero() || input.ExternalOccurredAt.Before(start) {
				start = *input.ExternalOccurredAt
			}
			if end.IsZero() || input.ExternalOccurredAt.After(end) {
				end = *input.ExternalOccurredAt
			}
		}
		normalized = append(normalized, input)
	}
	if start.IsZero() {
		start, end = time.Now(), time.Now()
	}
	return normalized, start, end, nil
}

type channelBillMatch struct {
	Status           string
	MatchedOrderNo   string
	MatchedPaymentNo string
	MatchedRefundNo  string
	DifferenceCents  int64
}

func matchChannelBill(tx *gorm.DB, tenantID, accountID uint, input ChannelBillInput) (channelBillMatch, error) {
	match := channelBillMatch{Status: "unmatched"}
	var order model.Order
	matchedByProviderID := false
	err := tx.Where("tenant_id = ? AND channel_account_id = ? AND external_no = ?", tenantID, accountID, input.ExternalNo).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && input.Operation == "refund" {
		var refund model.Refund
		if lookupErr := tx.Where("tenant_id = ? AND provider_refund_id = ? AND status = ?", tenantID, input.ExternalNo, "succeeded").Order("created_at DESC").First(&refund).Error; lookupErr == nil {
			err = tx.Where("tenant_id = ? AND channel_account_id = ? AND order_no = ?", tenantID, accountID, refund.OrderNo).First(&order).Error
			matchedByProviderID = err == nil
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && (input.Operation == "payment" || input.Operation == "sale") {
		var payment model.Payment
		if lookupErr := tx.Where("tenant_id = ? AND transaction_id = ? AND status IN ?", tenantID, input.ExternalNo, []string{"paid", "refunded", "partial_refunded"}).Order("created_at DESC").First(&payment).Error; lookupErr == nil {
			err = tx.Where("tenant_id = ? AND channel_account_id = ? AND order_no = ?", tenantID, accountID, payment.OrderNo).First(&order).Error
		}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return match, nil
	}
	if err != nil {
		return match, err
	}
	match.MatchedOrderNo = order.OrderNo
	expected := moneyCents(order.TotalAmount)
	switch input.Operation {
	case "sale", "payment":
		var payment model.Payment
		if err := tx.Where("tenant_id = ? AND order_no = ? AND status IN ?", tenantID, order.OrderNo, []string{"paid", "refunded", "partial_refunded"}).Order("created_at ASC").First(&payment).Error; err == nil {
			match.MatchedPaymentNo = payment.PaymentNo
			expected = moneyCents(payment.Amount)
		}
	case "refund":
		var refund model.Refund
		refundQuery := tx.Where("tenant_id = ? AND order_no = ? AND status = ?", tenantID, order.OrderNo, "succeeded")
		if matchedByProviderID {
			refundQuery = refundQuery.Where("provider_refund_id = ?", input.ExternalNo)
		}
		if err := refundQuery.Order("created_at DESC").First(&refund).Error; err == nil {
			match.MatchedRefundNo = refund.RefundNo
			expected = moneyCents(refund.Amount)
		} else {
			return match, nil
		}
	case "cancel":
		expected = 0
	}
	match.DifferenceCents = input.AmountCents - expected
	if match.DifferenceCents == 0 {
		match.Status = "matched"
	} else {
		match.Status = "mismatch"
	}
	return match, nil
}

func (s *ChannelService) ListReconciliations(tenantID, accountID uint, page, pageSize int) ([]model.ChannelReconciliation, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.ChannelReconciliation{}).Where("tenant_id = ? AND channel_account_id = ?", tenantID, accountID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.ChannelReconciliation
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
