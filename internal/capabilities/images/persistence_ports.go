// Package images — ports.go canonicalises the image-generation port
// consumed by the images.Service and implemented by infrastructure adapters.
//
// Per FASE 2 (June 2026): the Google Slides API path (slidesSvc.Presentations.
// Create/BatchUpdate/GetThumbnail) has been removed because it produced only
// slide thumbnails containing text, not AI-generated images. The real AI
// generation pipeline uses Playwright → Chrome → slides.new → Nano Banana Pro
// and is implemented by the infrastructure adapter in
// internal/infrastructure/images/chrome.
//
// The port is structural (signature-bearing) so compile-time assertions
// catch drift between the consumer and the concrete implementation.
package images

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────────

// GenerateImageRequest is the canonical request for AI image 
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

	// P1.1 (July 2026): free-text escape-hatch that the worker appends
	// to the prompt in the Slides textarea fill. Useful for callers
	// who need a custom worker-side composition format (e.g. the
	// "negative_keywords: avoid ..." directive if the canonical
	// Go-side ComposePrompt format `[negative: do not include Y]`
	// isn't suitable). Empty means no extra suffix; the canonical
	// P1.2 composition `[style: X] [negative: ...]` remains the
	// default.
	PromptSuffix string `json:"prompt_suffix,omitempty"`

	// P1.1 (July 2026): overrides the default 16:9 ratio the worker
	// selects in the Slides "Proporzioni" dropdown. Empty defaults
	// to "16:9" on the worker side. Forwarded as-is via workerReq;
	// no Go-side validation (the worker's post-click DOM verify is
	// the source of truth for whether the selected ratio matches).
	Ratio string `json:"ratio,omitempty"`

	// OutputPath is the canonical file path where the provider should save
	// the generated image. When set, the provider writes directly to this
	// path instead of a temporary location, enabling direct file-based
	// ingest into media_assets without an in-memory copy.
	// Empty means the provider chooses a temp path (backward-compatible
	// for sync endpoints without a workspace).
	OutputPath string `json:"output_path,omitempty"`
}

// ── Structural port ───────────────────────────────────────────────────────

// ImageGenerator is the canonical port for AI image 
//
// The service layer depends on this interface, not on concrete providers.
// Concrete infrastructure implementations satisfy this port
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
	// for real wiring.
	Generate(ctx context.Context, req GenerateImageRequest) (*GeneratedImage, error)

	// TriggerPrewarm asks the backend to start or warm its worker pool.
	// The count is an advisory ceiling for pooled implementations.
	TriggerPrewarm(ctx context.Context, jobID string, count int)
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

// ErrImageGenNoImageCandidate is the typed sentinel surfaced when the
// Playwright worker reports it could not extract a generated image from
// the slides.new panel (no candidate appeared, or only candidates that
// are clearly stale/from a previous generation).
//
// godlike/07 FAIL-CLOSED contract (P0.1, July 2026): the pre-fix behaviour
// had a generic 'File → Download → PNG' fallback which exported the
// CURRENT (often empty) slide as a PNG, producing blank/white artifacts
// that passed byte-level validation on the Go side and were ingested as
// valid GeneratedImage rows. We REMOVE that fallback. When the worker
// reports this error, the Go side MUST (a) remove any file at the
// canonical output_path, (b) NOT return a GeneratedImage to the caller,
// (c) surface the typed error so the worker / retry policy can decide
// retry vs dead-letter (the worker may have been on a stale page; this
// is RETRYABLE — surfaced via ErrImageGenNetwork classification in the
// top-level retry.Decision walker via errMsg classification).
var ErrImageGenNoImageCandidate = fmt.Errorf("image generation: no image candidate (worker reported ErrNoImageCandidate)")

