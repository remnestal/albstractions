package throttle

import (
	"context"
	"net/http"
)

// RoundTripper is an http.RoundTripper that paces outbound requests using
// [Throttle.Do].
//
// Requests are spaced by the [Throttle]'s schedule; use [WithMaxInflight] to
// control how many may be in flight concurrently (default: 1).
type RoundTripper struct {
	next http.RoundTripper
	t    *Throttle
}

// NewRoundTripper returns a [RoundTripper] that paces requests through next
// using s.
//
// If next is nil, http.DefaultTransport is used. Options are forwarded to the
// underlying [Throttle].
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
//
// If req's context ends before the request is forwarded, RoundTrip closes
// req.Body and returns the context error.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	forwarded := false
	err := rt.t.Do(req.Context(), func(context.Context) error {
		forwarded = true
		var innerErr error
		resp, innerErr = rt.next.RoundTrip(req)
		return innerErr
	})
	// http.RoundTripper must close the body on every path. Once forwarded, the
	// inner transport owns it.
	if !forwarded && req.Body != nil {
		_ = req.Body.Close()
	}
	return resp, err
}
