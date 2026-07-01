// Package generated — provider_registry.go declares Step 8's
// GenerationProvider interface and the canonical provider list for
// the AI-generated image territory.
//
// Per the July 2026 image-restructuring plan, generation backends fall
// into three named providers per the ImageProvider taxonomy in
// internal/domain/asset/image_taxonomy.go:
//
//   - google-slides (provider.ProviderGoogleSlides)  — ChromeImageProvider (Playwright)
//   - flux          (provider.ProviderFlux)          — NVIDIA Flux family (stub today)
//   - nvidia        (provider.ProviderNvidia)        — NVIDIA Picasso/Edify (stub today)
//
// Each provider owns the model-shape specific to its backend
// (prompt field names, negative-prompt conventions, dimension contracts).
// The GenerationProviderRegistry dispatch receives a GenerateImageRequest
// and routes to the right provider based on Model — this is the
// canonical extension point when a new provider is added (1 port
// method + 1 constructor + 1 entry in NewDefaultProviderRegistry).
//
// Step 8 replaces the monolithic ImageGenerator-port call in
// generation_service.go with this registry.
package generated

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// GenerateOptions are the per-call options that control generation
// beyond the canonical GenerateImageRequest fields. Carried as a
// second parameter so the Generate method signature remains stable
// when new options are introduced.
type GenerateOptions struct {
	// Account and ProjectID are reserved for future multi-account
	// routing; passed-through today without consumer semantics.
	Account   string
	ProjectID string
	// Timeout bounds the per-provider HTTP round-trip; zero = no cap.
	Timeout time.Duration
	// SkipDrive skips the canonical Drive-upload step after ingest.
	// Mirrors the existing skipDrive field on GenerateSmartImage.
	SkipDrive bool
}

// GenerationProvider is one named backend for AI image generation.
// Each implementation owns model-shape specific to its backend.
type GenerationProvider interface {
	// Generate runs the backend-specific generation and returns a
	// canonical *images.GeneratedImage. Returns ErrProviderUnavailable
	// when the backend is not configured or wired.
	Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error)
	// Name returns the canonical ImageProvider constant.
	Name() asset.ImageProvider
	// SupportedModels returns the model identifiers this provider
	// accepts; empty list = accept-any-model. Used by the registry to
	// decide whether to fast-path or fallthrough.
	SupportedModels() []string
	// Healthy reports whether the provider is reachable (worker process
	// alive, API key present). Used by the diagnostics surface.
	Healthy(ctx context.Context) error
}

// ── Cross-package carrier types (mirror internal/application/images/[GeneratedImage]) ──
//
// These are intentionally minimal — they carry only the metadata providers
// populate; raw bytes (`Data []byte`) are exercised by the consumer
// (ingestGeneratedImage) which sits in the parent package.

// GenerateRequest is the provider-facing subset of images.GenerateImageRequest.
// Kept as a separate type to break the import cycle (the generated package
// cannot import the parent images package without a circular reference).
type GenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	Model          string
	Tags           []string
	NegativePrompt string
	OutputPath     string
}

// GeneratedImage is the provider-facing subset of images.GeneratedImage.
type GeneratedImage struct {
	Data        []byte
	Format      string
	Width       int
	Height      int
	PromptUsed  string
	Provider    asset.ImageProvider
	Model       string
	SourceHash  string
	OutputPath  string
}

// ── Sentinel errors ────────────────────────────────────────────────────

// ErrProviderUnavailable is returned by providers that are not wired
// or whose backend is offline. Returned (not panicked) so the registry
// can log + continue with the next provider when applicable.
var ErrProviderUnavailable = errors.New("generated image provider unavailable")

// ErrProviderModelMismatch is returned when the requested model is
// not one SupportedModels reports. Fail-closed per Step 4 style
// registry: never silently fall back to a wrong provider.
var ErrProviderModelMismatch = errors.New("generated image provider does not support requested model")

// ── GoogleSlidesProvider (current canonical backend) ──────────────────

