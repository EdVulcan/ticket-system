package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CommerceCatalogController exposes the independent restaurant/retail
// catalog. It intentionally has no access to the ticket Product controller.
type CommerceCatalogController struct {
	Service service.CommerceCatalogService
	Images  service.CommerceImageStore
}

// CommerceOperationsController exposes the non-ticket operational pieces of
// the commercial catalog. Customer cart endpoints are intentionally kept out
// of this admin router until a channel/customer identity middleware is wired;
// accepting a caller-supplied customer ID from an operator token would break
// cart isolation.
type CommerceOperationsController struct {
	Service service.CommerceOperationsService
}

func (c *CommerceOperationsController) CheckoutCart(ctx *gin.Context) {
	cartID, err := parseCommerceID(ctx, "cartID")
	if err != nil {
		return
	}
	var input service.CommerceCheckoutCartInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "结算信息格式不正确"})
		return
	}
	order, err := c.Service.CheckoutCart(ctx.GetUint("tenant_id"), cartID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, order)
}

func (c *CommerceCatalogController) CreateProduct(ctx *gin.Context) {
	var input service.CreateCommerceProductInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品信息格式不正确"})
		return
	}
	product, err := c.Service.CreateProduct(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, product)
}

func (c *CommerceCatalogController) ListProducts(ctx *gin.Context) {
	products, err := c.Service.ListProducts(
		ctx.GetUint("tenant_id"),
		ctx.Query("business_type"),
		ctx.Query("status"),
		ctx.Query("search"),
	)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": products, "total": len(products)})
}

func (c *CommerceCatalogController) GetProduct(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	product, err := c.Service.GetProduct(ctx.GetUint("tenant_id"), productID)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, product)
}

func (c *CommerceCatalogController) SetProductStatus(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品状态不正确"})
		return
	}
	product, err := c.Service.SetProductStatus(ctx.GetUint("tenant_id"), productID, body.Status)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, product)
}

// UploadProductMedia accepts only server-managed commercial product images.
// The product and tenant are resolved from the authenticated route/context;
// clients cannot attach an arbitrary remote URL or another tenant's asset.
func (c *CommerceCatalogController) UploadProductMedia(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(ctx.PostForm("kind")))
	if kind != service.CommerceProductMediaCover && kind != service.CommerceProductMediaDetail {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品图片类型不正确"})
		return
	}
	file, header, err := ctx.Request.FormFile("image")
	if err != nil || file == nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请选择商品图片"})
		return
	}
	defer file.Close()
	if header != nil && header.Size > service.MaxCommerceImageBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品图片不能超过 5 MB"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, service.MaxCommerceImageBytes+1))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品图片读取失败"})
		return
	}
	if len(data) > service.MaxCommerceImageBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "商品图片不能超过 5 MB"})
		return
	}
	imageURL, err := c.Images.Save(ctx.GetUint("tenant_id"), productID, kind, data)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	media, err := c.Service.AddProductMedia(ctx.GetUint("tenant_id"), productID, kind, imageURL)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, media)
}

func (c *CommerceCatalogController) DeleteProductMedia(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	mediaID, err := parseCommerceID(ctx, "mediaID")
	if err != nil {
		return
	}
	if err := c.Service.RemoveProductMedia(ctx.GetUint("tenant_id"), productID, mediaID); err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (c *CommerceCatalogController) CreateSKU(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	var input service.CreateCommerceSKUInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "SKU 信息格式不正确"})
		return
	}
	sku, err := c.Service.CreateSKU(ctx.GetUint("tenant_id"), productID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, sku)
}

func (c *CommerceCatalogController) UpdateSKU(ctx *gin.Context) {
	skuID, err := parseCommerceID(ctx, "skuID")
	if err != nil {
		return
	}
	var input service.UpdateCommerceSKUInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "SKU 信息格式不正确"})
		return
	}
	sku, err := c.Service.UpdateSKU(ctx.GetUint("tenant_id"), skuID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, sku)
}

func (c *CommerceOperationsController) ListOptionGroups(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	rows, err := c.Service.ListOptionGroups(ctx.GetUint("tenant_id"), productID)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows})
}

