package xiaohongshu

import "encoding/json"

// A missing or null provider amount is not evidence of a zero-value transaction.
func (r *OrderUpsertResponse) UnmarshalJSON(data []byte) error {
	type plain OrderUpsertResponse
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields struct {
		Amount *int64 `json:"final_price"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = OrderUpsertResponse(value)
	r.FinalPricePresent = fields.Amount != nil
	return nil
}

func (r *GuaranteeOrderResponse) UnmarshalJSON(data []byte) error {
	type plain GuaranteeOrderResponse
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields struct {
		Amount *int64 `json:"pay_amount"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = GuaranteeOrderResponse(value)
	r.PayAmountPresent = fields.Amount != nil
	return nil
}

func (r *VoucherInfo) UnmarshalJSON(data []byte) error {
	type plain VoucherInfo
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields struct {
		Amount *int64 `json:"pay_amount"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = VoucherInfo(value)
	r.PayAmountPresent = fields.Amount != nil
	return nil
}
