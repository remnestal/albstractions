# retry

Re-invokes a fallible function until it succeeds, with between-attempt spacing supplied by a pluggable schedule and an explicit stop condition.

```bash
go get github.com/remnestal/albstractions/retry
```

## Overview

A `Retry` is constructed from a [`Schedule`](https://pkg.go.dev/github.com/remnestal/albstractions/schedule) (between-attempt delay) and a `StopFunc` (when to give up). The first attempt always runs; the `StopFunc` is consulted only after a failed attempt. If the supplied schedule implements `Reset()` (e.g. `schedule.Exponential`), `Do` resets it at the start of each invocation so a single `*Retry` is safe to reuse.

Stop conditions are composable: assemble budgets with `Any` (whichever fires first) or `All` (only when all fire) from the building blocks `MaxAttempts`, `MaxElapsed`, and `OnError`.

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

## Stop conditions

| Builder | Halts when |
|---------|------------|
| `MaxAttempts(n)` | the n-th failed attempt completes |
| `MaxElapsed(d)` | cumulative time since `Do` started reaches `d` |
| `OnError(pred)` | `pred(err)` returns `true` for the latest error |
| `Any(stops...)` | any of `stops` halts (logical OR) |
| `All(stops...)` | all of `stops` halt (logical AND) |

## Observation hook

`WithHook` installs a callback fired exactly once per attempt — including the successful one (with `Err == nil`). The hook is purely observational; it cannot influence retry behaviour and must not block.

## Cancellation

`ctx.Done()` interrupts the inter-attempt sleep, in which case `Do` returns `ctx.Err()`. Cancellation during `fn` itself surfaces as whatever `fn` returns.
