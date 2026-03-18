package throttle

import (
	"context"
	"net/http"
)

// RoundTripper is an http.RoundTripper that paces outbound requests using Do.
//
// Requests are spaced by the Throttle's schedule; use WithMaxInflight to control
// how many may be in flight concurrently (default: 1).
type RoundTripper struct {
	next http.RoundTripper
	t    *Throttle
}

// NewRoundTripper returns a RoundTripper that paces requests through next using s.
// If next is nil, http.DefaultTransport is used. Options are forwarded to the
// underlying Throttle.
func NewRoundTripper(next http.RoundTripper, s Schedule, opts ...Option) *RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &RoundTripper{
		next: next,
		t:    New(s, opts...),
	}
}

// RoundTrip implements http.RoundTripper.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := rt.t.Do(req.Context(), func(ctx context.Context) error {
		var innerErr error
		resp, innerErr = rt.next.RoundTrip(req)
		return innerErr
	})
	return resp, err
}
