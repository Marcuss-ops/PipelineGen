package imageverify

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// Policy is deliberately conservative and CPU-friendly. The policy is shared
// by web-image and generated-image verification.
type Policy struct {
	MaxBytes int64
	// MinWidth/MinHeight remain the canonical area floor (both were the
	// historical two-sided minimum). MinLongSide/MinShortSide add the
	// orientation-aware rule that admits vertical portraits instead of
	// rejecting a 600x900 image for being narrower than 640.
	MinWidth       int
	MinHeight      int
	MinLongSide    int
	MinShortSide   int
	MaxAspectRatio float64
	// MaxPixels rejects a declared decompression bomb before a full decode.
	MaxPixels    int
	Timeout      time.Duration
	MaxRedirects int
}

func DefaultPolicy() Policy {
	return Policy{
		MaxBytes:       20 << 20,
		MinWidth:       640,
		MinHeight:      360,
		MinLongSide:    640,
		MinShortSide:   360,
		MaxAspectRatio: 3.0,
		MaxPixels:      digest.DefaultImagePixelBudget,
		Timeout:        15 * time.Second,
		MaxRedirects:   3,
	}
}

func NormalizePolicy(policy Policy) Policy {
	defaults := DefaultPolicy()
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = defaults.MaxBytes
	}
	if policy.MinWidth <= 0 {
		policy.MinWidth = defaults.MinWidth
	}
	if policy.MinHeight <= 0 {
		policy.MinHeight = defaults.MinHeight
	}
	if policy.MinLongSide <= 0 {
		policy.MinLongSide = defaults.MinLongSide
	}
	if policy.MinShortSide <= 0 {
		policy.MinShortSide = defaults.MinShortSide
	}
	if policy.MaxAspectRatio <= 0 {
		policy.MaxAspectRatio = defaults.MaxAspectRatio
	}
	if policy.MaxPixels <= 0 {
		policy.MaxPixels = defaults.MaxPixels
	}
	if policy.Timeout <= 0 {
		policy.Timeout = defaults.Timeout
	}
	if policy.MaxRedirects < 0 {
		policy.MaxRedirects = defaults.MaxRedirects
	}
	return policy
}

func (p Policy) WithinPixelBudget(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	policy := NormalizePolicy(p)
	return int64(width)*int64(height) <= int64(policy.MaxPixels)
}

// AcceptsDimensions applies the orientation-aware minimum: long side, short
// side, area and aspect ratio. A portrait 600x900 passes; a 1200x300 banner
// and a tiny 320x240 thumbnail fail. It does not apply the upper pixel bound;
// callers check WithinPixelBudget separately so a bomb maps to "too large"
// rather than "invalid".
func (p Policy) AcceptsDimensions(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	policy := NormalizePolicy(p)
	longSide, shortSide := width, height
	if shortSide > longSide {
		longSide, shortSide = shortSide, longSide
	}
	if longSide < policy.MinLongSide || shortSide < policy.MinShortSide {
		return false
	}
	if policy.MinWidth > 0 && policy.MinHeight > 0 &&
		int64(width)*int64(height) < int64(policy.MinWidth)*int64(policy.MinHeight) {
		return false
	}
	if policy.MaxAspectRatio > 0 && float64(longSide)/float64(shortSide) > policy.MaxAspectRatio {
		return false
	}
	return true
}
