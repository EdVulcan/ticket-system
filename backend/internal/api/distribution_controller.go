package api

import (
	"math"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type DistributionController struct {
	Service service.DistributionService
}

func (c *DistributionController) CreateOffer(ctx *gin.Context) {
	var req struct {
		DistributorTenantID uint       `json:"distributor_tenant_id" binding:"required"`
		SourceProductID     uint       `json:"source_product_id" binding:"required"`
		SettlementPrice     float64    `json:"settlement_price" binding:"required"`
		MinimumRetailPrice  float64    `json:"minimum_retail_price"`
		Quota               int        `json:"quota"`
		CommissionBPS       int64      `json:"commission_bps"`
		AllowedChannels     string     `json:"allowed_channels" binding:"required"`
		SalesStartAt        *time.Time `json:"sales_start_at"`
		SalesEndAt          *time.Time `json:"sales_end_at"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	offer, err := c.Service.CreateOfferWithPolicy(ctx.GetUint("tenant_id"), req.DistributorTenantID, req.SourceProductID, req.SettlementPrice, req.MinimumRetailPrice, req.Quota, req.CommissionBPS, req.AllowedChannels, req.SalesStartAt, req.SalesEndAt)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"data": offer})
}

func (c *DistributionController) ListOffers(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	distributorID, _ := strconv.ParseUint(ctx.Query("distributor_tenant_id"), 10, 32)
	offers, total, err := c.Service.ListOffers(ctx.GetUint("tenant_id"), uint(distributorID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": offers, "total": total, "page": page, "page_size": pageSize})
}

func (c *DistributionController) SetOfferStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid offer id"})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.SetOfferStatus(ctx.GetUint("tenant_id"), uint(id), ctx.GetUint("user_id"), body.Status, body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}

func (c *DistributionController) ListFulfillmentOrders(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	distributorID, _ := strconv.ParseUint(ctx.Query("distributor_tenant_id"), 10, 32)
	rows, total, err := c.Service.ListFulfillmentOrders(ctx.GetUint("tenant_id"), uint(distributorID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *DistributionController) Search(ctx *gin.Context) {
	code := ctx.Query("code")
	if code == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "必须提供系统编号"})
		return
	}

	tenant, err := c.Service.GetSupplierByCode(code)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Only return public info
	ctx.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"name":    tenant.Name,
			"contact": tenant.Contact,
			"code":    tenant.SystemCode,
		},
	})
}

func (c *DistributionController) Apply(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	var req struct {
		SystemCode string `json:"system_code" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.ApplyAgent(tenantID.(uint), req.SystemCode); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "申请已提交"})
}

func (c *DistributionController) ListSuppliers(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	list, err := c.Service.ListSuppliers(tenantID.(uint))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": list})
}

func (c *DistributionController) ListAgents(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	list, err := c.Service.ListMyAgents(tenantID.(uint))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": list})
}

func (c *DistributionController) AuditAgent(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")
	id, _ := strconv.Atoi(ctx.Param("id"))

	var req struct {
		Status string `json:"status" binding:"required"` // active, rejected
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.AuditAgent(tenantID.(uint), uint(id), req.Status); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "审核完成"})
}

// ListDistributableProducts 代理商获取供应商的可分销商品
func (c *DistributionController) ListDistributableProducts(ctx *gin.Context) {
	supplierID, _ := strconv.Atoi(ctx.Query("supplier_id"))

	products, err := c.Service.ListDistributableProducts(ctx.GetUint("tenant_id"), uint(supplierID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"data": products})
}

// ImportProduct 代理商导入商品
func (c *DistributionController) ImportProduct(ctx *gin.Context) {
	tenantID, _ := ctx.Get("tenant_id")

	var req struct {
		SourceProductID uint    `json:"source_product_id" binding:"required"`
		Name            string  `json:"name" binding:"required"`
		Price           float64 `json:"price" binding:"required"`
		Type            string  `json:"type" binding:"required"` // online, offline
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.Service.ImportProduct(tenantID.(uint), req.SourceProductID, req.Name, req.Price, req.Type); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "商品对接成功"})
}

// RechargeAgent 充值接口
func (c *DistributionController) RechargeAgent(ctx *gin.Context) {
	supplierTenantID, _ := ctx.Get("tenant_id")

	// Agent ID from URL param
	agentID, _ := strconv.Atoi(ctx.Param("id"))

	var req struct {
		Amount         float64 `json:"amount"`
		AmountCents    int64   `json:"amount_cents"`
		IdempotencyKey string  `json:"idempotency_key" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// NOTE: In real app, we should probably check if agentID refers to a valid relationship first,
	// but Service logic (Where owner=agent AND manager=supplier) covers permission check implicitly.

	// Mapping: agentID here is the Relationship ID or Tenant ID?
	// The route will likely be /agents/:id/recharge.
	// If :id is RelationshipID, we need to find AgentTenantID.
	// If :id is AgentTenantID, straightforward.
	// Let's assume :id is DistributorRelationship.ID for consistency with Audit.
	// We need to resolve AgentTenantID from RelationshipID.

	// Wait, let's keep it simple. Let's assume frontend passes Relationship ID.
	// We need a helper to get AgentTenantID from RelID?
	// Or we can just pass AgentTenantID from frontend if available?
	// Frontend "My Agents" list has `id` (relationship id).
	// Let's fetch the rel to get AgentTenantID.

	// Actually, let's look at AuditAgent: it takes Relationship ID.
	// So let's stick to Relationship ID.

	// But `RechargeAgent` service needs `AgentTenantID`. A helper is needed or duplicates query.
	// Let's add a quick lookup in Controller or modify Service to take RelID?
	// Modifying Service is cleaner but I just wrote it to take TenantID.
	// Let's do a quick lookup here using DB (not ideal MVC but pragmatic).

	var rel model.DistributorRelationship
	if err := model.DB.First(&rel, agentID).Error; err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "Relationship not found"})
		return
	}

	// Verify this supplier owns this relationship
	if rel.SupplierTenantID != supplierTenantID.(uint) {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "Unauthorized"})
		return
	}

	if req.AmountCents <= 0 {
		req.AmountCents = int64(math.Round(req.Amount * 100))
	}
	if req.AmountCents <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "recharge amount must be greater than zero"})
		return
	}
	document, err := c.Service.RechargeAgent(supplierTenantID.(uint), rel.AgentTenantID, float64(req.AmountCents)/100, req.IdempotencyKey, ctx.GetUint("user_id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, document)
}
