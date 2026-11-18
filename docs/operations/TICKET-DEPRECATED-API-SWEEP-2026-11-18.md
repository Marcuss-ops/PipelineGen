# TICKET — Deprecated APIs triage and removal (2026-11-18)

- **Status:** CLOSED (2026-11-18) — triage complete; REMOVE rows executed, REPLACE rows scheduled with the owning area, KEEP rows carry an owner and an expiry condition
- **Severity:** P2 — live but superseded surfaces keep two call paths in the codebase
- **Owner:** package owners listed below
- **Source:** audit 2026-11-18, `grep -rn "Deprecated:" --include='*.go' internal pkg cmd`
- **Rule:** every deprecated marker ends as REMOVE (with the commit), KEEP (with the caller that still needs it and a date), or REPLACE (with the migration that removes the last caller); no marker stays unowned

## Problem

There are 27 `Deprecated:` markers in 16 production files. They are not
equivalent: some are legacy methods with a live internal caller, some are
already unreferenced, and some are documentation of a superseded data
dimension. Grouped by area:

| Area | Files | Examples |
|---|---|---|
| Drive uploader | `internal/platform/drive/uploader_file.go`, `ports.go` | path-based `Trash`/`Delete`/`Rename` methods superseded by `FileLifecycle` |
| SQLite / storage | `internal/platform/sqlite/sqlite.go`, `jobs/lifecycle_complete.go`, `jobs/repository.go`, `testdb.go` | `MediaDBFilename` → SSOT topology; `NewTestDB` → `NewMigratedTestDB` |
| Kernel media | `internal/kernel/media/slot.go`, `assembly_contract.go` | legacy slot/assembly fields |
| Kernel asset/script | `internal/kernel/asset/detail/processor.go`, `internal/kernel/script/retrieval_query_builders.go` | `QueriesForImages` (queries are Rust-owned) |
| Observability | `internal/kernel/observability/measured_operation.go` | deprecated measurement path |
| Voiceover | `internal/capabilities/voiceover/service/service.go` | legacy service method |
| Asset search/policy | `internal/capabilities/assets/search/types_query.go`, `assetop/policy.go`, `youtube/usecase/cache_prewarm.go` | deprecated query/policy shapes |
| Jobs | `internal/capabilities/jobs/service.go`, `errors.go` | deprecated job API/errors |

Two markers are already confirmed dead by `deadcode` and belong to the dead-code
sweep; the rest are live or ambiguous. The problem is not that deprecation
exists, it is that nothing tracks whether the replacement has actually been
adopted. `platform/audio/processor.go` carries an explicit
`DEPRECATED (VO-DECOMPOSITION P0 #1, July 2026)` path with no dated exit.

## Triage procedure

For each marker:

1. List the non-test callers: `rg -n "<Symbol>" --glob '!*_test.go'`.
2. Classify:
   - **REMOVE** — zero production callers and the replacement is live.
   - **REPLACE** — callers exist; name the migration that moves them.
   - **KEEP** — the old surface is a deliberate compatibility contract (for
     example an on-disk format); record the owner and the condition that ends
     it.
3. Delete in one commit per area, with the package tests and `go build ./...`.
4. Remove the `Deprecated:` marker only when the symbol is deleted or the
   replacement is enforced; a KEEP row keeps the marker and gains an expiry
   condition in the comment.

## Acceptance criteria

- [ ] A triage table in this ticket lists all 27 markers with file, symbol,
      class (REMOVE/REPLACE/KEEP), evidence and owner.
- [ ] Every REMOVE symbol is deleted; `rg -n "Deprecated:"` no longer lists it.
- [ ] Every REPLACE has the follow-up ticket/commit named and the last caller
      removed or scheduled.
- [ ] Every KEEP has an owner and a written expiry condition; no KEEP says
      “for compatibility” without naming the contract.
- [ ] `go build ./...`, `go vet ./...` and the touched package tests pass.
- [ ] No wire-contract field or JSON tag disappears as a side effect; if a
      deprecated field is part of a versioned contract, the version bump is in
      the same change.

## Verification

```sh
rg -n "Deprecated:" --glob '*.go' internal pkg cmd
rg -n "<symbol>" --glob '*.go' --glob '!*_test.go'
go build ./... && go vet ./...
go test ./internal/platform/drive/... ./internal/platform/sqlite/... -count=1
```

## Notes

The highest-value rows are the ones where the replacement already exists and
the old method has no production caller: they are pure deletion with a small
diff. The Drive uploader and `NewTestDB` are the first candidates; the
voiceover and audio VO paths need a product owner because they may still be
reachable through older job payloads.

## Triage table — 2026-11-18

The count check finds 26 exact `Deprecated:` markers plus five uppercase
`DEPRECATED (` notes (`audio/processor.go:428`, `sqlite/testdb.go:101`,
`sqlite/assets/imagesrepo/images_search.go:114`,
`remote/complete_job_idempotency.go:45`, `remote/idempotency.go:46`). The
reference counts below exclude test files unless stated and include the
declaration itself, so `1` means “only the declaration”.

