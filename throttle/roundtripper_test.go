package throttle_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/remnestal/albstractions/throttle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRoundTripper_spacing(t *testing.T) {
	t.Parallel()

	delay := 40 * time.Millisecond
	srv := newTestServer(t)

	var (
		mu    sync.Mutex
		times []time.Time
	)
	rt := throttle.NewRoundTripper(
		roundTripperFn(func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		constSchedule(delay),
		throttle.WithMaxInflight(throttle.Unbounded),
	)
	client := &http.Client{Transport: rt}

	const n = 4
	for range n {
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	require.Len(t, times, n)
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		assert.GreaterOrEqual(t, gap, delay-5*time.Millisecond,
			"gap between call %d and %d should be >= %v", i-1, i, delay)
	}
}

func TestRoundTripper_contextCancel(t *testing.T) {
	t.Parallel()

	var called atomic.Int32
	rt := throttle.NewRoundTripper(
		roundTripperFn(func(_ *http.Request) (*http.Response, error) {
			called.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		constSchedule(10*time.Second),
		throttle.WithMaxInflight(throttle.Unbounded),
	)
	client := &http.Client{Transport: rt}
	srv := newTestServer(t)

	// First request fires immediately.
	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	// Second request must wait 10s — cancel it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	assert.Error(t, err)
	assert.Equal(t, int32(1), called.Load(), "inner transport should only be called once")
}

func TestRoundTripper_concurrentCallersSerialised(t *testing.T) {
	t.Parallel()

	delay := 20 * time.Millisecond
	rt := throttle.NewRoundTripper(
		roundTripperFn(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		constSchedule(delay),
	)
	client := &http.Client{Transport: rt}
	srv := newTestServer(t)

	const n = 5
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		}()
	}
	wg.Wait()
	assert.GreaterOrEqual(t, time.Since(start), time.Duration(n-1)*delay)
}

func TestRoundTripper_withMaxInflight(t *testing.T) {
	t.Parallel()

	const cap = 2
	pt := &peakTracker{}
	rt := throttle.NewRoundTripper(
		roundTripperFn(func(_ *http.Request) (*http.Response, error) {
			pt.enter()
			time.Sleep(30 * time.Millisecond)
			pt.exit()
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		constSchedule(0),
		throttle.WithMaxInflight(cap),
	)
	client := &http.Client{Transport: rt}
	srv := newTestServer(t)

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, pt.peak, int64(cap))
}

// roundTripperFn adapts a function to http.RoundTripper.
type roundTripperFn func(*http.Request) (*http.Response, error)

func (f roundTripperFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
