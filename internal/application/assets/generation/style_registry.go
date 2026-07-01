// Package generation — style registry + fail-closed resolution (Step 3, July 2026).
//
// The StyleRegistry loads GenerationStyle definitions from YAML and exposes
// both the legacy query methods (Get, List, ApplyStyle) and the new fail-closed
// StyleResolver interface.
//
// Key design decisions:
//   - Resolve(styleID, provider, model) is fail-closed: unknown style,
//     incompatible provider, or incompatible model → typed error.
//   - Empty styleID → no-op default (no error, empty ResolvedStyle).
//   - ApplyStyle is deprecated — callers should use Resolve + PromptComposer.
//   - StyleRegistry implements StyleResolver, so existing wiring passes
//     *StyleRegistry as the StyleResolver dependency.
package generation

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"gopkg.in/yaml.v3"
)

// ── Sentinel errors ──────────────────────────────────────────────────────

// ErrStyleNotFound is returned when the requested style ID does not exist.
var ErrStyleNotFound = errors.New("generation style not found")

// ErrStyleProviderUnsupported is returned when the style exists but the
// requested provider is not in AllowedProviders.
var ErrStyleProviderUnsupported = errors.New("generation style not supported by the requested provider")

// ErrStyleModelUnsupported is returned when the style exists but the
// requested model is not in AllowedModels.
var ErrStyleModelUnsupported = errors.New("generation style not supported by the requested model")

// ErrStyleDisabled is returned when the style exists but is explicitly
// disabled (Enabled == false) in configuration.
var ErrStyleDisabled = errors.New("generation style is disabled")

// ── ResolvedStyle ────────────────────────────────────────────────────────

// ResolvedStyle is the output of StyleResolver.Resolve. It carries the
// fully-resolved prompt suffix, negative prompt, dimensions, and
// destination key so the caller never needs a second lookup.
type ResolvedStyle struct {
	// ID is the canonical style identifier (e.g. "cinematic").
	ID string `json:"id"`

	// Version is the style version number. 0 = unversioned.
	Version int `json:"version"`

	// PromptSuffix is the text appended to the user prompt.
	PromptSuffix string `json:"prompt_suffix"`

	// NegativePrompt is the negative prompt for providers that support it.
	NegativePrompt string `json:"negative_prompt,omitempty"`

	// Width and Height are the resolved output dimensions.
	Width  int `json:"width"`
	Height int `json:"height"`

	// DestinationKey is the logical destination (e.g. "ai-images/cinematic").
	DestinationKey string `json:"destination_key"`
}

// ── StyleResolver interface ──────────────────────────────────────────────

// StyleResolver resolves a style ID into validated generation parameters.
//
// Contract:
//   - Empty styleID → (ResolvedStyle{}, nil) — no-op default.
//   - Valid style + compatible provider/model → (ResolvedStyle, nil).
//   - Unknown style → (ResolvedStyle{}, ErrStyleNotFound).
//   - Disabled style → (ResolvedStyle{}, ErrStyleDisabled).
//   - Incompatible provider → (ResolvedStyle{}, ErrStyleProviderUnsupported).
//   - Incompatible model → (ResolvedStyle{}, ErrStyleModelUnsupported).
type StyleResolver interface {
	Resolve(styleID, provider, model string) (ResolvedStyle, error)
}

// Compile-time assertion: *StyleRegistry implements StyleResolver.
var _ StyleResolver = (*StyleRegistry)(nil)

// ── StyleRegistry ────────────────────────────────────────────────────────

// StyleRegistry manages a collection of generation styles loaded from YAML.
type StyleRegistry struct {
	styles map[string]domain.GenerationStyle
	mu     sync.RWMutex
}

// NewStyleRegistry creates a new registry and loads styles from the given
// YAML file. Returns an error if the file cannot be read or unmarshalled.
func NewStyleRegistry(yamlPath string) (*StyleRegistry, error) {
	r := &StyleRegistry{
		styles: make(map[string]domain.GenerationStyle),
	}
	if err := r.Load(yamlPath); err != nil {
		return nil, err
	}
	return r, nil
}

