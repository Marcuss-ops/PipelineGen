package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	corejobs "velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/fileutil"
	"velox/go-master/internal/pkg/media/downloader"
	"velox/go-master/internal/pkg/media/ffmpeg"
	driveup "velox/go-master/internal/upload/drive"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))



// Service orchestrates the stock video pipeline: search, download, clip extraction,
// effect overlay, chunk rendering, and Drive upload. All video parameters are read
// from config.Video to ensure consistency with other media pipelines.
type Service struct {
	cfg        *config.Config
	log        *zap.Logger
	driveSvc   *gdrive.Service
	driveUp    *driveup.Uploader
	ytdlp      *downloader.YTDLPDownloader
	ffmpegProc *ffmpeg.Processor
	pcfg       PipelineConfig
	jobsSvc    *jobservice.Service
	assetIndex *assetindex.Service
}

// NewService creates a stock pipeline service using the provided config, logger,
// and Google Drive service. Video processing defaults are loaded from cfg.Video.
func NewService(cfg *config.Config, log *zap.Logger, driveSvc *gdrive.Service) *Service {
	v := cfg.Video.WithDefaults()
	return &Service{
		cfg:        cfg,
		log:        log,
		driveSvc:   driveSvc,
		driveUp:    &driveup.Uploader{Service: driveSvc, Log: log},
		ytdlp:      downloader.NewYTDLP(cfg),
		ffmpegProc: ffmpeg.New(cfg),
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     "assets/effects/EffettiVisiv",
		},
	}
}

// SetJobsSvc injects the jobs service dependency.
func (s *Service) SetJobsSvc(jobsSvc *jobservice.Service) {
	s.jobsSvc = jobsSvc
}

// SetAssetIndex injects the asset index service dependency.
func (s *Service) SetAssetIndex(ai *assetindex.Service) {
	s.assetIndex = ai
}

// RegisterHandler registers the stock pipeline job handler with the jobs system.
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeMediaStock, s.HandleJob)
		s.log.Info("registered media.stock job handler", zap.String("type", string(models.JobTypeMediaStock)))
	}
}

func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	var payload corejobs.StockRunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	input := &RunInput{
		SearchQueries: payload.SearchQueries,
		DirectURLs:    payload.DirectURLs,
		TotalMinutes:  payload.TotalMinutes,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
	}

	if tools.Progress != nil {
		tools.Progress(5, "Starting stock pipeline")
	}

	result, err := s.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline complete")
	}

	return map[string]any{
		"total_clips":  result.TotalClips,
		"total_chunks": result.TotalChunks,
		"chunks":       result.Chunks,
	}, nil
}

// RunInput holds the parameters for a stock pipeline run.
type RunInput struct {
	// SearchQueries are YouTube search terms. Append " -N" to limit results (e.g. "Elon Musk -25").
	SearchQueries []string
	// DirectURLs are direct YouTube video URLs to process.
	DirectURLs []string
	// TotalMinutes is the desired total duration of the output compilation.
	TotalMinutes int
	// ChunkDuration is the target duration of each output chunk in seconds.
	// If zero, the value from config is used.
	ChunkDuration int
	// Subfolder is the Drive subfolder name (e.g. "Discovery").
	Subfolder string
	// FolderName is a new folder to create inside the subfolder.
	FolderName string
}

