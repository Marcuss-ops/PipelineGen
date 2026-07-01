// Package styles — ResolvedStyle + sentinel errors (image-territories
// action plan, July 2026, FASE 2A-2C).
//
// This package introduces the canonical "resolved" projection of an AI
// generation style plus the four fail-closed errors StyleResolver emits.
// Styles(Resolver) and its impl live in resolver.go. The backing store
// is delegated to the SourceBackend interface so any concrete loader
// (YAML, in-memory, future DB) can plug in without rewriting the
// resolver.
package styles

import "errors"

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
