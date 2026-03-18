package throttle_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
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
