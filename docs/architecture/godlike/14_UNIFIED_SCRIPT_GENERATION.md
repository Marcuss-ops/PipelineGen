# Unified Script Generation

> **Status**: PROPOSED execution specification.
> **Owner**: Scripts capability.
> **Migration ID**: `SCRIPT-GEN-UNIFY-001`.
> **Scope**: script creation from text, explicit clips, catalog clips, automatic clip search, image-enabled requests and batch requests.
> **Authority**: this document defines the target architecture and completion gates for the migration. It does not describe the current runtime as already compliant. Until CUTOVER, the current code and `ARCHITECTURE.md` remain the operational truth.

## 1. Goal

PipelineGen must have one unambiguous script-generation path.

Text, clips, catalog, search, images, voiceover and batch are not separate generators. They are variations of one request:

- the **source** changes;
- the **requested outputs** change;
- the **number of items** changes;
- the generation engine, normalization, validation, cache policy, persistence rules and result contract do not change.

The final invariant is:

```text
GenerationEnvelopeV2
    -> Normalize
    -> Validate
    -> Resolve source through SourceResolverRegistry
    -> Build ResolvedGenerationPlan
    -> GenerateOneUseCase
    -> Engine.Generate
    -> PostProcessorRegistry
    -> GenerationResult
```

A batch is only a collection of items executed through the same `GenerateOneUseCase`.

## 2. Problem statement

The current scripts capability contains several objects and paths that compete for ownership:

- `internal/domain/script.GenerationSpec` is described as the canonical serialized request;
- `internal/domain/script.ScriptGenerationPlan` is also described as the unified plan for all generation entrypoints;
- `internal/application/scripts.WriteScriptRequest` duplicates fields from both;
- `GenerateBatchRequest` owns a separate batch contract;
- `JobPayloadCatalogScript` and `JobPayloadCurate` own additional generation contracts;
- clips, catalog and batch build `WriteScriptRequest` independently;
- the job system uses different script job types;
- synchronous and asynchronous batch execution return different result shapes;
- durable results are frequently represented as `map[string]any`;
- source fingerprints can be carried through the same field used for model instructions;
- output flags can be forced by endpoint-specific code instead of one preset resolver;
- some output capabilities are advertised before their runtime implementation performs real work.

This makes equivalent requests semantically different depending on the endpoint or worker handler that received them.

## 3. Non-goals

This migration does not:

- redesign Ollama, Google Drive, Qdrant or the job broker;
- introduce a GUI or browser workflow;
- change the media catalog ownership model;
- add new generation features while unifying existing ones;
- preserve old contracts indefinitely;
- create another orchestration layer beside the existing ones.

The work is consolidation, not feature expansion.

## 4. Target invariants

The following invariants are mandatory.

### 4.1 One durable command

There is exactly one serialized command accepted by HTTP, jobs and internal callers:

```go
type GenerationEnvelopeV2 struct {
    Version   int                `json:"version"`
    Items     []GenerationItemV2 `json:"items"`
    Aggregate *AggregateSpec     `json:"aggregate,omitempty"`
}
```

A single generation contains one item. A batch contains more than one item.

No other request struct may be persisted as a script-generation job payload.

### 4.2 One internal plan

There is exactly one internal, normalized, immutable execution plan:

```go
type ResolvedGenerationPlan struct {
    ItemID       string
    Source       ResolvedSource
    Script       ResolvedScriptSpec
    Outputs      ResolvedOutputSpec
    Cache        ResolvedCacheSpec
    Persistence  ResolvedPersistenceSpec
}
```

`ResolvedGenerationPlan` is not accepted from HTTP and is not stored as an external command. It is produced only by the canonical normalizer and plan builder.

### 4.3 One generation use case

There is exactly one service allowed to generate one script:

```go
type GenerateOneUseCase struct {
    normalizer     *GenerationNormalizer
    validator      *GenerationValidator
    sourceRegistry *SourceResolverRegistry
    planBuilder    *GenerationPlanBuilder
    engine         ScriptEngine
    processors     *PostProcessorRegistry
}

func (u *GenerateOneUseCase) Execute(
    ctx context.Context,
    item GenerationItemV2,
    progress ProgressReporter,
) (*GenerationResult, error)
```

