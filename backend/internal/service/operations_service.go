package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OperationsService struct{}

type POSHoldView struct {
	model.POSHold
	Items []model.POSHoldLine `json:"items"`
}

// POSPaymentSummary is the cashier-facing breakdown for one payment method.
// Gross and refund amounts are kept in cents and are calculated from the
// immutable payment/refund facts belonging to the shift.
type POSPaymentSummary struct {
	Method        string `json:"method"`
	PaymentCount  int64  `json:"payment_count"`
	GrossCents    int64  `json:"gross_cents"`
	RefundCents   int64  `json:"refund_cents"`
	NetCents      int64  `json:"net_cents"`
}

type POSShiftSummary struct {
	Shift            model.POSShift       `json:"shift"`
	Payments         []POSPaymentSummary  `json:"payments"`
	RefundCount      int64                `json:"refund_count"`
	OrderCount       int64                `json:"order_count"`
	CashExpectedCents int64               `json:"cash_expected_cents"`
}

func generatePOSHoldNo() string {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return fmt.Sprintf("HOLD%d%s", time.Now().UnixMilli(), strings.ToUpper(hex.EncodeToString(raw)))
}

// CreatePOSHold persists a cashier draft without reserving inventory. The
// server calculates the current total and verifies the operator's open shift;
// ResumePOSHold performs the same product checks before it can be sold.
func (s *OperationsService) CreatePOSHold(tenantID, deviceID, operatorID, shiftID uint, lines []model.POSHoldLine, contactName, contactPhone, notes string, ttl time.Duration) (*POSHoldView, error) {
	if tenantID == 0 || deviceID == 0 || operatorID == 0 || shiftID == 0 {
		return nil, errors.New("tenant, device, operator and shift are required")
	}
	if len(lines) == 0 {
		return nil, errors.New("at least one product is required")
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 2 * time.Hour
	}
	clean := make([]model.POSHoldLine, 0, len(lines))
	seen := make(map[uint]int)
	for _, line := range lines {
		if line.ProductID == 0 || line.Quantity <= 0 {
			return nil, errors.New("product and positive quantity are required")
		}
		seen[line.ProductID] += line.Quantity
	}
	for productID, quantity := range seen {
		clean = append(clean, model.POSHoldLine{ProductID: productID, Quantity: quantity})
	}
	// Stable ordering makes the persisted draft and idempotent client retries
	// deterministic without trusting any client-provided amount.
	for i := 1; i < len(clean); i++ {
		for j := i; j > 0 && clean[j].ProductID < clean[j-1].ProductID; j-- {
			clean[j], clean[j-1] = clean[j-1], clean[j]
		}
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	var hold model.POSHold
	err = model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", deviceID, tenantID, "pos").First(&device).Error; err != nil {
			return errors.New("POS device not found")
		}
		var shift model.POSShift
		if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", shiftID, tenantID, deviceID, operatorID, "open").First(&shift).Error; err != nil {
			return errors.New("open POS shift not found")
		}
		var totalCents int64
		for _, line := range clean {
			var product model.Product
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND type = ?", line.ProductID, tenantID, "online", "offline").First(&product).Error; err != nil {
				return fmt.Errorf("product %d is unavailable", line.ProductID)
			}
			totalCents += moneyCents(product.Price) * int64(line.Quantity)
		}
		now := time.Now()
		hold = model.POSHold{
			TenantID: tenantID, DeviceID: deviceID, OperatorID: operatorID,
			HoldNo: generatePOSHoldNo(), Status: "held", ItemsJSON: string(encoded),
			ContactName: strings.TrimSpace(contactName), ContactPhone: strings.TrimSpace(contactPhone),
			TotalCents: totalCents, ExpiresAt: now.Add(ttl), Notes: strings.TrimSpace(notes),
		}
		return tx.Create(&hold).Error
	})
	if err != nil {
		return nil, err
	}
	return &POSHoldView{POSHold: hold, Items: clean}, nil
}

func decodePOSHold(hold *model.POSHold) (*POSHoldView, error) {
	var lines []model.POSHoldLine
	if err := json.Unmarshal([]byte(hold.ItemsJSON), &lines); err != nil {
		return nil, err
	}
	return &POSHoldView{POSHold: *hold, Items: lines}, nil
}

