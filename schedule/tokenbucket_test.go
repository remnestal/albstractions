package schedule_test

import (
	"testing"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
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

	t.Run("refused reservation returns zero", func(t *testing.T) {
		t.Parallel()

		// A limiter that can never issue capacity must not block the caller.
		s := schedule.NewTokenBucket(&fakeLimiter{})
		assert.Equal(t, time.Duration(0), s.Next())
	})
}
