# Audit — Cross-capability imports in `internal/application/` (June 2026)

**Status**: discovery ✅ + classification ✅ + refactor plan drafted.
**Authority**: governance rules = AGENTS.md §"Instructions" + Pattern 0
(port abstraction layer, June 2026) + AGENTS.md §"Modular edit patterns"
(Pattern 7 "Reusing existing services").

**Scope**: every direct import between two distinct `internal/application/X/`
subtrees, where X ≠ Y. Excluded: test files (`*_test.go`) — they legitimately
consume concretes for fixture building.

> **Canonical layering rule** (AGENTS.md Pattern 0, condensed):
> ports belong to the *consumer* (`internal/application/<consumer>/ports.go`),
> adapters belong to the *producer* (`internal/infrastructure/<feature>/`),
> composition happens in `internal/app/<feature>_adapters.go` with a
> compile-time `var _ <Port> = (*Adapter)(nil)` assertion. Per-package
> `shared` is forbidden.

---

## 1. Findings — 16 cross-capability imports classified

| # | Source file | Target package | Symbol(s) used | Verdict |
|---|-------------|----------------|----------------|---------|
| 1 | `internal/application/scripts/ports/voiceover_group_port.go` | `voiceover` | `voiceover.GroupsResolver`, `voiceover.ErrGroupNotFound` (inside `voiceoverGroupsAdapter`) | ✅ **PORT** — canonical Pattern 0 adapter; the consumer (scripts) declares the port and wraps the producer concrete here. |
| 2 | `internal/application/scripts/adapters/processor_voiceover.go` | `voiceover` | `*voiceover.Service`, `*voiceover.VoiceoverResult`, `*voiceover.DestinationRequest`, `voiceover.SanitizeBasename`, `voiceover.ErrGroupNotFound` | ✅ **PORT-LOCAL** — declares `VoiceoverService` port (lines 138-142) with struct-method compile-time assertion. Result types are typed (post Step 7 / M2), so the local type leak is the canonical path. |
| 3 | `internal/application/scripts/adapters/processor_voiceover_test.go` | `voiceover` | `*voiceover.Service` (test double wiring) | ✅ **TEST** — fixture wiring is exempt. |
| 4 | `internal/application/scripts/adapters/processor_images_voiceover_test.go` | `voiceover` | `*voiceover.Service` (test double wiring) | ✅ **TEST** |
| 5 | `internal/application/scripts/usecase/scene_builder_usecase.go` | `images`, `voiceover` | `*images.Service`, `*voiceover.Service`, `*voiceover.GroupsResolver` | ⚠️ **PLACEHOLDER** — file docstring states: "`Scripts.NewScenesService` was deleted from origin/main and will be re-introduced when the scene pipeline is re-constituted. Until then this stub returns an empty struct so the use case compiles without producing fake availability for the actual scene pipeline." The use case is a stub container; the concretes are field references that reach no business logic. **Action**: defer to scene pipeline re-constitution ticket; do NOT refactor in-place. |
| 6 | `internal/application/scripts/jobs/job_helpers.go` | `clips`, `voiceover` | `clips.ExtractDriveFolderID`, `*voiceover.Service`, `*voiceover.GroupsResolver`, `*voiceover.DestinationRequest`, `voiceover.ErrGroupNotFound` | ⚠️ **VIOLATION** — production helper called from job handler; uses concretes for both target packages. |
| 7 | `internal/application/scripts/jobs/voiceover_destination_test.go` | `voiceover` | `*voiceover.Service` (fixture) | ✅ **TEST** |
| 8–12 | `internal/application/assets/monitor/{job_handler,process_video,monitor_channel_check,channel_monitor,monitor_scheduler}.go` (5 files, one alert per file) | `channels` | `*channels.Service`, `channels.Channel`, `channels.{MarkCheckedCommand, UpdateCursorCommand, ClaimDueCommand, Channel}` | ⚠️ **VIOLATION** — production-grade wire (5 files, no port declared). High blast radius — concrete `*channels.Service` field on `ChannelMonitor`. Genuine cross-capability coupling. |
| 13 | `internal/application/assets/monitor/channel_monitor.go` | `youtube/usecase` | `*youtube.Service` (aliased as `youtube`) | ⚠️ **VIOLATION** — direct concrete injection of YouTube service into monitor. **Pair**: same-direction as Alerts 8–12 above (assets → channels AND assets → youtube). |
| 14 | `internal/application/assets/monitor/process_video.go` | `youtube/dto` | `yttypes.ExtractRequest`, `yttypes.DestinationRequest` (aliased as `yttypes`) | ⚠️ **VIOLATION** — direct DTO dependency on YouTube pipeline. Lower blast radius (DTOs only, no service), but the spec leak is real: monitor → youtube domain boundary. |
| 15 | `internal/application/books/service.go` | `voiceover` | `*voiceover.Service` (constructor + setter `SetVoiceoverService`) | ⚠️ **VIOLATION** — direct concrete field; constructor + setter both pinpoint the coupling. |
| 16 | `internal/application/books/drive.go` | `voiceover` | `*voiceover.Service`, `*voiceover.VoiceoverResult`, `*voiceover.DestinationRequest` (call sites: `GenerateWithDestination`, `Generate`) | ⚠️ **VIOLATION** — same root cause as Alert 15; refactor must include both files in lockstep. |
| 17 | `internal/application/assets/monitor/{process_video,channel_monitor}.go` | `jobs` | `*jobtools.Service` (aliased `jobtools` → `internal/application/jobs`) | ⚠️ **BORDERLINE** — `jobs.Service` is treated as a contract enforcer ("broker" role). Acceptable in most cross-capability cases, but flagged here for completeness. **Action**: keep but document; a `JobEnqueuer` port can be lifted at Wave X if other consumers repeat the pattern. |

