package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	sceneplanner "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

// artlistSegmentCacheKey makes explicit Artlist intent the stable identity of
// a tagged search. Generated prose can vary slightly across model retries;
// explicit keywords must still replay the same provider result. Untagged
// searches retain the text-hash identity used by the legacy path.
func artlistSegmentCacheKey(segmentID, textHash, intentHash, language, model, promptVersion string) string {
	if strings.TrimSpace(intentHash) != "" {
		textHash = ""
	}
	return versionedSegmentCacheKey("artlist-assets", scriptports.CacheVersion("artlist-v3"), segmentID, textHash, intentHash, language, model, promptVersion)
}

func materializeNarrativeScenes(plan *scriptpkg.ResolvedGenerationPlan, scenes []scriptpkg.SpecScene, text string) []scriptpkg.SpecScene {
	if plan == nil || plan.SingleScene || len(scenes) > 1 {
		return scenes
	}
	narrative := strings.TrimSpace(text)
	if narrative == "" && len(scenes) == 1 {
		narrative = strings.TrimSpace(scenes[0].Text)
	}
	if narrative == "" {
		narrative = strings.TrimSpace(plan.SourceText)
	}
	n := len(splitParagraphSegments(narrative))
	if n < 2 && plan.SourceText != "" {
		n = len(splitParagraphSegments(plan.SourceText))
	}
	if n < 2 && plan.SegmentWords > 0 {
		n = (len(strings.Fields(narrative)) + plan.SegmentWords - 1) / plan.SegmentWords
	}
	if n < 2 {
		return scenes
	}
	return sceneplanner.NewSceneSynthesizer().FromProse(narrative, n)
}

func buildCanonicalSegments(plan *scriptpkg.ResolvedGenerationPlan, scenes []scriptpkg.SpecScene, text string) []scriptpkg.CanonicalSegment {
	if plan == nil {
		return buildCanonicalSegmentsFromScenes(scenes, text)
	}
	if plan.SingleScene && len(plan.Segments) == 1 {
		id := strings.TrimSpace(plan.Segments[0].ID)
		if id == "" {
			id = "main"
		}
		sceneID := ""
		if len(scenes) > 0 {
			sceneID = strings.TrimSpace(scenes[0].ID)
		}
		segText := strings.TrimSpace(text)
		if segText == "" {
			segText = strings.TrimSpace(plan.Segments[0].SourceText)
		}
		if segText == "" {
			segText = strings.TrimSpace(plan.Segments[0].Topic)
		}
		return []scriptpkg.CanonicalSegment{{
			ID: id, SceneID: sceneID, Position: 0, Text: segText, SourceText: segText,
			TextHash: segmentTextHash(segText), SourceTextHash: segmentTextHash(segText),
		}}
	}
	if len(plan.Segments) > 0 {
		out := make([]scriptpkg.CanonicalSegment, 0, len(plan.Segments))
		for i, seg := range plan.Segments {
			segText := strings.TrimSpace(seg.SourceText)
			if segText == "" {
				segText = strings.TrimSpace(seg.Topic)
			}
			id := strings.TrimSpace(seg.ID)
			if id == "" {
				id = fmt.Sprintf("segment-%03d", i+1)
			}
			out = append(out, scriptpkg.CanonicalSegment{
				ID: id, Position: i, Text: segText, SourceText: segText,
				TextHash: segmentTextHash(segText), SourceTextHash: segmentTextHash(segText),
			})
		}
		return out
	}
	if len(scenes) > 0 {
		return buildCanonicalSegmentsFromScenes(scenes, text)
	}
	return buildCanonicalSegmentsFromScenes(nil, text)
}

