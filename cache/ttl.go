package cache

import "time"

// Sentinel TTL values accepted by [Cache.Set] and [Cache.SetContext] in place
// of a positive duration.
const (
	// DefaultTTL selects the TTL configured with [WithDefaultTTL], or no expiry
	// if none was configured.
	DefaultTTL time.Duration = 0

	// NoExpiration stores an entry that never expires.
	NoExpiration time.Duration = -1

	// KeepTTL replaces an entry's value while preserving its current expiry.
	//
	// If the key is absent the new entry never expires, matching Redis KEEPTTL.
	KeepTTL time.Duration = -2
)

// WithDefaultTTL sets the TTL applied when [Cache.Set] is called with
// [DefaultTTL].
//
// Without it, [DefaultTTL] means no expiry.
//
// Panics unless d is a positive duration or [NoExpiration].
func WithDefaultTTL(d time.Duration) MemoryOption {
	if d == DefaultTTL || d < NoExpiration {
		panic("cache.WithDefaultTTL: ttl must be positive or NoExpiration")
	}
	return func(c *memoryConfig) { c.defaultTTL = d }
}

// WithMinTTL sets a lower bound to which positive TTLs are clamped.
//
// A zero bound (the default) imposes no floor. Sentinel TTLs are never clamped.
//
// Panics if d is negative.
func WithMinTTL(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithMinTTL: ttl must not be negative")
	}
	return func(c *memoryConfig) { c.minTTL = d }
}

// WithMaxTTL sets an upper bound to which positive TTLs are clamped.
//
// A zero bound (the default) imposes no ceiling. Sentinel TTLs are never
// clamped.
//
// Panics if d is negative.
func WithMaxTTL(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithMaxTTL: ttl must not be negative")
	}
	return func(c *memoryConfig) { c.maxTTL = d }
}

// resolveExpiry converts a requested ttl into an absolute expiry, returning the
// zero time for an entry that never expires.
//
// found and existing describe the entry currently stored under the key and are
// consulted only to honour [KeepTTL].
func (c *memoryConfig) resolveExpiry(ttl time.Duration, now, existing time.Time, found bool) time.Time {
	switch ttl {
	case KeepTTL:
		if found {
			return existing
		}
		return time.Time{}
	case DefaultTTL:
		ttl = c.defaultTTL
	}
	if ttl <= 0 {
		return time.Time{}
	}
	if c.minTTL > 0 && ttl < c.minTTL {
		ttl = c.minTTL
	}
	if c.maxTTL > 0 && ttl > c.maxTTL {
		ttl = c.maxTTL
	}
	return now.Add(ttl)
}
