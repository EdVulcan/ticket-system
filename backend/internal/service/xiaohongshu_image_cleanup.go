package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ticket-backend/internal/model"

	"gorm.io/gorm"
)

const (
	// The cleanup command may retain files longer, but never less than seven
	// days. A file is moved only; it is never permanently deleted.
	XiaohongshuImageCleanupMinimumAge = 7 * 24 * time.Hour
	// This subtree is outside channel-products, the only upload subtree exposed
	// by the public HTTP route.
	XiaohongshuImageCleanupQuarantineDirectory       = ".quarantine/xiaohongshu-image-cleanup"
	xiaohongshuImageCleanupAdvisoryLockKey     int64 = 0x584853494d47434c // XHSIMGCL
)

var xiaohongshuImageCleanupReferencePattern = regexp.MustCompile(
	`(?:api/v1/public/channel-product-images|channel-products)/[0-9]+/[0-9]+/[0-9A-Fa-f]{32}\.(?i:png|jpg)`,
)

var errXiaohongshuImageSymlink = errors.New("xiaohongshu image path contains a symlink")

// XiaohongshuImageCleanupService is a global maintenance operation. It has no
// tenant argument because a file can be protected by another tenant's audit
// history. It does not migrate, seed, or modify database rows.
type XiaohongshuImageCleanupService struct {
	DB              *gorm.DB
	UploadDirectory string
	MinimumAge      time.Duration
	Now             func() time.Time
}

type XiaohongshuImageCleanupFile struct {
	TenantID         uint      `json:"tenant_id"`
	ChannelAccountID uint      `json:"channel_account_id"`
	RelativePath     string    `json:"relative_path"`
	QuarantinePath   string    `json:"quarantine_path"`
	Size             int64     `json:"size"`
	ModifiedAt       time.Time `json:"modified_at"`
}

type XiaohongshuImageCleanupResult struct {
	Apply           bool                          `json:"apply"`
	UploadDirectory string                        `json:"upload_directory"`
	QuarantineDir   string                        `json:"quarantine_directory"`
	MinimumAge      string                        `json:"minimum_age"`
	CutoffAt        time.Time                     `json:"cutoff_at"`
	Scanned         int                           `json:"scanned"`
	Recent          int                           `json:"recent"`
	Referenced      int                           `json:"referenced"`
	Eligible        int                           `json:"eligible"`
	Moved           int                           `json:"moved"`
	Skipped         int                           `json:"skipped"`
	Files           []XiaohongshuImageCleanupFile `json:"files"`
}

func NewXiaohongshuImageCleanupService(db *gorm.DB, uploadDirectory string) XiaohongshuImageCleanupService {
	return XiaohongshuImageCleanupService{DB: db, UploadDirectory: uploadDirectory}
}

