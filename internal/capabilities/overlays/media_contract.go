// Package overlays — media_contract.go is the single source of truth for
// the overlay render format contract. It replaces scattered hardcoded
// codec/container/pixel-format values across PipelineGen and RenderingGen
// with one canonical type that both sides share.
//
// Every Chronon overlay render MUST be validated against a contract before
// being published to Drive or consumed by downstream editing. The contract
// declares the expected resolution, FPS, codec, pixel format, container,
// and the invariant that overlays carry zero audio streams.
package overlays

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// OverlayMediaContractVersion is the schema version of the media contract.
const OverlayMediaContractVersion = 1

// OverlayMediaContract declares the expected properties of a rendered overlay
// artifact. Both the renderer (Chronon/RenderingGen) and the consumer
// (PipelineGen/VeloxEditing) share this contract so format mismatches are
// caught at validation time, never at composition time.
type OverlayMediaContract struct {
	// ID is the stable contract identifier (e.g. "overlay-v1").
	ID string `json:"id"`
	// Version is the contract schema version.
	Version int `json:"version"`
	// RequiresAlpha declares whether the rendered output must carry an
	// alpha channel (transparency). Chronon uses ProRes 4444 for alpha;
	// a contract with RequiresAlpha=true that produces yuv420p is invalid.
	RequiresAlpha bool `json:"requires_alpha"`
	// Width is the required output width in pixels.
	Width int `json:"width"`
	// Height is the required output height in pixels.
	Height int `json:"height"`
	// FPSNum is the FPS numerator (e.g. 30 for 30/1).
	FPSNum int `json:"fps_num"`
	// FPSDen is the FPS denominator (e.g. 1 for 30/1).
	FPSDen int `json:"fps_den"`
	// AudioStreams is the required number of audio streams. Overlays are
	// video-only; any audio stream is a contract violation.
	AudioStreams int `json:"audio_streams"`
	// Container is the expected container format (e.g. "mov", "webm", "mp4").
	Container string `json:"container"`
	// Codec is the expected video codec (e.g. "prores", "vp9", "h264").
	Codec string `json:"codec"`
	// PixelFormat is the expected pixel format (e.g. "yuva444p", "yuv420p").
	PixelFormat string `json:"pixel_format"`
}

// OverlayProbeResult is the Go-side representation of ffprobe output for a
// rendered overlay artifact. The caller populates this from ffprobe's
// JSON output; Validate checks it against the contract.
type OverlayProbeResult struct {
	// Width is the probed output width in pixels.
	Width int `json:"width"`
	// Height is the probed output height in pixels.
	Height int `json:"height"`
	// DurationUS is the probed duration in integer microseconds.
	DurationUS int64 `json:"duration_us"`
	// FPSNum is the probed FPS numerator.
	FPSNum int `json:"fps_num"`
	// FPSDen is the probed FPS denominator.
	FPSDen int `json:"fps_den"`
	// AudioStreams is the probed number of audio streams.
	AudioStreams int `json:"audio_streams"`
	// Codec is the probed video codec name.
	Codec string `json:"codec"`
	// PixelFormat is the probed pixel format.
	PixelFormat string `json:"pixel_format"`
	// Container is the probed container format.
	Container string `json:"container"`
	// SizeBytes is the probed file size in bytes.
	SizeBytes int64 `json:"size_bytes"`
	// SHA256 is the content hash of the probed artifact.
	SHA256 string `json:"sha256"`
}

