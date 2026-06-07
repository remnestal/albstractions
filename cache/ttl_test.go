package cache_test

import (
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
)

func TestWithDefaultTTL(t *testing.T) {
	t.Parallel()

	t.Run("panics on zero", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithDefaultTTL(0) })
	})

	t.Run("panics on a negative duration", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithDefaultTTL(-time.Second) })
	})

	t.Run("accepts a positive duration", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() {
			cache.NewMemory[string, int](cache.WithDefaultTTL(time.Hour))
		})
	})

	t.Run("is applied to Default sets", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithDefaultTTL(time.Hour))
		exp := c.Set("k", 1, cache.Default)
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
		exp := c.Set("k", 1, cache.After(time.Minute))
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
		exp := c.Set("k", 1, cache.After(time.Hour))
		assert.WithinDuration(t, time.Now().Add(time.Minute), exp, time.Second)
	})

	t.Run("caps Never to the ceiling", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithMaxTTL(time.Hour))
		exp := c.Set("k", 1, cache.Never)
		assert.False(t, exp.IsZero())
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})
}
