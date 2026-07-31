package api

import (
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
	var row model.TravelContract
	if err := ctx.ShouldBindJSON(&row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.CreateContract(ctx.GetUint("tenant_id"), &row); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
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
		DeviceID  uint   `json:"device_id"`
		MemberIDs []uint `json:"member_ids" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	batch, err := c.Service.EnterBatch(ctx.GetUint("tenant_id"), uint(groupID), body.DeviceID, ctx.GetUint("user_id"), body.MemberIDs)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, batch)
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

func (c *TeamController) GenerateSettlement(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	statement, err := c.Service.GenerateTeamSettlement(ctx.GetUint("tenant_id"), uint(id))
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
	if err := c.Service.SetTeamSettlementStatus(ctx.GetUint("tenant_id"), uint(id), body.Status, body.Detail); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}
