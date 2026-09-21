package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ticket-backend/internal/authz"
	"ticket-backend/internal/service"

	"github.com/gin-gonic/gin"
)

// MemberService is the API boundary for the tenant membership center. The
// implementation belongs in the service/model layer; controllers must never
// query member or order tables directly. TenantID is always supplied by the
// authenticated request context, not by a request body or query parameter.
type MemberService interface {
	List(context.Context, uint, MemberListQuery) (MemberPage, error)
	Get(context.Context, uint, uint) (MemberDetail, error)
	SetStatus(context.Context, uint, uint, string, uint, string) (MemberDetail, error)
}

var (
	ErrMemberNotFound    = errors.New("member not found")
	ErrMemberInvalid     = errors.New("invalid member request")
	ErrMemberConflict    = errors.New("member state conflict")
	ErrMemberUnavailable = errors.New("member service unavailable")
)

type MemberListQuery struct {
	Page     int
	PageSize int
	Status   string
	Keyword  string
	// Phone is normalized and blind-indexed by MemberService. The controller
	// deliberately passes it through without logging or hashing with SHA-256.
	Phone string
}

type MemberPage struct {
	Items    []MemberRecord `json:"items"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Total    int64          `json:"total"`
	HasNext  bool           `json:"has_next"`
}

type MemberRecord struct {
	ID               uint       `json:"id"`
	DisplayName      string     `json:"display_name"`
	Phone            string     `json:"-"`
	PhoneMasked      string     `json:"-"`
	Status           string     `json:"status"`
	MembershipStatus string     `json:"membership_status"`
	SourceCount      int        `json:"source_count"`
	CreatedAt        time.Time  `json:"created_at"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
}

