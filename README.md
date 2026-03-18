# albstractions

Small, focused Go modules for recurring service plumbing like throttling and certificates. Each one is versioned and importable on its own, dependency-free, and perfectly balanced, as all things should be.

## Usage

Tags are scoped by module path (`keyloader/v0.1.3`, `schedule/v0.1.0`). Import only the ones you need:

```bash
go get github.com/remnestal/albstractions/keyloader
go get github.com/remnestal/albstractions/schedule
```

## Modules

| Module | Description |
|--------|-------------|
| [`keyloader`](./keyloader) | Key loading and management |
| [`mtls`](./mtls) | Mutual TLS helpers |
| [`pki`](./pki) | Self-signed PKI and certificate utilities |
| [`schedule`](./schedule) | Delay strategies for throttling, rate-limiting, and backoff |
| [`throttle`](./throttle) | Paces function and HTTP call cadence using a pluggable schedule |

## Design

- Mandatory arguments in function signatures, optional config via the `(...Option)` variadic pattern
- Dependencies are passed in, never constructed internally and never read from package-level state
- Modules that warrant it expose a `mock/` sub-package with test doubles, safe to import in external test suites
- No third-party runtime dependencies. Modules depend only on the standard library, and test-only dependencies stay out of consumers' build graphs

## Contributing

Semantic commit messages with module scope: `feat(certkit): ...`, `fix(schedule): ...`

See [CLAUDE.md](./CLAUDE.md) for full development conventions.
