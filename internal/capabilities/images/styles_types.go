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
//
// Step-2 typed migration (A2, July 2026): the canonical sentinel
// owner for ApplyStyle is pkg/styleerrors. This file re-exports the
// 4 sentinels (ErrUnknownStyle / ErrStyleDisabled / ErrEmptyPrompt /
// ErrStyleVersionMismatch) as Go value-aliases so application-layer
// callers that import image/styles dispatch via errors.Is
// transparently. ErrStyleDisabled in particular is now the
// pkg/styleerrors canonical value (not a re-declaration), so
// resolver.go's existing emission point is byte-identical to the
// new canonical pkg/styleerrors.ErrStyleDisabled — the alias chain
// preserves single-source-of-truth without a re-export wrapper.
package images

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/styleerrors"
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
//
// Legacy sentinels (resolver.go emission source — unchanged A2):
//   - ErrStyleNotFound            : styleID absent in source/magic "default" absent
//   - ErrStyleProviderUnsupported : surface-3 retired (audit-pin only)
//   - ErrStyleModelUnsupported    : surface-3 retired (audit-pin only)
//
// A2 re-exports (canonical in pkg/styleerrors, value-aliased here):
//   - ErrUnknownStyle, ErrStyleDisabled, ErrEmptyPrompt, ErrStyleVersionMismatch
//
// godlike/06 audit-pinning: ErrStyleProviderUnsupported / ErrStyleModelUnsupported
// stay defined even though the underlying checks were retired in
// surface-3 (per the godlike/06 contract: re-exports preserve
// consumer-facing sentinel stability across code-shape revisions).
var (
	ErrStyleNotFound            = errors.New("styles: style not found")
	ErrStyleProviderUnsupported = errors.New("styles: provider not allowed for this style")
	ErrStyleModelUnsupported    = errors.New("styles: model not allowed for this style")
)

// ── A2 back-compat sentinel aliases ────────────────────────────────────
//
// godlike/06 SSOT: pkg/styleerrors is the canonical authority for
// the 4 A2 sentinels. This file re-exports them as Go value-aliases
// (NOT re-declarations) so:
//
//   - resolver.go continues to use image/styles.ErrStyleDisabled and
//     stays dispatch-identical to code that uses pkg/styleerrors.ErrStyleDisabled
//     (same underlying error value; errors.Is succeeds across both import paths)
//   - registry.go's ApplyStyle implementation emits through pkg/styleerrors
//     directly, the canonical import path
//   - Pre-A2 call sites that imported image/styles for ErrStyleDisabled
//     (resolver-go-side callers) compile unchanged post-A2 (no churn)
//
// A future wave-tracker entry (CONTRACT phase, after
// EXPAND→BACKFILL→CUTOVER in PR-IMAGES-AI-VS-NORMAL-PLAN) will
// physically retire the image/styles re-export surface and migrate
// all callers to import pkg/styleerrors directly.
var (
	// ErrUnknownStyle — canonical in pkg/styleerrors.
	// ApplyStyle emits when styleName is empty OR absent from registry.
	ErrUnknownStyle = styleerrors.ErrUnknownStyle

	// ErrStyleDisabled — canonical in pkg/styleerrors.
	// resolver.go's existing emission point is byte-identical to this
	// value pre-A2 and post-A2 (value-alias, not re-declaration).
	ErrStyleDisabled = styleerrors.ErrStyleDisabled

	// ErrEmptyPrompt — canonical in pkg/styleerrors.
	// ApplyStyle emits when prompt is empty AND style's PromptSuffix is empty.
	ErrEmptyPrompt = styleerrors.ErrEmptyPrompt

	// ErrStyleVersionMismatch — canonical in pkg/styleerrors.
	// ApplyStyle emits when caller-pinned version > 0 AND style.Version differs.
	ErrStyleVersionMismatch = styleerrors.ErrStyleVersionMismatch
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

// StyleComposedPrompt is the post-ApplyStyle success envelope. The
// canonical consumer-facing field is ComposedText (the rendered
// prompt ready to feed into the model input); the metadata fields
// are inspection handles callers that don't care about them can
// ignore.
//
// Step-2 typed migration (A2, July 2026): this shape replaces the
// pre-A2 `(prompt, styleName string) string` return value, giving
// callers a typed envelope to consume instead of a bare string.
//
// Composition rule (canonical):
//
//	If both prompt (TrimSpace-stripped) and PromptSuffix (TrimSpace-stripped)
//	are non-empty: <prompt> + ", " + <PromptSuffix>
//	If only PromptSuffix is non-empty:                <PromptSuffix>
//	If only prompt is non-empty:                      <prompt>  (no suffix to apply)
//
// godlike/06 SSOT: this struct is co-located with ApplyStyle
// (registry.go) so the typed envelope lives next to its only producer.
// Application consumers import it as `stylepkg.StyleComposedPrompt`
// via the styles package prefix.
type StyleComposedPrompt struct {
	// ComposedText is the canonical rendered prompt. Read this
	// field when piping the result into the model input.
	ComposedText string

	// StyleID is the canonical registry key of the resolved style.
	// Mirrors the domain/asset.StyleID typed alias for typed
	// consumers; the string-shape is for inspection convenience
	// (test debug + operator log lines).
	StyleID string

	// StyleVersion is the loaded StyleVersion snapshot at compose
	// time. Callers that pin a version explicitly via ApplyStyle's
	// `version` arg compare this against the pin to detect drift
	// (the drift itself fails closed via ErrStyleVersionMismatch).
	StyleVersion int

	// PromptSuffix is the resolved suffix (raw, not yet joined in
	// ComposedText). Exposed for callers that want to render the
	// prompt differently than the canonical comma-join (e.g. a
	// negative-prompt-aware router).
	PromptSuffix string

	// NegativePrompt is the resolved negative prompt, when set.
	// Callers that talk to a provider supporting negative prompts
	// (Flux, NVIDIA, etc.) inject this independently of the
	// composed positive prompt.
	NegativePrompt string

	// DestinationKey is the canonical folder identifier for the
	// rendered image (e.g. "ai-images/medieval"). Resolution to a
	// Drive folder ID is the caller's responsibility via the
	// DestinationResolver port; this field carries the
	// style-declared key, not the resolved Drive ID.
	DestinationKey string
}