func buildCanonicalSegmentsFromScenes(scenes []scriptpkg.SpecScene, text string) []scriptpkg.CanonicalSegment {
	if len(scenes) > 0 {
		out := make([]scriptpkg.CanonicalSegment, 0, len(scenes))
		seenIDs := make(map[string]struct{}, len(scenes))
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
			if _, exists := seenIDs[id]; exists {
				base := id
				for suffix := 1; ; suffix++ {
					candidate := fmt.Sprintf("%s-%d", base, suffix)
					if _, collision := seenIDs[candidate]; !collision {
						id = candidate
						break
					}
				}
			}
			seenIDs[id] = struct{}{}
			out = append(out, scriptpkg.CanonicalSegment{
				ID: id, SceneID: strings.TrimSpace(scene.ID), Position: i,
				Text: segText, SourceText: segText,
				TextHash: segmentTextHash(segText), SourceTextHash: segmentTextHash(segText),
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
			ID: "segment-001", Position: 0, Text: trimmed, SourceText: trimmed,
			TextHash: segmentTextHash(trimmed), SourceTextHash: segmentTextHash(trimmed),
		}}
	}
	out := make([]scriptpkg.CanonicalSegment, 0, len(parts))
	for i, part := range parts {
		out = append(out, scriptpkg.CanonicalSegment{
			ID: fmt.Sprintf("segment-%03d", i+1), Position: i,
			Text: part, SourceText: part,
			TextHash: segmentTextHash(part), SourceTextHash: segmentTextHash(part),
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
	sum := digest.SHA256Bytes([]byte(normalizeSegmentText(text)))
	return sum
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

// weightedKeywordValues projects a profile's weighted keyword stream onto the
// legacy string surface consumed by SegmentInsights and the ad-hoc query
// builders. It is the only legal way to read Keywords/VisualTerms back as a
// plain list; values keep the profile's deterministic order.
func weightedKeywordValues(keywords []scriptpkg.WeightedKeyword) []string {
	if len(keywords) == 0 {
		return nil
	}
	out := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if value := strings.TrimSpace(keyword.Value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func buildArtlistQueries(segmentText string, explicitKeywords []string, entities []scriptpkg.ExtractedEntity, phrases []string, words []string, topic string) []string {
	candidates := make([]string, 0, 12+len(explicitKeywords))
	for _, keyword := range explicitKeywords {
		if keyword = strings.TrimSpace(keyword); keyword != "" {
			candidates = append(candidates, keyword)
		}
	}
	explicitCount := len(candidates)
	if visual := compactVisualQuery(segmentText); visual != "" {
		candidates = append(candidates, compactArtlistQuery(visual))
	}
	for _, entity := range entities {
		v := strings.TrimSpace(entity.Value)
		if v == "" {
			continue
		}
		candidates = append(candidates, v)
	}
	candidates = append(candidates, phrases...)
	return uniqueLimitedStrings(append(candidates[:explicitCount], normalizeRetrievalQueries(candidates[explicitCount:], 6)...), 5)
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
	}
	candidates = append(candidates, phrases...)
	return normalizeRetrievalQueries(candidates, 8)
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
	// A very long sentence is narration, not a retrieval query. Moderately
	// sized source descriptions may still provide useful visual grounding;
	// compact those only after stop-word removal.
	if len(textutil.Tokenize(text)) > 15 {
		return ""
	}
	tokens := textutil.TokenizeWithStopWords(text, linguistics.DefaultStopWords())
	if len(tokens) > 8 {
		tokens = tokens[:8]
	}
	if len(tokens) < 2 {
		return ""
	}
	return normalizeRetrievalQuery(strings.Join(tokens, " "), 8)
}

func compactArtlistQuery(query string) string {
	tokens := textutil.TokenizeWithStopWords(query, linguistics.DefaultStopWords())
	if len(tokens) > 6 {
		tokens = tokens[len(tokens)-6:]
	}
	if len(tokens) < 2 {
		return ""
	}
	return normalizeRetrievalQuery(strings.Join(tokens, " "), 6)
}

func normalizeRetrievalQueries(candidates []string, maxWords int) []string {
	if maxWords < 2 {
		return nil
	}
	out := make([]string, 0, minInt(5, len(candidates)))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		query := normalizeRetrievalQuery(candidate, maxWords)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func normalizeRetrievalQuery(raw string, maxWords int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\n\r.!?,;:\"`") {
		return ""
	}
	lower := strings.ToLower(raw)
	for _, placeholder := range []string{
		"type", "subject", "item", "concrete keyword", "short visual concept phrase",
		"cinematic scene", "action scene", "visual scene", "visual concept",
	} {
		if lower == placeholder || strings.Contains(lower, placeholder) {
			return ""
		}
	}
	rawTokens := textutil.Tokenize(raw)
	if len(rawTokens) > maxWords {
		return ""
	}
	tokens := textutil.TokenizeWithStopWords(raw, linguistics.DefaultStopWords())
	if len(tokens) < 2 || len(tokens) > maxWords {
		return ""
	}
	for _, token := range tokens {
		if retrievalNoiseWords[token] {
			return ""
		}
	}
	return strings.Join(tokens, " ")
}

var retrievalNoiseWords = map[string]bool{
	"best": true, "understood": true, "often": true, "relentless": true,
	"endeavor": true, "endeavors": true, "march": true, "moment": true,
	"moments": true, "important": true, "importance": true, "history": true,
	"humanity": true, "tapestry": true, "manifestation": true, "manifestations": true,
	"ingenuity": true, "perhaps": true, "progress": true, "advancement": true,
}

func versionedSegmentCacheKey(stage string, version scriptports.CacheVersion, parts ...string) string {
	return segmentCacheKey(append([]string{scriptports.VersionedCacheNamespace(stage, version)}, parts...)...)
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
	out.Insights.YouTubeQueries = append([]string(nil), in.Insights.YouTubeQueries...)
	out.Insights.ImageQueries = append([]string(nil), in.Insights.ImageQueries...)
	out.Insights.ResearchSources = append([]scriptpkg.ResearchWebSource(nil), in.Insights.ResearchSources...)
	out.Insights.EntityMediaLinks = append([]scriptpkg.EntityMediaLink(nil), in.Insights.EntityMediaLinks...)
	if in.Insights.ImageEntityCanonicalIDs != nil {
		out.Insights.ImageEntityCanonicalIDs = make(map[string]string, len(in.Insights.ImageEntityCanonicalIDs))
		for key, id := range in.Insights.ImageEntityCanonicalIDs {
			out.Insights.ImageEntityCanonicalIDs[key] = id
		}
	}
	out.Assets.SecondaryImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.SecondaryImages...)
	out.Assets.GeneratedImages = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.GeneratedImages...)
	out.Assets.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), in.Assets.Candidates...)
	if in.Assets.PrimaryVideo != nil {
		primary := *in.Assets.PrimaryVideo
		out.Assets.PrimaryVideo = &primary
	}
	return out
}

// vidRushArtlistOnlyPlan identifies the strict V1 contract. Hybrid plans must
// keep Artlist best-effort so verified image or generation fallbacks can still
// complete the scene when Artlist is unavailable.
func vidRushArtlistOnlyPlan(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if plan == nil || !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		return false
	}
	return !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() &&
		!plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool()
}

