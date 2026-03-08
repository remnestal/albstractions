package schedule_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniform(t *testing.T) {
	t.Parallel()

	t.Run("returns values within [lo, hi]", func(t *testing.T) {
		t.Parallel()

		lo, hi := 1*time.Second, 5*time.Second
		src := rand.NewPCG(42, 0)
		s := schedule.NewUniform(lo, hi, schedule.WithSource(src))

		for i := range 100 {
			d := s.Next()
			assert.GreaterOrEqual(t, d, lo, "iteration %d", i)
			assert.LessOrEqual(t, d, hi, "iteration %d", i)
		}
	})

	t.Run("returns exact value when lo equals hi", func(t *testing.T) {
		t.Parallel()

		s := schedule.NewUniform(2*time.Second, 2*time.Second)
		assert.Equal(t, 2*time.Second, s.Next())
	})

	t.Run("panics when lo is negative", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewUniform(-time.Second, time.Second) })
	})

	t.Run("panics when hi is less than lo", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewUniform(5*time.Second, time.Second) })
	})
}
