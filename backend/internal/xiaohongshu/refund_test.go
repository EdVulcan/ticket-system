package xiaohongshu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddAfterSalesOrderUsesOfficialEndpoint(t *testing.T) {
	var tokenCalls, addCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			tokenCalls++
			_, _ = w.Write([]byte(`{"data":{"access_token":"token-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/after_sales_order/add":
			addCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("method=%s", r.Method)
			}
			assertAuthQuery(t, r)
			var request AfterSalesAddRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.ExternalOrderID != "ORD-1" || request.ExternalAfterSalesOrderID != "REFUND-1" || request.OpenID != "OPEN-1" ||
				request.Type != 1 || request.Reason != "游客取消" || request.BizCreateTime != 1786298400 || request.Price.RefundPrice != 8000 ||
				request.ProductType != ProductTypeGroupVoucher || !request.AutoConfirm || request.RefundRuleType != 1 ||
				len(request.Products) != 1 || request.Products[0].ExternalProductID != "PRODUCT-1" || request.Products[0].ExternalSKUID != "SKU-1" || request.Products[0].Count != 1 || request.Products[0].Price != 8000 ||
				len(request.Vouchers) != 1 || request.Vouchers[0].VoucherCode != "V-1" || request.Vouchers[0].RefundPrice != 8000 {
				t.Fatalf("request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client()}
	if err := client.AddAfterSalesOrder(context.Background(), validAfterSalesAddRequest()); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || addCalls != 1 {
		t.Fatalf("tokenCalls=%d addCalls=%d", tokenCalls, addCalls)
	}
}

func TestGetAfterSalesOrderUsesOfficialEndpointWithoutProducts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"token-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/after_sales_order/get":
			if r.Method != http.MethodPost {
				t.Fatalf("method=%s", r.Method)
			}
			assertAuthQuery(t, r)
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if len(request) != 3 || request["out_order_id"] != "ORD-1" || request["out_after_sales_order_id"] != "REFUND-1" || request["open_id"] != "OPEN-1" {
				t.Fatalf("request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"ORD-1","out_after_sales_order_id":"REFUND-1","open_id":"OPEN-1","status":2,"type":1,"price_info":{"refund_price":8000},"refund_voucher_detail":[{"voucher_code":"V-1","refund_price":8000}],"product_type":1},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client()}
	response, err := client.GetAfterSalesOrder(context.Background(), AfterSalesGetRequest{ExternalOrderID: "ORD-1", ExternalAfterSalesOrderID: "REFUND-1", OpenID: "OPEN-1"})
	if err != nil || response.Status != 2 || response.Price.RefundPrice != 8000 || len(response.Vouchers) != 1 || response.Vouchers[0].VoucherCode != "V-1" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestAfterSalesValidationFailsBeforeNetwork(t *testing.T) {
	var calls int
	client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	})}}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "missing order id",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.ExternalOrderID = ""
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "unsupported type",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.Type = 2
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "unsupported product type",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.ProductType = ProductTypePresaleVoucher
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "product amount mismatch",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.Products[0].Price = 7999
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "duplicate voucher code",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.Vouchers = append(request.Vouchers, AfterSalesVoucherDetail{VoucherCode: "V-1", RefundPrice: 1})
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "empty voucher code",
			call: func() error {
				request := validAfterSalesAddRequest()
				request.Vouchers[0].VoucherCode = " "
				return client.AddAfterSalesOrder(context.Background(), request)
			},
		},
		{
			name: "incomplete get identity",
			call: func() error {
				_, err := client.GetAfterSalesOrder(context.Background(), AfterSalesGetRequest{ExternalOrderID: "ORD-1", OpenID: "OPEN-1"})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("network calls=%d", calls)
	}
}

func TestGetAfterSalesOrderRejectsUnknownStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"token-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/after_sales_order/get":
			_, _ = w.Write([]byte(`{"data":{"status":4},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.GetAfterSalesOrder(context.Background(), AfterSalesGetRequest{ExternalOrderID: "ORD-1", ExternalAfterSalesOrderID: "REFUND-1", OpenID: "OPEN-1"})
	if err == nil || !strings.Contains(err.Error(), "unsupported status 4") {
		t.Fatalf("error=%v", err)
	}
}

func validAfterSalesAddRequest() AfterSalesAddRequest {
	return AfterSalesAddRequest{
		ExternalOrderID: "ORD-1", ExternalAfterSalesOrderID: "REFUND-1", OpenID: "OPEN-1", Type: 1, Reason: "游客取消", BizCreateTime: 1786298400,
		Price:       AfterSalesPriceInfo{RefundPrice: 8000},
		Products:    []AfterSalesProductInfo{{ExternalProductID: "PRODUCT-1", ExternalSKUID: "SKU-1", Count: 1, Price: 8000}},
		Vouchers:    []AfterSalesVoucherDetail{{VoucherCode: "V-1", RefundPrice: 8000}},
		ProductType: ProductTypeGroupVoucher, AutoConfirm: true, RefundRuleType: 1,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
