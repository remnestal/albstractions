package cache_test

import (
	"context"
	"sync"
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
		exp := c.Set("k", 1, cache.Never)
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
		c.Set("k", 42, cache.Never)
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
		c.Set("k", 1, cache.After(time.Nanosecond))
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
		exp := c.Set("k", 1, cache.After(time.Hour))
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})

	t.Run("Never yields the zero time", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, cache.Never)
		assert.True(t, exp.IsZero())
	})

	t.Run("Default uses the configured default", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int](cache.WithDefaultTTL(30 * time.Minute))
		exp := c.Set("k", 1, cache.Default)
		assert.WithinDuration(t, time.Now().Add(30*time.Minute), exp, time.Second)
	})

	t.Run("overwrites the existing value", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, cache.Never)
		c.Set("k", 2, cache.Never)
		v, _ := c.Get("k")
		assert.Equal(t, 2, v)
	})

	t.Run("Keep preserves the existing expiry", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		first := c.Set("k", 1, cache.After(time.Hour))
		time.Sleep(5 * time.Millisecond)
		kept := c.Set("k", 2, cache.Keep)
		assert.Equal(t, first, kept)
		v, _ := c.Get("k")
		assert.Equal(t, 2, v)
	})

	t.Run("Keep on an absent key falls back to Default", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		exp := c.Set("k", 1, cache.Keep)
		assert.True(t, exp.IsZero())
	})

	t.Run("Keep on an expired entry does not resurrect a stale expiry", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 1, cache.After(time.Nanosecond))
		time.Sleep(time.Millisecond)
		exp := c.Set("k", 2, cache.Keep)
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
		c.Set("k", 1, cache.Never)
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
		c.Set("a", 1, cache.Never)
		c.Set("b", 2, cache.Never)
		got := map[string]int{}
		for k, v := range c.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"a": 1, "b": 2}, got)
	})

	t.Run("excludes expired entries", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("live", 1, cache.After(time.Hour))
		c.Set("dead", 2, cache.After(time.Nanosecond))
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
			c.Set(i, i, cache.Never)
		}
		count := 0
		for k := range c.Items() {
			c.Set(k+100, k, cache.Never)
			c.Delete(k)
			count++
		}
		assert.Equal(t, 5, count)
	})

	t.Run("stops early when the body breaks", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[int, int]()
		for i := range 10 {
			c.Set(i, i, cache.Never)
		}
		seen := 0
		for range c.Items() {
			seen++
			break
		}
		assert.Equal(t, 1, seen)
	})
}

func TestMemory_ItemsContext(t *testing.T) {
	t.Parallel()

	t.Run("yields live entries with no error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("a", 1, cache.Never)
		c.Set("b", 2, cache.Never)
		seq, errf := c.ItemsContext(context.Background())
		got := map[string]int{}
		for k, v := range seq {
			got[k] = v
		}
		require.NoError(t, errf())
		assert.Equal(t, map[string]int{"a": 1, "b": 2}, got)
	})

	t.Run("a cancelled context stops iteration and reports the error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[int, int]()
		for i := range 10 {
			c.Set(i, i, cache.Never)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		seq, errf := c.ItemsContext(ctx)
		n := 0
		for range seq {
			n++
		}
		assert.Zero(t, n)
		assert.ErrorIs(t, errf(), context.Canceled)
	})
}

func TestMemory_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("matches Get and returns no error", func(t *testing.T) {
		t.Parallel()
		c := cache.NewMemory[string, int]()
		c.Set("k", 7, cache.Never)
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
		exp, err := c.SetContext(context.Background(), "k", 1, cache.After(time.Hour))
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
		c.Set("k", 1, cache.Never)
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
		c.Set("k", 1, cache.Never)
		v, ok := c.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 1, v)
	})
}

func TestMemory_Concurrent(t *testing.T) {
	t.Parallel()

	// Hammer the cache from many goroutines with a background janitor running, so
	// the -race detector exercises every lock path on the core type.
	c := cache.NewMemory[int, int](cache.WithCleanupInterval(time.Millisecond))
	defer func() { _ = c.Close() }()

	const goroutines = 8
	const ops = 500
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ops {
				key := (g*ops + i) % 64
				switch i % 4 {
				case 0:
					c.Set(key, i, cache.After(time.Minute))
				case 1:
					c.Get(key)
				case 2:
					c.Delete(key)
				default:
					for range c.Items() {
					}
				}
			}
		}()
	}
	wg.Wait()

	// Still usable after the storm.
	c.Set(1, 1, cache.Never)
	v, ok := c.Get(1)
	assert.True(t, ok)
	assert.Equal(t, 1, v)
}