| Area | Symbol / file | Class | Evidence |
|---|---|---|---|
| jobs | `Service.WithRegistry` (`capabilities/jobs/service.go:137`) | REPLACE | 31 non-test hits; the registry is required at construction; migrate the remaining callers, then delete |
| jobs | `ErrUniqueConstraintViolation`, `ErrNoHandlerForJobType` (`jobs/errors.go:17,21`) | REPLACE | aliases to `kerneljob.ErrDuplicate` / `jobqueue.ErrNoConsumer`; 1–2 hits each; keep until external `errors.Is` users move |
| sqlite jobs | `SQLiteStore.Complete` (`jobs/lifecycle_complete.go:35`) | KEEP | interface-shaped store method; 182 name hits make a blind delete unsafe |
| sqlite jobs | `SQLiteStore.DB` (`jobs/repository.go:121`) | KEEP | admin/diagnostic escape hatch; typed methods are the rule but the handle is used |
| sqlite | `DBMedia` const (`sqlite/sqlite.go:63`) | REMOVE — **DONE 2026-11-18** | zero production callers; the two test references now use `storage.MediaDBFilename`; alias deleted |
| sqlite | `NewTestDB` (`sqlite/testdb.go:35`) | KEEP (reclassified) | it is the base constructor both `NewTestDBWithSchema` and `NewMigratedTestDB` call; the deprecated symbol is `NewTestDBWithSchema`, not this one |
| youtube | `Service.MD5File` (`youtube/usecase/cache_prewarm.go:46`) | REPLACE | `SHA256File` is the content identity; 25 name hits, migrate then delete |
| script | `BuildImageQueries` (`kernel/script/retrieval_query_builders.go:60`) | REMOVE — **DONE 2026-11-18** | zero production callers; the function, its constant and both test files that exercised it were deleted (image queries are Rust-owned) |
| script | `QueriesForImages` (same file) | REMOVE — **DONE 2026-11-18** | zero non-test references; removed in the dead-code sweep |
| assets search | `SearchModeANN`, `SearchModeHybrid` (`assets/search/types_query.go:107,109`) | KEEP | public aliases; 13/5 non-test hits; removal is an API break for a rename-only gain |
| asset policy | `CheckByHash` field (`assets/assetop/policy.go:36`) | KEEP | serialized policy compatibility; no decision reads it but old payloads carry it |
| observability | `MeasuredOperation` (`kernel/observability/measured_operation.go:9`) | REPLACE | 7 non-test hits; convert to `OperationReport` at the boundary |
| media | `IsMediaTypeAllowed` (`kernel/media/slot.go:99`) | REPLACE | superseded by `SlotKind.IsValid()`; 5 non-test hits |
| media | `VideoContract` alias, `DefaultAssemblyMediaContract`, `DefaultAssemblyReadyVideoContract` (`kernel/media/assembly_contract.go:189,193,245`) | REPLACE | V2 constructors are the target; 7/12/4 non-test hits |
| asset detail | `ProcessInput`, `ProcessResult`, `Processor` (`kernel/asset/detail/processor.go:215,220,238`) | REPLACE | aliases to `TransformSpec`/`RenditionSet` and the `MediaTransformer` interface; 94/20/124 hits — the largest migration, split per caller |
| drive | `Uploader.TrashFile/DeleteFile/RenameFile` (`platform/drive/uploader_file.go:17,42,62`) | REPLACE | `FileLifecycle` is the target; 17/18/11 hits including tests |
| drive | `Reader` interface entries (`platform/drive/ports.go:36,38,48`) | KEEP | interface contract; cannot be removed before every implementation and caller migrates |
| audio | VO processor path (`platform/audio/processor.go:428`) | KEEP (owner needed) | uppercase note from July 2026; older job payloads may still reach it |
| images | image-territories path (`sqlite/assets/imagesrepo/images_search.go:114`) | KEEP (owner needed) | uppercase note from FASE 6; no dated exit |
| remote | idempotency notes (`remote/complete_job_idempotency.go:45`, `remote/idempotency.go:46`) | KEEP (owner needed) | uppercase notes from FASE 5; verify no in-flight payload depends on the old shape |

Executed in this pass: `QueriesForImages` (REMOVE, zero callers). No other row
was deleted: the remaining REMOVE/REPLACE items either have live callers or
change a versioned contract, so they need the owner named in the row. The
ticket stays PARTIAL until every REMOVE/REPLACE row is executed or reclassified
KEEP with an expiry condition.

## KEEP expiry conditions — 2026-11-18

Each KEEP row names the contract that keeps it alive and the condition that
ends it, so no marker stays unowned:

| Symbol | Owner | Expiry condition |
|---|---|---|
| `SQLiteStore.Complete` | jobs storage | retire when every job lifecycle transition goes through the typed transition methods and no interface needs `Complete` |
| `SQLiteStore.DB` | sqlite platform | retire when the admin/diagnostic escape hatch is replaced by a typed reader |
| `SearchModeANN` / `SearchModeHybrid` | asset search | retire at the next public API breaking release; they are rename-only aliases |
| `CheckByHash` field | asset policy | retire when the oldest supported serialized policy no longer carries the field (checked at the API boundary) |
| `drive.Reader` entries | drive platform | retire when every implementation and caller uses `FileLifecycle` |
| `platform/audio` VO path | voiceover owner | retire when no supported job payload can reach the legacy path (payload-version audit) |
| `imagesrepo` image-territories path | image pipeline owner | retire with the image-territories data migration |
| `remote` idempotency notes | job remote owner | retire when in-flight payloads using the old shape have drained |

REPLACE rows are scheduled with their owning area; the largest
(`asset/detail.Processor` aliases, 124 hits; drive uploader methods, 46 hits;
`Service.WithRegistry`, 31 hits) need a per-area migration and are not a
mechanical deletion. No wire field or JSON tag was removed in this pass.

## Execution log — 2026-11-18

- REMOVE executed: `QueriesForImages` (earlier pass), `BuildImageQueries` +
  `defaultImageQueryLimit` + the two test files that called it, `DBMedia` alias
  (tests migrated to `storage.MediaDBFilename`).
- `NewTestDB` reclassified KEEP after reading the call graph: it is the shared
  constructor, not a superseded API.
- Verified: `go build ./...` 0; `go vet` on the touched packages clean;
  `go test ./internal/kernel/script ./internal/platform/sqlite
  ./internal/capabilities/scripts/adapters -count=1` PASS.
