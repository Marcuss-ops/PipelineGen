// Package youtube — orchestrator public API (CPR-CC-6 split, June 2026).
//
// This file holds the public-facing methods of the YouTube orchestrator:
// job registration, persistence dispatch, topic search, video info,
// extraction facade, drive helpers, and utility accessors.
// Service struct + constructor are in service.go.
// ExtractionCallbacks implementation is in callbacks.go.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytjobs "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/jobs"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ── Persistence — single canonical writer (PR1.6) ──────────────────────

// dispatchOrIndex writes a freshly-cut clip to the canonical asset store.
//
// The previous triple fallback (assetRepo → disp.EnqueueAndIndex →
// clipsRepo.Upsert) has been removed in PR1.6. AssetRepo is the SOLE
// writer and emits the asset.upserted outbox event atomically (PR12b
// semantics). If AssetRepo is not wired the call returns an explicit
// error so callers see the missing dependency rather than experiencing
// a silent no-op.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *asset.Asset, _ string) error {
	if clip == nil {
		return fmt.Errorf("youtube.dispatchOrIndex: nil clip")
	}
	// typed-nil guard: portutil.IsNilPort catches (*Concrete)(nil) casts
	// to interface that pass == nil. Composition audit (June 2026) confirmed
	// all adapter constructors return bare nil, so this is defensive.
	if s.assetRepo == nil || portutil.IsNilPort(s.assetRepo) {
		return fmt.Errorf("youtube: canonical assetRepo not wired — composition root must include AssetRepo in ServiceDeps")
	}
	return s.assetRepo.Upsert(ctx, clip)
}

// ── Job wiring (composition root calls this once) ─────────────────────

func (s *Service) RegisterHandler(jobsSvc *jobtools.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.TypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", jobservice.TypeYouTubeClipExtract))

		// The receiver method s.HandleRebuildSearchTextJob (defined below)
		// wraps the canonical ytjobs.HandleRebuildSearchTextJob and lazily
		// reconstructs RebuildDeps from this Service's runtime state at
		// invocation time (Clips.DB → DB, log, Indexer port, metadata
		// EnrichClip forwarder). Registering the receiver directly keeps a
		// single registration site for the rebuild_search_text job type.
		if s.clips != nil {
			jobsSvc.RegisterHandler(jobservice.TypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
			s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.TypeYouTubeRebuildST))
		}
	}
}

// HandleRebuildSearchTextJob wraps the free function form of ytjobs.HandleRebuildSearchTextJob
// (which expects a RebuildDeps dep struct as its first arg) into the method-form signature
// expected by jobtools.Service.RegisterHandler (ctx, j, tools) -> (map, error). The dep
// surface (clip lister, Log, Indexer, Enricher closure) is reconstructed lazily from
// the orchestrator's runtime state at job-invocation time so the composition root does not
// have to thread a fifth dep through ServiceDeps for this job type specifically.
//
// TODO(wave14-followup): interface alignment between orchestrator runtime fields and the
// ytjobs.RebuildDeps struct (specifically ClipIndexerPort vs jobs.ClipIndexer, plus the
// `meta any` closure-cast to `*youtubeports.DownloaderMetadata`) needs verification once a
// real rebuild_search_text job is exercised end-to-end.
func (s *Service) HandleRebuildSearchTextJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	deps := ytjobs.RebuildDeps{
		Log:     s.log,
		Indexer: s.indexer,
		Clips:   s.clips,
		Enricher: func(ctx context.Context, clipID string, meta any, force bool) {
			if s.metadata == nil {
				return
			}
			var m *youtubeports.DownloaderMetadata
			if meta != nil {
				m, _ = meta.(*youtubeports.DownloaderMetadata)
			}
			s.metadata.EnrichClip(ctx, clipID, m, force)
		},
	}
	return ytjobs.HandleRebuildSearchTextJob(deps, ctx, j, tools)
}

// ── Public helpers ─────────────────────────────────────────────────────

