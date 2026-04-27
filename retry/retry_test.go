package retry_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/remnestal/albstractions/retry"
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

// resettableSchedule records calls to both Next and Reset.
type resettableSchedule struct {
	d      time.Duration
	resets atomic.Int64
	nexts  atomic.Int64
}

func (r *resettableSchedule) Next() time.Duration {
	r.nexts.Add(1)
	return r.d
}

func (r *resettableSchedule) Reset() {
	r.resets.Add(1)
}

func alwaysContinue(_ int, _ time.Duration, _ error) bool { return false }

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("panics on nil schedule", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.New(nil, retry.MaxAttempts(1)) })
	})

	t.Run("panics on nil stop", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.New(constSchedule(0), nil) })
	})
}

func TestWithHook(t *testing.T) {
	t.Parallel()

	t.Run("panics on nil fn", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { retry.WithHook(nil) })
	})
}

func TestDo(t *testing.T) {
	t.Parallel()

	t.Run("succeeds on first attempt", func(t *testing.T) {
		t.Parallel()
		cs := &countingSchedule{d: time.Millisecond}
		r := retry.New(cs, retry.MaxAttempts(5))

		var calls atomic.Int64
		err := r.Do(context.Background(), func(_ context.Context) error {
			calls.Add(1)
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), calls.Load())
		assert.Equal(t, int64(0), cs.calls.Load(), "schedule.Next must not be called on success")
	})

	t.Run("succeeds after failures", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(time.Millisecond), retry.MaxAttempts(10))

		var calls atomic.Int64
		transient := errors.New("transient")
		err := r.Do(context.Background(), func(_ context.Context) error {
			if calls.Add(1) < 4 {
				return transient
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, int64(4), calls.Load())
	})

	t.Run("halts on max attempts", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(0), retry.MaxAttempts(3))

		var calls atomic.Int64
		sentinel := errors.New("nope")
		err := r.Do(context.Background(), func(_ context.Context) error {
			calls.Add(1)
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
		assert.Equal(t, int64(3), calls.Load())
	})

	t.Run("halts on max elapsed", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(30*time.Millisecond), retry.MaxElapsed(50*time.Millisecond))

		sentinel := errors.New("nope")
		start := time.Now()
		err := r.Do(context.Background(), func(_ context.Context) error { return sentinel })
		elapsed := time.Since(start)

		assert.ErrorIs(t, err, sentinel)
		assert.GreaterOrEqual(t, elapsed, 30*time.Millisecond)
		assert.Less(t, elapsed, 200*time.Millisecond, "should give up shortly after the budget")
	})

	t.Run("halts on error", func(t *testing.T) {
		t.Parallel()
		permanent := errors.New("permanent")
		stop := retry.Any(
			retry.MaxAttempts(100),
			retry.OnError(func(err error) bool { return errors.Is(err, permanent) }),
		)
		r := retry.New(constSchedule(0), stop)

		var calls atomic.Int64
		err := r.Do(context.Background(), func(_ context.Context) error {
			calls.Add(1)
			return permanent
		})
		assert.ErrorIs(t, err, permanent)
		assert.Equal(t, int64(1), calls.Load(), "OnError should halt immediately on first attempt")
	})

	t.Run("context cancel during sleep returns ctx.Err", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(10*time.Second), retry.MaxAttempts(100))

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		var calls atomic.Int64
		transient := errors.New("transient")
		start := time.Now()
		err := r.Do(ctx, func(_ context.Context) error {
			calls.Add(1)
			return transient
		})
		elapsed := time.Since(start)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int64(1), calls.Load(), "fn should run once before sleep cancels")
		assert.Less(t, elapsed, 500*time.Millisecond)
	})

	t.Run("context cancel during fn surfaces as fn error", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(0), retry.MaxAttempts(5))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := r.Do(ctx, func(ctx context.Context) error { return ctx.Err() })
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("calls Reset at start of each invocation", func(t *testing.T) {
		t.Parallel()
		rs := &resettableSchedule{d: time.Millisecond}
		r := retry.New(rs, retry.MaxAttempts(2))

		for range 3 {
			_ = r.Do(context.Background(), func(_ context.Context) error { return errors.New("x") })
		}
		assert.Equal(t, int64(3), rs.resets.Load(), "Reset should be called once per Do")
	})

	t.Run("does not panic without Resetter", func(t *testing.T) {
		t.Parallel()
		// constSchedule does not implement Resetter; Do must still work.
		r := retry.New(constSchedule(0), retry.MaxAttempts(2))
		err := r.Do(context.Background(), func(_ context.Context) error { return errors.New("x") })
		assert.Error(t, err)
	})

	t.Run("hook fires once per attempt including success", func(t *testing.T) {
		t.Parallel()
		var mu sync.Mutex
		var infos []retry.AttemptInfo
		hook := retry.WithHook(func(a retry.AttemptInfo) {
			mu.Lock()
			infos = append(infos, a)
			mu.Unlock()
		})

		r := retry.New(constSchedule(time.Millisecond), retry.MaxAttempts(10), hook)

		var calls atomic.Int64
		transient := errors.New("transient")
		err := r.Do(context.Background(), func(_ context.Context) error {
			if calls.Add(1) < 3 {
				return transient
			}
			return nil
		})
		require.NoError(t, err)

		require.Len(t, infos, 3)
		assert.Equal(t, 1, infos[0].Attempt)
		assert.ErrorIs(t, infos[0].Err, transient)
		assert.Equal(t, time.Millisecond, infos[0].Next)

		assert.Equal(t, 2, infos[1].Attempt)
		assert.ErrorIs(t, infos[1].Err, transient)
		assert.Equal(t, time.Millisecond, infos[1].Next)

		assert.Equal(t, 3, infos[2].Attempt)
		assert.NoError(t, infos[2].Err)
		assert.Equal(t, time.Duration(0), infos[2].Next, "Next must be 0 on success")
	})

	t.Run("hook Next is zero on halt", func(t *testing.T) {
		t.Parallel()
		var last retry.AttemptInfo
		var mu sync.Mutex
		hook := retry.WithHook(func(a retry.AttemptInfo) {
			mu.Lock()
			last = a
			mu.Unlock()
		})

		r := retry.New(constSchedule(time.Second), retry.MaxAttempts(2), hook)

		sentinel := errors.New("x")
		_ = r.Do(context.Background(), func(_ context.Context) error { return sentinel })

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 2, last.Attempt)
		assert.ErrorIs(t, last.Err, sentinel)
		assert.Equal(t, time.Duration(0), last.Next, "Next must be 0 when Do is about to halt")
	})

	t.Run("schedule advanced only between attempts", func(t *testing.T) {
		t.Parallel()
		cs := &countingSchedule{d: 0}
		r := retry.New(cs, retry.MaxAttempts(4))

		sentinel := errors.New("x")
		_ = r.Do(context.Background(), func(_ context.Context) error { return sentinel })

		// 4 attempts → 3 inter-attempt sleeps → 3 Next() calls.
		assert.Equal(t, int64(3), cs.calls.Load())
	})

	t.Run("concurrent calls with stateless schedule", func(t *testing.T) {
		t.Parallel()
		r := retry.New(constSchedule(0), retry.MaxAttempts(5))

		var wg sync.WaitGroup
		const n = 16
		wg.Add(n)
		var total atomic.Int64
		for range n {
			go func() {
				defer wg.Done()
				_ = r.Do(context.Background(), func(_ context.Context) error {
					total.Add(1)
					return errors.New("x")
				})
			}()
		}
		wg.Wait()
		assert.Equal(t, int64(n*5), total.Load())
	})

	t.Run("always-continue stop respects context", func(t *testing.T) {
		t.Parallel()
		// StopFunc that never halts — only ctx can stop the loop.
		r := retry.New(constSchedule(5*time.Millisecond), retry.StopFunc(alwaysContinue))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		err := r.Do(ctx, func(_ context.Context) error { return errors.New("x") })
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
