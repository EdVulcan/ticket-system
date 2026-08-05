package service

import (
	"errors"
	"testing"

	"ticket-backend/internal/model"

	wechatv2 "github.com/go-pay/gopay/wechat"
)

func TestApplyWechatMicropayResponse(t *testing.T) {
	tests := []struct {
		name        string
		response    *wechatv2.MicropayResponse
		wantStatus  string
		wantPending bool
		wantError   bool
		permanent   bool
	}{
		{name: "paid", response: &wechatv2.MicropayResponse{ReturnCode: "SUCCESS", ResultCode: "SUCCESS", TransactionId: "wx-1"}, wantStatus: "paid"},
		{name: "customer entering password", response: &wechatv2.MicropayResponse{ReturnCode: "SUCCESS", ResultCode: "FAIL", ErrCode: "USERPAYING"}, wantStatus: "pending", wantPending: true},
		{name: "system error", response: &wechatv2.MicropayResponse{ReturnCode: "SUCCESS", ResultCode: "FAIL", ErrCode: "SYSTEMERROR"}, wantError: true, wantPending: true},
		{name: "declined", response: &wechatv2.MicropayResponse{ReturnCode: "SUCCESS", ResultCode: "FAIL", ErrCode: "NOTENOUGH", ErrCodeDes: "余额不足"}, wantError: true, permanent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment := &model.Payment{Method: "wechat", Status: "pending"}
			err := applyWechatMicropayResponse(payment, tt.response)
			if (err != nil) != tt.wantError || payment.Status != tt.wantStatus && tt.wantStatus != "" {
				t.Fatalf("status=%s err=%v", payment.Status, err)
			}
			if err != nil && providerRequestMayHaveBeenAccepted("wechat", err) != tt.wantPending {
				t.Fatalf("pending classification mismatch: %v", err)
			}
			var permanent permanentProviderError
			if errors.As(err, &permanent) != tt.permanent {
				t.Fatalf("permanent classification mismatch: %v", err)
			}
		})
	}
}
