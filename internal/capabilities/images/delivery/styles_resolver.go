// Package styles — StyleResolver interface + canonical impl.
// FASE 2A-2C: introduces a fail-closed port that downstream call sites
// (StyleResolver+caller, FASE 3 PromptComposer) can rely on without
// worrying about implicit defaults.
//
// StyleResolver.Resolve returns (ResolvedStyle{zero-value}, Err<...>)
// on any gate failure so callers MUST pattern-match via errors.Is.
//
// The canonical impl is *styleResolverImpl. A nil SourceBackend is
// rejected at construction time (godlike/07 "no fake availability"):
// New(nil) returns a failingResolver that emits ErrStyleNotFound for
// every Resolve/Validate call.
//
// Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026):
// StyleSnapshot lost Width/Height + AllowedProviders/AllowedModels
// because StyleDefinition no longer carries the source fields
// (DefaultWidth/DefaultHeight were removed, the per-style allowlist
// checks were already retired in surface-3). The shape is now the
// storage-agnostic projection that maps 1:1 onto StyleDefinition
// after the registry's post-load ID normalisation.
package delivery

import (
	"fmt"
	"strings"
)

// SourceBackend abstracts a per-call style lookup. The concrete
// implementations (YAML loader, in-memory map, future DB) live outside
// this package; this separation matches God Pattern 0 (port abstraction
// per AGENTS.md).
type SourceBackend interface {
	GetStyle(styleID string) (StyleSnapshot, error)
}

// StyleSnapshot is the storage-agnostic projection of a style. It is
// INTERNAL to this package — callers see only ResolvedStyle, never
// StyleSnapshot. The resolver translates snap -> ResolvedStyle after
// every fail-closed gate passes.
//
// Step-1 typed migration (A1, July 2026): the snapshot was slimmed
// down dramatically. Width/Height dropped together with the underlying
// StyleDefinition.DefaultWidth/DefaultHeight fields; Allowlists dropped
// with StyleDefinition.AllowedProviders/AllowedModels (the surface-3
// retirement). What's left maps 1:1 onto the canonical 8-field
// StyleDefinition plus optional source-side metadata.
type StyleSnapshot struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
	DestinationKey string
	Enabled        bool
}

// StyleResolver resolves a style by ID. Fail-closed contract.
// Validate is the void variant: callers only need the error.
//
// Step-1 (A1, July 2026): the per-style provider/model compatibility
// checks were retired already in surface-3 (July 2026 cut) once
// google-slides became the sole image-generation provider
// (commit d54728dc). The associated sentinels stay defined as
// re-export audit-pinning (godlike/06) so consumers can import them
// from this package even though they are never raised.
type StyleResolver interface {
	Resolve(styleID, provider, model string) (ResolvedStyle, error)
	Validate(styleID, provider, model string) error
}

// DefaultStyleID is the magic ID used when styleID is supplied empty.
// Operators may register a style under this name; if absent, empty
// input falls through to ErrStyleNotFound. The non-empty id
// "default" must be reserved in the source backend.
const DefaultStyleID = "default"

// New constructs the canonical resolver. nil source is rejected; returns
// a failing resolver that emits ErrStyleNotFound on every call.
func New(source SourceBackend) StyleResolver {
	if source == nil {
		return &failingResolver{err: ErrStyleNotFound}
	}
	return &styleResolverImpl{source: source}
}

// failingResolver is the nil-source fall-through.
type failingResolver struct{ err error }

func (r *failingResolver) Resolve(_, _, _ string) (ResolvedStyle, error) {
	return ResolvedStyle{}, r.err
}

func (r *failingResolver) Validate(_, _, _ string) error { return r.err }

// styleResolverImpl is the canonical explicit source-backed impl.
type styleResolverImpl struct{ source SourceBackend }

// Compile-time assertion: *styleResolverImpl satisfies StyleResolver.
var _ StyleResolver = (*styleResolverImpl)(nil)

// Compile-time assertion: *failingResolver satisfies StyleResolver.
var _ StyleResolver = (*failingResolver)(nil)

// Resolve performs the canonical fail-closed lookup in order:
//
//  1. nil-receiver/nil-source                     -> ErrStyleNotFound.
//  2. empty styleID                                -> fall back to
//     DefaultStyleID ("default"); if absent in source
//     -> ErrStyleNotFound.
//  3. ID absent in source                          -> ErrStyleNotFound.
//  4. ID found but Enabled = false                 -> ErrStyleDisabled.
//  5. success                                       -> ResolvedStyle populated.
//
// Step-1 typed migration (A1, July 2026): Width/Height were dropped
// from ResolvedStyle because StyleDefinition no longer carries
// DefaultWidth/DefaultHeight — dimensions are caller-supplied via
// the image generation request. The provider/model compatibility
// checks were retired already in surface-3 (July 2026) once
// google-slides became the sole image-generation provider; the
// ErrStyleProviderUnsupported / ErrStyleModelUnsupported sentinels
// stay defined for re-export audit-pinning (godlike/06).
func (r *styleResolverImpl) Resolve(styleID, provider, model string) (ResolvedStyle, error) {
	if r == nil || r.source == nil {
		return ResolvedStyle{}, ErrStyleNotFound
	}
	if strings.TrimSpace(styleID) == "" {
		styleID = DefaultStyleID
	}
	snap, err := r.source.GetStyle(styleID)
	if err != nil {
		return ResolvedStyle{}, fmt.Errorf("%w: %s", ErrStyleNotFound, styleID)
	}
	if snap.ID == "" {
		return ResolvedStyle{}, fmt.Errorf("%w: %s", ErrStyleNotFound, styleID)
	}
	if !snap.Enabled {
		return ResolvedStyle{}, fmt.Errorf("%w: %s", ErrStyleDisabled, styleID)
	}
	// Step-1 typed migration (A1, July 2026): the per-style
	// allowlist checks were retired already in surface-3; the
	// ErrStyleProviderUnsupported / ErrStyleModelUnsupported
	// sentinels stay defined for re-export audit-pinning.
	_ = ErrStyleProviderUnsupported
	_ = ErrStyleModelUnsupported
	return ResolvedStyle{
		ID:             snap.ID,
		Version:        snap.Version,
		PromptSuffix:   snap.PromptSuffix,
		NegativePrompt: snap.NegativePrompt,
		DestinationKey: snap.DestinationKey,
		Enabled:        snap.Enabled,
	}, nil
}

// Validate is the void variant.
func (r *styleResolverImpl) Validate(styleID, provider, model string) error {
	_, err := r.Resolve(styleID, provider, model)
	return err
}
