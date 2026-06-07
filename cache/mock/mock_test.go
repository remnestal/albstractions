package mock_test

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/remnestal/albstractions/cache"
	"github.com/remnestal/albstractions/cache/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackend_Get(t *testing.T) {
	t.Parallel()

	t.Run("treats a GetFunc error as a miss", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{
			GetFunc: func(context.Context, string) (int, bool, error) {
				return 0, false, errors.New("boom")
			},
		}
		_, ok := b.Get("k")
		assert.False(t, ok)
	})
}

func TestBackend_GetContext(t *testing.T) {
	t.Parallel()

	t.Run("misses by default", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{}
		v, ok, err := b.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, v)
	})

	t.Run("delegates to GetFunc", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{
			GetFunc: func(context.Context, string) (int, bool, error) { return 42, true, nil },
		}
		v, ok, err := b.GetContext(context.Background(), "k")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, 42, v)
	})
}

func TestBackend_SetContext(t *testing.T) {
	t.Parallel()

	t.Run("succeeds without storing by default", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{}
		exp, err := b.SetContext(context.Background(), "k", 1, cache.After(time.Hour))
		require.NoError(t, err)
		assert.True(t, exp.IsZero())
	})

	t.Run("delegates to SetFunc", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		b := &mock.Backend[string, int]{
			SetFunc: func(context.Context, string, int, cache.Expiry) (time.Time, error) {
				return time.Time{}, boom
			},
		}
		_, err := b.SetContext(context.Background(), "k", 1, cache.After(time.Hour))
		assert.ErrorIs(t, err, boom)
	})
}

func TestBackend_DeleteContext(t *testing.T) {
	t.Parallel()

	t.Run("succeeds by default", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{}
		assert.NoError(t, b.DeleteContext(context.Background(), "k"))
	})

	t.Run("delegates to DeleteFunc", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		b := &mock.Backend[string, int]{
			DeleteFunc: func(context.Context, string) error { return boom },
		}
		assert.ErrorIs(t, b.DeleteContext(context.Background(), "k"), boom)
	})
}

func TestBackend_Items(t *testing.T) {
	t.Parallel()

	t.Run("yields nothing by default", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{}
		count := 0
		for range b.Items() {
			count++
		}
		assert.Zero(t, count)
	})

	t.Run("delegates to ItemsFunc", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{
			ItemsFunc: func() iter.Seq2[string, int] {
				return func(yield func(string, int) bool) { yield("a", 1) }
			},
		}
		got := map[string]int{}
		for k, v := range b.Items() {
			got[k] = v
		}
		assert.Equal(t, map[string]int{"a": 1}, got)
	})
}

func TestBackend_Calls(t *testing.T) {
	t.Parallel()

	t.Run("records each call in order", func(t *testing.T) {
		t.Parallel()
		b := &mock.Backend[string, int]{}
		b.Set("k", 5, cache.After(time.Minute))
		b.Get("k")
		b.Delete("k")

		calls := b.Calls()
		require.Len(t, calls, 3)
		assert.Equal(t, "Set", calls[0].Op)
		assert.Equal(t, 5, calls[0].Val)
		assert.Equal(t, cache.After(time.Minute), calls[0].Exp)
		assert.Equal(t, "Get", calls[1].Op)
		assert.Equal(t, "Delete", calls[2].Op)
	})

	t.Run("records interactions driven by a wrapper", func(t *testing.T) {
		t.Parallel()
		front := &mock.Backend[string, int]{}
		rt := cache.NewReadThrough[string, int](front, func(context.Context, string) (int, error) {
			return 7, nil
		})

		v, ok := rt.Get("k")
		require.True(t, ok)
		assert.Equal(t, 7, v)

		var ops []string
		for _, c := range front.Calls() {
			ops = append(ops, c.Op)
		}
		assert.Equal(t, []string{"Get", "Set"}, ops)
	})
}
