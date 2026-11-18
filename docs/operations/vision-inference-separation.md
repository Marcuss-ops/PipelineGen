# Vision inference separation — entity-image verdicts

**Status:** implemented 2026-11-18. Applies to the entity-image VLM verdict
(`OllamaPersonImageScorer`), not to SigLIP visual embeddings.

## Why

The entity-image verdict used `root.AI.ScriptGen.GetClient()`: the same Ollama
client, model, endpoint, and admission budget as script generation. On a host
with `OLLAMA_NUM_PARALLEL=3` and `OLLAMA_MAX_LOADED_MODELS=1`, a portrait
verification fan-out and a script-generation fan-out competed for the same
three runner slots. The image lane could starve generation and vice versa, and
a different vision model on the same endpoint forced a resident-model reload.

## What changed

| Surface | Before | After |
|---|---|---|
| Client | `root.AI.ScriptGen.GetClient()` | `root.AI.VisionVerdictClient` when configured |
| Config | none | `external.ollama_vision_url` / `OLLAMA_VISION_ADDR` |
| Model | `OLLAMA_MODEL` | `external.ollama_vision_model` / `OLLAMA_VISION_MODEL` (optional; falls back to `OLLAMA_MODEL`) |
| Admission | shared with script generation | separate per endpoint (the limiter is keyed by URL) |
| Fallback | — | unset endpoint = legacy script client, logged at boot |

The composition root builds the client once (`newVisionVerdictClient`) and
stores it in `AIBundle.VisionVerdictClient`. `scriptVisualCandidateScorer`
prefers that field and falls back to the script client only for partial wiring
and legacy configurations.

## Configuration

```yaml
external:
  ollama_url: "http://127.0.0.1:11434"
  ollama_model: "gemma4:e2b"
  ollama_vision_url: "http://127.0.0.1:11435"
  ollama_vision_model: "gemma4:e2b"   # optional; omit to reuse ollama_model
```

Equivalent environment:

```bash
OLLAMA_VISION_ADDR=http://127.0.0.1:11435
OLLAMA_VISION_MODEL=gemma4:e2b
```

Validation rules:

- `ollama_vision_model` without `ollama_vision_url` is rejected: a different
  model on the shared endpoint would force a resident reload.
- `ollama_vision_url` must be an absolute `http` or `https` URL.
- If the vision URL equals `ollama_url`, boot logs a warning: the endpoint is
  distinct in configuration but the admission budget is still shared.

## Deployment: second Ollama instance

Run the vision model in its own process so it has its own runner slots,
resident model, and failure domain:

```ini
# /etc/systemd/system/ollama-vision.service.d/override.conf
[Service]
Environment="OLLAMA_HOST=127.0.0.1:11435"
Environment="OLLAMA_MAX_LOADED_MODELS=1"
Environment="OLLAMA_NUM_PARALLEL=2"
```

```bash
sudo systemctl daemon-reload
sudo systemctl restart ollama-vision
ollama pull gemma4:e2b   # or the chosen multimodal verdict model
curl -fsS http://127.0.0.1:11435/api/tags >/dev/null
```

Point `ollama_vision_url` at that instance. If the host has one GPU, give the
two Ollama services explicit, non-overlapping resource limits; otherwise they
still contend for VRAM even with separate admission budgets. On a CPU-only
host, keep the vision lane on a separate port/process and accept lower
parallelism.

## Verification

Boot logs must show one of:

- `vision verdict client configured` with the dedicated URL and model;
- `vision verdicts share the script generation Ollama client` when the lane is
  not configured;
- `OLLAMA_VISION_ADDR equals OLLAMA_ADDR` when the URL is not actually
  distinct.

Focused tests:

```bash
cd refactored
go test -count=1 ./internal/platform/config ./internal/platform/ollama/client ./internal/platform/ollama/adapters ./internal/app/wiring
```

They cover defaults, env bindings, validation, dedicated-client construction,
model fallback, the AI-bundle precedence/fallback, single-attempt vision
calls, and the one-decode image path.

## Not in this change

- **Batching:** the verdict is still one `Chat` per candidate. A batch port and
  dynamic batching in the vision service are the next step.
- **Content-addressed cache:** verdicts are not yet keyed by
  `sha256 + model_version + prompt_version`; repeated images still hit the VLM.
- **SigLIP prefilter:** SigLIP remains in the separate Python embedding sidecar
  (`127.0.0.1:8001`); it is already a separate process and is not part of this
  change.

## Reliability follow-up (2026-11-18)

The live Go path now keeps the vision lane isolated without reviving the
retired Rust crate:

- `AIBundle.VisionVerdictScorer` is the ONE scorer instance used by both the
  postprocess and generation wiring.
- The scorer calls `ChatOnce`, which makes one model attempt; the per-identity
  budget stays in the scorer. `Chat` keeps the documented transient retry
  policy (`retry.IsTransient`) for callers that do not own a budget.
- The verified bytes are decoded once for both the collage heuristic and the
  VLM normalization.
- The startup warm step also warms `VisionVerdictClient` when it points at a
  distinct endpoint, so the first image does not pay a cold model load.
- The embedding sidecar has per-family semaphores: text (default 2), visual
  (default 1), audio (default 1), overridable with
  `PIPELINEGEN_TEXT_INFERENCE_CONCURRENCY`,
  `PIPELINEGEN_VISUAL_INFERENCE_CONCURRENCY`, and
  `PIPELINEGEN_AUDIO_INFERENCE_CONCURRENCY`.

`BrainRust/imagecatalog` remains retired (TICKET-28). The provenance gate
(`IsUsableCandidateQuality`) remains fail-closed: accepted rows without a
current visual-identity verifier are not served as verified images. Unknown
`0x0` dimensions are deferred to the materialization probe, not treated as a
permanent rejection.
