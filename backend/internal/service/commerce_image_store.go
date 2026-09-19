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

const (
	MaxCommerceImageBytes       = 5 << 20
	CommerceProductMediaCover   = "cover"
	CommerceProductMediaDetail  = "detail"
)

// CommerceImageStore keeps commercial product media in its own filesystem
// subtree. It must not share the Xiaohongshu channel-product namespace.
type CommerceImageStore struct {
	Directory     string
	PublicBaseURL string
}

func normalizeCommerceMediaKind(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != CommerceProductMediaCover && kind != CommerceProductMediaDetail {
		return "", errors.New("商品图片类型无效")
	}
	return kind, nil
}

func (s CommerceImageStore) Save(tenantID, productID uint, kind string, data []byte) (string, error) {
	if tenantID == 0 || productID == 0 {
		return "", errors.New("租户或商品无效")
	}
	var err error
	kind, err = normalizeCommerceMediaKind(kind)
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > MaxCommerceImageBytes {
		return "", errors.New("商品图片必须小于 5 MB")
	}
	contentType := http.DetectContentType(data)
	extension := ""
	switch contentType {
	case "image/jpeg":
		extension = ".jpg"
	case "image/png":
		extension = ".png"
	default:
		return "", errors.New("商品图片仅支持 JPG 或 PNG 格式")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return "", errors.New("商品图片内容无效")
	}
	baseURL, err := url.Parse(strings.TrimSpace(s.PublicBaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return "", errors.New("系统未配置有效的 HTTPS 公网地址")
	}
	directory := strings.TrimSpace(s.Directory)
	if directory == "" {
		return "", errors.New("系统未配置商品图片存储目录")
	}

	nameBytes := make([]byte, 16)
	if _, err := rand.Read(nameBytes); err != nil {
		return "", fmt.Errorf("生成商品图片文件名失败: %w", err)
	}
	filename := hex.EncodeToString(nameBytes) + extension
	relativeParts := []string{"commerce-products", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(productID), 10), kind}
	targetDirectory := filepath.Join(append([]string{directory}, relativeParts...)...)
	if err := os.MkdirAll(targetDirectory, 0750); err != nil {
		return "", fmt.Errorf("创建商品图片目录失败: %w", err)
	}
	targetPath := filepath.Join(targetDirectory, filename)
	file, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if err != nil {
		return "", fmt.Errorf("保存商品图片失败: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(targetPath)
		return "", fmt.Errorf("保存商品图片失败: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(targetPath)
		return "", fmt.Errorf("保存商品图片失败: %w", err)
	}

	publicPath := "/api/v1/public/commerce-product-images/" + strings.Join(append(relativeParts[1:], filename), "/")
	return strings.TrimRight(baseURL.String(), "/") + publicPath, nil
}

// ValidateOwnedURL is used by tests and future import paths to ensure a
// product cannot point at another tenant/product's managed upload.
func (s CommerceImageStore) ValidateOwnedURL(tenantID, productID uint, kind, imageURL string) error {
	if tenantID == 0 || productID == 0 {
		return errors.New("租户或商品无效")
	}
	kind, err := normalizeCommerceMediaKind(kind)
	if err != nil {
		return err
	}
	baseURL, err := url.Parse(strings.TrimSpace(s.PublicBaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return errors.New("系统未配置有效的 HTTPS 公网地址")
	}
	candidate, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || len(imageURL) > 500 || candidate.User != nil || candidate.Scheme != "https" || candidate.Host != baseURL.Host || candidate.RawQuery != "" || candidate.Fragment != "" {
		return errors.New("商品图片必须使用系统上传的 HTTPS 图片")
	}
	prefix := "/api/v1/public/commerce-product-images/" + strconv.FormatUint(uint64(tenantID), 10) + "/" + strconv.FormatUint(uint64(productID), 10) + "/" + kind + "/"
	if !strings.HasPrefix(candidate.Path, prefix) {
		return errors.New("商品图片必须使用当前商品上传的图片")
	}
	filename := strings.TrimPrefix(candidate.Path, prefix)
	if strings.Contains(filename, "/") {
		return errors.New("商品图片地址无效")
	}
	extension := strings.ToLower(filepath.Ext(filename))
	stem := strings.TrimSuffix(filename, extension)
	if (extension != ".jpg" && extension != ".png") || len(stem) != 32 {
		return errors.New("商品图片地址无效")
	}
	if _, err := hex.DecodeString(stem); err != nil {
		return errors.New("商品图片地址无效")
	}
	directory := strings.TrimSpace(s.Directory)
	if directory == "" {
		return errors.New("系统未配置商品图片存储目录")
	}
	info, err := os.Stat(filepath.Join(directory, "commerce-products", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(productID), 10), kind, filename))
	if err != nil || info.IsDir() {
		return errors.New("商品图片必须使用当前商品上传的图片")
	}
	return nil
}
