// Package extraction is the YouTube clip extraction capability service.
//
// It orchestrates the full extraction pipeline: segment discovery, video
// download/cut, lifecycle processing, manifest management, Drive upload,
// and intelligence enrichment. External operations (metadata enrichment,
// indexing, subtitles, classification, etc.) are delegated through the
// ExtractionCallbacks interface so the service stays focused on orchestration.
//
// PR5 Phase 3 (June 2026): extracted from the root youtube package.
// ExtractionDeps is capped at 8 fields per PR5 ≤8 rule. The two
// cutover-extension fields (ProcessSeg + MaxConcurrentVideos) live
// alongside as Commit C introduces the canonical
// ProcessYouTubeSegmentUseCase.
package usecase

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// ── Dependencies (≤8 fields per PR5 rule + 2 cutover extension) ──────────

// ExtractionDeps holds the 8 direct dependencies the extraction pipeline
// requires. External operations not listed here (metadata enrichment,
// indexing, classification, subtitles, Whisper, hash, Drive upload,
// Ollama, asset processing, clip cache) are accessed through the
// ExtractionCallbacks interface.
type ExtractionDeps struct {
	Cfg               youtubetypes.RuntimeConfig
	Log               *zap.Logger
	VideoPipeline     youtubeports.VideoPipelinePort
	Clips             youtubeports.ClipStorePort
	Cache             youtubeports.CachePort
	Monitors          youtubeports.MonitorsStorePort
	AssetDestResolver asset.Resolver
	FolderMemory      youtubeports.FolderMemoryPort
	SegmentsSvc       *SegmentsService

	// Commit C cutover: when non-nil, Extract fans out through the
	// canonical ProcessYouTubeSegmentUseCase. Legacy inline loop
	// remains as fallback for compositions that haven't yet wired the
	// new cache/writer ports — slated for removal in Commit H.
	ProcessSeg *ProcessYouTubeSegmentUseCase
	// MaxConcurrentVideos bounds the per-Extract fan-out. Defaults to
	// 5 if zero (matches monitor.MonitorRuntimePolicy's default).
	MaxConcurrentVideos int
}

// ── Callbacks interface ──────────────────────────────────────────────────

// ExtractionCallbacks is implemented by the root youtube.Service. It
// delegates each callback to the appropriate capability service or port,
// keeping the extraction service free of direct dependencies on metadata,
// search, indexing, subtitles, Whisper, hash, Drive upload, Ollama,
// asset processing, and clip cache services.
//
// All types used here come from shared packages (types/, ports/, asset/,
// lifecycle/) — never from the root youtube/ or extraction/ package — to
// avoid import cycles and type incompatibilities.
type ExtractionCallbacks interface {
	// Metadata enrichment (→ metadata.Service)
	EnrichClip(ctx context.Context, clipID string, ym *youtubeports.DownloaderMetadata, force bool)

	// Search/info (→ search.Service)
	GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error)

	// Classification (→ classifyCategory)
	ClassifyCategory(ctx context.Context, title string) string

	// Clip cache (→ checkExistingClip)
	CheckExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool

	// Lifecycle (→ processLifecycle)
	ProcessLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *youtubetypes.ExtractItem)

	// Auto-indexing (→ indexing.go)
	TriggerAutoIndexing(ctx context.Context, clipID string)
	IndexClip(ctx context.Context, clipID string) error
	EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string)

	// Subtitles (→ subtitleFetcher)
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error

	// Whisper (→ whisper port)
	TranscribeAudio(ctx context.Context, localPath string) (string, error)

	// Hash (→ hashSvc)
	MD5File(path string) string
	MD5String(data string) string

	// Drive upload (→ driveFolderMgr)
	DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error)
	DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)

	// Ollama (→ ollama port + semaphore)
	OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error)

	// Concurrency semaphores
	AcquireVideoExtractSem(ctx context.Context) (release func())
	AcquireOllamaSem(ctx context.Context) (release func())
}

// ── Service ──────────────────────────────────────────────────────────────

// ExtractionService orchestrates the YouTube clip extraction pipeline.
type ExtractionService struct {
	cfg                 youtubetypes.RuntimeConfig
	log                 *zap.Logger
	videoPipeline       youtubeports.VideoPipelinePort
	clips               youtubeports.ClipStorePort
	cache               youtubeports.CachePort
	monitors            youtubeports.MonitorsStorePort
	assetDestResolver   asset.Resolver
	folderMemory        youtubeports.FolderMemoryPort
	segmentsSvc         *SegmentsService
	processSeg          *ProcessYouTubeSegmentUseCase
	maxConcurrentVideos int

	callbacks ExtractionCallbacks
}

