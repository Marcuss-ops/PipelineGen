// Package usecase — process_segment_deps.go: canonical Port DTO bundle
// for ProcessYouTubeSegmentUseCase.
//
// Phase 2 split (PR-SPLIT-PROCESS-SEGMENT, July 2026): this file owns
// the ProcessSegmentDeps struct + its policy-version constant + the
// 5-port panic-check validation. Previously these lived in
// process_segment.go (614 LOC monolithic god-method); the split
// extracts them into a focused sister file so the slim orchestrator
// can stay ≤150 LOC (godlike/07 minimum-blast-radius target).
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - ProcessSegmentPolicyVersion const  → THIS file (canonical SSOT for the v1 literal)
//   - ProcessSegmentDeps struct          → THIS file (canonical bundle of all 11 ports + 3 typed nullable services)
//   - ProcessSegmentDeps.Validate()      → THIS file (canonical 5 panic-on-nil pre-ctor check)
//
// Lookup paths preserved verbatim across the package:
//
//   - dependencies.ProcessSegmentDeps    → still resolves to this struct (no rename)
//   - ProcessSegmentPolicyVersion        → still resolves to this const (no rename)
//   - logic.NewProcessYouTubeSegmentUseCase → still resolves to the ctor at process_segment.go (it
//     delegates the panic-check work to ProcessSegmentDeps.Validate() defined here)
//
// godlike/07 typed-error contract on the REQUIRED ports: the 5 panic
// checks keep their existing panic messages verbatim (no string drift);
// callers (the composition root at internal/app/build_bundles_youtube.go)
// catch the panic + log + boot the process anyway per the canonical
// fail-closed composition-layer pattern.
package usecase

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// ProcessSegmentPolicyVersion is the canonical "v1" policy version
// stamped into the deterministic clip ID + filename. Bump it when
// the metadata enrichment prompt, semantic keywords, embedding
// model, or segment policy change.
const ProcessSegmentPolicyVersion = "v1"

