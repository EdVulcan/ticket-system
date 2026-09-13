package zyb

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSendCodeEnvelopeSignatureAndAmounts(t *testing.T) {
	var gotXML, gotSign string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		gotXML, gotSign = form.Get("xmlMsg"), form.Get("sign")
		_, _ = io.WriteString(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>0</code><orderResponse><order><orderCode>PROVIDER</orderCode><ticketOrders><ticketOrder><orderCode>SUB</orderCode><goodsCode>G</goodsCode><quantity>2</quantity></ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`)
	}))
	defer s.Close()
	c := Client{Config: Config{Endpoint: s.URL, CorpCode: `c&`, Username: `u<`, PrivateKey: "secret", Now: func() time.Time { return time.Date(2026, 9, 13, 12, 34, 56, 0, time.UTC) }}, HTTP: s.Client()}
	if _, _, err := c.SendCode(context.Background(), SendCodeRequest{ThirdPartyOrderCode: "LOCAL", ChildOrderCode: "SUB", ContactName: "N", ContactMobile: "138", GoodsCode: "G", GoodsName: "Name", VisitDate: "2026-09-20", Price: "19.90", Quantity: 2}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotXML, `<corpCode>c&amp;</corpCode>`) || !strings.Contains(gotXML, `<userName>u&lt;</userName>`) || !strings.Contains(gotXML, `<orderPrice>39.80</orderPrice>`) || !strings.Contains(gotXML, `<price>19.90</price>`) {
		t.Fatalf("xml=%s", gotXML)
	}
	h := md5.Sum([]byte("xmlMsg=" + gotXML + "secret"))
	if gotSign != hex.EncodeToString(h[:]) {
		t.Fatalf("sign=%s", gotSign)
	}
}

func TestQueryOrderNotFoundAndUnknownResponseFailClosed(t *testing.T) {
	for _, response := range []string{
		`<PWBResponse><code>6</code><description>查询订单失败:订单不存在/外部订单号不存在</description></PWBResponse>`,
		`<PWBResponse><transactionName>QUERY_ORDER_NEW_RES</transactionName><code>6</code><description>订单不存在/外部订单号不存在</description></PWBResponse>`,
		`<PWBResponse><transactionName>OTHER</transactionName><code>0</code><order><orderCode>P</orderCode><ticketOrders><ticketOrder><orderCode>S</orderCode><goodsCode>G</goodsCode><quantity>1</quantity></ticketOrder></ticketOrders></order></PWBResponse>`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, response) }))
		c := Client{Config: Config{Endpoint: s.URL, CorpCode: "c", Username: "u", PrivateKey: "k"}, HTTP: s.Client()}
		_, _, err := c.QueryOrder(context.Background(), "LOCAL")
		s.Close()
		if err == nil {
			t.Fatalf("expected failure")
		}
		if strings.Contains(response, "不存在") && !strings.Contains(err.Error(), ErrOrderNotFound.Error()) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestQueryRefundStates(t *testing.T) {
	responses := []struct {
		xml   string
		state RefundState
	}{
		{`<PWBResponse><transactionName>QUERY_RETREAT_STATUS_RES</transactionName><code>6</code><description>等待审核中</description></PWBResponse>`, RefundPending},
		{`<PWBResponse><transactionName>QUERY_RETREAT_STATUS_RES</transactionName><code>2</code><description>审核未通过</description></PWBResponse>`, RefundRejected},
		{`<PWBResponse><transactionName>QUERY_RETREAT_STATUS_RES</transactionName><code>0</code><description>审核完成</description></PWBResponse>`, RefundCompleted},
	}
	for _, tc := range responses {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.xml) }))
		c := Client{Config: Config{Endpoint: s.URL, CorpCode: "c", Username: "u", PrivateKey: "k"}, HTTP: s.Client()}
		r, _, err := c.QueryRefund(context.Background(), "BATCH")
		s.Close()
		if err != nil || r.State != tc.state {
			t.Fatalf("state=%+v err=%v", r, err)
		}
	}
}
