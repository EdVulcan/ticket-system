package zyb

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ParseArtifact accepts the provider's direct base64 image or URL response.
// URL retrieval is intentionally outside this package; QR decoding only accepts bytes.
func ParseArtifact(value string) (*Artifact, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("智游宝票码响应为空")
	}
	if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		return &Artifact{Kind: "url", Value: value}, nil
	}
	mime := "image/jpeg"
	encoded := value
	if strings.HasPrefix(value, "data:") {
		parts := strings.SplitN(value, ",", 2)
		if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
			return nil, errors.New("智游宝票码 data URL 无效")
		}
		mime = strings.TrimPrefix(strings.SplitN(parts[0], ";", 2)[0], "data:")
		encoded = parts[1]
	}
	encoded = strings.Join(strings.Fields(encoded), "")
	if strings.HasPrefix(encoded, "iVBOR") {
		mime = "image/png"
	}
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("智游宝票码图片 base64 无效")
	}
	if len(b) > maxResponseBytes {
		return nil, errors.New("智游宝票码图片过大")
	}
	return &Artifact{Kind: "image", Value: base64.StdEncoding.EncodeToString(b), MimeType: mime}, nil
}
