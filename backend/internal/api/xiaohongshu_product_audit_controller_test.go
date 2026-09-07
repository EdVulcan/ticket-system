package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRefreshXiaohongshuAuditRejectsInvalidIdentityBeforeQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ids := range [][2]string{{"0", "1"}, {"1", "0"}, {"bad", "1"}, {"1", "-1"}, {"4294967296", "1"}} {
		t.Run(ids[0]+"/"+ids[1], func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/audit-refresh", nil)
			ctx.Params = gin.Params{{Key: "id", Value: ids[0]}, {Key: "mappingId", Value: ids[1]}}
			ctx.Set("tenant_id", uint(1))
			(&ChannelController{}).RefreshXiaohongshuAudit(ctx)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
