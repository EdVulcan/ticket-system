package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/authz"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrXiaohongshuRefundHold              = errors.New("xiaohongshu after-sale fulfillment is on hold")
	ErrXiaohongshuRefundResolutionInvalid = errors.New("invalid xiaohongshu after-sale resolution")
	ErrXiaohongshuRefundResolutionDenied  = errors.New("xiaohongshu after-sale resolution requires tenant administrator")
	ErrXiaohongshuRefundOrderNotFound     = errors.New("xiaohongshu external order could not be resolved")
)

const xiaohongshuRefundCoordinationReason = "authenticated Xiaohongshu after-sale event; fulfillment is paused until evidence-backed resolution"

// XiaohongshuRefundCoordinationService owns the tenant-scoped manual
// reconciliation surface for authenticated after-sale callbacks. It is
// stateless so HTTP handlers and background workers share the same rules.
type XiaohongshuRefundCoordinationService struct{}

// XiaohongshuRefundResolutionRequest intentionally accepts no internal order,
// ticket, payment, amount, or tenant fields. The service derives all protected
// identities from the coordination row and the authenticated tenant context.
type XiaohongshuRefundResolutionRequest struct {
	TenantID        uint
	CoordinationID  uint
	ActorUserID     uint
	ActorRole       string
	Action          string // dismiss_no_refund, bind_order_hold, confirm_external_refund
	ExternalOrderID string // lookup hint only; the linked order is server-derived
	Reason          string
	Evidence        string
	IdempotencyKey  string
}