### 1.1 Direction pairs explicitly flagged by the audit task

| Pair | Count | Verdict summary |
|------|-------|-----------------|
| `application/scripts → application/images` | 1 (Alert 5, placeholder) | defer to scene pipeline re-constitution |
| `application/scripts → application/voiceover` | 7 (Alerts 1, 2, 3, 4, 5, 6, 7) | 1 production-violation (Alert 6); 1 placeholder (Alert 5); 2 ports (Alerts 1, 2); 3 tests (Alerts 3, 4, 7) |
| `application/assets → application/youtube` | 2 (Alerts 13, 14) | 2 violations (service + DTO) |
| `application/channels → application/assets` | 0 — **direction not present in code** | The reverse (assets → channels) is what's actually there; the user's expected pair is "channels → assets", which is **empty**. Document this asymmetry in `architecture/ownership.yaml` owner-table. |

---

## 2. Refactor plan per violation (one logical commit = one PR-style push)

### Refactor 1 — `scripts/jobs/job_helpers.go` → introduce `ClipsFolderExtPort` + extend `VoiceoverGenerator`

**Scope**: `internal/application/scripts/jobs/job_helpers.go` only.

**Changes**:
1. Add a narrow port `ClipsFolderExtPort { ExtractDriveFolderID(string) string }` in `internal/application/scripts/jobs/ports.go` (consumer-side ownership per AGENTS.md Pattern 0: `scripts/jobs` is the consumer of clips, so jobs owns the port; `clips` is the producer that supplies the adapter wired in `internal/app/wire_script.go`). **Why not in `internal/application/clips/ports.go`**: that file is already producer-owned (its file docstring declares ports that **clips** consumes — e.g., `ClipDriveUploaderPort`, `ClipRepositoryPort`) and adding a "consumer-from-jobs" port there would invert the layering rule.
2. Extend `voiceover.VoiceoverGenerator` in `voiceover/ports.go` to include `GenerateWithDestination(ctx, text, language, filename, *DestinationRequest) (*VoiceoverResult, error)`. This is the canonical voiceover surface and is already partly abstracted (Step 7 M2 typed-port).
3. Refactor `job_helpers.go::BuildVoiceoverDestination` and `GenerateSceneVoiceovers` to depend on these ports instead of the concretes.
4. Wire the adapters in `internal/app/wire_script.go` (already exists, the adaptation site).
5. Add a roundtrip test asserting the destination comes out of the port-driven path.

