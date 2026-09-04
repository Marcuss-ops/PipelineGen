package generation

import (
	"context"
	"time"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

const CanonicalGoogleSlidesModel = "nano-banana-pro"

// GenerateOptions carries execution-only options. Provider selection is not
// exposed here: Google Slides is the only production generation provider.
type GenerateOptions struct {
	Account   string
	ProjectID string
	Timeout   time.Duration
	SkipDrive bool
}

// GenerateImageRequest is the canonical application↔infrastructure request.
type GenerateImageRequest struct {
	Prompt         string   `json:"prompt"`
	Style          string   `json:"style,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	PromptSuffix   string   `json:"prompt_suffix,omitempty"`
	Ratio          string   `json:"ratio,omitempty"`
	OutputPath     string   `json:"output_path,omitempty"`
}

// GenerateRequest is the provider-facing spelling of the same canonical DTO.
type GenerateRequest = GenerateImageRequest

// GeneratedImage is the canonical result returned by the generation plane.
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

// ImageGenerator is the single infrastructure port. Chrome/Playwright and its
// pool implement this directly; no DTO-converting adapter is required.
type ImageGenerator interface {
	Generate(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// Provider is the capability-level provider contract owned by generation.
type Provider interface {
	Generate(ctx context.Context, req GenerateImageRequest, opts GenerateOptions) (*GeneratedImage, error)
	TriggerPrewarm(ctx context.Context, jobID string, count int)
	Name() detail.ImageProvider
	Healthy(ctx context.Context) error
}
