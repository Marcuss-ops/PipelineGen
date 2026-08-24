// Package styles — registry.go: canonical YAML-backed StyleRegistry
// (FASE 8 image-territories migration, July 2026).
//
// The real StyleRegistry implementation was moved here from
// internal/application/assets/generation/style_registry.go to break
// the styles → generation import cycle. generation/ now imports this
// package and uses Go type aliases for back-compat.
//
// StyleRegistry loads style definitions from YAML and exposes both
// the legacy query methods (Get, List, ListEnabled) and the
// fail-closed StyleResolver interface (Resolve + Validate).
//
// Key design decisions:
//   - Resolve(styleID, provider, model) is fail-closed: unknown style
//     or disabled style → typed error.
//   - Empty styleID → no-op default (zero ResolvedStyle, no error).
//   - ApplyStyle is the canonical compose surface — fail-closed
//     under A2 (returns (*StyleComposedPrompt, error), see below).
//   - StyleRegistry implements StyleResolver, so existing wiring passes
//     *StyleRegistry as the StyleResolver dependency.
//   - Uses ONLY local types from this package (types.go, resolver.go) —
//     no import of generation/ to keep the dependency graph cycle-free.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
//   - StyleDefinition is now the slim 8-field shape (no Description /
//     Tags / DefaultWidth / DefaultHeight / AllowedProviders /
//     AllowedModels). The registry's Load post-processes
//     ID = StyleID(s.Name) so the typed id stays in sync with the
//     yaml "name" key.
//   - ResolvedStyle.Width/Height were dropped (caller-supplied).
//   - The per-style allowlist checks were already retired in
//     surface-3 (July 2026); this cut inherits the cleanup.
//
// Step-2 typed migration (A2, July 2026):
//   - ApplyStyle signature changed from
//     `(prompt, styleName string) string`  to
//     `(prompt, styleName string, version int) (*StyleComposedPrompt, error)`.
package images

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/styleerrors"
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
//
// Step-1 typed migration (A1, July 2026): the inner map is keyed by
// the lowercased Name string (not the typed ID) because the ID is
// post-processed after unmarshal. Lookup-then-name path matches the
// legacy behaviour; Lookup via StyleID is supported because ID is
// a typed alias of string and Go's type system treats them
// transparently.
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
//
// Step-1 typed migration (A1, July 2026): post-processes
// ID = StyleID(s.Name) so the typed id stays in sync with the yaml
// "name" key. Load is the canonical surface for that normalisation;
// callers that build StyleRegistry directly via composite literals
// must perform the same normalisation themselves.
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
	for i := range container.Styles {
		// ID is yaml:"-" so it didn't get populated by unmarshal —
		// sync it with Name post-load so the typed shape stays
		// consistent across the registry + resolver surfaces.
		container.Styles[i].ID = domain.StyleID(container.Styles[i].Name)
		r.styles[strings.ToLower(container.Styles[i].Name)] = container.Styles[i]
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
// should filter via Enabled if they only want active styles).
//
// Step-1 typed migration (A1, July 2026): the legacy
// `s.IsEnabled()` method was retired along with the *bool tri-state
// pointer. The remaining `bool` field reads directly via style.Enabled.
func (r *StyleRegistry) List() []domain.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		res = append(res, s)
	}
	return res
}

