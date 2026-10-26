package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func okWriter(context.Context, string, int) error { return nil }

func TestNewWriteThrough(t *testing.T) {
	t.Parallel()

	t.Run("panics on a nil front", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.NewWriteThrough[string, int](nil, okWriter) })
	})

	t.Run("panics on a nil writer", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewWriteThrough[string, int](cache.NewMemory[string, int](), nil)
		})
	})
}

func TestWriteThrough_SetContext(t *testing.T) {
	t.Parallel()

	t.Run("writes to the backing store then the front", func(t *testing.T) {
		t.Parallel()
		backing := map[string]int{}
		writer := func(_ context.Context, k string, v int) error { backing[k] = v; return nil }
		front := cache.NewMemory[string, int]()
		wt := cache.NewWriteThrough[string, int](front, writer)

		_, err := wt.SetContext(context.Background(), "k", 5, cache.Never)
		require.NoError(t, err)
		assert.Equal(t, 5, backing["k"])
		v, ok := front.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 5, v)
	})

	t.Run("a backing failure leaves the front untouched", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		writer := func(context.Context, string, int) error { return boom }
		front := cache.NewMemory[string, int]()
		wt := cache.NewWriteThrough[string, int](front, writer)

		_, err := wt.SetContext(context.Background(), "k", 5, cache.Never)
		assert.ErrorIs(t, err, boom)
		_, ok := front.Get("k")
		assert.False(t, ok)
	})

	t.Run("concurrent writes to one key leave the front matching the store", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var mu sync.Mutex
			store := map[string]int{}
			writer := func(_ context.Context, k string, v int) error {
				mu.Lock()
				store[k] = v
				mu.Unlock()
				if v == 1 {
					time.Sleep(time.Second) // the first write stalls after reaching the store
				}
				return nil
			}
			front := cache.NewMemory[string, int]()
			wt := cache.NewWriteThrough[string, int](front, writer)

			var wg sync.WaitGroup
			wg.Go(func() { _, _ = wt.SetContext(t.Context(), "k", 1, cache.Never) })
			synctest.Wait() // the first write has reached the store and is stalled

			_, _ = wt.SetContext(t.Context(), "k", 2, cache.Never)
			wg.Wait()

			got, _ := front.Get("k")
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, store["k"], got, "front diverged from the backing store")
		})
	})

	t.Run("returns the context cause when it ends while waiting for a write to the same key", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var mu sync.Mutex
			var calls []int
			writer := func(_ context.Context, _ string, v int) error {
				mu.Lock()
				calls = append(calls, v)
				mu.Unlock()
				time.Sleep(2 * time.Second)
				return nil
			}
			front := cache.NewMemory[string, int]()
			wt := cache.NewWriteThrough[string, int](front, writer)

			var wg sync.WaitGroup
			wg.Go(func() { _, _ = wt.SetContext(t.Context(), "k", 1, cache.Never) })
			synctest.Wait() // the first write holds the key

			cause := errors.New("gave up")
			ctx, cancel := context.WithTimeoutCause(t.Context(), time.Second, cause)
			defer cancel()
			_, err := wt.SetContext(ctx, "k", 2, cache.Never)
			wg.Wait()

			assert.ErrorIs(t, err, cause)
			got, _ := front.Get("k")
			assert.Equal(t, 1, got)
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, []int{1}, calls, "the waiting write must not reach the store")
		})
	})

	t.Run("writes to different keys do not wait for each other", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			writer := func(_ context.Context, k string, _ int) error {
				if k == "slow" {
					time.Sleep(time.Hour)
				}
				return nil
			}
			wt := cache.NewWriteThrough[string, int](cache.NewMemory[string, int](), writer)

			var wg sync.WaitGroup
			defer wg.Wait()
			wg.Go(func() { _, _ = wt.SetContext(t.Context(), "slow", 1, cache.Never) })
			synctest.Wait()

			start := time.Now()
			_, err := wt.SetContext(t.Context(), "fast", 1, cache.Never)
			require.NoError(t, err)
			assert.Zero(t, time.Since(start))
		})
	})
}

func TestWriteThrough_Set(t *testing.T) {
	t.Parallel()

	t.Run("writes through and returns the front expiry", func(t *testing.T) {
		t.Parallel()
		backing := map[string]int{}
		writer := func(_ context.Context, k string, v int) error { backing[k] = v; return nil }
		front := cache.NewMemory[string, int]()
		wt := cache.NewWriteThrough[string, int](front, writer)

		exp := wt.Set("k", 1, cache.After(time.Hour))
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
		assert.Equal(t, 1, backing["k"])
	})
}

func TestWriteThrough_Get(t *testing.T) {
	t.Parallel()

	t.Run("reads from the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("k", 9, cache.Never)
		wt := cache.NewWriteThrough[string, int](front, okWriter)
		v, ok := wt.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 9, v)
	})
}