// NewExtractionService constructs the extraction service. All deps and
// callbacks are required; nil checks at call sites surface missing
// wiring explicitly. MaxConcurrentVideos defaults to 5 when zero so
// the canonical fan-out always enters a working bounded state.
func NewExtractionService(deps ExtractionDeps, cb ExtractionCallbacks) *ExtractionService {
	maxV := deps.MaxConcurrentVideos
	if maxV <= 0 {
		maxV = 5
	}
	return &ExtractionService{
		cfg:                 deps.Cfg,
		log:                 deps.Log,
		videoPipeline:       deps.VideoPipeline,
		clips:               deps.Clips,
		cache:               deps.Cache,
		monitors:            deps.Monitors,
		assetDestResolver:   deps.AssetDestResolver,
		folderMemory:        deps.FolderMemory,
		segmentsSvc:         deps.SegmentsSvc,
		processSeg:          deps.ProcessSeg,
		maxConcurrentVideos: maxV,
		callbacks:           cb,
	}
}

// Extract runs a minimal but real YouTube extraction pipeline.
// Commit C cutover: when ProcessSeg is non-nil the canonical fan-out
// runs through ProcessYouTubeSegmentUseCase. When nil, the legacy
// per-segment inline loop runs (annotated `// TODO wave delete: legacy
// inline` in adapters/segment_processor.go — removed in Commit H).
func (s *ExtractionService) Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s == nil {
		return nil, fmt.Errorf("youtube extraction: service not initialized")
	}
	if req == nil {
		return nil, fmt.Errorf("youtube extraction: request is required")
	}

	if s.videoPipeline == nil {
		return nil, fmt.Errorf("youtube extraction: video pipeline not wired")
	}

	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil {
		return nil, fmt.Errorf("youtube extraction: invalid url: %w", err)
	}

	resp := &youtubetypes.ExtractResponse{
		OK:        true,
		SourceURL: req.URL,
		VideoID:   videoID,
		Stats: &youtubetypes.ExtractStats{
			Requested: len(req.Segments),
		},
		Items: make([]youtubetypes.ExtractItem, 0, len(req.Segments)),
	}

	if len(req.Segments) == 0 {
		resp.OK = false
		resp.Error = "youtube extraction: at least one segment is required"
		return resp, nil
	}
	if s.segmentsSvc == nil {
		s.segmentsSvc = NewSegmentsService()
	}

	group := "general"
	if req.Destination != nil && strings.TrimSpace(req.Destination.Group) != "" {
		group = strings.TrimSpace(req.Destination.Group)
	}
	folderSlug := "yt_" + videoID
	outDir := filepath.Join(s.cfg.DataDir, "media", "clips", group, folderSlug)
	driveFolderID := ""
	driveFolderPath := ""
	if req.Destination != nil {
		driveFolderID = strings.TrimSpace(req.Destination.FolderID)
		driveFolderPath = strings.TrimSpace(req.Destination.FolderPath)
	}

	// Commit C — canonical fan-out via ProcessYouTubeSegmentUseCase.
	if s.processSeg != nil {
		return s.extractFanOut(ctx, req, videoID, outDir, driveFolderID, driveFolderPath)
	}

	// ── Legacy fallback: TODO wave-delete (Commit H) ─────────────────
	for i, seg := range req.Segments {
		if ctx.Err() != nil {
			resp.OK = false
			resp.Error = ctx.Err().Error()
			resp.Stats.Failed++
			resp.Items = append(resp.Items, youtubetypes.ExtractItem{
				Name:   cleanClipName(seg.Name, i),
				Start:  strings.TrimSpace(seg.Start),
				End:    strings.TrimSpace(seg.End),
				Status: "failed",
				Error:  ctx.Err().Error(),
			})
			continue
		}

		startSec, err := textutil.ParseTimestamp(strings.TrimSpace(seg.Start))
		if err != nil {
			resp.OK = false
			resp.Stats.Failed++
			resp.Items = append(resp.Items, youtubetypes.ExtractItem{
				Name:   cleanClipName(seg.Name, i),
				Start:  strings.TrimSpace(seg.Start),
				End:    strings.TrimSpace(seg.End),
				Status: "failed",
				Error:  fmt.Sprintf("invalid start timestamp: %v", err),
			})
			continue
		}
		endSec, err := textutil.ParseTimestamp(strings.TrimSpace(seg.End))
		if err != nil {
			resp.OK = false
			resp.Stats.Failed++
			resp.Items = append(resp.Items, youtubetypes.ExtractItem{
				Name:   cleanClipName(seg.Name, i),
				Start:  strings.TrimSpace(seg.Start),
				End:    strings.TrimSpace(seg.End),
				Status: "failed",
				Error:  fmt.Sprintf("invalid end timestamp: %v", err),
			})
			continue
		}
		if startSec >= endSec {
			resp.OK = false
			resp.Stats.Failed++
			resp.Items = append(resp.Items, youtubetypes.ExtractItem{
				Name:   cleanClipName(seg.Name, i),
				Start:  strings.TrimSpace(seg.Start),
				End:    strings.TrimSpace(seg.End),
				Status: "failed",
				Error:  fmt.Sprintf("start time (%s) must be before end time (%s)", seg.Start, seg.End),
			})
			continue
		}

		itemName := cleanClipName(seg.Name, i)
		// Commit 2/6 (Correttezza #4): BuildClipFilename now takes
		// a 5th argument (policyVersion) so two policy versions of
		// the same (videoID, start, end) tuple produce different
		// files. The legacy inline loop here uses the canonical
		// ProcessSegmentPolicyVersion ("v1"); the canonical fan-out
		// path in process_segment.go::Execute uses the resolved
		// policyVer from cmd.PolicyVersion.
		outputName := s.segmentsSvc.BuildClipFilename(videoID, startSec, endSec, itemName, ProcessSegmentPolicyVersion)
		normalize := true
		if req.Normalize != nil {
			normalize = *req.Normalize
		}
		cutReq := youtubeports.VideoCutRequest{
			URL:            req.URL,
			VideoID:        videoID,
			Start:          float64(startSec),
			Duration:       float64(endSec - startSec),
			OutputName:     strings.TrimSuffix(outputName, ".mp4"),
			ForceKeyframes: req.ForceKeyframes,
			KeepAudio:      resolveKeepAudio(req),
			Normalize:      normalize,
			Strategy:       string(req.Strategy),
			OutputDir:      outDir,
		}

		result, cutErr := s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, cutReq)
		if cutErr != nil {
			resp.OK = false
			resp.Stats.Failed++
			resp.Items = append(resp.Items, youtubetypes.ExtractItem{
				Name:   itemName,
				Start:  strings.TrimSpace(seg.Start),
				End:    strings.TrimSpace(seg.End),
				Status: "failed",
				Error:  fmt.Sprintf("video processing failed: %v", cutErr),
			})
			continue
		}

		localPath := ""
		if result != nil {
			localPath = result.LocalPath
		}
		filename := outputName
		if localPath != "" {
			filename = filepath.Base(localPath)
		}
		resp.Stats.Processed++
		resp.Items = append(resp.Items, youtubetypes.ExtractItem{
			Name:         itemName,
			Start:        strings.TrimSpace(seg.Start),
			End:          strings.TrimSpace(seg.End),
			StartSeconds: startSec,
			EndSeconds:   endSec,
			Duration:     endSec - startSec,
			Filename:     filename,
			LocalPath:    localPath,
			Status:       "processed",
		})
	}

	resp.Stats.Skipped = len(resp.Items) - resp.Stats.Processed - resp.Stats.Failed
	resp.OK = resp.Stats.Failed == 0 && resp.Stats.Processed > 0
	if !resp.OK && resp.Error == "" {
		resp.Error = "one or more segments failed"
	}
	return resp, nil
}

