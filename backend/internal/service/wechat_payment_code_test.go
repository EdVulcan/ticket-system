package service

import (
	"errors"
	"testing"

	"ticket-backend/internal/model"

	wechatv2 "github.com/go-pay/gopay/wechat"
	wechatv3 "github.com/go-pay/gopay/wechat/v3"
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

func TestApplyWeChatNativeResponseUsesGoPaySuccessCodeAndRequiresCodeURL(t *testing.T) {
	tests := []struct {
		name        string
		response    *wechatv3.NativeRsp
		wantErr     bool
		wantCodeURL string
	}{
		{
			name:        "success code zero with code url",
			response:    &wechatv3.NativeRsp{Code: wechatv3.Success, Response: &wechatv3.Native{CodeUrl: " https://pay.example.test/native "}},
			wantCodeURL: "https://pay.example.test/native",
		},
		{
			name:     "http status is not success code",
			response: &wechatv3.NativeRsp{Code: 200, Response: &wechatv3.Native{CodeUrl: "https://pay.example.test/native"}},
			wantErr:  true,
		},
		{
			name:     "provider failure response",
			response: &wechatv3.NativeRsp{Code: 500, Error: "provider failure"},
			wantErr:  true,
		},
		{
			name:    "nil response",
			wantErr: true,
		},
		{
			name:     "success without response body",
			response: &wechatv3.NativeRsp{Code: wechatv3.Success},
			wantErr:  true,
		},
		{
			name:     "success without code url",
			response: &wechatv3.NativeRsp{Code: wechatv3.Success, Response: &wechatv3.Native{}},
			wantErr:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payment := &model.Payment{}
			err := applyWeChatNativeResponse(payment, test.response)
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v, wantErr=%t", err, test.wantErr)
			}
			if payment.CodeURL != test.wantCodeURL {
				t.Fatalf("code URL=%q, want %q", payment.CodeURL, test.wantCodeURL)
			}
		})
	}
}
