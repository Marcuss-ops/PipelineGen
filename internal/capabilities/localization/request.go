// Package localization owns the canonical clip-localization capability:
// the typed localization request contract (which languages, in which
// editorial order, at which render priority), its fail-fast validation,
// and — in follow-up steps — the LocalizedClipPlan v1 contract, the
// canonical fingerprint, and the LocalizedClipPlan → RenderPlan compiler.
//
// godlike/06 SSOT (one canonical owner per fact): this package is the
// SINGLE owner of "which languages does a clip get localized into". It
// does NOT extend script.OutputSpec nor duplicate its Languages /
// TranslateTo fields — the localization request is the canonical wire
// shape for the multilingual render fan-out, and downstream steps
// consume it exclusively. No second way of expressing languages is
// introduced; callers that already carry languages migrate to this
// shape instead of adding a third representation.
package localization

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// DefaultRenderConcurrency is the canonical fallback when a caller
// omits render_concurrency. It matches the admin multilingual-render
// CLI default so the HTTP request and the admin path share one
// parallelism baseline.
const DefaultRenderConcurrency = 4

// ErrInvalidRequest is the typed sentinel wrapping every validation
// failure. Callers use errors.Is for classification; the message
// carries the human-readable reason.
var ErrInvalidRequest = errors.New("invalid localization request")

// LanguageRequest is one entry of the localization fan-out.
//
//   - Language is a BCP-47 tag (e.g. "en", "es", "pt-BR"). It is
//     canonicalized through asset.Normalize (lower-case language,
//     upper-case region, hyphen separator) before validation.
//   - Priority is the render-queue priority: 0 renders first, 1..N in
//     editorial order. It is the value carried into LocalizedClipPlan
//     and LocalizedDocumentEntry so the docs/report stay deterministic.
//
// The array order of LocalizationRequest.Languages is EDITORIAL and is
// preserved independently of Priority: the doc/report always lists
// languages in request order, never in render completion order.
type LanguageRequest struct {
	Language string `json:"language"`
	Priority int    `json:"priority"`
}

// LocalizationRequest is the canonical wire payload for "localize this
// clip into these languages". It is both the HTTP body and the job
// payload consumed by the worker — one shape, no drift between
// transport and worker.
//
// The array order is EDITORIAL: element [0] is the source language
// (priority 0 in the canonical form), elements [1:] are targets in
// display order. This order survives every downstream report regardless
// of render completion order, so EN is never listed behind ES merely
// because ES finished first.
type LocalizationRequest struct {
	// Languages is the ordered list of languages to produce. At least
	// one entry is required. Duplicates (after BCP-47 canonicalization)
	// are rejected.
	Languages []LanguageRequest `json:"languages"`

	// RenderConcurrency is the render fan-out parallelism. Zero is
	// normalized to DefaultRenderConcurrency; negative is rejected.
	RenderConcurrency int `json:"render_concurrency"`
}

// Normalize applies the canonical defaults and BCP-47 canonicalization.
// It is idempotent and mutating; call before Validate and before
// persisting the payload.
//
// Invalid language tags are left untouched (not silently replaced) so
// Validate reports the exact offending entry instead of swallowing a
// malformed code into a "default" language.
func (r *LocalizationRequest) Normalize() {
	if r == nil {
		return
	}
	// Zero means omitted and receives the canonical default. Negative values
	// are caller errors and must remain visible to Validate instead of being
	// silently converted into a valid request.
	if r.RenderConcurrency == 0 {
		r.RenderConcurrency = DefaultRenderConcurrency
	}
	for i := range r.Languages {
		if code, err := asset.Normalize(r.Languages[i].Language); err == nil && code != "und" {
			r.Languages[i].Language = code
		}
	}
}

// Validate performs the fail-fast contract gate. Call Normalize first —
// zero concurrency is interpreted per the canonical default, so a
// request that was never normalized is validated against the same
// defaults.
//
// godlike/07 fail-closed: a nil request, an empty language list, a
// malformed/non-BCP-47 code, a duplicate (post-canonicalization), or a
// negative priority/concurrency never reaches the render queue.
func (r *LocalizationRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: request is nil", ErrInvalidRequest)
	}
	if len(r.Languages) == 0 {
		return fmt.Errorf("%w: languages is required (at least one language)", ErrInvalidRequest)
	}
	if r.RenderConcurrency < 1 {
		return fmt.Errorf("%w: render_concurrency must be >= 1 (got %d)", ErrInvalidRequest, r.RenderConcurrency)
	}
	seen := make(map[string]int, len(r.Languages))
	for i, lr := range r.Languages {
		code := strings.TrimSpace(lr.Language)
		if code == "" {
			return fmt.Errorf("%w: languages[%d].language is required", ErrInvalidRequest, i)
		}
		norm, err := asset.Normalize(code)
		if err != nil {
			return fmt.Errorf("%w: languages[%d].language %q is not a valid BCP-47 tag: %v", ErrInvalidRequest, i, code, err)
		}
		if norm == "und" {
			return fmt.Errorf("%w: languages[%d].language resolves to undetermined (und)", ErrInvalidRequest, i)
		}
		if lr.Priority < 0 {
			return fmt.Errorf("%w: languages[%d].priority must be >= 0 (got %d)", ErrInvalidRequest, i, lr.Priority)
		}
		if prev, dup := seen[norm]; dup {
			return fmt.Errorf("%w: languages[%d].language %q duplicates languages[%d] (same canonical BCP-47 tag)", ErrInvalidRequest, i, norm, prev)
		}
		seen[norm] = i
	}
	return nil
}
