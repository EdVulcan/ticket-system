package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type TeamController struct{ Service service.TeamService }

func (c *TeamController) SearchSupplierPartner(ctx *gin.Context) {
	code := ctx.Query("code")
	if code == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "supplier system code is required"})
		return
	}
	row, err := c.Service.SearchSupplierPartner(ctx.GetUint("tenant_id"), code)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": row})
}

func (c *TeamController) ApplySupplierPartner(ctx *gin.Context) {
	var input struct {
		SystemCode string `json:"system_code" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.ApplySupplierPartnerAudited(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), ctx.GetString("role"), input.SystemCode); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"status": "pending"})
}

func (c *TeamController) ListSupplierPartners(ctx *gin.Context) {
	rows, err := c.Service.ListSupplierPartners(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *TeamController) ListTravelAgencyPartners(ctx *gin.Context) {
	rows, err := c.Service.ListTravelAgencyPartners(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *TeamController) AuditTravelAgencyPartner(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid partnership id"})
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AuditTravelAgencyPartnerAudited(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), ctx.GetString("role"), input.Status); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": input.Status})
}

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
func (c *TeamController) ListContractProducts(ctx *gin.Context) {
	rows, err := c.Service.ListContractProducts(ctx.GetUint("tenant_id"))
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
	var input service.TeamAgentInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.CreateAgent(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) UpdateAgent(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent id"})
		return
	}
	var input service.TeamAgentInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.UpdateAgent(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}
func (c *TeamController) SetAgentStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent id"})
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SetAgentStatus(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input.Status, input.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
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
	var input service.TeamGuideInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.CreateGuide(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) UpdateGuide(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid guide id"})
		return
	}
	var input service.TeamGuideInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.UpdateGuide(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}
func (c *TeamController) SetGuideStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid guide id"})
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SetGuideStatus(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input.Status, input.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
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
	var input service.TeamVehicleInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.CreateVehicle(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, row)
}
func (c *TeamController) UpdateVehicle(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid vehicle id"})
		return
	}
	var input service.TeamVehicleInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.UpdateVehicle(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}
func (c *TeamController) SetVehicleStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid vehicle id"})
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SetVehicleStatus(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), input.Status, input.Reason)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *TeamController) ListGroups(ctx *gin.Context) {
	options, err := parseTeamGroupListOptions(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	groups, total, err := c.Service.ListGroupsWithOptions(ctx.GetUint("tenant_id"), options)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": groups, "total": total, "page": options.Page, "page_size": options.PageSize})
}

func parseTeamGroupListOptions(ctx *gin.Context) (service.TeamGroupListOptions, error) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	options := service.TeamGroupListOptions{
		Page: page, PageSize: pageSize,
		Keyword: ctx.Query("keyword"), Status: ctx.Query("status"),
	}
	if value := ctx.Query("visit_start"); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return options, fmt.Errorf("开始到园日期格式必须为 YYYY-MM-DD")
		}
		options.VisitStart = &parsed
	}
	if value := ctx.Query("visit_end"); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return options, fmt.Errorf("结束到园日期格式必须为 YYYY-MM-DD")
		}
		options.VisitEnd = &parsed
	}
	if options.VisitStart != nil && options.VisitEnd != nil && options.VisitStart.After(*options.VisitEnd) {
		return options, fmt.Errorf("开始到园日期不能晚于结束到园日期")
	}
	return options, nil
}

func (c *TeamController) CreateGroup(ctx *gin.Context) {
	var input struct {
		Name             string `json:"name" binding:"required"`
		SupplierTenantID uint   `json:"supplier_tenant_id" binding:"required"`
		ScenicAreaID     uint   `json:"scenic_area_id" binding:"required"`
		ContractID       uint   `json:"contract_id"`
		VisitDate        string `json:"visit_date" binding:"required"`
		ExpectedCount    int    `json:"expected_count" binding:"required,min=1"`
		GuideID          uint   `json:"guide_id"`
		VehicleID        uint   `json:"vehicle_id"`
		AgentID          uint   `json:"agent_id"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "团队名称、景区供应商、所属景区、到园日期和计划人数均为必填，计划人数至少为 1"})
		return
	}
	visitDate, err := parseTeamVisitDate(input.VisitDate)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group := model.TourGroup{
		Name: input.Name, SupplierTenantID: input.SupplierTenantID, ScenicAreaID: input.ScenicAreaID,
		ContractID: input.ContractID, VisitDate: visitDate, ExpectedCount: input.ExpectedCount,
		GuideID: input.GuideID, VehicleID: input.VehicleID, AgentID: input.AgentID,
	}
	if err := c.Service.CreateGroup(ctx.GetUint("tenant_id"), &group); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, group)
}

func parseTeamVisitDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		year, month, day := parsed.Date()
		return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, fmt.Errorf("游玩日期格式必须为 YYYY-MM-DD 或 RFC3339")
}

func (c *TeamController) UpdateGroupPlan(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "团队编号无效"})
		return
	}
	var input service.TeamGroupPlanUpdate
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	group, err := c.Service.UpdateGroupPlan(ctx.GetUint("tenant_id"), uint(groupID), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func (c *TeamController) CancelGroup(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "团队编号无效"})
		return
	}
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "取消团队计划必须填写原因"})
		return
	}
	group, err := c.Service.CancelGroup(ctx.GetUint("tenant_id"), uint(groupID), ctx.GetUint("user_id"), body.Reason)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func (c *TeamController) CreateContractOrder(ctx *gin.Context) {
	groupID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || groupID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var input service.TeamOrderInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	order, err := c.Service.CreateContractOrder(ctx.GetUint("tenant_id"), uint(groupID), ctx.GetUint("user_id"), input)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, order)
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
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
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