Catalog, clips, text, search and batch code must not call `Engine` directly.

### 4.4 One job type

The final canonical job type is:

```go
const TypeScriptGenerate = "script.generate"
```

The job payload is always `GenerationEnvelopeV2`.

The final repository contains no active script-generation handlers for:

- `script.generate_from_clips`;
- `script.generate_batch`;
- `script.generate_from_catalog`.

Temporary coexistence is permitted only during EXPAND and CUTOVER, with a complete deprecation record required by `07_ZERO_LEGACY_POLICY.md`.

### 4.5 One result contract

Every generated item returns the same typed result:

```go
type GenerationResult struct {
    ItemID      string             `json:"item_id"`
    ScriptID    int64              `json:"script_id,omitempty"`
    Title       string             `json:"title"`
    Script      string             `json:"script"`
    WordCount   int                `json:"word_count"`
    Language    string             `json:"language"`
    Model       string             `json:"model,omitempty"`
    Source      SourceTrace        `json:"source"`
    Cache       CacheResult        `json:"cache"`
    Artifacts   ArtifactResult     `json:"artifacts"`
    Timings     GenerationTimings  `json:"timings"`
}

type GenerationEnvelopeResult struct {
    Items     []GenerationResult `json:"items"`
    Aggregate *AggregateResult   `json:"aggregate,omitempty"`
}
```

No durable generation command or result uses `map[string]any`.

## 5. Canonical request model

### 5.1 Generation item

```go
type GenerationItemV2 struct {
    ID      string       `json:"id,omitempty"`
    Preset  Preset       `json:"preset,omitempty"`
    Source  SourceSpec   `json:"source"`
    Script  ScriptSpec   `json:"script"`
    Outputs OutputSpec   `json:"outputs"`
}
```

### 5.2 Source union

```go
type SourceKind string

const (
    SourceText    SourceKind = "text"
    SourceClips   SourceKind = "clips"
    SourceCatalog SourceKind = "catalog"
    SourceSearch  SourceKind = "search"
)

type SourceSpec struct {
    Kind    SourceKind         `json:"kind"`
    Text    *TextSourceSpec    `json:"text,omitempty"`
    Clips   *ClipsSourceSpec   `json:"clips,omitempty"`
    Catalog *CatalogSourceSpec `json:"catalog,omitempty"`
    Search  *SearchSourceSpec  `json:"search,omitempty"`
}
```

Exactly one source payload must match `SourceSpec.Kind`.

Examples:

```json
{
  "kind": "text",
  "text": {
    "source_text": "..."
  }
}
```

```json
{
  "kind": "clips",
  "clips": {
    "clip_ids": ["clip-1", "clip-2"],
    "transcript_policy": "required",
    "ordering_strategy": "narrative"
  }
}
```

```json
{
  "kind": "catalog",
  "catalog": {
    "query": "topic",
    "max_clips": 10,
    "min_coverage": 0.7
  }
}
```

```json
{
  "kind": "search",
  "search": {
    "query": "topic",
    "max_clips": 10,
    "min_score": 0.5,
    "allow_text_only": false
  }
}
```

### 5.3 Script specification

```go
type ScriptSpec struct {
    Topic               string   `json:"topic,omitempty"`
    Title               string   `json:"title,omitempty"`
    Language            string   `json:"language,omitempty"`
    Languages           []string `json:"languages,omitempty"`
    Tone                string   `json:"tone,omitempty"`
    Style               string   `json:"style,omitempty"`
    Model               string   `json:"model,omitempty"`
    TargetWords         int      `json:"target_words,omitempty"`
    DurationSeconds     int      `json:"duration_seconds,omitempty"`
    Guidelines          string   `json:"guidelines,omitempty"`
    PromptProfile       string   `json:"prompt_profile,omitempty"`
    PromptVersion       string   `json:"prompt_version,omitempty"`
    EditorPromptVersion string   `json:"editor_prompt_version,omitempty"`
    QAPromptVersion     string   `json:"qa_prompt_version,omitempty"`
    ForceRefresh        bool     `json:"force_refresh,omitempty"`
}
```

### 5.4 Output specification