// Validate checks the probed artifact against the contract. It fails closed
// on any mismatch: wrong resolution, wrong codec, wrong pixel format,
// unexpected audio streams, or missing alpha when required.
func (c OverlayMediaContract) Validate(probed OverlayProbeResult) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("overlay media contract: id is required")
	}
	if c.Version <= 0 {
		return fmt.Errorf("overlay media contract: version must be positive")
	}
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("overlay media contract: width and height must be positive")
	}
	if c.FPSNum <= 0 || c.FPSDen <= 0 {
		return fmt.Errorf("overlay media contract: fps must be positive")
	}

	// Resolution check.
	if probed.Width != c.Width {
		return fmt.Errorf("overlay media contract: width mismatch: probed %d, want %d", probed.Width, c.Width)
	}
	if probed.Height != c.Height {
		return fmt.Errorf("overlay media contract: height mismatch: probed %d, want %d", probed.Height, c.Height)
	}

	// FPS check (compare as rational to avoid floating-point drift). A
	// 0/0 probe means the stream reported no usable frame rate, which must
	// fail closed rather than trivially cross-multiplying to zero.
	if probed.FPSNum <= 0 || probed.FPSDen <= 0 {
		return fmt.Errorf("overlay media contract: probed fps %d/%d must be positive",
			probed.FPSNum, probed.FPSDen)
	}
	if probed.FPSNum*c.FPSDen != c.FPSNum*probed.FPSDen {
		return fmt.Errorf("overlay media contract: fps mismatch: probed %d/%d, want %d/%d",
			probed.FPSNum, probed.FPSDen, c.FPSNum, c.FPSDen)
	}

	// Audio streams: overlays must have zero audio streams.
	if probed.AudioStreams != c.AudioStreams {
		return fmt.Errorf("overlay media contract: audio streams mismatch: probed %d, want %d",
			probed.AudioStreams, c.AudioStreams)
	}

	// Container check (case-insensitive, comma-token aware: ffprobe's
	// format_name is a comma-joined list like "mov,mp4,m4a,3gp,3g2,mj2").
	if c.Container != "" && !strings.EqualFold(firstContainerToken(probed.Container), firstContainerToken(c.Container)) {
		return fmt.Errorf("overlay media contract: container mismatch: probed %q, want %q",
			probed.Container, c.Container)
	}

	// Codec check (case-insensitive).
	if c.Codec != "" && !strings.EqualFold(probed.Codec, c.Codec) {
		return fmt.Errorf("overlay media contract: codec mismatch: probed %q, want %q",
			probed.Codec, c.Codec)
	}

	// Pixel format check (case-insensitive).
	if c.PixelFormat != "" && !strings.EqualFold(probed.PixelFormat, c.PixelFormat) {
		return fmt.Errorf("overlay media contract: pixel format mismatch: probed %q, want %q",
			probed.PixelFormat, c.PixelFormat)
	}

	// Alpha channel check: when the contract requires alpha, the pixel
	// format must contain 'a' (yuva444p, argb, etc.).
	if c.RequiresAlpha && !strings.Contains(strings.ToLower(probed.PixelFormat), "a") {
		return fmt.Errorf("overlay media contract: alpha required but pixel format %q does not contain alpha channel",
			probed.PixelFormat)
	}

	// Duration must be positive.
	if probed.DurationUS <= 0 {
		return fmt.Errorf("overlay media contract: probed duration %d us must be positive", probed.DurationUS)
	}

	// File size must be positive.
	if probed.SizeBytes <= 0 {
		return fmt.Errorf("overlay media contract: probed size %d bytes must be positive", probed.SizeBytes)
	}

	return nil
}

// DurationMS returns the contract FPS as a float64 frame duration in
// milliseconds. Useful for computing frame-accurate start/end from
// microsecond timestamps.
func (c OverlayMediaContract) DurationMS() float64 {
	if c.FPSNum <= 0 || c.FPSDen <= 0 {
		return 0
	}
	return 1000.0 * float64(c.FPSDen) / float64(c.FPSNum)
}

// FrameAtUS converts a microsecond timestamp to the frame index at the
// contract's FPS. The result is rounded to the nearest integer frame.
func (c OverlayMediaContract) FrameAtUS(us int64) int64 {
	if c.FPSNum <= 0 || c.FPSDen <= 0 || us < 0 {
		return 0
	}
	return int64(math.Round(float64(us) * float64(c.FPSNum) / (1000000.0 * float64(c.FPSDen))))
}

// ── Predefined contracts ────────────────────────────────────────────

