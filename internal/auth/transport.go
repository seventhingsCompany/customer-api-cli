package auth

import (
	"io"
	"net/http"
)

// Transport injects the session's access token into authenticated requests
// and retries once with a renewed token when the server answers 401.
type Transport struct {
	Base    http.RoundTripper
	Session *Session
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") == "" {
		return t.Base.RoundTrip(req) // unauthenticated SDK call
	}
	ctx := req.Context()
	token, err := t.Session.Token(ctx)
	if err != nil {
		return nil, err
	}

	r := withToken(req, token)
	resp, err := t.Base.RoundTrip(r)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return resp, nil // cannot replay
	}

	newToken, renewErr := t.Session.Renew(ctx, token)
	if renewErr != nil {
		return resp, nil // surface the original 401
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	r = withToken(req, newToken)
	if req.Body != nil && req.Body != http.NoBody {
		if r.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	return t.Base.RoundTrip(r)
}

func withToken(req *http.Request, token string) *http.Request {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}
