package zyb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestTicketImagesFallsBackToDocumentedURLEndpoint(t *testing.T) {
	var imageRequests, urlRequests int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(body))
		xml := values.Get("xmlMsg")
		switch {
		case strings.Contains(xml, "SEND_CODE_IMG_REQ"):
			imageRequests++
			_, _ = io.WriteString(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>6</code><description>失败: 履约-查询发码图片返回空</description><img></img></PWBResponse>`)
		case strings.Contains(xml, "QUERY_IMG_URL_REQ"):
			urlRequests++
			fmt.Fprintf(w, `<PWBResponse><transactionName>QUERY_IMG_URL_RES</transactionName><code>0</code><img>%s/boss/showCheckNo.htm?token</img></PWBResponse>`, server.URL)
		default:
			t.Fatalf("unexpected xml=%s", xml)
		}
	}))
	defer server.Close()

	client := Client{Config: Config{Endpoint: server.URL + "/boss/service/code.htm", CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}
	artifacts, _, err := client.TicketImages(context.Background(), "LOCAL")
	if err != nil {
		t.Fatal(err)
	}
	if imageRequests != 1 || urlRequests != 1 || len(artifacts) != 1 || artifacts[0].Kind != "url" {
		t.Fatalf("requests=%d/%d artifacts=%+v", imageRequests, urlRequests, artifacts)
	}
}

func TestResolveTicketPageUsesSessionAndDownloadsEveryGIF(t *testing.T) {
	const (
		orderDetailID = "2609140005215345446"
		gmCode        = "TOKEN"
	)
	payloads := map[string]string{"8385416242": "SUPPLIER-PERSON-2", "8385457442": "SUPPLIER-PERSON-1"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/boss/showCheckNo.htm":
			http.SetCookie(w, &http.Cookie{Name: "zyb-session", Value: "session-1", Path: "/"})
			_, _ = io.WriteString(w, `<html><body><div data-id="2609140005215345446" data-gmcode="TOKEN"></div></body></html>`)
		case r.Method == http.MethodPost && r.URL.Path == "/boss/gm/code/searchData.htm":
			if _, err := r.Cookie("zyb-session"); err != nil {
				t.Errorf("search request lost page cookie: %v", err)
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("orderDetailId") != orderDetailID || r.Form.Get("gmCode") != gmCode {
				t.Errorf("search form=%v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json;charset=UTF-8")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"isSuccess": true,
				"result": []map[string]string{
					{"assistCheckNo": "8385416242", "gmCode": gmCode},
					{"assistCheckNo": "8385457442", "gmCode": gmCode},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/boss/gmCheckCode.htm":
			if _, err := r.Cookie("zyb-session"); err != nil {
				t.Errorf("image request lost page cookie: %v", err)
			}
			parts := strings.Split(r.URL.RawQuery, "@@")
			if len(parts) != 2 || parts[0] != gmCode {
				t.Errorf("unexpected QR URL %q", r.URL.RawQuery)
				return
			}
			data, err := encodeQRCodeGIF(t, payloads[parts[1]])
			if err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("Content-Type", "image/gif; charset=utf-8")
			_, _ = w.Write(data)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := Client{Config: Config{Endpoint: server.URL + "/boss/service/code.htm", CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}
	resolved, err := client.ResolveTicketArtifacts(context.Background(), []*Artifact{{Kind: "url", Value: server.URL + "/boss/showCheckNo.htm?token"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != len(payloads) {
		t.Fatalf("resolved=%d, want %d", len(resolved), len(payloads))
	}
	var got []string
	for _, artifact := range resolved {
		if artifact.Kind != "image" || artifact.MimeType != "image/gif" {
			t.Fatalf("artifact=%+v", artifact)
		}
		data, err := decodeArtifact(artifact)
		if err != nil {
			t.Fatal(err)
		}
		codes, err := (QRCodeDecoder{}).DecodeAll(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, codes...)
	}
	sort.Strings(got)
	want := []string{"SUPPLIER-PERSON-1", "SUPPLIER-PERSON-2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("codes=%v want=%v", got, want)
	}
}

func TestResolveTicketArtifactsRejectsCrossHostAndRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "unexpected") }))
	defer other.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boss/showCheckNo.htm" {
			w.Header().Set("Location", other.URL+"/redirected")
			w.WriteHeader(http.StatusFound)
			return
		}
		t.Errorf("unexpected provider request: %s", r.URL.Path)
	}))
	defer provider.Close()
	client := Client{Config: Config{Endpoint: provider.URL + "/boss/service/code.htm", CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: provider.Client()}
	if _, err := client.ResolveTicketArtifacts(context.Background(), []*Artifact{{Kind: "url", Value: other.URL + "/boss/showCheckNo.htm"}}); err == nil || !strings.Contains(err.Error(), "当前供应商主机") {
		t.Fatalf("cross-host error=%v", err)
	}
	if _, err := client.ResolveTicketArtifacts(context.Background(), []*Artifact{{Kind: "url", Value: provider.URL + "/boss/showCheckNo.htm"}}); err == nil || !strings.Contains(err.Error(), "跨主机重定向") {
		t.Fatalf("redirect error=%v", err)
	}
}

func TestResolveTicketArtifactsRejectsPrivateURLForPublicProvider(t *testing.T) {
	client := Client{Config: Config{Endpoint: "https://ifdist.zhiyoubao.com/boss/service/code.htm", CorpCode: "C", Username: "U", PrivateKey: "K"}}
	if _, err := client.ResolveTicketArtifacts(context.Background(), []*Artifact{{Kind: "url", Value: "http://127.0.0.1/boss/showCheckNo.htm"}}); err == nil || !strings.Contains(err.Error(), "内网地址") {
		t.Fatalf("private URL error=%v", err)
	}
}

func TestTicketPageImageURLRejectsUnsafeProviderFields(t *testing.T) {
	page, _ := url.Parse("https://ifdist.zhiyoubao.com/boss/showCheckNo.htm")
	imageURL, err := ticketPageImageURL(page, ticketPageSearchRow{ThirdOfflineCheck: "/boss/direct.gif"})
	if err != nil || imageURL.Path != "/boss/direct.gif" || imageURL.Host != page.Host {
		t.Fatalf("relative thirdOfflineCheck=%v err=%v", imageURL, err)
	}
	for _, row := range []ticketPageSearchRow{
		{GMCode: "TOKEN&evil", AssistCheckNo: "1"},
		{GMCode: "TOKEN", AssistCheckNo: "1#fragment"},
		{ThirdOfflineCheck: "https://evil.example/image.gif"},
	} {
		if _, err := ticketPageImageURL(page, row); err == nil {
			t.Fatalf("unsafe row accepted: %+v", row)
		}
	}
}

func decodeArtifact(artifact *Artifact) ([]byte, error) {
	return base64.StdEncoding.DecodeString(artifact.Value)
}

func encodeQRCodeGIF(t *testing.T, payload string) ([]byte, error) {
	t.Helper()
	matrix, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, 240, 240, nil)
	if err != nil {
		return nil, err
	}
	img := image.NewGray(image.Rect(0, 0, 240, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 240; x++ {
			if matrix.Get(x, y) {
				img.SetGray(x, y, color.Gray{Y: 0})
			} else {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := gif.Encode(&encoded, img, nil); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
