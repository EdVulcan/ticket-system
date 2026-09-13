package zyb

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTicketImagesPreservesEveryImageAndRejectsPartialResponse(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		count        int
	}{
		{"single", "<img>Zmlyc3Q=</img>", 1},
		{"multiple", "<img>Zmlyc3Q=</img><img>c2Vjb25k</img>", 2},
		{"alias", "<img></img><image>Zmlyc3Q=</image>", 1},
		{"partial", "<img>Zmlyc3Q=</img><img></img>", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `<PWBResponse><transactionName>SEND_CODE_IMG_RES</transactionName><code>0</code>%s</PWBResponse>`, tc.fields)
			}))
			defer server.Close()
			client := Client{Config: Config{Endpoint: server.URL, CorpCode: "C", Username: "U", PrivateKey: "K"}, HTTP: server.Client()}
			images, _, err := client.TicketImages(context.Background(), "LOCAL")
			if tc.count == 0 {
				if err == nil || len(images) != 0 {
					t.Fatal("partial images accepted")
				}
				return
			}
			if err != nil || len(images) != tc.count {
				t.Fatalf("count=%d err=%v", len(images), err)
			}
			first, _ := base64.StdEncoding.DecodeString(images[0].Value)
			if string(first) != "first" {
				t.Fatal("first image changed")
			}
			if tc.count == 2 {
				second, _ := base64.StdEncoding.DecodeString(images[1].Value)
				if string(second) != "second" {
					t.Fatal("second image lost")
				}
				if _, _, err := client.TicketImage(context.Background(), "LOCAL"); err == nil {
					t.Fatal("single-image caller silently lost a code")
				}
			}
		})
	}
}
