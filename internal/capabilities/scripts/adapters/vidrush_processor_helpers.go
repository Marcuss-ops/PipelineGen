// Package adapters — vidrush_processor_helpers.go consolidates the small
// VidRush provider/materialization helpers that were previously one file each.
// Ownership and behavior are unchanged; the consolidation reduces same-package
// micro-files.
package adapters

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// clampUnit is a neutral score-normalization helper for discovery metadata.
// It does not select or rank media candidates.
func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func vidRushFanoutImageCacheKey(plan *vidRushFanoutPlan, generation *scriptpkg.ResolvedGenerationPlan) string {
	return segmentCacheKey("internet-images-assets-v5", plan.segmentID, plan.textHash, generation.Language, generation.Model, generation.PromptVersion, fmt.Sprintf("%d", plan.perQueryLimit))
}

// SceneImage is a single scene-image outcome from ImageProcessor.
// PR 9: images map to model-defined scenes 1:1.
type SceneImage struct {
	Index       int
	Text        string // scene text used as the generation prompt
	URL         string // public URL of the generated image
	DriveFileID string
}

func SceneImageDriveLink(img SceneImage) string {
	url := strings.TrimSpace(img.URL)
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	if img.DriveFileID != "" {
		return fmt.Sprintf("https://drive.google.com/file/d/%s/view", img.DriveFileID)
	}
	return url
}

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

// searchClassForEntityQuery carries the extractor's type into provider
// routing. Without it, the search adapter silently falls back to generic web
// image search even though person/editorial routes are implemented.
func searchClassForEntityQuery(entities []scriptpkg.ExtractedEntity, query string) string {
	query = normalizeEntitySearchText(query)
	if query == "" {
		return ""
	}
	for _, entity := range entities {
		name := normalizeEntitySearchText(entity.Value)
		if name == "" || !entityNameAppearsInQuery(name, query) {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(entity.Type)) {
		case "PERSON":
			return "person"
		case "BRAND", "LOGO":
			return "brand"
		case "ORG", "ORGANIZATION", "COMPANY":
			return "organization"
		case "GPE", "PLACE", "LOCATION", "LOC":
			return "location"
		case "PRODUCT", "OBJECT":
			return "object"
		}
	}
	return ""
}

func normalizeEntitySearchText(value string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func entityNameAppearsInQuery(name, query string) bool {
	return name == query || strings.Contains(" "+query+" ", " "+name+" ")
}

// logVidRushSelectedImages emits one debug line per selected image. It is a
// no-op for a nil logger or an empty selection and never mutates its input.
func logVidRushSelectedImages(log *zap.Logger, segmentID string, images []scriptpkg.SegmentAssetCandidate) {
	if log == nil {
		return
	}
	for index, image := range images {
		log.Debug("VidRush image selected",
			zap.String("segment_id", segmentID),
			zap.Int("position", index),
			zap.String("asset_id", image.AssetID),
			zap.String("provider", image.Provider),
			zap.String("query", image.Query),
			zap.Float64("score", image.Score),
			zap.String("perceptual_hash", image.PerceptualHash),
		)
	}
}

func (p *VidRushMaterializationProcessor) metadataOnlyResult(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, bool) {
	if plan == nil || plan.MediaPlan.Materialization.Mode != mediadomain.MaterializationMetadataOnly {
		return nil, false
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	for _, segment := range input.VidRushSegments {
		segments = append(segments, CloneVidRushSegmentResult(segment))
	}
	return &PostProcessResult{VidRushSegments: segments, Changed: len(segments) > 0}, true
}

func materializationDependenciesError(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer) error {
	if providers != nil && finalizer != nil {
		return nil
	}
	if vidRushMaterializationRequested(plan, input) {
		return fmt.Errorf("vidrush materialization: provider registry and common finalizer are required")
	}
	return nil
}