// ProcessSegmentDeps bundles every port ProcessYouTubeSegmentUseCase
// touches. nil-port tolerance matches the rest of the youtube package
// for the OPTIONAL ports (Subtitles/Transcriber/DriveFolderMgr); the
// REQUIRED ports panic on nil at ctor time.
type ProcessSegmentDeps struct {
	// Cache is the YouTube clip-cache port required by Step 2
	// (cache lookup). nil at composition MUST panic
	// (Validate() #1) — pre-Commit-1 silently passed through
	// and emitted "processed" with no clip evidence.
	Cache youtubeports.ClipCachePort
	// VideoPipeline is the YouTube-segment cut/extract port
	// required by Step 3-5. nil MUST panic (Validate() #2).
	VideoPipeline youtubeports.VideoPipelinePort
	// Subtitles is an OPTIONAL subtitle-fetcher port
	// (Step 6). nil → Step 6 silently skips.
	Subtitles youtubeports.SubtitleFetcherPort
	// Transcriber field RETIRED in PR-PY-CLIPS-CORRETTE-TRADOTTE
	// Fase 1.c (July 2026). The Whisper fallback at Step 7 is
	// now exclusively owned by TextTrackResolver (which holds
	// its own Transcriber reference wired at composition time).
	// Removing the duplicated direct use in Step 10 eliminates
	// the double-Whisper regression (Step 7 was invoking
	// Transcriber via the resolver; Step 10 was also calling it
	// directly on the same audio file).
	// Hash is the SHA-256 port required by Step 5 (file hash
	// fail-closed gate). nil MUST panic (Validate() #3).
	Hash youtubeports.HashServicePort
	// DriveFolderMgr is the OPTIONAL Drive-folder management
	// port. nil → Drive upload step uses StageSource fallback.
	DriveFolderMgr youtubeports.DriveFolderManagerPort
	// Writer is the legacy ClipAtomicWriter port (legacy Step 9
	// commit). Retained for callers that DO NOT carry localized
	// text (announcement / stock-without-i18n paths). For the
	// canonical localized super-tx path (PR-PY-CLIPS-CORRETTE-TRADOTTE
	// Fase 2.b, July 2026) the YouTube segment pipeline uses
	// LocalizedWriter. Both ports are satisfied by the same concrete
	// *ClipAtomicWriterAdapter; composition wires both to the same
	// instance. nil MUST panic (Validate() #4) — pre-Commit-1
	// silently wrote nothing and returned "processed".
	Writer youtubeports.ClipAtomicWriter
	// LocalizedWriter is the SOLE canonical surface for the
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b atomic super-tx
	// (clip + text tracks + cues + outbox in ONE SQLite tx).
	// Step 6-9 of the YouTube pipeline now uses this port
	// instead of the legacy Writer + TextTrackResolver.SaveMany
	// pair. The concrete instance is the SAME
	// *ClipAtomicWriterAdapter as Writer (the adapter satisfies
	// both ports — see clip_atomic_writer.go compile-time
	// assertion). nil port is a fail-closed wiring gap; the
	// step6to9 path mirrors the BLOCKER #4 partial-state pattern
	// when CommitClipTextAndIndexEvent returns a typed error.
	LocalizedWriter localized.LocalizedClipWriter
	// SegmentsSvc is the per-domain *SegmentsService (Step 1
	// timestamp parsing + Step 2 fingerprint extraction). nil
	// MUST panic (Validate() #5).
	SegmentsSvc *SegmentsService
	// SegmentPolicy is the duration gate (Min/Max in seconds).
	// Zero values default to {Min: 4, Max: 60}. Commit 2/6 #3.
	// Per user spec (2026-07-04): no effects, no transitions are
	// applied to extracted clips; the YouTube endpoint only cuts
	// the segment, preserves audio, uploads to Drive, writes
	// media_assets and emits the asset.index.requested outbox event.
	SegmentPolicy youtubetypes.SegmentPolicy
	// ClipMetadataWriter is the optional metadata-enrichment
	// writer (Commit 4/6, P1 #15). When non-nil, Step 10 of the
	// pipeline writes CanonicalClipMetadata to media_assets +
	// emits the metadata outbox event. When nil, Step 10
	// short-circuits silently.
	ClipMetadataWriter youtubeports.ClipMetadataWriter
	// MetadataService is the optional metadata-enrichment
	// orchestrator (Commit 4/6, P1 #15 + #16). When non-nil, Step
	// 10 calls EnrichClip to build + persist metadata. When nil,
	// Step 10 is a no-op.
	MetadataService *ytmetadata.MetadataService
	// Stager is the shared assets.SourceStager port (Step 9/12
	// wire-up, July 2026). Optional. When non-nil, Step 4
	// stages the FULL video via the shared stager before
	// retry.Do, then sets cutReq.PreDownloadedPath so the
	// concrete VideoPipeline uses ffmpeg -c copy to slice the
	// local staged file instead of re-downloading via yt-dlp.
	// This is genuine bandwidth-saving (one yt-dlp download per
	// Execute vs N downloads for the retry+replace case).
	Stager assets.SourceStager
	// FFProbe is the optional ffprobe validation port (audit
	// 2026-07-03 BLOCKER #3). When non-nil, Step 5a validates
	// the downloaded clip via ffprobe: container readable, video
	// stream present, duration within ±5% tolerance,
	// width/height > 0, FPS > 0, audio present when KeepAudio=
	// true. When nil, the validation step is silently skipped
	// (pre-existing hash + stat checks remain).
	FFProbe youtubeports.FFProbePort
	// TextTrackResolver is the OPTIONAL priority-chain resolver for
	// localized text tracks. When non-nil, it checks the API payload
	// and the DB before falling through to YouTube subtitles or Whisper,\t// reducing redundant transcription costs. After a transcript is
	// obtained from subtitles or Whisper, the caller invokes Save to
	// persist it for future reuse.
	// nil → skip resolver and proceed directly to subtitles/Whisper.
	TextTrackResolver *TextTrackResolver

	// Step10Metrics is the optional metrics-recorder port for
	// the partial-state Step 10 failure counter
	// (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY, July 2026). When
	// non-nil, the use case calls
	//   u.deps.Step10Metrics.IncStep10FailAfterClip(string(FailureCodeMetadataFailed))
	// on the Step 10 metadata-enrichment failure path BEFORE the
	// typed *ExtractionError return. The counter is partitioned
	// by failure_code so dashboards can aggregate partial-
	// state events across a batch extraction.
	//
	// When nil, the counter increment is silently skipped (the
	// operator Warn log at PR-PY-STEP10-FAIL-LOG is preserved
	// for granular forensics). Nil-tolerance matches the
	// optional-port pattern of Subtitles/Transcriber/
	// DriveFolderMgr/FFProbe.
	//
	// godlike/06 SSOT: this port is the SOLE canonical
	// application-layer surface for Step 10 partial-state
	// telemetry. The composition root wires the concrete
	// adapter (internal/infrastructure/observability.Step10
	// MetricsAdapter).
	Step10Metrics youtubeports.Step10MetricsRecorder

	// RequireTranscriptReady is the Fase 5
	// (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026) wire-up of
	// the pre-existing
	// localized.CommitLocalizedClipCommand.RequireTranscriptReady
	// policy gate. When true, Step 9 of
	// process_segment_step6to9 sets RequireTranscriptReady=true
	// on the super-tx; the writer then fails PRE-TX with
	// localized.ErrClipLocaleNotReady if no transcript-origin
	// READY track is present in the command's TextTracks.
	// When false (the canonical post-Fase-1.c default), the
	// writer commits even with no transcript — operators can
	// backfill via the Fase 5 admin command
	// (cmd/admin/text_tracks_backfill.go).
	//
	// Composition wires this from
	// cfg.Media.Multilingual.RequireTranscriptReady at
	// build_bundles_domain_media.go so the policy is read from
	// the canonical config and stays in sync with the
	// multilingual.yaml SSOT.
	//
	// godlike/07 fail-closed: when true and no transcript is
	// ready, the writer's typed error surfaces as
	// localized.IsClipLocaleNotReady in the orchestrator's
	// step6to9 error branch — the clip is NOT persisted
	// (the tx rolled back pre-commit), and the use case returns
	// a typed *ExtractionError (FailureCodeWriterFailed,
	// retryable=false) so the operator can decide whether to
	// backfill or relax the policy.
	RequireTranscriptReady bool

	Log *zap.Logger
}

