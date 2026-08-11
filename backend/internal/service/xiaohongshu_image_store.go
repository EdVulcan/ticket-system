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

const MaxXiaohongshuImageBytes = 5 << 20

type XiaohongshuImageStore struct {
	Directory     string
	PublicBaseURL string
}

func (s XiaohongshuImageStore) Save(tenantID, accountID uint, data []byte) (string, error) {
	if tenantID == 0 || accountID == 0 {
		return "", errors.New("租户或渠道账号无效")
	}
	if len(data) == 0 || len(data) > MaxXiaohongshuImageBytes {
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
	relativeParts := []string{"channel-products", strconv.FormatUint(uint64(tenantID), 10), strconv.FormatUint(uint64(accountID), 10)}
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

	publicPath := "/media/" + strings.Join(append(relativeParts, filename), "/")
	return strings.TrimRight(baseURL.String(), "/") + publicPath, nil
}
