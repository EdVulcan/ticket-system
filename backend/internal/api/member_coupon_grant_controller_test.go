package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ticket-backend/internal/authz"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type fakeMemberCouponGrantService struct {
	tenant uint
	input  service.MemberCouponGrantIssueInput
	key    string
	calls  int
	err    error
}

func (f *fakeMemberCouponGrantService) Preview(_ context.Context, tenant, template uint, members []uint) (*service.MemberCouponGrantPreviewResult, error) {
	f.calls++
	f.tenant = tenant
	f.input = service.MemberCouponGrantIssueInput{TemplateID: template, MemberIDs: members}
	return &service.MemberCouponGrantPreviewResult{}, f.err
}
func (f *fakeMemberCouponGrantService) Issue(_ context.Context, tenant uint, input service.MemberCouponGrantIssueInput) (*service.MemberCouponGrantResult, error) {
	f.calls++
	f.tenant = tenant
	f.input = input
	return &service.MemberCouponGrantResult{}, f.err
}
func (f *fakeMemberCouponGrantService) Get(_ context.Context, tenant uint, key string) (*service.MemberCouponGrantResult, error) {
	f.calls++
	f.tenant = tenant
	f.key = key
	return &service.MemberCouponGrantResult{}, f.err
}

func invokeMemberCouponGrantHTTP(t *testing.T, fake *fakeMemberCouponGrantService, method, path, scope, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.Use(func(ctx *gin.Context) {
		ctx.Set("scope", scope)
		ctx.Set("role", role)
		ctx.Set("tenant_id", uint(42))
		ctx.Set("user_id", uint(77))
		ctx.Next()
	})
	controller := MemberCouponGrantController{Service: fake}
	engine.POST("/grants/preview", controller.Preview)
	engine.POST("/grants", controller.Issue)
	engine.GET("/grants/:batchKey", controller.Get)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	return response
}

func TestMemberCouponGrantHTTPRejectsUnprivilegedScopesBeforeResolvingMembers(t *testing.T) {
	for _, operation := range []struct{ method, path string }{{http.MethodPost, "/grants/preview"}, {http.MethodPost, "/grants"}, {http.MethodGet, "/grants/batch-key"}} {
		for _, identity := range []struct{ scope, role string }{{"tenant", authz.RoleViewer}, {"tenant", authz.RoleProductOperator}, {"platform", "platform_admin"}, {"platform", authz.RoleTenantAdmin}, {"", authz.RoleTenantAdmin}} {
			fake := &fakeMemberCouponGrantService{}
			response := invokeMemberCouponGrantHTTP(t, fake, operation.method, operation.path, identity.scope, identity.role, `{"template_id":1,"member_ids":[9]}`)
			if response.Code != http.StatusForbidden || fake.calls != 0 {
				t.Fatalf("%s %s as %+v status=%d calls=%d", operation.method, operation.path, identity, response.Code, fake.calls)
			}
		}
	}
}

func TestMemberCouponGrantHTTPUsesServerOwnershipAndActor(t *testing.T) {
	body := `{"template_id":4,"member_ids":[9,10],"idempotency_key":"batch-key","reason":"  会员回馈  ","tenant_id":999,"actor_user_id":999,"actor_role":"platform_admin","customer_id":"forged-wallet","channel_account_id":999,"discount_cents":999999}`
	fake := &fakeMemberCouponGrantService{}
	response := invokeMemberCouponGrantHTTP(t, fake, http.MethodPost, "/grants", "tenant", authz.RoleTenantAdmin, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.tenant != 42 || fake.input.ActorUserID != 77 || fake.input.ActorRole != authz.RoleTenantAdmin || fake.input.TemplateID != 4 || len(fake.input.MemberIDs) != 2 || fake.input.Reason != "会员回馈" || fake.input.IdempotencyKey != "batch-key" {
		t.Fatalf("untrusted ownership or request: tenant=%d input=%+v", fake.tenant, fake.input)
	}
	response = invokeMemberCouponGrantHTTP(t, fake, http.MethodPost, "/grants/preview", "tenant", authz.RoleTenantAdmin, body)
	if response.Code != http.StatusOK || fake.tenant != 42 || fake.input.TemplateID != 4 {
		t.Fatalf("preview status=%d tenant=%d", response.Code, fake.tenant)
	}
	response = invokeMemberCouponGrantHTTP(t, fake, http.MethodGet, "/grants/original-key?tenant_id=999", "tenant", authz.RoleTenantAdmin, "")
	if response.Code != http.StatusOK || fake.tenant != 42 || fake.key != "original-key" {
		t.Fatalf("recovery widened tenant: %+v status=%d", fake, response.Code)
	}
}

func TestMemberCouponGrantHTTPDistinguishesRejectionConflictAndUnknown(t *testing.T) {
	for _, check := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrMemberCouponGrantInvalid, 400, "MEMBER_COUPON_GRANT_INVALID"},
		{service.ErrMemberCouponGrantEligibility, 400, "MEMBER_COUPON_GRANT_INELIGIBLE"},
		{service.ErrMemberCouponGrantConflict, 409, "MEMBER_COUPON_GRANT_CONFLICT"},
		{service.ErrPromotionScopeDenied, 403, "MEMBER_COUPON_GRANT_FORBIDDEN"},
		{service.ErrMemberCouponGrantScopeDenied, 403, "MEMBER_COUPON_GRANT_FORBIDDEN"},
		{gorm.ErrRecordNotFound, 404, "MEMBER_COUPON_GRANT_NOT_FOUND"},
		{service.ErrMemberCouponGrantUnavailable, 503, "MEMBER_COUPON_GRANT_UNAVAILABLE"},
		{errors.New("database connection lost"), 500, "MEMBER_COUPON_GRANT_UNKNOWN"},
	} {
		fake := &fakeMemberCouponGrantService{err: check.err}
		response := invokeMemberCouponGrantHTTP(t, fake, http.MethodPost, "/grants", "tenant", authz.RoleTenantAdmin, `{"template_id":4,"member_ids":[9],"idempotency_key":"batch-key"}`)
		var result map[string]interface{}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != check.status || result["reason_code"] != check.code {
			t.Fatalf("err=%v status=%d body=%s", check.err, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "connection lost") {
			t.Fatal("internal database error leaked")
		}
	}
	for _, path := range []string{"/grants", "/grants/preview"} {
		fake := &fakeMemberCouponGrantService{}
		response := invokeMemberCouponGrantHTTP(t, fake, http.MethodPost, path, "tenant", authz.RoleTenantAdmin, "{")
		if response.Code != http.StatusBadRequest || fake.calls != 0 {
			t.Fatalf("malformed JSON status=%d calls=%d", response.Code, fake.calls)
		}
	}
}
