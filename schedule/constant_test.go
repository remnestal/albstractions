package schedule_test

import (
	"testing"
	"time"

	"github.com/remnestal/albstractions/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstant(t *testing.T) {
	t.Parallel()

	t.Run("returns constant delay on every call", func(t *testing.T) {
		t.Parallel()

		s := schedule.NewConstant(3 * time.Second)
		for range 5 {
			assert.Equal(t, 3*time.Second, s.Next())
		}
	})

	t.Run("panics on negative delay", func(t *testing.T) {
		t.Parallel()

		require.Panics(t, func() { schedule.NewConstant(-time.Second) })
	})
}
