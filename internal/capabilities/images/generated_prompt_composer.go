// Package generated — prompt_composer.go is the single source of truth
// for prompt composition (FASE 3, July 2026, image-territories action plan).
//
// Every generation flow routes the user-supplied prompt through Compose
// to produce a canonical ResolvedGenerationRequest. The local
// `promptComposer(original, suffix) string` helper in
// internal/capabilities/images/workflow/generation_service.go (Step 4) is the
// legacy helper; this commit introduces the canonical port.
//
// Composition rules (godlike/07 fail-closed, no fake availability):
//   - Empty style.PromptSuffix → PromptFinal == PromptOriginal (no decoration).
//   - Empty PromptOriginal + non-empty suffix → PromptFinal == suffix.
//   - Non-empty both → PromptFinal == promptOriginal + ", " + suffix.
//   - Idempotent: if PromptOriginal already ends with style.PromptSuffix
//     (case-insensitive, whitespace-normalised) the composer does NOT
//     re-append. This is the "non-doppia-applicazione" guarantee.
//   - Regex-safe TrimSpace: leading/trailing whitespace stripped via
//     regexp `^\s+|\s+$` (covers \t, \n, \r, Unicode space etc.) instead
//     of strings.TrimSpace which only handles ASCII whitespace runes.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// dimensions are caller-supplied ONLY. The legacy "style.W/H
// fallback" path was retired when StyleDefinition lost
// DefaultWidth/DefaultHeight — callers (image generation request
// handlers) must pass Width/Height explicitly through the
// GenerateCommand. The composer just copies them through.
package images

import (
	"context"
	"regexp"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
)

// trimRegex matches any leading or trailing run of Unicode whitespace.
// Using \s (Unicode-aware) rather than strings.TrimSpace's ASCII subset
// is "regex-safe" per the action plan §3 bullet (b).
var trimRegex = regexp.MustCompile(`^\s+|\s+$`)

// ResolvedGenerationRequest is the canonical output the composer produces.
// ImageGenerator adapters consume this struct directly.
type ResolvedGenerationRequest struct {
	PromptOriginal string   `json:"prompt_original"`
	PromptFinal    string   `json:"prompt_final"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	StyleID        string   `json:"style_id"`
	StyleVersion   int      `json:"style_version,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	Width          int      `json:"width,omitempty"`
	Height         int      `json:"height,omitempty"`
	Tags           []string `json:"tags,omitempty"`
}

// GenerateCommand is the composer input: the user-supplied request BEFORE
// style resolution.
type GenerateCommand struct {
	Prompt   string
	Provider string
	Width    int
	Height   int
	Tags     []string
}

// PromptComposer is the port every gateway wraps.
type PromptComposer interface {
	Compose(ctx context.Context, cmd GenerateCommand, style generation.ResolvedStyle) (ResolvedGenerationRequest, error)
}

// Compile-time assertion: promptComposerImpl satisfies PromptComposer.
var _ PromptComposer = (*promptComposerImpl)(nil)

// promptComposerImpl is the canonical fail-closed implementation.
type promptComposerImpl struct{}

// NewPromptComposer returns the canonical PromptComposer.
func NewPromptComposer() PromptComposer {
	return &promptComposerImpl{}
}

// Compose applies the canonical composition rules.
func (*promptComposerImpl) Compose(ctx context.Context, cmd GenerateCommand, style generation.ResolvedStyle) (ResolvedGenerationRequest, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedGenerationRequest{}, err
	}

	original := trimRegex.ReplaceAllString(cmd.Prompt, "")
	suffix := trimRegex.ReplaceAllString(style.PromptSuffix, "")

	// Special rules (godlike/07 fail-closed):
	//   - Empty PromptOriginal + non-empty suffix → suffix becomes base.
	//   - Empty suffix → no decoration.
	if original == "" && suffix != "" {
		original = suffix
		suffix = ""
	}

	// IDEMPOTENT: skip suffix re-application if original already ends with suffix.
	if suffix != "" && endsWithSuffixCI(original, suffix) {
		suffix = ""
	}

	promptFinal := original
	if suffix != "" {
		promptFinal = original + ", " + suffix
	}

	return ResolvedGenerationRequest{
		PromptOriginal: cmd.Prompt,
		PromptFinal:    trimRegex.ReplaceAllString(promptFinal, ""),
		NegativePrompt: style.NegativePrompt,
		StyleID:        style.ID,
		StyleVersion:   style.Version,
		Provider:       cmd.Provider,
		// Step-1 typed migration (A1, July 2026): dimensions are
		// caller-supplied. The legacy `fallbackDim(cmd.W, style.W)`
		// chain was retired when StyleDefinition lost the
		// per-style DefaultWidth/DefaultHeight fields.
		Width:  cmd.Width,
		Height: cmd.Height,
		Tags:   cmd.Tags,
	}, nil
}

// endsWithSuffixCI returns true if `s` already ends with `suffix`
// (ASCII case-insensitive — stdlib-only to avoid importing golang.org/x/text).
func endsWithSuffixCI(s, suffix string) bool {
	if s == "" || suffix == "" {
		return false
	}
	ls := toLowerASCII(s)
	lsuf := toLowerASCII(suffix)
	if len(ls) < len(lsuf) {
		return false
	}
	return ls[len(ls)-len(lsuf):] == lsuf
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