// Run defaults to a read-only preview. With apply=true it locks all reference
// tables, rescans them and the filesystem, and then moves eligible files into
// the recoverable quarantine subtree before committing the short transaction.
func (s XiaohongshuImageCleanupService) Run(ctx context.Context, apply bool) (*XiaohongshuImageCleanupResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	minimumAge := s.MinimumAge
	if minimumAge == 0 {
		minimumAge = XiaohongshuImageCleanupMinimumAge
	}
	root, err := s.uploadRoot()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	result := &XiaohongshuImageCleanupResult{
		Apply:           apply,
		UploadDirectory: root,
		QuarantineDir:   filepath.ToSlash(XiaohongshuImageCleanupQuarantineDirectory),
		MinimumAge:      minimumAge.String(),
		CutoffAt:        now.Add(-minimumAge),
		Files:           []XiaohongshuImageCleanupFile{},
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !apply {
		plan, err := s.plan(ctx, s.DB, root, now, minimumAge)
		if err != nil {
			return nil, err
		}
		return cleanupResult(result, plan), nil
	}
	return s.apply(ctx, root, now, minimumAge, result)
}

func (s XiaohongshuImageCleanupService) validate() error {
	if s.DB == nil {
		return errors.New("xiaohongshu image cleanup database is required")
	}
	if s.DB.Dialector == nil || s.DB.Dialector.Name() != "postgres" {
		return errors.New("xiaohongshu image cleanup requires PostgreSQL")
	}
	if strings.TrimSpace(s.UploadDirectory) == "" {
		return errors.New("xiaohongshu image cleanup upload directory is required")
	}
	if s.MinimumAge < 0 || (s.MinimumAge > 0 && s.MinimumAge < XiaohongshuImageCleanupMinimumAge) {
		return fmt.Errorf("minimum image age must be at least %s", XiaohongshuImageCleanupMinimumAge)
	}
	return nil
}

func (s XiaohongshuImageCleanupService) uploadRoot() (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(s.UploadDirectory))
	if err != nil {
		return "", fmt.Errorf("resolve upload directory: %w", err)
	}
	// The deployment keeps backend/data as a symlink to persistent storage;
	// resolve that configured root, while nested upload symlinks are rejected.
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve upload directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect upload directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("configured upload directory is not a directory")
	}
	return filepath.Clean(root), nil
}

type xiaohongshuImageCleanupCandidate struct {
	XiaohongshuImageCleanupFile
	source      string
	destination string
	oldEnough   bool
}

type xiaohongshuImageCleanupPlan struct {
	files      []xiaohongshuImageCleanupCandidate
	references map[string]struct{}
	scanned    int
	recent     int
	skipped    int
}

func (s XiaohongshuImageCleanupService) plan(ctx context.Context, db *gorm.DB, root string, now time.Time, minimumAge time.Duration) (xiaohongshuImageCleanupPlan, error) {
	plan, err := scanXiaohongshuImageUploads(ctx, root, now.Add(-minimumAge))
	if err != nil {
		return plan, err
	}
	plan.references, err = s.collectReferences(ctx, db)
	if err != nil {
		return plan, err
	}
	return plan, nil
}

func cleanupResult(result *XiaohongshuImageCleanupResult, plan xiaohongshuImageCleanupPlan) *XiaohongshuImageCleanupResult {
	result.Scanned, result.Recent, result.Skipped = plan.scanned, plan.recent, plan.skipped
	for _, candidate := range plan.files {
		if !candidate.oldEnough {
			continue
		}
		if _, referenced := plan.references[candidate.RelativePath]; referenced {
			result.Referenced++
			continue
		}
		result.Eligible++
		result.Files = append(result.Files, candidate.XiaohongshuImageCleanupFile)
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].RelativePath < result.Files[j].RelativePath })
	return result
}