// Load reads styles from a YAML file. Existing styles are replaced atomically.
func (r *StyleRegistry) Load(yamlPath string) error {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("failed to read styles file: %w", err)
	}

	var container domain.GenerationStyles
	if err := yaml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("failed to unmarshal styles: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.styles = make(map[string]domain.GenerationStyle, len(container.Styles))
	for _, s := range container.Styles {
		r.styles[strings.ToLower(s.Name)] = s
	}

	return nil
}

// ── Legacy query methods (backward-compatible) ───────────────────────────

// Get retrieves a style by name (case-insensitive).
func (r *StyleRegistry) Get(name string) (domain.GenerationStyle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.styles[strings.ToLower(name)]
	return s, ok
}

// List returns all available styles (including disabled ones — callers
// should filter via IsEnabled() if they only want active styles).
func (r *StyleRegistry) List() []domain.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		res = append(res, s)
	}
	return res
}

// ListEnabled returns only styles where IsEnabled() is true.
func (r *StyleRegistry) ListEnabled() []domain.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		if s.IsEnabled() {
			res = append(res, s)
		}
	}
	return res
}

// ApplyStyle appends the style suffix to the prompt. Deprecated: prefer
// Resolve + PromptComposer for fail-closed resolution. Kept for backward
// compatibility. Empty or unknown styleName returns the prompt unchanged
// (silent fallback — the old behaviour).
//
// Deprecated: use StyleResolver.Resolve instead.
func (r *StyleRegistry) ApplyStyle(prompt, styleName string) string {
	if styleName == "" {
		return prompt
	}

	style, ok := r.Get(styleName)
	if !ok {
		return prompt
	}

	effectiveSuffix := style.EffectiveSuffix()
	if effectiveSuffix == "" {
		return prompt
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return effectiveSuffix
	}

	return prompt + ", " + effectiveSuffix
}

// ── Fail-closed resolution (canonical) ───────────────────────────────────

// Resolve implements StyleResolver. It validates styleID, provider, and
// model against the registry and returns a ResolvedStyle on success.
//
// Empty styleID is permitted and returns a zero ResolvedStyle with no
// error — the caller can treat this as "no style requested".
func (r *StyleRegistry) Resolve(styleID, provider, model string) (ResolvedStyle, error) {
	// Empty styleID = no style requested → no-op default.
	if styleID == "" {
		return ResolvedStyle{}, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	style, ok := r.styles[strings.ToLower(styleID)]
	if !ok {
		return ResolvedStyle{}, fmt.Errorf("%w: %q", ErrStyleNotFound, styleID)
	}

	if !style.IsEnabled() {
		return ResolvedStyle{}, fmt.Errorf("%w: %q", ErrStyleDisabled, styleID)
	}

	// Provider compatibility check.
	if len(style.AllowedProviders) > 0 && provider != "" {
		if !stringInSlice(provider, style.AllowedProviders) {
			return ResolvedStyle{}, fmt.Errorf(
				"%w: style %q allows providers %v, got %q",
				ErrStyleProviderUnsupported, styleID, style.AllowedProviders, provider,
			)
		}
	}

	// Model compatibility check.
	if len(style.AllowedModels) > 0 && model != "" {
		if !stringInSlice(model, style.AllowedModels) {
			return ResolvedStyle{}, fmt.Errorf(
				"%w: style %q allows models %v, got %q",
				ErrStyleModelUnsupported, styleID, style.AllowedModels, model,
			)
		}
	}

	width := style.DefaultWidth
	height := style.DefaultHeight

	destKey := style.DestinationKey
	if destKey == "" {
		destKey = "ai-images/" + style.Name
	}

	return ResolvedStyle{
		ID:             style.Name,
		Version:        style.Version,
		PromptSuffix:   style.EffectiveSuffix(),
		NegativePrompt: style.NegativePrompt,
		Width:          width,
		Height:         height,
		DestinationKey: destKey,
	}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

// stringInSlice reports whether needle is in haystack (case-insensitive).
func stringInSlice(needle string, haystack []string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}
