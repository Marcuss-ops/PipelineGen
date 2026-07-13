# capacity-sweep — SigLIP sidecar capacity sweep

This runbook documents `scripts/operations/capacity_sweep.sh`, which
runs an operator-side capacity sweep across worker fan-out counts
**{1, 2, 3, 4}** against the live SigLIP embedding sidecar.

Kind of test: **SigLIP sidecar locality, single host API load** —
the sweep measures the sidecar's realistic per-process throughput when
fed by a bounded bash fan-out pool. It is **not** a multi-host bench
and **not** a substitute for a real load test against a cluster.

## Scope and intent

| Field | Value |
|-------|-------|
| Workload | `POST /embed_visual_from_image` (live Python sidecar) |
| Knob | Per-call concurrency of a bounded background-loop pool |
| Counts swept | `1 2 3 4` (configurable via `--counts`) |
| Wall-clock per tier | 30 s (configurable via `--timeout`) |
| Sample cadence for RSS | 2 s (configurable via `--sample-every`) |

The sidecar's Python process gates concurrent inferences with an
internal `_inference_sem` semaphore
(`scripts/services/embedding_server/__init__.py`). Going above that
semaphore's cap will queue rather than parallelise; treat the resulting
throughput plateau as the sidecar's effective capacity, not the bash
fan-out pool's capacity.

## Prerequisites

- The SigLIP embedding sidecar is running (`bash scripts/start_embedding_server.sh`,
  default `http://127.0.0.1:8001`).
- A real, on-disk PNG/JPEG fixture. The sweep POSTs its absolute path
  every iteration; the sidecar's `PIL.Image.open` reads it. ≥64×64
  recommended.
- Tools in `PATH`: `curl`, `jq`, `awk`, `ps` (`pidstat` is **not**
  required; the sweep polls `/proc/<pid>/status` directly).
- `VELOX_EMBEDDING_SERVER_URL` env var (optional; default:
  `http://127.0.0.1:8001`).

## Quick start

```bash
# Minimal run: 30s per tier × 4 tiers ≈ 2 min total.
bash scripts/operations/capacity_sweep.sh --image-path /tmp/fixture.png

# JSON mode (single object, easier to feed to a dashboard).
bash scripts/operations/capacity_sweep.sh \
    --image-path /tmp/fixture.png --json > /tmp/sweep.json

# Short / cheap smoke run (validate the wiring without the 2-min wait).
bash scripts/operations/capacity_sweep.sh \
    --image-path /tmp/fixture.png --counts "1" --timeout 5

# Different sidecar URL or a longer sweep.
bash scripts/operations/capacity_sweep.sh \
    --url http://127.0.0.1:8001 \
    --image-path /tmp/fixture.png \
    --counts "1 2 3 4 6" \
    --timeout 60
```

## Output — Markdown table (default)

```
Capacity sweep — SigLIP sidecar http://127.0.0.1:8001
image: fixture.png   timeout: 30s/tier   baseline_p50: 0.413s

| workers | throughput (emb/min) | p50 (s) | p95 (s) | err % | throttle | avg RSS (KiB) | safety     |
|---------|----------------------|---------|---------|-------|----------|---------------|------------|
|       1 |                60.00 |   0.413 |   0.872 |  0.00 |        0 |        45120  | ok         |
|       2 |                98.50 |   0.612 |   1.541 |  0.20 |        0 |        56880  | ok         |
|       3 |               108.00 |   0.901 |   2.330 |  0.50 |        0 |        73210  | p95_blowup |
|       4 |               110.20 |   1.402 |   3.880 |  1.10 |        0 |        88450  | p95_blowup |

Recommendation: N=2 — lowest N satisfying throughput ≥ 98.50 emb/min with err<5%, throttle=0, p95≤2×baseline
```

(Numbers above are illustrative — run the script to get your actual
host baseline.)

### Column glossary

| Column | Meaning |
|--------|---------|
| `workers` | Fan-out concurrency (background subshells POSTing in parallel) |
| `throughput (emb/min)` | Successful (HTTP 200) embeddings per minute |
| `p50 (s)` | Median request latency on HTTP-200 responses only |
| `p95 (s)` | 95th-percentile latency on HTTP-200 responses only |
| `err %` | Non-200 / curl-error count as a percentage of total requests |
| `throttle` | Count of HTTP 429 / 503 / 000 responses ("backpressure marker") |
| `avg RSS (KiB)` | Mean sum of VmRSS across the worker PID group, polled every `--sample-every` seconds |
| `safety` | Per-row verdict: `ok`, `high_err`, `throttled`, `p95_blowup` |

### Recommendation heuristic

