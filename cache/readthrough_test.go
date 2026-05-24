package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func constLoader[K comparable, V any](val V, err error) cache.Loader[K, V] {
	return func(context.Context, K) (V, error) { return val, err }
}

func TestNewReadThrough(t *testing.T) {
	t.Parallel()

	t.Run("panics on a nil front", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewReadThrough[string, int](nil, constLoader[string](0, nil))
		})
	})

	t.Run("panics on a nil loader", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewReadThrough[string, int](cache.NewMemory[string, int](), nil)
		})
	})
}

func TestReadThrough_Get(t *testing.T) {
	t.Parallel()

	t.Run("loads and populates on a miss", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		rt := cache.NewReadThrough[string, int](front, constLoader[string](5, nil))
		v, ok := rt.Get("k")
		require.True(t, ok)
		assert.Equal(t, 5, v)

		fv, fok := front.Get("k")
		assert.True(t, fok)
		assert.Equal(t, 5, fv)
	})

	t.Run("returns a clean miss when the loader reports absence", func(t *testing.T) {
		t.Parallel()
		rt := cache.NewReadThrough[string, int](cache.NewMemory[string, int](), constLoader[string](0, cache.ErrCacheMiss))
		_, ok := rt.Get("k")
		assert.False(t, ok)
	})
}

func TestReadThrough_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("returns a front hit without loading", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int64
		loader := func(context.Context, string) (int, error) { calls.Add(1); return 0, nil }
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.NoExpiration)
		rt := cache.NewReadThrough[string, int](front, loader)

		v, ok, err := rt.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 1, v)
		assert.Zero(t, calls.Load())
	})

	t.Run("recognised miss returns false with no error", func(t *testing.T) {
		t.Parallel()
		rt := cache.NewReadThrough[string, int](cache.NewMemory[string, int](), constLoader[string](0, cache.ErrCacheMiss))
		_, ok, err := rt.GetContext(context.Background(), "k")
		assert.False(t, ok)
		assert.NoError(t, err)
	})

	t.Run("unrecognised loader error propagates", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		rt := cache.NewReadThrough[string, int](cache.NewMemory[string, int](), constLoader[string](0, boom))
		_, ok, err := rt.GetContext(context.Background(), "k")
		assert.False(t, ok)
		assert.ErrorIs(t, err, boom)
	})
}

func TestReadThrough_Set(t *testing.T) {
	t.Parallel()

	t.Run("passes through to the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		rt.Set("k", 3, cache.NoExpiration)
		v, ok := front.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 3, v)
	})
}

func TestReadThrough_Delete(t *testing.T) {
	t.Parallel()

	t.Run("passes through to the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.NoExpiration)
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		rt.Delete("k")
		_, ok := front.Get("k")
		assert.False(t, ok)
	})
}

func TestReadThrough_Items(t *testing.T) {
	t.Parallel()

	t.Run("iterates the front only", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("a", 1, cache.NoExpiration)
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		got := map[string]int{}
		for k, v := range rt.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"a": 1}, got)
	})
}

func TestReadThrough_SetContext(t *testing.T) {
	t.Parallel()

	t.Run("passes through to the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		_, err := rt.SetContext(context.Background(), "k", 4, cache.NoExpiration)
		require.NoError(t, err)
		v, ok := front.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 4, v)
	})
}

func TestReadThrough_DeleteContext(t *testing.T) {
	t.Parallel()

	t.Run("passes through to the front", func(t *testing.T) {
		t.Parallel()
		front := cache.NewMemory[string, int]()
		front.Set("k", 1, cache.NoExpiration)
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		require.NoError(t, rt.DeleteContext(context.Background(), "k"))
		_, ok := front.Get("k")
		assert.False(t, ok)
	})
}

func TestReadThrough_Close(t *testing.T) {
	t.Parallel()

	t.Run("closes the front when it implements io.Closer", func(t *testing.T) {
		t.Parallel()
		var closes atomic.Int64
		front := closerCache{Memory: cache.NewMemory[string, int](), closes: &closes}
		rt := cache.NewReadThrough[string, int](front, constLoader[string](0, nil))
		require.NoError(t, rt.Close())
		assert.Equal(t, int64(1), closes.Load())
	})
}

func TestWithSingleFlight(t *testing.T) {
	t.Parallel()

	t.Run("collapses concurrent loads of the same key", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int64
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		loader := func(context.Context, string) (int, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return 42, nil
		}
		rt := cache.NewReadThrough[string, int](cache.NewMemory[string, int](), loader, cache.WithSingleFlight())

		const n = 20
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, ok := rt.Get("k")
				assert.True(t, ok)
				assert.Equal(t, 42, v)
			}()
		}
		<-entered
		time.Sleep(20 * time.Millisecond)
		close(release)
		wg.Wait()

		assert.Equal(t, int64(1), calls.Load())
	})
}

func TestWithMissError(t *testing.T) {
	t.Parallel()

	t.Run("treats a registered error as a miss", func(t *testing.T) {
		t.Parallel()
		errNotFound := errors.New("not found")
		rt := cache.NewReadThrough[string, int](
			cache.NewMemory[string, int](),
			constLoader[string](0, errNotFound),
			cache.WithMissError(errNotFound),
		)
		_, ok, err := rt.GetContext(context.Background(), "k")
		assert.False(t, ok)
		assert.NoError(t, err)
	})
}

func TestLoaderFromCache(t *testing.T) {
	t.Parallel()

	t.Run("returns the stored value", func(t *testing.T) {
		t.Parallel()
		src := cache.NewMemory[string, int]()
		src.Set("k", 8, cache.NoExpiration)
		load := cache.LoaderFromCache[string, int](src)
		v, err := load(context.Background(), "k")
		require.NoError(t, err)
		assert.Equal(t, 8, v)
	})

	t.Run("returns ErrCacheMiss on a miss", func(t *testing.T) {
		t.Parallel()
		load := cache.LoaderFromCache[string, int](cache.NewMemory[string, int]())
		_, err := load(context.Background(), "absent")
		assert.ErrorIs(t, err, cache.ErrCacheMiss)
	})

	t.Run("composes read-through tiers and populates both", func(t *testing.T) {
		t.Parallel()
		var originCalls atomic.Int64
		origin := func(_ context.Context, key string) (int, error) {
			originCalls.Add(1)
			if key == "k" {
				return 7, nil
			}
			return 0, cache.ErrCacheMiss
		}

		l2Front := cache.NewMemory[string, int]()
		l2 := cache.NewReadThrough[string, int](l2Front, origin)
		l1Front := cache.NewMemory[string, int]()
		l1 := cache.NewReadThrough[string, int](l1Front, cache.LoaderFromCache[string, int](l2))

		v, ok := l1.Get("k")
		require.True(t, ok)
		assert.Equal(t, 7, v)

		_, ok1 := l1Front.Get("k")
		assert.True(t, ok1)
		_, ok2 := l2Front.Get("k")
		assert.True(t, ok2)

		l1.Get("k")
		assert.Equal(t, int64(1), originCalls.Load())

		_, miss := l1.Get("absent")
		assert.False(t, miss)
	})
}
