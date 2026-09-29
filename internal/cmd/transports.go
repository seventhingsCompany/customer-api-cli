package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// debugTransport logs requests to stderr. Headers are never printed, so the
// Authorization token cannot leak.
type debugTransport struct {
	Base http.RoundTripper
	Err  io.Writer
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	_, _ = fmt.Fprintf(t.Err, "→ %s %s\n", req.Method, req.URL)
	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		_, _ = fmt.Fprintf(t.Err, "← error: %v (%s)\n", err, time.Since(start).Round(time.Millisecond))
		return nil, err
	}
	_, _ = fmt.Fprintf(t.Err, "← %s (%s)\n", resp.Status, time.Since(start).Round(time.Millisecond))
	return resp, nil
}

// dryRunTransport prints write requests instead of sending them. Reads pass
// through so commands that look things up first still work.
type dryRunTransport struct {
	Base http.RoundTripper
	Out  io.Writer
}

func (t *dryRunTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return t.Base.RoundTrip(req)
	}
	entry := map[string]any{"dry_run": true, "method": req.Method, "url": req.URL.String()}
	if req.Body != nil && req.Body != http.NoBody {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var parsed any
		if strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") && json.Unmarshal(b, &parsed) == nil {
			entry["body"] = parsed
		} else {
			entry["body_bytes"] = len(b)
			entry["content_type"] = req.Header.Get("Content-Type")
		}
	}
	enc := json.NewEncoder(t.Out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entry); err != nil {
		return nil, err
	}

	// Synthesize a success so the SDK call returns normally.
	h := http.Header{}
	h.Set("Location", req.URL.Path+"/dry-run")
	h.Set("Location-Id", "0")
	h.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: http.StatusCreated,
		Status:     "201 Created",
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
		Request:    req,
	}, nil
}
