package throttle_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/remnestal/albstractions/throttle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closeRecorder is a request body that records whether it was closed.
type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// testURL is the address every test requests. Each test injects a fake inner
// transport, so nothing is ever dialled.
const testURL = "http://example.test/"

func TestRoundTripper_spacing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		delay := 40 * time.Millisecond

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
			resp, err := client.Get(testURL)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		}

		require.Len(t, times, n)
		for i := 1; i < len(times); i++ {
			assert.Equal(t, delay, times[i].Sub(times[i-1]), "gap between call %d and %d", i-1, i)
		}
	})
}

func TestRoundTripper_contextCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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

		// First request fires immediately.
		resp, err := client.Get(testURL)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Second request must wait 10s — cancel it.
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
		require.NoError(t, err)

		_, err = client.Do(req)
		assert.Error(t, err)
		assert.Equal(t, int32(1), called.Load(), "inner transport should only be called once")
	})
}

func TestRoundTripper_concurrentCallersSerialised(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pt := &peakTracker{}
		release := make(chan struct{})
		rt := throttle.NewRoundTripper(
			roundTripperFn(func(_ *http.Request) (*http.Response, error) {
				pt.enter()
				<-release
				pt.exit()
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
			}),
			constSchedule(0), // only the default cap keeps the requests apart
		)
		client := &http.Client{Transport: rt}

		const n = 5
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				resp, err := client.Get(testURL)
				if assert.NoError(t, err) {
					assert.NoError(t, resp.Body.Close())
				}
			}()
		}
		// Every request is now inside the inner transport or blocked on a slot.
		synctest.Wait()
		assert.Equal(t, int64(1), pt.inside(), "only one request should be in flight by default")
		close(release)
		wg.Wait()
		assert.Equal(t, int64(1), pt.peak)
	})
}

func TestRoundTripper_withMaxInflight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const cap = 2
		pt := &peakTracker{}
		release := make(chan struct{})
		rt := throttle.NewRoundTripper(
			roundTripperFn(func(_ *http.Request) (*http.Response, error) {
				pt.enter()
				<-release
				pt.exit()
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
			}),
			constSchedule(0),
			throttle.WithMaxInflight(cap),
		)
		client := &http.Client{Transport: rt}

		const n = 8
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				resp, err := client.Get(testURL)
				if assert.NoError(t, err) {
					assert.NoError(t, resp.Body.Close())
				}
			}()
		}
		// Every request is now inside the inner transport or blocked on a slot.
		synctest.Wait()
		assert.Equal(t, int64(cap), pt.inside(), "cap requests should be in flight together")
		close(release)
		wg.Wait()
		assert.Equal(t, int64(cap), pt.peak)
	})
}

// roundTripperFn adapts a function to http.RoundTripper.
type roundTripperFn func(*http.Request) (*http.Response, error)

func (f roundTripperFn) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRoundTripper_RoundTrip(t *testing.T) {
	t.Parallel()

	// forwardingTransport closes each forwarded body, as a real transport
	// does, and counts the requests it receives.
	forwardingTransport := func(forwarded *atomic.Int64, release <-chan struct{}) http.RoundTripper {
		return roundTripperFn(func(req *http.Request) (*http.Response, error) {
			forwarded.Add(1)
			if req.Body != nil {
				_ = req.Body.Close()
			}
			if release != nil {
				<-release
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		})
	}

	t.Run("closes the request body when cancelled during the paced wait", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var forwarded atomic.Int64
			rt := throttle.NewRoundTripper(forwardingTransport(&forwarded, nil), constSchedule(time.Hour))

			// The first request passes immediately and pushes the next slot an hour out.
			first, err := http.NewRequestWithContext(t.Context(), http.MethodGet, testURL, nil)
			require.NoError(t, err)
			_, err = rt.RoundTrip(first)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			body := &closeRecorder{Reader: strings.NewReader("payload")}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, testURL, body)
			require.NoError(t, err)

			_, err = rt.RoundTrip(req)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.True(t, body.closed, "RoundTrip must close req.Body even when it never forwards the request")
			assert.Equal(t, int64(1), forwarded.Load(), "only the first request should reach the inner transport")
		})
	})

	t.Run("closes the request body when cancelled waiting for an in-flight slot", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var forwarded atomic.Int64
			release := make(chan struct{})
			rt := throttle.NewRoundTripper(forwardingTransport(&forwarded, release), constSchedule(0))

			// The first request holds the only in-flight slot until released.
			var wg sync.WaitGroup
			wg.Go(func() {
				first, err := http.NewRequestWithContext(t.Context(), http.MethodGet, testURL, nil)
				if assert.NoError(t, err) {
					_, err = rt.RoundTrip(first)
					assert.NoError(t, err)
				}
			})
			synctest.Wait()

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			body := &closeRecorder{Reader: strings.NewReader("payload")}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, testURL, body)
			require.NoError(t, err)

			_, err = rt.RoundTrip(req)
			close(release)
			wg.Wait()

			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.True(t, body.closed, "RoundTrip must close req.Body even when it never forwards the request")
			assert.Equal(t, int64(1), forwarded.Load(), "only the first request should reach the inner transport")
		})
	})
}
