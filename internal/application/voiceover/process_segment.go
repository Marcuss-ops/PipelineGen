// Package voiceover — usecase/process_segment.go (PR-VO-USECASE-PROCESS-DRY,
// P1 in VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-15).
//
// ProcessSegmentUseCase is the SINGLE canonical per-item voiceover pipeline
// runner. It replaces the divergent per-item bodies in:
//
//   - usecase.go::processOneLanguage (batch path, pre-DRY): manual
//     TX orchestration (BeginTx → DeleteByIDTx → InsertTx →
//     TransactionalOutbox.EnqueueIndexEvent → Commit). MISSING the
//     dedupe gate (Step 1), media_assets projection (Step 4), and
//     cleanup outbox (Step 6) — silent-success gap (godlike/07
//     "no fake availability" violation that the post-DRY migration
//     closes).
//
//   - process_voiceover_item.go::Execute (per-item path, pre-DRY):
//     delegated to VoiceoverFinalizer (P0.4 Fase 3a, July 2026).
//     Carries all 6 finalization steps correctly.
//
// Post-DRY BOTH callers consume the same ProcessSegmentUseCase.Execute
// method. The batch path is migrated to the finalizer (gains the
// dedupe gate + media_assets projection + cleanup outbox that it
// was missing). The per-item path loses its inline TTS/AudioPost/
// Publish/TX code (still uses the finalizer under the hood).
//
// godlike/06 SSOT: ProcessSegmentUseCase is the single owner of the
// per-item pipeline. The 5 stage files (stage_synthesize.go etc.
// from PR-VO-STAGES-SPLIT) are file-level owners of legacy batch
// path stages; the ProcessSegmentUseCase is the cross-file neutral
// owner that BOTH the batch and per-item callers delegate to.
//
// godlike/07 honest-limitation: this extraction does NOT touch
// the bounded parallel fan-out in usecase.go::Execute (the
// *Executor field). The fan-out layer is the worker-pool; the
// ProcessSegmentUseCase is the per-task body. They are distinct
// concerns: fan-out = how to schedule N tasks, ProcessSegmentUseCase
// = how to execute ONE task. The bounded pool continues to call
// the use case's processOneTask → processOneLanguage which now
// delegates to the ProcessSegmentUseCase.
//
// Package note: this file lives in the `usecase/` subdirectory of
// the `voiceover` package but is declared `package voiceover` (same
// as the parent). The subdirectory is organizational only — it
// keeps the canonical use case co-located with future use-case
// siblings (e.g. process_segment_aggregate.go, process_segment_promo.go)
// without forcing a subpackage split that would create an import
// cycle on `*VoiceoverItemResult` (which lives in the parent
// `voiceover` package). Per AGENTS.md Pattern 0 / godlike/07
// minimum-blast-radius: the file location is canonical, the package
// boundary stays the same.
package voiceover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// Canonical observability stage names for the per-item voiceover pipeline.
// They match the structured-log stage names (stageLog) and are recorded by
// the kernel Run via MeasureStage so each stage's duration flows into
// run_stage_observations through the SQLiteRecorder. stageLog remains the
// structured lifecycle log only — it never measures on its own.
const (
	canonicalStageTTS       kernobs.StageName = "tts"
	canonicalStageAudioPost kernobs.StageName = "audio_post"
	canonicalStagePublish   kernobs.StageName = "publish"
	canonicalStageFinalize  kernobs.StageName = "finalize"
)

// BuildVoiceoverIdempotencyKey derives the deterministic retry-safe
// deduplication key for the voiceover pipeline (FASE 3, July 2026).
// The canonical tuple (jobID + language + textHash + policyFingerprint)
// ensures that:
//   - Same job retried → same key (idempotency gate fires)
//   - Different job with same text → different key (no cross-job collision)
//   - Same job, different language → different key (per-language isolation)
//   - Same job/text but a DIFFERENT timing/post-processing policy →
//     different key, so a legacy audio-only cache entry is never reused
//     as a valid hit for a timing-required request.
//
// The key is a SHA-256 hex string of
// "jobID:language:textHash:policyFingerprint" so it is byte-stable across
// retries with the same inputs. Empty inputs produce a unique key that
// still hashes deterministically.
//
// godlike/07 minimum-blast-radius: the hash is computed via
// crypto/sha256 directly (no new dependencies).
func BuildVoiceoverIdempotencyKey(jobID string, language Language, textHash TextHash, policyFingerprint string) string {
	h := sha256.New()
	h.Write([]byte(jobID + ":" + string(language) + ":" + string(textHash) + ":" + policyFingerprint))
	return hex.EncodeToString(h.Sum(nil))
}

// BuildVoiceoverTimingIdempotencyKey derives the deterministic retry-safe
// key for a timing bundle file (timing.json / SRT / VTT). It extends the
// canonical audio tuple with the timing policy fingerprint and a kind
// suffix so the timing files never collide with the audio upload's
// idempotency key, each projection retries to the same Drive file, and a
// changed timing policy produces a fresh timing file rather than reusing a
// stale one.
func BuildVoiceoverTimingIdempotencyKey(jobID string, language Language, textHash TextHash, policyFingerprint string, kind string) string {
	h := sha256.New()
	h.Write([]byte(jobID + ":" + string(language) + ":" + string(textHash) + ":" + policyFingerprint + ":timing:" + kind))
	return hex.EncodeToString(h.Sum(nil))
}

