package styles

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/styleerrors"
)

// ResolvedStyle is the provider-neutral projection returned after style validation.
type ResolvedStyle struct {
	ID             string
	Version        int
	PromptSuffix   string
	NegativePrompt string
	DestinationKey string
	Enabled        bool
}

var (
	ErrStyleNotFound            = errors.New("styles: style not found")
	ErrStyleProviderUnsupported = errors.New("styles: provider not allowed for this style")
	ErrStyleModelUnsupported    = errors.New("styles: model not allowed for this style")
	ErrUnknownStyle             = styleerrors.ErrUnknownStyle
	ErrStyleDisabled            = styleerrors.ErrStyleDisabled
	ErrEmptyPrompt              = styleerrors.ErrEmptyPrompt
	ErrStyleVersionMismatch     = styleerrors.ErrStyleVersionMismatch
)

type StyleID = asset.StyleID
type StyleDefinition = asset.GenerationStyle

// StyleComposedPrompt is the canonical rendered prompt plus resolved style metadata.
type StyleComposedPrompt struct {
	ComposedText   string
	StyleID        string
	StyleVersion   int
	PromptSuffix   string
	NegativePrompt string
	DestinationKey string
}
