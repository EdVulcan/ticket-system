package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrXiaohongshuWebhookNotConfigured = errors.New("xiaohongshu webhook is not configured")
	ErrXiaohongshuWebhookSignature     = errors.New("xiaohongshu webhook signature is invalid")
	ErrXiaohongshuWebhookPayload       = errors.New("xiaohongshu webhook payload is invalid")
)

const xiaohongshuWebhookManualReviewReason = "event has no authorized local consumer; manual review required"

const xiaohongshuProductAuditUnrecognizedReason = "PRODUCT_AUDIT status or product code could not be safely recognized; product remains unavailable"

type XiaohongshuWebhookMessage struct {
	Nonce        string `json:"Nonce"`
	Timestamp    int64  `json:"Timestamp"`
	Encrypt      string `json:"Encrypt"`
	MsgSignature string `json:"MsgSignature"`
}

type XiaohongshuWebhookService struct{}

func (XiaohongshuWebhookService) VerifyURL(appID, signature, timestamp, nonce, echo string) (string, error) {
	_, token, _, err := loadXiaohongshuWebhookConfig(appID)
	if err != nil {
		return "", err
	}
	if echo == "" || !xiaohongshu.VerifyMessageSignature(signature, token, timestamp, nonce) {
		return "", ErrXiaohongshuWebhookSignature
	}
	return echo, nil
}

