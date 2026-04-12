# lifecycle

Application lifecycle manager for Go. Coordinates goroutine startup, HTTP server attachment, graceful teardown, and OS signal handling.

## Install

```bash
go get github.com/remnestal/albstractions/lifecycle@latest
```

## Usage

```go
g := lifecycle.New(5*time.Second,
    lifecycle.WithSignals(syscall.SIGTERM, syscall.SIGINT),
)

// Register a long-running goroutine.
g.Go(func(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        case job := <-queue:
            process(job)
        }
    }
})

// Attach an HTTP server — started as a goroutine, stopped during teardown.
g.Serve(&http.Server{Addr: ":8080", Handler: mux})

// Register teardown callbacks in the order they should run.
// Callbacks registered after Serve run after the server is fully drained.
g.Defer(func(ctx context.Context) {
    db.Close()
})

// Start everything and block until shutdown completes.
if err := g.Run(); err != nil {
    log.Fatal(err)
}
```

Shutdown is triggered by a configured OS signal, a call to `g.Shutdown()`, or a goroutine exiting unexpectedly. Teardown callbacks run sequentially in registration order within the graceful deadline; `Run` returns once all goroutines have exited or the deadline expires.
