package vidrush

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
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
	return internetImageSegmentCacheKey(plan.segmentID, plan.textHash, generation.Language, generation.Model, generation.PromptVersion, plan.perQueryLimit)
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

// FinalizeVidRushBindingsWithCache is the canonical binding finalizer used by
// the generation use case. The in-memory map remains a fast L1 cache, while
// cache provides the durable L2 replay surface across process restarts.
// Cache failures are deliberately non-fatal: they must never turn a valid,
// already-persisted binding into a false failure or a false cache hit.
func FinalizeVidRushBindingsWithCache(ctx context.Context, segments []scriptpkg.VidRushSegmentResult, forceRefresh bool, cache scriptports.VidRushCachePort) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	segmentIndex := make(map[string]int, len(segments))
	boundAssetOwners := make(map[string]string)
	for _, original := range segments {
		seg := CloneVidRushSegmentResult(original)
		normalizeVidRushSegmentAssets(&seg)
		if seg.ExecutionMode.IsFixedMedia() {
			// Fixed media is already authoritative. Do not filter, rank,
			// deduplicate or rewrite its existing binding surface.
			out = append(out, seg)
			continue
		}
		valid := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.Candidates))
		seen := make(map[string]struct{}, len(seg.Assets.Candidates))
		for _, candidate := range seg.Assets.Candidates {
			if !validVidRushCandidate(candidate) || !readyVidRushCandidate(candidate) {
				continue
			}
			// Candidates are untrusted provider output, including replayed
			// durable-cache rows. Never rebind a candidate stamped for another
			// segment during finalization.
			if owner := strings.TrimSpace(candidate.SegmentID); owner != "" && owner != strings.TrimSpace(seg.SegmentID) {
				continue
			}
			if candidate.Position != seg.Position ||
				(strings.TrimSpace(candidate.TextHash) != "" && strings.TrimSpace(candidate.TextHash) != strings.TrimSpace(seg.TextHash)) {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if owner, exists := boundAssetOwners[key]; exists && owner != seg.SegmentID {
				// The same provider asset may not be rebound to another
				// segment. Drop the later binding rather than leaking it.
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			boundAssetOwners[key] = seg.SegmentID
			valid = append(valid, candidate)
		}
		seg.Assets.Candidates = valid
		selectedImages := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.SecondaryImages))
		for _, image := range seg.Assets.SecondaryImages {
			if owner := strings.TrimSpace(image.SegmentID); owner != "" && owner != strings.TrimSpace(seg.SegmentID) {
				continue
			}
			if image.Position != seg.Position ||
				(strings.TrimSpace(image.TextHash) != "" && strings.TrimSpace(image.TextHash) != strings.TrimSpace(seg.TextHash)) {
				continue
			}
			selectedImages = append(selectedImages, image)
		}
		seg.Assets.SecondaryImages = preserveSelectedVidRushImages(selectedImages, valid)
		seg.Assets.GeneratedImages = filterVidRushGeneratedImages(seg.Assets.SecondaryImages)
		// Binding finalization validates and persists discovered candidates. It
		// deliberately does not choose a winner; MediaSampler owns selection.
		if len(seg.Assets.SecondaryImages) > 0 {
			// Image-only plans have no primary video by design. The durable,
			// rights-verified secondary image set is nevertheless the scene's
			// definitive VidRush binding and must be surfaced as such.
			seg.Assets.SelectionReason = "highest scored provenance-valid secondary images for image fallback"
		} else {
			seg.Assets.PrimaryVideo = nil
			seg.Assets.SelectionReason = "no provenance-valid candidate available"
		}
		seg.Assets.CandidateSetHash = candidateSetHash(valid)
		for i := range seg.Assets.Candidates {
			seg.Assets.Candidates[i].CandidateSetHash = seg.Assets.CandidateSetHash
		}
		bindingKey := segmentCacheKey("binding", seg.SegmentID, seg.TextHash, seg.Assets.CandidateSetHash, "vidrush-binding-v1")
		if len(valid) == 0 {
			seg.Cache.Binding = "BYPASSED"
		} else if !forceRefresh {
			_, l1Hit := cacheLoad(vidrushBindingCache, bindingKey)
			l2Hit, _ := loadVidRushPersistentJSON(ctx, cache, "binding", bindingKey, new(bool))
			if l1Hit || l2Hit {
				seg.Cache.Binding = "HIT_EXACT"
			} else {
				seg.Cache.Binding = "MISS"
				cacheStore(vidrushBindingCache, bindingKey, true)
				_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
			}
		} else {
			seg.Cache.Binding = "REFRESHED"
			cacheStore(vidrushBindingCache, bindingKey, true)
			_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
		}
		if existing, ok := segmentIndex[seg.SegmentID]; ok && strings.TrimSpace(seg.SegmentID) != "" {
			// Multiple provider processors may return the same segment delta.
			// Collapse it here so the durable response has one authoritative
			// segment and cannot expose duplicate keyword/provider bindings.
			out[existing] = mergeVidRushSegmentResult(out[existing], seg)
			continue
		}
		if strings.TrimSpace(seg.SegmentID) != "" {
			segmentIndex[seg.SegmentID] = len(out)
		}
		out = append(out, seg)
	}
	return out
}