func (XiaohongshuWebhookService) Receive(ctx context.Context, appID string, message XiaohongshuWebhookMessage) error {
	account, token, encodingAESKey, err := loadXiaohongshuWebhookConfig(appID)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(message.Timestamp, 10)
	if message.Nonce == "" || message.Timestamp <= 0 || message.Encrypt == "" ||
		!xiaohongshu.VerifyMessageSignature(message.MsgSignature, token, timestamp, message.Nonce, message.Encrypt) {
		return ErrXiaohongshuWebhookSignature
	}
	payload, payloadAppID, err := xiaohongshu.DecryptMessage(message.Encrypt, encodingAESKey)
	if err != nil || payloadAppID != account.AppID || !json.Valid(payload) {
		return ErrXiaohongshuWebhookPayload
	}
	var envelope struct {
		Event string `json:"Event"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || strings.TrimSpace(envelope.Event) == "" {
		return ErrXiaohongshuWebhookPayload
	}
	digest := sha256.Sum256(payload)
	payloadCiphertext, err := utils.EncryptAES(string(payload))
	if err != nil {
		return err
	}
	eventType := strings.TrimSpace(envelope.Event)
	status, lastError := xiaohongshuWebhookDisposition(eventType)
	event := model.XiaohongshuWebhookEvent{
		TenantID: account.TenantID, ChannelAccountID: account.ID,
		PayloadHash: hex.EncodeToString(digest[:]), EventType: eventType,
		PayloadCiphertext: payloadCiphertext, Status: status, LastError: lastError, ReceivedAt: time.Now(),
	}
	return model.Write(func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		if strings.EqualFold(eventType, "AFTER_SALE_REFUND") {
			if err := CreateXiaohongshuRefundCoordinationTx(tx, account, &event, payload); err != nil {
				return err
			}
			return nil
		}
		if !strings.EqualFold(eventType, "PRODUCT_AUDIT") {
			return nil
		}

		productCode, auditStatus, auditMessage := parseXiaohongshuProductAudit(payload)
		var config model.XiaohongshuProductConfig
		if productCode == "" {
			return markXiaohongshuWebhookManualReview(tx, &event, xiaohongshuProductAuditUnrecognizedReason)
		}
		err := tx.Table("xiaohongshu_product_configs AS config").Select("config.*").
			Joins("JOIN channel_product_mappings AS mapping ON mapping.id = config.channel_product_mapping_id AND mapping.channel_account_id = config.channel_account_id").
			Where("config.tenant_id = ? AND config.channel_account_id = ? AND mapping.external_code = ? AND config.deleted_at IS NULL AND mapping.deleted_at IS NULL", account.TenantID, account.ID, productCode).
			First(&config).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return markXiaohongshuWebhookManualReview(tx, &event, xiaohongshuProductAuditUnrecognizedReason)
		}
		if err != nil {
			return err
		}

		if auditStatus == "" {
			if err := tx.Model(&config).Updates(map[string]interface{}{
				"audit_status": "pending", "audit_message": xiaohongshuProductAuditUnrecognizedReason, "audited_at": nil,
			}).Error; err != nil {
				return err
			}
			return markXiaohongshuWebhookManualReview(tx, &event, xiaohongshuProductAuditUnrecognizedReason)
		}

		now := time.Now()
		if auditStatus == "pending" {
			// GORM can omit both nil pointers and SQL expressions supplied through
			// Updates for nullable model fields. Use a parameterized statement so
			// a re-review always clears a stale approval timestamp.
			if err := tx.Exec(`UPDATE xiaohongshu_product_configs
				SET audit_status = ?, audit_message = ?, audited_at = NULL, updated_at = ?
				WHERE id = ? AND tenant_id = ? AND channel_account_id = ?`,
				auditStatus, auditMessage, now, config.ID, account.TenantID, account.ID).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&config).Updates(map[string]interface{}{
			"audit_status": auditStatus, "audit_message": auditMessage, "audited_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&event).Updates(map[string]interface{}{
			"status": "processed", "last_error": "", "processed_at": now,
		}).Error
	})
}

func markXiaohongshuWebhookManualReview(tx *gorm.DB, event *model.XiaohongshuWebhookEvent, reason string) error {
	return tx.Model(event).Updates(map[string]interface{}{
		"status": "manual_review", "last_error": truncateChannelError(reason),
	}).Error
}

// parseXiaohongshuProductAudit accepts the casing and naming variants used by
// provider callbacks without assuming undocumented numeric states. Only
// statuses with an explicit textual meaning can pass the catalog gate; numeric
// values are intentionally left unresolved until the provider's production
// webhook enum is verified from an official sample.
func parseXiaohongshuProductAudit(payload []byte) (productCode, auditStatus, message string) {
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
				var text string
				if json.Unmarshal(raw, &text) == nil {
					return strings.TrimSpace(text)
				}
				var number json.Number
				if json.Unmarshal(raw, &number) == nil {
					return number.String()
				}
			}
		}
		return ""
	}
	productCode = value("out_product_id", "out_product_code")
	statusValue := value("audit_status", "audit_result", "status", "product_status")
	message = truncateChannelError(value("reject_reason", "audit_reason", "reason", "message", "msg"))
	return productCode, xiaohongshuProductAuditStatus(statusValue), message
}

func normalizeXiaohongshuWebhookField(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func xiaohongshuProductAuditStatus(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "pending", "submitted", "auditing", "reviewing", "under_review", "in_review":
		return "pending"
	case "approved", "approve", "passed", "pass", "success", "online", "on_sale", "onsale", "active":
		return "approved"
	case "rejected", "reject", "failed", "fail", "refused":
		return "rejected"
	case "offline", "off_shelf", "offshelf", "disabled", "removed", "deleted":
		return "offline"
	default:
		return ""
	}
}

// After-sale/refund and every unknown business event are safely retained but
// must be manually reconciled; they cannot mutate local money, tickets, or
// fulfillment merely because an authenticated callback arrived.
func xiaohongshuWebhookDisposition(eventType string) (status, lastError string) {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "PRODUCT_AUDIT":
		return "pending", ""
	default:
		return "manual_review", xiaohongshuWebhookManualReviewReason
	}
}

func loadXiaohongshuWebhookConfig(appID string) (*model.ChannelAccount, string, string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, "", "", ErrXiaohongshuWebhookNotConfigured
	}
	var account model.ChannelAccount
	if err := model.DB.Where("type = ? AND app_id = ? AND status IN ?", "xiaohongshu", appID, []string{"active", "sandbox"}).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", "", ErrXiaohongshuWebhookNotConfigured
		}
		return nil, "", "", err
	}
	if account.VerifyKeyCiphertext == "" || account.ProtocolConfigCiphertext == "" {
		return nil, "", "", ErrXiaohongshuWebhookNotConfigured
	}
	token, err := utils.DecryptAES(account.VerifyKeyCiphertext)
	if err != nil {
		return nil, "", "", err
	}
	configJSON, err := utils.DecryptAES(account.ProtocolConfigCiphertext)
	if err != nil {
		return nil, "", "", err
	}
	var config XiaohongshuMessageConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil || config.EncodingAESKey == "" {
		return nil, "", "", ErrXiaohongshuWebhookNotConfigured
	}
	return &account, token, config.EncodingAESKey, nil
}