func (s XiaohongshuRefundCoordinationService) List(tenantID uint, states []string, limit int) ([]model.XiaohongshuRefundCoordination, error) {
	if tenantID == 0 {
		return nil, ErrXiaohongshuRefundResolutionInvalid
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	query := model.DB.Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if len(states) > 0 {
		query = query.Where("state IN ?", states)
	}
	var rows []model.XiaohongshuRefundCoordination
	if err := query.Order("updated_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// CreateXiaohongshuRefundCoordinationTx records a hold after the webhook has
// already passed signature, decryption, and AppID checks. It never changes
// payment, refund, inventory, ticket, or settlement facts.
func CreateXiaohongshuRefundCoordinationTx(tx *gorm.DB, account *model.ChannelAccount, event *model.XiaohongshuWebhookEvent, payload []byte) error {
	if tx == nil || account == nil || account.ID == 0 || account.TenantID == 0 || event == nil || event.ID == 0 {
		return ErrXiaohongshuRefundResolutionInvalid
	}
	externalOrderID, externalAfterSaleID, externalRefundID := parseXiaohongshuAfterSaleIdentifiers(payload)
	// Lock the account before checking the external after-sale number. Two
	// different payloads for one after-sale must serialize before the unique
	// coordination index is reached, otherwise one request would surface as a
	// generic database error instead of an idempotent success.
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", account.ID, account.TenantID, "xiaohongshu").First(&model.ChannelAccount{}).Error; err != nil {
		return err
	}
	if externalAfterSaleID != "" {
		var existing model.XiaohongshuRefundCoordination
		err := tx.Where("tenant_id = ? AND channel_account_id = ? AND external_after_sale_id = ? AND deleted_at IS NULL", account.TenantID, account.ID, externalAfterSaleID).First(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	// If the callback carries an order identifier that is already known to this
	// channel account, keep the hold at order scope. Some provider payloads use
	// the merchant out-order id while others use the platform order id; an exact
	// match on either stored identifier is safe, while an ambiguous match stays
	// account-scoped for manual resolution.
	var orderLink *model.XiaohongshuOrderLink
	var err error
	if externalOrderID != "" {
		orderLink, err = findXiaohongshuOrderLinkTx(tx, account.TenantID, account.ID, externalOrderID)
		if err != nil {
			return err
		}
	}
	digest := sha256.Sum256(payload)
	evidenceCiphertext, err := utils.EncryptAES(string(payload))
	if err != nil {
		return err
	}
	requestHash := event.PayloadHash
	if requestHash == "" {
		requestHash = hex.EncodeToString(digest[:])
	}
	row := model.XiaohongshuRefundCoordination{
		TenantID: account.TenantID, ChannelAccountID: account.ID, WebhookEventID: event.ID,
		ExternalOrderID: externalOrderID, ExternalAfterSaleID: externalAfterSaleID, ExternalRefundID: externalRefundID,
		Scope: "account", State: "received_unmapped", RequestHash: requestHash,
		Reason: xiaohongshuRefundCoordinationReason, EvidenceCiphertext: evidenceCiphertext,
		EvidenceHash: hex.EncodeToString(digest[:]), LastError: xiaohongshuRefundCoordinationReason,
	}
	if orderLink != nil {
		row.XiaohongshuOrderLinkID = orderLink.ID
		row.Scope = "order"
		row.State = "order_held"
	}
	return tx.Create(&row).Error
}

// EnsureNoXiaohongshuRefundHoldTx is called while the ticket transaction is
// holding the ticket row. Locking the channel account serializes it with a
// callback creating a new account-level hold, so a committed hold cannot be
// bypassed by a concurrent verification.
func EnsureNoXiaohongshuRefundHoldTx(tx *gorm.DB, order *model.Order) error {
	if tx == nil || order == nil || order.TenantID == 0 || order.Channel != "xiaohongshu" || order.ChannelAccountID == 0 {
		return nil
	}
	var account model.ChannelAccount
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND type = ?", order.ChannelAccountID, order.TenantID, "xiaohongshu").First(&account).Error; err != nil {
		return err
	}
	var link model.XiaohongshuOrderLink
	if err := tx.Where("order_id = ? AND tenant_id = ? AND channel_account_id = ? AND deleted_at IS NULL", order.ID, order.TenantID, order.ChannelAccountID).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var hold model.XiaohongshuRefundCoordination
	err := tx.Where(`tenant_id = ? AND channel_account_id = ? AND state IN ? AND deleted_at IS NULL
		AND (scope = 'account' OR (scope = 'order' AND xiaohongshu_order_link_id = ?))`,
		order.TenantID, order.ChannelAccountID,
		[]string{"received_unmapped", "order_held", "external_refund_confirmed"}, link.ID).
		Order("updated_at DESC, id DESC").First(&hold).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: coordination %d", ErrXiaohongshuRefundHold, hold.ID)
}

// HasXiaohongshuRefundAccountHoldTx is used before creating a new channel
// order. An account-level hold must not allow new orders that cannot currently
// be fulfilled.
func HasXiaohongshuRefundAccountHoldTx(tx *gorm.DB, tenantID, accountID uint) (bool, error) {
	if tenantID == 0 || accountID == 0 {
		return false, nil
	}
	var count int64
	if err := tx.Model(&model.XiaohongshuRefundCoordination{}).
		Where("tenant_id = ? AND channel_account_id = ? AND scope = ? AND state IN ? AND deleted_at IS NULL", tenantID, accountID, "account", []string{"received_unmapped", "order_held", "external_refund_confirmed"}).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s XiaohongshuRefundCoordinationService) Resolve(req XiaohongshuRefundResolutionRequest) (*model.XiaohongshuRefundCoordination, error) {
	if req.TenantID == 0 || req.CoordinationID == 0 || req.ActorUserID == 0 || !authz.IsTenantAdministrator(strings.TrimSpace(req.ActorRole)) {
		return nil, ErrXiaohongshuRefundResolutionDenied
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.ExternalOrderID = strings.TrimSpace(req.ExternalOrderID)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Evidence = strings.TrimSpace(req.Evidence)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Action != "dismiss_no_refund" && req.Action != "bind_order_hold" && req.Action != "confirm_external_refund" {
		return nil, ErrXiaohongshuRefundResolutionInvalid
	}
	if req.Reason == "" || len(req.Reason) > 500 || req.Evidence == "" || len(req.Evidence) > 2000 || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 120 {
		return nil, ErrXiaohongshuRefundResolutionInvalid
	}
	if req.Action == "bind_order_hold" && (req.ExternalOrderID == "" || len(req.ExternalOrderID) > 100) {
		return nil, ErrXiaohongshuRefundResolutionInvalid
	}
	canonical, _ := json.Marshal(struct {
		Action, ExternalOrderID, Reason, Evidence string
	}{req.Action, req.ExternalOrderID, req.Reason, req.Evidence})
	digest := sha256.Sum256(canonical)
	evidenceCiphertext, err := utils.EncryptAES(req.Evidence)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var result model.XiaohongshuRefundCoordination
	err = model.Write(func(tx *gorm.DB) error {
		var coordination model.XiaohongshuRefundCoordination
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", req.CoordinationID, req.TenantID).First(&coordination).Error; err != nil {
			return err
		}
		if coordination.ResolutionIdempotencyKey != "" {
			if coordination.ResolutionIdempotencyKey != req.IdempotencyKey || coordination.ResolutionRequestHash != hex.EncodeToString(digest[:]) {
				if coordination.ResolutionIdempotencyKey == req.IdempotencyKey {
					return ErrXiaohongshuRefundResolutionInvalid
				}
			} else {
				result = coordination
				return nil
			}
		}
		if coordination.State == "reconciled" || coordination.State == "dismissed_no_refund" || coordination.State == "external_refund_confirmed" {
			return ErrXiaohongshuRefundResolutionInvalid
		}
		before, _ := json.Marshal(struct {
			State string `json:"state"`
			Scope string `json:"scope"`
		}{coordination.State, coordination.Scope})
		updates := map[string]interface{}{
			"reason": req.Reason, "evidence_ciphertext": evidenceCiphertext,
			"evidence_hash": hex.EncodeToString(digest[:]), "resolved_by": req.ActorUserID,
			"resolved_at": now, "resolution_idempotency_key": req.IdempotencyKey,
			"resolution_request_hash": hex.EncodeToString(digest[:]), "last_error": "",
		}
		switch req.Action {
		case "dismiss_no_refund":
			updates["state"] = "dismissed_no_refund"
		case "confirm_external_refund":
			updates["state"] = "external_refund_confirmed"
		case "bind_order_hold":
			link, err := findXiaohongshuOrderLinkTx(tx, req.TenantID, coordination.ChannelAccountID, req.ExternalOrderID)
			if err != nil {
				return err
			}
			if link == nil {
				return ErrXiaohongshuRefundOrderNotFound
			}
			if coordination.Scope == "order" && coordination.XiaohongshuOrderLinkID != 0 && coordination.XiaohongshuOrderLinkID != link.ID {
				return ErrXiaohongshuRefundResolutionInvalid
			}
			updates["state"], updates["scope"] = "order_held", "order"
			updates["xiaohongshu_order_link_id"], updates["external_order_id"] = link.ID, link.ExternalOrderID
		}
		if err := tx.Model(&coordination).Updates(updates).Error; err != nil {
			return err
		}
		coordination.State = updates["state"].(string)
		if scope, ok := updates["scope"].(string); ok {
			coordination.Scope = scope
		}
		if linkID, ok := updates["xiaohongshu_order_link_id"].(uint); ok {
			coordination.XiaohongshuOrderLinkID = linkID
			coordination.ExternalOrderID = updates["external_order_id"].(string)
		}
		coordination.Reason, coordination.EvidenceHash = req.Reason, hex.EncodeToString(digest[:])
		coordination.ResolvedBy, coordination.ResolvedAt = req.ActorUserID, &now
		coordination.ResolutionIdempotencyKey, coordination.ResolutionRequestHash = req.IdempotencyKey, hex.EncodeToString(digest[:])
		after, _ := json.Marshal(struct {
			State string `json:"state"`
			Scope string `json:"scope"`
		}{coordination.State, coordination.Scope})
		if err := recordAuditTx(tx, req.ActorUserID, req.TenantID, req.ActorRole, "tenant", "xiaohongshu.refund_coordination.resolve", "xiaohongshu_refund_coordination", coordination.ID, req.Reason, string(before), string(after)); err != nil {
			return err
		}
		result = coordination
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func findXiaohongshuOrderLinkTx(tx *gorm.DB, tenantID, accountID uint, externalOrderID string) (*model.XiaohongshuOrderLink, error) {
	externalOrderID = strings.TrimSpace(externalOrderID)
	if tx == nil || tenantID == 0 || accountID == 0 || externalOrderID == "" {
		return nil, nil
	}
	var links []model.XiaohongshuOrderLink
	if err := tx.Where(`tenant_id = ? AND channel_account_id = ? AND deleted_at IS NULL
		AND (external_order_id = ? OR (platform_order_id <> '' AND platform_order_id = ?))`,
		tenantID, accountID, externalOrderID, externalOrderID).Limit(2).Find(&links).Error; err != nil {
		return nil, err
	}
	if len(links) != 1 {
		return nil, nil
	}
	return &links[0], nil
}

func parseXiaohongshuAfterSaleIdentifiers(payload []byte) (externalOrderID, externalAfterSaleID, externalRefundID string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return "", "", ""
	}
	value := func(names ...string) string {
		for _, name := range names {
			for key, raw := range fields {
				if normalizeXiaohongshuWebhookField(key) != normalizeXiaohongshuWebhookField(name) {
					continue
				}
				var value string
				if json.Unmarshal(raw, &value) == nil {
					return truncateExternalIdentifier(value)
				}
				var number json.Number
				if json.Unmarshal(raw, &number) == nil {
					return truncateExternalIdentifier(number.String())
				}
			}
		}
		return ""
	}
	return value("order_id", "order_no", "external_order_id", "orderid"),
		value("after_sale_id", "aftersale_id", "after_sale_no", "aftersale_no", "service_id"),
		value("refund_id", "refund_no", "external_refund_id", "refundid")
}

func truncateExternalIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 100 {
		return ""
	}
	return value
}
