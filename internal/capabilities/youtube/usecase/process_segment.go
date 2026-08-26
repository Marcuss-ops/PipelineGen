// Package usecase — process_segment.go: canonical slim orchestrator for
// ProcessYouTubeSegmentUseCase.
//
// Commit C (PR-C-YouTube-Cutover, June 2026) lifts the legacy per-segment
// orchestration out of the youtube/adapters/ package into a typed use case.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the use case became the
// production path. 5 required ports panic on nil at ctor time
// (Cache/VideoPipeline/Hash/Writer/SegmentsSvc).
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza): 5 fail-closed
// corrections landed (StrategyReplace cache-bypass + SegmentPolicy bounds +
// policyVersion in filename + runtime fail-closed at Step 5 + ClipAsset
// canonical wiring). See CHANGELOG.md §1 for the full per-issue commentary.
//
// Phase 2 split (PR-SPLIT-PROCESS-SEGMENT, July 2026): the original 614-LOC
// Execute god-method is decomposed into 6 step methods localized in
// godlike/06 SSOT owner files (one canonical owner per fact per step):
//
//   - process_segment_step1.go      → Step 1   deterministic clip ID + SegmentPolicy
//   - process_segment_step2.go      → Step 2   cache lookup + StrategyReplace bypass
//   - process_segment_step3to5.go   → Steps 3-5 cut/retry/hash/fail-closed + Step 4a Stager
//   - process_segment_step5a_ffprobe.go → Step 5a ffprobe validation
//   - process_segment_step6to9.go   → Steps 6-9 subtitles + Drive upload + writer commit
//   - process_segment_step10.go     → RETIRED Step 10 (pure-analysis regression seam)
//
// Phase 2 Gruppo C-2 (PR-GRUPOC-2, July 2026): the use case struct
// holds 4 capability-area sub-bundles (Core/Media/Metadata/Observability)
// directly as 4 fields. The pre-PR `deps ProcessSegmentDeps` field
// is RETIRED — per the user's explicit VINCOLO ASSOLUTO against
// struct-bag aliasing. The 17 fields are now in 4 sub-bundles
// (process_segment_deps.go), each <=7 fields, to clear
// percheck_struct_deps <=8.
//
// The public Execute(ctx, cmd) (ProcessSegmentResult, error) signature is
// byte-identical to pre-split (godlike/07 minimum-blast-radius). Lookup
// path *UseCase.{Name,Policy,Execute} unchanged. Helpers
// (buildClipAsset, deriveNormalizedGroup, fail, failInvalidTimestamp,
// composeYouTubeClipSearchText, cleanSegmentName, validateFFProbeReport)
// still live in process_segment_helpers.go.
package usecase

