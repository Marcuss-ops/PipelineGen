# Composition, Documentation and Configuration Cleanup

> Status: PARTIAL
>
> Verified against `main` at `401e3847` on 2026-06-23.
>
> Priority: MEDIUM. Tracker accuracy is HIGH because Wave 14 currently claims a completed boundary that the code does not yet satisfy.

## Verified progress

The following items from the original audit are now complete:

- lifecycle startup order is Drive, Qdrant, Outbox, then job runner;
- transition and effect interval value `0` disables the behavior;
- negative intervals select a default;
- renderer guards prevent modulo-by-zero;
- the concrete transition registry has focused tests;
- `ARCHITECTURE.md` no longer presents CoreDeps, `internal/media` or `internal/sources` as active architecture;
- the generated API document was refreshed from the router;
- health implementation details moved out of the API package.

## Remaining work

### 1. Split `internal/app/composition.go`

The file still owns bundle declarations, `ComposeRoot`, adapter construction, every `Build*Bundle` function, deferred-start closures and root assembly. Split it physically while retaining `package app` and existing public behavior.

Recommended layout:

```text
internal/app/
    composition_root.go
    compose.go
    compose_drive.go
    compose_repositories.go
    compose_search.go
    compose_process.go
    compose_ai.go
    compose_domains.go
    compose_jobs.go
    compose_outbox.go
    compose_sync.go
    compose_maintenance.go
    compose_utility.go
```

Rules:

- relocate symbols before changing behavior;
- keep one canonical definition of every bundle and builder;
- do not leave forwarding functions in `composition.go`;
- keep imports local to the capability file;
- retain focused builder tests;
- leave `NewComposition` as a readable dependency-order map.

### 2. Move ScriptFlow wiring out of `registry.go`

`wireScriptFlow` still constructs repository adapters, batch and curation services, the clip source builder, Qdrant search adapter, reranker, harvest service, use cases and the API handler inside `registry.go`.

Move the function to:

```text
internal/app/wire_script.go
```

This physical move should happen after, or separately from, the remaining ScriptFlow business extraction. Do not mix a pure file split with major workflow changes.

### 3. Propagate startup failures

Ordering is fixed, but startup functions are still `func()` values and `serverLifecycle.Start` ignores its context. Qdrant preparation is explicitly best-effort, so the job runner may start even when a required vector capability failed to initialize.

Target contract:

```go
type StartFunc func(context.Context) error

type StartupStep struct {
    Name     string
    Required bool
    Start    StartFunc
}
```

Required behavior:

- required preparation failure prevents worker startup;
- optional failure is logged and exposed through health;
- cancellation stops remaining steps;
- startup order remains deterministic;
- cleanup remains idempotent.

### 4. Correct Wave 14

Wave 14 currently declares:

```yaml
status: done
verified_zero: true
```

Its own rule says that the API contains transport only and no goroutine orchestration. Current `internal/api/script/handler_jobs.go` still contains semaphore admission, pipeline construction, prewarm goroutine, path dispatch and final result assembly. Therefore the wave is not verified-zero.

Until the ScriptFlow blocker is closed, use:

```yaml
status: in_progress
verified_zero: false
```

Track these remaining items explicitly:

- remove ScriptFlow job orchestration from `internal/api/script`;
- remove forwarding aliases and wrappers from `flow.go`;
- remove Drive and filesystem implementation access from the API package;
- promote the API forbidden-import check to a hard zero gate.

The Wave 14 exit gate must verify boundaries as well as file counts:

- no production API import from `internal/infrastructure`;
- no ScriptFlow job receiver methods in API;
- no API semaphore, pipeline construction or raw goroutine orchestration;
- strict architecture checks pass.

### 5. Make migration YAML validation fail closed

The current verification path may suppress a parser failure while querying the tracker. Add a separate YAML parse validation before checking wave fields. A malformed migration tracker must fail validation rather than appear clean.

### 6. Promote the API import check

The current forbidden-infrastructure check is informational. It also enumerates selected infrastructure packages and therefore misses imports such as `internal/infrastructure/files`.

After ScriptFlow cleanup:

- reject production imports from `internal/infrastructure/` anywhere under `internal/api/`;
- retain only narrowly justified middleware or bootstrap exceptions;
- make the gate fail instead of logging a backlog;
- keep the target at zero.

### 7. Clarify `ARCHITECTURE.md`

The refreshed document accurately shows that `handler_jobs.go` currently executes ScriptFlow jobs, but this should be labeled as a temporary migration violation. After extraction, document the canonical journey as:

```text
HTTP handler -> application command or enqueue
Job runner -> application script job handler -> orchestration service
```

Do not document a target file as current until it exists.

### 8. Keep generated API documentation deterministic

The API snapshot was refreshed. Continue generating it from the same router configuration used by production and fail validation whenever regeneration changes the committed file.

### 9. Complete video configuration documentation

`TransitionInterval` exists in `VideoConfig`, but `config.example.yaml` still lacks the corresponding setting. Add:

```yaml
transition_interval: 4
```

Document that zero disables transitions.

There is also a default mismatch to resolve: configuration uses `4` as the negative fallback for effects, while the renderer uses `3`. Select one canonical value and enforce it through tests. Prefer having configuration be the single source of defaults.

## Suggested work batches

### Batch A — Repository truth

Update:

- `architecture/migration.yaml`;
- architecture checks;
- the temporary ScriptFlow note in `ARCHITECTURE.md`.

### Batch B — ScriptFlow extraction

Follow `SCRIPT_ORCHESTRATION_MIGRATION.md`. Keep this separate from pure composition file moves.

### Batch C — Physical composition split

Limit changes to `internal/app/` and preserve runtime behavior.

### Batch D — Startup error contracts

Introduce context-aware startup steps and required versus optional failure policy.

### Batch E — Configuration consistency

Add the example field and align effect interval defaults between config and renderer.

## Validation checklist

- format changed Go files;
- run focused application, API, app and render tests;
- run repository-wide vet and build;
- parse `architecture/migration.yaml` as valid YAML;
- run architectural checks;
- regenerate API documentation and verify no diff;
- inspect working-tree status and recent commit history.

## Definition of done

This cleanup group is complete when:

- composition and registry files are physically cohesive;
- `wireScriptFlow` lives in `wire_script.go`;
- ScriptFlow job orchestration no longer lives in API;
- required startup failures are observable before workers start;
- Wave 14 matches the actual code state and has a boundary-based exit gate;
- API infrastructure-import enforcement is a hard zero gate;
- architecture and generated API documentation match current code;
- `config.example.yaml` contains `transition_interval`;
- effect and transition defaults are consistent between configuration, renderer and tests;
- focused tests, full build, vet and architecture validation pass.
