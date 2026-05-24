package cache_test

import (
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
)

func TestWithCleanupInterval(t *testing.T) {
	t.Parallel()

	t.Run("panics on a negative interval", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithCleanupInterval(-time.Second) })
	})

	t.Run("accepts a zero interval", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { cache.NewMemory[string, int](cache.WithCleanupInterval(0)) })
	})
}

func TestWithRebuildInterval(t *testing.T) {
	t.Parallel()

	t.Run("panics on a negative interval", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithRebuildInterval(-time.Second) })
	})

	t.Run("accepts a zero interval", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { cache.NewMemory[string, int](cache.WithRebuildInterval(0)) })
	})
}
