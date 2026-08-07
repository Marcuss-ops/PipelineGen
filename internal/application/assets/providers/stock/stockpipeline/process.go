// Package stockpipeline — process.go refactored (PR6 + FASE 2.4, July 2026).
//
// Pre-PR6: processSingleVideo reached directly into
// `internal/infrastructure/media/ffmpeg::Processor`, constructed ffmpeg.CutJob
// values, invoked CutReencodeBatch / CutReencode via `s.ffmpegProc.*`, and
// verified outputs with `os.Stat`. All of this leaked FFmpeg knowledge into
// the application layer (violates AGENTS.md Pattern 0 + 8).
//
// Post-PR6: this file is PURE ORCHESTRATION. It computes deterministic
// non-overlapping clip window offsets, hands a neutral `stock.CutRequest` to
// the canonical `stock.VideoCutter` port, and reads CutBatchResult directly
// — no ffmpeg / process / os import for verification.
//
// FASE 2.4 (July 2026): the legacy parallel-array return shape
// `([]paths, []titles, []sourceIDs, err)` is collapsed into a single
// `[]Clip` slice. Each Clip carries Path/Title/SourceID/Status/SizeBytes/
// DurationSec/Err in one struct, eliminating the producedSet-and-index
// alignment logic the previous code paths relied on (processSingleVideo
// simply iterates CutBatchResult.Items and projects into Clips — Items
// already carry the canonical Outcome per JobID).
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
	"hash/fnv"
	"math"
	"math/rand"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// processSingleVideo downloads a single video source, then extracts and
