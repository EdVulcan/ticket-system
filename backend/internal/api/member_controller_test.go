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
	memberservice "ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeMemberService struct {
	page       MemberPage
	detail     MemberDetail
	getErr     error
	listErr    error
	statusErr  error
	listTenant uint
	getTenant  uint
	statusArgs struct {
		tenant, id, actor uint
		status, requestID string
	}
	exportErr  error
	exportArgs struct {
		tenant    uint
		reason    string
		sensitive bool
	}
}

func (s *fakeMemberService) List(_ context.Context, tenantID uint, query MemberListQuery) (MemberPage, error) {
	s.listTenant = tenantID
	if query.Page != 2 || query.PageSize != 5 || query.Status != "active" || query.Keyword != "彭" || query.Phone != "13346516523" {
		return MemberPage{}, errors.New("query not forwarded")
	}
	return s.page, s.listErr
}

func (s *fakeMemberService) Get(_ context.Context, tenantID, id uint) (MemberDetail, error) {
	s.getTenant = tenantID
	if id != 9 {
		return MemberDetail{}, errors.New("id not forwarded")
	}
	return s.detail, s.getErr
}

func (s *fakeMemberService) SetStatus(_ context.Context, tenantID, id uint, status string, actor uint, requestID string) (MemberDetail, error) {
	s.statusArgs = struct {
		tenant, id, actor uint
		status, requestID string
	}{tenant: tenantID, id: id, actor: actor, status: status, requestID: requestID}
	return s.detail, s.statusErr
}

func (s *fakeMemberService) Export(_ context.Context, tenantID uint, _ MemberListQuery, reason string, _ uint, _ string, _ string, sensitive bool) ([]MemberRecord, error) {
	s.exportArgs = struct {
		tenant    uint
		reason    string
		sensitive bool
	}{tenant: tenantID, reason: reason, sensitive: sensitive}
	if s.exportErr != nil {
		return nil, s.exportErr
	}
	return []MemberRecord{{MemberNo: "M-001", DisplayName: "彭程", Phone: "13346516523", PhoneMasked: "133****6523", Status: "active", MembershipStatus: "active", SourceCount: 1}}, nil
}

func invokeMemberController(t *testing.T, method, path string, role string, service MemberService, handler gin.HandlerFunc, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Request-ID", "request-123")
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	ctx.Params = params
	ctx.Set("tenant_id", uint(42))
	ctx.Set("user_id", uint(77))
	ctx.Set("role", role)
	handler(ctx)
	return recorder
}

func testMemberDetail() MemberDetail {
	return MemberDetail{MemberRecord: MemberRecord{ID: 9, DisplayName: "彭程", Phone: "13346516523", Status: "active", MembershipStatus: "active"}, Identities: []MemberIdentitySummary{{Channel: "wechat", Subject: "wx-subject", Status: "active"}}, Authorization: MemberAuthorizationSummary{PhoneVerified: true}, Orders: MemberOrderSummary{CommerceCount: 2}}
}

