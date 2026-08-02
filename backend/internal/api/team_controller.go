package api

import (
	"fmt"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type TeamController struct{ Service service.TeamService }

func (c *TeamController) ListContracts(ctx *gin.Context) {
	rows, err := c.Service.ListContracts(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *TeamController) CreateContract(ctx *gin.Context) {
	var input service.TravelContractInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.CreateContract(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) UpdateContract(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid contract id"})
		return
	}
	var input service.TravelContractInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.UpdateContract(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}
func (c *TeamController) ListContractPartners(ctx *gin.Context) {
	rows, err := c.Service.ListContractPartners(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *TeamController) ListAgents(ctx *gin.Context) {
	rows, err := c.Service.ListAgents(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *TeamController) CreateAgent(ctx *gin.Context) {
	var row model.TravelAgent
	if err := ctx.ShouldBindJSON(&row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateAgent(ctx.GetUint("tenant_id"), &row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) ListGuides(ctx *gin.Context) {
	rows, err := c.Service.ListGuides(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *TeamController) CreateGuide(ctx *gin.Context) {
	var row model.TourGuide
	if err := ctx.ShouldBindJSON(&row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateGuide(ctx.GetUint("tenant_id"), &row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) ListVehicles(ctx *gin.Context) {
	rows, err := c.Service.ListVehicles(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}
func (c *TeamController) CreateVehicle(ctx *gin.Context) {
	var row model.TravelVehicle
	if err := ctx.ShouldBindJSON(&row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateVehicle(ctx.GetUint("tenant_id"), &row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *TeamController) ListGroups(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	groups, total, err := c.Service.ListGroups(ctx.GetUint("tenant_id"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": groups, "total": total, "page": page, "page_size": pageSize})
}

func (c *TeamController) CreateGroup(ctx *gin.Context) {
	var group model.TourGroup
	if err := ctx.ShouldBindJSON(&group); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateGroup(ctx.GetUint("tenant_id"), &group); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, group)
}

func (c *TeamController) AddMembers(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		Members []model.TourGroupMember `json:"members" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	count, err := c.Service.AddMembers(ctx.GetUint("tenant_id"), uint(groupID), body.Members)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"added": count})
}

func (c *TeamController) ReplaceMembers(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		Members []model.TourGroupMember `json:"members"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	count, err := c.Service.ReplaceMembers(ctx.GetUint("tenant_id"), uint(groupID), body.Members)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"replaced": count})
}

func (c *TeamController) ListMembers(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	members, err := c.Service.ListMembers(ctx.GetUint("tenant_id"), uint(groupID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": members})
}

func (c *TeamController) EnterBatch(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		DeviceID       uint   `json:"device_id" binding:"required"`
		MemberIDs      []uint `json:"member_ids" binding:"required"`
		IdempotencyKey string `json:"idempotency_key" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := service.RequireStaffResource(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), "device", body.DeviceID); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	batch, err := c.Service.EnterBatch(ctx.GetUint("tenant_id"), uint(groupID), body.DeviceID, ctx.GetUint("user_id"), body.MemberIDs, body.IdempotencyKey)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, batch)
}

func (c *TeamController) ListEntryBatches(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	batches, err := c.Service.ListEntryBatches(ctx.GetUint("tenant_id"), uint(groupID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": batches})
}

func (c *TeamController) ListConfirmations(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	rows, err := c.Service.ListTeamConfirmations(ctx.GetUint("tenant_id"), uint(groupID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *TeamController) SubmitConfirmation(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		ConfirmedCount int    `json:"confirmed_count" binding:"required"`
		GuideID        uint   `json:"guide_id"`
		VehicleID      uint   `json:"vehicle_id"`
		Notes          string `json:"notes"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SubmitTeamConfirmation(ctx.GetUint("tenant_id"), uint(groupID), ctx.GetUint("user_id"), body.ConfirmedCount, body.GuideID, body.VehicleID, body.Notes)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *TeamController) AcknowledgeConfirmation(ctx *gin.Context) {
	groupID, groupErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	confirmationID, confirmationErr := strconv.ParseUint(ctx.Param("confirmationId"), 10, 32)
	if groupErr != nil || confirmationErr != nil || groupID == 0 || confirmationID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group or confirmation id"})
		return
	}
	if err := c.Service.AcknowledgeTeamConfirmation(ctx.GetUint("tenant_id"), uint(groupID), uint(confirmationID), ctx.GetUint("user_id")); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}

func (c *TeamController) ListMemberChanges(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	rows, err := c.Service.ListTeamMemberChanges(ctx.GetUint("tenant_id"), uint(groupID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *TeamController) ChangeMember(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		Action   string                `json:"action" binding:"required"`
		MemberID uint                  `json:"member_id"`
		Member   model.TourGroupMember `json:"member"`
		Reason   string                `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.ChangeTeamMember(ctx.GetUint("tenant_id"), uint(groupID), ctx.GetUint("user_id"), body.Action, body.MemberID, body.Member, body.Reason)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *TeamController) AttachOrder(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var body struct {
		OrderID uint `json:"order_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AttachOrder(ctx.GetUint("tenant_id"), uint(groupID), body.OrderID); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "confirmed"})
}

func (c *TeamController) ListSettlements(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListTeamSettlements(ctx.GetUint("tenant_id"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *TeamController) ExportSettlement(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid settlement id"})
		return
	}
	data, filename, err := c.Service.ExportTeamSettlementCSV(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "team settlement not found"})
		return
	}
	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	ctx.Data(http.StatusOK, "text/csv; charset=utf-8", data)
}

func (c *TeamController) ListAccountSummaries(ctx *gin.Context) {
	rows, err := c.Service.ListTeamAccountSummaries(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *TeamController) GenerateSettlement(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	statement, err := c.Service.GenerateTeamSettlement(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"))
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, statement)
}

func (c *TeamController) SetSettlementStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid settlement id"})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
		Detail string `json:"detail"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.SetTeamSettlementStatus(ctx.GetUint("tenant_id"), uint(id), body.Status, body.Detail, ctx.GetUint("user_id")); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}

func (c *TeamController) AdjustSettlement(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid settlement id"})
		return
	}
	var body struct {
		AmountCents int64  `json:"amount_cents" binding:"required"`
		Reason      string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AdjustTeamSettlement(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.AmountCents, body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"status": "draft"})
}
