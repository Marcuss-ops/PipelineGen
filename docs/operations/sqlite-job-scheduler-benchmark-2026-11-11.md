# SQLite job claim ordering benchmark — 2026-11-11

This compares the previous per-row `julianday('now')` ordering expression
against the persisted hourly effective-priority index introduced by
`sqlite_jobs/008_claim_aging_index.sql`. Both queries ran against the same
synthetic SQLite queues, using the production index layout from migrations 007
and 008. The query benchmark isolates ordered candidate selection; it does not
include payload hydration, the lease transition, or worker contention.

Machine: Linux amd64, Intel Xeon E3-12xx v2. Each query ran five times per
queue size. Treat these as local comparative measurements, not production SLOs.

| Queued jobs | Dynamic aging query | Persisted score query | Dynamic plan | Persisted plan |
|---:|---:|---:|---|---|
| 1,000 | 0.618 ms/op | 0.066 ms/op | temp B-tree sort | covering `idx_jobs_claim_order` |
| 10,000 | 6.648 ms/op | 0.047 ms/op | temp B-tree sort | covering `idx_jobs_claim_order` |
| 100,000 | 65.238 ms/op | 0.084 ms/op | temp B-tree sort | covering `idx_jobs_claim_order` |

The indexed `ClaimNextMatching` path also found a single compatible job at the
tail of a 100,000-row queue in 1.65 ms/op in a one-iteration smoke benchmark;
its lane query used the covering `idx_jobs_claim_lane_order` index. With all
100,000 rows eligible, that full claim path measured 3.13 ms/op.

The aging refresh has a different cost profile. When the benchmark forced all
100,000 rows due at once, one claim took about 0.96 s while the due rows were
updated. This is a mass catch-up case, not the ordinary indexed selection
latency; the refresh preserves the existing hourly anti-starvation order.
Production monitoring should distinguish this one-time aging catch-up from
steady-state claim cost before considering a background aging worker.

Reproduce the direct query comparison with:

```sh
go test ./internal/platform/sqlite/jobs -run '^$' \
  -bench '^BenchmarkClaimOrderingQuery$' -benchtime=5x -count=1
```

The benchmark logs `EXPLAIN QUERY PLAN` output for both query shapes. The
payload-scoped full-claim benchmark is `BenchmarkClaimNextMatchingQueueSizes`;
it covers 1k, 10k and 100k queues, with sparse, dense and aging-boundary
distributions.

## Recheck with scheduled availability in the production predicate

The first measurements above predated the `available_at` predicate now applied
by every claim path. On 2026-11-11 I reran the ordering comparison with the
production condition `(available_at IS NULL OR available_at <= ?)` in both
query shapes. The queue contained only ready jobs; each value is one local
`-benchtime=5x` sample, so use it to compare query shape and scale, not as an
SLO:

| Queued jobs | Dynamic aging + availability | Persisted aging + availability | Dynamic plan | Persisted plan |
|---:|---:|---:|---|---|
| 1,000 | 1.05 ms/op | 0.078 ms/op | temp B-tree sort | `idx_jobs_claim_order` |
| 10,000 | 7.45 ms/op | 0.062 ms/op | temp B-tree sort | `idx_jobs_claim_order` |
| 100,000 | 81.26 ms/op | 0.112 ms/op | temp B-tree sort | `idx_jobs_claim_order` |

The full indexed `ClaimNextMatching` path was also rechecked with the new
predicate (`-benchtime=1x`, one sample per shape):

| Queued jobs | One compatible row | All rows compatible | Hour-boundary catch-up |
|---:|---:|---:|---:|
| 1,000 | 1.35 ms/op | 2.85 ms/op | 12.62 ms/op |
| 10,000 | 1.27 ms/op | 3.51 ms/op | 122.31 ms/op |
| 100,000 | 1.60 ms/op | 2.97 ms/op | 1,410.50 ms/op |

`hour-boundary` deliberately makes every queued row due for aging refresh, so
the 100k result measures a one-time mass catch-up, not steady-state candidate
selection. The new benchmark includes `available_at` in query-plan capture for
both phase lanes, ensuring future performance reports exercise the production
claim predicate.
