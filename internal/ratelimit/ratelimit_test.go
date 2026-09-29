package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// noSleep replaces the transport's sleep so 429 tests run instantly.
func noSleep(t *Transport, slept *[]time.Duration) {
	t.sleep = func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return ctx.Err()
	}
	// Clear the pause so the real clock doesn't matter.
	t.mu.Lock()
	t.pauseUntil = time.Time{}
	t.mu.Unlock()
}

func TestSpacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	// 1200/min = one request every 50ms after the first.
	hc := &http.Client{Transport: New(nil, 1200, WithBurst(1))}
	start := time.Now()
	for range 5 {
		resp, err := hc.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if elapsed := time.Since(start); elapsed < 190*time.Millisecond {
		t.Fatalf("5 requests at 20/s took %v, want >= ~200ms", elapsed)
	}
}

func TestBurstNotDelayed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	hc := &http.Client{Transport: New(nil, DefaultPerMinute)}
	start := time.Now()
	for range DefaultBurst {
		resp, err := hc.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("burst of %d took %v", DefaultBurst, elapsed)
	}
}

func TestRetry429ReplaysBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Location", "/customer-api/v1/object/abc-123")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var slept []time.Duration
	var notified int
	tr := New(nil, 0, WithNotify(func(time.Duration, int) { notified++ }))
	tr.sleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		tr.mu.Lock()
		tr.pauseUntil = time.Time{}
		tr.mu.Unlock()
		return nil
	}

	c := client.NewWithToken(srv.URL, "tok", client.WithHTTPClient(&http.Client{Transport: tr}))
	uuid, err := c.ObjectCreate(context.Background(), map[string]any{"name": "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "abc-123" {
		t.Fatalf("uuid = %q", uuid)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("body not replayed: %q vs %q", bodies[0], bodies[1])
	}
	if len(slept) != 1 || slept[0] < time.Second || slept[0] > 2*time.Second {
		t.Fatalf("slept = %v, want ~2s from Retry-After", slept)
	}
	if notified != 1 {
		t.Fatalf("notified = %d", notified)
	}
}

func TestRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var slept []time.Duration
	tr := New(nil, 0, WithMaxRetries(2))
	noSleep(tr, &slept)

	c := client.NewWithToken(srv.URL, "tok", client.WithHTTPClient(&http.Client{Transport: tr}))
	_, err := c.ObjectGet(context.Background(), "x")
	if !models.IsRateLimited(err) {
		t.Fatalf("err = %v, want 429 APIError", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", calls.Load())
	}
}

type onceReader struct{ io.Reader }

func TestNonReplayableBodyNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: New(nil, 0)}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, onceReader{strings.NewReader("x")})
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls.Load() != 1 {
		t.Fatalf("status %d, calls %d", resp.StatusCode, calls.Load())
	}
}

func TestContextCancelWhileWaiting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	hc := &http.Client{Transport: New(nil, 1, WithBurst(1))} // 1/min
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if resp, err := hc.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error when context ends before a token is available")
	} else if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline") &&
		!strings.Contains(err.Error(), "would exceed context deadline") {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if d := retryAfter("5", 0); d != 5*time.Second {
		t.Errorf("seconds: %v", d)
	}
	if d := retryAfter("9999", 0); d != maxBackoff {
		t.Errorf("cap: %v", d)
	}
	date := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if d := retryAfter(date, 0); d < time.Second || d > 3*time.Second {
		t.Errorf("date: %v", d)
	}
	for attempt, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		d := retryAfter("", attempt)
		if d < want || d >= want+want/2 {
			t.Errorf("backoff attempt %d: %v", attempt, d)
		}
	}
}
