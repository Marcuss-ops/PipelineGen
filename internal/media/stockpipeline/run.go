package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Run executes the full stock pipeline: resolve sources, download, extract clips,
// apply overlay effects, render chunks, upload to Drive, and index assets.
// It reads all video parameters from cfg.Video for codec consistency.
func (s *Service) Run(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	input.SearchQueries = expandSearchQueries(input.SearchQueries, s.log)

	start := time.Now()
	s.log.Info("compilation pipeline start",
		zap.Strings("queries", input.SearchQueries),
		zap.Strings("direct_urls", input.DirectURLs),
		zap.Int("total_minutes", input.TotalMinutes),
		zap.Int("chunk_duration_override", input.ChunkDuration),
		zap.String("subfolder", input.Subfolder),
		zap.String("folder_name", input.FolderName),
		zap.String("folder_id", input.FolderID),
	)

	chunkDur := input.ChunkDuration
	if chunkDur <= 0 {
		chunkDur = s.pcfg.ChunkDuration
	}

	clipDurOverride := s.cfg.Video.WithDefaults().ClipDuration
	if input.ClipDuration > 0 {
		clipDurOverride = input.ClipDuration
	}

	s.log.Info("stock timing config",
		zap.Int("chunk_duration", chunkDur),
		zap.Int("clip_duration", clipDurOverride),
		zap.Int("effect_interval", s.pcfg.EffectInterval),
		zap.Int("max_clips_per_source", s.cfg.Video.WithDefaults().MaxClipsPerSource),
	)

	var videoSources []VideoSource

	for _, q := range input.SearchQueries {
		s.log.Info("resolving search query", zap.String("query", q))
		videos, err := s.resolveQuery(ctx, q)
		if err != nil {
			s.log.Warn("failed to resolve query", zap.String("query", q), zap.Error(err))
			continue
		}
		videoSources = append(videoSources, videos...)
		s.log.Info("query resolved", zap.String("query", q), zap.Int("videos_found", len(videos)))
	}

	for _, url := range input.DirectURLs {
		src := VideoSource{
			URL:    url,
			Title:  extractVideoID(url),
			Source: url,
		}
		if info, err := s.getDirectVideoInfo(ctx, url); err != nil {
			s.log.Warn("failed to resolve direct video metadata", zap.String("url", url), zap.Error(err))
		} else if info != nil {
			if info.Title != "" {
				src.Title = info.Title
			}
			src.DurationSec = info.Duration
			s.log.Info("direct video metadata resolved",
				zap.String("url", url),
				zap.String("title", src.Title),
				zap.Float64("duration_sec", src.DurationSec),
			)
		}
		s.log.Info("adding direct url source", zap.String("url", url), zap.String("video_id", extractVideoID(url)))
		videoSources = append(videoSources, src)
	}

	if len(videoSources) == 0 {
		return nil, fmt.Errorf("no video sources found")
	}

	if input.Progress != nil {
		input.Progress(10, fmt.Sprintf("Found %d video sources", len(videoSources)))
	}

	s.log.Info("video sources resolved",
		zap.Int("count", len(videoSources)),
		zap.Int("search_queries", len(input.SearchQueries)),
		zap.Int("direct_urls", len(input.DirectURLs)),
	)

	if input.MaxVideos > 0 && len(videoSources) > input.MaxVideos {
		s.log.Info("limiting stock sources",
			zap.Int("max_videos", input.MaxVideos),
			zap.Int("before", len(videoSources)),
		)
		videoSources = videoSources[:input.MaxVideos]
		s.log.Info("stock sources limited",
			zap.Int("after", len(videoSources)),
		)
	}

	totalSecs := input.TotalMinutes * 60
	videoCfg := s.cfg.Video.WithDefaults()
	clipDur := videoCfg.ClipDuration
	if input.ClipDuration > 0 {
		clipDur = input.ClipDuration
	}
	secsPerVideo := totalSecs / len(videoSources)
	if secsPerVideo < clipDur*3 {
		secsPerVideo = clipDur * 3
	}

	s.log.Info("per-video budget computed",
		zap.Int("total_seconds", totalSecs),
		zap.Int("video_count", len(videoSources)),
		zap.Int("seconds_per_video", secsPerVideo),
		zap.Int("clip_duration", clipDur),
		zap.Int("planned_clips_per_source", secsPerVideo/clipDur),
	)

	tempDir := filepath.Join(s.cfg.Storage.TempPath(), "yt_compile_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	s.log.Info("creating working directory", zap.String("temp_dir", tempDir))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if input.Progress != nil {
		input.Progress(15, fmt.Sprintf("Processing %d videos...", len(videoSources)))
	}

	var processedClips []string
	var clipTitles []string
	var processedSourceIDs []string

	type videoResult struct {
		index     int
		url       string
		title     string
		clips     []string
		titles    []string
		sourceIDs []string
		err       error
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	results := make(chan videoResult, len(videoSources))

	for i, vs := range videoSources {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		i, vs := i, vs
		wg.Add(1)
		concurrent.SafeGoFunc("stock-video-worker", struct {
			Idx     int
			Src     VideoSource
			Sem     chan struct{}
			Results chan videoResult
		}{Idx: i, Src: vs, Sem: sem, Results: results}, func(arg struct {
			Idx     int
			Src     VideoSource
			Sem     chan struct{}
			Results chan videoResult
		}) {
			defer wg.Done()
			s.log.Info("video worker started",
				zap.Int("video_index", arg.Idx),
				zap.String("video_url", arg.Src.URL),
				zap.String("video_title", arg.Src.Title),
				zap.Float64("video_duration_sec", arg.Src.DurationSec),
			)
			arg.Sem <- struct{}{}
			defer func() { <-arg.Sem }()

			clips, titles, sourceIDs, err := s.processSingleVideo(ctx, tempDir, arg.Src, arg.Idx, secsPerVideo, input.ClipDuration, input.NoAudio)
			arg.Results <- videoResult{index: arg.Idx, url: arg.Src.URL, title: arg.Src.Title, clips: clips, titles: titles, sourceIDs: sourceIDs, err: err}
		})
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("panic in stock pipeline results goroutine", zap.Any("recover", r))
			}
		}()
		wg.Wait()
		close(results)
	}()

	clipsBySource := make([][]string, len(videoSources))
	titlesBySource := make([][]string, len(videoSources))
	sourceIDsBySource := make([][]string, len(videoSources))

	for res := range results {
		if res.err != nil {
			s.log.Warn("video processing failed",
				zap.Int("video_index", res.index),
				zap.String("video_url", res.url),
				zap.String("video_title", res.title),
				zap.Error(res.err),
			)
			continue
		}
		s.log.Info("video processed",
			zap.Int("video_index", res.index),
			zap.String("video_url", res.url),
			zap.String("video_title", res.title),
			zap.Int("clips_created", len(res.clips)),
		)
		clipsBySource[res.index] = res.clips
		titlesBySource[res.index] = res.titles
		sourceIDsBySource[res.index] = res.sourceIDs
	}

	processedClips, clipTitles, processedSourceIDs = InterleaveClips(clipsBySource, titlesBySource, sourceIDsBySource)

	if len(processedClips) == 0 {
		return nil, fmt.Errorf("no clips were successfully downloaded and processed")
	}

	if input.Progress != nil {
		input.Progress(50, fmt.Sprintf("Extracted %d clips, preparing chunks...", len(processedClips)))
	}

	s.log.Info("processed clips interleaved", zap.Int("count", len(processedClips)))

	folderID, err := s.resolveFolderTarget(ctx, input.FolderID, input.Subfolder, input.FolderName)
	if err != nil {
		return nil, fmt.Errorf("drive folder resolution: %w", err)
	}
	s.log.Info("drive destination resolved",
		zap.String("folder_id", folderID),
		zap.String("subfolder", input.Subfolder),
		zap.String("folder_name", input.FolderName),
	)

	clipsPerChunk := (chunkDur + clipDur - 1) / clipDur
	if clipsPerChunk < 1 {
		clipsPerChunk = 1
	}
	if clipsPerChunk > len(processedClips) {
		clipsPerChunk = len(processedClips)
	}

	numChunks := int(math.Ceil(float64(len(processedClips)) / float64(clipsPerChunk)))
	s.log.Info("rendering chunks", zap.Int("clips_per_chunk", clipsPerChunk), zap.Int("num_chunks", numChunks))

	result := &PipelineResult{
		SearchTerms: append(input.SearchQueries, input.DirectURLs...),
		TotalClips:  len(processedClips),
		TotalChunks: numChunks,
	}

	for chunkIdx := 0; chunkIdx < numChunks; chunkIdx++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		startClip := chunkIdx * clipsPerChunk
		endClip := startClip + clipsPerChunk
		if endClip > len(processedClips) {
			endClip = len(processedClips)
		}

		chunkClips := processedClips[startClip:endClip]
		chunkTitles := clipTitles[startClip:endClip]

		s.log.Info("stock pipeline chunk created",
			zap.Int("chunk_index", chunkIdx),
			zap.Int("clip_count", len(chunkClips)),
		)

		chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%04d.mp4", chunkIdx))
		s.log.Info("rendering chunk",
			zap.Int("chunk", chunkIdx),
			zap.Int("start_clip", startClip),
			zap.Int("end_clip", endClip),
			zap.Int("clip_count", len(chunkClips)),
			zap.Strings("titles", chunkTitles),
			zap.String("output_path", chunkPath),
		)

		err := s.renderChunk(ctx, chunkClips, chunkTitles, chunkPath, input.NoTransitions, input.NoEffects, input.NoAudio, chunkIdx)
		if err != nil {
			s.log.Error("failed to render chunk", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
		}

		chunkTitle := fmt.Sprintf("timestamp_%d", chunkIdx)

		chunkRes := ChunkResult{
			Index:         chunkIdx,
			TimelineStart: float64(chunkIdx * chunkDur),
			TimelineEnd:   float64((chunkIdx + 1) * chunkDur),
			LocalPath:     chunkPath,
			Title:         chunkTitle,
			SourceIDs:     processedSourceIDs[startClip:endClip],
		}

		s.uploadAndIndexChunk(ctx, chunkIdx, chunkPath, chunkTitle, folderID, &chunkRes, input, &videoCfg)

		result.Chunks = append(result.Chunks, chunkRes)

		if input.Progress != nil {
			pct := 55 + int(float64(chunkIdx+1)/float64(numChunks)*35)
			input.Progress(pct, fmt.Sprintf("Rendered chunk %d/%d, uploaded to Drive", chunkIdx+1, numChunks))
		}

		s.log.Info("chunk uploaded",
			zap.Int("chunk", chunkIdx),
			zap.String("drive_link", chunkRes.DriveLink),
		)
	}

	meta := s.buildPipelineMetadata(input, chunkDur, clipDur, result, clipTitles)

	if input.Progress != nil {
		input.Progress(95, "Uploading metadata JSON...")
	}

	metaPath := filepath.Join(tempDir, "metadata.json")
	metaBytes, _ := jsonMarshalIndent(meta)
	if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil {
		s.log.Error("failed to write pipeline metadata JSON", zap.Error(err))
	} else {
		metaUp, metaErr := s.driveUp.UploadFile(ctx, metaPath, folderID, "metadata.json")
		if metaErr != nil {
			s.log.Error("failed to upload pipeline metadata JSON", zap.Error(metaErr))
		} else {
			result.MetadataLink = metaUp.WebViewLink
			result.MetadataFileID = metaUp.FileID
			s.log.Info("pipeline metadata JSON uploaded",
				zap.String("drive_link", metaUp.WebViewLink),
			)
		}
	}

	s.log.Info("compilation pipeline complete",
		zap.Int("total_clips", len(processedClips)),
		zap.Int("chunks_uploaded", len(result.Chunks)),
		zap.Duration("duration", time.Since(start)),
	)

	return result, nil
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
