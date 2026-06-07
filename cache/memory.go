package cache

import (
	"context"
	"iter"
	"sync"
	"time"
)

// entry is a stored value together with its absolute expiry. A zero expiresAt
// means the entry never expires.
type entry[V any] struct {
	value     V
	expiresAt time.Time
}

// Memory is an in-memory [Cache] backed by a single map guarded by an RWMutex.
//
// Expired entries are removed lazily on read and, when configured, by a
// background janitor ([WithCleanupInterval]) and a periodic map rebuild
// ([WithRebuildInterval]). Call [Memory.Close] to stop those goroutines.
//
// Values are stored by assignment: when V is a reference type (slice, map,
// pointer) the cache and its callers share the underlying data, so a retrieved
// value must not be mutated without external synchronisation.
//
// Memory is safe for concurrent use.
type Memory[K comparable, V any] struct {
	cfg       memoryConfig
	mu        sync.RWMutex
	m         map[K]entry[V]
	done      chan struct{}
	closeOnce sync.Once
}

var _ Cache[int, int] = (*Memory[int, int])(nil)

// NewMemory returns an empty [Memory].
//
// By default entries never expire and no background maintenance runs. Configure
// expiry with [WithDefaultTTL], [WithMinTTL], and [WithMaxTTL], and maintenance
// with [WithCleanupInterval] and [WithRebuildInterval].
//
// Panics if a configured minimum TTL exceeds the configured maximum.
func NewMemory[K comparable, V any](opts ...MemoryOption) *Memory[K, V] {
	cfg := memoryConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxTTL > 0 && cfg.minTTL > cfg.maxTTL {
		panic("cache.NewMemory: WithMinTTL exceeds WithMaxTTL")
	}
	m := &Memory[K, V]{
		cfg:  cfg,
		m:    make(map[K]entry[V]),
		done: make(chan struct{}),
	}
	if cfg.cleanupInterval > 0 {
		go m.loop(cfg.cleanupInterval, m.cleanup)
	}
	if cfg.rebuildInterval > 0 {
		go m.loop(cfg.rebuildInterval, m.rebuild)
	}
	return m
}

// Get implements [Cache.Get].
func (m *Memory[K, V]) Get(key K) (V, bool) {
	return m.lookup(key)
}

// GetContext implements [Cache.GetContext]. It never returns an error.
func (m *Memory[K, V]) GetContext(_ context.Context, key K) (V, bool, error) {
	v, ok := m.lookup(key)
	return v, ok, nil
}

// Set implements [Cache.Set].
func (m *Memory[K, V]) Set(key K, val V, exp Expiry) time.Time {
	return m.set(key, val, exp)
}

// SetContext implements [Cache.SetContext]. It never returns an error.
func (m *Memory[K, V]) SetContext(_ context.Context, key K, val V, exp Expiry) (time.Time, error) {
	return m.set(key, val, exp), nil
}

// Delete implements [Cache.Delete].
func (m *Memory[K, V]) Delete(key K) {
	m.mu.Lock()
	delete(m.m, key)
	m.mu.Unlock()
}

// DeleteContext implements [Cache.DeleteContext]. It never returns an error.
func (m *Memory[K, V]) DeleteContext(_ context.Context, key K) error {
	m.Delete(key)
	return nil
}

// Items implements [Cache.Items].
//
// It snapshots the live entries under a read lock when iteration begins, then
// yields from the snapshot with no lock held, so the loop body may safely call
// back into the cache.
func (m *Memory[K, V]) Items() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		now := time.Now()
		m.mu.RLock()
		keys := make([]K, 0, len(m.m))
		vals := make([]V, 0, len(m.m))
		for k, e := range m.m {
			if !expired(e.expiresAt, now) {
				keys = append(keys, k)
				vals = append(vals, e.value)
			}
		}
		m.mu.RUnlock()

		for i := range keys {
			if !yield(keys[i], vals[i]) {
				return
			}
		}
	}
}

// Close stops the janitor and rebuild goroutines.
//
// It is idempotent and safe to call concurrently. Reads and writes continue to
// work after Close; only background maintenance stops.
func (m *Memory[K, V]) Close() error {
	m.closeOnce.Do(func() { close(m.done) })
	return nil
}

// lookup reads key under a read lock, reporting expired entries as absent
// without removing them.
func (m *Memory[K, V]) lookup(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.m[key]
	if !ok || expired(e.expiresAt, time.Now()) {
		var zero V
		return zero, false
	}
	return e.value, true
}

// set stores val under key, resolving exp against the configured bounds and the
// existing entry, and returns the resulting expiry.
func (m *Memory[K, V]) set(key K, val V, exp Expiry) time.Time {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.m[key]
	// An expired-but-unswept entry counts as absent, so Keep never preserves a
	// stale past expiry.
	found := ok && !expired(existing.expiresAt, now)
	expiresAt := m.cfg.resolveExpiry(exp, now, existing.expiresAt, found)
	m.m[key] = entry[V]{value: val, expiresAt: expiresAt}
	return expiresAt
}

// loop runs fn on every tick until the cache is closed.
func (m *Memory[K, V]) loop(interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			fn()
		}
	}
}

// cleanup removes expired entries in place.
func (m *Memory[K, V]) cleanup() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.m {
		if expired(e.expiresAt, now) {
			delete(m.m, k)
		}
	}
}

// rebuild copies the live entries into a fresh map, reclaiming the bucket memory
// the old map retained after deletions.
func (m *Memory[K, V]) rebuild() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh := make(map[K]entry[V], len(m.m))
	for k, e := range m.m {
		if !expired(e.expiresAt, now) {
			fresh[k] = e
		}
	}
	m.m = fresh
}

// expired reports whether an entry with the given expiry is expired at now. A
// zero expiry never expires.
func expired(expiresAt, now time.Time) bool {
	return !expiresAt.IsZero() && now.After(expiresAt)
}