// TimingPolicyFingerprint derives the stable cache fingerprint for the
// timing + post-processing policy. It captures the boundary mode, the
// timing schema version, the timing mode (disabled/best_effort/required)
// and the silence-removal policy, so a legacy audio-only cache entry (or
// an entry produced under a different policy) can never be reused as a
// valid hit for a request that expects a different timing bundle.
//
// nil timing falls back to the canonical defaults (best_effort / word /
// json), mirroring how the pipeline normalizes an absent policy.
func TimingPolicyFingerprint(timing *audio.TimingRequest, removeSilence bool) string {
	policy := audio.DefaultTimingRequest()
	if timing != nil {
		policy = timing.Normalized()
	}
	schema := 0
	if policy.Mode != audio.TimingDisabled {
		schema = audio.SpeechTimingVersion
	}
	return fmt.Sprintf("mode=%s;boundary=%s;schema=%d;silence=%t", policy.Mode, policy.BoundaryMode, schema, removeSilence)
}

// BuildVoiceoverContentFingerprint identifies reusable audio across jobs.
// The destination is included because the persisted artifact and its Drive
// ownership are part of the delivery contract. JobID is intentionally not
// included; retry isolation belongs to BuildVoiceoverIdempotencyKey.
func BuildVoiceoverContentFingerprint(textHash TextHash, language Language, voice, folderID string, timing *audio.TimingRequest, removeSilence bool) string {
	h := sha256.New()
	h.Write([]byte("voiceover-content-v1:" + string(textHash) + ":" + string(language) + ":" + voice + ":" + folderID + ":" + TimingPolicyFingerprint(timing, removeSilence)))
	return hex.EncodeToString(h.Sum(nil))
}

// ────────────────────────────────────────────────────────────────────────
// ProcessSegmentCommand — neutral DTO consumed by ProcessSegmentUseCase
// ────────────────────────────────────────────────────────────────────────

// ProcessSegmentCommand is the neutral DTO consumed by ProcessSegmentUseCase.
// Both callers (GenerateVoiceoversUseCase::processOneLanguage and
// ProcessVoiceoverItemUseCase::Execute) populate this DTO from their
// own input types and pass it to Execute.
//
// godlike/06 SSOT: this is the SINGLE canonical shape of the
// per-item pipeline input. Callers do the type translation from
// their input types (VoiceoverItem / GenerateVoiceoverItemCommand)
// into this DTO; the ProcessSegmentUseCase reads ONLY this DTO.
//
// godlike/07 minimal-blast-radius: ID + Filename are pre-computed
// by the caller (buildVoiceoverID + BuildVoiceoverFilename). The
// ProcessSegmentUseCase trusts them verbatim — it does NOT re-derive
// them, mirroring the BLOC4 P0.6 pass-through invariant pinned
// on the per-item path.
type ProcessSegmentCommand struct {
	// JobID is the canonical job identifier that produced this voiceover
	// item. Used to derive the deterministic idempotency key via
	// BuildVoiceoverIdempotencyKey(jobID, language, textHash, policyFingerprint).
	// Empty JobID is OK — the idempotency gate is skipped when empty
	// (backward-compat with pre-FASE-3 callers).
	JobID string

	// Identity (pre-computed by caller)
	ID        string
	RequestID string
	TextHash  TextHash // typed (PR-VO-TYPED-PRIMITIVES, July 2026)
	Text      string
	Language  Language // typed (PR-VO-TYPED-PRIMITIVES, July 2026)
	Voice     string
	Filename  string
	Strategy  string
	Metadata  map[string]any

	// Behavior flags
	RemoveSilence bool

	// Timing is the canonical voiceover timing policy for this segment.
	// nil means the canonical defaults apply (best_effort / word /
	// [json]) — timing capture is never implicitly mandatory. Consumed
	// by the TTS stage once providers produce raw word boundaries.
	Timing *audio.TimingRequest

	// Moments are the optional LLM-produced annotation queries (kind +
	// value) to anchor onto the canonical word timing. The model provides
	// only text; timestamps are derived by the timing stage via
	// PhraseLocator. nil means no moment projection (legacy behavior).
	Moments []audio.MomentQuery

	// Semantic routing (PR-P12-VOICEOVER-SEMANTIC-FIELDS, July 2026).
	// Canonical project identifier forwarded from the per-item command.
	// Empty Project is OK — the adapter builds the canonical subpath
	// from other fields when Project is absent.
	Project string

	// Destination (pre-resolved by caller). The ProcessSegmentUseCase
	// does NOT call DestinationResolver — destination resolution
	// is a caller-side concern (usecase.go resolves ONCE per
	// batch; process_voiceover_item.go resolves PER-ITEM with
	// DefaultFolderResolver fallback).
	Dest *ResolvedDestination

	// Replace-mode cleanup context (only for batch path's
	// ShouldSwap=true case; per-item path leaves these empty).
	// The finalizer's Step 6 (cleanup outbox) is guard-skipped
	// when ShouldSwap=false (pre-P0.7 + P0.7 Wave 21 June 2026
	// back-compat: no cleanup event for the per-item path).
	ShouldSwap     bool
	OldDriveFileID string
	OldLocalPath   string
	OldCleanedPath string
}