```go
type OutputSpec struct {
    Document  DocumentOutputSpec  `json:"document"`
    Images    ImageOutputSpec     `json:"images"`
    Voiceover VoiceoverOutputSpec `json:"voiceover"`
    Entities  EntityOutputSpec    `json:"entities"`
    Metadata  MetadataOutputSpec  `json:"metadata"`
    Persist   PersistOutputSpec   `json:"persist"`
}
```

Output capabilities are independent. Enabling images must not silently enable voiceover, disable metadata or change entity extraction.

## 6. Preset policy

A preset is a convenience for producing an explicit canonical request. It is not a separate execution path.

Preset resolution happens once, before normalization and validation:

```text
caller values
    > explicit preset values
    > configuration defaults
    > hard safety defaults
```

The resolver returns a complete `GenerationItemV2`. Downstream code reads only resolved fields and must not branch on endpoint name.

Required preset semantics:

| Preset | Source effect | Output effect |
|---|---|---|
| `custom` | none | none |
| `with_images` | none | `images.enabled=true` only |
| `full_media` | none | images and voiceover enabled explicitly |
| `catalog` | `source.kind=catalog` | none |
| `search` | `source.kind=search` | none |

A preset must never alter unrelated fields silently.

## 7. SourceResolverRegistry

Source acquisition is owned by one registry:

```go
type SourceResolver interface {
    Kind() SourceKind
    Resolve(
        ctx context.Context,
        item GenerationItemV2,
        progress ProgressReporter,
    ) (*ResolvedSource, error)
}
```

Required registrations:

- `TextSourceResolver`;
- `ExplicitClipsSourceResolver`;
- `CatalogSourceResolver`;
- `SearchSourceResolver`.

Registry rules:

- duplicate `SourceKind` registration fails composition;
- lookup after freeze is read-only;
- missing resolver fails before generation;
- resolvers do not call the script engine;
- resolvers do not create documents, images or voiceovers;
- resolvers return evidence, source text, selected clip IDs and a technical fingerprint;
- all source-specific defaults are normalized before resolver execution.

The output is:

```go
type ResolvedSource struct {
    Kind        SourceKind
    Text        string
    ClipIDs     []string
    Evidence    []ClipEvidence
    Fingerprint string
    Trace       SourceTrace
}
```

After source resolution, the generator must not care whether clip IDs came from the request, catalog or semantic search.

## 8. Prompt, cache and fingerprint separation

The following concepts are distinct and must have separate fields:

- `PromptProfile`: selects the prompt family;
- `PromptVersion`: selects the exact prompt template version;
- `Guidelines`: caller writing instructions;
- `SourceFingerprint`: technical identity of resolved evidence;
- `CacheKey`: deterministic cache identity;
- `RenderedPrompt`: final prompt sent to the model, retained only for diagnostics where allowed.

Mandatory rule:

> `SourceFingerprint` and `CacheKey` must never be injected into system or user messages sent to the LLM.

The cache key is derived from normalized semantic inputs, including:

- source fingerprint;
- title/topic;
- language;
- tone/style;
- target words;
- model;
- prompt profile and versions;
- relevant output-independent generation options.

Artifact flags such as document creation must not invalidate the script text cache unless they alter model output.

## 9. Engine contract

The engine accepts only the resolved plan:

```go
type ScriptEngine interface {
    Generate(
        ctx context.Context,
        plan ResolvedGenerationPlan,
    ) (*EngineResult, error)
}
```

The engine owns:

- prompt rendering;
- model invocation;
- script text generation;
- word count and model metadata;
- cache lookup and cache write through typed ports.

The engine does not own:

- endpoint presets;
- source lookup;
- catalog search;
- document creation;
- image generation;
- voiceover generation;
- result JSON shaping;
- batch iteration.

`WriteScriptRequest` is removed during CONTRACT. No caller constructs an engine request independently.

## 10. PostProcessorRegistry

Artifacts are created through one registry after successful script generation:

```go
type PostProcessor interface {
    Kind() ArtifactKind
    Process(
        ctx context.Context,
        plan ResolvedGenerationPlan,
        generated EngineResult,
        progress ProgressReporter,
    ) (Artifact, error)
}
```

Required processors:

- `DocumentProcessor`;
- `ImageProcessor`;
- `VoiceoverProcessor`;
- `EntityProcessor`;
- `MetadataProcessor`;
- `PersistenceProcessor`.

