# Composition, Documentation and Configuration Cleanup

> Priority: MEDIUM, with one startup-order correctness item that should be
> treated as HIGH.

This plan covers the remaining non-blocker cleanup after the health and script
boundaries are extracted. The work should be split into focused commits even
when committed directly to `main`.

## Scope

1. Physically split `internal/app/composition.go`.
2. Move `wireScriptFlow` out of `registry.go`.
3. Correct startup ordering for Drive, Qdrant, Outbox and workers.
4. Repair `ARCHITECTURE.md` and `architecture/migration.yaml`.
5. Regenerate API documentation from the real router.
6. Add `transition_interval` to example configuration and lock its semantics.

## Non-goals

- Do not introduce a new package for every file.
- Do not move composition logic into application or API packages.
- Do not change public routes or job payloads.
- Do not rename all existing bundles during the physical split.
- Do not redesign the full lifecycle system in the same change.
- Do not manually edit generated API route tables.

# Part 1: Physical composition split

## Current problem

`internal/app/composition.go` still contains bundle types, root definitions,
multiple `Build*Bundle` functions, startup closures and root assembly. Even if
individual functions are reasonable, the file is difficult to navigate and
creates merge conflicts between unrelated capabilities.

`wireScriptFlow` has been extracted from the main `WireRegistry` body but still
lives in `registry.go`, so script-specific wiring continues to expand the
registry file.

## Target file layout

Keep all files in `package app`. This is a physical/cohesion split, not a public
package redesign.

```text
internal/app/
    composition_root.go       bundle types, ComposeRoot, shared start types
    compose.go                NewComposition orchestration only
    compose_drive.go          BuildDriveBundle + Drive startup helper
    compose_repositories.go   BuildRepoBundle
    compose_search.go         BuildSearchBundle
    compose_process.go        BuildProcessBundle + Qdrant startup helper
    compose_ai.go             BuildAIBundle
    compose_domains.go        BuildDomainBundle
    compose_jobs.go           BuildJobsBundle
    compose_outbox.go         BuildOutboxBundle + pool startup helper
    compose_sync.go           BuildSyncBundle
    compose_maintenance.go    BuildMaintBundle
    compose_utility.go        utility/system bundles

    registry.go               registry assembly, freeze and shared registration
    wire_script.go            script services, jobs and routes
    wire_assets.go            existing asset module wiring if useful
    wire_youtube.go           existing YouTube module wiring if useful
    server_lifecycle.go       ordered startup and shutdown
```

Existing `module_*.go` files may remain. Do not duplicate their wiring merely to
match the suggested names.

## Split rules

- Use pure file moves and symbol relocation first; preserve function signatures.
- Keep one canonical definition of every bundle and builder.
- Do not add forwarding functions in `composition.go` after moving code.
- Delete `composition.go` when all symbols have canonical new files, or reduce it
  to `NewComposition` only if that is the clearest final ownership.
- Keep imports local to the file that uses them; the split should materially
  reduce import surface per file.
- Retain compile-time checks and focused tests next to the relevant builder.

## Suggested move order

1. Move bundle and root types to `composition_root.go`.
2. Move leaf builders with few dependencies:
   - utility;
   - maintenance;
   - sync;
   - search.
3. Move repositories and jobs.
4. Move Drive, process and outbox with their deferred start helpers.
5. Move AI and domain composition.
6. Leave `NewComposition` in `compose.go` as the readable dependency-order map.
7. Move `wireScriptFlow` to `wire_script.go`.
8. Run formatting/tests after each group to avoid a giant debugging surface.

## Desired `NewComposition` shape

The final function should read like a dependency graph, not contain adapter
construction details:

```go
func NewComposition(...) (*ComposeRoot, error) {
    repos, err := BuildRepoBundle(...)
    search, err := BuildSearchBundle(...)
    drive, driveStart, err := BuildDriveBundle(...)
    process, processStart, err := BuildProcessBundle(...)
    ai, err := BuildAIBundle(...)
    jobs, err := BuildJobsBundle(...)
    domains, err := BuildDomainBundle(...)
    outbox, outboxStart, err := BuildOutboxBundle(...)
    syncBundle, err := BuildSyncBundle(...)
    maint, err := BuildMaintBundle(...)
    utility, err := BuildUtilityBundle(...)

    return &ComposeRoot{...}, nil
}
```

Error wrapping should identify the failed bundle.

# Part 2: Startup ordering

## Current risk

The lifecycle currently starts the job runner before Drive initialization,
Outbox startup and Qdrant collection setup. A worker can therefore consume a job
before a required dependency has completed initialization.

## Target order

Separate required preparation from background worker start:

