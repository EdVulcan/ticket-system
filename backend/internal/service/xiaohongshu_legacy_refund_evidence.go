package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/xiaohongshu"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const legacyXiaohongshuTypeAction = "xiaohongshu.order.legacy_type_confirmed"

// LegacyXiaohongshuEvidenceInput is maintenance-only. It is deliberately not
// registered as an API or offered to customers. Approval is out-of-band;
// ApproverUserID identifies whose approval was recorded, not a logged-in actor.
type LegacyXiaohongshuEvidenceInput struct {
	TenantID, ChannelAccountID, ApproverUserID   uint
	ConfigureAuditID, SyncAuditID                uint
	OrderNo, Reason, Executor, ApprovalReference string
}

type legacyXiaohongshuTypeEvidence struct {
	Version            int    `json:"version"`
	Source             string `json:"source"`
	TenantID           uint   `json:"tenant_id"`
	ChannelAccountID   uint   `json:"channel_account_id"`
	OrderID            uint   `json:"order_id"`
	OrderNo            string `json:"order_no"`
	LinkID             uint   `json:"link_id"`
	OperationID        uint   `json:"operation_id"`
	CiphertextHash     string `json:"ciphertext_hash"`
	ExternalOrderID    string `json:"external_order_id"`
	ExternalProductID  string `json:"external_product_id"`
	ExternalSKUID      string `json:"external_sku_id"`
	MappingID          uint   `json:"mapping_id"`
	ProductType        int    `json:"product_type"`
	ConfigureAuditID   uint   `json:"configure_audit_id"`
	ConfigureAuditHash string `json:"configure_audit_hash"`
	SyncAuditID        uint   `json:"sync_audit_id"`
	SyncAuditHash      string `json:"sync_audit_hash"`
	ApproverUserID     uint   `json:"approver_user_id"`
	Executor           string `json:"executor"`
	ApprovalReference  string `json:"approval_reference"`
	Reason             string `json:"reason"`
}

// Preview prints only a digest and non-secret identifiers, never the snapshot.
type LegacyXiaohongshuEvidenceResult struct {
	Digest      string `json:"digest"`
	AuditID     uint   `json:"audit_id,omitempty"`
	OrderNo     string `json:"order_no"`
	ProductType int    `json:"product_type"`
}

func legacyEvidenceHash(value interface{}) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func legacyCiphertextHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// CheckLegacyXiaohongshuRefundEvidence uses an explicit read-only transaction.
func CheckLegacyXiaohongshuRefundEvidence(input LegacyXiaohongshuEvidenceInput) (*LegacyXiaohongshuEvidenceResult, error) {
	var result *LegacyXiaohongshuEvidenceResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return err
		}
		evidence, err := buildLegacyXiaohongshuEvidenceTx(tx, input, false)
		if err != nil {
			return err
		}
		result = &LegacyXiaohongshuEvidenceResult{Digest: legacyEvidenceHash(evidence), OrderNo: evidence.OrderNo, ProductType: evidence.ProductType}
		return nil
	})
	return result, err
}

// Apply inserts exactly one audit attestation. It cannot create a refund or
// call a provider, and never updates any original sale or channel snapshot.
func ApplyLegacyXiaohongshuRefundEvidence(input LegacyXiaohongshuEvidenceInput, expectedDigest string) (*LegacyXiaohongshuEvidenceResult, error) {
	if len(expectedDigest) != 64 {
		return nil, errors.New("a checked evidence digest is required")
	}
	var result *LegacyXiaohongshuEvidenceResult
	err := model.Write(func(tx *gorm.DB) error {
		evidence, err := buildLegacyXiaohongshuEvidenceTx(tx, input, true)
		if err != nil {
			return err
		}
		digest := legacyEvidenceHash(evidence)
		if digest != expectedDigest {
			return errors.New("legacy evidence changed since check")
		}
		var existing []model.AuditLog
		if err := legacyEvidenceQuery(tx, evidence.TenantID, evidence.OperationID).Find(&existing).Error; err != nil {
			return err
		}
		raw, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		if len(existing) > 1 {
			return errors.New("ambiguous legacy evidence")
		}
		if len(existing) == 1 {
			if existing[0].AfterJSON != string(raw) || existing[0].ActorUserID != 0 || existing[0].ActorRole != "maintenance" || existing[0].Reason != evidence.Reason {
				return errors.New("conflicting legacy evidence")
			}
			result = &LegacyXiaohongshuEvidenceResult{Digest: digest, AuditID: existing[0].ID, OrderNo: evidence.OrderNo, ProductType: 1}
			return nil
		}
		row := model.AuditLog{TenantID: evidence.TenantID, ActorRole: "maintenance", Scope: "tenant", Action: legacyXiaohongshuTypeAction, TargetType: "xiaohongshu_order_operation", TargetID: evidence.OperationID, Reason: evidence.Reason, AfterJSON: string(raw)}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = &LegacyXiaohongshuEvidenceResult{Digest: digest, AuditID: row.ID, OrderNo: evidence.OrderNo, ProductType: 1}
		return nil
	})
	return result, err
}

