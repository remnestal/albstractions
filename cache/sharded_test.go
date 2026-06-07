package cache_test

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/remnestal/albstractions/cache/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// closerCache is a Memory that records how many times it is closed, used to
// verify that Sharded.Close cascades to its backends.
type closerCache struct {
	*cache.Memory[string, int]
	closes *atomic.Int64
}

func (c closerCache) Close() error {
	c.closes.Add(1)
	return c.Memory.Close()
}

func memoryShards[K comparable, V any]() func() cache.Cache[K, V] {
	return func() cache.Cache[K, V] { return cache.NewMemory[K, V]() }
}

func TestHashShard(t *testing.T) {
	t.Parallel()

	t.Run("is deterministic for a given function", func(t *testing.T) {
		t.Parallel()
		h := cache.HashShard[string]()
		assert.Equal(t, h("key"), h("key"))
	})

	t.Run("works as a Sharded algorithm", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		s.Set("k", 1, cache.Never)
		v, ok := s.Get("k")
		assert.True(t, ok)
		assert.Equal(t, 1, v)
	})
}

func TestModuloShard(t *testing.T) {
	t.Parallel()

	t.Run("maps an integer to its own value", func(t *testing.T) {
		t.Parallel()
		m := cache.ModuloShard[int]()
		assert.Equal(t, uint64(5), m(5))
	})

	t.Run("routes every key to a retrievable backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[int, int](8, cache.ModuloShard[int](), memoryShards[int, int]())
		for i := range 100 {
			s.Set(i, i*2, cache.Never)
		}
		for i := range 100 {
			v, ok := s.Get(i)
			require.True(t, ok)
			assert.Equal(t, i*2, v)
		}
	})
}

func TestNewSharded(t *testing.T) {
	t.Parallel()

	t.Run("panics on a size below one", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewSharded[string, int](0, cache.HashShard[string](), memoryShards[string, int]())
		})
	})

	t.Run("panics on a nil algorithm", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewSharded[string, int](2, nil, memoryShards[string, int]())
		})
	})

	t.Run("panics on a nil constructor", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			cache.NewSharded[string, int](2, cache.HashShard[string](), nil)
		})
	})

	t.Run("routes every key to a retrievable backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[int, int](8, cache.HashShard[int](), memoryShards[int, int]())
		for i := range 100 {
			s.Set(i, i*2, cache.Never)
		}
		for i := range 100 {
			v, ok := s.Get(i)
			require.True(t, ok)
			assert.Equal(t, i*2, v)
		}
	})
}

func TestSharded_Get(t *testing.T) {
	t.Parallel()

	t.Run("returns a value stored through the same shard", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		s.Set("k", 11, cache.Never)
		v, ok := s.Get("k")
		require.True(t, ok)
		assert.Equal(t, 11, v)
	})

	t.Run("reports a missing key", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		_, ok := s.Get("absent")
		assert.False(t, ok)
	})
}

func TestSharded_Set(t *testing.T) {
	t.Parallel()

	t.Run("stores and returns the resolved expiry", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		exp := s.Set("k", 1, cache.After(time.Hour))
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})
}

func TestSharded_Delete(t *testing.T) {
	t.Parallel()

	t.Run("removes a key from its backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		s.Set("k", 1, cache.Never)
		s.Delete("k")
		_, ok := s.Get("k")
		assert.False(t, ok)
	})
}

func TestSharded_Items(t *testing.T) {
	t.Parallel()

	t.Run("unions the live entries of every backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[int, int](4, cache.HashShard[int](), memoryShards[int, int]())
		for i := range 100 {
			s.Set(i, i, cache.Never)
		}
		got := map[int]int{}
		for k, v := range s.Items() {
			got[k] = v
		}
		assert.Len(t, got, 100)
	})
}

func TestSharded_ItemsContext(t *testing.T) {
	t.Parallel()

	t.Run("unions live entries with no error", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[int, int](4, cache.HashShard[int](), memoryShards[int, int]())
		for i := range 50 {
			s.Set(i, i, cache.Never)
		}
		seq, errf := s.ItemsContext(context.Background())
		got := map[int]int{}
		for k, v := range seq {
			got[k] = v
		}
		require.NoError(t, errf())
		assert.Len(t, got, 50)
	})

	t.Run("reports a backend error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("scan failed")
		s := cache.NewSharded[string, int](2, cache.HashShard[string](), func() cache.Cache[string, int] {
			return &mock.Backend[string, int]{
				ItemsContextFunc: func(context.Context) (iter.Seq2[string, int], func() error) {
					return func(func(string, int) bool) {}, func() error { return boom }
				},
			}
		})
		seq, errf := s.ItemsContext(context.Background())
		for range seq {
		}
		assert.ErrorIs(t, errf(), boom)
	})
}

func TestSharded_Close(t *testing.T) {
	t.Parallel()

	t.Run("cascades to backends that implement io.Closer", func(t *testing.T) {
		t.Parallel()
		var closes atomic.Int64
		s := cache.NewSharded[string, int](3, cache.HashShard[string](), func() cache.Cache[string, int] {
			return closerCache{Memory: cache.NewMemory[string, int](), closes: &closes}
		})
		require.NoError(t, s.Close())
		assert.Equal(t, int64(3), closes.Load())
	})

	t.Run("skips backends that are not io.Closer", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](3, cache.HashShard[string](), func() cache.Cache[string, int] {
			return &mock.Backend[string, int]{}
		})
		assert.NoError(t, s.Close())
	})
}

func TestSharded_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("delegates to the routed backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		_, err := s.SetContext(context.Background(), "k", 9, cache.Never)
		require.NoError(t, err)
		v, ok, err := s.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 9, v)
	})
}

func TestSharded_SetContext(t *testing.T) {
	t.Parallel()

	t.Run("delegates to the routed backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		exp, err := s.SetContext(context.Background(), "k", 1, cache.After(time.Hour))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().Add(time.Hour), exp, time.Second)
	})
}

func TestSharded_DeleteContext(t *testing.T) {
	t.Parallel()

	t.Run("delegates to the routed backend", func(t *testing.T) {
		t.Parallel()
		s := cache.NewSharded[string, int](4, cache.HashShard[string](), memoryShards[string, int]())
		s.Set("k", 1, cache.Never)
		require.NoError(t, s.DeleteContext(context.Background(), "k"))
		_, ok := s.Get("k")
		assert.False(t, ok)
	})
}