Rules:

- a processor runs only when its output is enabled;
- duplicate artifact registrations fail composition;
- missing mandatory processor fails before expensive generation when possible;
- a route advertising an output is absent or returns a typed unavailable error if the processor is not wired;
- processors do real work; placeholders returning empty success are forbidden;
- processor results are written to `ArtifactResult`, never to an ad hoc map.

## 11. Batch model

Batch is aggregation, not a second generator.

```go
type GenerateManyUseCase struct {
    one *GenerateOneUseCase
}

func (u *GenerateManyUseCase) Execute(
    ctx context.Context,
    envelope GenerationEnvelopeV2,
    progress ProgressReporter,
) (*GenerationEnvelopeResult, error)
```

For every item, `GenerateManyUseCase` calls `GenerateOneUseCase.Execute`.

It may own:

- bounded concurrency;
- item-level progress aggregation;
- fail-fast or continue-on-error policy declared in `AggregateSpec`;
- final aggregate document creation;
- deterministic result ordering.

It must not own:

- prompt construction;
- source resolution;
- direct Engine calls;
- per-item defaulting;
- a separate cache policy;
- a different result type.

The same item executed alone and inside a batch must produce the same normalized plan, cache key, engine request and per-item result, assuming the same external state and deterministic model settings.

## 12. HTTP surface

The final canonical endpoint is:

```text
POST /api/script/generate
```

It accepts `GenerationEnvelopeV2`, enqueues `script.generate` and returns:

```json
{
  "ok": true,
  "job_id": "...",
  "status": "QUEUED",
  "status_url": "/api/jobs/.../full"
}
```

Generation HTTP is asynchronous. Direct synchronous execution is an application-layer/test concern, not a second public generation contract.

Current generation routes may coexist temporarily only through tracked deprecations:

- `/api/script/generate-from-clips`;
- `/api/script/generate-with-images`;
- `/api/script/generate-batch`;
- `/api/script/generate-from-catalog`;
- script-producing behavior hidden behind `/api/script/curate`.

Each temporary route must translate into `GenerationEnvelopeV2` and enqueue `script.generate`. It must not call an old use case or old job handler. All temporary routes are removed at CONTRACT unless a product decision explicitly keeps a route as a first-class non-duplicate API.

## 13. Error contract

All generation paths use typed errors grouped into these classes:

- invalid command: HTTP 400, permanent job failure;
- unsupported source or output: HTTP 422 or 503 depending on configuration;
- conflict/idempotency collision: HTTP 409;
- dependency unavailable: HTTP 503, retry classification based on dependency type;
- generation failure: retryable only when the underlying cause is retryable;
- cancelled/deadline exceeded: propagated without remapping to generic internal error.

The same error must receive the same classification whether the item is generated alone or in a batch.

## 14. Migration plan

The migration follows `EXPAND / BACKFILL / CUTOVER / CONTRACT` from `07_ZERO_LEGACY_POLICY.md`.

### Phase A - Inventory and behavior lock

Tasks:

1. Inventory every script-producing route, job type, handler, request type, result type and direct `Engine.WriteScript` call.
2. Add characterization tests for current requests that must remain supported during migration.
3. Record temporary routes, job types and payload adapters in `architecture/deprecations.yaml`.
4. Add a repository check that prevents new direct Engine callers outside the canonical use case.
5. Resolve current compile/schema drift, including fields used by services but absent from the durable request contract.

Exit gate:

- complete inventory committed;
- every compatibility path has owner, replacement, issue, deadline, test and metric where applicable;
- no unknown script-producing entrypoint remains.

### Phase B - EXPAND: introduce V2 contracts

Tasks:

1. Add `GenerationEnvelopeV2`, `GenerationItemV2`, source union, output spec and typed result.
2. Add the normalizer, validator and preset resolver.
3. Add versioned decoding for V2 jobs.
4. Keep existing runtime behavior unchanged through explicit adapters only.
5. Add valid/invalid fixtures for every source and output combination.

Exit gate:

- V2 round-trip tests pass;
- invalid source unions are rejected;
- defaults are applied in one place;
- no endpoint-specific default is added during this phase.

### Phase C - EXPAND: canonical source and generation path