// ErrImageGenBlankOrPlaceholder is the typed sentinel surfaced when the
// post-extraction visual_validate pass rejects the file at output_path:
//   - white_pct exceeds the style-gated threshold (>99% standard, >99.8% whiteboard)
//   - variance < 5 (monochrome fill)
//   - pHash distance from the slide-vuoto reference hash is <= 5 (too
//     similar to the canonical blank placeholder)
//
// godlike/07 FAIL-CLOSED contract (P0.2, July 2026): the pre-fix
// pipeline only checked file size and SHA-256 on the generated PNG,
// so any blank PNG (a real artifact produced by the worker over an empty
// slides.new page) was ingested as a valid GeneratedImage. We close
// the gap by decoding + asserting content invariants in
// the infrastructure adapter's visual validation package. The path
// is FAIL-CLOSED: chrome_provider.Generate removes output_path, callers
// see a typed error, the worker retry policy sees a deterministic
// retryable (the page may have been stale; the same prompt with a
// fresh page should not re-produce a blank).
//
// This error is mapped onto the ERR_BLANK_OR_PLACEHOLDER or
// errblankorplaceholder code from worker responses. The sentinel is
// recommended as RETRYABLE (transient environment in the panel) per
// retry.IsTransient contract via errMsg classification.
var ErrImageGenBlankOrPlaceholder = fmt.Errorf("image generation: blank/placeholder detected by visual_validate")

// ErrImageGenTimeout is the typed sentinel surfaced when the worker
// waited the canonical 60s for a new image to appear in the
// docs-content-library-image-generation-item panel and saw no new
// candidate that differed from the pre-click baseline.
//
// godlike/07 FAIL-CLOSED contract (P0.4, July 2026): the pre-fix
// behaviour logged a screenshot and PROCEEDED with extraction,
// potentially ingesting a stale image from a previous 
// We RETIRE that path: timeout is a terminal failure for this
// request, the worker reports ErrImageGenTimeout, and the caller
// (and the page-recycle wiring in chrome_provider.go::resetWorker)
// decides whether to retry on a fresh page.
//
// Recommended as RETRYABLE via the errgenerationtimeout substring
// path in ClassifyError.
var ErrImageGenTimeout = fmt.Errorf("image generation: timeout waiting for new candidate (worker reported ErrGenerationTimeout)")

// ErrImageGenPolicy wraps content-policy rejections (prompt blocked by
// provider safety filter). NOT retryable — different prompt needed.
var ErrImageGenPolicy = fmt.Errorf("image generation: content policy rejection")

// ErrImageGenRatioNotSelected is surfaced when the worker reports that
// the mandatory 16:9 ratio selection failed during request prep. The
// failure mode is one of:
//
//   - the "Proporzioni" button was not visible (panel in a different
//     state — e.g. not on the image-mode tab);
//   - the 16:9 option locator was not reachable (dropdown collapses
//     before the click registers);
//   - the post-click DOM query (`_check_169_selected`) did not surface
//     a label containing "16:9" (the click registered on a different
//     element).
//
// godlike/07 fail-closed contract (P1.3, July 2026): the pre-fix
// `except: pass` in slide_worker.py::Step 3 silently accepted whatever
// ratio the panel happened to have (often the prior request's 16:9 or
// 4:3). We now return a typed error so the Go side can resetWorker +
// retry-once. The retry path uses a freshly-launched subprocess so the
// next request opens on a clean panel where the 16:9 menu can be
// re-selected without contamination from the failed attempt.
//
// Note: this is RETRYABLE via the chrome_provider.Generate retry-once
// path (not via retry.IsTransient / retry.Decision — it is a panel-
// state-specific recovery, not a generic transient).
var ErrImageGenRatioNotSelected = fmt.Errorf("image generation: 16:9 ratio not selected (mandatory UI step failed)")

