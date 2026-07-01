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

// StyleResolver resolves a style by ID, validating provider/model
// compatibility against the style's allow-lists. Fail-closed contract.
// Validate is the void variant: callers only need the error.
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
//  5. AllowedProviders non-empty AND provider not
//     in the list                                  -> ErrStyleProviderUnsupported.
//  6. AllowedModels non-empty AND model not
//     in the list                                 -> ErrStyleModelUnsupported.
//  7. success                                       -> ResolvedStyle populated.
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
	if len(snap.AllowedProviders) > 0 && !containsString(snap.AllowedProviders, provider) {
		return ResolvedStyle{}, fmt.Errorf("%w: style=%s provider=%s",
			ErrStyleProviderUnsupported, styleID, provider)
	}
	if len(snap.AllowedModels) > 0 && !containsString(snap.AllowedModels, model) {
		return ResolvedStyle{}, fmt.Errorf("%w: style=%s model=%s",
			ErrStyleModelUnsupported, styleID, model)
	}
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

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
