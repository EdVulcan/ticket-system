package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"ticket-backend/internal/service"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RefreshXiaohongshuAudit queries the saved product identity, never client
// supplied status or publication data. It does not resubmit the product.
func (c *ChannelController) RefreshXiaohongshuAudit(ctx *gin.Context) {
	accountID, accountErr := strconv.ParseUint(ctx.Param("id"), 10, 32)
	mappingID, mappingErr := strconv.ParseUint(ctx.Param("mappingId"), 10, 32)
	if accountErr != nil || mappingErr != nil || accountID == 0 || mappingID == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel or mapping id"})
		return
	}
	queryContext, cancel := context.WithTimeout(ctx.Request.Context(), 20*time.Second)
	defer cancel()
	svc := service.XiaohongshuProductAuditService{Products: c.XiaohongshuProducts}
	view, err := svc.RefreshAudit(queryContext, ctx.GetUint("tenant_id"), uint(accountID), uint(mappingID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "小红书商品发布配置不存在或不可用"})
			return
		}
		ctx.JSON(http.StatusBadGateway, gin.H{"error": "审核查询未完成，请稍后重试或检查商品发布配置"})
		return
	}
	ctx.JSON(http.StatusOK, view)
}
