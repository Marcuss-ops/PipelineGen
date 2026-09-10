// Package youtube — infrastructure-level FFProbe adapter for the
// ProcessYouTubeSegmentUseCase Step 5a gate (audit 2026-07-03 BLOCKER #3).
//
// The gate exists in the use case (process_segment_step5a_ffprobe.go +
// validateFFProbeReport) but was left UNWIRED in production composition
// (build_bundles_domain_media.go "FFProbe are intentionally left zero"),
// which silently skipped clip validation: a 262-byte empty MP4 stub
// produced by a bot-checked/rate-limited yt-dlp section download passed
// Step 5 (size > 0) and the job "succeeded" with a dead artifact.
//
// This adapter routes through the shared mediaexec probe (the same Rust
// execution plane that cut_and_normalize uses), so the validation is
// consistent with the rest of the media stack.
package youtube

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	youtubeapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// Compile-time pin: FFProbeAdapter satisfies the canonical Step 5a port.
var _ youtubeapp.FFProbePort = (*FFProbeAdapter)(nil)

// MediaProbe is the narrow probe port the adapter consumes. The shared
// *rustexec.VideoProcessor implements it (Probe → mediaexec.MediaInfo).
type MediaProbe interface {
	Probe(ctx context.Context, path string) (*mediaexec.MediaInfo, error)
}

// FFProbeAdapter validates a downloaded/cut clip file via the shared
// media probe and maps the result to the canonical FFProbeReport the
// use case's fail-closed gates consume.
type FFProbeAdapter struct {
	probe MediaProbe
}

// NewFFProbeAdapter constructs the adapter around the shared probe.
func NewFFProbeAdapter(probe MediaProbe) *FFProbeAdapter {
	return &FFProbeAdapter{probe: probe}
}

// factsFromInfo maps the raw mediaexec.MediaInfo onto the canonical
// mediaexec.MediaFacts used by the CutModeResolver (single owner of the
// copy-vs-render decision). Only the fields the resolver compares are
// projected; everything else stays in MediaInfo.
func factsFromInfo(info *mediaexec.MediaInfo) *mediaexec.MediaFacts {
	if info == nil {
		return nil
	}
	return &mediaexec.MediaFacts{
		VideoCodec:  info.VideoCodec,
		PixelFormat: info.PixelFormat,
		Width:       info.Width,
		Height:      info.Height,
		FPSNum:      info.FPSNum,
		FPSDen:      info.FPSDen,
		HasAudio:    info.HasAudio,
		AudioCodec:  info.AudioCodec,
		SampleRate:  info.SampleRate,
		Channels:    info.Channels,
	}
}

// ProbeFacts implements youtubeapp.FFProbePort.ProbeFacts — the "probe
// the staged full source ONCE per extraction" owner. The fanout calls it
// before segment fan-out; a nil/errored probe degrades the extraction to
// CutModeNormalize (fail-closed, no unproven stream-copy).
func (a *FFProbeAdapter) ProbeFacts(ctx context.Context, localPath string) (*mediaexec.MediaFacts, error) {
	if a == nil || a.probe == nil {
		return nil, fmt.Errorf("ffprobe adapter: probe port not wired")
	}
	info, err := a.probe.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("ffprobe probe failed for %q: %w", localPath, err)
	}
	facts := factsFromInfo(info)
	if facts == nil || facts.Width <= 0 || facts.Height <= 0 || facts.FPSNum <= 0 || facts.FPSDen <= 0 {
		return nil, fmt.Errorf("ffprobe probe returned incomplete facts for %q", localPath)
	}
	return facts, nil
}

// ValidateClip implements youtubeapp.FFProbePort.ValidateClip.
// The use case's validateFFProbeReport applies the fail-closed gates
// (container readable / video stream present / duration tolerance /
// dimensions / FPS / audio-when-keepAudio); this adapter only needs to
// surface the raw probe facts (plus a typed error when probing fails).
func (a *FFProbeAdapter) ValidateClip(ctx context.Context, localPath string, expectedDurationSec int, keepAudio bool) (*youtubeapp.FFProbeReport, error) {
	if a == nil || a.probe == nil {
		return nil, fmt.Errorf("ffprobe adapter: probe port not wired")
	}
	info, err := a.probe.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("ffprobe probe failed for %q: %w", localPath, err)
	}
	if info == nil {
		return nil, fmt.Errorf("ffprobe probe returned nil MediaInfo for %q", localPath)
	}
	report := &youtubeapp.FFProbeReport{
		ContainerReadable:  true,
		VideoStreamPresent: info.HasVideo,
		AudioPresent:       info.HasAudio,
		DurationSeconds:    info.Duration.Seconds(),
		Width:              info.Width,
		Height:             info.Height,
		FPS:                info.FPS,
	}
	if info.Width <= 0 || info.Height <= 0 || info.FPS <= 0 || !info.HasVideo {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("probe returned partial metadata (video=%v audio=%v %dx%d fps=%.2f)",
				info.HasVideo, info.HasAudio, info.Width, info.Height, info.FPS))
	}
	return report, nil
}
