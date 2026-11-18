# Entity image pipeline: one live flow, explicit hit → bindable contract

**Status:** canonical for the live Go path (2026-11-14).
**Owner:** `internal/capabilities/scripts/adapters` + `internal/capabilities/images/entitycatalog`.

## One live flow

PipelineGen has exactly one live entity-image flow:

```text
provider search
  → discovery candidate (URL, provider, query, rank)
  → acquisition (download bytes)
  → content identity (content_sha256)
  → visual verdict (accepted/rejected + verifier_version)
  → selection (only current, accepted, usable rows)
  → materialization (durable Drive/content-addressed asset)
  → binding (annotation/overlay carries the verified asset)
```

The durable catalog is the replay projection between discovery and selection.
It is not a second planner and it does not own pixels.

The reviewed local asset registry (`VELOX_ENTITY_ASSET_REGISTRY`) follows the
same ownership rule: `internal/capabilities/images/entitycatalog` loads and
re-hashes it in `verified_asset_registry.go`, and the overlay presentation
layer only binds the verified entries to cards. A malformed or unsupported
registry is an error, not a silent empty catalog.

`BrainRust/imagecatalog` was **retired on 2026-11-18** (TICKET-28): it was an
offline experiment with no Go consumer and a second catalog-policy
implementation. The live path above is the only flow that may claim to produce
a bound image; the shared policy fixture now lives in
`internal/capabilities/images/entitycatalog/testdata/catalog_policy_contract_rust.json`
and the Go contract test is the single policy gate.

## State contract

| State | Owner | Meaning | May bind? |
|---|---|---|---|
| `found` | provider adapter | A search returned a URL for a query. No bytes, no verdict. | No |
| `acquired` | materializer | Bytes were downloaded and hashed. | No |
| `verified` | visual verifier | The bytes depict the requested identity under `verifier_version`. | Only after selection |
| `selected` | selector | This candidate won a slot for a scene/entity. | No |
| `materialized` | finalizer | Durable asset exists with `asset_id`, `content_sha256`, Drive link. | No |
| `bound` | overlay planner | The plan carries the verified, materialized asset for the entity. | Yes |

A catalog row stores:

- `provider`: the concrete retriever (`duckduckgo`, `searxng`,
  `wikimedia_commons`) when known; `internet_images` only when no concrete
  backend was recorded;
- `content_sha256`: the digest of the bytes the URL resolved to, empty until
  acquisition;
- `verifier_version`: the policy that produced `semantic_status`. Empty means
  metadata-only discovery, never a reusable verdict. A changed
  `content_sha256` invalidates the old verdict.

## Report counters

`result.overlay_audit.entity_images.pipeline` exposes:

- `retrieved` — selected image promises with a discovery candidate;
- `verified` — promises with a usable visual verdict;
- `selected` — promises whose plan carries a planned image;
- `materialized` — promises with a durable Drive/content-addressed asset;
- `bound` — promises both planned and verified;
- `skipped` — promises with no planned image.

The per-candidate detail (provider, status, hash, verifier version) remains in
`segments[].assets`. These counters are the summary; they are not a substitute
for the candidate rows.

## Failure rules

1. A search hit is never reported as a bound image.
2. A verdict from another `verifier_version` is not reusable.
3. A changed content hash invalidates the old verdict.
4. A catalog lookup never downloads, verifies, or binds; it only replays
   durable state.
5. A plan may degrade to a text card, but it must report the reason
   (`no_verified_asset`, `verified_asset_not_bound`, `unverified_asset_bound`,
   …).

## Reliability fixes (2026-11-18)

The live Go path was tightened without changing the provenance policy:

- `AIBundle.VisionVerdictScorer` is the one scorer instance shared by the
  postprocess and generation wiring; the two sites no longer construct
  independent scorers.
- The scorer calls `ChatOnce` (one model attempt) and owns the per-identity
  attempt budget. `Chat` keeps the documented transient retry policy; the
  vision lane does not inherit it.
- The verified bytes are decoded once and reused for both the collage heuristic
  and the VLM normalization.
- The startup warm step warms the dedicated `VisionVerdictClient`, so a
  separate vision endpoint does not pay a cold model load on the first image.
- The embedding sidecar uses per-family semaphores (text/visual/audio), so a
  SigLIP batch cannot starve text embeddings.

The provenance gate stays fail-closed: accepted rows without a current
visual-identity verifier are not served as verified images. Unknown `0x0`
dimensions are deferred to the materialization probe, not treated as a
permanent rejection. `BrainRust/imagecatalog` remains retired (TICKET-28); the
Rust Phase A plan is not revived because the crate has no live consumer.
