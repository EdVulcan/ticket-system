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
	"time"
)

type deferUpstreamTransaction struct{ transaction string }

func (g deferUpstreamTransaction) Acquire(_ context.Context, transaction, _ string) (zyb.RequestPermit, error) {
	if g.transaction == "" || g.transaction == transaction {
		return nil, &zyb.DeferredError{RetryAt: time.Now().Add(time.Minute)}
	}
	return passUpstreamPermit{}, nil
}

type passUpstreamPermit struct{}

func (passUpstreamPermit) Start(context.Context) error                       { return nil }
func (passUpstreamPermit) Finish(context.Context, *zyb.RateLimitError) error { return nil }

func TestUpstreamDeferredIssuanceDoesNotRecordSendAttempt(t *testing.T) {
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "must not send", 500) }))
	defer server.Close()
	order := seedUpstreamWorkerOrder(t, server.URL)
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", IdempotencyKey: "queued-issue"}
	if err := (&PaymentService{}).CreatePayment(order.TenantID, &payment); err != nil {
		t.Fatal(err)
	}
	w := UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: "C", Username: "U", PrivateKey: "K", Gate: deferUpstreamTransaction{}}, HTTP: server.Client()}, nil
	}}
	if _, err := w.ProcessTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	if calls != 0 || snapshot.IssueAttemptedAt != nil || snapshot.SyncFailureCount != 0 || snapshot.NextAttemptAt == nil {
		t.Fatalf("deferral recorded a send: calls=%d snapshot=%+v", calls, snapshot)
	}
}

func TestUpstreamDeferredCancellationKeepsTicketsLockedWithoutSubmittedMarker(t *testing.T) {
	order, ticket := seedReadyUpstreamRefund(t)
	var sends int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		body := r.Form.Get("xmlMsg")
		switch {
		case strings.Contains(body, "QUERY_ORDER_NEW_REQ"):
			fmt.Fprint(w, `<PWBResponse><transactionName>QUERY_ORDER_NEW_RES</transactionName><code>0</code><order><orderCode>PROVIDER</orderCode><ticketOrders><ticketOrder><orderCode>SUB</orderCode><goodsCode>GOODS</goodsCode><quantity>1</quantity><returnNum>0</returnNum></ticketOrder></ticketOrders></order></PWBResponse>`)
		case strings.Contains(body, "CHECK_STATUS_QUERY_REQ"):
			fmt.Fprint(w, `<PWBResponse><transactionName>CHECK_STATUS_QUERY_RES</transactionName><code>0</code><subOrders><subOrder><orderCode>SUB</orderCode><needCheckNum>1</needCheckNum><alreadyCheckNum>0</alreadyCheckNum><returnNum>0</returnNum><checkStatus>un_check</checkStatus></subOrder></subOrders></PWBResponse>`)
		default:
			sends++
			http.Error(w, "mutation must stay queued", 500)
		}
	}))
	defer server.Close()
	svc := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: server.URL, CorpCode: "C", Username: "U", PrivateKey: "K", Gate: deferUpstreamTransaction{transaction: "SEND_CODE_CANCEL_NEW_REQ"}}, HTTP: server.Client()}, nil
	}}
	refund, err := svc.CreateCashRefund(order.TenantID, order.OrderNo, "queued-refund", 99.5, []string{ticket.TicketCode}, "取消")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProcessDigitalRefundTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snapshot)
	model.DB.First(&ticket, ticket.ID)
	var task model.DigitalRefundTask
	model.DB.Where("refund_id = ?", refund.ID).First(&task)
	if sends != 0 || snapshot.CancelStatus != "" || snapshot.CancelAttemptedAt != nil || ticket.PendingRefundID != refund.ID || task.Status != "pending" || task.AttemptCount != 0 {
		t.Fatalf("unsafe defer: sends=%d cancel=%s pending=%d task=%+v", sends, snapshot.CancelStatus, ticket.PendingRefundID, task)
	}
}
