package schedule_test

import (
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
}