func (s *OperationsService) ListPOSHolds(tenantID, operatorID uint, status string, page, pageSize int) ([]POSHoldView, int64, error) {
	if tenantID == 0 {
		return nil, 0, errors.New("tenant is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := model.DB.Model(&model.POSHold{}).Where("tenant_id = ?", tenantID)
	if operatorID > 0 {
		query = query.Where("operator_id = ?", operatorID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.POSHold
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	views := make([]POSHoldView, 0, len(rows))
	for i := range rows {
		view, err := decodePOSHold(&rows[i])
		if err != nil {
			return nil, 0, err
		}
		views = append(views, *view)
	}
	return views, total, nil
}

// ResumePOSHold consumes a held draft. The caller can use the returned lines
// to create an order; the order service rechecks availability and price, so a
// stale hold can never authorize an old price or product.
func (s *OperationsService) ResumePOSHold(tenantID, holdID, operatorID uint) (*POSHoldView, error) {
	var view *POSHoldView
	err := model.Write(func(tx *gorm.DB) error {
		var hold model.POSHold
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", holdID, tenantID).First(&hold).Error; err != nil {
			return err
		}
		if hold.OperatorID != operatorID {
			return errors.New("hold belongs to another operator")
		}
		if hold.Status != "held" {
			return fmt.Errorf("hold cannot resume from status %s", hold.Status)
		}
		if !hold.ExpiresAt.After(time.Now()) {
			if err := tx.Model(&hold).Update("status", "expired").Error; err != nil {
				return err
			}
			return errors.New("hold has expired")
		}
		decoded, err := decodePOSHold(&hold)
		if err != nil {
			return err
		}
		lines := decoded.Items
		for _, line := range lines {
			var product model.Product
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND type = ?", line.ProductID, tenantID, "online", "offline").First(&product).Error; err != nil {
				return fmt.Errorf("product %d is no longer available", line.ProductID)
			}
		}
		if err := tx.Model(&hold).Updates(map[string]interface{}{"status": "resumed"}).Error; err != nil {
			return err
		}
		hold.Status = "resumed"
		view = &POSHoldView{POSHold: hold, Items: lines}
		return nil
	})
	return view, err
}

func (s *OperationsService) CancelPOSHold(tenantID, holdID, operatorID uint, role, reason string) (*model.POSHold, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("cancellation reason is required")
	}
	var hold model.POSHold
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", holdID, tenantID).First(&hold).Error; err != nil {
			return err
		}
		if role != "admin" && role != "super_admin" && hold.OperatorID != operatorID {
			return errors.New("hold belongs to another operator")
		}
		if hold.Status != "held" {
			return fmt.Errorf("hold cannot cancel from status %s", hold.Status)
		}
		now := time.Now()
		hold.Status, hold.CancelledAt, hold.Notes = "cancelled", &now, strings.TrimSpace(reason)
		return tx.Model(&hold).Updates(map[string]interface{}{"status": hold.Status, "cancelled_at": now, "notes": hold.Notes}).Error
	})
	return &hold, err
}

func (s *OperationsService) ExpirePOSHolds(now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	count := 0
	err := model.Write(func(tx *gorm.DB) error {
		var rows []model.POSHold
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND expires_at <= ?", "held", now).Order("id").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			if err := tx.Model(&rows[i]).Update("status", "expired").Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *OperationsService) OpenShift(tenantID, deviceID, operatorID uint, openingCents int64) (*model.POSShift, error) {
	if tenantID == 0 || deviceID == 0 || operatorID == 0 || openingCents < 0 {
		return nil, errors.New("tenant, device, operator and opening amount are required")
	}
	var shift model.POSShift
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ?", deviceID, tenantID).First(&device).Error; err != nil {
			return errors.New("device not found")
		}
		if device.Type != "pos" {
			return errors.New("device is not a POS terminal")
		}
		var count int64
		if err := tx.Model(&model.POSShift{}).Where("tenant_id = ? AND device_id = ? AND status = ?", tenantID, deviceID, "open").Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("device already has an open shift")
		}
		now := time.Now()
		shift = model.POSShift{TenantID: tenantID, ScenicAreaID: device.ScenicAreaID, DeviceID: deviceID, OperatorID: operatorID, ShiftNo: fmt.Sprintf("SHIFT-%d-%d", now.UnixNano(), deviceID), Status: "open", OpeningCents: openingCents, OpenedAt: now}
		return tx.Create(&shift).Error
	})
	if err != nil {
		return nil, err
	}
	return &shift, nil
}

