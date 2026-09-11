package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/xiaohongshu"
	"time"

	"gorm.io/gorm"
)

const xiaohongshuProductAuditInterval = 5 * time.Minute

var (
	ErrXiaohongshuProductAuditIdentity = errors.New("xiaohongshu product audit response does not match the configured product")
	ErrXiaohongshuProductAuditState    = errors.New("xiaohongshu product audit response has an unrecognized status")
)

// XiaohongshuProductAuditService reconciles the provider review gate. It has
// no publish operation: refreshing a review can never resubmit a product.
type XiaohongshuProductAuditService struct {
	Products XiaohongshuProductService
	Now      func() time.Time
}

type xiaohongshuProductAuditTarget struct {
	config  model.XiaohongshuProductConfig
	mapping model.ChannelProductMapping
	account model.ChannelAccount
}

func NewXiaohongshuProductAuditService() XiaohongshuProductAuditService {
	return XiaohongshuProductAuditService{Products: NewXiaohongshuProductService()}
}

// RefreshAudit checks one mapping after establishing tenant, channel-account,
// mapping, product and account-status scope locally. A changed local revision
// wins over an in-flight provider response through the updated_at predicate.
func (s XiaohongshuProductAuditService) RefreshAudit(ctx context.Context, tenantID, accountID, mappingID uint) (*XiaohongshuProductConfigView, error) {
	target, err := s.loadRefreshTarget(tenantID, accountID, mappingID)
	if err != nil {
		return nil, err
	}
	client, _, err := s.Products.client(tenantID, accountID)
	if err != nil {
		return s.recordFailedCheck(target, err)
	}
	result, err := client.GetLocalLifeProductAudit(ctx, target.mapping.ExternalCode)
	if err != nil {
		return s.recordFailedCheck(target, err)
	}
	if strings.TrimSpace(result.ExternalProductID) != strings.TrimSpace(target.mapping.ExternalCode) {
		return s.recordFailedCheck(target, ErrXiaohongshuProductAuditIdentity)
	}
	if result.BusinessUpdatedAt > 0 && target.config.LastSyncedAt != nil && result.BusinessUpdatedAt < target.config.LastSyncedAt.Unix() {
		return s.recordFailedCheck(target, errors.New("xiaohongshu product audit response predates the current submission"))
	}
	status, message, auditedAt, statusErr := xiaohongshuProductAuditResult(result)
	checkError := ""
	if statusErr != nil {
		checkError = "小红书审核状态无法识别"
	}
	if status == "rejected" && strings.TrimSpace(message) == "" {
		var detailsErr error
		message, auditedAt, detailsErr = s.rejectionDetails(ctx, target, auditedAt)
		if detailsErr != nil {
			// Missing diagnostics must never prevent the authoritative REJECT
			// from making a previously approved product unavailable.
			checkError = "商品审核已拒绝，读取历史驳回原因失败"
		}
	}
	view, applied, writeErr := s.applyCheck(target, status, message, auditedAt, checkError)
	if writeErr != nil {
		return nil, writeErr
	}
	if !applied {
		return s.Products.GetConfig(tenantID, accountID, mappingID)
	}
	if statusErr != nil {
		return view, statusErr
	}
	return view, nil
}

// A product query can omit audit_info entirely. Preserve a matching review's
// details, or recover them from the authenticated encrypted inbox. This only
// supplements an authoritative REJECT; it cannot approve a product.
func (s XiaohongshuProductAuditService) rejectionDetails(ctx context.Context, target *xiaohongshuProductAuditTarget, queryTime *time.Time) (string, *time.Time, error) {
	config := &target.config
	if config.LastSyncedAt == nil {
		return "", queryTime, nil
	}
	if queryTime != nil && queryTime.Unix() < config.LastSyncedAt.Unix() {
		return "", nil, nil
	}
	matchesReview := func(at *time.Time) bool {
		return at != nil &&
			at.Unix() >= config.LastSyncedAt.Unix() &&
			(queryTime == nil || at.Unix() == queryTime.Unix())
	}
	if config.AuditStatus == "rejected" && strings.TrimSpace(config.AuditMessage) != "" && matchesReview(config.AuditedAt) {
		return config.AuditMessage, config.AuditedAt, nil
	}
	var events []model.XiaohongshuWebhookEvent
	if err := model.DB.WithContext(ctx).
		Where("tenant_id = ? AND channel_account_id = ? AND UPPER(event_type) = ? AND received_at >= ?",
			config.TenantID, config.ChannelAccountID, "PRODUCT_AUDIT", *config.LastSyncedAt).
		Order("id DESC").Limit(100).Find(&events).Error; err != nil {
		return "", queryTime, err
	}
	message, auditedAt := "", queryTime
	for _, event := range events {
		payload, err := utils.DecryptAES(event.PayloadCiphertext)
		if err != nil {
			continue
		}
		product, status, reason, at := parseXiaohongshuProductAudit([]byte(payload))
		if product != strings.TrimSpace(target.mapping.ExternalCode) || status != "rejected" || !matchesReview(at) {
			continue
		}
		if auditedAt == nil || at.After(*auditedAt) || (at.Equal(*auditedAt) && message == "") {
			message, auditedAt = reason, at
		}
	}
	return message, auditedAt, nil
}

