# Parallel-Images Verification Runbook

This runbook is the canonical operator-side companion for the
`/api/images/generated/generate` endpoint and its supporting benchmarks,
inspectors, and visual-side verifiers. It is **reproducible
end-to-end** in one shell session on a fresh host.

## 1. Scope & status

| Field | Value |
|-------|-------|
| Endpoint | `POST /api/images/generated/generate` |
| Endpoint surface | `internal/api/images/generated_generate_handler.go` |
| Generation backend | **RETIRED** per `PR-IMAGES-CHROME-RETIRED` (July 2026). The HTTP route is wired but `Service.Gen == nil` ⇒ handler returns `ErrImageGenNotImplemented` ⇒ HTTP 501. |
| Benchmarks | Three new operator scripts under `scripts/operations/` (this PR). |
| Inspectors | One new operator script + one existing media-asset inspector. |
| Visual verifiers | Two existing scripts (`verify_siglip_sidecar.sh`, `verify_qdrant_point.sh`). |
| Worker-count tuning | One existing script (`capacity_sweep.sh`). |

**Honest operator signal**: the new benchmarks below will return HTTP 501
on a host where the image-generation backend has not been re-installed.
This is **not a regression**. It is the godlike/07 fail-closed contract
holding (no fake OK; the operator is told the wire works but the
provider failed closed). The benchmarks still produce throughput +
latency + RSS telemetry; throughput counts only HTTP 200 successes.

## 2. Prerequisites

- The PipelineGen HTTP server is running and reachable. Default URL
  `http://127.0.0.1:${VELOX_PORT:-8000}`. Set `VELOX_PORT` to override.
- The SigLIP sidecar is running and reachable. Default URL
  `http://127.0.0.1:8001`. Set `VELOX_EMBEDDING_SERVER_URL` to override.
- Qdrant is running and reachable. Default URL `http://127.0.0.1:6333`.
  Set `VELOX_QDRANT_URL` to override.
- Tools in `PATH`: `curl`, `jq`, `awk`, `ps`, `sqlite3`, `lsof`.
- A real PNG fixture at `/tmp/cs_fixture.png` (384×384, valid). Created
  in 5 s by:
  ```bash
  python3 -c "from PIL import Image; Image.new('RGB',(384,384),(128,128,128)).save('/tmp/cs_fixture.png')"
  ```
- Access to the SQLite DB. Default path lookup: `$VELOX_DB` →
  `data/media/media.db.sqlite` → `data/velox.db` → `/var/lib/velox/velox.db`.

## 3. Boot-warm pool

The `chrome-pool-prewarm` StartUpStep kicks off at server boot. The step
is declared in `internal/app/wire_services.go::StartupStep` and calls
`imgSvc.TriggerPrewarm(ctx, "startup-prewarm", poolSize)` against the
`internal/application/images/ChromeImageProviderPool`.

```go
// wire_services.go (canonical shape)
{   Name: "chrome-pool-prewarm", Required: true,
    Start: func(ctx context.Context) error {
        imgSvc.TriggerPrewarm(ctx, "startup-prewarm", poolSize)
        ...
    }},
```

### Current production reality

| State | Behaviour |
|-------|-----------|
| Generation backend LIVE | Pre-warm spins up the browser/Playwright subprocess ahead of first traffic; readiness probe passes. |
| Generation backend RETIRED (current) | Pool is empty. Pre-warm is a no-op. Step logs `pool size 0` and `ErrImageGenNotImplemented` to the operator log. Step Required=true means boot fails closed — DO NOT turn this off. |

To restore: re-implement `ChromeImageProviderPool` per `PR-CHROME-PROVIDER-SPLIT`
(`internal/application/images/chrome_provider_pool.go`) and rebuild the
server. Until then, the boot step is the **honest start-up signal**.

## 4. Endpoint contract — `POST /api/images/generated/generate`

### Request envelope

```json
{
  "prompt":         "a peaceful mountain valley at dawn (parallel images benchmark)",
  "width":          512,
  "height":         512,
  "style":          "cinematic",
  "tags":           ["smoke", "parallel_images_benchmark"],
  "delivery_mode":  "fast" | "complete"
}
```

`delivery_mode` is **optional**. When omitted, the handler defaults to
`"fast"`. Unknown values return HTTP 400 with body
`{"error":"delivery_mode must be \"fast\" or \"complete\""}`.

### Response envelope (HTTP 200)

```json
{
  "asset_id":                   "<sha256:...>",
  "origin":                     "generated",
  "provider":                   "google-slides",
  "preview_url":               "<local relative path>",
  "style_id":                   "<style>",
  "license":                    "",
  "author":                     "",
  "drive":                      {"file_id":"","folder_id":"","link":"","path":""},
  "indexed":                    false,
  "visual_embedding_dimensions": 768,
  "embedding_version_visual":   "2026-06-26-v1",
  "metadata_json":              "<...>",
  "delivery_mode":              "fast" | "complete",
  "location":                   {"category":"","subject":"<id>","provider":"<name>","style":"<style>"}
}
```

