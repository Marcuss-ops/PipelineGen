// Package images — ports.go canonicalises the image-generation port
// consumed by the images.Service and implemented by infrastructure adapters.
//
// Per FASE 2 (June 2026): the Google Slides API path (slidesSvc.Presentations.
// Create/BatchUpdate/GetThumbnail) has been removed because it produced only
// slide thumbnails containing text, not AI-generated images. The real AI
// generation pipeline uses Playwright → Chrome → slides.new → Nano Banana Pro
// and will be implemented by ChromeImageProvider (chrome_provider.go).
//
// The port is structural (signature-bearing) so compile-time assertions
// catch drift between the consumer and the concrete implementation.
package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────────

// GenerateImageRequest is the canonical request for AI image generation.
// It contains all parameters needed by any image generation provider
// (Playwright/Chrome, NVIDIA Flux, remote generation, etc.).
type GenerateImageRequest struct {
	// Prompt is the natural-language description of the desired image.
	Prompt string `json:"prompt"`

	// Style is the visual style (e.g. "cinematic", "anime", "watercolor").
	// Empty means no specific style.
	Style string `json:"style,omitempty"`

	// Width and Height are the desired output dimensions in pixels.
	// 0 means use the provider default (1920x1080 for YouTube format).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// Tags are metadata labels to attach to the generated asset.
	Tags []string `json:"tags,omitempty"`

	// NegativePrompt is the negative prompt for providers that support it
	// (Flux, NVIDIA, etc.). Empty = no negative prompt.
	// Step 4 (July 2026): populated from StyleResolver.ResolvedStyle.
	NegativePrompt string `json:"negative_prompt,omitempty"`

	// OutputPath is the canonical file path where the provider should save
	// the generated image. When set, the provider writes directly to this
	// path instead of a temporary location, enabling direct file-based
	// ingest into media_assets without an in-memory copy.
	// Empty means the provider chooses a temp path (backward-compatible
	// for sync endpoints without a workspace).
	OutputPath string `json:"output_path,omitempty"`
}

// GeneratedImage is the canonical result of a successful image generation.
// It carries the raw image bytes, metadata, and the on-disk path for
// file-based ingest into media_assets.
type GeneratedImage struct {
	// Data is the raw image bytes (PNG, JPEG, etc.).
	Data []byte `json:"-"`

	// Format is the MIME type or file extension (e.g. "png", "jpg").
	Format string `json:"format"`

	// Width and Height are the actual pixel dimensions of the generated image.
	Width  int `json:"width"`
	Height int `json:"height"`

	// PromptUsed is the exact prompt that was sent to the generator
	// (may differ from request if the provider normalises it).
	PromptUsed string `json:"prompt_used"`

	// Provider identifies which generator produced this image
	// (e.g. "google-slides", "nvidia-flux").
	// Used as the source label in media_assets.
	Provider string `json:"provider"`

	// SourceHash is a deterministic idempotency key computed as
	// SHA256(provider + normalized_prompt + style + width + height + model).
	// It prevents duplicate generation of identical requests.
	SourceHash string `json:"source_hash,omitempty"`

	// OutputPath is the canonical on-disk path where the image was saved
	// by the provider. When set, ingestGeneratedImage opens this file
	// directly instead of re-reading Data bytes, enabling zero-copy
	// file-based ingest into media_assets (FASE 9, June 2026).
	OutputPath string `json:"output_path,omitempty"`
}

// ── Structural port ───────────────────────────────────────────────────────

// ImageGenerator is the canonical port for AI image generation.
//
// The service layer depends on this interface, not on concrete providers.
// Concrete implementations (ChromeImageProvider, etc.) satisfy this port
// and are injected at composition time.
//
// The Go service must never know about:
//   - CSS selectors, Playwright cookies, googleusercontent URLs
//   - Italian button labels, fixed sleep() waits
//   - Chromium profile directories, SingletonLock
//   - Google Slides API presentation/thumbnail internals
//
// All of that belongs to the infrastructure adapter behind this port.
type ImageGenerator interface {
	// Generate produces an AI-generated image from the given request.
	// Returns ErrImageGenNotImplemented if the provider is a stub waiting
	// for real wiring (e.g. ChromeImageProvider before FASE 7-8).
	Generate(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error)
}

