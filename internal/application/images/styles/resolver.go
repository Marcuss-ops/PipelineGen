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
package styles

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
// AllowedProviders / AllowedModels are retained on the snapshot for
// yaml back-compat (config/generation_styles.yaml still carries the
// keys). As of surface-3 (July 2026) the canonical resolver no longer
// reads these fields — see Resolve's doc-comment for the rationale.
type StyleSnapshot struct {
	ID               string
	Version          int
	PromptSuffix     string
	NegativePrompt   string
	Width, Height    int
	DestinationKey   string
	Enabled          bool
	AllowedProviders []string
	AllowedModels    []string
}

// StyleResolver resolves a style by ID. Fail-closed contract.
// Validate is the void variant: callers only need the error.
//
// The provider/model compatibility checks that historically lived
// inside Resolve (formerly steps 5 and 6 of the fail-closed chain)
// were retired in surface-3 (July 2026) — see Resolve's doc-comment
// for the canonical rationale.
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
//                                                    -> ErrStyleNotFound.
//  3. ID absent in source                          -> ErrStyleNotFound.
//  4. ID found but Enabled = false                 -> ErrStyleDisabled.
//  5. success                                       -> ResolvedStyle populated.
//
// Dead-code (surface-3, July 2026): the per-style AllowedProviders /
// AllowedModels checks (formerly steps 5 and 6 of this list, which
// raised ErrStyleProviderUnsupported / ErrStyleModelUnsupported via
// the containsString helper) were retired once google-slides became
// the sole image-generation provider (commit d54728dc — surface 1
// cut). The GenerationStyle.AllowedProviders / AllowedModels fields
// (types_aux.go:83,87) remain on the domain type for yaml
// back-compat (existing config/generation_styles.yaml files still
// carry the keys; ignored at resolve time). The
// ErrStyleProviderUnsupported / ErrStyleModelUnsupported sentinels
// stay defined for re-export audit-pinning (godlike/06) — see
// internal/application/assets/generation/style_registry.go for the
// stable re-export surface, and
// resolver_test.go::TestStyleResolver_AllSentinelErrorsNonNil for
// the non-nil semantic contract that callers can rely on.
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
	// surface-3 (July 2026): per-style allowlist checks retired. See
	// the doc-comment above for the rationale + audit-pinning chain.
	// The sentinels stay defined for re-export but are never raised.
	_ = ErrStyleProviderUnsupported
	_ = ErrStyleModelUnsupported
	return ResolvedStyle{
		ID:             snap.ID,
		Version:        snap.Version,
		PromptSuffix:   snap.PromptSuffix,
		NegativePrompt: snap.NegativePrompt,
		Width:          snap.Width,
		Height:         snap.Height,
		DestinationKey: snap.DestinationKey,
		Enabled:        snap.Enabled,
	}, nil
}

// Validate is the void variant.
func (r *styleResolverImpl) Validate(styleID, provider, model string) error {
	_, err := r.Resolve(styleID, provider, model)
	return err
}
