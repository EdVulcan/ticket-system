package xiaohongshu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCachesTokenAndUsesSelfDevelopedEndpoints(t *testing.T) {
	var tokenCalls, productCalls, orderCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			tokenCalls++
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["appid"] != "miniapp" || request["secret"] != "secret" {
				t.Fatalf("token request=%+v", request)
			}
			_, _ = w.Write([]byte(`{"data":{"access_token":"token-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/poi/product/upsert":
			productCalls++
			assertAuthQuery(t, r)
			_, _ = w.Write([]byte(`{"data":{},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/order/upsert":
			orderCalls++
			assertAuthQuery(t, r)
			_, _ = w.Write([]byte(`{"data":{"out_order_id":"ORD-1","order_id":"XHS-1","final_price":8000,"pay_token":"pay-token","expired_time":1786300200,"open_pay_type":"life_gpay"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client(), Now: func() time.Time { return time.Unix(1786298400, 0) }}
	err := client.UpsertLocalLifeProduct(context.Background(), validProduct())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.UpsertOrder(context.Background(), validOrder())
	if err != nil {
		t.Fatal(err)
	}
	if response.OrderID != "XHS-1" || response.PayToken != "pay-token" || response.OpenPayType != "life_gpay" {
		t.Fatalf("response=%+v", response)
	}
	if tokenCalls != 1 || productCalls != 1 || orderCalls != 1 {
		t.Fatalf("token=%d product=%d order=%d", tokenCalls, productCalls, orderCalls)
	}
}

func TestGetGuaranteeOrderAndVerifyVoucher(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/rmp/token":
			_, _ = w.Write([]byte(`{"data":{"access_token":"token-1","expire_in":7200},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/gpay_order/get":
			assertAuthQuery(t, r)
			_, _ = w.Write([]byte(`{"data":{"order_id":"XHS-1","pay_amount":8000,"order_status":6,"voucher_infos":[{"voucher_code":"V-1","voucher_status":1,"pay_amount":8000}],"third_trade_no":"PAY-1"},"success":true,"msg":"success","code":0}`))
		case "/api/rmp/mp/deal/voucher/verify":
			assertAuthQuery(t, r)
			_, _ = w.Write([]byte(`{"data":{"verify_id":"VERIFY-1"},"success":true,"msg":"success","code":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client()}
	order, err := client.GetGuaranteeOrder(context.Background(), GuaranteeOrderRequest{ExternalOrderID: "ORD-1", OpenID: "OPEN-1", OrderType: 1})
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderStatus != 6 || len(order.Vouchers) != 1 || order.Vouchers[0].Code != "V-1" {
		t.Fatalf("order=%+v", order)
	}
	verified, err := client.VerifyVouchers(context.Background(), VoucherVerifyRequest{ExternalOrderID: "ORD-1", POIID: "POI-1", Vouchers: []VoucherCode{{Code: "V-1"}}})
	if err != nil || verified.VerifyID != "VERIFY-1" {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
}

func TestClientReturnsOfficialAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"success":false,"msg":"trade ability is unavailable","code":40001}`))
	}))
	defer server.Close()
	client := Client{AppID: "miniapp", Secret: "secret", BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.GetGuaranteeOrder(context.Background(), GuaranteeOrderRequest{ExternalOrderID: "ORD-1", OpenID: "OPEN-1", OrderType: 1})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 40001 || !strings.Contains(apiErr.Message, "trade ability") {
		t.Fatalf("error=%v", err)
	}
}

func TestOrderPriceMismatchIsRejectedBeforeNetwork(t *testing.T) {
	request := validOrder()
	request.Price.OrderPrice++
	client := Client{AppID: "miniapp", Secret: "secret"}
	_, err := client.UpsertOrder(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "price mismatch") {
		t.Fatalf("error=%v", err)
	}
}

func TestVoucherBatchLimitIsEnforced(t *testing.T) {
	vouchers := make([]VoucherCode, 11)
	for index := range vouchers {
		vouchers[index].Code = "V"
	}
	client := Client{}
	_, err := client.VerifyVouchers(context.Background(), VoucherVerifyRequest{ExternalOrderID: "ORD-1", Vouchers: vouchers})
	if err == nil || !strings.Contains(err.Error(), "1 to 10") {
		t.Fatalf("error=%v", err)
	}
}

func assertAuthQuery(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Query().Get("appid") != "miniapp" || r.URL.Query().Get("access_token") != "token-1" {
		t.Fatalf("query=%s", r.URL.RawQuery)
	}
}

func validProduct() LocalLifeProductRequest {
	return LocalLifeProductRequest{
		ExternalProductID: "PRODUCT-1", Name: "景区门票", ShortTitle: "景区门票", Description: "景区门票",
		Path: "/pages/product/detail?id=PRODUCT-1", TopImage: "https://example.com/ticket.png", CategoryID: "category",
		CreatedAt: 1786298400, UpdatedAt: 1786298400, POIIDs: []string{"POI-1"}, ProductType: ProductTypeGroupVoucher, SettleType: SettleAtPOI,
		SKUs: []ProductSKU{{ExternalSKUID: "SKU-1", Name: "成人票", Image: "https://example.com/ticket.png", OriginPrice: 8000, SalePrice: 8000, Status: 1}},
	}
}

func validOrder() OrderUpsertRequest {
	return OrderUpsertRequest{
		ExternalOrderID: "ORD-1", OpenID: "OPEN-1", Path: "/pages/order/detail?id=ORD-1", CreatedAt: 1786298400, ExpiresAt: 1786300200,
		Products: []OrderProduct{{ExternalProductID: "PRODUCT-1", ExternalSKUID: "SKU-1", Count: 1, SalePrice: 8000, RealPrice: 8000}},
		Price:    OrderPrice{OrderPrice: 8000},
	}
}
