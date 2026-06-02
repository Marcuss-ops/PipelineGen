package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/semantic"
)

// Run executes the full stock pipeline: resolve sources, download, extract clips,
// apply overlay effects, render chunks, upload to Drive, and index assets.
// It reads all video parameters from cfg.Video for codec consistency.
func (s *Service) Run(ctx context.Context, input *RunInput) (*PipelineResult, error) {
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

	s.log.Info("stock timing config",
		zap.Int("chunk_duration", chunkDur),
		zap.Int("clip_duration", s.cfg.Video.WithDefaults().ClipDuration),
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

	var processedClips []string
	var clipTitles []string

	type videoResult struct {
		index  int
		url    string
		title  string
		clips  []string
		titles []string
		err    error
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

		wg.Add(1)
		go func(idx int, src VideoSource) {
			defer wg.Done()
			s.log.Info("video worker started",
				zap.Int("video_index", idx),
				zap.String("video_url", src.URL),
				zap.String("video_title", src.Title),
				zap.Float64("video_duration_sec", src.DurationSec),
			)
			sem <- struct{}{}
			defer func() { <-sem }()

			clips, titles, err := s.processSingleVideo(ctx, tempDir, src, idx, secsPerVideo)
			results <- videoResult{index: idx, url: src.URL, title: src.Title, clips: clips, titles: titles, err: err}
		}(i, vs)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

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
		processedClips = append(processedClips, res.clips...)
		clipTitles = append(clipTitles, res.titles...)
	}

	if len(processedClips) == 0 {
		return nil, fmt.Errorf("no clips were successfully downloaded and processed")
	}

	s.log.Info("processed clips", zap.Int("count", len(processedClips)))

	folderID, err := s.resolveFolderTarget(ctx, input.FolderID, input.Subfolder, input.FolderName)
	if err != nil {
		return nil, fmt.Errorf("drive folder resolution: %w", err)
	}
	s.log.Info("drive destination resolved",
		zap.String("folder_id", folderID),
		zap.String("subfolder", input.Subfolder),
		zap.String("folder_name", input.FolderName),
	)

	rng.Shuffle(len(processedClips), func(i, j int) {
		processedClips[i], processedClips[j] = processedClips[j], processedClips[i]
		clipTitles[i], clipTitles[j] = clipTitles[j], clipTitles[i]
	})

	if videoCfg.EffectInterval > 0 {
		s.log.Info("overlay effects skipped in fast stock path",
			zap.Int("effect_interval", videoCfg.EffectInterval),
			zap.String("reason", "keep single encode final"),
		)
	}

	clipsPerChunk := chunkDur / clipDur
	if clipsPerChunk < 1 {
		clipsPerChunk = 1
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

		chunkPath := filepath.Join(tempDir, fmt.Sprintf("chunk_%04d.mp4", chunkIdx))
		s.log.Info("rendering chunk",
			zap.Int("chunk", chunkIdx),
			zap.Int("start_clip", startClip),
			zap.Int("end_clip", endClip),
			zap.Int("clip_count", len(chunkClips)),
			zap.Strings("titles", chunkTitles),
			zap.String("output_path", chunkPath),
		)

		err := s.renderChunk(ctx, chunkClips, chunkTitles, chunkPath)
		if err != nil {
			s.log.Error("failed to render chunk", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
		}

		chunkTitle := fmt.Sprintf("timestamp_%d", chunkIdx)
		s.log.Info("uploading chunk to drive",
			zap.Int("chunk", chunkIdx),
			zap.String("chunk_title", chunkTitle),
			zap.String("folder_id", folderID),
			zap.String("local_path", chunkPath),
		)

		upResult, err := s.driveUp.UploadFile(ctx, chunkPath, folderID, chunkTitle+".mp4")
		if err != nil {
			s.log.Error("failed to upload chunk to drive", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
		}

		// Generate and upload metadata.json via unified MetadataWriter.
		// Always runs if metaWriter is configured — basic semantic enrichment is always on.
		// Multilingual translations are opt-in via cfg.Multilingual.Enabled.
		if s.metaWriter != nil {
			s.enrichChunkMetadata(ctx, chunkTitle, chunkPath, folderID, input.SearchQueries, input.FolderName)
		}

		chunkRes := ChunkResult{
			Index:         chunkIdx,
			TimelineStart: float64(chunkIdx * chunkDur),
			TimelineEnd:   float64((chunkIdx + 1) * chunkDur),
			LocalPath:     chunkPath,
			DriveLink:     upResult.WebViewLink,
			DownloadLink:  upResult.DownloadLink,
			DriveFileID:   upResult.FileID,
			Title:         chunkTitle,
		}
		result.Chunks = append(result.Chunks, chunkRes)

		s.log.Info("chunk uploaded",
			zap.Int("chunk", chunkIdx),
			zap.String("drive_link", upResult.WebViewLink),
		)

		if s.assetIndex != nil {
			assetID := "stock_" + upResult.FileID
			s.log.Info("upserting chunk into asset_index",
				zap.Int("chunk", chunkIdx),
				zap.String("asset_id", assetID),
				zap.String("group_name", input.FolderName),
			)

			meta := semantic.BuildAssetMetadata(semantic.AssetSemanticInput{
				AssetID:             assetID,
				AssetType:           "stock_clip",
				Source:              "stock",
				MediaType:           "stock_clip",
				Generator:           "stock-pipeline",
				PromptOriginal:      strings.Join(append(append([]string{}, input.SearchQueries...), input.DirectURLs...), " | "),
				SemanticDescription: semantic.MergeMetadataSearchText(chunkTitle, input.FolderName, input.Subfolder),
				SearchText:          semantic.MergeMetadataSearchText(chunkTitle, input.FolderName, input.Subfolder, strings.Join(input.SearchQueries, " ")),
				Subjects:            semantic.AppendUniqueStrings(nil, input.FolderName, input.Subfolder),
				Tags:                semantic.AppendUniqueStrings(nil, chunkTitle, input.FolderName, input.Subfolder, "stock", "clip"),
				Categories:          semantic.AppendUniqueStrings(nil, "file", "stock", "clip"),
				Confidence:          0.75,
				EmbeddingStatus:     "ready",
				Extra: map[string]any{
					"filename":    chunkTitle + ".mp4",
					"folder_id":   folderID,
					"folder_path": input.Subfolder + "/" + input.FolderName + "/" + chunkTitle + ".mp4",
					"media_type":  "stock_clip",
					"category":    "file",
				},
			}, nil)
			metaJSON, _ := json.Marshal(meta)
			rec := &assetindex.AssetRecord{
				AssetID:      assetID,
				AssetType:    "stock_clip",
				Source:       "stock",
				SourceID:     upResult.FileID,
				GroupName:    input.FolderName,
				Subfolder:    input.Subfolder,
				LocalPath:    chunkPath,
				DriveLink:    upResult.WebViewLink,
				DownloadLink: upResult.DownloadLink,
				Status:       "ready",
				Metadata:     string(metaJSON),
				CreatedAt:    time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
			}
			if err := s.assetIndex.Upsert(ctx, rec); err != nil {
				s.log.Warn("failed to save chunk to asset_index", zap.Int("chunk", chunkIdx), zap.Error(err))
			} else {
				s.log.Info("chunk saved to asset_index", zap.String("asset_id", assetID))
			}

			// Trigger automatic vector indexing asynchronously since we have local file (Deep visual + text index)
			if s.clipIndexer != nil && s.clipIndexer.IsEnabled() {
				go func(id string) {
					indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
					defer cancel()
					s.log.Info("triggering automatic vector indexing for stock chunk", zap.String("id", id))
					if err := s.clipIndexer.IndexClip(indexCtx, id); err != nil {
						s.log.Error("failed to automatically index stock chunk", zap.String("id", id), zap.Error(err))
					}
				}(upResult.FileID)
			}
		}
	}

	s.log.Info("compilation pipeline complete",
		zap.Int("total_clips", len(processedClips)),
		zap.Int("chunks_uploaded", len(result.Chunks)),
		zap.Duration("duration", time.Since(start)),
	)

	return result, nil
}

// enrichChunkMetadata generates metadata.json for a rendered stock chunk and
// uploads it to the same Drive folder alongside the video.
// Uses the unified MetadataWriter for consistency with all other media types.
func (s *Service) enrichChunkMetadata(ctx context.Context, chunkTitle, chunkPath, folderID string, searchQueries []string, folderName string) {
	if s.metaWriter == nil {
		return
	}

	// Build rich prompt from search queries and chunk info
	prompt := strings.Join(searchQueries, ", ")
	if folderName != "" {
		prompt = folderName + ": " + prompt
	}

	s.log.Info("enriching stock chunk metadata",
		zap.String("chunk_title", chunkTitle),
		zap.String("folder_id", folderID),
	)

	lang := s.cfg.Multilingual.SourceLanguage
	if lang == "" {
		lang = "en"
	}
	// Only translate if explicitly enabled AND languages are configured
	var translateTo []string
	if s.cfg.Multilingual.Enabled && len(s.cfg.Multilingual.TranslateLanguages) > 0 {
		translateTo = s.cfg.Multilingual.TranslateLanguages
	}

	result, err := s.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetType:           "stock_clip",
		MediaType:           "video",
		Source:              "stock",
		Generator:           "stock-pipeline",
		Style:               "stock",
		Prompt:              prompt,
		SearchText:          semantic.MergeMetadataSearchText(chunkTitle, folderName, strings.Join(searchQueries, " ")),
		Language:            lang,
		TranslateLanguages:  translateTo,
		LocalPath:           chunkPath,
		Extensions:          semantic.BuildClipExtension(0, "stock", 0.75, nil, nil, "", ""),
	})
	if err != nil {
		s.log.Warn("enrichChunkMetadata: metadata write failed", zap.Error(err))
		return
	}

	// Upload metadata.json to the same Drive folder as the video
	if result.LocalPath != "" && folderID != "" && s.driveUp != nil {
		_, err := s.driveUp.UploadFile(ctx, result.LocalPath, folderID, chunkTitle+"_metadata.json")
		if err != nil {
			s.log.Warn("enrichChunkMetadata: failed to upload metadata.json to Drive", zap.Error(err))
			return
		}
		s.log.Info("enrichChunkMetadata: metadata.json uploaded to Drive",
			zap.String("chunk_title", chunkTitle),
			zap.String("folder_id", folderID),
			zap.Float64("confidence", result.Payload.Confidence),
			zap.Int("tags", len(result.Payload.Tags)),
			zap.Int("translations", len(result.Payload.Translations)),
		)
	}
}