func (s *OperationsService) CloseShift(tenantID, shiftID uint, closingCents int64, notes string) (*model.POSShift, error) {
	return s.closeShift(tenantID, shiftID, 0, "admin", closingCents, notes)
}

// CloseShiftForOperator applies the operator boundary before calculating and
// closing the shift. Supervisors may close any tenant shift, while ordinary
// operators may only close their own shift and must have access to its POS
// device. Keeping this check in the service prevents a controller-only bypass.
func (s *OperationsService) CloseShiftForOperator(tenantID, shiftID, operatorID uint, role string, closingCents int64, notes string) (*model.POSShift, error) {
	if operatorID == 0 {
		return nil, errors.New("operator is required")
	}
	return s.closeShift(tenantID, shiftID, operatorID, role, closingCents, notes)
}

// ReconcileShift is a separate supervisory action from cashier close. A
// closed shift is immutable in its sales/refund facts; reconciliation only
// records the counted-cash variance and reviewer evidence.
func (s *OperationsService) ReconcileShift(tenantID, shiftID, operatorID uint, role, notes string) (*model.POSShift, error) {
	if tenantID == 0 || operatorID == 0 {
		return nil, errors.New("tenant and reviewer are required")
	}
	if role != "admin" && role != "super_admin" {
		return nil, errors.New("only an administrator can reconcile a shift")
	}
	var shift model.POSShift
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", shiftID, tenantID).First(&shift).Error; err != nil {
			return err
		}
		if shift.Status != "closed" {
			return fmt.Errorf("shift cannot reconcile from status %s", shift.Status)
		}
		now := time.Now()
		shift.Status = "reconciled"
		shift.VarianceCents = shift.ClosingCents - shift.ExpectedCents
		shift.ReconciledAt = &now
		shift.ReconciledBy = operatorID
		shift.Notes = strings.TrimSpace(notes)
		return tx.Model(&shift).Updates(map[string]interface{}{
			"status": shift.Status, "variance_cents": shift.VarianceCents, "reconciled_at": now,
			"reconciled_by": operatorID, "notes": shift.Notes,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &shift, nil
}

func (s *OperationsService) closeShift(tenantID, shiftID, operatorID uint, role string, closingCents int64, notes string) (*model.POSShift, error) {
	if closingCents < 0 {
		return nil, errors.New("closing amount cannot be negative")
	}
	var shift model.POSShift
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", shiftID, tenantID).First(&shift).Error; err != nil {
			return err
		}
		if role != "admin" && role != "super_admin" {
			if shift.OperatorID != operatorID {
				return errors.New("only the shift operator can close this shift")
			}
			var scopeCount int64
			if err := tx.Model(&model.StaffResourceScope{}).Where("tenant_id = ? AND staff_id = ? AND resource_type = ? AND resource_id = ?", tenantID, operatorID, "device", shift.DeviceID).Count(&scopeCount).Error; err != nil {
				return err
			}
			if scopeCount == 0 {
				return ErrResourceScopeDenied
			}
		}
		if shift.Status != "open" {
			return fmt.Errorf("shift cannot close from status %s", shift.Status)
		}
		var paidCents int64
		if err := tx.Model(&model.Payment{}).Where("tenant_id = ? AND shift_id = ? AND method = ? AND status IN ?", tenantID, shift.ID, "cash", []string{"paid", "refunded"}).Select("COALESCE(SUM(CASE WHEN amount_cents != 0 THEN amount_cents ELSE CAST(ROUND(amount * 100.0) AS INTEGER) END), 0)").Scan(&paidCents).Error; err != nil {
			return err
		}
		var refundedCents int64
		if err := tx.Table("refunds").Joins("JOIN payments ON payments.id = refunds.payment_id").Where("refunds.tenant_id = ? AND payments.shift_id = ? AND refunds.method = ? AND refunds.status = ? AND refunds.created_at >= ?", tenantID, shift.ID, "cash", "succeeded", shift.OpenedAt).Select("COALESCE(SUM(CASE WHEN refunds.amount_cents != 0 THEN refunds.amount_cents ELSE CAST(ROUND(refunds.amount * 100.0) AS INTEGER) END), 0)").Scan(&refundedCents).Error; err != nil {
			return err
		}
		now := time.Now()
		shift.ClosingCents = closingCents
		shift.ExpectedCents = shift.OpeningCents + paidCents - refundedCents
		shift.Status = "closed"
		shift.ClosedAt = &now
		shift.Notes = strings.TrimSpace(notes)
		return tx.Model(&shift).Updates(map[string]interface{}{"closing_cents": shift.ClosingCents, "expected_cents": shift.ExpectedCents, "status": shift.Status, "closed_at": now, "notes": shift.Notes}).Error
	})
	if err != nil {
		return nil, err
	}
	return &shift, nil
}

