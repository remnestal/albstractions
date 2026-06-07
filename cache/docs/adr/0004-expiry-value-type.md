# Expiry value type for Set; Set returns the resolved expiry

`Set` and `SetContext` take an `Expiry` value rather than a `time.Duration`. An
`Expiry` is built with `After(d)` (relative) or `At(t)` (absolute), or is one of
the package values `Never`, `Default`, and `Keep` (the zero `Expiry` is
`Default`). `Set` returns the resolved absolute expiry (`time.Time`, zero means
never).

A requested lifetime is clamped to the configured `[min, max]` bounds. The
maximum is a hard ceiling applied to every expiry except `Keep`, including
`Never`: with a maximum set, no entry can be permanent. `Keep` returns the
existing entry's expiry unchanged (already bounded when it was set), or falls
back to `Default` when there is nothing to preserve. The resolution chain is
`Keep -> Default -> Never/After/At`, then clamp.

## Why a value type

An earlier design passed a `time.Duration` and overloaded three sentinel values
(`0`, `-1`, `-2`) for default / never / keep. That made computed durations a
footgun: `Set(k, v, time.Until(deadline))` silently misbehaves once the deadline
is reached — `0` collides with the default sentinel, and a negative duration was
treated as "never" (and could even collide exactly with the never/keep
sentinels). The `Expiry` type removes the overloading by construction: every case
is a distinct, typed value, and `At` makes "expire at this instant" first-class.

## Considered options

- **`time.Duration` + sentinels** (the original): ergonomic and Redis/memcached
  -like, but the sentinel overloading is a latent footgun for any computed
  duration near zero. Rejected for that reason.
- **A bare `time.Time` argument**: removes the footgun and makes `Never` the zero
  time, but `Default` and `Keep` have no `time.Time` encoding, so they would need
  separate methods (method sprawl) or a re-introduced sentinel time. Rejected.
- **A sealed `Lifetime`/`Expiry` interface split** so `WithDefaultTTL` could
  reject `Default`/`Keep` at compile time: more type-safe but heavier (two
  interfaces plus sealed concrete types). Rejected in favour of a duration-typed
  `WithDefaultTTL(d time.Duration)`, which makes the meaningless `Default`/`Keep`
  defaults unrepresentable with no extra surface.
- **A `Peek` accessor (or widening `Get`) to read an entry's expiry**: dropped
  because `Keep` already covers the motivating "update without resetting the
  expiry" use case, keeping `Get` lean and off the hot path.