type MemberIdentitySummary struct {
	Channel string `json:"channel"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

type MemberAuthorizationSummary struct {
	PhoneVerified bool `json:"phone_verified"`
	Consented     bool `json:"consented"`
}

type MemberOrderSummary struct {
	TicketCount   int64 `json:"ticket_count"`
	HotelCount    int64 `json:"hotel_count"`
	CommerceCount int64 `json:"commerce_count"`
}

type MemberDetail struct {
	MemberRecord
	Identities    []MemberIdentitySummary    `json:"identities"`
	Authorization MemberAuthorizationSummary `json:"authorization"`
	Orders        MemberOrderSummary         `json:"orders"`
}

// MemberController exposes the tenant-admin read/status boundary. It is
// deliberately dependency-injected so the router cannot silently construct a
// controller that writes through an unscoped global database handle.
type MemberController struct {
	Service MemberService
}

// MemberServiceAdapter keeps the HTTP contract independent from the domain
// service DTOs. The adapter is the only place where service-layer records are
// projected into the response boundary, so controllers never need a database
// handle or service implementation details.
type MemberServiceAdapter struct {
	Service *service.MemberService
}

func NewMemberServiceAdapter(memberService *service.MemberService) *MemberServiceAdapter {
	if memberService == nil {
		return nil
	}
	return &MemberServiceAdapter{Service: memberService}
}

func (a *MemberServiceAdapter) List(ctx context.Context, tenantID uint, query MemberListQuery) (MemberPage, error) {
	if a == nil || a.Service == nil {
		return MemberPage{}, ErrMemberUnavailable
	}
	page, err := a.Service.ListMembers(ctx, tenantID, service.MemberAdminListQuery{
		Page: query.Page, PageSize: query.PageSize, Status: query.Status, Keyword: query.Keyword, Phone: query.Phone,
	})
	if err != nil {
		return MemberPage{}, mapMemberServiceError(err)
	}
	items := make([]MemberRecord, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, memberRecordFromService(item))
	}
	return MemberPage{Items: items, Page: page.Page, PageSize: page.PageSize, Total: page.Total, HasNext: page.HasNext}, nil
}

func (a *MemberServiceAdapter) Get(ctx context.Context, tenantID, memberID uint) (MemberDetail, error) {
	if a == nil || a.Service == nil {
		return MemberDetail{}, ErrMemberUnavailable
	}
	detail, err := a.Service.GetMember(ctx, tenantID, memberID)
	if err != nil {
		return MemberDetail{}, mapMemberServiceError(err)
	}
	identities := make([]MemberIdentitySummary, 0, len(detail.Identities))
	for _, identity := range detail.Identities {
		identities = append(identities, MemberIdentitySummary{Channel: identity.Channel, Subject: identity.Subject, Status: identity.Status})
	}
	return MemberDetail{
		MemberRecord: memberRecordFromService(detail.MemberAdminRecord),
		Identities:   identities,
		Authorization: MemberAuthorizationSummary{
			PhoneVerified: detail.Authorization.PhoneVerified,
			Consented:     detail.Authorization.Consented,
		},
		Orders: MemberOrderSummary{
			TicketCount:   detail.Orders.TicketCount,
			HotelCount:    detail.Orders.HotelCount,
			CommerceCount: detail.Orders.CommerceCount,
		},
	}, nil
}

func (a *MemberServiceAdapter) SetStatus(ctx context.Context, tenantID, memberID uint, status string, actorID uint, requestID string) (MemberDetail, error) {
	if a == nil || a.Service == nil {
		return MemberDetail{}, ErrMemberUnavailable
	}
	actorRole, _ := ctx.Value(memberActorRoleContextKey{}).(string)
	detail, err := a.Service.SetMemberStatus(ctx, tenantID, memberID, status, actorID, actorRole, requestID)
	if err != nil {
		return MemberDetail{}, mapMemberServiceError(err)
	}
	identities := make([]MemberIdentitySummary, 0, len(detail.Identities))
	for _, identity := range detail.Identities {
		identities = append(identities, MemberIdentitySummary{Channel: identity.Channel, Subject: identity.Subject, Status: identity.Status})
	}
	return MemberDetail{
		MemberRecord: memberRecordFromService(detail.MemberAdminRecord),
		Identities:   identities,
		Authorization: MemberAuthorizationSummary{
			PhoneVerified: detail.Authorization.PhoneVerified,
			Consented:     detail.Authorization.Consented,
		},
		Orders: MemberOrderSummary{
			TicketCount:   detail.Orders.TicketCount,
			HotelCount:    detail.Orders.HotelCount,
			CommerceCount: detail.Orders.CommerceCount,
		},
	}, nil
}

type memberActorRoleContextKey struct{}

func memberRecordFromService(record service.MemberAdminRecord) MemberRecord {
	return MemberRecord{ID: record.ID, DisplayName: record.DisplayName, Phone: record.Phone, PhoneMasked: record.PhoneMasked, Status: record.Status, MembershipStatus: record.MembershipStatus, SourceCount: record.SourceCount, CreatedAt: record.CreatedAt, LastSeenAt: record.LastSeenAt}
}

func mapMemberServiceError(err error) error {
	switch {
	case errors.Is(err, service.ErrMemberInvalidInput):
		return ErrMemberInvalid
	case errors.Is(err, service.ErrMemberNotFound):
		return ErrMemberNotFound
	case errors.Is(err, service.ErrMemberLifecycle):
		return ErrMemberConflict
	case errors.Is(err, service.ErrMemberIdempotencyConflict):
		return ErrMemberConflict
	case errors.Is(err, service.ErrMemberTenantUnavailable):
		return ErrMemberUnavailable
	default:
		return err
	}
}

func (c *MemberController) List(ctx *gin.Context) {
	if c == nil || c.Service == nil {
		memberError(ctx, ErrMemberUnavailable)
		return
	}
	query, err := parseMemberListQuery(ctx)
	if err != nil {
		memberError(ctx, err)
		return
	}
	page, err := c.Service.List(ctx.Request.Context(), ctx.GetUint("tenant_id"), query)
	if err != nil {
		memberError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"items": pageViews(page.Items, canReadSensitiveMember(ctx)),
		"page":  page.Page, "page_size": page.PageSize, "total": page.Total, "has_next": page.HasNext,
	})
}

func (c *MemberController) Get(ctx *gin.Context) {
	if c == nil || c.Service == nil {
		memberError(ctx, ErrMemberUnavailable)
		return
	}
	id, err := memberIDParam(ctx)
	if err != nil {
		memberError(ctx, err)
		return
	}
	detail, err := c.Service.Get(ctx.Request.Context(), ctx.GetUint("tenant_id"), id)
	if err != nil {
		memberError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, memberDetailView(detail, canReadSensitiveMember(ctx)))
}

func (c *MemberController) Freeze(ctx *gin.Context) {
	c.setStatus(ctx, "frozen")
}

func (c *MemberController) Unfreeze(ctx *gin.Context) {
	c.setStatus(ctx, "active")
}

func (c *MemberController) setStatus(ctx *gin.Context, status string) {
	if c == nil || c.Service == nil {
		memberError(ctx, ErrMemberUnavailable)
		return
	}
	id, err := memberIDParam(ctx)
	if err != nil {
		memberError(ctx, err)
		return
	}
	// The actor and idempotency key are taken from authenticated/request
	// metadata. Any JSON body, including tenant_id or member_id, is ignored.
	requestContext := context.WithValue(ctx.Request.Context(), memberActorRoleContextKey{}, ctx.GetString("role"))
	detail, err := c.Service.SetStatus(requestContext, ctx.GetUint("tenant_id"), id, status, ctx.GetUint("user_id"), requestID(ctx))
	if err != nil {
		memberError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, memberDetailView(detail, canReadSensitiveMember(ctx)))
}

// RegisterMemberRoutes is intentionally separate from InitRouter. Until a
// concrete MemberService is wired by the application bootstrap, exposing
// these routes would create an always-failing production endpoint.
func RegisterMemberRoutes(group *gin.RouterGroup, controller *MemberController) {
	if group == nil || controller == nil {
		return
	}
	group.GET("", controller.List)
	group.GET("/:id", controller.Get)
	group.POST("/:id/freeze", controller.Freeze)
	group.POST("/:id/unfreeze", controller.Unfreeze)
}

func parseMemberListQuery(ctx *gin.Context) (MemberListQuery, error) {
	query := MemberListQuery{Page: 1, PageSize: 20, Status: strings.TrimSpace(ctx.Query("status")), Keyword: strings.TrimSpace(ctx.Query("keyword")), Phone: ctx.Query("phone")}
	if raw := ctx.Query("page"); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 {
			return MemberListQuery{}, ErrMemberInvalid
		}
		query.Page = page
	}
	if raw := ctx.Query("page_size"); raw != "" {
		pageSize, err := strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return MemberListQuery{}, ErrMemberInvalid
		}
		query.PageSize = pageSize
	}
	if len(query.Status) > 32 || len(query.Keyword) > 128 || len(query.Phone) > 64 {
		return MemberListQuery{}, ErrMemberInvalid
	}
	return query, nil
}

func memberIDParam(ctx *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 || id > uint64(^uint(0)) {
		return 0, ErrMemberInvalid
	}
	return uint(id), nil
}

func requestID(ctx *gin.Context) string {
	return strings.TrimSpace(ctx.GetHeader("X-Request-ID"))
}

func canReadSensitiveMember(ctx *gin.Context) bool {
	return authz.HasTenantPermission(ctx.GetString("role"), authz.PermissionMembersSensitiveRead)
}

type memberView struct {
	ID               uint       `json:"id"`
	DisplayName      string     `json:"display_name"`
	Phone            string     `json:"phone"`
	Status           string     `json:"status"`
	MembershipStatus string     `json:"membership_status"`
	SourceCount      int        `json:"source_count"`
	CreatedAt        time.Time  `json:"created_at"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
}

