package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ChannelController struct {
	Service             service.ChannelService
	Gateway             *service.ChannelGatewayService
	CtripSync           service.CtripSyncService
	XiaohongshuProducts service.XiaohongshuProductService
	XiaohongshuImages   service.XiaohongshuImageStore
}

func (c *ChannelController) List(ctx *gin.Context) {
	rows, err := c.Service.List(ctx.GetUint("tenant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) Create(ctx *gin.Context) {
	var body struct {
		model.ChannelAccount
		Secret         string `json:"secret"`
		AESKey         string `json:"aes_key"`
		AESIV          string `json:"aes_iv"`
		MessageToken   string `json:"message_token"`
		EncodingAESKey string `json:"encoding_aes_key"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Type == "ctrip" {
		if err := c.Service.CreateCtrip(ctx.GetUint("tenant_id"), &body.ChannelAccount, body.AppID, body.Secret, body.AESKey, body.AESIV); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		body.ProtocolConfigured = true
		body.Secret = ""
		body.SecretCiphertext = ""
		body.ProtocolConfigCiphertext = ""
		ctx.JSON(http.StatusCreated, body.ChannelAccount)
		return
	}
	if body.Type == "xiaohongshu" {
		if err := c.Service.CreateXiaohongshuIntegration(ctx.GetUint("tenant_id"), &body.ChannelAccount, body.AppID, body.Secret, body.MessageToken, body.EncodingAESKey); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		body.ProtocolConfigured = body.SecretCiphertext != "" && body.VerifyKeyCiphertext != "" && body.ProtocolConfigCiphertext != ""
		body.Secret = ""
		body.SecretCiphertext = ""
		body.VerifyKeyCiphertext = ""
		body.ProtocolConfigCiphertext = ""
		ctx.JSON(http.StatusCreated, body.ChannelAccount)
		return
	}
	secret, err := c.Service.Create(ctx.GetUint("tenant_id"), &body.ChannelAccount, body.Secret)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	body.Secret = secret
	body.SecretCiphertext = ""
	body.VerifyKeyCiphertext = ""
	ctx.JSON(http.StatusCreated, body)
}

func (c *ChannelController) SetStatus(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.SetStatus(ctx.GetUint("tenant_id"), uint(id), body.Status); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": body.Status})
}

func (c *ChannelController) RotateSecret(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	secret, err := c.Service.RotateSecret(ctx.GetUint("tenant_id"), uint(id))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"secret": secret})
}

func (c *ChannelController) ConfigureCtrip(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		AccountID string `json:"account_id" binding:"required"`
		SignKey   string `json:"sign_key" binding:"required"`
		AESKey    string `json:"aes_key" binding:"required"`
		AESIV     string `json:"aes_iv" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.ConfigureCtrip(ctx.GetUint("tenant_id"), uint(id), body.AccountID, body.SignKey, body.AESKey, body.AESIV); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"configured": true})
}

func (c *ChannelController) ConfigureXiaohongshu(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		AppID          string `json:"app_id" binding:"required"`
		AppSecret      string `json:"app_secret" binding:"required"`
		MessageToken   string `json:"message_token" binding:"required"`
		EncodingAESKey string `json:"encoding_aes_key" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.ConfigureXiaohongshuIntegration(ctx.GetUint("tenant_id"), uint(id), body.AppID, body.AppSecret, body.MessageToken, body.EncodingAESKey); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"configured": true})
}

func (c *ChannelController) AddMapping(ctx *gin.Context) {
	var mapping model.ChannelProductMapping
	if err := ctx.ShouldBindJSON(&mapping); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AddMapping(ctx.GetUint("tenant_id"), &mapping); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, mapping)
}

func (c *ChannelController) ListMappings(ctx *gin.Context) {
	accountID, _ := strconv.ParseUint(ctx.Query("channel_account_id"), 10, 32)
	rows, err := c.Service.ListMappings(ctx.GetUint("tenant_id"), uint(accountID))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) UpdateMapping(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	var body struct {
		ExternalCode     string `json:"external_code" binding:"required"`
		DisplayName      string `json:"display_name"`
		ChannelSaleCents int64  `json:"channel_sale_cents"`
		ChannelCostCents int64  `json:"channel_cost_cents"`
		Status           string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.UpdateMapping(ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID), service.ChannelMappingUpdate{
		ExternalCode: body.ExternalCode, DisplayName: body.DisplayName,
		ChannelSaleCents: body.ChannelSaleCents, ChannelCostCents: body.ChannelCostCents, Status: body.Status,
	}); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"message": "mapping updated"})
}

func (c *ChannelController) ListXiaohongshuCategories(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	rows, err := c.XiaohongshuProducts.ListCategories(ctx.Request.Context(), ctx.GetUint("tenant_id"), uint(accountID))
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) ListXiaohongshuPOIs(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	result, err := c.XiaohongshuProducts.ListPOIs(ctx.Request.Context(), ctx.GetUint("tenant_id"), uint(accountID), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": result.List, "total": result.Total})
}

func (c *ChannelController) GetXiaohongshuProductConfig(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	config, err := c.XiaohongshuProducts.GetConfig(ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusOK, gin.H{})
			return
		}
		ctx.JSON(http.StatusNotFound, gin.H{"error": "尚未配置小红书商品发布参数"})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *ChannelController) UploadXiaohongshuProductImage(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	tenantID := ctx.GetUint("tenant_id")
	if err := c.XiaohongshuProducts.EnsureMappingAccess(tenantID, uint(accountID), uint(mappingID)); err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书商品映射不存在或不可用"})
		return
	}
	header, err := ctx.FormFile("image")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择商品图片"})
		return
	}
	if header.Size <= 0 || header.Size > service.MaxXiaohongshuImageBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品图片必须小于 5 MB"})
		return
	}
	file, err := header.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "读取商品图片失败"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxXiaohongshuImageBytes+1))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "读取商品图片失败"})
		return
	}
	imageURL, err := c.XiaohongshuImages.Save(tenantID, uint(accountID), data)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"image_url": imageURL})
}

func (c *ChannelController) SaveXiaohongshuProductConfig(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	var body service.XiaohongshuProductConfigInput
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config, err := c.XiaohongshuProducts.SaveConfig(ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID), ctx.GetUint("user_id"), ctx.GetString("role"), body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, config)
}

func (c *ChannelController) SyncXiaohongshuProduct(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	if err := c.XiaohongshuProducts.Sync(ctx.Request.Context(), ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID), ctx.GetUint("user_id"), ctx.GetString("role")); err != nil {
		ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"synced": true})
}

func (c *ChannelController) SyncCtripMapping(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	var body struct {
		StartDate string `json:"start_date" binding:"required"`
		EndDate   string `json:"end_date" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	start, startErr := time.ParseInLocation("2006-01-02", body.StartDate, time.Local)
	end, endErr := time.ParseInLocation("2006-01-02", body.EndDate, time.Local)
	if startErr != nil || endErr != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid synchronization date range"})
		return
	}
	result, err := c.CtripSync.EnqueueMappingSync(ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID), start, end)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, result)
}

