package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

var (
	vidrushExtractionCache sync.Map
	vidrushArtlistCache    sync.Map
	vidrushImageCache      sync.Map
	vidrushBindingCache    sync.Map
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
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	lastAssetByProvider := make(map[string]string)
	for _, original := range segments {
		seg := cloneVidRushSegmentResult(original)
		valid := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.Candidates))
		seen := make(map[string]struct{}, len(seg.Assets.Candidates))
		for _, candidate := range seg.Assets.Candidates {
			if !validVidRushCandidate(candidate) {
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
			if _, ok := cacheLoad(&vidrushBindingCache, bindingKey); ok {
				seg.Cache.Binding = "HIT_EXACT"
			} else {
				seg.Cache.Binding = "MISS"
				cacheStore(&vidrushBindingCache, bindingKey, true)
			}
		} else {
			seg.Cache.Binding = "REFRESHED"
			cacheStore(&vidrushBindingCache, bindingKey, true)
		}
		out = append(out, seg)
	}
	return out
}

func validVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.Provider) == "" || candidate.Score < 0 {
		return false
	}
	if strings.TrimSpace(candidate.Provider) == "artlist" {
		return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.DriveLink) != ""
	}
	return strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}

func chooseVidRushPrimary(candidates []scriptpkg.SegmentAssetCandidate, previous map[string]string) *scriptpkg.SegmentAssetCandidate {
	var best *scriptpkg.SegmentAssetCandidate
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Provider != "artlist" {
			continue
		}
		if previous[candidate.Provider] == candidate.AssetID && len(candidates) > 1 {
			continue
		}
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
		if candidate.Provider != "artlist" {
			out = append(out, candidate)
		}
	}
	return out
}

func candidateSetHash(candidates []scriptpkg.SegmentAssetCandidate) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, strings.Join([]string{candidate.AssetID, candidate.Provider, candidate.Query, candidate.SourceURL, candidate.PreviewURL}, "\x00"))
	}
	return segmentCacheKey(parts...)
}