// FinalizeVidRushBindings is the single binding finalization step for the
// per-segment result. It accepts only provider candidates with provenance,
// computes a stable candidate-set hash, and records binding cache state.
// Provider processors remain responsible for searching; this function only
// normalizes and selects from their closed candidate set.
func FinalizeVidRushBindings(segments []scriptpkg.VidRushSegmentResult, forceRefresh bool) []scriptpkg.VidRushSegmentResult {
	return FinalizeVidRushBindingsWithCache(context.Background(), segments, forceRefresh, nil)
}

// DeduplicateVidRushSegments collapses repeated processor deltas by the
// canonical segment id while preserving provider candidates and insights.
func DeduplicateVidRushSegments(segments []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	return mergeVidRushSegments(nil, segments)
}

// FinalizeVidRushBindingsWithCache is the canonical binding finalizer used by
// the generation use case. The in-memory map remains a fast L1 cache, while
// cache provides the durable L2 replay surface across process restarts.
// Cache failures are deliberately non-fatal: they must never turn a valid,
// already-persisted binding into a false failure or a false cache hit.
func FinalizeVidRushBindingsWithCache(ctx context.Context, segments []scriptpkg.VidRushSegmentResult, forceRefresh bool, cache scriptports.VidRushCachePort) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	segmentIndex := make(map[string]int, len(segments))
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
		} else if len(seg.Assets.SecondaryImages) > 0 {
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
	if provider == scriptpkg.VidRushProviderYouTube {
		return strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.SourceURL) != "" && candidate.SourceEndMs > candidate.SourceStartMs
	}
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
	if provider != scriptpkg.VidRushProviderYouTube {
		for _, pattern := range vidRushForbiddenURLPatterns {
			if strings.Contains(sourceURL, pattern) {
				return false
			}
		}
	}
	if provider == "artlist" {
		return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.DriveLink) != ""
	}
	return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}

func readyVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if candidate.Provider == scriptpkg.VidRushProviderYouTube &&
		strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.DriveLink) != "" &&
		strings.TrimSpace(candidate.SourceURL) != "" && candidate.SourceEndMs > candidate.SourceStartMs &&
		(strings.TrimSpace(candidate.RightsStatus) == "" || strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "verified")) &&
		candidate.AcquisitionStatus == scriptpkg.VidRushStatusAcquired &&
		candidate.VerificationStatus == scriptpkg.VidRushStatusVerified &&
		candidate.PersistenceStatus == scriptpkg.VidRushStatusPersisted &&
		(candidate.IndexStatus == scriptpkg.VidRushStatusIndexed || candidate.IndexStatus == "pending" || candidate.IndexStatus == "discovered" || candidate.IndexStatus == "indexing_skipped_no_indexer") {
		return true
	}
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
	// Internet image retrieval records unknown_allowed when the source page
	// does not expose a machine-verifiable license. That is still sufficient
	// for the technical image-retrieval contract (download, Drive persistence,
	// SQLite state and Qdrant projection); only an explicit rejection must
	// block this image-only fallback. Video and generated-image providers keep
	// the stricter ReadyForBinding rights requirement below.
	if candidate.Provider == scriptpkg.VidRushProviderYouTube &&
		strings.TrimSpace(candidate.AssetID) != "" &&
		strings.TrimSpace(candidate.DriveLink) != "" &&
		(strings.TrimSpace(candidate.RightsStatus) == "" || strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "verified")) &&
		(candidate.AcquisitionStatus == "acquired" || candidate.AcquisitionStatus == scriptpkg.VidRushStatusAcquired) &&
		(candidate.VerificationStatus == "verified" || candidate.VerificationStatus == scriptpkg.VidRushStatusVerified) &&
		(candidate.PersistenceStatus == "persisted" || candidate.PersistenceStatus == scriptpkg.VidRushStatusPersisted) &&
		(candidate.IndexStatus == "indexed" || candidate.IndexStatus == "pending" || candidate.IndexStatus == "discovered" || candidate.IndexStatus == "indexing_skipped_no_indexer") {
		return true
	}
	if candidate.Provider == scriptpkg.VidRushProviderInternetImages &&
		strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "unknown_allowed") &&
		candidate.AcquisitionStatus == scriptpkg.VidRushStatusAcquired &&
		candidate.VerificationStatus == scriptpkg.VidRushStatusVerified &&
		candidate.PersistenceStatus == scriptpkg.VidRushStatusPersisted &&
		(strings.EqualFold(candidate.IndexStatus, "indexed") ||
			strings.EqualFold(candidate.IndexStatus, "pending") ||
			strings.EqualFold(candidate.IndexStatus, "discovered") ||
			strings.EqualFold(candidate.IndexStatus, "indexing_skipped_no_indexer")) &&
		strings.TrimSpace(candidate.LegacyFileMD5) != "" &&
		strings.TrimSpace(candidate.DriveLink) != "" {
		return true
	}
	if candidate.Provider == scriptpkg.VidRushProviderYouTube &&
		strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.DriveLink) != "" &&
		strings.TrimSpace(candidate.SourceURL) != "" && candidate.SourceEndMs > candidate.SourceStartMs &&
		(strings.TrimSpace(candidate.RightsStatus) == "" || strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "verified")) &&
		(candidate.AcquisitionStatus == "acquired" || candidate.AcquisitionStatus == scriptpkg.VidRushStatusAcquired) &&
		(candidate.VerificationStatus == "verified" || candidate.VerificationStatus == scriptpkg.VidRushStatusVerified) &&
		(candidate.PersistenceStatus == "persisted" || candidate.PersistenceStatus == scriptpkg.VidRushStatusPersisted) &&
		(candidate.IndexStatus == "indexed" || candidate.IndexStatus == "pending" || candidate.IndexStatus == "discovered" || candidate.IndexStatus == "indexing_skipped_no_indexer") {
		return true
	}
	if candidate.Provider == scriptpkg.VidRushProviderYouTube &&
		candidate.ReadyForBinding() && strings.TrimSpace(candidate.DriveLink) != "" {
		return true
	}
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.LegacyFileMD5) != "" && strings.TrimSpace(candidate.DriveLink) != ""
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
	return scoreVidRushCandidateWithProfile(candidate, scriptpkg.SegmentSemanticProfile{}, repeated)
}

