package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMemory(t *testing.T) {
	t.Parallel()

	t.Run("zero value is usable", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, cache.NoExpiration)
		assert.True(t, exp.IsZero())
		v, ok := c.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 1, v)
	})

	t.Run("panics when min exceeds max", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewMemory[string, int](cache.WithMinTTL(time.Hour), cache.WithMaxTTL(time.Minute))
		})
	})
}

func TestMemory_Get(t *testing.T) {
	t.Parallel()

	t.Run("returns the stored value", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 42, cache.NoExpiration)
		v, ok := c.Get("k")
		require.True(t, ok)
		assert.Equal(t, 42, v)
	})

	t.Run("reports a missing key", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		v, ok := c.Get("absent")
		assert.False(t, ok)
		assert.Zero(t, v)
	})

	t.Run("reports an expired entry as missing", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, time.Nanosecond)
		time.Sleep(time.Millisecond)
		_, ok := c.Get("k")
		assert.False(t, ok)
	})
}

func TestMemory_Set(t *testing.T) {
	t.Parallel()

	t.Run("returns the absolute expiry for a positive ttl", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, time.Hour)
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})

	t.Run("NoExpiration yields the zero time", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, cache.NoExpiration)
		assert.True(t, exp.IsZero())
	})

	t.Run("DefaultTTL uses the configured default", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithDefaultTTL(30 * time.Minute))
		exp := c.Set("k", 1, cache.DefaultTTL)
		assert.WithinDuration(t, time.Now().Add(30*time.Minute), exp, time.Second)
	})

	t.Run("overwrites the existing value", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, cache.NoExpiration)
		c.Set("k", 2, cache.NoExpiration)
		v, _ := c.Get("k")
		assert.Equal(t, 2, v)
	})

	t.Run("KeepTTL preserves the existing expiry", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		first := c.Set("k", 1, time.Hour)
		time.Sleep(5 * time.Millisecond)
		kept := c.Set("k", 2, cache.KeepTTL)
		assert.Equal(t, first, kept)
		v, _ := c.Get("k")
		assert.Equal(t, 2, v)
	})

	t.Run("KeepTTL on an absent key never expires", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, cache.KeepTTL)
		assert.True(t, exp.IsZero())
	})

	t.Run("KeepTTL on an expired entry does not resurrect a stale expiry", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, time.Nanosecond)
		time.Sleep(time.Millisecond)
		exp := c.Set("k", 2, cache.KeepTTL)
		assert.True(t, exp.IsZero())
		v, ok := c.Get("k")
		require.True(t, ok)
		assert.Equal(t, 2, v)
	})
}

func TestMemory_Delete(t *testing.T) {
	t.Parallel()

	t.Run("removes the entry", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, cache.NoExpiration)
		c.Delete("k")
		_, ok := c.Get("k")
		assert.False(t, ok)
	})

	t.Run("an absent key is a no-op", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		assert.NotPanics(t, func() { c.Delete("absent") })
	})
}

func TestMemory_Items(t *testing.T) {
	t.Parallel()

	t.Run("yields all live entries", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("a", 1, cache.NoExpiration)
		c.Set("b", 2, cache.NoExpiration)
		got := map[string]int{}
		for k, v := range c.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"a": 1, "b": 2}, got)
	})

	t.Run("excludes expired entries", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("live", 1, time.Hour)
		c.Set("dead", 2, time.Nanosecond)
		time.Sleep(time.Millisecond)
		got := map[string]int{}
		for k, v := range c.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"live": 1}, got)
	})

	t.Run("the body may mutate the cache without deadlock", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[int, int]()
		for i := range 5 {
			c.Set(i, i, cache.NoExpiration)
		}
		count := 0
		for k := range c.Items() {
			c.Set(k+100, k, cache.NoExpiration)
			c.Delete(k)
			count++
		}
		assert.Equal(t, 5, count)
	})

	t.Run("stops early when the body breaks", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[int, int]()
		for i := range 10 {
			c.Set(i, i, cache.NoExpiration)
		}
		seen := 0
		for range c.Items() {
			seen++
			break
		}
		assert.Equal(t, 1, seen)
	})
}

func TestMemory_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("matches Get and returns no error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 7, cache.NoExpiration)
		v, ok, err := c.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 7, v)
	})
}

func TestMemory_SetContext(t *testing.T) {
	t.Parallel()

	t.Run("stores and returns the expiry with no error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp, err := c.SetContext(context.Background(), "k", 1, time.Hour)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
		v, _ := c.Get("k")
		assert.Equal(t, 1, v)
	})
}

func TestMemory_DeleteContext(t *testing.T) {
	t.Parallel()

	t.Run("removes and returns no error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, cache.NoExpiration)
		require.NoError(t, c.DeleteContext(context.Background(), "k"))
		_, ok := c.Get("k")
		assert.False(t, ok)
	})
}

func TestMemory_Close(t *testing.T) {
	t.Parallel()

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithCleanupInterval(time.Hour))
		require.NoError(t, c.Close())
		assert.NoError(t, c.Close())
	})

	t.Run("the cache remains usable after close", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithCleanupInterval(time.Hour))
		require.NoError(t, c.Close())
		c.Set("k", 1, cache.NoExpiration)
		v, ok := c.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 1, v)
	})
}
