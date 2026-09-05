# cache

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/cache.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/cache)

A generic in-memory cache behind a small interface, with composable sharding, read-through, and write-through layers.

```bash
go get github.com/remnestal/albstractions/cache
```

## Overview

The in-memory `Memory`, every wrapper, and any backend you write all satisfy one generic interface:

```go
type Cache[K comparable, V any] interface {
    Get(key K) (V, bool)
    Set(key K, val V, exp Expiry) time.Time
    Delete(key K)
    Items() iter.Seq2[K, V]

    GetContext(ctx context.Context, key K) (V, bool, error)
    SetContext(ctx context.Context, key K, val V, exp Expiry) (time.Time, error)
    DeleteContext(ctx context.Context, key K) error
    ItemsContext(ctx context.Context) (iter.Seq2[K, V], func() error)
}
```

Each operation has an ergonomic infallible form and a context form for backends that perform I/O, such as Redis or a database. The context methods are authoritative; the infallible ones call them with a background context and treat an error as a miss on reads or ignore it on writes. Implement the interface to plug in a backend, and wrap any `Cache` to build multi-tier stacks, since every layer is itself a `Cache`.

## Usage

```go
import (
    "time"

    "github.com/remnestal/albstractions/cache"
)

c := cache.NewMemory[string, []byte](
    cache.WithDefaultTTL(5*time.Minute),
    cache.WithCleanupInterval(time.Minute),
)
defer c.Close()

c.Set("token", payload, cache.Default)          // expires in 5m
c.Set("session", blob, cache.After(time.Hour))  // this one entry lives longer
c.Set("config", data, cache.Never)              // never expires
if v, ok := c.Get("token"); ok {
    use(v)
}
```

## Expiry

`Set` takes an `Expiry` and returns the resulting absolute expiry, where a zero `time.Time` means "never":

| Expiry | Meaning |
|--------|---------|
| `After(d)` | expire `d` after the set, clamped to the configured bounds |
| `At(t)` | expire at the absolute time `t`; a past `t` expires immediately |
| `Never` | never expire |
| `Default` | use the duration from `WithDefaultTTL`, or `Never` if none is set |
| `Keep` | replace the value, keep the existing expiry (or `Default` if the key is absent) |

`WithMinTTL` and `WithMaxTTL` bound an entry's lifetime. The maximum is a hard ceiling: with it set, even `Never` resolves to `now + max`, so no entry can be permanent. `Keep` preserves an existing expiry and is never re-clamped.

## Memory options

| Option | Effect |
|--------|--------|
| `WithDefaultTTL(d)` | Lifetime applied by the `Default` expiry. Unset means `Never` |
| `WithMinTTL(d)` / `WithMaxTTL(d)` | Clamp every resolved lifetime into `[min, max]` |
| `WithCleanupInterval(d)` | Background goroutine that evicts expired entries. Without it, expiry is lazy and applied on read |
| `WithRebuildInterval(d)` | Background goroutine that rebuilds the backing map. Go never shrinks a map's bucket array, so a cache that has churned through many keys keeps that memory until the map is replaced |

Both intervals start goroutines, so a cache configured with either must be closed. Tying `Close` to the lifetime of whatever scope owns the cache is the safe habit regardless, since it also cascades through a wrapper stack.

## Sharding

`Sharded` partitions keys across independent backends to reduce lock contention, and is itself a `Cache`:

```go
s := cache.NewSharded[string, int](
    16,
    cache.HashShard[string](),
    func() cache.Cache[string, int] { return cache.NewMemory[string, int]() },
)
defer s.Close() // closes every backend
```

Use `HashShard[K]()` for any comparable key, `ModuloShard[K]()` for integer keys, or supply your own `func(K) uint64`.

## Read-through and write-through

Both wrappers are `Cache` values, so they nest. Read-through loads misses from a source and populates the front:

```go
rt := cache.NewReadThrough[string, User](
    cache.NewMemory[string, User](),
    func(ctx context.Context, id string) (User, error) {
        u, err := db.LookupUser(ctx, id)
        if errors.Is(err, sql.ErrNoRows) {
            return User{}, cache.ErrCacheMiss
        }
        return u, err
    },
    cache.WithSingleFlight(), // collapse concurrent loads of the same key
)
```

A loader error recognised as a miss (`ErrCacheMiss`, or any error registered with `WithMissError`) surfaces as a plain miss, so the caller can create the resource while keeping the cache layer.

Write-through persists to a backing store before updating the front, keeping them consistent. The store is always written first, so the front is never ahead of the store when a write fails. Deletes are opt-in: without `WithDeleter` a delete evicts from the front only and leaves the store untouched.

```go
wt := cache.NewWriteThrough[string, User](
    cache.NewMemory[string, User](),
    func(ctx context.Context, id string, u User) error { return db.SaveUser(ctx, id, u) },
    cache.WithDeleter[string, User](func(ctx context.Context, id string) error { return db.DeleteUser(ctx, id) }),
)
```

Stack them into tiers. `LoaderFromCache` turns a lower cache into a read-through source:

```go
l2 := cache.NewReadThrough[string, User](cache.NewMemory[string, User](), loadFromDB)
l1 := cache.NewReadThrough[string, User](cache.NewMemory[string, User](), cache.LoaderFromCache[string, User](l2))
```

## Iteration

`Items` returns an `iter.Seq2[K, V]` over the live entries:

```go
for k, v := range c.Items() {
    // ...
}
```

For an I/O backend that can fail or be cancelled mid-scan, `ItemsContext` returns the iterator plus a terminal-error accessor. Check it after the loop, like `bufio.Scanner.Err`:

```go
seq, errf := c.ItemsContext(ctx)
for k, v := range seq {
    // ...
}
if err := errf(); err != nil {
    // the scan failed or ctx was cancelled
}
```

`Items` is the infallible form of `ItemsContext`, using a background context and discarding the error. Iteration is a point-in-time snapshot taken under a read lock, then yielded with no lock held, so the loop body may safely read or write the cache. `Sharded` visits one backend at a time, and the wrappers iterate their front only, so an origin store cannot be enumerated through the cache.

## Lifecycle and concurrency

All caches are safe for concurrent use. A `Memory` with `WithCleanupInterval` or `WithRebuildInterval` runs background goroutines; call `Close` to stop them. Closing a wrapper or a `Sharded` cascades to the caches beneath it, so closing the outermost layer of a stack is enough.

Values are stored by assignment. If `V` is a reference type such as a slice, map, or pointer, the cache and its callers share the underlying data, so do not mutate a retrieved value without synchronisation, or store a copy.

## Testing

`cache/mock` provides a `Backend` that satisfies `cache.Cache` and records every call, intended for import by other projects' tests. Its zero value behaves as an empty cache, and each operation can be overridden to return fixtures or errors:

```go
b := &mock.Backend[string, User]{
    GetFunc: func(ctx context.Context, key string) (User, bool, error) {
        return User{ID: key}, true, nil
    },
}

rt := cache.NewReadThrough[string, User](b, loadFromDB)
rt.Get("alice")

for _, call := range b.Calls() {
    // assert on call.Op, call.Key, call.Val, call.Exp
}
```