func legacyEvidenceQuery(tx *gorm.DB, tenantID, operationID uint) *gorm.DB {
	return tx.Model(&model.AuditLog{}).Where("tenant_id = ? AND scope = ? AND action = ? AND target_type = ? AND target_id = ?", tenantID, "tenant", legacyXiaohongshuTypeAction, "xiaohongshu_order_operation", operationID)
}

func buildLegacyXiaohongshuEvidenceTx(tx *gorm.DB, in LegacyXiaohongshuEvidenceInput, lock bool) (*legacyXiaohongshuTypeEvidence, error) {
	if in.TenantID == 0 || in.ChannelAccountID == 0 || in.ApproverUserID == 0 || in.ConfigureAuditID == 0 || in.SyncAuditID == 0 || strings.TrimSpace(in.OrderNo) == "" || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 255 || strings.TrimSpace(in.Executor) == "" || strings.TrimSpace(in.ApprovalReference) == "" {
		return nil, errors.New("scoped order, history, approval reference, executor and reason are required")
	}
	var approver model.User
	if err := tx.Where("id = ? AND tenant_id = ? AND is_initial_admin = ?", in.ApproverUserID, in.TenantID, true).First(&approver).Error; err != nil {
		return nil, errors.New("initial administrator approval is required")
	}
	if err := requireActiveScenicSupplier(tx, in.TenantID); err != nil {
		return nil, err
	}
	var account model.ChannelAccount
	if err := tx.Where("id = ? AND tenant_id = ? AND type = ? AND environment = ? AND status = ?", in.ChannelAccountID, in.TenantID, "xiaohongshu", "production", "active").First(&account).Error; err != nil {
		return nil, errors.New("active production channel not found")
	}
	query := func() *gorm.DB {
		if lock {
			return tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		return tx
	}
	var order model.Order
	if err := query().Where("tenant_id = ? AND channel_account_id = ? AND order_no = ? AND channel = ? AND status = ?", in.TenantID, in.ChannelAccountID, in.OrderNo, "xiaohongshu", "paid").First(&order).Error; err != nil {
		return nil, errors.New("paid channel order not found")
	}
	var items []model.OrderItem
	if err := tx.Where("order_id = ?", order.ID).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, errors.New("one ordinary order item is required")
	}
	var link model.XiaohongshuOrderLink
	if err := query().Where("tenant_id = ? AND channel_account_id = ? AND order_id = ? AND state = ? AND voucher_issuance_status = ?", in.TenantID, in.ChannelAccountID, order.ID, "paid", "ready").First(&link).Error; err != nil {
		return nil, errors.New("paid issued order link not found")
	}
	var op model.XiaohongshuOrderOperation
	if err := query().Where("tenant_id = ? AND channel_account_id = ? AND xiaohongshu_order_link_id = ? AND status = ?", in.TenantID, in.ChannelAccountID, link.ID, "completed").First(&op).Error; err != nil {
		return nil, errors.New("completed original operation not found")
	}
	payload, err := decryptXiaohongshuOrderOperationPayload(op.RequestPayloadCiphertext)
	if err != nil {
		return nil, err
	}
	if payload.ProductType != 0 || len(payload.Request.Products) != 1 || payload.Request.ExternalOrderID != link.ExternalOrderID {
		return nil, errors.New("not a matching legacy snapshot with missing type")
	}
	product := payload.Request.Products[0]
	if strings.TrimSpace(link.ExternalOrderID) == "" || strings.TrimSpace(product.ExternalProductID) == "" || strings.TrimSpace(product.ExternalSKUID) == "" {
		return nil, errors.New("original external identifiers are required")
	}
	if product.Count != items[0].Quantity || payload.Request.Price.OrderPrice != moneyCents(order.TotalAmount) || product.RealPrice != moneyCents(order.TotalAmount) {
		return nil, errors.New("legacy amount or quantity mismatch")
	}
	var configAudit, syncAudit model.AuditLog
	for _, entry := range []struct {
		id     uint
		action string
		row    *model.AuditLog
	}{{in.ConfigureAuditID, "xiaohongshu.product.configure", &configAudit}, {in.SyncAuditID, "xiaohongshu.product.sync", &syncAudit}} {
		if err := query().Where("id = ? AND tenant_id = ? AND scope = ? AND action = ? AND target_type = ?", entry.id, in.TenantID, "tenant", entry.action, "channel_product_mapping").First(entry.row).Error; err != nil {
			return nil, errors.New("matching historical audit not found")
		}
	}
	if configAudit.TargetID == 0 || configAudit.TargetID != syncAudit.TargetID || !configAudit.CreatedAt.Before(syncAudit.CreatedAt) || !syncAudit.CreatedAt.Before(order.CreatedAt) || syncAudit.AfterJSON != "production" {
		return nil, errors.New("invalid historical configure/sync/sale sequence")
	}
	var mapping model.ChannelProductMapping
	if err := query().Where("id = ? AND channel_account_id = ? AND product_id = ? AND external_code = ?", configAudit.TargetID, in.ChannelAccountID, items[0].ProductID, product.ExternalProductID).First(&mapping).Error; err != nil {
		return nil, errors.New("historical product mapping mismatch")
	}
	if !mapping.CreatedAt.Equal(mapping.UpdatedAt) || mapping.CreatedAt.After(configAudit.CreatedAt) {
		return nil, errors.New("historical mapping was changed")
	}
	var configuration XiaohongshuProductConfigInput
	if err := json.Unmarshal([]byte(configAudit.AfterJSON), &configuration); err != nil {
		return nil, errors.New("invalid historical configuration")
	}
	if configuration.ProductType != xiaohongshu.ProductTypeGroupVoucher || configuration.ExternalSKUID != product.ExternalSKUID {
		return nil, errors.New("historical type or SKU mismatch")
	}
	var intervening int64
	if err := tx.Model(&model.AuditLog{}).Where("tenant_id = ? AND scope = ? AND action = ? AND target_type = ? AND target_id = ? AND created_at >= ? AND created_at <= ? AND id <> ?", in.TenantID, "tenant", "xiaohongshu.product.configure", "channel_product_mapping", mapping.ID, configAudit.CreatedAt, order.CreatedAt, configAudit.ID).Count(&intervening).Error; err != nil {
		return nil, err
	}
	if intervening != 0 {
		return nil, errors.New("ambiguous historical configuration")
	}
	return &legacyXiaohongshuTypeEvidence{Version: 1, Source: "local_history_user_confirmed", TenantID: in.TenantID, ChannelAccountID: in.ChannelAccountID, OrderID: order.ID, OrderNo: order.OrderNo, LinkID: link.ID, OperationID: op.ID, CiphertextHash: legacyCiphertextHash(op.RequestPayloadCiphertext), ExternalOrderID: link.ExternalOrderID, ExternalProductID: product.ExternalProductID, ExternalSKUID: product.ExternalSKUID, MappingID: mapping.ID, ProductType: 1, ConfigureAuditID: configAudit.ID, ConfigureAuditHash: legacyEvidenceHash(configAudit), SyncAuditID: syncAudit.ID, SyncAuditHash: legacyEvidenceHash(syncAudit), ApproverUserID: in.ApproverUserID, Executor: in.Executor, ApprovalReference: in.ApprovalReference, Reason: in.Reason}, nil
}

