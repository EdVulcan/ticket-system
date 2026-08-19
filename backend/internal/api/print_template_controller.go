package api

import (
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type PrintTemplateController struct{ Service service.PrintTemplateService }

func (c *PrintTemplateController) List(ctx *gin.Context) {
	scenicAreaID, _ := strconv.ParseUint(ctx.Query("scenic_area_id"), 10, 32)
	productID, _ := strconv.ParseUint(ctx.Query("product_id"), 10, 32)
	rows, err := c.Service.List(ctx.GetUint("tenant_id"), uint(scenicAreaID), uint(productID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *PrintTemplateController) Get(ctx *gin.Context) {
	id, err := parsePrintTemplateID(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.Get(ctx.GetUint("tenant_id"), id)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "print template not found"})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *PrintTemplateController) Revisions(ctx *gin.Context) {
	id, err := parsePrintTemplateID(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rows, err := c.Service.ListRevisions(ctx.GetUint("tenant_id"), id)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "print template revisions not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *PrintTemplateController) Preview(ctx *gin.Context) {
	var body service.PrintTemplatePreviewRequest
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := c.Service.Preview(ctx.GetUint("tenant_id"), body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func (c *PrintTemplateController) Save(ctx *gin.Context) {
	var body service.PrintTemplateSaveRequest
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SaveDraft(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), body, ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *PrintTemplateController) Publish(ctx *gin.Context) {
	id, err := parsePrintTemplateID(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.Publish(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), id, ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *PrintTemplateController) SetStatus(ctx *gin.Context) {
	id, err := parsePrintTemplateID(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := c.Service.SetStatus(ctx.GetUint("tenant_id"), ctx.GetUint("user_id"), id, body.Status, ctx.GetString("role"))
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func parsePrintTemplateID(ctx *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		return 0, errors.New("invalid print template id")
	}
	return uint(id), nil
}
