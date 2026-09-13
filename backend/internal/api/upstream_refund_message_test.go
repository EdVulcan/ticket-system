package api

import (
	"fmt"
	"strings"
	"testing"
	"ticket-backend/internal/service"
)

func TestUpstreamRefundCheckErrorsDoNotClaimOrderWasUsed(t *testing.T) {
	message := serviceMiniappRefundMessage(fmt.Errorf("%w: response identity mismatch", service.ErrUpstreamRefundUnknown))
	if !strings.Contains(message, "退款尚未提交") || strings.Contains(message, "已核销") || strings.Contains(message, "售后状态待核查") {
		t.Fatal(message)
	}
	if message = serviceMiniappRefundMessage(service.ErrUpstreamRefundUsed); !strings.Contains(message, "已在合作景区使用") {
		t.Fatal(message)
	}
}
