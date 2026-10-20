package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now()

	cases := []struct {
		name     string
		cfg      memoryConfig
		exp      Expiry
		existing time.Time
		found    bool
		want     time.Time
	}{
		// After / At.
		{"after a positive duration", memoryConfig{}, After(time.Minute), time.Time{}, false, now.Add(time.Minute)},
		{"after zero expires immediately", memoryConfig{}, After(0), time.Time{}, false, now},
		{"at a future time is exact", memoryConfig{}, At(now.Add(time.Hour)), time.Time{}, false, now.Add(time.Hour)},
		{"at a past time expires immediately", memoryConfig{}, At(now.Add(-time.Hour)), time.Time{}, false, now},

		// Never.
		{"never without a max", memoryConfig{}, Never, time.Time{}, false, time.Time{}},
		{"never is capped by max", memoryConfig{maxTTL: time.Hour}, Never, time.Time{}, false, now.Add(time.Hour)},

		// Default.
		{"default unset resolves to never", memoryConfig{}, Default, time.Time{}, false, time.Time{}},
		{"default resolves to its duration", memoryConfig{defaultTTL: time.Minute}, Default, time.Time{}, false, now.Add(time.Minute)},
		{"default clamped down to max", memoryConfig{defaultTTL: time.Hour, maxTTL: time.Minute}, Default, time.Time{}, false, now.Add(time.Minute)},

		// Clamping of relative lifetimes.
		{"after clamped up to min", memoryConfig{minTTL: time.Hour}, After(time.Minute), time.Time{}, false, now.Add(time.Hour)},
		{"after clamped down to max", memoryConfig{maxTTL: time.Minute}, After(time.Hour), time.Time{}, false, now.Add(time.Minute)},
		{"elapsed at floored up to min", memoryConfig{minTTL: time.Minute}, At(now.Add(-time.Hour)), time.Time{}, false, now.Add(time.Minute)},

		// Keep.
		{"keep preserves the existing expiry", memoryConfig{}, Keep, now.Add(time.Hour), true, now.Add(time.Hour)},
		{"keep does not re-floor a near expiry below min", memoryConfig{minTTL: time.Hour}, Keep, now.Add(time.Minute), true, now.Add(time.Minute)},
		{"keep without an entry uses the default", memoryConfig{defaultTTL: time.Minute}, Keep, time.Time{}, false, now.Add(time.Minute)},
		{"keep without an entry and no default is never", memoryConfig{}, Keep, time.Time{}, false, time.Time{}},
		{"keep without an entry is capped by max", memoryConfig{maxTTL: time.Hour}, Keep, time.Time{}, false, now.Add(time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.cfg.resolveExpiry(tc.exp, now, tc.existing, tc.found)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMemoryLookup(t *testing.T) {
	t.Parallel()

	getters := []struct {
		name string
		get  func(m *Memory[string, int]) bool
	}{
		{"Get", func(m *Memory[string, int]) bool { _, ok := m.Get("k"); return ok }},
		{"GetContext", func(m *Memory[string, int]) bool {
			_, ok, _ := m.GetContext(context.Background(), "k")
			return ok
		}},
	}
	for _, g := range getters {
		t.Run("removes an expired entry on read via "+g.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				m := NewMemory[string, int]()
				m.Set("k", 1, After(time.Second))
				time.Sleep(2 * time.Second)

				assert.False(t, g.get(m))
				m.mu.RLock()
				defer m.mu.RUnlock()
				assert.Empty(t, m.m, "an expired entry must be deleted when read")
			})
		})
	}

	t.Run("keeps a live entry", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			m := NewMemory[string, int]()
			m.Set("k", 1, After(time.Hour))

			v, ok := m.Get("k")
			assert.True(t, ok)
			assert.Equal(t, 1, v)
			m.mu.RLock()
			defer m.mu.RUnlock()
			assert.Len(t, m.m, 1)
		})
	})

	t.Run("does not remove an entry re-set concurrently", func(t *testing.T) {
		t.Parallel()
		// Needs real interleaving, so no synctest. Readers that saw the expired
		// entry must not delete the fresh one written after it.
		m := NewMemory[string, int]()
		var stop atomic.Bool
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for !stop.Load() {
					m.Get("k")
				}
			})
		}
		lost := 0
		for i := range 1000 {
			m.Set("k", i, At(time.Now().Add(-time.Hour)))
			m.Set("k", i, After(time.Hour))
			if _, ok := m.Get("k"); !ok {
				lost++
			}
		}
		stop.Store(true)
		wg.Wait()
		assert.Zero(t, lost, "fresh entries removed by readers of an earlier, expired one")
	})
}

func TestMemoryCleanup(t *testing.T) {
	t.Parallel()

	m := NewMemory[string, int]()
	m.Set("live", 1, After(time.Hour))
	m.Set("dead", 2, After(time.Nanosecond))
	time.Sleep(time.Millisecond)

	m.cleanup()

	m.mu.RLock()
	defer m.mu.RUnlock()
	require.Len(t, m.m, 1)
	_, ok := m.m["live"]
	assert.True(t, ok)
}

func TestMemoryRebuild(t *testing.T) {
	t.Parallel()

	m := NewMemory[string, int]()
	m.Set("live", 1, After(time.Hour))
	m.Set("dead", 2, After(time.Nanosecond))
	time.Sleep(time.Millisecond)

	m.mu.RLock()
	old := m.m
	m.mu.RUnlock()

	m.rebuild()

	m.mu.RLock()
	defer m.mu.RUnlock()
	require.Len(t, m.m, 1)
	_, ok := m.m["live"]
	assert.True(t, ok)
	// The original map is left untouched, proving a fresh map was swapped in.
	assert.Len(t, old, 2)
}

func TestMemoryBackgroundCleanup(t *testing.T) {
	t.Parallel()

	m := NewMemory[string, int](WithCleanupInterval(5 * time.Millisecond))
	defer func() { _ = m.Close() }()
	m.Set("dead", 1, After(time.Nanosecond))

	assert.Eventually(t, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.m) == 0
	}, time.Second, 5*time.Millisecond)
}

func TestMemoryBackgroundRebuild(t *testing.T) {
	t.Parallel()

	m := NewMemory[string, int](WithRebuildInterval(5 * time.Millisecond))
	defer func() { _ = m.Close() }()
	m.Set("dead", 1, After(time.Nanosecond))

	assert.Eventually(t, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.m) == 0
	}, time.Second, 5*time.Millisecond)
}
