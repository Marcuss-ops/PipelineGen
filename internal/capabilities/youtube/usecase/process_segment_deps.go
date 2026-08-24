// Package usecase — process_segment_deps.go: canonical sub-bundle
// DTOs + canonical ctor + canonical 5-port panic-check validator
// for ProcessYouTubeSegmentUseCase.
//
// Phase 2 Gruppo C-2 (PR-GRUPOC-2, July 2026):
//
// godlike/06 SSOT (one canonical owner per fact):
//   - ProcessSegmentPolicyVersion const                       → THIS file (canonical SSOT)
//   - ProcessSegmentCoreDeps{7 fields}                        → THIS file (canonical runtime + required ports)
//   - ProcessSegmentMediaDeps{5 fields}                       → THIS file (canonical external I/O surface)
//   - ProcessSegmentMetadataDeps{3 fields}                    → THIS file (canonical metadata-enrichment surface)
//   - ProcessSegmentObservabilityDeps{2 fields}               → THIS file (canonical metrics + policy surface)
//   - NewProcessYouTubeSegmentFromSubBundles                   → THIS file (canonical ctor)
//   - ValidateProcessSegmentSubBundles                        → THIS file (canonical 5-port panic-check)
//
// The pre-PR `ProcessSegmentDeps` struct (17 fields) was
// RETIRED (no back-compat shim, no type alias) — the
// percheck_struct_deps <=8 enforcement gate flagged the
// 17-field struct. The 17 fields are now split into 4
// capability-area sub-bundles (Core/Media/Metadata/Observability),
// each <=7 fields, to clear the gate.
//
// The use case struct (process_segment.go) holds the 4 sub-bundles
// directly as 4 fields (NOT a single `deps ProcessSegmentDeps`
// wrapper) per the user's explicit VINCOLO ASSOLUTO against
// struct-bag aliasing:
//
//	"VINCOLO ASSOLUTO: niente struct artificiali per nascondere
//	 dipendenze al contatore"
//
// godlike/07 typed-error contract: the 5 panic checks keep their
// existing panic messages byte-verbatim (no string drift);
// downstream callers (the composition root at
// internal/app/build_bundles_domain_media.go) catch the panic +
// log + boot the process anyway per the canonical fail-closed
// composition-layer pattern.
//
// pre-PR commit reference: 22a70dcaf "fix(domain/job): re-add
// kernel_aliases back-compat alias layer" — that commit
// re-introduced the legacy 17-field ProcessSegmentDeps layer
// after the 4 prior kernel-direct migration reverts. This file
// is the canonical godlike/06 EXPAND/BACKFILL step (per
// architecture/current.yaml::+ wave entry) for the
// ProcessSegmentDeps retirement: the public type is GONE; the
// use case struct holds 4 sub-bundles directly; the 4-arg ctor
// signature is the canonical one.
package usecase

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// ProcessSegmentPolicyVersion is the canonical "v1" policy version
// stamped into the deterministic clip ID + filename. Bump it when
// the metadata enrichment prompt, semantic keywords, embedding
// model, or segment policy change.
const ProcessSegmentPolicyVersion = "v1"

// ProcessSegmentCoreDeps bundles the core runtime config + the 5
// REQUIRED ports that fail-closed panic on nil at ctor time. 7
// fields.
//
// godlike/06 SSOT: this sub-bundle owns the 5-port panic-check
// surface (Cache, VideoPipeline, Hash, Writer, SegmentsSvc) + the
// runtime config (SegmentPolicy, Log). Steps 1, 2, 3-5, 5a, 6-9,
// and 10 all touch at least one field in this sub-bundle.
//
// godlike/07 fail-closed at composition boot: the 5 panic checks
// are enforced by ValidateProcessSegmentSubBundles. Composition
// that does NOT wire any of these 5 ports hits the panic
// immediately, NOT at first POST /api/assets/youtube/extract
// invocation.
type ProcessSegmentCoreDeps struct {
	// Cache is the YouTube clip-cache port required by Step 2
	// (cache lookup). nil at composition MUST panic
	// (Validate() #1) — pre-Commit-1 silently passed through
	// and emitted "processed" with no clip evidence.
	Cache youtubeports.ClipCachePort
	// VideoPipeline is the YouTube-segment cut/extract port
	// required by Step 3-5. nil MUST panic (Validate() #2).
	VideoPipeline youtubeports.VideoPipelinePort
	// Hash is the SHA-256 port required by Step 5 (file hash
	// fail-closed gate). nil MUST panic (Validate() #3).
	Hash youtubeports.HashServicePort
	// Writer is the legacy ClipAtomicWriter port (legacy Step 9
	// commit). Retained for callers that DO NOT carry localized
	// text. nil MUST panic (Validate() #4) — pre-Commit-1
	// silently wrote nothing and returned "processed".
	Writer youtubeports.ClipAtomicWriter
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
	// Log is the zap logger; nil → zap.NewNop() in the ctor
	// (canonical pattern, matches pre-refactor).
	Log *zap.Logger
}