func (c *ChannelController) SimulateCtripSandboxConsumption(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		SupplierOrderID string `json:"supplier_order_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := c.CtripSync.SimulateSandboxConsumption(
		ctx.GetUint("tenant_id"), uint(accountID), body.SupplierOrderID,
		ctx.GetUint("user_id"), ctx.GetString("role"),
	)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, gin.H{"task": task})
}

func (c *ChannelController) UpdateCtripMappingPricing(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	var body struct {
		SaleCents int64 `json:"channel_sale_cents"`
		CostCents int64 `json:"channel_cost_cents"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.UpdateMappingPricing(ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID), body.SaleCents, body.CostCents); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"updated": true})
}

func (c *ChannelController) ListCtripSyncTasks(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	limit, _ := strconv.Atoi(ctx.DefaultQuery("limit", "100"))
	rows, err := c.CtripSync.ListTasks(ctx.GetUint("tenant_id"), uint(accountID), limit)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *ChannelController) ListRequests(ctx *gin.Context) {
	accountID, _ := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListRequests(ctx.GetUint("tenant_id"), uint(accountID), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *ChannelController) ListOrders(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListOrders(ctx.GetUint("tenant_id"), uint(accountID), ctx.Query("search"), ctx.Query("status"), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *ChannelController) GetOrder(ctx *gin.Context) {
	accountID, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || accountID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	row, err := c.Service.GetOrder(ctx.GetUint("tenant_id"), uint(accountID), ctx.Param("orderNo"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "channel order not found"})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *ChannelController) AuthorizeRequestRetry(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	requestID, requestErr := strconv.ParseUint(ctx.Param("requestId"), 10, 32)
	if accountErr != nil || requestErr != nil || accountID == 0 || requestID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or request id"})
		return
	}
	var body struct {
		Reason string `json:"reason" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := c.Service.AuthorizeRequestRetry(ctx.GetUint("tenant_id"), uint(accountID), uint(requestID), ctx.GetUint("user_id"), ctx.GetString("role"), body.Reason); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "retryable"})
}

func (c *ChannelController) ImportBill(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	var body struct {
		IdempotencyKey string                     `json:"idempotency_key" binding:"required"`
		Records        []service.ChannelBillInput `json:"records" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := c.Service.ImportBill(ctx.GetUint("tenant_id"), uint(id), body.IdempotencyKey, body.Records)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, report)
}

func (c *ChannelController) ListReconciliations(ctx *gin.Context) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 32)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel id"})
		return
	}
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(ctx.DefaultQuery("page_size", "20"))
	rows, total, err := c.Service.ListReconciliations(ctx.GetUint("tenant_id"), uint(id), page, pageSize)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": total, "page": page, "page_size": pageSize})
}