// VoiceoverCacheLookup is the optional cross-run cache port. When wired,
// ProcessSegmentUseCase.Execute checks the content fingerprint BEFORE
// Stage 1 (TTS); on a verified hit the entire TTS + upload + finalize
// pipeline is short-circuited and the result is built from the cached
// DB row. When nil (pre-cache callers), the cache check is skipped.
//
// godlike/07 honest-limitation: the cache trusts the DB row — if the
// Drive file was deleted externally the cached link becomes stale.
// A future BLOC can add an optional Drive probe step.
type VoiceoverCacheLookup interface {
	// Lookup returns a cache hit when the fingerprint matches a row
	// with a reusable status AND a non-empty DriveFileID. When timing
	// is required, the metadata column must also carry timing links
	// (timing_json_link); otherwise the hit is downgraded to a miss
	// (the audio exists but the timing bundle is not hydrated).
	Lookup(ctx context.Context, fingerprint string, timingRequired bool) (*VoiceoverCacheHit, error)
}

// AsyncPublishPool is the optional bounded pool for publish+finalize
// work (Drive upload + timing publish + SQLite commit). When wired,
// ProcessSegmentUseCase.Execute fires Stage 3+4 in a background goroutine
// via this pool AFTER Stage 1+2 complete, freeing the TTS slot immediately.
// The pool bounds concurrency so Drive uploads never overwhelm the network.
//
// Submit enqueues the publish+finalize work. It must not block indefinitely
// (a full pool is a configuration error, surfaced as a warn log).
//
// Wait blocks until all previously submitted tasks complete. The runner
// calls this after the voiceover phase but before audio compile so Drive
// links are hydrated on the scenes before downstream stages consume them.
//
// When nil (backward compat), Execute runs all 4 stages synchronously.
type AsyncPublishPool interface {
	Submit(ctx context.Context, fn func())
	Wait()
}

// VoiceoverCacheHit carries the cached row data needed to build a
// VoiceoverItemResult without running TTS or upload.
type VoiceoverCacheHit struct {
	ID            string
	Voice         string
	Filename      string
	DriveFileID   string
	DriveLink     string
	DownloadLink  string
	LocalPath     string
	CleanedPath   string
	DurationMs    int64
	LegacyFileMD5 string
	MetaJSON      []byte
}

// ────────────────────────────────────────────────────────────────────────
// ProcessSegmentDeps — narrow dep surface for the per-item pipeline
// ────────────────────────────────────────────────────────────────────────

// ProcessSegmentDeps wires the per-item pipeline dependencies.
// All required deps are mandatory (panic on nil — fail-fast per
// AGENTS.md WireUp pattern). AudioPostProcessor is nil-safe
// (only invoked when RemoveSilence is true).
//
// Note: DestinationResolver and DefaultFolderResolver are NOT
// in ProcessSegmentDeps because destination resolution is a
// caller-side concern (see ProcessSegmentCommand.Dest comment).
//
// Note: TransactionalOutbox is NOT in ProcessSegmentDeps because
// the finalizer owns the outbox (PR-VO-B3, June 2026). Pre-DRY
// the batch path used TransactionalOutbox directly; post-DRY
// the finalizer handles the index + cleanup outbox events inside
// the same tx.
//
// FASE 4 (July 2026): TxOutboxEnqueuer is an OPTIONAL port used
// ONLY for the orphan-cleanup path — when Stage 3 (Drive upload)
// succeeded but Stage 4 (Finalize) failed, the use case opens a
// SEPARATE tx (the original Finalize tx was rolled back) and
// enqueues a voiceover.cleanup.requested event for the orphaned
// Drive file. When nil (pre-FASE-4 callers), the orphan-cleanup
// path is silently skipped — the orphan sweeper will eventually
// recover the Drive file.
type ProcessSegmentDeps struct {
	TTSProvider         TTSProvider
	AudioPostProcessor  AudioPostProcessor // nil-safe
	Publisher           VoiceoverPublisher
	VoiceoverRepository persistence.Repository
	Finalizer           VoiceoverFinalizer   // mandatory (P0.4 Fase 3a)
	TxOutboxEnqueuer    TxOutboxEnqueuer     // optional (FASE 4 orphan-cleanup path; nil-safe)
	SemanticTagger      SemanticTaggerFunc   // optional; enriches canonical metadata when wired
	VoiceoverCache      VoiceoverCacheLookup // optional; cross-run cache — nil-safe, skip cache when nil
	AsyncPublish        AsyncPublishPool     // optional; nil = synchronous (backward compat). When wired, Stage 3+4 run in a bounded background pool so the TTS slot is freed after synthesis.
	Logger              *zap.Logger
}