// Validate enforces the 5 REQUIRED-port panic-check invariant. Called
// from NewProcessYouTubeSegmentUseCase (defined at process_segment.go)
// BEFORE the use case is constructed. Composition-time fail-closed
// at the panic site per godlike/07 NO-FAKE-AVAILABILITY: a missing
// required adapter is detected at boot, not at first
// POST /api/assets/youtube/extract invocation.
//
// The 5 panic messages are byte-verbatim to pre-PRCommit-1/6 —
// downstream callers (internal/app/build_bundles_youtube.go) recover
// via panic-catch + log + continue booting per the canonical
// fail-closed composition-layer pattern.
func (d ProcessSegmentDeps) Validate() {
	if d.Cache == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Cache port is required (composition must wire ClipCacheAdapter from internal/infrastructure/database/sqlite/assets/clip_cache_adapter.go)")
	}
	if d.VideoPipeline == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: VideoPipeline port is required (composition must wire the YouTube pipeline adapter)")
	}
	if d.Hash == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Hash port is required (composition must wire hashutil.NewHashAdapter)")
	}
	// godlike/07 fail-closed at composition boot: Writer is REQUIRED.
	// LocalizedWriter is RECOMMENDED but NOT a panic-checked required
	// field today — production composition (internal/app/
	// build_bundles_domain_media.go) wires BOTH Writer and
	// LocalizedWriter to the same concrete ClipAtomicWriterAdapter
	// instance. Tests that exercise failure paths (process_segment_*
	// failfast/correttezza/extraction_stubs tests) only wire Writer
	// because the test doesn't exercise the LocalizedWriter path.
	// step6to9.go's downgrade branch (else if u.deps.Writer != nil)
	// makes the test paths safe: a nil LocalizedWriter cleanly falls
	// back to the legacy CommitClipAndIndexEvent path, which is
	// identical to the pre-Fase 2.b behavior. Promoting the
	// LocalizedWriter nil-check to a panic would force every Writer-
	// stubbed test to add a LocalizedWriter stub; that breach in
	// blast-radius is not justified by the production-side win.
	// godlike/06 SSOT: the Fase 2.b canonical path is
	// LocalizedWriter. Composition MUST wire it (paths that don't
	// will silently take the legacy downgrade).
	if d.Writer == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Writer port is required — composition must wire ClipAtomicWriterAdapter (PR-C P0 #3 fail-closed; pre-Commit-1 silently wrote nothing and returned 'processed')")
	}
	if d.SegmentsSvc == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: SegmentsSvc port is required (composition must construct *SegmentsService via youtube.NewSegmentsService())")
	}
}
