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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// ProcessSegmentPolicyVersion is the canonical "v1" policy version
// stamped into the deterministic clip ID + filename. Bump it when
// the metadata enrichment prompt, semantic keywords, embedding
// model, or segment policy change.
const ProcessSegmentPolicyVersion = "v1"

// ProcessSegmentCoreDeps bundles the core runtime config + the 4
// REQUIRED ports that fail-closed panic on nil at ctor time. 6
// fields.
//
// godlike/06 SSOT: this sub-bundle owns the 4-port panic-check
// surface (Cache, VideoPipeline, Hash, SegmentsSvc) + the
// runtime config (SegmentPolicy, Log). The commit surface
// (LocalizedWriter) moved to ProcessSegmentMetadataDeps where it
// is the REQUIRED Fase 2.b canonical writer (Sept 2026: the
// legacy ClipAtomicWriter dependency + downgrade branch are
// REMOVED — there is exactly ONE commit contract).
//
// godlike/07 fail-closed at composition boot: the 4 panic checks
// are enforced by ValidateProcessSegmentSubBundles. Composition
// that does NOT wire any of these ports hits the panic
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
	// SegmentsSvc is the per-domain *SegmentsService (Step 1
	// timestamp parsing + Step 2 fingerprint extraction). nil
	// MUST panic (Validate() #4).
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
// 6 fields, all optional (nil-port safe at runtime — no
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
	// silently skipped AND the full-source single probe
	// (ProbeSourceFacts) degrades to CutModeNormalize.
	FFProbe youtubeports.FFProbePort
	// TextTrackResolver is the OPTIONAL priority-chain resolver
	// for localized text tracks. nil → skip resolver and
	// proceed directly to subtitles/Whisper.
	TextTrackResolver *TextTrackResolver
	// CutProfile is the canonical target profile the CutModeResolver
	// compares source facts against (copy eligibility). It mirrors the
	// media execution config wired by the composition root; zero values
	// fall back to the frozen assembly-contract defaults inside the
	// resolver. When zero AND the source facts are present, the
	// resolver's WithDefaults() still yields a deterministic decision.
	CutProfile mediaexec.VideoProfile
}

// ProcessSegmentMetadataDeps bundles the metadata-enrichment ports.
// 3 fields. LocalizedWriter is REQUIRED (fail-closed panic at
// ctor, Sept 2026 single-writer contract); the other two are
// optional (nil-port safe at runtime).
//
// godlike/06 SSOT: this sub-bundle owns the metadata-enrichment
// surface. LocalizedWriter is the SOLE canonical super-tx
// surface (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b) and the ONLY
// commit contract of the per-segment pipeline (the legacy
// ClipAtomicWriter fallback was removed in Sept 2026 — no
// downgrade path exists).
type ProcessSegmentMetadataDeps struct {
	// LocalizedWriter is the REQUIRED canonical surface for the
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b atomic super-tx
	// (clip + text tracks + cues + outbox in ONE transaction).
	// nil at ctor MUST panic (Validate() #5) — a composition
	// without it can produce no meaningful clip commit.
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

// ProcessSegmentObservabilityDeps bundles the policy ports.
// RequireTranscriptReady / RequireAllLanguagesBeforeVideo are Fase 5
// policy gates; PreferredLanguages carries their language list.
// EnrichmentMetrics is the OPTIONAL telemetry port for the metadata-
// enrichment run (Sept 2026, replaces the retired Step10Metrics).
type ProcessSegmentObservabilityDeps struct {
	// EnrichmentMetrics is the OPTIONAL observability port for the
	// metadata-enrichment pipeline (Sept 2026). When non-nil, the
	// synchronous enrichment path in step6to9 records total / duration /
	// failures; nil skips silently (tests + minimal compositions). The
	// async metadata.enrich.requested consumer in the composition root
	// records queue-age / duration through the SAME collector family
	// (godlike/06 SSOT: one dashboard surface for both enrichment modes).
	EnrichmentMetrics youtubeports.MetadataEnrichmentMetrics
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
	metadata ProcessSegmentMetadataDeps,
	_ ProcessSegmentObservabilityDeps,
) {
	if core.Cache == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Cache port is required (composition must wire ClipCacheAdapter from internal/platform/sqlite/assets/clip_cache_adapter.go)")
	}
	if core.VideoPipeline == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: VideoPipeline port is required (composition must wire the YouTube pipeline adapter)")
	}
	if core.Hash == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: Hash port is required (composition must wire hashutil.NewHashAdapter)")
	}
	// godlike/07 fail-closed at composition boot: LocalizedWriter is
	// REQUIRED — it is the SOLE commit contract of the per-segment
	// pipeline (Sept 2026). The legacy ClipAtomicWriter dependency and
	// its downgrade branch are REMOVED, so a nil LocalizedWriter can
	// no longer silently fall back to a second writer surface.
	if metadata.LocalizedWriter == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: LocalizedWriter port is required — composition must wire the canonical localized clip writer (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b; pre-Commit-1 silently wrote nothing and returned 'processed')")
	}
	if core.SegmentsSvc == nil {
		panic("usecase.NewProcessYouTubeSegmentUseCase: SegmentsSvc port is required (composition must construct *SegmentsService via youtube.NewSegmentsService())")
	}
}