// ErrImageGenProviderNotAvailable is returned when an ImageGenerator
// implementation is wired but temporarily unavailable (e.g. cooldown,
// quota exceeded, auth expired).
var ErrImageGenProviderNotAvailable = fmt.Errorf("image generation provider temporarily unavailable")

// ErrImageGenPermanent is returned when a request cannot be fulfilled
// by any retry (e.g. prompt rejected by policy, unsupported format).
var ErrImageGenPermanent = fmt.Errorf("image generation request permanently rejected")

// ── Typed retryable errors (FASE 10, June 2026) ──────────────────────────
//
// Each sentinel wraps a specific failure category so the worker system
// can decide whether to retry (network, quota, auth) or dead-letter
// (policy, permanent). Callers use errors.Is to classify.

// ErrImageGenNetwork wraps transient network errors (connection refused,
// timeout, DNS resolution failure). Retryable with short backoff.
var ErrImageGenNetwork = fmt.Errorf("image generation: network error")

// ErrImageGenQuota wraps rate-limit / quota-exceeded errors from the
// provider. Retryable with long backoff + account cooldown.
var ErrImageGenQuota = fmt.Errorf("image generation: quota exceeded")

// ErrImageGenAuth wraps authentication/session errors (expired cookies,
// login required). Retryable after session refresh.
var ErrImageGenAuth = fmt.Errorf("image generation: authentication error")

// ErrImageGenPolicy wraps content-policy rejections (prompt blocked by
// provider safety filter). NOT retryable — different prompt needed.
var ErrImageGenPolicy = fmt.Errorf("image generation: content policy rejection")

// ClassifyError maps a provider-level error string to the appropriate
// typed sentinel. Used by ChromeImageProvider to wrap worker responses.
func ClassifyError(errMsg string) error {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "quota") || strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many") || strings.Contains(lower, "429"):
		return fmt.Errorf("%w: %s", ErrImageGenQuota, errMsg)
	case strings.Contains(lower, "auth") || strings.Contains(lower, "login") ||
		strings.Contains(lower, "session") || strings.Contains(lower, "cookie") ||
		strings.Contains(lower, "401") || strings.Contains(lower, "403"):
		return fmt.Errorf("%w: %s", ErrImageGenAuth, errMsg)
	case strings.Contains(lower, "policy") || strings.Contains(lower, "safety") ||
		strings.Contains(lower, "blocked") || strings.Contains(lower, "content"):
		return fmt.Errorf("%w: %s", ErrImageGenPolicy, errMsg)
	case strings.Contains(lower, "network") || strings.Contains(lower, "connection") ||
		strings.Contains(lower, "timeout") || strings.Contains(lower, "refused") ||
		strings.Contains(lower, "dns") || strings.Contains(lower, "eof"):
		return fmt.Errorf("%w: %s", ErrImageGenNetwork, errMsg)
	default:
		return fmt.Errorf("%w: %s", ErrImageGenPermanent, errMsg)
	}
}

// ComputeSourceHash returns a deterministic idempotency key for a generation
// request. SHA256(provider + "|" + normalized_prompt + "|" + style + "|" +
// width + "|" + height + "|" + model). Same request → same hash → no
// duplicate generation.
func ComputeSourceHash(provider, prompt, style string, width, height int, model string) string {
	// Normalise: trim + lowercase for consistent hashing.
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	style = strings.TrimSpace(strings.ToLower(style))
	model = strings.TrimSpace(strings.ToLower(model))

	payload := fmt.Sprintf("%s|%s|%s|%d|%d|%s", provider, prompt, style, width, height, model)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// IsRetryable returns true if the error is a typed retryable error
// (network, quota, auth) as opposed to a permanent failure (policy, other).
func IsRetryable(err error) bool {
	return errors.Is(err, ErrImageGenNetwork) ||
		errors.Is(err, ErrImageGenQuota) ||
		errors.Is(err, ErrImageGenAuth)
}
