package cache_test

import (
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
)

func TestWithDefaultTTL(t *testing.T) {
	t.Parallel()

	t.Run("panics on the DefaultTTL sentinel", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithDefaultTTL(cache.DefaultTTL) })
	})

	t.Run("panics below NoExpiration", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithDefaultTTL(cache.KeepTTL) })
	})

	t.Run("accepts NoExpiration", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { cache.WithDefaultTTL(cache.NoExpiration) })
	})

	t.Run("is applied to DefaultTTL sets", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithDefaultTTL(time.Hour))
		exp := c.Set("k", 1, cache.DefaultTTL)
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})
}

func TestWithMinTTL(t *testing.T) {
	t.Parallel()

	t.Run("panics on a negative bound", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithMinTTL(-time.Second) })
	})

	t.Run("clamps a smaller ttl up to the floor", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithMinTTL(time.Hour))
		exp := c.Set("k", 1, time.Minute)
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})
}

func TestWithMaxTTL(t *testing.T) {
	t.Parallel()

	t.Run("panics on a negative bound", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithMaxTTL(-time.Second) })
	})

	t.Run("clamps a larger ttl down to the ceiling", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithMaxTTL(time.Minute))
		exp := c.Set("k", 1, time.Hour)
		assert.WithinDuration(t, time.Now().Add(time.Minute), exp, time.Second)
	})
}
