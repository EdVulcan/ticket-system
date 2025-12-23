package api

import (
	"net/http"
	"ticket-backend/internal/model"
	"ticket-backend/internal/service"
	"ticket-backend/internal/utils"

	"github.com/gin-gonic/gin"
)

type PaymentConfigController struct {
	Service service.PaymentService // Reuse PaymentService or create ConfigService
}

// GetConfigs 获取当前租户的所有支付配置
func (c *PaymentConfigController) GetConfigs(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")

	var configs []model.PaymentConfig
	if err := model.DB.Where("tenant_id = ?", tenantID).Find(&configs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Mask Secrets
	for i := range configs {
		if configs[i].Key != "" {
			configs[i].Key = "******"
		}
		if configs[i].PrivateKey != "" {
			configs[i].PrivateKey = "******"
		}
		if configs[i].PublicKey != "" {
			configs[i].PublicKey = "******"
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"data": configs})
}

// SaveConfig 保存或更新配置
func (c *PaymentConfigController) SaveConfig(ctx *gin.Context) {
	tenantID := ctx.GetUint("tenant_id")
	var req model.PaymentConfig
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.TenantID = tenantID

	// Encrypt Secrets
	if req.Key != "" && req.Key != "******" {
		enc, err := utils.EncryptAES(req.Key)
		if err == nil {
			req.Key = enc
		}
	}
	if req.PrivateKey != "" && req.PrivateKey != "******" {
		enc, err := utils.EncryptAES(req.PrivateKey)
		if err == nil {
			req.PrivateKey = enc
		}
	}
	if req.PublicKey != "" && req.PublicKey != "******" {
		enc, err := utils.EncryptAES(req.PublicKey)
		if err == nil {
			req.PublicKey = enc
		}
	}

	// Upsert based on TenantID + Provider
	var existing model.PaymentConfig
	if err := model.DB.Where("tenant_id = ? AND provider = ?", tenantID, req.Provider).First(&existing).Error; err == nil {
		// Update logic: preserve existing secrets if not changed
		if req.Key == "******" || req.Key == "" {
			req.Key = existing.Key
		}
		if req.PrivateKey == "******" || req.PrivateKey == "" {
			req.PrivateKey = existing.PrivateKey
		}
		if req.PublicKey == "******" || req.PublicKey == "" {
			req.PublicKey = existing.PublicKey
		}

		req.ID = existing.ID
		if err := model.DB.Save(&req).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		// Create
		if err := model.DB.Create(&req).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	ctx.JSON(http.StatusOK, req)
}