func pageViews(records []MemberRecord, sensitive bool) []memberView {
	views := make([]memberView, 0, len(records))
	for _, record := range records {
		views = append(views, memberRecordView(record, sensitive))
	}
	return views
}

func memberRecordView(record MemberRecord, sensitive bool) memberView {
	phone := ""
	if sensitive {
		phone = record.Phone
	} else {
		// Never trust a service-provided display field for authorization. The
		// controller applies masking at the response boundary even if a future
		// repository accidentally returns an unmasked PhoneMasked value.
		phone = maskMemberPhone(record.Phone)
		if phone == "" {
			phone = maskMemberPhone(record.PhoneMasked)
		}
	}
	return memberView{ID: record.ID, DisplayName: record.DisplayName, Phone: phone, Status: record.Status, MembershipStatus: record.MembershipStatus, SourceCount: record.SourceCount, CreatedAt: record.CreatedAt, LastSeenAt: record.LastSeenAt}
}

type memberDetailViewResponse struct {
	memberView
	Identities    []MemberIdentitySummary    `json:"identities"`
	Authorization MemberAuthorizationSummary `json:"authorization"`
	Orders        MemberOrderSummary         `json:"orders"`
}

func memberDetailView(detail MemberDetail, sensitive bool) memberDetailViewResponse {
	return memberDetailViewResponse{memberView: memberRecordView(detail.MemberRecord, sensitive), Identities: detail.Identities, Authorization: detail.Authorization, Orders: detail.Orders}
}

func maskMemberPhone(phone string) string {
	digits := []rune(strings.TrimSpace(phone))
	if len(digits) < 7 {
		return ""
	}
	end := len(digits) - 4
	if end <= 3 {
		digits[3] = '*'
		return string(digits)
	}
	for i := 3; i < end; i++ {
		if digits[i] >= '0' && digits[i] <= '9' {
			digits[i] = '*'
		}
	}
	return string(digits)
}

func memberError(ctx *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "会员服务处理失败"
	switch {
	case errors.Is(err, ErrMemberInvalid):
		status, message = http.StatusBadRequest, "会员请求参数不正确"
	case errors.Is(err, ErrMemberNotFound):
		status, message = http.StatusNotFound, "会员不存在"
	case errors.Is(err, ErrMemberConflict):
		status, message = http.StatusConflict, "会员状态冲突"
	case errors.Is(err, ErrMemberUnavailable):
		status, message = http.StatusNotImplemented, "会员服务尚未配置"
	case errors.Is(err, service.ErrMemberNotFound):
		status, message = http.StatusNotFound, "会员不存在"
	case errors.Is(err, service.ErrMemberLifecycle), errors.Is(err, service.ErrMemberPhoneConflict), errors.Is(err, service.ErrMemberIdempotencyConflict):
		status, message = http.StatusConflict, "会员状态冲突"
	case errors.Is(err, service.ErrMemberInvalidInput):
		status, message = http.StatusBadRequest, "会员请求参数不正确"
	}
	ctx.JSON(status, gin.H{"error": message})
}