### `delivery_mode` semantics — canonical (godlike/06 + godlike/07)

| Mode | Behaviour | Honour order |
|------|------------|--------------|
| `""` (omitted) | Defaults to `"fast"`. Handler invokes `GenerateSmartImage` with `skipDrive := true`. | equivalent to `"fast"` |
| `"fast"` | `skipDrive := true`. Local write + `media_assets` row + `asset.index.requested` outbox row all in one SQLite transaction. Response carries `asset_id` immediately. Drive upload + SigLIP embedding + Qdrant upsert run async via the outbox dispatcher AFTER SQLite commit. | AGENTS.md "durable side effects after database commits must use the transactional outbox" — NEVER inline. |
| `"complete"` | Same as `"fast"` but ALSO waits (bounded timeout) for the outbox dispatcher to ack the `asset.index.requested` event. On timeout the response still carries `asset_id` — the timeout is a hint, not an error, because delivery is by definition async-safe. | godlike/07 — surface the timeout as a hint, NOT as an error. |

### HTTP error mapping

| HTTP code | Body | Meaning | Script exit |
|-----------|------|---------|-------------|
| `200` | per envelope above | success | `0` |
| `400` | `{"error":"delivery_mode must be \"fast\" or \"complete\""}` | Unknown `delivery_mode` value | `2` |
| `500` | per `app.InternalError` | Internal failure | `2` |
| `501` | `{"error":"image generation endpoint has been removed", "message":"image generation via Google Slides is not configured"}` | `Gen == nil` (current production state). HONEST, not an outage. | `2` (with banner) |
| Network refuse / timeout | curl exit code | Sidecar not running | `1` |

## 5. SQLite/outbox inspector

`scripts/operations/inspect_outbox.sh` is the canonical read-only
inspector for the `outbox_events` table.

### Subcommands

| Subcommand | Behaviour | Exit |
|------------|------------|------|
| `stats` | Counts by status (pending / processing / completed / dead_letter / superseded). Surfaces oldest pending. | `0` |
| `list-pending` | `WHERE status='pending' ORDER BY id ASC LIMIT N` | `0` or `2` |
| `list-processing` | `WHERE status='processing' ORDER BY lease_expires_at ASC LIMIT N` | `0` or `2` |
| `list-completed` | `WHERE status='completed' ORDER BY completed_at DESC LIMIT N` | `0` or `2` |
| `list-stuck` | `WHERE status='pending' AND attempt_count >= max_attempts - 2` (canonical pre-DLQ) + MAX_MISSING (pending + max_attempts NULL/0). | `0` or `2` |
| `list-dead-letter` | `WHERE status IN ('dead_letter','superseded') ORDER BY id DESC LIMIT N` | `0` or `2` |
| `lookup <aggregate_id>` | `WHERE aggregate_id = '<id>' ORDER BY id DESC LIMIT N` | `0` (rows) or `2` (no rows) |

### Common flags

- `--db <PATH>` — override SQLite path lookup.
- `--limit <N>` — cap rows returned (default 50, max 1000).
- `--event-type <T>` — filter by canonical event_type.
- `--json` — emit single-line JSON instead of TSV.

### Output columns

`id`, `event_type`, `status`, `attempt_count`, `max_attempts`,
`lease_id`, `last_error` (first 80 chars), `completed_at`,
`created_at`, `aggregate_id`.

### Tagged tokens (pager-friendly grep targets)

The TSV renderer tags STUCK / MAX_MISSING rows with literal
`${C_RED}STUCK${C_RESET}` / `MAX_MISSING` even when stdout is
redirected (NO_COLOR=1 ⇒ no ANSI; otherwise bracket-coloured). Operators
can:

```bash
bash scripts/operations/inspect_outbox.sh list-stuck | grep -F MAX_MISSING
bash scripts/operations/inspect_outbox.sh list-stuck | grep -F STUCK
```

## 6. The 3-request benchmark — `parallel_images_3req_benchmark.sh`

Runs three sequential POSTs exercising the delivery_mode contracts.

```bash
# Cheapest smoke (~3 s):
bash scripts/operations/parallel_images_3req_benchmark.sh

# Override prompt + timeout:
bash scripts/operations/parallel_images_3req_benchmark.sh \
    --prompt "a stil..." --timeout 5

# JSON output for CI:
bash scripts/operations/parallel_images_3req_benchmark.sh --json | jq
```

### Each request's emission