// ListEnabled returns only styles where Enabled is true.
func (r *StyleRegistry) ListEnabled() []domain.GenerationStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.GenerationStyle, 0, len(r.styles))
	for _, s := range r.styles {
		if s.Enabled {
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

// ── ApplyStyle — fail-closed contract (A2, July 2026) ──────────────────

// ApplyStyle composes a prompt using the resolved style's
// prompt suffix + negative prompt. Returns
// (*StyleComposedPrompt, error) where every input failure mode
// surfaces a typed pkg/styleerrors sentinel (godlike/07
// fail-closed contract; A2 closure of pre-A2 silent fallback).
//
// version is the caller's pin: 0 means "wildcard, accept whatever
// the registry loaded"; > 0 means "exact-match required". A
// non-zero pin that doesn't match the loaded StyleVersion emits
// ErrStyleVersionMismatch.
//
// Per the godlike/06 SSOT owner-pkg/styleerrors contract, every
// emitted sentinel is dispatched via errors.Is — callers
// pattern-match on the typed value, not the message text.
//
// Sentinel triggers (canonical):
//   - ErrUnknownStyle        empty styleName OR name absent in registry
//   - ErrStyleDisabled       style found but Enabled=false
//   - ErrEmptyPrompt         prompt empty AND PromptSuffix empty (no rendered text)
//   - ErrStyleVersionMismatch version > 0 AND style.Version != version
//
// Pre-A2 silent fallback (now CLOSED): every failure mode returned
// the prompt unchanged; an unknown style + a non-empty prompt was
// the canonical silent-fall-through anti-pattern. Post-A2: every
// failure mode is a typed error so the caller learns that the
// prompt was rendered as offered (no style applied) vs. rejected
// (style requested but unfulfilled).
//
// Step-1 typed migration (A1, July 2026): PromptSuffix is the sole
// suffix source (the legacy Description fallback was retired). The
// new A2 contract surfacing ErrEmptyPrompt when BOTH prompt and
// PromptSuffix are empty ensures the caller knows the render is
// empty rather than emitting an empty string downstream.
func (r *StyleRegistry) ApplyStyle(prompt, styleName string, version int) (*StyleComposedPrompt, error) {
	// Step-2 typed migration (A2, July 2026): the canonical
	// fail-closed gates, in deterministic order:
	//
	//   1. nil-receiver                     -> ErrUnknownStyle (registry not initialised — silent fail-open pre-A2)
	//   2. empty styleName                  -> ErrUnknownStyle
	//   3. style absent from registry       -> ErrUnknownStyle
	//   4. style found but Enabled=false    -> ErrStyleDisabled
	//   5. prompt empty AND suffix empty    -> ErrEmptyPrompt
	//   6. version > 0 AND style.Version != -> ErrStyleVersionMismatch
	//   7. success                          -> *StyleComposedPrompt

	if r == nil {
		return nil, fmt.Errorf("%w: registry receiver is nil", styleerrors.ErrUnknownStyle)
	}

	name := strings.TrimSpace(styleName)
	if name == "" {
		return nil, fmt.Errorf("%w: styleName is empty", styleerrors.ErrUnknownStyle)
	}

	style, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrUnknownStyle, name)
	}

	if !style.Enabled {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrStyleDisabled, name)
	}

	// Step-2 typed migration (A2, July 2026): the canonical
	// fail-closed ErrEmptyPrompt gate. Both the user-supplied
	// prompt and the style's PromptSuffix must be non-empty
	// (TrimSpace-stripped) for a composed text to be produced;
	// otherwise surface a typed error so the caller knows the
	// render is empty rather than emitting "" downstream.
	trimmedPrompt := strings.TrimSpace(prompt)
	trimmedSuffix := strings.TrimSpace(style.PromptSuffix)
	if trimmedPrompt == "" && trimmedSuffix == "" {
		return nil, fmt.Errorf("%w: %q", styleerrors.ErrEmptyPrompt, name)
	}

	if version > 0 && int(style.Version) != version {
		return nil, fmt.Errorf("%w: %q loaded=%d want=%d",
			styleerrors.ErrStyleVersionMismatch, name, int(style.Version), version)
	}

	destKey := strings.TrimSpace(style.DestinationKey)
	if destKey == "" {
		destKey = "ai-images/" + style.Name
	}

	composed := trimmedPrompt
	if trimmedSuffix != "" {
		if composed == "" {
			composed = trimmedSuffix
		} else {
			composed = composed + ", " + trimmedSuffix
		}
	}

	return &StyleComposedPrompt{
		ComposedText:   composed,
		StyleID:        style.Name,
		StyleVersion:   int(style.Version),
		PromptSuffix:   style.PromptSuffix,
		NegativePrompt: style.NegativePrompt,
		DestinationKey: destKey,
	}, nil
}

// ── Fail-closed resolution (canonical) ──────────────────────────────────

// Resolve implements StyleResolver. It validates styleID against the
// registry and returns a ResolvedStyle on success.
//
// Empty styleID is permitted and returns a zero ResolvedStyle with no
// error — the caller can treat this as "no style requested".
//
// Step-1 typed migration (A1, July 2026): the per-style allowlist
// checks (width/height, providers, models) were retired in
// surface-3. The Resolve body now reads PromptSuffix + Enabled +
// DestinationKey directly off the slim 8-field StyleDefinition; the
// helper stringInSlice was removed (was only called from the
// retired checks). ResolvedStyle lost Width/Height in lockstep —
// see types.go for the surface-1 cut rationale.
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

	if !style.Enabled {
		// godlike/06: image/styles.ErrStyleDisabled is byte-identical
		// to pkg/styleerrors.ErrStyleDisabled via the value-alias in
		// types.go; the wrap chain dispatches transparently across
		// both import paths.
		return ResolvedStyle{}, fmt.Errorf("%w: %q", ErrStyleDisabled, styleID)
	}

	// Step-1 typed migration (A1, July 2026): per-style allowlist
	// checks retired (surface-3 cut). The re-exported sentinels
	// stay defined for godlike/06 audit-pinning (the canonical
	// non-nil contract is locked in by
	// resolver_test.go::TestStyleResolver_AllSentinelErrorsNonNil).
	_ = ErrStyleProviderUnsupported
	_ = ErrStyleModelUnsupported

	destKey := style.DestinationKey
	if destKey == "" {
		destKey = "ai-images/" + style.Name
	}

	return ResolvedStyle{
		ID:             style.Name,
		Version:        int(style.Version),
		PromptSuffix:   style.PromptSuffix,
		NegativePrompt: style.NegativePrompt,
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
// *StyleRegistry. After FASE 8 migration, the returned type
// is *StyleRegistry (the canonical implementation).
func (r *StyleRegistry) Inner() *StyleRegistry {
	return r
}
