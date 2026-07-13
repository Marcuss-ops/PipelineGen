# siglip-batch-endpoint — Python sidecar batch contract

This runbook documents `POST /embed_visual_from_images` on the Python
embedding sidecar (`scripts/services/embedding_server/visual.py`) — the
true-batch counterpart to `POST /embed_visual_from_image`.

## Intent

`/embed_visual_from_image` issues one forward pass per HTTP call. Bulk
callers (video-frame backfills, slide-deck ingestion, artlist image
reindexing) amortise HTTP cost and CPU forward-pass cost via a single
`/embed_visual_from_images` call: **N image paths → 1 HTTP round-trip →
N×768d vectors**.

The Go-side `EmbedImages` adapter (`internal/infrastructure/qdrant/search/embedders.go`)
unconditionally uses the batch endpoint — even for N=1 — so the per-image
single-image wire is structurally dead on the Go caller side and remains
present only for the verifier happy-path surface.

## Wire shape

### Request

```http
POST /embed_visual_from_images HTTP/1.1
Content-Type: application/json

{
  "image_paths": ["/abs/path/a.png", "/abs/path/b.png", "/abs/path/c.png"]
}
```

| Field | Type | Constraint |
|-------|------|------------|
| `image_paths` | `list[str]` | `1 ≤ len ≤ 512` (Pydantic validator on the sidecar; FastAPI surfaces 422 on violation). |

### Response (HTTP 200, order-preserved)

```json
{
  "embeddings": [
    [0.0123, 0.0456, ..., 0.0789],
    [-0.0345, 0.0678, ..., 0.0912],
    [0.0234, -0.0567, ..., 0.0345]
  ],
  "dimensions": 768,
  "count": 3,
  "model": "google/siglip-so400m-patch14-384",
  "model_version": "2026-06-26-v1"
}
```

| Field | Type | Notes |
|-------|------|-------|
| `embeddings[i]` | `list[float]` (length = `dimensions`) | `embeddings[i]` corresponds to `image_paths[i]` (JSON list order, preserved by FastAPI encoding). |
| `dimensions` | `int` | Canonical SigLIP so400m patch14-384 output: 768. |
| `count` | `int` | Always equal to `len(request.image_paths)`; Go caller fail-closes on mismatch. |
| `model` | `string` | `"google/siglip-so400m-patch14-384"` (sidecar-claimed; verifier substring-validates against canonical short form). |
| `model_version` | `string` | `"2026-06-26-v1"`. |

## Fail-closed semantics (godlike/07)

| Failure | HTTP code | Body (key/value) |
|---------|-----------|------------------|
| Sidecar unreachable (no `siglip_model`) | `501` | `{"detail":"SigLIP model not loaded (set SKIP_SIGLIP=0 and restart)"}` — Go caller treats as `transport.ErrChannelUnavailable` and short-circuits. |
| Empty `image_paths` | `422` | FastAPI Pydantic-rendered. |
| `len(image_paths) > 512` | `422` | FastAPI Pydantic-rendered. |
| PIL read failure on `image_paths[idx]` | `500` | `{"detail":"image_paths[<idx>] PIL open failed: <ErrorType>: <reason>"}`. Caller must retry the **whole** batch — no silent drop of failing items. |
| Internal encode failure | `500` | `{"detail":"<reason>"}`. Caller must retry. |

The batch endpoint NEVER returns a partial-success array. Either every
image is embedded (HTTP 200, ordered N×768d vectors) or the request fails
closed with a typed error.

## Concurrency

The FastAPI route acquires the canonical `_inference_sem`
(`scripts/services/embedding_server/__init__.py`, `INFERENCE_CONCURRENCY=2`)
for the **entire duration** of `siglip_model.encode([img1, ...])`. Two
concurrent batch callers serialize rather than fan out — protecting CPU
and RAM under the `N=512` upper bound.

## Verifier

`scripts/operations/verify_siglip_sidecar.sh` exercises the batch
endpoint when called with `--batch-count N` + `--batch-image-paths <csv>`.
Failures map to the existing 0..7 exit-code family (no new codes added):

| Failure | Exit code |
|---------|-----------|
| HTTP non-200 (e.g. 501 under SKIP_SIGLIP=1) | `2` |
| Envelope drift (`.embeddings` is not an array, or `count` mismatch) | `2` |
| `dimensions` ≠ 768 | `3` |
| `model` does not contain canonical short form | `4` |
| `model_version` does not match canonical | `5` |
| Per-vector length mismatch (one or more `embeddings[i] != dimensions`) | `6` |
| CLI parse failure | `7` |

Example:

```bash
bash scripts/operations/verify_siglip_sidecar.sh \
  --image-path /tmp/cs_fixture.png \
  --batch-count 04 \
  --batch-image-paths "/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png,/tmp/cs_fixture.png"
```

## When to add / use this endpoint

- **Use** the batch endpoint for any caller that already knows N≥1 paths to
  encode. The round-trip cost amortises well.
- **Don't** add new callers that issue the single-image
  `/embed_visual_from_image` endpoint — prefer the batch one. The
  per-image endpoint remains only for the verifier (operator confidence
  check) and as a fail-safe signature surface for older sidecars.

## Related runbook

The single-image companion `/embed_visual_from_image` is documented
implicitly through the per-image verifier assertions. There is no
dedicated single-image runbook — the batch endpoint is canonical for
all production callers.