func (s XiaohongshuImageCleanupService) apply(ctx context.Context, root string, now time.Time, minimumAge time.Duration, result *XiaohongshuImageCleanupResult) (*XiaohongshuImageCleanupResult, error) {
	tx := s.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("begin image cleanup transaction: %w", tx.Error)
	}
	moved := []xiaohongshuImageCleanupCandidate{}
	finish := func(err error) (*XiaohongshuImageCleanupResult, error) {
		if err != nil {
			restoreErr := restoreXiaohongshuImageMoves(moved, root)
			_ = tx.Rollback().Error
			if restoreErr != nil {
				return nil, errors.Join(err, fmt.Errorf("restore moved images after failure: %w", restoreErr))
			}
			return nil, err
		}
		if err := tx.Commit().Error; err != nil {
			restoreErr := restoreXiaohongshuImageMoves(moved, root)
			if restoreErr != nil {
				return nil, errors.Join(fmt.Errorf("commit image cleanup transaction: %w", err), fmt.Errorf("restore moved images after commit failure: %w", restoreErr))
			}
			return nil, fmt.Errorf("commit image cleanup transaction: %w", err)
		}
		result.Moved = len(moved)
		return result, nil
	}

	if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
		return finish(fmt.Errorf("set image cleanup lock timeout: %w", err))
	}
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", xiaohongshuImageCleanupAdvisoryLockKey).Error; err != nil {
		return finish(fmt.Errorf("lock image cleanup operation: %w", err))
	}
	// Keep this order stable and aligned with normal storefront/product writes
	// before their audit append, avoiding lock-order deadlocks.
	for _, table := range []string{"channel_accounts", "xiaohongshu_product_configs", "audit_logs"} {
		if err := tx.Exec("LOCK TABLE " + table + " IN SHARE MODE").Error; err != nil {
			return finish(fmt.Errorf("lock image reference table %s: %w", table, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	plan, err := s.plan(ctx, tx, root, now, minimumAge)
	if err != nil {
		return finish(err)
	}
	cleanupResult(result, plan)
	eligible := make([]xiaohongshuImageCleanupCandidate, 0, result.Eligible)
	for _, candidate := range plan.files {
		if candidate.oldEnough {
			if _, referenced := plan.references[candidate.RelativePath]; !referenced {
				eligible = append(eligible, candidate)
			}
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].RelativePath < eligible[j].RelativePath })
	for _, candidate := range eligible {
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		if err := ensureXiaohongshuImageDestination(root, candidate); err != nil {
			return finish(err)
		}
		ok, err := moveXiaohongshuImage(candidate, root, result.CutoffAt)
		if err != nil {
			return finish(err)
		}
		if ok {
			moved = append(moved, candidate)
		}
	}
	return finish(nil)
}

func scanXiaohongshuImageUploads(ctx context.Context, root string, cutoff time.Time) (xiaohongshuImageCleanupPlan, error) {
	plan := xiaohongshuImageCleanupPlan{files: []xiaohongshuImageCleanupCandidate{}}
	channelRoot := filepath.Join(root, "channel-products")
	channelInfo, err := os.Lstat(channelRoot)
	if os.IsNotExist(err) {
		return plan, nil
	}
	if err != nil {
		return plan, fmt.Errorf("inspect channel product image directory: %w", err)
	}
	if channelInfo.Mode()&os.ModeSymlink != 0 || !channelInfo.IsDir() {
		plan.skipped++
		return plan, nil
	}
	err = filepath.WalkDir(channelRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current == channelRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			plan.skipped++
			return nil
		}
		relativeToRoot, err := filepath.Rel(channelRoot, current)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relativeToRoot), "/")
		switch len(parts) {
		case 1:
			if _, ok := canonicalImageID(parts[0]); !ok || !entry.IsDir() {
				plan.skipped++
				if entry.IsDir() {
					return fs.SkipDir
				}
			}
		case 2:
			if _, ok := canonicalImageID(parts[1]); !ok || !entry.IsDir() {
				plan.skipped++
				if entry.IsDir() {
					return fs.SkipDir
				}
			}
		case 3:
			filename := parts[2]
			if entry.IsDir() || !validXiaohongshuImageFilename(filename) {
				plan.skipped++
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				plan.skipped++
				return nil
			}
			tenantID, _ := canonicalImageID(parts[0])
			accountID, _ := canonicalImageID(parts[1])
			pathFromRoot := filepath.ToSlash(filepath.Join("channel-products", parts[0], parts[1], filename))
			quarantine := filepath.ToSlash(filepath.Join(XiaohongshuImageCleanupQuarantineDirectory, parts[0], parts[1], filename))
			candidate := xiaohongshuImageCleanupCandidate{
				XiaohongshuImageCleanupFile: XiaohongshuImageCleanupFile{
					TenantID: tenantID, ChannelAccountID: accountID, RelativePath: pathFromRoot,
					QuarantinePath: quarantine, Size: info.Size(), ModifiedAt: info.ModTime(),
				},
				source: current, destination: filepath.Join(root, filepath.FromSlash(quarantine)),
				oldEnough: !info.ModTime().After(cutoff),
			}
			plan.files = append(plan.files, candidate)
			plan.scanned++
			if !candidate.oldEnough {
				plan.recent++
			}
		default:
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return plan, fmt.Errorf("scan channel product image directory: %w", err)
	}
	return plan, nil
}

