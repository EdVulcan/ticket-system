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

type OperationsService struct{}

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
	if closingCents < 0 {
		return nil, errors.New("closing amount cannot be negative")
	}
	var shift model.POSShift
	err := model.Write(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", shiftID, tenantID).First(&shift).Error; err != nil {
			return err
		}
		if shift.Status != "open" {
			return fmt.Errorf("shift cannot close from status %s", shift.Status)
		}
		var paidAmount float64
		if err := tx.Model(&model.Payment{}).Where("tenant_id = ? AND shift_id = ? AND method = ? AND status IN ?", tenantID, shift.ID, "cash", []string{"paid", "refunded"}).Select("COALESCE(SUM(amount), 0)").Scan(&paidAmount).Error; err != nil {
			return err
		}
		var refundedAmount float64
		if err := tx.Table("refunds").Joins("JOIN payments ON payments.id = refunds.payment_id").Where("refunds.tenant_id = ? AND payments.shift_id = ? AND refunds.method = ? AND refunds.status = ? AND refunds.created_at >= ?", tenantID, shift.ID, "cash", "succeeded", shift.OpenedAt).Select("COALESCE(SUM(refunds.amount), 0)").Scan(&refundedAmount).Error; err != nil {
			return err
		}
		now := time.Now()
		shift.ClosingCents = closingCents
		shift.ExpectedCents = shift.OpeningCents + moneyCents(paidAmount-refundedAmount)
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
		return tx.Model(job).Updates(map[string]interface{}{"status": job.Status, "last_error": job.LastError}).Error
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
