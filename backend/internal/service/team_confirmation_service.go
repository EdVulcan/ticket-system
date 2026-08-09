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

func (s *TeamService) SubmitTeamConfirmation(tenantID, groupID, actorUserID uint, confirmedCount int, guideID, vehicleID uint, notes string) (*model.TourGroupConfirmation, error) {
	if tenantID == 0 || groupID == 0 || confirmedCount < 1 {
		return nil, errors.New("group and confirmed count are required")
	}
	notes = strings.TrimSpace(notes)
	var confirmation model.TourGroupConfirmation
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("team group not found")
		}
		if group.SalesOrderID == 0 || (group.Status != "confirmed" && group.Status != "partial_entry") {
			return errors.New("only a confirmed team can submit an operational confirmation")
		}
		var activeMemberCount int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND status != ?", group.ID, "cancelled").Count(&activeMemberCount).Error; err != nil {
			return err
		}
		if int64(confirmedCount) != activeMemberCount && notes == "" {
			return errors.New("confirmation count differs from the active roster; notes are required")
		}
		var guide model.TourGuide
		if guideID != 0 {
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", guideID, tenantID, "active").First(&guide).Error; err != nil {
				return errors.New("active team guide not found")
			}
		}
		var vehicle model.TravelVehicle
		if vehicleID != 0 {
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ?", vehicleID, tenantID, "active").First(&vehicle).Error; err != nil {
				return errors.New("active team vehicle not found")
			}
		}
		var count int64
		if err := tx.Model(&model.TourGroupConfirmation{}).Where("group_id = ?", group.ID).Count(&count).Error; err != nil {
			return err
		}
		confirmation = model.TourGroupConfirmation{
			GroupID: group.ID, Sequence: int(count) + 1, TravelTenantID: group.TenantID,
			SupplierTenantID: group.SupplierTenantID, ScenicAreaID: group.ScenicAreaID,
			ConfirmedCount: confirmedCount, GuideID: guide.ID, GuideName: guide.Name, GuidePhone: guide.Phone,
			VehicleID: vehicle.ID, PlateNumber: vehicle.PlateNumber, Notes: notes,
			SubmittedBy: actorUserID, SubmittedAt: time.Now(),
		}
		if err := tx.Create(&confirmation).Error; err != nil {
			return err
		}
		if err := tx.Model(&group).Updates(map[string]interface{}{"guide_id": guide.ID, "vehicle_id": vehicle.ID}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.confirmation.submit", "tour_group_confirmation", confirmation.ID,
			confirmation.Notes, "", fmt.Sprintf(`{"group_id":%d,"sequence":%d,"confirmed_count":%d}`, group.ID, confirmation.Sequence, confirmedCount))
	})
	if err != nil {
		return nil, err
	}
	return &confirmation, nil
}

func (s *TeamService) AcknowledgeTeamConfirmation(tenantID, groupID, confirmationID, actorUserID uint) error {
	return model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "supplier"); err != nil {
			return err
		}
		var confirmation model.TourGroupConfirmation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND group_id = ? AND supplier_tenant_id = ?", confirmationID, groupID, tenantID).First(&confirmation).Error; err != nil {
			return errors.New("team confirmation not found")
		}
		if confirmation.SupplierAcknowledgedAt != nil {
			return nil
		}
		now := time.Now()
		if err := tx.Model(&confirmation).Updates(map[string]interface{}{"supplier_acknowledged_by": actorUserID, "supplier_acknowledged_at": now}).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.confirmation.acknowledge", "tour_group_confirmation", confirmation.ID,
			"supplier acknowledged team operational confirmation", `{"acknowledged":false}`, `{"acknowledged":true}`)
	})
}

func (s *TeamService) ListTeamConfirmations(tenantID, groupID uint) ([]model.TourGroupConfirmation, error) {
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND (tenant_id = ? OR supplier_tenant_id = ?)", groupID, tenantID, tenantID).First(&group).Error; err != nil {
		return nil, errors.New("team group not found")
	}
	var rows []model.TourGroupConfirmation
	return rows, model.DB.Where("group_id = ?", groupID).Order("sequence DESC").Find(&rows).Error
}

