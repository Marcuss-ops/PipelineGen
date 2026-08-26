// Package generated (application/images/generated) — types.go holds
// the canonical DTOs and constants for the generated-image territory.
// Per PR-IMG-SPLIT-5 (July 2026), types live in their own file.
//
// Google Slides, driven through Chrome/Playwright and Nano Banana Pro,
// is the only supported generation path. The canonical model is
// CanonicalGoogleSlidesModel ("nano-banana-pro").
package images

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"time"
)

const (
	// CanonicalGoogleSlidesModel is the only model accepted by the generated
	// image pipeline. Empty model values are normalized to this value for
	// backward-compatible callers that relied on provider defaults.
	CanonicalGoogleSlidesModel = "nano-banana-pro"
)

// GenerateOptions are per-call execution options. They remain separate from
// GenerateRequest so transport-only settings do not pollute the canonical
// generation request.
type GenerateOptions struct {
	Account   string
	ProjectID string
	Timeout   time.Duration
	SkipDrive bool
}

// GenerateRequest is the provider-facing subset of images.GenerateImageRequest.
//
// surface-4 (July 2026): the Model field was retired. Image generation
// routes through the canonical CanonicalGoogleSlidesModel ("nano-banana-pro")
// only and is no longer caller-selectable.
type GenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	Tags           []string
	NegativePrompt string
	OutputPath     string
}

// GeneratedImage is the provider-facing generated image result.
type GeneratedImage struct {
	Data       []byte
	Format     string
	Width      int
	Height     int
	PromptUsed string
	Provider   detail.ImageProvider
	Model      string
	SourceHash string
	OutputPath string
}

// PortGenerateRequest is the adapter-level request passed to the backend.
//
// surface-4 (July 2026): the Model field was retired. Image generation
// routes through the canonical CanonicalGoogleSlidesModel ("nano-banana-pro")
// only and is no longer caller-selectable.
type PortGenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	NegativePrompt string
	Tags           []string
	OutputPath     string
}

// PortGeneratedImage is the adapter-level result returned by the backend.
type PortGeneratedImage struct {
	Data       []byte
	Format     string
	Width      int
	Height     int
	PromptUsed string
	Provider   string
	Model      string
	SourceHash string
	OutputPath string
}
