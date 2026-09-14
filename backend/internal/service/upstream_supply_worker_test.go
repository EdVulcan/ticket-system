package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"ticket-backend/internal/model"
	"ticket-backend/internal/zyb"
	"time"
)

type upstreamTestDecoder func(context.Context, []byte) (string, error)

func (f upstreamTestDecoder) Decode(c context.Context, b []byte) (string, error) { return f(c, b) }

func seedUpstreamWorkerOrder(t *testing.T, endpoint string) model.Order {
	return seedUpstreamWorkerOrderWithMode(t, endpoint, 1, "")
}

func seedUpstreamWorkerOrderWithMode(t *testing.T, endpoint string, quantity int, codeMode string) model.Order {
	t.Helper()
	resetBusinessData(t)
	tenant, product := seedSellableProduct(t, "unlimited", 0)
	var p model.Product
	model.DB.First(&p, product)
	if codeMode != "" {
		if err := model.DB.Model(&p).Update("code_mode", codeMode).Error; err != nil {
			t.Fatal(err)
		}
	}
	var group model.RuleGroup
	model.DB.Where("rule_id = ?", p.RuleID).First(&group)
	if err := model.DB.Model(&group).Update("max_total_check_in", 2).Error; err != nil {
		t.Fatal(err)
	}
	second := model.CheckPoint{TenantID: tenant, ScenicAreaID: p.ScenicAreaID, Name: "第二区域"}
	if err := model.DB.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.RuleItem{GroupID: group.ID, CheckPointID: second.ID, MaxPerCheckIn: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Create(&model.Device{TenantID: tenant, ScenicAreaID: p.ScenicAreaID, Name: "第二区域设备", SerialNumber: fmt.Sprintf("UPSTREAM-SECOND-%d", tenant), Type: "gate", Status: "online", CheckPointID: &second.ID, AuthKeyCiphertext: encryptedDeviceKeyForTest(t, "test-device-key")}).Error; err != nil {
		t.Fatal(err)
	}
	svc := UpstreamSupplyService{}
	conn, err := svc.CreateConnection(tenant, 0, "admin", UpstreamConnectionInput{Name: "ZYB", Provider: "zhiyoubao", Endpoint: endpoint, CorpCode: "corp", Username: "user", PrivateKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetConnectionStatus(tenant, conn.ID, 0, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SetProduct(tenant, product, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: conn.ID, ExternalProductCode: "GOODS"}); err != nil {
		t.Fatal(err)
	}
	date := startOfDay(time.Now())
	order := model.Order{TenantID: tenant, Channel: "online", ContactName: "测试", ContactPhone: "13800000000", Items: []model.OrderItem{{ProductID: product, Quantity: quantity, UseDate: &date}}}
	if err = (&OrderService{}).Create(&order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestUpstreamWorkerImageRetryDoesNotReissue(t *testing.T) {
	var sends, images atomic.Int32
	date := time.Now().Format("2006-01-02")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		if strings.Contains(xml, "<transactionName>SEND_CODE_REQ</transactionName>") {
			sends.Add(1)
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>0</code><orderResponse><order><orderCode>ZYB-ORDER</orderCode><orderPrice>99.50</orderPrice><ticketOrders><ticketOrder><orderCode>ZYB-SUB</orderCode><goodsCode>GOODS</goodsCode><quantity>1</quantity><price>99.50</price><totalPrice>99.50</totalPrice><occDate>%s</occDate></ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`, date)
			return
		}
		if strings.Contains(xml, "SEND_CODE_IMG_REQ") {
			if images.Add(1) == 1 {
				http.Error(w, "temporary image failure", 503)
				return
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>0</code><img>%s</img></PWBResponse>`, base64.StdEncoding.EncodeToString([]byte("image")))
			return
		}
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	order := seedUpstreamWorkerOrder(t, server.URL)
	worker := UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: c.CorpCode, Username: c.Username, PrivateKey: "key"}, HTTP: server.Client()}, nil
	}, Decoder: upstreamTestDecoder(func(context.Context, []byte) (string, error) { return "SHARED-ZYB-CODE", nil })}
	now := time.Now()
	if n, err := worker.ProcessTasks(context.Background(), now, 1); err != nil || n != 0 {
		t.Fatalf("unpaid issued: %d %v", n, err)
	}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", IdempotencyKey: "upstream-paid"}
	if err := (&PaymentService{}).CreatePayment(order.TenantID, &payment); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessTasks(context.Background(), now, 1); err != nil {
		t.Fatal(err)
	}
	var snap model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snap).Error; err != nil {
		t.Fatal(err)
	}
	if snap.ProviderOrderCode != "ZYB-ORDER" || snap.IssueStatus != "pending" {
		t.Fatalf("identity not saved before image: %+v", snap)
	}
	if _, err := worker.ProcessTasks(context.Background(), snap.NextAttemptAt.Add(time.Second), 1); err != nil {
		t.Fatal(err)
	}
	var ticket model.Ticket
	model.DB.First(&ticket, order.Items[0].Tickets[0].ID)
	if ticket.TicketCode != "SHARED-ZYB-CODE" || ticket.Status != "unused" || sends.Load() != 1 || images.Load() != 2 {
		t.Fatalf("ticket=%+v send=%d images=%d", ticket, sends.Load(), images.Load())
	}
	var right model.TicketEntitlement
	model.DB.Where("ticket_id = ?", ticket.ID).First(&right)
	if right.TicketCode != ticket.TicketCode {
		t.Fatal("existing entitlement code not updated")
	}
	// Upstream use must not spend either local checkpoint's rights.
	if err := model.DB.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ?", order.ID).Update("provider_status", "checked").Error; err != nil {
		t.Fatal(err)
	}
	var devices []model.Device
	model.DB.Where("tenant_id = ?", order.TenantID).Order("id").Find(&devices)
	for _, d := range devices {
		if err := (&TicketService{}).Verify(ticket.TicketCode, *d.CheckPointID, d.ID, order.TenantID); err != nil {
			t.Fatalf("independent checkpoint failed: %v", err)
		}
	}
	model.DB.First(&ticket, ticket.ID)
	if ticket.CheckInCount != 2 {
		t.Fatalf("local count=%d", ticket.CheckInCount)
	}
}

