package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func moneyCents(value float64) int64 {
	return int64(math.Round(value * 100))
}

func centsMoney(value int64) float64 {
	return float64(value) / 100
}

// appendLedgerEntryTx appends one immutable cent-based fact. It is intentionally
// transaction-scoped so the legacy balance projection and the new ledger can
// never commit separately.
func appendLedgerEntryTx(tx *gorm.DB, account *model.CapitalAccount, entryType string, amountCents int64, idempotencyKey, orderNo, fulfillmentNo, memo string, operatorID uint) error {
	if account == nil || account.ID == 0 {
		return errors.New("capital account is required")
	}
	if strings.TrimSpace(entryType) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return errors.New("ledger entry type and idempotency key are required")
	}
	var existing model.LedgerEntry
	err := tx.Where("account_id = ? AND idempotency_key = ?", account.ID, idempotencyKey).First(&existing).Error
	if err == nil {
		if existing.AmountCents != amountCents || existing.EntryType != entryType {
			return fmt.Errorf("ledger idempotency key reused with different data")
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&model.LedgerEntry{
		AccountID: account.ID, OwnerTenantID: account.OwnerTenantID, ManagerTenantID: account.ManagerTenantID,
		EntryType: entryType, AmountCents: amountCents, BalanceCents: moneyCents(account.Balance),
		UsedCreditCents: moneyCents(account.UsedCredit), FrozenCents: moneyCents(account.FrozenAmount),
		IdempotencyKey: idempotencyKey, RelatedOrderNo: orderNo, RelatedFulfillment: fulfillmentNo,
		OperatorID: operatorID, Memo: strings.TrimSpace(memo),
	}).Error
}

// ListLedger returns only the ledger for accounts owned by the requested
// distributor. Supplier-side access is handled separately by a scoped query.
func (s *FinanceService) ListLedger(ownerTenantID uint, page, pageSize int, accountID uint) ([]model.LedgerEntry, int64, error) {
	if ownerTenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.LedgerEntry{}).Where("owner_tenant_id = ?", ownerTenantID)
	if accountID > 0 {
		query = query.Where("account_id = ?", accountID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var entries []model.LedgerEntry
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