func (c *ChannelController) GetReconciliation(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	reconciliationID, reconciliationErr := strconv.ParseUint(ctx.Param("reconciliationId"), 10, 32)
	if accountErr != nil || reconciliationErr != nil || accountID == 0 || reconciliationID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or reconciliation id"})
		return
	}
	row, err := c.Service.GetReconciliation(ctx.GetUint("tenant_id"), uint(accountID), uint(reconciliationID))
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "channel reconciliation not found"})
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *ChannelController) Reserve(ctx *gin.Context) {
	var body struct {
		ExternalProductCode string `json:"external_product_code" binding:"required"`
		ExternalNo          string `json:"external_no" binding:"required"`
		Quantity            int    `json:"quantity" binding:"required"`
		Date                string `json:"date"`
		StockSlot           string `json:"stock_slot"`
		TTLSeconds          int    `json:"ttl_seconds"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var useDate *time.Time
	if body.Date != "" {
		value, err := time.ParseInLocation("2006-01-02", body.Date, time.Local)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid visit date"})
			return
		}
		useDate = &value
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	reservation, err := c.Gateway.CreateReservation(ctx, ctx.GetString("channel_type"), service.ChannelReservationRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ExternalProductCode: body.ExternalProductCode, ExternalNo: body.ExternalNo, Quantity: body.Quantity,
		UseDate: useDate, StockSlot: body.StockSlot, TTL: time.Duration(body.TTLSeconds) * time.Second,
	})
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, reservation)
}

func (c *ChannelController) Confirm(ctx *gin.Context) {
	var body struct {
		ReservationID uint   `json:"reservation_id" binding:"required"`
		ContactName   string `json:"contact_name"`
		ContactPhone  string `json:"contact_phone"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	order, err := c.Gateway.ConfirmOrder(ctx, ctx.GetString("channel_type"), service.ChannelConfirmRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ReservationID: body.ReservationID, ContactName: body.ContactName, ContactPhone: body.ContactPhone,
	})
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"order_no": order.Order.OrderNo, "status": order.Status, "items": order.Order.Items, "ticket_codes": order.TicketCodes})
}

func (c *ChannelController) Release(ctx *gin.Context) {
	var body struct {
		ReservationID uint `json:"reservation_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if c.Gateway == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "channel gateway is not configured"})
		return
	}
	if err := c.Gateway.ReleaseReservation(ctx, ctx.GetString("channel_type"), service.ChannelReleaseRequest{
		TenantID: ctx.GetUint("tenant_id"), AccountID: ctx.GetUint("channel_account_id"), Channel: ctx.GetString("channel_code"),
		ReservationID: body.ReservationID, Reason: "channel request",
	}); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "released"})
}