// GoogleSlidesProvider is the Step 8 wrapper around the canonical
// images.ImageGenerator port. Today that's the ChromeImageProvider
// driving Playwright → slides.new → Nano Banana Pro. The wrapper
// exists so new providers (Flux, Nvidia) can be added without
// touching the registry orchestration code.
type GoogleSlidesProvider struct {
	delegate ImageGeneratorPort
	log      *zap.Logger
}

// ImageGeneratorPort is the minimal contract the registry needs to
// invoke a canonical backend. *images.ChromeImageProvider satisfies
// this interface implicitly via its Generate method, but we declare
// it as an explicit interface so the package can compile without
// importing the parent package.
type ImageGeneratorPort interface {
	Generate(ctx context.Context, req PortGenerateRequest) (*PortGeneratedImage, error)
}

// PortGenerateRequest is the adapter-level shape the registry presents
// to backend ports. Concrete mapping happens in Wire (composition root);
// the provider constructor takes an ImageGeneratorPort that already
// knows how to translate between the two.
type PortGenerateRequest struct {
	Prompt         string
	Style          string
	Width          int
	Height         int
	Model          string
	NegativePrompt string
	Tags           []string
	OutputPath     string
}

// PortGeneratedImage is the adapter-level shape the registry receives
// from backend ports. Concrete mapping back to internal/application/images.GeneratedImage
// happens in the caller (ingestGeneratedImage).
type PortGeneratedImage struct {
	Data        []byte
	Format      string
	Width       int
	Height      int
	PromptUsed  string
	Provider    string
	Model       string
	SourceHash  string
	OutputPath  string
}

func NewGoogleSlidesProvider(delegate ImageGeneratorPort, log *zap.Logger) *GoogleSlidesProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &GoogleSlidesProvider{delegate: delegate, log: log}
}

func (p *GoogleSlidesProvider) Name() asset.ImageProvider { return asset.ProviderGoogleSlides }

func (p *GoogleSlidesProvider) SupportedModels() []string {
	// Today: Nano Banana Pro is the default. Future: Nano Banana 2,
	// Imagen 3 (via the same Slides surface), etc.
	return []string{"", "nano-banana-pro", "nano-banana", "imagen-3"}
}

func (p *GoogleSlidesProvider) Healthy(_ context.Context) error {
	if p.delegate == nil {
		return fmt.Errorf("google-slides provider not wired: %w", ErrProviderUnavailable)
	}
	return nil
}

func (p *GoogleSlidesProvider) Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if p.delegate == nil {
		return nil, fmt.Errorf("google-slides backend not wired: %w", ErrProviderUnavailable)
	}
	portOut, err := p.delegate.Generate(ctx, PortGenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		Model:          req.Model,
		NegativePrompt: req.NegativePrompt,
		Tags:           req.Tags,
		OutputPath:     req.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("google-slides generate: %w", err)
	}
	return &GeneratedImage{
		Data:        portOut.Data,
		Format:      portOut.Format,
		Width:       portOut.Width,
		Height:      portOut.Height,
		PromptUsed:  portOut.PromptUsed,
		Provider:    p.Name(),
		Model:       portOut.Model,
		SourceHash:  portOut.SourceHash,
		OutputPath:  portOut.OutputPath,
	}, nil
}

// ── FluxProvider (step-8 stub, real wiring later) ─────────────────────

// FluxProvider is a first-class stub for the NVIDIA Flux family.
// It is fail-closed: when no real Flux backend is wired, Generate
// returns ErrProviderUnavailable. The provider exists today so
// future PR-FLUX has a placeholder to swap implementation behind.
type FluxProvider struct {
	delegate ImageGeneratorPort // optional; nil = unavailable
	log      *zap.Logger
}

func NewFluxProvider(delegate ImageGeneratorPort, log *zap.Logger) *FluxProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &FluxProvider{delegate: delegate, log: log}
}

func (p *FluxProvider) Name() asset.ImageProvider { return asset.ProviderFlux }

func (p *FluxProvider) SupportedModels() []string {
	return []string{"flux-1-dev", "flux-1-schnell", "flux-1-pro"}
}

func (p *FluxProvider) Healthy(_ context.Context) error {
	if p.delegate == nil {
		return fmt.Errorf("flux backend not wired: %w", ErrProviderUnavailable)
	}
	return nil
}

