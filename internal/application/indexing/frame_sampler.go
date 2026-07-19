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

// FFMPEGFrameSampler wraps internal/infrastructure/media/ffmpeg
// .Processor.ExtractFrame to provide the canonical "1 frame every
// N seconds" cadence. Real ffmpeg run; the production wire path.
//
// Tests can swap to a fake sampler (interface implementation) to
// bypass the ffmpeg CLI invocation.
type FFMPEGFrameSampler struct {
	proc *ffmpeg.Processor
}

// NewFFMPEGFrameSampler wires the sampler. nil processor is a hard
// error per godlike/07 (the constructor fails closed).
func NewFFMPEGFrameSampler(proc *ffmpeg.Processor) (*FFMPEGFrameSampler, error) {
	if proc == nil {
		return nil, errors.New("ffmpeg_frame_sampler: ffmpeg.Processor is nil")
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
