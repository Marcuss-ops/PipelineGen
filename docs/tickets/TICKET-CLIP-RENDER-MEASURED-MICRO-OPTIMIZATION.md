# TICKET — clip.render: measured micro-optimisation (Wave E)

**Priority:** P2 — do not optimise before measuring.
**Status:** OPEN (blocked on a real GPU/Chronon/RenderingGen deployment).
**Owner:** `internal/platform/renderinggen` + ops benchmark harness.

## 1. Rule

No generic connection pooling, no speculative IPC work. Measure first, then
optimise only what the numbers justify.

## 2. Already landed

The serial-prefetch fix this ticket depended on is done:

* `prefetchClipAssets` already uploads with `errgroup.SetLimit(4)`.
* `prefetchClipAssets` now **deduplicates by content digest** before fan-out
  (the same bytes reachable from several plan slots are staged once).
* `NewHTTPAssetPrefetcher` (the shared overlay prefetcher) already has both the
  bounded errgroup and digest dedupe.

## 3. What to measure before touching IPC

The Chronon IPC question is real but lower priority. Instrument and report:

```text
IPC connect duration (per request)
prefetch request duration (HEAD + PUT)
requests per render
artifact download duration
```

## 4. Decision rule

Only if connect cost is material:

* **Preferred:** one persistent Chronon IPC connection per RenderingGen worker.
* **Acceptable:** a small bounded pool (2–4 Unix connections).

A generic connection pool is explicitly out of scope until the numbers demand
it.

## 5. Acceptance criteria

- [ ] Report with median/p95 IPC connect duration, prefetch request duration
      and requests-per-render from a real deployment.
- [ ] Decision recorded (persistent per-worker connection vs bounded pool vs no
      change) with the measurement that justifies it.
- [ ] If a change is made: end-to-end benchmark re-run showing the delta with
      no correctness regression.
