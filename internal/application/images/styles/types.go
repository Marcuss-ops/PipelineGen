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
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// ResolvedStyle lost its Width/Height fields because the canonical
// StyleDefinition in domain/asset/types_style.go no longer carries
// DefaultWidth/DefaultHeight — dimensions are caller-supplied through
// the image generation request. The 3-level alias chain
// (image/styles.StyleDefinition = asset.GenerationStyle =
// asset.StyleDefinition) collapses to a single identity at compile time.
package styles

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ResolvedStyle is the post-validation read-side projection of a
// generation style. The resolver returns this from Resolve when every
// fail-closed gate passes; on failure it returns
// (ResolvedStyle{}, Err<one_of_four>).
//
// Step-1 typed migration (A1, July 2026): Width/Height were dropped.
// The canonical resolver no longer carries per-style dimensions —
// callers (image generation request handlers) supply dimensions
// explicitly through their `Width`/`Height` request fields. Storing
// per-style defaults was a godlike/06 SSOT violation: callers should
// pass dimensions explicitly, not infer them from the style.
type ResolvedStyle struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
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
// StyleDefinition lookups. Per AGENTS.md Pattern 0, this is a typed
// alias over the canonical StyleID from domain/asset so callers and
// linters can refer to a single named shape even though Style-style
// ids are internally map keys in styles.StyleRegistry.
//
// Step-1 typed migration (A1, July 2026): StyleID now aliases
// asset.StyleID (the new typed shape defined in types_style.go).
// Previously it was `type StyleID = string`; the new typed shape
// makes "unknown style id" a compile error in callers that consume
// the typed surface.
type StyleID = asset.StyleID

// StyleDefinition is the application-layer re-export of the canonical
// generation definition. It aliases asset.GenerationStyle (which
// itself aliases asset.StyleDefinition; the chain is transparent for
// purposes of type identity and method-set lookup) so application code
// under package styles can refer to it via the styles namespace without
// importing internal/domain/asset directly.
//
// Per AGENTS.md Pattern 0 + the wrapper role documented in registry.go,
// StyleDefinition is governance-locked at this alias; future changes
// to the canonical definition must ship via domain/asset (forward-
// pointer: architecture/ownership.generated.yaml).
//
// Step-1 typed migration (A1, July 2026): the underlying shape is
// now the slim 8-field StyleDefinition (no Description / Tags /
// DefaultWidth / DefaultHeight / AllowedProviders / AllowedModels;
// Enabled is plain bool, silent-flip absent→false). Callers that
// consumed the retired fields MUST migrate.
type StyleDefinition = asset.GenerationStyle