Tasks:

1. Implement and freeze `SourceResolverRegistry`.
2. Implement `GenerateOneUseCase`.
3. Change the engine to accept `ResolvedGenerationPlan`.
4. Separate prompt instructions, source fingerprint and cache key.
5. Migrate text and explicit-clips generation first.
6. Migrate catalog and search resolution into registry adapters.

Exit gate:

- all source kinds reach the same `GenerateOneUseCase`;
- only the canonical use case calls `Engine.Generate`;
- identical resolved sources produce identical plans regardless of entrypoint;
- fingerprints never appear in rendered LLM messages.

### Phase D - EXPAND: canonical artifact processors

Tasks:

1. Implement and freeze `PostProcessorRegistry`.
2. Move documents, images, voiceover, entities, metadata and persistence behind typed processors.
3. Fail fast when a requested output is unavailable.
4. Remove unconditional document creation.
5. Remove output-specific branching from HTTP and source resolvers.

Exit gate:

- every enabled output performs real work;
- disabled outputs produce no side effects;
- `with_images` differs from custom generation only by resolved image output options;
- artifact results use one typed schema.

### Phase E - BACKFILL: batch and job migration

Tasks:

1. Implement `GenerateManyUseCase` as repeated calls to `GenerateOneUseCase`.
2. Replace batch direct Engine calls.
3. Introduce `job.TypeScriptGenerate`.
4. Enqueue all new requests as `GenerationEnvelopeV2`.
5. Decide how already-queued legacy jobs are drained, transformed or explicitly failed before cutover.
6. Preserve correlation, idempotency and progress semantics in the new handler.

Exit gate:

- standalone and batch execution of the same item have plan/result parity;
- one worker handler owns new script generation;
- queue backfill/drain evidence is recorded;
- no new legacy job payload is written.

### Phase F - CUTOVER

Tasks:

1. Register `POST /api/script/generate` as the canonical route.
2. Switch every internal producer to `GenerationEnvelopeV2`.
3. Switch every active route adapter to `script.generate`.
4. Switch result consumers to `GenerationEnvelopeResult`.
5. Regenerate API and architecture manifests.
6. Observe compatibility usage metrics for the required zero-use window.

Exit gate:

- all production traffic uses the canonical job and worker;
- old job handlers have zero invocations;
- old payload writers have zero invocations;
- generated docs and route/job manifests match the runtime.

### Phase G - CONTRACT

Tasks:

1. Remove old script job constants and handlers.
2. Remove `GenerateBatchRequest`, `JobPayloadCatalogScript`, `JobPayloadCurate` and obsolete payload codecs.
3. Remove `WriteScriptRequest` and direct Engine construction paths.
4. Remove legacy route adapters after their deprecation deadline and zero-use window.
5. Remove obsolete config fields, metrics, tests, comments, aliases and allowlists.
6. Update `ARCHITECTURE.md`, `AGENTS.md`, generated API docs, ownership and current migration tracker to describe only the final system.

Exit gate:

- repository search returns zero active references to superseded contracts and job types;
- no compatibility path remains;
- architecture baselines are reduced to zero, not merely frozen.

## 15. Required tests

### 15.1 Contract tests

- V2 encode/decode round trip.
- Unsupported version rejected.
- Empty item list rejected.
- Source kind and payload mismatch rejected.
- Multiple source payloads rejected.
- Invalid output combinations rejected with typed errors.

### 15.2 Normalization tests

- precedence is caller > preset > config > hard default;
- normalization is idempotent;
- endpoint name is not an input to normalization;
- batch and single item normalize identically;
- `with_images` changes only image output fields;
- defaults are not applied in handlers, resolvers or processors.

### 15.3 Resolver registry tests

- duplicate source registration fails;
- mutation after freeze fails;
- missing source resolver fails before Engine invocation;
- explicit clips and catalog-selected equivalent clip IDs produce equivalent `ResolvedSource` semantics;
- search with zero clips respects `allow_text_only` and never silently falls back.

### 15.4 Engine tests

- only `ResolvedGenerationPlan` is accepted;
- source fingerprint and cache key never appear in model messages;
- prompt profile/version changes alter the cache key;
- document/images/voiceover flags do not alter text cache identity unless they affect model output;
- model errors preserve retry classification.

