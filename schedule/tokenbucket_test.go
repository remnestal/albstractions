package schedule_test

import (
	"math"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A generic TokenBucket still satisfies the Schedule interface.
var _ schedule.Schedule = (*schedule.TokenBucket[fakeReservation])(nil)

// fakeReservation is a schedule.Reservation with fixed answers.
type fakeReservation struct {
	ok    bool
	delay time.Duration
}

func (r fakeReservation) OK() bool             { return r.ok }
func (r fakeReservation) Delay() time.Duration { return r.delay }

// fakeLimiter mimics a token bucket: the first burst reservations are granted
// with no delay, and each one after that waits a further interval. A limiter
// with neither burst nor interval refuses every reservation.
type fakeLimiter struct {
	burst    int
	interval time.Duration
	issued   int
}

func (l *fakeLimiter) Reserve() fakeReservation {
	if l.burst == 0 && l.interval == 0 {
		return fakeReservation{}
	}
	l.issued++
	if l.issued <= l.burst {
		return fakeReservation{ok: true}
	}
	return fakeReservation{ok: true, delay: time.Duration(l.issued-l.burst) * l.interval}
}

func TestTokenBucket(t *testing.T) {
	t.Parallel()

	t.Run("panics on a nil limiter", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() { schedule.NewTokenBucket[fakeReservation](nil) })
	})

	t.Run("no delay within burst", func(t *testing.T) {
		t.Parallel()

		// A burst of 5 allows 5 immediate reservations with zero delay.
		s := schedule.NewTokenBucket(&fakeLimiter{burst: 5, interval: time.Second})
		for range 5 {
			assert.Equal(t, time.Duration(0), s.Next())
		}
	})

	t.Run("delays after burst is exhausted", func(t *testing.T) {
		t.Parallel()

		// After consuming the single burst token, the next reservation must wait.
		s := schedule.NewTokenBucket(&fakeLimiter{burst: 1, interval: time.Second})
		s.Next() // consume burst
		assert.Greater(t, s.Next(), time.Duration(0))
	})

	t.Run("refused reservation never grants a slot", func(t *testing.T) {
		t.Parallel()

		// A limiter that can never issue capacity, such as a
		// golang.org/x/time/rate limiter with burst 0, must not turn into
		// "no limit at all".
		s := schedule.NewTokenBucket(&fakeLimiter{})
		for range 3 {
			assert.Equal(t, time.Duration(math.MaxInt64), s.Next())
		}
	})
}

// waitingLimiter is a limiter with its own Wait, returning err. It counts calls
// so tests can check that Wait delegates instead of reserving.
type waitingLimiter struct {
	err      error
	waits    int
	reserves int
}

func (l *waitingLimiter) Reserve() fakeReservation {
	l.reserves++
	return fakeReservation{ok: true}
}

func (l *waitingLimiter) Wait(context.Context) error {
	l.waits++
	return l.err
}

// burstLimiter is a waitingLimiter that also reports its burst, as
// *rate.Limiter does.
type burstLimiter struct {
	waitingLimiter
	burst int
}

func (l *burstLimiter) Burst() int { return l.burst }

// cancelReservation is a reservation that records whether it was cancelled.
type cancelReservation struct {
	ok        bool
	delay     time.Duration
	cancelled bool
}

func (r *cancelReservation) OK() bool             { return r.ok }
func (r *cancelReservation) Delay() time.Duration { return r.delay }
func (r *cancelReservation) Cancel()              { r.cancelled = true }

// cancelLimiter hands out a fixed cancelReservation and counts reservations.
type cancelLimiter struct {
	res      *cancelReservation
	reserves int
}

func (l *cancelLimiter) Reserve() *cancelReservation {
	l.reserves++
	return l.res
}

func TestTokenBucket_Wait(t *testing.T) {
	t.Parallel()

	errOpaque := errors.New("rate: opaque limiter error")

	t.Run("delegates to the limiter's Wait without reserving", func(t *testing.T) {
		t.Parallel()
		l := &waitingLimiter{}
		require.NoError(t, schedule.NewTokenBucket(l).Wait(t.Context()))
		assert.Equal(t, 1, l.waits)
		assert.Zero(t, l.reserves)
	})

	t.Run("passes the limiter's context errors through", func(t *testing.T) {
		t.Parallel()
		l := &waitingLimiter{err: context.DeadlineExceeded}
		assert.ErrorIs(t, schedule.NewTokenBucket(l).Wait(t.Context()), context.DeadlineExceeded)
	})

	t.Run("reports a burst-0 refusal as ErrRefused", func(t *testing.T) {
		t.Parallel()
		l := &burstLimiter{waitingLimiter: waitingLimiter{err: errOpaque}, burst: 0}
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()

		err := schedule.NewTokenBucket(l).Wait(ctx)
		assert.ErrorIs(t, err, schedule.ErrRefused)
		assert.ErrorIs(t, err, errOpaque)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("reports an unmeetable deadline as DeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		l := &burstLimiter{waitingLimiter: waitingLimiter{err: errOpaque}, burst: 1}
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()

		err := schedule.NewTokenBucket(l).Wait(ctx)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.ErrorIs(t, err, errOpaque)
		assert.NotErrorIs(t, err, schedule.ErrRefused)
	})

	t.Run("reports an unexplained error without a deadline as ErrRefused", func(t *testing.T) {
		t.Parallel()
		l := &waitingLimiter{err: errOpaque}
		err := schedule.NewTokenBucket(l).Wait(t.Context())
		assert.ErrorIs(t, err, schedule.ErrRefused)
		assert.ErrorIs(t, err, errOpaque)
	})

	t.Run("waits for the reservation's delay", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			res := &cancelReservation{ok: true, delay: time.Second}
			start := time.Now()
			require.NoError(t, schedule.NewTokenBucket(&cancelLimiter{res: res}).Wait(t.Context()))
			assert.Equal(t, time.Second, time.Since(start))
			assert.False(t, res.cancelled)
		})
	})

	t.Run("cancels the reservation when ctx ends first", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			res := &cancelReservation{ok: true, delay: time.Hour}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			start := time.Now()
			err := schedule.NewTokenBucket(&cancelLimiter{res: res}).Wait(ctx)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, time.Second, time.Since(start))
			assert.True(t, res.cancelled)
		})
	})

	t.Run("returns ErrRefused at once for a refused reservation", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			res := &cancelReservation{}
			start := time.Now()
			err := schedule.NewTokenBucket(&cancelLimiter{res: res}).Wait(t.Context())
			assert.ErrorIs(t, err, schedule.ErrRefused)
			assert.Zero(t, time.Since(start))
			assert.False(t, res.cancelled)
		})
	})

	t.Run("returns ctx error for a reservation without Cancel", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			s := schedule.NewTokenBucket(&fakeLimiter{burst: 1, interval: time.Hour})
			s.Next() // consume the burst
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			assert.ErrorIs(t, s.Wait(ctx), context.DeadlineExceeded)
		})
	})

	t.Run("makes no reservation when ctx is already done", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		reserving := &cancelLimiter{res: &cancelReservation{ok: true}}
		assert.ErrorIs(t, schedule.NewTokenBucket(reserving).Wait(ctx), context.Canceled)
		assert.Zero(t, reserving.reserves)

		waiting := &waitingLimiter{}
		assert.ErrorIs(t, schedule.NewTokenBucket(waiting).Wait(ctx), context.Canceled)
		assert.Zero(t, waiting.waits)
	})
}
