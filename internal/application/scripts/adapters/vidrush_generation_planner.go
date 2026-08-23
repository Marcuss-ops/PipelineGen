package adapters

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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

func MissingVidRushImageCount(segment scriptpkg.VidRushSegmentResult, target int) int {
	if target <= 0 {
		return 0
	}
	verified := 0
	for _, image := range segment.Assets.SecondaryImages {
		if (image.Provider != scriptpkg.VidRushProviderInternetImages && image.Provider != scriptpkg.VidRushProviderImageGeneration) || !image.LifecycleComplete() {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(image.RightsStatus), "rejected") {
			continue
		}
		verified++
	}
	if verified >= target {
		return 0
	}
	return target - verified
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
