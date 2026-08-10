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
	event := model.XiaohongshuWebhookEvent{
		TenantID: account.TenantID, ChannelAccountID: account.ID,
		PayloadHash: hex.EncodeToString(digest[:]), EventType: strings.TrimSpace(envelope.Event),
		PayloadCiphertext: payloadCiphertext, Status: "pending", ReceivedAt: time.Now(),
	}
	return model.Write(func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error
	})
}

func loadXiaohongshuWebhookConfig(appID string) (*model.ChannelAccount, string, string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return nil, "", "", ErrXiaohongshuWebhookNotConfigured
	}
	var account model.ChannelAccount
	if err := model.DB.Where("type = ? AND app_id = ? AND status = ?", "xiaohongshu", appID, "active").First(&account).Error; err != nil {
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
