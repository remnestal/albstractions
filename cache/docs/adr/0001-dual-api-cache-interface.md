# Dual-API Cache interface, no adapter scaffolding

`Cache[K,V]` exposes both an infallible form (`Get`/`Set`/`Delete`/`Items`) and a
context form (`GetContext`/`SetContext`/`DeleteContext`) on a single interface.
The context methods are authoritative; the infallible ones call them with a
background context and treat an error as a miss on reads or ignore it on writes.
We chose this so that the ergonomic in-memory call site and an I/O backend
(Redis, a database) that needs cancellation and error reporting both satisfy the
*same* interface — which is what lets the wrapper layers compose.

## Considered options

- **Infallible-only** (`Get(key) (V, bool)`): cleanest for `Memory`, but an
  external backend has nowhere to report I/O errors or honour cancellation,
  defeating the "plug in Redis" goal.
- **Context-only**: honest for I/O, but burdens the headline in-memory cache
  with a `ctx` and `error` that every call ignores.
- **A minimal `Core` interface plus an `Adapt` helper** to derive the infallible
  trio for backend authors: rejected as scaffolding that added two exported
  symbols and a concept without earning its keep. Backends implement all seven
  methods directly; the three infallible ones are trivial wrappers, and one
  unexported `miss()` helper keeps their semantics identical internally.