**Risk**: medium — touches one production helper, but no behaviour change. Drift risk low because the new ports keep existing method signatures.

**Push**: separate commit on `main` ("refactor(scripts): ports for clips+voiceover helpers").

### Refactor 2 — `assets/monitor/{5 files}` → introduce `MonitorChannelsPort` + `MonitorYouTubePort` + `MonitorJobsPort`

**Scope**: 5 files in `internal/application/assets/monitor/`.

**Changes**:
1. Declare three ports in `internal/application/assets/monitor/ports.go`:
   - `MonitorChannelsPort` — subset of `*channels.Service` the monitor actually uses (GetByID, ListEnabled, ClaimDue, MarkChecked, UpdateCursor, plus the typed `Channel` DTO + the four `*Command` DTOs).
   - `MonitorYouTubePort` — subset of `*youtube.Service` + DTOs (`ExtractRequest`, `DestinationRequest`) used in `enqueueClipExtract`.
   - `MonitorJobsPort` — `Enqueue(*job.EnqueueRequest) (*job.Job, error)` (already abstracted in `scripts/ports/ports.go` as `JobEnqueuer` — **reuse** instead of redeclare).
2. Wrap the concrete services in three adapters in `internal/app/monitor_adapters.go` (or similar) with `var _ MonitorChannelsPort = (*monitorChannelsAdapter)(nil)`.
3. Change `ChannelMonitor` struct fields to use the ports; constructor arg `youtubeSvc *youtube.Service` → `ytService MonitorYouTubePort`.
4. Add adapter-boundary tests.

**Risk**: HIGH — 5 files, scheduler-tick correctness, job-handler contract preservation. **Recommend**: ticket this with explicit owner + deadline + closure criteria, NOT bundle in a single commit. Spread across 3 commits:
   - commit A: introduce ports + adapters (no behavior change)
   - commit B: swap concretes to ports in `ChannelMonitor` struct (compile passes only)
   - commit C: update constructor + wiring in composition; re-run `bash scripts/ci-architectural-checks.sh` and `go vet ./...`.

**Push**: 3 commits on `main`, each independently `go build`-clean.

### Refactor 3 — `books/{service,drive}.go` → depend on `voiceover.VoiceoverGenerator` + `voiceover.DriveUploaderPort`-equivalent

**Scope**: `internal/application/books/{service,drive}.go`.

**Changes**:
1. Reuse `voiceover.VoiceoverGenerator` (need to widen to cover `GenerateWithDestination` — same move as Refactor 1 step 2, but books/dive.go uses it. Coordinate so the widen is a single atomic PR).
2. Change `books.Service.voiceoverSvc` field type from `*voiceover.Service` to `voiceover.VoiceoverGenerator`.
3. Constructor + setter updated; composition wiring in `internal/app/books_adapters.go` (or wherever books is wired) adjusted.
4. Roundtrip test asserting `ProcessBookFromDrive` call site still triggers voiceover generation.

**Risk**: low (2 files, narrow surface).

**Push**: 1 commit on `main` after Refactor 1.

### Refactor 4 — `scripts/usecase/scene_builder_usecase.go` → DO NOT refactor

Per docstring, this is an explicit placeholder until scene pipeline re-constitution. Add to the wave tracker as `architecture/current.yaml::follow_up_tickets.scene_pipeline_reconstitution` so it doesn't fall through silently.

---

## 3. Test coverage additions (one test per refactor)