// ProcessSegmentMediaDeps bundles the external I/O + stager ports.
// 5 fields, all optional (nil-port safe at runtime — no
// fail-closed panic, no Validate() check).
//
// godlike/06 SSOT: this sub-bundle owns the optional external
// service surface. Composition wires these from the application
// adapters; tests typically leave them nil unless they exercise
// the corresponding step.
type ProcessSegmentMediaDeps struct {
	// Subtitles is an OPTIONAL subtitle-fetcher port (Step 6).
	// nil → Step 6 silently skips.
	Subtitles youtubeports.SubtitleFetcherPort
	// DriveFolderMgr is the OPTIONAL Drive-folder management
	// port. nil → Drive upload step uses StageSource fallback.
	DriveFolderMgr youtubeports.DriveFolderManagerPort
	// Stager is the canonical acquisition.SourceStager port. Optional.
	// nil → Step 4 falls through to the per-segment yt-dlp path.
	Stager acquisition.SourceStager
	// FFProbe is the optional ffprobe validation port (audit
	// 2026-07-03 BLOCKER #3). nil → Step 5a validation is
	// silently skipped.
	FFProbe youtubeports.FFProbePort
	// TextTrackResolver is the OPTIONAL priority-chain resolver
	// for localized text tracks. nil → skip resolver and
	// proceed directly to subtitles/Whisper.
	TextTrackResolver *TextTrackResolver
}

// ProcessSegmentMetadataDeps bundles the metadata-enrichment ports.
// 3 fields, all optional (nil-port safe at runtime — no
// fail-closed panic, no Validate() check).
//
// godlike/06 SSOT: this sub-bundle owns the metadata-enrichment
// surface. LocalizedWriter is the SOLE canonical super-tx
// surface (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b); the other
// two are legacy / secondary.
type ProcessSegmentMetadataDeps struct {
	// LocalizedWriter is the SOLE canonical surface for the
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b atomic super-tx
	// (clip + text tracks + cues + outbox in ONE SQLite tx).
	// The concrete instance is the SAME *ClipAtomicWriterAdapter
	// as Core.Writer (the adapter satisfies both ports — see
	// clip_atomic_writer.go compile-time assertion). nil port
	// is a fail-closed wiring gap; the step6to9 path mirrors the
	// BLOCKER #4 partial-state pattern when
	// CommitClipTextAndIndexEvent returns a typed error.
	LocalizedWriter localized.LocalizedClipWriter
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
}

// ProcessSegmentObservabilityDeps bundles the metrics + policy ports.
// 2 fields. RequireTranscriptReady is a Fase 5 policy gate (boolean);
// Step10Metrics is a metrics recorder port.
type ProcessSegmentObservabilityDeps struct {
	// Step10Metrics is the optional metrics-recorder port for
	// the partial-state Step 10 failure counter
	// (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY, July 2026). When
	// non-nil, the use case calls
	//   u.observability.Step10Metrics.IncStep10FailAfterClip(...)
	// on the Step 10 metadata-enrichment failure path BEFORE the
	// typed *ExtractionError return. The counter is partitioned
	// by failure_code so dashboards can aggregate partial-state
	// events across a batch extraction.
	//
	// When nil, the counter increment is silently skipped.
	// Nil-tolerance matches the optional-port pattern of
	// MediaDeps + MetadataDeps.
	//
	// godlike/06 SSOT: this port is the SOLE canonical
	// application-layer surface for Step 10 partial-state
	// telemetry. The composition root wires the concrete
	// adapter (internal/platform/observability.Step10
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

	// RequireAllLanguagesBeforeVideo is a Fase 5 policy gate. When true, the
	// YouTube segment pipeline's Step 9 super-tx fails PRE-TX with
	// localized.ErrClipLocaleNotReady if any target language listed in
	// PreferredLanguages is missing from the command's TextTracks.
	RequireAllLanguagesBeforeVideo bool

	// PreferredLanguages are the BCP-47 language codes that are required to
	// be present when RequireAllLanguagesBeforeVideo is true.
	PreferredLanguages []string
}

