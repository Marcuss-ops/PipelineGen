package stockpipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
)

// processSingleVideo downloads a single video source, then extracts and normalizes
// short clips using ffmpeg. Uses a single ffmpeg invocation (CutReencodeBatch) to
// produce all clips from the source, reducing disk I/O and process spawn overhead.
func (s *Service) processSingleVideo(ctx context.Context, tempDir string, vs VideoSource, idx int, secsPerVideo int, clipDurOverride int, noAudio bool) ([]string, []string, []string, error) {
	select {
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	default:
	}

	s.log.Info("downloading from video",
		zap.Int("index", idx),
		zap.String("url", vs.URL),
		zap.String("title", vs.Title),
		zap.Int("seconds_per_video", secsPerVideo),
		zap.Float64("source_duration_sec", vs.DurationSec),
	)

	rawPath := filepath.Join(tempDir, fmt.Sprintf("raw_%04d.mp4", idx))

	startTime := rng.Float64() * math.Max(0, vs.DurationSec-float64(secsPerVideo))
	startStr := formatDuration(startTime)
	endStr := formatDuration(startTime + float64(secsPerVideo))
	section := fmt.Sprintf("*%s-%s", startStr, endStr)
	s.log.Info("video download window computed",
		zap.Int("video_index", idx),
		zap.String("download_section", section),
		zap.String("start", startStr),
		zap.String("end", endStr),
	)

	err := s.ytdlp.Download(ctx, &downloader.DownloadRequest{
		URL:              vs.URL,
		OutputPath:       rawPath,
		MergeFormat:      "mp4",
		DownloadSections: []string{section},
		ForceKeyframes:   true,
		Timeout:          10 * time.Minute,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("yt-dlp download failed for %q: %w", vs.URL, err)
	}

	actualPath := resolveActualPath(rawPath)
	if actualPath == "" {
		return nil, nil, nil, fmt.Errorf("downloaded file not found for %q", vs.URL)
	}
	if info, statErr := os.Stat(actualPath); statErr == nil {
		s.log.Info("video downloaded",
			zap.Int("video_index", idx),
			zap.String("download_path", actualPath),
			zap.Int64("download_size_bytes", info.Size()),
		)
	}

	v := s.cfg.Video.WithDefaults()
	clipDur := v.ClipDuration
	if clipDurOverride > 0 {
		clipDur = clipDurOverride
	}
	s.log.Info("stock pipeline clip duration resolved", zap.Int("clip_duration", clipDur))

	maxClipsPerSource := v.MaxClipsPerSource

	numClips := secsPerVideo / clipDur
	if numClips < 1 {
		numClips = 1
	}
	if numClips > maxClipsPerSource {
		numClips = maxClipsPerSource
	}
	s.log.Info("clip plan computed",
		zap.Int("video_index", idx),
		zap.Int("clip_duration", clipDur),
		zap.Int("max_clips_per_source", maxClipsPerSource),
		zap.Int("planned_clips", numClips),
	)

	var processedClips []string
	var clipTitles []string
	usedOffsets := make(map[float64]bool)

	maxStart := float64(secsPerVideo) - float64(clipDur)
	if maxStart < 1 {
		maxStart = 1
	}

	jobs := make([]ffmpeg.CutJob, 0, numClips)
	for clipIdx := 0; clipIdx < numClips; clipIdx++ {
		select {
		case <-ctx.Done():
			_ = os.Remove(actualPath)
			return processedClips, clipTitles, uniqueRepeat(extractVideoID(vs.URL), len(processedClips)), ctx.Err()
		default:
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

		outputPath := filepath.Join(tempDir, fmt.Sprintf("clip_%04d_%04d.mp4", idx, clipIdx))
		jobs = append(jobs, ffmpeg.CutJob{
			StartSec: offset,
			EndSec:   offset + float64(clipDur),
			Output:   outputPath,
		})
		clipTitles = append(clipTitles, fmt.Sprintf("%s_%04d", vs.Title, clipIdx))
	}

	s.log.Info("single-pass batch cut starting",
		zap.Int("video_index", idx),
		zap.Int("clip_count", len(jobs)),
		zap.Bool("no_audio", noAudio),
		zap.String("codec", v.Codec),
	)

	batchErr := s.ffmpegProc.CutReencodeBatch(ctx, actualPath, jobs, noAudio, v.Codec, v.Preset, v.CRF)
	if batchErr != nil {
		s.log.Warn("batch cut failed, falling back to individual cuts", zap.Error(batchErr))
		for i, j := range jobs {
			cutErr := s.ffmpegProc.CutReencode(ctx, actualPath, j.Output,
				ffmpeg.FormatSec(j.StartSec), ffmpeg.FormatSec(j.EndSec), noAudio, v.Codec, v.Preset, v.CRF)
			if cutErr != nil {
				s.log.Warn("fallback cut failed", zap.Int("clip", i), zap.Error(cutErr))
				continue
			}
			processedClips = append(processedClips, j.Output)
		}
		_ = os.Remove(actualPath)
		s.log.Info("video processing finished (fallback)",
			zap.Int("video_index", idx),
			zap.Int("clips_created", len(processedClips)),
		)
		return processedClips, clipTitles, uniqueRepeat(extractVideoID(vs.URL), len(processedClips)), nil
	}

	for i, j := range jobs {
		if _, err := os.Stat(j.Output); err == nil {
			processedClips = append(processedClips, j.Output)
			_ = i
		}
	}

	_ = os.Remove(actualPath)
	s.log.Info("video processing finished (single-pass)",
		zap.Int("video_index", idx),
		zap.Int("clips_created", len(processedClips)),
		zap.String("source_url", vs.URL),
	)
	return processedClips, clipTitles, uniqueRepeat(extractVideoID(vs.URL), len(processedClips)), nil
}
