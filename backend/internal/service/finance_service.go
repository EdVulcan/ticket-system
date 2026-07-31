package service

import (
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FinanceService struct{}

func financeDocumentNo(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// RechargeAccount records a supplier-approved recharge and its cent ledger
// fact in one transaction. Repeating the idempotency key returns the original
// document without applying the balance twice.
func (s *FinanceService) RechargeAccount(managerTenantID, ownerTenantID uint, amountCents int64, idempotencyKey string, operatorID uint, memo string) (*model.FinancialDocument, error) {
	if managerTenantID == 0 || ownerTenantID == 0 || amountCents <= 0 || strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("manager, owner, amount and idempotency key are required")
	}
	var document model.FinancialDocument
	err := model.Write(func(tx *gorm.DB) error {
		var existing model.FinancialDocument
		if err := tx.Where("tenant_id = ? AND idempotency_key = ?", managerTenantID, idempotencyKey).First(&existing).Error; err == nil {
			if existing.Type != "recharge" || existing.CounterpartyTenantID != ownerTenantID || existing.AmountCents != amountCents {
				return errors.New("recharge idempotency key was reused with different data")
			}
			document = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var account model.CapitalAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_tenant_id = ? AND manager_tenant_id = ? AND status = ?", ownerTenantID, managerTenantID, "active").First(&account).Error; err != nil {
			return errors.New("distribution capital account not found")
		}
		syncCapitalAccountCents(&account)
		account.BalanceCents += amountCents
		syncCapitalAccountProjection(&account)
		if err := tx.Model(&account).Update("balance", account.Balance).Error; err != nil {
			return err
		}
		if err := tx.Model(&account).Updates(map[string]interface{}{
			"balance_cents": account.BalanceCents, "credit_line_cents": account.CreditLineCents,
			"used_credit_cents": account.UsedCreditCents, "frozen_cents": account.FrozenCents,
		}).Error; err != nil {
			return err
		}
		memo = strings.TrimSpace(memo)
		if memo == "" {
			memo = "supplier recharge"
		}
		if err := tx.Create(&model.TransactionRecord{AccountID: account.ID, Type: "deposit", Amount: centsMoney(amountCents), BalanceAfter: account.Balance, AmountCents: amountCents, BalanceAfterCents: account.BalanceCents, Memo: memo, OperatorID: operatorID}).Error; err != nil {
			return err
		}
		if err := appendLedgerEntryTx(tx, &account, "recharge", amountCents, "recharge:"+idempotencyKey, "", "", memo, operatorID); err != nil {
			return err
		}
		document = model.FinancialDocument{TenantID: managerTenantID, DocumentNo: financeDocumentNo("RC"), IdempotencyKey: idempotencyKey, Type: "recharge", Status: "approved", AmountCents: amountCents, CounterpartyTenantID: ownerTenantID, Description: memo, ApprovedBy: operatorID}
		now := time.Now()
		document.ApprovedAt = &now
		return tx.Create(&document).Error
	})
	if err != nil {
		return nil, err
	}
	return &document, nil
}

func (s *FinanceService) CreateDocument(tenantID uint, document *model.FinancialDocument) error {
	if tenantID == 0 || document == nil || strings.TrimSpace(document.Type) == "" || document.AmountCents < 0 {
		return errors.New("tenant, document type and non-negative amount are required")
	}
	document.Base = model.Base{}
	document.TenantID = tenantID
	document.DocumentNo = financeDocumentNo("FD")
	if strings.TrimSpace(document.IdempotencyKey) == "" {
		document.IdempotencyKey = "document:" + document.DocumentNo
	}
	document.Status = "draft"
	return model.Write(func(tx *gorm.DB) error { return tx.Create(document).Error })
}

func (s *FinanceService) ListDocuments(tenantID uint, status, docType string, page, pageSize int) ([]model.FinancialDocument, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.FinancialDocument{}).Where("tenant_id = ?", tenantID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if docType != "" {
		query = query.Where("type = ?", docType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.FinancialDocument
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

func (s *FinanceService) ApproveDocument(tenantID, documentID, operatorID uint, evidence string) (*model.FinancialDocument, error) {
	var document model.FinancialDocument
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", documentID, tenantID).First(&document).Error; err != nil {
			return err
		}
		if document.Status != "draft" && document.Status != "submitted" {
			return errors.New("financial document is not awaiting approval")
		}
		now := time.Now()
		updates := map[string]interface{}{"status": "approved", "approved_by": operatorID, "approved_at": now}
		if strings.TrimSpace(evidence) != "" {
			updates["evidence_json"] = evidence
		}
		if err := tx.Model(&document).Updates(updates).Error; err != nil {
			return err
		}
		document.Status, document.ApprovedBy, document.ApprovedAt = "approved", operatorID, &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &document, nil
}

// GetCapitalAccount 获取当前租户的资金账户信息 (对于分销商)
// 实际上可能存在多个账户(对应不同供应商)，这里我们需要根据 supplierTenantID 获取特定账户，
// 或者列出所有账户。
// 为了简化 FinanceView，我们先做一个 "ListAccounts" 或者 "GetAccountBySupplier".
// 实际上 Finance页面可能想看"总览"。
// 让我们先实现 ListAccounts (获取该租户作为分销商的所有资金账户)
func (s *FinanceService) ListAccounts(agentTenantID uint) ([]map[string]interface{}, error) {
	var accounts []model.CapitalAccount
	// Preload Supplier to show who holds the money
	if err := model.DB.Preload("ManagerTenant").Where("owner_tenant_id = ?", agentTenantID).Find(&accounts).Error; err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0)
	for _, acc := range accounts {
		result = append(result, map[string]interface{}{
			"id":               acc.ID,
			"supplier_name":    acc.ManagerTenant.Name,
			"supplier_code":    acc.ManagerTenant.SystemCode,
			"supplier_contact": acc.ManagerTenant.Contact,
			"supplier_phone":   acc.ManagerTenant.Phone,
			"balance":          acc.Balance,
			"credit_line":      acc.CreditLine,
			"frozen":           acc.FrozenAmount,
			"status":           acc.Status,
		})
	}
	return result, nil
}

// ListTransactions 获取交易流水
// 可以筛选: account_id (特定供应商账户), type (deposit/payment), order_no, date range
func (s *FinanceService) ListTransactions(agentTenantID uint, page, pageSize int, filters map[string]interface{}) ([]model.TransactionRecord, int64, error) {
	var records []model.TransactionRecord
	var total int64
	offset := (page - 1) * pageSize

	// Join CapitalAccount to ensure we only show records owned by this agent
	query := model.DB.Model(&model.TransactionRecord{}).
		Joins("JOIN capital_accounts ON transaction_records.account_id = capital_accounts.id").
		Where("capital_accounts.owner_tenant_id = ?", agentTenantID)

	if val, ok := filters["supplier_id"]; ok && val != "" {
		// Filter by specific capital account (supplier)
		query = query.Where("capital_accounts.manager_tenant_id = ?", val)
	}

	if val, ok := filters["type"]; ok && val != "" {
		query = query.Where("transaction_records.type = ?", val)
	}

	if val, ok := filters["start_date"]; ok && val != "" {
		query = query.Where("transaction_records.created_at >= ?", val.(string)+" 00:00:00")
	}
	if val, ok := filters["end_date"]; ok && val != "" {
		query = query.Where("transaction_records.created_at <= ?", val.(string)+" 23:59:59")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Preload Account to show context if needed
	if err := query.Order("transaction_records.created_at desc").Offset(offset).Limit(pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}

	return records, total, nil
}
