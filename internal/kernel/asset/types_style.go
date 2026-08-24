package asset

import (
	"errors"
	"fmt"
	"strings"
)

// ── Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026) ──
//
// This file is the canonical home for the slim 8-field generation style
// definition. The legacy 14-field GenerationStyle struct (with
// Description / Tags / DefaultWidth / DefaultHeight / AllowedProviders /
// AllowedModels + Enabled *bool tri-state pointer) was retired under
// godlike/06 one-owner-per-fact: the value-object the resolver, the
// YAML loader, and the admin endpoint consume lives here (and only
// here). Application-layer packages expose a Go type alias for
// portability (image/styles.StyleDefinition = asset.GenerationStyle);
// the chain collapses to a single type identity at compile time.
//
// Back-compat horizon: `type GenerationStyle = StyleDefinition`
// keeps existing call sites compiling unchanged during the 1-wave
// migration. Future callers MUST consume StyleDefinition directly via
// the canonical owner; the alias will be retired by a wave-tracker
// closure entry (architecture/current.yaml#PR-IMAGES-AI-VS-NORMAL-PLAN
// BACKFILL phase).
//
// godlike/07 "no fake availability":
//   - DisplayName is required (operators read this label).
//   - PromptSuffix is required (resolver composes into the prompt).
//   - ID/Name are required (key in the registry map).
//   - Enabled default is "false on YAML absence" (existing config
//     pins `enabled: true` on every entry; the silent flip is
//     documented and fail-closed).

// StyleID is the canonical typed identifier for an AI generation
// style. Mirrors the YAML `name:` field; the registry's Load step
// post-processes ID = StyleID(s.Name) so the two stay in sync.
//
// Per AGENTS.md Pattern 0 + godlike/06 one-owner-per-fact: StyleID
// is canonical. Application code MUST consume the typed shape so a
// future rename (or invalid-name gating via Valid) lands as a compile
// error, not a runtime panic.
type StyleID string

// StyleVersion is an integer counter bumped when a style's prompt
// suffix or negative prompt changes. 0 means unversioned (legacy).
//
// godlike/07: StyleVersion is informational only today. The resolver
// does NOT refuse a request when a style's version is older than a
// caller-supplied expectation — operators must coordinate the bump
// manually by editing the YAML. Future wave-tracker entry will
// surface "version mismatch" as an opt-in gate.
type StyleVersion int

