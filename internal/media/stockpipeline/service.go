package stockpipeline

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/media/downloader"
	"velox/go-master/internal/pkg/media/ffmpeg"
	driveup "velox/go-master/internal/upload/drive"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

var transitionPresets = []string{
	"fade", "fadeblack", "fadewhite",
	"slideleft", "slideright", "slideup", "slidedown",
	"circleclose", "circleopen",
	"horzclose", "horzopen", "vertclose", "vertopen",
	"dissolve", "pixelize",
	"zoomin", "glitch",
}

type Service struct {
	cfg        *config.Config
	log        *zap.Logger
	driveSvc   *gdrive.Service
	driveUp    *driveup.Uploader
	ytdlp      *downloader.YTDLPDownloader
	ffmpegProc *ffmpeg.Processor
	pcfg       PipelineConfig
}

func NewService(cfg *config.Config, log *zap.Logger, driveSvc *gdrive.Service) *Service {
	return &Service{
		cfg:        cfg,
		log:        log,
		driveSvc:   driveSvc,
		driveUp:    &driveup.Uploader{Service: driveSvc, Log: log},
		ytdlp:      downloader.NewYTDLP(cfg),
		ffmpegProc: ffmpeg.New(cfg),
		pcfg:       DefaultPipelineConfig(),
	}
}

type RunInput struct {
	SearchQueries     []string // "Elon Musk -25" or direct YouTube URLs
	DirectURLs        []string // direct YouTube video URLs
	TotalMinutes      int
	SegmentDuration   int
	ChunkDuration     int
	OutputFolderID    string
}

func (s *Service) Run(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	start := time.Now()
	s.log.Info("compilation pipeline start",
		zap.Strings("queries", input.SearchQueries),
		zap.Int("total_minutes", input.TotalMinutes),
	)

	segDur := input.SegmentDuration
	if segDur <= 0 {
		segDur = s.pcfg.SegmentDuration
	}
	chunkDur := input.ChunkDuration
	if chunkDur <= 0 {
		chunkDur = s.pcfg.ChunkDuration
	}

	// 1. Resolve all video URLs from queries + direct URLs
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

	// 2. Calculate per-video download duration
	totalSecs := input.TotalMinutes * 60
	secsPerVideo := totalSecs / len(videoSources)
	if secsPerVideo < segDur {
		secsPerVideo = segDur
	}

	tempDir := filepath.Join(s.cfg.Storage.TempPath(), "yt_compile_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 3. Download sections from each video and trim to segments
	var processedClips []string
	var clipTitles []string

	for i, vs := range videoSources {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		s.log.Info("downloading from video",
			zap.Int("index", i),
			zap.String("url", vs.URL),
			zap.String("title", vs.Title),
		)

		rawPath := filepath.Join(tempDir, fmt.Sprintf("raw_%04d.mp4", i))

		// Download the whole section
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
			s.log.Warn("yt-dlp download failed", zap.String("url", vs.URL), zap.Error(err))
			continue
		}

		// Check actual downloaded file (yt-dlp may add extension)
		actualPath := resolveActualPath(rawPath)
		if actualPath == "" {
			s.log.Warn("downloaded file not found", zap.String("path", rawPath))
			continue
		}

		// Cut segment(s) from downloaded portion
		numSegments := secsPerVideo / segDur
		if numSegments < 1 {
			numSegments = 1
		}
		segStep := float64(secsPerVideo) / float64(numSegments)

		for segIdx := 0; segIdx < numSegments; segIdx++ {
			segStart := rng.Float64() * (segStep - float64(segDur))
			if segStart < 0 {
				segStart = 0
			}
			offsetStart := float64(segIdx) * segStep
			cutStart := formatDuration(offsetStart + segStart)
			cutEnd := formatDuration(offsetStart + segStart + float64(segDur))

			trimmedPath := filepath.Join(tempDir, fmt.Sprintf("seg_%04d_%04d.mp4", i, segIdx))
			err = s.ffmpegProc.CutSegment(ctx, actualPath, trimmedPath, cutStart, cutEnd, ffmpeg.CutOptions{
				Codec:  "h264_nvenc",
				Preset: "p4",
				CRF:    23,
			})
			if err != nil {
				s.log.Warn("cut failed", zap.Int("video", i), zap.Int("seg", segIdx), zap.Error(err))
				continue
			}

			normPath := filepath.Join(tempDir, fmt.Sprintf("norm_%04d_%04d.mp4", i, segIdx))
			err = s.ffmpegProc.Normalize(ctx, trimmedPath, normPath, ffmpeg.NormalizeOptions{
				Width:     1920,
				Height:    1080,
				FPS:       30,
				Codec:     "h264_nvenc",
				Preset:    "p4",
				CRF:       23,
				KeepAudio: true,
			})
			if err != nil {
				s.log.Warn("normalize failed", zap.Int("video", i), zap.Int("seg", segIdx), zap.Error(err))
				_ = os.Remove(trimmedPath)
				continue
			}

			processedClips = append(processedClips, normPath)
			clipTitles = append(clipTitles, fmt.Sprintf("%s_seg%d", vs.Title, segIdx))
			_ = os.Remove(trimmedPath)
		}

		_ = os.Remove(actualPath)
	}

	if len(processedClips) == 0 {
		return nil, fmt.Errorf("no clips were successfully downloaded and processed")
	}

	s.log.Info("processed clips", zap.Int("count", len(processedClips)))

	// 4. Shuffle for variety, group into chunks
	rng.Shuffle(len(processedClips), func(i, j int) {
		processedClips[i], processedClips[j] = processedClips[j], processedClips[i]
		clipTitles[i], clipTitles[j] = clipTitles[j], clipTitles[i]
	})

	clipsPerChunk := chunkDur / segDur
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

		err := s.renderChunk(ctx, chunkClips, chunkTitles, chunkPath, segDur)
		if err != nil {
			s.log.Error("failed to render chunk", zap.Int("chunk", chunkIdx), zap.Error(err))
			continue
		}

		label := "compilation"
		if len(input.SearchQueries) > 0 {
			label = slugify(input.SearchQueries[0])
		}
		chunkTitle := fmt.Sprintf("yt_compile_%s_chunk_%d", label, chunkIdx)
		folderID := input.OutputFolderID
		if folderID == "" {
			folderID = s.cfg.Drive.ClipsRootFolder
		}

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
	}

	s.log.Info("compilation pipeline complete",
		zap.Int("total_clips", len(processedClips)),
		zap.Int("chunks_uploaded", len(result.Chunks)),
		zap.Duration("duration", time.Since(start)),
	)

	return result, nil
}

