// Package indexing — frame_sampler.go owns the [Build] layer of the
// VLM VisualSummary pipeline: FFmpeg-based uniform-cadence frame
// extraction from a media file at LocalPath.
//
// Split rationale (cx0030 build / cx0031 embed / cx0032 render):
//   - frame_sampler.go  : [Build]   — THIS FILE. FrameSampler port
//   - the canonical FFMPEGFrame
//     Sampler concrete impl.
//   - vlm_client.go     : [Embed]   — Python /vlm/visual-tag
//     sidecar HTTP transport.
//   - vlm_aggregator.go : [Render]  — deterministic dedup + cap
//     of per-frame responses.
//   - visual_summary.go : Orchestrator — service struct, RunJob,
//     defaults, typed errors.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// FrameSample carries metadata for a single extracted frame.
type FrameSample struct {
	// Path is the absolute filesystem path of the extracted PNG frame.
	Path string
	// Timestamp is the frame position in seconds within the source video.
	Timestamp float64
	// Percentage is the normalized position in [0,1] (0=start, 1=end).
	Percentage float64
}

// FrameSampler extracts one PNG frame every IntervalSeconds from a
// video at LocalPath. Frame paths are written to a caller-supplied
// directory that the caller is responsible for cleaning up
// (the service cleans the temp dir on each RunJob exit).
type FrameSampler interface {
	// ExtractFrames returns the absolute paths of the frames
	// extracted from LocalPath at uniform timestamps
	// (1s, 1+interval, 1+2*interval, ... up to duration).
	ExtractFrames(ctx context.Context, localPath string, intervalSeconds float64, outDir string) ([]string, error)
}

// PercentageFrameSampler extracts frames at caller-specified
// percentages of the source video duration (e.g. 0%, 25%, 50%,
// 75%, 100%). This is the canonical sampler for the multi-frame
// VLM autotag pipeline.
type PercentageFrameSampler interface {
	// ExtractPercentageFrames returns the extracted frames in the
	// same order as the requested percentages. Empty percentages or
	// percentages outside [0,1] are ignored. The caller owns outDir
	// cleanup.
	ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]FrameSample, error)
}

// FFMPEGFrameSampler wraps internal/infrastructure/media/ffmpeg
// .Processor.ExtractFrame to provide the canonical "1 frame every
// N seconds" cadence. Real ffmpeg run; the production wire path.
//
// Tests can swap to a fake sampler (interface implementation) to
// bypass the ffmpeg CLI invocation.
// FrameProcessor is the narrow media execution port required by the sampler.
// The application does not care whether the implementation is Go/FFmpeg or
// the Rust execution plane.
type FrameProcessor interface {
	Probe(context.Context, string) (*ffmpeg.MediaInfo, error)
	ExtractFrame(context.Context, string, string, float64) error
}

type FFMPEGFrameSampler struct {
	proc FrameProcessor
}

// NewFFMPEGFrameSampler wires the sampler. nil processor is a hard
// error per godlike/07 (the constructor fails closed).
func NewFFMPEGFrameSampler(proc FrameProcessor) (*FFMPEGFrameSampler, error) {
	if proc == nil {
		return nil, errors.New("ffmpeg_frame_sampler: media processor is nil")
	}
	return &FFMPEGFrameSampler{proc: proc}, nil
}

// ExtractFrames runs ffmpeg -ss {ts} -i {localPath} -frames:v 1
// {framePath} at uniform timestamps 1s, 1+interval, ... up to
// duration. Returns the absolute paths of the extracted frames.
//
// godlike/07 NO-FAKE-AVAILABILITY: every ffmpeg failure surfaces
// a wrapped error (no silent empty result); the canonical
// godlike/07 contract is that callers see every failure with
// audit-stable context (asset path + frame index + timestamp).
func (s *FFMPEGFrameSampler) ExtractFrames(ctx context.Context, localPath string, intervalSeconds float64, outDir string) ([]string, error) {
	if intervalSeconds <= 0 {
		return nil, ErrVLMJobIntervalSecondsInvalid
	}
	if strings.TrimSpace(localPath) == "" {
		return nil, ErrVLMJobConfigLocalPathRequired
	}
	info, err := s.proc.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg_frame_sampler.Probe(%q): %w", localPath, err)
	}
	duration := info.Duration.Seconds()
	if duration <= 0 {
		return nil, fmt.Errorf(
			"ffmpeg_frame_sampler: duration non-positive (%.3fs) for %q",
			duration, localPath)
	}
	var timestamps []float64
	for ts := 1.0; ts < duration; ts += intervalSeconds {
		timestamps = append(timestamps, ts)
	}
	if len(timestamps) == 0 {
		// Degenerate (sub-interval durations): sample at midpoint.
		timestamps = []float64{duration / 2.0}
	}
	frames := make([]string, 0, len(timestamps))
	for i, ts := range timestamps {
		framePath := filepath.Join(outDir, fmt.Sprintf("frame_%03d.png", i))
		if err := s.proc.ExtractFrame(ctx, localPath, framePath, ts); err != nil {
			return nil, fmt.Errorf(
				"ffmpeg_frame_sampler.ExtractFrame[idx=%d, t=%.3fs, asset=%q]: %w",
				i, ts, localPath, err)
		}
		frames = append(frames, framePath)
	}
	return frames, nil
}

// ExtractPercentageFrames extracts one frame at each requested
// percentage of the source video duration. Percentages are clamped
// to [0,1] and deduplicated after rounding to the nearest whole
// percentage. Timestamps are derived from ffprobe duration; if
// duration is unavailable, the method fails closed.
//
// The canonical cadence for the autotag video pipeline is:
//
//	0.0, 0.25, 0.50, 0.75, 1.0
//
// (start, 25%, 50%, 75%, end).
func (s *FFMPEGFrameSampler) ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]FrameSample, error) {
	if strings.TrimSpace(localPath) == "" {
		return nil, ErrVLMJobConfigLocalPathRequired
	}
	if len(percentages) == 0 {
		return nil, fmt.Errorf("ffmpeg_frame_sampler: percentages slice is empty")
	}

	info, err := s.proc.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg_frame_sampler.Probe(%q): %w", localPath, err)
	}
	durationSec := info.Duration.Seconds()
	if durationSec <= 0 {
		return nil, fmt.Errorf(
			"ffmpeg_frame_sampler: duration non-positive (%.3fs) for %q",
			durationSec, localPath)
	}

	// Normalize percentages: clamp, dedupe by rounded whole percent.
	seen := make(map[int]struct{})
	var normalized []float64
	for _, p := range percentages {
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		key := int(p*100 + 0.5)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, p)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("ffmpeg_frame_sampler: no valid percentages after normalization")
	}

	frames := make([]FrameSample, 0, len(normalized))
	for i, p := range normalized {
		ts := p * durationSec
		framePath := filepath.Join(outDir, fmt.Sprintf("frame_%03d_%.0f.png", i, p*100))
		if err := s.proc.ExtractFrame(ctx, localPath, framePath, ts); err != nil {
			return nil, fmt.Errorf(
				"ffmpeg_frame_sampler.ExtractFrame[idx=%d, t=%.3fs, asset=%q]: %w",
				i, ts, localPath, err)
		}
		frames = append(frames, FrameSample{
			Path:       framePath,
			Timestamp:  ts,
			Percentage: p,
		})
	}
	return frames, nil
}
