# schedule

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/schedule.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/schedule)

Configurable delay strategies for throttling, rate-limiting, and backoff.

```bash
go get github.com/remnestal/albstractions/schedule
```

## Overview

All implementations satisfy the `Schedule` interface:

```go
type Schedule interface {
    Next() time.Duration
}
```

All implementations are safe for concurrent use.

## Implementations

### Constant

Always returns the same delay.

```go
s := schedule.NewConstant(500 * time.Millisecond)
time.Sleep(s.Next()) // always 500ms
```

### Exponential

Grows from a base delay up to a cap, multiplying by a configurable factor (default 2.0) on each call. Call `Reset` to start a new sequence.

```go
s := schedule.NewExponential(100*time.Millisecond, 30*time.Second)

// With a custom factor:
s := schedule.NewExponential(100*time.Millisecond, 30*time.Second,
    schedule.WithFactor(1.5),
)
```

### Uniform

Picks a uniformly distributed random delay in [lo, hi] on each call.

```go
s := schedule.NewUniform(100*time.Millisecond, 500*time.Millisecond)

// With a fixed random source for deterministic behaviour:
s := schedule.NewUniform(100*time.Millisecond, 500*time.Millisecond,
    schedule.WithSource(rand.NewPCG(42, 0)),
)
```

### Sine

Oscillates between lo and hi following a raised cosine curve over the given period. Starts at lo, peaks at hi at the midpoint, and returns to lo at the end of each period. Phase is derived from the wall clock rather than the call count, so two calls at the same instant return the same delay and the cadence is independent of how often `Next` is called.

```go
s := schedule.NewSine(100*time.Millisecond, 1*time.Second, time.Minute)

// With an injected clock, for deterministic tests:
s := schedule.NewSine(100*time.Millisecond, 1*time.Second, time.Minute,
    schedule.WithClock(func() time.Time { return fixed }),
)
```

### TokenBucket

Adapts any rate limiter that hands out reservations to the `Schedule` interface. Each call reserves capacity and returns the wait until it is available. Note that capacity is consumed on every call regardless of whether the caller actually waits.

The limiter and its reservation are declared as interfaces, so the module carries no dependency on `golang.org/x/time`. A `*rate.Limiter` satisfies them structurally, so one can be passed directly:

```go
type Reservation interface {
    OK() bool
    Delay() time.Duration
}

type Limiter[R Reservation] interface {
    Reserve() R
}
```

```go
s := schedule.NewTokenBucket(rate.NewLimiter(rate.Every(time.Second), 10))
```

## Options

| Option | Applies to | Effect |
|--------|-----------|--------|
| `WithFactor(f)` | `Exponential` | Growth multiplier per call. Default 2.0 |
| `WithSource(src)` | `Uniform` | Random source, for deterministic tests. Default a seeded PCG |
| `WithClock(fn)` | `Sine` | Clock used to derive phase. Default `time.Now` |

Every constructor panics on an invalid range, such as a negative delay or a `hi` below `lo`, so misconfiguration surfaces at startup rather than as a silently wrong cadence.