// StyleDefinition is the canonical definition of an AI generation
// style. Holds only the typed fields the resolver needs to compose
// a prompt and redirect the artifact to the configured Drive
// destination.
//
// godlike/06 one-owner-per-fact: this is the SOLE canonical shape.
// ImageAsset/StyleSnapshot/ResolvedStyle are projections, not
// duplicates, of this type.
type StyleDefinition struct {
	// ID is the typed registry key. Set post-unmarshal by the
	// loader (registry.Load) from the Name field so yaml:v3's
	// `yaml:"-"` keeps the wire-format single-sourced on `name:`.
	ID StyleID `yaml:"-" json:"id"`

	// Version is incremented when the prompt or negative prompt
	// changes. 0 = unversioned legacy entry.
	Version StyleVersion `yaml:"version,omitempty" json:"version,omitempty"`

	// Name is the canonical string id (yaml key + lookup handle).
	// Mirrors ID at the wire-format level; kept distinct from ID
	// so application code can use the typed shape without
	// constraining the yaml schema.
	Name string `yaml:"name" json:"name"`

	// DisplayName is the human-readable label surfaced to operators.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`

	// PromptSuffix is appended to the user prompt after a comma
	// separator. Required: Valid fails closed on empty PromptSuffix.
	PromptSuffix string `yaml:"prompt_suffix,omitempty" json:"prompt_suffix,omitempty"`

	// NegativePrompt is injected as the negative prompt for
	// providers that support it (Flux, NVIDIA, etc.).
	NegativePrompt string `yaml:"negative_prompt,omitempty" json:"negative_prompt,omitempty"`

	// DestinationKey is the logical destination identifier (e.g.
	// "ai-images/medieval"). Resolved to a Drive folder ID by
	// DestinationResolver at generation time; the resolver falls
	// back to "ai-images/<Name>" when this field is empty.
	DestinationKey string `yaml:"destination_key,omitempty" json:"destination_key,omitempty"`

	// Enabled controls whether the resolver admits the style.
	// true = active; false = present in config but disabled.
	// YAML absence defaults to false (silent flip from the legacy
	// tri-state *bool "absent = enabled" semantics; existing
	// config pins `enabled: true` on every entry, so the flip is
	// transparent in production).
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// ── Validation (godlike/07 fail-closed contract) ──────────────────────

// ErrStyleMissingID is returned by Valid when ID is empty after
// unmarshal (e.g. the loader post-process step failed or the entry
// was constructed in-memory without a Name).
var ErrStyleMissingID = errors.New("StyleDefinition.ID is empty")

// ErrStyleMissingDisplayName is returned by Valid when DisplayName
// is unset. DisplayName is required for any modern (post-Step-1)
// style — operators read this label in the admin endpoint output
// and use it as a human-readable key in error logs.
var ErrStyleMissingDisplayName = errors.New("StyleDefinition.DisplayName is empty")

// ErrStyleMissingPromptSuffix is returned by Valid when PromptSuffix
// is empty. The resolver composes PromptSuffix into the user
// prompt; an empty value would silently produce a no-op suffix
// (fail-open). Valid fails closed on it.
var ErrStyleMissingPromptSuffix = errors.New("StyleDefinition.PromptSuffix is empty")

// Valid returns nil if the style is well-formed for use by the
// resolver. Fail-closed rules:
//
//   - ID == ""                 -> ErrStyleMissingID
//   - DisplayName == ""        -> ErrStyleMissingDisplayName
//   - PromptSuffix == ""       -> ErrStyleMissingPromptSuffix
//
// Valid does NOT touch the Enabled flag — disabled styles are still
// well-formed (operators keep them on disk for archival / A-B
// testing). The Enabled check happens at StyleResolver.Resolve-time.
func (s StyleDefinition) Valid() error {
	if strings.TrimSpace(string(s.ID)) == "" {
		return fmt.Errorf("%w", ErrStyleMissingID)
	}
	if strings.TrimSpace(s.DisplayName) == "" {
		return fmt.Errorf("%w (style %q)", ErrStyleMissingDisplayName, s.ID)
	}
	if strings.TrimSpace(s.PromptSuffix) == "" {
		return fmt.Errorf("%w (style %q)", ErrStyleMissingPromptSuffix, s.ID)
	}
	return nil
}

// ── Back-compat alias (1-wave horizon) ────────────────────────────────

// GenerationStyle is the historical identifier for the canonical
// generation definition.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, action A1,
// July 2026): the legacy 14-field struct was retired. This is now a
// Go type alias to StyleDefinition. The alias preserves the existing
// call sites in
// internal/capabilities/images/workflow/styles/{registry,resolver}.go and
// the application-layer re-export chain
// (image/styles.StyleDefinition = asset.GenerationStyle =
// asset.StyleDefinition) compiling unchanged while the underlying
// shape moves to the slim 8-field typed definition.
//
// Migration note: the legacy GenerationStyle.{Description, Tags,
// DefaultWidth, DefaultHeight, AllowedProviders, AllowedModels}
// fields are gone. Callers that consumed them MUST be migrated to
// either (a) caller-supplied parameters (ResolverShape Width/Height
// routes through the generation request) or (b) a future replacement
// port. ResolvedStyle.Width/Height was dropped in lockstep — see
// internal/capabilities/images/workflow/styles/types.go for the surface-1
// cut rationale.
//
// Future wave-tracker entry: the alias will be physically removed in
// the BACKFILL → CUTOVER → CONTRACT closure sequence
// (architecture/current.yaml#PR-IMAGES-AI-VS-NORMAL-PLAN).
type GenerationStyle = StyleDefinition

// ── Container (the multi-entry yaml shape) ────────────────────────────

// GenerationStyles is the yaml on-disk container for multiple
// styles. The Styles slice aliases the legacy []GenerationStyle via
// the type-alias chain so the YAML loader still works byte-stable.
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}