// ProcessProductAuditRefreshes advances a bounded, restart-safe fair batch.
// The persisted check timestamp also schedules provider failures, preventing a
// failed account from monopolising every one-minute worker tick.
func (s XiaohongshuProductAuditService) ProcessProductAuditRefreshes(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	if limit > 100 {
		limit = 100
	}
	dueBefore := s.now().Add(-xiaohongshuProductAuditInterval)
	type target struct {
		TenantID  uint
		AccountID uint
		MappingID uint
	}
	var targets []target
	if err := model.DB.WithContext(ctx).Table("xiaohongshu_product_configs AS config").
		Select("config.tenant_id, config.channel_account_id AS account_id, config.channel_product_mapping_id AS mapping_id").
		Joins("JOIN channel_accounts AS account ON account.id = config.channel_account_id AND account.deleted_at IS NULL").
		Joins("JOIN channel_product_mappings AS mapping ON mapping.id = config.channel_product_mapping_id AND mapping.deleted_at IS NULL").
		Joins("JOIN products AS product ON product.id = mapping.product_id AND product.deleted_at IS NULL").
		Joins("JOIN tenants AS tenant ON tenant.id = config.tenant_id AND tenant.deleted_at IS NULL").
		Where("config.deleted_at IS NULL AND config.sync_status IN ? AND account.type = ? AND account.status IN ? AND mapping.status = ? AND product.status = ? AND tenant.status = ?", []string{"submitted", "synced"}, "xiaohongshu", []string{"active", "sandbox"}, "active", "online", "active").
		Where("config.audit_checked_at IS NULL OR config.audit_checked_at <= ?", dueBefore).
		Order("(config.audit_checked_at IS NOT NULL) ASC, config.audit_checked_at ASC, config.id ASC").
		Limit(limit).Scan(&targets).Error; err != nil {
		return 0, err
	}
	processed := 0
	failed := false
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		processed++
		// A single unavailable provider account is represented durably on its
		// config and must not stop other accounts in this fair batch.
		if _, err := s.RefreshAudit(ctx, target.TenantID, target.AccountID, target.MappingID); err != nil {
			failed = true
		}
	}
	if failed {
		return processed, errors.New("one or more xiaohongshu product audit refreshes failed")
	}
	return processed, nil
}

func (s XiaohongshuProductAuditService) loadRefreshTarget(tenantID, accountID, mappingID uint) (*xiaohongshuProductAuditTarget, error) {
	var tenant model.Tenant
	if err := model.DB.Where("id = ? AND status = ?", tenantID, "active").First(&tenant).Error; err != nil {
		return nil, errors.New("租户不可用")
	}
	if err := loadXiaohongshuMappingTx(model.DB, tenantID, accountID, mappingID, nil, nil); err != nil {
		return nil, err
	}
	var config model.XiaohongshuProductConfig
	if err := model.DB.Where("tenant_id = ? AND channel_account_id = ? AND channel_product_mapping_id = ? AND sync_status IN ?", tenantID, accountID, mappingID, []string{"submitted", "synced"}).First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("小红书商品尚未提交审核")
		}
		return nil, err
	}
	var mapping model.ChannelProductMapping
	if err := model.DB.Where("id = ? AND channel_account_id = ? AND status = ?", mappingID, accountID, "active").First(&mapping).Error; err != nil {
		return nil, errors.New("小红书商品映射不存在或已停用")
	}
	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type = ? AND status IN ?", accountID, tenantID, "xiaohongshu", []string{"active", "sandbox"}).First(&account).Error; err != nil {
		return nil, errors.New("小红书渠道账号不可用")
	}
	return &xiaohongshuProductAuditTarget{config: config, mapping: mapping, account: account}, nil
}

