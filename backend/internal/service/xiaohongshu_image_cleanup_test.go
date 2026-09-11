package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"time"
)

func TestXiaohongshuImageCleanupPreviewApplyRecoverAndRepeat(t *testing.T) {
	resetBusinessData(t)
	root := t.TempDir()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	orphan := writeCleanupImage(t, root, "1", "2", "0123456789abcdef0123456789abcdef.png", now.Add(-8*24*time.Hour), "orphan")
	service := NewXiaohongshuImageCleanupService(model.DB, root)
	service.Now = func() time.Time { return now }

	preview, err := service.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Apply || preview.Scanned != 1 || preview.Eligible != 1 || preview.Moved != 0 || len(preview.Files) != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("preview changed source: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(XiaohongshuImageCleanupQuarantineDirectory))); !os.IsNotExist(err) {
		t.Fatalf("preview created quarantine directory: %v", err)
	}

	applied, err := service.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Apply || applied.Eligible != 1 || applied.Moved != 1 || len(applied.Files) != 1 {
		t.Fatalf("apply=%+v", applied)
	}
	movedPath := filepath.Join(root, filepath.FromSlash(applied.Files[0].QuarantinePath))
	contents, err := os.ReadFile(movedPath)
	if err != nil || string(contents) != "orphan" {
		t.Fatalf("quarantined image contents=%q err=%v", contents, err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("source still exists after apply: %v", err)
	}

	repeated, err := service.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Moved != 0 || repeated.Scanned != 0 || repeated.Eligible != 0 {
		t.Fatalf("repeat apply=%+v", repeated)
	}

	if err := os.Rename(movedPath, orphan); err != nil {
		t.Fatal(err)
	}
	recovered, err := os.ReadFile(orphan)
	if err != nil || string(recovered) != "orphan" {
		t.Fatalf("recovered image contents=%q err=%v", recovered, err)
	}
}

func TestXiaohongshuImageCleanupProtectsCurrentAndHistoricalReferencesGlobally(t *testing.T) {
	resetBusinessData(t)
	root := t.TempDir()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	currentStorefront := writeCleanupImage(t, root, "1", "2", "11111111111111111111111111111111.jpg", now.Add(-8*24*time.Hour), "storefront")
	currentProduct := writeCleanupImage(t, root, "1", "2", "22222222222222222222222222222222.png", now.Add(-8*24*time.Hour), "product")
	historical := writeCleanupImage(t, root, "1", "2", "33333333333333333333333333333333.jpg", now.Add(-8*24*time.Hour), "historical")
	orphan := writeCleanupImage(t, root, "1", "2", "44444444444444444444444444444444.png", now.Add(-8*24*time.Hour), "orphan")

	firstTenant := createCleanupTenant(t, "cleanup-first")
	account := model.ChannelAccount{TenantID: firstTenant.ID, Code: "cleanup-account", Type: "xiaohongshu", Status: "active", Environment: "sandbox", StorefrontImageURL: "https://old.example.invalid/api/v1/public/channel-product-images/1/2/11111111111111111111111111111111.jpg"}
	if err := model.DB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	mapping := model.ChannelProductMapping{ChannelAccountID: account.ID, ProductID: 999, ExternalCode: "cleanup-product", Status: "active"}
	if err := model.DB.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.XiaohongshuProductConfig{
		TenantID: firstTenant.ID, ChannelAccountID: account.ID, ChannelProductMappingID: mapping.ID,
		ExternalSKUID: "cleanup-sku", CategoryID: "ticket", ImageURL: "https://new.example.invalid/api/v1/public/channel-product-images/1/2/22222222222222222222222222222222.png",
		Description: "cleanup", ProductPath: "/pages/product", OrderPath: "/pages/order", ProductType: 1, SettleType: 1,
		SyncStatus: "pending", AuditStatus: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}
	secondTenant := createCleanupTenant(t, "cleanup-second")
	auditJSON, err := json.Marshal(map[string]string{"historical_image": "https://retired.example.invalid/api/v1/public/channel-product-images/1/2/33333333333333333333333333333333.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.AuditLog{TenantID: secondTenant.ID, Scope: "tenant", Action: "legacy.product.configure", TargetType: "product", TargetID: 77, BeforeJSON: string(auditJSON)}).Error; err != nil {
		t.Fatal(err)
	}

	service := NewXiaohongshuImageCleanupService(model.DB, root)
	service.Now = func() time.Time { return now }
	result, err := service.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 4 || result.Referenced != 3 || result.Eligible != 1 || result.Moved != 1 {
		t.Fatalf("result=%+v", result)
	}
	for _, protected := range []string{currentStorefront, currentProduct, historical} {
		if _, err := os.Stat(protected); err != nil {
			t.Fatalf("protected image moved: %s err=%v", protected, err)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("unreferenced image was not moved: %v", err)
	}
}

func TestXiaohongshuImageCleanupProtectsRecentAndSkipsUnexpectedPaths(t *testing.T) {
	resetBusinessData(t)
	root := t.TempDir()
	now := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	recent := writeCleanupImage(t, root, "1", "2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png", now.Add(-24*time.Hour), "recent")
	invalidExtension := writeCleanupImage(t, root, "1", "2", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.gif", now.Add(-8*24*time.Hour), "gif")
	invalidName := writeCleanupImage(t, root, "1", "2", "short.png", now.Add(-8*24*time.Hour), "short")
	if err := os.MkdirAll(filepath.Join(root, "channel-products", "1", "2", "nested"), 0750); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "channel-products", "1", "2", "nested", "cccccccccccccccccccccccccccccccc.png")
	if err := os.WriteFile(nested, []byte("nested"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nested, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	nonnumeric := writeCleanupImage(t, root, "not-a-tenant", "2", "dddddddddddddddddddddddddddddddd.png", now.Add(-8*24*time.Hour), "non-numeric")

	service := NewXiaohongshuImageCleanupService(model.DB, root)
	service.Now = func() time.Time { return now }
	result, err := service.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Recent != 1 || result.Eligible != 0 || result.Moved != 0 || result.Skipped < 4 {
		t.Fatalf("unexpected-path result=%+v", result)
	}
	for _, untouched := range []string{recent, invalidExtension, invalidName, nested, nonnumeric} {
		if _, err := os.Stat(untouched); err != nil {
			t.Fatalf("unexpected path changed: %s err=%v", untouched, err)
		}
	}

	// A symlink with a legal-looking name must be ignored rather than followed.
	target := filepath.Join(root, "outside.png")
	if err := os.WriteFile(target, []byte("outside"), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "channel-products", "1", "2", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.png")
	if err := os.Symlink(target, link); err == nil {
		defer os.Remove(link)
		second, err := service.Run(context.Background(), true)
		if err != nil {
			t.Fatal(err)
		}
		if second.Moved != 0 {
			t.Fatalf("symlink moved: %+v", second)
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("symlink target changed: %v", err)
		}
	}
}

func createCleanupTenant(t *testing.T, code string) model.Tenant {
	t.Helper()
	tenant := model.Tenant{Name: code, SystemCode: strings.ToUpper(code), SecretKey: "cleanup-secret", Status: "active", QualificationStatus: "approved"}
	if err := model.DB.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	return tenant
}

func writeCleanupImage(t *testing.T, root, tenant, account, filename string, modified time.Time, contents string) string {
	t.Helper()
	directory := filepath.Join(root, "channel-products", tenant, account)
	if err := os.MkdirAll(directory, 0750); err != nil {
		t.Fatal(err)
	}
	filenamePath := filepath.Join(directory, filename)
	if err := os.WriteFile(filenamePath, []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filenamePath, modified, modified); err != nil {
		t.Fatal(err)
	}
	return filenamePath
}

func TestXiaohongshuImageCleanupMinimumAgeCannotBeRelaxed(t *testing.T) {
	resetBusinessData(t)
	service := XiaohongshuImageCleanupService{DB: model.DB, UploadDirectory: t.TempDir(), MinimumAge: time.Hour}
	_, err := service.Run(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("minimum age error=%v", err)
	}
}

func TestXiaohongshuImageCleanupReferencePathIgnoresURLDomain(t *testing.T) {
	filename := fmt.Sprintf("%032x.png", 42)
	for _, raw := range []string{
		"https://old.example.invalid/api/v1/public/channel-product-images/1/2/" + filename,
		"https://new.example.invalid/api/v1/public/channel-product-images/1/2/" + filename + "?cache=1",
		`{"image_url":"https:\/\/retired.example.invalid\/api\/v1\/public\/channel-product-images\/1\/2\/` + filename + `"}`,
	} {
		references := map[string]struct{}{}
		addXiaohongshuReferences(references, raw, true)
		if _, ok := references["channel-products/1/2/"+filename]; !ok {
			t.Fatalf("reference not found for %q: %#v", raw, references)
		}
	}
}
