package generation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
func ClassifyError(errMsg string) error {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "quota") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many") || strings.Contains(lower, "429"):
		return fmt.Errorf("%w: %s", ErrImageGenQuota, errMsg)
	case strings.Contains(lower, "auth") || strings.Contains(lower, "login") || strings.Contains(lower, "session") || strings.Contains(lower, "cookie") || strings.Contains(lower, "401") || strings.Contains(lower, "403"):
		return fmt.Errorf("%w: %s", ErrImageGenAuth, errMsg)
	case strings.Contains(lower, "policy") || strings.Contains(lower, "safety") || strings.Contains(lower, "blocked") || strings.Contains(lower, "content"):
		return fmt.Errorf("%w: %s", ErrImageGenPolicy, errMsg)
	case strings.Contains(lower, "errnoimagecandidate"):
		return fmt.Errorf("%w: %s", ErrImageGenNoImageCandidate, errMsg)
	case strings.Contains(lower, "errblankorplaceholder"):
		return fmt.Errorf("%w: %s", ErrImageGenBlankOrPlaceholder, errMsg)
	case strings.Contains(lower, "errgenerationtimeout"):
		return fmt.Errorf("%w: %s", ErrImageGenTimeout, errMsg)
	case strings.Contains(lower, "errimagegenrationotselected") || strings.Contains(lower, "ratio-not-selected"):
		return fmt.Errorf("%w: %s", ErrImageGenRatioNotSelected, errMsg)
	case strings.Contains(lower, "network") || strings.Contains(lower, "connection") || strings.Contains(lower, "timeout") || strings.Contains(lower, "refused") || strings.Contains(lower, "dns") || strings.Contains(lower, "eof"):
		return fmt.Errorf("%w: %s", ErrImageGenNetwork, errMsg)
	default:
		return fmt.Errorf("%w: %s", ErrImageGenPermanent, errMsg)
	}
}

func ComputeSourceHash(provider, prompt, style string, width, height int, model string) string {
	prompt = strings.TrimSpace(strings.ToLower(prompt))
	style = strings.TrimSpace(strings.ToLower(style))
	model = strings.TrimSpace(strings.ToLower(model))
	return digest.SHA256Bytes([]byte(fmt.Sprintf("%s|%s|%s|%d|%d|%s", provider, prompt, style, width, height, model)))
}

func IsRetryable(err error) bool {
	return errors.Is(err, ErrImageGenNetwork) || errors.Is(err, ErrImageGenQuota) || errors.Is(err, ErrImageGenAuth)
}
