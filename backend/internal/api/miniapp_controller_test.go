package api

import (
	"strings"
	"testing"
	"ticket-backend/internal/xiaohongshu"
)

func TestXiaohongshuLoginErrorExposesCodeWithoutPlatformMessage(t *testing.T) {
	platformError := &xiaohongshu.APIError{Code: 40001, Message: "sensitive upstream detail"}
	message := xiaohongshuLoginError(platformError)
	if !strings.Contains(message, "40001") {
		t.Fatalf("message=%q", message)
	}
	if strings.Contains(message, platformError.Message) {
		t.Fatalf("platform detail leaked in public response: %q", message)
	}
}