- Refactor 1 test: `internal/application/scripts/jobs/job_helpers_test.go` covering:
  - destination routes through port (folder-id non-empty → direct folder)
  - destination routes through port (folder-id empty, group non-empty)
  - GenerateSceneVoiceovers counts successes through port
- Refactor 2 tests: `internal/application/assets/monitor/adapters_test.go` covering:
  - MarkCheckedCommand + UpdateCursorCommand pass-through (channel scope)
  - ExtractRequest DTO pass-through (YouTube scope)
- Refactor 3 test: `internal/application/books/service_test.go` covering:
  - ProcessBookFromDrive with `GenerateVoiceover=true` exercises the port
  - ProcessBookFromDrive with nil voiceover port completes with `VoiceoverError`

---

## 4. Out-of-scope followups (for visibility)

- `architecture/ownership.yaml` is still missing an entry for the `assets/monitor → channels` direction; add a row so the asymmetry (no `channels → assets`) is documented.
- The `ChannelMonitor.youtubeSvc *youtube.Service` field is a TWO-CAPABILITY coupling in one struct; widening Pattern 0 with a `MonitorYouTubeCapabilityPort` (separate from a possibly-existing `youtube.ClipExtractorPort` if any future refactor extracts one).
- `books` itself contains a hardcoded `database/sql` import — already a separate P1-8 ticket ("Sposta SQL sotto infrastructure/database") from the plan-of-action. **Do not confuse with audit Alert 8** in §1 above (which is `monitor/job_handler.go → channels`). Defer cross-capability refactor of `books` until the P1-8 SQL-pushdown ticket closes (otherwise we'd refactor a layer that's about to be emptied).
- `assets/monitor/process_video.go::enqueueClipExtract` indirectly reaches the YouTube pipeline via `yttypes` DTOs; today it bridges through `m.jobsSvc.Enqueue` so the actual YouTube call is downstream. The DTO leak is the cleanup target, not the indirect enqueue.

---

## 5. Status snapshot at the time of this audit

- `internal/application/voiceover/ports.go` — 3 ports declared ✅
- `internal/application/clips/ports.go` — 10+ ports declared ✅
- `internal/application/images/ports.go` — 1 port declared ✅
- `internal/application/youtube/ports/ports.go` — 12 ports declared ✅
- `internal/application/scripts/ports/ports.go` — 2 ports declared ✅
- `internal/application/assets/ports.go` — 3 ports declared ✅
- `internal/application/books/ports.go` — **MISSING** — book capability has NO ports. Refactor 3 + books sql pushdown will introduce the first one.

**Net refactor backlog (alphabetical by capability consumer)**:
- `assets/monitor` → 3 ports to introduce + 5 files to swap + 3 commits (HIGH risk)
- `books` → 1 port to consume + 2 files to swap + 1 commit (LOW risk)
- `scripts/jobs` → 1 port to consume (Clip extraction) + 1 port widening (VoiceoverGenerator) + 1 file to swap + 1 commit (MEDIUM risk)
- `scripts/usecase` → DEFER (placeholder, not refactor-worthy)

Total commits: **5 commits** for the full cross-capability cleanup. Each commit independently `go build` + `go vet` + `bash scripts/ci-architectural-checks.sh` clean.

---

## 6. Verification checklist (for the final commit of this bundle)

- [ ] `go build ./...` green
- [ ] `go vet ./...` green
- [ ] `bash scripts/ci-architectural-checks.sh` green (Check 1: context.Background; other capabilities unchanged)
- [ ] No new test failures in `go test ./internal/... ./pkg/... -short`
- [ ] `rg '"database/sql"' internal/application/internal/api internal/domain` — count non-decreasing (Refactor 3 will move `books` off this list when paired with P1-8)
- [ ] `rg 'internal/application/(scripts|images|voiceover|assets|youtube|channels)' internal/application` — count should drop by at least 4 (Alert 1 stays since it's the canonical port); target = drops below 10 post-Refactor-2
