# `POST /api/script/generate` — job payload schema

Operator reference for the request body of the async script-generation
endpoint. The **Go structs are the single source of truth**; this page
documents the JSON nesting that callers get wrong, so a rejected attempt
is fixed on the first try instead of costing a 400 round-trip.

## Source of truth (godlike/06 SSOT)

| Contract | Owner |
|---|---|
| Top-level envelope + item | `internal/kernel/script/generation_envelope.go` (`GenerationEnvelopeV2`, `GenerationItemV2`) |
| `source` | `internal/kernel/script/source_spec.go` (`SourceSpec`) |
| `script_params` | `internal/kernel/script/script_spec.go` (`ScriptSpec`) |
| `output` | `internal/kernel/script/output_spec.go` (`OutputSpec`) |
| Result envelope (`items`) | `internal/kernel/script/generation_result.go` (`GenerationEnvelopeResult`, `GenerationEnvelopeItem`) |
| Request binding + validation | `internal/capabilities/script/handler_generate_request.go` (`bindGenerateEnvelope`), `internal/kernel/script/generation_validation.go` (`GenerationEnvelopeV2.Validate`), `internal/capabilities/scripts/usecase/gencore/payload_validator.go` (`PayloadValidator`) |

Any field rename or move MUST update this page in lockstep.

## Nesting

The body is an **envelope** with an `items` array. Every generation option
lives **inside an item**; there are no top-level shortcut keys.

```json
{
  "version": 2,
  "preset": "custom",
  "correlation_id": "optional-trace-id",
  "force_refresh": false,
  "items": [
    {
      "id": "optional-item-id",
      "title": "My Title",
      "language": "en",
      "source":       { "type": "text", "source_text": "..." },
      "script_params": { "target_words": 1200 },
      "output":       { "...": "opt-in post-generation artifacts" },
      "docs":         { "enabled": true, "languages": ["en", "it"] },
      "audio":        { "...": "audio.mode + editorial audio intent" },
      "media_plan":   { "...": "visual media selection" }
    }
  ]
}
```

- `version` is always `2`. `items` must contain **at least one** entry.
- A single-item envelope maps to the unified `/generate` flow; multiple
  items map to batch generation.
- Every per-item block (`source`, `script_params`, `output`, `docs`,
  `audio`, `media_plan`, `overlay_background`, `overlay_style`,
  `video_metadata`, `intro`, `outro`) is optional unless its own
  validator says otherwise (`source` is required).

## Fail-closed contract (why a wrong body returns 400)

`bindGenerateEnvelope` decodes with **`DisallowUnknownFields`** and
additionally rejects removed contract keys against the raw body before
decoding. Therefore a misplaced or retired key is a hard `400`, never a
silently ignored field:

- malformed JSON / wrong top-level shape → `400 {"ok":false,"error":"invalid payload: <gin error>"}`;
- unknown field (e.g. `script_params` at the envelope root, or a removed
  key such as `assemble_final`) → `400 {"ok":false,"error":"invalid payload: json: unknown field ..."}`;
- typed validation failure (limits, empty segments, oversized
  `source_text`, …) → `400 {"ok":false,"error":{code,message,stage,retryable,extra}}`.
- `GenerationEnvelopeV2.Validate()` runs the structural checks; the
  config-aware `PayloadValidator` runs second (structural → semantic →
  config-aware order is intentional).

## The two mistakes that produce the expensive 400s

1. **`script_params` at the wrong level or in the wrong case.** It is a
   key **inside each object of `items[]`** — not at the envelope root,
   and not `scriptParams`. The Go field is `ScriptParams` with json tag
   `script_params`.
2. **`docs` vs `output` at the wrong level.** Post-generation artifact
   selection is **per item**: `docs` is its own item key (document
   publication) and `output` is the item's opt-in artifact block. The
   **result** envelope is where `items` reappears
   (`GenerationEnvelopeResult.items`); there is no top-level `output`
   key in the request or the result.

## Related

- `docs/operations/network-exposure.md` §3 — the `curl` invocation for
  `POST /api/script/generate` and the `202 Accepted` + polling contract.
- `docs/operations/job-debug-runbook.md` — diagnosing the resulting job
  row when generation fails after acceptance.
- `docs/api/ACTIVE_API_GENERATED.md` — the generated route manifest.
