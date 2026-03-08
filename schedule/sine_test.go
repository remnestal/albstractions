package schedule_test

import (
	"testing"
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

		s := schedule.NewSine(lo, hi, period)
		for i := range 100 {
			d := s.Next()
			assert.GreaterOrEqual(t, d, lo, "iteration %d", i)
			assert.LessOrEqual(t, d, hi, "iteration %d", i)
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
}
