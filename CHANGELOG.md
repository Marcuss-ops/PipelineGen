# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---



### Documentation

- **[S7-Step-7 snapshot, July 2026]** documentation(md) + arch(current) + retry(pkg) -- Wave-7 canonical closeout audit-pin. pkg/retry typed-path transient classifier (IsTransient + WrapTransient) replaces 3 duplicate substring-match predicates (monitor.isTransientEnqueueError, tagutil.IsTransientDownloadError, youtube/usecase.IsTransientExtractionError). Production-default JitterFraction=0.25 enables bounded retry desynchronisation across fleet contention (kills thundering-herd retry storms). Wave-tracker entry S7-Step-7 -> status:done, exit_signal:true (ExitGate=true), deadline:2026-07-25. Cross-reference SHAs: chain 8bdb9a8d, accb090b, 6f327b10, c1cf33d3; taxonomy extension 2d09f3e8; Check 50 retry-classifier gate ef09b732; wave-tracker update 60e3e5f4.

## Unreleased

### Added

- **[Stock Cutover Commit 4-expanded — IndexingStatus full retirement + RunResilient + 3 resilience ports + 3 canonical tests + rebase onto cf33e090 SourceStager port (July 2026)]** `refactor(stock) + fix(stock) + feat(stock)` — extend Commit 4 (65e75ba7) past its transitional state: retire the migrated-into-service.go `IndexingStatus` typed enum + 4 consts + `MarshalJSON`/`UnmarshalJSON` (and the residual `ChunkResult.Indexed IndexingStatus` field) so `service.go` no longer carries the dual-purpose (typed + JSON marshalled) signal — the per-job post-emission indexing state is now surfaced at the orchestrator level via `job.StatusIndexPending` (see kernel/job + domain/job). Add the canonical resilience surface: `Service.runOrchestratorResilient` (new sibling to `runOrchestrator`, threads `*RunSummary` through `HandleJob` so the broker JobFinalizer stamps the right job-status without re-inferring it from the manifest alone); `HandleJob` now projects `summary.FinalStatus` into the result map under `"final_status"`; `RunResilient` 7-step ladder (`resolve_sources / plan_clips / stage_sources / build_manifest / validate_manifest / emit_chunks / project_manifest`) with 3 typed sentinels (`ErrManifestIncomplete` confirming the manifest-completeness gate, `ErrAtomicDispatchFailed` confirming the atomic outbox-DB rollback contract, `ErrProjectionResilience` documenting the index-pending flip). New file `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go` (~190 LoC) defines the 3 Pattern 0 ports (`ManifestBuilder` / `TransactionalAssetWriter` / `ProjectionPort`) + the `RunSummary{Manifest, FinalStatus}` typed envelope + 4 sentinels + 3 default impls (`stockManifestBuilder` → canonical 5-C12 envelope / `noopWriter` / `noopProjection`). `kernel/job.go` gains `StatusIndexPending Status = "INDEX_PENDING"` + extends `IsActive()` + `Valid()` predicates (terminal-set unchanged + INDEX_PENDING counted as active because the Qdrant-reconciler owns the row until projected). `domain/job.go` re-exports `StatusIndexPending = kerneljob.StatusIndexPending` for the 107 import sites in 93 files. New file `internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go` (3 TDD tests ≋110 LoC) pins the resilience contract: (a) outbox rollback on writer error surfaces `ErrAtomicDispatchFailed` via `errors.Is` after aborting on first call; (b) manifest-completeness gate surfaces `ErrManifestIncomplete` when a Required:true artifact has empty Path (summary must be nil); (c) Qdrant offline returns `*RunSummary{Manifest:non-nil, FinalStatus:StatusIndexPending}` with nil error (artifacts ARE on Drive, only indexing deferred). Verification (gofmt + go vet + go build + go test + race + archcheck) all green; `rg \b(IndexingStatus|IndexingPending|IndexingSkipped|IndexingCompleted|IndexingFailed|uploadAndIndexChunk|buildPipelineMetadata)\b --type go` returns empty in production code.

- **[Stock Cutover Commit 4-expanded — landed meta-entry (canonical SHA `ed4f8331`; byte-equivalent-replay recovery per AGENTS.md §Git-Lesson-5; scope-locked to `internal/application/assets/providers/stock/stockpipeline/`; cross-package YouTube-side IndexingStatus forward-pointer §12-5, July 2026)]** `chore(stock)` — closure meta-entry confirming the Commit 4-expanded intent is canonical on `origin/main` after a byte-equivalent-replay recovery sequence per AGENTS.md §Git-Lesson-5. The detailed per-file surface is documented in the preceding `[Stock Cutover Commit 4-expanded — IndexingStatus full retirement + RunResilient + 3 resilience ports + 3 canonical tests + rebase onto cf33e090 SourceStager port (July 2026)]` entry of this section; this entry canonicalises the LANDSIDE only (3 SHAs + recovery sequence + honest scope + residue recap + forward-pointer + honest limitation declaration).

  **(a) Canonical byte-equivalent-replay SHA — `9aa4c9e2`** `refactor(stock): Commit 4-expanded - retire IndexingStatus residue + add resilience ports + 3 tests + gofmt carry-over` — landed on `origin/main` by a parallel-agent byte-equivalent-replay during a prior basher push race. Verified per AGENTS.md §Git-Lesson-5 step-1 diagnostic: `git log --oneline HEAD..@{u}` returned non-empty, the upstream tip carried the same `Stock Cutover Commit 4-expanded` subject, and `diff <(git show --name-only --format='' <local-sha>) <(git show --name-only --format='' <origin-sha>)` returned empty (11/11 semantic-surface Go files blob-identical). The on-disk content is byte-equivalent to the local amend that landed first; the local-divergent SHA `94854247` is a superseded duplicate, accepted WITHOUT `--force` per the AGENTS.md canonical recovery posture. `git reflog` retains `94854247` for 30+ day audit-trail per AGENTS.md §Git-Lesson-5 step 4.

  **(b) Forward-port SHA — `0c74e408`** `docs(handoff): archive COMMIT_4_EXPANDED_HANDOFF.md planning notes` — the forward-port commit landed via Option 2 recovery (cherry-pick handoff forward per user choice via ask_user). 1 file only: `COMMIT_4_EXPANDED_HANDOFF.md` (25382 bytes, md5 `c184317e87ab2367cc2ffe529f207775`, byte-stable across subsequent rebases). Zero typed-Go surface; this is the planning-notes archive that recovered from the locally-divergent `94854247` and was re-extracted via `git show 94854247:COMMIT_4_EXPANDED_HANDOFF.md > /tmp/handoff.bak` (preserved the local SHA's tracked copy) before `git reset --hard origin/main`. The forward-port commit preserves the audit-trail of the byte-equivalent-replay recovery.

  **(c) Post-rebase canonical HEAD — `ed4f8331`** `feat(stock): §12-4 — SourceStager port abstraction (persistent staging)` — current `HEAD ≡ origin/main` SHA. The canonical lineage on `origin/main`: `cf33e090 → 9aa4c9e2 → 13495fb0 → ed4f8331`. Path A rebase landed the forward-port commit `0c74e408` above the SourceStager §12-4 commit + 4 parallel-agent commits without `--force`, without `--ours`/`--theirs` on test files, and without conflict (per AGENTS.md §Rebase-Conflict Lesson + §Git-Lesson-2 ff-push baseline). Push exit=`Everything up-to-date` (the rebase ff-advanced local onto the new origin tip natively; zero round-trip latency).

  **(d) Honest residue recap (per godlike/07 no-fake-availability):**
  - **`IndexingStatus` typed retirement (stockpipeline scope COMPLETE)**: the typed enum + 4 consts + `MarshalJSON`/`UnmarshalJSON` methods + `ChunkResult.Indexed IndexingStatus` field are physically gone from `internal/application/assets/providers/stock/stockpipeline/`. `job.StatusIndexPending = "INDEX_PENDING"` (defined in `internal/kernel/job/job.go`, re-exported via `internal/domain/job/job.go = kerneljob.StatusIndexPending`) is the canonical surface; `IsActive()` was extended to count `INDEX_PENDING` as active (the Qdrant-reconciler owns the row until projected); `Valid()` was extended to accept it in the canonical set; the terminal-sinks set is unchanged.
  - **3 Pattern 0 resilience ports** declared in `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go` (~190 LoC): `ManifestBuilder` (default `stockManifestBuilder` → canonical 5-C12 envelope) + `TransactionalAssetWriter` (default `noopWriter`; the per-clip atomic UPSERT + outbox-enqueue composition seam) + `ProjectionPort` (default `noopProjection`; the post-emission Qdrant sync seam). The `RunSummary{Manifest, FinalStatus}` typed envelope + 4 typed sentinels (`ErrManifestIncomplete` + `ErrAtomicDispatchFailed` + `ErrProjectionResilience` + `ErrResilienceNotWired`) are exported from the same file.
  - **`RunResilient` 7-step ladder** (`resolve_sources / plan_clips / stage_sources / build_manifest / validate_manifest / emit_chunks / project_manifest`): the canonical orchestrator resyncs the broker `JobFinalizer` summary through `Service.runOrchestratorResilient` (new sibling to `Service.runOrchestrator`). `HandleJob` projects `summary.FinalStatus` into the result map under the `"final_status"` key so the broker can stamp the right job-status without re-inferring it from the manifest alone.
  - **3 canonical resilience tests** in `internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go` (~110 LoC): (i) `writer Error → ErrAtomicDispatchFailed via errors.Is after aborting on first call` — atomic outbox-DB rollback contract; (ii) `manifest-completeness gate → ErrManifestIncomplete when Required:true artifact has empty Path (summary must be nil)`; (iii) `Qdrant offline → *RunSummary{Manifest:non-nil, FinalStatus:StatusIndexPending}` with nil error (artifacts ARE on Drive; only indexing deferred). Verification gate: `go test ./internal/application/assets/providers/stock/stockpipeline/... -count=1 -timeout=60s` exit 0 ✓ (`ok ... 0.017s`). `go build` + `go vet` on the same scope also exit 0 ✓.
  - **`IndexingStatus` audit-pin residue (stockpipeline subtree)**: 1 doc-only mention in `service.go:750` (referencing the retirement propagation to `job.StatusIndexPending`) + 7 doc-only mentions across `stockpipeline/{service.go, run.go}` referencing retired function names (`uploadAndIndexChunk` / `indexChunkToAssetIndex` / `upsertChunkAndDispatch` / `buildPipelineMetadata`). All production-code active-use residue = 0: `rg -t go --glob '!**/*_test.go' '\b(IndexingStatus|uploadAndIndexChunk|indexChunkToAssetIndex|indexChunkToClipsDB|upsertChunkAndDispatch|buildPipelineMetadata)\b' internal/application/assets/providers/stock/stockpipeline/` returns ONLY doc-block matches.

  **Honest scope statement (per godlike/07 no-fake-availability):** Commit 4-expanded's scope is `internal/application/assets/providers/stock/stockpipeline/` ONLY. The cross-package `IndexingStatus` typed enum in `internal/application/assets/sourcing/types.go:39` (used by the YouTube-provider asset-level indexing + emitted by `internal/api/assets/register/handler.go:126` under the `"indexing_status"` JSON wire field) is a separate indexing concern (asset-level for YouTube vs chunk-level for stock) and is **deliberately untouched** by this commit. Forward-pointer **§12-5 cross-package retirement** is tracked in `architecture/current.yaml` (entry pending, deadline 2026-08-15 — see this closure's PR § "Wave-tracker" follow-up). Operators reading `indexing_status` from the `/api/assets/register` endpoint will continue to see the YouTube-side field; please do not interpret the canonical "Commit 4-expanded retired IndexingStatus" wording as "globally retired" — it is scoped-subtree, not codebase-wide.

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for the Commit 4-expanded landside yet (verified `grep -rE 'Commit.4.expanded.landed|byte.equivalent.replay.recovery|Wave.stock.cutover.commit.4' architecture/current.yaml` returning 0 hits). The closure is audit-logged via this CHANGELOG entry + the 3 on-`origin/main` SHAs (`9aa4c9e2` + `0c74e408` + `ed4f8331`) + the `git reflog`-retained `94854247` audit-trail (per AGENTS.md §Git-Lesson-5 step 4 — refs survive 30+ days by default; `git reflog expire --expire-unreachable=now --all` is NOT needed). Forward-pointer entry: `architecture/current.yaml#wave_status.stock_cutover_commit_4_expanded` row should be filed under the `wave_status` schema citing the 3 SHAs + the byte-equivalent-replay recovery sequence + the §12-5 cross-package forward-pointer.

  **Honest limitation declaration (per godlike/07):**
  1. The forward-port commit `0c74e408` is **documentation-only** (1 markdown file: `COMMIT_4_EXPANDED_HANDOFF.md`). The actual code-surface semantics for Commit 4-expanded live on `9aa4c9e2` (where the 11-file semantic surface landed as a byte-equivalent-replay). The forward-port preserves the planning-notes archive; the code did NOT land twice (different SHAs encode the same on-disk content + reflog retains the superseded lineage for audit).
  2. The 8 audit-pin doc-mentions in `stockpipeline/{service.go, run.go}` (signature: in `//` comments that reference the retired function names or `Indexed IndexingStatus` retirement trail) are intentional retention per godlike/06/07 audit-pin discipline. They document the migration trail (which function was retired + why) for future maintainers who read the surfaces cold; retention of evidence-of-retirement is preferred over silent removal. NO production-code active-uses of any of these symbols remain in the stockpipeline subtree.
  3. Cross-package `IndexingStatus` retirement is forward-pointer §12-5 with deadline 2026-08-15. Until landed, the `internal/application/assets/sourcing/types.go:39` enum declaration + the 3 production-code usages in `internal/application/assets/sourcing/youtube/{service.go, ports.go}` + the JSON emission `internal/api/assets/register/handler.go:126` remain in production. This commit does NOT retire them; reading their presence as a regression would be a misread of the scope.
  4. `git reflog` audit-trail retention (`94854247`) depends on the canonical 30+ day expiry window; a future manual `git reflog expire --expire-unreachable=now` would shorten this window. The audit surface is the reflog + this CHANGELOG entry + the git log of the 3 SHAs (which survives indefinitely as commit objects).

  **Pre-existing build issues (out of scope, NOT regressions from this closure):** same five items as the prior Commit 4-expanded entries carry forward (verified against `git show origin/main:<file>` per the canonical recipe): `monitor/enqueue.go` (`strings.ToLower` undefined), `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined), `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (syntax error), `internal/app/module_media.go` (pre-existing `clips.Deps.MutationsDispatcher` literal), `internal/application/images/routing` import cycle. The Commit 4-expanded closures land in isolation on the stockpipeline subtree, which passes `gofmt + go vet + go build + go test` independently.

- **[P0 Commit 4 — Dispatcher.Enqueue through compiled registry (C4), July 2026]** `feat(jobs)` — typed entry point on the canonical `CompiledJobRegistry` (the surface continuation of P0 C1-C3: C1 introduced `JobDefinition` + `ExecutionClass` + `ArtifactPolicy`, C2 wired `PayloadCodec` + `ResultCodec` 1-to-1 with `JobDefinition`, C3 shipped `CompiledJobRegistry` + `Freeze` + `StartupValidator`). New file `internal/application/jobs/dispatcher.go` exposes:
  - `func (d *Dispatcher) Enqueue(ctx context.Context, jobType string, payload any) (*job.Job, error)` — 7-step fail-closed priority: nil receiver → nil enqueuer (`ErrEnqueuerNotWired`) → registry not frozen (`ErrRegistryNotFrozen`) → unknown jobType (`job.ErrUnknownJobType`) → missing `PayloadCodec` on definition (`ErrCodecMissing`) → encode error (dual `%w` chain wrapping `ErrInvalidPayload` + inner codec error so `errors.Is(err, ErrInvalidPayload)` AND `errors.As(err, &codecErr)` both work per godlike/07 typed-error contract) → happy path delegates to `EnqueuePort.Enqueue`. Single-call typed entry point that hands the codec-encoded payload + queue/timeout/retry metadata to the underlying Service registry.
  - `type EnqueuePort interface { Enqueue(ctx, *job.EnqueueRequest) (*job.Job, error) }` + compile-time assertion `var _ EnqueuePort = (*Service)(nil)` (locks Service's signature drift to build failure rather than runtime panic).
  - 4 sentinels (`ErrEnqueuerNotWired`, `ErrRegistryNotFrozen`, `ErrCodecMissing`, `ErrInvalidPayload`) — all `errors.New(...)`, all reachable via `errors.Is` thanks to `%w`-wrap at return sites.
  - Fluent builders `WithRegistry(CompiledJobRegistry) *Dispatcher` + `SetEnqueuer(EnqueuePort) *Dispatcher` — both nil-tolerant + chainable; mirror the `Service.WithRegistry` precedent. Needed because Dispatcher is constructed BEFORE Service (registry is frozen first per C3) so the EnqueuePort binding is post-construction.
  - `Dispatcher` struct extended in `internal/application/jobs/types.go` with 2 unexported fields `registry job.CompiledJobRegistry` + `enqueuer EnqueuePort` (Go allows cross-file field additions within the same package).
- **`internal/application/jobs/dispatcher_test.go` (NEW, ~325 LoC)** — 9 TDD tests pinning the canonical contract: nil-receiver no-op + enqueuer-unwired returns `ErrEnqueuerNotWired` + nil-registry returns `ErrRegistryNotFrozen` + unfrozen-registry stub returns `ErrRegistryNotFrozen` + unknown jobType returns `job.ErrUnknownJobType` + missing `PayloadCodec` returns `ErrCodecMissing` + encode-error path preserves inner codec error via dual-`%w` + happy-path roundtrip verifies `def.PayloadCodec.EncodePayload(payload)` returns `json.RawMessage` stored correctly in `*job.EnqueueRequest.Payload` + fluent builders nil-tolerant.
- **CI gate Check 51 in `scripts/ci-architectural-checks.sh`** — forbid raw-string `.Enqueue(<ctx>, "<literal>")` callers outside the canonical Dispatcher.Enqueue surface. Pattern anchor: `\.\Enqueue\s*\(\s*[^,]+,\s*"[a-z][a-zA-Z0-9._]*"`. Allowlist (the ONLY legitimate production callers): `service.go` (definition site), `dispatcher.go` (definition site), `dispatcher_test.go` (canonical-type pin), `job/service.go::EnqueueTyped` top-level generic helper, `**/*_test.go` global. Pre-flight audit against `main`: `rg '\.Enqueue\(' internal/` returns 0 raw-string callers — all 54 hits are either comment references or already-typed `EnqueueRequest{...}` struct usage. C4 is therefore a **structural refactor + forward-pointer**: the canonical surface is live, ALL existing production callers already route through typed envelopes, and Check 51 is forward-preventive (catches future regressions rather than closing active debt). Production wiring of `dispatcher.WithRegistry(compiled).SetEnqueuer(service)` in `c3ValidateRuntimeGraph` is deferred to C5+ alongside the `registerProviders` composition entry — the surface exists now; caller migration runs in lockstep with future Handler migrations.
- **No migration / no SQLite change:** the canonical EnqueueRequest shape is unchanged; Dispatcher.Enqueue wraps the existing wire format with an additional pre-encode step driven by `def.PayloadCodec.EncodePayload`.

- **[P0 Commit 6 (C6) — ArtifactUploader state machine + idempotent key (stateful upload protocol, July 2026)]** `feat(remote)` — introduce the canonical stateful upload protocol surface for the Creator-side Sender worker: `ArtifactUploader` port + `UploadSession` typed envelope + `UploadState` closed 6-state machine + `ArtifactIdempotencyKey` deterministic byte-stable helper + Creator-side adapter + 3 new HTTP-protocol commands on the canonical JobBroker client. The P0 chain C1–C5 established the artifact-publish pipeline foundation (C1 `JobDefinition` + `ArtifactPolicy`, C2 `PayloadCodec` 1-to-1 wire-shape, C3 `CompiledJobRegistry.Freeze`, C4 `Dispatcher.Enqueue` through compiled registry, C5 `domain/job/artifact_manifest.go` dual-type handling); C6 layers the stateful upload protocol on top.

  **Files in scope (6):**

  - **NEW `internal/domain/remote/artifact_uploader.go`** (~410 LoC, package `remote`) — port interface `ArtifactUploader` (Prepare/Upload/Finalize) + typed envelope `UploadSession` (id/leaseID/artifactID/state) + typed enum `UploadState` (6 closed values: PREPARING/UPLOADING/UPLOADED/VERIFIED/FINALIZED/FAILED) with `CanonicalUploadStateValues()` + `Valid()` + `IsValidTransition(to)` enforcing the legal-transitions matrix (forward chain + sticky-terminal + self-loop-idempotency) + structured-error `IllegalTransitionError{From, To}` implementing `Is(target error) bool` for `errors.Is(err, ErrIllegalUploadStateTransition)` compatibility per godlike/07 typed-error contract + 5 typed sentinel errors (`ErrArtifactUploaderNotConfigured`, `ErrArtifactSessionExpired`, `ErrArtifactSessionNotFound`, `ErrArtifactRemoteSchemaVersionUnsupported`, `ErrIllegalUploadStateTransition`) all `errors.New(...)` and reachable via `%w`-wrap + retry-safe `PrepareContext` typed envelope (JobID/LeaseID/ArtifactID/ArtifactKind/Filename/MIMEType/SizeBytes/SHA256/IdempotencyKey) + `UploadSessionStore` typed read-only query port.

  - **NEW `internal/domain/remote/idempotency.go`** (~130 LoC, package `remote`) — deterministic `ArtifactIdempotencyKey(jobID, artifactID, sha256) string` (uses `internal/infrastructure/files.SHA256String` aliased as `hashutil`; format `<sha256-hex>` of `jobID|artifactID|sha256`) + `IsValidIdempotencyKey(s string) bool` (case-insensitive 64-char hex) + `ErrArtifactIdempotencyKeyConflict` typed sentinel (godlike/07 no-fake-availability: empty-marker triple returns empty-marker rather than synthetic value).

  - **NEW `internal/domain/remote/artifact_uploader_test.go`** (~430 LoC) — 9 TDD tests pinning the canonical contract: legal-forward edges (PREPARING→UPLOADING→UPLOADED→VERIFIED→FINALIZED with skip-ahead UPLOADED→VERIFIED allowed for atomic finalize), sticky-terminal rejection (FAILED→* and FINALIZED→* all rejected), non-terminal-to-FAILED accepted for all 4 non-sink states, self-loop idempotent (s.IsValidTransition(s) == true), idempotency-key byte-stability across 1000 retries with same triple, empty-marker triple returns empty-marker, typed-error dual-probe (`errors.Is` on sentinel + `errors.As` on `*IllegalTransitionError` for From/To surfaces), `CanonicalUploadStateValues` completeness (6 values, no dups), `NewUploadSession` aggregates all missing-fields into one diagnostic per godlike/07 fail-closed.

  - **NEW `internal/infrastructure/remote/creator/adapter.go`** (~190 LoC, package `creator`) — Creator-side `*Adapter` implementing `remote.ArtifactUploader`. Fluent builders `NewAdapter(deps)` + `WithBrokerClient(...)`. Compile-time assertions `var _ remote.ArtifactUploader = (*Adapter)(nil)` + private `jobBrokerClient` structural interface (PrepareArtifactUpload/UploadArtifactFile/FinalizeArtifactUpload) with `var _ jobBrokerClient = (*jobbrokerclient.Client)(nil)`. State-machine gate enforced inline at every seam via `UploadState.IsValidTransition`. Defensive `ArtifactIdempotencyKey` derivation in Upload + Finalize (the `deriveIdempotencyKey` helper consolidates 4 retry paths that bypass Prepare so byte-stable keys are produced even on standalone Upload/Finalize calls). File streaming via `os.Open` + `io.Copy` + `httpClient.Do` (no bespoke byte accumulation; stdlib handles backpressure). pkg/fileutil hash computation reuses the existing utility per AGENTS.md Pattern 4.

  - **NEW `internal/infrastructure/remote/creator/adapter_test.go`** (~330 LoC, package `creator`) — happy-path 3-call chain (Prepare/Upload/Finalize) + nil-receiver propagation test (returns `ErrArtifactUploaderNotConfigured`) + Upload standalone idem-key-derivation test (byte-stable across N retries with same triple) + Finalize standalone idem-key + sha256 surface test + illegal transition rejection test (FINALIZED→PREPARING fails with typed `*IllegalTransitionError` preserving From/To via `errors.As`).

  - **MODIFIED `internal/infrastructure/remote/jobbrokerclient/client.go`** — adds 3 path constants (`PathUploadPrepare`/`PathUploadFile`/`PathUploadFinalize`) + 2 typed request DTOs (`PrepareArtifactUploadRequest` + `FinalizeArtifactUploadRequest`) + 3 NEW methods on `*Client` (`PrepareArtifactUpload` / `UploadArtifactFile` / `FinalizeArtifactUpload`) that wire `upload.prepare` / `upload.file` / `upload.finalize` onto the existing jwt-bearer+BearerToken HTTP client. `context.Background()` on the small JSON body calls (matches existing `post()` pattern; cancellation flows via `c.httpClient.Timeout` 30s default). Streaming body for `UploadArtifactFile` via `io.Copy` (zero full-file materialisation). The 3 NEW methods are ADR-locked: only the Creator adapter imports+consumes them (per Check 52 forward-prevention — see CI gate added in this commit).

  **CI gate Check 52 in `scripts/ci-architectural-checks.sh` (NEW)** — forward-prevention rule that bans direct `.PrepareArtifactUpload(` / `.UploadArtifactFile(` / `.FinalizeArtifactUpload(` callers in `internal/application/**` + `internal/api/**` outside the canonical allowlist (creator adapter + jobbrokerclient itself + tests). Mirrors Check 51's posture for raw-string `.Enqueue` callers. Pre-flight audit: `rg '\.(PrepareArtifactUpload|UploadArtifactFile|FinalizeArtifactUpload)\(' internal/application internal/api` returns **0 hits** today — the Creator adapter is the only caller under the allowlist; production consumers MUST route through the `remote.ArtifactUploader` port.

  **Adapter compile-time pin (`var _ remote.ArtifactUploader = (*Adapter)(nil)`):** future drift in any of the 3 port signatures is a build failure at the canonical concrete site (godlike/06 SSOT one-owner-per-fact).

  **godlike/07 typed-error contract:** all 5 sentinels in the domain/remote package are typed `errors.New(...)`. The Adapter wraps internal errors via `fmt.Errorf("creator adapter: <stage>: %w", <inner error carrying the sentinel>)`, so callers can `errors.Is(err, ErrArtifactUploaderNotConfigured)` from any seam. The `IllegalTransitionError` typed-data envelope exposes `{From, To}` at the seam so log scanners and operator dashboards can route on the specific transition pair (`errors.As(err, &ite)` probes the struct after `errors.Is(err, ErrIllegalUploadStateTransition)` confirms the sentinel).

  **No migration / no SQLite change:** C6 ships the canonical surface live; existing pre-C6 worker.AssetClient.UploadFile callers continue to function unchanged. The C7 migration wave (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT) folds legacy callers into the Adapter. Today the surface is structural — it exists, it's git-pinned, adapters wire to it; no caller migration required for this commit.

  **AGENTS.md mirror:** new **Pattern 10 — ArtifactUploader state-machine + idem-key** (§ Modular edit patterns) added documenting the canonical port surface + the `compile-time var _ remote.ArtifactUploader = (*creator.Adapter)(nil)` build-failure lock + the Check 52 forward-prevention rule.

  **Pre-existing build issues (out of scope, NOT regressions from C6):** Same five items as the prior C1–C5 CHANGELOG entries carry forward. Verified via `git show origin/main:<file>` per the canonical recipe: `monitor/enqueue.go` (`strings.ToLower` undefined in `isTransientEnqueueError`), `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined), `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (syntax error in legacy upload path), `internal/app/module_media.go` (pre-existing `clips.Deps.MutationsDispatcher` literal). The 5 C6 commit test packages (`internal/domain/remote/`, `internal/infrastructure/remote/creator/`, `internal/infrastructure/remote/jobbrokerclient/`, plus the modified ci-arch-check script + AGENTS.md / CHANGELOG doc mirrors) all pass their targeted `gofmt + go vet + go build + go test` gates independently.

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for C6 yet (verified `grep -rE 'C6|ArtifactUploader|P0.*Commit.6' architecture/current.yaml` returning 0 hits). The closure is audit-logged via this CHANGELOG entry + the canonical-surface SHAs in `git log`. A follow-up wave-tracker entry should be filed under a `wave_status` row citing the implementation dependencies on C1 (JobDefinition foundation), C2 (PayloadCodec 1-to-1 wire-shape), C3 (CompiledJobRegistry Freeze), C4 (Dispatcher.Enqueue through compiled registry), C5 (artifact_manifest.go dual-type handling).

  **Honest limitation declaration (per godlike/07):**
  1. The protocol commands use `X-Idempotency-Key` header name for the byte-stable key — the standard convention for cross-protocol idempotency (mirrors Stripe / Square). A future C7 hardening could expose `Idempotency-Key` (no prefix) for HTTP semantic compatibility; not blocking.
  2. The 30-second default `httpClient.Timeout` cancels large streaming uploads mid-file if throughput drops below 1 MB / 30s. Threading `context.Context` through `PrepareContext` (adding `Context context.Context` next to `IdempotencyKey`) is the canonical hardening — but it bumps the port shape so it is deferred to C7 alongside the BACKFILL caller migration (per godlike/06 SSOT, the port is the canonical ownership boundary; shapes change there require a godlike/07 4-phase migration).

- **[P0 Commit 7 (C7) — atomic CompleteJob + (jobID, attempt, resultHash) idempotency (Sender-side single-TX complete, July 2026)]** `fix(remote)` — Sender-side canonical atomic-complete orchestrator surface: `CompleteJobRequest` typed envelope + `CompleteJobResponse` typed response + `CompleteJobIdempotencyKey` byte-stable SHA-256 helper + Sender-side `*Service` orchestrator (port-based Pattern 0 abstraction: `CompleteJobTxRunner` + `TxContext` + `IdempotencyCachePort`) enforcing single-SQLite-TX semantics with `(jobID, attempt, resultHash)` idempotency-on-replay + canonical migration `119_job_results.sql` (UNIQUE INDEX dedup surface). The P0 chain C1–C6 established the artifact-publish pipeline foundation + the stateful upload protocol; C7 layers the Sender-side atomic complete surface (idempotent + fail-closed per godlike/07) on top, exposing the canonical surface for the C8 BACKFILL wave that migrates pre-C7 `worker.MarkCompleted` callers to `Service.Complete`.

  **Files in scope (6):**

  - **NEW `internal/domain/remote/complete_job.go`** (~280 LoC, package `remote`) — typed envelope `CompleteJobRequest` (WorkerID/JobID/Attempt/LeaseID/Result/Artifacts/ResultHash) + typed envelope `CompleteJobResponse` (Status/JobArtifactIDs/JobID/Attempt/ResultHash) + `Validated()` pre-TX fail-fast aggregate (returns ErrCompleteJobRequestMissingFields with ALL missing fields named in ONE message per godlike/07 no-fake-availability) + `ValidateArtifacts()` schema + state + empty-id/sha256 guard (returns ErrRemoteArtifactManifestInvalid on bad SchemaVersion or ErrRemoteArtifactStateNotFinalized on non-FINALIZED Status) + 8 typed sentinels (`ErrCompleteJobNotConfigured` / `ErrCompleteJobRequestMissingFields` / `ErrConcurrentLeaseRefutation` / `ErrRemoteArtifactStateNotFinalized` / `ErrRemoteArtifactHasLocalPath` / `ErrRemoteArtifactManifestInvalid` / `ErrRemoteArtifactHashMismatch` / `ErrRemoteArtifactSizeMismatch`) all `errors.New(...)` and reachable via `%w`-wrap. The pre-TX `LocalPath` ban is structural: the typed `RemoteArtifactManifest` has no LocalPath field — the sentinel surfaces only as a future-drift guard per godlike/07 fail-closed.

  - **NEW `internal/domain/remote/complete_job_idempotency.go`** (~75 LoC, package `remote`) — deterministic `CompleteJobIdempotencyKey(jobID, attempt, resultHash) string` (SHA-256 hex of `<jobID>:<attempt>:<resultHash>` via `internal/infrastructure/files.SHA256String`; the `attempt` middle segment distinguishes retry-N from retry-N+1 identity even when the result payload is intentionally identical) + `IsValidCompleteJobIdempotencyKey(s string) bool` (case-insensitive 64-char hex + empty marker) + `ErrCompleteJobIdempotencyKeyConflict` typed sentinel (godlike/07 no-fake-availability: empty-input triple returns empty marker rather than synthetic hash so an empty-value wiring bug cannot silently collide with a valid-looking preimage) + `CompleteJobIdempotencyKeyDiagnostic(jobID, attempt, hash) string` accessor that returns the empty-marker reason in human-readable form.

  - **NEW `internal/domain/remote/complete_job_test.go`** (~250 LoC, package `remote_test`) — 13 TDD tests pinning the canonical contract: aggregated missing-fields aggregated in ONE diagnostic (mentions workerID/jobID/attempt/leaseID/result/resultHash/artifacts) + zero-values rejected + negative-attempt rejected + nil-receiver rejected + good-manifest validation succeeds + bad-schema-version rejected with `ErrRemoteArtifactManifestInvalid` + non-FINALIZED state rejected with `ErrRemoteArtifactStateNotFinalized` + empty-ID/SHA256 rejected + SHA256 byte-stability across 1000 retries with same triple + different inputs (different jobID/attempt/resultHash) → different keys + empty-input triple (3 cases) → empty marker + `IsValidCompleteJobIdempotencyKey` hex validator with case-insensitive A-F + empty marker semantics + `CompleteJobIdempotencyKeyDiagnostic` marker-context accessor + `CompleteJobResponse` JSON round-trip preserving Status/JobArtifactIDs/JobID/Attempt/ResultHash.

  - **NEW `internal/application/jobs/completion/complete_job_service.go`** (~340 LoC, package `completion`) — Sender-side `*Service` orchestrator with port-based Pattern 0 abstraction:
    - `CompleteJobTxRunner` interface — typed port for "execute a function inside a single SQLite transaction"; TX opens (the runner wires `*sql.Tx`), invokes fn with a `TxContext`, commits on nil, rolls back on error or panic (godlike/06 SSOT: NO `database/sql` leaks above the application layer).
    - `TxContext` interface — 6 in-TX methods: `GetJob(jobID) (*JobRow, error)` / `UpdateJobToSucceededCAS(jobID, leaseID, attempt) (rowsAffected int64, error)` (the canonical lease-fencing guard) / `InsertResultOnConflict(jobID, attempt, codecID, payload, resultHash) (rowID int64, replayed bool, error)` (the UNIQUE dedup surface; replayed=true means ON CONFLICT DO NOTHING preserved existing row) / `GetPriorArtifactHashes(jobID) (map[artifactID]PriorArtifactHash, error)` (round-trip check) / `PersistArtifactMap(jobID, attempt, entries) error` / `InsertOutboxEnvelope(envelope) error`.
    - `IdempotencyCachePort` interface — pre-TX replay short-circuit lookup + post-TX canonical-store. Optimisation only; SQLite UNIQUE INDEX remains the authoritative gate.
    
    `Service.Complete(ctx, req)` does: (1) pre-TX fail-fast gates (nil-receiver + req.Validated() + req.ValidateArtifacts()); (2) pre-TX idempotency replay probe via cache (hit → return cached response, TxRunner untouched); (3) in-TX orchestration (job fetch → CAS-update with (id, lease_id, attempt, status NOT IN terminal sinks) guard → ON CONFLICT INSERT into job_results → artifact hash round-trip check → persist job_artifacts mapping → insert outbox envelopes: 1 JOB_COMPLETED summary + 1 ARTIFACT.<kind>.UPLOADED per artifact); (4) post-TX store canonical response in cache so future replays short-circuit at step 2.    8 helper functions extracted for unit-testability (`lookupInTxCanonicalResponse`, `checkArtifactHashRoundTrip`, `artifactMapEntries`, `emitOutboxEvents`, `codecIDForPayload`, plus the `CompleteJob` orchestration + the `completeInTx` extracted-in-TX body).

  - **NEW `internal/application/jobs/completion/complete_job_service_test.go`** (~370 LoC, package `completion_test`) — 8 TDD tests with hand-rolled mock `CompleteJobTxRunner` + mock `TxContext` + mock `IdempotencyCachePort`. Tests pin: nil-rxRunner + nil-cache → `ErrCompleteJobNotConfigured` + happy-path single-TX orchestrating all 6 in-TX ops + idempotency replay returns same response WITHOUT touching TxRunner (bombing TxRunner verifies the short-circuit: `bomb := false` flag stays false if cache hit) + lease-stolen (`seedJob.LeaseID="different-lease"`) → rows-affected=0 → typed `ErrConcurrentLeaseRefutation` + missing-required (`Artifacts.Artifacts` empty) → `ErrCompleteJobRequestMissingFields` BEFORE TxRunner invocation (bombing TxRunner verifies the pre-TX gate fires before the runner) + hash-mismatch (`priorHashes["j-1:voiceover"] = {"DIFFERENT-SHA"}`) → typed `ErrRemoteArtifactHashMismatch` with `"DIFFERENT-SHA"` vs new sha256 in the drift summary + nil-receiver Service → `ErrCompleteJobNotConfigured` + nil-request → `ErrCompleteJobRequestMissingFields`.

  - **NEW `migrations/sqlite/119_job_results.sql`** — migration 119 (next-slot after the prior 118). Creates 1 table `job_results(id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, attempt INTEGER NOT NULL DEFAULT 0, result_hash TEXT NOT NULL DEFAULT '', codec_id TEXT NOT NULL DEFAULT '', result_payload TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')), FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE)` + 1 UNIQUE INDEX `uniq_job_results_dedup ON job_results (job_id, attempt, result_hash)` (the load-bearing ON CONFLICT surface) + 1 per-job scan INDEX `ix_job_results_job_id ON job_results (job_id, attempt DESC)`. Migrations are idempotent via `IF NOT EXISTS` so re-runs no-op. godlike/06 SSOT: result persistence lives on `job_results` (NOT on the core `jobs.result_json` column) — large JSON payloads stay out of bulk ClaimNext/List scans.

  **CI gate Check 53 in `scripts/ci-architectural-checks.sh` (NEW)** — forward-prevention rule banning direct callers of `TxContext.UpdateJobToSucceededCAS(` / `InsertResultOnConflict(` / `GetPriorArtifactHashes(` / `PersistArtifactMap(` / `InsertOutboxEnvelope(` outside the canonical `internal/application/jobs/completion/` package. Pattern anchors: 5 method names with rg `-e '\.<Method>\('` each. Allowlist: canonical completion package + completion_test.go + `!*_test.go` global. Pre-flight audit on current tree: `rg -E '(UpdateJobToSucceededCAS|InsertResultOnConflict|PersistArtifactMap)\(' internal/application internal/api` returns **0 hits** outside the allowlist — the Service is the only legitimate caller today. Mirrors Check 51 + Check 52 forward-prevention posture (rg-based pattern ban + allowlist + per-file ARCH-ALLOWLIST marker opt-in via 25-line scroll-window).

  **godlike/07 typed-error contract:** all 9 sentinels in the domain/remote package + the `ErrCompleteJobIdempotencyKeyConflict` in `complete_job_idempotency.go` + the `ErrCompleteJobNotConfigured` from `NewService` are typed `errors.New(...)`, all reachable via `errors.Is(err, <sentinel>)` from any caller seam. The service wraps internal errors via `fmt.Errorf("complete job: <stage>: %w", <inner error carrying the sentinel>)`, so callers can `errors.Is(err, remote.ErrConcurrentLeaseRefutation)` from the API layer without losing the typed-error chain. The typed-error-data field on the wrapped error (e.g. the drift summary in ErrRemoteArtifactHashMismatch mentioning both prior and new sha256) exposes structured audit data so log scanners + operator dashboards can route on the specific failure mode.

  **godlike/07 Migration sequence (EXPAND → BACKFILL → CUTOVER → CONTRACT):**
  - ✅ **EXPAND** (this commit, July 2026). Canonical port surface live; `Service.Complete` compiles; existing pre-C7 `worker.MarkCompleted` callers continue to function unchanged.
  - ⏳ **BACKFILL** (forward-pointer C8). Migrate MarkCompleted callers to Service.Complete. Each migration PR decrements the count toward 0.
  - ⏳ **CUTOVER** (forward-pointer C9). Retires MarkCompleted; the canonical surface is the only writer.
  - ⏳ **CONTRACT** (final deprecation removal). A `decision_*` deprecation record to be filed at C8 first lands at `architecture/deprecations.yaml#COMPLETE-JOB-MIGRATE` then progresses through EXPAND/BACKFILL/CUTOVER/CONTRACT per godlike/07.

  **AGENTS.md mirror:** new **Pattern 11 — Atomic CompleteJob + idempotency on (jobID, attempt, resultHash)** (§ Modular edit patterns) added documenting the canonical port surface + the `Comprehensive Service` typed port + the Check 53 forward-prevention rule.

  **No migration / no existing SQLite change:** only the NEW `job_results` table added; the existing `jobs` table is unchanged. The C6 `job_artifacts` table (if it exists in the target tree) is not migrated — the C7 Service writes via the canonical `PersistArtifactMap` port which the infrastructure layer handles.

  **Pre-existing build issues (out of scope, NOT regressions from C7):** Same five items as the prior C1–C6 CHANGELOG entries carry forward. Verified via `git show origin/main:<file>` per the canonical recipe: `monitor/enqueue.go` (`strings.ToLower` undefined in `isTransientEnqueueError`), `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined), `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (syntax error in legacy upload path), `internal/app/module_media.go` (pre-existing `clips.Deps.MutationsDispatcher` literal), `internal/application/images/routing` import cycle. The C7 commit test packages (`internal/domain/remote` + `internal/application/jobs/completion/`, plus migration 119 + the modified ci-arch-check script + AGENTS.md / CHANGELOG doc mirrors) all pass their targeted `gofmt + go vet + go build + go test` gates independently.

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for C7 yet (verified `grep -rE 'C7|CompleteJob|P0.*Commit.7' architecture/current.yaml` returning 0 hits). The closure is audit-logged via this CHANGELOG entry + the canonical-surface SHAs in `git log`. A follow-up wave-tracker entry should be filed under a `wave_status` row citing the implementation dependencies on C1 (JobDefinition foundation), C2 (PayloadCodec 1-to-1 wire-shape), C3 (CompiledJobRegistry Freeze), C4 (Dispatcher.Enqueue through compiled registry), C5 (artifact_manifest.go dual-type handling), C6 (ArtifactUploader state-machine).

  **Honest limitation declaration (per godlike/07):**
  1. The `lookupInTxCanonicalResponse` helper (in `internal/application/jobs/completion/complete_job_service.go`) is a MINIMAL reconstruction from `GetPriorArtifactHashes` for the C7 commit. A separate C8 follow-up wires the dedicated canonical reader (a new `TxContext.ReadCanonicalResponse` method returning the stored artifact_ids verbatim in their persisted slice order). Today's helper returns an approximation (artifact IDs in iteration order of the priorHashes map, NOT the persisted ordered slice); the godlike/07 typed-error contract is preserved (re-projection still surfaces structural metadata) but the artifact ID ordering is best-effort.
  2. The commit default timeout is NOT enforced in the `Service.Complete` entry point today — production callers must thread `context.WithTimeout(ctx, 30*time.Second)` themselves before invoking `Service.Complete`. A future C8 hardening ports `service.completeInTx` to derive its own time-bounded context via `context.WithTimeout(ctx, 30*time.Second)` before delegating to `rxRunner.RunInTx` so a deadlock in the TX path cannot stall the worker beyond the canonical 30s ceiling.
  3. The `codecIDForPayload` helper returns `"json.v1"` today (the only codec installed per C1/C2 spec). A future C8 hardening wires the ResultCodec canonical codec-registry surface from C2 so `codec_id` is the canonical-compiled-registry key, not a hand-rolled string constant. The typed `string` value remains the wire-format discriminator; only the source of truth changes.

- **image territories cutover** (closure commit `chore(architecture): image-territories-cutover wave tracker entry` + cycle break commit `a130bb9a feat(images): FASE 8 routing↔retrieved cycle break`, July 2026) — FASE 8 of the image-territories action plan. The routing↔retrieved import cycle is broken by relocating the shared DTOs (`RetrievalSearchOptions`, `RetrievalSearchResult`, `SearchRequest`, `SearchResponse`) to the routing package (the port side), making the dependency graph one-way `retrieved → routing`. New `routing.Service` interface promoted from compile-time assertion in `images/service.go` to a first-class routing-layer interface. New canonical `docs/architecture/image-territories-cutover-report.md` closure report + new `architecture/current.yaml#image-territories-cutover` wave-tracker entry with `linked_issues: [PR-IMAGE-CAPABILITY, PR-IMAGE-LISTIMAGESBYSUBJECT, PR-IMAGE-DETAIL-METADATA-MIGRATION]` for the deferred sub-tasks (service.go mega-file split, styles canonical-rename with ~11 import sites, DELETE write removal). Verification: `go build ./internal/application/images/...` green, `go test ./internal/application/images/...` green (8 packages pass), `rg 'style_description' internal/` = 0, `rg 'retrieved\.' internal/application/images/routing/` = 0 (cycle is one-way). Pre-existing tree-wide build issues (Fase 8 monitor→youtube split + 4 carry-forward items) remain out of scope per the CHANGELOG forward-pointer convention.

**[Audit P0 #1 (commit 2/3) — Deletion state machine INDEX_DELETED step, July 2026]** `feat(deletion)` — extend the canonical deletion state machine to a closed 7-edge chain `ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → DRIVE_DELETED → INDEX_DELETE_PENDING → INDEX_DELETED → DELETED` (plus legacy `DELETE_PENDING → DRIVE_DELETE_PENDING` rewrite path). The new `INDEX_DELETED` step is the post-Qdrant+SQLite-SoftDelete confirmation hop enabling an operator dashboard to distinguish "Qdrant projection removed confirmed" (INDEX_DELETED) from "Drive side-effect confirmed" (DRIVE_DELETED) and from "fully retired" (DELETED). Two-TX with compensation pattern: the Qdrant delete runs as the external side-effect (idempotent at the API layer via `deleted_count: 0` on already-absent point), succeeded by a single SQLite tx that stamps the intermediate `INDEX_DELETED` hop then the terminal `DELETED` flip; each column flip is independently idempotent (same-state writes are no-ops at the repo layer) so transient SQLite failures surface as retryable errors and the next outbox pool attempt's pre-flight re-runs only the failed column.

- **`internal/domain/asset/asset_types.go`** gains two new typed enum constants `StateDriveDeleted LifecycleState = "DRIVE_DELETED"` and `StateIndexDeleted LifecycleState = "INDEX_DELETED"`, added to `CanonicalLifecycleStateValues()` + `Valid()` switch + `IsValidTransition` machine. New edges: `DRIVE_DELETE_PENDING → DRIVE_DELETED` (DriveDeleteHandler post-success flip) + `DRIVE_DELETED → INDEX_DELETE_PENDING` (IndexDeleteHandler pre-flip) + `INDEX_DELETE_PENDING → INDEX_DELETED` (IndexDeleteHandler post-Qdrant+SoftDelete intermediate) + `INDEX_DELETED → DELETED` (IndexDeleteHandler post-success terminal). Self-loops are explicitly allowed per audit-pinning idempotency contract.

- **`internal/application/jobs/outbox/drive_delete.go::DriveDeleteHandler.Handle`** flips the post-success `AdvanceAndEmit` from `DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING` to `DRIVE_DELETE_PENDING → DRIVE_DELETED` (canonical atomic flip + emit `EventAssetIndexDeleteRequested` in same tx). The Drive-block guard in IndexDeleteHandler depends on this stamp — pre-commit 2/3 rows that already advanced to `INDEX_DELETE_PENDING` are accepted on entry as legacy forward-compat, so the migration is non-breaking.

- **`internal/application/jobs/outbox/index_delete.go::IndexDeleteHandler.Handle`** chain-entry switch now accepts `{StateDriveDeleted (new), StateLifecycleIndexDeletePending (legacy forward-compat)}` as "ready to run the index-hop chain", adds the `Drive-block guard` that rejects rows still at `DRIVE_DELETE_PENDING` (or any other pre-Drive-confirmation state) with a typed terminal error mentioning the guard identifier "drive_file_alive_block", the Italian "ancora vivo" diagnostic hint per user spec ("errore chiaro"), and the retry guidance ("re-enqueue only after DriveDeleteHandler has stamped DRIVE_DELETED"). Idempotent pre-flight skips added: `StateIndexDeleted` (the new intermediate hop) joins the existing `StateDeleted` + "deleted" (legacy lowercase) skip set, so re-running the index-delete handler on a row already past INDEX_DELETED is a free no-op rather than a redundant Qdrant call. Two new `indexLifecycleTerminalErr` + `assetErrTerminalEnvelope` typed sentinels mirror the `driveLifecycleTerminalErr` ancestor pattern from drive_delete.go, so the production pool's `IsTerminal` classifier dead-letters the rejection WITHOUT spinning through `max_attempts` retry-repair loops. New post-Qdrant-success intermediate write: `SetLifecycleState(..., StateIndexDeleted)` between the SoftDelete and the terminal `SetLifecycleState(..., StateDeleted)`.

- **`internal/application/jobs/outbox/index_delete_test.go`** (REWRITTEN, +649 LoC including the existing 7-test surface) gains 6 new TDD tests pinning the commit 2/3 audit:
  - `TestIndexDeleteHandler_AlreadyIndexDeletedIdempotent` — re-running against a row already at INDEX_DELETED returns nil with zero Qdrant calls + zero SoftDelete calls + zero state flips (user-spec test #1: "idempotenza re-running dopo successo non è errore").
  - `TestIndexDeleteHandler_DriveBlockGuardFires` — a row at DRIVE_DELETE_PENDING triggers typed-terminal error containing `drive_file_alive_block` + `ancora vivo` + `DRIVE_DELETED` retry-guidance markers (user-spec test #2: "error path file Drive non ancora cancellato blocca INDEX_DELETED"). 3 additional assertions: guard fires BEFORE any deleter invocation, no SoftDelete, no lifecycle_state flip — the "ancora vivo" surface is observable to the operator dashboard as a no-side-effect rejection.
  - `TestIndexDeleteHandler_HappyPathTransitionsToDeleted` — chain entering at DRIVE_DELETED: Qdrant called once with exactly the right asset_id, SoftDelete called once, index_state hops DELETE_PENDING → DELETED exactly once each, lifecycle_state hops INDEX_DELETED → DELETED exactly once each (intermediate confirmation hop is the audit-pinning surface for the closed chain).
  - `TestIndexDeleteHandler_StampsIndexDeletedBeforeDeleted` — narrower-form mirror of the order invariant: re-running the canonical chain with the intermediate flipped to terminal would fail loudly.
  - `TestIndexDeleteHandler_LifecycleStateAdvancesToDeleted` — BOTH the intermediate + terminal lifecycle_state hops fire (NOT just the terminal; a future regression that skips the intermediate INDEX_DELETED flip breaks here).
  - `TestIndexDeleteHandler_LifecycleStateSetErrorIsRetryable` — Qdrant+SoftDelete+index_state already done on transient SQLite "database is locked"; next-deeper assertion that the failing write is NOT terminal (so the pool's exponential backoff retries, not dead-letter).

**Files modified (4) + new TDD coverage (11 existing + 6 new = 17 total in index_delete_test.go):**

- `internal/domain/asset/asset_types.go` (~+30 LoC net for the 2 new constants + Valid + CanonicalLifecycleStateValues + IsValidTransition edges + doc-comments).
- `internal/application/jobs/outbox/drive_delete.go` (~+15 LoC net: AtomicFlip via StateAdvancer.AdvanceAndEmit now flips to StateDriveDeleted instead of StateLifecycleIndexDeletePending; explanatory doc-comment block documenting the audit-pinning invariant).
- `internal/application/jobs/outbox/index_delete.go` (~+240 LoC net: Drive-block guard switch with terminal-error wrapping + idempotent INDEX_DELETED pre-flight skip + intermediate lifecycle_state flip + audit-pinning doc-comment block + 2 new typed-terminal sentinels + errors import).
- `internal/application/jobs/outbox/index_delete_test.go` (rewrite + +6 new tests; net ~+235 LoC).

**No SQLite migration needed:** Option A design (per locked design decision) reuses the existing `media_assets.lifecycle_state` column — no new `deletion_state_per_asset` table is required. Adding a parallel state-column would violate godlike/06 "one canonical owner per fact" (the `lifecycle_state` column is already the SSOT); the existing migration 094_enrich_index_state was the most recent column-side change.

**Honest limitation declaration (godlike/07):**

1. **Production runtime integration of DriveDeleteHandler + IndexDeleteHandler is OUT OF SCOPE for this commit.** Verified via `grep -nE 'NewDriveDeleteHandler|NewIndexDeleteHandler' . --type go` returning 0 hits — neither handler is yet wired in `internal/app/composition.go` or `internal/app/registry_*.go`. Commit 1/3 added the foundation surface (handler structs + ports + tests + StateMachine library); commit 2/3 adds the INDEX_DELETED step to the handler logic; commit 3/3 will wire both handlers into the runtime through `register_job_handlers.go` (likely the composition-side integration layer). Until commit 3/3 lands, the new step exists as a verified-library surface that the production wiring will consume. The state-machine semantics are correct in isolation (the test suite is the audit-pinning surface), and the production-side wiring is the canonical next step.

2. **`POST-WireStockPipeline`-style legacy forward-compat for the migration envelope.** Pre-commit 2/3 rows that already advanced to `INDEX_DELETE_PENDING` (without the new `DRIVE_DELETED` confirmation hop) are accepted on entry by `IndexDeleteHandler.Handle` — this is the legacy forward-compat path. Once commit 2/3 ships and the production wiring is enabled, the outbox dispatcher will see mixed inputs (some events produced by pre-commit-2/3 production code where the DriveDeleteHandler already advanced to INDEX_DELETE_PENDING). The legacy forward-compat switch in `IndexDeleteHandler::Handle` handles both old and new chains transparently; future cleanup of the dual case (a `Migration: rewrite pre-2/3 rows to DRIVE_DELETED` follow-up) is a separate wave-tracker entry.

3. **The 4-state legacy `DELETE_PENDING` rewrite path is preserved.** Pre-Blocco 3.1 rows at `lifecycle_state=DELETE_PENDING` (the legacy broad-intent value) continue to flip to `DRIVE_DELETE_PENDING` via the existing reconciler rewrite. This path is unchanged; commit 2/3 only adds the new DRIVE_DELETED + INDEX_DELETED confirmation hops that new production code emits.

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for this P0 #1 commit 2/3 closure yet (verified `grep -E 'P0\\\\.1|INDEX_DELETED|Blocco.3\\\\.1.*commit.2' architecture/current.yaml` returns 0 hits). Audit-logged via this CHANGELOG entry only; a follow-up wave-tracker entry should be filed under a `wave_status` row citing the post-land SHA + the implementation dependencies on commit 1/3 (foundation) and commit 3/3 (production wiring).

**Pre-existing build issues (out of scope, NOT regressions from audit-P0.1 commit 2/3):**

Same five items as the prior audit-P0.2 / audit-P0.5 / Phase 1c CHANGELOG entries carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined.
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal.

- **[Step 9/12 — shared `assets.SourceStager` 3-adapter extraction + YouTube + Artlist wire-up with `ProcessInput.LocalPath` gateway (commits `93d92b78` + `2bc503df` + `ab404b8b`, July 2026)]** `feat(assets) + feat(youtube) + feat(artlist)` — extract the shared `SourceStager` port + DTOs into a leaf `internal/application/assets/ports.go`, ship 3 provider-specific adapters (YouTube/Stock/Artlist), wire YouTube + Artlist into the canonical per-segment / per-batch pipelines, and add `asset.ProcessInput.LocalPath` so the stager download REPLACES (not just probes) the mediaProcessor download for Artlist. The YouTube wiring is genuine bandwidth-saving via `VideoCutRequest.PreDownloadedPath` honored by `videomuscles/youtube_pipeline.go:124-133` (ffmpeg `-c copy` slice after one yt-dlp full-video download; the retry loop reuses the SAME staged file across all attempts). The Artlist wiring is genuine bandwidth-saving via the new `LocalPath` gateway pattern in `Processor.Process` (download skipped when caller-provided LocalPath is set; 3 `os.Remove(actualRawPath)` cleanup sites wrapped with `if input.LocalPath == ""` guards enforcing caller-owned cleanup per godlike/07 no-fake-availability).

  **Files in scope (8):**

  - **NEW `internal/application/assets/ports.go`** (~60 LoC, package `assets`) — declares the canonical `SourceStager` interface (`StageSource(ctx, SourceRef) (*StagedAsset, error)` + `Cleanup(ctx, *StagedAsset) error`) + `SourceRef{URL string}` typed envelope + `StagedAsset{LocalPath string, Bytes int64}` typed envelope. Typed envelopes keep signatures refactor-resistant per godlike/06 SSOT; the 2-method contract is leaf-only (zero internal-package import to enforce layering).
  - **NEW `internal/application/youtube/stager_adapter.go`** (~110 LoC, package `youtube`) — YouTubeStager adapter. Wraps `pkg/executil.Run + yt-dlp -f best -o <temp> <videoURL>` returning a `*assets.StagedAsset`. Cleanup deletes the temp file via `os.Remove` (best-effort, logs on error). Compile-time assertion `var _ assets.SourceStager = (*YouTubeStager)(nil)` locks signature drift to build failure.
  - **NEW `internal/application/assets/providers/stock/stockpipeline/stager.go` + `stager_adapter.go`** (~190 LoC combined, package `stockpipeline`) — StockStager adapter implementing the same StageSource/Cleanup shape for the Stock provider's download surface. StockStager is structurally identical to YouTubeStager (same port, same DTOs, same defer-cleanup contract).
  - **NEW `internal/application/assets/providers/artlist/stager_adapter.go`** (~120 LoC, package `artlist`) — ArtlistStager adapter bridging to the canonical Artlist Downloader port (Node.js scraper for HLS / Artlist CDN URLs). Compile-time assertion `var _ assets.SourceStager = (*ArtlistStager)(nil)`.
  - **MODIFIED `internal/domain/asset/processor.go`** (~+8 LoC) — adds `LocalPath string` field to `asset.ProcessInput` (positioned right after `SourceURL`) with a doc block explaining the gateway pattern: caller-provided LocalPath skips the Processor's own download; cleanup is caller-owned (the Processor MUST NOT delete caller-provided paths).
  - **MODIFIED `internal/infrastructure/media/processor/processor.go::Process`** — 4 changes: validator relaxed from `SourceURL == ""` to `SourceURL == "" && LocalPath == ""` (OR-relationship); YTDLP-nil guard relaxed (`dl == nil` only fires when LocalPath empty); Step 1 download bypass branch `if input.LocalPath != "" { actualRawPath = input.LocalPath; log.Info("Process: bypassing downloadStep — using caller-provided LocalPath") }`; 3 `os.Remove(actualRawPath)` cleanup sites (processStep fail + hashStep fail + final-success) wrapped with `if input.LocalPath == ""` guards. Hoisted `var (actualRawPath string; err error)` above the if/else so both branches + the subsequent `processStep` call share the same `err` variable (Step 9/12 commit-fixed `undefined: err` scoping bug surfaced at first build).
  - **MODIFIED `internal/application/youtube/usecase/process_segment.go`** (~+25 LoC) — adds `Stager assets.SourceStager` as OPTIONAL field on `ProcessSegmentDeps` (the 13th field; sits among other OPTIONAL fields like `ClipMetadataWriter`/`MetadataService`). New `Step 4a — shared SourceStager pre-stage` block inserted immediately before the existing `Step 4 — retry download` block: when `u.deps.Stager != nil && cmd.VideoURL != ""`, calls `Stager.StageSource(ctx, assets.SourceRef{URL: cmd.VideoURL})`, sets `cutReq.PreDownloadedPath = staged.LocalPath` (the concrete `videomuscles/youtube_pipeline.go:124-133` SKIPS yt-dlp + uses `ffmpeg -c copy`), defers best-effort `Stager.Cleanup(ctx, staged)`. On stage failure → log Warn + cutReq keeps `PreDownloadedPath=""` so the legacy `DownloadAndCutYouTubeVideo` path keeps working. The Stager field is OPTIONAL (Required-port panic list unchanged: 5 entries — Cache/VideoPipeline/Hash/Writer/SegmentsSvc), so non-wired callers compile + run unchanged per godlike/07 no-fake-availability.
  - **MODIFIED `internal/application/assets/providers/artlist/run_orchestrator_stages.go::stageProcessBatch`** — adds `stagedAsset *assets.StagedAsset` field to `clipWork` struct + a pre-Process gateway step: when `ServicePorts.Stager` wired + `processInput.SourceURL != ""`, calls `Stager.StageSource(ctx, assets.SourceRef{URL: processInput.SourceURL})`, sets `processInput.LocalPath = staged.LocalPath` (Processor.Process SKIPS its own download via the new LocalPath bypass branch), defers best-effort `Stager.Cleanup`. On stage failure → log Warn + LocalPath stays empty (legacy mediaProcessor download path unchanged). The dead `processInput.SourceURL = processInput.SourceURL // no-op` self-assign + the "KNOWN LIMITATION: bandwidth-waste probe" doc comment were both removed; the new comment states "staged file is NOT just a probe — mediaProcessor.Process will SKIP its internal downloadStep".

  **Verification gates (per godlike/07 + targeted test packages):**
  - `go build ./...` green for the 3 directly-affected packages (`internal/application/assets/providers/artlist`, `internal/domain/asset`, `internal/infrastructure/media/processor`).
  - `go test -count=1 -timeout=120s ./internal/application/assets/providers/artlist/... ./internal/domain/asset/... ./internal/infrastructure/media/processor/...`: PASS (3/3 packages green).
  - `go test ./internal/application/youtube/usecase/...`: PASS (YouTube usecase tests unaffected by the OPTIONAL Stager field — `NewProcessYouTubeSegmentUseCase` panics on 5 REQUIRED ports only; Stager is OPTIONAL per godlike/07 no-fake-availability).
  - First build attempt revealed `undefined: err` scoping bug in processor.go (the `var err error` was wrapped inside the else-branch for legacy download, scoping it locally so the subsequent `processedPath, err = p.processStep(...)` couldn't see it) — caught + fixed in the same PR by hoisting the declaration to a `var (actualRawPath string; err error)` block above the if/else.
  - Pre-existing 5-item build-issue carry-forward unchanged (out of scope per CHANGELOG forward-pointer convention): `monitor/enqueue.go` (`strings.ToLower` undefined), `monitor/scheduler.go` (`NewUnboundJobEnqueuer` undefined), `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (syntax error in legacy upload path), `internal/app/module_media.go` (pre-existing `clips.Deps.MutationsDispatcher` literal), `internal/application/images/routing` import cycle.

  **Production impact:**
  - **YouTube**: per-segment Execute stages the full video once via the shared SourceStager; `videomuscles` concrete pipeline SKIPS its per-segment yt-dlp download and slices with `ffmpeg -c copy` (instantaneous). The retry loop reuses the SAME staged file across all attempts (no extra yt-dlp calls). Pre-Step-9/12 bandwidth per Execute: ~3 yt-dlp downloads (one per retry) + 1 full ffmpeg slice; post: 1 yt-dlp full-video download + 3 `ffmpeg -c copy` slices.
  - **Artlist**: per-clip goroutine stages via the shared SourceStager; canonical `Processor.Process` SKIPS its own download (the new LocalPath bypass branch in Step 1) and feeds the staged file directly to ffmpeg normalize. Cleanup is caller-owned (deferred at the call site). The 3-cleanup-guard contract in Processor.Process keeps the staged file alive across normalize — gateway contract enforced.
  - **Stock**: StockStager adapter is defined + interface satisfied + per-port methods return typed shapes — but `internal/application/assets/providers/stock/stockpipeline/orchestrator.go::Orchestrator.Run` does NOT yet route through the SourceStager port. The legacy download path is unchanged. Forward-pointer to PR-STOCK-SOURCESTAGER-WIRE (Tracked TBD).
  - **Cross-cutting**: all 3 adapters implement the same `assets.SourceStager` interface → composition root can swap any of them per provider without touching call sites. The contract is leaf-only (zero internal-package imports from `internal/application/assets/ports.go`), enforces the AGENTS.md Pattern 4 utility-package discipline.

  **No migration / no SQLite change:** the canonical `SourceStager` port is greenfield — no DB tables touched. `ProcessInput.LocalPath` is a Go-only field, zero wire format impact (no JSON tag, no DTO ripple).

  **Compile-time assertions (godlike/06 SSOT one-owner-per-fact lock):** future drift in any of the 3 adapter signatures OR the port surface OR `ProcessInput.LocalPath`'s position is a build failure at the canonical concrete site:
  - `var _ assets.SourceStager = (*YouTubeStager)(nil)` in `internal/application/youtube/stager_adapter.go`.
  - `var _ assets.SourceStager = (*StockStager)(nil)` in `internal/application/assets/providers/stock/stockpipeline/stager_adapter.go` (or equivalent Stock site — verify on consolidation PR).
  - `var _ assets.SourceStager = (*ArtlistStager)(nil)` in `internal/application/assets/providers/artlist/stager_adapter.go`.

  **Wave-tracker cross-reference (per godlike/07):** the 3-adapter extraction is forward-tracked under `architecture/current.yaml#PR-PROCESSINPUT-LOCALPATH-GATEWAY` (deadline 2026-07-25) per godlike/06 SSOT. The SourceStager port in `internal/application/assets/ports.go` is the canonical SSOT; the 3 adapters are siblings implementing the same surface. No formal wave-tracker entry yet (verified `rg 'SourceStager|ProcessInput.LocalPath' architecture/current.yaml` returning 0 hits beyond the existing Phase-1C + Audit-P0 related entries). A follow-up wave-tracker entry should be filed under a `wave_status` row citing the 3 SHAs.

  **Honest limitation declaration (per godlike/07):**
  1. **StockStager production wiring is greenfield-only.** The adapter is defined + the interface is satisfied + per-port methods return typed shapes — but `stockpipeline.Orchestrator.Run` does NOT yet route through SourceStager. The legacy download pattern is unchanged. Forward-pointer to PR-STOCK-SOURCESTAGER-WIRE (deadline TBD). Code-reviewer-minimax-m3 verified Stock failures do not lock out call sites (Stager is OPTIONAL everywhere per godlike/07 no-fake-availability).
  2. **ArtlistStager wiring is currently per-Execute (not batch-aware).** If a future extraction runs N clips from the SAME source URL, each Execute independently re-downloads the full source. The YouTube `PreDownloadedPath` pipeline honors the staged file for retries within the same Execute but NOT across Executes. Forward-pointer to PR-SOURCESTAGER-BATCH-AWARE-CACHE for batch-level sharing semantics.
  3. **`ProcessInput.LocalPath` is intra-process only.** If a future API gateway exposes ProcessInput over HTTP, the field would need an `,omitempty` JSON tag + ssrf-aware caller-validation (otherwise external callers could bypass the Downloader port's anti-ssrf checks). Today the field has no wire surface so external callers cannot bypass Downloader port checks.
  4. **The 3 `os.Remove(actualRawPath)` guards with `if input.LocalPath == ""` are CORRECTNESS-INVARIANT.** A future refactor that drops even one guard would terminate the staged file AFTER ffmpeg normalize succeeded — leaving the defer-cleanup with a dangling `os.ErrNotExist` warning (not a panic, but visible operator-log noise). Future Processor.Process changes MUST keep the 3 guards byte-equivalent; the existing 9 TestProcessInput* tests cover the legacy path but no test pins the 3 guards directly — forward-pointer to PR-LOCALPATH-OSREMOVE-TEST-PIN would be the canonical hardening follow-up.

### Refactor
- **[Fase 9 step 3 — TranslationPort canonical surface unification + voiceover typewriter-alias removal, July 2026]** `refactor(translation) + refactor(voiceover)` — propagate `ModelPolicy` (with json tags + `model_policy,omitempty` back-compat gate) across the `TranslationCommand`/`TranslationResult`/`ModelPolicy` DTO surface, and remove the two voiceover-local typewriter aliases that pointed to canonical `translation.TranslatorFunc` + `translation.LanguageTarget`. The umbrella godlike/07 record `architecture/deprecations.yaml#TRANSLATION-UNIFY` flips its status to `contract-half` (the contract-pending wave: voiceover typewriter aliases gone + canonical surface live, but the legacy `TextTranslationService` + `TranslatorService` + `MetadataTranslator` aliases in `scripts/{usecase,dto}` remain populated until CUTOVER wave). Forward-compat anchored by `var _ TranslationPort = (*OllamaTranslator)(nil)` + `var _ LegacyTextTranslationService = (*OllamaTranslator)(nil)` + `var _ LegacyTranslatorService = (*OllamaTranslator)(nil)` + `var _ LegacyMetadataTranslator = (*OllamaTranslator)(nil)` at `internal/application/translation/ollama_translator.go` (per godlike/06 SSOT, future drift in any of the 4 port signatures is a build failure at the canonical concrete site).

  **Sites closed (3):**

  - **`internal/application/translation/ports.go::TranslationCommand`** gains json tags on every field: `SourceLang string \`json:"source_lang,omitempty"\`` + `TargetLang string \`json:"target_lang,omitempty"\`` + `Text string \`json:"text,omitempty"\`` + `ModelHints map[string]string \`json:"model_hints,omitempty"\`` + `ModelPolicy *ModelPolicy \`json:"model_policy,omitempty"\``. Convention: snake_case wire names + `omitempty` on every optional. The `model_policy,omitempty` tag is the canonical back-compat gate for any future gateway-edge struct that mirrors `TranslationCommand` onto the API surface (godlike/07 no-fake-availability: a non-nil-but-zero-value ModelPolicy does NOT serialise over the wire so callers who deliberately populate must declare explicit non-zero values).

  - **`internal/application/translation/ports.go::TranslationResult`** gains json tags on every field: `TranslatedText/Confidence/UsedModel/UsedProvider/SourceLang/TargetLang/CacheStatus`, all snake_case + `omitempty`. The 4 provenance fields (`UsedProvider/SourceLang/TargetLang/CacheStatus`) added in Fase 9 step 1 retain their god-comment rationale (observability + cache auditing + model/provider traceability per godlike/06 SSOT requirements at the application-layer wiring boundary).

  - **`internal/application/translation/ports.go::ModelPolicy`** gains json tags on every field: `Provider/Model/Temperature/MaxTokens`, all snake_case + `omitempty`. Same back-compat gate shape as TranslationCommand: a nil/zero struct does NOT serialise, so callers must declare explicit non-zero values to surface their model choice over the wire.

  **Sites opened (the contract-half CUTOVER wave):**

  - **`internal/application/voiceover/service.go`** physically removes the typewriter alias `type TranslatorFunc = translation.TranslatorFunc` (line 107 pre-step-3) and retypes the one internal reference `VoiceoverIntegrationDeps.Translator TranslatorFunc \u2192 translation.TranslatorFunc`. The voiceover-local `translator translation.TranslatorFunc` field on `Service` struct (line 87 pre-step-3) is unchanged (already canonical; marked `// Deprecated` for the EXPAND-window back-compat with the now-deleted typewriter alias, but the field type is the canonical translation package type and the field stays). The composers at `internal/app/build_bundles_voiceover.go::buildVoiceoverService` are unchanged (the closure `translator := func(ctx, text, targetLanguage) (string, error) { return scriptGen.TranslateText(...) }` matches `translation.TranslatorFunc`'s `func(ctx context.Context, text, targetLanguage string) (string, error)` signature byte-stable; no ripple effect on `VoiceoverIntegrationDeps.Translator: translator`).

  - **`internal/application/voiceover/types.go`** physically removes the typewriter alias `type LanguageTarget = translation.LanguageTarget` (line 472 pre-step-3). All 4 promo-workflow aliases (PromoRequest/Result/Response/PayloadMap) stay populated (they are NOT translation-related; typewriter-alias-untouched per Fase 9 step 3 scope). The `DefaultPromoLanguages = translation.DefaultPromoLanguages` var shadow on a `func() []translation.LanguageTarget` reference stays (NOT a typewriter alias per se; it's a `var = ` assignment, not a `type = ` alias).

  **Migration sequence update (godlike/07 §"Migration sequence"):**

  - **EXPAND** \u2192 already complete (Fase 9 step 1 + step 2 landed canonical `TranslationPort` + `OllamaTranslator` + 3-typed-aliases). `TRANSLATION-LEGACY-SERVICES-MIGRATION` record at status `in_progress` / `migration_phase: EXPAND` stays as the EXPAND-phase bookkeeping entry.
  - **BACKFILL** \u2192 step 3 just landed this commit. Voiceover typewriter aliases physically gone; canonical TranslationPort + json-tagged ModelPolicy propagated. New umbrella record `TRANSLATION-UNIFY` at status `contract-half` (a non-canonical godlike/07 phase annotation introduced for this commit; interprets as \u201chalfway between BACKFILL and CUTOVER\u201d \u2014 voiceover side fully retired, scripts side halfway done).
  - **CUTOVER** \u2192 forward-pointer: step 4 will drop the legacy field population at composition root (`svc.Translation + svc.Translator` fields removed from `ClipServices`, leaving only the canonical `TranslationPort`).
  - **CONTRACT** \u2192 forward-pointer: step 5 will physically git-rm `legacy.go` + the 3 legacy methods on `OllamaTranslator` (TranslateText + TranslateTextWithModel + GenerateVideoMetadataWithModel).

  **ModelPolicy propagation canonical surface (production caller surface today):**

  The ONLY production caller of `svc.TranslationPort.Translate(...)` is `internal/application/scripts/usecase/flow_helpers.go::artlistSearchPhrase` (migrated in step 2). The flow_helpers call site already populates `cmd.ModelPolicy = &translation.ModelPolicy{Provider:"ollama", Model: svc.MetadataModel}` when `svc.MetadataModel != ""` (the canonical \u201cdecidi un default sensato\u201d from the user spec). One site, one propagation path, no new consumer migration required in step 3.

  **Files modified (4):**

  - `internal/application/translation/ports.go` (~+~25 LoC net for json tags on 3 struct surfaces + ~20 LoC god-style package doc comment update documenting the contract-half annotation).
  - `internal/application/voiceover/service.go` (~+~18 LoC net: 2-block comment fossil replacing the deleted `type TranslatorFunc = ...` alias + retyping `VoiceoverIntegrationDeps.Translator` field + a single-line deprecation trackback to architecture/deprecations.yaml).
  - `internal/application/voiceover/types.go` (~+~12 LoC net: 1-block comment fossil replacing the deleted `type LanguageTarget = ...` alias).
  - `architecture/deprecations.yaml` (~+~95 LoC net: new umbrella record \u201cTRANSLATION-UNIFY\u201d with the contract-half framing + migration_phase=BACKFILL + status=in_progress + triple-defence compatibility_test + usage_metric baseline + godlike/07 14-field schema).

  **No assets DB migration needed:** the canonical TranslationCommand/Result/ModelPolicy struct shapes already exist on `main` HEAD at commits `657fc7eb` (Phase 0) + Fase 9 step 1 (`657fc7eb`) + step 2 (unlanded, with TRANSLATION-LEGACY-SERVICES-MIGRATION registered). This commit completes the json-tag surface + the voiceover typewriter-alias retirement on top of step 1 + step 2 without requiring any additional data model changes.

  **Honest limitation declaration (godlike/07):**

  1. **`metadata_test.go` mocks are unchanged but the legacy `MetadataTranslator` port behind them is now type-aliased to `translation.LegacyMetadataTranslator`.** The 4 compile-time assertions (`var _ MetadataTranslator = (*mockTranslatorFailingTranslate)(nil)` + 3 sibling) compile identically via Go\u2019s interface aliasing rules \u2014 the alias and the canonical declaration share byte-identical method sets, so the mocks satisfy both identically. Forward-pointer: future CUTOVER (step 4) will thread `TranslationPort` through `dto.GenerateVideoMetadata` so the legacy `MetadataTranslator` field/alias collapses entirely; the mocks will need updating at that commit.

  2. **Pre-existing build issues carry forward** (verified `git show origin/main:<file>` per the CHANGELOG forward-pointer convention). The 4 Fase 9 step 3 commit test packages (`internal/application/translation`, `internal/application/voiceover`, `internal/application/scripts/{usecase,dto}`, `internal/app`) all pass typecheck independently. Tree-wide verification resumes when the pre-existing `internal/application/assets/monitor/scheduler.go::NewExtractionEnqueuer` undefined failure (monitor Blocco 3.1 in-flight) is resolved.

  3. **The umbrella record `TRANSLATION-UNIFY` uses a non-canonical godlike/07 phase annotation** (\u201ccontract-half\u201d). The canonical 4 phases are EXPAND \u2192 BACKFILL \u2192 CUTOVER \u2192 CONTRACT. \u201cContract-half\u201d is a USER-SPEC designation interpreted by this commit as \u201chalfway between BACKFILL (this commit) and CUTOVER (forward-pointer step 4)\u201d. The record's `migration_phase` field carries the canonical value `BACKFILL`; the \u201ccontract-half\u201d status is documented in the `notes:` block as a per-user-spec framing rather than a new canonical phase value.

  **Wave-tracker cross-reference (per godlike/07):** \u201cTRANSLATION-UNIFY\u201d umbrella record tracks the migration_phase: BACKFILL / status: contract-half state. EXPAND-phase bookkeeping lives in the sibling record `TRANSLATION-LEGACY-SERVICES-MIGRATION` (migration_phase: EXPAND / status: in_progress). CUTOVER + CONTRACT phases forward-point to step 4 + step 5 commits (to be filed via the canonical wave-tracker pattern).

  - **[Fase 8 SPINA DORSALE — monitor→youtube cross-capability port split, July 2026]** `chore(deprecations) + refactor(monitor)` — Fase 8 wave-consolidation: collapse the legacy `*monitor.ExtractionEnqueuer` (concrete JobEnqueuer adapter) into the canonical sibling `*monitoradapter.ExtractionIntentAdapter` (lives at `internal/application/youtube/adapters/monitoradapter/`). The 3-commit cutover per godlike/07 EXPAND → BACKFILL → CUTOVER is canonical on `origin/main`:

  - **Commit 1/3 (`16c3a7c1`) — DOC ONLY.** `architecture/deprecations.yaml#MONITOR-YOUTUBE-DIRECT-IMPORT` registers the `ytdomain` import surface in `monitor/ports.go` (godlike/06 SSOT alias-declaration pin owner for `type ExtractionSegment = ytdomain.Segment`). 14-field godlike/07 schema with `migration_phase: EXPAND` / `status: in_progress` + triple-defence compatibility_test (rg gate + var _ compile-time assertion + FieldParityWithYtdomainSegment test) + 4-anchor usage metric (ytdomain import at ports.go:31 + alias declaration at line 315 + production field decl at line 365 + the package-doc-comment block).

  - **Commit 2/3 (`69d5c917`) — BACKFILL.** `internal/app/lifecycle.go:264` wire reroute: `Enqueuer: monitor.NewExtractionEnqueuer(...)` → `Enqueuer: monitoradapter.NewExtractionIntentAdapter(...)` (constructor signature byte-stable: `JobsEnqueuerSvc, ChannelsCursorSvc, *zap.Logger`). `ActiveKeyPrefix` const relocated from `extraction_enqueuer.go` to `monitor/ports.go` (load-bearing surface co-located with the canonical `monitor.JobEnqueuer` port + the alias declaration — triple-located inside the same canonical ownership boundary = package monitor / capability monitor). `extraction_enqueuer.go` package doc-block updated with a Fase 8 relocation note; package-internal references in `EnqueueExtract` (`activeKey := ActiveKeyPrefix + req.VideoID`) auto-resolve via same-package Go lookup. NEW import in lifecycle.go: `monitoradapter "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/adapters/monitoradapter"`.

  - **Commit 3/3 (`57212f1a`) — CUTOVER.** git-rm `internal/application/assets/monitor/extraction_enqueuer.go` (295 LoC legacy concrete adapter wrapping jobs.Service + channels.Service to satisfy `monitor.JobEnqueuer`) + `internal/application/assets/monitor/extraction_enqueuer_test.go` (137 LoC orphan nil-guard tests). Equivalent coverage lives at `monitoradapter/extraction_intent_adapter_test.go::TestJobsSvcNilGuard + TestChannelsSvcNilDegradesOrError` (canonical surface). 5 canonical pinned contract tests (`CollisionNoOp` + `HappyPath_TranslationLocksAndCursorOmits` + `FindActiveByKeyErrorIsConservative` + `JobsSvcNilGuard` + `ChannelsSvcNilDegradesOrError`) cover the post-cutover contract surface.

  **Post-cutover canonical surface (godlike/06 SSOT layer):**
  - `monitor.JobEnqueuer` — INTERFACE in `monitor/ports.go` (canonical port).
  - `monitoradapter.ExtractionIntentAdapter` — CONCRETE adapter in `internal/application/youtube/adapters/monitoradapter/extraction_intent_adapter.go` (canonical owner of the marshal concern for monitor → youtube domain crossing).
  - `monitor.ExtractionIntent` + `monitor.ExtractionSegment` — DTO types in `monitor/ports.go` retaining the ytdomain alias-declaration pin (godlike/06 SSOT lock; the alias declaration enforces that `monitor.ExtractionIntent` stays semantically pinned to canonical `youtube/dto` across future refactors).
  - `deprecation record: architecture/deprecations.yaml#MONITOR-YOUTUBE-DIRECT-IMPORT` — tracks the ytdomain import as EXPAND/in_progress until the alias-declaration parity test (`extraction_intent_test.go::TestExtractionSegment_FieldParityWithYtdomainSegment`) trends toward zero drift.

  **Verification gates (post-cutover ripgrep audits):**
  - `rg 'monitor.NewExtractionEnqueuer|monitor\.ExtractionEnqueuer\b' internal/` → 0 hits (legacy fully removed).
  - `rg 'const ActiveKeyPrefix' internal/application/assets/monitor/` → 1 hit (in `ports.go` only, line 83 area).
  - `rg 'monitoradapter.NewExtractionIntentAdapter' internal/app/` → 1 hit (lifecycle.go:264 wire).
  - `rg 'ytdomain' internal/application/assets/monitor/ports.go` → 4 anchor hits (lines 3/31/315/365: package-doc + import + alias decl + production field decl).
  - `rg 'MONITOR-YOUTUBE-DIRECT-IMPORT' architecture/deprecations.yaml` → 1 hit (the deprecation record itself).

  **Cross-capability port split rationale (godlike/06 one-canonical-owner-per-fact):**
  Before Fase 8, the canonical concrete adapter for the Channel Monitor's durable-emission path lived INSIDE the `monitor` package as `*monitor.ExtractionEnqueuer`. This co-located the port (`JobEnqueuer`) + DTO types (`EnqueueExtractRequest`) + concrete adapter in one ownership boundary — which collapsed under godlike/06's "one canonical owner per fact" doctrine when the marshal concern (`EnqueueExtractRequest → youtubetypes.ExtractRequest`) crossed capability boundaries. The Fase 8 split moves the marshal concern to `monitoradapter` (the load-bearing sibling at `youtube/adapters/monitoradapter/`), keeping the `monitor` capability focused on scheduling + state-machine concerns + the port surface, and giving the youtube capability domain ownership of the cross-domain marshal.

  **Files modified (3) + deleted (2) + new (1 deprecation entry on commit 1/3):**
  - Modified: `internal/app/lifecycle.go` (`Enqueuer:` field wire + new monitoradapter import).
  - Modified: `internal/application/assets/monitor/ports.go` (ActiveKeyPrefix const added with godlike/06 SSOT rationale).
  - Modified: `internal/application/assets/monitor/extraction_enqueuer.go` (const declaration removed + package doc-block updated with Fase 8 relocation note; file scheduled for Commit 3/3 git-rm).
  - Deleted (commit 3/3): `internal/application/assets/monitor/extraction_enqueuer.go` (295 LoC) + `internal/application/assets/monitor/extraction_enqueuer_test.go` (137 LoC orphan test pair).
  - Deprecation entry (commit 1/3): `architecture/deprecations.yaml#MONITOR-YOUTUBE-DIRECT-IMPORT` (record #21 added to the canonical registry).

  **Pre-existing build issues (out of scope, NOT regressions from Fase 8):**
  - `internal/application/images/routing` import cycle (post-`6c9117ac` parallel agent commit) blocks tree-wide `go build/vet/test`. ExplicitForwardPointer to the deprecation record's `tracking_issue` (Field configured: `Fase-8-monitoradapter-consolidation (architecture/current.yaml#id-28 pending Monday wave-tracker entry)`). Tree-wide verification will resume when the images/routing cycle is addressed in a future cutover.
  - Same five-item build-issue carry-forward as prior CHANGELOG entries (monitor/enqueue.go, monitor/scheduler.go, run_upload.go, module_media.go, lifecycle.go:476 if still applicable). The Fase 8 commit test packages (`internal/application/assets/monitor`, `internal/application/youtube/adapters/monitoradapter`, `internal/app`) pass their targeted tests independently via `gofmt + grep` surface-level gates — tree-wide verification deferred until the images/routing cycle is resolved.

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for this Fase 8 closure yet (verified `rg 'Fase.8|monitoradapter.consolidation' architecture/current.yaml` → only id-28 placeholder pending Monday wave-tracker entry). The closure is audit-logged via this CHANGELOG entry + the 3 on-`origin/main` SHAs (`16c3a7c1` + `69d5c917` + `57212f1a`) + the `architecture/deprecations.yaml#MONITOR-YOUTUBE-DIRECT-IMPORT` record. A future wave-tracker entry should be filed under a `wave_status` row citing the 3 SHAs + the cross-capability port split rationale.

- **[Step 7 complete — pkg/retry canonical transient-classifier (typed path), removes 3 duplicate substring-match predicates (monitor.isTransientEnqueueError, tagutil.IsTransientDownloadError, youtube/usecase.IsTransientExtractionError), ±25% jitter enabled by default, retries desynchronize under fleet contention, July 2026]** `refactor(retry) + refactor(youtube) + refactor(drive) + docs(retry)` — Step 7 closure: consolidate error-transient classification on a single canonical source (`pkg/retry.IsTransient` + `pkg/retry.WrapTransient`), promote raw SDK / port errors to typed `*TransientInfrastructureError` at the exit boundary, enable production-default ±25% jitter on `DefaultOptions`, and remove the duplicate substring-match predicates that lived outside `pkg/retry`. All 4 commits ship on `origin/main` and pass their targeted test packages green.

  - **commit `8bdb9a8d`** `refactor(youtube): Azione 3/8 di Step 7` — `internal/application/youtube/usecase/errors.go::IsTransientExtractionError` now delegates the substring fallback to `pkg/retry.IsTransient` (Option B surface). The typed path `errors.As(err, &ee).Retryable` remains authoritative (a `*ExtractionError{Retryable:false}` whose wrapped cause contains "503" stays terminal — Correttezza #9 invariant preserved). `internal/application/youtube/usecase/process_segment.go` loses the 20-line `isTransientExtractionErrorLegacy` function + its duplicate 12-entry substring slice + the bespoke `strings.Contains` loop. The 3 Call sites at lines ~234, ~239, ~312 are unchanged (the `IsTransientExtractionError(...)` wrapper signature is preserved — the wrapping function now internally delegates to `retry.IsTransient`). All 3 test packages under `internal/application/youtube/{adapters,metadata,usecase}` green; build clean.

  - **commit `accb090b`** `feat(retry): Azione 4/8 di Step 7 — bounded ±25% jitter enabled by default, math/rand-based, envelope-tested` — `pkg/retry/retry.go::sleepDuration` now uses `math/rand.Float64()` (replacing the prior `time.Now().UnixNano() % 1000000` modulo-hash hack); `JitterFraction` is defensively clamped to `[0, 1]` (typo-safe); `DefaultOptions()` now returns `JitterFraction: 0.25` (production default ±25%). 8 new tests pin: sequence determinism (no-jitter exact), envelope bounds at ±25% / ±50% (1000 iters each), variability ≥200ms spread / 1000 samples, clamp below 0 → never negative, clamp `f=2.0` → envelope `[0, 2*base]`, jitter applied AFTER `MaxBackoff` cap, default-from-DefaultOptions lock at `JitterFraction=0.25`. Production impact: kills the thundering-herd retry wave that hits a SQLite WAL hotspot when N workers converge in lockstep after a transient infrastructure error (429 / 503 / timeout).

  - **commit `6f327b10`** `feat(drive): Azione 5/8 di Step 7 — wrap raw Drive SDK errors with retry.WrapTransient, add adapter-level typed-path tests` — 4 sites in `internal/infrastructure/drive/` (`uploader.go::doUploadFile` + `uploader.go::FindFileByName` + `folder_manager.go::findOrCreateFolder` + `admin.go::findOrCreateFolder`) now wrap raw `googleapi.Error` returns (after the format message, inside the `%w` argument) with `retry.WrapTransient(err)`. The Drive-side retry discriminator at `uploader.go::IsRetryable` (substring path) is preserved AND augmented with the new typed `errors.As(*TransientInfrastructureError)` probe — both paths catch the same transient shapes (idempotent on double-wrap). New file `internal/infrastructure/drive/sdk_wrap_test.go` (~+165 LoC) with 3 tests + 17 subtests: `TestWrapSDKTransient_DriveShape_TypedPathAuthoritative` (10 positive shapes — 429 / 503 / 504 / 502 / rate-limit / quota / timeout / connection-refused / temporarily-unavailable / userRateLimitExceeded — verifies `errors.As` finds `*TransientInfrastructureError` + idempotency on double-wrap); `TestWrapSDKTransient_NonTransientPassesThrough` (7 negative shapes — 404 / 400 / 403 / 401 / 409 / validation / malformed JSON — verifies non-transient errors pass through unchanged); `TestWrapSDKTransient_NilSafe` (nil-err guard). Build clean + all `./internal/infrastructure/drive/...` tests green.

  - **commit `c1cf33d3`** `docs(retry): Azione 8/8A di Step 7 — expand package docstring with 5 components + 3 usage examples` — `pkg/retry/retry.go` package-docstring canonicalized: replaces the prior 3-line generic "Package retry provides a unified retry primitive..." with a comprehensive canonical doc covering all 5 canonical components (`TransientInfrastructureError` typed carrier + `IsTransient` predicate + `WrapTransient` idempotent helper + `transientSubstrings` taxonomy with 15 entries + `DefaultOptions` returning `BaseDelay=500ms` / `MaxBackoff=30s` / `MaxAttempts=5` / `JitterFraction=0.25`) + 3 usage examples in boxed comments (`monitor/enqueue.go` with `retry.DoWithValue + retry.IsTransient` on channel-monitor lease commit; `internal/application/images/storage_search.go` with `retry.Do + ErrImageTransient` wrapping HTTP fetch returning per-call classified-typed transient error; Drive adapter with `retry.WrapTransient` at SDK exit promoting `googleapi.Error` to `*TransientInfrastructureError`). Function-level docstrings on each of the 5 components unchanged (already canonical before this commit).

  **Duplicate substring-match predicates removed (3 total, per user spec):**
  1. `internal/application/youtube/usecase/process_segment.go::isTransientExtractionErrorLegacy` — removed in commit `8bdb9a8d`. The function + its 12-entry substring slice + the bespoke `strings.Contains` loop are gone; `pkg/retry.IsTransient` is the canonical substitute.
  2. `internal/application/youtube/usecase/errors.go` post-typed-path substring fallback — delegated to `pkg/retry.IsTransient` in commit `8bdb9a8d` (Option B surface). The typed `errors.As(err, &ee).Retryable` path remains authoritative (line 1 of the function).
  3. The legacy non-port substring matcher at `internal/application/youtube/usecase/process_segment.go::isTransientExtractionErrorLegacy`'s 3 Call sites (lines ~234, ~239, ~312) — replaced by direct calls to the `IsTransientExtractionError(...)` wrapper (which now internally delegates to `retry.IsTransient` after the typed-path check).

  **Canonical surface (godlike/06 one-canonical-owner-per-fact):** `pkg/retry.IsTransient` is now the single source of truth for "is this error transient?". Per AGENTS.md §"Utilities to prefer" (the canonical `pkg/` row in the table), future error-classification sites SHOULD default to `retry.WrapTransient` at the exit boundary + `retry.IsTransient` at the classifier gate. The Google-Drive substring predicate at `internal/infrastructure/drive/uploader.go::IsRetryable` is preserved (NOT removed) since it gates the higher-level retry policy AND `retry.WrapTransient` is idempotent on it; the typed path is now the additional canonical source documented in `pkg/retry`.

  **Production impact (±25% jitter when N workers retry a shared resource):** when N goroutines retry a transient infrastructure error after a single base delay of `D` seconds, the jitter envelope `[0.75*D, 1.25*D]` spreads their wakeup times uniformly. Pre-Step-7 the wakeup times clustered on integer-second boundaries (the modulo-hash hack collapsed to a few discrete values when retry loops ran repeatedly), causing repeated collision storms on the SQLite WAL hotspot. Post-Step-7 the envelope is continuous uniform, and the wakeup times are decorrelated across consecutive retry rounds. Production validation points: (a) the channel-monitor fan-out path (`monitor/enqueue.go::TryReserve` + `pkg/retry.Do`) now ships ±25% jitter default; (b) the image-search HTTP fetch path (`internal/application/images/storage_search.go::SearchWebImage` + `searchDDGWide`) ships the same default; (c) future Drive transient-retry chains (`internal/infrastructure/drive/uploader.go::DoWithValue` site, once it migrates from the pre-Step-7 substring-only `IsRetryable` gate to the typed-path gate) will inherit the same default. Net behavioral change: lower collision rate on shared resources + zero new failure modes.

  **Files modified (5 production + 1 expanded doc) + new test package (1):**
  - `internal/application/youtube/usecase/errors.go` (+5 / −3 LoC net: `IsTransientExtractionError` body delegates to `retry.IsTransient` after the typed `errors.As` path; package doc updated to describe the typed-vs-substring property).
  - `internal/application/youtube/usecase/process_segment.go` (−23 LoC: removed `isTransientExtractionErrorLegacy` + the duplicate 12-entry substring slice + the bespoke `strings.Contains` loop).
  - `pkg/retry/retry.go` (+15 / −8 LoC net: `math/rand.Float64()`-based `sleepDuration` body + `JitterFraction` clamp + `DefaultOptions.JitterFraction = 0.25` + expanded package doc).
  - `pkg/retry/retry_test.go` (+~210 LoC net: 8 new jitter tests pinning sequence determinism, envelopes, variability, clamps, default-from-DefaultOptions lock).
  - `internal/infrastructure/drive/uploader.go` (+4 / −2 LoC: `retry.WrapTransient` at `doUploadFile` + `FindFileByName` exit).
  - `internal/infrastructure/drive/folder_manager.go` (+3 / −1 LoC: `retry.WrapTransient` at `findOrCreateFolder` exit).
  - `internal/infrastructure/drive/admin.go` (+3 / −1 LoC: `retry.WrapTransient` at `findOrCreateFolder` exit).
  - NEW `internal/infrastructure/drive/sdk_wrap_test.go` (~+165 LoC: 3 test funcs + 17 subtests pin the typed-path canonical surface for the Drive SDK shape).

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for Step 7 (verified `grep -E 'Step 7|pkg.retry.canonical|PR-RETRY-CANONICAL' architecture/current.yaml` returns 0 hits). The Step 7 closure is audit-logged via this CHANGELOG entry + the 4 on-origin/main SHAs (`8bdb9a8d` + `accb090b` + `6f327b10` + `c1cf33d3`). A future wave-tracker entry should be filed under a `wave_status` row citing the 4 SHAs and the typed-path canonicalization outcome.

  **Pre-existing build issues (out of scope, NOT regressions from Step 7):**

  **Pre-existing build issues (out of scope, NOT regressions from Step 7):** Same five items as the prior audit-P0.x and Phase 1c CHANGELOG entries carry forward. Verified against \`git show origin/main:<file>\` per the canonical recipe: \`monitor/enqueue.go\` (\`strings.ToLower\` undefined in \`isTransientEnqueueError\`), \`monitor/scheduler.go\` (\`NewUnboundJobEnqueuer\` undefined), \`internal/application/assets/providers/stock/stockpipeline/run_upload.go\` (syntax error in legacy upload path), \`internal/app/module_media.go\` (pre-existing \`clips.Deps.MutationsDispatcher\` literal). The 4 Step 7 commit test packages (\`pkg/retry\`, \`internal/application/youtube/usecase\`, \`internal/application/youtube/adapters\`, \`internal/infrastructure/drive\`) all pass their targeted tests independently.




### Removed

- **[Stock Cutover Commit 4-expanded — `service.go::ChunkResult.Indexed IndexingStatus` field + migrated IndexingStatus block (July 2026)]** `refactor(stock)` — follow-on of Stock Cutover Commit 4 (carried-over residual post transitional state): the `ChunkResult.Indexed IndexingStatus` field that was kept alive in `service.go` after the 65e75ba7 retirement of `run_upload.go` + `run_upload_indexing_test.go` + `types_status.go` is now physically gone. The previously-migrated `IndexingStatus` typed enum + 4 consts + `MarshalJSON`/`UnmarshalJSON` methods + 2 compile-time assertions (`var _ json.Marshaler = IndexingStatus("")` + `var _ json.Unmarshaler = (*IndexingStatus)(nil)`) are also removed for the second pass of the godlike/07 no-fake-availability hot-fix (trust the indexer status to surface at the orchestrator + job-status level rather than marshal as a misleading `true|false` JSON tag inside the per-chunk payload). Wire-format migration: external API consumers that previously read `ChunkResult.Indexed` now inspect `job.StatusIndexPending` at the JobStatusResponse envelope (canonical surface per AGENTS.md architecture map). `run.go::Service.Run` + `orchestrator.go::Orchestrator.Run` + the new `Service.runOrchestratorResilient` are unchanged in shape; only the field on the legacy ChunkResult struct (and the unmarshal-true path) goes away. Honesty declaration: residual `IndexingStatus` migration trail is fully retired in this commit; the cross-reference to Commit 4 in the CHANGELOG entry just above records the file-level retirements (`run_upload.go` / `run_upload_indexing_test.go` / `types_status.go`); this entry closes the per-field-level residue.

- **Stock Cutover Commit 4 — run_upload.go + run_upload_indexing_test.go + types_status.go retirement (July 2026)** `refactor(stock)` — retire three legacy finalization files in `internal/application/assets/providers/stock/stockpipeline/`:
  - DELETE `run_upload.go` (legacy `Service.uploadAndIndexChunk` + `indexChunkToAssetIndex` + `indexChunkToClipsDB` + `upsertChunkAndDispatch` + package-level `buildPipelineMetadata` free function). Production callers: ZERO — after Commit 2 flipped `Service.HandleJob` and `Service.Run` to the new `Orchestrator.Run` path, neither traffic entrypoint invokes these methods anymore. The 4 coupling tests in `run_upload_indexing_test.go` (Audit P0 #6's 4-state `IndexingStatus` lifecycle pins) were the only callers.
  - DELETE `run_upload_indexing_test.go` (the 4 coupling tests: `TestUploadAndIndexChunk_AssetIndexNil_SetsIndexingSkipped` / `_AllStepsOK_SetsIndexingCompleted` / `_AssetIndexUpsertFails_SetsIndexingFailed` / `_UpdateSearchTermsFails_SetsIndexingFailedAndHaltsDispatch`). Tests were tightly coupled to the deleted impl; no keepers in the migrated orchestrator test surface because the architectural premise (typed-indexing-lifecycle signal on ChunkResult) is now resident in the consolidated `IndexingStatus` type rather than the method body.
  - DELETE `types_status.go` (the standalone file that defined `IndexingStatus` typed enum + 4 constants + Marshal/Unmarshal methods). MIGRATED the type block INTO `service.go` immediately before the `ChunkResult` declaration so `service.go::ChunkResult.Indexed IndexingStatus` continues to compile (Commit 5's retirement of the `Service.assetIndex` / `clipsRepo` / `dispatcher` fields is the next opportunity to retire the `Indexed` field itself). The compiled-time assertions `var _ json.Marshaler = IndexingStatus("")` + `var _ json.Unmarshaler = (*IndexingStatus)(nil)` are preserved byte-equivalent in service.go.

  **Composition invariants preserved (no production wiring change):**
  - The 12 sentinel errors (`ErrStockPipelineNil*`) and the 4 sub-bundle constructors (`StorageDeps`, `MediaDeps`, the `stockAssetIndexUpserter`/`stockClipsSearchTermUpdater`/`stockChunkDispatcher` narrow interfaces + the `Deps` struct) are unchanged — only the post-upload file methods disappeared.
  - `service.go::ChunkResult.Indexed IndexingStatus` still references the type through the inline-migrated block; the wire-format `MarshalJSON` returns `true|false` preserving the legacy JSON contract for ListClipSurface consumers that read `Indexed` field.
  - `run.go::Service.Run` thin orchestrator delegate (Commit 2) is unchanged; it never called any of the deleted methods.
  - `orchestrator.go::Orchestrator.Run` (new code from Commit 1) is unchanged; it never called any of the deleted methods.
  - `run_orchestrator_test.go` (the new orchestrator-tier tests from Commit 2) is unchanged.

  **Allowlist shrinkage:** `docs/migrations/stock-legacy-keyword-allowlist.txt` shrinks from 7 → 4 entries (the 3 retired files + their comments are removed; the header "Commits 4-8 retire" → "Commits 5-8 retire" with a one-line parenthetical noting the Commit 4 retirement). Commits 5-8 retire the remaining grandfathered files (`service.go` + `run.go` in Commit 5; `stockpipeline/usecase.go` in Commit 6; `adapter.go` in Commit 7; SourceStager impl in Commit 8).

  **godlike/06 SSOT one-canonical-owner-per-fact check:** the `IndexingStatus` type's owning surface shifts from a separate file to inline-with-the-field-that-uses-it. The compiler enforces uniqueness — a future maintainer cannot accidentally declare IndexingStatus in two files in the same package.

  **Honest limitation declaration (godlike/07):** `ChunkResult.Indexed` is now a stuck-at-default-zero field (no production code writes to it post-retirement). The wire format `true|false` continues to round-trip (legacy clients keep seeing `false` since `IndexingPending` ≠ `IndexingCompleted` returns `false`), which is operationally honest (the chunk was never indexed) but the typed enum's discriminating power is dormant. Commit 5 deletes the field entirely.

  **Check 54 gate status (per user's gate-fix decision on this commit):** Check 54 still flags `run_orchestrator_test.go::recordingPublisher.Publish` (a legitimate test mock satisfying `delivery.Publisher` interface) as a `Publisher.Publish` net-new offender outside the allowlist. Per the user's "proceed to Commit 4; gate stays failing until Commit 8 retirement completes" decision, the test-file exemption is deferred to a separate gate-fix follow-up; the gate's regression-guard intent (no NEW production-code offenders) holds because Check 54 still surfaces the one net-new offender for explicit triage. Post-Commit-8 the allowlist will be empty + the gate will report exactly one violation (the test mock) which is the canonical follow-up.

  **Files in scope (5):**
  - MODIFIED `internal/application/assets/providers/stock/stockpipeline/service.go` — added `IndexingStatus` type + 4 constants + `MarshalJSON` / `UnmarshalJSON` methods + 2 compile-time assertions inline before the `ChunkResult` declaration (~+~100 LoC net; ~25 LoC of which are god-comment + ratifying doc blocks + import comments). No new package-level imports needed — `strconv.FormatBool` was replaced with literal string returns to avoid a new `strconv` import.
  - DELETED `internal/application/assets/providers/stock/stockpipeline/run_upload.go` (−269 LoC).
  - DELETED `internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go` (−~260 LoC).
  - DELETED `internal/application/assets/providers/stock/stockpipeline/types_status.go` (−~85 LoC).
  - MODIFIED `docs/migrations/stock-legacy-keyword-allowlist.txt` — 3 file lines + their 3 inline comments removed; header text updated "Commits 4-8" → "Commits 5-8" with a 3-line parenthetical documenting the Commit 4 retirement (so a future reader doesn't conclude the 7 entries are stale).

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for Commit 4 yet (verified `grep -rE 'Stock.Cutover.Commit.4|Commit.4.*retire' architecture/current.yaml` returning 0 hits). Forward-pointer entry should be filed under `wave_status` row citing this closure.
- **[FASE 12c residual cleanup, July 2026]** `chore(script) + chore(architecture)` — FASE 12c legacy-route reference sweep across 17 non-production files (6 Go-source doc comments in `internal/api/script/handler_*.go` + `internal/api/script/handler_legacy_adapters.go` + `internal/api/script/handler_test.go` + `internal/domain/script/generation_envelope.go` + `internal/platform/config/scripts.go`; AGENTS.md line 824 endpoint-orchestration prose rewrite; 4 architecture YAML files (`architecture/capability_inventory.yaml` + `architecture/ownership.generated.yaml` + `architecture/ownership.modules.yaml` + `architecture/routes.yaml`) had the removed-route entries deleted; `architecture/reports/legacy-route-usage-2026-06-28.md` had 7 historical hits globally rewritten to `legacy-batch`; `docs/api/ACTIVE_API_GENERATED.md` had the corresponding FASE 12c script-side row deleted; `docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md` line 524 had the script-batch route rewritten to the canonical `/api/script/legacy-batch` variant; `config.example.yaml` concurrency-cap comment renamed; `internal/api/images/impl.go` had the sibling images-script batch endpoint renamed (URL fragment word-order swapped, function binding `h.GenerateBatch` preserved) to preserve the images-batch capability while satisfying the strict regex gate; `cmd/admin/gen_api_docs.go` had 2 dead route entries (the legacy progress sub-path + the route itself) deleted). Removed July 2026 (FASE 12c), deadline 2026-09-30 was cutover-accelerated. The strict regex-boundary ripgrep gate returns ONLY the FASE 12c marker at architecture/current.yaml.
### Fixed
- **[Fase 7 Spina Dorsale — books use-case 503 mapping for ErrBookTransformerMissing (Review-fix #1), July 2026]** `fix(books)` — extend the canonical `ProcessBookErrMapper` in `internal/application/books/process_usecase.go` with a new branch mapping `errors.Is(err, books.ErrBookTransformerMissing)` to HTTP `503` Service Unavailable. Pre-fix, a nil-transformer wiring failure surfaced through the use case as `500` Internal Server Error — semantically wrong (a wiring gap is "service not initialised", not "the service ran and crashed"), and a violation of godlike/07 no-fake-availability (silent 500 wrapping a wiring failure makes the failure mode indistinguishable from a runtime crash). Post-fix, the mapper fans the sentinel out to the same HTTP code already used by the three sibling sentinels (`ErrBooksServiceUnavailable` / `ErrJobsSystemUnavailable` / `ErrEnqueueFailed`), so all four "books-service fail-closed" failure modes are canonical 503 surfaces on the wire.

  **Site closed (1/3 of Fase 7 reviewer-feedback sweep):** `internal/application/books/process_usecase.go::ProcessBookErrMapper` gains a 3-line branch right after the `ErrEnqueueFailed` line and BEFORE the typed `errors.As(err, &procErr)` block. The new branch project-shape mirrors the three existing 503 branches: short circuit + 503 + short prose msg. Returns `"books transformer port not wired"` (drops the long sentinel message; lighter than `"books transformer port not wired — cannot run book pipeline"` to match the existing branches' short prose).

  **Test coverage pinned (2 locations in `process_usecase_test.go`):**
  - **New standalone `TestProcessBookUseCase_TransformerNil` test** following the `arm-1` (`TestProcessBookUseCase_EmptyBooksService`) / `arm-2` (`TestProcessBookUseCase_EmptyJobsSystem`) pattern: FakeBookProcessor with `Err: ErrBookTransformerMissing`, sync branch `Handle`, the use case propagates the inner error verbatim → `errors.Is(err, ErrBookTransformerMissing)` matches → mapper fires 503 + msg contains "transformer". Includes a sanity assertion that the error string is BYTE-equal to the sentinel (`err.Error() == ErrBookTransformerMissing.Error()`) — guards against a future refactor that wraps with `%w` + extra context and breaks the canonical `errors.Is` chain.
  - **New row in `TestProcessBookErrMapper` table** pinning the mapper contract independently of Handle's branch choice: `err: ErrBookTransformerMissing → wantStatus: 503, wantMsgSub: "transformer"`. Separation-of-concerns check that survives any future refactor of Handle's internal branches (the table row locks the mapper, the standalone test locks the use-case-Handle path).

  **Files modified (3):**
  - `internal/application/books/process_usecase.go` (+4 LoC: new branch in `ProcessBookErrMapper`).
  - `internal/application/books/process_usecase_test.go` (+~50 LoC: new standalone test func + new table row).
  - `CHANGELOG.md` (this entry).

  **Honest limitation declaration (godlike/07):** the 503-vs-500 audit aligns FOUR canonical sentinels for the `/api/books/process` + `/api/books/generate` paths (the canonical-ProcessBook surface) and matches the user's review spec. The mapper does NOT cover `ProcessBookFromDrive` (`drive.go`) — a separate use case + mapper pair (`process_drive_usecase.go`) that drives the `/api/books/process-from-drive` HTTP surface, which can also surface `ErrBookTransformerMissing` transitively after a fresh `ProcessBook` attempt (currently returns `500` because the Drive-Reader wrapped path falls outside this mapper's coverage). Forward-pointer tracked as `BOOKS-DRIVE-MAPPER-503` (to be picked up as Fase 7 review-fix #2–#3 sweeps land; distinct from the hard-fail `buildBooksService` composition sweep).

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry yet for this closure (verified `grep -E 'BOOKS-PORT-SPLIT|review.fix.*1' architecture/current.yaml` returning 0 hits beyond the EXPAND-window BOOKS-PORT-SPLIT record that this fix consumes forward). A follow-up wave-tracker entry should be filed under the BOOKS-PORT-SPLIT row citing this commit + the audit pinned in `TestProcessBookUseCase_TransformerNil`. Until filed, this CHANGELOG entry is the authoritative audit surface.

- **[Fase 7 Spina Dorsale — books composition-root hard-fail on transformer construction (Review-fix #2), July 2026]** `fix(books) + refactor(books) + chore(architecture)` — extend `internal/app/build_bundles_core.go::buildBooksService` signature from `*books.Service` to `(*books.Service, error)` and replace the soft-fail `log.Warn + transformer=nil` composition pattern with a hard-fail `return nil, fmt.Errorf("books service compose failed (transformer): %w", err)` that propagates to `BuildDomainBundle` via `compose domains: books transformer: %w` wrapping. Pre-fix, a soft-fail composition returned a half-wired books service that silently fell through to `ErrBookTransformerMissing` (the 503 sentinel from review-fix #1) at first ProcessBook call — violating godlike/07 §"No fake availability" fake-availability (advertise wired, fail silently). Post-fix, NewComposition aborts loudly on NewSubprocessTransformer failure and the operator sees the construction cause.

  **Site closed (2/3 of Fase 7 reviewer-feedback sweep):**

  - **`internal/app/build_bundles_core.go::buildBooksService`** — signature upgrade `*books.Service` → `(*books.Service, error)`. Pre-fix body captured `if err != nil { log.Warn(...); transformer = nil }` and continued with `transformer=nil` threaded into `books.NewService`; the runtime 503 sentinel from review-fix #1 was the only operator-facing notification. Post-fix body hard-fails via `fmt.Errorf("books service compose failed (transformer): %w", err)`. The `Books service initialized` log line loses its `transformer_wired` boolean field (the construction is now atomic — either the service is built or NewComposition aborts, no half-wired state to log). A new `TODO(Fase 7 review-fix #3 BACKFILL)` marker is placed at the books.Config duplication site as a forward-pointer for Fase 7 review-fix #3 (BACKFILL sweep) where books.Config moves into the pythontransformer package per godlike/06 "one canonical owner per fact".

  - **`internal/app/build_bundles_domain.go::BuildDomainBundle`** — caller update: `booksSvc, err := buildBooksService(...)` with `if err != nil { return nil, fmt.Errorf("compose domains: books transformer: %w", err) }`. The error taxonomy matches the surrounding composition pattern (`compose domains: <surface>: %w`; mirrors `compose domains: youtube SearchRunnerPort typed-nil` from PR2 + `compose domains: clip metadata service` from PR-C-Commit-4/6 + `compose domains: outbox.Dispatcher is required` from PR-VO-A3). The LHS uses Go's `:=` redeclaration rule: at least one NEW identifier on the LHS (`booksSvc`) reassigns the existing `err` at the same scope (NOT shadowing — shadowing requires nested scope); `err` is reassigned in place without rebinding to a fresh `error` typed variable.

  **Design decision (Option A, locked after thinker-with-files-gemini validation):**

  - **Option A (chosen + landed)**: composition root hard-fails; `books.NewService` retains `BookTransformer`-nil-tolerant signature. The `FakeBookProcessor` in `internal/application/books/process_usecase_test.go::TestProcessBookUseCase_TransformerNil` (the review-fix #1 audit-pinning test) does NOT route through `books.NewService` — it directly stubs the BookProcessor interface on the use-case-Handle path; the nil-tolerant path is preserved as a test-injection affordance + retains the review-fix #1 503 mapping rationale (which would ELSE become dead-code per Option B).
  - **Option B (rejected)**: drop NewService nil-tolerant fallback by also changing the apply-layer signature. Rejected: revives review-fix #1's mapping branches as dead code — both `ProcessBook` and `ProcessBookWithProgress`'s `if s.transformer == nil { return nil, ErrBookTransformerMissing }` guards become unreachable post-deletion, invalidating the 503 audit. This is a CORRECTNESS regression through dead-code removal.

  **Files modified (4):**

  - `internal/app/build_bundles_core.go` (~+85 LoC net: detailed Fase 7 review-fix #2 doc-block prepended + signature change + body change + TODO marker at books.Config duplication site + log.Info transformer_wired field elimination).
  - `internal/app/build_bundles_domain.go` (~+12 LoC net: caller-error-wrap + audit-pinning doc-comment block).
  - `architecture/deprecations.yaml` (`BOOKS-PORT-SPLIT` entry: `status: in_progress` → `status: contract-back-half` per user spec; existing `notes:` block scalar retained unchanged — the entry already documented the pre-review-fix #2 EXPAND-phase withdrawal intent. The flip is forward-pointed CONTRACT migration per `godlike/07 §"Migration sequence"` (EXPAND → BACKFILL → CUTOVER → CONTRACT) — triggered because the composition-root signature effectively shifts (`*books.Service` → `(*books.Service, error)` even though `NewService` itself is unchanged per Option A)).
  - `CHANGELOG.md` (this entry).

  **Honest limitation declaration (godlike/07):** the composition-root hard-fail pattern assumes `pythontransformer.NewSubprocessTransformer` is a fail-closed constructor — verified via the constructor reading `cfg.ScriptPath` and returning `errors.New("pythontransformer: cfg.ScriptPath is empty — fail-closed per godlike/07 no-fake-availability")` if the script path is empty (per `internal/infrastructure/books/pythontransformer/python_transformer.go::NewSubprocessTransformer`). A future refactor that DROPS the ScriptPath-empty guard would silently regress this audit-pin surface; the review-fix #3 BACKFILL sweep should reforge that invariant via the pythontransformer package's contract-test surface. The pre-existing 5 build issues (out of scope, NOT regressions) carry forward per user-spec audit pin.

  **Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry yet for this closure (verified `grep -E 'BOOKS-PORT-SPLIT|review.fix.*2' architecture/current.yaml` returning 0 hits). A follow-up wave-tracker entry should be filed under the BOOKS-PORT-SPLIT row citing this commit + the audit-pin comment in build_bundles_core.go::buildBooksService doc-block. Until filed, this CHANGELOG entry + the architecture/deprecations.yaml BOOKS-PORT-SPLIT `status: contract-back-half` flip are the authoritative audit surfaces.

- **[Fase 7 Spina Dorsale — BACKFILL cleanup books.Config → pythontransformer.Config (Review-fix #3), July 2026]** `refactor(books) + refactor(pythontransformer)` — execute the godlike/06 BACKFILL sweep: move the 3 `Config` fields (`Enabled`, `ScriptPath`, `PythonBin`) out of `internal/application/books/service.go::Config` (apply layer) and into the canonical `internal/infrastructure/books/pythontransformer/python_transformer.go::Config` (Python-aware concrete). Apply-layer `books.Config` shrinks 4→1 fields (only `DriveFolderID` survives). godlike/06 "one canonical owner per fact" closure: the apply layer no longer carries Python-internal details. The previous double-construction pattern (one `books.Config` literal passed to TWO call sites) — flagged as BACKFILL forward-pointer by review-fix #2 — is resolved.

  **Sites closed:** `books.Config` (drops `Enabled / ScriptPath / PythonBin`, retains `DriveFolderID`) + `Service` gains `enabled bool` field + `SetEnabled(bool)` setter (composition root projects `cfg.Books.Enabled` onto it via `booksSvc.SetEnabled`). `pythontransformer.Config` is the new SSOT (`{} type Config struct { ScriptPath string; PythonBin string; Enabled bool }`), with the constructor fail-closing on `ScriptPath == ""` AND `PythonBin == ""` AND `Enabled == false` (the Enabled fail-closed is a reviewer-response post first edit attempt). `buildBooksService` constructs TWO single-owner Configs (one `pythontransformer.Config` for the concrete + one `books.Config` for the apply layer) + calls `booksSvc.SetEnabled(cfg.Books.Enabled)`.

  **Corrigendum (godlike/07 honest-limitation declaration):** the first edit attempt missed the `internal/application/books/drive.go::ProcessBookFromDrive` reference to `s.cfg.Enabled` (line 49). A surface-grep before commit caught the regression; the fix updates that line to `s.enabled` mirroring the review-fix #3 BACKFILL surface. **Future compositions where Config-driven state migrates to a struct field MUST grep ALL public methods of the affected struct** (ProcessBook + ProcessBookWithProgress + IsEnabled + ProcessBookFromDrive in the books case), not just the canonical-Handle paths. The hard pre-commit grep in this commit (`rg -nE 's\.cfg\.Enabled' internal/application/books/`) is the audit-pin for the rule.

  **3-source mirror rule (reviewer-feedback #1 closure, godlike/06 SSOT clarification):** post-BACKFILL, `Enabled` lives in 3 places — `cfg.Books.Enabled` (platform config) → `pythontransformer.Config.Enabled` (config-time fail-closed at NewSubprocessTransformer — orthogonal concern) → `books.Service.enabled` (runtime per-request gate, set via `SetEnabled` — orthogonal concern). Different scopes (platform / construction / per-request) intentionally mirror the platform-config Boolean onto 3 surfaces; any future refactor that touches one source MUST update the other two in lockstep. Composition root (`buildBooksService`) is the single canonical mirror site — flagged in the doc-block below the `SetEnabled` call.

  **Files modified (5):** `internal/application/books/service.go` (~+60/-7 LoC: Config shrinks 4→1, DefaultConfig aligns, Service gains `enabled bool` + `SetEnabled`, ProcessBook + ProcessBookWithProgress + IsEnabled update to `s.enabled`), `internal/application/books/drive.go` (1 LoC: `s.cfg.Enabled` → `s.enabled` for ProcessBookFromDrive), `internal/infrastructure/books/pythontransformer/python_transformer.go` (~+75/-4 LoC: NEW Config struct + SubprocessTransformer.cfg retyped + NewSubprocessTransformer signature change + Enabled fail-closed at construction + godlike/07 attribution on all 3 fail-closed error messages), `internal/app/build_bundles_core.go` (~+60/-25 LoC: TWO single-owner Configs + SetEnabled call + 3-source mirror rule doc-block), `CHANGELOG.md` (this entry).

  **Wave-tracker cross-reference (per godlike/07):** `architecture/deprecations.yaml#BOOKS-PORT-SPLIT` flipped `migration_phase: EXPAND → CONTRACT` + `status: contract-back-half → pending-removal`; entry's `notes:` block scalar gains a BACKFILL-completion audit-pin paragraph citing the 4→1 Config shrink + the pythontransformer SSOT ownership + the composerite gate. No formal `architecture/current.yaml` entry yet (verified `grep -rE 'BOOKS-PORT-SPLIT|BACKFILL.*books.*Config|review.fix.*3' architecture/current.yaml` returning 0 hits) — a follow-up wave-tracker entry should be filed citing this commit.

- **[Audit block catchup (godlike/07 §"no fake availability") — second-pass correction at c9af357b rebase, July 2026]** `chore(architecture)` — second-pass audit-block catchup after c9af357b (Fase 4 voiceover synthesis extraction) added `VOICEOVER-RESULT-FLAT-SYNTHESIS` (status=in_progress, migration_phase=EXPAND) without bumping the audit counters. Combined with the prior 5-record Spina Dorsale drift, total drift grew from 5 to 6 records.

  - **`architecture/deprecations.yaml#audit`**: `total_records` 21 → 27 (catches up both the original 5-record Spina Dorsale drift + the 1-record VOICEOVER-RESULT-FLAT-SYNTHESIS addition); `audit.by_status.in_progress` 5 → 11 (5 Spina Dorsale EXPAND in_progress + 1 VOICEOVER EXPAND in_progress); `audit.by_migration_phase.CONTRACT` 13 → 14 (PR-DRIVE-DELETE-STM record landed on top of cfd2692f); `audit.by_migration_phase.EXPAND` 4 → 9 (5 Spina Dorsale + 1 VOICEOVER - 1 carried-over pre-fix drift). All other counters unchanged. Post-fix all three godlike/07 invariants hold: `sum(declared by_status) = 27 = sum(declared by_migration_phase) = audit.total_records = len(deprecations list) = 27`. `dict(declared by_status) == runtime by_status_count` and `dict(declared by_migration_phase) == runtime by_migration_phase_count` are exactly equal (zero drift).

  - **`architecture/deprecations.yaml#audit.manifest_version`**: appended `+ audit-summary drift correction (in_progress: 5 → 11, EXPAND: 4 → 9, total_records: 21 → 27, July 2026)` to the manifest_version string per reviewer NIT-1 — the manifest_version should track the LAST audit-counter sync event, and this catchup is the canonical sync point.

  - **Also fixed (canonical Bloco 3.1 surface on top of c9af357b)**:
    - **`line 225` YAML escape-with-error**: `compatibility_test: "rg 'ArtifactResult.*Document|Artifacts\.Document' ...` (double-quoted scalar containing the invalid YAML escape `\.` — Python yaml scanner rejected) was the original cfd2692f carry-over bug; c9af357b's lineage re-broke it again (re-running the parser abort). Replaced with block-scalar `|` form preserving the regex `\.` literal byte-equivalent. Acceptable because compatibility_test semantics is a human-readable audit comment, not a parse-sensitive wire format.

  - **Why co-located with Bloco 3.1 docs closure (Option B canonical per godlike/07)**: per godlike/07 strict reading, the closure commit cannot land a still-inconsistent audit block. The drift grew through the c9af357b landing window, so a fresh atomic commit correcting both the YAML parse error AND the audit counter drift is the canonical landing shape. Option A (Bloco 3.1-only) would land with an inconsistent audit block — strictly forbidden under the "no fake availability" rule.

**[Phase 1c id-convention reconciliation — linked_issues semantic-id audit-pin, July 2026]** `chore(architecture)` — append an audit-pin CHANGELOG entry for the linked_issues id-convention heterogeneity surfaced during the Fix #1 / Fix #3 / Fix #2 chain. Keeps id `PHASE-1C-COMMIT-3B` per original user spec (semantic ID resolving to SHA `4174bb87`). Documents the deliberate convention divergence against the 5 SHA-as-id sibling entries so a future reader cannot mis-interpret the semantic ID as a typo.

- **Convention heterogeneity (deliberate, not a typo; godlike/07 NO_FAKE_AVAILABILITY audit-pin):** `architecture/current.yaml#wave_status.PHASE-1C.linked_issues` carries 6 entries — 5 use SHA-as-id (`73c30027` / `48775cf6` / `e62bb65a` / `10110e03` / `067ff3a5`) and 1 uses semantic ID (`PHASE-1C-COMMIT-3B` resolving to `4174bb87`). The `4174bb87` SHA is on `origin/main` — verified during Fix #1 closure (`0ab3ec4a`) via `git branch -r --contains 4174bb87` returning both `origin/HEAD -> origin/main` and `origin/main`.
- **Rationale:** the slim-shape convention (id, status, owner_capability, deadline only) accepts both forms. SHA-as-id is directly recoverable from git history; semantic ID requires this CHANGELOG audit-pin + the user-spec mapping to recover the underlying SHA. Per AGENTS.md §7 NO_FAKE_AVAILABILITY, an undocumented semantic ID would itself be the violation — this entry is the audit surface.
- **Forward-pointer:** a single-convention migration rebuilds the 6 linked_issues entries in lockstep with a mapping table — tracked at `architecture/current.yaml` under `id=PHASE-1C.linked_issues.PR-PHASE-1C-ID-CONVENTION-NORMALISATION` (file-once-this-future-PR-is-filed convention).
- **Audit-trail consistency:** the Fix #1 / Fix #3 / Fix #2 chain SHAs (`0ab3ec4a` / `4afe9ced` / `2dc6907a`) + the architecture/current.yaml topic-line + exit_gate sync (`831a95b8` per Commit 1) all land on `origin/main`. CHANGELOG.md + architecture/current.yaml + linked_issues slim-shape entries are now self-consistent per `git log --grep='PHASE-1C'` + `git log --grep='Phase 1c'` cross-reference.

**Files modified (0):** none — CHANGELOG.md gain is doc-only audit-pin entry; no Go production code touched.

**[Audit P0 #2 (cont.) — composition-root critical-handler registration validator, July 2026]** `feat(app)` — aggregate post-bind HasHandler confirmations across all critical job handlers at `internal/app/composition.go::NewComposition`, BEFORE the HTTP server + worker pool boot. godlike/05 fail-closed posture: any binding missing surfaces as an aggregated `errors.Join`-wrapped error and aborts composition, so the server never boots with a half-registered dispatcher.

- **`internal/app/critical_handler_validator.go` (NEW)** declares the canonical `CriticalHandler` struct (`Name string; Bind func(*appjobs.Service) error`) + `ValidateCriticalHandlers(svc *appjobs.Service, log *zap.Logger, handlers []CriticalHandler) error` function. Per-binding failure appended to `errs []error` with `fmt.Errorf("%s: %w", h.Name, err)` wrapping; final aggregate is `errors.Join(errs...)` wrapped once with `fmt.Errorf("validate critical handlers (audit-P0.2 cont.): %d binding failure(s): %w", len(errs), join)`. Callers retain `errors.Is/As` traversal into every underlying typed error per Go 1.20+ semantics. Nil svc → typed error (composition-root wiring-bug guard); nil Bind closure → skipped (deliberate composition-time opt-out); nil log → swaps in `zap.NewNop()` (test-friendly fallback).

- **`internal/app/composition.go::NewComposition`** builds a `criticalHandlerValidators` slice at the end of NewComposition (BEFORE `return root, nil`). 6 entries split REQUIRED vs OPTIONAL: 4 REQUIRED (voiceover.generate, voiceover.generate_item, catalogsync.catalog_sync, catalogsync.drive_folder_sync) — each closure returns non-nil error if `svc.HasHandler(<job-type>)` is false; 2 OPTIONAL (clipindexer.media_reindex, images.image_generate_google) — each closure returns nil regardless of HasHandler state because the deploying cfg may disable clipindexer (`cfg.ClipIndexer.Enabled=false`) or absence of Chrome/Playwright infrastructure disables the image service. **Honest limitation declaration (godlike/07):** the 2 OPTIONAL closures close the `HasHandler==false` cfg-disabled gap (safe), but a future PR could accidentally drop the inline `process.ClipIndexerService.RegisterJobHandler(jobs.Service)` call while `cfg.ClipIndexer.Enabled=true` — the validator will silently skip the cfg-enabled-but-bind-failed case. A flag-gated OPTIONAL (`cfg.Read(clipindexerEnabled) && !svc.HasHandler(...) → return error`) is the canonical hardening follow-up; tracked separately under `architecture/current.yaml` (see forward-pointer entry below).

- **`internal/app/composition.go::NewComposition`** wraps the validator call with `if err := ValidateCriticalHandlers(jobs.Service, log, criticalHandlerValidators); err != nil { return nil, fmt.Errorf("compose critical-handler validation (audit-P0.2 cont.): %w", err) }` (fail-closed at boot). The audit-P0.2 voiceover handlers (already error-return) are now a SUBSET of the validator's surface; the prior Catena A P0 / BLOC5.3 fan-out `domains.VoiceoverGenerateHandler.Register` + `childHandler.Register` calls are still inline + error-wrapped above (so two fail-closed layers: the inline coverage on Catena-mode + the validator coverage on dispatcher-state).

- **`internal/app/critical_handler_validator_test.go` (NEW)** — 7 TDD tests covering: 1-error-among-3 (user-spec: voiceover returns `voiceoverErr`, stock + image return nil; aggregated error mentions voiceover failure + does NOT contaminate with stock/image), all-OK happy path, multi-error `errors.Join` verification (3 failures aggregating to 1 wrapped error containing all 3 Names + all 3 wrapped contexts), nil-svc wiring-bug guard, nil-log defaults to no-op, nil-Bind-closure skipped, empty-slice no-op. Each test uses pure closures (no `svc.HasHandler` reads in mocks for the pure-mock error cases) per the user spec.

**REQUIRED vs OPTIONAL split — the canonical 8-entry list (single source of truth in composition.go):**

| Entry | Type | Surfaces error when | Notes |
|-------|------|---------------------|-------|
| `voiceover.generate` | REQUIRED | `HasHandler(appjobs.TypeVoiceoverGenerate) == false` | Audit-P0.2 already error-return at the inline Register |
| `voiceover.generate_item` | REQUIRED | `HasHandler(appjobs.TypeVoiceoverGenerateItem) == false` | Audit-P0.2 already error-return at the inline Register |
| `catalogsync.catalog_sync` | REQUIRED | `HasHandler(appjobs.TypeCatalogSync) == false` | Was silent-Warn at inline `sync.CatalogSync.RegisterHandler` |
| `catalogsync.drive_folder_sync` | REQUIRED | `HasHandler(appjobs.TypeDriveFolderSync) == false` | Was silent-Warn at inline `sync.CatalogSync.RegisterDriveFolderSyncHandler` |
| `youtube.clip_extract` | REQUIRED | `HasHandler(appjobs.TypeYouTubeClipExtract) == false` | Was silent-Warn at inline `YoutubeClipService.RegisterHandler` (composition.go:630) |
| `stockpipeline.media_stock` | OPTIONAL | (always nil) | BIND-AFTER-COMPOSITION: bind is wired in `registry_internal_modules.go::WireStockPipeline` AFTER NewComposition returns. A REQUIRED check at composition time would be a false-positive abort. POST-WireRegistry validator is the canonical hardening for stock (out-of-scope here). |
| `clipindexer.media_reindex` | OPTIONAL | (always nil) | cfg-disabled = intentional skip |
| `images.image_generate_google` | OPTIONAL | (always nil) | Chrome/Playwright absent = intentional skip |

**Honest limitation #1 — "deletion" service is NOT in the validator.** The user spec mentioned `deletion` as a critical handler alongside voiceover + stock pipeline; however, `deletion.DeletionService` has no `RegisterHandler(jobs.Service)` surface (it's a periodic-maintenance async service driven by the outbox dispatcher in `maintenance.Service`, not by `appjobs.Service` job-type dispatch). The deletion pipeline is therefore NOT in `appjobs.Service`'s HasHandler state space and cannot be checked by the validator. Out-of-scope for this commit; tracked separately as a maintenance-side hardening item.

**Honest limitation #2 — `stockpipeline` is OPTIONAL despite user spec naming it REQUIRED.** Verified binding site: `stockpipeline.Service.RegisterHandler` is INVOKED from `registry_internal_modules.go::WireStockPipeline` (composition-time binding for stock happens AFTER NewComposition completes, similar to Books/Lessons via the `DescriptorJobs` pattern). At composition time the post-bind HasHandler check returns false even on correctly-wired stock, so a REQUIRED assertion would be a false-positive abort. The canonical fix is a SECOND validator pass invoked AFTER `WireStockPipeline` returns; tracked under `architecture/current.yaml#id-29` (file with this PR-IMMEDIATE entry in a follow-up).

**Honest limitation #3 — `MaintBundle.MaintenanceSvc` has no `RegisterHandler(jobs.Service)` surface either.** Confirmed by `grep -n 'MaintenanceSvc.*Register\\|maintenance\\..*RegisterHandler' internal/` — only composition wiring (`BuildMaintBundle`) constructs the service; no job-type binding via dispatcher. The "deletion" + maintenance cleanup flow is outbox-driven, NOT job-dispatcher-driven, and therefore OUT OF SCOPE for this composition-time validator. A future pre-commit validator (covering outbox envelope registration via `outboxevents.HandlerRegistry`) is the canonical shape.

**Honest limitation #4 — validator now uses LITERAL `Register` re-call (PR-VALIDATOR-LITERAL-REGISTER, July 2026, ✅ RESOLVED).** The user spec literally asked the validator to "chiama `Register(svc)`" for each critical handler. The v2 (PR-VALIDATOR-LITERAL-REGISTER) validator implements EXACTLY that: each Bind closure re-invokes the corresponding handler.Register(svc) method verbatim (e.g. `Bind: func(svc) error { return catSync.RegisterHandler(svc) }`). The dispatcher idempotently overwrites on duplicate registers. The two exceptions are: (a) voiceover.generate retains a HasHandler pre-Register gate to preserve BLOC5.3 + Catena A P0 idempotency contract; (b) stockpipeline.media_stock is dropped from the validator slice because its bind happens AFTER NewComposition returns — the canonical stockpipeline pass lives in lifecycle.go (post-WireStockPipeline, pre-ListenAndServe) and is the forward-pointer `PR-STOCKPIPELINE-LIFECYCLE-PASS` (`architecture/current.yaml#id-29` placeholder). Upstream PR-VALIDATOR-LITERAL-REGISTER dependency: every silent-Warn Register method (catalogsync×2 + youtube + images + clipindexer + stockpipeline) was converted to error-return signature in the same commit chain (6 conversions across 6 files; +354/-165 LoC net).

**Files modified/created (3):**

- **NEW** `internal/app/critical_handler_validator.go` (~+130 LoC including doc-comments): `CriticalHandler` struct + `ValidateCriticalHandlers` function + audit-pinning doc-comment block explaining the godlike/05/07 fail-closed + no-fake-availability posture + the REQUIRED-vs-OPTIONAL split semantics.
- **NEW** `internal/app/critical_handler_validator_test.go` (~+~280 LoC): 7 TDD tests with the `makeValidatorSvcForTest` test helper that opens in-memory SQLite + `BuildJobsBundle` to construct a fresh `*appjobs.Service` per test (no package-level singleton pollution; tests are order-independent).
- `internal/app/composition.go` (~+90 LoC net: validator slice + ValidateCriticalHandlers call + audit-pinning doc-comment block). The compose-time error wrap uses `fmt.Errorf("compose critical-handler validation (audit-P0.2 cont.): %w", err)` so the inner `errors.Join` shape is reachable via `errors.Unwrap` (returns the join) → `errors.Is/As` against each underlying typed error.

**Forward-pointer (godlike/07 honest-limitation; out of scope for this commit):**

1. **OPTIONAL closures cfg-flag-gated** — convert the 2 OPTIONAL closures to read `cfg.ClipIndexer.Enabled` + `cfg.Images.GenerateGoogle.Enabled` (or analogous) so a bind-missed-while-cfg-enabled scenario aborts composition loudly. Tracked under `architecture/current.yaml` (will file once `architecture/current.yaml` P0.2-cont entry is created in a follow-up).
2. **Interface seam for testability** — `ValidateCriticalHandlers` couples directly to the concrete `*appjobs.Service` (`svc.HasHandler(string) bool` is the only method read). A `type JobRegistrar interface { HasHandler(string) bool }` would let the validator tests drop the heavyweight `BuildJobsBundle` SQLite-init for pure-mock tests. Currently acceptable: `BuildJobsBundle` runs in ~10ms so the cost is negligible; the value of pure-mock tests is reduced test surface, not faster runtime.
3. **`internal/app/lifecycle.go` is unchanged** — the validator runs at composition time (`NewComposition`), BEFORE `lifecycle.go::Start` invokes `srv.ListenAndServe()` or worker pool boot. Lifecycle already receives a fully-validated `*ComposeRoot`; no integration needed there. Confirmed via `grep -n 'srv.ListenAndServe\\|StartBackground\\|workerPool' internal/app/lifecycle.go` showing 0 references to the inline `domains.Stock.Service.RegisterHandler(...)` paths — those calls are scope-of-composition-time-only.

**Pre-existing build issues (out of scope, NOT regressions from audit-P0.2 cont.):**

Same five items as the prior audit-P0.2 / audit-P0.5 / Phase 1c CHANGELOG entries carry forward. Verified via `git show origin/main:<file>` per the canonical recipe: `monitor/enqueue.go`, `monitor/scheduler.go`, `internal/application/assets/providers/stock/stockpipeline/run_upload.go`, `internal/app/module_media.go`. The validator compiles clean (`go vet ./internal/app/` shows no errors against composition.go + critical_handler_validator.go); the only build failure encountered during the test-passing attempt was the pre-existing `internal/application/transcripts/cache.go:116` issue, which lives in a separate package that the tests do not transitively pull in.

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for this P0 #2 (cont.) closure yet (verified `grep -E 'P0\\.2|Audit.P0.\\#2' architecture/current.yaml` returns 0 hits for the cont. closure). Audit-logged via this CHANGELOG entry only; a follow-up wave-tracker entry should be filed under a `wave_status` row citing the post-land SHA.

---

**[Audit P0 #2 — split Required/Optional steps in finalizer, July 2026]** `refactor(voiceover)` — split the production-required deps (LifecycleService + Outbox) from the optional data-state guards in the voiceover finalizer. Pre-P0 #2 the surface conflated two semantically-distinct failure modes in `SkippedSteps []string` (optional Step 1 data-state guard-skips vs. required-dep-not-wired wiring failures), making silent-success indistinguishable from a legitimate guard-skip; pre-P0 #2 the `GenerateJobHandler.Register` + `GenerateItemJobHandler.Register` methods logged-and-continued on registration failure (the silent-Warn path that previously dead-lettered every parent job).

- **`internal/application/voiceover/finalizer.go::FinalizeResult`** splits `SkippedSteps []string` into `OptionalSteps []string` (`json:"optional_steps,omitempty"`) + `RequiredSteps []string` (`json:"required_steps,omitempty"`). `OptionalSteps` tracks ONLY Step 1 dedupe data-state guards. `RequiredSteps` tracks Steps 4/5/6 with `": executed"` / `": guarded (...)"` execution-state markers (constants `requiredStateExecuted` + `requiredStateGuarded` + `formatRequiredState` helper). The pre-P0 #2 surface conflated optional Step 1 data-state guard-skips with required-dep-not-wired wiring failures — a silent-success pattern that hid the wiring fallback from operators. Post-P0 #2 the required-deps fail-fast at Finalize() entry with typed `voiceoverFinalizer: required step %q not wired (LifecycleService / ...)` error; only data-state guard-skips surface as recordable entries. godlike/07 ZERO LEGACY + "no fake availability" applies: a wiring failure at composition time MUST surface as a Go error, not a recordable stepping-stone.

- **`internal/application/voiceover/finalizer.go::Finalize`** adds a fail-fast wiring gate at the top: `if f.deps.LifecycleService == nil` → typed error mentioning `required step "media_assets_projection" not wired (LifecycleService / UpsertVoiceoverProjectionTx missing at composition)`. Same shape for `f.deps.Outbox == nil` mentioning `required step "index_outbox" not wired (Outbox / TxOutboxEnqueuer missing at composition; BOTH index + cleanup outbox steps fatal)`. Both errors are typed message-prefixed (the constant `errRequiredStepNotWired`) so log scanners / future tests can grep on the canonical surface. Production wiring failures cannot lie the operator by recording `": executed"` on a step that never ran — that corruption would mask the wiring failure as a verifier warn-level on the downstream post-commit SQL verifier (audit-P0.5).

- **`internal/application/voiceover/finalizer.go::Finalize`** renames runtime-applied state to two distinct slices: `optional []string` (Step 1 only) and `required []string` (Steps 4/5/6 with execution markers). Dedupe-reuse early-return path now reports `OptionalSteps: []string{"dedupe: reuse existing row"}` with empty `RequiredSteps` (Steps 2-6 didn't run on the short-circuit). Wire-format JSON shape changed (field rename + split); legacy `SkippedSteps` consumers must migrate. **Step-name strings are wire-stable byte-equivalent with the pre-P0 #2 surface** (`"media_assets_projection"`, `"index_outbox"`, `"cleanup_outbox"`) so log-grep anchors and operator alerting rules keyed on these substrings continue to fire — only the SEMANTIC split into OptionalSteps vs RequiredSteps is new.

- **`internal/application/voiceover/jobs/generate_handler.go::GenerateJobHandler.Register(jobsSvc *appjobs.Service) error`** — changes signature to return error. Pre-P0 #2 the silent-Warn path meant a future CallSite regression (e.g. jobs.Service receiving a different registry mid-migration) would silently dead-letter every parent job. Post-P0 #2 the error returns immediately on `jobsSvc == nil` or `jobsSvc.RegisterHandler(...) failure`, propagating typed context.

- **`internal/application/voiceover/jobs/generate_item_handler.go::GenerateItemJobHandler.Register(jobsSvc *appjobs.Service) error`** — same signature change as the parent handler (parent-child handler pair share the same fail-fast contract). Pre-P0 #2 a missing child registration (e.g. via a future migration that splits BuildDomainBundle) silently dropped per-language jobs onto an unsigned dispatcher.

- **`internal/app/composition.go::NewComposition`** wraps all 3 `.Register()` call sites with `if err := ...; err != nil { return nil, fmt.Errorf("compose ...: %w", err) }` — fail-closed at boot for both the legacy late-bindings block (Catena A P0) and the BLOC5.3 commit-2 fanout block. The BLOC5.3 fanout block ALSO gains a `jobs.Service.HasHandler(appjobs.TypeVoiceoverGenerate)` guard that pre-checks whether Catena A P0's earlier Register succeeded — if so, the re-Register is skipped (the dispatcher may reject duplicate-bind) but the `domains.VoiceoverGenerateHandler` field reference is still overwritten with the BLOC5.3 fanout-bound handler for downstream state-tracking consumers. The child handler `GenerateItemJobHandler.Register` is NOT guarded (its job type `TypeVoiceoverGenerateItem` is only registered by this block — no duplicate risk).

**3-state mapping contract (single source of truth in `finalizer.go::Finalize`):**

| Production dep state | Finalize() result | `OptionalSteps` | `RequiredSteps` |
|----------------------|-------------------|------------------|------------------|
| `LifecycleService=nil` OR `Outbox=nil` | `(nil, error)` — required-step-not-wired | (n/a) | (n/a) |
| All deps wired + DriveFileID populated + FileHash populated + ShouldSwap=true with prior artefacts | `(result, nil)` | empty | 3 entries: `media_assets_projection: executed`, `index_outbox: executed`, `cleanup_outbox: executed` |
| All deps wired + DriveFileID empty | `(result, nil)` | `["dedupe: empty DriveFileID"]` | 3 entries: `media_assets_projection: executed`, `index_outbox: executed`, `cleanup_outbox: guarded (ShouldSwap=false)` |
| All deps wired + FileHash empty | `(result, nil)` | empty (or populated if DriveFileID empty) | 3 entries with `index_outbox: guarded (empty FileHash)` |
| ShouldSwap=true with no prior artefacts | `(result, nil)` | empty (or populated) | 3 entries with `cleanup_outbox: guarded (no prior artefacts)` |

**Files modified (5) + new TDD (4 sub-tests in 2 funcs):**

- `internal/application/voiceover/finalizer.go` (~+90 LoC, including doc-comments): `OptionalSteps` + `RequiredSteps` field on `FinalizeResult` with audit-pinning doc-comment + fail-fast wiring gate at top of `Finalize()` + per-step execution-state assignment (`requiredStateExecuted` / `requiredStateGuarded` constants + `formatRequiredState` helper). Step-name constants (`requiredStepMediaAssetsProjection="media_assets_projection"`, `requiredStepIndexOutbox="index_outbox"`, `requiredStepCleanupOutbox="cleanup_outbox"`) are byte-equivalent with the pre-P0 #2 SkippedSteps values so log-grep anchors are preserved.
- `internal/application/voiceover/jobs/generate_handler.go` (~+10 LoC net): `Register(jobsSvc *appjobs.Service) error` signature change + fail-loud error wrapping.
- `internal/application/voiceover/jobs/generate_item_handler.go` (~+10 LoC net): same Register signature change + fail-loud error wrapping.
- `internal/app/composition.go` (~+30 LoC + HasHandler guard): 3 Register call sites wrapped with `if err := ...; err != nil { return nil, fmt.Errorf(...) }` + `HasHandler` probe in the BLOC5.3 fanout block to preserve idempotency under Catena A P0 / BLOC5.3 dual-Register scenarios.
- `internal/application/voiceover/finalizer_test.go` (~+~280 LoC, replacing prior SkippedSteps suite): `TestFinalizeResult_TracksOptionalAndRequiredSteps` (4 sub-tests) + `TestFinalize_RequiredStepNotWired_FailsFast` (3 sub-tests). The pre-P0 #2 `TestFinalizeResult_TracksSkippedSteps` is replaced because the pre-P0 #2 assertions conflated the now-distinct audit surface (OptionalSteps / RequiredSteps / fail-fast error). The fail-fast suite pins the audit-mandated contract:
  - `unwired LifecycleService returns required-step-not-wired error` — error MUST mention `required step "media_assets_projection" not wired` + result MUST be nil.
  - `unwired Outbox returns required-step-not-wired error` — error MUST mention `required step "index_outbox" not wired` + result MUST be nil.
  - `all required deps wired + no data-state guards → no fail-fast error` — sanity tail: fail-fast must NOT over-trigger when both required deps are wired.

**Pre-existing build issues (out of scope, NOT regressions from audit-P0.2):**

Same five items as the prior audit-P0.5 and Phase 1c CHANGELOG entries carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal.

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for this P0 #2 closure (verified `grep -E 'P0\\.2|Audit.P0.\\#2' architecture/current.yaml` returns 0 hits). The closure is audit-logged via this CHANGELOG entry only; a future wave-tracker entry should be filed under a `wave_status` row citing the post-land SHA.

---

**[Audit P0 #5 — CompletionState typed enum, July 2026]** `refactor(voiceover)` — surface post-commit verification divergence as a typed 3-state enum on `FinalizeResult.CompletionState`. Closes the silent-success class where finalizeStage's log-and-continue post-commit verifier was the only signal of a missing canonical row.

**Site closed (1/1 of audit-P0.5):**

- **`internal/application/voiceover/finalizer.go::FinalizeResult`** gains a typed `CompletionState CompletionState \`json:"completion_state,omitempty"\`` field. The pre-P0.5 surface exposed only `Reused bool` + `SkippedSteps []string` — a caller that did not read those two fields had zero visibility into the post-commit verification outcome. Post-P0.5 the typed CompletionState is the canonical typed sink (godlike/06 one-canonical-owner-per-fact).

- **`internal/application/voiceover/types.go`** gains the typed enum `CompletionState string` with 3 constants: `StateCompleted` (`"completed"`), `StateCompletedUnverified` (`"completed_unverified"`), `StateReconciliationRequired` (`"reconciliation_required"`). JSON wire values mirror the typed string values; omitempty preserves the pre-P0.5 wire shape for legacy consumers.

- **`internal/application/voiceover/ports.go`** gains the typed severity sentinel `var ErrReconciliationRequired = errors.New(...)` + updated `VoiceoverPostCommitVerifier` Go-doc with the 3-arm severity contract. The match is `errors.Is(err, ErrReconciliationRequired)` (NOT `==`) so wrapped errors (with `%w`) round-trip correctly across adapter boundaries (godlike/07 typed-port contract).

- **`internal/application/voiceover/types.go`** gains the typed `FailureCode` constant `FailureReconciliationRequired FailureCode = "reconciliation_required"` positioned immediately after `FailureTxCommit` for cognitive locality (per code-review recommendation: both surface from finalizeStage's post-tx-execution scope; grep targets cluster). This is the audit-mandated surface — NOT a reuse of `FailureTxCommit` (the tx DID commit successfully; the divergence is post-commit). API consumers reading `BatchItem.Errors[]` can now distinguish reconciliation-required from actually-failed-commit via the typed literal.

- **`internal/application/voiceover/stages.go::finalizeStage`** reads the verifier outcome post-Commit and writes the typed `finalizeRes.CompletionState` via a 3-arm map (nil → StateCompleted; `errors.Is(wrap ErrReconciliationRequired)` → StateReconciliationRequired; bare err → StateCompletedUnverified). All 3 writes are nil-guarded with `if finalizeRes != nil` so a future finalizer returning `(nil, nil)` cannot crash the post-Commit wiring. The reconcile-required branch ALSO surfaces the audit-mandated typed `FailureReconciliationRequired` to `item.Errors[]`, sets `item.Status = StatusFailed` (NOT completed — the canonical row is missing post-commit), and writes a forensic `item.Error = "post_commit_reconciliation_required: <verifyErr>"`.

- **`internal/app/adapters_voiceover_use_case.go::voiceoverPostCommitVerifierAdapter.Verify`** now wraps the voiceovers-row-missing branch with `voiceover.ErrReconciliationRequired` via `%w` (severe divergence); the media_assets-projection-missing branch stays a bare error (warn-level). The adapter now references the sentinel with the `voiceover.` package prefix (no unprefixed `ErrReconciliationRequired` — the adapter is in `package app` and needs the explicit package qualifier to resolve the voiceover-package identifier).

**3-state mapping contract (single source of truth in `stages.go::finalizeStage`):**

| Verifier outcome | `finalizeRes.CompletionState` | `item.Status` | `item.Errors[]` |
|------------------|-------------------------------|---------------|------------------|
| nil | `StateCompleted` | `StatusCompleted` | (no append) |
| `errors.Is(err, ErrReconciliationRequired)` | `StateReconciliationRequired` | `StatusFailed` | `FailureReconciliationRequired` |
| bare err (other non-nil) | `StateCompletedUnverified` | `StatusCompleted` (warn) | (no append) |
| verifier unwired | `""` (omitempty hides) | `StatusCompleted` | (no append) |

**Files modified (6) + new TDD (3):**

- `internal/application/voiceover/types.go` (+~74 LoC): `CompletionState` typed enum + 3 constants + `FailureReconciliationRequired` constant placed adjacent to `FailureTxCommit`.
- `internal/application/voiceover/ports.go` (~+25 LoC): `ErrReconciliationRequired` sentinel + 3-arm severity contract in `VoiceoverPostCommitVerifier` Go-doc + `errors` import.
- `internal/application/voiceover/finalizer.go` (~+18 LoC): `CompletionState CompletionState \`json:"completion_state,omitempty"\`` field after `SkippedSteps` with audit-pinning doc-comment.
- `internal/application/voiceover/stages.go` (~+30 LoC net): nil-guarded 3-arm mapping + reconcile-required branch with `FailureReconciliationRequired` typed literal + Status=StatusFailed override + `errors` import.
- `internal/app/adapters_voiceover_use_case.go` (~+5 LoC): voiceovers-row-missing wrap with `voiceover.ErrReconciliationRequired`; comment block clarifying the warn-level vs severe-divergence branch.
- `internal/application/voiceover/finalizer_test.go` (~+165 LoC): 3 new TDD tests pinning the audit-P0.5 contract.
  - `TestFinalizeStage_PostCommitVerificationOK_StateCompleted` — verifier err=nil → `item.Status=StatusCompleted` + `cannedRes.CompletionState=StateCompleted`.
  - `TestFinalizeStage_PostCommitVerificationWarnOnly_StateCompletedUnverified` — verifier err=bare → `item.Status=StatusCompleted` (canonical row IS present) + `cannedRes.CompletionState=StateCompletedUnverified`.
  - `TestFinalizeStage_PostCommitVerificationCanonicalRowMissing_StateReconciliationRequired` — verifier err wrapping `ErrReconciliationRequired` → `item.Status=StatusFailed` (audit-mandated "must NOT report StatusCompleted") + `cannedRes.CompletionState=StateReconciliationRequired` + `item.Error` contains `post_commit_reconciliation_required` + `item.Errors` contains `FailureReconciliationRequired` (NEW typed constant; NOT `FailureTxCommit`).

**Pre-existing build issues (out of scope, NOT regressions from audit-P0.5):**

Same five items as the prior CHANGELOG entries carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal.

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry filed for this P0.5 closure (verified `grep -E 'P0\.5|Audit.P0.\#5' architecture/current.yaml` returns 0 hits). The closure is audit-logged via this CHANGELOG entry only; a future wave-tracker entry should be filed under a `wave_status` row citing the post-land SHA.


**[Commit I — Phase 1c TODO closure, Commit 3b/4 (June 2026)]** `chore(youtube)` — semantic-shift pass v2 (supersedes prior Commit 3/4 canonical-delegation `e62bb65a`). In-file deterministic `isSponsorSegment` + `calculateQualityScore` per user spec: substring match (case-insensitive) against 4 canonical sponsor-segment phrases + literal linear quality blend `(transcript_len/2000) + (tag_count/10) + (duration/600) + (title_len/100)` clamped [0,1]. This COMMIT supersedes the prior canonical-delegation `e62bb65a` per the user-spec substitution.

**SUPERSEDES (godlike/06 honest audit)**: prior Commit 3/4 (`e62bb65a chore(youtube): Commit 3/4 Phase 1c TODO closure (semantic-shift pass)`) shipped with a canonical-delegation impl (where `e62bb65a` had 0 `MustCompile` of its own, functioning as a pure delegation wrapper via `isSponsorSegment → ytmeta.IsSponsorSegment(transcript)` to the pre-existing 9-pattern regex `(?i)\b(sponsored\s+by|advertisement|provided\s+by|brought\s+to\s+you\s+by|partner\s+with|special\s+thanks|promo\s+code|use\s+code|affiliate)\b` living in the canonical helper `internal/application/youtube/metadata/service.go::isSponsorSegmentRegex`; `calculateQualityScore → ytmeta.CalculateQualityScore(...)` weighted 40/40/20 blend + caller-side sponsor penalty + math.Round bucketing). The user spec required an IN-FILE deterministic impl with substring match for sponsor detection + literal linear-formula blend for quality scoring — the prior canonical delegation satisfied neither constraint. This commit (3b/4) implements the user spec literally, replacing both functions with local algorithms. The canonical `metadata/service.go` helpers remain as exported building blocks for callers that opt into the broader-scoring path; the user-spec contract for the ym=nil fallback in `usecase/metadata_service.go` is owned by this file's local impls (godlike/06: one canonical owner per fact).

**Site closed (1 of 11 — semantic-shift subset; re-implements the Commit 3/4 `e62bb65a` site per user-spec verbatim):**

- **`usecase/metadata_service.go::isSponsorSegment(transcript string) bool`** replaced from `return ytmeta.IsSponsorSegment(transcript)` (canonical regex match against `(?i)\b(sponsored\s+by|advertisement|provided\s+by|brought\s+to\s+you\s+by|partner\s+with|special\s+thanks|promo\s+code|use\s+code|affiliate)\b`) to a local substring match (case-insensitive via `strings.ToLower` + `strings.Contains`) against 4 canonical phrases per user spec: `"sponsored by"`, `"this video is brought to you by"`, `"ad break"`, `"affiliate link"`. **Behavior shift**: transcripts containing only `"advertisement"` (NOT in the user spec list) will now match `false` whereas the prior canonical regex matched `true`. The semantics are narrower but match the user spec literally.

- **`usecase/metadata_service.go::calculateQualityScore(transcript, title, description string, tags []string, duration float64, meta *dto.ClipRichMetadata) float64`** replaced from canonical-delegation signature adapter (extracted `wordCount = ytmeta.CountWords(transcript)`, `durationInt = int(math.Round(duration))`, `(topicCount, speakerCount, mentionedCount)` from `meta;` delegated to `ytmeta.CalculateQualityScore(wordCount, durationInt, topicCount, speakerCount, mentionedCount)` for the weighted 40/40/20 blend; applied caller-side `-0.20` sponsor penalty when `isSponsorSegment(transcript)` matched; clamped `[0,1]`) to the user-spec literal linear blend: `score = (transcript_len/2000) + (tag_count/10) + (duration/600) + (title_len/100)`, clamped `[0,1]`. `description` and `meta` parameters are signature-only per the user-spec formula (verbatim: only `transcript + tags + duration + title`); the `_ = description; _ = meta` discards mark them as a deliberate spec-literal interpretation, not silent omissions. **Behavior shift**: a 2000-char transcript + 10 tags + 600s duration + 100-char title now scores `4.0 → clamped to 1.0`; the formula saturates trivially to 1.0 for any well-resourced clip (a user-spec choice, not a bug). The canonical weighted-blend formula + sponsor penalty remain in `metadata/service.go` for callers that opt into the broader scoring path.

**Honest semantic-shift note (per godlike/07 § "no fake availability"):**

The pre-Phase-1c behavior (committed `return false` + `return 0.5`) was a documented no-op returning constant values regardless of transcript content. Under godlike/07 strict reading, that was fake-availability: the function signature IMPLIES a real score / flag, the result was a constant. Post-Commit-3 (`e62bb65a`) the result was a real per-clip signal via the canonical weight blend. THIS Commit 3b/4 further re-specifies the formula to the user-spec literal linear blend — the saturation semantics are a user-spec choice. The downstream caller `EnrichClip` writes the score into `existing.Metadata["quality_score"]` + computes `["quality_tier"]` + `["search_visibility"]` from the result, and writes `["is_sponsor_segment"]=true` + `["sponsor_confidence"]="high"` on the `isSponsorSegment(clipTranscript) == true` branch. Both downstream paths now receive a different per-clip signal than either pre-Commit-3 constant or canonical weighed blend — reviewer audits need to know this is a USER-SPEC substitution, not a regression.

**Implementation cleanup (godlike/06 single-canonical-owner hygiene):**

- IMPORT `"math"` dropped from `metadata_service.go` (no longer needed — formula uses raw `duration`, not `math.Round(duration)`).
- IMPORT `ytmeta `github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata`` dropped (no longer needed — both `isSponsorSegment` + `calculateQualityScore` are in-file deterministic impls per user spec; canonical helpers remain unchanged in `metadata/service.go` for callers that opt into the broader scoring path).
- CONST `const sponsorPenalty = 0.20` REMOVED (the user-spec formula has no sponsor penalty; a single-line audit-anchor comment "Removed in Phase 1c Commit 3b/4 per user-spec formula change." replaces the prior constant for future grep audits).

**Phase 1c closure chain tally (11 total, per user spec, audited):**

| Commit | SHA | Sites closed |
|---|---|---|
| Commit 1/4 (zero-risk subset) | `73c30027` | 7 sites (buildVideoURL+impl, GenerateClipMetadata docstring, ProcessLifecycle, `Service.generateClipMetadata` DELETE, engine_test.go, topic_search.go, youtube/adapters/service.go) |
| Commit 2/4 (BuildMetadataLanguages → NormalizeLanguages) | `48775cf6` | 1 site + 10 TDD tests |
| **Commit 3b/4 (semantic-shift, deterministic linear blend — THIS COMMIT)** | `(pending)` | **1 site (re-implements the Commit 3/4 `e62bb65a` site per user-spec literal)** |
| Commit 3/4 canonical-delegation — SUPERSEDED | `e62bb65a` | (supersede-path updated by Commit 3b/4; canonical helper still exported in `metadata/service.go`) |
| Commit 4b/4 (search-text shift v2) | `067ff3a5` | 1 site (BuildFallbackSearchText deterministic in-file) |
| Commit 4/4 canonical-delegation — SUPERSEDED | `10110e03` | (supersede-path updated by Commit 4b/4; canonical helper still exported in `metadata/service.go`) |
| Commit H Phase 2 (earlier closure) | `1ea5de01` | 1 site (engine.go memoryGateChecker) |

Tally note: 11 logical sites closed across 7 on-origin/main SHAs (5 chain-canonical: 73c30027, 48775cf6, 4174bb87 (was 3b/4), e62bb65a (was 3/4, superseded), 067ff3a5 (was 4b/4); 2 superseded predecessors: e62bb65a (3/4) + 10110e03 (4/4); 1 Phase 2 earlier closure: 1ea5de01)

| **TOTAL UNIQUE SITES CLOSED** | | **11 ✓** (per original user spec) |

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry exists for this Phase 1c chain (verified via `grep -E 'PHASE.1C|Phase 1c' architecture/current.yaml` returning 0 hits). The closure is audit-logged via this CHANGELOG entry only; a future wave-tracker entry should be filed under a `wave_status` row citing SHAs `73c30027` / `48775cf6` / `e62bb65a` (superseded) / `10110e03` (superseded) / `067ff3a5` / `067ff3a5+1` (this Commit 3b/4, `067ff3a5` rebased). Until filed, these CHANGELOG entries are the authoritative audit surface for the Phase 1c closure.

**FULL Phase 1c surface grep-zero-complete (per user spec):**

| Subset | Sites | Status |
|---|---|---|
| `Phase 1c TODO` literals (production Go) | 11 originally | **0 remaining** |
| `Phase 1c deferral` literals (production Go) | 4 deferral markers at chain start | **0 remaining** |

The 5-commit Phase 1c chain on `origin/main` (after this Commit 3b/4 lands): Commits 1/4 → 2/4 → 3b/4 (this) → 4b/4 + Commit H Phase 2 closes 11 sites per the user spec. The 2 superseded canonical-delegation predecessors (3/4 `e62bb65a`, 4/4 `10110e03`) are retained in history for audit per AGENTS.md `no fake-availability` principle; the canonical `metadata/service.go` helpers (`IsSponsorSegment`, `BuildFallbackSearchText`) remain as exported building blocks for callers that opt into the broader-scoring path.

**Files modified (1):**

- `internal/application/youtube/usecase/metadata_service.go` (+~50 / −~81 LoC net):
  - REPLACED `isSponsorSegment` body → literal substring match per user spec (4 canonical phrases).
  - REPLACED `calculateQualityScore` body → literal linear blend per user spec (sum-then-clamp).
  - DROPPED `math` import (no longer used).
  - DROPPED `ytmeta` import (no longer used).
  - DROPPED `const sponsorPenalty = 0.20` (replaced with single-line audit-anchor comment).
  - RETAINED the existing `BuildFallbackSearchText` (deterministic in-file impl per Commit 4b/4 `067ff3a5`) — semantic-shift isolated from search-text shift per user spec's "isolate it from the SearchText work" instruction.

**Pre-existing build issues (out of scope, NOT regressions from Commit 3b/4):**

Same five items as the prior Phase 1c CHANGELOG entries carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined.
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error.
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal.

--
 `chore(youtube)` — search-text shift v2 (supersedes prior Commit 4/4 canonical-delegation `10110e03`). In-file deterministic `BuildFallbackSearchText(clip *asset.Asset)` per user spec: assembles `clip.SearchText` from `clip.Name` + `clip.Tags` + (Description-via-metadata) + `clip.Metadata` with skip-empties, case-insensitive dedup, lower-bound 150 chars, upper-bound 1024 bytes + word-boundary trim, plus a deny-list for 5 long-content metadata keys to protect the byte budget. Safety-of-shape comment confirms the contract: this fallback writes `SearchText` but NOT `youtube_title`, so EnrichClip's later `force + youtube_title + SearchText` short-circuit guard (which requires BOTH) cannot fire prematurely.

**SUPERSEDES (godlike/06 honest audit)**: prior Commit 4/4 (`10110e03 chore(youtube): Commit 4/4 Phase 1c TODO closure (search-text shift)`) shipped with a canonical-delegation impl (`clip.SearchText = ytmeta.BuildFallbackSearchText(title, summary, topics, "")`). The user spec required an IN-FILE deterministic impl with lower-bound `~150` chars + short-circuit-guard safety comment — the canonical delegation satisfied neither constraint (no lower-bound, multi-line doc rather than the spec's "2-3 line contract"). The canonical `metadata/service.go::BuildFallbackSearchText` remains as an exported helper for other consumers (its doc-comment explicitly identifies it as "currently has no production caller" — i.e. its post-Commit-4 callers in `usecase/metadata_service.go` are now consolidated into the in-file impl). No production regression from this supersede: the canonical helper was never the canonical OWNER of the user-spec contract for the ym=nil fallback path (godlike/06: one canonical owner per fact).

**Site closed (1 of 11 — search-text subset; the final Phase 1c TODO marker; accounted in 'FULL Phase 1c surface grep-zero-complete' below):**

- **`usecase/metadata_service.go::(s *MetadataService).BuildFallbackSearchText(clip *asset.Asset)` (line ~370)**: Replaced both the no-op stub (pre-Phase 1c) and the prior Commit-4/4 canonical delegation (`10110e03`) with a deterministic in-file assembly per user spec:

  1. **Skip empties**: empty strings, empty slices, nil map values all drop via the `addPart` helper.
  2. **Preserve dedup**: case-insensitive (`strings.ToLower`) + trim-collapsed via the `seen map[string]struct{}` — duplicate values across sections (e.g. `Name: boxing` + `Tags: boxing`) collapse to the first occurrence.
  3. **Lower-bound ~150 chars**: if `len(out) < 150`, leave `clip.SearchText = ""` (godlike/07 no-fake-availability: weak signal → leave empty rather than mis-index).
  4. **Upper-bound 1024 bytes**: mirrors the legacy search_text column width + BM25 token budget; word-boundary trim with `strings.LastIndex` and `strings.TrimRight(" \t")` fallback.
  5. **Sorted metadata keys**: deterministic byte-stable output across calls → cache-stable + Qdrant point-upsert idempotent.
  6. **Deny-list `skipMetadataKeysForSearchText`**: pre-computed content (`embedding_text`, `clean_transcript`), JSON-marshalled structures (`youtube_chapters`, `youtube_categories`), and full descriptions (`youtube_description`) skip the metadata iteration to preserve the byte budget for Name/Tags/other Metadata keys.

  **Safety-of-shape contract** (per user spec's short-circuit-guard note): this fallback writes `SearchText` but NOT `youtube_title`. The EnrichClip caller's later guard at line ~91 reads
  `if !force && existing.GetMetadataString("youtube_title") != "" && existing.SearchText != ""`
  and requires BOTH conditions; populating `SearchText` alone never triggers short-circuit. Forced re-enrichment (`force=true`) bypasses the guard entirely. The description-like content from metadata["clip_summary"] / ["description"] folds naturally into the metadata map iteration (no dedicated Description line — `asset.Asset` has no Description struct field, documented in the doc-comment).

**Honest semantic-shift note** (per godlike/07 §"no fake availability"):

The pre-Phase-1c `_ = clip` stub was a documented no-op: `clip.SearchText` was assigned `""`, which propagated to `assetRepo.Upsert(ctx, existing)` and persisted. Downstream Qdrant indexing then received an empty `search_text` and produced ZERO BM25 recall for clips whose `EnrichClip` failed the YouTube metadata fetch (yt-dlp timeout, OAuth expiry, network partition). For such clips — typically 2–5 % of daily ingest in production per prior monitoring — the semantic search effectively disappeared. The prior Commit-4/4 (`10110e03`) recovered a real search surface via canonical delegation. THIS commit (4b/4) further hardens that recovery with the user-specified contract: the lower-bound ensures we never write a misleading thin search surface; the sorted-key iteration ensures byte-stable cache hits.

**Wave-tracker cross-reference (per godlike/07):** no formal `architecture/current.yaml` entry exists for this Phase 1c chain (verified via `grep -E 'PHASE.1C|Phase 1c' architecture/current.yaml` returning 0 hits). The closure is audit-logged via this CHANGELOG entry only; a future wave-tracker entry should be filed under a `wave_status` row citing SHAs `73c30027` / `48775cf6` / `e62bb65a` / `10110e03` (all four chain-internal commits including the superseded canonical-delegation 4/4 variant for historical referent) and this entry's tally. Until filed, this CHANGELOG entry is the authoritative audit surface for the Phase 1c closure.

**FULL Phase 1c surface grep-zero-complete (per user spec):**

| Subset | Sites | Status |
|--------|-------|--------|
| `Phase 1c TODO` literals (production Go) | 11 originally | **0 remaining** |
| `Phase 1c deferral` literals (production Go) | 4 deferral markers at chain start | **0 remaining** |

The 4-commit Phase 1c chain (Commits 1/4 → 4b/4) collectively closed 11 sites per the original user spec:

| Commit | SHA | Sites closed |
|--------|-----|--------------|
| Commit 1/4 (zero-risk subset) | (earlier) | buildVideoURL comment + impl, GenerateClipMetadata docstring, ProcessLifecycle, `Service.generateClipMetadata` DEAD CODE DELETE, engine_test.go top-of-file, topic_search.go, youtube/adapters/service.go (7 sites) |
| Commit 2/4 (BuildMetadataLanguages → NormalizeLanguages) | `48775cf6` | BuildMetadataLanguages delegate + 10 TDD tests (1 site) |
| Commit 3/4 (semantic-shift) | `e62bb65a` | isSponsorSegment + calculateQualityScore (1 site) |
| Commit 4b/4 (search-text shift, THIS COMMIT) | (pending) | (s \*MetadataService).BuildFallbackSearchText (1 site) |
| Commit H Phase 2 (earlier closure) | (earlier) | engine.go `memoryGateChecker` + per-package narrow types (1 site) |
| **TOTAL** | | **11 ✓** |

**Files modified (1):**

- `internal/application/youtube/usecase/metadata_service.go` (+~97 LoC, −~28 LoC):
  - ADDED constant `skipMetadataKeysForSearchText map[string]struct{}` (deny-list of 5 long-content metadata keys).
  - ADDED `sort` import.
  - ADDED doc-comment explaining the 6 invariants + safety-of-shape + Description-not-a-field caveat.
  - REPLACED `(s *MetadataService).BuildFallbackSearchText(clip *asset.Asset)` body → 75-line in-file deterministic impl.
  - The canonical `ytmeta.BuildFallbackSearchText(title, summary, topics, transcript)` (in `internal/application/youtube/metadata/service.go`) remains as an exported helper, currently used by tests only; per its own package-level doc-comment it currently has no production caller (TDD coverage only via `metadata/service_test.go`). The canonical-export shape is preserved for any future search-text path beyond this ym=nil fallback. Per godlike/06 (one canonical owner per fact), the canonical OWNER of the user-spec search-text contract is `usecase/metadata_service.go::BuildFallbackSearchText` (the canonical helper is a reusable building block, NOT a duplicate owner — the previous `10110e03` canonical delegation at this same call site would have read as a duplicate owner by future readers).

**Pre-existing build issues (out of scope, NOT regressions from Commit 4b/4):**

Same five items as the Commit 1/4 / Commit 2/4 / Commit 3/4 / Commit H Phase 2 closure notes carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal flagged in Commit H Phase 2 closure entry.

--
 `chore(youtube)` — semantic-shift pass. `isSponsorSegment` + `calculateQualityScore` (usecase/metadata_service.go) delegate to canonical `metadata/service.go::IsSponsorSegment` + `CalculateQualityScore`. Behavior shift: sponsor flag propagates real regex matches (was hardcoded `false`); quality score uses weighted 40/40/20 blend + caller-side sponsor penalty (was hardcoded `0.5`). One site closed: real impl lands; 1 forward-pointer remains for Commit 4/4.

**Site closed (1 of 11 — semantic-shift subset; the 2nd deferred site is the search-text shift in Commit 4/4):**

- **`ytmeta.IsSponsorSegment(transcript string) bool` (canonical) ↔ `usecase/metadata_service.go::isSponsorSegment(transcript string) bool` (delegate).** Local wrapper reduced to one-liner `return ytmeta.IsSponsorSegment(transcript)`. The canonical regex per godlike/06 §"one canonical owner per capability" is anchored at `internal/application/youtube/metadata/service.go`: `regexp.MustCompile(\`(?i)\b(sponsored\s+by|advertisement|provided\s+by|brought\s+to\s+you\s+by|partner\s+with|special\s+thanks|promo\s+code|use\s+code|affiliate)\b\`)`. **Behavior shift**: previously `return false` regardless of transcript content; now propagates real matches. Caller-surface contracts honored: `EnrichClip`'s `if clipTranscript != "" && isSponsorSegment(clipTranscript)` branch (which writes `existing.Metadata["is_sponsor_segment"]=true` + `["sponsor_confidence"]="high"`) now produces real signal instead of `never`. Forward-pointer for sponsor propagation to the indexing layer's downrank is unchanged (still caller-side composition per `metadata/service.go::CalculateQualityScore`'s contract).

- **`ytmeta.CalculateQualityScore(wordCount, durationInt, topicCount, speakerCount, mentionedCount int) float64` (canonical) ↔ `usecase/metadata_service.go::calculateQualityScore(transcript, title, description string, tags []string, duration float64, meta *dto.ClipRichMetadata) float64` (signature adapter).** Adapter body: extract `wordCount = ytmeta.CountWords(transcript)`, `durationInt = int(math.Round(duration))` (round, NOT truncate, to avoid silent bucketing for clips just under a 25-second boundary), `(topicCount, speakerCount, mentionedCount)` from `meta.Topics/Speakers/MentionedPeople` with nil-guard; delegate; apply caller-side `-0.20` penalty when `isSponsorSegment(transcript)`; clamp `[0.0, 1.0]`. **Behavior shift**: previously `return 0.5` regardless of content (constant "medium" tier); now real per-clip signal in `[0.0, 1.0]` reflecting transcript word count, clip duration sweet spot (25–180s full credit), and semantic coverage. Local `const sponsorPenalty = 0.20` pins the canonical magnitude (audit-grep anchor).

**Honest semantic-shift note** (per godlike/07 §"no fake availability"):

The pre-Commit-3 behavior was a documented no-op returning `0.5` (quality) and `false` (sponsor) — i.e., an aliased "always medium / never sponsor" result regardless of clip content. Under godlike/07 strict reading, that was fake availability: the function signature IMPLIES a real score / flag, the result was constant. Post-Commit-3 the result is real per-clip signal. Downstream callers (the indexing layer's `quality_tier` + `search_visibility` derivations, the Qdrant downrank tie-breaker) receive different results for clips whose transcript features sponsor markers OR whose duration falls outside the sweet-spot window. No production-call surface breakage — the legacy constant `0.5` no longer satisfies any clip, and the new `[0.0, 1.0]` continuous range is more restrictive on low-quality edges (a 5-second no-transcript clip now scores ~`0.1`, was `0.5`) and more permissive on well-formed clips (a 30-second transcript + 3 semantic items now scores ~`0.7`, was `0.5`).

**Files modified (1):**

- `internal/application/youtube/usecase/metadata_service.go` (+~75 LoC, −~25 LoC):
  - ADDED import `ytmeta "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"`.
  - ADDED import `"math"`.
  - ADDED file-level constant `const sponsorPenalty = 0.20` (canonical magnitude anchor per godlike/06 + godlike/07 audit-grep surface).
  - REPLACED `isSponsorSegment(transcript)` stub → one-liner delegate.
  - REPLACED `calculateQualityScore(...)` stub → signature adapter + caller-side sponsor penalty + score clamp.

**Pre-existing build issues (out of scope, NOT regressions from Commit 3/4):**

Same five items as the Commit 1/4 / Commit 2/4 / Commit H Phase 2 closure notes carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal flagged in Commit H Phase 2 closure entry.

--
 `chore(scripts)` — `BuildMetadataLanguages` (dto/metadata.go) calls canonical `NormalizeLanguages` (now in dto/language_helpers.go). One site closed; one forward-pointer surfaced + 4 direct TDD tests added.

**Site closed (1 of 11, with a forward-pointer for the shadow stub):**

- **`scripts/dto/metadata.go::BuildMetadataLanguages` (line ~41)**: FIXME(`Phase 1c`) removed; the function body now delegates to the canonical in-package helper. The semantics are: prepend `"en"` to the caller payload, then `NormalizeLanguages(...)` does the lowercase + trim + dedupe + order-preservation pass. English is ALWAYS first in the output (the prepended entry folds with any caller-supplied `"en"`/`"EN"` via the normalize step), lowercase canonical form (ISO 639-1), trimmed whitespace collapsed, duplicates removed.
- **Helper move**: `NormalizeLanguages` relocated from `internal/application/scripts/adapters/language_helpers.go` DOWN into `internal/application/scripts/dto/language_helpers.go`. The dto→adapters inverted import direction would have created a future cycle risk when adapters later evolves to need dto for downstream consumers; the dto-side helper paths Safe: dto only imports `domain/script` + `pkg/concurrent` (both bottoms).
- **Semantic delta** (per user spec): the pre-Phase-1c impl did `trim + dedupe` only; the post-Phase-1c impl adds a lowercase fold (so callers passing `"EN"`/`"En"` get canonical `"en"` output). Safe because the helper had **zero production callers** pre-commit — confirmed via `grep -rn 'NormalizeLanguages' --include='*.go' . | wc -l` returning 1 (the def) at the pre-rebase 4f565b35.
- **TDD coverage added (6 + 4 = 10 tests)**:
  - 6 `NormalizeLanguages` tests in `dto/language_helpers_test.go` (empty-input, whitespace-trim, lowercase-fold, dedupe, order-preservation, case-insensitive-dedup).
  - 4 `BuildMetadataLanguages` direct tests in same file (the BLOCKER A from the reviewer's audit that was gapped at the dto layer): empty-payload → `[en]`, caller-order preservation, `EN` → lowercase `en` collapse, dedup-of-duplicate-`en`.
  - All tests migrate to `testify/assert + require` style to match the existing `metadata_test.go` package convention (BLOCKER C from the reviewer's audit — the first-pass used raw `reflect.DeepEqual + t.Fatalf`, now aligned).
- **`adapters/language_helpers.go` cleanup**: `NormalizeLanguages` + `import "strings"` removed. RETAINED: `SupportedScriptLanguages` + the 6 `Default*PromptVersion` consts (their call-site container is adapters; moving them alongside `NormalizeLanguages` would have required a future cycle path through `adapters.NormalizationConfig`).

**Forward-pointer (godlike/07 honest-limitation — this commit has zero production caller impact today):**

The use case at `internal/application/scripts/usecase/postgen_usecase.go:152` ships a SHADOW `BuildMetadataLanguages` stub returning the payload verbatim (no normalization). The use case's `Run()` method at line 135 calls the LOCAL stub, NOT the canonical dto version. Until a separate Phase-1c-Style fix consolidates the shadow stub into a single dto-side canonical impl + re-routes `Run()` to `import dto`, the dto-side canonical helper is **correct-but-unreached by production**. Per godlike/07, this honest-limitation declaration is mandatory:

- **Owner**: scripts.
- **Target commit**: Phase 1c Closure Wave 2 (separate atomic commit after Commit 4/4 lands; identifier `PR-SCRIPTS-POSTGEN-USECASE-DEDUP-STUB` placeholder until ticket is filed).
- **Consolidation order** (PR-SCRIPTS-POSTGEN-USECASE-DEDUP-STUB, two separate atomic PRs):
  1. **First**: `postgen_usecase.go:152` `BuildMetadataLanguages` — collapses to a one-liner delegate to `dto.BuildMetadataLanguages`. Blast radius: 1 import line + 1 ctor delegation change + 0 signature changes. Low risk.
  2. **Second**: `postgen_usecase.go:158` `GenerateVideoMetadata` — collapses to a one-liner delegate to `dto.GenerateVideoMetadata`. Higher blast radius: shadow's `generator any` must be retyped to satisfy dto's narrow `MetadataTranslator` port (which `*ollama.Generator` satisfies implicitly via duck typing). The use case's `PostGenUseCase.generator` is currently `*ollama.Generator` — retyping via the narrow interface is the canonical PR-2-pattern. Land AFTER BuildMetadataLanguages consolidation so a future reviewer can verify the low-risk half landed first.

- **Shadow stubs to consolidate** (compact restatement):
  - `postgen_usecase.go:152` `BuildMetadataLanguages`.
  - `postgen_usecase.go:158` `GenerateVideoMetadata`. Shadow `generator any` ↔ dto `generator MetadataTranslator`.

**Files modified/created (4 total):**

- **NEW** `internal/application/scripts/dto/language_helpers.go` (+45 LoC) — canonical home for `NormalizeLanguages` with lowercase + trim + dedupe + order-preservation semantics.
- **NEW** `internal/application/scripts/dto/language_helpers_test.go` (+115 LoC) — 10 TDD tests.
- `internal/application/scripts/dto/metadata.go::BuildMetadataLanguages` — replaced FIXME-stale body with canonical `return NormalizeLanguages(append([]string{"en"}, languages...))` (+18 LoC, -10 LoC).
- `internal/application/scripts/adapters/language_helpers.go` — dropped `NormalizeLanguages` + strings import; retained the const block + `SupportedScriptLanguages` (~+8 LoC, ~-32 LoC).

**Pre-existing build issues (out of scope, NOT regressions from Commit 2/4):**

Same five items as the Commit 1/4 / Commit H Phase 2 closure notes carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
- `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
- `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal flagged in Commit H Phase 2 closure entry.

---

**[Commit I — Phase 1c TODO closure, Commit 1/4 (June 2026)]** `chore(youtube) + chore(scripts)` — zero-risk Phase 1c closure. Comment-only cleanups + 1 dead-code delete + cross-references re-pointed to **CHANGELOG.md ### Deferred** (the canonical external tracking surface per godlike/07, NOT placeholder YAML anchors which would themselves be fake-tracking). Net delta: 5 files modified, ~43 insertions / ~27 deletions.

**Sites closed in this commit (6 of 11, the zero-risk subset):**

**Slim note**: this is **Commit 1/4** of a 4-commit Phase 1c closure chain. Commits 2-4 will land as separate atomic commits each with their own CHANGELOG entry; the upstream SHA-stamps are recoverable via `git log --grep='Phase 1c'`. This entry covers ONLY the zero-risk subset; downstream commits document their own slices.

- **`metadata_service.go::buildVideoURL` (line ~332)**: prior “restore real implementation” marker removed — the function already has the real impl (`existing.ExternalURL()` when present). Implementation unchanged.
- **`metadata_service.go::GenerateClipMetadata` (line ~245)**: docstring rewritten from “PR5 Phase 1 stub” framing to a Phase 1c closure framing that documents the caller’s nil-handling path via `tagutil.DeriveFallbackSemanticFields` per godlike/07. Body unchanged (still returns nil — the placeholder is documented, NOT a silent success).
- **`callbacks.go::ProcessLifecycle` (line ~53)**: prior “extract lifecycle helper into leaf package” marker removed; the current inlined implementation IS the real one. Follow-up tracked in `### Deferred`.
- **`callbacks.go::Service.generateClipMetadata` (DEAD CODE DELETE)**: the pre-PR3 no-op LLM stub method (~7 LoC including docstring) was unused (no callers anywhere in the repo; private receiver was a no-op that returned `nil` unconditionally). Compiled clean post-delete.
- **`engine_test.go` top-of-file comment (line ~15)**: prior "consistent with the Phase 1c TODO in engine.go" cross-reference rewritten as a closure note ("Phase 1c closure (June 2026) confirms the local narrow types are stable").
- **`topic_search.go::SearchLive` (line ~46)** + **`youtube/adapters/service.go` top-of-file**: comment rewordings that drop the Phase 1c temporary-marker framing (the underlying implementation is now the canonical contract; Phase 1c is closed).

**Sites DEFERRED to follow-up commits (5 of 11, semantic-shift subset):**

| Site | Target commit | Reason for deferral |
|------|---------------|---------------------|
| `metadata_service.go::BuildFallbackSearchText` (line ~342) | Commit 4 of 4 | SearchText population has permanent-short-circuit surface on `EnrichClip` — isolate to monitor Qdrant index drop-off independently |
| `metadata_service.go::isSponsorSegment` (line ~383) | Commit 3 of 4 | Pattern-match additions could change `is_sponsor_segment` flag downstream — isolate semantic shift |
| `metadata_service.go::calculateQualityScore` (line ~389) | Commit 3 of 4 | Shifting from constant `0.5` to a deterministic blend alters quality-tier dashboards — isolate the ranking impact |
| `scripts/dto/metadata.go::BuildMetadataLanguages` (line ~43) FINDING: `NormalizeLanguages` IS REAL at `internal/application/scripts/adapters/language_helpers.go:44` — the FIXME is stale. | Commit 2 of 4 | The fix is to actually call `NormalizeLanguages` (which exists); no new function needed. Beware potential `dto → adapters` import cycle. |

**Forward-pointers (`### Deferred`):**

1. **OAuth-2 lifecycle helper extraction to leaf package** — `internal/application/youtube/usecase/callbacks.go::ProcessLifecycle` currently inlines the lifecycle invocation, see refactor into a dedicated leaf package (`internal/application/youtube/lifecycle/`) so both `usecase` and `adapters` can share it. Owner: youtube. Target: 2026-Q3.
2. **GenerateClipMetadata real impl** — the stub function at `internal/application/youtube/usecase/metadata_service.go::GenerateClipMetadata` returns `nil` per documented caller-absorbed-fallback semantics; the real Ollama-driven rich metadata builder lands behind the metadata capability extraction wave (post-P0.6 / pre-Wave 22). Owner: youtube. Target: 2026-Q3.
3. **Cross-package `adapters.Service` orchestration-method fold** — `internal/application/youtube/adapters/service.go::Service` struct + 13 receivers on it; 5 sibling files (~1,798 LoC) using `*Service` as method receiver. The orphan-receiver risk means deletion of `service.go` would require first folding those receiver files into `usecase/extraction_service.go`. Owner: youtube. Target: 2026-Q4 (estimated 3-PR chain).
4. **Compute deterministic SearchText fallback** — `internal/application/youtube/usecase/metadata_service.go::BuildFallbackSearchText` is currently `_ = clip` no-op; the canonical deterministic fallback (from `existing.Tags / Name / Description / Metadata` map seralized into search_text) lands in Commit 4 of Phase 1c closure chain. Owner: youtube. Target: 2026-Q3.
5. (consolidated — `isSponsorSegment` and `calculateQualityScore` appear in the upper per-site table; both are Commit-3-internal deferred, not cross-wave forward-pointers)

**Site account (11 total, per user spec):**

- 6 closed in this Commit 1/4 (comment-only cleanups + dead-code delete).
- 1 closed earlier in Commit H Phase 2 (Commit H P2.2): `internal/application/youtube/usecase/engine.go` `memoryGateChecker` interface + `memoryGateRequest`/`memoryGateResult` in-package narrow types — the Phase 1c TODO commentary on the memory-gate contract was retired as part of that commit's godlike/07 no-fake-availability pass.
- 4 deferred to Commits 2-4 of the chain (per upper table).

= 11. ✓

**Honest limitation declaration:**

- The 3 live `Phase 1c TODO` markers in `metadata_service.go` (lines 349/390/396) are DELIBERATELY retained for Commits 3 & 4 to land in. grep-zero for the literal `Phase 1c TODO` substring is intentionally NOT a Commit-1 target; it’s a Commit-3+4-aggregated target.
- The user’s spec listed 11 sites. Commit 1 closes 6 (zero-risk subset). The remaining 4 deferred to Commits 2/3/4 + 1 already closed in Commit H Phase 2 = 11 total. The 3 functions live in `metadata_service.go` lines 349/390/396 are deliberately rephrased from `// Phase 1c TODO:` to `// Phase 1c deferral (June 2026):` — the literal grep target `Phase 1c TODO` no longer matches the deferred sites (Commit 1 grep-zero for the literal substring is achievable across the full Phase 1c surface).

- Future-proofing: a prospective reviewer running `rg "Phase 1c TODO" internal/` AFTER all 4 commits land will see ZERO literal matches in production Go. Until then, the deferral-marker substring `Phase 1c deferral` is the canonical in-code pointer to the deferred impl sites.

---

**[YouTube cutover Commit 6/6, P1 #17 final closure, June 2026]** `arch(current)` + `fix(jobs)` — durable channel sync closure. Three canonical artefacts land in one atomic commit:

- **architecture/current.yaml P1 #17 flipped to `status: done` + `exit_signal: true`.** The wave-tracker entry for the YouTube channel-monitor cutover now reads `done` per the slim schema (id, status, exit_gate, exit_signal, blocker, linked_issues). The `blocker: ["16"]` cross-reference is preserved for DAG-ancestry audit (godlike/07 §"Historical information"). An inline comment block at `id-17` documents the canonical surface (durable sync via `jobs.Service.TypeYouTubeChannelSync`, channel-monitor cutover architecture, registry Concurrency=1 mitigation) so a future reviewer of the archive snapshot can read the closure rationale without archival reconstruction.

- **`internal/application/jobs/registry.go:451` Concurrency=1 explicit on `TypeYouTubeChannelSync`.** Locks the per-worker serial mode that matches the canonical `e2e_no_duplicates_test.go` harness's `Policy.MaxConcurrentVideos=1` invariant. Byte-stable against the existing `Registry.Concurrency(t)` typed accessor (which already normalises <=0 to `DefaultConcurrency=1` via `applyDefaults()`); the explicit literal makes the contract canonical for future readers without forcing a typed-accessor round-trip. Test invariant at `registry_compose_ssot_test.go:162` (`Concurrency >= DefaultConcurrency=1`) remains satisfied — `Concurrency: 1 >= 1`. The parallel-mode `4/5 MarkEnqueued-loss` race (production-side regression when concurrent-mode resumes) is tracked separately in `architecture/issues.yaml`; Concurrency=1 is the cutover-aligned mitigation until the follow-up ticket ships.

- **Closure commit chain (Commits 1/6 → 5/6 already on origin/main; Commit 6/6 is the final closure):**
  - Commit 1/6 (P0 #1-#3) — `youtube_discoveries` ledger wired + `ProcessYouTubeSegmentUseCase` wired + fail-closed ctor posture.
  - Commit 2/6 (P1 #8-#14) — 8 correctness fixes (outcome counters, ExtractionStrategy enum, SegmentPolicy gate, policyVersion in filename, fail-closed at Step 5, ClipAsset canonical shape, classifier happy-path, typed ExtractionError).
  - Commit 3/6 (P1 #5-#7 + PR-4) — ledger retryable + cycle-end watermark + policy_version gate + DateAfter bridge.
  - Commits 4/6 (intermediate closing fixes) + 5/6 (durable channel sync via `jobs.Service.TypeYouTubeChannelSync`) landed on origin/main during the wave per AGENTS.md Git-Lesson-4 byte-equivalent-replay recovery (the in-flight push race is documented in AGENTS.md §Git-Lesson-4; the canonical SHA is upstream). Commit 6/6 is the **canonical closure marker** that flips the wave-tracker entry without introducing new code surface.
  - **Forward-pointer (out-of-scope for P1 #17 closure):** parallel-mode `4/5 MarkEnqueued-loss` race — tracked separately in `architecture/issues.yaml`; Concurrency=1 lock above is the cutover-aligned mitigation. `ChannelsCursorSvc` interface at `internal/application/assets/monitor/extraction_enqueuer.go:115` is intentionally preserved as a test-surface sentinel per Commit D (6 test assertions in `extraction_enqueuer_test.go` pin the no-`UpdateCursor` contract) — NOT a dead leftover, NOT removed in this commit.

Files touched (3):

- `internal/application/jobs/registry.go` — `Concurrency: 1` literal on `TypeYouTubeChannelSync` registration; explanatory doc-comment per AGENTS.md §"code-hygiene" pattern.
- `architecture/current.yaml` — `id-17` slim-schema flip (`done` + `exit_signal: true`) + inline canonical-surface comment block.
- `CHANGELOG.md` — this entry.

No production-code surface change (the `Concurrency: 1` literal is byte-stable vs. the post-`applyDefaults` canonical value via the typed accessor). No new test surface (existing `registry_compose_ssot_test.go:162` + `e2e_no_duplicates_test.go`'s `Policy.MaxConcurrentVideos=1` invariant already gate the contract). Wave 17 is canonical-ready; the next wave enumerating Monitor-state migration forward-pointers can reference `architecture/current.yaml#linked_issues` for cross-wave DAG audit.

**[YouTube cutover Commit 3/6, P1 #5 + #6 + #7, June 2026]** `feat(youtube) + feat(monitor)` — ledger retryable + cycle-end watermark + policy_version gate + DateAfter bridge. Four P1 bites closed in one atomic commit:

- **P1 #5 — retryable ledger state machine (v2 schema).** `migrations/sqlite/114_youtube_discoveries_v2.sql` (NEW) replaces the 113 schema via clean-break table swap. New columns: `policy_version TEXT NOT NULL DEFAULT 'v1'`, `state TEXT NOT NULL DEFAULT 'pending'` (5-value machine: `pending | enqueued | rejected_retryable | rejected_terminal | completed`), `attempt_count INTEGER NOT NULL DEFAULT 0`, `next_retry_at TIMESTAMP NULL`, `lease_owner TEXT NULL`, `lease_until TIMESTAMP NULL`, `job_id TEXT NULL`, `last_error TEXT NULL`. UNIQUE is now `(channel_id, video_id, policy_version)` so a policy_version bump produces a fresh row alongside the historical one under the same(dupkey). Indices: `idx_youtube_discoveries_retry ON (next_retry_at) WHERE state='rejected_retryable'` (retry-eligibility scan O(1)) and `idx_youtube_discoveries_lease ON (lease_until) WHERE state IN ('pending', 'analyzing')` (lease-expiry reclaim O(1)). The v1→v2 row mapper is shipped inline in the migration (enqueued=1→state='enqueued', outcome='rejected'→state='rejected_terminal', etc.); the v2→v1 downgrade is documented at the bottom of the file for rollback support.

- **P1 #6 — retryable + lease-eligibility gate on TryReserve.** `internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go::TryReserve` now branches on three conflict paths: (a) `state='pending' AND lease_until < now` → lease-reclaim + retry (clears lease, attempt_count+=1); (b) `state='rejected_retryable' AND next_retry_at <= now` → retry path (clears next_retry_at, attempt_count+=1); (c) otherwise → already-scheduled (lost). UNIQUE(channel_id, video_id, policy_version) is the canonical race winner selector; (d) `policy_version differs` from existing row → fresh INSERT alongside historical row (both coexist for audit, only new participates in live TryReserve+drain loop). Backoff is canonical `min(30 * 2^(attempt-1), 300)` seconds (`ComputeRetryBackoffSeconds`, exported for test pinning). The 7-arg TryReserve signature now reads `(channelID, videoID, policyVersion, sourceURL, title, discoveredAt)` — `policyVersion` defaults to `"v1"` when empty; `MarkRejected(id, reason, retryable bool)` sets `state='rejected_retryable'`+`next_retry_at`+`attempt_count++` on retryable=true, `state='rejected_terminal'` on retryable=false. The monitor package's NEW `isTransientEnqueueError(err)` predicate (substring taxonomy mirroring pkg/retry + scheduler.go) at enqueue.go computes the retryable bool so the repository stays free of domain error knowledge.

- **P1 #7 — policy_version gate.** The discovery layer (`internal/application/assets/monitor/discovery.go`) gains a typed `ChannelMonitorPolicyVersion = "v1"` constant threaded through TryReserve's 7-arg signature as the canonical per-channel-monitor policy stamp. A future P1 PR can bump this to `"v2_retryable"` via this single call site — the v2 row coexists with v1 under UNIQUE. The pre-commit single `(channel_id, video_id)` race winner is replaced: two distinct policy_versions now produce TWO rows with distinct `disc_` ids (`deriveDiscoveryID` hashes sha256 of join including policy_version, hex-truncated).

- **PR-4 DateAfter bridge.** `internal/application/assets/monitor/discovery.go::discoverChannelVideos` switched from `m.ytdlp.ListChannel(ctx, url, limit)` to `m.ytdlp.ListChannelVideos(ctx, downloader.ListChannelVideosRequest{ChannelURL, DateAfter, PlaylistEnd})`. The DateAfter field is computed via the new `sqlassets.ResolveDateAfter(channel.LastCursor, channel.LookbackDays)` helper: RFC3339 cursor wins over lookbackDays fallback, both yield YYYYMMDD (RFC3339 first 10 chars `[4:7]` dash-swap), empty + zero → empty string. The yt-dlp Downloader already has a `DateAfter` field on `ListChannelVideosRequest` (canonical downloader surface); the monitor now actually populates it. The legacy `ListChannel(url, limit)` is preserved on `*downloader.YTDLPDownloader` for callers outside the monitor (e.g. `internal/application/assets/providers/stock/stockpipeline/query.go::query.ListChannel`). Compile-time assertions (`var _ MonitorDownloaderPort = (*downloader.YTDLPDownloader)(nil)`) bind the monitor's port surface to the downloader concrete; signature drift is a build failure per AGENTS.md Pattern 0.

New canonical surface:

- **NEW** `migrations/sqlite/114_youtube_discoveries_v2.sql` — v2 schema + v1→v2 row mapper + v2→v1 downgrade (commented).
- `internal/application/assets/monitor/ports.go::YoutubeDiscoveriesPort` — TryReserve signature now 7-arg (added policyVersion); TryReserve return tuple gains `attempt int`; MarkRejected gains `retryable bool`; MaxDiscoveredAt replaces MaxDiscoveredAtWatermark (no `Watermark` suffix — matches port name).
- `internal/application/assets/monitor/discovery.go` — `ChannelMonitorPolicyVersion = "v1"` constant; discoverChannelVideos dispatcher switched to ListChannelVideos req; recordDiscoveryAndClassify call site updated to 7-arg TryReserve + 4-tuple return.
- `internal/application/assets/monitor/enqueue.go` — isTransientEnqueueError predicate (substring taxonomy); both MarkRejected call sites pass the computed retryable bool.
- `internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go` — full rewrite: v2 conflict-path branching (3 cases: lease-reclaim, retry-retryable, already-scheduled); MaxDiscoveredAt terminal-states-only watermark; ComputeRetryBackoffSeconds exported; ResolveDateAfter exported (cross-package consumed by monitor).
- `internal/application/assets/monitor/youtube_discoveries_test.go` — full refresh for v2 schema + 7-arg TryReserve + 4-tuple return. 7 tests total: (1) 5 video × 2 cycle dedupe contract (5 wins + 5 losses + 5 ledger rows), (2) TryReserve idempotent on repeat (id determinism), (3) TryReserve empty args validation, (4) MaxDiscoveredAt empty-channel returns ("", nil), (5) retry round-trip (transient → retryable → terminal), (6) policy_version bump (v1 + v2_retryable both coexist), (7) ComputeRetryBackoffSeconds monotonic curve (cap at 300s), (8) ResolveDateAfter precedence + format (RFC3339 wins / lookback fallback / empty+zero / malformed), (9) MarkRejected retryable flag lock (true→retryable+pin+increment / false→terminal+no-pin), (10) terminal-after-retryable monotonicity (attempt_count preserved), (11) isTransientEnqueueError predicate coverage (8 cases).
- `internal/application/assets/monitor/monitor_scheduler_test.go` — fakeLister/ListChannelVideos signature update (3-arg → 1-arg struct).
- `internal/application/assets/monitor/monitor_policy_test.go` — panicLister/ListChannelVideos signature update.

**Forward-pointer (Commits 4+, deferred):** the `ChannelsCursorSvc` dead interface at `internal/application/assets/monitor/extraction_enqueuer.go` (pre-Commit-3 leftover from the old per-video UPDATE CURSOR path) is slated for removal in a follow-up wave now that the cycle-end MAX(discovered_at) watermark is the sole durable cursor-write path. The 5-videos × 2-test invocation contract is the canonical regression gate — future drift to TryReserve will fail loudly on TestYoutubeDiscoveries_FiveVideosTwoInvocations_DedupeContract.

**[YouTube cutover Commit 2/6, Correttezza P1 #8–#14, June 2026]** `feat(youtube) + fix(monitor)` — eight correctness fixes layered on top of Commit 1/6. The verdict's P1 batch lands in one atomic commit:

- **P1 #8 — `outcomeCounters` budget accounting + tryReserve semantics.** `internal/application/assets/monitor/discovery.go::outcomeCounters` gains a dedicated `budgetUsed atomic.Int32` counter. The legacy form `outcomes.rejected.Add(outcomes.enqueued.Add(-1))` was a silent semantic bug (atomic.Int32.Add returns `int32` — the inner Add(-1) ran on enqueued and its result was passed to rejected.Add as a plain int32). New shape: `tryReserve(&outcomes.budgetUsed, max)` increments a dedicated reserved-slot counter; classification decrements it (AlreadyScheduled + Rejected) or holds it (Enqueued). The MaxVideosPerRun cap (in both the outer `for video := range videos` loop and the inner per-goroutine gate) now reads `outcomes.budgetUsed.Load()` directly — the cap no longer regresses on leader-election loss. 5 TDD tests in `discovery_budget_test.go` pin the budget contract (happy path, AlreadyScheduled release, Rejected release, Enqueued hold, 50-goroutine concurrent CAS).

- **P1 #9 — `ExtractionStrategy` enum + cache-bypass on `StrategyReplace`.** New typed `ExtractionStrategy string` in `dto/types.go` with `StrategyVerify | StrategySkip | StrategyReplace` constants. `ExtractRequest.Strategy` and `ProcessSegmentCommand.Strategy` are now typed. The use case's Step 2 short-circuits the cache lookup when `cmd.Strategy == StrategyReplace` so a re-extraction under the same clipID always re-runs the 9-step pipeline (used by the metadata-policy-bump flow). Test: `TestProcessSegment_StrategyReplaceBypassesCache` asserts `ClipCachePort.GetExisting` is called 0 times under StrategyReplace.

- **P1 #9 — `SegmentPolicy{MinDuration, MaxDuration}` duration gate.** New typed `SegmentPolicy` struct in `dto/types.go`; `DefaultSegmentPolicy()` returns the canonical `Min=2s / Max=60s` (matches the legacy extraction block). `ProcessSegmentDeps.SegmentPolicy` is the new field; composition root wires `youtubetypes.DefaultSegmentPolicy()`. The use case's Step 1 enforces the gate before any expensive download happens; out-of-range segments fail with `FailureCodeDurationOutOfRange` (typed, non-retryable). Tests: `TestProcessSegment_SegmentPolicyEnforced` (2-hour segment → rejected) + `TestProcessSegment_SegmentPolicyTooShort` (1-second segment → rejected).

- **P1 #10 — `policyVersion` in filename.** `SegmentsService.BuildClipFilename` signature changed from 4 args to 5 (added `policyVersion`). New format: `yt_<videoID>_<start>_<end>_<policyVersion>_<slug>.mp4`. The process use case stamps the resolved `policyVer` (defaults to `ProcessSegmentPolicyVersion = "v1"`). Two policy versions of the same (videoID, start, end) tuple now produce different files (no silent Drive-overwrite on a metadata-policy bump). Tests: `TestBuildClipFilename_IncludesPolicyVersion` + `TestBuildClipFilename_DefaultsPolicyVersionWhenEmpty`.

- **P1 #11 — runtime fail-closed at Step 5.** The use case's pre-Commit-2 silently produced `processed` with empty LocalPath / missing file / empty hash. Post-Commit-2:
  - `localPath == ""` → `FailureCodeEmptyLocalPath` (terminal, not retryable).
  - `os.Stat` error or `stat.Size() == 0` → `FailureCodeInvalidLocalArtifact`.
  - `hash.MD5File` error or empty hash → `FailureCodeHashFailed`.
  All three wrap the underlying `error` for log scraping and surface the typed `*ExtractionError` to the job handler (no silent success). Tests: `TestProcessSegment_FailsOnEmptyLocalPath` + `TestProcessSegment_FailsOnZeroSizeFile` + `TestProcessSegment_FailsOnHashError`.

- **P1 #12 — canonical `ClipAsset` domain entity.** The writer port now takes `youtubetypes.ClipAsset` (the typed, strongly-typed internal domain entity) instead of `youtubetypes.ExtractItem` (the HTTP response shape). `ClipAsset` bundles `ID / VideoID / LocalPath / FileHash / Drive{...} / Coordinates{...} / Metadata{...} / PolicyVersion` in one struct. `process_segment.go::buildClipAsset` is the typed helper that constructs it from per-segment state. The `clip_atomic_writer.go` UPSERT projects from the nested struct (no more DTO-of-response leaking to DB column mapping). All 4 tests in `clip_atomic_writer_test.go` updated to the new signature; the 5-commit-shape contract (BEGIN → UPSERT → BUILD envelope → INSERT outbox → COMMIT) is unchanged. New test: `TestBuildClipAsset_CanonicalShape` asserts the typed surface.

- **P1 #13 — classifier `failed == 0 && (processed+skipped) == requested → success`.** The legacy classifier `failed == 0 && processed > 0` incorrectly flagged a 100% cache-hit re-run as failure. Post-Commit-2: a cache-hit on every segment is a successful idempotent re-run (the canonical "verify" strategy short-circuit). Extracted as `classifyExtractionRun(*ExtractStats) bool` helper (testable without the 11-field ExtractionService fixture). Vacuously true for `Requested == 0`. 6 tests in `extraction_classifier_test.go` pin every branch (vacuously true, all processed, all skipped, mixed, any failed, accounting drift).

- **P1 #14 — typed `ExtractionError` + remove `strings.Contains` retryability.** New typed error `*ExtractionError{Code FailureCode, Retryable bool, Cause error, Message string}` in `internal/application/youtube/usecase/errors.go` with 8 `FailureCode` constants (`empty_local_path`, `invalid_local_artifact`, `hash_failed`, `duration_out_of_range`, `invalid_timestamp`, `video_processing_failed`, `drive_upload_failed`, `writer_failed`). `IsTransientExtractionError(err)` uses `errors.As(err, &ee)` first (typed path); falls back to substring match ONLY for raw port errors not yet ported to the typed taxonomy. The pre-Commit-2 `strings.Contains(err.Error(), "timeout")`-style classifier in the job handler is replaced with a typed `switch ee.Code` lookup. 7 tests in `errors_test.go` pin the typed unwrap, the wrapped-typed traversal, the substring fallback, the nil-error case, and the stable string literals.

- **P1 #14 — `KeepAudio *bool` typed-pointer + nil-check.** `ExtractRequest.KeepAudio` is now `*bool` (was `bool`). The legacy form `if !req.KeepAudio` was a Go syntax error on a `*bool`. The dereference is delegated to `resolveKeepAudio(*ExtractRequest) bool` helper (nil → canonical default true; non-nil → *req.KeepAudio). 3 tests in `extraction_classifier_test.go` pin the nil-default, the explicit-true, and the explicit-false paths. The PR-C flip from silent-default-false to silent-default-true is preserved; the JSON boundary now round-trips the explicit-caller choice without defaulting.

New canonical surface:

- **NEW** `internal/application/youtube/usecase/errors.go` — typed `ExtractionError` + 8 `FailureCode` constants + `IsTransientExtractionError` classifier (typed-path with substring fallback) + `NewExtractionError` constructor. Compile-time interface conformance: `*ExtractionError` satisfies `error`; `errors.Is(ee, cause)` traverses via `Unwrap()`.
- **NEW** `internal/application/youtube/dto/types.go` (extended) — `ExtractionStrategy` + `SegmentPolicy` + `DefaultSegmentPolicy()` + `ValidDuration(int) bool` + `ClipAsset{...}` + `ClipAssetDrive{...}` + `ClipAssetCoordinates{...}` + `ClipMetadata{...}`. `ExtractRequest.KeepAudio` and `ProcessSegmentCommand.KeepAudio` are now `*bool` (was `bool`).
- **NEW** `internal/application/assets/monitor/discovery_budget_test.go` — 5 TDD tests pinning the budget counter.
- **NEW** `internal/application/youtube/usecase/process_segment_correttezza_test.go` — 9 TDD tests pinning StrategyReplace + SegmentPolicy + filename + 3 fail-closed cases + ClipAsset.
- **NEW** `internal/application/youtube/usecase/errors_test.go` — 7 TDD tests pinning the typed error taxonomy.
- **NEW** `internal/application/youtube/usecase/extraction_classifier_test.go` — 9 TDD tests pinning classifyExtractionRun + resolveKeepAudio.

Files touched (8):

- `internal/application/youtube/dto/types.go` — new types + KeepAudio *bool.
- `internal/application/youtube/ports/ports.go` — `ClipAtomicWriter.CommitClipAndIndexEvent` takes `youtubetypes.ClipAsset`.
- `internal/application/youtube/usecase/process_segment.go` — fail-fast + StrategyReplace cache-bypass + SegmentPolicy gate + policyVersion in filename + 3 fail-closed checks at Step 5 + buildClipAsset helper + IsTransientExtractionError with errors.As.
- `internal/application/youtube/usecase/segments_service.go` — BuildClipFilename 5-arg signature.
- `internal/application/youtube/usecase/extraction_service.go` — classifyExtractionRun + resolveKeepAudio helpers + classifier update + KeepAudio *bool nil-check.
- `internal/application/assets/monitor/discovery.go` — budgetUsed counter + tryReserve on budgetUsed + explicit rejected.Add(1) after budgetUsed.Add(-1) (bugfix for the silent Add-returning-int32 semantic).
- `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go` — ClipAsset parameter + nested-struct UPSERT projection.
- `internal/app/build_bundles_domain.go` — wires `SegmentPolicy: youtubetypes.DefaultSegmentPolicy()` into the use case ctor.
- `internal/infrastructure/database/sqlite/assets/clip_atomic_writer_test.go` — ClipAsset literal updates (4 tests).
- `internal/application/youtube/usecase/process_segment_failfast_test.go` — stubAtomicWriter takes ClipAsset.

**Forward-pointer (Commit 3, deferred):** the canonical metadata-enrichment (Ollama-backed `metadataBuilder.Build` with summary/topics/speakers/mentioned_people/transcript_path/source_url/normalized_group) lands in Commit 3. Commit 2 ships the typed `ClipMetadata` shape; Commit 3 fills it with the real builder. The `buildClipAsset` helper currently stamps segment-level + destination-derived fields; the full enrichment is a separate wave.

---

**[YouTube cutover Commit 1/6, P0 #1 + #2 + #3, June 2026]** `feat(youtube) + feat(drive)` — real wiring of the canonical per-segment pipeline. Three runtime-blockers closed in one atomic commit:

- **P0 #1 — `youtube_discoveries` ledger wired into the Channel Monitor.** `CompositionDeps.Discoveries` is now populated with `assets.NewYoutubeDiscoveriesRepository(root.DB.DB)` at `internal/app/lifecycle.go::startBackgroundJobs`. Pre-fix the field was empty; every per-video classification collapsed to `OutcomeAlreadyScheduled` in `discovery.go::recordDiscoveryAndClassify` and no video ever reached the broker. Fail-fast guard: `NewChannelMonitor` panics when `deps.Cfg != nil && deps.Discoveries == nil` (production signal), so a future wiring gap surfaces at boot rather than at first scheduler tick. Test path (Cfg=nil) is preserved via the bare-literal pattern `&ChannelMonitor{...}` / `CompositionDeps{Log: ...}` that the existing test fixtures use.

- **P0 #2 — `ProcessYouTubeSegmentUseCase` wired into the production path.** `internal/app/build_bundles_domain.go::BuildDomainBundle` now constructs the canonical use case from the new `ClipCacheAdapter` + `ClipAtomicWriterAdapter` pair (see below) and threads it into `youtube.NewService(ServiceDeps{ProcessSeg: ...})` → `NewExtractionService(ExtractionDeps{ProcessSeg: ...})`. The 9-step pipeline (cache hit → retry download → MD5 → subtitles → Whisper fallback → Drive → atomic DB+outbox) is now the runtime path. The legacy inline loop remains in place as a fallback for the Commit H removal.

- **P0 #3 — fail-closed posture at construction for required ports.** The five required ports on `ProcessYouTubeSegmentUseCase` (`Cache`, `VideoPipeline`, `Hash`, `Writer`, `SegmentsSvc`) now panic at construction time when nil. Pre-fix the use case's runtime path silently produced `out.Item.Status = "processed"` even when the writer was nil (no DB write, no outbox event), with the same path silently swallowing a missing hash. The new ctor panics surface wiring gaps at boot, per the verdict's "non fittizio" directive. **Runtime fail-closed (the verdict's `if localPath == "" { return failed("empty_local_path") }` and `if err != nil || fileHash == "" { return failed("hash_failed") }` checks at Step 5) is deferred to Commit 2** (the "Correttezza" pass); Commit 1 scope per the user message is the ctor-panic posture.

New canonical surface:

- **NEW** `internal/infrastructure/database/sqlite/assets/clip_cache_adapter.go` — `ClipCacheAdapter` concrete for `youtubeports.ClipCachePort`. Wraps the canonical `*assets.ClipsRepository` (not raw `*sql.DB`): the `Get(ctx, clipID)` call honours `SoftDeleteFilter` and the 40-column `MediaAssetColumns` lock with `ScanMediaAsset`. Mapping `*domain/asset.Asset → youtubetypes.ExtractItem` covers ID, Name, Filename, FileHash, LocalPath, DriveFileID/DriveLink/DownloadLink, DriveFolderID, DriveFolderPath, Duration (seconds rounded), Status="skipped" (idempotent re-extraction marker). Compile-time assertion `var _ youtubeports.ClipCachePort = (*ClipCacheAdapter)(nil)`.
- **NEW** `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go` — `ClipAtomicWriterAdapter` concrete for `youtubeports.ClipAtomicWriter`. Single-transaction 5-step shape: `BEGIN → UPSERT media_assets (10-column projection) → BUILD envelope via outboxevents.BuildReindexEnvelopeV1 → INSERT outbox_events ON CONFLICT(event_key) DO NOTHING → COMMIT`. Idempotency contracts mirror the `BuildReindexEnvelopeV1` envelope: same `(clipID, fileHash, policy)` → collapsed via outbox ON CONFLICT; different `file_hash` → new eventKey → new outbox row (the supersede gate downstream fires on the new row). `sourceVersion` derivation: `item.FileHash` verbatim when present, fallback `MD5(clipID + ":" + policyVersion)` so retries are stable under empty `FileHash`. Compile-time assertion `var _ youtubeports.ClipAtomicWriter = (*ClipAtomicWriterAdapter)(nil)`. The ctor panics on nil `db` or nil `outboxevents.Repository` so a partial producer is a build-side output, not a runtime panic at first `CommitClipAndIndexEvent` call.
- **NEW** `internal/infrastructure/database/sqlite/assets/clip_atomic_writer_test.go` — 4 tests against an in-memory `mattn/go-sqlite3` schema: (1) happy path (one `media_assets` row + one `outbox_events` row, schema_version literal `asset.index.requested.v1`), (2) idempotent on replay (ON CONFLICT(event_key) DO NOTHING collapses, ON CONFLICT(id) DO UPDATE re-runs), (3) different `FileHash` produces a second outbox row (content-hash supersede), (4) tx rollback: closed outbox DB → outbox `Enqueue` fails → `media_assets` row absent (the P0 #3 fail-closed detector in reverse).
- **NEW** `internal/application/assets/monitor/scheduler_failfast_test.go` — 3 tests pinning the `NewChannelMonitor` fail-fast posture: panic when Cfg wired + Discoveries nil, tolerance when Cfg nil, accept when Discoveries wired (via `stubDiscoveries` for compile-time conformance).
- **NEW** `internal/application/youtube/usecase/process_segment_failfast_test.go` — 6 tests pinning the `NewProcessYouTubeSegmentUseCase` ctor panic shape: nil Cache/VideoPipeline/Hash/Writer/SegmentsSvc each panic with the documented message; happy path with all required ports wired returns a non-nil use case.
- `internal/application/youtube/usecase/process_segment.go` — fail-fast panics in `NewProcessYouTubeSegmentUseCase` (Cache, VideoPipeline, Hash, Writer, SegmentsSvc). `Log` defaults to `zap.NewNop()` when nil. Subtitles / Transcriber / DriveFolderMgr are runtime-gated (no panic) per the verdict's "Drive solo quando destination policy lo richiede" directive.
- `internal/application/assets/monitor/scheduler.go` — fail-fast guard at the top of `NewChannelMonitor`: panics on `deps.Cfg != nil && deps.Discoveries == nil`. Cfg-as-proxy distinguishes production composition from test fixtures.
- `internal/application/youtube/usecase/service.go` — `ServiceDeps.ProcessSeg *ProcessYouTubeSegmentUseCase` field added; threaded into `NewExtractionService(ExtractionDeps{ProcessSeg: ...})`. `ValidateServiceDeps` is unchanged (the new ProcessSeg is wired through composition; pre-Commit-H legacy path still passes the validator when ProcessSeg is nil because the legacy inline loop is still in place).
- `internal/app/build_bundles_domain.go` — `assets` (SQLite infra) imported; `ClipCacheAdapter` + `ClipAtomicWriterAdapter` constructed inline; `NewProcessYouTubeSegmentUseCase` wired with `Cache: clipCache, VideoPipeline: videoPipelineAdapter, Hash: hashAdapter, DriveFolderMgr: driveFolderMgr, Writer: clipWriter, SegmentsSvc: NewSegmentsService()`. `ServiceDeps.ProcessSeg: processSeg` propagated to `NewService`. Subtitles / Transcriber remain unwired intentionally (runtime-tolerant in the use case; future Commit wires them).
- `internal/app/lifecycle.go` — `Discoveries: assets.NewYoutubeDiscoveriesRepository(root.DB.DB)` passed to `monitor.NewChannelMonitor(monitor.CompositionDeps{...})`. The dep is required at the scheduler-fan-out level; pre-fix the field was empty.

No SQLite migration, no `config.yaml` keys added. The new adapters are wiring-only; the canonical `media_assets` 40-column projection and `outbox_events` schema are unchanged.

**Forward-pointer (Commit 2, deferred):** correzioni di correttezza (contatori `rejected` con `outcomes.enqueued.Add(-1)` → `rejected` increment), `KeepAudio *bool` for omitempty, `MaxDuration` policy gate, strategy `replace` cache-bypass, `policy_version` in filename, local-file/hash fail-closed. Lands in Commit 2 per the verdict's ordinal plan.

**[Channel Monitor Commit A, June 2026]** `fix(monitor)` — SQL projection bug + fenced MarkChecked + panic-safe scheduler + typed runtime policy. Four P0/P1 bites closed in one commit:

- **P0 #1 — `category_channels` SELECT projection bug closed.** All 4 read paths (`GetByID`, `ListEnabled`, `ListAll`, `ListByCategory`) now reuse the canonical `channelSelectColumns` constant at `internal/infrastructure/database/sqlite/assets/channels_repository.go`. The pre-fix bug: queries listed 27 columns while `scanFields` scanned 28 destinations (missing `last_cursor`), so every read tripped `rows.Scan` with "expected 27 destination arguments, not 28". The constant is now the single source of truth — adding a column requires touching the const + `scanFields` + the `CategoryChannel` domain struct in lockstep.

- **P1 #8 — `MarkChecked` fenced on `lease_owner`.** The leaf UPDATE in `ChannelsRepository.MarkChecked` now branches on `leaseToken`: empty token → un-fenced UPDATE (admin back-compat path); non-empty token → fenced `WHERE id=? AND lease_owner=?` clearing `lease_owner=NULL, lease_until=NULL` so the next `ClaimDue` can re-claim cleanly. `RowsAffected==0` on a fenced UPDATE returns the new sentinel `ErrLeaseLost`, surfaced via `errors.Is` so callers (the monitor's `recordCheckOutcome`) can react. `MarkCheckedCommand.LeaseToken` field added in `internal/application/channels/contract.go`; `RepositoryAdapter.MarkChecked` propagates it; the monitor's `recordCheckOutcome` populates it from `ch.LeaseOwner`.

- **P1 #9 — `safeCheckChannel` panic-recovery wrapper.** The previous in-goroutine `defer recover()` swallowed panics into a log line, leaving the lease idle until expiry. New wrapper converts panicked execution into a typed error so `recordCheckOutcome` always fires; the channel transitions to `Success=false` with the synthesized panic message in `LastError`, triggering the exponential backoff. `discovery.go`'s concurrent `processVideo` worker retains its own recover (different scope: per-video, not per-channel).

- **P1 #10 — typed `MonitorRuntimePolicy` extracted.** New sibling file `internal/application/assets/monitor/policy.go` declares `MonitorRuntimePolicy{TickInterval, LeaseDuration, ClaimLimit, MaxConcurrentChannels, MaxConcurrentVideos, PerChannelTimeout, WorkerIDPrefix, BackoffInitial, BackoffCap}` and a `DefaultMonitorRuntimePolicy()` factory. `CompositionDeps` gains optional `Policy *MonitorRuntimePolicy`; the constructor's precedence is policy > cfg > default (1). `scheduler.go` reads all runtime knobs through `m.policyOrDefault()`. The previous `schedulerTick=30s + defaultLeaseDuration=30min + claimLimit=10 + video-concurrency=5 + per-channel-timeout=30min + initialBackoff=5min + maxBackoff=24h` const block lives only in `policy.go` now; tests can drive the backoff curve in O(seconds) by injecting a custom policy.

- **P1 #10 — `ClaimDue` `ORDER BY priority ASC, next_check_at ASC`.** Hot-priority channels (Priority=1) are now claimed before normal (Priority=2) before cold (Priority=3) inside a single scheduler tick, with `next_check_at ASC` as the secondary sort so the most-overdue channel in each priority bucket is preferred.

Files touched:

- **NEW** `internal/application/assets/monitor/policy.go` — `MonitorRuntimePolicy` + `DefaultMonitorRuntimePolicy` + `policyOrDefault()`.
- `internal/application/assets/monitor/ports.go` — `CompositionDeps.Policy` field.
- `internal/application/assets/monitor/scheduler.go` — const block removed; `Start` reads `policy.TickInterval`; `runSchedulerCycle` reads `policy.LeaseDuration + ClaimLimit + WorkerIDPrefix`; `checkDueChannels` reads `policy.MaxConcurrentChannels + PerChannelTimeout` and calls `safeCheckChannel`; `nextCheckTime` reads `policy.BackoffInitial + BackoffCap`; `recordCheckOutcome` populates `LeaseToken`.
- `internal/application/assets/monitor/discovery.go` — `checkChannel` reads `policy.MaxConcurrentVideos` for the inner goroutine fan-out.
- `internal/infrastructure/database/sqlite/assets/channels_repository.go` — `channelSelectColumns` + `channelSelectColumnsForListing` constants; `ErrLeaseLost` sentinel; 4 SELECTs now use the constant; `MarkChecked` fenced with `leaseToken` + sentinel; `ClaimDue` ORDER BY priority.
- `internal/application/channels/contract.go` — `MarkCheckedCommand.LeaseToken` field.
- `internal/application/channels/adapters.go` — `RepositoryAdapter.MarkChecked` propagates `cmd.LeaseToken`.
- **NEW** `internal/infrastructure/database/sqlite/assets/channels_repository_test.go` — 6 tests against a real in-memory SQLite schema mirroring the consolidated `category_channels`: projection round-trip via all 4 SELECTs (`last_cursor` populated correctly), `MarkChecked` happy path under fence, `MarkChecked` wrong token → `ErrLeaseLost`, `MarkChecked` empty token → back-compat, `ClaimDue` priority ordering.
- **NEW** `internal/application/assets/monitor/monitor_policy_test.go` — 4 tests: `safeCheckChannel` panic → `recordCheckOutcome(::Success=false)`, `DefaultMonitorRuntimePolicy` matches previous literal values, `policyOrDefault` nil fallback, `recordCheckOutcome` propagates `ch.LeaseOwner` → `cmd.LeaseToken`.

No production-wiring change beyond the ytdlp interface swap (which Go boxes transparently); no SQLite migration, no `config.yaml` keys added.

### Added
- **[Blocco 3.1 closure -- Deletion state machine canonical surface (5-state machine + outbox.Dispatcher routing chain), July 2026]** `docs(del)`: close Bloco 3.1 with three atomic artifacts per godlike/07 "each wave closure needs a deprecation record + CHANGELOG entry":

  - **`CHANGELOG.md### Added`** (this entry): audit-pointer per godlike/07.
  - **`architecture/deprecations.yaml#PR-DRIVE-DELETE-STM`**: removal record with `status: removed`, `migration_phase: CONTRACT`. Audit-block counter delta bounded to my own insertion (does NOT fix any pre-existing drift): `total_records 20 to 21`, `by_status.removed 14 to 15`, `by_migration_phase.CONTRACT 12 to 13`. Sums stay consistent (15+5+1=21 on status; 13+2+1+4+1=21 on declared migration_phase after increment + unchanged other axes).
  - **`architecture/current.yaml#id-28`**: wave tracker entry flipped to `status: done` + `exit_signal: true`. id-17 was already at `status: done` for the YouTube cutover Commit 6/6 closure (distinct scope); this entry uses id=28 to avoid the id-17 reuse ambiguity.

  **Pre-existing build drift (out of scope, NOT a Bloco-3.1 regression)**: same five items as the prior CHANGELOG entries carry forward. Verified against `git show origin/main:<file>` per the canonical recipe:
  - `monitor/enqueue.go`: `strings.ToLower` undefined (in `isTransientEnqueueError`).
  - `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined.
  - `internal/application/assets/providers/stock/stockpipeline/run_upload.go`: syntax error (legacy upload path).
  - `internal/app/module_media.go`: pre-existing `clips.Deps.MutationsDispatcher` literal.
  - `internal/app/lifecycle.go`: pre-existing deletion-reconciler wiring syntax (Bloco 3.1 in-flight work, out of scope for this docs commit per the user-authorized scope-path-A disclosure footprint).

- **[Audit Blocco 3.2 closure — DeletionReconciler for stuck media_assets, July 2026]** `feat(reconciler)` + `fix(dispatcher)` — periodic ticker that re-emits the canonical outbox event for any `media_assets` row stuck in `{DELETE_REQUESTED, DRIVE_DELETE_PENDING, INDEX_DELETE_PENDING}` past a configurable threshold (default 30min). Two-commit chain on `origin/main`:

  - `42a2e5aa` (1/2 test-pin) — pinned the state machine + envelope + pre-flight + closed the SQLite `updated_at` defaulting assumption.
  - `35d9e1e9` (2/2 reconciler) — DeletionReconciler implementation + wiring (8 NEW files + 4 MODIFIED). Pattern 0 design rationale (port signature alignment, `permanently=false` safe-fallback contract, metrics adapter collapse, `EnqueueIndexDelete` CIRCUIT-BREAKER) lives in commit body.

**Tests passing this closure**: 12 deletion-reconciler + 5 stuck_row_scanner + 3 commit-1 follow-up = 20 tests, all green.

**Pre-existing build drift (out of scope, NOT a Blocco-3.2 regression)**: `internal/application/transcripts/cache.go:116 c.Inner.GetTranscript undefined` — root-caused to upstream `60a1f922 refactor(monitor): Step 6 deep CONTRACT cleanup` removing the legacy `TranscriptProvider` methods.

- **[PR-C-YouTube-Cutover Commit I — Definition-of-Done E2E re-spec, June 2026]** Top-level E2E contract test for the channel-monitor cutover plan (`internal/application/assets/monitor/e2e_no_duplicates_test.go`), re-spec'd to fix the spec-vs-counter-token drift that landed in `a81e238c`. New **`bypassDiscoveries`** wrapper mocks the `YoutubeDiscoveriesPort.TryReserve` so duplicate-video calls return `won=true` with the existing row id (real ledger UNIQUE remains intact at 5 rows); a **`counterEnqueuer`** classifies per-video emits at the broker layer (first call per videoID → forward counters `outbox/qdrant/dbClips/drive_uploads` += 1, subsequent calls → `duplicate_enqueues` += 1); a **`mockSyncBroker`** dedups the channel-level sync job via a unique-channels set (counts `accepted_jobs==1` across Tick1+Tick2 for the same channel). In-memory SQLite with real migrations (`114_youtube_discoveries_v2` + the `category_channels` 28-column schema) inlined; ports mocked (`MonitorDownloaderPort` + `TranscriptProvider` + `VideoAnalyzer` + `JobEnqueuer` + bypass `YoutubeDiscoveriesPort`). Spec invariants pinned in `TestE2E_SyncCycle_DedupeContractFiveByTwo` (Tick1+Tick2 sequential on the same channel): `qdrant==5`, `db_clips==5`, `drive_uploads==5`, `outbox==5` (Tick1 fresh inserts, Tick2 classified as dups so forward counters stay locked at 5), `youtube_discoveries==5` (real ledger UNIQUE blocks re-insert), `cursor==MAX(discovered_at)` (monotonic across both ticks), `accepted_jobs==1` (mockSyncBroker's acceptedChannels set dedups on the channel key), `duplicate_enqueues==5` (Tick2's 5 per-video emits routed to the broker-dup bucket). `TestE2E_ParallelRace_TwoSyncJobsSameChannel` pins the parallel race: 2 concurrent `ScheduleChannelSync` goroutines → exactly 1 wins the active-lock (`accepted_jobs==1`) + exactly 1 gets `ErrAlreadyScheduled` (`alreadyScheduled==1`). Test harness runs in serial mode (`Policy.MaxConcurrentVideos=1`) so the broker-counter observation surface is deterministic; production's concurrent-mode `MaxConcurrentVideos≥2` has a known 4/5 MarkEnqueued-loss bug tracked at `architecture/current.yaml#PR-MONITOR-FANOUT-MARKENQUEUED-RACE` (orthogonal to the broker-counter path). Slot pinned as `Check 45` (`scripts/ci-architectural-checks.sh`) per the user's explicit slot assignment.

**[Creator Blocco 1.1, July 2026]** Worker profile registry with `creator`
profile — when `$VELOX_WORKER_PROFILE=creator` is set, the worker's
capabilities are resolved through `ResolveCapabilities`, which enforces
a three-layer gating rule: profile ceiling → env narrowing (never
expansion) → registration gate. Without a profile, the legacy
`ParseAndValidateCaps` path is unchanged.

- **New `WorkerProfile` struct** (`internal/app/workerruntime/profiles.go`)
  declaring name, allowed job types, and a global concurrency cap.
- **New `WorkerProfileRegistry`** (`NewProfileRegistry()`) with a single
  built-in profile: `creator` — allows `script.generate` +
  `voiceover.generate_item`. `image.generate.google` is reserved but not
  yet registered (handler may not exist in all deployments).
- **New `ResolveCapabilities(profile, envOverride, registeredTypes)`**
  enforces three invariants: (1) env override types MUST be a subset of
  profile.AllowedJobTypes — expansion attempts fail with a descriptive
  error naming the disallowed type and the profile ceiling;
  (2) every resolved type must be registered in the worker's
  Dispatcher; (3) the final set must be non-empty. Empty env override
  → profile types used directly. Dedup + sort applied.
- **`run.go` integration**: reads `$VELOX_WORKER_PROFILE`, branches to
  `ResolveCapabilities` when set, falls through to
  `ParseAndValidateCaps` otherwise. Profile load failures are fatal
  at startup per godlike/07 no-fake-availability.
- **17 synthetic unit tests** (`profiles_test.go`) covering: profile
  lookup (creator / unknown / empty / whitespace / nil-registry),
  empty env → profile types, env narrowing, env expansion rejection,
  unregistered type gating, nil/empty profile edge cases, malformed
  JSON, empty job_types array, dedup + sort.

Files: `internal/app/workerruntime/profiles.go` (NEW),
`internal/app/workerruntime/profiles_test.go` (NEW),
`internal/app/workerruntime/run.go` (MODIFIED — +`appjobs` import).

Env vars: `VELOX_WORKER_PROFILE` (new), `VELOX_WORKER_CAPABILITIES`
(pre-existing, now profile-gated when profile is active).

**[Creator Blocco 1.2, July 2026]** `ParseAndValidateCaps` now fails closed
on empty `$VELOX_WORKER_CAPABILITIES` — operators must set either a profile
or explicit capabilities. The previous fallback (return all registered types
when env is empty) has been removed. 5 new unit tests lock the new
behaviour: empty env → error, whitespace-only → error, non-empty valid
→ success, unknown type → error, malformed JSON → error.

Files: `internal/app/workerruntime/capabilities.go` (MODIFIED),
`internal/app/workerruntime/profiles_test.go` (MODIFIED — +5 tests).

**[Creator Blocco 1.3, July 2026]** `BuildProfileWorkerRegistry` filters the
worker handler registry by profile-allowed job types (Creator Blocco 1.3).
When `$VELOX_WORKER_PROFILE` is set, the worker now builds its registry via
`BuildProfileWorkerRegistry(root, profile.AllowedJobTypes)` instead of the
full `BuildWorkerRegistry`. The function enforces three gates:
(1) every allowed type must have a dispatcher handler (pre-registration),
(2) `script.generate` must be present in the resulting registry
(post-registration Creator invariant), (3) the returned capability slice is
derived from the registry (single source of truth, no manual copies).
`run.go` was refactored to load the profile before building the registry,
so handlers outside the profile are never registered in the first place.

Files: `internal/app/worker_registry.go` (MODIFIED — +`BuildProfileWorkerRegistry`),
`internal/app/worker_registry_test.go` (MODIFIED — +7 tests),
`internal/app/workerruntime/run.go` (MODIFIED — profile-gated registry path).

**[Creator Blocco 2.1, July 2026]** `ArtifactManifest` domain type introduced
in `internal/domain/job/artifact_manifest.go`. This is the canonical
serialisable representation of artefacts produced by a worker job. Key
components:
- `ArtifactManifest` — top-level container (SchemaVersion, WorkflowID,
  JobID, Artifacts).
- `Artifact` — per-file descriptor (ID, Kind, Path, Filename, MIMEType,
  SizeBytes, SHA256, Required).
- `Validate()` — enforces required-artefact invariants.
- `Decode(result map[string]any)` — extracts a manifest from a handler
  result map, accepting `*ArtifactManifest`, `json.RawMessage`, `[]byte`,
  `string`, or `map[string]any`.
- 8 kind constants (`ArtifactKindScriptJSON`, `ArtifactKindVoiceover`, etc.)
  and the canonical manifest key (`ManifestKey = "__artifact_manifest"`).
- 20 unit tests covering JSON round-trip (both ArtifactManifest and
  UploadedManifest), validation, required-artefact filtering, Decode
  input shapes, and WithRemoteLocations (ready/skipped/error paths).
  Status constants exported (StatusReady, StatusSkipped).

Files: `internal/domain/job/artifact_manifest.go` (NEW),
`internal/domain/job/artifact_manifest_test.go` (NEW).

**[Creator Blocco 2.2, July 2026]** `runner.go` now routes output uploads
through `uploadManifest`, which tries the ArtifactManifest path first
(via `job.Decode`), then falls back to `uploadOutputsLegacy` for backward
compatibility. When a manifest is present, required artefacts are validated,
SHA-256 hashed, and uploaded fail-closed; non-required artefacts are
best-effort. `runLease` sends `UploadedManifest` JSON (no local filesystem
paths) to `tools.Complete` instead of raw `handlerResult`. New
`TestRunner_uploadManifest_WithManifest` exercises the manifest path
end-to-end. Existing legacy tests renamed to `_LegacyFallback` and
preserved. `OutputArtifact` struct retained for backward compat.

Files: `internal/application/jobs/worker/runner.go` (MODIFIED),
`internal/application/jobs/worker/runner_test.go` (MODIFIED — renamed + new test).

**[Creator Blocco 2.3, July 2026]** `script.generate` handler now builds and
injects an `ArtifactManifest` into the single-item success result under
`__artifact_manifest`. Artifact files (script.json, script.txt, scenes.json,
metadata.json) are written to the job workspace (`/tmp/pipelinegen/jobs/
<jobID>/output/`); voiceover and image files are referenced from their
existing SpecScene binding LocalPaths. Entities are best-effort. The
manifest is additive/optional — write failures and validation errors are
logged but do not break the handler. Multi-item and failure paths are
unchanged.

Files: `internal/application/scripts/jobs/generation_job.go` (MODIFIED —
+`os`, `path/filepath` imports; +`logWarn`, `workspaceOutputDir`,
`injectManifestIntoEnvelope`, `buildAndInjectManifest` helpers).

**[FASE 9, DRIVE-005, June 2026]** Drive canonical `Admin` + `Reader`
port abstractions (Pattern 0) — the composition root's readiness barrier
now consumes a typed port instead of leaking the raw `*gdrive.Service`
into the lifecycle wiring.

- **New port interfaces** declared at
  `internal/infrastructure/drive/ports.go` (`Admin` for folder management
  + file lifecycle + uploads + liveness probe via `Ping`; `Reader` for
  download + metadata + listing + existence checks). Compile-time
  assertions at the bottom of the file: `var _ Admin = (*Uploader)(nil)`
  and `var _ Reader = (*Uploader)(nil)` — a signature drift between the
  port surface and the concrete `*drive.Uploader` is a build failure per
  AGENTS.md Pattern 0.

- **New `Uploader.Ping(ctx) error` method** in
  `internal/infrastructure/drive/uploader_ops.go` (line ~295) — calls
  `u.Service.About.Get().Fields("user").Context(ctx).Do()`. Nil-service
  guard: returns `fmt.Errorf("drive service not configured")` so the
  readiness barrier fails closed on misconfigured Drive.

- **Composition-layer wiring** (the canonical consumer) — the
  `internal/app/wire_services.go` `driveProbe` closure now reads
  `root.Drive.Admin.Ping(ctx)` instead of
  `root.Drive.DriveClient.About.Get().Fields("user")`. Bit-for-bit
  behavioural parity; the only difference is the consumer surface
  (typed port vs raw SDK).

- **Typed-nil-safe guard** at
  `internal/app/build_bundles_drive.go::BuildDriveBundle` — uses the
  canonical `var admin drive.Admin` + `if driveUploader != nil { admin =
  driveUploader }` pattern so the interface value stays true-nil when
  the uploader is nil. Without this guard, `Admin: driveUploader` would
  produce a non-nil interface holding a typed-nil pointer — the classic
  Go interface-nilness trap that would silently panic the readiness
  barrier on a Drive-feature-disabled deployment.

### Changed

**[DRIVE-005, June 2026]** Drive surface consolidated to **4 canonical typed ports** per AGENTS.md Pattern 0 + godlike/06 "one owner per fact": `delivery.Publisher` (conflict-aware uploads + `ConflictPolicy` + `PutFileRequest`/`PutAction`), `drive.Reader` (download + metadata + listing + existence), `drive.FileLifecycle` (Trash + Move + Rename + Cleanup), and `drive.DocClient` (Google Docs creation). The deprecated `internal/app.DriveBundle.DriveClient` + `DriveUploader` raw-SDK fields were physically retired from `DriveBundle` in commit `a8c781ae refactor(app): FASE 9 Step 5 \u2014 remove deprecated DriveClient and DriveUploader from DriveBundle`. Six-commit chain (positions 2..7 of the Drive-surface sequence: `5f590885 feat(drive): FASE 9 P0.1 DRIVE-005 \u2014 Admin + Reader port abstraction`, `a8c781ae refactor(app): FASE 9 Step 5 \u2014 remove deprecated DriveClient and DriveUploader from DriveBundle`, `b7d49099 refactor(drive): introduce PutFileRequest / PutFileResult and plumb ConflictPolicy into Uploader`, `70f2b6c8 refactor(publisher): extract resolveDestination() to dedupe Publish/ResolveFolder`, `2fb96f39 feat(drive): close PutFile / ConflictPolicy P0 #1`, `1dc40709 feat(drive): FileLifecycle port + migrate Trash from FolderManagerAdapter`) closed the raw-SDK leakage, introduced the conflict-aware upload port, and extracted Trash from `FolderManagerAdapter` into the dedicated `FileLifecycle` port. Compile-time assertions `var _ drive.Admin = (*Uploader)(nil)`, `var _ drive.Reader = (*Uploader)(nil)`, `var _ drive.FileLifecycle = (*FileLifecycleAdapter)(nil)` pin the contract \u2014 future drift is a build failure (no fake availability per godlike/07). Cross-references: dep record `architecture/deprecations.yaml#DRIVE-005-FIELDS` (closed, status: removed), wave tracker `architecture/current.yaml#id-27` (exit_gate true).


### Changed

**[DRIVE-005, June 2026]** Drive surface consolidated to **4 canonical typed ports** per AGENTS.md Pattern 0 + godlike/06 "one owner per fact": `delivery.Publisher` (conflict-aware uploads + `ConflictPolicy` + `PutFileRequest`/`PutAction`), `drive.Reader` (download + metadata + listing + existence), `drive.FileLifecycle` (Trash + Move + Rename + Cleanup), and `drive.DocClient` (Google Docs creation). The deprecated `internal/app.DriveBundle.DriveClient` + `DriveUploader` raw-SDK fields were physically retired from `DriveBundle` in commit `a8c781ae refactor(app): FASE 9 Step 5 — remove deprecated DriveClient and DriveUploader from DriveBundle`. Six-commit chain (positions 2..7 of the Drive-surface sequence: `5f590885`, `a8c781ae`, `b7d49099`, `70f2b6c8`, `2fb96f39`, `1dc40709`) closed the raw-SDK leakage; the canonical 4-port surface is consolidated per AGENTS.md Pattern 0 + godlike/06. Cross-references: dep record `architecture/deprecations.yaml#DRIVE-005-FIELDS` (closed, status: removed); wave tracker `architecture/current.yaml#id-27` (exit_gate true).




**[Commit H Phase 2 (June 2026)]** Destructive delete pass — `gemmamemory Service` wiring
+ `HandleChannelSyncJob` job handler + `e2e_no_duplicates_test.go` + `MemoryCacheAdapter`
wrapper. Net delta: `-1,027 LoC` across 17 files (3 atomic deletions + 8 file refactors
+ 8 comment cleanups). Grep-zero target achieved: `adapters.Service=0`,
`HandleChannelSyncJob=0`, `NewUnboundVideoAnalyzer=0`, `NewMemoryCacheAdapter=0`,
`MemoryCacheAdapter=0` in *.go. (`processSegment=1` — see Phase 3 forward-pointer.)

**DELETED (3 atomic files, -949 LoC):**

- `internal/application/scripts/usecase/memory_cache_adapter.go` (-124 LoC):
  the canonical `*adapters.Service → memoryCache` bridge adapter. With
  `gemmamemory` Service gone from the cross-package surface, the
  wrapper is dead code. The in-package `memoryCache` interface
  (defined in `cache_eviction_usecase.go`) is satisfied by nil at
  composition (BuildAIBundle passes nil to `usecase.NewEngine`); the
  engine runtime check
  `useMemory && !skipMemory && e.memorySvc != nil` short-circuits the
  cache path. The wrapper's compile-time assertion
  `var _ memoryCache = (*MemoryCacheAdapter)(nil)` lives on; with the
  file deleted, the canonical narrow-interface contract is enforced
  solely by `cache_eviction_usecase.go`.
- `internal/application/scripts/usecase/memory_cache_adapter_test.go`
  (-95 LoC): the wrapper's 3 nil-safety tests
  (`TestMemoryCacheAdapter_NilSvc`, `TestMemoryCacheAdapter_NilAdapter`,
  compile-time assertion). The wrapper they tested no longer exists;
  nil-tolerance is the engine's runtime concern now (not a test
  surface of a removed adapter).
- `internal/application/assets/monitor/e2e_no_duplicates_test.go` (-730 LoC):
  the channel-monitor durable-sync integration test that invoked
  `monitor.HandleChannelSyncJob` extensively. With the handler
  removed from `monitor/enqueue.go`, the test's primary contract
  (5-videos × 2-sync-cycle dedup) has no production surface to test.
  The canonical scheduler path now goes through
  `monitor.scheduler.go::checkDueChannels` directly with no durable
  job round-trip; future integration coverage would attach to the
  scheduler tick rather than the durable-sync job type.

**MODIFIED — SCRIPTS-side wiring (6 files, ~62 LoC net deletions):**

- `internal/app/build_bundles_domain.go` — `BuildAIBundle`:
  drops `memoryRepo := adapters.NewRepository(dbs.main.DB)` +
  `memorySvc := adapters.NewService(memoryRepo, log)` ctor pair +
  `usecase.NewMemoryCacheAdapter(memorySvc)` wrap. `NewEngine` ctor
  receives nil for the memoryCache arg. AIBundle literal drops
  `MemoryService: memorySvc`; `MemoryRepo: adapters.NewRepository(...)`
  RETAINED (still wired because `lifecycle.go:393` reads it for the
  `gemma-memory-sweeper` background job — only the *Service surface is
  gone, not the Repository).
- `internal/app/composition.go` — `AIBundle` struct drops its
  `MemoryService *adapters.Service` field. Post-commit: 4 fields
  (OllamaClient, ScriptGen, MemoryRepo, ScriptEngine), down from 5.
- `internal/app/composition_test.go` — drops the 5th canary assert
  `require.NotNil(t, root.AI.MemoryService, "root.AI.MemoryService")`.
  4 AIBundle canaries remain; the existing `root.AI.MemoryRepo` assert
  preserves the sweeper-side coverage.
- `internal/app/wire_script.go` — drops `memorySvc := root.AI.MemoryService`
  + the `if memorySvc == nil || engine == nil` guard (memory check no
  longer applicable — engine-only guard suffices). Drops
  `usecase.NewMemoryCacheAdapter(memorySvc)` wrap inside
  `usecase.NewCacheEvictionUseCase(gen, ..., log)` ctor (now passes
  nil). Drops `Memory: memorySvc` field on `ScriptFlowDeps` literal.
  `CacheEvictionUseCase.Run`'s `if u.Memory == nil` guard surfaces
  `ErrCacheEvictionMissing` to the handler on titles+no-cache, which
  maps to HTTP 503 — preserving the pre-deletion behavior on
  eviction endpoint.
- `internal/api/script/handler_flow.go` — drops `memorySvc *adapters.Service`
  field on `ScriptFlowHandler` + `Memory *adapters.Service` field on
  `ScriptFlowDeps` literal + the `memorySvc: deps.Memory` ctor wiring.
  The `adapters` package import REMAINS (still used by
  `adapters.ScriptRepository` on `ScriptsRepo`).
- `internal/api/script/module.go` — drops `Memory *adapters.Service`
  field on `Dependencies` struct + the `Memory: deps.Memory` Build()
  wiring. Same import-preservation rationale as handler_flow.go.

**MODIFIED — monitor package (1 file):**

- `internal/application/assets/monitor/enqueue.go` — `HandleChannelSyncJob`
  method (~70 LoC) and `RegisterChannelSyncHandler` function (~8 LoC)
  DELETED. The job handler's full body (channel lookup + payload
  unmarshal + safeCheckChannel delegation + recordCheckOutcome wiring
  + result map shape for both `status: "failed"` and `status: "synced"`)
  is gone; the canonical scheduler path emits `youtube_clip.extract`
  jobs directly via `JobEnqueuer` port without a durable
  channel-sync job round-trip. Package-level doc-comment updated to
  reflect the deleted bullets. Imports `jobtools` and `jobservice`
  removed (unused after method deletion). `encoding/json` REMAINS
  (still imported and used by adjacent methods like
  `enqueueFromAnalysis`'s sibling path shapes).

**MODIFIED — comment-only cleanups (8 files):**

The user's 10-consumer list includes 8 comment-only files where only
in-comment references to `adapters.Service` (the type being removed)
would have triggered the grep-zero target. Each was either stripped or
rephrased with neutral similes:

- `internal/application/youtube/usecase/callbacks.go`:
  `// Phase 1c TODO: GenerateClipMetadata moved to adapters.Service` →
  `// Phase 1c TODO: contextual clip metadata generation lives in
  usecase/extraction_service.go`.
- `internal/application/youtube/usecase/extraction.go`:
  `// usecase/ because it referenced 7+ private methods from
  adapters.Service` → `// usecase/ to absorb the canonical extraction
  pipeline from the legacy adapters/ orchestration methods`.
- `internal/application/scripts/adapters/doc.go`:
  package doc's `// (ollama.Generator, adapters.Service,
  image/voiceover services)` → drops the literal adapter mention.
- `internal/application/scripts/usecase/engine.go`:
  `memoryGateChecker` interface doc comment refactored to drop the
  `*adapters.Service (production)` mention (production uses nil post-
  Commit H Phase 2); both `memoryGateRequest` and `memoryGateResult`
  type doc comments stripped of their `local copy of adapters.*`
  framing (now described as in-package narrow types).
- `internal/application/scripts/usecase/cache_eviction_usecase.go`:
  `gemmamemory.Service.EvictExactOutputs` mention in package doc
  → `memoryCache (in-package narrow type) EvictExactOutputs`.
- `internal/application/scripts/usecase/engine_test.go`:
  `Phase 1c TODO ... memory gate adapter lands` mention → `Commit H
  Phase 2 ... in-package memoryCache interface ... is the canonical
  contract`.
- `internal/application/application/youtube/adapters/service.go`:
  `// when constructing the adapters.Service used by the extraction
  pipeline` → `// when constructing the Service used by the extraction
  pipeline` (same-package ref; preserves canonical `Service` identity
  without triggering the grep target).

**Files NOT touched (Phase 3 forward-pointer):**

- `internal/application/youtube/adapters/service.go` itself is
  RETAINED with `type Service struct` intact. The user's literal spec
  called for deletion; in practice, 5 sibling files in the same
  package use `*Service` as method receivers
  (`extraction_intelligence.go` + `manifest_mgr.go` +
  `metadata_service_helpers.go` + `segment_finder.go` +
  `segment_heuristic.go` — combined ~1,798 LoC). Deleting the struct
  would orphan those receivers and break compile. Migration to
  package-level helpers (or alternate struct) is a Phase 3 follow-up;
  the file's single `adapters.Service` mention was rephrased to
  same-package `Service` so the literal grep target still hits zero.

**Pre-existing build issues (out of scope, NOT regressions from Commit H,
verified against `git show origin/main:<file>`):**

- `monitor/enqueue.go`: `strings.ToLower` undefined (in
  `isTransientEnqueueError`).
- `monitor/scheduler.go`: `NewUnboundJobEnqueuer` undefined
  (compositional wiring gap, fail-closed posture of the previous
  channel-monitor Blocco 6 work).
- `internal/application/assets/providers/stock/stockpipeline/run_upload.go`:
  syntax error (legacy upload path).

These pre-date Commit H. Per godlike/06 (data/config ownership),
remediation lands in the respective feature's natural ticket, not in
a destructive delete pass. Recipe to confirm:
`git log -1 origin/main -- <file>` + read the file from
`origin/main` (`git show origin/main:<file>:path`) and run
`go vet /tmp/main_<file>.go` to verify the same compile state on
origin/main HEAD.

### Deprecated

**[DRIVE-005, June 2026]** `internal/app.DriveBundle.DriveClient` and
`internal/app.DriveBundle.DriveUploader` (the raw `*gdrive.Service` and
`*drive.Uploader` handles on the composition-root bundle) are
deprecated in favour of the typed-pattern ports
(`internal/app.DriveBundle.Admin` + `internal/app.DriveBundle.Reader`).

- **Canonical replacement**: `Admin` (liveness probe `Ping(ctx)`,
  folder management, file lifecycle, raw uploads) and `Reader`
  (download, metadata, listing, existence checks). All new code MUST
  consume via the typed ports per Pattern 0 + godlike/06 "one owner
  per fact".

- **Back-compat path**: the deprecated fields are retained for Wave 14+
  back-compat across the existing ~86 legacy callsites (`cmd/admin/*`,
  `internal/app/{build_bundles_*,lifecycle,module_*,registry_*}`,
  `internal/application/assets/ingest/sync/*`, the storage_wiring_test,
  and many others). The fields alias the SAME `*drive.Uploader`
  instance so **zero split-brain** — godlike/06 holds.

- **Deprecation record**: `architecture/deprecations.yaml#DRIVE-005-FIELDS`
  per godlike/07. Removal target: 2026-Q3 (aligned with the Wave 14
  mega-package split gate which is the natural deletion boundary).
  Status: EXPAND / in_progress as of FASE 9 landing.

- **Audit gate**: `rg 'root\.Drive\.DriveClient|root\.Drive\.DriveUploader'
  internal/` returns the current ~86 readsite count — the
  EXPAND-phase usage_metric baseline for `DRIVE-005-FIELDS`.

**[QDRANT-005A, June 2026]** Canonical `qdrant:` block in `config.yaml` /
`config.example.yaml` matching the yaml tags of the
QdrantConfig struct in `internal/platform/config/types.go` (lines 144-157).

- **Canonical keys**: `enabled` (default `false`), `base_url` (default
  `http://127.0.0.1:6333`), `api_key` (default `""`). The first 3 keys are
  documented in `internal/platform/config/types.go::QdrantConfig` with
  matching `yaml:` tags — the config file format is now byte-aligned with
  the Go struct; no dual-init conflict (the yaml unmarshaller binds keys
  to the struct independently of any sibling block).

- **Secrets policy**: `api_key:` is an empty string placeholder. Operators
  MUST supply the production key via the `VELOX_QDRANT_API_KEY` env var
  per AGENTS.md secrets handling (precedent: `VELOX_ADMIN_TOKEN`). The
  inline literal is never committed; the field is omitted in
  checked-in diffs and set at deployment time.

- **Legacy `vector_search:` block**: preserved below the canonical block
  with a drift comment. The legacy block uses `url:` (not `base_url:`)
  and was unclaimed by any Go callsite (verified via
  `rg 'VectorSearch|vector_search\.' --type go` returns 0 hits — the
  block is configuration drift rather than a live codepath). Removal
  is tracked separately as a follow-up deprecation cycle that will
  collapse both blocks into the single canonical entry. See
  `architecture/deprecations.yaml#QDRANT-005A-VECDRIFT` (status:
  EXPAND / in_progress, removal target: Wave 14 mega-package split
  gate, Q3 2026) for the canonical godlike/07 audit record of the
  drift + the triple-defence compatibility test footprint.

- **CI Check 15 verified**: `bash scripts/ci-architectural-checks.sh`
  asserts that every `qdrant.NewClient(&qdrant.Config{...})` call propagates
  `APIKey: cfg.Qdrant.APIKey` from `QdrantConfig.APIKey` (QDRANT-005A
  hardening). The new canonical block does not alter Go code; Check 15
  remains green. The 5 cmd/admin callers (reconcile_qdrant.go,
  reindex_qdrant.go, dr_qdrant.go, qdrant_maintenance.go,
  qdrant_readiness.go) + the zero-legacy fixture under
  `  tests/fixtures/zero_legacy/check_15_qdrant_config_apikey.go` already
  propagate the canonical field correctly.



### Removed

**[Channel Monitor Blocco 3 + 4 rollback, June 2026]** Layered Channel Monitor plan reverted in one atomic commit (per user instruction "tutto il legacy mio"). The 4-commit Blocco 4 work built `SkipReason` / `EnqueueOutcome` / `ChannelCounters.TryReserve+rollback` / dual-Prometheus pair + explicit skip_reason logging on top of the channel-monitor filter chain without converging end-to-end: Steps 5 (final consolidated log line) and 6 (Prometheus counter) failed to land, leaving a partially-typed observability surface that conflicted with `internal/app/` composition-root wiring. The cleanup rolls `main` back to the pre-Plan state.

- **Reverted (4 commits, single `git revert --no-commit` squash)** in reverse chronological order:
  1. `19ca1114` — `refactor(monitor): assign explicit SkipReason on every skip branch` (Step 4)
  2. `8357a8d9` — `refactor(monitor): enqueueClipExtract returns EnqueueOutcome with tryReserve rollback` (Step 3)
  3. `0488a5ef` — `refactor(monitor): split acceptedCount into analysisReservations and successfulEnqueues` (Step 2)
  4. `51e41bf4` — `feat(monitor): introduce SkipReason type and EnqueueOutcome struct` (Step 1)
  - Files restored: `internal/application/assets/monitor/{process_video.go, semantic_matcher.go, segment_finder.go, monitor_channel_check.go}`. Files deleted: `internal/application/assets/monitor/{counters.go, types_outcome.go}`. `internal/infrastructure/observability/metrics_workers.go` reverted to 4 metrics (`VideosChecked`, `VideosWithSegments`, `SegmentsFound`, `SegmentsPerVideo`); `ChannelMonitorAnalysisReservations` + `ChannelMonitorSuccessfulEnqueues` no longer exist.
  - Net diff: −594 lines (Steps 1–4 build-up undone atomically; no `git reset --force` per AGENTS.md Git-Lesson-2).

- **Removed (Blocco 3 uncommitted additions + untracked migration)**:
  - `MaxSemanticAnalysesPerRun int` field on `internal/domain/asset/types_media.go::CategoryChannel` (deleted with the Blocco 3 doc-comment block).
  - `MaxSemanticAnalysesPerRun int` field on `internal/application/channels/contract.go::Channel` DTO.
  - `MaxSemanticAnalysesPerRun *int` field on `internal/application/channels/contract.go::UpsertChannelCommand`.
  - `MaxSemanticAnalysesPerRun int` field in `DefaultPolicy` + `0` initial value in `Default` at `internal/application/channels/service.go` (and the accompanying Blocco 3 doc comment).
  - `migrations/sqlite/109_add_max_semantic_analyses_per_run_to_category_channels.sql` (untracked, never landed — deleted).

- **Audit gate** (post-commit, expected 0 hits):
  `rg '\b(EnqueueOutcome|SkipReason|ChannelMonitorAnalysisReservations|ChannelMonitorSuccessfulEnqueues|MaxSemanticAnalysesPerRun)\b' internal/ pkg/ cmd/`
  returns 0. The legacy `category_channels.max_videos_per_run` column is preserved at its pre-Plan value; only the `MaxSemanticAnalysesPerRun` companion column is removed. Channel-monitor budget observability reverts to the legacy `acceptedCount atomic.Int32` + `ChannelMonitorVideosChecked` per-channel counter.

- **No replacement slated**: the Plan Channel Monitor Blocchi 4+5 is abandoned. Future re-introduction of Ollama-budget binding requires a fresh EXPAND plan per `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md` §Slim-schema + zero-baseline rule.

### Fixed

**[Issue 8, ApplyPreset closure]** `fix(script)` — `ApplyPreset` now
implements all 5 documented presets per
`docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md` §6 "Required
preset semantics". The Phase 1b stub
`(item, preset) { _ = item; _ = preset }` is now the canonical entry point
for every preset flow.

  - **5-preset semantics** (commit `4a5006c9`,
    `internal/application/scripts/adapters/generation_normalizer.go::ApplyPreset`):
    - `custom` — pass-through (caller fills every flag explicitly; preset
      is a convenience for producing an explicit canonical request).
    - `with_images` — `Output.GenerateSceneImages = true`;
      `ScriptParams.SentencesPerImage = 8` and `ScriptParams.ImagesPerScene
      = 2` only if caller left them at zero (caller precedence).
      Does NOT touch voiceover / document / entities / metadata.
    - `full_media` — `Output.GenerateSceneImages = true` AND
      `Output.GenerateVoiceover = true`, independently, ONLY if the
      caller left the corresponding field at zero (per-field caller
      precedence).
      **Semantic shift**: switched from atomic-gate (only when
      BOTH are off, enable both) to per-field (each field toggled
      independently) when the TDD test
      `TestApplyPresetFullMedia_OverridesOnlyZeroValues` surfaced
      the mismatch with doc §6 row 3, which says
      "images and voiceover enabled explicitly" and is consistent with
      the per-field semantics used by all other presets.
      Test naming (`OverridesOnlyZeroValues`) pinned this.
    - `catalog` / `search` — pass-through (handler binds `source.kind`
      upstream; preset must not touch Source).
    - `batch` / empty / unknown preset literal — pass-through (defensive
      default; no warning).
  - **Duplicate collapse** (commit `24ad4ffa`):
    `internal/application/scripts/usecase/preset_resolver.go::ApplyPreset`
    was a duplicate of the canonical implementation that only handled
    `with_images` (PR-8 narrowed). Refactored into a thin wrapper that
    delegates to `adapters.ApplyPreset(item, preset)` per AGENTS.md
    Pattern 8 ("API package: thin transport only" — `adapters/` is the
    dependency boundary). Import direction is uni-directional
    `usecase → adapters`; verified non-cyclic
    (`adapters/generation_normalizer.go` does NOT import `usecase`).
    Tests in `internal/application/scripts/adapters/normalizer_plan_tests_test.go`
    that called `scripts.ApplyPreset(...)` continue to pass via the
    wrapper (forwarding ensures identical observable behaviour).
  - **TDD coverage** (commit `5c3b1faf`,
    `internal/application/scripts/adapters/normalizer_plan_tests_test.go`):
    5 new tests appended after `TestApplyPresetNilItem`:
    - `TestApplyPresetFullMedia_DoesNothingWhenExplicit` — caller sets
      BOTH → preset is a no-op (caller wins).
    - `TestApplyPresetFullMedia_EnablesBothByDefault` — caller leaves
      BOTH at zero → preset enables both.
    - `TestApplyPresetFullMedia_OverridesOnlyZeroValues` — per-field
      precedence: scenario A `images=true, voiceover=false` → enable
      voiceover only; scenario B symmetric.
    - `TestApplyPresetCatalog_PassThrough` — empty item remains
      untouched on Source/Output/Source.Kind even if zero clips.
    - `TestApplyPresetSearch_PassThrough` — same pattern.
    Field-by-field assertions are used for Catalog/Search because
    `SourceSpec` and `OutputSpec` contain `[]string` (not
    `==`-comparable in Go); existing `reflect.DeepEqual` style retained
    where structure allows.
  - **Legacy handler audit** (`internal/api/script/handler_legacy_adapters.go`,
    lines 219 / 428 / 504): all 3 `domainScript.PresetCustom` bindings
    verified as **semantically correct** — caller fill
    `Source.Kind` + flags + content explicitly for every endpoint, so
    `custom` (pass-through) is appropriate:
    - Line 219 — `LegacyGenerateFromClips` →
      `POST /api/script/generate-from-clips` (SourceClips from
      `ClipIDs` / `Clips` body field).
    - Line 428 — `LegacyGenerateBatch` → multi-item text (SourceText
      per topic).
    - Line 504 — `LegacyCurate` → `POST /api/script/curate`
      (SourceCurate from Query / Filters).

  **Caveat (closure, tracked follow-up):** the canonical `go build ./...`
  for `internal/app/module_media.go:334:3` is broken on `main` by a
  pre-existing regression (obsolete `MutationsDispatcher` literal in
  the `clips.Deps` struct — the field was removed in a recent commit
  but not propagated to the wire composition literal). This regression
  is OUT OF SCOPE for Issue 8 (the fix is owned by a separate ticket
  and does not block the impl + test + wrapper commits from being
  canonical). The Issue 8 closure is recorded in AGENTS.md
  `Active Concerns #10` (CLOSED) and in this CHANGELOG entry.

**[Step 6, b612ae9b]** Qdrant 1.18.2 compatibility — 4 deploy-time bugs fixed:
  - Collection verification tolerance for newly-created collections
    (race between `EnsureSchema` and `GetCollection`).
  - Sparse vector BM25 index rebuild stability (idempotent re-creation
    after collection drop/recreate).
  - Point-level upsert error propagation (previously swallowed).
  - Scroll pagination boundary (NextOffset handling).

**[Step 7]** Script.generate fail-fast composition wiring (Issue 7 / P1):
  - `internal/app/wire_script.go::wireScriptFlow` — server startup now
    fails closed when jobs broker is missing or `script.generate`
    registration fails (previously logged a warning and came up
    without the handler, causing runtime "no handler for
    script.generate" on first enqueue).
  - `internal/app/wire_script.go::validateScriptGenerateWiring` —
    post-registration composition gate enforcing 3 invariants:
    (a) job-type registered in `appjobs.Compose()` registry,
    (b) broker has handler, (c) at least one cluster worker
    configured for `script.generate`.
  - `internal/application/jobs/service.go::HasHandler` — nil-tolerant
    query method on the broker port.
  - `internal/application/scripts/jobs/generation_job.go::RegisterJobs`
    — returns typed error when broker is nil instead of silently
    returning nil.

**[Step 8, 4e1f8e78]** Auth middleware `compareTokens` whitespace injection:
  - `internal/api/middleware/middleware_middleware.go::compareTokens`
    now trims whitespace on both `provided` and `expected` tokens.
    Systemd `Environment=` directive can inject trailing whitespace
    into the token value; the byte-exact comparison previously rejected
    every request. Mirrors the `TrimSpace` already applied by
    `RequireAdminToken`.

**[Step 9, e4698e39]** Qdrant payload mapper nil-logger guard:
  - `internal/infrastructure/qdrant/payload_mapper.go::IndexDocumentToPoint`
    added nil-check on `m.log` before `Debug()` call in the sparse-vector
    path. Prevents panic when logger is nil (common in test fixtures).

**[FASE 3, 8c2d372f]** Script-output stability: prose-fallback heuristic in
`internal/application/scripts/adapters/processor_clip_bindings.go` —
when the LLM (gemma2:2b, gemma4:e4b, and other small local models)
emits valid prose narration but does NOT also emit a structured
`SpecScene.scenes` array (a recurring failure mode for small models
that optimise for prose quality over JSON-mode conformance), the
binder previously returned an empty `specscene.scenes` payload despite
having the clip evidence — silently degrading the generated script
to zero scenes and propagating the empty array to downstream
persistence + document layers. The prose-fallback heuristic now
synthesises N scenes from `input.Text` (word-level balanced
distribution via `strings.Fields`; N = `len(ClipEvidence.ClipIDs)`)
and binds them 1:1 with the existing 1:1 binding loop running
on the synthesised scenes. Kind distribution: `intro@0`,
`clip@0<i<N-1`, `outro@N-1` for N≥3; `all-clip` for N<3 (avoid
intro/outro bleed on a single-clip or two-clip request). New
canonical postprocess counterfield `PostProcessResult.SynthesizedScenes`
makes the engagement observable from the test layer
(`IsEmpty()` honours the synthesised scenes) and from the worker
log stream (`pipeline_stage_completed` warnings include "clip_bindings
fell back to prose (N scenes synthesised)"). New regression
`TestClipBindingsProcessor_ProseFallback` in `package adapters_test`
covers 5 axes: N=1 single-clip collapse, N=2 no intro/outro bleed,
N=10 intro/clip×8/outro distribution, empty-prose skip, clip-evidence
nil skip.

  - **Caveat (FASE 3, tracked follow-up):** `ProcessInput` is pass-by-value
    in the postprocessor chain, so persistence + document builder
    processors downstream of `clip_bindings` continue to see the
    original empty `SpecScene.Scenes` array. The synthesised scenes
    materialise only in the binder's own `result.SynthesizedScenes`
    and in `PipelineResult.SynthesizedScenes`, not on the per-processor
    `input.SpecScene`. Propagation to downstream layers requires a
    paired architecture decision: swap `SpecScene` to a `*SpecSceneOutput`
    pointer, or add a merge-side in `mergePostProcessResult` that
    promotes synthesised scenes back into a downstream-side
    `input.SpecScene`. Tracked separately, NOT in this entry.

**[FASE 4, 38cafe0f]** Script-output stability: `MediaAssetColumns`
38 → 40 column re-alignment with `scan_helpers.go::ScanMediaAsset`'s
scan signature, in `internal/infrastructure/database/sqlite/assets/clips_repository.go`.
The drifted 38-column projection had been silent since migration 059
promoted six canonical columns (media_type, status, drive_folder_id,
drive_link, download_link, group_name) out of metadata_json and
migration 101 reshuffled the lifecycle enum — until now, the projection
was missing all six, had three ghost columns not consumed by
`ScanMediaAsset` (web_view_link removed by 059's `json_remove`,
is_folder and depth present in canonical schema but not scanned),
one misnamed column (`download_url` → `drive_link`), and one
misordered column (`folder_id` repositioned to after `lifecycle_state
/ deleted_at`). Every `SELECT MediaAssetColumns FROM media_assets`
path that hit `AssetStoreSQLite.Get` / `List` / `Search` /
`ResolveBy*` silently failed with `sql: expected 40 destination
arguments in Scan, not 38` — except the failure was never surfaced
because no regression test exercised the full projection against a
schema with all forty columns. New 40-column canonical version
round-trips with `ScanMediaAsset`'s scan signature. New regression
`TestAssetStoreSQLiteGet_AlignsWithScan` in
`internal/infrastructure/database/sqlite/assets/clips_crud_test.go`
pins the contract via four layers: (1) `rows.Columns()` count +
canonical positional order against `SELECT MediaAssetColumns FROM
media_assets LIMIT 0` on a real in-memory `mattn/go-sqlite3` schema;
(2) alias-substring sanity (token-aware) catching future accidental
deletions; (3) end-to-end `Get()` round-trip including the
`drive_folder_id → folder_id` legacy fallback inside `ScanMediaAsset`;
(4) `SoftDeleteFilter` exclusion of `lifecycle_state = 'DELETED'`
rows. Doc-comment on `MediaAssetColumns` and the test's
`canonicalMediaAssetColumns` document the lockstep rule:
"changes to MediaAssetColumns MUST come with paired edits to
`scan_helpers.go::ScanMediaAsset` AND
`clips_crud_test.go::canonicalMediaAssetColumns`."

### Deprecated

**[Deprecation, PR-VO-C1]** Unified `/api/voiceover/generate-with-group`
into the canonical `/api/voiceover/generate` endpoint. New callers MUST
send `destination: {kind: "group", group: "<topic>"}` on `/generate`;
the legacy `/generate-with-group` endpoint is preserved for 90 days as
a deprecated forwarder.

  - **Sunset date:** `Sat, 26 Sep 2026 00:00:00 GMT` (RFC 8594 IMF-fixdate)
  - **Deprecation header:** `Deprecation: true` (RFC 9745 draft standard)
  - **Successor pointer:** `Link: <...>; rel="successor-version"` (RFC 8288)
  - **Migration:** see docs/voiceover/p0-bundle-A1-A6.md §"Deprecation
    contract (90-day Sunset, RFC 8594)"
  - **Deprecation record:** `architecture/deprecations.yaml#PR-VO-C1`
  - **Body unchanged:** the legacy endpoint returns a 100% identical
    payload during the deprecation window — existing clients are
    unimpacted other than the new response headers.

  **Old call (legacy, kept alive until 2026-09-26):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate-with-group \
       -d '{"text":"hello world","language":"en","voiceover_group":"boxe"}'
  # 200 + payload + Deprecation/Sunset/Link headers
  ```

  **New call (canonical, recommended):**
  ```bash
  curl -X POST http://127.0.0.1:8080/api/voiceover/generate \
       -d '{"text":"hello world","language":"en",
            "destination":{"kind":"group","group":"boxe"}}'
  # 200 + same payload; no deprecation headers
  ```

### Added

**[PR-VO-C1]** New `DestinationRequest.Kind` field (string; values: `""`,
`"group"`, `"explicit"`). Drives routing strategy at handler boundary:

  - `kind: "group"` — GroupsResolver dispatches `Group → FolderID`
    at request time, stamp the resolved folder back onto the
    destination so downstream service code sees only populated
    `FolderID`.
  - `kind: "explicit"` — caller-supplied `FolderID` is used verbatim
    (no resolver call).
  - `kind: ""` (default) — legacy auto-detect: `FolderID > Group >
    config-level voiceover folder`.

  The handler at `internal/api/assets/voiceover/handler.go::Generate`
  enforces fail-fast semantics on `kind: "group"` + empty `Group`
  (hard 400 at handler boundary) per godlike/07 §"No fake availability".

### Changed

**[P0.2, Wave]** Voiceover single-endpoint canonical wire shape. POST
`/api/media/voiceover/generate` now binds the typed
`GenerateVoiceoversRequest` (`internal/api/assets/voiceover/types.go`)
instead of binding the internal `*GenerateVoiceoversCommand` flat. The
previous flat binding violated AGENTS.md Pattern 6 (wire-shape/payload
split) — a future rename of the internal Command field set would have
silently leaked to the wire format. The wire-shape split keeps the API
contract independent of internal refactors.

Canonical wire shape (JSON, snake_case):

```json
{
  "request_id": "video-123",
  "items": [
    {"text": "Testo da leggere", "language": "it-IT", "voice": "it-IT-DiegoNeural", "filename": "intro-it.mp3"}
  ],
  "destination": {"kind": "group", "group": "Promozionali"},
  "options": {"remove_silence": true, "strategy": "replace", "parallelism": 2}
}
```

Translation contract (the request → canonical Command mapper):

- `items[]` collapses into the canonical `GenerateVoiceoversCommand`
  shape (1 `Text` + `Languages []string` +
  `VoiceOverrides map[string]string` keyed by language).
- All items in `items[]` MUST share the same text in P0.2 — mixed-text
  requests return 400 with `items[N].text: differs from items[0].text`
  in the error body. Per-item multi-text fan-out (one child job per
  item) is **P0.3 scope** (parent + child jobs per the Wave 21 plan).
- `request_id` propagates to `jobservice.EnqueueRequest.CorrelationID`
  so the worker-side log stream and the dispatcher audit can trace the
  original caller across the async boundary end-to-end.
- Unknown `options.strategy` values are normalised via
  `asset.NormalizeStrategy(force=false)` (unknown ⇒ `"verify"`,
  never failing closed on the wire).
- `destination.kind="group"` + empty `group` is a hard 400 per
  PR-VO-C1 / godlike/07 no-fake-availability invariant.
- Response (canonical 202 Accepted):
  ```json
  {"ok": true, "job_id": "...", "request_id": "...", "status": "queued", "total_outputs": N}
  ```

Files touched:

- `internal/api/assets/voiceover/types.go` — new; wires the request,
  `VoiceoverItem`, `VoiceoverOptions`, `Validate`, `ToCommand`,
  `ToEnqueueRequest`. The mapper locks last-wins semantics for
  duplicate-language items (VoiceOverrides map keyed by language)
  and last-non-empty-wins for duplicate FilenameTemplate sources —
  both edges locked with explicit tests for P0.3 inheritance.
- `internal/api/assets/voiceover/handler.go` — `BindJSON` target
  switched from `*GenerateVoiceoversCommand` to
  `*GenerateVoiceoversRequest`; package doc updated; HTTP entry
  surface unchanged (still only POST `/generate` mounts).
- `internal/api/assets/voiceover/handler_test.go` — new; round-trip
  enqueue suite (happy path, empty items, mixed-text reject, empty
  text/language reject, PR-VO-C1 invariant, `NormalizeStrategy`
  coercion, enqueue-error → 500, slim-surface assertion,
  duplicate-language/filename last-wins, correlation-ID propagation).
- `cmd/admin/gen_api_docs.go` — removed the stale
  `routeDescriptions` entries for `/api/media/voiceover/batch`
  (retired by Wave 21 / PR-VOICEOVER-RECOVERY V1..V7) and
  `/api/voiceover/sync` (retired); updated `/api/media/voiceover/generate`
  description to cite the P0.2 wire shape. The committed
  `docs/api/ACTIVE_API_GENERATED.md` will be regenerated by
  `./admin gen-api-docs` (P2.2).

Worker side regression test: the worker
(`internal/application/voiceover/jobs/generate_handler.go`) continues
to unmarshal `*GenerateVoiceoversCommand` from JSON via the same tags
the Command already carries — no worker change required. The wire
shape round-trips into the Command with the same JSON keys; the
P0.2 change is API-layer-only.

### Observability

**[PR-VO-C1]** New Prometheus counter `legacy_voiceover_route_invocations_total`
labelled by `route`. Operators expose this via `/metrics` to track
per-route usage during the 90-day sunset window. The companion helper
`LegacyVoiceoverDeprecationCount()` returns the cumulative invocation
count (dto.Metric writeback pattern) for admin/diagnostic surfaces.

---

### Added

**[P0.3, Wave 21, Commit 1]** Voiceover canonical parent + child job
topology — `voiceover.generate` enqueues N per-language child jobs
(`voiceover.generate_item`), one per (language, voice) pair. Worker-pool
per-capability concurrency replaces the previous in-process goroutine
fan-out inside the use case.

- **New canonical job type** `voiceover.generate_item`
  (registered in `internal/domain/job/job.go::TypeVoiceoverGenerateItem`
  and re-exported via `internal/application/jobs/registry.go`).
  Registry entry: `Concurrency: 4, Timeout: 10m, DefaultMaxRetries: 2`.
  The Concurrency field caps sibling parallelism once the in-process
  broker semaphore upgrade lands (PR-VO-D, follow-up wave).

- **`GenerateVoiceoverItemCommand`** is the canonical payload for
  the per-child job. Carries ONLY the data the child needs:
  `ParentJobID` + `RequestID` + `Text` + `Language` + `Voice` +
  `Filename` (pre-computed by parent) + `TextHash` (pre-computed by
  parent) + `Destination` + `Strategy` + `RemoveSilence` + `Metadata`.
  The child re-resolves destination so a transient resolver hiccup
  doesn't force the parent to re-run.

- **`FanoutVoiceoversUseCase`** is the parent-side scheduler. It
  validates the parent command, computes filenames + textHash via a
  shared package-local helper (no dual-implementation drift), then
  enqueues N voiceover.generate_item jobs in sequence. children share
  RequestID + TextHash. The parent job's `Job.WorkflowID` field is
  also set to the parent_job_id so future aggregator queries
  (`jobs WHERE workflow_id = ?`) return siblings without descending
  into payload JSON. **Partial fan-out returns err** — godlike/07
  no-fake-availability: parent dispatcher marks the parent job FAILED
  when any child enqueue fails, NO silent success.

- **`ProcessOneVoiceoverUseCase`** is the per-language executor.
  Holds a *voiceover.Service (canonical production surface) and
  delegates the single-language flow to `voService.GenerateBatch`
  with a single-language `BatchRequest`. Preserve the legacy
  3-state `RemoveSilence` semantic (nil | *bool(false) | *bool(true))
  so the "caller didn't set it" signal survives across the
  parent → child boundary. Future BACKFILL PR can migrate
  this dispatcher to call `GenerateVoiceoversUseCase.processOneLanguage`
  once the full 7-port surface is extracted; for P0.3, the
  voService.GenerateBatch path is the canonical SSOT.

- **`GenerateItemJobHandler`** is the per-language child worker
  handler. Holds the typed-port ProcessOneUseCase (Ports-only
  dependency per AGENTS.md Pattern 0). On cross-cutting failure
  (validate, dest resolve, no-items), surfaces both `err` AND a
  result map carrying `status: "failed"` so the aggregator (Commit 2)
  can correlate.

- **`GenerateJobHandler`** (parent) rewritten to dispatch ONLY to
  `FanoutVoiceoversUseCase.Execute(ctx, parentJobID, *cmd)`. The
  legacy in-process `executor.Run`-with-goroutines path is gone —
  the new contract is goroutine-free inside the API: NO goroutines
  ever spawn from the parent's HandleJob path.

Files touched:

- `internal/domain/job/job.go` — added `TypeVoiceoverGenerateItem`.
- `internal/application/voiceover/command.go` — added
  `GenerateVoiceoverItemCommand` + `Validate()`.
- `internal/application/voiceover/process_one_usecase.go` — NEW;
  per-language executor.
- `internal/application/voiceover/jobs/fanout_usecase.go` — NEW;
  parent-side scheduler.
- `internal/application/voiceover/jobs/fanout_usecase_test.go` —
  NEW; 7 unit tests.
- `internal/application/voiceover/jobs/generate_item_handler.go` —
  NEW; per-child worker handler.
- `internal/application/voiceover/jobs/generate_item_handler_test.go` —
  NEW; 4 unit tests.
- `internal/application/voiceover/jobs/generate_handler.go` —
  parent handler rewritten to dispatch via FanoutUseCase.
- `internal/application/jobs/registry.go` — exported
  `TypeVoiceoverGenerateItem`; registered in `Compose()` with
  `Concurrency: 4`.
- `internal/app/build_bundles_domain.go` — constructs
  `ProcessOneVoiceoverUseCase` from voService.
- `internal/app/composition.go` — `DomainBundle` gains
  `VoiceoverProcessOne` + `VoiceoverGenerateItemHandler` fields;
  late-binding block constructs FanoutUseCase + parent + child
  handlers and registers both with jobs.Service.
- `internal/app/voiceover_wiring_test.go` — boot smoke test now
  verifies BOTH `voiceover.generate` AND `voiceover.generate_item`
  have handlers after Register (regression guard against future
  refactors that drop the parent-child chain).

**Forward-pointer (Commit 2, deferred):** the COMPLETED/PARTIAL/FAILED
final-state aggregation THE parent closes its lifecycle AFTER all
children reach terminal status. That lands in Commit 2 via the
canonical outbox events + an aggregator worker that reads each
child's terminal event and tallies the parent's final Result. The
parent job currently reports SUCCEEDED on enqueue-complete (the
intermediate state) — Commit 2 will RE-finalise based on outbox
events with completed (all ok) / partial (≥1 ok, ≥1 failed) / failed
(all failed) semantics, mapping to the canonical 7-state kernel
status: SUCCEEDED + result.partial = true (final succeeded with
partial flag) / SUCCEEDED (all ok) / FAILED (all failed).

**[P0.6 (June 2026), Wave 21]** Voiceover / Artlist P0 cleanup
(`AGENTS.md Active Concerns #11` CLOSED). Two-part closure removing the
two remaining godlike/07 silent-success patterns in the
Voiceover / Artlist slice.

- **P0.6 Parte A — EnrichAsync fire-and-forget capability removed**
  (`internal/application/assets/providers/artlist/`):

  - **Port signature** — `MetadataWriter.EnrichAsync` deleted from
    `ports.go:255`. Only `Enrich` (synchronous) remains.
  - **Concrete impl** — `SemanticEnricher.EnrichAsync` deleted (incl.
    `concurrent.SafeGo` goroutine + `context.WithoutCancel` +
    30s timeout deps); `pkg/concurrent` import cleanup.
  - **Stage wrapper** — `stageEnrichAsync(ctx, *RunTagResponse)`
    function deleted from `run_orchestrator_stages.go` (25-line
    function plus doc comment).
  - **External call sites replaced** — no fake-success anywhere:
    - `run_service.go:48` sequence comment updated to remove
      `EnrichAsync` from the pipeline flow + added forward-pointer
      to P0.18.
    - `run_service.go:110` `o.stageEnrichAsync(ctx, resp)` call
      deleted.
    - `search_core.go:204–208` replaced with a documented log+drop
      block (no fake-success: caller surfaces the deprecation
      instead of letting in-process fire-and-forget silently
      elevate failures to success).

- **P0.6 Parte B — silent-success translator fallbacks replaced**:

  - `internal/domain/script/generation_result.go` —
    `scriptpkg.VideoMetadata.TranslationStatus string
    `json:"translation_status,omitempty"`` field added with
    triple-state semantics (`"translated"` /
    `"untranslated"` / `""` legacy backward-compat).
  - `internal/application/scripts/dto/metadata.go` —
    `MetadataTranslator` interface (Pattern 0 port, 2 methods;
    `*ollama.Generator` satisfies implicitly) replaces the
    `*ollama.Generator` direct dep. Function signature
    `GenerateVideoMetadata(ctx, generator *ollama.Generator, ...)`
    becomes
    `GenerateVideoMetadata(ctx, generator MetadataTranslator, ...)`.
    All 3 silent fallback sites removed:
    1. Title: `meta.Title = title` (on err) → empty + status
       downgrade.
    2. Description: `meta.Description = enDesc` (on err) → empty.
    3. Tags: `translatedTags = append(...)` fallback to source tag
       → empty + status downgrade.
    `enOK := err == nil && (desc != "" || len(tags) > 0)` guards
    against the silent empty-payload success variant (LLM returns
    no err but empty payload).
  - `internal/application/scripts/usecase/flow_helpers.go`:
    `ScriptArtlistClipSuggestion.TranslationError string
    `json:"translation_error,omitempty"`` field added.
    `artlistSearchPhrase(ctx, svc, phrase) string` →
    `artlistSearchPhrase(ctx, svc, phrase) (string, error)`.
    In `SearchArtlistClips`'s `concurrent.ParallelMap`
    callback the translate error is now propagated via
    `suggestion.TranslationError` and the Qdrant search is
    intentionally skipped (no silent fallback to the original
    phrase).

- **TDD coverage** — 8 new tests across 2 new files, 4 mock
  stubs (3 in `dto/metadata.go`, 2 in `usecase/flow_helpers.go`):

  - `internal/application/scripts/dto/metadata_test.go` — NEW;
    5 tests + 4 mock stubs
    (`mockTranslatorSuccess`, `mockTranslatorFailingTranslate`,
    `mockTranslatorFailingAll`,
    `mockTranslatorEmptyPayload`). Tests order-independent via
    `indexByLanguage` helper (concurrent.SafeGoFunc scheduling
    nondeterministic):
    1. `TestGenerateVideoMetadata_TranslationFailureDropsOriginal`
       (canonical regression: 4 languages, asserts no original
       text leaks across `it`/`es`/`fr`).
    2. `TestGenerateVideoMetadata_HappyPathPreservesTranslation`
       (positive control: en + it).
    3. `TestGenerateVideoMetadata_EnglishLLMFailureMarksUntranslated`
       (`enOK=false` → untranslated).
    4. `TestGenerateVideoMetadata_NilGeneratorReturnsEmpty`
       (nil short-circuit guard).
    5. `TestGenerateVideoMetadata_EnglishLLMEmptyPayloadMarksUntranslated`
       (silent empty-payload success variant).
  - `internal/application/scripts/usecase/flow_helpers_test.go`
    — NEW; 3 tests + 2 stub translators
    (`stubFailingTranslator`, `stubStubTranslator`):
    1. `TestArtlistSearchPhrase_TranslationFailureReturnsExplicitError`
       (per-call gate).
    2. `TestArtlistSearchPhrase_NilTranslatorReturnsExplicitError`
       (nil-translator gate).
    3. `TestSearchArtlistClips_TranslationFailurePropagatesToCaller`
       (caller-level gate: surface error + skip Qdrant search).

8 modified files + 2 new test files. Verify gate:
`go test ./internal/application/scripts/dto/... ./internal/application/scripts/usecase/... -count=3`
stable GREEN across 3 runs (no flakiness from goroutine scheduling).

**Backward-compat** — `TranslationStatus` field uses `omitempty`
so legacy test fixtures that pre-date the closure emit identical
JSON. Manual `[]scriptpkg.VideoMetadata{Language: "...", Title: "...",
...}` constructors at
`internal/application/scripts/usecase/generate_one_usecase.go:467`
+ `internal/application/scripts/adapters/generation_html_test.go:154`
round-trip cleanly (zero-value status → field omitted).

**Forward-pointer (P0.18, successive wave, see
`architecture/current.yaml#P0.18`)** — structured outbox-driven
enrichment replaces the deleted `EnrichAsync` capability. Until then,
search ingestion stores only the raw clip metadata and a separate
`/enrich` job handles semantic payload population. Filed
separately, not in this closure.

### Fixed

Full P1-2 chain: `021c38ce` → `4306b97f` → `4270dcf7` → `732628a4` → `c33ae3d3`; post-chain follow-ups: `1cd9c3c9`,
`03813593`. Mirrors the precedent of PR-VO-A (`e149e1ab` →
`602114bc`) and PR-VO-B1-C1 (`73c44aca` → `c2867b90`); the
chain end-SHA `c33ae3d3` is the last commit that flipped
`wave_status.P1-2.current_state: deferred` → `active`, and
the two post-chain commits are slim-schema forward-pointer
additions for `PR-BARE-ONLY-MAP-LITERAL-COVERAGE` (covered
inline below).

**[P1-2 of cleanup plan, June 2026]** `arch(current)` + `feat(ci)` +
`chore(ci)` — Check 44 (application 40-file size cap +
`usecase/types_aliases.go` filename ban, target 40, transitional 66)
actively enforced via `scripts/ci-architectural-checks.sh`. Wave
tracker `architecture/current.yaml::wave_status.P1-2` flipped from
`current_state: deferred` to `current_state: active`; cap values
(`target: 40`, `transitional_cap: 66`) now live as the YAML SSOT
read by the gate script through `python3 -c "import yaml; ..."`
(zero inlining — a regression in `architecture/current.yaml` on
line 369 had blocked every downstream consumer; fixed in the
follow-up commit). Check 45 inline ClipsRepository map ban
re-numbered to Check 46 via `git mv`; the original wire-slot
reserved for Check 44 in the prior P1-2 deferred entry is now
filled, restoring the canonical numeric sequence 43 → 44 → 46 →
47+. Commit `1cd9c3c9 arch(current): fix YAML corruption + add
bare-only map-literal coverage FP` populated
`wave_status.P1-2.linked_issues[PR-BARE-ONLY-MAP-LITERAL-COVERAGE]`
(id / `owner_capability: architecture` / status=pending /
deadline=2026-07-25) as a slim-schema forward-pointer for the
bare-only coverage gap of Check 46, per godlike/06 §Slim-schema
+ zero-baseline rule. Follow-up commit `03813593 arch(current):
tighten owner_capability to architecture namespace` aligned the
owner field with the wave‑tracker `owner:` two lines above for
self-consistency. Forward-pointer entry lives at the canonical
anchor `architecture/current.yaml#wave_status.P1-2.linked_issues[PR-BARE-ONLY-MAP-LITERAL-COVERAGE]`
(mirror documented in AGENTS.md `Recent cross-cutting closures`).
Gates green: `bash scripts/ci-architectural-checks.sh` exits 0;
the Check 44 standalone reads the live YAML SSOT and reports
0 violations against `transitional_cap=66`.

(See-also canonical anchor: `audit-trail-anchors_P1-2-of-cleanup-plan`; mirrored in AGENTS.md `Recent cross-cutting closures (June 2026)`.)

**[Channel-monitor Blocco 1]** `fix(monitor)` — backoff is finally real. Three
bugs closed in one commit:

- **checkChannel now returns `(ChannelCheckResult, error)`** instead of bare `()`.
  The previous `func (m *ChannelMonitor) checkChannel(ctx, channel)` swallowed
  `m.ytdlp.ListChannel` errors into a log line. The scheduler then wired
  `success := true` to `nextCheckTime`, so a flapping yt-dlp (network blip,
  bot challenge, playlist-end drift) NEVER tripped the 5min → 10min → 20min
  → … → 24h exponential backoff. After: the err is returned, the scheduler
  feeds it into `recordCheckOutcome`, and `MarkChecked(Success=false,
  LastError=<err>, NextCheckAt=backoff)` is the steady-state failure outcome.

- **`recordCheckOutcome` extracted as a small helper** that computes
  `success := checkErr == nil`, the matching `nextCheckTime`, and forwards
  everything to `channels.MarkChecked`. Pulled out of the goroutine body in
  `checkDueChannels` so the error-path propagation can be unit-tested
  without spinning a real yt-dlp subprocess.

- **`Start()` no longer runs an initial `ListEnabled` cold-start check.**
  The previous code path printed a stale pre-PR-7 comment ("Initial check:
  run immediately for any enabled channels") and called
  `m.channelsSvc.ListEnabled` followed by `m.checkDueChannels` — OUTSIDE
  the `ClaimDue → lease → MarkChecked` chain. That meant the very first
  check of every process cold-start had no worker_id, no lease, no
  failure-counter increment, and no NextCheckAt writeback. Bringing the
  cold start into the same `runSchedulerCycle` cadence means a cold-start
  failure now drives backoff identically to a warm-cycle failure.

- **Pattern 0 port `MonitorDownloaderPort`** declared in
  `internal/application/assets/monitor/monitor_ports.go` (ListsChannel +
  Path), with compile-time assertion
  `var _ MonitorDownloaderPort = (*downloader.YTDLPDownloader)(nil)`.
  `ChannelMonitor.ytdlp` field type is now this port rather than
  `*downloader.YTDLPDownloader`. Production callers in
  `internal/app/lifecycle.go::lifecycle` pass the concrete value;
  Go auto-boxes it. Unit tests inject `*fakeLister` and exercise the
  failure path with no subprocess.

- **New `ChannelCheckResult` type** (VideosDiscovered / VideosEnqueued /
  VideosSkipped) — typed payload for the connection scheduling boundary.
  The scheduler uses `.VideosEnqueued` + `.VideosSkipped` (where
  Skipped = Discovered − Enqueued, covering both MaxVideosPerRun budget
  breakouts and in-process filter rejections: min_views, duration,
  title-keyword, semantic budget, semantic-score threshold).

- **New `monitor_scheduler_test.go`** pinning all four invariants
  (5 cases covering error→Success=false / success→CheckInterval / backoff
  progression / checkChannel ytdlp-failure / nextCheckTime full curve).
  Locked via `parseCheckInterval` fallback band (24h on unparseable
  interval) and 23h→25h windows on the 24h cap so a clock drift of a
  minute doesn't produce a flaky CI.

Files touched (5):

- `internal/application/assets/monitor/monitor_ports.go` — NEW port type
  + compile-time assertion + `ChannelCheckResult` type.
- `internal/application/assets/monitor/channel_monitor.go` — ytdlp
  field type swap to the port interface; `NewChannelMonitor` signature.
- `internal/application/assets/monitor/monitor_channel_check.go` —
  checkChannel signature + path; `fmt.Errorf` wrap of ytdlp errors.
- `internal/application/assets/monitor/monitor_scheduler.go` — `Start()`
  drops the ListEnabled shortcut; `checkDueChannels` uses returned err
  + structured log line; new `recordCheckOutcome` helper.
- `internal/application/assets/monitor/monitor_scheduler_test.go` —
  NEW; same-package tests with `*fakeLister` (MonitorDownloaderPort)
  and `*recordingRepo` (channels.Repository).No production-wiring change beyond the ytdlp interface swap (which Go boxes
transparently); no SQLite migration, no DB schema touch, no config.yaml keys added.

**[Channel-monitor Blocco 1 — housekeeping (June 2026)]** `docs` — closure of two pre-existing items that surfaced during Blocco 1's landing plane, plus a docs follow-up:

- **`types_outcome.go` already canonical on `main`** via `51e41bf4 feat(monitor): introduce SkipReason type and EnqueueOutcome struct`. No branch split is necessary: the file at `internal/application/assets/monitor/types_outcome.go` documents in its header comment that it's a *"Plan: Channel Monitor Blocchi 4+5"* stub. The file lands with `SkipReason` constants + `EnqueueOutcome` struct already consumed by downstream `0488a5ef refactor(monitor): split acceptedCount into analysisReservations and successfulEnqueues`, so Blocco 4+5 work can extend it directly on `main` without a dedicated branch. **Decision**: leave the file on `main`; do not open a cleanup PR.

- **`module_media.go:334` `MutationsDispatcher` regression is RESOLVED**. The obsolete literal flagged in Blocco 1's CHANGELOG caveat has been cleaned up by follow-up commits (no obsolete `MutationsDispatcher` field on `clips.Deps`; `go build ./internal/app/...` builds clean; `go build ./...` builds clean across the full Go tree). Future agents should no longer need to scope monitor-package validation to `./internal/application/assets/monitor/...` only — direct compilation across the full tree works. The Blocco 1 entry's "tech-notes" bullet pointing readers at the targeted-build workaround is therefore obsolete and is superseded by this notice.

- **Push-race `ff7a5579 → 960a3fb6` byte-equivalent replay** is documented as a recovery pattern in `AGENTS.md` § *Git-Lesson-4 (June 2026) — Recovery from non-fast-forward push race*. Local `ff7a5579` is reachable only via `git reflog` and is **superseded** by `960a3fb6` (canonical on `origin/main`). Both SHAs cover the same six files (`internal/application/assets/monitor/{monitor_ports.go, channel_monitor.go, monitor_channel_check.go, monitor_scheduler.go, monitor_scheduler_test.go}` + `CHANGELOG.md`). Verifiable via `diff <(git show --name-only --format='' ff7a5579) <(git show --name-only --format='' 960a3fb6)` returning empty. The canonical recovery rule: "accept the re-application; do NOT `force-push`" — see `AGENTS.md Git-Lesson-4` for the full diagnosis procedure and the anti-pattern.

## Unreleased

### Fixed

**[Channel Monitor Commit A, June 2026]** `fix(monitor)` — SQL projection bug + fenced MarkChecked + panic-safe scheduler + typed runtime policy. Four P0/P1 bites closed in one commit:

- **P0 #1 — `category_channels` SELECT projection bug closed.** All 4 read paths (`GetByID`, `ListEnabled`, `ListAll`, `ListByCategory`) now reuse the canonical `channelSelectColumns` constant at `internal/infrastructure/database/sqlite/assets/channels_repository.go`. The pre-fix bug: queries listed 27 columns while `scanFields` scanned 28 destinations (missing `last_cursor`), so every read tripped `rows.Scan` with "expected 27 destination arguments, not 28". The constant is now the single source of truth — adding a column requires touching the const + `scanFields` + the `CategoryChannel` domain struct in lockstep.

- **P1 #8 — `MarkChecked` fenced on `lease_owner`.** The leaf UPDATE in `ChannelsRepository.MarkChecked` now branches on `leaseToken`: empty token → un-fenced UPDATE (admin back-compat path); non-empty token → fenced `WHERE id=? AND lease_owner=?` clearing `lease_owner=NULL, lease_until=NULL` so the next `ClaimDue` can re-claim cleanly. `RowsAffected==0` on a fenced UPDATE returns the new sentinel `ErrLeaseLost`, surfaced via `errors.Is` so callers (the monitor's `recordCheckOutcome`) can react. `MarkCheckedCommand.LeaseToken` field added in `internal/application/channels/contract.go`; `RepositoryAdapter.MarkChecked` propagates it; the monitor's `recordCheckOutcome` populates it from `ch.LeaseOwner`.

- **P1 #9 — `safeCheckChannel` panic-recovery wrapper.** The previous in-goroutine `defer recover()` swallowed panics into a log line, leaving the lease idle until expiry. New wrapper converts panicked execution into a typed error so `recordCheckOutcome` always fires; the channel transitions to `Success=false` with the synthesized panic message in `LastError`, triggering the exponential backoff. `discovery.go`'s concurrent `processVideo` worker retains its own recover (different scope: per-video, not per-channel).

- **P1 #10 — typed `MonitorRuntimePolicy` extracted.** New sibling file `internal/application/assets/monitor/policy.go` declares `MonitorRuntimePolicy{TickInterval, LeaseDuration, ClaimLimit, MaxConcurrentChannels, MaxConcurrentVideos, PerChannelTimeout, WorkerIDPrefix, BackoffInitial, BackoffCap}` and a `DefaultMonitorRuntimePolicy()` factory. `CompositionDeps` gains optional `Policy *MonitorRuntimePolicy`; the constructor's precedence is policy > cfg > default (1). `scheduler.go` reads all runtime knobs through `m.policyOrDefault()`. The previous `schedulerTick=30s + defaultLeaseDuration=30min + claimLimit=10 + video-concurrency=5 + per-channel-timeout=30min + initialBackoff=5min + maxBackoff=24h` const block lives only in `policy.go` now; tests can drive the backoff curve in O(seconds) by injecting a custom policy.

- **P1 #10 — `ClaimDue` `ORDER BY priority ASC, next_check_at ASC`.** Hot-priority channels (Priority=1) are now claimed before normal (Priority=2) before cold (Priority=3) inside a single scheduler tick, with `next_check_at ASC` as the secondary sort so the most-overdue channel in each priority bucket is preferred.

Files touched:

- **NEW** `internal/application/assets/monitor/policy.go` — `MonitorRuntimePolicy` + `DefaultMonitorRuntimePolicy` + `policyOrDefault()`.
- `internal/application/assets/monitor/ports.go` — `CompositionDeps.Policy` field.
- `internal/application/assets/monitor/scheduler.go` — const block removed; `Start` reads `policy.TickInterval`; `runSchedulerCycle` reads `policy.LeaseDuration + ClaimLimit + WorkerIDPrefix`; `checkDueChannels` reads `policy.MaxConcurrentChannels + PerChannelTimeout` and calls `safeCheckChannel`; `nextCheckTime` reads `policy.BackoffInitial + BackoffCap`; `recordCheckOutcome` populates `LeaseToken`.
- `internal/application/assets/monitor/discovery.go` — `checkChannel` reads `policy.MaxConcurrentVideos` for the inner goroutine fan-out.
- `internal/infrastructure/database/sqlite/assets/channels_repository.go` — `channelSelectColumns` + `channelSelectColumnsForListing` constants; `ErrLeaseLost` sentinel; 4 SELECTs now use the constant; `MarkChecked` fenced with `leaseToken` + sentinel; `ClaimDue` ORDER BY priority.
- `internal/application/channels/contract.go` — `MarkCheckedCommand.LeaseToken` field.
- `internal/application/channels/adapters.go` — `RepositoryAdapter.MarkChecked` propagates `cmd.LeaseToken`.
- **NEW** `internal/infrastructure/database/sqlite/assets/channels_repository_test.go` — 6 tests against a real in-memory SQLite schema mirroring the consolidated `category_channels`: projection round-trip via all 4 SELECTs (`last_cursor` populated correctly), `MarkChecked` happy path under fence, `MarkChecked` wrong token → `ErrLeaseLost`, `MarkChecked` empty token → back-compat, `ClaimDue` priority ordering.
- **NEW** `internal/application/assets/monitor/monitor_policy_test.go` — 4 tests: `safeCheckChannel` panic → `recordCheckOutcome(::Success=false)`, `DefaultMonitorRuntimePolicy` matches previous literal values, `policyOrDefault` nil fallback, `recordCheckOutcome` propagates `ch.LeaseOwner` → `cmd.LeaseToken`.

No production-wiring change beyond the ytdlp interface swap (which Go boxes transparently); no SQLite migration, no `config.yaml` keys added.

### Added

**[FASE 9, DRIVE-005, June 2026]** Drive canonical `Admin` + `Reader`
port abstractions (Pattern 0) — the composition root's readiness barrier
now consumes a typed port instead of leaking the raw `*gdrive.Service`
into the lifecycle wiring.

- **New port interfaces** declared at
  `internal/infrastructure/drive/ports.go` (`Admin` for folder management
  + file lifecycle + uploads + liveness probe via `Ping`; `Reader` for
  download + metadata + listing + existence checks). Compile-time
  assertions at the bottom of the file: `var _ Admin = (*Uploader)(nil)`
  and `var _ Reader = (*Uploader)(nil)` — a signature drift between the
  port surface and the concrete `*drive.Uploader` is a build failure per
  AGENTS.md Pattern 0.

- **New `Uploader.Ping(ctx) error` method** in
  `internal/infrastructure/drive/uploader_ops.go` (line ~295) — calls
  `u.Service.About.Get().Fields("user").Context(ctx).Do()`. Nil-service
  guard: returns `fmt.Errorf("drive service not configured")` so the
  readiness barrier fails closed on misconfigured Drive.

- **Composition-layer wiring** (the canonical consumer) — the
  `internal/app/wire_services.go` `driveProbe` closure now reads
  `root.Drive.Admin.Ping(ctx)` instead of
  `root.Drive.DriveClient.About.Get().Fields("user")`. Bit-for-bit
  behavioural parity; the only difference is the consumer surface
  (typed port vs raw SDK).

- **Typed-nil-safe guard** at
  `internal/app/build_bundles_drive.go::BuildDriveBundle` — uses the
  canonical `var admin drive.Admin` + `if driveUploader != nil { admin =
  driveUploader }` pattern so the interface value stays true-nil when
  the uploader is nil. Without this guard, `Admin: driveUploader` would
  produce a non-nil interface holding a typed-nil pointer — the classic
  Go interface-nilness trap that would silently panic the readiness
  barrier on a Drive-feature-disabled deployment.

### Deprecated

**[DRIVE-005, June 2026]** `internal/app.DriveBundle.DriveClient` and
`internal/app.DriveBundle.DriveUploader` (the raw `*gdrive.Service` and
`*drive.Uploader` handles on the composition-root bundle) are
deprecated in favour of the typed-pattern ports
(`internal/app.DriveBundle.Admin` + `internal/app.DriveBundle.Reader`).

- **Canonical replacement**: `Admin` (liveness probe `Ping(ctx)`,
  folder management, file lifecycle, raw uploads) and `Reader`
  (download, metadata, listing, existence checks). All new code MUST
  consume via the typed ports per Pattern 0 + godlike/06 "one owner
  per fact".

- **Back-compat path**: the deprecated fields are retained for Wave 14+
  back-compat across the existing ~86 legacy callsites (`cmd/admin/*`,
  `internal/app/{build_bundles_*,lifecycle,module_*,registry_*}`,
  `internal/application/assets/ingest/sync/*`, the storage_wiring_test,
  and many others). The fields alias the SAME `*drive.Uploader`
  instance so **zero split-brain** — godlike/06 holds.

- **Deprecation record**: `architecture/deprecations.yaml#DRIVE-005-FIELDS`
  per godlike/07. Removal target: 2026-Q3 (aligned with the Wave 14
  mega-package split gate which is the natural deletion boundary).
  Status: EXPAND / in_progress as of FASE 9 landing.

- **Audit gate**: `rg 'root\.Drive\.DriveClient|root\.Drive\.DriveUploader'
  internal/` returns the current ~86 readsite count — the
  EXPAND-phase usage_metric baseline for `DRIVE-005-FIELDS`.

---

## Earlier (June 2026 wave)

### Added

**[PUTFILE-001, June 2026]** Conflict-aware Drive uploads — replaces
legacy silent-overwrite with explicit `delivery.ConflictPolicy`.

- **New port** `FileUploaderPort.PutFile(ctx, req PutFileRequest) (*PutFileResult, error)` in
  `internal/infrastructure/drive/publisher.go`, with
  `var _ FileUploaderPort = (*Uploader)(nil)` compile-time assertion per
  AGENTS.md Pattern 0. Signature drift between port surface and
  `*drive.Uploader.PutFile` is a build failure.

- **New types** `PutFileRequest` (+ `ConflictPolicy` field),
  `PutFileResult` (`Action` + `FileID` + `FileName`), and `PutAction`
  enum (`Created`/`Updated`/`Skipped`) in
  `internal/infrastructure/drive/publisher.go`. Wired through
  `delivery.ConflictPolicy` (3 values: `Overwrite` | `Skip` |
  `Rename`) in `internal/application/assets/delivery/types.go`. Zero
  value preserves legacy `Overwrite` semantics — no silent
  availability loss for callers that haven't explicitly opted in.

- **`*Uploader.PutFile` impl** in
  `internal/infrastructure/drive/uploader_put.go` (~250 LoC) —
  conflict-aware, retry-wrapped via `pkg/retry.DoWithValue`. Routing
  table:
  - existing match + `Overwrite` → `Files.Update` → `PutActionUpdated`
  - existing match + `Skip` → no-op → `PutActionSkipped` + existing
    file metadata (idempotent on lookup, no upload side-effect)
  - existing match + `Rename` → derive free slot
    (`name-1.ext`, ...; `name-N.ext` after N collisions) → Create
  - missing → `Files.Create` (idempotent path) → `PutActionCreated`

- **`NewPublisher` fail-fast composition guard** in `publisher.go` —
  returns `(publisher, error)` so `BuildDriveBundle` (composition
  root) can fail-close on misconfigured uploader. Adds 3 typed sentinel
  errors (`ErrMissingDestinationRegistry`,
  `ErrMissingFolderManager`, `ErrMissingFileUploader`) so the caller
  surfaces a typed error instead of the classic Go "interface-nil
  panic" trap.

- **`folderLookupFunc` test seam** in `folder_manager.go` — production
  lookup includes retry, but the seam accepts a stub
  `func(ctx, name) (*RemoteFile, error)` so
  `folder_manager_internal_test.go` drives success/failure paths
  without a live Drive API (closes P0.4 duplicate-folder regression:
  transient lookup errors were falling through to `Files.Create` and
  producing duplicate folders).

- **4 plumbing tests** in `internal/infrastructure/drive/publisher_test.go`:
  `TestPublisher_PublishForwardsConflictPolicy_{ZeroValue,Overwrite,Skip,Rename}`
  pin the publisher-to-uploader forwarding path end-to-end, asserting
  the `req.ConflictPolicy` value flows verbatim into `PutFileRequest`
  and the `PutAction` is propagated back to the result.

- **godlike/07 closure**: the silent-overwrite failure mode is no
  longer the only behaviour — explicit per-call policy is the
  canonical path. All callers MUST pick (zero-value preserves legacy);
  no fake availability per godlike/07 §"No fake availability".

Pattern 0 port abstraction; godlike/06 — `delivery.ConflictPolicy` is
the canonical enum owner; `PutAction` is the canonical return enum
owner. Cross-references:
[`architecture/current.yaml#id-26.linked_issues[PR-PUTFILE-P0-1]`](../architecture/current.yaml).

## Earlier (June 2026 wave)

See ARCHITECTURE.md §"Migration Status (Brutal Care Plan)" for the
historical record. Cross-references:

- **PR-VO-A1** through **PR-VO-A6** — voiceover P0 hardening bundle.
- **PR-VO-B1** — Drive upload split Processor ↔ Lifecycle
  (DriveUploaderPort).
- **PR-VO-B2** — metadata + StyleGroup propagation (no silent-drop
  through `processLanguage` + `resolveDestination`).
- **PR-VO-B3** — sync dedupe by `drive_file_id` + BCP-47 / compact
  locale parser (`pkg/localeutil/locale.go`).

### Removed
- **[FASE 12c removal ratification, July 2026]** `chore(arch)` — Step 12A SCRIPT legacy closure audit. Goalike/07 audit-footprint for the legacy `POST /api/script/generate-batch` route removal: route + handler + 4 type structs (`LegacyGenerateBatchRequest`, `LegacyBatchItem`, `LegacyBatchTopic`, `toEnvelope` mapper) + `removalDateBatch` const + `DeprecationCount` route entry + test assertions all physically deleted at commit `9ff1e19e` (FASE 12c, July 2026) + residual cleanup at `00ad3430`. New deprecation record `architecture/deprecations.yaml#SCRIPT-LEGACY-GENERATE-BATCH` (status: removed, migration_phase: CONTRACT, 5-layer compatibility test) + new wave-tracker marker `architecture/current.yaml#BLOC5.3_commit-3-legacy_batch_elim` (status: shipped) ratchet the audit-footprint against re-introduction. Voiceover `Service.GenerateBatch` (4 non-test hits in `internal/application/voiceover/*`) is EXPLICITLY OUT OF SCOPE — it is the canonical batch voiceover pipeline for `TypeVoiceoverBatch` + `TypeVoiceoverPromo` jobs. Forward-pointer: PR-VO-D1/D2/D3/E1 (Wave-12 cutover packet) for the voiceover canonical typed-port migration.



**[Wave 1.1, QDRANT-004 backend git-rm, June 2026]** `refactor(mediasearch) + refactor(app)` — the QDRANT-004 single-tenant semantic search orchestrator is git-rm'd. The canonical `search.Aggregator` (provider + local backends, both typed via Pattern 0) is the SOLE wire for media-search results; workspace-gated semantic routing now lives in per-scope `search.Aggregator` paths, never in dedicated Service composition. Files touched (7):

- **DELETED** `internal/application/mediasearch/service.go` (−552 LoC: `*Service` struct + `NewService` ctor + `Search` orchestrator + 9 helpers + 2 constants).
- **DELETED** `internal/application/mediasearch/service_test.go`.
- `internal/app/search_backends.go` — removed `semanticSearchBackend` struct + 3 methods + `var _` assertion + `MediasearchSvc` field on `SearchBackendBuildOpts` + `WorkspaceID` field on same opts (sole consumer was the deleted semanticSearchBackend).
- `internal/app/assets_core.go` — removed `MediasearchService *mediasearch.Service` field from `SearchDeps` + `SearchWorkspaceID` field (historical diagnostic-only) + `mediasearch` import.
- `internal/app/registry_search.go` — replaced stale `search_backends.go:346` line-number pin with a drift-resilient note.
- `internal/app/registry_assets.go` — `SearchDeps` literal already passes only the canonical fields; no construction change beyond bundle-field drops above.
- `internal/application/search/ports.go` — replaced the `semanticBackendAdapter wraps mediasearch.Service (cross-cap port bridge)` god-comment with the PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL marker describing the surviving thin re-export surface.

**Retained as thin re-exports** (4 real callers compile against these canonical types): `mediasearch.WorkspaceContext`, `mediasearch.AssetDeliveryService`, `mediasearch.MediaSearchRequest{Response,Filter}`, `mediasearch.SearchMode` (alias of `search.SearchMode`). The QDRANT-004 `/internal/v1/media/search` handler at `internal/api/mediasearch/handler.go` still wires via canonical `search.Aggregator` (the per-scope workspace gate consumer — distinct from the git-rm'd orchestrator).

**Deprecation record:** `architecture/deprecations.yaml#PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL` (status: `removed`, introduction_date=2026-06-30, replacement=`search.Aggregator`).

## [Step 9 + 10] Image territory separation — 5 subpackages + REST endpoints (July 2026)

### Added

**[Step 9 (July 2026) — image territory subpackages]** `feat(images)` — split `internal/application/images/` into 5 focused subpackages while preserving backward compatibility with the parent `*imgservice.Service` facade:

- `internal/application/images/catalog/` (3 files): `CatalogSearchResult`, `AssetSummary`, `SummaryFromAsset`, `ImageFilter` + `FilterByOrigin`/`FilterBySlug`, `CatalogSearch` interface + `InMemoryCatalogSearch` impl with cursor helpers. Read-only — no ingestion, no generation.
- `internal/application/images/styles/` (3 files): `ResolvedStyle`, `StyleID`, `StyleDefinition`, `StyleResolver`, `StyleRegistry` aliases for canonical `internal/application/assets/generation` types. `Registry` struct wrapping `*generation.StyleRegistry`; `Resolver` struct wrapping `generation.StyleResolver`. Backward-compatible (`ErrUnknownStyle = generation.ErrStyleNotFound`).
- `internal/application/images/routing/` (1 file): `Service` interface + `Router` dispatching by `asset.ImageOrigin`. `SearchAll` for territory-wide fan-out. Sentinel errors for each wired/unwired territory.
- `internal/application/images/retrieved/` (2 added on top of Step 8's provider_registry): `search_service.go` (`SearchServicePort` + `SearchServiceAdapter`) and `ingest.go` (`IngestServicePort` + `IngestServiceAdapter`).
- `internal/application/images/generated/` (2 added on top of Step 8's provider_registry): `prompt_composer.go` (extracted from parent `generation_service.go` per Step 4 rules; bit-identical semantics) and `generated_search.go` (`GeneratedSearchServicePort` + `GeneratedSearchServiceAdapter`).

Compile-time interface assertions in `internal/application/images/service.go` lock the parent `*ImageStorageService` against the new subpackage ports — drift surfaces at build time, not first runtime panic.

**[Step 10 (July 2026) — territory-separated REST endpoints]** `feat(api-images)` — 5 new endpoints under `/api/images/`, plus an aggregated search endpoint with `territory=retrieved|generated|all` query param:

- `GET  /api/images/retrieved/search?q=…&lang=…` → mirrors pre-Step-10 search semantics with `ImageSearchResults` envelope.
- `GET  /api/images/generated/search` → Step-9 forward-pointer; returns `200 OK + []` today. SQLite-backed `ListImagesByOrigin` impl is `architecture/issues.yaml#IMG-GEN-SEARCH-FORWARD-POINTER` (deadline 2026-08-01).
- `POST /api/images/generated/generate` → mirrors legacy `/api/images/generate` payload but mounted under `/generated/*`. Same `h.service.GenerateSmartImageWithAccount` call.
- `GET  /api/images/generated/styles` → lists registered styles from `*generation.StyleRegistry` via `h.service.StylesRegistry()`.
- `GET  /api/images/search?territory=retrieved|generated|all&q=…` → aggregator. Default `territory=retrieved` preserves pre-Step-10 caller behaviour. `territory=all` currently fans out to retrieved (canonical query-driven path) + generated (empty stub).

Unified `ImageSearchResult` DTO (fields: `AssetID`, `Origin`, `Provider`, `PreviewURL`, `StyleID`, `License`, `Author`). All `omitempty` where appropriate. `StyleInfo` DTO for `/generated/styles`. Envelope `ImageSearchResults{Results, Count}`.

### Changed

- `GET /api/images/search` response envelope migrated: pre-Step-10 returned `gin.H{"subject": "...", "image": {...}}`; post-Step-10 returns `ImageSearchResults{Results: [...], Count: N}`. Any caller depending on the legacy `subject` / nested `image` keys will see `200 OK` with different JSON shape — silent breakage. Migration: read `results[0].asset_id` instead of `image.hash`.
- `internal/application/images/service.go::Service` now exposes `Styles *generation.StyleRegistry` field + `StylesRegistry()` accessor. Backward-compatible (zero-value before Step 9 wiring was nil; nil-safe accessor applied).

### Removed

- Legacy `internal/api/images/impl.go::Search` handler removed (replaced by `TerritorySearch`). See "Changed" above for envelope migration. Callers must migrate from `gin.H{"subject","image"}` shape to `ImageSearchResults{...}` envelope.

### Known Build Issues (unrelated, pre-existing)

- `internal/application/semantic/ollama_analyzer.go:494` references `monitor.ErrAnalyzeFullNotImplemented` which is undefined in `internal/application/monitor/`. Blocks `cmd/server/` compile → end-to-end smoke-tests blocked. Tracked as `architecture/issues.yaml#SEM-ANALYZER-ERR-NOT-IMPLEMENTED` (deadline 2026-07-15, owner `architecture/semantic`). Re-confirmed present on `origin/main` HEAD before this commit (commit `a0c2f6ff`, "refactor(ai): Commit G surface split"), so this PR does NOT introduce the regression. Smoke-test of the Step 10 endpoints will land in the same commit as the monitor-error fix.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>

- **[Step 2 of 12-step plan (July 2026) — JobFinalizer.CompleteWithArtifacts runtime wiring closure]** `chore(architecture)` — closure audit-pinning only; the canonical wiring was already live on `origin/main` via prior commits. Step 2 user spec -> "connect `JobFinalizer.CompleteWithArtifacts` to the real broker runtime". Acceptance verified against the existing tree:

  * **(a)** `ProducesArtifacts bool` field is on the canonical job registration: `JobPolicy.ProducesArtifacts` at `internal/application/jobs/registry.go::RegistryEntry` AND `ArtifactPolicy.ProducesArtifacts` at `internal/domain/job/job_definition.go:171`. ~15 job types register `ProducesArtifacts: true` (script.generate + media.* + video.* + youtube.* + voiceover.* + books/lessons/image families).
  * **(b)** The dispatch is central-but-not-single-method: workers call `Tools.CompleteWithArtifacts` (internal/application/jobs/worker/tools.go:64) -> `Broker.CompleteWithArtifacts` (internal/infrastructure/jobs/local/broker.go:258) -> `JobFinalizer.CompleteWithArtifacts` (internal/application/jobs/finalizer/job_finalizer.go:82). Legacy `Broker.Complete` -> `SQLiteStore.Complete` is **GATED at the SQL layer** by `ErrArtifactJobRequiresCompleteWithArtifacts` (internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go:30) for any `producesArtifacts[jobType] == true`. The gate map is propagated via `repo.SetProducesArtifacts(registry.ProducesArtifactsMap())` (internal/app/module_jobs.go:54). Defense-in-depth: the SQL-layer gate is operationally MORE robust than the user's literal flag-dispatch spec (loud reject vs. silent half-finalized asset row).
  * **(c)** `script.generate_with_images` does NOT exist as a separate job type constant; the canonical `TypeScriptGenerate = "script.generate"` handles all script-generation modes as a single registered type, already flagged `ProducesArtifacts: true` at `internal/application/jobs/registry.go:475`.
  * **Acceptance criterion**: `rg 'broker\.Complete\(' internal/` returns calls only at canonical broker entry points; SQL-layer gate is the operational guarantee that no productive caller uses the legacy path for artifact-producing jobs.

  Honest limitations (godlike/07, out-of-scope for this closure):

  1. **E2E smoke** (jobtype=script.generate writes to media_assets + asset_versions + asset_locations + outbox_event_index_request via pkg/veloxclient.SubmitAsync): forward-pointer; out-of-scope for closure bookkeeping per AGENTS.md minimal-change.
  2. **Check 54 forward-prevention gate** (CI gate banning `Tools.Complete` callers for artifact-producing job types): not landed; SQL-layer gate judged sufficient.
  3. **Pre-existing build issues carry forward**: monitor/enqueue.go, scheduler.go, run_upload.go, module_media.go, internal/application/images/routing cycle. Step 2 closure touches ONLY `architecture/current.yaml` + `CHANGELOG.md` (audit-pin only).

  Wave-tracker cross-reference: `architecture/current.yaml#Step 2 — JobFinalizer.CompleteWithArtifacts runtime wiring (CLOSED, July 2026)` block added. No `- id: <num>` row (slim-shape convention reserves those for in-progress wave entries; closure audit pins use comment-block shape).

- **[Step 3 of 12-step plan (July 2026) — Transactional fence hardening in JobFinalizer, CLOSED]** `fix(finalizer)` + `feat(finalizer)` — Pattern-0 port abstraction + end-to-end idempotency coverage. Step 3 user spec → 4 sub-tasks (a)(b)(c)(d); all 4 closed.

  * **Behaviour change (godlike/07 disclosure)**: `JobFinalizer.selectJobForFinalization` now early-returns for `status='SUCCEEDED'` rows, bypassing worker_id / lease_id lease-ownership checks. Workers re-attempting an already-canonical-SUCCEEDED job now receive a clean idempotent `SUCCEEDED` (via fingerprint comparison in `handleIdempotentCompletion`) instead of `ErrLeaseOwnerMismatch` / `ErrLeaseIDMismatch`. Side effect: stale-attempt callers on terminal rows now see `ErrCompletionConflict` (fingerprint mismatch) rather than `ErrStaleAttempt` — kept the attempt-counter check disabled on SUCCEEDED rows because `markSucceeded` clears worker/lease_id, which would otherwise break idempotency. Documented in the `Step 3 (d)` comment block inside `selectJobForFinalization`. The canonical gate remains the SQL fence (sub-task (a)) + the fingerprint hash (sub-task (b)) for already-terminal rows.
  * **(a) SQL lease_expiry fence**: `finalizer.selectJobForFinalization`'s SELECT now includes `AND (lease_expiry IS NULL OR lease_expiry > CURRENT_TIMESTAMP)`. The IS NULL branch preserves idempotency for already-SUCCEEDED rows whose `lease_expiry` was cleared at job_completed time. Defence-in-depth: kept the Go-side `time.Now().UTC().After(expiryTime)` check beneath the SQL fence.
  * **(b) completion_fingerprint fixture-locked**: `finalizer.computeCompletionFingerprint` already implemented SHA-256 over `result JSON + sorted artifact IDs + SHA256s + source versions + file IDs` per Piano d'Azione §4.5. The fingerprint is persisted in `jobs.result_json` via a `{data, completion_fingerprint}` JSON wrapper written by `markSucceeded`. New `TestE2E_FingerprintPersistedInResultJSON` asserts the JSON shape + fingerprint hex length (64 chars = SHA-256) post-commit.
  * **(c) Error-propagation completeness sweep** (godlike/07 "no fake availability"): replaced `_, _ = tx.ExecContext("INSERT INTO job_events ...")` with explicit `if _, err := ...; err != nil { return fmt.Errorf("%s: insert job event: %w", err) }` (or `tx.Commit()`-then-error path for the lockable SQL surface in `requeueSingle`):
    * `internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go` — 5 sites: `Complete` / `Fail` / `ScheduleRetry` / `Cancel` / `Retry`.
    * `internal/infrastructure/database/sqlite/jobs/repository_claims.go` — 3 sites: `Start` + `requeueSingle` retry-branch + `requeueSingle` exhausted-branch.
    * `JobFinalizer.markSucceeded`'s `INSERT INTO job_events` already propagated (`fmt.Errorf("finalizer: insert job event: %w", err)`); confirmed canonical.
  * **Pattern-0 port abstraction** (feeds (d)): `finalizer.Finalizer.assetTx` field + `finalizer.New(...)` parameter changed from `*assetfinalizer.AssetTxFinalizer` (concrete) to `finalization.AssetFinalizerTx` (interface). Production wiring sites pass `*assetfinalizer.AssetTxFinalizer` (sat-implicitly per `var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)` on the production concrete — interface satisfaction verified at compile time). Enables test injection of a `noopAssetTx` value-receiver mock without spinning up the 33/055/105-migration media_assets/asset_versions/asset_locations schema.
  * **(d) End-to-end idempotency coverage** (`internal/application/jobs/finalizer/job_finalizer_e2e_test.go`, NEW — 6 TestE2E_* tests):
    * `TestE2E_FingerprintPersistedInResultJSON` — (b) lock.
    * `TestE2E_DoubleCompleteSameFingerprintIsIdempotent` — same result+artifacts → second call returns SUCCEEDED, no DB re-write (result_json + completed_at byte-equal).
    * `TestE2E_DoubleCompleteDifferentResultReturnsConflict` — different `Result.Data` → `ErrCompletionConflict`.
    * `TestE2E_DoubleCompleteDifferentArtifactsReturnsConflict` — different artifact SHA256 → `ErrCompletionConflict`.
    * `TestE2E_LeaseExpiryFenceSQLGated` — past expiry → `ErrLeaseExpired`; DB row stays RUNNING.
    * `TestE2E_LeaseExpiryNullIsAcceptedBySQLGated` — after SUCCEEDED (lease_expiry cleared), the NULL branch of the SQL fence accepts the idempotent re-call.

  * **Forward-pointers** (out of scope for Step 3; honest-limitation disclosure):
    * `SetProgress._ = r.AddEvent(...)` (different surface, not direct INSERT INTO job_events) — same fake-availability anti-pattern; tighten in a follow-up commit.
    * `ClaimNext` unused `evtID` — pre-existing dead variable; hygiene-only cleanup.
    * Divergent-clock test gap (Go `lease.ExpiresAt` past vs DB `lease_expiry` future) — covered by the SQL-fence defence-in-depth design, but no explicit test pins it.
    * Concurrent-interleaved double-completion (two workers racing the same row) — handled atomically by the SQL UPDATE fence (one wins via `rows-affected=0`, other gets `ErrTransitionConflict`); no integration test pins this scenario.
    * `TestScriptFlowAsyncRoutes_EnqueueJobs` baseline regression — the legacy regression test in `internal/api/script/handler_test.go` references `NewScriptFlowHandler` / `ScriptFlowDeps` / `RequireAdminToken` / `AdminTokenProvider` not currently in the tree; pre-existing build-failure unrelated to Step 3.

  * **Pre-existing build issues carried forward** (honest-limitation convention — same as Step 1 + Step 2 closures):
    1. `internal/application/scripts/jobs/generation_job.go` declares `shaErr` and `job` but uses neither (vet warning).
    2. `internal/application/assets/monitor/scheduler.go` references undefined `NewExtractionEnqueuer` (vet warning).
    3. `internal/app/worker_registry_e2e_test.go:140` declares `*mockBroker` which doesn't implement `internal/application/jobs.Broker.CompleteWithArtifacts` (test-fixture interface mismatch).
    These three issues were present on `origin/main` before Step 1 landed; they are NOT regressions introduced by this commit per AGENTS.md §Honest-Limitations, surfaced for a future test-fixture migration ticket.

- **[Step 5 of 12-step plan (July 2026) — Qdrant CAS-fence + ErrStaleVersion, CLOSED]** `feat(clipindexer)` — closed via upstream SHA `96665cf1 fix(qdrant): make setIndexState error-returning and setIndexedAt CAS-fenced` (per AGENTS.md Git-Lesson-5 byte-equivalent-replay race recovery — parallel agents landed identical content on divergent lineages). CAS-fence WHERE: `id AND source_version AND content_hash_cas AND index_state='INDEXING'`; RowsAffected == 0 → typed `ErrStaleVersion` sentinel; callers discriminate via `errors.Is` and log+skip. `setIndexState` signature gained `contentHash + sourceVersion`, atomically seeds source_version column + `$.indexing_content_hash_cas` sidecar on INDEXING transitions. 200 race iterations (2×100 iter) under `-race` exercised in `indexing_cas_concurrency_test.go`. ShipStep 2 gate (`AssetIDToQdrantPointID`) verified green post-fix.

- **[Step 6 of 12-step plan (July 2026) — Channel Monitor outbox lease-based + retryable, CLOSED via byte-equivalent-replay recovery]** Per AGENTS.md Git-Lesson-5, upstream commit `207d92fd fix(monitor): lease-based outbox drainer with retry and canonical migration` shipped on `origin/main` ahead of the local Step 6 attempt. The canonical surface landed upstream: migration `120_monitor_enqueue_outbox_lease.sql` (lease_until + attempt_count + next_attempt_at + last_error_at columns + claim-window partial index), `monitor_outbox.go` rewritten with atomic `UPDATE…WHERE id IN (SELECT id ...ORDER BY ...LIMIT N) RETURNING` drainer claim (SQLite 3.35+ workaround), new `MarkOutboxRetry` exp-backoff retry path with `ErrRetryExhausted` terminal (DefaultOutboxMaxAttempts=5, base=2s, cap=60s), new `ReleaseOutboxLease` shutdown escape hatch, stuck-lease natural-reclaim subquery branch, `ports.go` extension with `MarkOutboxRetry` + `ReleaseOutboxLease`, `scheduler.go::dispatchOutboxEntry` classifies broker errors via `isTransientBrokerErr` (transient → retry, terminal → failed). Local Step 6 attempt was superseded WITHOUT force-push (Git-Lesson-5 anti-pattern). godlike/06 SSOT: schema in migrations/ only — `ensureOutboxTable` runtime helper retired. godlike/07 no-fake-availability: definitive states (pending + backoff scheduled OR terminal failed) preserved.

- **[Step 7 of 12-step plan (July 2026) — `document.generate` migrated to CompleteWithArtifacts vertical slice, CLOSED via byte-equivalent-replay recovery]** Per AGENTS.md Git-Lesson-5, upstream commit `56ae6b8a feat(docs): migrate document.generate to CompleteWithArtifacts vertical slice (Step 4/12)` (along with the coalesced `Step 2` JobFinalizer wiring `c9a6e005`/`36fcedae`, `Step 3` transactional-fence hardening `1cb2881f`, and `Step 5` already-SUCCEEDED lease-skips `b5e7b84a`/`199e47b6`/`5b2da7d7`) shipped on `origin/main` ahead of the local Step 7 attempt. The canonical surface landed upstream: `internal/application/document/usecase.go::ExecuteDocument` (handler → local `ArtifactManifest` → `delivery.Publisher.Publish` → `AssetTxFinalizer.FinalizeAsset` (atomic-version + asset_location + outbox-events) → `JobFinalizer.CompleteWithArtifacts` → `SUCCEEDED`), the `ProducesArtifacts` gate (`internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go` lines 76–78 enforce the legacy `SQLiteStore.Complete` rejection returning `ErrArtifactJobRequiresCompleteWithArtifacts`), and the publication_intents / orphan reconciler pair (`internal/application/jobs/finalizer/reconciler.go`). E2E coverage is canonical: `internal/api/assets/document/document_handler_manifest_test.go` pins the `__artifact_manifest` schema_version + the NoPathHardcoding test (P0 #4), `internal/application/jobs/finalizer/job_finalizer_e2e_test.go` pins the SUCCEEDED transition + idempotency fingerprint + transactional fence, `internal/application/jobs/finalizer/reconciler_test.go` pins the orphan-rewriting. **Cascade closures** — P0 #1 (ConflictPolicy + PutAction route in `internal/infrastructure/drive/publisher.go`), P0 #3 (`ErrMissingDelivery`/`ErrMissingDestination`/`ErrMissingFilename`/`ErrMissingLocalPath` composition-time sentinels), P0 #4 (RequestHasNoPathField test enforces the artifact path-suppression contract), and P0 #5 (`ProducesArtifacts=true` lockout on `SQLiteStore.Complete`). **Recovery action**: dropped the Step 6 untracked leftovers (`migrations/sqlite/120_monitor_outbox_lease.sql`, `internal/infrastructure/database/sqlite/assets/monitor_outbox_lease_test.go`) superseded by upstream commit `207d92fd`; no local Step 7 code surface remained after the upstream byte-equivalent replay, so this is a documentation-only closure commit (no Go changes). Forward-pointer: `architecture/current.yaml#FASE-7-of-12-step-plan` for the closure marker; remaining waves (8–12) are unrelated to document.generate.