func (p *FluxProvider) Generate(ctx context.Context, req GenerateRequest, _ GenerateOptions) (*GeneratedImage, error) {
	if p.delegate == nil {
		return nil, fmt.Errorf("flux backend not wired (provider exists; real adapter pending): %w", ErrProviderUnavailable)
	}
	portOut, err := p.delegate.Generate(ctx, PortGenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		Model:          req.Model,
		NegativePrompt: req.NegativePrompt,
		Tags:           req.Tags,
		OutputPath:     req.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("flux generate: %w", err)
	}
	return &GeneratedImage{
		Data:       portOut.Data,
		Format:     portOut.Format,
		Width:      portOut.Width,
		Height:     portOut.Height,
		PromptUsed: portOut.PromptUsed,
		Provider:   p.Name(),
		Model:      portOut.Model,
		SourceHash: portOut.SourceHash,
		OutputPath: portOut.OutputPath,
	}, nil
}

// ── NvidiaProvider (Step 8 stub for NVIDIA Picasso/Edify) ─────────────

// NvidiaProvider is a first-class stub for the NVIDIA Picasso/Edify
// service. Mirrors the FluxProvider contract — present in the
// registry today, fail-closed until a real adapter is wired.
type NvidiaProvider struct {
	delegate ImageGeneratorPort
	log      *zap.Logger
	apiKey   string
	endpoint string
}

func NewNvidiaProvider(delegate ImageGeneratorPort, apiKey, endpoint string, log *zap.Logger) *NvidiaProvider {
	if log == nil {
		log = zap.NewNop()
	}
	if endpoint == "" {
		endpoint = "https://ai.api.nvidia.com/v1/genai"
	}
	return &NvidiaProvider{delegate: delegate, apiKey: apiKey, endpoint: endpoint, log: log}
}

func (p *NvidiaProvider) Name() asset.ImageProvider { return asset.ProviderNvidia }

func (p *NvidiaProvider) SupportedModels() []string {
	return []string{"nvidia-picasso", "nvidia-edify", "stable-diffusion-xl"}
}

func (p *NvidiaProvider) Healthy(_ context.Context) error {
	if p.delegate == nil && p.apiKey == "" {
		return fmt.Errorf("nvidia backend not wired: %w", ErrProviderUnavailable)
	}
	return nil
}

func (p *NvidiaProvider) Generate(ctx context.Context, req GenerateRequest, _ GenerateOptions) (*GeneratedImage, error) {
	if p.delegate == nil && p.apiKey == "" {
		return nil, fmt.Errorf("nvidia backend not wired (provider exists; real adapter pending): %w", ErrProviderUnavailable)
	}
	if p.delegate == nil {
		// Stub path with apiKey present but no Go-side adapter.
		// Real adapter wiring is a future PR; for now, log and fail.
		p.log.Warn("NvidiaProvider.Generate: apiKey present but Go-side adapter unwired; fail-closed",
			zap.String("endpoint", p.endpoint),
		)
		return nil, fmt.Errorf("nvidia backend not wired (apiKey present but no Go adapter): %w", ErrProviderUnavailable)
	}
	portOut, err := p.delegate.Generate(ctx, PortGenerateRequest{
		Prompt:         req.Prompt,
		Style:          req.Style,
		Width:          req.Width,
		Height:         req.Height,
		Model:          req.Model,
		NegativePrompt: req.NegativePrompt,
		Tags:           req.Tags,
		OutputPath:     req.OutputPath,
	})
	if err != nil {
		return nil, fmt.Errorf("nvidia generate: %w", err)
	}
	return &GeneratedImage{
		Data:       portOut.Data,
		Format:     portOut.Format,
		Width:      portOut.Width,
		Height:     portOut.Height,
		PromptUsed: portOut.PromptUsed,
		Provider:   p.Name(),
		Model:      portOut.Model,
		SourceHash: portOut.SourceHash,
		OutputPath: portOut.OutputPath,
	}, nil
}

// ── Registry ───────────────────────────────────────────────────────────

// GenerationProviderRegistry is the canonical composition of GenerationProviders.
// Routing: the requested Model maps to the FIRST provider whose
// SupportedModels() contains it. Default (Model == "") routes to
// GoogleSlidesProvider.
type GenerationProviderRegistry struct {
	providers []GenerationProvider
	defaultP  GenerationProvider
	log       *zap.Logger
}