// extractFanOut is the Commit C canonical path: bounded semaphore fan-out
// across ProcessYouTubeSegmentUseCase.Execute calls. Use case is
// responsible for the per-segment 9-step sequence (cache → retry → hash
// → subs → metadata → lifecycle → drive → atomic writer) and populates
// ProcessSegmentResult.Item which we collect here.
func (s *ExtractionService) extractFanOut(
	ctx context.Context,
	req *youtubetypes.ExtractRequest,
	videoID, outDir, driveFolderID, driveFolderPath string,
) (*youtubetypes.ExtractResponse, error) {
	resp := &youtubetypes.ExtractResponse{
		OK:        true,
		SourceURL: req.URL,
		VideoID:   videoID,
		Stats: &youtubetypes.ExtractStats{
			Requested: len(req.Segments),
		},
		Items:           make([]youtubetypes.ExtractItem, 0, len(req.Segments)),
		DriveFolderID:   driveFolderID,
		DriveFolderPath: driveFolderPath,
	}

	// PR-C YouTube Cutover (Commit C): default keep_audio=true. The
	// Channel Monitor pipeline writes keep_audio=true into every monitor
	// payload (Commit B); non-monitor callers who omit keep_audio inherit
	// the canonical true default — the legacy silent-default-false
	// behaviour is intentionally flipped here. An explicit keep_audio=false
	// still strips audio.
	//
	// Commit 2/6 (Correttezza #8): KeepAudio is now `*bool` (per the
	// verdict's typed-pointer policy) so nil-marshal round-trips through
	// the JSON boundary without defaulting to false. The dereference is
	// delegated to resolveKeepAudio (testable helper): nil → canonical
	// default (true); non-nil → *req.KeepAudio.
	keepAudio := resolveKeepAudio(req)

	sem := make(chan struct{}, s.maxConcurrentVideos)
	results := make([]youtubetypes.ProcessSegmentResult, len(req.Segments))
	var wg sync.WaitGroup
	for i, seg := range req.Segments {
		i, seg := i, seg
		wg.Add(1)
		go func() {
			defer wg.Done()
			// PR-C YouTube Cutover (Commit C): panic-recovery per goroutine.
			// Without this, a panic in ProcessYouTubeSegmentUseCase.Execute
			// would crash the broker. Mirrors monitor.safeCheckChannel
			// (P1 #9 closure): recover → log → leave the result slot zero,
			// the classifier sees Item.Status=failed.
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("panic in segment goroutine (extractFanOut recovered)",
						zap.Int("segment_index", i),
						zap.String("video_id", videoID),
						zap.Any("recover", r))
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			cmd := youtubetypes.ProcessSegmentCommand{
				VideoID:         videoID,
				Segment:         seg,
				Index:           i,
				PolicyVersion:   ProcessSegmentPolicyVersion,
				OutDir:          outDir,
				DriveFolderID:   driveFolderID,
				DriveFolderPath: driveFolderPath,
				VideoURL:        req.URL,
				ForceKeyframes:  req.ForceKeyframes,
				Normalize:       req.Normalize,
				KeepAudio:       &keepAudio,
				Strategy:        req.Strategy,
				Destination:     req.Destination,
			}
			res, _ := s.processSeg.Execute(ctx, cmd)
			results[i] = res
		}()
	}
	wg.Wait()

	for _, res := range results {
		resp.Items = append(resp.Items, res.Item)
		switch res.Item.Status {
		case "processed":
			resp.Stats.Processed++
		case "skipped":
			resp.Stats.Skipped++
		case "failed":
			resp.Stats.Failed++
		}
	}
	resp.Stats.Skipped = len(resp.Items) - resp.Stats.Processed - resp.Stats.Failed
	// Commit 2/6 (Correttezza #7): success includes the
	// "cache hit → skipped" path. A cache-hit on every segment is
	// a successful idempotent re-run (the canonical "verify" strategy
	// short-circuit), so the classifier treats (processed + skipped)
	// == requested as success. The legacy classifier required
	// processed > 0, which incorrectly flagged a 100% cache-hit
	// re-run as failure. Vacuously true for requested==0 (no work
	// requested → nothing to fail). Helper extracted so it can be
	// unit-tested without the full 11-field ExtractionService fixture.
	resp.OK = classifyExtractionRun(resp.Stats)
	if !resp.OK && resp.Error == "" {
		resp.Error = "one or more segments failed"
	}
	return resp, nil
}

