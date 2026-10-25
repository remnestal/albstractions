package throttle_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/remnestal/albstractions/throttle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// constSchedule is a Schedule that always returns the same duration.
type constSchedule time.Duration

func (c constSchedule) Next() time.Duration { return time.Duration(c) }

// countingSchedule counts how many times Next is called.
type countingSchedule struct {
	d     time.Duration
	calls atomic.Int64
}

func (c *countingSchedule) Next() time.Duration {
	c.calls.Add(1)
	return c.d
}

// waitingBucket is a Waiter that reserves tokens like schedule.TokenBucket over
// a golang.org/x/time/rate limiter: Next reserves one token and returns the
// wait from now until it is available, and Wait reserves one and blocks until
// then. Unlike the real limiter it never hands a cancelled token back.
type waitingBucket struct {
	mu       sync.Mutex
	interval time.Duration
	burst    int
	tat      time.Time // theoretical arrival time of the next token (GCRA)
	waits    atomic.Int64
}

func (b *waitingBucket) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.tat.Before(now) {
		b.tat = now
	}
	allowAt := b.tat.Add(-time.Duration(b.burst-1) * b.interval)
	b.tat = b.tat.Add(b.interval)
	return max(allowAt.Sub(now), 0)
}

func (b *waitingBucket) Wait(ctx context.Context) error {
	b.waits.Add(1)
	timer := time.NewTimer(b.Next())
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// peakTracker measures the peak number of concurrent goroutines inside fn.
type peakTracker struct {
	mu      sync.Mutex
	current int64
	peak    int64
}

func (p *peakTracker) enter() {
	p.mu.Lock()
	p.current++
	if p.current > p.peak {
		p.peak = p.current
	}
	p.mu.Unlock()
}

func (p *peakTracker) exit() {
	p.mu.Lock()
	p.current--
	p.mu.Unlock()
}

func TestThrottle_Do_spacing(t *testing.T) {
	t.Parallel()

	delay := 40 * time.Millisecond
	th := throttle.New(constSchedule(delay), throttle.WithMaxInflight(throttle.Unbounded))

	var (
		mu    sync.Mutex
		times []time.Time
	)
	const n = 4
	for range n {
		err := th.Do(context.Background(), func(_ context.Context) error {
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
			return nil
		})
		require.NoError(t, err)
	}

	require.Len(t, times, n)
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		assert.GreaterOrEqual(t, gap, delay-5*time.Millisecond,
			"gap between call %d and %d should be >= %v", i-1, i, delay)
	}
}

func TestThrottle_Do_firstCallImmediate(t *testing.T) {
	t.Parallel()

	th := throttle.New(constSchedule(10 * time.Second))
	start := time.Now()
	err := th.Do(context.Background(), func(_ context.Context) error { return nil })
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 50*time.Millisecond)
}

func TestThrottle_Do_contextCancelDuringWait(t *testing.T) {
	t.Parallel()

	th := throttle.New(constSchedule(10*time.Second), throttle.WithMaxInflight(throttle.Unbounded))
	// Prime so the next call has to wait.
	err := th.Do(context.Background(), func(_ context.Context) error { return nil })
	require.NoError(t, err)

	var called atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err = th.Do(ctx, func(_ context.Context) error {
		called.Store(true)
		return nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, called.Load(), "fn should not have been called")
}

func TestThrottle_Do_fnErrorPropagated(t *testing.T) {
	t.Parallel()

	th := throttle.New(constSchedule(0))
	sentinel := errors.New("boom")
	err := th.Do(context.Background(), func(_ context.Context) error { return sentinel })
	assert.ErrorIs(t, err, sentinel)
}

func TestThrottle_Do_scheduleAdvancedPerCall(t *testing.T) {
	t.Parallel()

	cs := &countingSchedule{d: 0}
	th := throttle.New(cs)
	const n = 5
	for range n {
		require.NoError(t, th.Do(context.Background(), func(_ context.Context) error { return nil }))
	}
	assert.Equal(t, int64(n), cs.calls.Load())
}

func TestThrottle_Do_defaultIsSerial(t *testing.T) {
	t.Parallel()

	pt := &peakTracker{}
	th := throttle.New(constSchedule(0))

	var wg sync.WaitGroup
	const n = 8
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = th.Do(context.Background(), func(_ context.Context) error {
				pt.enter()
				time.Sleep(10 * time.Millisecond)
				pt.exit()
				return nil
			})
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(1), pt.peak, "default should be serial (peak in-flight == 1)")
}