// Run executes the full stock pipeline: resolve sources, download, extract clips,
// apply overlay effects, render chunks, upload to Drive, and index assets.
// It reads all video parameters from cfg.Video for codec consistency.
func (s *Service) Run(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	start := time.Now()
	s.log.Info("compilation pipeline start",
		zap.Strings("queries", input.SearchQueries),
		zap.Int("total_minutes", input.TotalMinutes),
	)

	chunkDur := input.ChunkDuration
	if chunkDur <= 0 {
		chunkDur = s.pcfg.ChunkDuration
	}

	var videoSources []VideoSource

	for _, q := range input.SearchQueries {
		videos, err := s.resolveQuery(ctx, q)
		if err != nil {
			s.log.Warn("failed to resolve query", zap.String("query", q), zap.Error(err))
			continue
		}
		videoSources = append(videoSources, videos...)
	}

	for _, url := range input.DirectURLs {
		videoSources = append(videoSources, VideoSource{
			URL:    url,
			Title:  extractVideoID(url),
			Source: url,
		})
	}

	if len(videoSources) == 0 {
		return nil, fmt.Errorf("no video sources found")
	}

	s.log.Info("video sources resolved", zap.Int("count", len(videoSources)))

	totalSecs := input.TotalMinutes * 60
	videoCfg := s.cfg.Video.WithDefaults()
	clipDur := videoCfg.ClipDuration
	secsPerVideo := totalSecs / len(videoSources)
	if secsPerVideo < clipDur*3 {
		secsPerVideo = clipDur * 3
	}

	tempDir := filepath.Join(s.cfg.Storage.TempPath(), "yt_compile_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	var processedClips []string
	var clipTitles []string

	type videoResult struct {
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
			sem <- struct{}{}
			defer func() { <-sem }()

			clips, titles, err := s.processSingleVideo(ctx, tempDir, src, idx, secsPerVideo)
			results <- videoResult{clips, titles, err}
		}(i, vs)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		if res.err != nil {
			s.log.Warn("video processing failed", zap.Error(res.err))
			continue
		}
		processedClips = append(processedClips, res.clips...)
		clipTitles = append(clipTitles, res.titles...)
	}

	if len(processedClips) == 0 {
		return nil, fmt.Errorf("no clips were successfully downloaded and processed")
	}

	s.log.Info("processed clips", zap.Int("count", len(processedClips)))

	folderID, err := s.resolveFolderTarget(ctx, input.Subfolder, input.FolderName)
	if err != nil {
		return nil, fmt.Errorf("drive folder resolution: %w", err)
	}

	rng.Shuffle(len(processedClips), func(i, j int) {
		processedClips[i], processedClips[j] = processedClips[j], processedClips[i]
		clipTitles[i], clipTitles[j] = clipTitles[j], clipTitles[i]
	})

	if videoCfg.EffectInterval > 0 {
		effects, err := loadEffects(s.pcfg.EffectsDir)
		if err != nil {
			s.log.Warn("no overlay effects loaded", zap.String("dir", s.pcfg.EffectsDir), zap.Error(err))
		} else {
			s.log.Info("applying overlay effects", zap.Int("interval", s.pcfg.EffectInterval), zap.Int("effects_found", len(effects)))
			for i := s.pcfg.EffectInterval - 1; i < len(processedClips); i += s.pcfg.EffectInterval {
				effectPath := effects[rng.Intn(len(effects))]
				outputPath := filepath.Join(tempDir, fmt.Sprintf("effected_%04d.mp4", i))
				err := s.ffmpegProc.ApplyOverlay(ctx, processedClips[i], effectPath, outputPath, ffmpeg.OverlayOptions{
					Width:   videoCfg.Width,
					Height:  videoCfg.Height,
					FPS:     videoCfg.FPS,
					Opacity: videoCfg.OverlayOpacity,
					Codec:   videoCfg.Codec,
					Preset:  videoCfg.Preset,
					CRF:     videoCfg.CRF,
				})
				if err != nil {
					s.log.Warn("overlay effect failed", zap.Int("clip_idx", i), zap.Error(err))
					continue
				}
				processedClips[i] = outputPath
				clipTitles[i] = clipTitles[i] + "_FX"
				s.log.Debug("overlay effect applied", zap.Int("clip_idx", i), zap.String("effect", effectPath))
			}
		}
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

		err := s.renderChunk(ctx, chunkClips, chunkTitles, chunkPath)
		if err != nil {
			s.log.Error("failed to render chunk", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
		}

		chunkTitle := fmt.Sprintf("timestamp_%d", chunkIdx)

		upResult, err := s.driveUp.UploadFile(ctx, chunkPath, folderID, chunkTitle+".mp4")
		if err != nil {
			s.log.Error("failed to upload chunk to drive", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
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
			meta := map[string]any{
				"filename":    chunkTitle + ".mp4",
				"folder_id":   folderID,
				"folder_path": input.Subfolder + "/" + input.FolderName + "/" + chunkTitle + ".mp4",
				"media_type":  "stock_clip",
				"category":    "file",
				"search_text": chunkTitle,
			}
			metaJSON, _ := json.Marshal(meta)
			assetID := "stock_" + upResult.FileID
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
		}
	}

	s.log.Info("compilation pipeline complete",
		zap.Int("total_clips", len(processedClips)),
		zap.Int("chunks_uploaded", len(result.Chunks)),
		zap.Duration("duration", time.Since(start)),
	)

	return result, nil
}

// processSingleVideo downloads a single video source, then extracts and normalizes
// short clips using ffmpeg. The clip duration and max clips are controlled by
// cfg.Video. It avoids double re-encoding by using CutAndNormalize.
func (s *Service) processSingleVideo(ctx context.Context, tempDir string, vs VideoSource, idx int, secsPerVideo int) ([]string, []string, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	s.log.Info("downloading from video",
		zap.Int("index", idx),
		zap.String("url", vs.URL),
		zap.String("title", vs.Title),
	)

	rawPath := filepath.Join(tempDir, fmt.Sprintf("raw_%04d.mp4", idx))

	startTime := rng.Float64() * math.Max(0, vs.DurationSec-float64(secsPerVideo))
	startStr := formatDuration(startTime)
	endStr := formatDuration(startTime + float64(secsPerVideo))
	section := fmt.Sprintf("*%s-%s", startStr, endStr)

	err := s.ytdlp.Download(ctx, &downloader.DownloadRequest{
		URL:              vs.URL,
		OutputPath:       rawPath,
		MergeFormat:      "mp4",
		DownloadSections: []string{section},
		ForceKeyframes:   true,
		Timeout:          10 * time.Minute,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("yt-dlp download failed for %q: %w", vs.URL, err)
	}

	actualPath := resolveActualPath(rawPath)
	if actualPath == "" {
		return nil, nil, fmt.Errorf("downloaded file not found for %q", vs.URL)
	}

	v := s.cfg.Video.WithDefaults()
	clipDur := v.ClipDuration
	maxClipsPerSource := v.MaxClipsPerSource

	numClips := secsPerVideo / clipDur
	if numClips < 1 {
		numClips = 1
	}
	if numClips > maxClipsPerSource {
		numClips = maxClipsPerSource
	}

	var processedClips []string
	var clipTitles []string
	usedOffsets := make(map[float64]bool)

	for clipIdx := 0; clipIdx < numClips; clipIdx++ {
		select {
		case <-ctx.Done():
			_ = os.Remove(actualPath)
			return processedClips, clipTitles, ctx.Err()
		default:
		}

		maxStart := float64(secsPerVideo) - float64(clipDur)
		if maxStart < 1 {
			maxStart = 1
		}

		var offset float64
		for attempt := 0; attempt < 20; attempt++ {
			offset = rng.Float64() * maxStart
			rounded := math.Round(offset)
			if !usedOffsets[rounded] {
				usedOffsets[rounded] = true
				break
			}
		}

		cutStart := formatDuration(offset)
		cutEnd := formatDuration(offset + float64(clipDur))

		// Single-pass: cut + normalize in one ffmpeg call, avoiding double re-encode
		normPath := filepath.Join(tempDir, fmt.Sprintf("clip_%04d_%04d.mp4", idx, clipIdx))
		err = s.ffmpegProc.CutAndNormalize(ctx, actualPath, normPath, cutStart, cutEnd, ffmpeg.CutAndNormalizeOptions{
			Width:   v.Width,
			Height:  v.Height,
			FPS:     v.FPS,
			Codec:   v.Codec,
			Preset:  v.Preset,
			CRF:     v.CRF,
			NoAudio: true,
		})
		if err != nil {
			s.log.Warn("cut+normalize failed", zap.Int("video", idx), zap.Int("clip", clipIdx), zap.Error(err))
			continue
		}

		processedClips = append(processedClips, normPath)
		clipTitles = append(clipTitles, fmt.Sprintf("%s_%04d", vs.Title, clipIdx))
	}

	_ = os.Remove(actualPath)
	return processedClips, clipTitles, nil
}

// resolveFolderTarget resolves the Google Drive folder ID for upload.
// It walks from the configured stock root folder through subfolder and folderName.
func (s *Service) resolveFolderTarget(ctx context.Context, subfolder, folderName string) (string, error) {
	rootID := s.cfg.Drive.StockRootFolder
	if rootID == "" {
		rootID = s.cfg.Drive.ClipsRootFolder
	}
	if rootID == "" {
		return "", fmt.Errorf("drive.stock_root_folder not configured in config.yaml")
	}

	currentID := rootID

	if subfolder != "" {
		id, err := s.driveUp.GetOrCreateFolder(ctx, subfolder, currentID)
		if err != nil {
			return "", fmt.Errorf("subfolder %q: %w", subfolder, err)
		}
		currentID = id
	}

	if folderName != "" {
		id, err := s.driveUp.GetOrCreateFolder(ctx, folderName, currentID)
		if err != nil {
			return "", fmt.Errorf("folder %q: %w", folderName, err)
		}
		currentID = id
	}

	return currentID, nil
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}

// resolveQuery converts a query string into a list of VideoSource entries.
// If the query is a YouTube URL, it returns it directly. Otherwise it searches
// YouTube using yt-dlp. The result count is read from cfg.Video.SearchCount.
func (s *Service) resolveQuery(ctx context.Context, query string) ([]VideoSource, error) {
	query = strings.TrimSpace(query)

	if strings.HasPrefix(query, "http") && (strings.Contains(query, "youtube.com") || strings.Contains(query, "youtu.be")) {
		return []VideoSource{{
			URL:    query,
			Title:  extractVideoID(query),
			Source: query,
		}}, nil
	}

	vCfg := s.cfg.Video.WithDefaults()
	numVideos := vCfg.SearchCount
	searchTerm := query

	if idx := strings.LastIndex(query, " -"); idx > 0 {
		searchTerm = strings.TrimSpace(query[:idx])
		countStr := strings.TrimSpace(query[idx+2:])
		if c, err := fmt.Sscanf(countStr, "%d", &numVideos); err != nil || c == 0 {
			numVideos = vCfg.SearchCount
		}
	}
	if numVideos < 1 {
		numVideos = 1
	}
	if numVideos > 50 {
		numVideos = 50
	}

	s.log.Info("searching YouTube", zap.String("term", searchTerm), zap.Int("count", numVideos))

	searchURL := fmt.Sprintf("ytsearch%d:%s", numVideos, searchTerm)
	videos, err := s.ytdlp.ListChannel(ctx, searchURL, numVideos)
	if err != nil {
		videos, err = s.ytdlp.ListChannel(ctx, query, numVideos)
		if err != nil {
			return nil, fmt.Errorf("failed to list videos for query %q: %w", query, err)
		}
	}

	var sources []VideoSource
	for _, v := range videos {
		url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", v.ID)
		title := v.Title
		if title == "" {
			title = v.ID
		}
		sources = append(sources, VideoSource{
			URL:         url,
			Title:       title,
			Source:      url,
			DurationSec: v.Duration,
		})
	}

	return sources, nil
}

// loadEffects scans the given directory for .mp4 overlay effect files.
func loadEffects(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("effects dir is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read effects dir %q: %w", dir, err)
	}
	var effects []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			effects = append(effects, filepath.Join(dir, e.Name()))
		}
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("no .mp4 effect files found in %s", dir)
	}
	return effects, nil
}

