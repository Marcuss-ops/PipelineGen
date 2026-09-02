package adapters

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

// NewClipRenderSubtitleCompiler returns the deterministic ASS subtitle
// compiler (texttracks.CompileASSContent — the single owner of ASS content).
func NewClipRenderSubtitleCompiler() *ClipRenderSubtitleCompiler {
	return &ClipRenderSubtitleCompiler{}
}

// NewClipRenderTranscriptResolver wires the transcript resolver. repo may be
// nil (a nil repo fails closed on Lookup/Generate); the streaming transcriber
// is attached separately via SetStreaming.
func NewClipRenderTranscriptResolver(log *zap.Logger) *ClipRenderTranscriptResolver {
	return &ClipRenderTranscriptResolver{log: log}
}

// SetRepo attaches the canonical text-track repository.
func (r *ClipRenderTranscriptResolver) SetRepo(repo detail.TextTrackRepository) { r.repo = repo }

// SetAcquire attaches the canonical Whisper acquisition service (fallback).
func (r *ClipRenderTranscriptResolver) SetAcquire(acquire *texttracks.AcquireService) {
	r.acquire = acquire
}

// SetCueWriter attaches the canonical timed-cue writer.
func (r *ClipRenderTranscriptResolver) SetCueWriter(cueWriter texttracks.TimedCueWriter) {
	r.cueWriter = cueWriter
}

// SetStreaming attaches the streaming PCM transcriber (preferred path).
func (r *ClipRenderTranscriptResolver) SetStreaming(streaming *ClipRenderStreamingTranscriber) {
	r.streaming = streaming
}

// NewOverlaySegmentResolver wires the overlays content-cache resolver.
func NewOverlaySegmentResolver(cache *infraoverlays.Cache) *OverlaySegmentResolver {
	return &OverlaySegmentResolver{cache: cache}
}

// NewFFmpegOverlayCompositor wires the ffmpeg blend compositor with the
// composition-root encoder policy.
func NewFFmpegOverlayCompositor(ffmpegPath, codec, preset string, crf int) *FFmpegOverlayCompositor {
	return &FFmpegOverlayCompositor{ffmpegPath: ffmpegPath, codec: codec, preset: preset, crf: crf}
}