func TestWriteThrough_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("reads from the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("k", 9, cache.Never)
		wt := cache.NewWriteThrough[string, int](front, okWriter)
		v, ok, err := wt.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 9, v)
	})
}

func TestWriteThrough_DeleteContext(t *testing.T) {
	t.Parallel()

	t.Run("deletes from the backing store then the front", func(t *testing.T) {
		t.Parallel()
		backing := map[string]int{"k": 1}
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.Never)
		deleter := func(_ context.Context, k string) error { delete(backing, k); return nil }
		wt := cache.NewWriteThrough[string, int](front, okWriter, cache.WithDeleter[string, int](deleter))

		require.NoError(t, wt.DeleteContext(context.Background(), "k"))
		_, inBacking := backing["k"]
		assert.False(t, inBacking)
		_, ok := front.Get("k")
		assert.False(t, ok)
	})

	t.Run("a backing failure leaves the front untouched", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.Never)
		deleter := func(context.Context, string) error { return boom }
		wt := cache.NewWriteThrough[string, int](front, okWriter, cache.WithDeleter[string, int](deleter))

		assert.ErrorIs(t, wt.DeleteContext(context.Background(), "k"), boom)
		_, ok := front.Get("k")
		assert.True(t, ok)
	})

	t.Run("without a deleter only the front is affected", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.Never)
		wt := cache.NewWriteThrough[string, int](front, okWriter)

		require.NoError(t, wt.DeleteContext(context.Background(), "k"))
		_, ok := front.Get("k")
		assert.False(t, ok)
	})

	t.Run("a delete racing a write leaves the front matching the store", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			var mu sync.Mutex
			store := map[string]int{}
			writer := func(_ context.Context, k string, v int) error {
				mu.Lock()
				store[k] = v
				mu.Unlock()
				time.Sleep(time.Second) // stall after reaching the store
				return nil
			}
			deleter := func(_ context.Context, k string) error {
				mu.Lock()
				delete(store, k)
				mu.Unlock()
				return nil
			}
			front := cache.NewMemory[string, int]()
			wt := cache.NewWriteThrough[string, int](front, writer, cache.WithDeleter[string, int](deleter))

			var wg sync.WaitGroup
			wg.Go(func() { _, _ = wt.SetContext(t.Context(), "k", 1, cache.Never) })
			synctest.Wait()
			require.NoError(t, wt.DeleteContext(t.Context(), "k"))
			wg.Wait()

			_, inFront := front.Get("k")
			mu.Lock()
			_, inStore := store["k"]
			mu.Unlock()
			assert.False(t, inStore)
			assert.False(t, inFront, "front diverged from the backing store")
		})
	})
}

func TestWriteThrough_Items(t *testing.T) {
	t.Parallel()

	t.Run("iterates the front only", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("a", 1, cache.Never)
		wt := cache.NewWriteThrough[string, int](front, okWriter)
		got := map[string]int{}
		for k, v := range wt.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"a": 1}, got)
	})
}

func TestWriteThrough_ItemsContext(t *testing.T) {
	t.Parallel()

	t.Run("iterates the front only", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("a", 1, cache.Never)
		wt := cache.NewWriteThrough[string, int](front, okWriter)
		seq, errf := wt.ItemsContext(context.Background())
		got := map[string]int{}
		for k, v := range seq {
			got[k] = v
		}
		require.NoError(t, errf())
		assert.Equal(t, map[string]int{"a": 1}, got)
	})
}

func TestWriteThrough_Delete(t *testing.T) {
	t.Parallel()

	t.Run("deletes through the configured deleter", func(t *testing.T) {
		t.Parallel()
		backing := map[string]int{"k": 1}
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.Never)
		deleter := func(_ context.Context, k string) error { delete(backing, k); return nil }
		wt := cache.NewWriteThrough[string, int](front, okWriter, cache.WithDeleter[string, int](deleter))

		wt.Delete("k")
		_, inBacking := backing["k"]
		assert.False(t, inBacking)
		_, ok := front.Get("k")
		assert.False(t, ok)
	})
}

func TestWriteThrough_Close(t *testing.T) {
	t.Parallel()

	t.Run("closes the front when it implements io.Closer", func(t *testing.T) {
		t.Parallel()
		var closes atomic.Int64
		front := closerCache{Memory: cache.NewMemory[string, int](), closes: &closes}
		wt := cache.NewWriteThrough[string, int](front, okWriter)
		require.NoError(t, wt.Close())
		assert.Equal(t, int64(1), closes.Load())
	})
}

func TestWithDeleter(t *testing.T) {
	t.Parallel()

	t.Run("panics on a nil deleter", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { cache.WithDeleter[string, int](nil) })
	})
}