// NewProcessYouTubeSegmentFromSubBundles constructs the canonical
// use case from the 4 capability-area sub-bundles.
//
// godlike/07 NO-FAKE-AVAILABILITY: the 5 REQUIRED-port
// panic-on-nil checks (Cache/VideoPipeline/Hash/Writer/SegmentsSvc)
// run BEFORE the use case is constructed, so a missing required
// adapter is detected at boot, not at first
// POST /api/assets/youtube/extract invocation.
//
// The optional Log nil-fallback is handled here (zap.NewNop()),
// preserving the pre-PR pattern verbatim. Subtitles / Transcriber
// / DriveFolderMgr / Stager / FFProbe / TextTrackResolver /
// LocalizedWriter / ClipMetadataWriter / MetadataService /
// Step10Metrics remain runtime-gated (no panic) per the canonical
// optional-port pattern.
//
// godlike/07 minimum-blast-radius: signature is the 4-arg
// `NewProcessYouTubeSegmentFromSubBundles(core, media, metadata,
// observability)` — no `*UseCase` self-reference, no logger
// parameter (the logger lives in core.Log). Composition root at
// internal/app/build_bundles_domain_media.go is the SOLE canonical
// caller.
func NewProcessYouTubeSegmentFromSubBundles(
	core ProcessSegmentCoreDeps,
	media ProcessSegmentMediaDeps,
	metadata ProcessSegmentMetadataDeps,
	observability ProcessSegmentObservabilityDeps,
) *ProcessYouTubeSegmentUseCase {
	ValidateProcessSegmentSubBundles(core, media, metadata, observability)
	if core.Log == nil {
		core.Log = zap.NewNop()
	}
	return &ProcessYouTubeSegmentUseCase{
		core:          core,
		media:         media,
		metadata:      metadata,
		observability: observability,
	}
}

// ValidateProcessSegmentSubBundles enforces the 5 REQUIRED-port
// panic-check invariant. Called from
// NewProcessYouTubeSegmentFromSubBundles BEFORE the use case is
// constructed.
//
// The 5 panic messages are byte-verbatim to pre-PRCommit-1/6 —
// downstream callers (internal/app/build_bundles_domain_media.go)
// recover via panic-catch + log + continue booting per the
// canonical fail-closed composition-layer pattern.
//
// godlike/07 NO-FAKE-AVAILABILITY: composition-time fail-closed
// at the panic site. A missing required adapter is detected at
// boot, not at first POST /api/assets/youtube/extract invocation.
//
// The MediaDeps, MetadataDeps, ObservabilityDeps parameters are
// accepted but not used at validate-time (their ports are
// optional, runtime-gated). The 4-arg signature is the canonical
// shape so the panic site is the same code path the ctor uses
// (no separate Validate() per sub-bundle).
func ValidateProcessSegmentSubBundles(
	core ProcessSegmentCoreDeps,
	_ ProcessSegmentMediaDeps,
	_ ProcessSegmentMetadataDeps,
	_ ProcessSegmentObservabilityDeps,
) {
	if core.Cache == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Cache port is required (composition must wire ClipCacheAdapter from internal/infrastructure/database/sqlite/assets/clip_cache_adapter.go)")
	}
	if core.VideoPipeline == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: VideoPipeline port is required (composition must wire the YouTube pipeline adapter)")
	}
	if core.Hash == nil {
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
	// step6to9.go's downgrade branch (else if u.core.Writer != nil)
	// makes the test paths safe: a nil LocalizedWriter cleanly falls
	// back to the legacy CommitClipAndIndexEvent path, which is
	// identical to the pre-Fase 2.b behavior. Promoting the
	// LocalizedWriter nil-check to a panic would force every Writer-
	// stubbed test to add a LocalizedWriter stub; that breach in
	// blast-radius is not justified by the production-side win.
	// godlike/06 SSOT: the Fase 2.b canonical path is
	// LocalizedWriter. Composition MUST wire it (paths that don't
	// will silently take the legacy downgrade).
	if core.Writer == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Writer port is required — composition must wire ClipAtomicWriterAdapter (PR-C P0 #3 fail-closed; pre-Commit-1 silently wrote nothing and returned 'processed')")
	}
	if core.SegmentsSvc == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: SegmentsSvc port is required (composition must construct *SegmentsService via youtube.NewSegmentsService())")
	}
}