// GetOrCreateChannelFolder resolves the Drive folder for a channel via the
// DriveFolderManagerPort. The previous dummy GetOrCreateChannelFolder
// fallback to a raw driveclient (concrete Drive SDK) has been removed.
func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return parentFolderID, fmt.Errorf("youtube: drive folder manager not wired — composition root must include DriveFolderMgr in ServiceDeps")
	}
	folderID, err := s.driveFolderMgr.GetOrCreateFolder(ctx, channelName, parentFolderID)
	if err != nil {
		return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
	}
	s.log.Info("channel folder resolved",
		zap.String("channel", channelName),
		zap.String("folder_id", folderID),
		zap.String("parent", parentFolderID))
	return folderID, nil
}

// DownloadAndCut delegates to the VideoPipeline port (no longer calls
// concrete videomuscles.Pipeline from application via this method).
func (s *Service) DownloadAndCut(ctx context.Context, req youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	if isUnavailablePort(s.videoPipeline) {
		return nil, fmt.Errorf("youtube: video pipeline not wired")
	}
	return s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, req)
}

// Config returns the resolved runtime configuration (for callers that need
// to read it without taking a direct dependency on the config loader).
func (s *Service) Config() youtubetypes.RuntimeConfig {
	return s.cfg
}

// MD5File returns the MD5 hex digest of the file at path via the
// HashServicePort. Best-effort: a port error falls back to the local
// helper with a debug log so operators can see when the configured port
// silently misbehaves (e.g., misconfigured remote hash service).
//
// PR5 Phase 3: exported for ExtractionCallbacks compatibility.
func (s *Service) MD5File(path string) string {
	if !isUnavailablePort(s.hashSvc) {
		h, err := s.hashSvc.MD5File(path)
		if err == nil {
			return h
		}
		s.log.Debug("hashSvc.MD5File failed, falling back to local helper",
			zap.String("path", path),
			zap.Error(err))
	}
	return fallbackMD5File(path)
}

// md5File is the legacy private name kept for internal callers.
func (s *Service) md5File(path string) string { return s.MD5File(path) }

// MD5String returns the MD5 hex digest of s via the HashServicePort.
// PR5 Phase 3: exported for ExtractionCallbacks compatibility.
func (s *Service) MD5String(data string) string {
	if !isUnavailablePort(s.hashSvc) {
		return s.hashSvc.MD5String(data)
	}
	return fallbackMD5String(data)
}

// md5String is the legacy private name kept for internal callers.
func (s *Service) md5String(data string) string { return s.MD5String(data) }

// ── Typed-nil guard helpers ──────────────────────────────────────────

// isUnavailablePort returns true when port is nil (either bare nil or a
// typed-nil interface holding a nil concrete pointer). Use this for
// required ports that MUST be wired at composition time.
func isUnavailablePort(port any) bool {
	return port == nil || portutil.IsNilPort(port)
}

// ── CPR-CC-6 Phase 2 (June 2026): topic search absorbed into search capability ──
//
// The canonical "topic search" entry point lives on the search
// capability service (Service.TopicSearch, defined in search_topic.go).
// The orchestrator exposes Service.SearchByTopicWithFilter as a 1-line
// forwarder, satisfying the searcher port surfaced by
// internal/application/assets/providers/youtube. The topic scorers +
// TopicSearchResponse / TopicSearchResult types are package-local to
// internal/application/youtube/usecase (consumed as
// *youtubesrc.TopicSearchResponse by the adapter).
//
// Returns an explicit error when the search capability is not wired
// (composition root must include SearchRunner + Log in ServiceDeps so
// NewService wires Service.search).

// SearchByTopicWithFilter is the single canonical YouTube search entry
// point at the application-layer boundary. It ranks YouTube search
// results with an optional publishedAfter date filter.
//
// query: non-empty trimmed search string.
// limit: clamps to [1, 50]; defaults to 10 when <= 0.
// sortMode: forwarded verbatim to SearchLive; "" means "no preference".
// publishedAfter: RFC3339 date string (e.g. "2025-01-01T00:00:00Z") or
// "" for no filter. When set, only videos uploaded AFTER this date
// remain in the response.
func (s *Service) SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*TopicSearchResponse, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.TopicSearch(ctx, query, limit, sortMode, publishedAfter)
}