Pick the **lowest N** that satisfies all of:

- `err_pct < 5%`
- `throttle == 0`
- `p95 ≤ 2.0 × baseline p50` (the N=1 latency)
- Maximises `throughput` subject to the above

If no N satisfies → recommend `N=1` with reason
"saturated at 1 worker on this host".

`core_dump` for the table is per-tier raw curl log + RSS sample log under
`/tmp/capacity_sweep.XXXXXX/` (auto-cleaned on success, kept on crash for
forensics).

## Exit codes (canonical, pager-friendly)

| Code | Meaning |
|------|---------|
| `0` | Sweep completed; verdict emitted |
| `1` | Sidecar unreachable (`GET /health` non-200 or refused) |
| `2` | Fixture missing on disk or unreadable |
| `3` | Required tool missing (`curl`, `jq`, `awk`, `ps`) |
| `4` | All tiers returned zero successful responses (saturation) |
| `5` | Stats aggregation produced no usable samples |
| `6` | Reserved |
| `7` | Bad CLI usage / unknown flag / missing required arg |

## Troubleshooting

### "Sidecar unreachable at http://127.0.0.1:8001 (HTTP 000)"

The embedding sidecar is not running. Start it with
`bash scripts/start_embedding_server.sh` and re-check `GET /health`
manually with `curl http://127.0.0.1:8001/health`. **Do not** interpret
HTTP 000 as "sidecar slow to respond" — it's a hard reachability failure
and the sweep refuses to run.

### "no tier produced any successful responses"

The sidecar is reachable but every request is failing. Common causes:

- The fixture PNG is invalid (PIL rejects 0-byte or corrupt files).
- `_file(1)` says PNG but PIL still rejects it (HTTP 500 from the sidecar).
  That's a fixture-encoder incompatibility, not a sweep regression — try a
  different fixture (e.g. a fresh 384×384 PNG generated by PIL itself).
- The model is unloading due to memory pressure (`SKIP_SIGLIP=0` only —
  check sidecar logs).
- The Python `_inference_sem` cap is set tight and the loop traffic
  exceeds it. Try `--counts "1"` for a single-worker stress test first.
- **Every row reports `err=100%` with each request failing fast (~0.005s).**
  The sidecar is returning **HTTP 501** with body
  `{"detail":"SigLIP model not loaded (set SKIP_SIGLIP=0 and restart)"}`.
  This is a typed sidecar-disabled signal — the host's sidecar was
  started with `SKIP_SIGLIP=1` (CPU-light mode for ops that don't need
  vision embeddings). **The sweep is correctly fail-closed; the operator
  must decide whether to load the model on this host before re-running.**
  Verify with `tr '\0' '\n' < /proc/$(lsof -ti tcp:8001)/environ | grep SKIP_SIGLIP`.

### Rows show `p95_blowup` for every N>1

Latency degrades sharply as concurrency grows. This is the **expected**
shape for a CPU-only SigLIP encoder — the model saturates a single
core and additional workers only queue. The recommendation will
collapse to `N=1` (or the lowest N that fits safety). This is **not**
a bug — it's the signal you want from the sweep.

### Throughput plateaus across N=2, N=3, N=4

Most likely the sidecar's `_inference_sem` is the bottleneck. Inspect
`scripts/services/embedding_server/__init__.py` for the semaphore
construction and consider widening it if your host has CPU headroom
(unlikely on a single-host CPU-only box).

### Every row shows `avg RSS (KiB) = 0`

The sweep polls `/proc/<pid>/status` directly to read VmRSS. In
containerised environments (Docker with `--read-only` flags, Podman,
some sandboxes) `/proc` reads are denied and the awk `getline` silently
returns 0. Test with `cat /proc/self/status` — if that fails, RSS will
always read 0 regardless of N. Until a future revision adds a
`sysstat`/`pidstat` fallback, treat an all-zero RSS column as "host
denies `/proc` reads" rather than "the host has zero resident memory."

### Sidecar log churn during a sweep

Each `/embed_visual_from_image` POST produces an INFO-level access line
in the FastAPI sidecar log. A full N=4 sweep at TIMEOUT=30 fires
~240–400 POSTs, which means a 200–400-line log tail. If you're
grepping for unrelated log noise during a sweep, lower the sidecar log
level to WARN temporarily, or capture stderr to a separate file.

## When to re-run

- After sidecar restart / model version bump.
- After host upgrade (more cores, more RAM).
- After `_inference_sem` cap change in the sidecar.
- After moving the sidecar between hosts (e.g., to a beefier GPU host).

`make verify-main` is unrelated and should still pass — the sweep is
an **operator-side tool**, not a `make` target.