// Manual refunds alone may consume an explicit attestation. Self-service
// continues to require the original sale-time ProductType snapshot.
func requireXiaohongshuRefundProductTypeTx(tx *gorm.DB, order *model.Order, link *model.XiaohongshuOrderLink, op *model.XiaohongshuOrderOperation, payload *xiaohongshuOrderOperationPayload) error {
	if payload.ProductType == xiaohongshu.ProductTypeGroupVoucher {
		return nil
	}
	if payload.ProductType != 0 {
		return errors.New("小红书当前仅支持普通团购券退款")
	}
	var rows []model.AuditLog
	if err := legacyEvidenceQuery(tx, order.TenantID, op.ID).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("小红书历史订单缺少商品类型快照，需核实并记录兼容依据后再退款")
	}
	row := rows[0]
	var evidence legacyXiaohongshuTypeEvidence
	decoder := json.NewDecoder(bytes.NewBufferString(row.AfterJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return errors.New("小红书历史订单兼容依据无效")
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return errors.New("小红书历史订单兼容依据无效")
	}
	if evidence.Version != 1 || evidence.Source != "local_history_user_confirmed" || evidence.TenantID != order.TenantID || evidence.ChannelAccountID != order.ChannelAccountID || evidence.OrderID != order.ID || evidence.OrderNo != order.OrderNo || evidence.LinkID != link.ID || evidence.OperationID != op.ID || evidence.CiphertextHash != legacyCiphertextHash(op.RequestPayloadCiphertext) || row.ActorUserID != 0 || row.ActorRole != "maintenance" || row.Reason != evidence.Reason || !row.CreatedAt.Equal(row.UpdatedAt) {
		return errors.New("小红书历史订单兼容依据与原订单不匹配")
	}
	rebuilt, err := buildLegacyXiaohongshuEvidenceTx(tx, LegacyXiaohongshuEvidenceInput{TenantID: order.TenantID, ChannelAccountID: order.ChannelAccountID, OrderNo: order.OrderNo, ApproverUserID: evidence.ApproverUserID, ConfigureAuditID: evidence.ConfigureAuditID, SyncAuditID: evidence.SyncAuditID, Reason: evidence.Reason, Executor: evidence.Executor, ApprovalReference: evidence.ApprovalReference}, false)
	if err != nil || legacyEvidenceHash(rebuilt) != legacyEvidenceHash(evidence) {
		return errors.New("小红书历史订单兼容依据已变化，需人工复核")
	}
	return nil
}