| Field | Captured from |
|--------|-------------|
| `code` | `curl -w '%{http_code}'` |
| `time_s` | `curl -w '%{time_total}'` |
| `asset_id` | `jq -r '.asset_id'` |
| `server_mode` | `jq -r '.delivery_mode'` |
| `error_msg` | `jq -r '.error // .message // .detail'` (first 160 chars) |

### `backend_state` verdict

| All 3 status | Verdict |
|----|----|
| 200 | `live` (operationally healthy). Exit `0`. |
| 501 | `retired` (current production). Exit `2` with banner. |
| Anything else | `drift`. Exit `2`. |

## 7. The 30-image stress test — `parallel_images_30img_stress.sh`

Spawns N background `curl` workers (default 30) POSTing the canonical
`ImageGenerationRequest` to `/api/images/generated/generate` for a
fixed wall-clock duration (default 15 s). Aggregates:
throughput-emb/min, p50, p95, error rate, throttles, RSS.

```bash
# Default (30 workers × 15 s):
bash scripts/operations/parallel_images_30img_stress.sh

# Smaller sweep for CI:
bash scripts/operations/parallel_images_30img_stress.sh \
    --workers 5 --timeout 5

# JSON output:
bash scripts/operations/parallel_images_30img_stress.sh --json | jq
```

### Output table (human mode)

```
| workers | throughput (emb/min) | p50 (s) | p95 (s) | err % | throttle | avg RSS (KiB) | safety                |
|---------|----------------------|---------|---------|-------|----------|---------------|-----------------------|
|      30 |                 0.00 |   0.000 |   0.000 | 100.00|        0 |             0 | retired_or_drift      |
```

`safety` is `retired_or_drift` when `err_pct >= 90%`, else `ok`. The
banner is emitted to inform the operator in case of systematic 501.

### Output table (JSON mode)

```json
{
  "tool":"parallel_images_30img_stress",
  "kind":"fan_out_load_test",
  "url":"http://127.0.0.1:8000",
  "prompt":"a peaceful mountain valley at dawn (parallel images 30-image stress)",
  "timeout_s_per_tier":15,
  "workers":30,
  "any_success":false,
  "tiers":[
    {"tag":"n=30","n":30,"throughput_emb_per_min":0.0,"p50_s":0.0,"p95_s":0.0,
     "err_pct":100.0,"throttle_signals":0,"avg_rss_kib":0}
  ]
}
```

## 8. Sidecar visual checks — `verify_siglip_sidecar.sh`

Per the canonical SigLIP sidecar verifier SSOT. Runs four assertions
(model, model_version, dimensions=768, |embedding|=768) against
`POST /embed_visual_from_image` (single) and `POST /embed_visual_from_images`
(batch, 1..512 paths).

```bash
bash scripts/operations/verify_siglip_sidecar.sh \
    --image-path /tmp/cs_fixture.png --json

bash scripts/operations/verify_siglip_sidecar.sh \
    --image-path /tmp/cs_fixture.png \
    --batch-count 4 \
    --batch-image-paths "/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png" \
    --json
```

See `docs/operations/siglip-batch-endpoint.md` for the batch endpoint
contract and `verify_siglip_sidecar.sh` § Section 9 for batch assertions.

## 9. Qdrant visual checks — `verify_qdrant_point.sh`

Per the canonical Qdrant pointer verifier. Asserts the asset point in
Qdrant's `media_assets_current` collection matches the canonical
`(points=1, payload.media_type=image, payload.visual_dimensions=768)`
invariants, then performs a SigLIP text-to-visual search.

```bash
bash scripts/operations/verify_qdrant_point.sh \
    --asset-id <sha256:abc...> --query "stock cinematic wide shot" --json
```

See `docs/operations/verify-qdrant-point.md` (when present) for the full
contract.

## 10. Worker-count tuning table — `capacity_sweep.sh`

Iterates worker fan-out N ∈ {1, 2, 3, 4} against the live SigLIP
sidecar and records per-N throughput + p50 + p95 + RSS + CPU% for
human operator review. Outputs a Markdown table with a recommended
N value enforcing the safety envelope
`err<5% / throttle=0 / p95≤2×baseline_p50`.

```bash
bash scripts/operations/capacity_sweep.sh \
    --image-path /tmp/cs_fixture.png \
    --counts "1 2 3 4" \
    --timeout 30
```

Sample output (current state — `SKIP_SIGLIP=1` on this host, so all
tiers return HTTP 501):