### 15.5 Processor registry tests

- duplicate processor registration fails;
- mutation after freeze fails;
- disabled processor is never called;
- requested but unavailable processor fails explicitly;
- no processor returns empty success for unavailable work;
- artifact serialization is stable.

### 15.6 Batch parity tests

For the same normalized item:

- standalone and one-item batch plans are deeply equal;
- cache keys are equal;
- engine inputs are equal;
- per-item results are equal excluding nondeterministic timing and generated IDs;
- error classification is equal;
- result ordering matches input ordering under concurrency.

### 15.7 HTTP and job tests

- all generation requests enqueue `script.generate`;
- the stored payload is `GenerationEnvelopeV2`;
- every enqueue response has the same shape;
- job status exposes `GenerationEnvelopeResult`;
- no public route invokes Engine directly;
- no worker handler re-enqueues itself;
- idempotency applies to the canonical normalized request identity.

### 15.8 Architecture tests

CI must reject:

- a new script-generation job type outside `internal/domain/job`;
- direct Engine calls outside `GenerateOneUseCase` and engine tests;
- durable `map[string]any` generation contracts;
- a second source or artifact registry;
- duplicate resolver or processor keys;
- endpoint-specific script defaulting;
- a second canonical service construction site;
- `interface{}` in new script contracts.

## 16. Definition of Done

The migration is DONE only when every item below is true.

### Contract and ownership

- [ ] `GenerationEnvelopeV2` is the only durable script-generation command.
- [ ] `ResolvedGenerationPlan` is the only internal execution plan.
- [ ] `GenerationResult` and `GenerationEnvelopeResult` are the only durable result contracts.
- [ ] Contract ownership is documented in `architecture/ownership.yaml`.
- [ ] No durable generation command/result uses `map[string]any`, broad `any` or `interface{}`.

### API and jobs

- [ ] `POST /api/script/generate` is the canonical generation endpoint.
- [ ] All generation HTTP requests return the same enqueue response shape.
- [ ] `script.generate` is the only active script-generation job type.
- [ ] One worker handler decodes and executes `GenerationEnvelopeV2`.
- [ ] No worker-side path can recursively enqueue the same envelope.
- [ ] Correlation ID, idempotency, cancellation, deadlines, progress and retry classification work for single and batch requests.

### Execution path

- [ ] `GenerateOneUseCase` is the only production caller of the script engine.
- [ ] Text, explicit clips, catalog and search are registered source resolvers.
- [ ] Resolver and processor registries reject duplicate keys and mutation after freeze.
- [ ] Defaults and validation are applied once before source resolution.
- [ ] Endpoint names never select generation logic after request adaptation.
- [ ] The same item has identical normalized plan and engine input inside or outside a batch.

### Prompt and cache correctness

- [ ] Prompt profile/version, writing guidelines, source fingerprint and cache key are separate fields.
- [ ] Source fingerprints and cache keys never appear in model messages.
- [ ] Cache identity is deterministic from normalized semantic inputs.
- [ ] Output-only artifact flags do not unnecessarily invalidate script text cache.
- [ ] Force refresh behavior is identical for all source kinds and batch execution.

### Outputs

- [ ] Document, images, voiceover, entities, metadata and persistence are typed postprocessors.
- [ ] Each processor runs only when explicitly enabled.
- [ ] Requested unavailable outputs fail explicitly before returning success.
- [ ] No output route or preset advertises work that is not implemented.
- [ ] Document creation is not unconditional.
- [ ] `with_images` changes only image output configuration unless the caller explicitly selects another preset.

### Batch

- [ ] Batch calls `GenerateOneUseCase` for every item.
- [ ] Batch contains no direct Engine call, prompt builder, source resolver or independent cache policy.
- [ ] One-item batch parity tests pass.
- [ ] Partial-failure policy is explicit in `AggregateSpec`.
- [ ] Result ordering is deterministic.
- [ ] Sync and async execution do not expose different per-item result contracts.

### Legacy removal

