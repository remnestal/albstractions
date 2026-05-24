# Bounded by time only — no capacity eviction

The cache evicts strictly by TTL; there is no size cap and no LRU/LFU policy, so
a workload with unboundedly many distinct keys grows until entries expire. This
is deliberate: capacity eviction is a separable concern that can be added later
as a wrapper or a `Memory` option without disturbing this design, and adding it
pre-emptively (a policy plus per-entry recency/frequency bookkeeping) is
unrequested complexity.

This is distinct from the memory concern that *is* handled:
`WithRebuildInterval` periodically rebuilds the underlying map to reclaim the
bucket array Go never shrinks after deletions. That bounds *map overhead*, not
*entry count*.