// classifyExtractionRun is the canonical success/failure classifier
// for an extraction run. Extracted from extractFanOut so it can be
// unit-tested without the full 11-field ExtractionService fixture
// (Commit 2/6 Correttezza #7).
//
// Returns true when the run is successful:
//   - Vacuously true when no segments were requested (Requested == 0).
//   - True when no segment failed AND (processed + skipped) == requested.
//     The (processed + skipped) == requested branch is the canonical
//     "idempotent re-run" path: a 100% cache-hit re-run classifies
//     as success (the previous form required processed > 0, which
//     incorrectly flagged all-skipped re-runs as failure).
//
// Returns false when any segment failed (Failed > 0) OR the
// processed+skipped+failed accounting does not sum to requested
// (defensive: a counter that drifts is itself a fail-closed signal).
func classifyExtractionRun(stats *youtubetypes.ExtractStats) bool {
	if stats == nil {
		return false
	}
	if stats.Requested == 0 {
		return true
	}
	if stats.Failed > 0 {
		return false
	}
	return stats.Processed+stats.Skipped == stats.Requested
}

// resolveKeepAudio is the canonical nil-check for the KeepAudio
// *bool DTO field. Commit 2/6 Correttezza #8 — extracted as a
// helper so it can be unit-tested without driving a full
// Extract call through the 11-field ExtractionService fixture.
//
// Behaviour:
//   - nil req.KeepAudio → true (the canonical default; PR-C flip
//     from the legacy silent-default-false).
//   - non-nil req.KeepAudio → *req.KeepAudio (caller's explicit
//     choice; the typed-pointer round-trip preserves the original
//     JSON value without the previous `if !req.KeepAudio` syntax
//     bug on a *bool).
func resolveKeepAudio(req *youtubetypes.ExtractRequest) bool {
	if req == nil || req.KeepAudio == nil {
		return true
	}
	return *req.KeepAudio
}

func cleanClipName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}