- [ ] Old script job constants and worker handlers are deleted.
- [ ] `GenerateBatchRequest`, `JobPayloadCatalogScript`, `JobPayloadCurate`, obsolete `GenerationSpec` fields/codecs and `WriteScriptRequest` are removed or transformed into the final canonical types.
- [ ] Legacy route adapters are deleted after the tracked zero-use observation window.
- [ ] No cross-package aliases or pass-through compatibility wrappers remain.
- [ ] No old and new writer are active simultaneously after CUTOVER.
- [ ] Obsolete config, metrics, comments, tests, generated inventory entries and allowlists are removed.
- [ ] Repository search finds no untracked references to superseded route names, job types or payload types in production code.

### Verification and documentation

- [ ] Unit, integration, race-sensitive and parity tests pass.
- [ ] `go test ./...` passes.
- [ ] `go test -race -v ./internal/... ./pkg/...` passes.
- [ ] `go vet ./...` passes.
- [ ] `go build ./...` passes.
- [ ] `go mod tidy` leaves `go.mod` and `go.sum` unchanged.
- [ ] `bash scripts/ci-architectural-checks.sh` passes.
- [ ] strict architecture checks pass with no new baseline or allowlist.
- [ ] generated API/architecture manifests are reproducible and leave a clean working tree.
- [ ] `ARCHITECTURE.md`, `AGENTS.md`, ownership, migration tracker and generated API docs describe only the final runtime.
- [ ] A clean checkout reproduces the same green result.

### Operational evidence

- [ ] Metrics show all new generation traffic on `script.generate`.
- [ ] Metrics show zero invocations of deprecated routes/jobs for the required observation period.
- [ ] Existing queued legacy jobs have been drained, migrated or explicitly resolved.
- [ ] Production diagnostics can distinguish source resolution, engine generation and each artifact processor.
- [ ] Rollback procedure is documented for EXPAND and CUTOVER without enabling dual writers after cutover.

## 17. Completion rule

Passing tests while old paths remain is not completion.

The migration is complete only after CONTRACT removes the superseded contracts, routes, job handlers, defaults, result maps, compatibility code and architecture baselines. The final acceptable duplicate count is zero.

## 18. PR 9 Zero-Legacy §07 deprecation register

PR 9 (June 2026) completes the CONTRACT phase for the deprecated types
listed below. Each remaining legacy item is recorded with the canonical
deprecation fields required by `07_ZERO_LEGACY_POLICY.md`: id, owner,
replacement, introduction date, removal deadline, tracking issue,
compatibility test, and usage metric. Removal is gated by a zero-use
observation window; the entries below are NOT eligible for deletion
until the metric holds zero for the configured duration.

| Deprecation ID | Owner | Replacement | Removal deadline | Metric |
|---|---|---|---|---|
| `DL-CURATIONTYPES-001` | `internal/application/scripts` wave owner | `SourceCurate` resolver + `GenerateOneUseCase` (`PR 4` + `PR 5`) | 2026-09-27 (90-day grace) | `scripts.Curate_legacy_invocations_per_day == 0` for 30 consecutive days |
| `DL-COMPAT-LEGACYDECODER-001` | `internal/application/scripts/compat` owner | Canonical `DecodeModelOutput` (`PR 1`); legacy array decoder is read-only fallback for pre-V1 cache rows | 2026-12-31 (180-day grace) | `compat.LegacyArrayToOutput_invocations_per_day == 0` for 60 consecutive days |

The deprecation records live at:

- `internal/application/scripts/curation_types.go` (DL-CURATIONTYPES-001)
- `internal/application/scripts/compat/legacy_model_output_decoder.go` (DL-COMPAT-LEGACYDECODER-001)

Both deprecation records MUST be kept in lockstep with the deployment
metrics; the deletion PR is BLOCKED until the metric gate passes. The
metric name MUST be exposed via Prometheus so operators can read the
countdown window at a glance (see `internal/infrastructure/observability/
metrics.go`).

The nine anti-regression CI gates (`Check 24` through `Check 32`) in
`scripts/ci-architectural-checks.sh` enforce the invariants from
PR 6 + PR 7 + PR 8 + PR 9. Any new code that resurrects the legacy
WriteScript surface, writes a fingerprint into a model input, sets
`OutputFmt = "prose"`, references the `Single *GenerationResult`
envelope field, or constructs scenes via the legacy paragraph-
splitter helpers will fail CI immediately. The canonical pipeline is
enforced, not just documented.
