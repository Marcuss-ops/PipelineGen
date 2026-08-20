package rustexec

// backend_capabilities.go — the concrete ffmpeg-backed render capability
// probe for the cliprender.BackendCapabilityProbe port. It detects the
// hardware stages the ffmpeg binary actually exposes (NVENC encoders + the
// CUDA hwaccel). GPU scale and alpha are probed from the actual filter set;
// ASS still rasterizes on a transparent overlay layer while the full-size
// video surface remains device-local through overlay_cuda and NVENC.

import (
	"context"
	"os/exec"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ffmpegBackendCapabilityProbe probes the host through the canonical ffmpeg
// binary. It is wired at the composition root and satisfies the capability's
// BackendCapabilityProbe port.
type ffmpegBackendCapabilityProbe struct {
	ffmpegPath string
}

// NewFFmpegBackendCapabilityProbe constructs the canonical probe. An empty
// ffmpegPath falls back to `ffmpeg` on PATH.
func NewFFmpegBackendCapabilityProbe(ffmpegPath string) cliprender.BackendCapabilityProbe {
	return &ffmpegBackendCapabilityProbe{ffmpegPath: ffmpegPath}
}

// ProbeCapabilities runs `ffmpeg -encoders` and `ffmpeg -hwaccels` and maps
// the results onto the capability set. Fail-closed: any probe failure is
// returned as an error (the caller resolves to the software fallback).
func (p *ffmpegBackendCapabilityProbe) ProbeCapabilities(ctx context.Context) (cliprender.RendererCapabilities, error) {
	capabilities := cliprender.RendererCapabilities{}

	encoders, err := p.run(ctx, "-encoders")
	if err != nil {
		return capabilities, err
	}
	capabilities.NVENCH264 = tokenPresent(encoders, config.EncoderNVENCH264)
	capabilities.NVENCHEVC = tokenPresent(encoders, config.EncoderNVENCHEVC)

	hwaccels, err := p.run(ctx, "-hwaccels")
	if err != nil {
		return capabilities, err
	}
	capabilities.NVDEC = tokenPresent(hwaccels, "cuda")
	filters, err := p.run(ctx, "-filters")
	if err != nil {
		return capabilities, err
	}
	capabilities.GPUScale = tokenPresent(filters, "scale_cuda")
	capabilities.GPUAlpha = tokenPresent(filters, "overlay_cuda")
	return capabilities, nil
}

func (p *ffmpegBackendCapabilityProbe) run(ctx context.Context, flag string) (string, error) {
	ffmpeg := p.ffmpegPath
	if strings.TrimSpace(ffmpeg) == "" {
		ffmpeg = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", flag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// tokenPresent reports whether token appears as a standalone whitespace-delimited
// field (the ffmpeg encoder/hwaccel listings use fixed-width columns).
func tokenPresent(output, token string) bool {
	for _, line := range strings.Split(output, "\n") {
		for _, field := range strings.Fields(line) {
			if field == token {
				return true
			}
		}
	}
	return false
}
