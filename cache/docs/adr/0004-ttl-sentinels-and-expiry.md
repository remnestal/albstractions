# Relative-duration TTL with sentinels; Set returns the resolved expiry

`Set` takes a relative `time.Duration` plus the sentinels `DefaultTTL`,
`NoExpiration`, and `KeepTTL`, and returns the resolved absolute expiry
(`time.Time`, zero means never); positive durations are clamped to the
configured `[min, max]` bounds. The argument stays a `Duration` rather than an
absolute `time.Time` because the sentinels cannot be encoded as a `time.Time`
and relative expiry matches Redis/memcached ergonomics — the accuracy an
absolute argument would give is delivered instead through the *returned* expiry.

## Considered options

- **A `time.Time` argument**: rejected — there is no room for `DefaultTTL` /
  `NoExpiration` / `KeepTTL`, and it is clumsier for the common "expire in 5m"
  case.
- **A `Peek` accessor (or widening `Get`) to read an entry's expiry**: dropped
  because `KeepTTL` (replace the value, preserve the existing expiry) already
  covers the motivating "update without resetting the TTL" use case, keeping
  `Get` lean and off the hot path.
