# DRIVE-UPLOAD-ANALYSIS-2026-07-06 — Drive Upload Analysis & Action Plan

> **Derived from**: Italian architectural audit (2026-07-06) applying the user's
> checklist: fragility/coupling, complexity/maintainability, performance,
> dead-code elimination, and the frequency×complexity priority matrix.
>
> **Canonical surfaces analyzed**: `internal/infrastructure/drive/` (40 files),
> `internal/application/assets/delivery/`, `internal/app/build_bundles_drive.go`,
> `internal/application/voiceover/ports.go`.

---

## §1 — Architecture Snapshot (Current State)

```
┌─ Application Layer ─────────────────────────────────────────┐
│  delivery.Publisher (port)    →  Publish / ResolveFolder    │
│  delivery.DestinationKey      →  10 destinations             │
│  delivery.ConflictPolicy      →  Overwrite/Skip/Rename/...  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─ Infrastructure Layer ──────────────────────────────────────┐
│  drive.Publisher              →  resolveDestination() canal │
│  drive.FolderManagerPort      →  EnsureFolder              │
│  drive.FileUploaderPort       →  PutFile (conflict-aware)  │
│  drive.Admin / Reader         →  *Uploader structural      │
│  drive.FileLifecycle          →  Trash/Delete/Cleanup      │
│  drive.DocClient              →  Google Docs (NO port!)    │
│  drive.Store                  →  ⚠️ LEGACY, bypasses Pub    │
└─────────────────────────────────────────────────────────────┘
```

### Strengths (already in place)
- **Pattern 0 port abstraction** — `delivery.Publisher` is the SINGLE canal for all Drive writes
- **Fail-closed at composition** — 3 sentinels (`ErrMissingDestinationRegistry` etc.) + `validateDriveServiceAvailability()`
- **Conflict policy robust** — `ConflictPolicyUnset` iota-zero sentinel + registry-driven defaults
- **Retry with jitter** — `pkg/retry.DoWithValue` on ALL paths
- **Idempotency key (P0.6)** — `DeriveIdempotencyKey(dest:artifactID:hash:version)` via SHA-256 → Drive `appProperties`
- **Startup validation (P1.3)** — `StartupDriveRootsValidator` probes all 10 destinations at boot
- **Ambiguity detection** — `ErrAmbiguousDriveFile` / `ErrAmbiguousDriveFolder` on >1 match

---

## §2 — 6 Prioritized Interventions

### Priority matrix: Frequency × Complexity

| # | Problem | Complexity | Frequency | Priority |
|---|---------|------------|-----------|----------|
| 1 | **DRY folder lookup (3 duplicate impls)** | 🔴 Alta | 🟡 Media | 🔥 **P0 ABSOLUTE** |
| 2 | **`Store.UploadToDrive` legacy bypass** | 🟡 Media | 🔴 Alta | 🔥 **P0 ABSOLUTE** |
| 3 | **`Admin` port too wide (11 methods)** | 🟡 Media | 🟢 Bassa | P1 Medium |
| 4 | **Nil-list deref in `Cleanup`** | 🟢 Bassa | 🟢 Bassa | P2 Low |
| 5 | **voiceover → `Admin.UploadFile`** | 🟢 Bassa | 🟡 Media | P1 Fluid |
| 6 | **`DocClient` without Pattern 0 port** | 🟡 Media | 🟢 Bassa | P1 Medium |

---

## §3 — Per-Intervention Depth

### P0-1 — DRY folder lookup (3 duplicate implementations)

**Files involved:**
- `internal/infrastructure/drive/folder_manager.go::newDefaultFolderLookup` — retry+jitter+firstFolderID
- `internal/infrastructure/drive/admin.go::newAdminDefaultLookup` — identical query, same retry, NO P0.7 re-lookup
- `internal/infrastructure/drive/uploader_ops.go::findOrCreateFolderSerialized` — third impl, has P0.7 Stage 3

**Root cause:** `folderLookupFunc` seam + retry constants (`folderLookupRetry*`) are declared in `folder_manager.go` but not reusable by `admin.go`/`uploader_ops.go` because the seam signature captures `*driveapi.Service` via closure — each file recreates the closure.

**Fix path:**
1. Hoist the query construction + retry wrapper into a SINGLE helper `lookupFolderCanonical(ctx, svc, parent, name) (string, error)` exported from `folder_manager.go` (canonical home).
2. `admin.go::newAdminDefaultLookup` → calls `lookupFolderCanonical`.
3. `uploader_ops.go::lookupFolderExact` → calls `lookupFolderCanonical` (already uses `folderLookupRetryOpts()` — align).
4. Add P0.7 re-lookup (`findOrCreateFolder`+post-create detection) to `AdminAdapter` so all 3 paths share the same duplicate-protection.
5. DRY verification: `rg 'name = .* and trashed = false and mimeType.*folder' internal/infrastructure/drive/` must return ≤1 non-test hit.