func TestThrottle_Do_maxInflight_caps(t *testing.T) {
	t.Parallel()

	const cap = 2
	pt := &peakTracker{}
	th := throttle.New(constSchedule(0), throttle.WithMaxInflight(cap))

	var wg sync.WaitGroup
	const n = 8
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = th.Do(context.Background(), func(_ context.Context) error {
				pt.enter()
				time.Sleep(20 * time.Millisecond)
				pt.exit()
				return nil
			})
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, pt.peak, int64(cap))
}

func TestThrottle_Do_unbounded_allowsConcurrentFns(t *testing.T) {
	t.Parallel()

	pt := &peakTracker{}
	th := throttle.New(constSchedule(0), throttle.WithMaxInflight(throttle.Unbounded))

	var wg sync.WaitGroup
	const n = 8
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = th.Do(context.Background(), func(_ context.Context) error {
				pt.enter()
				time.Sleep(50 * time.Millisecond)
				pt.exit()
				return nil
			})
		}()
	}
	wg.Wait()
	assert.Greater(t, pt.peak, int64(1), "unbounded should allow > 1 concurrent fn")
}

func TestThrottle_Do_maxInflight_contextCancelDuringAcquire(t *testing.T) {
	t.Parallel()

	// Default cap=1; hold the slot with a slow fn.
	th := throttle.New(constSchedule(0))

	slotHeld := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = th.Do(context.Background(), func(_ context.Context) error {
			close(slotHeld)
			time.Sleep(200 * time.Millisecond)
			return nil
		})
	}()

	<-slotHeld // slot is now occupied

	var called atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := th.Do(ctx, func(_ context.Context) error {
		called.Store(true)
		return nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, called.Load())
	wg.Wait()
}

