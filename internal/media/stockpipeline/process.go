package stockpipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/pkg/media/downloader"
)

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
		return nil, nil, fmt.Errorf("yt-dlp download failed for %q: %w", vs.URL, err)
	}

	actualPath := resolveActualPath(rawPath)
	if actualPath == "" {
		return nil, nil, fmt.Errorf("downloaded file not found for %q", vs.URL)
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

	type clipJob struct {
		clipIdx    int
		cutStart   string
		cutEnd     string
		outputPath string
		title      string
	}
	clipJobs := make([]clipJob, 0, numClips)

	maxStart := float64(secsPerVideo) - float64(clipDur)
	if maxStart < 1 {
		maxStart = 1
	}

	for clipIdx := 0; clipIdx < numClips; clipIdx++ {
		select {
		case <-ctx.Done():
			_ = os.Remove(actualPath)
			return processedClips, clipTitles, ctx.Err()
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

		cutStart := formatDuration(offset)
		cutEnd := formatDuration(offset + float64(clipDur))
		outputPath := filepath.Join(tempDir, fmt.Sprintf("clip_%04d_%04d.mp4", idx, clipIdx))
		clipJobs = append(clipJobs, clipJob{
			clipIdx:    clipIdx,
			cutStart:   cutStart,
			cutEnd:     cutEnd,
			outputPath: outputPath,
			title:      fmt.Sprintf("%s_%04d", vs.Title, clipIdx),
		})
	}

	const maxParallelCutdown = 3
	workers := maxParallelCutdown
	if workers > len(clipJobs) {
		workers = len(clipJobs)
	}
	if workers < 1 {
		workers = 1
	}
	s.log.Info("fast cut worker pool configured",
		zap.Int("video_index", idx),
		zap.Int("workers", workers),
		zap.Int("clip_jobs", len(clipJobs)),
	)

	jobCh := make(chan clipJob)
	type clipResult struct {
		idx   int
		path  string
		title string
		err   error
	}
	resultCh := make(chan clipResult, len(clipJobs))
	var cutWG sync.WaitGroup

	for workerIdx := 0; workerIdx < workers; workerIdx++ {
		cutWG.Add(1)
		go func(workerID int) {
			defer cutWG.Done()
			for job := range jobCh {
				select {
				case <-ctx.Done():
					resultCh <- clipResult{idx: job.clipIdx, err: ctx.Err()}
					continue
				default:
				}

				s.log.Info("extracting clip",
					zap.Int("video_index", idx),
					zap.Int("clip_index", job.clipIdx),
					zap.Int("worker_id", workerID),
					zap.String("input_path", actualPath),
					zap.String("cut_start", job.cutStart),
					zap.String("cut_end", job.cutEnd),
				)
				s.log.Info("fast cut starting",
					zap.Int("video_index", idx),
					zap.Int("clip_index", job.clipIdx),
					zap.Int("worker_id", workerID),
					zap.String("input_path", actualPath),
					zap.String("output_path", job.outputPath),
				)

				err := s.ffmpegProc.CutCopy(ctx, actualPath, job.outputPath, job.cutStart, job.cutEnd)
				if err != nil {
					resultCh <- clipResult{idx: job.clipIdx, err: err}
					continue
				}

				resultCh <- clipResult{idx: job.clipIdx, path: job.outputPath, title: job.title}
			}
		}(workerIdx)
	}

	go func() {
		for _, job := range clipJobs {
			jobCh <- job
		}
		close(jobCh)
		cutWG.Wait()
		close(resultCh)
	}()

	orderedClips := make([]string, len(clipJobs))
	orderedTitles := make([]string, len(clipJobs))
	clipOK := make([]bool, len(clipJobs))

	for res := range resultCh {
		if res.err != nil {
			s.log.Warn("fast cut failed", zap.Int("video", idx), zap.Int("clip", res.idx), zap.Error(res.err))
			continue
		}
		orderedClips[res.idx] = res.path
		orderedTitles[res.idx] = res.title
		clipOK[res.idx] = true
		s.log.Info("clip extracted",
			zap.Int("video_index", idx),
			zap.Int("clip_index", res.idx),
			zap.String("output_path", res.path),
		)
	}

	for i := range orderedClips {
		if !clipOK[i] {
			continue
		}
		processedClips = append(processedClips, orderedClips[i])
		clipTitles = append(clipTitles, orderedTitles[i])
	}

	_ = os.Remove(actualPath)
	s.log.Info("video processing finished",
		zap.Int("video_index", idx),
		zap.Int("clips_created", len(processedClips)),
		zap.String("source_url", vs.URL),
	)
	return processedClips, clipTitles, nil
}
