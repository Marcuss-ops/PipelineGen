# SQLite job claim benchmark — 2026-11-10

> **Storico:** questo report misura la query precedente, che calcolava
> l'aging durante l'ordinamento e usava `idx_jobs_scheduling_lane`. Il
> 2026-11-11 la selezione è stata aggiornata all'indice della priorità
> effettiva persistita (`idx_jobs_claim_order` /
> `idx_jobs_claim_lane_order`). Per il confronto corrente, i piani query e il
> costo di catch-up dell'aging, vedere
> [sqlite-job-scheduler-benchmark-2026-11-11.md](sqlite-job-scheduler-benchmark-2026-11-11.md).

## Setup

Command:

```sh
go test ./internal/platform/sqlite/jobs -run '^$' \
  -bench '^BenchmarkClaimNextMatchingQueueSizes$' -benchtime=10x -count=3 -benchmem
```

The benchmark seeds 1k, 10k and 100k queued jobs in an in-memory broker DB.
Each size has two distributions: one eligible `settle` job at the tail of the
ordered queue, and all jobs eligible for the dedicated `settle` lane. The
benchmark includes the claim and payload selection path; the seeded jobs use
the persisted scheduling descriptor. In the sparse case, the one claimed row
is restored while the timer is stopped so every iteration sees the same queue
shape. Reported values below are medians of three 10-operation samples; ranges
show the minimum and maximum sample means, not production latency percentiles.

Environment: Linux amd64, Intel Xeon E3-12xx v2 (Ivy Bridge, IBRS).

## Results

| Queued jobs | Distribution | Median claim time (range) | Bytes/op | Allocs/op |
|---:|---|---:|---:|---:|
| 1,000 | one eligible | 0.86 ms (0.82–1.37) | 28,352 | 610 |
| 1,000 | all eligible | 3.90 ms (3.49–4.16) | 291,029 | 4,882 |
| 10,000 | one eligible | 0.88 ms (0.86–0.92) | 28,132 | 610 |
| 10,000 | all eligible | 7.73 ms (7.59–8.40) | 291,378 | 4,884 |
| 100,000 | one eligible | 0.86 ms (0.83–0.94) | 27,693 | 609 |
| 100,000 | all eligible | 55.89 ms (55.75–60.92) | 290,828 | 4,882 |

All six query plans reported:

```text
SEARCH jobs USING COVERING INDEX idx_jobs_scheduling_lane
(status=? AND type=? AND execution_lane=?)
USE TEMP B-TREE FOR ORDER BY
```

## Interpretation

Persisting `execution_lane` removes payload reads for the dedicated lane and
keeps the sparse-match case roughly flat as unrelated queued jobs grow. The
dynamic aging score (`priority + floor(queue_age_hours)`) still requires a
temporary sort over all eligible rows. The dense-lane case has a 56 ms median
at 100k jobs, so the current index does not remove the scheduler's dense-queue
cost.

This isolated in-memory benchmark does not establish p50/p99 latency under
concurrent claimers, production queue distributions, or WAL contention. Keep
the existing starvation ordering until a replacement proves equivalent aging
behavior and is measured against those workloads.