// ClassifyError maps a provider-level error string to the appropriate
// typed sentinel. Used by the Chrome infrastructure adapter to wrap worker responses.
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
	case strings.Contains(lower, "errnoimagecandidate"):
		// P0.1 (July 2026): FAIL-CLOSED typed sentinel for the 'extraction
		// failed AND no slide-export fallback' path. The pre-fix code
		// exposed a generic 'no image extracted' string and fell back to
		// exporting the current slide — that produced white artifacts
		// that passed byte-level validation. We now surface a typed
		// sentinel so callers (and the chrome_provider fail-closed path)
		// can distinguish 'candidate not recoverable' from any other
		// terminal failure.
		//
		// Ordering rationale: this case is placed BEFORE the network/timeout
		// case so a compound error like 'ErrNoImageCandidate: network timeout'
		// is still classified as ErrImageGenNoImageCandidate, NOT as a
		// retryable network error (the FAIL-CLOSED contract on the worker
		// side is what we want to surface, not the underlying transport
		// signal). Review feedback P0.1: a future 'sentinel-specific detection
		// helper' cut can refactor this switch into a registry of
		// (substring → sentinel) pairs to make the precedence explicit
		// without the case-ordering dance.
		return fmt.Errorf("%w: %s", ErrImageGenNoImageCandidate, errMsg)
	case strings.Contains(lower, "errblankorplaceholder"):
		// P0.2 (July 2026): typed sentinel for the visual-validate
		// FAIL-CLOSED path. The content validator (visual_validate
		// package) rejects near-white / monochrome / slide-vuoto
		// images; this branch surfaces the resulting code from the
		// worker. Placed before network/timeout for the same
		// reason as ErrNoImageCandidate above: typed contract wins
		// over transport signal.
		return fmt.Errorf("%w: %s", ErrImageGenBlankOrPlaceholder, errMsg)
	case strings.Contains(lower, "errgenerationtimeout"):
		// P0.4 (July 2026): typed sentinel for the 'no new candidate
		// after 60s polling' path. Placed before network/timeout to
		// ensure the typed timeout (a content-event) wins over a
		// generic transport signal.
		return fmt.Errorf("%w: %s", ErrImageGenTimeout, errMsg)
	case strings.Contains(lower, "errimagegenrationotselected") || strings.Contains(lower, "ratio-not-selected"):
		// P1.3 (July 2026): typed sentinel for the mandatory 16:9
		// selection failure. Placed before the generic network/timeout
		// fallthrough so the typed panel-state recovery contract is
		// surfaced, NOT a transport-class retry. The Generate() loop
		// recognises this specific sentinel via errors.Is(err,
		// ErrImageGenRatioNotSelected) and triggers resetWorker +
		// retry-once instead of bubbling the error to the caller.
		return fmt.Errorf("%w: %s", ErrImageGenRatioNotSelected, errMsg)
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
// duplicate 
func ComputeSourceHash(provider, prompt, style string, width, height int, model string) string {
	// Normalise: trim + lowercase for consistent hashing.
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	style = strings.TrimSpace(strings.ToLower(style))
	model = strings.TrimSpace(strings.ToLower(model))

	payload := fmt.Sprintf("%s|%s|%s|%d|%d|%s", provider, prompt, style, width, height, model)
	h := digest.SHA256Bytes([]byte(payload))
	return h
}

// IsRetryable returns true if the error is a typed retryable error
// (network, quota, auth) as opposed to a permanent failure (policy, other).
func IsRetryable(err error) bool {
	return errors.Is(err, ErrImageGenNetwork) ||
		errors.Is(err, ErrImageGenQuota) ||
		errors.Is(err, ErrImageGenAuth)
}

// ── Subject/Tags extraction port (PR C9, July 2026) ────────────────────
//
// Replaces the silent-fake stub `extractSubjectAndTags` (which returned
// `("", nil)` for any description, violating godlike/07 no-fake-availability).
// The real port returns typed-error sentinels for hard failures
// (empty description, no subject derivable) and useful data on success.
// Callers compose subject/tags from description (e.g. image generation
// prompt) and use them as a hint when the upstream payload is missing
// the subject+tags fields.

// ErrEmptyDescription is returned when the description is empty or
// whitespace-only. NOT retryable.
var ErrEmptyDescription = errors.New("subject/tags: empty description")

// ErrNoSubjectDerivable is returned when no capitalized word is found
// in the description (no candidate subject slug). NOT retryable.
var ErrNoSubjectDerivable = errors.New("subject/tags: no subject derivable from description")

// SubjectTagsService extracts a subject slug + tag list from a free-form
// description (typically the AI image generation prompt).
//
// Returns ErrEmptyDescription if description is empty/whitespace-only.
// Returns ErrNoSubjectDerivable if no capitalized word is present
// (cannot derive a slug from the description).
//
// On success, the returned subject is a non-empty slug; the returned
// tags are de-duplicated and the subject slug is filtered out to avoid
// duplication. The order is stable: subject is the FIRST capitalized
// word; tags are sorted by extraction order.
type SubjectTagsService interface {
	ExtractSubjectAndTags(ctx context.Context, description string) (subject string, tags []string, err error)
}
