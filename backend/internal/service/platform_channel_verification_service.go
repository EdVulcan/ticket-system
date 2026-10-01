package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"ticket-backend/internal/config"
	"ticket-backend/internal/model"
	"time"

	"gorm.io/gorm"
)

const MaxChannelVerificationFileBytes = 64 << 10

var (
	ErrVerificationFileNotFound = errors.New("校验文件不存在")
	ErrVerificationFileConflict = errors.New("校验文件名已被其他渠道账号使用")
)

type ChannelVerificationFileView struct {
	ID               uint      `json:"id"`
	TenantID         uint      `json:"tenant_id"`
	TenantName       string    `json:"tenant_name"`
	SystemCode       string    `json:"system_code"`
	ChannelAccountID uint      `json:"channel_account_id"`
	ChannelCode      string    `json:"channel_code"`
	ChannelType      string    `json:"channel_type"`
	AppID            string    `json:"app_id"`
	Kind             string    `json:"kind"`
	Filename         string    `json:"filename"`
	PublicURL        string    `json:"public_url"`
	ContentHash      string    `json:"content_hash"`
	ContentSize      int64     `json:"content_size"`
	UploadedBy       uint      `json:"uploaded_by"`
	UploadedAt       time.Time `json:"uploaded_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ChannelVerificationAccountView struct {
	TenantID         uint   `json:"tenant_id"`
	TenantName       string `json:"tenant_name"`
	SystemCode       string `json:"system_code"`
	ChannelAccountID uint   `json:"channel_account_id"`
	ChannelCode      string `json:"channel_code"`
	ChannelType      string `json:"channel_type"`
	AppID            string `json:"app_id"`
}

type platformChannelVerificationRow struct {
	model.ChannelVerificationFile
	TenantName  string `gorm:"column:tenant_name"`
	SystemCode  string `gorm:"column:system_code"`
	ChannelCode string `gorm:"column:channel_code"`
	ChannelType string `gorm:"column:channel_type"`
	AppID       string `gorm:"column:app_id"`
}

type platformChannelVerificationAccountRow struct {
	TenantID         uint   `gorm:"column:tenant_id"`
	TenantName       string `gorm:"column:tenant_name"`
	SystemCode       string `gorm:"column:system_code"`
	ChannelAccountID uint   `gorm:"column:channel_account_id"`
	ChannelCode      string `gorm:"column:channel_code"`
	ChannelType      string `gorm:"column:channel_type"`
	AppID            string `gorm:"column:app_id"`
}

type PlatformChannelVerificationService struct {
	Directory     string
	PublicBaseURL string
}

func NewPlatformChannelVerificationService() PlatformChannelVerificationService {
	return PlatformChannelVerificationService{
		Directory:     config.GlobalConfig.Server.UploadDirectory,
		PublicBaseURL: config.GlobalConfig.Server.PublicBaseURL,
	}
}

func (s PlatformChannelVerificationService) Accounts() ([]ChannelVerificationAccountView, error) {
	var rows []platformChannelVerificationAccountRow
	err := model.DB.Table("channel_accounts AS account").
		Select("account.tenant_id, tenant.name AS tenant_name, tenant.system_code, account.id AS channel_account_id, account.code AS channel_code, account.type AS channel_type, account.app_id").
		Joins("JOIN tenants AS tenant ON tenant.id = account.tenant_id AND tenant.deleted_at IS NULL").
		Where("account.deleted_at IS NULL AND account.type IN ?", []string{"wechat_miniapp", "xiaohongshu"}).
		Order("tenant.name ASC, account.type ASC, account.code ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]ChannelVerificationAccountView, 0, len(rows))
	for _, row := range rows {
		result = append(result, ChannelVerificationAccountView{
			TenantID: row.TenantID, TenantName: row.TenantName, SystemCode: row.SystemCode,
			ChannelAccountID: row.ChannelAccountID, ChannelCode: row.ChannelCode,
			ChannelType: row.ChannelType, AppID: row.AppID,
		})
	}
	return result, nil
}

func (s PlatformChannelVerificationService) List(tenantID, accountID uint) ([]ChannelVerificationFileView, error) {
	var rows []platformChannelVerificationRow
	query := model.DB.Table("channel_verification_files AS file").
		Select("file.*, tenant.name AS tenant_name, tenant.system_code, account.code AS channel_code, account.type AS channel_type, account.app_id").
		Joins("JOIN tenants AS tenant ON tenant.id = file.tenant_id AND tenant.deleted_at IS NULL").
		Joins("JOIN channel_accounts AS account ON account.id = file.channel_account_id AND account.tenant_id = file.tenant_id AND account.deleted_at IS NULL").
		Where("file.deleted_at IS NULL")
	if tenantID != 0 {
		query = query.Where("file.tenant_id = ?", tenantID)
	}
	if accountID != 0 {
		query = query.Where("file.channel_account_id = ?", accountID)
	}
	if err := query.Order("file.updated_at DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ChannelVerificationFileView, 0, len(rows))
	for _, row := range rows {
		result = append(result, s.view(row.ChannelVerificationFile, row.TenantName, row.SystemCode, row.ChannelCode, row.ChannelType, row.AppID))
	}
	return result, nil
}

func (s PlatformChannelVerificationService) Upload(actorID, tenantID, accountID uint, kind, filename string, data []byte) (ChannelVerificationFileView, error) {
	kind = strings.TrimSpace(kind)
	rawFilename := strings.TrimSpace(filename)
	if rawFilename == "" || strings.ContainsAny(rawFilename, `/\\`) || rawFilename != filepath.Base(rawFilename) {
		return ChannelVerificationFileView{}, errors.New("校验文件名无效")
	}
	filename = rawFilename
	if actorID == 0 || tenantID == 0 || accountID == 0 {
		return ChannelVerificationFileView{}, errors.New("平台、租户和渠道账号不能为空")
	}
	if len(data) == 0 || len(data) > MaxChannelVerificationFileBytes {
		return ChannelVerificationFileView{}, fmt.Errorf("校验文件必须小于 %d KB", MaxChannelVerificationFileBytes/1024)
	}
	if strings.ContainsRune(string(data), '\x00') {
		return ChannelVerificationFileView{}, errors.New("校验文件内容无效")
	}
	if err := validateChannelVerificationFile(kind, filename); err != nil {
		return ChannelVerificationFileView{}, err
	}

	var account model.ChannelAccount
	if err := model.DB.Where("id = ? AND tenant_id = ? AND type IN ?", accountID, tenantID, []string{"wechat_miniapp", "xiaohongshu"}).First(&account).Error; err != nil {
		return ChannelVerificationFileView{}, gorm.ErrRecordNotFound
	}
	if (kind == "wechat_miniapp" && account.Type != kind) || (kind == "xiaohongshu" && account.Type != kind) {
		return ChannelVerificationFileView{}, errors.New("校验文件类型与渠道账号类型不匹配")
	}

	hash := sha256.Sum256(data)
	contentHash := hex.EncodeToString(hash[:])
	storagePath, err := s.writeFile(tenantID, accountID, filename, data)
	if err != nil {
		return ChannelVerificationFileView{}, err
	}
	var oldPath string
	var result platformChannelVerificationRow
	now := time.Now().UTC()
	err = model.Write(func(tx *gorm.DB) error {
		var conflict model.ChannelVerificationFile
		if err := tx.Where("filename = ?", filename).First(&conflict).Error; err == nil && (conflict.TenantID != tenantID || conflict.ChannelAccountID != accountID) {
			return ErrVerificationFileConflict
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var file model.ChannelVerificationFile
		err := tx.Where("tenant_id = ? AND channel_account_id = ? AND kind = ?", tenantID, accountID, kind).First(&file).Error
		before := "{}"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			file = model.ChannelVerificationFile{TenantID: tenantID, ChannelAccountID: accountID, Kind: kind}
		} else if err != nil {
			return err
		} else {
			beforeBytes, _ := json.Marshal(map[string]interface{}{"filename": file.Filename, "content_hash": file.ContentHash, "content_size": file.ContentSize})
			before = string(beforeBytes)
			oldPath = file.StoragePath
		}
		file.Filename = filename
		file.StoragePath = storagePath
		file.ContentHash = contentHash
		file.ContentSize = int64(len(data))
		file.UploadedBy = actorID
		file.UploadedAt = now
		if err := tx.Save(&file).Error; err != nil {
			return err
		}
		afterBytes, _ := json.Marshal(map[string]interface{}{"filename": file.Filename, "content_hash": file.ContentHash, "content_size": file.ContentSize})
		if err := recordAuditTx(tx, actorID, tenantID, "platform_admin", "platform", "platform.channel_verification_file.upload", "channel_verification_file", file.ID, "平台管理员上传渠道校验文件", before, string(afterBytes)); err != nil {
			return err
		}
		result = platformChannelVerificationRow{ChannelVerificationFile: file, TenantName: "", SystemCode: "", ChannelCode: account.Code, ChannelType: account.Type, AppID: account.AppID}
		return tx.Table("tenants AS tenant").Select("tenant.name AS tenant_name, tenant.system_code").Where("tenant.id = ?", tenantID).Scan(&result).Error
	})
	if err != nil {
		_ = os.Remove(s.absolutePath(storagePath))
		return ChannelVerificationFileView{}, err
	}
	if oldPath != "" && oldPath != storagePath {
		_ = os.Remove(s.absolutePath(oldPath))
	}
	return s.view(result.ChannelVerificationFile, result.TenantName, result.SystemCode, result.ChannelCode, result.ChannelType, result.AppID), nil
}

func (s PlatformChannelVerificationService) Delete(actorID, id uint) error {
	if actorID == 0 || id == 0 {
		return errors.New("平台账号和校验文件编号不能为空")
	}
	var file model.ChannelVerificationFile
	if err := model.DB.First(&file, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVerificationFileNotFound
		}
		return err
	}
	if err := model.Write(func(tx *gorm.DB) error {
		// Verification filenames are public global paths. Remove the metadata
		// row physically so a later platform upload may safely reclaim the same
		// filename after deletion.
		if err := tx.Unscoped().Delete(&file).Error; err != nil {
			return err
		}
		return recordAuditTx(tx, actorID, file.TenantID, "platform_admin", "platform", "platform.channel_verification_file.delete", "channel_verification_file", file.ID, "平台管理员删除渠道校验文件", fmt.Sprintf(`{"filename":%q}`, file.Filename), "{}")
	}); err != nil {
		return err
	}
	_ = os.Remove(s.absolutePath(file.StoragePath))
	return nil
}

func (s PlatformChannelVerificationService) ReadPublic(filename string) ([]byte, error) {
	filename = filepath.Base(strings.TrimSpace(filename))
	if model.DB == nil || filename == "" || strings.Contains(filename, "..") {
		return nil, ErrVerificationFileNotFound
	}
	var file model.ChannelVerificationFile
	if err := model.DB.Where("filename = ?", filename).First(&file).Error; err != nil {
		return nil, ErrVerificationFileNotFound
	}
	data, err := os.ReadFile(s.absolutePath(file.StoragePath))
	if err != nil {
		return nil, ErrVerificationFileNotFound
	}
	return data, nil
}

func (s PlatformChannelVerificationService) PublicPath(filename string) string {
	return "/" + filepath.Base(strings.TrimSpace(filename))
}

func (s PlatformChannelVerificationService) view(file model.ChannelVerificationFile, tenantName, systemCode, channelCode, channelType, appID string) ChannelVerificationFileView {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	return ChannelVerificationFileView{
		ID: file.ID, TenantID: file.TenantID, TenantName: tenantName, SystemCode: systemCode,
		ChannelAccountID: file.ChannelAccountID, ChannelCode: channelCode, ChannelType: channelType,
		AppID: appID, Kind: file.Kind, Filename: file.Filename,
		PublicURL: base + s.PublicPath(file.Filename), ContentHash: file.ContentHash,
		ContentSize: file.ContentSize, UploadedBy: file.UploadedBy, UploadedAt: file.UploadedAt, UpdatedAt: file.UpdatedAt,
	}
}

func (s PlatformChannelVerificationService) writeFile(tenantID, accountID uint, filename string, data []byte) (string, error) {
	directory := filepath.Join(strings.TrimSpace(s.Directory), "channel-verification", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(accountID), 10))
	if directory == "" || strings.TrimSpace(s.Directory) == "" {
		return "", errors.New("系统未配置校验文件存储目录")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("创建校验文件目录失败: %w", err)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("生成校验文件存储名失败: %w", err)
	}
	storageName := hex.EncodeToString(random) + ".txt"
	relativePath := filepath.Join("channel-verification", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(accountID), 10), storageName)
	temporaryPath := filepath.Join(directory, "."+storageName+".tmp")
	if err := os.WriteFile(temporaryPath, data, 0o640); err != nil {
		return "", fmt.Errorf("保存校验文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, storageName)); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("提交校验文件失败: %w", err)
	}
	_ = filename
	return relativePath, nil
}

func (s PlatformChannelVerificationService) absolutePath(relativePath string) string {
	root, err := filepath.Abs(strings.TrimSpace(s.Directory))
	if err != nil || strings.TrimSpace(relativePath) == "" {
		return ""
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(filepath.ToSlash(relativePath))))
	if err != nil {
		return ""
	}
	prefix := root + string(os.PathSeparator)
	if candidate != root && !strings.HasPrefix(candidate, prefix) {
		return ""
	}
	return candidate
}

func validateChannelVerificationFile(kind, filename string) error {
	if kind != "wechat_miniapp" && kind != "xiaohongshu" {
		return errors.New("不支持的校验文件类型")
	}
	if strings.TrimSpace(filename) == "" {
		return errors.New("校验文件名不能为空")
	}
	return nil
}
