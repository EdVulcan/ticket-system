package xiaohongshu

import (
	"encoding/json"
	"testing"
)

func TestProviderAmountPresence(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		present     bool
		amount      int64
	}{
		{"missing", "", false, 0}, {"null", "null", false, 0}, {"zero", "0", true, 0}, {"positive", "1", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pay, final := `{}`, `{}`
			if tc.value != "" {
				pay = `{"pay_amount":` + tc.value + `}`
				final = `{"final_price":` + tc.value + `}`
			}
			var order GuaranteeOrderResponse
			var voucher VoucherInfo
			var upsert OrderUpsertResponse
			if err := json.Unmarshal([]byte(pay), &order); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(pay), &voucher); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(final), &upsert); err != nil {
				t.Fatal(err)
			}
			if order.PayAmountPresent != tc.present || voucher.PayAmountPresent != tc.present || upsert.FinalPricePresent != tc.present || order.PayAmount != tc.amount || voucher.PayAmount != tc.amount || upsert.FinalPrice != tc.amount {
				t.Fatalf("incorrect amount evidence: %+v %+v %+v", order, voucher, upsert)
			}
		})
	}
}