// DefaultOverlayContractV1 is the canonical overlay render format for
// alpha-bearing overlays (ProRes 4444 in MOV container). This is the
// contract Chronon produces for overlays that require transparency.
var DefaultOverlayContractV1 = OverlayMediaContract{
	ID:            "overlay-v1",
	Version:       OverlayMediaContractVersion,
	RequiresAlpha: true,
	Width:         1920,
	Height:        1080,
	FPSNum:        30,
	FPSDen:        1,
	AudioStreams:  0,
	Container:     "mov",
	Codec:         "prores",
	PixelFormat:   "yuva444p",
}

// DefaultOverlayContractNoAlpha is the overlay render format for overlays
// without transparency (H.264 in MP4 container). Lighter and smaller than
// the alpha contract; used for non-transparent overlays.
var DefaultOverlayContractNoAlpha = OverlayMediaContract{
	ID:            "overlay-v1-noalpha",
	Version:       OverlayMediaContractVersion,
	RequiresAlpha: false,
	Width:         1920,
	Height:        1080,
	FPSNum:        30,
	FPSDen:        1,
	AudioStreams:  0,
	Container:     "mp4",
	Codec:         "h264",
	PixelFormat:   "yuv420p",
}

// OverlayContractForCanvas returns the appropriate default contract for
// the given canvas dimensions and alpha requirement. When requiresAlpha
// is true, returns DefaultOverlayContractV1; otherwise
// DefaultOverlayContractNoAlpha. The width/height of the returned contract
// are set to match the canvas.
func OverlayContractForCanvas(width, height, fpsNum, fpsDen int, requiresAlpha bool) OverlayMediaContract {
	if requiresAlpha {
		c := DefaultOverlayContractV1
		c.Width = width
		c.Height = height
		c.FPSNum = fpsNum
		c.FPSDen = fpsDen
		return c
	}
	c := DefaultOverlayContractNoAlpha
	c.Width = width
	c.Height = height
	c.FPSNum = fpsNum
	c.FPSDen = fpsDen
	return c
}

// ResolveMediaContract resolves a contract ID (the OverlayPlan.MediaContract
// field) to its canonical OverlayMediaContract. It is the single owner of the
// ID → contract mapping; a plan carrying an unknown ID fails closed rather
// than silently rendering with the wrong codec/container.
func ResolveMediaContract(id string) (OverlayMediaContract, error) {
	switch strings.TrimSpace(id) {
	case "", DefaultOverlayContractV1.ID:
		return DefaultOverlayContractV1, nil
	case DefaultOverlayContractNoAlpha.ID:
		return DefaultOverlayContractNoAlpha, nil
	default:
		return OverlayMediaContract{}, fmt.Errorf("overlay media contract: unknown contract id %q", id)
	}
}

// ContractIDForCanvas returns the canonical contract ID for the given canvas
// and alpha requirement. It is the single owner of canvas → contract-ID
// selection; callers stamp it onto OverlayPlan.MediaContract so the render
// contract travels with the plan instead of being re-derived downstream.
func ContractIDForCanvas(width, height, fpsNum, fpsDen int, requiresAlpha bool) string {
	return OverlayContractForCanvas(width, height, fpsNum, fpsDen, requiresAlpha).ID
}

// firstContainerToken returns the first comma-separated token of an ffprobe
// format_name string, trimmed. ffprobe reports container families as a
// comma-joined list ("mov,mp4,m4a,3gp,3g2,mj2"); the first token is the
// canonical container identity used for contract comparison.
func firstContainerToken(v string) string {
	v = strings.TrimSpace(v)
	if idx := strings.IndexByte(v, ','); idx >= 0 {
		v = v[:idx]
	}
	return strings.TrimSpace(v)
}

// MediaProber is the port the render worker uses to certify a rendered
// overlay artifact. Implementations probe the rendered file through the
// canonical media probe capability (rustexec.VideoProcessor.Probe — never a
// raw ffprobe/ffmpeg subprocess) and hash it, returning the facts that
// OverlayMediaContract.Validate compares. A rendered artifact is valid only
// when it has been probed AND its facts match the contract; the renderer's
// exit code is never a validity criterion.
type MediaProber interface {
	ProbeOverlay(ctx context.Context, path string) (OverlayProbeResult, error)
}
