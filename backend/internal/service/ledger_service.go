package service

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

func moneyCents(value float64) int64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	// Parse the shortest decimal representation instead of multiplying the
	// binary float directly. This makes 0.29, 0.1+0.2 and half-cent inputs
	// deterministic at the domain boundary.
	rational, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	if !ok {
		return 0
	}
	rational.Mul(rational, big.NewRat(100, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(rational.Num(), rational.Denom(), remainder)
	if remainder.Sign() != 0 {
		absoluteRemainder := new(big.Int).Abs(remainder)
		doubled := new(big.Int).Lsh(absoluteRemainder, 1)
		if doubled.Cmp(rational.Denom()) >= 0 {
			if rational.Num().Sign() < 0 {
				quotient.Sub(quotient, big.NewInt(1))
			} else {
				quotient.Add(quotient, big.NewInt(1))
			}
		}
	}
	if !quotient.IsInt64() {
		return 0
	}
	return quotient.Int64()
}

func centsMoney(value int64) float64 {
	return float64(value) / 100
}

func syncCapitalAccountCents(account *model.CapitalAccount) {
	if account == nil {
		return
	}
	// A zero cent projection with a non-zero legacy value identifies a row
	// written before migration 31. Keep those rows usable during rollout.
	if account.BalanceCents == 0 && account.Balance != 0 {
		account.BalanceCents = moneyCents(account.Balance)
	}
	if account.CreditLineCents == 0 && account.CreditLine != 0 {
		account.CreditLineCents = moneyCents(account.CreditLine)
	}
	if account.UsedCreditCents == 0 && account.UsedCredit != 0 {
		account.UsedCreditCents = moneyCents(account.UsedCredit)
	}
	if account.FrozenCents == 0 && account.FrozenAmount != 0 {
		account.FrozenCents = moneyCents(account.FrozenAmount)
	}
}

func syncCapitalAccountProjection(account *model.CapitalAccount) {
	if account == nil {
		return
	}
	account.Balance = centsMoney(account.BalanceCents)
	account.CreditLine = centsMoney(account.CreditLineCents)
	account.UsedCredit = centsMoney(account.UsedCreditCents)
	account.FrozenAmount = centsMoney(account.FrozenCents)
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
	syncCapitalAccountCents(account)
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
		EntryType: entryType, AmountCents: amountCents, BalanceCents: account.BalanceCents,
		UsedCreditCents: account.UsedCreditCents, FrozenCents: account.FrozenCents,
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

func (s *FinanceService) ListManagedLedger(managerTenantID uint, page, pageSize int, accountID uint) ([]model.LedgerEntry, int64, error) {
	if managerTenantID == 0 {
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
	query := model.DB.Model(&model.LedgerEntry{}).Where("manager_tenant_id = ?", managerTenantID)
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