func (s *TeamService) ChangeTeamMember(tenantID, groupID, actorUserID uint, action string, memberID uint, member model.TourGroupMember, reason string) (*model.TourGroupMemberChange, error) {
	action = strings.TrimSpace(action)
	reason = strings.TrimSpace(reason)
	if action != "add" && action != "remove" || reason == "" {
		return nil, errors.New("add or remove action and reason are required")
	}
	var change model.TourGroupMemberChange
	err := model.Write(func(tx *gorm.DB) error {
		if err := requireActiveTenantCapability(tx, tenantID, "travel_agency"); err != nil {
			return err
		}
		var group model.TourGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
			return errors.New("team group not found")
		}
		if group.SalesOrderID == 0 || (group.Status != "confirmed" && group.Status != "partial_entry") {
			return errors.New("temporary member changes require a confirmed team before final entry")
		}
		beforeCount := group.ExpectedCount
		afterCount := beforeCount
		var changedMember model.TourGroupMember
		switch action {
		case "add":
			member.Name = strings.TrimSpace(member.Name)
			member.IdentityNo = strings.TrimSpace(member.IdentityNo)
			member.Phone = strings.TrimSpace(member.Phone)
			if member.Name == "" {
				return errors.New("member name is required")
			}
			if member.IdentityNo != "" {
				var duplicate int64
				if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND identity_no = ? AND status != ?", group.ID, member.IdentityNo, "cancelled").Count(&duplicate).Error; err != nil {
					return err
				}
				if duplicate > 0 {
					return errors.New("member identity already exists in team")
				}
			}
			member.Base = model.Base{}
			member.GroupID = group.ID
			member.Status = "planned"
			var ticket model.Ticket
			ticketErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(`order_id = ? AND fulfillment_tenant_id = ? AND fulfillment_scenic_area_id = ? AND status = ? AND code_mode != ?
				AND NOT EXISTS (SELECT 1 FROM tour_group_members WHERE tour_group_members.ticket_code = tickets.ticket_code AND tour_group_members.status != ?)`,
				group.SalesOrderID, group.SupplierTenantID, group.ScenicAreaID, "unused", "order", "cancelled").Order("id").First(&ticket).Error
			if errors.Is(ticketErr, gorm.ErrRecordNotFound) {
				return errors.New("当前团队订单没有可分配的备用票权，暂不能加员")
			}
			if ticketErr != nil {
				return ticketErr
			}
			member.TicketCode = ticket.TicketCode
			member.Status = "ticketed"
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
			changedMember = member
			afterCount++
		case "remove":
			if memberID == 0 {
				return errors.New("member id is required for removal")
			}
			if err := tx.Where("id = ? AND group_id = ?", memberID, group.ID).First(&changedMember).Error; err != nil {
				return errors.New("team member not found")
			}
			if changedMember.Status == "cancelled" {
				return errors.New("该成员已随退票或作废完成减员，无需重复操作")
			}
			if changedMember.Status == "entered" {
				return errors.New("entered or cancelled member cannot be removed")
			}
			if changedMember.TicketCode != "" {
				var ticket model.Ticket
				if err := tx.Where("ticket_code = ? AND order_id = ?", changedMember.TicketCode, group.SalesOrderID).First(&ticket).Error; err != nil || (ticket.Status != "refunded" && ticket.Status != "void") {
					return errors.New("refund or void the member ticket before removal")
				}
			}
			if err := tx.Model(&changedMember).Update("status", "cancelled").Error; err != nil {
				return err
			}
			if afterCount > 0 {
				afterCount--
			}
		}
		if err := tx.Model(&group).Update("expected_count", afterCount).Error; err != nil {
			return err
		}
		var entered int64
		if err := tx.Model(&model.TourGroupMember{}).Where("group_id = ? AND status = ?", group.ID, "entered").Count(&entered).Error; err != nil {
			return err
		}
		if entered > 0 && int(entered) >= afterCount {
			if err := tx.Model(&group).Update("status", "entered").Error; err != nil {
				return err
			}
		}
		var count int64
		if err := tx.Model(&model.TourGroupMemberChange{}).Where("group_id = ?", group.ID).Count(&count).Error; err != nil {
			return err
		}
		change = model.TourGroupMemberChange{
			GroupID: group.ID, Sequence: int(count) + 1, TravelTenantID: group.TenantID, SupplierTenantID: group.SupplierTenantID,
			ChangeType: action, MemberID: changedMember.ID, MemberName: changedMember.Name,
			BeforeExpectedCount: beforeCount, AfterExpectedCount: afterCount, Reason: reason, ActorUserID: actorUserID,
		}
		if err := tx.Create(&change).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorUserID, tenantID, auditRoleTx(tx, actorUserID), "tenant", "team.member.change", "tour_group_member_change", change.ID, reason,
			fmt.Sprintf(`{"expected_count":%d}`, beforeCount), fmt.Sprintf(`{"expected_count":%d,"action":%q,"member_id":%d}`, afterCount, action, changedMember.ID))
	})
	if err != nil {
		return nil, err
	}
	return &change, nil
}

func (s *TeamService) ListTeamMemberChanges(tenantID, groupID uint) ([]model.TourGroupMemberChange, error) {
	var group model.TourGroup
	if err := model.DB.Where("id = ? AND (tenant_id = ? OR supplier_tenant_id = ?)", groupID, tenantID, tenantID).First(&group).Error; err != nil {
		return nil, errors.New("team group not found")
	}
	var rows []model.TourGroupMemberChange
	return rows, model.DB.Where("group_id = ?", groupID).Order("sequence DESC").Find(&rows).Error
}
