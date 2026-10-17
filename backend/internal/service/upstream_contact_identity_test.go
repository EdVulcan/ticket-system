package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm/logger"
	"ticket-backend/internal/model"
	"ticket-backend/internal/utils"
	"ticket-backend/internal/zyb"
)

func seedUpstreamContactOrder(t *testing.T, endpoint string, required bool) model.Order {
	t.Helper()
	f := seedXiaohongshuOrderRegressionFixture(t)
	if err := model.DB.Model(&model.Product{}).Where("id = ?", f.mapping.ProductID).Update("code_mode", "order").Error; err != nil {
		t.Fatal(err)
	}
	updateMiniappContactPolicy(t, f.tenantID, f.mapping.ProductID, required)
	supply := UpstreamSupplyService{}
	conn, err := supply.CreateConnection(f.tenantID, 0, "admin", UpstreamConnectionInput{Name: "contact upstream", Provider: "zhiyoubao", Endpoint: endpoint, CorpCode: "corp", Username: "user", PrivateKey: "test-key", Environment: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	if err := supply.SetConnectionStatus(f.tenantID, conn.ID, 0, "admin", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := supply.SetProduct(f.tenantID, f.mapping.ProductID, 0, "admin", ProductSupplyInput{Enabled: true, UpstreamConnectionID: conn.ID, ExternalProductCode: "CONTACT-GOODS"}); err != nil {
		t.Fatal(err)
	}
	service, server := newXiaohongshuOrderRegressionService(t)
	defer server.Close()
	result, err := service.CreateXiaohongshuOrder(context.Background(), &f.customer, miniappContactTestInput(f.mapping.ID, "upstream-contact"))
	if err != nil {
		t.Fatal(err)
	}
	if err := (&OrderService{}).MarkAsPaid(result.OrderNo, f.tenantID); err != nil {
		t.Fatal(err)
	}
	var order model.Order
	if err := model.DB.Preload("Items.Tickets").Where("order_no = ? AND tenant_id = ?", result.OrderNo, f.tenantID).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	return order
}

func TestUpstreamContactIdentitySnapshotRecoveryAndIsolation(t *testing.T) {
	for _, tc := range []struct {
		name                                                               string
		required, saved, attempted, foreign, lostLease, mismatch, complete bool
		wantSends, wantQueries                                             int32
	}{
		{name: "non-real-name order", wantSends: 1},
		{name: "unattempted old request needs review", required: true, saved: true},
		{name: "existing complete request", required: true, saved: true, complete: true, wantSends: 1},
		{name: "attempted old request only queries", required: true, saved: true, attempted: true, wantQueries: 1},
		{name: "wrong snapshot identity", required: true, saved: true, mismatch: true},
		{name: "foreign sales tenant", required: true, foreign: true},
		{name: "lost initial lease", required: true, lostLease: true},
		{name: "lost old-request lease", required: true, saved: true, lostLease: true},
		{name: "lost complete-request lease", required: true, saved: true, complete: true, lostLease: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sends, queries atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				xml := r.Form.Get("xmlMsg")
				if strings.Contains(xml, "SEND_CODE_REQ") {
					sends.Add(1)
					if tc.required && !strings.Contains(xml, "<certificateNo>"+testMiniappContactID+"</certificateNo>") {
						t.Error("required certificate not sent")
					}
					if !tc.required && !strings.Contains(xml, "<certificateNo></certificateNo>") {
						t.Error("unnecessary identity sent")
					}
				} else if strings.Contains(xml, "QUERY_ORDER_NEW_REQ") {
					queries.Add(1)
				}
				// Providers may echo the full or reformatted request in an error.
				fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>6</code><description>证件错误 %s，110105 19491231 002X</description></PWBResponse>`, testMiniappContactID)
			}))
			defer server.Close()
			order := seedUpstreamContactOrder(t, server.URL, tc.required)
			var snapshot model.OrderItemSupplySnapshot
			if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
				t.Fatal(err)
			}
			lease := time.Now().Truncate(time.Microsecond)
			updates := map[string]interface{}{"locked_at": lease}
			if tc.saved {
				item := order.Items[0]
				request := zyb.SendCodeRequest{ThirdPartyOrderCode: order.OrderNo, ChildOrderCode: fmt.Sprintf("%s_%d", order.OrderNo, item.ID), ContactName: order.ContactName, ContactMobile: order.ContactPhone, GoodsCode: "CONTACT-GOODS", GoodsName: item.ProductName, Quantity: 3, PriceCents: 1, VisitDate: order.CreatedAt.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02") + " 00:00:00"}
				if tc.mismatch {
					request.CertificateNo = contactIDWithCheckDigit("11010519500101002")
				}
				if tc.complete {
					request.CertificateNo = testMiniappContactID
				}
				encoded, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				cipher, err := utils.EncryptAES(string(encoded))
				if err != nil {
					t.Fatal(err)
				}
				updates["request_payload_ciphertext"] = cipher
			}
			if tc.attempted {
				updates["issue_attempted_at"] = lease
			}
			if err := model.DB.Model(&snapshot).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.DB.First(&snapshot, snapshot.ID).Error; err != nil {
				t.Fatal(err)
			}
			original := snapshot.RequestPayloadCiphertext
			if tc.foreign {
				snapshot.SalesTenantID += 999
			}
			if tc.lostLease {
				wrongLease := lease.Add(-time.Second)
				snapshot.LockedAt = &wrongLease
			}
			worker := contactUpstreamWorker(server)
			err := worker.processSnapshot(context.Background(), &snapshot)
			if err == nil {
				t.Fatal("expected provider failure or refused request")
			}
			if tc.name == "unattempted old request needs review" && !strings.Contains(err.Error(), "普通重试不能补齐") {
				t.Fatal("historical missing-identity request needs an actionable review message")
			}
			if tc.required && (strings.Contains(err.Error(), testMiniappContactID) || strings.Contains(err.Error(), "19491231")) {
				t.Fatal("provider error exposed contact identity")
			}
			if sends.Load() != tc.wantSends || queries.Load() != tc.wantQueries {
				t.Fatalf("sends=%d queries=%d", sends.Load(), queries.Load())
			}
			var stored model.OrderItemSupplySnapshot
			if err := model.DB.First(&stored, snapshot.ID).Error; err != nil {
				t.Fatal(err)
			}
			if (tc.saved || tc.attempted || tc.lostLease || tc.foreign || tc.mismatch) && stored.RequestPayloadCiphertext != original {
				t.Fatal("original request changed on unsafe recovery path")
			}
			if tc.required && !tc.saved && tc.wantSends == 1 && stored.RequestPayloadCiphertext == original {
				t.Fatal("unattempted request missing encrypted contact enrichment")
			}
		})
	}
}

func TestUpstreamContactIdentityFailureRecordsArePrivate(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.Contains(r.Form.Get("xmlMsg"), "SEND_CODE_REQ") {
			sends.Add(1)
		}
		fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>6</code><description>%s / 110105 19491231 002X</description></PWBResponse>`, testMiniappContactID)
	}))
	defer server.Close()
	order := seedUpstreamContactOrder(t, server.URL, true)
	var logs bytes.Buffer
	previousLogger := model.DB.Logger
	model.DB.Logger = logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Info})
	t.Cleanup(func() { model.DB.Logger = previousLogger })
	worker := contactUpstreamWorker(server)
	if n, err := worker.ProcessTasks(context.Background(), time.Now(), 1); err != nil || n != 1 {
		t.Fatalf("processed=%d err=%v", n, err)
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.LastError == "" || strings.Contains(snapshot.LastError, testMiniappContactID) || strings.Contains(snapshot.LastError, "19491231") || strings.Contains(logs.String(), testMiniappContactID) {
		t.Fatal("contact identity exposed in task error or SQL log")
	}
	if _, err := worker.ProcessTasks(context.Background(), snapshot.NextAttemptAt.Add(time.Second), 1); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 1 {
		t.Fatal("ambiguous provider response repeated purchase")
	}
}

