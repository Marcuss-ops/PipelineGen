// Package styles — registry.go: canonical YAML-backed StyleRegistry
// (FASE 8 image-territories migration, July 2026).
//
// The real StyleRegistry implementation was moved here from
// internal/application/assets/generation/style_registry.go to break
// the styles → generation import cycle. generation/ now imports this
// package and uses Go type aliases for back-compat.
//
// StyleRegistry loads GenerationStyle definitions from YAML and exposes
// both the legacy query methods (Get, List, ListEnabled, ApplyStyle) and
// the fail-closed StyleResolver interface (Resolve + Validate).
//
// Key design decisions:
//   - Resolve(styleID, provider, model) is fail-closed: unknown style
//     or disabled style → typed error.
//   - Empty styleID → no-op default (zero ResolvedStyle, no error).
//   - ApplyStyle is deprecated — callers should use Resolve + PromptComposer.
//   - StyleRegistry implements StyleResolver, so existing wiring passes
//     *StyleRegistry as the StyleResolver dependency.
//   - Uses ONLY local types from this package (types.go, resolver.go) —
//     no import of generation/ to keep the dependency graph cycle-free.
//
// Surface-3 (July 2026): the per-style AllowedProviders / AllowedModels
// compatibility checks that historically fired ErrStyleProviderUnsupported
// / ErrStyleModelUnsupported were retired from Resolve. The
// GenerationStyle fields stay loaded from yaml for back-compat; the
// helper stringInSlice was removed (was only called from the
// retired checks). See resolver.go's doc-comment for the canonical
// rationale + audit-pinning chain.
package styles

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"gopkg.in/yaml.v3"
)

// ── Compile-time assertions ────────────────────────────────────────────

// StyleRegistry satisfies StyleResolver (defined in resolver.go).
var _ StyleResolver = (*StyleRegistry)(nil)

// ── RegistryReadOnly sentinel ───────────────────────────────────────────

// ErrRegistryReadOnly is returned by Register when caller code
// attempts to add a StyleDefinition at runtime. The canonical
// registry is YAML-backed today; runtime mutations require
// re-via NewStyleRegistry.
var ErrRegistryReadOnly = errors.New("styles.StyleRegistry: runtime Register not supported (YAML bootstrap only)")

// ── StyleRegistry ──────────────────────────────────────────────────────

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

// ── Legacy query methods (backward-compatible) ─────────────────────────

// Get retrieves a style by name (case-insensitive).
func (r *StyleRegistry) Get(name string) (domain.GenerationStyle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.styles[strings.ToLower(name)]
	return s, ok
}

// Lookup is the application-layer alias for Get. It returns the
// StyleDefinition for the given StyleID, or (zero, false) if not found.
// Uses case-insensitive name lookup identical to Get.
func (r *StyleRegistry) Lookup(name string) (StyleDefinition, bool) {
	return r.Get(name)
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

// Register adds a new StyleDefinition at runtime. Today this
// always returns ErrRegistryReadOnly because canonical state is
// YAML-backed; composition roots that need to extend the recipe
// set should write to the style registry YAML and reload.
func (r *StyleRegistry) Register(_ StyleDefinition) error {
	return ErrRegistryReadOnly
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

// ── Fail-closed resolution (canonical) ──────────────────────────────────

// Resolve implements StyleResolver. It validates styleID against the
// registry and returns a ResolvedStyle on success.
//
// Empty styleID is permitted and returns a zero ResolvedStyle with no
// error — the caller can treat this as "no style requested".
//
// surface-3 (July 2026): the per-style AllowedProviders / AllowedModels
// compatibility checks (formerly raising ErrStyleProviderUnsupported /
// ErrStyleModelUnsupported via the stringInSlice helper) were retired
// once google-slides became the sole image-generation provider. See
// resolver.go for the canonical rationale + godlike/06 audit-pinning
// chain — the GenerationStyle fields stay loaded from yaml for
// back-compat but are no longer enforced here.
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

	// surface-3 (July 2026): per-style allowlist checks retired. See
	// resolver.go's doc-comment for the canonical rationale. The
	// helper stringInSlice was removed; the re-exported sentinels
	// stay defined for godlike/06 audit-pinning (the canonical
	// non-nil contract is locked in by
	// resolver_test.go::TestStyleResolver_AllSentinelErrorsNonNil).
	_ = ErrStyleProviderUnsupported
	_ = ErrStyleModelUnsupported

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
		Enabled:        true,
	}, nil
}

// Validate implements StyleResolver. It is the void variant of Resolve.
func (r *StyleRegistry) Validate(styleID, provider, model string) error {
	_, err := r.Resolve(styleID, provider, model)
	return err
}

// ── Inner (back-compat) ─────────────────────────────────────────────────

// Inner returns self. It preserves the back-compat surface for callers
// that used the thin-wrapper's Inner() method to reach the underlying
// *generation.StyleRegistry. After FASE 8 migration, the returned type
// is *StyleRegistry (the canonical implementation).
func (r *StyleRegistry) Inner() *StyleRegistry {
	return r
}
