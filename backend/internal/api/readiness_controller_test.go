package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"

	"github.com/gin-gonic/gin"
)

func TestReadinessRequiresCurrentSchemaAndExposesRelease(t *testing.T) {
	previous := model.DB
	t.Cleanup(func() { model.DB = previous })
	model.DB = testdb.Open(t)
	if err := model.DB.AutoMigrate(&model.SchemaMigration{}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/ready", Readiness)
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/ready", nil))
		return w
	}
	if w := get(); w.Code != 503 {
		t.Fatalf("empty schema=%d", w.Code)
	}
	if err := model.DB.Create(&model.SchemaMigration{Version: model.CurrentPostgresSchemaVersion, Name: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	w := get()
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ready=%d", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["revision"] != ReleaseRevision || body["schema_version"] != float64(model.CurrentPostgresSchemaVersion) {
		t.Fatalf("body=%v", body)
	}
	model.DB = nil
	if w := get(); w.Code != 503 {
		t.Fatalf("missing database=%d", w.Code)
	}
}
