package api

import (
	"errors"
	"net/http"
	"testing"
	"ticket-backend/internal/service"
)

func TestMobileVerificationErrorsUseFieldFriendlyChinese(t *testing.T) {
	tests := []struct {
		err     error
		status  int
		reason  string
		message string
	}{
		{service.ErrInvalidTicket, http.StatusUnprocessableEntity, "invalid_ticket", "无效票"},
		{service.ErrTicketRefunded, http.StatusUnprocessableEntity, "refunded", "订单已退款，不能核销"},
		{service.ErrTicketExpired, http.StatusUnprocessableEntity, "expired", "门票已过期"},
		{service.ErrTicketNotStarted, http.StatusUnprocessableEntity, "not_started", "门票尚未生效"},
		{service.ErrPointLimitReached, http.StatusUnprocessableEntity, "already_used", "当前检票点可用次数已满"},
		{errors.New("internal database error"), http.StatusBadRequest, "request_failed", "暂时无法读取票券，请稍后重试"},
	}
	for _, test := range tests {
		status, reason, message := mobileErrorPresentation(test.err)
		if status != test.status || reason != test.reason || message != test.message {
			t.Fatalf("error=%v got=(%d,%q,%q)", test.err, status, reason, message)
		}
	}
}
