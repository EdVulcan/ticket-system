package service

import "testing"

func TestDenyResponseUsesDistinctLocalVoiceCodesForValidity(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		displayText string
		voiceCode   string
	}{
		{name: "expired", err: ErrTicketExpired, displayText: "已过期", voiceCode: "expired"},
		{name: "not started", err: ErrTicketNotStarted, displayText: "未生效", voiceCode: "not_started"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := denyResponse(tt.err)
			if resp.DisplayText != tt.displayText || resp.VoiceCode != tt.voiceCode {
				t.Fatalf("response=%+v", resp)
			}
		})
	}
}
