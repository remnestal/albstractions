package cache

import "time"

// Expiry specifies when an entry passed to [Cache.Set] or [Cache.SetContext]
// expires.
//
// Construct one with [After] (a relative duration) or [At] (an absolute time),
// or use the package values [Never], [Default], and [Keep]. The zero value is
// [Default].
type Expiry struct {
	kind expiryKind
	dur  time.Duration
	at   time.Time
}

// expiryKind enumerates the variants of [Expiry]. Its zero value is the
// [Default] variant, so the zero [Expiry] is [Default].
type expiryKind uint8

const (
	expiryDefault expiryKind = iota
	expiryNever
	expiryKeep
	expiryAfter
	expiryAt
)

// After returns an [Expiry] that expires the entry the given duration after it
// is set.
//
// The duration is clamped to the bounds set by [WithMinTTL] and [WithMaxTTL]. A
// duration that is zero or has already elapsed expires the entry immediately,
// subject to [WithMinTTL].
func After(d time.Duration) Expiry {
	return Expiry{kind: expiryAfter, dur: d}
}

// At returns an [Expiry] that expires the entry at the given absolute time.
//
// The implied lifetime (t minus the moment of the set) is clamped to the bounds
// set by [WithMinTTL] and [WithMaxTTL]. A t at or before the set expires the
// entry immediately, subject to [WithMinTTL].
func At(t time.Time) Expiry {
	return Expiry{kind: expiryAt, at: t}
}

// Never is the [Expiry] for an entry that does not expire.
//
// With [WithMaxTTL] configured no entry can be permanent: Never resolves to the
// moment of the set plus the maximum.
var Never = Expiry{kind: expiryNever}

// Default is the [Expiry] that uses the duration configured with
// [WithDefaultTTL], or [Never] if none was configured.
//
// Default is the zero value of [Expiry].
var Default = Expiry{kind: expiryDefault}

// Keep is the [Expiry] that replaces an entry's value while preserving its
// current expiry.
//
// If the key is absent or already expired there is nothing to preserve, so Keep
// falls back to [Default].
var Keep = Expiry{kind: expiryKeep}

// WithDefaultTTL sets the duration applied when an entry is set with [Default].
//
// Without it, [Default] resolves to [Never].
//
// Panics if d is not positive.
func WithDefaultTTL(d time.Duration) MemoryOption {
	if d <= 0 {
		panic("cache.WithDefaultTTL: ttl must be positive")
	}
	return func(c *memoryConfig) { c.defaultTTL = d }
}

// WithMinTTL sets a lower bound on an entry's lifetime; a shorter lifetime is
// clamped up to it.
//
// A zero bound (the default) imposes no floor. It applies to [After], [At], and
// [Default]; [Keep] preserves the existing expiry and is never re-clamped.
//
// Panics if d is negative.
func WithMinTTL(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithMinTTL: ttl must not be negative")
	}
	return func(c *memoryConfig) { c.minTTL = d }
}

// WithMaxTTL sets an upper bound on an entry's lifetime; a longer lifetime is
// clamped down to it.
//
// A zero bound (the default) imposes no ceiling. It applies to every expiry
// except [Keep], including [Never]: with a maximum set, no entry can be
// permanent. [Keep] preserves the existing expiry and is never re-clamped.
//
// Panics if d is negative.
func WithMaxTTL(d time.Duration) MemoryOption {
	if d < 0 {
		panic("cache.WithMaxTTL: ttl must not be negative")
	}
	return func(c *memoryConfig) { c.maxTTL = d }
}

// resolveExpiry converts exp into an absolute expiry, returning the zero time
// for an entry that never expires.
//
// found and existing describe the entry currently stored under the key and are
// consulted only to honour [Keep]. The resolution chain is
// Keep -> Default -> Never/After/At, then the requested lifetime is clamped.
func (c *memoryConfig) resolveExpiry(exp Expiry, now, existing time.Time, found bool) time.Time {
	switch exp.kind {
	case expiryKeep:
		// Preserve the existing expiry as-is (already bounded when it was set),
		// or fall back to Default when there is nothing to preserve.
		if found {
			return existing
		}
		return c.resolveDefault(now)
	case expiryDefault:
		return c.resolveDefault(now)
	case expiryNever:
		return c.clampNever(now)
	case expiryAt:
		return c.clampLifetime(now, exp.at.Sub(now))
	default: // expiryAfter
		return c.clampLifetime(now, exp.dur)
	}
}

// resolveDefault resolves [Default]: the configured default duration, or [Never]
// when none is configured.
func (c *memoryConfig) resolveDefault(now time.Time) time.Time {
	if c.defaultTTL > 0 {
		return c.clampLifetime(now, c.defaultTTL)
	}
	return c.clampNever(now)
}

// clampNever resolves [Never], honouring an upper bound: with a maximum set, no
// entry can be permanent.
func (c *memoryConfig) clampNever(now time.Time) time.Time {
	if c.maxTTL > 0 {
		return now.Add(c.maxTTL)
	}
	return time.Time{}
}

// clampLifetime clamps a requested lifetime to the configured [min, max] and
// returns the resulting absolute expiry. A non-positive result (an elapsed or
// floored-away lifetime) expires the entry immediately.
func (c *memoryConfig) clampLifetime(now time.Time, life time.Duration) time.Time {
	if c.minTTL > 0 && life < c.minTTL {
		life = c.minTTL
	}
	if c.maxTTL > 0 && life > c.maxTTL {
		life = c.maxTTL
	}
	if life <= 0 {
		return now
	}
	return now.Add(life)
}
