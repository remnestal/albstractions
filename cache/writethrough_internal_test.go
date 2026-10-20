package cache

import (
	"context"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteThroughLock(t *testing.T) {
	t.Parallel()

	okWriter := func(context.Context, float64, int) error { return nil }

	t.Run("removes lock entries once unused", func(t *testing.T) {
		t.Parallel()
		w := NewWriteThrough[float64, int](NewMemory[float64, int](), okWriter)
		for k := range 10 {
			_, err := w.SetContext(t.Context(), float64(k), k, Never)
			require.NoError(t, err)
			require.NoError(t, w.DeleteContext(t.Context(), float64(k)))
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		assert.Empty(t, w.locks)
	})

	t.Run("does not leak entries for NaN keys", func(t *testing.T) {
		t.Parallel()
		w := NewWriteThrough[float64, int](NewMemory[float64, int](), okWriter)
		for range 10 {
			_, err := w.SetContext(t.Context(), math.NaN(), 1, Never)
			require.NoError(t, err)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		assert.Empty(t, w.locks)
	})

	t.Run("removes the entry of a waiter whose context ended", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			slowWriter := func(context.Context, float64, int) error {
				time.Sleep(2 * time.Second)
				return nil
			}
			w := NewWriteThrough[float64, int](NewMemory[float64, int](), slowWriter)

			var wg sync.WaitGroup
			wg.Go(func() { _, _ = w.SetContext(t.Context(), 1, 1, Never) })
			synctest.Wait() // the first write holds the key

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := w.SetContext(ctx, 1, 2, Never)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			wg.Wait()

			w.mu.Lock()
			defer w.mu.Unlock()
			assert.Empty(t, w.locks)
		})
	})
}
