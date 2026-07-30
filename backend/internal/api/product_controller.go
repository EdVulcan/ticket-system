package api

import (
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type ProductController struct {
	Service service.ProductService
}

// CreateRequest 聚合创建请求
type CreateProductRequest struct {
	Product model.Product    `json:"product"`
	Rule    model.TicketRule `json:"rule"`
}

func (c *ProductController) Create(ctx *gin.Context) {
	var req CreateProductRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Ensure TenantID consistency
	tenantID, _ := ctx.Get("tenant_id")
	req.Product.TenantID = tenantID.(uint)
	req.Rule.TenantID = req.Product.TenantID

	if err := c.Service.Create(&req.Product, &req.Rule); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, gin.H{
		"product": req.Product,
		"rule":    req.Rule,
	})
}

func (c *ProductController) Get(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	product, err := c.Service.Get(uint(id), ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Product not found"})
		return
	}
	ctx.JSON(http.StatusOK, product)
}

func (c *ProductController) Update(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var req CreateProductRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.Update(uint(id), ctx.GetUint("tenant_id"), &req.Product, &req.Rule); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "updated successfully"})
}

func (c *ProductController) List(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "10"))
	productType := ctx.DefaultQuery("type", "")

	products, total, err := c.Service.List(page, pageSize, ctx.GetUint("tenant_id"), productType)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"data":  products,
		"total": total,
		"page":  page,
	})
}

func (c *ProductController) Delete(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	if err := c.Service.Delete(uint(id), ctx.GetUint("tenant_id")); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "deleted successfully"})
}

func (c *ProductController) UpdateStatus(ctx *gin.Context) {
	id, _ := strconv.Atoi(ctx.Param("id"))
	var req struct {
		Status string `json:"status"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.UpdateStatus(uint(id), ctx.GetUint("tenant_id"), req.Status); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "status updated"})
}