func scoreVidRushCandidateWithProfile(candidate scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile, repeated bool) float64 {
	if candidate.RelevanceScore == 0 && candidate.TechnicalQualityScore == 0 && candidate.RightsScore == 0 && candidate.DiversityScore == 0 && candidate.ProviderReliability == 0 && candidate.SemanticScore == 0 && len(profile.Keywords) == 0 && len(profile.VisualTerms) == 0 && len(profile.Entities) == 0 && strings.TrimSpace(profile.Topic) == "" {
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
	semantic := clamp(candidate.SemanticScore)
	if semantic == 0 {
		semantic = profileSemanticMatch(candidate, profile)
	}
	relevance := clamp(candidate.RelevanceScore)
	if relevance == 0 {
		relevance = semantic
	}
	score := w.Relevance*relevance +
		w.TechnicalQuality*clamp(candidate.TechnicalQualityScore) +
		w.Rights*clamp(candidate.RightsScore) +
		w.Diversity*clamp(candidate.DiversityScore) +
		w.ProviderReliability*clamp(candidate.ProviderReliability) + 0.10*semantic
	if repeated {
		score *= 0.75
	}
	return score
}

func compareVidRushPrimaryCandidates(a, b scriptpkg.SegmentAssetCandidate) int {
	aScore := ScoreVidRushCandidate(a, false)
	bScore := ScoreVidRushCandidate(b, false)
	if aScore > bScore {
		return 1
	}
	if aScore < bScore {
		return -1
	}
	// Stable deterministic tie-breakers: provider, then asset identity.
	if strings.ToLower(a.Provider) < strings.ToLower(b.Provider) {
		return 1
	}
	if strings.ToLower(a.Provider) > strings.ToLower(b.Provider) {
		return -1
	}
	if strings.ToLower(a.AssetID) < strings.ToLower(b.AssetID) {
		return 1
	}
	if strings.ToLower(a.AssetID) > strings.ToLower(b.AssetID) {
		return -1
	}
	return 0
}

func chooseVidRushPrimaryWithProfile(candidates []scriptpkg.SegmentAssetCandidate, previous map[string]string, profile scriptpkg.SegmentSemanticProfile) *scriptpkg.SegmentAssetCandidate {
	var best *scriptpkg.SegmentAssetCandidate
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Provider != scriptpkg.VidRushProviderArtlist && candidate.Provider != scriptpkg.VidRushProviderYouTube {
			continue
		}
		repeated := previous[candidate.Provider] == candidate.AssetID
		if repeated && len(candidates) > 1 {
			continue
		}
		candidate.Score = scoreVidRushCandidateWithProfile(candidate, profile, repeated)
		if best == nil || compareVidRushPrimaryCandidates(candidate, *best) > 0 {
			selected := candidate
			best = &selected
		}
	}
	return best
}

func chooseVidRushPrimary(candidates []scriptpkg.SegmentAssetCandidate, previous map[string]string) *scriptpkg.SegmentAssetCandidate {
	return chooseVidRushPrimaryWithProfile(candidates, previous, scriptpkg.SegmentSemanticProfile{})
}

func profileSemanticMatch(candidate scriptpkg.SegmentAssetCandidate, profile scriptpkg.SegmentSemanticProfile) float64 {
	if len(profile.Keywords) == 0 && len(profile.VisualTerms) == 0 && len(profile.Entities) == 0 && strings.TrimSpace(profile.Topic) == "" {
		return 0
	}
	text := strings.ToLower(strings.Join([]string{candidate.Query, candidate.Entity, candidate.SourceURL}, " "))
	terms := []string{profile.Topic}
	for _, keyword := range profile.Keywords {
		terms = append(terms, keyword.Value)
	}
	for _, term := range profile.VisualTerms {
		terms = append(terms, term.Value)
	}
	for _, entity := range profile.Entities {
		terms = append(terms, entity.Value)
	}
	matched := 0
	for _, term := range terms {
		if value := strings.TrimSpace(term); value != "" && strings.Contains(text, strings.ToLower(value)) {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	return float64(matched) / float64(len(terms))
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
			candidate.PreviewURL, candidate.LegacyFileMD5, candidate.DriveLink,
			candidate.AcquisitionStatus, candidate.VerificationStatus,
			candidate.PersistenceStatus, candidate.IndexStatus,
		}, "\x00"))
	}
	return segmentCacheKey(parts...)
}