func measureVidRushProvider(ctx context.Context, metrics any, info kernobs.OperationInfo, fn func(context.Context) error) error {
	if kernobs.FromContext(ctx) != nil {
		return kernobs.MeasureOperation(ctx, info, fn)
	}
	run := kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{AttemptID: "standalone"})
	started := time.Now()
	err := kernobs.MeasureOperation(kernobs.WithRun(ctx, run), info, fn)
	if timing, ok := metrics.(VidRushTimingMetrics); ok {
		report := run.Report()
		seconds := time.Since(started).Seconds()
		if len(report.Operations) > 0 {
			seconds = float64(report.Operations[len(report.Operations)-1].DurationMs) / 1000
		}
		timing.ObserveProviderDuration(info.Provider+"_search", seconds)
	}
	return err
}

// SegmentTimingBudget is the canonical visual timing input. Voiceover timing
// has precedence over scene timing, which has precedence over text estimation.
type SegmentTimingBudget = scriptpkg.VisualTimingBudget

func ResolveSegmentTimingBudget(segment scriptpkg.VidRushSegmentResult, plan *scriptpkg.ResolvedGenerationPlan) SegmentTimingBudget {
	if duration := voiceoverDurationMs(segment); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "voiceover"}
	}
	if duration := segmentSceneDurationMs(segment); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "scene"}
	}
	if duration := estimatedSegmentDurationMs(segment, plan); duration > 0 {
		return SegmentTimingBudget{SegmentID: segment.SegmentID, DurationMs: duration, Source: "estimated"}
	}
	return SegmentTimingBudget{SegmentID: segment.SegmentID}
}

func segmentDurationBudgetMs(segment scriptpkg.VidRushSegmentResult, plan *scriptpkg.ResolvedGenerationPlan) (int64, string) {
	budget := ResolveSegmentTimingBudget(segment, plan)
	return budget.DurationMs, budget.Source
}

func voiceoverDurationMs(segment scriptpkg.VidRushSegmentResult) int64 {
	if segment.Assets.PrimaryVideo != nil && segment.Assets.PrimaryVideo.DurationMs > 0 {
		return segment.Assets.PrimaryVideo.DurationMs
	}
	return 0
}

func segmentSceneDurationMs(segment scriptpkg.VidRushSegmentResult) int64 {
	for _, candidate := range segment.Assets.Candidates {
		if candidate.DurationMs > 0 && !strings.EqualFold(candidate.Provider, scriptpkg.VidRushProviderYouTube) {
			return candidate.DurationMs
		}
	}
	return 0
}
func estimatedSegmentDurationMs(segment scriptpkg.VidRushSegmentResult, _ *scriptpkg.ResolvedGenerationPlan) int64 {
	words := len(strings.Fields(segment.Text))
	if words == 0 {
		return 0
	}
	duration := int64(words) * 400
	if duration < 4000 {
		return 4000
	}
	return duration
}