// renderChunk concatenates multiple clips into a single output video using ffmpeg.
// It applies random xfade transitions between clips. All encoding parameters
// (codec, preset, CRF, resolution, FPS) are read from cfg.Video.
func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string) error {
	if len(clips) == 0 {
		return fmt.Errorf("no clips to render")
	}
	if len(clips) == 1 {
		return fileutil.CopyFile(clips[0], outputPath)
	}

	v := s.cfg.Video.WithDefaults()
	clipDur := v.ClipDuration
	transitionPresets := v.TransitionPresets

	var inputArgs []string
	var filterParts []string

	for i, clip := range clips {
		inputArgs = append(inputArgs, "-i", clip)
		filterParts = append(filterParts, fmt.Sprintf(
			"[%d:v]setpts=PTS-STARTPTS,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d[v%d]",
			i, v.Width, v.Height, v.Width, v.Height, v.FPS, i,
		))
		clips[i] = fmt.Sprintf("v%d", i)
	}

	lastLabel := clips[0]
	cumOffset := clipDur

	for i := 1; i < len(clips); i++ {
		trans := transitionPresets[rng.Intn(len(transitionPresets))]
		nextLabel := clips[i]
		outLabel := fmt.Sprintf("c%d", i)

		filterParts = append(filterParts, fmt.Sprintf(
			"[%s][%s]xfade=transition=%s:duration=1:offset=%d[%s]",
			lastLabel, nextLabel, trans, cumOffset-1, outLabel,
		))

		lastLabel = outLabel
		cumOffset += clipDur - 1
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}
	args = append(args, inputArgs...)
	args = append(args, "-filter_complex", joinFilterParts(filterParts))
	args = append(args, "-map", fmt.Sprintf("[%s]", lastLabel))
	args = append(args, "-an")
	args = append(args, "-c:v", v.Codec, "-preset", v.Preset, "-cq", fmt.Sprintf("%d", v.CRF))
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart")
	args = append(args, outputPath)

	s.log.Debug("ffmpeg render chunk", zap.Int("clips", len(clips)))
	_ = titles

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		s.log.Debug("ffmpeg output", zap.String("stderr", string(output)))
	}
	if err != nil {
		return fmt.Errorf("ffmpeg render failed: %w", err)
	}
	return nil
}

func joinFilterParts(parts []string) string {
	result := ""
	for _, p := range parts {
		if result != "" {
			result += ";"
		}
		result += p
	}
	return result
}



func formatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	d := time.Duration(sec * float64(time.Second))
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := (d - s*time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		for _, part := range strings.Split(url, "&") {
			if strings.HasPrefix(part, "v=") {
				return strings.TrimPrefix(part, "v=")
			}
		}
	}
	if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

func resolveActualPath(basePath string) string {
	if _, err := os.Stat(basePath); err == nil {
		return basePath
	}
	if _, err := os.Stat(basePath + ".mp4"); err == nil {
		return basePath + ".mp4"
	}
	if _, err := os.Stat(basePath + ".mkv"); err == nil {
		return basePath + ".mkv"
	}
	if _, err := os.Stat(basePath + ".webm"); err == nil {
		return basePath + ".webm"
	}
	return ""
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	s = strings.Trim(s, "_")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}