import (
	"context"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ProcessYouTubeSegmentUseCase is the canonical per-segment pipeline.
//
// godlike/06 SSOT for the accompanying types:
//   - ProcessSegmentPolicyVersion const                       → process_segment_deps.go (canonical SSOT)
//   - 4 sub-bundle types (Core/Media/Metadata/Observability)   → process_segment_deps.go (canonical bundles)
//   - NewProcessYouTubeSegmentFromSubBundles                   → process_segment_deps.go (canonical ctor)
//   - ValidateProcessSegmentSubBundles                        → process_segment_deps.go (canonical 5-port panic-check)
//
// PR-GRUPOC-2 (July 2026): the struct holds 4 sub-bundle fields
// directly (NOT a `deps ProcessSegmentDeps` wrapper) per the user's
// VINCOLO ASSOLUTO against struct-bag aliasing. Each sub-bundle
// is <=7 fields (percheck_struct_deps <=8 enforcement).
type ProcessYouTubeSegmentUseCase struct {
	core          ProcessSegmentCoreDeps
	media         ProcessSegmentMediaDeps
	metadata      ProcessSegmentMetadataDeps
	observability ProcessSegmentObservabilityDeps
}

// AcquireStockTranscript exposes the existing resolver for the transcript-
// first stock planner. It never downloads video bytes.
func (u *ProcessYouTubeSegmentUseCase) AcquireStockTranscript(ctx context.Context, videoID string, durationMs int64) (*detail.ResolvedTextBundle, error) {
	if u == nil || u.media.TextTrackResolver == nil {
		return nil, nil
	}
	return u.media.TextTrackResolver.AcquireSegmentText(ctx, TextTrackAcquireRequest{
		VideoID: videoID, StartSec: 0, EndSec: int(durationMs / 1000),
	})
}

// Execute runs the canonical 9-step pipeline for one segment. The body
// is decomposed into 6 step methods (see package-doc godoc for the
// per-file ownership map). This function is the SOLE orchestrator —
// the order of step calls here is the canonical pipeline order
// (gated at runtime by the per-step defer/fail-closed invariants).
//
// godlike/06 SSOT: the orchestrator owns the 6-step SEQUENCE only;
// each step body lives in exactly one sister file. Fail-closed gates
// (fail/failInvalidTimestamp) live in process_segment_helpers.go.
//
// godlike/07 minimum-blast-radius: signature byte-identical to pre-split
// (parent commit e9a5b3994 / process_segment.go 614 LOC). The 17 existing
// tests in process_segment_test.go + _correttezza_test.go +
// _step10_metrics_test.go + _step10_partial_state_e2e_test.go exercise
// this signature directly with mocked ports — they MUST PASS unchanged.
func (u *ProcessYouTubeSegmentUseCase) Execute(ctx context.Context, cmd youtubetypes.ProcessSegmentCommand) (youtubetypes.ProcessSegmentResult, error) {
	out := youtubetypes.ProcessSegmentResult{
		Status: "failed",
		Item: youtubetypes.ExtractItem{
			Name:            cleanSegmentName(cmd.Segment.Name, cmd.Index),
			Start:           strings.TrimSpace(cmd.Segment.Start),
			End:             strings.TrimSpace(cmd.Segment.End),
			DriveFolderID:   cmd.DriveFolderID,
			DriveFolderPath: cmd.DriveFolderPath,
			Status:          "failed",
		},
	}

	// Resolve keepAudio/normalize ONCE here so both step3to5 (whose
	// cutReq.KeepAudio is read by the concrete VideoPipeline) and
	// step5a_FFProbeValidate (whose keepAudio gate en-/disables the
	// audio-stream check) see the same value. NIT-1 from the
	// pre-PR thinker-with-files-gemini code review.
	keepAudio := true
	if cmd.KeepAudio != nil {
		keepAudio = *cmd.KeepAudio
	}

	// Step 1 — deterministic clip ID + timestamp validation +
	// SegmentPolicy bounds + filename. (step1 does NOT use keepAudio
	// — only step3to5 + step5a need it, so the orchestrator resolves
	// it once and threads it to those two methods only.)
	startSec, endSec, duration, clipID, policyVer, err := u.step1_BuildClipID(cmd, &out)
	if err != nil {
		return out, err
	}

	// Step 2 — cache lookup + StrategyReplace bypass.
	cacheHit, err := u.step2_CacheLookup(ctx, cmd, &out, clipID)
	if err != nil {
		return out, err
	}

	// Steps 3-5 — cut + retry + runtime fail-closed (path / size / hash)
	// + Step 4a shared SourceStager pre-stage (deferred best-effort
	// cleanup at scope end).
	//
	// policyVer is intentionally NOT threaded here: it's consumed
	// downstream by Step 9 (buildClipAsset filename) + Step 10
	// (metadata writer audit-pin) via step6to9 (godlike/07 minimum-
	// blast-radius, no YAGNI hooks at this seam).
	//
	// PR-CACHE-HIT-FINALIZATION (godlike/06 SSOT): a binary cache hit
	// skips ONLY acquisition/cut (Steps 3-5) and ffprobe (Step 5a).
	// The canonical enrichment/finalization gate (Steps 6-9) STILL
	// runs on the cached binary so a cache hit repairs missing/stale
	// metadata, text tracks and the index request instead of
	// short-circuiting before the semantic snapshot exists. The cached
	// binary coordinates (LocalPath + LegacyFileMD5) are surfaced on `out`
	// by step2_CacheLookup on the hit path.
	var fileHash, localPath string
	if cacheHit {
		fileHash = out.Item.LegacyFileMD5
		localPath = out.Item.LocalPath
	} else {
		fileHash, localPath, err = u.step3to5_CutRetryHash(ctx, cmd, &out, clipID, startSec, endSec, duration, keepAudio)
		if err != nil {
			return out, err
		}

		// Step 5a — ffprobe validation (optional; nil-port = skip).
		if err := u.step5a_FFProbeValidate(ctx, &out, clipID, localPath, duration, keepAudio); err != nil {
			return out, err
		}
	}

	// Steps 6-9 — subtitle slicing (Step 6) + Whisper fallback (Step 7) +
	// Drive upload (Step 8) + canonical ClipAsset Writer commit (Step 9).
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026): the
	// returned *detail.ResolvedTextBundle is the canonical
	// transcript bundle acquired by the 5-priority chain
	// (TextTrackResolver). It is threaded into Step 10 so Step 10
	// does NOT re-invoke Whisper on the same audio file. The
	// bundle can be nil when TextTrackResolver is nil OR all 5
	// priorities failed; Step 10's contract is fail-closed on
	// nil bundle (empty transcript, empty cues, no error).
	_, err = u.step6to9_SubtitlesDriveWriter(ctx, cmd, &out, clipID, startSec, endSec, localPath, fileHash, policyVer)
	if err != nil {
		return out, err
	}

	// Step 10 is RETIRED (PR-ASSET-COMMITTER-ENRICHMENT, August 2026).
	// The metadata analysis now runs INSIDE step6to9 BEFORE the canonical
	// atomic super-tx, so the single commit already carries the complete
	// semantic snapshot (summary/topics/speakers/mentioned people/hook/
	// quality score/tags + taxonomy). There is no post-commit metadata-only
	// write and no second asset.index.requested event; Execute no longer
	// performs a second enrichment pass.

	out.Item.Status = "processed"
	out.Status = "processed"
	return out, nil
}
