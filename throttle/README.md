# throttle

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/throttle.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/throttle)

Paces function and HTTP call cadence using a pluggable schedule.

```bash
go get github.com/remnestal/albstractions/throttle
```

## Overview

A `Throttle` wraps a [`Schedule`](https://pkg.go.dev/github.com/remnestal/albstractions/schedule) and ensures calls are spaced by at least the duration it returns. The first call is never delayed, unless the schedule decides per caller (see below). By default invocations run serially, one at a time; use `WithMaxInflight` to allow more concurrency.

Pacing and concurrency are separate knobs. The schedule decides how often a call may start, irrespective of whether earlier calls have completed; `WithMaxInflight` decides how many may be in flight at once, counting callers still waiting for their dispatch time.

## Usage

### Throttled function calls

```go
import (
    "github.com/remnestal/albstractions/schedule"
    "github.com/remnestal/albstractions/throttle"
)

s := schedule.NewConstant(200 * time.Millisecond)
t := throttle.New(s, throttle.WithMaxInflight(5))

err := t.Do(ctx, func(ctx context.Context) error {
    // started at most once every 200ms, with at most 5 running concurrently
    return doWork(ctx)
})
```

Pass `throttle.Unbounded` to remove the concurrency cap entirely and pace call starts only:

```go
t := throttle.New(s, throttle.WithMaxInflight(throttle.Unbounded))
```

### Rate-limited HTTP client

`NewRoundTripper` applies the same pacing to outbound requests, so an entire `http.Client` respects an API's rate limit without touching call sites. It takes the same options as `New`.

```go
rt := throttle.NewRoundTripper(nil, s, throttle.WithMaxInflight(4)) // nil uses http.DefaultTransport
client := &http.Client{Transport: rt}

resp, err := client.Get("https://example.com/api")
```

Pairing it with a token-bucket schedule tracks a published rate limit directly. `schedule.TokenBucket` implements `Waiter`, so the throttle defers to the limiter for each caller instead of spacing calls itself, and a limiter that can never grant capacity fails the call instead of letting it through. This needs a `schedule` release that includes `TokenBucket.Wait`:

```go
s := schedule.NewTokenBucket(rate.NewLimiter(rate.Every(time.Second), 10))
client := &http.Client{Transport: throttle.NewRoundTripper(nil, s)}
```

## Options

| Option | Effect |
|--------|--------|
| `WithMaxInflight(n)` | Maximum concurrent invocations. Default 1, which runs calls serially. `Unbounded` removes the cap |

## Cancellation and cadence

`Do` returns `ctx.Err()` if the context is cancelled while waiting for its slot, either for the paced delay or for an in-flight slot. `RoundTrip` uses `req.Context()`, so a cancelled request stops waiting too.

Callers first queue for an in-flight slot and take their turn in the cadence once they hold one. A caller cancelled while queued leaves the cadence untouched. A caller cancelled during the paced delay has already taken its turn, and it stays taken, so a run of cancelled callers cannot hand the next caller a free burst. An idle throttle does not accumulate credit either, so a long pause is not followed by a rush of immediate calls.

If banking idle time is what you want, pace the throttle with `schedule.TokenBucket` instead. A token bucket refills while idle up to its burst size, so a quiet period earns an allowance that the next callers can spend at once. A caller cancelled while waiting on the bucket hands its token back to callers that reserve after it; callers already waiting keep their times.
