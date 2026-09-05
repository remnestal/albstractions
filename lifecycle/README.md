# lifecycle

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/lifecycle.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/lifecycle)

Application lifecycle manager for Go. Coordinates goroutine startup, HTTP server attachment, readiness signalling, graceful teardown, and OS signal handling.

```bash
go get github.com/remnestal/albstractions/lifecycle
```

## Overview

A `Group` supervises a set of long-running goroutines. Register background work with `Require`, HTTP servers with `Serve`, which wraps the server's own serve-and-drain cycle for you, and teardown callbacks with `Defer`, then call `Run` to start everything and block until shutdown completes.

Shutdown is triggered by a configured OS signal, an explicit `Shutdown()` call, cancellation of a parent context, or a required goroutine failing or returning early. `ShutdownCause` reports which of these it was.

`Require`, `Serve`, and `Defer` may be called before or after `Run`. A late registration starts immediately, and one made during shutdown is a no-op.

## Usage

### Graceful shutdown

```go
g := lifecycle.New(
    lifecycle.WithSignals(syscall.SIGTERM, syscall.SIGINT),
    lifecycle.WithGracefulTimeout(20*time.Second),
)

// A required goroutine. Returning an error, or returning nil before the
// group's context is cancelled, triggers shutdown of the whole group.
g.Require("worker", func(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        case job := <-queue:
            process(job)
        }
    }
})

// Attach an HTTP server: started as a goroutine, drained during teardown.
g.Serve("api", &http.Server{Addr: ":8080", Handler: mux})

// Teardown callbacks run in registration order, each with its own deadline.
g.Defer(5*time.Second, func(ctx context.Context) {
    db.Close()
})

if err := g.Run(); err != nil {
    log.Fatalf("shutdown with errors: %v", err)
}
```

### Readiness and shutdown cause

`Ready` closes once every goroutine registered before `Run` has been spawned, which gives health checks and dependent services a synchronisation point. `Done` closes once `Run` has fully returned, so shutdown can be observed without blocking on `Run` itself.

```go
go func() {
    <-g.Ready()
    readiness.Store(true) // /readyz starts reporting healthy
}()

go func() {
    <-g.Done()
    switch cause := g.ShutdownCause(); {
    case cause == nil:
        log.Print("shut down on request") // g.Shutdown() was called
    case errors.Is(cause, lifecycle.ErrSignal):
        log.Print("shut down on signal")
    default:
        log.Printf("shut down after failure: %v", cause)
    }
}()

if err := g.Run(); err != nil {
    log.Fatal(err)
}
```

To fold the group into an existing context tree, pass `WithContext`. Cancelling the parent triggers shutdown and its error becomes the cause. `g.Context()` is the group's own context, valid before `Run` and cancelled when shutdown begins.

## Options

| Option | Effect |
|--------|--------|
| `WithGracefulTimeout(d)` | Total budget for the whole teardown: server drain, callbacks, and goroutine exit combined. Default 30s |
| `WithSignals(sigs...)` | OS signals that trigger shutdown. None by default |
| `WithContext(ctx)` | Parent context whose cancellation triggers shutdown and becomes the cause |
| `WithErrorHandler(fn)` | Called for each goroutine error as it is collected. May be called concurrently |

## Group API

| Member | Purpose |
|--------|---------|
| `Require(name, fn)` | Supervised goroutine. The name identifies it in `ShutdownCause` |
| `Serve(name, server)` | Runs `ListenAndServe` and registers the server's drain |
| `Defer(timeout, fn)` | Teardown callback with its own deadline |
| `Run() error` | Starts everything, blocks until shutdown completes. Idempotent |
| `Shutdown()` | Triggers shutdown. Idempotent, and leaves `ShutdownCause` nil |
| `Ready() <-chan struct{}` | Closed once pre-`Run` goroutines are spawned |
| `Done() <-chan struct{}` | Closed once `Run` has returned |
| `Context() context.Context` | The group's context, cancelled when shutdown begins |
| `ShutdownCause() error` | Why shutdown happened, or nil if it was requested |

## Shutdown semantics

Teardown runs in a fixed order. Every HTTP server registered with `Serve` is drained first, then the `Defer` callbacks run sequentially in registration order. Registering a callback after `Serve` does not change this; servers always drain first.

Each `Defer` callback gets its own deadline, but all of teardown shares the single graceful budget. Once that budget expires the remaining callbacks are skipped and `Run` returns without waiting for stragglers.

`Run` returns the goroutine errors joined with `errors.Join`. Goroutines that return `context.Canceled` or `context.DeadlineExceeded` do not contribute, since a clean stop is not a failure.
