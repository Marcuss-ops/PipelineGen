package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func loadVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, dst any) (bool, error) {
	if cache == nil {
		return false, nil
	}
	raw, hit, err := cache.Get(ctx, namespace, key)
	if err != nil || !hit {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("vidrush cache %s/%s: decode: %w", namespace, key, err)
	}
	return true, nil
}

func storeVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, value any) error {
	if cache == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("vidrush cache %s/%s: encode: %w", namespace, key, err)
	}
	if err := scriptports.ValidateVidRushCachePayload(raw); err != nil {
		return fmt.Errorf("vidrush cache %s/%s: validate: %w", namespace, key, err)
	}
	return cache.Put(ctx, namespace, key, raw)
}

var (
	vidrushExtractionCache   sync.Map
	vidrushArtlistCache      sync.Map
	vidrushImageCache        sync.Map
	vidrushBindingCache      sync.Map
	vidrushMaterializedCache sync.Map
)

func buildCanonicalSegments(plan *scriptpkg.ResolvedGenerationPlan, scenes []scriptpkg.SpecScene, text string) []scriptpkg.CanonicalSegment {
	if plan == nil {
		return buildCanonicalSegmentsFromScenes(scenes, text)
	}
	if len(scenes) > 0 {
		return buildCanonicalSegmentsFromScenes(scenes, text)
	}
	if len(plan.Segments) > 0 {
		out := make([]scriptpkg.CanonicalSegment, 0, len(plan.Segments))
		for i, seg := range plan.Segments {
			segText := strings.TrimSpace(seg.SourceText)
			if segText == "" {
				segText = strings.TrimSpace(seg.Topic)
			}
			if segText == "" {
				segText = strings.TrimSpace(text)
			}
			id := strings.TrimSpace(seg.ID)
			if id == "" {
				id = fmt.Sprintf("segment-%03d", i+1)
			}
			out = append(out, scriptpkg.CanonicalSegment{
				ID:       id,
				Position: i,
				Text:     segText,
				TextHash: segmentTextHash(segText),
			})
		}
		return out
	}
	return buildCanonicalSegmentsFromScenes(nil, text)
}

func buildCanonicalSegmentsFromScenes(scenes []scriptpkg.SpecScene, text string) []scriptpkg.CanonicalSegment {
	if len(scenes) > 0 {
		out := make([]scriptpkg.CanonicalSegment, 0, len(scenes))
		for i, scene := range scenes {
			segText := strings.TrimSpace(scene.Text)
			if segText == "" {
				continue
			}
			id := strings.TrimSpace(scene.SegmentID)
			if id == "" {
				id = strings.TrimSpace(scene.ID)
			}
			if id == "" {
				id = fmt.Sprintf("segment-%03d", i+1)
			}
			out = append(out, scriptpkg.CanonicalSegment{
				ID:       id,
				SceneID:  strings.TrimSpace(scene.ID),
				Position: i,
				Text:     segText,
				TextHash: segmentTextHash(segText),
			})
		}
		if len(out) > 0 {
			return out
		}
	}
	parts := splitParagraphSegments(text)
	if len(parts) == 0 {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil
		}
		return []scriptpkg.CanonicalSegment{{
			ID:       "segment-001",
			Position: 0,
			Text:     trimmed,
			TextHash: segmentTextHash(trimmed),
		}}
	}
	out := make([]scriptpkg.CanonicalSegment, 0, len(parts))
	for i, part := range parts {
		out = append(out, scriptpkg.CanonicalSegment{
			ID:       fmt.Sprintf("segment-%03d", i+1),
			Position: i,
			Text:     part,
			TextHash: segmentTextHash(part),
		})
	}
	return out
}

func splitParagraphSegments(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n\n")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func segmentTextHash(text string) string {
	sum := sha256.Sum256([]byte(normalizeSegmentText(text)))
	return hex.EncodeToString(sum[:])
}

func normalizeSegmentText(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func uniqueLimitedEntities(values []scriptpkg.ExtractedEntity, limit int) []scriptpkg.ExtractedEntity {
	if limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]scriptpkg.ExtractedEntity, 0, minInt(limit, len(values)))
	for _, v := range values {
		if strings.TrimSpace(v.Value) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(v.Value)) + "|" + strings.ToUpper(strings.TrimSpace(v.Type))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func uniqueLimitedStrings(values []string, limit int) []string {
	return sliceutil.UniqueLimitedStrings(values, limit)
}

func buildArtlistQueries(segmentText string, entities []scriptpkg.ExtractedEntity, phrases []string, words []string, topic string) []string {
	candidates := make([]string, 0, 12)
	if visual := compactVisualQuery(segmentText); visual != "" {
		candidates = append(candidates, visual)
	}
	if topic = strings.TrimSpace(topic); topic != "" {
		// Keep one query grounded in the plan context before the per-segment
		// enrichment terms consume the provider query limit.
		candidates = append(candidates, topic+" cinematic scene")
	}
	for _, entity := range entities {
		v := strings.TrimSpace(entity.Value)
		if v == "" {
			continue
		}
		candidates = append(candidates, v+" action scene")
	}
	candidates = append(candidates, phrases...)
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		lower := strings.ToLower(word)
		if textutil.IsStopWord(lower) {
			continue
		}
		candidates = append(candidates, word+" visual scene")
	}
	if segmentText != "" {
		candidates = append(candidates, segmentText)
	}
	return uniqueLimitedStrings(candidates, 5)
}

