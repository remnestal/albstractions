package schedule_test

import (
	"math"
	"testing"
	"testing/synctest"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSine(t *testing.T) {
	t.Parallel()

	lo, hi := 1*time.Second, 5*time.Second
	period := time.Minute

	t.Run("returns values within [lo, hi]", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			// Sample the default clock across a whole period, peaking at hi halfway.
			s := schedule.NewSine(lo, hi, period)
			for i := range 100 {
				d := s.Next()
				assert.GreaterOrEqual(t, d, lo, "iteration %d", i)
				assert.LessOrEqual(t, d, hi, "iteration %d", i)
				if i == 50 {
					assert.Equal(t, hi, d, "half a period in")
				}
				time.Sleep(period / 100)
			}
		})
	})

	t.Run("follows a raised cosine that wraps each period", func(t *testing.T) {
		t.Parallel()

		// A sixth of a period tells the raised cosine from a triangle wave,
		// which agrees with it at every quarter period. Past one period the
		// phase must wrap rather than stay at the end of the cycle.
		tests := []struct {
			name    string
			elapsed time.Duration
			want    time.Duration
		}{
			{"a quarter of the way up at a sixth of a period", period / 6, lo + (hi-lo)/4},
			{"midway at a quarter period", period / 4, (lo + hi) / 2},
			{"hi at one and a half periods", 3 * period / 2, hi},
			{"a quarter of the way up at seven sixths of a period", period + period/6, lo + (hi-lo)/4},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					s := schedule.NewSine(lo, hi, period)
					s.Next() // starts the cycle at the bubble's current time
					time.Sleep(tt.elapsed)
					// Allow 1ns for the float64 round trip through math.Cos.
					assert.InDelta(t, float64(tt.want), float64(s.Next()), 1)
				})
			})
		}
	})

	t.Run("returns lo at phase zero", func(t *testing.T) {
		t.Parallel()

		// Clock always returns the same time, so elapsed is always 0 → phase = 0 → lo.
		t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		s := schedule.NewSine(lo, hi, period, schedule.WithClock(func() time.Time { return t0 }))
		assert.Equal(t, lo, s.Next())
	})

	t.Run("panics when lo is negative", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewSine(-time.Second, time.Second, period) })
	})

	t.Run("panics when hi is less than lo", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewSine(hi, lo, period) })
	})

	t.Run("panics when period is not positive", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewSine(lo, hi, 0) })
		require.Panics(t, func() { schedule.NewSine(lo, hi, -time.Second) })
	})

	t.Run("returns hi at half period", func(t *testing.T) {
		t.Parallel()

		// First clockFn call initialises start; second call is at period/2 → phase = π → hi.
		t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		calls := 0
		clock := func() time.Time {
			calls++
			if calls == 1 {
				return t0
			}
			return t0.Add(period / 2)
		}
		s := schedule.NewSine(lo, hi, period, schedule.WithClock(clock))
		assert.Equal(t, hi, s.Next())
	})

	// halfPeriodClock reports t0 on its first call and half a minute later on
	// every call after, so a one-minute Sine is sampled at its peak.
	halfPeriodClock := func() func() time.Time {
		t0 := time.Unix(0, 0)
		calls := 0
		return func() time.Time {
			calls++
			if calls == 1 {
				return t0
			}
			return t0.Add(time.Minute / 2)
		}
	}

	t.Run("returns hi at half period for a range of math.MaxInt64", func(t *testing.T) {
		t.Parallel()

		const hi = time.Duration(math.MaxInt64)
		s := schedule.NewSine(0, hi, time.Minute, schedule.WithClock(halfPeriodClock()))
		assert.Equal(t, hi, s.Next())
	})

	t.Run("returns hi at half period when lo > 0 and hi is math.MaxInt64", func(t *testing.T) {
		t.Parallel()

		const hi = time.Duration(math.MaxInt64)
		s := schedule.NewSine(100, hi, time.Minute, schedule.WithClock(halfPeriodClock()))
		assert.Equal(t, hi, s.Next())
	})
}

func TestWithClock(t *testing.T) {
	t.Parallel()

	t.Run("panics on a nil clock", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() { schedule.WithClock(nil) })
	})
}
