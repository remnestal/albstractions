# Sharding is a composable wrapper, not a Memory option

`Sharded[K,V]` is a `Cache` that fans out across N backend caches built by a
`constructor` factory and routed by an `algorithm`
(`NewSharded(size, algorithm, constructor)`), rather than `Memory` carrying a
`WithShards` option. Because a shard backend is any `Cache`, sharding composes
with the read-through and write-through layers, and `Memory` stays a single map
with no sharding concern of its own.

## Consequences

- `Items` is part of the `Cache` interface and `Close` is recovered via
  `io.Closer`, so both pass through a generic `Sharded` for free. This is the
  reason iteration and lifecycle had to be expressible through the interface
  (and an optional `io.Closer`) rather than as `Memory`-only methods.
- Each shard is an independent backend with its own maintenance goroutines, so a
  sharded `Memory` runs up to `2 × size` background goroutines — callers choose
  `size` with that in mind.
