# Fallible enumeration via ItemsContext

`Items` (infallible) is joined by
`ItemsContext(ctx) (iter.Seq2[K, V], func() error)`, which returns the iterator
plus a terminal-error accessor in the style of `bufio.Scanner.Err` /
`sql.Rows.Err`. Iteration honours `ctx`; the accessor, checked after the range
loop, reports a backend failure or the cancellation cause. `Items` becomes the
infallible wrapper (background context, error discarded), completing the dual API
for enumeration.

This closes a gap in ADR 0001: every operation had a context form except `Items`,
yet enumeration is the heaviest I/O operation (a Redis `SCAN` over millions of
keys) — exactly the case that needs cancellation and error reporting. The
in-memory `Memory` never errors, so its accessor is non-nil only on `ctx`
cancellation; the value is in letting an external backend report a mid-scan
failure that would otherwise be indistinguishable from end-of-iteration.

## Considered options

- **A `func() error` terminal accessor** (chosen): keeps the natural
  `for k, v := range seq` loop and surfaces the error out of band. The cost is
  that the accessor is easy to forget — the same trade-off as `bufio.Scanner.Err`.
- **`iter.Seq2[Item[K, V], error]`** (a fallible iterator yielding an error per
  step): the error cannot be ignored, but it forces a public `Item[K, V]` type and
  replaces the natural `k, v` destructuring with `item, err`. Rejected for the
  surface cost and the clumsier loop.
- **A `ctx`-only `ItemsContext(ctx) iter.Seq2[K, V]`**: adds cancellation but no
  error channel, so it would not actually close the gap (a mid-scan error would
  still be invisible). Rejected as a half-measure.
- **Leave `Items` exempt and document why**: cheapest, but the dual API is the
  module's headline contract and an enumerable I/O backend is a stated goal, so the
  inconsistency was worth removing rather than excusing.

## Consequences

- Every `Cache` implementation and the `mock` backend implement `ItemsContext`.
- `Sharded.ItemsContext` reports a single terminal error — the one that stopped
  the scan — rather than an aggregate: iteration halts at the first backend error
  or `ctx` cancellation, matching the terminal-error model.
