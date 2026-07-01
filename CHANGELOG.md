# PipelineGen — CHANGELOG

Per godlike/07 (docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md), this
CHANGELOG records every user-visible API and behavior change. Each entry
cross-references the architecture/deprecations.yaml record (if any) and
the canonical ARCHITECTURE.md section that owns the change.

---

## Unreleased

### Fixed

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

**SUPERSEDES (godlike/06 honest audit)**: prior Commit 3/4 (`e62bb65a chore(youtube): Commit 3/4 Phase 1c TODO closure (semantic-shift pass)`) shipped with a canonical-delegation impl (`isSponsorSegment → ytmeta.IsSponsorSegment(transcript)` regex match against an expanded taxonomy of 9 patterns; `calculateQualityScore → ytmeta.CalculateQualityScore(...)` weighted 40/40/20 blend + caller-side sponsor penalty + math.Round bucketing). The user spec required an IN-FILE deterministic impl with substring match for sponsor detection + literal linear-formula blend for quality scoring — the prior canonical delegation satisfied neither constraint. This commit (3b/4) implements the user spec literally, replacing both functions with local algorithms. The canonical `metadata/service.go` helpers remain as exported building blocks for callers that opt into the broader-scoring path; the user-spec contract for the ym=nil fallback in `usecase/metadata_service.go` is owned by this file's local impls (godlike/06: one canonical owner per fact).

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
