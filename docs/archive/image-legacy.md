# Image Subsystem Legacy Surfaces — Canonical Retirement Archive

> **Authority**: this file is the canonical home for the retirement narrative behind
> the dead-code surfaces removed in PR-IMG-LEGACY-1 (commit on `origin/main`, 2026-07-06).
> Runtime files (handlers, generators, registries) intentionally carry only a 1-line
> pointer to this archive — per godlike/06 SSOT, runtime code explains the present
> while `docs/archive/` documents the past.

---

## §1 — `/api/images/webhook/remote` route retirement

### Original surface
```
POST /api/images/webhook/remote   (multipart upload ≤ 500MB)
```

The remote-worker ingest pipeline (NVIDIA-era, pre-`image.generate.google` job
system) bypassed the worker pool and fed image assets straight into the local
ingest pipeline via `ingest.Service.IngestImage` from a worker callback.

### Why retired
**closure: surface-2 (July 2026)** collapsed into the canonical async job
system. Operators no longer call /api/images/webhook/remote from a remote worker;
instead they submit `image.generate.google` jobs through the canonical
`jobs.Service.Enqueue` API + receive results via `SQLiteStore`.

### Replacement contract
- **Canonical entry-point**: `jobs.Service.Enqueue` with `TypeImageGenerateGoogle`
  (defined at `internal/application/jobs/registry.go`).
- **Worker-side ingest path**: `internal/application/assets/ingest.Service.IngestImage`
  is now invoked by the worker job handler, never by a remote HTTP webhook.

### Audit-pin (locks the retirement)
- `internal/api/middleware/middleware_auth_test.go::TestAuth_RetiredWebhookPathReturns404`
  asserts HTTP 404 for any `POST /api/images/webhook/remote` regardless of
  credentials — **load-bearing regression lock**.

### Removal date
2026-07-06 (PR-IMG-LEGACY-1, this commit).

---

## §2 — `ReceiveRemoteWebhook` Go handler type retirement

### Original surface
The `ImagesHandler.ReceiveRemoteWebhook` Go method (pre-`PR-IMG-SPLIT-2`:
in `internal/api/images/impl.go`) plus the `RemoteWebhookJobJSON` typed DTO.
Both wired in `RegisterRoutes` to consume the multipart webhook stream.

### Why retired
Same closure as §1: surface-2 collapse. The Go handler was orphaned when the
HTTP route was retired; the matching DTO was orphaned with it.

### Replacement contract
No replacement Go surface exists — the worker-side ingest pipeline (see §1)
reads from the canonical protocol buffer (`internal/domain/remote/staged_artifact_reference.go`).

### Removal date
Pre-`PR-IMG-SPLIT-2` (June 2026). This PR-IMG-LEGACY-1 closes the residual
comment-only references in runtime files; the Go method itself was already
gone from `internal/api/images/handler.go` after the `impl.go` → capability-
file split.

---

## §3 — `ErrUnsupportedModel` sentinella retirement

### Original surface
```go
// internal/application/images/generated/provider_registry.go
var ErrUnsupportedModel = errors.New("generated image model unsupported; only nano-banana-pro via google-slides is available")
```

### Why retired
**closure: surface-4 (July 2026)** removed the caller-facing surface that could
select a non-canonical model. The AI backend now routes exclusively through
`CanonicalGoogleSlidesModel = "nano-banana-pro"` (the same constant that
already lives at the top of `provider_registry.go`).

The sentinella was **retired as audit-pin** for ~30 days: a typed value that
would catch a future contributor re-introducing model-routing against an
intentional `errors.Is(err, ErrUnsupportedModel)` probe. After 30 days with
**zero live callers** (canonical `rg "ErrUnsupportedModel" internal/`
cross-checked on 2026-07-06: zero production hits, only the typed-grep audit
itself and the comment block), the sentinella loses its audit-pin function
because no future contributor test can probe against an absent typed value.

### Replacement contract
`ErrProviderUnavailable` already exists as the canonical typed-error for the
generated-image pipeline (defined at `provider_registry.go:71`). All failure
modes in `GoogleSlidesProvider.Generate` + `GenerationProviderRegistry.Resolve`
+ `GoogleSlidesProvider.Healthy` already route through it.

### Audit-pin
- `git grep ErrUnsupportedModel internal/` → 0 hits post-PR-IMG-LEGACY-1
  (archive doc + this entry are exempted as godlike/06 historical references).
- `git grep nano-banana-pro google-slides canonical internal/application/images/generated/`
  → confirms `CanonicalGoogleSlidesModel` is the SOLE canonical string.

### Removal date
2026-07-06 (PR-IMG-LEGACY-1, this commit).

---

## §4 — Cross-references (godlike/06 SSOT lockstep)

- `architecture/current.yaml#IMAGES-LEGACY-CLEANUP-2026-07-06` — wave-tracker
  anchor + `linked_issues[PR-IMG-LEGACY-1]` flipped to `status: shipped`.
- `architecture/action-plans/2026-07-06-images-legacy-cleanup.md` §2 —
  per-PR execution plan that scoped this PR.
- `CHANGELOG.md ## Unreleased → ### Removed` — closure meta-entry.
- `AGENTS.md ## Recent cross-cutting closures` — mini-mirror entry per
  per-PR landing convention.
- `internal/api/middleware/middleware_auth_test.go` — 404 regression lock for §1.
- godlike/06 SSOT: this file is the canonical SOLE owner of the retirement
  narrative; runtime files (`handler.go`, `handler_full.go`, `provider_registry.go`)
  carry only 1-line pointers per the "explain present, document past" invariant.

---

## §5 — Honest Scope-Lock (godlike/07 NO-FAKE-AVAILABILITY)

**`ErrUnsupportedModel` removal is safe** because:
1. Live-caller grep returns zero hits across `internal/` (not counting this
   archive doc + the auto-generated audit-grep).
2. `CanonicalGoogleSlidesModel` and `ErrProviderUnavailable` together cover every
   failure mode the sentinella's comment block referenced.
3. The 1-line pointer replacement preserves the audit-trail without runtime
   comment-bloat (the 7-line handler.go comment + 1-line handler_full.go
   reference + 12-line provider_registry.go comment were already-dead context
   aboard a live codebase).

**`/webhook/remote` comment removal is safe** because:
1. The HTTP route was already gone from `RegisterRoutes` post-`PR-IMG-SPLIT-2`.
2. The 404 audit-pin test in `middleware_auth_test.go` locks the contract
   independent of comment presence.
3. The replacement contract (§1) is the canonical async job system, which has
   its own comprehensive test surface.

**ReceiveRemoteWebhook Go handler removal** (pre-this-PR) was safe because:
1. The handler was physically removed when `impl.go` → capability-files split happened.
2. Only comment-only references survived (which this PR evaporates).

Pre-existing 5-item voiceover + app build-issue carry-forward per
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` is unchanged
— NOT regressions of PR-IMG-LEGACY-1.

---

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
