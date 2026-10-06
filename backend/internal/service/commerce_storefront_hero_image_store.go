package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MaxCommerceStorefrontHeroImageBytes = 5 << 20

// CommerceStorefrontHeroImageStore keeps uploaded storefront hero images
// separate from product media and contact QR codes. Each binding owns its
// image namespace, so restaurant and retail bindings can be configured
// independently even when they use the same WeChat account.
type CommerceStorefrontHeroImageStore struct {
	Directory     string
	PublicBaseURL string
}

func (s CommerceStorefrontHeroImageStore) Save(tenantID, bindingID uint, data []byte) (string, error) {
	if tenantID == 0 || bindingID == 0 {
		return "", errors.New("租户或小程序发布配置无效")
	}
	if len(data) == 0 || len(data) > MaxCommerceStorefrontHeroImageBytes {
		return "", errors.New("首页头图必须小于 5 MB")
	}
	contentType := http.DetectContentType(data)
	extension := ""
	switch contentType {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	default:
		return "", errors.New("首页头图仅支持 JPG 或 PNG 格式")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return "", errors.New("首页头图图片内容无效")
	}
	baseURL, err := url.Parse(strings.TrimSpace(s.PublicBaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return "", errors.New("系统未配置有效的 HTTPS 公网地址")
	}
	directory := strings.TrimSpace(s.Directory)
	if directory == "" {
		return "", errors.New("系统未配置首页头图存储目录")
	}

	nameBytes := make([]byte, 16)
	if _, err := rand.Read(nameBytes); err != nil {
		return "", fmt.Errorf("生成首页头图文件名失败: %w", err)
	}
	filename := hex.EncodeToString(nameBytes) + extension
	relativeParts := []string{"commerce-storefront-heroes", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(bindingID), 10)}
	targetDirectory := filepath.Join(append([]string{directory}, relativeParts...)...)
	if err := os.MkdirAll(targetDirectory, 0750); err != nil {
		return "", fmt.Errorf("创建首页头图目录失败: %w", err)
	}
	targetPath := filepath.Join(targetDirectory, filename)
	file, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return "", fmt.Errorf("保存首页头图失败: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(targetPath)
		return "", fmt.Errorf("保存首页头图失败: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(targetPath)
		return "", fmt.Errorf("保存首页头图失败: %w", err)
	}
	publicPath := "/api/v1/public/commerce-storefront-hero-images/" + strings.Join(append(relativeParts[1:], filename), "/")
	return strings.TrimRight(baseURL.String(), "/") + publicPath, nil
}

func (s CommerceStorefrontHeroImageStore) ownedPath(tenantID, bindingID uint, imageURL string) (string, error) {
	if tenantID == 0 || bindingID == 0 {
		return "", errors.New("租户或小程序发布配置无效")
	}
	baseURL, err := url.Parse(strings.TrimSpace(s.PublicBaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return "", errors.New("系统未配置有效的 HTTPS 公网地址")
	}
	candidate, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || len(imageURL) > 500 || candidate.User != nil || candidate.Scheme != "https" || candidate.Host != baseURL.Host || candidate.RawQuery != "" || candidate.Fragment != "" {
		return "", errors.New("首页头图必须使用系统上传的 HTTPS 图片")
	}
	prefix := "/api/v1/public/commerce-storefront-hero-images/" + strconv.FormatUint(uint64(tenantID), 10) + "/" + strconv.FormatUint(uint64(bindingID), 10) + "/"
	if !strings.HasPrefix(candidate.Path, prefix) {
		return "", errors.New("首页头图必须属于当前小程序发布配置")
	}
	filename := strings.TrimPrefix(candidate.Path, prefix)
	if filename == "" || strings.Contains(filename, "/") {
		return "", errors.New("首页头图地址无效")
	}
	extension := strings.ToLower(filepath.Ext(filename))
	stem := strings.TrimSuffix(filename, extension)
	if (extension != ".jpg" && extension != ".png") || len(stem) != 32 {
		return "", errors.New("首页头图地址无效")
	}
	if _, err := hex.DecodeString(stem); err != nil {
		return "", errors.New("首页头图地址无效")
	}
	directory := strings.TrimSpace(s.Directory)
	if directory == "" {
		return "", errors.New("系统未配置首页头图存储目录")
	}
	return filepath.Join(directory, "commerce-storefront-heroes", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(bindingID), 10), filename), nil
}

func (s CommerceStorefrontHeroImageStore) ValidateOwnedURL(tenantID, bindingID uint, imageURL string) error {
	path, err := s.ownedPath(tenantID, bindingID, imageURL)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return errors.New("首页头图图片不存在")
	}
	return nil
}

func (s CommerceStorefrontHeroImageStore) RemoveOwnedURL(tenantID, bindingID uint, imageURL string) error {
	if strings.TrimSpace(imageURL) == "" {
		return nil
	}
	path, err := s.ownedPath(tenantID, bindingID, imageURL)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
