package adapters

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// VidRushGenerationRequest describes only the missing-image calculation. The
// actual async dispatch remains owned by image.generate.google and its existing
// ArtifactManifest finalizer.
type VidRushGenerationRequest struct {
	SegmentTextHash string
	Prompt          string
	Style           string
	Width           int
	Height          int
	Provider        string
	PromptVersion   string
	TargetImages    int
}

// VidRushGenerationCacheKey is stable across process restarts and includes
// every input that can change the generated artifact.
func VidRushGenerationCacheKey(req VidRushGenerationRequest) string {
	sum := digest.SHA256Bytes([]byte(strings.Join([]string{
		req.SegmentTextHash, req.Prompt, req.Style, fmt.Sprint(req.Width), fmt.Sprint(req.Height),
		req.Provider, req.PromptVersion,
	}, "\x00")))
	return sum
}