func canonicalImageID(value string) (uint, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return uint(parsed), true
}

func validXiaohongshuImageFilename(filename string) bool {
	extension := strings.ToLower(filepath.Ext(filename))
	if extension != ".png" && extension != ".jpg" {
		return false
	}
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	if len(stem) != 32 {
		return false
	}
	_, err := hex.DecodeString(stem)
	return err == nil
}

func (s XiaohongshuImageCleanupService) collectReferences(ctx context.Context, db *gorm.DB) (map[string]struct{}, error) {
	references := make(map[string]struct{})
	var accounts []struct {
		ImageURL string `gorm:"column:storefront_image_url"`
	}
	if err := db.WithContext(ctx).Unscoped().Model(&model.ChannelAccount{}).Select("storefront_image_url").Find(&accounts).Error; err != nil {
		return nil, fmt.Errorf("read current storefront image references: %w", err)
	}
	for _, row := range accounts {
		addXiaohongshuReferences(references, row.ImageURL, false)
	}
	var configs []struct {
		ImageURL string `gorm:"column:image_url"`
	}
	if err := db.WithContext(ctx).Unscoped().Model(&model.XiaohongshuProductConfig{}).Select("image_url").Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("read current product image references: %w", err)
	}
	for _, row := range configs {
		addXiaohongshuReferences(references, row.ImageURL, false)
	}
	var audits []struct {
		BeforeJSON string `gorm:"column:before_json"`
		AfterJSON  string `gorm:"column:after_json"`
	}
	if err := db.WithContext(ctx).Unscoped().Model(&model.AuditLog{}).Select("before_json", "after_json").Find(&audits).Error; err != nil {
		return nil, fmt.Errorf("read historical image references: %w", err)
	}
	for _, row := range audits {
		addXiaohongshuReferences(references, row.BeforeJSON, true)
		addXiaohongshuReferences(references, row.AfterJSON, true)
	}
	return references, nil
}

func addXiaohongshuReferences(references map[string]struct{}, raw string, decodeJSON bool) {
	if decodeJSON {
		var decoded interface{}
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			walkXiaohongshuAuditJSON(references, decoded)
		}
	}
	for _, match := range xiaohongshuImageCleanupReferencePattern.FindAllString(strings.ReplaceAll(raw, `\/`, "/"), -1) {
		addParsedXiaohongshuImageReference(references, match)
	}
	addParsedXiaohongshuImageReference(references, raw)
}

func addParsedXiaohongshuImageReference(references map[string]struct{}, raw string) {
	if relative, ok := xiaohongshuImageRelativePath(raw); ok {
		references[relative] = struct{}{}
	}
}

func walkXiaohongshuAuditJSON(references map[string]struct{}, value interface{}) {
	switch typed := value.(type) {
	case string:
		addXiaohongshuReferences(references, typed, false)
	case []interface{}:
		for _, item := range typed {
			walkXiaohongshuAuditJSON(references, item)
		}
	case map[string]interface{}:
		for _, item := range typed {
			walkXiaohongshuAuditJSON(references, item)
		}
	}
}

func xiaohongshuImageRelativePath(raw string) (string, bool) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, `\/`, "/"))
	if value == "" {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	pathValue := parsed.Path
	if pathValue == "" {
		pathValue = value
	}
	pathValue = strings.TrimPrefix(pathValue, "/")
	for _, prefix := range []string{"api/v1/public/channel-product-images/", "channel-products/"} {
		if !strings.HasPrefix(pathValue, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(pathValue, prefix), "/")
		if len(parts) != 3 {
			return "", false
		}
		tenant, ok := canonicalImageID(parts[0])
		if !ok {
			return "", false
		}
		account, ok := canonicalImageID(parts[1])
		if !ok || !validXiaohongshuImageFilename(parts[2]) {
			return "", false
		}
		return filepath.ToSlash(filepath.Join("channel-products", strconv.FormatUint(uint64(tenant), 10), strconv.FormatUint(uint64(account), 10), parts[2])), true
	}
	return "", false
}

