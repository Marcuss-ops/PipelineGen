package generation

import (
	"errors"
)

var (
	ErrProviderUnavailable          = errors.New("generated image provider unavailable")
	ErrProviderNotFound             = errors.New("generated: provider id not found in registry")
	ErrNoGenerationProviderWired    = errors.New("images: GenerationProviderRegistry not wired (PR-GODOBJ-3 KILL LIST a — legacy imageGen.Generate fallback REMOVED; composition must wire NewGenerationProviderRegistry from internal/capabilities/images/generation)")
	ErrImageGenProviderNotAvailable = errors.New("image generation provider temporarily unavailable")
	ErrImageGenPermanent            = errors.New("image generation request permanently rejected")
	ErrImageGenNetwork              = errors.New("image generation: network error")
	ErrImageGenQuota                = errors.New("image generation: quota exceeded")
	ErrImageGenAuth                 = errors.New("image generation: authentication error")
	ErrImageGenNoImageCandidate     = errors.New("image generation: no image candidate (worker reported ErrNoImageCandidate)")
	ErrImageGenBlankOrPlaceholder   = errors.New("image generation: blank/placeholder detected by visual_validate")
	ErrImageGenTimeout              = errors.New("image generation: timeout waiting for new candidate (worker reported ErrGenerationTimeout)")
	ErrImageGenPolicy               = errors.New("image generation: content policy rejection")
	ErrImageGenRatioNotSelected     = errors.New("image generation: 16:9 ratio not selected (mandatory UI step failed)")
)

// ClassifyError maps worker/provider messages onto the canonical typed errors.
// Case order is intentional: typed content failures win over generic transport
// words that may be present in the same message.