func buildImageQueries(segmentText string, entities []scriptpkg.ExtractedEntity, phrases []string, words []string, topic string) []string {
	candidates := make([]string, 0, 12)
	if visual := compactVisualQuery(segmentText); visual != "" {
		candidates = append(candidates, visual)
	}
	for _, entity := range entities {
		v := strings.TrimSpace(entity.Value)
		if v == "" {
			continue
		}
		candidates = append(candidates, v)
		if topic != "" {
			candidates = append(candidates, v+" "+topic)
		}
	}
	candidates = append(candidates, phrases...)
	candidates = append(candidates, words...)
	if topic = strings.TrimSpace(topic); topic != "" {
		candidates = append(candidates, topic)
	}
	if segmentText != "" {
		candidates = append(candidates, segmentText)
	}
	return uniqueLimitedStrings(candidates, 5)
}

// compactVisualQuery converts a source or narration sentence into a bounded
// provider query. Retrieval providers rank short visual noun phrases more
// reliably than model prose containing several clauses and editorial filler.
// It is deterministic, language-agnostic at the tokenizer boundary, and
// never replaces the original segment text or its hash.
func compactVisualQuery(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if end := strings.IndexAny(text, ".!?\n"); end > 0 {
		text = text[:end]
	}
	tokens := textutil.TokenizeWithStopWords(text)
	if len(tokens) > 7 {
		tokens = tokens[:7]
	}
	return strings.Join(tokens, " ")
}

func segmentCacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cacheLoad(cache *sync.Map, key string) (any, bool) {
	if cache == nil || key == "" {
		return nil, false
	}
	return cache.Load(key)
}

func cacheStore(cache *sync.Map, key string, value any) {
	if cache == nil || key == "" {
		return
	}
	cache.Store(key, value)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cloneVidRushSegmentResult(in scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := in
	out.Insights.Entities = append([]scriptpkg.ExtractedEntity(nil), in.Insights.Entities...)
	out.Insights.ImportantPhrases = append([]string(nil), in.Insights.ImportantPhrases...)
	out.Insights.ImportantWords = append([]string(nil), in.Insights.ImportantWords...)
	out.Insights.ArtlistQueries = append([]string(nil), in.Insights.ArtlistQueries...)
	out.Insights.ImageQueries = append([]string(nil), in.Insights.ImageQueries...)
	out.Assets.SecondaryImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.SecondaryImages...)
	out.Assets.GeneratedImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.GeneratedImages...)
	out.Assets.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.Candidates...)
	if in.Assets.PrimaryVideo != nil {
		primary := *in.Assets.PrimaryVideo
		out.Assets.PrimaryVideo = &primary
	}
	return out
}

// FinalizeVidRushBindings is the single binding finalization step for the
// per-segment result. It accepts only provider candidates with provenance,
// computes a stable candidate-set hash, and records binding cache state.
// Provider processors remain responsible for searching; this function only
// normalizes and selects from their closed candidate set.
func FinalizeVidRushBindings(segments []scriptpkg.VidRushSegmentResult, forceRefresh bool) []scriptpkg.VidRushSegmentResult {
	return FinalizeVidRushBindingsWithCache(context.Background(), segments, forceRefresh, nil)
}

// FinalizeVidRushBindingsWithCache is the canonical binding finalizer used by
// the generation use case. The in-memory map remains a fast L1 cache, while
// cache provides the durable L2 replay surface across process restarts.
// Cache failures are deliberately non-fatal: they must never turn a valid,
// already-persisted binding into a false failure or a false cache hit.
func FinalizeVidRushBindingsWithCache(ctx context.Context, segments []scriptpkg.VidRushSegmentResult, forceRefresh bool, cache scriptports.VidRushCachePort) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	lastAssetByProvider := make(map[string]string)
	for _, original := range segments {
		seg := cloneVidRushSegmentResult(original)
		valid := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.Candidates))
		seen := make(map[string]struct{}, len(seg.Assets.Candidates))
		for _, candidate := range seg.Assets.Candidates {
			if !validVidRushCandidate(candidate) || !readyVidRushCandidate(candidate) {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(candidate.Provider)) + "\x00" + strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			valid = append(valid, candidate)
		}
		seg.Assets.Candidates = valid
		seg.Assets.SecondaryImages = filterVidRushImages(valid)
		seg.Assets.GeneratedImages = filterVidRushGeneratedImages(valid)
		if primary := chooseVidRushPrimary(valid, lastAssetByProvider); primary != nil {
			primary.SelectionReason = "highest scored provenance-valid candidate for segment"
			seg.Assets.PrimaryVideo = primary
			lastAssetByProvider[primary.Provider] = primary.AssetID
			seg.Assets.SelectionReason = primary.SelectionReason
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
			_, l1Hit := cacheLoad(&vidrushBindingCache, bindingKey)
			l2Hit, _ := loadVidRushPersistentJSON(ctx, cache, "binding", bindingKey, new(bool))
			if l1Hit || l2Hit {
				seg.Cache.Binding = "HIT_EXACT"
			} else {
				seg.Cache.Binding = "MISS"
				cacheStore(&vidrushBindingCache, bindingKey, true)
				_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
			}
		} else {
			seg.Cache.Binding = "REFRESHED"
			cacheStore(&vidrushBindingCache, bindingKey, true)
			_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
		}
		out = append(out, seg)
	}
	return out
}