// RecoverStalePrintJobs turns jobs left in "printing" by a crashed process
// into explicit failed jobs. The physical output may have succeeded before
// the crash, so they are never silently reprinted; an operator must review
// and retry them through the normal audited path.
func (s *OperationsService) RecoverStalePrintJobs(now time.Time, age time.Duration, limit int) (int, error) {
	if age <= 0 {
		age = 2 * time.Minute
	}
	if limit <= 0 {
		limit = 100
	}
	cutoff := now.Add(-age)
	count := 0
	err := model.Write(func(tx *gorm.DB) error {
		var jobs []model.PrintJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND updated_at <= ?", "printing", cutoff).Order("id").Limit(limit).Find(&jobs).Error; err != nil {
			return err
		}
		for i := range jobs {
			message := strings.TrimSpace(jobs[i].LastError)
			if message == "" {
				message = "print attempt became stale after process restart; verify physical output before retry"
			} else {
				message = "stale after process restart: " + message
			}
			if err := tx.Model(&jobs[i]).Updates(map[string]interface{}{"status": "failed", "last_error": message}).Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *OperationsService) ListShifts(tenantID uint, page, pageSize int) ([]model.POSShift, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.POSShift{}).Where("tenant_id = ?", tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.POSShift
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetShiftSummary gives a window operator or supervisor one practical
// reconciliation view without introducing a separate accounting workflow.
// The shift and all related payment/refund rows are tenant-scoped.
func (s *OperationsService) GetShiftSummary(tenantID, shiftID uint) (*POSShiftSummary, error) {
	if tenantID == 0 || shiftID == 0 {
		return nil, errors.New("tenant and shift are required")
	}
	var shift model.POSShift
	if err := model.DB.Where("id = ? AND tenant_id = ?", shiftID, tenantID).First(&shift).Error; err != nil {
		return nil, err
	}
	var payments []model.Payment
	if err := model.DB.Where("tenant_id = ? AND shift_id = ? AND status IN ?", tenantID, shiftID, []string{"paid", "partial_refunded", "refunded"}).Find(&payments).Error; err != nil {
		return nil, err
	}
	var refunds []model.Refund
	if err := model.DB.Table("refunds").
		Joins("JOIN payments ON payments.id = refunds.payment_id").
		Where("refunds.tenant_id = ? AND payments.tenant_id = ? AND payments.shift_id = ? AND refunds.status = ?", tenantID, tenantID, shiftID, "succeeded").
		Find(&refunds).Error; err != nil {
		return nil, err
	}

	byMethod := make(map[string]*POSPaymentSummary)
	orderNos := make(map[string]struct{})
	for i := range payments {
		method := strings.TrimSpace(payments[i].Method)
		if method == "" {
			method = "unknown"
		}
		row := byMethod[method]
		if row == nil {
			row = &POSPaymentSummary{Method: method}
			byMethod[method] = row
		}
		amount := payments[i].AmountCents
		if amount == 0 {
			amount = moneyCents(payments[i].Amount)
		}
		row.PaymentCount++
		row.GrossCents += amount
		orderNos[payments[i].OrderNo] = struct{}{}
	}
	var refundCount int64
	for i := range refunds {
		method := strings.TrimSpace(refunds[i].Method)
		if method == "" {
			method = "unknown"
		}
		row := byMethod[method]
		if row == nil {
			row = &POSPaymentSummary{Method: method}
			byMethod[method] = row
		}
		amount := refunds[i].AmountCents
		if amount == 0 {
			amount = moneyCents(refunds[i].Amount)
		}
		row.RefundCents += amount
		refundCount++
	}
	methods := make([]string, 0, len(byMethod))
	for method := range byMethod {
		methods = append(methods, method)
	}
	for i := 1; i < len(methods); i++ {
		for j := i; j > 0 && methods[j] < methods[j-1]; j-- {
			methods[j], methods[j-1] = methods[j-1], methods[j]
		}
	}
	rows := make([]POSPaymentSummary, 0, len(methods))
	var cashGross, cashRefund int64
	for _, method := range methods {
		row := *byMethod[method]
		row.NetCents = row.GrossCents - row.RefundCents
		if method == "cash" {
			cashGross, cashRefund = row.GrossCents, row.RefundCents
		}
		rows = append(rows, row)
	}
	return &POSShiftSummary{
		Shift: shift, Payments: rows, RefundCount: refundCount,
		OrderCount: int64(len(orderNos)), CashExpectedCents: shift.OpeningCents + cashGross - cashRefund,
	}, nil
}

func (s *OperationsService) GetOpenShift(tenantID, deviceID, operatorID uint) (*model.POSShift, error) {
	var shift model.POSShift
	if err := model.DB.Where("tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", tenantID, deviceID, operatorID, "open").Order("opened_at DESC").First(&shift).Error; err != nil {
		return nil, err
	}
	return &shift, nil
}

func (s *OperationsService) QueuePrint(tenantID, deviceID, operatorID, shiftID uint, orderNo, ticketCode string) (*model.PrintJob, error) {
	if tenantID == 0 || deviceID == 0 || operatorID == 0 || shiftID == 0 || strings.TrimSpace(orderNo) == "" {
		return nil, errors.New("device, operator, shift and order are required")
	}
	var job model.PrintJob
	err := model.Write(func(tx *gorm.DB) error {
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND type = ?", deviceID, tenantID, "pos").First(&device).Error; err != nil {
			return errors.New("POS device not found")
		}
		var shift model.POSShift
		if err := tx.Where("id = ? AND tenant_id = ? AND device_id = ? AND operator_id = ? AND status = ?", shiftID, tenantID, deviceID, operatorID, "open").First(&shift).Error; err != nil {
			return errors.New("open POS shift not found")
		}
		var order model.Order
		if err := tx.Where("order_no = ? AND tenant_id = ?", orderNo, tenantID).First(&order).Error; err != nil {
			return errors.New("order not found")
		}
		if order.Status != "paid" && order.Status != "completed" && order.Status != "partial_refunded" {
			return fmt.Errorf("order cannot be printed from status %s", order.Status)
		}
		if strings.TrimSpace(ticketCode) != "" {
			var count int64
			if err := tx.Model(&model.Ticket{}).Where("order_id = ? AND ticket_code = ?", order.ID, strings.TrimSpace(ticketCode)).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return errors.New("ticket does not belong to order")
			}
		}
		job = model.PrintJob{TenantID: tenantID, DeviceID: deviceID, OperatorID: operatorID, ShiftID: shiftID, OrderNo: orderNo, TicketCode: strings.TrimSpace(ticketCode), Status: "queued"}
		return tx.Create(&job).Error
	})
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *OperationsService) ListPrintJobs(tenantID, deviceID uint, status string) ([]model.PrintJob, error) {
	query := model.DB.Where("tenant_id = ?", tenantID)
	if deviceID != 0 {
		query = query.Where("device_id = ?", deviceID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var jobs []model.PrintJob
	err := query.Order("created_at ASC").Limit(100).Find(&jobs).Error
	return jobs, err
}

func (s *OperationsService) ListAlerts(tenantID uint, status string) ([]model.DeviceAlert, error) {
	query := model.DB.Where("tenant_id = ?", tenantID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var alerts []model.DeviceAlert
	err := query.Order("opened_at DESC").Limit(200).Find(&alerts).Error
	return alerts, err
}

func (s *OperationsService) StartPrint(tenantID, jobID, deviceID, operatorID uint) (*model.PrintJob, error) {
	return s.updatePrintJob(tenantID, jobID, func(tx *gorm.DB, job *model.PrintJob) error {
		if job.DeviceID != deviceID || job.OperatorID != operatorID || (job.Status != "queued" && job.Status != "failed") {
			return errors.New("print job cannot be started by this terminal")
		}
		job.Status = "printing"
		job.AttemptCount++
		job.LastError = ""
		return tx.Model(job).Updates(map[string]interface{}{"status": job.Status, "attempt_count": job.AttemptCount, "last_error": ""}).Error
	})
}

func (s *OperationsService) CompletePrint(tenantID, jobID, deviceID, operatorID uint) (*model.PrintJob, error) {
	return s.updatePrintJob(tenantID, jobID, func(tx *gorm.DB, job *model.PrintJob) error {
		if job.DeviceID != deviceID || job.OperatorID != operatorID || job.Status != "printing" {
			return errors.New("print job is not being printed by this terminal")
		}
		now := time.Now()
		job.Status = "printed"
		job.PrintedAt = &now
		if err := tx.Model(job).Updates(map[string]interface{}{"status": job.Status, "printed_at": now, "last_error": ""}).Error; err != nil {
			return err
		}
		if job.AfterSaleRequestNo != "" {
			var pending int64
			if err := tx.Model(&model.PrintJob{}).Where("after_sale_request_no = ? AND status != ?", job.AfterSaleRequestNo, "printed").Count(&pending).Error; err != nil {
				return err
			}
			if pending == 0 {
				var req model.AfterSaleRequest
				if err := tx.Where("request_no = ? AND tenant_id = ? AND status = ?", job.AfterSaleRequestNo, tenantID, "processing").First(&req).Error; err == nil {
					if err := completeAfterSaleTx(tx, &req, operatorID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (s *OperationsService) FailPrint(tenantID, jobID, deviceID, operatorID uint, reason string) (*model.PrintJob, error) {
	return s.updatePrintJob(tenantID, jobID, func(tx *gorm.DB, job *model.PrintJob) error {
		if job.DeviceID != deviceID || job.OperatorID != operatorID || job.Status != "printing" {
			return errors.New("print job is not being printed by this terminal")
		}
		job.Status = "failed"
		job.LastError = strings.TrimSpace(reason)
		if job.LastError == "" {
			job.LastError = "printer returned an unknown error"
		}
		if err := tx.Model(job).Updates(map[string]interface{}{"status": job.Status, "last_error": job.LastError}).Error; err != nil {
			return err
		}
		// A reissue is a durable after-sale workflow. Leaving it in
		// processing after a terminal printer failure makes the request look
		// successful in the workbench and prevents a controlled retry/review.
		if job.AfterSaleRequestNo != "" {
			var req model.AfterSaleRequest
			if err := tx.Where("request_no = ? AND tenant_id = ?", job.AfterSaleRequestNo, tenantID).First(&req).Error; err == nil && req.Status == "processing" {
				message := job.LastError
				if err := tx.Model(&req).Updates(map[string]interface{}{"status": "failed", "error_message": message}).Error; err != nil {
					return err
				}
				if err := appendAfterSaleEvent(tx, &req, "processing", "failed", "print_failed", operatorID, message); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *OperationsService) updatePrintJob(tenantID, jobID uint, mutate func(*gorm.DB, *model.PrintJob) error) (*model.PrintJob, error) {
	var job model.PrintJob
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", jobID, tenantID).First(&job).Error; err != nil {
			return err
		}
		return mutate(tx, &job)
	})
	if err != nil {
		return nil, err
	}
	return &job, nil
}