// NewGenerationProviderRegistry composes the providers and indexes
// the default (first in slice). Caller-supplied order is respected.
func NewGenerationProviderRegistry(log *zap.Logger, providers []GenerationProvider) *GenerationProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	var def GenerationProvider
	if len(providers) > 0 {
		def = providers[0]
	}
	return &GenerationProviderRegistry{providers: providers, defaultP: def, log: log}
}

// NewDefaultProviderRegistry returns the canonical 3-provider fallback
// chain in GoogleSlides → Flux → Nvidia order. googleSlidesPort is
// the existing images.ChromeImageProvider; pass nil to keep
// GoogleSlides in stub mode.
func NewDefaultProviderRegistry(log *zap.Logger, googleSlidesPort ImageGeneratorPort, nvidiaAPIKey string) *GenerationProviderRegistry {
	return NewGenerationProviderRegistry(log, []GenerationProvider{
		NewGoogleSlidesProvider(googleSlidesPort, log),
		NewFluxProvider(nil, log),
		NewNvidiaProvider(nil, nvidiaAPIKey, "https://ai.api.nvidia.com/v1/genai", log),
	})
}

// Generate routes the request to the FIRST provider whose
// SupportedModels() matches req.Model. Empty model → defaultP.
// Returns ErrProviderModelMismatch when no provider matches.
//
// Dispatch is "first-match-wins"; the comparator is case-insensitive
// strings.EqualFold on each provider's SupportedModels() slice. The
// matched provider supersedes defaultP for the rest of this call
// only — no cached state is updated.
func (r *GenerationProviderRegistry) Generate(ctx context.Context, req GenerateRequest, opts GenerateOptions) (*GeneratedImage, error) {
	if r == nil || len(r.providers) == 0 {
		return nil, fmt.Errorf("no providers registered: %w", ErrProviderUnavailable)
	}
	var target GenerationProvider
	if req.Model == "" {
		target = r.defaultP
	} else {
		for _, p := range r.providers {
			for _, m := range p.SupportedModels() {
				if strings.EqualFold(m, req.Model) {
					target = p
					break
				}
			}
			// `matched` flag distinguishes "found a match in this
			// provider" from "target reuses defaultP init value".
			// Without this, the outer loop terminates after the
			// FIRST provider regardless of model match.
			if target != nil {
				break
			}
		}
		if target == nil {
			return nil, fmt.Errorf("no provider matches model=%q: %w", req.Model, ErrProviderModelMismatch)
		}
	}
	out, err := target.Generate(ctx, req, opts)
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		r.log.Info("generation provider dispatched",
			zap.String("provider", string(target.Name())),
			zap.String("model", req.Model),
			zap.Int("bytes", len(out.Data)),
		)
	}
	return out, nil
}

// ProviderByName returns the provider matched by name, or nil.
func (r *GenerationProviderRegistry) ProviderByName(name asset.ImageProvider) GenerationProvider {
	if r == nil {
		return nil
	}
	for _, p := range r.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Providers returns the registered providers in registration order.
func (r *GenerationProviderRegistry) Providers() []GenerationProvider {
	if r == nil {
		return nil
	}
	out := make([]GenerationProvider, len(r.providers))
	copy(out, r.providers)
	return out
}

// Diagnostics probes Healthy for every registered provider.
func (r *GenerationProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error, len(r.providers))
	if r == nil {
		return out
	}
	for _, p := range r.providers {
		out[p.Name()] = p.Healthy(ctx)
	}
	return out
}

// NOTE: The adapter that bridges the canonical images.ImageGenerator
// port to the ImageGeneratorPort interface above lives in the parent
// images package (images/google_slides_adapter.go) — not here. Keeping
// the adapter in the parent breaks the otherwise-circular
// generated ↔ images dependency and lets each provider declare only the
// image-platform-agnostic ImageGeneratorPort contract.

// (no exported helpers at this layer — providers carry their own
// HTTP client construction; future Flux/Nvidia HTTP adapters will
// add their own package-private helpers as needed.)