// normalizes short clips via the canonical stock.VideoCutter port. The
// port encapsulates the batch-then-fallback-to-individual FFmpeg logic,
// the per-job on-disk verification, and the ffprobe validation step —
// the application layer stays free of ffmpeg knowledge.
//
// FASE 2.4 returns: ([]Clip, error). The Clip slice carries each
// produced clip's Path + Title + SourceID + Status + SizeBytes +
// DurationSec + Err. A non-nil error is surfaced only when the entire
// batch failed (zero Items succeeded); partial-success calls return
// nil error with a Clips slice mixing Succeeded / Validated / Failed
// items so callers iterate the slice and self-route per Status.
//
// Backward-compat: callers that previously destructured into
// `paths / titles / sourceIDs` parallel slices can substitute
// `for _, c := range clips { paths = append(paths, c.Path); ... }`
// — the Index / parallel-array contract is gone (single structured
// slice replaces it) but the per-clip fields are still individually
// extractable.
func (s *Service) processSingleVideo(ctx context.Context, tempDir string, vs VideoSource, idx int, secsPerVideo int, clipDurOverride int, noAudio bool) ([]Clip, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.log.Info("downloading from video",
		zap.Int("index", idx),
		zap.String("url", vs.URL),
		zap.String("title", vs.Title),
		zap.Int("seconds_per_video", secsPerVideo),
		zap.Float64("source_duration_sec", vs.DurationSec),
	)

	// P2 fix (July 2026): deterministic seed from video URL replaces
	// the package-level var rng (was non-deterministic time.Now().UnixNano()).
	// Same URL ⇒ same start offset ⇒ reproducible runs per godlike/07.
	videoSeed := hashFnv64(vs.URL)
	videoRng := rand.New(rand.NewSource(videoSeed))
	startTime := videoRng.Float64() * math.Max(0, vs.DurationSec-float64(secsPerVideo))
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
		ForceKeyframes:  false,
		MergeFormat:     "mp4",
	})
	if err != nil {
		return nil, fmt.Errorf("yt-dlp download failed for %q: %w", vs.URL, err)
	}

	actualPath := staged.LocalPath
	if actualPath == "" {
		return nil, fmt.Errorf("downloaded file not found for %q", vs.URL)
	}
	if s.localFS != nil {
		if info, statErr := s.localFS.Stat(actualPath); statErr == nil {
			s.log.Info("video downloaded",
				zap.Int("video_index", idx),
				zap.String("download_path", actualPath),
				zap.Int64("download_size_bytes", info.Size()),
			)
		}
	}

	clipDur := s.runtime.ClipDurationSec
	if clipDurOverride > 0 {
		clipDur = clipDurOverride
	}
	s.log.Info("stock pipeline clip duration resolved", zap.Int("clip_duration", clipDur))

	maxClipsPerSource := s.runtime.MaxResults

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

	usedOffsets := make(map[float64]bool)

	maxStart := float64(secsPerVideo) - float64(clipDur)
	if maxStart < 1 {
		maxStart = 1
	}

	// ── Build the deterministic non-overlapping cut plan ─────────────
	clipTitles := make([]string, 0, numClips)
	jobs := make([]CutJob, 0, numClips)
	for clipIdx := 0; clipIdx < numClips; clipIdx++ {
		var offset float64
		for attempt := 0; attempt < 20; attempt++ {
			offset = videoRng.Float64() * maxStart
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
		return nil, fmt.Errorf("processSingleVideo: VideoCutter port is nil — was composition root build correct?")
	}

	s.log.Info("stock extractor: hand-off to VideoCutter port",
		zap.Int("video_index", idx),
		zap.Int("clip_count", len(jobs)),
	)

	cfg := DefaultPipelineConfig()
	batch, cutErr := s.cutter.Cut(ctx, CutRequest{
		SourcePath: actualPath,
		Jobs:       jobs,
		// The cutter owns the single encoder-policy decision.
		Codec:     "",
		Preset:    cfg.Preset,
		CRF:       cfg.CRF,
		NoAudio:   noAudio,
		Logger:    s.log,
		SourceIdx: idx,
	})

	// FASE 2.4 (July 2026, audit P0 #4 continuation): the legacy
	// `producedSet` alignment map is GONE because the new contract
	// is "every Job has a CutItemResult with JobID=j.OutputPath".
	// The Items slice preserves input-Jobs order, so the per-clip
	// title / sourceID projection is a direct index-aligned loop.
	//
	// The port contract guarantees len(Items) == len(req.Jobs) (the
	// mai-nil-with-zero-output invariant). A mismatch surfaces as a
	// wire-format regression that's loud to debug but silent to the
	// caller — we panic with a typed diagnostic so the next agent
	// has an actionable stack trace rather than a misplaced title
	// on a randomly aligned clip.
	if len(batch.Items) != len(clipTitles) {
		panic(fmt.Sprintf("processSingleVideo: cutter contract violated: len(Items)=%d != len(clipTitles)=%d for source %q", len(batch.Items), len(clipTitles), vs.URL))
	}
	sourceID := extractVideoID(vs.URL)
	clips := make([]Clip, 0, len(batch.Items))
	for i, item := range batch.Items {
		clip := Clip{
			Path:        item.OutputPath,
			Title:       clipTitles[i],
			SourceID:    sourceID,
			Status:      item.Status,
			SizeBytes:   item.SizeBytes,
			DurationSec: item.DurationSec,
			Err:         item.Err,
		}
		clips = append(clips, clip)
	}

	if cutErr != nil {
		// Batch-level error propagates only when zero clips
		// succeeded (FFmpegCutter.batchErr convention). For a
		// partial-success batch the top-level err is nil and
		// FailedItems carries the per-clip reason; the caller
		// iterates Clips and self-routes per Status without
		// inspecting the err.
		successful := 0
		for _, c := range clips {
			if c.Succeeded() {
				successful++
			}
		}
		if successful == 0 {
			return nil, fmt.Errorf("cutter failed for %q with zero clips produced: %w", vs.URL, cutErr)
		}
		s.log.Warn("stock extractor: cutter reported partial / error (continuing with successful clips)",
			zap.Int("video_index", idx),
			zap.Int("clips_planned", len(jobs)),
			zap.Int("clips_produced", successful),
			zap.Int("clips_failed", len(clips)-successful),
			zap.Error(cutErr),
		)
	}

	s.log.Info("video processing finished",
		zap.Int("video_index", idx),
		zap.Int("clips_planned", len(jobs)),
		zap.Int("clips_created", len(clips)),
		zap.String("source_url", vs.URL),
	)
	return clips, nil
}

// hashFnv64 returns a deterministic int64 seed from a string using FNV-1a.
// Used by processSingleVideo to seed the per-video RNG deterministically
// (same URL ⇒ same clip offsets ⇒ reproducible runs per godlike/07).
// P2 fix (July 2026): replaces the package-level var rng which used
// time.Now().UnixNano().
func hashFnv64(s string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return int64(h.Sum64())
}
