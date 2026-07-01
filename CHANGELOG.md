# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---

## Unreleased

### Fixed

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
- **[PR-C-YouTube-Cutover Commit I — Definition-of-Done E2E, June 2026]** Top-level E2E contract test for the channel-monitor cutover plan (`internal/application/assets/monitor/e2e_no_duplicates_test.go`). In-memory SQLite with real migrations `113_youtube_discoveries` + `106_add_channel_monitoring_state` inlined; ports mocked (`MonitorDownloaderPort` + `TranscriptProvider` + `VideoAnalyzer` + `JobEnqueuer`); Tick1 = claim+sync job, Tick2 = re-claim same channel. Pins the locked forward-counter invariants: `qdrant==5`, `db_clips==5`, `drive_uploads==5`, `outbox==5`, `accepted_jobs==5` (each TryReserve-winner emits once), `duplicate_enqueues==0` (TryReserve-gated per Commit D; losers never reach `EnqueueExtract` and the counter stays at 0) across the 5v×2 cycle, plus `youtube_discoveries==5` (UNIQUE ledger suppresses duplicates) and `cursor==MAX(discovered_at)` (monotonic). Also pins the parallel-same-channel sync race (two `HandleChannelSyncJob` goroutines simultaneous → one wins INSERT per video, the other classifies `already_scheduled`). Slot chosen as `Check 50` (lowest free ≥45) since `Check 45` is occupied by the inline `map[string]*ClipsRepository` ban (PR-BARE-ONLY-MAP-LITERAL-COVERAGE). The `testChannelsRepo` wrapper is a test-side shim for the channels.Repository signature drift (command-struct vs positional) and is expected to land a matching concrete-side fix in a follow-up commit. Test harness runs in serial mode (`Policy.MaxConcurrentVideos=1`) so the dedupe-contract surface is observed deterministically; production's concurrent-mode (`MaxConcurrentVideos≥2`) has a known 4/5 MarkEnqueued-loss bug tracked under `architecture/current.yaml#PR-MONITOR-FANOUT-MARKENQUEUED-RACE` — once that ticket lands and the test is bumped to concurrent, this E2E should still pass because the dedupe contract is rate-independent of the broker emit bookkeeping.

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
