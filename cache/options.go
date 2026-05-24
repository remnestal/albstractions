package cache

import "time"

// MemoryOption configures a [Memory].
type MemoryOption func(*memoryConfig)

// memoryConfig holds the resolved settings for a [Memory]. Its zero value is a
// cache with no TTL bounds, no default expiry, and no background maintenance;
// [NewMemory] seeds defaultTTL with [NoExpiration] before applying options.
type memoryConfig struct {
	defaultTTL      time.Duration
	minTTL          time.Duration
	maxTTL          time.Duration
	cleanupInterval time.Duration
	rebuildInterval time.Duration
}

// WithCleanupInterval runs a background janitor that evicts expired entries on
// the given interval.
//
// A zero interval (the default) disables the janitor, leaving expiry to lazy
// eviction on read.
//
// Panics if d is negative.
func WithCleanupInterval(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithCleanupInterval: interval must not be negative")
	}
	return func(c *memoryConfig) { c.cleanupInterval = d }
}

// WithRebuildInterval periodically rebuilds the underlying map on the given
// interval, reclaiming the bucket memory Go does not release after deletions.
//
// A zero interval (the default) disables rebuilding. Rebuilding is more
// expensive than cleanup but also reclaims memory; the two are independent.
//
// Panics if d is negative.
func WithRebuildInterval(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithRebuildInterval: interval must not be negative")
	}
	return func(c *memoryConfig) { c.rebuildInterval = d }
}