func (c *CommerceOperationsController) CreateOptionGroup(ctx *gin.Context) {
	productID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	var input service.CreateCommerceOptionGroupInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "规格组信息格式不正确"})
		return
	}
	row, err := c.Service.CreateOptionGroup(ctx.GetUint("tenant_id"), productID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommerceOperationsController) UpdateOptionGroup(ctx *gin.Context) {
	groupID, err := parseCommerceID(ctx, "groupID")
	if err != nil {
		return
	}
	var input service.UpdateCommerceOptionGroupInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "规格组信息格式不正确"})
		return
	}
	row, err := c.Service.UpdateOptionGroup(ctx.GetUint("tenant_id"), groupID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceOperationsController) DeleteOptionGroup(ctx *gin.Context) {
	groupID, err := parseCommerceID(ctx, "groupID")
	if err != nil {
		return
	}
	if err := c.Service.DeleteOptionGroup(ctx.GetUint("tenant_id"), groupID); err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (c *CommerceOperationsController) CreateOption(ctx *gin.Context) {
	groupID, err := parseCommerceID(ctx, "groupID")
	if err != nil {
		return
	}
	var input service.CreateCommerceOptionInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "规格信息格式不正确"})
		return
	}
	row, err := c.Service.CreateOption(ctx.GetUint("tenant_id"), groupID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommerceOperationsController) UpdateOption(ctx *gin.Context) {
	groupID, err := parseCommerceID(ctx, "groupID")
	if err != nil {
		return
	}
	optionID, err := parseCommerceID(ctx, "optionID")
	if err != nil {
		return
	}
	var input service.UpdateCommerceOptionInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "规格信息格式不正确"})
		return
	}
	row, err := c.Service.UpdateOption(ctx.GetUint("tenant_id"), groupID, optionID, input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceOperationsController) DeleteOption(ctx *gin.Context) {
	groupID, err := parseCommerceID(ctx, "groupID")
	if err != nil {
		return
	}
	optionID, err := parseCommerceID(ctx, "optionID")
	if err != nil {
		return
	}
	if err := c.Service.DeleteOption(ctx.GetUint("tenant_id"), groupID, optionID); err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (c *CommerceOperationsController) ListLocations(ctx *gin.Context) {
	rows, err := c.Service.ListLocations(ctx.GetUint("tenant_id"), ctx.Query("business_type"), ctx.Query("status"))
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommerceOperationsController) CreateLocation(ctx *gin.Context) {
	var input service.CreateCommerceLocationInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "履约地点信息格式不正确"})
		return
	}
	row, err := c.Service.CreateLocation(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, row)
}

func (c *CommerceOperationsController) SetLocationStatus(ctx *gin.Context) {
	locationID, err := parseCommerceID(ctx, "id")
	if err != nil {
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "履约地点状态不正确"})
		return
	}
	row, err := c.Service.SetLocationStatus(ctx.GetUint("tenant_id"), locationID, body.Status)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceOperationsController) ListInventory(ctx *gin.Context) {
	rows, err := c.Service.ListInventory(ctx.GetUint("tenant_id"), ctx.Query("business_type"))
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"data": rows, "total": len(rows)})
}

func (c *CommerceOperationsController) SetInventory(ctx *gin.Context) {
	var input service.CommerceInventoryInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "库存信息格式不正确"})
		return
	}
	input.ActorUserID = ctx.GetUint("user_id")
	input.ActorRole = ctx.GetString("role")
	row, err := c.Service.SetInventory(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func (c *CommerceOperationsController) AdjustInventory(ctx *gin.Context) {
	var input service.CommerceInventoryAdjustmentInput
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "库存调整信息格式不正确"})
		return
	}
	input.ActorUserID = ctx.GetUint("user_id")
	input.ActorRole = ctx.GetString("role")
	row, err := c.Service.AdjustInventory(ctx.GetUint("tenant_id"), input)
	if err != nil {
		commerceCatalogError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, row)
}

func parseCommerceID(ctx *gin.Context, name string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(ctx.Param(name)), 10, 32)
	if err != nil || value == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的商品编号"})
		return 0, errors.New("invalid commerce id")
	}
	return uint(value), nil
}

func commerceCatalogError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrBusinessCapabilityInactive), errors.Is(err, service.ErrCapabilityInactive), errors.Is(err, service.ErrTenantUnavailable):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrCommerceProductInvalid), errors.Is(err, service.ErrCommerceSKUInvalid),
		errors.Is(err, service.ErrCommerceOptionInvalid), errors.Is(err, service.ErrCommerceLocationInvalid),
		errors.Is(err, service.ErrCommerceInventoryInvalid), errors.Is(err, service.ErrCommerceCartInvalid):
		status = http.StatusBadRequest
	}
	message := "商业商品操作失败"
	if status == http.StatusNotFound {
		message = "商品不存在"
	} else if status == http.StatusForbidden {
		message = "当前商户未启用该商业能力"
	} else if status == http.StatusBadRequest {
		message = "商品信息不符合要求"
	}
	ctx.JSON(status, gin.H{"error": message})
}
