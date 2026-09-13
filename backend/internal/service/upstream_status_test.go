package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
)

func TestUpstreamReturnedTicketsAreNotUsed(t *testing.T) {
	order, _ := seedReadyUpstreamRefund(t)
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	model.DB.Model(&snapshot).Update("cancel_status", "submitted")
	var recordQueries int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("xmlMsg") == "" {
			t.Error("missing request")
		}
		if r.Form.Get("xmlMsg") != "" {
			if strings.Contains(r.Form.Get("xmlMsg"), "QUERY_SUB_ORDER_CHECK_RECORD_REQ") {
				recordQueries++
				http.Error(w, "unexpected records query", 500)
				return
			}
		}
		fmt.Fprintf(w, `<PWBResponse><transactionName>CHECK_STATUS_QUERY_RES</transactionName><code>0</code><subOrders><subOrder><needCheckNum>3</needCheckNum><alreadyCheckNum>0</alreadyCheckNum><returnNum>3</returnNum><checkStatus>checked</checkStatus><orderCode>%s</orderCode></subOrder></subOrders></PWBResponse>`, upstreamThirdPartyChild(order.OrderNo, snapshot.OrderItemID))
	}))
	defer server.Close()
	client := &zyb.Client{Config: zyb.Config{Endpoint: server.URL, CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}
	if err := (&UpstreamSupplyWorker{}).syncStatus(context.Background(), client, &snapshot, &order); err != nil {
		t.Fatal(err)
	}
	rows, err := GetUpstreamOrderView(order.TenantID, order.OrderNo)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].ProviderStatus != "refunded" || rows[0].CancelStatus != "succeeded" || rows[0].ProviderFirstUsedAt != nil || recordQueries != 0 {
		t.Fatalf("wrong returned projection: %+v record queries=%d", rows[0], recordQueries)
	}
	model.DB.First(&snapshot, snapshot.ID)
	if snapshot.CancelStatus != "submitted" {
		t.Fatal("status query completed a financial cancellation workflow")
	}
}

func TestUpstreamUsageStatusCounts(t *testing.T) {
	for _, tc := range []struct {
		name, checked, returned, raw, want string
		used                               bool
	}{
		{"returned", "0", "3", "checked", "refunded", false},
		{"part returned", "0", "1", "checked", "partial_refunded", false},
		{"unused", "0", "0", "un_check", "un_check", false},
		{"used", "3", "0", "checked", "checked", true},
		{"part used", "1", "0", "checking", "checking", true},
		{"refunded after use", "3", "3", "checked", "refunded", true},
		{"ambiguous", "0", "0", "checked", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, used, err := upstreamUsageStatus([]zyb.CheckStatusSubOrder{{NeedCheckNum: "3", AlreadyCheckNum: tc.checked, ReturnNum: tc.returned, CheckStatus: tc.raw}})
			if err != nil || status != tc.want || used != tc.used {
				t.Fatalf("%s %v %v", status, used, err)
			}
		})
	}
}

func TestUpstreamConfirmedCancellationCorrectsOldDisplayWithoutOverridingSpecialRefund(t *testing.T) {
	order, _ := seedReadyUpstreamRefund(t)
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	for _, state := range []string{"succeeded", "override"} {
		if err := model.DB.Model(&snapshot).Updates(map[string]interface{}{"provider_status": "checked", "cancel_status": state}).Error; err != nil {
			t.Fatal(err)
		}
		rows, err := GetUpstreamOrderView(order.TenantID, order.OrderNo)
		if err != nil {
			t.Fatal(err)
		}
		want := "refunded"
		if state == "override" {
			want = "checked"
		}
		if rows[0].ProviderStatus != want || rows[0].CancelStatus != state {
			t.Fatalf("unexpected state: %+v", rows[0])
		}
	}
}
