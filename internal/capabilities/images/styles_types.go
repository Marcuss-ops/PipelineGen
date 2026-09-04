package images

import imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"

// Root compatibility aliases. Canonical ownership lives in images/styles.
type ResolvedStyle = imagestyles.ResolvedStyle
type StyleID = imagestyles.StyleID
type StyleDefinition = imagestyles.StyleDefinition
type StyleComposedPrompt = imagestyles.StyleComposedPrompt

var (
	ErrStyleNotFound            = imagestyles.ErrStyleNotFound
	ErrStyleProviderUnsupported = imagestyles.ErrStyleProviderUnsupported
	ErrStyleModelUnsupported    = imagestyles.ErrStyleModelUnsupported
	ErrUnknownStyle             = imagestyles.ErrUnknownStyle
	ErrStyleDisabled            = imagestyles.ErrStyleDisabled
	ErrEmptyPrompt              = imagestyles.ErrEmptyPrompt
	ErrStyleVersionMismatch     = imagestyles.ErrStyleVersionMismatch
)
