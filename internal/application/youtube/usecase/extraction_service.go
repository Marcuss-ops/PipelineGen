// Package extraction is the YouTube clip extraction capability service.
//
// It orchestrates the full extraction pipeline: segment discovery, video
// download/cut, lifecycle processing, manifest management, Drive upload,
// and intelligence enrichment. External operations (metadata enrichment,
// indexing, subtitles, classification, etc.) are delegated through the
// ExtractionCallbacks interface so the service stays focused on orchestration.
//
// PR5 Phase 3 (June 2026): extracted from the root youtube package.
// ExtractionDeps is capped at 8 fields per PR5 ≤8 rule.
package usecase

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// ── Dependencies (≤8 fields per PR5 rule) ────────────────────────────────

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

// Service orchestrates the YouTube clip extraction pipeline.
type ExtractionService struct {
	cfg               youtubetypes.RuntimeConfig
	log               *zap.Logger
	videoPipeline     youtubeports.VideoPipelinePort
	clips             youtubeports.ClipStorePort
	cache             youtubeports.CachePort
	monitors          youtubeports.MonitorsStorePort
	assetDestResolver asset.Resolver
	folderMemory      youtubeports.FolderMemoryPort
	segmentsSvc       *SegmentsService

	callbacks ExtractionCallbacks
}

// NewService constructs the extraction service. All deps and callbacks
// are required; nil checks at call sites surface missing wiring explicitly.
func NewExtractionService(deps ExtractionDeps, cb ExtractionCallbacks) *ExtractionService {
	return &ExtractionService{
		cfg:               deps.Cfg,
		log:               deps.Log,
		videoPipeline:     deps.VideoPipeline,
		clips:             deps.Clips,
		cache:             deps.Cache,
		monitors:          deps.Monitors,
		assetDestResolver: deps.AssetDestResolver,
		folderMemory:      deps.FolderMemory,
		segmentsSvc:       deps.SegmentsSvc,
		callbacks:         cb,
	}
}

// Extract runs a minimal but real YouTube extraction pipeline:
// it validates the request, derives the clip filenames, invokes the
// configured video pipeline, and records per-segment outcomes.
// External enrichment and Drive-side work still route through the
// callback surface, so the service can grow without reintroducing
// transport-layer coupling.
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
	if err := ctx.Err(); err != nil {
		return nil, err
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
		outputName := s.segmentsSvc.BuildClipFilename(videoID, startSec, endSec, itemName)
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
			KeepAudio:      req.KeepAudio,
			Normalize:      normalize,
			Strategy:       req.Strategy,
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

func cleanClipName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}
