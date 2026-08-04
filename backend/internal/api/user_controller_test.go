package api

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/testdb"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func TestRestoredTenantAdministratorRevokesOldTokenVersion(t *testing.T) {
	db := testdb.Open(t)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	model.DB = db
	model.InitWriter(db, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = model.CloseWriter(ctx)
		model.DB = nil
	})

	user := model.User{TenantID: 7, Username: "restored-admin", Password: "old-password", Role: "admin", TokenVersion: 3}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&user).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(`{"username":"restored-admin","password":"new-password","role":"admin"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request
	ctx.Set("tenant_id", uint(7))
	(&UserController{}).Create(ctx)
	if response.Code != 201 {
		t.Fatalf("restore response=%d body=%s", response.Code, response.Body.String())
	}

	var restored model.User
	if err := db.Unscoped().First(&restored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restored.DeletedAt.Valid || restored.TokenVersion != 4 {
		t.Fatalf("restored account deleted=%v token_version=%d", restored.DeletedAt.Valid, restored.TokenVersion)
	}
	if cost, err := bcrypt.Cost([]byte(restored.Password)); err != nil || cost != 12 {
		t.Fatalf("restored password bcrypt cost=%d err=%v", cost, err)
	}
}
