package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func teamGroupQueryContext(target string) *gin.Context {
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest("GET", target, nil)
	return ctx
}

func TestParseTeamVisitDateAcceptsDateOnlyAndRFC3339(t *testing.T) {
	for _, input := range []string{"2026-08-18", "2026-08-18T00:00:00+08:00"} {
		parsed, err := parseTeamVisitDate(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		if parsed.Location() != time.UTC || parsed.Format("2006-01-02 15:04:05") != "2026-08-18 00:00:00" {
			t.Fatalf("parse %q = %v, want UTC date boundary", input, parsed)
		}
	}
	for _, input := range []string{"", "2026/08/18", "2026-02-30"} {
		if _, err := parseTeamVisitDate(input); err == nil {
			t.Fatalf("invalid team visit date was accepted: %q", input)
		}
	}
}

func TestParseTeamGroupListOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := teamGroupQueryContext("/api/v1/team/groups?page=2&page_size=40&keyword=TEAM-88&status=confirmed&visit_start=2026-08-01&visit_end=2026-08-31")
	options, err := parseTeamGroupListOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if options.Page != 2 || options.PageSize != 40 || options.Keyword != "TEAM-88" || options.Status != "confirmed" {
		t.Fatalf("unexpected options: %+v", options)
	}
	if options.VisitStart == nil || options.VisitStart.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("unexpected visit_start: %+v", options.VisitStart)
	}
	if options.VisitEnd == nil || options.VisitEnd.Format("2006-01-02") != "2026-08-31" {
		t.Fatalf("unexpected visit_end: %+v", options.VisitEnd)
	}
}

func TestParseTeamGroupListOptionsNormalizesPaginationAndRejectsInvalidRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := teamGroupQueryContext("/api/v1/team/groups?page=0&page_size=500")
	options, err := parseTeamGroupListOptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if options.Page != 1 || options.PageSize != 100 {
		t.Fatalf("pagination was not normalized: %+v", options)
	}

	for _, target := range []string{
		"/api/v1/team/groups?visit_start=2026/08/01",
		"/api/v1/team/groups?visit_end=not-a-date",
		"/api/v1/team/groups?visit_start=2026-08-31&visit_end=2026-08-01",
	} {
		if _, err := parseTeamGroupListOptions(teamGroupQueryContext(target)); err == nil {
			t.Fatalf("invalid query was accepted: %s", target)
		}
	}
}
