package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

type TeamService struct{}

func (s *TeamService) CreateContract(tenantID uint, contract *model.TravelContract) error {
	if tenantID == 0 || contract.SupplierTenantID == 0 || strings.TrimSpace(contract.ContractNo) == "" {
		return errors.New("supplier and contract number are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		if err := requireActiveTenantCapability(tx, contract.SupplierTenantID, "supplier"); err != nil {
			return err
		}
		if contract.TravelTenantID != 0 && contract.TravelTenantID != tenantID {
			return errors.New("contract tenant cannot be changed")
		}
		contract.Base = model.Base{}
		contract.TravelTenantID = tenantID
		if contract.Status == "" {
			contract.Status = "active"
		}
		var relationship model.DistributorRelationship
		if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status IN ?", tenantID, contract.SupplierTenantID, []string{"active", "suspended"}).First(&relationship).Error; err != nil {
			return errors.New("active supplier relationship not found")
		}
		return tx.Create(contract).Error
	})
}

func (s *TeamService) ListContracts(tenantID uint) ([]model.TravelContract, error) {
	var rows []model.TravelContract
	return rows, model.DB.Where("travel_tenant_id = ? OR supplier_tenant_id = ?", tenantID, tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateAgent(tenantID uint, agent *model.TravelAgent) error {
	if strings.TrimSpace(agent.Name) == "" || strings.TrimSpace(agent.JobNumber) == "" {
		return errors.New("agent name and job number are required")
	}
	agent.Base = model.Base{}
	agent.TenantID = tenantID
	if agent.Status == "" {
		agent.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(agent).Error
	})
}

func (s *TeamService) ListAgents(tenantID uint) ([]model.TravelAgent, error) {
	var rows []model.TravelAgent
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateGuide(tenantID uint, guide *model.TourGuide) error {
	if strings.TrimSpace(guide.Name) == "" {
		return errors.New("guide name is required")
	}
	guide.Base = model.Base{}
	guide.TenantID = tenantID
	if guide.Status == "" {
		guide.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(guide).Error
	})
}

func (s *TeamService) ListGuides(tenantID uint) ([]model.TourGuide, error) {
	var rows []model.TourGuide
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func (s *TeamService) CreateVehicle(tenantID uint, vehicle *model.TravelVehicle) error {
	if strings.TrimSpace(vehicle.PlateNumber) == "" {
		return errors.New("plate number is required")
	}
	vehicle.Base = model.Base{}
	vehicle.TenantID = tenantID
	if vehicle.Status == "" {
		vehicle.Status = "active"
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		return tx.Create(vehicle).Error
	})
}

func (s *TeamService) ListVehicles(tenantID uint) ([]model.TravelVehicle, error) {
	var rows []model.TravelVehicle
	return rows, model.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&rows).Error
}

func teamNo(prefix string, id uint) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), id)
}

func (s *TeamService) CreateGroup(tenantID uint, group *model.TourGroup) error {
	if tenantID == 0 || strings.TrimSpace(group.Name) == "" || group.SupplierTenantID == 0 || group.ScenicAreaID == 0 || group.VisitDate.IsZero() {
		return errors.New("group name, supplier, scenic area and visit date are required")
	}
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var area model.ScenicArea
		if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", group.ScenicAreaID, group.SupplierTenantID, "active").First(&area).Error; err != nil {
			return errors.New("supplier scenic area not found")
		}
		if group.SupplierTenantID != tenantID {
			var relationship model.DistributorRelationship
			if err := tx.Where("agent_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", tenantID, group.SupplierTenantID, "active").First(&relationship).Error; err != nil {
				return errors.New("active supplier relationship not found")
			}
		}
		if err := validateTeamContractTx(tx, tenantID, group); err != nil {
			return err
		}
		group.Base = model.Base{}
		group.TenantID = tenantID
		group.SalesOrderID = 0
		group.Status = "draft"
		group.GroupNo = teamNo("TEAM", tenantID)
		return tx.Create(group).Error
	})
}

