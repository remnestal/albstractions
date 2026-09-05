# retry

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/retry.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/retry)

Re-invokes a fallible function until it succeeds, with between-attempt spacing supplied by a pluggable schedule and an explicit stop condition.

```bash
go get github.com/remnestal/albstractions/retry
```

## Overview

A `Retry` is constructed from a [`Schedule`](https://pkg.go.dev/github.com/remnestal/albstractions/schedule), which supplies the between-attempt delay, and a `StopFunc`, which decides when to give up. The first attempt always runs; the `StopFunc` is consulted only after a failed attempt.

Stop conditions are composable. Assemble a budget with `Any` (whichever fires first) or `All` (only when all fire) from the building blocks `MaxAttempts`, `MaxElapsed`, and `OnError`.

Both dependencies are declared as local interfaces, so `retry` imports nothing. Any type with a `Next() time.Duration` method fits, including every implementation in the `schedule` module.

## Usage

```go
import (
    "context"
    "errors"
    "log"
    "os"
    "time"

    "github.com/remnestal/albstractions/retry"
    "github.com/remnestal/albstractions/schedule"
)

r := retry.New(
    schedule.NewExponential(100*time.Millisecond, 5*time.Second),
    retry.Any(
        retry.MaxAttempts(5),
        retry.MaxElapsed(30*time.Second),
        retry.OnError(func(err error) bool { return errors.Is(err, os.ErrPermission) }),
    ),
    retry.WithHook(func(a retry.AttemptInfo) {
        if a.Err != nil {
            log.Printf("attempt %d failed: %v (next in %s)", a.Attempt, a.Err, a.Next)
        } else {
            log.Printf("succeeded on attempt %d after %s", a.Attempt, a.Elapsed)
        }
    }),
)

err := r.Do(ctx, callFlakyAPI)
```

The same `*Retry` can be reused across independent calls. If the supplied schedule implements `Resetter`:

```go
type Resetter interface {
    Reset()
}
```

then `Do` resets it at the start of every invocation, so backoff never carries over from an earlier call. `schedule.Exponential` implements it. Concurrent calls to `Do` share the schedule's state, so a stateful schedule is best reused sequentially.

## Stop conditions

| Builder | Halts when |
|---------|------------|
| `MaxAttempts(n)` | the n-th failed attempt completes |
| `MaxElapsed(d)` | cumulative time since `Do` started reaches `d` |
| `OnError(pred)` | `pred(err)` returns `true` for the latest error |
| `Any(stops...)` | any of `stops` halts (logical OR) |
| `All(stops...)` | all of `stops` halt (logical AND) |

## Observation hook

`WithHook` installs a callback fired exactly once per attempt, including the successful one, where `Err` is nil. The hook is purely observational; it cannot influence retry behaviour and must not block.

The `AttemptInfo` it receives carries:

| Field | Meaning |
|-------|---------|
| `Attempt` | 1-based attempt number |
| `Elapsed` | Time since `Do` was called |
| `Err` | The attempt's error, or nil on success |
| `Next` | Delay before the next attempt, or 0 when no retry follows |

## Cancellation

`ctx.Done()` interrupts the inter-attempt sleep, in which case `Do` returns `ctx.Err()`. Cancellation during `fn` itself surfaces as whatever `fn` returns.