```
| workers | throughput (emb/min) | p50 (s) | p95 (s) | err % | throttle | avg RSS (KiB) | safety     |
|---------|----------------------|---------|---------|-------|----------|---------------|------------|
|       1 |                 0.00 |   0.000 |   0.000 | 100.00|        0 |             0 | high_err   |
|       2 |                 0.00 |   0.000 |   0.000 | 100.00|        0 |             0 | high_err   |
|       3 |                 0.00 |   0.000 |   0.000 | 100.00|        0 |             0 | high_err   |
|       4 |                 0.00 |   0.000 |   0.000 | 100.00|        0 |             0 | high_err   |

Recommendation: N=1 — saturated at 1 worker on this host
```

For full semantics, see `docs/operations/capacity-sweep.md`.

## 11. End-to-end reproduction recipe

The single-line recipe below reproduces all benchmark + verifier
artefacts on a fresh host that has the PipelineGen HTTP server running:

```bash
bash scripts/operations/parallel_images_3req_benchmark.sh && \
    bash scripts/operations/parallel_images_30img_stress.sh --workers 30 --timeout 15 && \
    bash scripts/operations/capacity_sweep.sh --image-path /tmp/cs_fixture.png --counts "1 2 3 4" --timeout 30 && \
    bash scripts/operations/verify_siglip_sidecar.sh --image-path /tmp/cs_fixture.png --batch-count 4 \
        --batch-image-paths "/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png" && \
    bash scripts/operations/inspect_outbox.sh stats
```

Per-script exit codes surface slack in any step. `make verify` (or the
canonical `-fail-closed` chain) is gated on all exit codes being
`0` (or expected-`2` on the retired-backend state).

## 12. Troubleshooting

### "Why am I seeing HTTP 501 on `/api/images/generated/generate`?"

The image-generation backend was retired per `PR-IMAGES-CHROME-RETIRED`
(July 2026). The HTTP route is wired (so verifiers can probe it
honestly), but `Service.Gen == nil` ⇒ `ErrImageGenNotImplemented`. This
is godlike/07 fail-closed — DO NOT replace with a fake 200 response.

To restore the backend: re-implement `ChromeImageProviderPool` per the
split in `internal/application/images/chrome_provider_pool.go` + the
chained helpers (`chrome_provider_retry.go`, `slide_worker_*.go`).

### "Why is the boot-warm pool step failing in `make run` logs?"

Same retired-backend state. The `Required: true` flag means the
startup step WILL return a typed error and the server boot WILL
abort. This is intentional. Either restore the backend or set
`Required: false` for `chrome-pool-prewarm` in `wire_services.go` (NOT
recommended — disables the canonical startup probe).

### "My monitor dashboard shows 0 throughput on the 30-image stress."

Expected on a retired-backend host. The script emits a yellow-info
banner near the end that explains this. Look for the actual error_rate
column — `100.00%` confirms the HTTP 501 state.

### "How do I find a STUCK outbox event?"

```bash
bash scripts/operations/inspect_outbox.sh list-stuck | column -t -s$'\t'
```

`STUCK` rows tagged in the rightmost column surface `attempt_count` ≥
`max_attempts - 2`. `MAX_MISSING` rows tagged surface `pending +
max_attempts IS NULL OR 0` — these will never reach DLQ because the
threshold is missing.

### "How do I find the canonical SigLIP sidecar failure status?"

```bash
SKIP_SIGLIP=1 bash scripts/start_embedding_server.sh &
# /health returns 200; /embed_visual_from_* returns 501.
bash scripts/operations/verify_siglip_sidecar.sh --image-path /tmp/cs_fixture.png  # exits 2
```

### "How do I tell capacity_sweep from 'saturated' vs 'retired'?"

`recommendation` row in the table:
- `saturated at 1 worker on this host (no N satisfied throughput + safety)` —
  every tier returned non-200; godlike/07 fail-closed.

## 13. Forward pointers (canonical SSOT)

- **Endpoint SSOT**: `internal/api/images/generated_generate_handler.go`,
  request envelope `internal/api/images/request_types.go::ImageGenerationRequest`.
- **Boot step SSOT**: `internal/app/wire_services.go::StartupStep`.
- **Outbox SSOT**: `internal/infrastructure/database/sqlite/outboxevents/pool.go`,
  `errors.go`.
- **Sidecar SSOT**: `scripts/services/embedding_server/visual.py` + `models.py`
  for `/embed_visual_from_image` + `/embed_visual_from_images`.
- **Qdrant SSOT**: `internal/infrastructure/qdrant/search/embedders.go::EmbedImages`
  + `internal/infrastructure/qdrant/schema/schema.go::VisualEmbeddingDim`.
- **Media asset inspection SSOT**: `scripts/operations/inspect_media_asset.sh`
  + `docs/operations/inspect-media-asset.md`.
- **Capacity sweep SSOT**: `docs/operations/capacity-sweep.md`.

When this runbook changes: `git grep` MUST remain green on
`'parallel-images-verification-runbook.md'` (mirrors the SSOT
forward-pointer contract).