type contactRefundRejection struct{ upstreamRefundFake }

func (*contactRefundRejection) QueryRefund(context.Context, string) (*zyb.RefundResult, []byte, error) {
	return &zyb.RefundResult{Rejected: true, Description: testMiniappContactID + " / 110105 19491231 002X"}, nil, nil
}

func TestUpstreamContactIdentityRefundRejectionIsPrivate(t *testing.T) {
	order := seedUpstreamContactOrder(t, "https://supplier.example/api", true)
	refund := model.Refund{TenantID: order.TenantID, OrderNo: order.OrderNo, RefundNo: "CONTACT-REFUND", IdempotencyKey: "contact-refund", Method: "xiaohongshu", Status: "pending", Amount: 0.03, AmountCents: 3}
	if err := model.DB.Create(&refund).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.Ticket{}).Where("order_id = ?", order.ID).Update("pending_refund_id", refund.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB.Model(&model.OrderItemSupplySnapshot{}).Where("order_id = ?", order.ID).Updates(map[string]interface{}{"issue_attempted_at": time.Now(), "cancel_batch_no": "CONTACT-BATCH", "cancel_status": "submitted", "refund_id": refund.ID}).Error; err != nil {
		t.Fatal(err)
	}
	service := RefundService{NewUpstreamRefundClient: func(*model.UpstreamConnection) (UpstreamRefundClient, error) { return &contactRefundRejection{}, nil }}
	err := service.processUpstreamRefundCancellation(context.Background(), &refund)
	if !errors.Is(err, ErrUpstreamRefundRejected) || strings.Contains(err.Error(), testMiniappContactID) {
		t.Fatal("refund classification lost or private data exposed")
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if snapshot.CancelStatus != "failed" || snapshot.LastError == "" || strings.Contains(snapshot.LastError, testMiniappContactID) || strings.Contains(snapshot.LastError, "19491231") {
		t.Fatal("refund rejection persisted provider identity echo")
	}
}

func TestUpstreamContactIdentityRejectsInvalidCiphertextAndPreservesErrorTypes(t *testing.T) {
	for _, value := range []string{"", "not-encrypted"} {
		_, err := upstreamOrderContactCertificate(&model.Order{Channel: "xiaohongshu", ContactRealNameRequired: true, ContactIDCardCiphertext: value})
		if err == nil {
			t.Fatal("missing or damaged contact identity accepted")
		}
	}
	for _, id := range []string{"not-an-id", "110105194912310021"} {
		cipher, err := utils.EncryptAES(id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = upstreamOrderContactCertificate(&model.Order{Channel: "xiaohongshu", ContactRealNameRequired: true, ContactIDCardCiphertext: cipher, ContactName: "联系人", ContactPhone: "13800138000"})
		if err == nil || strings.Contains(err.Error(), id) {
			t.Fatal("invalid identity accepted or exposed")
		}
	}
	order := &model.Order{ContactRealNameRequired: true}
	source := &zyb.DeferredError{RetryAt: time.Now().Add(time.Minute), Cause: errors.New(testMiniappContactID)}
	protected := protectUpstreamContactError(order, source)
	var deferred *zyb.DeferredError
	if !errors.As(protected, &deferred) || strings.Contains(protected.Error(), testMiniappContactID) {
		t.Fatal("private error lost retry type or exposed identity")
	}
}

func contactUpstreamWorker(server *httptest.Server) UpstreamSupplyWorker {
	return UpstreamSupplyWorker{NewClient: func(c model.UpstreamConnection) (*zyb.Client, error) {
		return &zyb.Client{Config: zyb.Config{Endpoint: c.Endpoint, CorpCode: c.CorpCode, Username: c.Username, PrivateKey: "test-key"}, HTTP: server.Client()}, nil
	}, Decoder: upstreamTestDecoder(func(context.Context, []byte) (string, error) { return "CONTACT-TICKET-CODE", nil })}
}

func TestUpstreamContactIdentitySentOnceAndImageRetryUsesSnapshot(t *testing.T) {
	var sends, images atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		xml := r.Form.Get("xmlMsg")
		if strings.Contains(xml, "SEND_CODE_REQ") {
			sends.Add(1)
			if strings.Count(xml, "<certificateNo>"+testMiniappContactID+"</certificateNo>") != 1 || strings.Contains(xml, "<credentials>") || !strings.Contains(xml, "<quantity>3</quantity>") {
				t.Error("three-ticket order did not send one order-level contact identity")
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_RES</transactionName><code>0</code><orderResponse><order><orderCode>CONTACT-PROVIDER</orderCode><ticketOrders><ticketOrder><orderCode>CONTACT-SUB</orderCode><goodsCode>CONTACT-GOODS</goodsCode><quantity>3</quantity><price>0.01</price><totalPrice>0.03</totalPrice><occDate>%s</occDate></ticketOrder></ticketOrders></order></orderResponse></PWBResponse>`, time.Now().Format("2006-01-02"))
			return
		}
		if strings.Contains(xml, "SEND_CODE_IMG_REQ") {
			if images.Add(1) == 1 {
				http.Error(w, "temporary", 503)
				return
			}
			fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>0</code><img>%s</img></PWBResponse>`, base64.StdEncoding.EncodeToString([]byte("qr")))
			return
		}
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	order := seedUpstreamContactOrder(t, server.URL, true)
	updateMiniappContactPolicy(t, order.TenantID, order.Items[0].ProductID, false)
	worker := contactUpstreamWorker(server)
	if _, err := worker.ProcessTasks(context.Background(), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	var snapshot model.OrderItemSupplySnapshot
	if err := model.DB.Where("order_id = ?", order.ID).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot.RequestPayloadCiphertext, testMiniappContactID) {
		t.Fatal("plaintext identity in stored request")
	}
	plain, err := utils.DecryptAES(snapshot.RequestPayloadCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	var request zyb.SendCodeRequest
	if err := json.Unmarshal([]byte(plain), &request); err != nil {
		t.Fatal(err)
	}
	if request.CertificateNo != testMiniappContactID || len(request.Credentials) != 0 {
		t.Fatal("encrypted request missing contact snapshot")
	}
	original := snapshot.RequestPayloadCiphertext
	if _, err := worker.ProcessTasks(context.Background(), snapshot.NextAttemptAt.Add(time.Second), 1); err != nil {
		t.Fatal(err)
	}
	if err := model.DB.First(&snapshot, snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 1 || snapshot.IssueStatus != "ready" || snapshot.RequestPayloadCiphertext != original {
		t.Fatalf("retry changed request or repeated purchase: sends=%d status=%s", sends.Load(), snapshot.IssueStatus)
	}
}