// ────────────────────────────────────────────────────────────────────────
// ProcessSegmentUseCase — SINGLE canonical per-item pipeline runner
// ────────────────────────────────────────────────────────────────────────

// ProcessSegmentUseCase is the SINGLE canonical per-item pipeline runner
// (PR-VO-USECASE-PROCESS-DRY, P1 in VO-DECOMPOSITION-2026-07-04).
//
// godlike/06 SSOT: this struct + its Execute method are the
// ONE canonical owner of the per-item voiceover pipeline. Both
// GenerateVoiceoversUseCase (batch) and ProcessVoiceoverItemUseCase
// (per-item) delegate to it. No other per-item body should exist.
type ProcessSegmentUseCase struct {
	deps ProcessSegmentDeps
}

// NewProcessSegmentUseCase constructs the canonical use case. Mandatory
// deps are fail-fast (panic on nil — per AGENTS.md WireUp pattern).
// AudioPostProcessor and SemanticTagger are nil-safe. SemanticTagger is
// optional so the per-item child path can remain compatible while the
// composition root enables semantic enrichment for the batch path.
func NewProcessSegmentUseCase(deps ProcessSegmentDeps) *ProcessSegmentUseCase {
	if deps.TTSProvider == nil {
		panic("voiceover.NewProcessSegmentUseCase: TTSProvider is required (ProcessSegmentDeps.TTSProvider)")
	}
	if deps.Publisher == nil {
		panic("voiceover.NewProcessSegmentUseCase: Publisher is required (ProcessSegmentDeps.Publisher)")
	}
	if deps.VoiceoverRepository == nil {
		panic("voiceover.NewProcessSegmentUseCase: VoiceoverRepository is required (ProcessSegmentDeps.VoiceoverRepository)")
	}
	if deps.Finalizer == nil {
		panic("voiceover.NewProcessSegmentUseCase: Finalizer is required (P0.4 Fase 3a — unified finalization port)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &ProcessSegmentUseCase{deps: deps}
}

// Execute runs the canonical 4-stage per-item pipeline:
//
//	Stage 1: TTSProvider.Synthesize
//	Stage 2: AudioPostProcessor.Process (nil-safe, only when RemoveSilence)
//	Stage 3: VoiceoverPublisher.Publish (Drive upload)
//	Stage 4: VoiceoverRepository.BeginTx + VoiceoverFinalizer.Finalize + Commit
//
// Stage 4 delegates to the finalizer which runs all 6 sub-steps
// (dedupe, delete, insert, media_assets projection, index outbox,
// cleanup outbox) inside the caller-owned tx. The
// ProcessSegmentUseCase opens the tx, calls Finalize, then commits.
//
// godlike/07 failure mode: every stage returns a VoiceoverItemResult
// with typed Status + ErrorCode and a typed PipelineError. Stage 0
// (nil input or missing destination) fails permanently; transient
// provider and persistence failures carry their retry policy in the
// PipelineError. Error remains human-readable diagnostics only and is
// never used for stage classification.
func (u *ProcessSegmentUseCase) Execute(ctx context.Context, cmd *ProcessSegmentCommand) (*VoiceoverItemResult, error) {
	// Stage 0: nil-safe + required fields check.
	if cmd == nil {
		return nil, fmt.Errorf("ProcessSegmentUseCase.Execute: nil input")
	}
	if cmd.Dest == nil || cmd.Dest.FolderID == "" {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out := &VoiceoverItemResult{
			Language:  cmd.Language,
			Status:    StatusFailed,
			ErrorCode: string(FailureMissingFolder),
			Error:     "missing_folder_id: voiceover destination has no FolderID for upload",
		}
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageDestinationResolve, false, FailureMissingFolder, fmt.Errorf("%s", out.Error))
	}

	out := &VoiceoverItemResult{
		Language: cmd.Language,
		Voice:    cmd.Voice,
		Filename: cmd.Filename,
		ID:       cmd.ID,
		Status:   StatusFailed,
	}

	// ── Structured logging helper ───────────────────────────────────
	// The shared stageLog helper emits lifecycle logs; canonical runs own
	// timing observations.
	log := u.deps.Logger

	// ── Cross-run voiceover cache check ─────────────────────────────
	// Before invoking TTS (expensive) + Drive upload + timing publish,
	// check whether a previous run already produced the same voiceover
	// for the same content fingerprint (textHash + language + voice +
	// folderID + timing policy + silence policy). A verified cache hit
	// short-circuits the entire 4-stage pipeline and returns the
	// existing result — 0 TTS calls, 0 Drive uploads.
	timingPolicy := audio.DefaultTimingRequest()
	if cmd.Timing != nil {
		timingPolicy = cmd.Timing.Normalized()
	}
	if u.deps.VoiceoverCache != nil {
		fingerprint := BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, cmd.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence)
		hit, lookupErr := u.deps.VoiceoverCache.Lookup(ctx, fingerprint, timingPolicy.Mode != audio.TimingDisabled)
		if lookupErr != nil {
			log.Warn("voiceover cache lookup error — falling through to full pipeline",
				zap.String("fingerprint", fingerprint),
				zap.String("language", string(cmd.Language)),
				zap.Error(lookupErr))
		} else if hit != nil {
			log.Info("voiceover cache HIT — reusing existing audio, skipping TTS + upload + finalize",
				zap.String("fingerprint", fingerprint),
				zap.String("cached_id", hit.ID),
				zap.String("drive_file_id", hit.DriveFileID),
				zap.String("language", string(cmd.Language)))
			observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
			return buildCachedResult(cmd, hit, timingPolicy, log), nil
		}
		log.Info("voiceover cache MISS — full pipeline will run",
			zap.String("fingerprint", fingerprint),
			zap.String("language", string(cmd.Language)))
	}

	// Stage 1: TTSProvider.Synthesize.
	// P0.2 Fase 2c (July 2026): RemoveSilence is ALWAYS false here.
	// AudioPostProcessor owns silence removal (Stage 2), not the TTS
	// provider. Passing cmd.RemoveSilence=true to Synthesize would
	// cause the TTS bridge to strip silence inline, and then
	// AudioPostProcessor would re-process an already-cleaned file —
	// double-processing that wastes CPU and risks audio artifacts.
	emitTTS := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "tts", string(cmd.Language))
	// Timing capture lifecycle: capture.started fires before synthesis and
	// capture.completed after, so operators can trace boundary capture
	// without ever logging the per-word array.
	// timingPolicy is already resolved before the cache check above.
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.started", cmd, "", "", 0, 0)
	}
	// Stage 1 timing is recorded by the canonical Run (MeasureStage), so the
	// TTS duration flows into run_stage_observations via the SQLiteRecorder;
	// stageLog remains the structured lifecycle log only.
	var ttsOut TTSOutput
	err := kernobs.MeasureStage(ctx, canonicalStageTTS, func(stageCtx context.Context) error {
		var synthErr error
		ttsOut, synthErr = u.deps.TTSProvider.Synthesize(stageCtx, TTSInput{
			Text:          cmd.Text,
			Language:      cmd.Language,
			Voice:         cmd.Voice,
			Filename:      cmd.Filename,
			OutputDir:     cmd.Dest.FolderPath,
			RemoveSilence: false, // P0.2 Fase 2c: never delegate to TTS
			// Timing is the canonical timing policy; nil means the provider
			// applies the defaults. The provider only returns RAW boundaries;
			// the canonical artifact is built later from the final audio.
			Timing: cmd.Timing,
		})
		return synthErr
	})
	if err != nil {
		emitTTS("failed")
		observability.TTSFailuresTotal.Inc()
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTTS)
		out.Error = fmt.Sprintf("%s: %v", FailureTTS, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTTS, true, FailureTTS, err)
	}
	emitTTS("completed")
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.completed", cmd, ttsOut.Provider, ttsOut.BoundaryMode, len(ttsOut.WordBoundaries), ttsOut.Duration.Microseconds())
	}
	out.LocalPath = ttsOut.LocalPath
	out.CleanedPath = ttsOut.CleanedPath
	out.DurationMs = ttsOut.Duration.Milliseconds()
	if ttsOut.Voice != "" {
		out.Voice = ttsOut.Voice
	}
	out.LegacyFileMD5 = ttsOut.LegacyFileMD5

	// Stage 2: optional AudioPostProcessor (silence removal). Nil-safe.
	// The post output (cleaned path + edit map) is forwarded to the
	// publish stage so timing boundaries are remapped onto the CLEANED
	// timeline instead of producing timestamps for the pre-clean audio.
	var post *AudioPostOutput
	if cmd.RemoveSilence && u.deps.AudioPostProcessor != nil && ttsOut.LocalPath != "" {
		emitPost := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "audio_post", string(cmd.Language))
		var postOut AudioPostOutput
		err = kernobs.MeasureStage(ctx, canonicalStageAudioPost, func(stageCtx context.Context) error {
			var postErr error
			postOut, postErr = u.deps.AudioPostProcessor.Process(stageCtx, AudioPostInput{
				LocalPath: ttsOut.LocalPath,
				OutputDir: cmd.Dest.FolderPath,
				Filename:  cmd.Filename,
			})
			return postErr
		})
		if err != nil {
			emitPost("failed")
			observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
			out.ErrorCode = string(FailureAudioPost)
			out.Error = fmt.Sprintf("%s: %v", FailureAudioPost, err)
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, newPipelineErrorCode(StageAudioPost, false, FailureAudioPost, err)
		}
		emitPost("completed")
		if postOut.CleanedPath != "" {
			out.CleanedPath = postOut.CleanedPath
		}
		post = &postOut
		// Summarize the silence removal for observability: the original
		// pre-clean duration, the leading/trailing trims, and the cleaned
		// duration the timeline must use.
		out.SilenceCleanup = BuildSilenceCleanupReport(ttsOut.Duration.Microseconds(), postOut.DurationUS, postOut.EditMap)
		// Emit the structured observability event so operators can verify
		// "Edge aveva N ms di silenzio artificiale, li ho rimossi, la
		// timeline usa la durata pulita" without re-probing the file.
		silenceCleanupEvent(log, cmd, out.SilenceCleanup)
	}

	if out.LocalPath == "" && out.CleanedPath == "" {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureNoLocalPayload)
		out.Error = "no_local_payload: TTSProvider + AudioPostProcessor produced no local path"
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTTS, false, FailureNoLocalPayload, fmt.Errorf("%s", out.Error))
	}

	// ── Async publish path (P0.4: separate TTS pool from publish pool) ──
	// When AsyncPublish is wired, Stage 3 (Drive upload + timing) and
	// Stage 4 (SQLite finalize) run in a background goroutine via the
	// bounded publish pool. The TTS slot is freed immediately after
	// synthesis so the next scene can start TTS while Drive upload and
	// DB commit run concurrently.
	//
	// The partial result carries local file paths (LocalPath, CleanedPath,
	// DurationMs, Voice, Filename, ID) and a StatusGenerated status.
	// DriveFileID, DriveLink, DownloadLink, and Timing are NOT populated —
	// they are committed to the DB by the async goroutine and become
	// visible to downstream DB readers after the publish pool drains.
	if u.deps.AsyncPublish != nil {
		out.Status = StatusGenerated
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		observability.VoiceoverJobsTotal.WithLabelValues("generated").Inc()

		// Capture by-value copies for the closure so there is no race
		// between the caller (which reads the returned partial result)
		// and the background goroutine (which mutates its own copy
		// during publish+finalize).
		capturedCmd := *cmd
		capturedOut := *out
		capturedTTS := ttsOut
		var capturedPost *AudioPostOutput
		if post != nil {
			cp := *post
			capturedPost = &cp
		}

		log.Info("voiceover: async publish submitted — TTS slot freed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Int64("duration_ms", out.DurationMs))

		u.deps.AsyncPublish.Submit(ctx, func() {
			u.runAsyncPublish(ctx, &capturedCmd, &capturedOut, &capturedTTS, capturedPost, log)
		})

		return out, nil
	}

	// Stage 3: VoiceoverPublisher.Publish — delegates to publishStage
	// (process_segment_publish.go) for metadata building + idempotency
	// key derivation + Drive upload + the timing bundle publish (audio
	// + timing.json + optional SRT/VTT with required/best-effort/disabled
	// semantics).
	var pub *publishStageResult
	err = kernobs.MeasureStage(ctx, canonicalStagePublish, func(stageCtx context.Context) error {
		var pubErr error
		pub, pubErr = u.publishStage(stageCtx, cmd, out, &ttsOut, post, log)
		return pubErr
	})
	if err != nil {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		var pipelineErr *PipelineError
		if errors.As(err, &pipelineErr) {
			out.ErrorCode = string(pipelineErr.FailureCode())
			out.Error = pipelineErr.Error()
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, err
		}
		if errors.Is(err, delivery.ErrDestinationParentMismatch) {
			observability.DriveUploadFailuresTotal.Inc()
			out.ErrorCode = VoiceoverDestinationMismatchCode
			out.Error = fmt.Sprintf("%s: %v", VoiceoverDestinationMismatchCode, err)
			setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
			return out, newPipelineErrorCode(StageUpload, false, FailureDestinationMismatch, err)
		}
		observability.DriveUploadFailuresTotal.Inc()
		out.ErrorCode = string(FailureUpload)
		out.Error = fmt.Sprintf("%s: %v", FailureUpload, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageUpload, true, FailureUpload, err)
	}

	// Stage 4: BeginTx + Finalize + Commit (delegated to finalizer).
	// PR-VO-USECASE-PROCESS-DRY migration: the batch path now
	// delegates to the finalizer (gains the dedupe gate +
	// media_assets projection + cleanup outbox that it was
	// missing pre-DRY). The per-item path was already using the
	// finalizer; the only change is that the body now lives in
	// the ProcessSegmentUseCase.
	emitFinalize := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "finalize", string(cmd.Language))
	tx, err := u.deps.VoiceoverRepository.BeginTx(ctx)
	if err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTxBegin)
		out.Error = fmt.Sprintf("%s: %v", FailureTxBegin, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTxBegin, true, FailureTxBegin, err)
	}
	defer func() { _ = tx.Rollback() }() // safe after Commit

	finalizeCmd := &FinalizeCommand{
		Fingerprint:     BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, out.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence),
		IdempotencyKey:  pub.IdemKey,
		JobID:           cmd.JobID,
		ID:              cmd.ID,
		RequestID:       cmd.RequestID,
		TextHash:        string(cmd.TextHash),
		Text:            cmd.Text,
		Language:        cmd.Language,
		Voice:           out.Voice,
		Filename:        cmd.Filename,
		Strategy:        cmd.Strategy,
		MetaJSON:        pub.MetaJSON,
		LocalPath:       out.LocalPath,
		CleanedPath:     out.CleanedPath,
		LegacyFileMD5:   out.LegacyFileMD5,
		DurationSeconds: ttsOut.Duration.Seconds(),
		FolderID:        cmd.Dest.FolderID,
		FolderPath:      cmd.Dest.FolderPath,
		DriveFileID:     out.DriveFileID,
		DriveLink:       out.DriveLink,
		DownloadLink:    out.DownloadLink,
		ShouldSwap:      cmd.ShouldSwap,
		OldDriveFileID:  cmd.OldDriveFileID,
		OldLocalPath:    cmd.OldLocalPath,
		OldCleanedPath:  cmd.OldCleanedPath,
	}

	var finalizeRes *FinalizeResult
	err = kernobs.MeasureStage(ctx, canonicalStageFinalize, func(stageCtx context.Context) error {
		var finalizeErr error
		finalizeRes, finalizeErr = u.deps.Finalizer.Finalize(stageCtx, tx, finalizeCmd)
		return finalizeErr
	})
	if err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureFinalize)
		out.Error = fmt.Sprintf("%s: %v", FailureFinalize, err)

		// Rollback the transaction immediately to release the connection pool lock
		_ = tx.Rollback()

		// FASE 4 (July 2026): orphan-cleanup path — when Drive upload
		// succeeded (Stage 3) but DB finalize failed (Stage 4), the
		// Drive file is orphaned (exists on Drive with no DB row).
		// Open a SEPARATE tx (the Finalize tx is about to be rolled
		// back by defer) and enqueue a voiceover.cleanup.requested
		// outbox event so the orphan sweeper can trash the Drive
		// file. When TxOutboxEnqueuer is nil (pre-FASE-4 callers),
		// this path is silently skipped — the background orphan
		// sweeper will eventually recover the file.
		//
		// godlike/07 NO-FAKE-AVAILABILITY: the cleanup event is
		// committed in its OWN tx, independent of the rolled-back
		// Finalize tx. A failure to enqueue the cleanup event (e.g.
		// DB unreachable) is logged at Warn level but does NOT
		// change the finalize_failed error — the caller still sees
		// the original Finalize error.
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}

		return out, newPipelineErrorCode(StageTxCommit, true, FailureFinalize, err)
	}

	// If the dedupe gate matched an existing row (Reused=true),
	// adopt the matched ID as the canonical identifier.
	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if err := tx.Commit(); err != nil {
		emitFinalize("failed")
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out.ErrorCode = string(FailureTxCommit)
		out.Error = fmt.Sprintf("%s: %v", FailureTxCommit, err)
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageTxCommit, true, FailureTxCommit, err)
	}
	emitFinalize("completed")

	out.Status = StatusCompleted
	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
	observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
	return out, nil
}

