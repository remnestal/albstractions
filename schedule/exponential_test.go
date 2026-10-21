package schedule_test

import (
	"math"
	"testing"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExponential(t *testing.T) {
	t.Parallel()

	t.Run("grows exponentially up to maxDelay", func(t *testing.T) {
		t.Parallel()

		s := schedule.NewExponential(100*time.Millisecond, 1*time.Second)
		want := []time.Duration{100, 200, 400, 800, 1000}
		for i, ms := range want {
			assert.Equal(t, ms*time.Millisecond, s.Next(), "step %d", i)
		}
	})

	t.Run("resets to base", func(t *testing.T) {
		t.Parallel()

		s := schedule.NewExponential(100*time.Millisecond, 1*time.Second)
		s.Next()
		s.Next()
		s.Reset()

		assert.Equal(t, 100*time.Millisecond, s.Next())
	})

	t.Run("custom factor", func(t *testing.T) {
		t.Parallel()

		s := schedule.NewExponential(100*time.Millisecond, 10*time.Second, schedule.WithFactor(3.0))
		assert.Equal(t, 100*time.Millisecond, s.Next())
		assert.Equal(t, 300*time.Millisecond, s.Next())
		assert.Equal(t, 900*time.Millisecond, s.Next())
	})

	t.Run("panics on non-positive base", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewExponential(0, time.Second) })
		require.Panics(t, func() { schedule.NewExponential(-time.Millisecond, time.Second) })
	})

	t.Run("panics when maxDelay less than base", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewExponential(time.Second, 100*time.Millisecond) })
	})

	t.Run("saturates at a maxDelay of math.MaxInt64", func(t *testing.T) {
		t.Parallel()

		const maxDelay = time.Duration(math.MaxInt64)
		s := schedule.NewExponential(time.Hour, maxDelay)
		var d time.Duration
		for i := range 40 { // 1h doubles past MaxInt64 after ~22 steps
			d = s.Next()
			assert.GreaterOrEqual(t, d, time.Duration(0), "step %d", i)
		}
		assert.Equal(t, maxDelay, d)
	})

	t.Run("never exceeds a maxDelay that float64 cannot represent", func(t *testing.T) {
		t.Parallel()

		const maxDelay = time.Duration(1<<62 + 513)
		s := schedule.NewExponential(1<<61, maxDelay)
		for i := range 4 {
			assert.LessOrEqual(t, s.Next(), maxDelay, "step %d", i)
		}
	})
}

func TestWithFactor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		factor float64
	}{
		{name: "zero", factor: 0},
		{name: "negative", factor: -1},
		{name: "below one", factor: 0.5},
		{name: "NaN", factor: math.NaN()},
		{name: "infinite", factor: math.Inf(1)},
		{name: "negative infinity", factor: math.Inf(-1)},
	}
	for _, tc := range tests {
		t.Run("panics on "+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { schedule.WithFactor(tc.factor) })
		})
	}

	t.Run("one gives a constant delay", func(t *testing.T) {
		t.Parallel()
		e := schedule.NewExponential(100*time.Millisecond, time.Second, schedule.WithFactor(1))
		for range 3 {
			assert.Equal(t, 100*time.Millisecond, e.Next())
		}
	})
}
