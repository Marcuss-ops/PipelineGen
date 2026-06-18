package youtube

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/security"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"

	"go.uber.org/zap"
)

// videoExtractSem serializes YouTube clip extraction so only one video is
// processed at a time. Multiple videos in parallel cause "database is locked"
// errors because each segment writes to the same SQLite database.
var videoExtractSem = make(chan struct{}, 1)

// Extract processes a YouTube clip extraction request.
func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
	// Acquire the global video extraction semaphore — wait if another video
	// is currently being extracted. This ensures truly sequential processing
	// across all workers.
	select {
	case videoExtractSem <- struct{}{}:
		defer func() { <-videoExtractSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	startTime := time.Now()
	s.log.Info("YouTube Extract service called", zap.String("url", req.URL))

	// Apply configurable timeout if no deadline is set
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.cfg.Jobs.YouTubeExtractTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.cfg.Jobs.YouTubeExtractTimeout)*time.Second)
		defer cancel()
	}

	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil || videoID == "" {
		videoID = hashutil.MD5String(req.URL)[:12]
	}
	if canonical := canonicalYouTubeURL(req.URL, videoID); canonical != "" {
		req.URL = canonical
	}

	// ── Phase 1: Validation ───────────────────────────────────────
	if strings.TrimSpace(req.URL) == "" {
		return &ExtractResponse{OK: false, Error: "url is required"}, fmt.Errorf("url is required")
	}
	if err := security.ValidateDownloadURL(strings.TrimSpace(req.URL)); err != nil {
		return &ExtractResponse{OK: false, Error: err.Error()}, err
	}

	// Ensure we fetch video info once to use for classification and protagonist resolving
	var videoTitle string
	info, infoErr := s.GetVideoInfo(ctx, req.URL)
	if infoErr == nil && info != nil {
		videoTitle = info.Title
	}

	// Dynamic group/category classification using Ollama if group is not specified.
	// If the user provides an explicit FolderID, we still keep the auto-generated
	// video subfolder unless they explicitly provide a SubfolderName.
	if req.Destination == nil {
		req.Destination = &DestinationRequest{}
	}
	explicitSubfolder := strings.TrimSpace(req.Destination.SubfolderName) != ""
	hasExplicitFolder := req.Destination.FolderID != ""
	if !hasExplicitFolder && (req.Destination.Group == "" || req.Destination.Group == "general") {
		if videoTitle != "" {
			s.log.Info("no group specified, attempting auto-classification using video info", zap.String("url", req.URL))
			req.Destination.Group = s.classifyCategory(ctx, videoTitle)
			s.log.Info("auto-classified video category", zap.String("title", videoTitle), zap.String("group", req.Destination.Group))
		} else {
			req.Destination.Group = "general"
		}
	}

	// Dynamic segment generation via Ollama if empty
	if len(req.Segments) == 0 {
		s.log.Info("no segments provided, finding interesting segments automatically", zap.String("url", req.URL))
		autoSegments, err := s.findInterestingSegments(ctx, req.URL)
		if err != nil {
			s.log.Warn("failed to analyze segments", zap.Error(err))
			return &ExtractResponse{OK: false, Error: "no segments could be generated for this video"}, fmt.Errorf("findInterestingSegments failed: %w", err)
		}
		if len(autoSegments) == 0 {
			s.log.Warn("no interesting segments found for video", zap.String("url", req.URL))
			return &ExtractResponse{OK: false, Error: "no interesting segments found for this video — try a video with subtitles or chapters"}, nil
		}
		req.Segments = autoSegments
	}

	if req.Shuffle && len(req.Segments) > 1 {
		s.log.Info("shuffling segments for extraction as requested", zap.Int("count", len(req.Segments)))
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		r.Shuffle(len(req.Segments), func(i, j int) {
			req.Segments[i], req.Segments[j] = req.Segments[j], req.Segments[i]
		})
	}

	// Always enforce max segment duration: split any segments longer than 60s.
	// With MaxSegmentDuration=60, this ensures no clip exceeds the limit.
	req.Segments = splitLongSegments(req.Segments)

	resp := &ExtractResponse{
		OK:        true,
		SourceURL: strings.TrimSpace(req.URL),
		VideoID:   videoID,
		Stats: &ExtractStats{
			Requested: len(req.Segments),
		},
	}

	if len(req.Segments) > 20 {
		resp.OK = false
		resp.Error = "too many segments, max 20"
		return resp, fmt.Errorf("too many segments")
	}

	// ── Phase 2: Segment discovery ────────────────────────────────
	now := timeutil.FormatRFC3339(time.Now())
	monitoredSource := &models.MonitoredSource{
		ID:           "youtube_" + videoID,
		Source:       "youtube",
		ExternalID:   videoID,
		ExternalURL:  req.URL,
		Status:       "processing",
		LastSeenAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
		MetadataJSON: "{}",
	}
	if req.Destination != nil {
		monitoredSource.GroupName = req.Destination.Group
	}
	if s.monitoredRepo != nil {
		if err := s.monitoredRepo.UpsertSource(ctx, monitoredSource); err != nil {
			s.log.Error("Failed to upsert monitored source", zap.Error(err))
		}
	}

	// ── Phase 3: Drive + folder setup ────────────────────────────
	folderSlug := videoID
	group := "general"
	if req.Destination != nil && req.Destination.Group != "" {
		group = req.Destination.Group
	}

	// Folder naming must be stable and title-based. Do not depend on Ollama
	// for the directory name, otherwise the folder can change across retries.
	if videoTitle != "" {
		titleSlug := textutil.SlugifyWithMax(videoTitle, 80)
		if titleSlug != "" {
			folderSlug = titleSlug
			s.log.Info("used slugified title for folder",
				zap.String("title", videoTitle),
				zap.String("slug", folderSlug))
		}
	}

	folderSlug = strings.TrimPrefix(folderSlug, "yt_")

	// When SubfolderName is explicitly provided, override the folder slug to match.
	// This ensures that multiple videos targeting the same Drive subfolder share
	// the same local path, clip folder ID, and most importantly — the same manifest.
	// Without this, each video creates a separate local manifest and the combined
	// metadata.json on Drive would only contain the latest video's clips.
	if req.Destination != nil && req.Destination.SubfolderName != "" {
		subfolderName := strings.TrimPrefix(req.Destination.SubfolderName, "yt_")
		if subfolderName != "" && subfolderName != folderSlug {
			s.log.Info("using subfolder_name as folder slug for manifest merging",
				zap.String("original_slug", folderSlug),
				zap.String("subfolder_name", subfolderName))
			folderSlug = subfolderName
		}
	} else if hasExplicitFolder {
		// Build video slug: videoID + title-slug for unique, identifiable subfolders
		videoSlug := videoID
		if videoTitle != "" {
			titleSlug := textutil.SlugifyWithMax(videoTitle, 60)
			if titleSlug != "" {
				videoSlug = videoID + "-" + titleSlug
			}
		}
		req.Destination.SubfolderName = videoSlug
		req.Destination.CreateSubfolder = true
	}

	outDir := filepath.Join(s.cfg.Storage.DataDir, "media", "clips", group, folderSlug)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}
	s.log.Info("using stable folder for video", zap.String("folder", outDir), zap.String("video_id", videoID))

	// ── Drive destination resolution ──────────────────────────────
	driveFolderID, resolvedPath := s.resolveDriveDestination(ctx, req, folderSlug)

	// ── Folder info on response ───────────────────────────────────
	resp.Folder = &FolderInfo{
		ID:               fmt.Sprintf("clipfolder_youtube_%s", folderSlug),
		LocalFolderPath:  outDir,
		DriveFolderID:    driveFolderID,
		DriveFolderPath:  resolvedPath,
		ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
		ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
	}
	resp.DriveFolderID = driveFolderID
	resp.DriveFolderPath = resolvedPath

	// ── Clip folder loading ───────────────────────────────────────
	clipFolder := s.loadClipFolder(ctx, folderSlug, outDir, driveFolderID, resolvedPath, resp, req)

	// ── Manifest loading ──────────────────────────────────────────
	manifest := s.loadManifest(clipFolder, folderSlug, outDir, driveFolderID, resolvedPath, resp.SourceURL, resp.VideoID, explicitSubfolder)

	// ── Phase 4: Parallel segment processing ──────────────────────
	s.log.Info("processing segments",
		zap.Int("count", len(req.Segments)),
		zap.Int("concurrency", defaultConcurrency(req.Concurrency)),
		zap.String("video_id", videoID))

	concurrency := defaultConcurrency(req.Concurrency)
	sem := make(chan struct{}, concurrency) // semaphore limits parallel workers
	type segResult struct {
		index int
		item  ExtractItem
	}
	resultCh := make(chan segResult, len(req.Segments))
	var manifestMu sync.Mutex // protects manifest from concurrent updates

	var wg sync.WaitGroup
	for i, seg := range req.Segments {
		i, seg := i, seg  // capture per-iteration
		sem <- struct{}{} // acquire — blocks if concurrency already reached
		wg.Add(1)
		concurrent.SafeGoFunc("youtube-extract-segment", struct {
			Idx      int
			Seg      Segment
			Sem      chan struct{}
			Result   chan segResult
			Mu       *sync.Mutex
			Manifest *models.ClipManifest
		}{Idx: i, Seg: seg, Sem: sem, Result: resultCh, Mu: &manifestMu, Manifest: manifest}, func(arg struct {
			Idx      int
			Seg      Segment
			Sem      chan struct{}
			Result   chan segResult
			Mu       *sync.Mutex
			Manifest *models.ClipManifest
		}) {
			defer wg.Done()
			defer func() { <-arg.Sem }() // release semaphore

			// Each segment downloads its own specific range via processSegment
			item := s.processSegment(ctx, arg.Seg, req, resp, videoID, driveFolderID,
				resolvedPath, folderSlug, arg.Idx, "")

			// Serialize manifest updates to avoid race conditions
			arg.Mu.Lock()
			s.updateManifest(arg.Manifest, arg.Seg, item.ID, item, item.StartSeconds, item.EndSeconds,
				item.Duration, item.LocalPath, item.FileHash)
			arg.Mu.Unlock()

			arg.Result <- segResult{index: arg.Idx, item: item}
		})
	}

	// Close result channel when all workers finish
	concurrent.SafeGo("youtube-extract-results-closer", func() {
		wg.Wait()
		close(resultCh)
	})

	// Collect results in order and update response
	results := make([]segResult, len(req.Segments))
	for r := range resultCh {
		results[r.index] = r
	}

	s.log.Info("all segment workers completed",
		zap.Int("total", len(req.Segments)),
		zap.String("video_id", videoID))

	// Detect duplicate file hashes across clips — if two different clips share
	// the same MD5, the cutting/download pipeline produced identical files.
	// This is a bug (likely the pipeline returned the pre-downloaded full video
	// instead of the specific segment range). Log a warning so operators can
	// investigate.
	hashSeen := make(map[string]string) // file_hash → first clip_id
	for _, r := range results {
		item := r.item
		if item.FileHash == "" || item.Status == "failed" || item.Status == "skipped" {
			continue
		}
		if firstID, exists := hashSeen[item.FileHash]; exists {
			s.log.Warn("duplicate file hash detected — two clips have identical file content",
				zap.String("clip_a", firstID),
				zap.String("clip_b", item.ID),
				zap.String("file_hash", item.FileHash),
			)
		} else {
			hashSeen[item.FileHash] = item.ID
		}
	}

	for _, r := range results {
		item := r.item
		resp.Items = append(resp.Items, item)
		switch item.Status {
		case "failed":
			resp.Stats.Failed++
			resp.OK = false
		case "skipped":
			resp.Stats.Skipped++
		default:
			resp.Stats.Processed++
			// Trigger auto-indexing for successfully processed clips (async, non-blocking)
			s.triggerAutoIndexing(ctx, item.ID)
		}
	}

	// ── Phase 5: Manifest save ────────────────────────────────────
	s.log.Info("saving manifest and uploading to Drive",
		zap.Int("processed", resp.Stats.Processed),
		zap.Int("failed", resp.Stats.Failed),
		zap.Int("skipped", resp.Stats.Skipped))
	s.saveManifest(ctx, clipFolder, manifest, req, outDir)

	// ── MonitoredSource status update ─────────────────────────────
	s.updateMonitoredSourceStatus(ctx, monitoredSource, resp)

	s.log.Info("YouTube extraction complete",
		zap.String("url", req.URL),
		zap.Int("requested", resp.Stats.Requested),
		zap.Int("processed", resp.Stats.Processed),
		zap.Int("failed", resp.Stats.Failed),
		zap.Int("skipped", resp.Stats.Skipped),
		zap.String("drive_folder", resp.DriveFolderPath),
		zap.String("duration", time.Since(startTime).Round(time.Second).String()))

	return resp, nil
}