// vidRushForbiddenProviders lists provider values that MUST be rejected
// regardless of provenance. This gate enforces the VidRush provider separation
// contract (internet_images for images, artlist for video, zero YouTube/GAI).
var vidRushForbiddenProviders = map[string]bool{
	"youtube":             true,
	"generated_images":    true,
	"local_youtube_stock": true,
	"local_stock":         true,
}

// vidRushForbiddenURLPatterns lists URL substrings that disqualify a candidate
// even when the provider field is not directly "youtube".
var vidRushForbiddenURLPatterns = []string{
	"youtube-nocookie.com",
	"youtube.com",
	"youtu.be",
}

func validVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.Provider) == "" || candidate.Score < 0 {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if vidRushForbiddenProviders[provider] {
		return false
	}
	// Generated assets are accepted only through the lifecycle-aware
	// image.generate.google path. A legacy remote URL must never masquerade as
	// a generated, durable artifact.
	if provider == scriptpkg.VidRushProviderImageGeneration && candidate.IsLegacyCandidate() {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "rejected") {
		return false
	}
	sourceURL := strings.ToLower(strings.TrimSpace(candidate.SourceURL))
	for _, pattern := range vidRushForbiddenURLPatterns {
		if strings.Contains(sourceURL, pattern) {
			return false
		}
	}
	if provider == "artlist" {
		return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.DriveLink) != ""
	}
	return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}

func readyVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	// Lifecycle-aware candidates are fail-closed. Legacy candidates remain
	// readable during the migration window and are validated by the existing
	// provenance predicate above.
	if candidate.IsLegacyCandidate() {
		// Legacy rows are readable during migration, but a remote search
		// candidate without a durable Drive location is not legacy evidence.
		// In particular, failed acquisition paths must not remain eligible
		// merely because their lifecycle fields are empty.
		return strings.TrimSpace(candidate.DriveLink) != ""
	}
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.FileHash) != "" && strings.TrimSpace(candidate.DriveLink) != ""
}

// VidRushRankingWeights is the shared deterministic ranking policy used by
// every provider. Scores are expected in the [0,1] range and are clamped.
type VidRushRankingWeights struct {
	Relevance           float64
	TechnicalQuality    float64
	Rights              float64
	Diversity           float64
	ProviderReliability float64
}

var defaultVidRushRankingWeights = VidRushRankingWeights{
	Relevance: 0.40, TechnicalQuality: 0.20, Rights: 0.20, Diversity: 0.10, ProviderReliability: 0.10,
}

func ScoreVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate, repeated bool) float64 {
	if candidate.RelevanceScore == 0 && candidate.TechnicalQualityScore == 0 && candidate.RightsScore == 0 && candidate.DiversityScore == 0 && candidate.ProviderReliability == 0 {
		return candidate.Score
	}
	clamp := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	w := defaultVidRushRankingWeights
	score := w.Relevance*clamp(candidate.RelevanceScore) +
		w.TechnicalQuality*clamp(candidate.TechnicalQualityScore) +
		w.Rights*clamp(candidate.RightsScore) +
		w.Diversity*clamp(candidate.DiversityScore) +
		w.ProviderReliability*clamp(candidate.ProviderReliability)
	if repeated {
		score *= 0.75
	}
	return score
}

func chooseVidRushPrimary(candidates []scriptpkg.SegmentAssetCandidate, previous map[string]string) *scriptpkg.SegmentAssetCandidate {
	var best *scriptpkg.SegmentAssetCandidate
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Provider != "artlist" {
			continue
		}
		repeated := previous[candidate.Provider] == candidate.AssetID
		if repeated && len(candidates) > 1 {
			continue
		}
		candidate.Score = ScoreVidRushCandidate(candidate, repeated)
		if best == nil || candidate.Score > best.Score {
			selected := candidate
			best = &selected
		}
	}
	return best
}

func filterVidRushImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != "artlist" && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
			out = append(out, candidate)
		}
	}
	return out
}

func filterVidRushGeneratedImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider == scriptpkg.VidRushProviderImageGeneration {
			out = append(out, candidate)
		}
	}
	return out
}

func candidateSetHash(candidates []scriptpkg.SegmentAssetCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, strings.Join([]string{
			candidate.AssetID, candidate.Provider, candidate.Query, candidate.SourceURL,
			candidate.PreviewURL, candidate.FileHash, candidate.DriveLink,
			candidate.AcquisitionStatus, candidate.VerificationStatus,
			candidate.PersistenceStatus, candidate.IndexStatus,
		}, "\x00"))
	}
	return segmentCacheKey(parts...)
}