```text
1. Validate/prepare critical local resources.
2. Prepare enabled remote capabilities required by jobs.
3. Start Outbox delivery pool.
4. Start job runner and other consumers.
5. Begin accepting traffic if server startup controls that boundary.
```

Recommended immediate ordering:

```text
Drive preparation
Qdrant collection preparation
Outbox pool start
Job runner start
```

If Drive is optional for the deployment mode, its preparation may be
best-effort. Qdrant must be required only when the enabled feature requires it.
The lifecycle must make this policy explicit.

## Improve start contracts

`type IOpaqueStartFunc func()` cannot report failure. Replace or supplement it
with a typed startup contract:

```go
type StartFunc func(context.Context) error

type StartupStep struct {
    Name     string
    Required bool
    Start    StartFunc
}
```

A small ordered startup registry is preferable to adding more positional
parameters to `NewServerLifecycle`.

```go
type serverLifecycle struct {
    prepare []StartupStep
    start   []StartupStep
    cleanup func()
}
```

Rules:

- Required preparation failure prevents readiness and server startup.
- Optional preparation failure is logged and represented in health status.
- Worker consumers start only after required preparation passes.
- Startup respects context cancellation.
- Shutdown remains idempotent and ordered.
- Do not hide network calls inside constructors to avoid addressing startup
  policy.

## Lifecycle tests

Add tests that record execution order:

```text
expected: drive -> qdrant -> outbox -> jobs
```

Cover:

- required step failure stops later steps;
- optional step failure allows startup but is observable;
- cancellation stops startup;
- cleanup runs exactly once;
- no worker starts when required Qdrant preparation fails in a mode that needs
  vector search.

# Part 3: Architecture documentation repair

## `ARCHITECTURE.md`

Replace legacy references to:

- `CoreDeps`;
- removed `internal/module/*` files;
- removed `internal/media` ownership;
- removed `internal/sources` ownership;
- old ScriptFlow handler/job filenames;
- obsolete module counts and route ownership.

The diagram should show:

```text
cmd/server
  -> internal/api Router + Registry
  -> internal/app ComposeRoot + capability bundles
  -> internal/application capabilities
  -> internal/domain contracts
  -> internal/infrastructure adapters
  -> SQLite / Drive / Qdrant / external services
```

The module table should be generated or manually verified from current
`internal/app/registry.go`, `wire_*.go`, `module_*.go` and route registration.
Do not document aspirational files as current files.

Add a short section distinguishing:

- composition-time construction;
- lifecycle preparation/start;
- HTTP route registration;
- job handler registration.

## `architecture/migration.yaml`

Wave 14 must be made internally coherent.

Required corrections:

- Properly indent `verified_zero` within the Wave 14 mapping.
- Do not use `status: done` while the API package still contains orchestration.
- Recommended current status until script migration completes:

```yaml
- id: 14
  title: API transport modules consolidation
  status: in_progress
  verified_zero: false
```

- Remove completed items from `pending` or replace the field with an accurate
  `remaining` list.
- Use the real package name `internal/api/script` unless a rename is actually
  completed.
- Correct contradictory file counts.
- Make the exit gate verify behavior/boundaries, not only file count.

Recommended Wave 14 exit gate:

```text
- API root and feature directories remain under agreed size limits.
- No internal/api package imports forbidden infrastructure.
- No script job handler or orchestration remains under internal/api/script.
- Every feature exposes a focused route registration surface.
- Full archcheck strict mode passes.
```

Add a YAML parse gate before querying fields:

```bash
yq eval '.' architecture/migration.yaml >/dev/null
```

A parse failure must fail CI. Never hide it with `|| true`.

## README and cleanup entry point

Ensure `REPOSITORY_CLEANUP.md` links to `docs/cleanup/README.md`. Remove references
to missing cleanup documents or recreate only documents that are intentionally
canonical.

# Part 4: Generated API documentation

## Rule

`docs/api/ACTIVE_API_GENERATED.md` must be produced only by:

```bash
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
```

Do not manually remove stale routes from the generated file.

## Generator fixes/checks

- Ensure the output directory exists before `os.WriteFile`:

```go
if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
    return fmt.Errorf("create output directory: %w", err)
}
```

- Build the same router configuration used by production route registration.
- Ensure feature flags used during generation are deterministic and documented.
- Include `/health` and `/ready` only once at their real paths.
- Remove obsolete route-description entries when routes no longer exist.

## CI gate

Run generation before architecture/doc drift checks, then fail on any dirty
change:

```bash
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code -- docs/api/ACTIVE_API_GENERATED.md
test -z "$(git status --porcelain docs/api/ACTIVE_API_GENERATED.md)"
```

Because `git diff` alone misses untracked files, retain the status check.

