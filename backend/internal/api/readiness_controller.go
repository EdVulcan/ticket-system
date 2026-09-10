package api

import (
	"context"
	"net/http"
	"ticket-backend/internal/model"
	"time"

	"github.com/gin-gonic/gin"
)

// ReleaseRevision is injected into the production binary by the build job.
var ReleaseRevision = "development"

func Readiness(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	if model.DB == nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	queryContext, cancel := context.WithTimeout(ctx.Request.Context(), 2*time.Second)
	defer cancel()
	var version int
	err := model.DB.WithContext(queryContext).Model(&model.SchemaMigration{}).Select("COALESCE(MAX(version),0)").Scan(&version).Error
	if err != nil || version != model.CurrentPostgresSchemaVersion {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "ok", "revision": ReleaseRevision, "schema_version": version})
}
