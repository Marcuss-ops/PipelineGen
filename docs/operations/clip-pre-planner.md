# Clip Pre-Planner Pipeline Runbook

This runbook is the canonical operational reference for the **Clip Pre-Planner
Pipeline** — the 7-stage asset-assembly chain that turns a user request into a
binding-resolved scene before downstream rendering.

| Field | Value |
|---|---|
| Pipeline scope | Clip pre-planning (asset selection + binding + redaction) |
| Related conduct | `internal/application/scripts/usecase/generate_one_usecase.go` |
| Companion runtime docs | `docs/operations/capacity-sweep.md`, `docs/operations/youtube-live-testing-runbook.md` |
| Godlike/05 SSOT pointer | `internal/application/scripts/usecase` |
| Godlike/06 SSOT pointer | one owner per stage (cited below) |
| Godlike/07 NO-FAKE-AVAILABILITY | every stage failures closed; no silent no-op |

## 7-Stage Pipeline Overview

```
[Input]
   │
   ▼
[1. Planner]      stockpipeline/planner.go              (deterministicPlanner)
   │
   ▼
[2. Search]       scripts/usecase/source_resolver_search.go
   │              + assets/search/ports.go (SearchResult port)
   │
   ▼
[3. Sampler]      scripts/usecase/clip_sampler_impl.go
   │              + providers/stock/fingerprint.go (Sampler)
   │
   ▼
[4. View Redaction]  clipview/types.go (CandidateView allow-list)
   │                  + scripts/usecase/output_sanitizer.go (artifact scrub)
   │
   ▼
[5. Generator]    domain/generation/generator.go (Generator interface)
   │              + ai/ollama/generate.go (Ollama-backed impl)
   │
   ▼
[6. Binding]      scripts/scene/binder.go (BindClipsFromManifest)
```

Each stage passes one typed envelope forward. The boundary contracts are
typed sentinels (no implicit success). A stage that fails MUST fail closed —
no fake-availability substitution (godlike/07).

---

## Stage 1 — Input

The pre-planner pipeline is triggered when an upstream caller hands the engine
a payload describing the desired clip asset assembly. The payload typically
carries topic, source text, target duration, and content slot hints. The
conductor entry-point reads this payload, validates required fields, then
threads the typed struct through all downstream stages.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/application/scripts/usecase/generate_one_usecase.go`, `internal/application/scripts/usecase/engine_prompt.go` |
| **Godlike contract** | godlike/07: payload validation is fail-closed — unknown fields, missing topic, or empty source text MUST abort the chain before stage 2. |
| **Inputs** | typed payload with `Topic`, `SourceText`, `SlotHints`, target duration |
| **Outputs** | hydrated request struct passed to stage 2 (`ClipPlanner.Plan`) |
| **Failure modes** | empty topic → abort; missing source text → abort; payload schema drift → typed sentinel + structured log |

---

## Stage 2 — Planner

The planner converts the validated request into a deterministic `ClipPlan`.
The canonical owner is the stock-pipeline planner. The deterministic
implementation derives stable permutation from a SHA-256 hash of
`(URL + index)` so successive retries yield the same plan order.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/capabilities/assets/providers/stock/stockpipeline/planner.go` (`ClipPlanner` interface, `ClipPlan`, `ClipSpec`, `deterministicPlanner`) |
| **Godlike contract** | godlike/06 SSOT: there is exactly one canonical planner. Any new planner MUST re-route via the `ClipPlanner` interface — no inlined plan-builders. godlike/07: deterministic hashing replaces any RNG-based permutation; retries MUST be byte-identical. |
| **Inputs** | stage 1 typed request |
| **Outputs** | `ClipPlan` (slot list, ordered indices, source-anchor data) |
| **Failure modes** | planner hash collision → typed sentinel (extremely rare); planner nil wired → runtime error (no silent fallback) |

---

## Stage 3 — Search

The search step expands each `ClipPlan` slot into a set of candidate clips by
querying the search backend. The search port emits a `SearchResult`
typed envelope carrying candidates, scores, and source hints.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/application/scripts/usecase/source_resolver_search.go` (orchestration), `internal/application/assets/search/ports.go` (`SearchResult` port), `internal/domain/asset/search_core.go` (`SearchResult` type), `internal/capabilities/scripts/dto/curation_types.go` (`SearchResultInfo`) |
| **Godlike contract** | godlike/06 SSOT: `assets/search/ports.go` is the only canonical search port; alternative search wrappers MUST route through it. godlike/07: empty result set is a typed failure, not a silent pass. |
| **Inputs** | `ClipPlan` slots |
| **Outputs** | `SearchResult` with `[]Candidate` + scores |
| **Failure modes** | search backend returns 5xx → typed sentinel + retry budget; empty results → typed sentinel (no fake fallback); qdrant unavailable → typed sentinel (no in-memory shadow) |

---

## Stage 4 — Sampler

The sampler reads the candidate pool for each slot and selects a subset for
downstream assembly. Deterministic sampling is a godlike/06 invariant — the
canonical owner is `Sampler` in the provider package, instantiated with a
seed-hash so reproducible sample sets can be re-derived.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/application/scripts/usecase/clip_sampler_impl.go` (orchestration), `internal/capabilities/assets/providers/stock/fingerprint.go` (`Sampler` struct + `NewSampler(seedHex string)`) |
| **Godlike contract** | godlike/06 SSOT: the canonical Sampler lives in the provider package (not in any application script). Seeding MUST be deterministic and content-addressed (SHA-256 over `(plan, slot, source_text)`). godlike/07: under-sampling (candidates < limit) is a typed failure, not a downgrade to a smaller set. |
| **Inputs** | `SearchResult` per slot |
| **Outputs** | `[]Sample` per slot with provenance back to the candidates |
| **Failure modes** | empty candidate pool → typed sentinel; sampler seed collision → typed sentinel |