func TestMemberListUsesAuthenticatedTenantAndMasksPhone(t *testing.T) {
	service := &fakeMemberService{page: MemberPage{Items: []MemberRecord{{ID: 9, DisplayName: "彭程", Phone: "13346516523", Status: "active", MembershipStatus: "active"}}, Page: 2, PageSize: 5, Total: 1}}
	response := invokeMemberController(t, http.MethodGet, "/api/v1/members?page=2&page_size=5&status=active&keyword=%E5%BD%AD&phone=13346516523", "viewer", service, (&MemberController{Service: service}).List, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.listTenant != 42 {
		t.Fatalf("service tenant=%d want 42", service.listTenant)
	}
	var body struct {
		Items []struct {
			Phone            string `json:"phone"`
			MembershipStatus string `json:"membership_status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Phone != "133****6523" {
		t.Fatalf("phone=%q want masked phone", body.Items[0].Phone)
	}
	if body.Items[0].MembershipStatus != "active" {
		t.Fatalf("membership_status=%q want active", body.Items[0].MembershipStatus)
	}
}

func TestMemberSensitiveReadRequiresSeparatePermission(t *testing.T) {
	service := &fakeMemberService{detail: testMemberDetail()}
	ordinary := invokeMemberController(t, http.MethodGet, "/api/v1/members/9", "viewer", service, (&MemberController{Service: service}).Get, gin.Params{{Key: "id", Value: "9"}})
	admin := invokeMemberController(t, http.MethodGet, "/api/v1/members/9", "admin", service, (&MemberController{Service: service}).Get, gin.Params{{Key: "id", Value: "9"}})
	for _, check := range []struct {
		name     string
		response *httptest.ResponseRecorder
		want     string
	}{{"ordinary", ordinary, "133****6523"}, {"admin", admin, "13346516523"}} {
		var body struct {
			Phone string `json:"phone"`
		}
		if err := json.Unmarshal(check.response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Phone != check.want {
			t.Errorf("%s phone=%q want %q", check.name, body.Phone, check.want)
		}
	}
	if service.getTenant != 42 {
		t.Fatalf("detail service tenant=%d want 42", service.getTenant)
	}
}

func TestMemberOrdinaryReadRemasksServiceFallback(t *testing.T) {
	service := &fakeMemberService{page: MemberPage{Items: []MemberRecord{{ID: 9, DisplayName: "彭程", PhoneMasked: "13346516523", Status: "active"}}, Page: 1, PageSize: 20}}
	response := invokeMemberController(t, http.MethodGet, "/api/v1/members?page=2&page_size=5&status=active&keyword=%E5%BD%AD&phone=13346516523", "viewer", service, (&MemberController{Service: service}).List, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Items []struct {
			Phone string `json:"phone"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Phone != "133****6523" {
		t.Fatalf("phone=%q want remasked phone", body.Items[0].Phone)
	}
}

func TestMemberStatusIgnoresBodyOwnershipAndForwardsActorRequestID(t *testing.T) {
	service := &fakeMemberService{detail: testMemberDetail()}
	response := invokeMemberController(t, http.MethodPost, "/api/v1/members/9/freeze", "admin", service, (&MemberController{Service: service}).Freeze, gin.Params{{Key: "id", Value: "9"}})
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := service.statusArgs; got.tenant != 42 || got.id != 9 || got.actor != 77 || got.status != "frozen" || got.requestID != "request-123" {
		t.Fatalf("status args=%+v", got)
	}
}

func TestMemberAuthzIsAdminOnlyUntilServiceRollout(t *testing.T) {
	for _, role := range []string{"product_operator", "team_operator", "settlement_operator", "viewer", "seller", "checker"} {
		if authz.HasTenantPermission(role, authz.PermissionMembersRead) {
			t.Fatalf("role %s unexpectedly has member read permission", role)
		}
	}
	for _, permission := range []string{authz.PermissionMembersRead, authz.PermissionMembersSensitiveRead, authz.PermissionMembersStatusWrite, authz.PermissionMembersExport, authz.PermissionMembersSecurityResolve} {
		if !authz.HasTenantPermission("admin", permission) {
			t.Fatalf("admin missing %s", permission)
		}
	}
}

func TestMemberControllerRejectsInvalidIDAndUnavailableService(t *testing.T) {
	service := &fakeMemberService{}
	invalid := invokeMemberController(t, http.MethodGet, "/api/v1/members/x", "admin", service, (&MemberController{Service: service}).Get, gin.Params{{Key: "id", Value: "x"}})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status=%d want 400", invalid.Code)
	}
	var nilService *MemberController
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/members", nil)
	nilService.List(ctx)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("nil service status=%d want 501", recorder.Code)
	}
}

func TestMemberControllerMapsIdempotencyConflictToConflict(t *testing.T) {
	fake := &fakeMemberService{statusErr: memberservice.ErrMemberIdempotencyConflict}
	response := invokeMemberController(t, http.MethodPost, "/api/v1/members/9/freeze", "admin", fake, (&MemberController{Service: fake}).Freeze, gin.Params{{Key: "id", Value: "9"}})
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409", response.Code, response.Body.String())
	}
}

func TestMemberExportRequiresReasonAndMasksWithoutSensitivePermission(t *testing.T) {
	service := &fakeMemberService{}
	controller := &MemberController{Service: service}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/members/export", strings.NewReader(`{"reason":"月度客户归档"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Request-ID", "export-1")
	ctx.Set("tenant_id", uint(42))
	ctx.Set("user_id", uint(77))
	ctx.Set("role", "viewer")
	controller.Export(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.exportArgs.tenant != 42 || service.exportArgs.reason != "月度客户归档" || service.exportArgs.sensitive {
		t.Fatalf("export args=%+v", service.exportArgs)
	}
	if !strings.Contains(recorder.Body.String(), "133****6523") || strings.Contains(recorder.Body.String(), "13346516523") {
		t.Fatalf("export leaked or omitted masked phone: %s", recorder.Body.String())
	}
}