func (s XiaohongshuProductAuditService) recordFailedCheck(target *xiaohongshuProductAuditTarget, cause error) (*XiaohongshuProductConfigView, error) {
	view, applied, err := s.applyCheck(target, "", "", nil, xiaohongshuAuditCheckError(cause))
	if err != nil {
		return nil, err
	}
	if !applied {
		view, err = s.Products.GetConfig(target.config.TenantID, target.config.ChannelAccountID, target.config.ChannelProductMappingID)
		if err != nil {
			return nil, err
		}
	}
	if errors.Is(cause, ErrXiaohongshuProductAuditIdentity) || errors.Is(cause, ErrXiaohongshuProductAuditState) {
		return view, cause
	}
	return view, errors.New("小红书审核查询失败")
}

func (s XiaohongshuProductAuditService) applyCheck(target *xiaohongshuProductAuditTarget, status, message string, auditedAt *time.Time, checkError string) (*XiaohongshuProductConfigView, bool, error) {
	config := &target.config
	now := s.now()
	query := `
		UPDATE xiaohongshu_product_configs AS config
		SET audit_checked_at = ?, audit_check_error = ?, updated_at = ?`
	args := []interface{}{now, truncateChannelError(checkError), now}
	if status != "" {
		query += `, audit_status = ?, audit_message = ?`
		args = append(args, status, truncateChannelError(message))
		if auditedAt == nil {
			// Audit timestamps are provider facts. Never substitute polling time.
			query += `, audited_at = NULL`
		} else {
			query += `, audited_at = ?`
			args = append(args, auditedAt)
		}
	}
	query += `
		WHERE config.id = ? AND config.tenant_id = ? AND config.channel_account_id = ? AND config.updated_at = ? AND config.deleted_at IS NULL
		  AND EXISTS (
			SELECT 1 FROM channel_accounts AS account
			WHERE account.id = config.channel_account_id AND account.tenant_id = ?
			  AND account.type = 'xiaohongshu' AND account.status IN ('active','sandbox')
			  AND account.app_id = ? AND account.environment = ? AND account.key_version = ?
			  AND account.updated_at = ? AND account.deleted_at IS NULL
		  )
		  AND EXISTS (
			SELECT 1 FROM channel_product_mappings AS mapping
			WHERE mapping.id = config.channel_product_mapping_id AND mapping.channel_account_id = config.channel_account_id
			  AND mapping.status = 'active' AND mapping.external_code = ? AND mapping.updated_at = ? AND mapping.deleted_at IS NULL
		  )
		  AND EXISTS (
			SELECT 1 FROM tenants AS tenant
			WHERE tenant.id = config.tenant_id AND tenant.status = 'active' AND tenant.deleted_at IS NULL
		  )`
	args = append(args,
		config.ID, config.TenantID, config.ChannelAccountID, config.UpdatedAt,
		target.account.TenantID, target.account.AppID, target.account.Environment, target.account.KeyVersion, target.account.UpdatedAt,
		target.mapping.ExternalCode, target.mapping.UpdatedAt,
	)
	result := model.DB.Exec(query, args...)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, false, nil
	}
	view, err := s.Products.GetConfig(config.TenantID, config.ChannelAccountID, config.ChannelProductMappingID)
	return view, true, err
}

func xiaohongshuProductAuditResult(result *xiaohongshu.LocalLifeProductAudit) (string, string, *time.Time, error) {
	if result == nil {
		return "pending", "小红书审核查询结果为空，商品保持不可售", nil, ErrXiaohongshuProductAuditState
	}
	var auditedAt *time.Time
	if result.AuditInfo.AuditedAt > 0 {
		value := time.Unix(result.AuditInfo.AuditedAt, 0)
		auditedAt = &value
	}
	switch strings.ToUpper(strings.TrimSpace(result.AuditStatus)) {
	case "PASS":
		return "approved", "", auditedAt, nil
	case "REJECT":
		return "rejected", result.AuditInfo.RejectReason, auditedAt, nil
	case "AUDITING":
		return "pending", "", nil, nil
	default:
		return "pending", "小红书审核状态无法识别，商品保持不可售", nil, ErrXiaohongshuProductAuditState
	}
}

func xiaohongshuAuditCheckError(err error) string {
	var apiErr *xiaohongshu.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("小红书审核查询失败（错误码 %d）", apiErr.Code)
	}
	return "小红书审核查询失败"
}

func (s XiaohongshuProductAuditService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