type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}

// resolveQuery parses a query like "Elon Musk -25" or a channel URL
func (s *Service) resolveQuery(ctx context.Context, query string) ([]VideoSource, error) {
	query = strings.TrimSpace(query)

	// If it's already a YouTube URL, return as single source
	if strings.HasPrefix(query, "http") && (strings.Contains(query, "youtube.com") || strings.Contains(query, "youtu.be")) {
		return []VideoSource{{
			URL:    query,
			Title:  extractVideoID(query),
			Source: query,
		}}, nil
	}

	// Parse "SearchTerm -N" format (e.g., "Elon Musk -25")
	numVideos := 10
	searchTerm := query

	if idx := strings.LastIndex(query, " -"); idx > 0 {
		searchTerm = strings.TrimSpace(query[:idx])
		countStr := strings.TrimSpace(query[idx+2:])
		if c, err := fmt.Sscanf(countStr, "%d", &numVideos); err != nil || c == 0 {
			numVideos = 10
		}
	}
	if numVideos < 1 {
		numVideos = 1
	}
	if numVideos > 50 {
		numVideos = 50
	}

	s.log.Info("searching YouTube", zap.String("term", searchTerm), zap.Int("count", numVideos))

	// Use yt-dlp to search: ytsearch10:search term
	searchURL := fmt.Sprintf("ytsearch%d:%s", numVideos, searchTerm)
	videos, err := s.ytdlp.ListChannel(ctx, searchURL, numVideos)
	if err != nil {
		// Fallback: try as a channel URL
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

func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string, segDur int) error {
	if len(clips) == 0 {
		return fmt.Errorf("no clips to render")
	}
	if len(clips) == 1 {
		return copyFile(clips[0], outputPath)
	}

	var inputArgs []string
	var filterParts []string

	for i, clip := range clips {
		inputArgs = append(inputArgs, "-i", clip)
		filterParts = append(filterParts, fmt.Sprintf(
			"[%d:v]setpts=PTS-STARTPTS,scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,fps=30[v%d]",
			i, i,
		))
		filterParts = append(filterParts, fmt.Sprintf("[%d:a]asetpts=PTS-STARTPTS[a%d]", i, i))
	}

	lastVLabel := "v0"
	lastALabel := "a0"
	cumOffset := segDur

	for i := 1; i < len(clips); i++ {
		trans := transitionPresets[rng.Intn(len(transitionPresets))]
		vLabel := fmt.Sprintf("v%d", i)
		aLabel := fmt.Sprintf("a%d", i)
		outVLabel := fmt.Sprintf("vo%d", i)
		outALabel := fmt.Sprintf("ao%d", i)

		filterParts = append(filterParts, fmt.Sprintf(
			"[%s][%s]xfade=transition=%s:duration=1:offset=%d[%s]",
			lastVLabel, vLabel, trans, cumOffset-1, outVLabel,
		))

		filterParts = append(filterParts, fmt.Sprintf(
			"[%s][%s]acrossfade=duration=1:curve=tri[%s]",
			lastALabel, aLabel, outALabel,
		))

		if i%3 == 0 {
			bLabel := fmt.Sprintf("vb%d", i)
			filterParts = append(filterParts, fmt.Sprintf(
				"[%s]colorbalance=ah=%.2f:bh=%.2f:ch=%.2f[%s]",
				outVLabel, 0.75, 0.75, 0.75, bLabel,
			))
			lastVLabel = bLabel
		} else {
			lastVLabel = outVLabel
		}
		lastALabel = outALabel

		cumOffset += segDur - 1
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "warning"}
	args = append(args, inputArgs...)
	args = append(args, "-filter_complex", joinFilterParts(filterParts))
	args = append(args, "-map", fmt.Sprintf("[%s]", lastVLabel))
	args = append(args, "-map", fmt.Sprintf("[%s]", lastALabel))
	args = append(args, "-c:v", "h264_nvenc", "-preset", "p4", "-cq", "23")
	args = append(args, "-c:a", "aac", "-b:a", "128k")
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart")
	args = append(args, outputPath)

	s.log.Debug("ffmpeg render chunk",
		zap.Int("clips", len(clips)),
	)
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		in.Close()
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
	// Extract a short ID from a YouTube URL
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
	// yt-dlp may append .mp4 extension
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
