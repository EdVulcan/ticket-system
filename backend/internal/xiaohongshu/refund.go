package xiaohongshu

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AfterSalesPriceInfo represents a refund amount in cents.
type AfterSalesPriceInfo struct {
	RefundPrice int64 `json:"refund_price"`
}

// AfterSalesProductInfo identifies a refunded product line. Price is the
// total refund amount for the line, in cents.
type AfterSalesProductInfo struct {
	ExternalProductID string `json:"out_product_id"`
	ExternalSKUID     string `json:"out_sku_id"`
	Count             int    `json:"num"`
	Price             int64  `json:"price"`
}

// AfterSalesVoucherDetail identifies one group-voucher refund in cents.
type AfterSalesVoucherDetail struct {
	VoucherCode string `json:"voucher_code"`
	RefundPrice int64  `json:"refund_price"`
}

// AfterSalesAddRequest is the official local-life after-sales submission.
// It supports only group-voucher refunds (type and product_type both 1).
type AfterSalesAddRequest struct {
	ExternalOrderID           string                    `json:"out_order_id"`
	ExternalAfterSalesOrderID string                    `json:"out_after_sales_order_id"`
	OpenID                    string                    `json:"open_id"`
	Type                      int                       `json:"type"`
	Reason                    string                    `json:"reason"`
	BizCreateTime             int64                     `json:"biz_create_time"`
	Price                     AfterSalesPriceInfo       `json:"price_info"`
	Products                  []AfterSalesProductInfo   `json:"product_infos"`
	Vouchers                  []AfterSalesVoucherDetail `json:"refund_voucher_detail"`
	ProductType               int                       `json:"product_type"`
	AutoConfirm               bool                      `json:"auto_confirm"`
	RefundRuleType            int                       `json:"refund_rule_type"`
}

// AfterSalesGetRequest identifies an official after-sales query.
type AfterSalesGetRequest struct {
	ExternalOrderID           string `json:"out_order_id"`
	ExternalAfterSalesOrderID string `json:"out_after_sales_order_id"`
	OpenID                    string `json:"open_id"`
}

// AfterSalesOrderResponse is the provider's after-sales state. Status 1 is
// pending, 2 is successful, and 3 is failed. A successful add submission only
// records the request with the provider; it is not a financial success.
type AfterSalesOrderResponse struct {
	ExternalOrderID           string                    `json:"out_order_id"`
	ExternalAfterSalesOrderID string                    `json:"out_after_sales_order_id"`
	OpenID                    string                    `json:"open_id"`
	Status                    int                       `json:"status"`
	Type                      int                       `json:"type"`
	Price                     AfterSalesPriceInfo       `json:"price_info"`
	Vouchers                  []AfterSalesVoucherDetail `json:"refund_voucher_detail"`
	ProductType               int                       `json:"product_type"`
}

// AddAfterSalesOrder submits a group-voucher refund request. Envelope success
// only means the platform accepted the submission; callers must query or
// consume the provider's later result before making financial changes.
func (c *Client) AddAfterSalesOrder(ctx context.Context, request AfterSalesAddRequest) error {
	if err := validateAfterSalesAdd(request); err != nil {
		return err
	}
	var response struct{}
	return c.authenticatedPost(ctx, "/api/rmp/mp/deal/order/after_sales_order/add", request, &response)
}

// GetAfterSalesOrder reads the provider's current after-sales state. Identity
// and amount reconciliation intentionally belongs to the persistent service
// that owns the local refund and its financial state transition.
func (c *Client) GetAfterSalesOrder(ctx context.Context, request AfterSalesGetRequest) (*AfterSalesOrderResponse, error) {
	if err := validateAfterSalesGet(request); err != nil {
		return nil, err
	}
	var response AfterSalesOrderResponse
	if err := c.authenticatedPost(ctx, "/api/rmp/mp/deal/order/after_sales_order/get", request, &response); err != nil {
		return nil, err
	}
	switch response.Status {
	case 1, 2, 3:
		return &response, nil
	default:
		return nil, fmt.Errorf("xiaohongshu after-sales response has unsupported status %d", response.Status)
	}
}

func validateAfterSalesAdd(request AfterSalesAddRequest) error {
	if strings.TrimSpace(request.ExternalOrderID) == "" || strings.TrimSpace(request.ExternalAfterSalesOrderID) == "" ||
		strings.TrimSpace(request.OpenID) == "" || strings.TrimSpace(request.Reason) == "" || request.BizCreateTime <= 0 {
		return errors.New("xiaohongshu after-sales identifiers, reason and timestamp are required")
	}
	if request.Type != 1 || request.ProductType != ProductTypeGroupVoucher || !request.AutoConfirm || request.RefundRuleType != 1 {
		return errors.New("xiaohongshu after-sales supports only auto-confirmed group-voucher refunds that return resources")
	}
	if request.Price.RefundPrice <= 0 || len(request.Products) == 0 || len(request.Vouchers) == 0 {
		return errors.New("xiaohongshu after-sales refund amount, products and vouchers are required")
	}

	var productTotal, voucherTotal int64
	for _, product := range request.Products {
		if strings.TrimSpace(product.ExternalProductID) == "" || strings.TrimSpace(product.ExternalSKUID) == "" || product.Count <= 0 || product.Price <= 0 {
			return errors.New("xiaohongshu after-sales product identifiers, quantity and positive price are required")
		}
		if product.Price > (1<<63-1)-productTotal {
			return errors.New("xiaohongshu after-sales product refund amount is too large")
		}
		productTotal += product.Price
	}

	voucherCodes := make(map[string]struct{}, len(request.Vouchers))
	for _, voucher := range request.Vouchers {
		voucherCode := strings.TrimSpace(voucher.VoucherCode)
		if voucherCode == "" || voucher.RefundPrice <= 0 {
			return errors.New("xiaohongshu after-sales voucher code and positive refund amount are required")
		}
		if _, exists := voucherCodes[voucherCode]; exists {
			return errors.New("xiaohongshu after-sales voucher codes must be unique")
		}
		if voucher.RefundPrice > (1<<63-1)-voucherTotal {
			return errors.New("xiaohongshu after-sales voucher refund amount is too large")
		}
		voucherCodes[voucherCode] = struct{}{}
		voucherTotal += voucher.RefundPrice
	}
	if productTotal != request.Price.RefundPrice || voucherTotal != request.Price.RefundPrice {
		return fmt.Errorf("xiaohongshu after-sales refund amount mismatch: products %d, vouchers %d, declared %d", productTotal, voucherTotal, request.Price.RefundPrice)
	}
	return nil
}

// ValidateAfterSalesAdd lets durable callers reject deterministic mistakes
// before crossing their persisted-before-send barrier.
func ValidateAfterSalesAdd(request AfterSalesAddRequest) error { return validateAfterSalesAdd(request) }

func validateAfterSalesGet(request AfterSalesGetRequest) error {
	if strings.TrimSpace(request.ExternalOrderID) == "" || strings.TrimSpace(request.ExternalAfterSalesOrderID) == "" || strings.TrimSpace(request.OpenID) == "" {
		return errors.New("xiaohongshu after-sales order, after-sales order and open_id are required")
	}
	return nil
}