func TestUpstreamWorkerResolvesOnePersonOneCodePage(t *testing.T) {
	var sends, imageRequests, urlRequests, searchRequests, qrRequests atomic.Int32
	date := time.Now().Format("2006-01-02")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/boss/showCheckNo.htm" {
			http.SetCookie(w, &http.Cookie{Name: "zyb-session", Value: "page-session", Path: "/"})
			fmt.Fprint(w, `<html><body><div data-id="DETAIL-1" data-gmcode="TOKEN-1"></div></body></html>`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/boss/gm/code/searchData.htm" {
			searchRequests.Add(1)
			if _, err := r.Cookie("zyb-session"); err != nil {
				t.Errorf("page session was not retained: %v", err)
			}
			_ = r.ParseForm()
			if r.Form.Get("orderDetailId") != "DETAIL-1" || r.Form.Get("gmCode") != "TOKEN-1" {
				t.Errorf("unexpected page search form: %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"isSuccess":true,"result":[{"assistCheckNo":"ASSIST-1","gmCode":"TOKEN-1"},{"assistCheckNo":"ASSIST-2","gmCode":"TOKEN-1"}]}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/boss/gmCheckCode.htm" {
			qrRequests.Add(1)
			if _, err := r.Cookie("zyb-session"); err != nil {
				t.Errorf("QR request lost page session: %v", err)
			}
			if r.URL.RawQuery != "TOKEN-1@@ASSIST-1" && r.URL.RawQuery != "TOKEN-1@@ASSIST-2" {
				t.Errorf("unexpected QR query: %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "image/gif")
			fmt.Fprintf(w, "gif-bytes-%s", r.URL.RawQuery)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method=%s path=%s", r.Method, r.URL.Path)
			return
		}
		_ = r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		switch {
		case strings.Contains(xml, "<transactionName>SEND_CODE_REQ</transactionName>"):
			sends.Add(1)
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>0</code><orderResponse><order><orderCode>ZYB-PAGE-ORDER</orderCode><orderPrice>199.00</orderPrice><ticketOrders><ticketOrder><orderCode>ZYB-PAGE-SUB</orderCode><goodsCode>GOODS</goodsCode><quantity>2</quantity><price>99.50</price><totalPrice>199.00</totalPrice><occDate>%s</occDate></ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`, date)
		case strings.Contains(xml, "SEND_CODE_IMG_REQ"):
			imageRequests.Add(1)
			fmt.Fprint(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>6</code><description>失败: 履约-查询发码图片返回空</description></PWBResponse>`)
		case strings.Contains(xml, "QUERY_IMG_URL_REQ"):
			urlRequests.Add(1)
			fmt.Fprintf(w, `<PWBResponse><transactionName>QUERY_IMG_URL_RES</transactionName><code>0</code><img>%s/boss/showCheckNo.htm?token</img></PWBResponse>`, server.URL)
		default:
			t.Errorf("unexpected XML request: %s", xml)
		}
	}))
	defer server.Close()

	order := seedUpstreamWorkerOrderWithMode(t, server.URL, 2, "ticket")
	worker := UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: c.CorpCode, Username: c.Username, PrivateKey: "key"}, HTTP: server.Client()}, nil
	}, Decoder: upstreamTestDecoder(func(_ context.Context, image []byte) (string, error) {
		if strings.Contains(string(image), "ASSIST-1") {
			return "ONE-PERSON-UPSTREAM-CODE-1", nil
		}
		return "ONE-PERSON-UPSTREAM-CODE-2", nil
	})}
	payment := model.Payment{OrderNo: order.OrderNo, Method: "cash", IdempotencyKey: "one-person-page"}
	if err := (&PaymentService{}).CreatePayment(order.TenantID, &payment); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var tickets []model.Ticket
	model.DB.Where("order_id = ?", order.ID).Order("id").Find(&tickets)
	if sends.Load() != 1 || imageRequests.Load() != 1 || urlRequests.Load() != 1 || searchRequests.Load() != 1 || qrRequests.Load() != 2 || len(tickets) != 2 || tickets[0].Status != "unused" || tickets[1].Status != "unused" || tickets[0].TicketCode != "ONE-PERSON-UPSTREAM-CODE-1" || tickets[1].TicketCode != "ONE-PERSON-UPSTREAM-CODE-2" {
		t.Fatalf("send=%d images=%d urls=%d searches=%d qrs=%d tickets=%+v", sends.Load(), imageRequests.Load(), urlRequests.Load(), searchRequests.Load(), qrRequests.Load(), tickets)
	}
}

func TestUpstreamFinishCannotReviveRefundedTicket(t *testing.T) {
	order := seedUpstreamWorkerOrder(t, "https://supplier.example/api")
	if err := model.DB.Model(&order).Update("status", "paid").Error; err != nil {
		t.Fatal(err)
	}
	var snap model.OrderItemSupplySnapshot
	model.DB.Where("order_id = ?", order.ID).First(&snap)
	at := time.Now()
	snap.LockedAt = &at
	if err := model.DB.Model(&snap).Update("locked_at", at).Error; err != nil {
		t.Fatal(err)
	}
	ticket := order.Items[0].Tickets[0]
	if err := model.DB.Model(&ticket).Update("status", "refunded").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&UpstreamSupplyWorker{}).finishIssue(&snap, "LATE-CODE"); err == nil {
		t.Fatal("late issuance revived refunded ticket")
	}
	model.DB.First(&ticket, ticket.ID)
	if ticket.Status != "refunded" || ticket.TicketCode == "LATE-CODE" {
		t.Fatal("refunded ticket changed")
	}
}
