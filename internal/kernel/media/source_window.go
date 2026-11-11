package media

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SourceWindow identifies an exact temporal window in an immutable source
// asset. Path/URL locators deliberately live outside this value: they may
// expire or differ between workers without changing the media identity.
type SourceWindow struct {
	AssetSHA256 string `json:"asset_sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	StartUS     int64  `json:"start_us"`
	EndUS       int64  `json:"end_us"`
}

// Validate checks the immutable asset identity and the half-open time range.
func (w SourceWindow) Validate() error {
	if !digest.IsCanonicalSHA256(w.AssetSHA256) {
		return fmt.Errorf("source window: asset_sha256 must be canonical SHA-256")
	}
	if w.SizeBytes <= 0 {
		return fmt.Errorf("source window: size_bytes must be positive")
	}
	if w.StartUS < 0 || w.EndUS <= w.StartUS {
		return fmt.Errorf("source window: invalid half-open range [%d,%d)", w.StartUS, w.EndUS)
	}
	return nil
}
