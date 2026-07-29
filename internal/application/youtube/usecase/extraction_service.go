// Package usecase — extraction_service.go: THIN orchestration surface
// for the YouTube clip extraction pipeline.
//
// PR-GODOBJ-1 (July 2026): the legacy inline per-seg loop was REMOVED
// per godlike/07 fail-closed (no fake-availability: ProcessSeg is REQUIRED
// at composition time and the silent fallback path is physically gone).
// The canonical 9-step per-segment pipeline (process_segment.go) is the
// ONLY processor invoked; Extract's per-segment logic + concurrency live
// in extraction_fanout.go, normalization helpers live in extraction_request.go,
// destination resolvers in extraction_destination.go, and stats + classifier
// in extraction_result.go. The original 511-LoC god service is now a thin
// orchestrator.
//
// Split topology per godlike/06 one-owner-per-fact:
//   - extraction_request.go      — pure validation/normalization helpers
//   - extraction_destination.go  — pure outDir + Drive dest resolvers
//   - extraction_fanout.go       — bounded-concurrency goroutine dispatch
//   - extraction_result.go       — pure aggregator + success classifier
//
// ExtractionCallbacks (the inbound port for the orchestrator) lives in
// service.go alongside the package composition root per godlike/06 SSOT.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	assetdomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// ExtractionDeps is the canonical deps bundle for the extraction
// pipeline. ProcessSeg is REQUIRED (panic fail-closed at ctor; godlike/07).
// LegacyCompositionDeps holds the legacy CompositionState wiring fields
// that are kept for back-compat but not used in the canonical path.
// Grouped into a sub-struct to keep ExtractionDeps under the archcheck
// 8-field cap.
type LegacyCompositionDeps struct {
	VideoPipeline youtubeports.VideoPipelinePort
	Clips         youtubeports.ClipStorePort
	Cache         youtubeports.CachePort
	Monitors      youtubeports.MonitorsStorePort
}

type ExtractionDeps struct {
	Cfg                 youtubetypes.RuntimeConfig
	Log                 *zap.Logger
	Legacy              LegacyCompositionDeps // reserved: legacy wiring kept for CompositionState back-compat (not used in canonical path)
	AssetDestResolver   assetdomain.Resolver
	FolderMemory        youtubeports.FolderMemoryPort
	SegmentsSvc         *SegmentsService              // auto-constructed if nil (lazy-init)
	ProcessSeg          *ProcessYouTubeSegmentUseCase // REQUIRED (godlike/07 fail-closed)
	MaxConcurrentVideos int
}

// ExtractionService orchestrates the canonical per-segment pipeline.
// Per-segment dispatch + concurrency live in extraction_fanout.go.
// This struct is intentionally slim — only the fields the canonical
// path ACTUALLY reads are stored (VideoPipeline/Clips/Cache/Monitors
// scaffolding fields are kept on ExtractionDeps for CompositionState
// wiring back-compat but not stored on Service; see PR-GODOBJ-1 honest-
// limitation disclosure in CHANGELOG.md).
type ExtractionService struct {
	cfg                 youtubetypes.RuntimeConfig
	log                 *zap.Logger
	segmentsSvc         *SegmentsService
	processSeg          *ProcessYouTubeSegmentUseCase
	maxConcurrentVideos int
	callbacks           ExtractionCallbacks
}

// NewExtractionService constructs the canonical extraction orchestrator.
// ProcessSeg MUST be non-nil (godlike/07 fail-closed: a composition
// that fails to wire the canonical per-segment pipeline MUST panic at
// boot rather than silently run the legacy inline loop — the legacy
// loop was physically removed in PR-GODOBJ-1 so a nil ProcessSeg can
// no longer produce any meaningful output).
func NewExtractionService(deps ExtractionDeps, cb ExtractionCallbacks) *ExtractionService {
	if deps.ProcessSeg == nil {
		panic("usecase.NewExtractionService: ProcessSeg is required (composition must wire ProcessYouTubeSegmentUseCase; nil wires panic per godlike/07 fail-closed; legacy inline loop removed in PR-GODOBJ-1)")
	}
	maxV := deps.MaxConcurrentVideos
	if maxV <= 0 {
		maxV = 5
	}
	return &ExtractionService{
		cfg:                 deps.Cfg,
		log:                 deps.Log,
		segmentsSvc:         ensureSegmentsService(deps.SegmentsSvc),
		processSeg:          deps.ProcessSeg,
		maxConcurrentVideos: maxV,
		callbacks:           cb,
	}
}

// Extract is the canonical entry point for the YouTube extraction
// pipeline. Preflights (ctx / nil / URL / segment presence) are
// inlined; per-segment dispatch + concurrency + result aggregation
// live in extraction_fanout.go (via extractFanOut).
//
// Behaviour:
//   - ProcessSeg is non-nil (ctor panic guarantees this; godlike/07).
//   - empty Segments → returns an ExtractResponse with OK=false and
//     Error set to "youtube extraction: at least one segment is required".
//   - URL parse failure returns ErrInvalidURL.
//   - successful preflight → delegates to extractFanOut.
func (s *ExtractionService) Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s == nil {
		return nil, fmt.Errorf("youtube extraction: service not initialized")
	}
	if req == nil || req.URL == "" {
		return nil, fmt.Errorf("youtube extraction: URL is required")
	}
	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil {
		return nil, fmt.Errorf("youtube extraction: invalid url: %w", err)
	}
	if len(req.Segments) == 0 {
		return &youtubetypes.ExtractResponse{
			OK: false, SourceURL: req.URL, VideoID: videoID,
			Error: "youtube extraction: at least one segment is required",
		}, nil
	}
	dest := resolveDestination(req)
	outDir := resolveOutDir(s.cfg.DataDir, videoID, canonicalGroup(req))
	return s.extractFanOut(ctx, req, videoID, outDir, dest.FolderID, dest.FolderPath)
}
