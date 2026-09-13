package zyb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryOrderLiveTicketThirdCode(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		reject       bool
		price        string
	}{
		{"live", "<ticketThirdCode>LOCAL_39</ticketThirdCode><price>8000</price><totalPrice>8000</totalPrice>", false, "80.00"},
		{"documented", "<scenicThirdCode>LOCAL_39</scenicThirdCode><price>80</price><totalPrice>80</totalPrice>", false, "80"},
		{"conflicting", "<ticketThirdCode>OTHER_39</ticketThirdCode><scenicThirdCode>LOCAL_39</scenicThirdCode>", true, ""},
		{"matching aliases", "<ticketThirdCode>LOCAL_39</ticketThirdCode><scenicThirdCode>LOCAL_39</scenicThirdCode><price>8000</price><totalPrice>8000</totalPrice>", false, "80.00"},
		{"invalid cents", "<ticketThirdCode>LOCAL_39</ticketThirdCode><price>80.5</price>", true, ""},
		{"negative cents", "<ticketThirdCode>LOCAL_39</ticketThirdCode><price>-8000</price>", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `<PWBResponse><transactionName>QUERY_ORDER_NEW_RES</transactionName><code>0</code><orderResponse><order><orderCode>PROVIDER</orderCode><ticketOrders><ticketOrder><goodsCode>GOODS</goodsCode><quantity>1</quantity><returnNum>0</returnNum><alreadyCheckNum>0</alreadyCheckNum>%s</ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`, tc.fields)
			}))
			defer server.Close()
			client := Client{Config: Config{Endpoint: server.URL, CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}
			result, _, err := client.QueryOrder(context.Background(), "LOCAL")
			if tc.reject {
				if err == nil {
					t.Fatal("conflicting identity accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			row := result.Tickets[0]
			if row.ScenicThirdCode != "LOCAL_39" || row.ProviderSubOrderCode != "" || row.CheckedQuantity != "0" || row.Price != tc.price {
				t.Fatalf("unexpected parsed ticket: %+v", row)
			}
		})
	}
}
