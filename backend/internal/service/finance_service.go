package service

import (
	"ticket-backend/internal/model"
)

type FinanceService struct{}

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
			"id":            acc.ID,
			"supplier_name": acc.ManagerTenant.Name,
			"supplier_code": acc.ManagerTenant.SystemCode,
			"balance":       acc.Balance,
			"credit_line":   acc.CreditLine,
			"frozen":        acc.FrozenAmount,
			"status":        acc.Status,
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