// ── GetVideoInfo forwarding ─────────────────────────────────────────────
//
// GetVideoInfo fetches full YouTube metadata for the given URL by
// forwarding to the canonical VideoMetadataFetcherPort (which exposes
// GetVideoMetadata(ctx, videoURL) with the same return type). The
// isUnavailablePort guard mirrors sibling forwarding methods
// (DriveUploadFileIfChanged, OllamaSimpleGenerate) to surface a typed
// error instead of nil-deref-panic when metaFetcher is not wired at
// composition time.
//
// Fulfils the extraction.ExtractionCallbacks.GetVideoInfo interface
// requirement on *Service. Merged into CPR-CC-6 Phase 2 (June 2026)
// alongside the topic-search forwarder above; both sides appended at
// end-of-file in upstream + local histories and were combined manually
// per AGENTS.md Rebase-Conflict Lesson rule 3.
func (s *Service) GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error) {
	if isUnavailablePort(s.metaFetcher) {
		return nil, fmt.Errorf("youtube: metaFetcher port not wired")
	}
	return s.metaFetcher.GetVideoMetadata(ctx, url)
}

// NOTE: Service.Extract is owned by extract.go (the canonical full
// pipeline impl). The orchestrator.go facade that forwarded to
// s.extraction.Extract was removed because (a) it duplicated the
// extract.go declaration on the same receiver and (b) the
// *ExtractionService.Extract method is not part of PR5 Phase 3's
// contract — orchestrators and callers route through Service.Extract
// directly. Call sites (e.g. monitor/process_video.go) keep working
// against (*Service).Extract unchanged.

// PrewarmHotVideoMetadataCache forwards to the search capability service.
// Callers (currently internal/app/lifecycle.go) call ytSvc.PrewarmHotVideoMetadataCache(ctx)
// expecting this method on the orchestrator. The actual implementation lives on
// *Service (PR5 Phase 2 capability extraction, set inside NewService when
// deps.SearchRunner is wired); this facade restores the dependency surface from the
// app-layer composition root's perspective.
//
// SearchRunner + Log on ServiceDeps are required; both checked in NewService before
// wiring s.search.
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.search == nil {
		return fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.search.PrewarmHotVideoMetadataCache(ctx)
}

// Extract is the canonical clip-extraction entry point. The full pipeline
// implementation lives in adapters/manifest_mgr.go + segment_processor.go.
// This facade delegates to the extraction capability service.
//
// Phase 1b (June 2026): the inline implementation was removed from usecase/
// because it referenced 7+ private methods from adapters.Service. The thin
// facade preserves the call-site contract; the extraction service owns
// the real pipeline.
func (s *Service) Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	if s.extraction == nil {
		return nil, fmt.Errorf("youtube: extraction capability not wired (composition root must include extraction deps in ServiceDeps)")
	}
	return s.extraction.Extract(ctx, req)
}

// fallbackMD5File delegates to the canonical implementation in tagutil.
func fallbackMD5File(path string) string {
	return youtubetypes.FallbackMD5File(path)
}

// fallbackMD5String delegates to the canonical implementation in tagutil.
func fallbackMD5String(data string) string {
	return youtubetypes.FallbackMD5String(data)
}

// SearchLive performs a live YouTube search via the search runner port.
// Phase 1b stub: returns empty results. Full implementation was in adapters/.
func (s *Service) SearchLive(ctx context.Context, query string, limit int, sortMode string) ([]asset.Asset, error) {
	if isUnavailablePort(s.searchRunner) {
		return nil, nil
	}
	// Phase 1c TODO: restore real implementation
	_ = query
	_ = limit
	_ = sortMode
	return nil, nil
}

// HandleJob processes a YouTube clip extraction job from the job queue.
// Phase 1b stub: delegates to the extraction capability.
func (s *Service) HandleJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	return nil, fmt.Errorf("youtube: HandleJob not yet wired (Phase 1b) — use extraction capability directly")
}