// buildCachedResult constructs a VoiceoverItemResult from a cache hit
// without running TTS, audio post-processing, Drive upload, or DB
// finalize. The result carries the cached DriveFileID, DriveLink,
// DownloadLink, LocalPath, DurationMs, and Voice from the DB row.
//
// When the metadata column carries timing links, a VoiceoverTimingResult
// is reconstructed so downstream consumers (script binding, docs render)
// receive the same shape as a cold-run result. Word-level timing data
// (the SpeechTimingArtifact) is NOT reconstructed — only the summary
// fields and links are hydrated; consumers that need per-word timing
// must download the timing.json artifact from Drive.
func buildCachedResult(cmd *ProcessSegmentCommand, hit *VoiceoverCacheHit, timingPolicy audio.TimingRequest, log *zap.Logger) *VoiceoverItemResult {
	out := &VoiceoverItemResult{
		Language:      cmd.Language,
		Voice:         hit.Voice,
		Filename:      hit.Filename,
		ID:            hit.ID,
		Status:        StatusCompleted,
		CacheHit:      true,
		DriveFileID:   hit.DriveFileID,
		DriveLink:     hit.DriveLink,
		DownloadLink:  hit.DownloadLink,
		LocalPath:     hit.LocalPath,
		CleanedPath:   hit.CleanedPath,
		DurationMs:    hit.DurationMs,
		LegacyFileMD5: hit.LegacyFileMD5,
	}

	// Reconstruct the timing result from the persisted metadata when
	// timing was requested. The full word-level artifact is not stored
	// in the metadata column (only the SSOT timing.json on Drive has
	// it), so the summary fields are hydrated from metadata.
	if timingPolicy.Mode != audio.TimingDisabled && len(hit.MetaJSON) > 0 {
		var meta map[string]any
		if err := json.Unmarshal(hit.MetaJSON, &meta); err == nil {
			if jsonLink, ok := meta["timing_json_link"].(string); ok && jsonLink != "" {
				timingRes := &VoiceoverTimingResult{Status: TimingStatusCompleted}
				timingRes.JSONLink = jsonLink
				if srtLink, _ := meta["timing_srt_link"].(string); srtLink != "" {
					timingRes.SRTLink = srtLink
				}
				if vttLink, _ := meta["timing_vtt_link"].(string); vttLink != "" {
					timingRes.VTTLink = vttLink
				}
				if boundaryMode, _ := meta["timing_boundary_mode"].(string); boundaryMode != "" {
					timingRes.BoundaryMode = boundaryMode
				}
				if wordCount, ok := meta["timing_word_count"].(float64); ok {
					timingRes.WordCount = int(wordCount)
				}
				if durationUS, ok := meta["timing_duration_us"].(float64); ok {
					timingRes.DurationUS = int64(durationUS)
				}
				if audioSHA, _ := meta["audio_sha256"].(string); audioSHA != "" {
					timingRes.AudioSHA256 = audioSHA
				}
				out.Timing = timingRes
			}
		}
	}

	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)

	log.Info("voiceover cache HIT result built",
		zap.String("id", out.ID),
		zap.String("drive_file_id", out.DriveFileID),
		zap.String("language", string(cmd.Language)),
		zap.Int64("duration_ms", out.DurationMs))

	return out
}

