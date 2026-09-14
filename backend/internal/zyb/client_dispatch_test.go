package zyb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type recordingGate struct {
	mu     sync.Mutex
	start  bool
	finish *RateLimitError
}

func (g *recordingGate) Acquire(context.Context, string, string) (RequestPermit, error) {
	return &recordingPermit{gate: g}, nil
}

type recordingPermit struct {
	gate *recordingGate
}

func (p *recordingPermit) Start(context.Context) error {
	p.gate.mu.Lock()
	p.gate.start = true
	p.gate.mu.Unlock()
	return nil
}

func (p *recordingPermit) Finish(_ context.Context, limited *RateLimitError) error {
	p.gate.mu.Lock()
	p.gate.finish = limited
	p.gate.mu.Unlock()
	return nil
}

func TestClientStartsBeforeDoAndFinishesStructured429(t *testing.T) {
	gate := &recordingGate{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate.mu.Lock()
		started := gate.start
		gate.mu.Unlock()
		if !started {
			t.Error("HTTP request reached the server before permit.Start")
		}
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "not XML and no guessed provider text")
	}))
	defer server.Close()

	client := Client{Config: Config{Endpoint: server.URL, CorpCode: "corp", Username: "user", PrivateKey: "key", Timeout: time.Second, Gate: gate}, HTTP: server.Client()}
	_, raw, err := client.QueryOrder(context.Background(), "ORDER-1")
	if len(raw) == 0 {
		t.Fatal("429 body should remain available to the caller")
	}
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("error=%T %v, want RateLimitError", err, err)
	}
	if limited.StatusCode != http.StatusTooManyRequests || limited.RetryAfter != 2*time.Second {
		t.Fatalf("rate error=%+v", limited)
	}
	gate.mu.Lock()
	finished := gate.finish
	gate.mu.Unlock()
	if finished == nil || finished.RetryAfter != 2*time.Second {
		t.Fatalf("Finish received=%+v", finished)
	}
}

func TestParseRetryAfterSupportsHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	date := now.Add(3 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(date, now); got != 3*time.Second {
		t.Fatalf("retry-after date=%s, want 3s", got)
	}
	if got := parseRetryAfter("provider says wait", now); got != 0 {
		t.Fatalf("invalid retry-after=%s, want zero", got)
	}
}