---

## Stage 5 — View Redaction

View Redaction is the stage that **strips pipeline-internal taxonomy from the
projection seen by downstream stages** (notably the Generator). Two packages
compose the gate:

1. **`clipview.CandidateView`** is the SOLE owner of the model-facing
   projection. It is a STRICT allow-list: asset identifier fields like
   `AssetRef` and `SlotRef` are excluded by construction. Any candidate that
   would leak private taxonomy MUST be rejected at this boundary.
2. **`output_sanitizer.go`** (under `scripts/usecase`) is the canonical
   scrubber of non-prose artifacts in generated text, providing an idempotent
   pass that strips cache-replay residue and other system-only tokens.

There is NO central redaction package; the two files compose the redaction
layer. Future consolidation is an open follow-up.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/application/clipview/types.go` (`CandidateView`), `internal/application/scripts/usecase/output_sanitizer.go` |
| **Godlike contract** | godlike/06 SSOT: `clipview.CandidateView` is the SOLE owner of the model-facing projection; nowhere else in the codebase may emit a candidate-shaped struct that includes `AssetRef` / `SlotRef` / `DriveLink`. godlike/07: any unredacted projection reaching the Generator is a fail-closed event. |
| **Inputs** | candidate projections from stage 4 + raw model text from stage 6 |
| **Outputs** | cleaned candidate projections (no asset_id, no drive_link) + scrubbed text |
| **Failure modes** | a candidate with `AssetRef` set → typed sentinel (clipview enforces); text containing schema_version / scenes / specscene keys → typed sentinel (output_sanitizer enforces) |

---

## Stage 6 — Generator

The Generator turns the redacted projection into prose/narrative destined for
binding. The Generator is a typed port (interface in the domain package) with
at least one concrete implementation (Ollama-backed) and a smaller mock for
unit testing.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/domain/generation/generator.go` (`Generator` interface), `internal/platform/ollama/generate.go` (`Generator` Ollama impl), `internal/application/scripts/usecase/generate_one_usecase.go` (orchestrator invocation) |
| **Godlike contract** | godlike/06 SSOT: there is one Generator contract (`domain/generation/generator.go`); alternative generation backends MUST implement it. godlike/07: every Generator call MUST return a typed result; raw text without a typed envelope is reject. |
| **Inputs** | redacted candidate projections from stage 5 |
| **Outputs** | generated typed envelope (per the Generator contract) |
| **Failure modes** | provider timeout → typed sentinel; malformed typed envelope → typed sentinel; non-prose artifacts → output_sanitizer re-pass |

---

## Stage 7 — Binding

The Binder resolves the generated envelope into a binding manifest, attaching
clip identifiers and segment timing to the slots authored by stage 2.

| Field | Value |
|---|---|
| **Primary code paths** | `internal/capabilities/scripts/scene/binder.go` (`BindClipsFromManifest`) |
| **Godlike contract** | godlike/06 SSOT: `BindClipsFromManifest` is the canonical binding pipeline; alternative binders MUST route through the same scene package. godlike/07: an unresolved ref MUST fail closed with a typed sentinel — never a partial binding. |
| **Inputs** | generated envelope from stage 6 + `ClipPlan` from stage 2 |
| **Outputs** | typed binding manifest (per scene package contract) |
| **Failure modes** | ref not in plan → typed sentinel; ref resolves to a binding with a non-existent clip → typed sentinel |

---

## Cross-Stage Operational Notes

### Determinism Guarantees

Stages 2 and 4 are deterministic via `ClipPlanner` plan-hash + `Sampler` seed
hash. Retries that re-thread the same payload MUST produce the same plan +
sample. Pass this property as a regression test: hash the per-slot binding
manifest across retries; expect byte-equal.

### Failure-Closed Discipline

Every stage MUST emit a typed sentinel on failure. The set of sentinel error
types is exported from each package — refer to the package-level error
declarations. Operators inspecting logs MUST find `err.Error()` strings
prefixed by the sentinel error's text (godlike/07).

### View-Redaction Coverage

When auditing candidate emissions across the codebase, search for
`AssetRef`, `SlotRef`, and `DriveLink` as exported field names. Any match
inside a model-facing projection is a godlike/08 forward-prevention violation
and MUST be reported under the `root_override_ban` family of archcheck rules.

---

## Operational Triage

When the pre-planner pipeline fails:

1. Identify the failing stage from the typed sentinel prefix.
2. Verify the godlike/07 contract for that stage fails closed (no substitute
   output).
3. Inspect logs for the structured sentinel line; the canonical pre-planner
   emits `slog` lines whose `msg` field starts with the stage name.
4. Reference the table above for the stage's primary code path; trace the
   sentinel to the typed failure boundary.

## Related Operational Docs

- `docs/operations/stock-e2e-runbook.md` — end-to-end plan verification,
  including pre-planner validation.
- `docs/operations/inspect-media-asset.md` — adjudicate individual asset
  metadata when stage 3 or stage 4 sentinel rates spike.
- `docs/operations/capacity-sweep.md` — capacity-load diagnostics for
  stages 3 (Search) and 6 (Generator).
- `docs/operations/worker-certification-checklist.md` — pre-planner is part of
  the certified pipeline; production rollout must pass the checklist.

## Conflict Resolution

If a generator in this runbook conflicts with the executable code, the
executable source wins per `CANONICAL.md` Conflict rule. Correct or delete
the stale prose immediately.