func ensureXiaohongshuImageDestination(root string, candidate xiaohongshuImageCleanupCandidate) error {
	parent := filepath.Dir(filepath.FromSlash(candidate.QuarantinePath))
	if err := ensureXiaohongshuImageDirectory(root, parent); err != nil {
		return err
	}
	if info, err := os.Lstat(candidate.destination); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("quarantine destination is a symlink: %s", candidate.QuarantinePath)
		}
		return fmt.Errorf("quarantine destination already exists: %s", candidate.QuarantinePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect quarantine destination %s: %w", candidate.QuarantinePath, err)
	}
	return nil
}

func ensureXiaohongshuImageDirectory(root, relative string) error {
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) {
		return errors.New("invalid image quarantine directory")
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid image quarantine directory")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0750); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create image quarantine directory: %w", err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect image quarantine directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("image quarantine directory component is unsafe: %s", filepath.ToSlash(clean))
		}
	}
	return nil
}

func moveXiaohongshuImage(candidate xiaohongshuImageCleanupCandidate, root string, cutoff time.Time) (bool, error) {
	info, err := lstatXiaohongshuImagePath(root, candidate.RelativePath)
	if errors.Is(err, errXiaohongshuImageSymlink) || os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
		return false, nil
	}
	if err := os.Rename(candidate.source, candidate.destination); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("move image %s to quarantine: %w", candidate.RelativePath, err)
	}
	return true, nil
}

func lstatXiaohongshuImagePath(root, relative string) (os.FileInfo, error) {
	clean := filepath.ToSlash(filepath.Clean(relative))
	if filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, errors.New("invalid image path")
	}
	current := root
	var info os.FileInfo
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("invalid image path")
		}
		current = filepath.Join(current, part)
		var err error
		info, err = os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errXiaohongshuImageSymlink
		}
	}
	return info, nil
}

func restoreXiaohongshuImageMoves(moved []xiaohongshuImageCleanupCandidate, root string) error {
	var restoreErrors []error
	for index := len(moved) - 1; index >= 0; index-- {
		candidate := moved[index]
		if _, err := lstatXiaohongshuImagePath(root, candidate.QuarantinePath); err != nil {
			switch {
			case errors.Is(err, errXiaohongshuImageSymlink):
				restoreErrors = append(restoreErrors, fmt.Errorf("quarantine path became a symlink: %s", candidate.QuarantinePath))
			case os.IsNotExist(err):
				restoreErrors = append(restoreErrors, fmt.Errorf("quarantine file is missing: %s", candidate.QuarantinePath))
			default:
				restoreErrors = append(restoreErrors, err)
			}
			continue
		}
		parent := filepath.Dir(filepath.FromSlash(candidate.RelativePath))
		if info, err := lstatXiaohongshuImagePath(root, parent); err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("image source parent is not a directory")
			}
			restoreErrors = append(restoreErrors, fmt.Errorf("restore image %s: %w", candidate.RelativePath, err))
			continue
		}
		if _, err := os.Lstat(candidate.source); err == nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("cannot restore image because source exists: %s", candidate.RelativePath))
			continue
		} else if !os.IsNotExist(err) {
			restoreErrors = append(restoreErrors, err)
			continue
		}
		if err := os.Rename(candidate.destination, candidate.source); err != nil && !os.IsNotExist(err) {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore image %s: %w", candidate.RelativePath, err))
		}
	}
	return errors.Join(restoreErrors...)
}