**Godlike/07 fail-closed contract:** the query MUST be canonical (exact-match, escaped single-quotes, trashed=false, folder MIME). Post-Create re-lookup MUST detect >1 match via `ErrAmbiguousDriveFolder` (already active in `folder_manager.go`/`uploader_ops.go` — `admin.go` gap).

**Deadline:** 2026-07-13 (1 week)

---

### P0-2 — `Store.UploadToDrive` legacy bypass

**Files involved:**
- `internal/infrastructure/drive/store.go::UploadToDrive` — marked "Deprecated: Fase 3 Spina Dorsale"
- Callers (need audit): `internal/application/images/` package (storage_drive.go, storage_ingest.go, metadata_service.go)
- Target: `delivery.Publisher.Publish(ctx, PublishRequest{Destination: DestinationImage, ...})`

**Problems:**
1. Calls `driveUploader.UploadFile()` directly → no `ConflictPolicy`, no `DestinationRegistry`
2. `s.driveUploader == nil` returns `("", "", nil)` — **silent success** (godlike/07 violation: caller thinks upload succeeded, got empty IDs)
3. No `PutFile` conflict-aware routing (Overwrite/Skip/Rename)
4. `s.rootFolder != "" && folderID == s.rootFolder` guard is a one-off check not replicable via Publisher

**Fix path:**
1. Audit callers: `rg '\.UploadToDrive\(' internal/ --glob '!**/*_test.go'` → list all production call sites.
2. For each caller, replace `store.UploadToDrive(ctx, req, filePath)` with `publisher.Publish(ctx, delivery.PublishRequest{Destination: delivery.DestinationImage, LocalPath: filePath, Filename: ..., ConflictPolicy: delivery.ConflictSkip})`.
3. Wire `delivery.Publisher` into the callers' dependency injection (may require adding a `Publisher` field to Service structs).
4. Remove the nil-uploader silent-success path — nil publisher at construction time → fail-closed.
5. Deprecation record in `architecture/deprecations.yaml`.

**Godlike/07 contract:** the `ImagesFolder()` root check (line 220-222) MUST be replicated via `DestinationRegistry`'s `RequireSubpath` policy for `DestinationImage` (or a `RootFolderOverride`-less publish that goes through `resolveDestination` Step 3 empty-root rejection).

**Deadline:** 2026-07-20 (2 weeks)

---

### P1-3 — `Admin` port too wide

**Files involved:**
- `internal/infrastructure/drive/ports.go::Admin` — 11 methods
- `internal/infrastructure/drive/file_lifecycle.go::FileLifecycle` — already has Trash/Delete/AddParent/Rename/Cleanup

**Overlap:**
| Method | On `Admin` | On `FileLifecycle` |
|--------|-----------|-------------------|
| `TrashFile` | ✅ | ✅ (`Trash`) |
| `DeleteFile` | ✅ | ✅ (`Delete`) |
| `MoveFile` | ✅ | ❌ (`AddParent` only, not true move) |
| `RenameFile` | ✅ | ✅ (`Rename`) |

**Fix path:**
1. Deprecate `TrashFile`/`DeleteFile`/`RenameFile` on `Admin` — add deprecation doc comments pointing to `FileLifecycle`.
2. Audit all callers of these methods on `Admin` and migrate to `FileLifecycle`.
3. `MoveFile` on `Admin` does true move (add+remove parents) while `FileLifecycle.AddParent` only adds — consolidate semantics or keep separate with clear doc.
4. `UploadFile`/`UploadFileWithDescription`/`UploadFileIfChanged` — mark as "raw admin-only, DO NOT use in application code; prefer `delivery.Publisher.Publish`".
5. Goal: `Admin` goes from 11 methods to ~4 (GetOrCreateFolder, GetFolderName, TrashFolder, DeleteFolder, Ping + raw upload for cmd/admin only).

**Deadline:** 2026-07-27 (3 weeks)

---

### P2-4 — Nil-list dereference in `FileLifecycleAdapter.Cleanup`

**File:** `internal/infrastructure/drive/file_lifecycle.go::Cleanup`, line ~260

**Problem:** The `for _, f := range res.Files` loop has no `if res == nil` guard before iterating. `ListFiles` and `SearchFiles` in `uploader_ops.go` both have `if list == nil { return ErrDriveListNil }` guards. Same Drive API, same nil-edge-case risk, no guard here.

**Fix:** Add `if res == nil || len(res.Files) == 0 { return result, nil }` before the loop. 1-line surgical fix per godlike/07 minimum-blast-radius.

**Deadline:** 2026-07-13 (1 week, quick win)

---

### P1-5 — voiceover `useCasePublisherAdapter` → `delivery.Publisher`

**File:** `internal/app/adapters_voiceover_publisher.go`

```go
// TODO(Fase 3.5): migrate from drive.Admin.UploadFile to delivery.Publisher.Publish.
```

**Fix path:**
1. Replace `admin.UploadFile(ctx, localPath, folderID, filename)` with `publisher.Publish(ctx, delivery.PublishRequest{Destination: delivery.DestinationVoiceover, ...})`.
2. This is the last application-layer caller of the raw upload path — closure unblocks P1-3 Admin-port tightening.

