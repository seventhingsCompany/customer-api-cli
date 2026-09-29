// Package ratelimit provides an http.RoundTripper that keeps the CLI within
// the customer API rate limit and retries requests rejected with 429.
package ratelimit

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// DefaultPerMinute is the customer API's default rate limit.
	DefaultPerMinute = 200
	// DefaultBurst is the number of requests that may be sent back to back
	// before spacing kicks in.
	DefaultBurst = 10
	// DefaultMaxRetries is how often a request rejected with 429 is retried.
	DefaultMaxRetries = 3

	maxBackoff = 60 * time.Second
)

// Option configures a Transport.
type Option func(*Transport)

// WithBurst overrides DefaultBurst.
func WithBurst(n int) Option {
	return func(t *Transport) { t.burst = n }
}

// WithMaxRetries overrides DefaultMaxRetries.
func WithMaxRetries(n int) Option {
	return func(t *Transport) { t.maxRetries = n }
}

// WithNotify registers a callback invoked before the transport waits because
// the server answered 429. Used for --debug output and the TUI status bar.
func WithNotify(fn func(wait time.Duration, attempt int)) Option {
	return func(t *Transport) { t.notify = fn }
}

// Transport limits outgoing requests to a fixed rate and retries 429
// responses, honoring Retry-After. It is safe for concurrent use.
type Transport struct {
	base       http.RoundTripper
	limiter    *rate.Limiter // nil when limiting is disabled
	burst      int
	maxRetries int
	notify     func(time.Duration, int)
	sleep      func(context.Context, time.Duration) error

	mu         sync.Mutex
	pauseUntil time.Time // set on 429 so concurrent requests back off too
}

// New wraps base (http.DefaultTransport if nil). perMinute <= 0 disables the
// client-side limit but keeps 429 retries.
func New(base http.RoundTripper, perMinute int, opts ...Option) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	t := &Transport{
		base:       base,
		burst:      DefaultBurst,
		maxRetries: DefaultMaxRetries,
		sleep:      sleepCtx,
	}
	for _, opt := range opts {
		opt(t)
	}
	if perMinute > 0 {
		burst := max(min(t.burst, perMinute), 1)
		t.limiter = rate.NewLimiter(rate.Limit(float64(perMinute)/60), burst)
	}
	return t
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	for attempt := 0; ; attempt++ {
		if err := t.waitPause(ctx); err != nil {
			return nil, err
		}
		if t.limiter != nil {
			if err := t.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		r := req
		if attempt > 0 {
			var err error
			if r, err = rewind(req); err != nil {
				return nil, err
			}
		}

		resp, err := t.base.RoundTrip(r)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests ||
			attempt >= t.maxRetries || !replayable(req) {
			return resp, err
		}

		wait := retryAfter(resp.Header.Get("Retry-After"), attempt)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		t.setPause(wait)
		if t.notify != nil {
			t.notify(wait, attempt+1)
		}
	}
}

func (t *Transport) setPause(d time.Duration) {
	until := time.Now().Add(d)
	t.mu.Lock()
	if until.After(t.pauseUntil) {
		t.pauseUntil = until
	}
	t.mu.Unlock()
}

func (t *Transport) waitPause(ctx context.Context) error {
	t.mu.Lock()
	d := time.Until(t.pauseUntil)
	t.mu.Unlock()
	if d <= 0 {
		return nil
	}
	return t.sleep(ctx, d)
}

func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func rewind(req *http.Request) (*http.Request, error) {
	r := req.Clone(req.Context())
	if req.Body != nil && req.Body != http.NoBody {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	return r, nil
}

// retryAfter parses a Retry-After header (seconds or HTTP date), falling back
// to exponential backoff with jitter.
func retryAfter(header string, attempt int) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
			return min(time.Duration(secs)*time.Second, maxBackoff)
		}
		if at, err := http.ParseTime(header); err == nil {
			return min(max(time.Until(at), 0), maxBackoff)
		}
	}
	base := time.Second << attempt
	jitter := time.Duration(rand.Int64N(int64(base / 2)))
	return min(base+jitter, maxBackoff)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