# Part 5: Video configuration semantics

## Missing example field

Add to `config.example.yaml` under `video`:

```yaml
transition_interval: 4
```

Add the same field to staging/deployment examples where they intentionally list
all video settings.

## Resolve zero-value contradiction

Current comments indicate that zero disables effects/transitions, while the
renderer treats non-positive values as defaults. Choose one canonical semantic.

Recommended semantic:

```text
0 = disabled
positive integer = apply every N clips
negative/omitted after parsing = default
```

Go scalar config cannot distinguish omitted from explicit zero without pointer
or custom unmarshalling. Therefore choose one implementation strategy:

### Strategy A: zero means default

Simplest and compatible with standard zero values:

- Update comments to say zero/default resolves to 4 or 3.
- Use explicit booleans `NoTransitions` and `NoEffects` for disabling.
- Document the behavior in examples.

### Strategy B: zero means disabled

Requires preserving omitted-vs-zero intent:

- use pointer fields in raw config DTOs; or
- custom YAML unmarshalling with presence flags; or
- introduce explicit `transitions_enabled` / `effects_enabled` booleans.

Recommended for the current codebase: **Strategy A**, because the render request
already contains `NoTransitions` and `NoEffects`. Then:

- `TransitionInterval <= 0` resolves to 4;
- `EffectInterval <= 0` resolves to 3 or the documented canonical default;
- comments and example config state this clearly;
- API/job payload flags remain the disable mechanism.

Also decide whether the default effect interval is 3 or 4. The infrastructure
renderer currently falls back to 3 while `VideoConfig` defaults to 4. Use one
value everywhere and lock it with tests.

## Transition tests

Add `internal/infrastructure/media/render/transitions_test.go` covering:

- exact catalog length;
- exact insertion order;
- unique names;
- every entry has non-nil `RenderEnd` and `RenderStart`;
- `Register` appends a new entry;
- replacing an existing name does not change its position;
- `All` returns a defensive copy;
- render interval defaults match configuration defaults.

Fix documentation that says both 14 and 15 entries. The actual catalog and test
must define the canonical count.

# Suggested implementation batches

## Batch A: lifecycle correctness

Files:

```text
internal/app/server_lifecycle.go
internal/app/bootstrap.go
internal/app/composition_root.go or existing composition.go
internal/app/*_test.go
```

Deliverable: deterministic startup order and typed startup failures.

## Batch B: physical file split

Files limited to `internal/app/`. No behavior changes.

Deliverable: same package/API, smaller cohesive files, full tests unchanged.

## Batch C: docs and migration truth

Files:

```text
ARCHITECTURE.md
architecture/migration.yaml
REPOSITORY_CLEANUP.md
scripts/ci-architectural-checks.sh
```

Deliverable: parseable tracker and documentation matching current code.

## Batch D: generated API docs

Files:

```text
cmd/admin/gen_api_docs.go
docs/api/ACTIVE_API_GENERATED.md
.github/workflows/ci.yml
```

Deliverable: deterministic regeneration and no stale routes.

## Batch E: video configuration and tests

Files:

```text
internal/infrastructure/config/video.go
internal/infrastructure/media/render/ffmpeg.go
internal/infrastructure/media/render/transitions.go
internal/infrastructure/media/render/transitions_test.go
config.example.yaml
config.staging.yaml if applicable
```

Deliverable: one documented interval semantic and complete catalog tests.

# Validation

```bash
gofmt -w internal/app internal/infrastructure/config internal/infrastructure/media/render cmd/admin
go test ./internal/app/...
go test ./internal/infrastructure/config/...
go test ./internal/infrastructure/media/render/...
go test ./cmd/admin/...
go vet ./...
go build ./...
yq eval '.' architecture/migration.yaml >/dev/null
bash scripts/ci-architectural-checks.sh
go run ./cmd/admin gen-api-docs docs/api/ACTIVE_API_GENERATED.md
git diff --exit-code -- docs/api/ACTIVE_API_GENERATED.md
git status -sb
git log -n 5 --oneline
```

# Definition of done

- Composition and wiring files are cohesive and navigable.
- `wireScriptFlow` lives in `wire_script.go`.
- Worker consumers start only after required infrastructure preparation.
- Startup failures are observable and required failures stop readiness/startup.
- `ARCHITECTURE.md` contains no active references to removed ownership paths.
- Wave 14 is valid YAML and accurately reflects the code state.
- Generated API docs match the current router.
- `transition_interval` is present in example configuration.
- Transition/effect interval semantics are consistent across config, renderer,
  comments and tests.
- Transition catalog behavior has concrete infrastructure tests.
- Full build, vet, architecture checks and generated-doc gates pass.