**Deadline:** 2026-07-20 (2 weeks)

---

### P1-6 — `DocClient` Pattern 0 port

**File:** `internal/infrastructure/drive/doc_client.go`

**Problem:** `DocClient` interface is declared in the infrastructure package. Application code that creates Google Docs must import `internal/infrastructure/drive` (violates Pattern 8 "API package: thin transport only"). No equivalent exists in `internal/application/assets/delivery/`.

**Fix path:**
1. Create `internal/application/assets/delivery/doc_client.go` with `type DocPublisher interface { CreateDoc(...) (*Doc, error); UpdateDoc(...) error }`.
2. `drive.DocClientImpl` satisfies it structurally (add compile-time assertion).
3. Wire through composition root (`DriveBundle.DocPublisher` field).
4. Migrate callers (`wire_script_adapters.go::docCreatorImpl`, `lessons/service.go`) to use the port.

**Deadline:** 2026-07-27 (3 weeks)

---

## §4 — Execution Order

```
Week 1 (by 2026-07-13):
  ├── P2-4: Nil-list deref in Cleanup (quick win, 1 line)
  └── P0-1: DRY folder lookup unification (high-risk, deep surgery)

Week 2 (by 2026-07-20):
  ├── P0-2: Store.UploadToDrive → delivery.Publisher (caller migration)
  └── P1-5: voiceover → delivery.Publisher (last raw upload caller)

Week 3 (by 2026-07-27):
  ├── P1-3: Admin port tightening (depends on P0-1 + P1-5 completing)
  └── P1-6: DocClient Pattern 0 port
```

**Dependency chain:** P0-1 (folder lookup) → P0-2 (Store migration, needs canonical folder path) → P1-3 (Admin tightening, needs all callers migrated). P1-5 is a prerequisite for P1-3. P2-4 and P1-6 are independent.

---

## §5 — Verification Gates (per-PR)

Each PR lands **directly on `main`** per AGENTS.md Git-Lesson-2. Verification:

```bash
# Per ogni PR:
gofmt -l <touched_files>          # must be empty
go vet ./internal/infrastructure/drive/...  # must exit 0
go build ./internal/infrastructure/drive/... # must exit 0
go test -short -count=1 ./internal/infrastructure/drive/...  # must PASS
```

Pre-existing build-issue carry-forward (`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`) unchanged per convention.

---

## §6 — Cross-References (godlike/06 SSOT)

- **Parent audit:** `AGENTS.md` section "DRIVE-UPLOAD-ANALYSIS-2026-07-06" (to be added in lockstep commit)
- **Wave-tracker:** `architecture/current.yaml#DRIVE-UPLOAD-ANALYSIS-2026-07-06` (to be added)
- **Related waves:** `ART-002` (Artlist composition), `DRIVE-005` (canonical ports), `PR-VO-SUBFOLDER` (PathBuilder incomplete override)
- **Pre-existing build issues:** `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`

---

## §7 — Honest Limitations (godlike/07)

1. **Static analysis only** — no `git log --since=90.days` frequency measurement. Cross-validation deferred to forward-pointer `PR-DRIVE-UPLOAD-HOTSPOT-CROSSREF` (deadline 2026-08-01).
2. **`Store` has unknown callers** — `rg '\.UploadToDrive\('` audit required before P0-2 can estimate actual migration effort.
3. **`Admin` port tightening** may uncover callers in `cmd/admin/` that legitimately need raw upload (e.g. `reset_video_ai.go`). These are intentionally kept as raw-admin exceptions.
4. **Pre-existing build issues** carry forward unchanged per convention.

---

## §8 — Per-Commit Execution Checklist

```
☐ P2-4  → file_lifecycle.go nil-guard (1 line)
☐ P0-1  → folder_manager.go: extract lookupFolderCanonical
☐ P0-1  → admin.go: migrate to lookupFolderCanonical + add P0.7 re-lookup
☐ P0-1  → uploader_ops.go: align lookupFolderExact to canonical helper
☐ P0-2  → audit all .UploadToDrive callers (rg sweep)
☐ P0-2  → migrate each caller to delivery.Publisher.Publish
☐ P0-2  → remove nil-uploader silent-success path
☐ P1-5  → voiceover adapter: Admin.UploadFile → Publisher.Publish
☐ P1-3  → deprecate TrashFile/DeleteFile/RenameFile on Admin
☐ P1-3  → migrate callers to FileLifecycle
☐ P1-6  → create delivery.DocPublisher port
☐ P1-6  → wire through composition root + migrate callers
☐ DRIVE-UPLOAD-HOTSPOT-CROSSREF → git-log frequency cross-validation
```

---

**godlike/06 3-surface lockstep:** this action plan ≡ `CHANGELOG.md ## Unreleased → ### Documentation` entry ≡ `architecture/current.yaml#DRIVE-UPLOAD-ANALYSIS-2026-07-06` (wave-tracker anchor) ≡ `AGENTS.md §DRIVE-UPLOAD-ANALYSIS-2026-07-06` mirror entry.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
