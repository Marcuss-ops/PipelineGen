// Package styles — ResolvedStyle + sentinel errors (image-territories
// action plan, July 2026, FASE 2A-2C).
//
// This package introduces the canonical "resolved" projection of an AI
// generation style plus the four fail-closed errors StyleResolver emits.
// Styles(Resolver) and its impl live in resolver.go. The backing store
// is delegated to the SourceBackend interface so any concrete loader
// (YAML, in-memory, future DB) can plug in without rewriting the
// resolver.
//
// Step 6 wrap-up audit (July 2026): the package also hosts the
// thin wrapper types StyleID + StyleDefinition so the application-
// layer Registry (registry.go) can expose a stable surface without
// forcing callers to import the deeper generation package.
package styles

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ResolvedStyle is the post-validation read-side projection of a
// generation style. The resolver returns this from Resolve when every
// fail-closed gate passes; on failure it returns
// (ResolvedStyle{}, Err<one_of_four>).
type ResolvedStyle struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
	Width, Height  int
	DestinationKey string
	Enabled        bool
}

// Sentinel errors for fail-closed resolution. Consumers dispatch via
// errors.Is(err, Err<...>).
var (
	ErrStyleNotFound            = errors.New("styles: style not found")
	ErrStyleProviderUnsupported = errors.New("styles: provider not allowed for this style")
	ErrStyleModelUnsupported    = errors.New("styles: model not allowed for this style")
	ErrStyleDisabled            = errors.New("styles: style is disabled")
)

// StyleID is the opaque identifier type accepted by Registry.Lookup and
// GenerationStyle lookups. Per AGENTS.md Pattern 0, this is a typed
// alias over the canonical string id so callers and linters can refer
// to a single named shape even though Generation-style ids are
// internally map keys in generation.StyleRegistry.
type StyleID = string

// StyleDefinition is the application-layer re-export of the canonical
// generation definition. It aliases asset.GenerationStyle (the type
// generation.StyleRegistry.Get / .List produce) so application code
// under package styles can refer to it via the styles namespace without
// importing internal/application/assets/generation directly.
//
// Per AGENTS.md Pattern 0 + the wrapper role documented in registry.go,
// StyleDefinition is governance-locked at this alias; future changes to
// the canonical definition must ship via domain/asset (Forward-
// pointer: architecture/ownership.generated.yaml).
type StyleDefinition = asset.GenerationStyle
