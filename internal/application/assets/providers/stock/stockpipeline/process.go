// Package stockpipeline — process.go refactored (PR6, June 2026).
//
// Pre-PR6: processSingleVideo reached directly into
// `internal/infrastructure/media/ffmpeg::Processor`, constructed ffmpeg.CutJob
// values, invoked CutReencodeBatch / CutReencode via `s.ffmpegProc.*`, and
// verified outputs with `os.Stat`. All of this leaked FFmpeg knowledge into
// the application layer (violates AGENTS.md Pattern 0 + 8).
//
// Post-PR6: this file is PURE ORCHESTRATION. It computes deterministic
// non-overlapping clip window offsets, hands a neutral `stock.CutRequest` to
// the canonical `stock.VideoCutter` port, and uses `CutResult.ProducedPaths`
// directly — no ffmpeg / process / os import for verification.
//
// Import-boundary invariant:
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. This file respects the invariant.
package stockpipeline

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// processSingleVideo downloads a single video source, then extracts and
// normalizes short clips via the canonical stock.VideoCutter port. The
// port encapsulates the batch-then-fallback-to-individual FFmpeg logic
// and disk verification — the application layer stays free of ffmpeg
// knowledge.
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

	// Step 9/12 (July 2026): route download through the shared
	// stageSection helper instead of inline yt-dlp call. The helper
	// is also used by StockStager for the SourceStager port.
	staged, err := s.stageSection(ctx, assets.SourceRef{
		URL:             vs.URL,
		DownloadSection: section,
		ForceKeyframes:  true,
		MergeFormat:     "mp4",
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("yt-dlp download failed for %q: %w", vs.URL, err)
	}

	actualPath := staged.LocalPath
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

	var clipTitles []string
	usedOffsets := make(map[float64]bool)

	maxStart := float64(secsPerVideo) - float64(clipDur)
	if maxStart < 1 {
		maxStart = 1
	}

	// ── Build the deterministic non-overlapping cut plan ─────────────
	jobs := make([]CutJob, 0, numClips)
	for clipIdx := 0; clipIdx < numClips; clipIdx++ {
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
		jobs = append(jobs, CutJob{
			StartSec:   offset,
			EndSec:     offset + float64(clipDur),
			OutputPath: outputPath,
		})
		clipTitles = append(clipTitles, fmt.Sprintf("%s_%04d", vs.Title, clipIdx))
	}

	// ── Port delegation ──────────────────────────────────────────────
	if s.cutter == nil {
		return nil, nil, nil, fmt.Errorf("processSingleVideo: VideoCutter port is nil — was composition root build correct?")
	}

	s.log.Info("stock extractor: hand-off to VideoCutter port",
		zap.Int("video_index", idx),
		zap.Int("clip_count", len(jobs)),
	)

	res, cutErr := s.cutter.Cut(ctx, CutRequest{
		SourcePath: actualPath,
		Jobs:       jobs,
		Codec:      v.Codec,
		Preset:     v.Preset,
		CRF:        v.CRF,
		NoAudio:    noAudio,
		Logger:     s.log,
		SourceIdx:  idx,
	})

	// Blocco 4 (July 2026, audit P0 #4): the pre-Blocco-4 code
	// accepted a cutter error as a warning and returned nil (false
	// success) even when zero clips were produced. Fix:
	//   - cutErr != nil && len(res.ProducedPaths) == 0 → hard error
	//     (the cutter failed completely — caller must skip this video)
	//   - cutErr != nil && len(res.ProducedPaths) > 0 → partial success
	//     (some clips produced — continue with what we have)
	//   - clipTitles is now filtered to match ProducedPaths so the
	//     two slices stay aligned (pre-Blocco-4, clipTitles contained
	//     entries for ALL planned jobs regardless of cutter outcome).
	if cutErr != nil {
		if len(res.ProducedPaths) == 0 {
			return nil, nil, nil, fmt.Errorf("cutter failed for %q with zero clips produced: %w", vs.URL, cutErr)
		}
		// Partial success — align clipTitles with produced paths.
		s.log.Warn("stock extractor: cutter reported partial / error",
			zap.Int("video_index", idx),
			zap.Int("clips_planned", len(jobs)),
			zap.Int("clips_produced", len(res.ProducedPaths)),
			zap.Error(cutErr),
		)
	}

	// Build a set of produced paths for O(1) lookup when filtering
	// clipTitles. The pre-Blocco-4 code returned clipTitles for ALL
	// planned jobs regardless of which OutputPaths the cutter actually
	// produced; this misalignment broke downstream InterleaveClips
	// when ProducedPaths was shorter than clipTitles.
	producedSet := make(map[string]bool, len(res.ProducedPaths))
	for _, p := range res.ProducedPaths {
		producedSet[p] = true
	}
	alignedTitles := make([]string, 0, len(res.ProducedPaths))
	for i, job := range jobs {
		if producedSet[job.OutputPath] {
			alignedTitles = append(alignedTitles, clipTitles[i])
		}
	}

	s.log.Info("video processing finished",
		zap.Int("video_index", idx),
		zap.Int("clips_planned", len(jobs)),
		zap.Int("clips_created", len(res.ProducedPaths)),
		zap.String("source_url", vs.URL),
	)
	return res.ProducedPaths, alignedTitles, uniqueRepeat(extractVideoID(vs.URL), len(res.ProducedPaths)), nil
}