// enqueueOrphanCleanup is defined in process_segment_orphan.go.

// runAsyncPublish executes Stage 3 (Drive upload + timing publish) and
// Stage 4 (SQLite finalize) in a background goroutine via the async
// publish pool. It is only called when AsyncPublish is wired.
//
// The method accepts by-pointer copies of the command, partial result,
// and TTS output — the originals are owned by the caller (which already
// returned the partial result to free the TTS slot). Errors are logged
// at Warn level; the caller cannot observe the outcome synchronously
// (by design — the TTS slot is freed before publish completes).
//
// godlike/07 honest-limitation: publish failures in the async path are
// NOT surfaced to the immediate caller. The DB row carries the failure
// in its status/error columns; the run result's per-scene Voiceover
// reference remains StatusGenerated with local paths. Downstream
// observability dashboards must read the DB to detect async failures.
func (u *ProcessSegmentUseCase) runAsyncPublish(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	ttsOut *TTSOutput,
	post *AudioPostOutput,
	log *zap.Logger,
) {
	// Stage 3: publish (Drive upload + timing bundle).
	pub, pubErr := u.publishStage(ctx, cmd, out, ttsOut, post, log)
	if pubErr != nil {
		log.Warn("voiceover async publish failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(pubErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		return
	}

	// Stage 4: BeginTx + Finalize + Commit.
	tx, txErr := u.deps.VoiceoverRepository.BeginTx(ctx)
	if txErr != nil {
		log.Warn("voiceover async finalize tx begin failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(txErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		// FASE 4 orphan-cleanup: the Drive file was uploaded but
		// the DB tx couldn't start. Enqueue a cleanup event.
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}
		return
	}
	defer func() { _ = tx.Rollback() }()

	fingerprint := BuildVoiceoverContentFingerprint(cmd.TextHash, cmd.Language, out.Voice, cmd.Dest.FolderID, cmd.Timing, cmd.RemoveSilence)
	finalizeCmd := &FinalizeCommand{
		Fingerprint:     fingerprint,
		IdempotencyKey:  pub.IdemKey,
		JobID:           cmd.JobID,
		ID:              cmd.ID,
		RequestID:       cmd.RequestID,
		TextHash:        string(cmd.TextHash),
		Text:            cmd.Text,
		Language:        cmd.Language,
		Voice:           out.Voice,
		Filename:        cmd.Filename,
		Strategy:        cmd.Strategy,
		MetaJSON:        pub.MetaJSON,
		LocalPath:       out.LocalPath,
		CleanedPath:     out.CleanedPath,
		LegacyFileMD5:   out.LegacyFileMD5,
		DurationSeconds: ttsOut.Duration.Seconds(),
		FolderID:        cmd.Dest.FolderID,
		FolderPath:      cmd.Dest.FolderPath,
		DriveFileID:     out.DriveFileID,
		DriveLink:       out.DriveLink,
		DownloadLink:    out.DownloadLink,
		ShouldSwap:      cmd.ShouldSwap,
		OldDriveFileID:  cmd.OldDriveFileID,
		OldLocalPath:    cmd.OldLocalPath,
		OldCleanedPath:  cmd.OldCleanedPath,
	}

	finalizeRes, finalizeErr := u.deps.Finalizer.Finalize(ctx, tx, finalizeCmd)
	if finalizeErr != nil {
		log.Warn("voiceover async finalize failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(finalizeErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		_ = tx.Rollback()
		if out.DriveFileID != "" && u.deps.TxOutboxEnqueuer != nil {
			u.enqueueOrphanCleanup(ctx, cmd.ID, out.DriveFileID, out.LocalPath, out.CleanedPath)
		}
		return
	}

	if finalizeRes != nil && finalizeRes.Reused {
		out.ID = finalizeRes.ID
	}

	if commitErr := tx.Commit(); commitErr != nil {
		log.Warn("voiceover async finalize commit failed",
			zap.String("scene_id", cmd.ID),
			zap.String("language", string(cmd.Language)),
			zap.Error(commitErr))
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		return
	}

	log.Info("voiceover async publish+finalize completed",
		zap.String("scene_id", cmd.ID),
		zap.String("language", string(cmd.Language)),
		zap.String("drive_file_id", out.DriveFileID))
	observability.VoiceoverJobsTotal.WithLabelValues("completed").Inc()
}
