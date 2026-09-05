# albstractions

Small, focused Go modules for recurring service plumbing like retries, throttling, lifecycle, certificates, and caching. Each one is versioned and importable on its own, dependency-free, and perfectly balanced, as all things should be.

## Usage

Tags are scoped by module path (`certkit/v0.2.0`, `schedule/v0.1.0`). Import only the ones you need:

```bash
go get github.com/remnestal/albstractions/certkit
go get github.com/remnestal/albstractions/schedule
```

## Modules

| Module | Description |
|--------|-------------|
| [`schedule`](./schedule) | Delay strategies for throttling, rate-limiting, and backoff |
| [`retry`](./retry) | Re-invokes a fallible function with a pluggable schedule and composable stop conditions |
| [`throttle`](./throttle) | Paces function and HTTP call cadence using a pluggable schedule, with an optional concurrency cap |
| [`certkit`](./certkit) | Certificate and key management: key loading with secure zeroing, self-signed PKI, and mutual TLS helpers |
| [`lifecycle`](./lifecycle) | Application lifecycle manager: supervised goroutines, HTTP servers, readiness signalling, graceful shutdown with cause attribution |
| [`cache`](./cache) | Generic in-memory cache with sharding, read-through, and write-through layers behind one interface |

## Design

- Mandatory arguments in function signatures, optional config via the `(...Option)` variadic pattern
- Dependencies are passed in, never constructed internally and never read from package-level state
- Interfaces are declared by the consumer, so no module is ever required to import another
- Modules that warrant it expose a `mock/` sub-package with test doubles, safe to import in external test suites. Today that is [`cache/mock`](./cache/README.md#testing) and [`certkit/keyloader/mock`](./certkit/README.md#testing)
- No third-party runtime dependencies. Modules depend only on the standard library, and test-only dependencies stay out of consumers' build graphs
- Each module README advertises what the package offers; [pkg.go.dev](https://pkg.go.dev/github.com/remnestal/albstractions) carries the exhaustive per-symbol reference

## Contributing

Semantic commit messages with module scope: `feat(certkit): ...`, `fix(schedule): ...`

Requires [go-task](https://taskfile.dev). Run `task hooks:install` once per clone to enable the pre-commit gate (lint, test, fmt, tidy).

See [CLAUDE.md](./CLAUDE.md) for full development conventions.
