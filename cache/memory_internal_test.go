package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveExpiry(t *testing.T) {
	t.Parallel()

	now := time.Now()
	existing := now.Add(time.Hour)

	cases := []struct {
		name     string
		cfg      memoryConfig
		ttl      time.Duration
		existing time.Time
		found    bool
		want     time.Time
	}{
		{"positive ttl", memoryConfig{defaultTTL: NoExpiration}, time.Minute, time.Time{}, false, now.Add(time.Minute)},
		{"no expiration", memoryConfig{defaultTTL: NoExpiration}, NoExpiration, time.Time{}, false, time.Time{}},
		{"default resolves to no expiry", memoryConfig{defaultTTL: NoExpiration}, DefaultTTL, time.Time{}, false, time.Time{}},
		{"default resolves to a duration", memoryConfig{defaultTTL: time.Minute}, DefaultTTL, time.Time{}, false, now.Add(time.Minute)},
		{"keep with existing entry", memoryConfig{defaultTTL: NoExpiration}, KeepTTL, existing, true, existing},
		{"keep without existing entry", memoryConfig{defaultTTL: NoExpiration}, KeepTTL, time.Time{}, false, time.Time{}},
		{"clamped up to min", memoryConfig{defaultTTL: NoExpiration, minTTL: time.Hour}, time.Minute, time.Time{}, false, now.Add(time.Hour)},
		{"clamped down to max", memoryConfig{defaultTTL: NoExpiration, maxTTL: time.Minute}, time.Hour, time.Time{}, false, now.Add(time.Minute)},
		{"default then clamped to max", memoryConfig{defaultTTL: time.Hour, maxTTL: time.Minute}, DefaultTTL, time.Time{}, false, now.Add(time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.cfg.resolveExpiry(tc.ttl, now, tc.existing, tc.found)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMemoryCleanup(t *testing.T) {
	t.Parallel()

	m := NewMemory[string, int]()
	m.Set("live", 1, time.Hour)
	m.Set("dead", 2, time.Nanosecond)
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
	m.Set("live", 1, time.Hour)
	m.Set("dead", 2, time.Nanosecond)
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
	m.Set("dead", 1, time.Nanosecond)

	assert.Eventually(t, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return len(m.m) == 0
	}, time.Second, 5*time.Millisecond)
}