func TestThrottle_Do(t *testing.T) {
	t.Parallel()

	t.Run("keeps spacing when callers queue for an in-flight slot", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			const delay = 100 * time.Millisecond
			th := throttle.New(constSchedule(delay)) // default: one in flight

			// The first call holds the only slot long enough for the others'
			// pacing slots to pass while they wait for it.
			durations := []time.Duration{time.Second, time.Millisecond, time.Millisecond}
			starts := make([]time.Time, len(durations))
			var wg sync.WaitGroup
			for i, d := range durations {
				wg.Go(func() {
					_ = th.Do(t.Context(), func(context.Context) error {
						starts[i] = time.Now()
						time.Sleep(d)
						return nil
					})
				})
				synctest.Wait() // fix arrival order
			}
			wg.Wait()

			for i := 1; i < len(starts); i++ {
				assert.GreaterOrEqual(t, starts[i].Sub(starts[i-1]), delay, "gap before call %d", i)
			}
		})
	})

	t.Run("does not consume a schedule step for a caller cancelled while queued for an in-flight slot", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			s := &countingSchedule{}
			th := throttle.New(s)

			var wg sync.WaitGroup
			wg.Go(func() {
				_ = th.Do(t.Context(), func(context.Context) error {
					time.Sleep(time.Second)
					return nil
				})
			})
			synctest.Wait() // the first call holds the only slot

			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := th.Do(ctx, func(context.Context) error { return nil })
			wg.Wait()

			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, int64(1), s.calls.Load(), "only the first call should take a turn")
		})
	})

	t.Run("does not consume a schedule step when ctx is already cancelled", func(t *testing.T) {
		t.Parallel()
		s := &countingSchedule{}
		th := throttle.New(s)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// With a free slot the semaphore select is a coin flip, so repeat.
		for range 100 {
			_ = th.Do(ctx, func(context.Context) error { return nil })
		}
		assert.Zero(t, s.calls.Load())
	})

	t.Run("releases the in-flight slot when cancelled during the paced wait", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			th := throttle.New(constSchedule(time.Second))
			require.NoError(t, th.Do(t.Context(), func(context.Context) error { return nil }))

			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := th.Do(ctx, func(context.Context) error { return nil })
			require.ErrorIs(t, err, context.DeadlineExceeded)

			// A held slot would leave this call blocked and the bubble deadlocked.
			assert.NoError(t, th.Do(t.Context(), func(context.Context) error { return nil }))
		})
	})

	t.Run("dispatches at a Waiter's rate", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			bucket := &waitingBucket{interval: time.Second, burst: 3}
			th := throttle.New(bucket, throttle.WithMaxInflight(throttle.Unbounded))

			var mu sync.Mutex
			var got []time.Duration
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					_ = th.Do(t.Context(), func(context.Context) error {
						mu.Lock()
						got = append(got, time.Since(start))
						mu.Unlock()
						return nil
					})
				})
				synctest.Wait() // fix arrival order
			}
			wg.Wait()

			s := time.Second
			assert.Equal(t, []time.Duration{0, 0, 0, 1 * s, 2 * s, 3 * s, 4 * s, 5 * s}, got)
		})
	})

	t.Run("returns a Waiter's error without calling fn", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			bucket := &waitingBucket{interval: time.Hour, burst: 1}
			th := throttle.New(bucket)
			require.NoError(t, th.Do(t.Context(), func(context.Context) error { return nil })) // drain the burst

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			called := false
			err := th.Do(ctx, func(context.Context) error { called = true; return nil })

			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.False(t, called)
		})
	})

	t.Run("takes the in-flight slot before asking a Waiter", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			bucket := &waitingBucket{interval: time.Second, burst: 3}
			th := throttle.New(bucket) // default: one in flight

			// The first call holds the only slot for 10s. Callers queued behind
			// it must reserve their tokens only once they hold the slot, or
			// they all fire together when it frees.
			got := make([]time.Duration, 8)
			var wg sync.WaitGroup
			for i := range got {
				wg.Go(func() {
					_ = th.Do(t.Context(), func(context.Context) error {
						got[i] = time.Since(start)
						if i == 0 {
							time.Sleep(10 * time.Second)
						}
						return nil
					})
				})
				synctest.Wait() // fix arrival order
			}
			wg.Wait()

			s := time.Second
			assert.Equal(t, []time.Duration{0, 10 * s, 10 * s, 10 * s, 11 * s, 12 * s, 13 * s, 14 * s}, got)
		})
	})

	t.Run("does not call a Waiter when ctx is already cancelled", func(t *testing.T) {
		t.Parallel()
		bucket := &waitingBucket{interval: time.Second, burst: 1}
		th := throttle.New(bucket)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// With a free slot the semaphore select is a coin flip, so repeat.
		for range 100 {
			assert.ErrorIs(t, th.Do(ctx, func(context.Context) error { return nil }), context.Canceled)
		}
		assert.Zero(t, bucket.waits.Load())
	})

	tests := []struct {
		name string
		opts []throttle.Option
	}{
		{name: "default in-flight cap", opts: nil},
		{name: "unbounded", opts: []throttle.Option{throttle.WithMaxInflight(throttle.Unbounded)}},
	}
	for _, tc := range tests {
		t.Run("does not run fn when ctx is already cancelled with "+tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			// With a free slot the outcome used to be a coin flip, so repeat
			// enough times that a lucky pass is out of the question.
			calls, notCancelled := 0, 0
			for range 100 {
				th := throttle.New(constSchedule(0), tc.opts...)
				err := th.Do(ctx, func(context.Context) error {
					calls++
					return nil
				})
				if !errors.Is(err, context.Canceled) {
					notCancelled++
				}
			}
			assert.Zero(t, calls, "fn ran with an already-cancelled ctx")
			assert.Zero(t, notCancelled, "Do did not return context.Canceled")
		})
	}
}

func TestNew_panicsOnInvalidMaxInflight(t *testing.T) {
	t.Parallel()

	t.Run("zero panics", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { throttle.WithMaxInflight(0) })
	})
	t.Run("negative below unbounded panics", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { throttle.WithMaxInflight(-2) })
	})
	t.Run("one does not panic", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { throttle.WithMaxInflight(1) })
	})
	t.Run("unbounded does not panic", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { throttle.WithMaxInflight(throttle.Unbounded) })
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("panics on nil schedule", func(t *testing.T) {
		t.Parallel()
		assert.PanicsWithValue(t, "throttle.New: schedule must not be nil", func() { throttle.New(nil) })
	})
}

func TestNewRoundTripper(t *testing.T) {
	t.Parallel()

	t.Run("panics on nil schedule", func(t *testing.T) {
		t.Parallel()
		assert.PanicsWithValue(t, "throttle.NewRoundTripper: schedule must not be nil", func() { throttle.NewRoundTripper(nil, nil) })
	})
}
