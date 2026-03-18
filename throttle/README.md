# throttle

Paces function and HTTP call cadence using a pluggable schedule.

```bash
go get github.com/remnestal/albstractions/throttle
```

## Overview

A `Throttle` wraps a [`Schedule`](https://pkg.go.dev/github.com/remnestal/albstractions/schedule) and ensures calls are spaced by at least the duration it returns. The first call is never delayed. By default invocations run serially (one at a time); use `WithMaxInflight` to allow more concurrency.

## Usage

### Throttled function calls

```go
import (
    "github.com/remnestal/albstractions/schedule"
    "github.com/remnestal/albstractions/throttle"
)

s := schedule.NewConstant(200 * time.Millisecond)
t := throttle.New(s)

err := t.Do(ctx, func(ctx context.Context) error {
    // called at most once every 200ms
    return doWork(ctx)
})
```

Allow up to five concurrent in-flight calls:

```go
t := throttle.New(s, throttle.WithMaxInflight(5))
```

Remove the concurrency cap entirely:

```go
t := throttle.New(s, throttle.WithMaxInflight(throttle.Unbounded))
```

### Rate-limited HTTP client

```go
rt := throttle.NewRoundTripper(nil, s) // nil uses http.DefaultTransport
client := &http.Client{Transport: rt}

resp, err := client.Get("https://example.com/api")
```
