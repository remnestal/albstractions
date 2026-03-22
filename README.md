# albstractions

Small, focused Go modules for recurring service plumbing like throttling and certificates. Each one is versioned and importable on its own, dependency-free, and perfectly balanced, as all things should be.

## Usage

Tags are scoped by module path (`certkit/v0.1.0`, `schedule/v0.1.0`). Import only the ones you need:

```bash
go get github.com/remnestal/albstractions/certkit
go get github.com/remnestal/albstractions/schedule
```

## Modules

| Module | Description |
|--------|-------------|
| [`schedule`](./schedule) | Delay strategies for throttling, rate-limiting, and backoff |
| [`throttle`](./throttle) | Paces function and HTTP call cadence using a pluggable schedule |
| [`certkit`](./certkit) | Certificate and key management: key loading, self-signed PKI, and mutual TLS helpers |

## Design

- Mandatory arguments in function signatures, optional config via the `(...Option)` variadic pattern
- Dependencies are passed in, never constructed internally and never read from package-level state
- Interfaces are declared by the consumer, so no module is ever required to import another
- Modules that warrant it expose a `mock/` sub-package with test doubles, safe to import in external test suites
- No third-party runtime dependencies. Modules depend only on the standard library, and test-only dependencies stay out of consumers' build graphs

## Contributing

Semantic commit messages with module scope: `feat(certkit): ...`, `fix(schedule): ...`

See [CLAUDE.md](./CLAUDE.md) for full development conventions.
