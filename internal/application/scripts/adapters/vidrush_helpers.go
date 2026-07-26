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
	if topic = strings.TrimSpace(topic); topic != "" {
		candidates = append(candidates, topic+" cinematic scene")
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