func (s *TeamService) ListGroups(tenantID uint, page, pageSize int) ([]model.TourGroup, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := model.DB.Model(&model.TourGroup{}).Where("tenant_id = ?", tenantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []model.TourGroup
	if err := query.Order("visit_date ASC, created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&groups).Error; err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

func (s *TeamService) AddMembers(tenantID, groupID uint, members []model.TourGroupMember) (int, error) {
	if len(members) == 0 {
		return 0, errors.New("members are required")
	}
	count := 0
	err := model.Write(func(tx *gorm.DB) error {
		var group model.TourGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.Status == "entered" || group.Status == "cancelled" {
			return errors.New("cannot add members to a completed or cancelled group")
		}
		for i := range members {
			if strings.TrimSpace(members[i].Name) == "" {
				return errors.New("member name is required")
			}
			members[i].Base = model.Base{}
			members[i].GroupID = groupID
			if members[i].Status == "" {
				members[i].Status = "planned"
				if strings.TrimSpace(members[i].TicketCode) != "" {
					members[i].Status = "ticketed"
				}
			}
			if err := tx.Create(&members[i]).Error; err != nil {
				return err
			}
			count++
		}
		return tx.Model(&group).UpdateColumn("expected_count", gorm.Expr("expected_count + ?", len(members))).Error
	})
	return count, err
}

func (s *TeamService) ListMembers(tenantID, groupID uint) ([]model.TourGroupMember, error) {
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		return nil, errors.New("group not found")
	}
	var members []model.TourGroupMember
	return members, model.DB.Where("group_id = ?", groupID).Order("id ASC").Find(&members).Error
}

func (s *TeamService) EnterBatch(tenantID, groupID, deviceID, operatorID uint, memberIDs []uint) (*model.TourEntryBatch, error) {
	if len(memberIDs) == 0 || deviceID == 0 || operatorID == 0 {
		return nil, errors.New("member ids, supplier device and operator are required")
	}
	var batch model.TourEntryBatch
	err := model.Write(func(tx *gorm.DB) error {
		var group model.TourGroup
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		if err := tx.Where("id = ? AND supplier_tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		if group.SalesOrderID == 0 || (group.Status != "confirmed" && group.Status != "partial_entry") {
			return errors.New("group has no confirmed sales fulfillment")
		}
		var order model.Order
		if err := tx.Where("id = ? AND tenant_id = ? AND status IN ?", group.SalesOrderID, group.TenantID, []string{"paid", "completed", "partial_refunded"}).First(&order).Error; err != nil {
			return errors.New("group sales order is not paid")
		}
		var device model.Device
		if err := tx.Where("id = ? AND tenant_id = ? AND scenic_area_id = ? AND status = ? AND check_point_id IS NOT NULL", deviceID, group.SupplierTenantID, group.ScenicAreaID, "online").First(&device).Error; err != nil {
			return errors.New("entry device does not belong to group scenic area")
		}
		var operatorCount int64
		if err := tx.Model(&model.User{}).Where("id = ? AND tenant_id = ?", operatorID, group.SupplierTenantID).Count(&operatorCount).Error; err != nil {
			return err
		}
		if operatorCount == 0 {
			if err := tx.Model(&model.Staff{}).Where("id = ? AND tenant_id = ? AND status = ?", operatorID, group.SupplierTenantID, "active").Count(&operatorCount).Error; err != nil {
				return err
			}
		}
		if operatorCount == 0 {
			return errors.New("entry operator does not belong to supplier tenant")
		}
		checkpointID := *device.CheckPointID
		batch = model.TourEntryBatch{GroupID: groupID, BatchNo: teamNo("BATCH", groupID), DeviceID: deviceID, EnteredCount: 0, OperatorID: operatorID, EnteredAt: time.Now()}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, memberID := range memberIDs {
			var member model.TourGroupMember
			if err := tx.Where("id = ? AND group_id = ? AND status = ?", memberID, groupID, "ticketed").First(&member).Error; err != nil {
				return fmt.Errorf("member %d is not ticketed", memberID)
			}
			var ticket model.Ticket
			if err := tx.Preload("OrderItem").Where("ticket_code = ? AND order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status IN ?", member.TicketCode, order.ID, group.SupplierTenantID, group.ScenicAreaID, []string{"unused", "active"}).First(&ticket).Error; err != nil {
				return fmt.Errorf("member %d has no valid ticket entitlement", memberID)
			}
			if ticket.OrderItem.UseDate == nil || !sameTeamDate(*ticket.OrderItem.UseDate, group.VisitDate) {
				return fmt.Errorf("member %d ticket visit date does not match team", memberID)
			}
			if ticket.CodeMode == "order" {
				return errors.New("team admission requires one ticket entitlement per member")
			}
			var rule model.TicketRule
			if ticket.RuleSnapshot == "" || json.Unmarshal([]byte(ticket.RuleSnapshot), &rule) != nil {
				return errors.New("ticket entitlement has no valid rule snapshot")
			}
			if groupMatch, itemMatch := matchRule(rule, checkpointID); groupMatch == nil || itemMatch == nil {
				return errors.New("entry checkpoint is not allowed by ticket entitlement")
			}
			if err := tx.Create(&model.CheckInRecord{TenantID: group.SupplierTenantID, ScenicAreaID: group.ScenicAreaID, TicketCode: ticket.TicketCode, TicketID: ticket.ID, CheckPointID: checkpointID, DeviceID: device.ID, CheckInTime: now, Result: "success", Message: "team admission"}).Error; err != nil {
				return err
			}
			if err := tx.Model(&ticket).Updates(map[string]interface{}{"status": "used", "check_in_count": gorm.Expr("check_in_count + 1")}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.TicketEntitlement{}).Where("ticket_id = ? AND fulfillment_order_id = ?", ticket.ID, ticket.FulfillmentOrderID).Update("status", "used").Error; err != nil {
				return err
			}
			if err := tx.Model(&member).Updates(map[string]interface{}{"status": "entered", "entered_at": now, "entry_batch_no": batch.BatchNo}).Error; err != nil {
				return err
			}
			batch.EnteredCount++
		}
		if err := tx.Model(&batch).Update("entered_count", batch.EnteredCount).Error; err != nil {
			return err
		}
		var totalEntered int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND status = ?", group.ID, "entered").Count(&totalEntered).Error; err != nil {
			return err
		}
		status := "partial_entry"
		if int(totalEntered) >= group.ExpectedCount && group.ExpectedCount > 0 {
			status = "entered"
		}
		return tx.Model(&group).Update("status", status).Error
	})
	if err != nil {
		return nil, err
	}
	return &batch, nil
}

func (s *TeamService) AttachOrder(tenantID, groupID, orderID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		var group model.TourGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("group not found")
		}
		var order model.Order
		if err := tx.Preload("Items").Where("id = ? AND tenant_id = ? AND status IN ?", orderID, tenantID, []string{"paid", "completed", "partial_refunded"}).First(&order).Error; err != nil {
			return errors.New("sales order not found")
		}
		if err := validateTeamContractTx(tx, tenantID, &group); err != nil {
			return err
		}
		for _, item := range order.Items {
			if item.UseDate == nil || !sameTeamDate(*item.UseDate, group.VisitDate) {
				return errors.New("sales order visit date does not match team visit date")
			}
		}
		var count int64
		if err := tx.Model(&model.FulfillmentOrder{}).Where("sales_order_id = ? AND supplier_tenant_id = ? AND scenic_area_id = ?", order.ID, group.SupplierTenantID, group.ScenicAreaID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("order has no matching supplier fulfillment")
		}
		var members []model.TourGroupMember
		if err := tx.Where("group_id = ? AND status = ?", group.ID, "planned").Order("id").Find(&members).Error; err != nil {
			return err
		}
		var tickets []model.Ticket
		if err := tx.Where("order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status = ? AND code_mode != ?", order.ID, group.SupplierTenantID, group.ScenicAreaID, "unused", "order").Order("id").Find(&tickets).Error; err != nil {
			return err
		}
		if len(tickets) < len(members) {
			return errors.New("order does not have enough member ticket entitlements")
		}
		for i := range members {
			if err := tx.Model(&members[i]).Updates(map[string]interface{}{"ticket_code": tickets[i].TicketCode, "status": "ticketed"}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&group).Updates(map[string]interface{}{"sales_order_id": order.ID, "status": "confirmed"}).Error
	})
}

func sameTeamDate(left, right time.Time) bool {
	return startOfDay(left).Equal(startOfDay(right))
}

func validateTeamContractTx(tx *gorm.DB, tenantID uint, group *model.TourGroup) error {
	if group == nil || group.SupplierTenantID == 0 || group.VisitDate.IsZero() {
		return errors.New("team supplier and visit date are required")
	}
	if tenantID == group.SupplierTenantID {
		return requireActiveTenantCapability(tx, tenantID, "supplier")
	}
	if group.ContractID == 0 {
		return errors.New("active travel contract is required")
	}
	var contract model.TravelContract
	if err := tx.Where("id = ? AND travel_tenant_id = ? AND supplier_tenant_id = ? AND status = ?", group.ContractID, tenantID, group.SupplierTenantID, "active").First(&contract).Error; err != nil {
		return errors.New("active travel contract not found")
	}
	visit := startOfDay(group.VisitDate)
	if contract.StartsAt != nil && visit.Before(startOfDay(*contract.StartsAt)) {
		return errors.New("travel contract is not active on team visit date")
	}
	if contract.EndsAt != nil && visit.After(startOfDay(*contract.EndsAt)) {
		return errors.New("travel contract is not active on team visit date")
	}
	return nil
}
