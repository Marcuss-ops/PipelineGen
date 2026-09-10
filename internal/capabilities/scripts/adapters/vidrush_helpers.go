package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	vidrushArtlistCache      sync.Map
	vidrushImageCache        sync.Map
	vidrushBindingCache      sync.Map
	vidrushMaterializedCache sync.Map
)

// ── L1 cache janitor ───────────────────────────────────────────────────
// The VidRush L1 maps are no-TTL by contract (a warm replay of the same
// query must hit without re-calling the provider), but they must not grow
// without bound over the process lifetime. A periodic janitor clears them;
// the durable L2 cache (VidRushCachePort, TTL 48h) re-warms L1 on the next
// replay with identical HIT_EXACT semantics, so the janitor only bounds
// memory, never changes results.
const vidrushL1CacheJanitorInterval = 10 * time.Minute

var (
	vidrushCacheJanitorOnce sync.Once
	// vidrushL1Caches is the bounded L1 surface. entityImageCache is declared
	// in media_resolver_image_stage.go (same package) and participates in the
	// same janitor; entityImageLocks is excluded because the canonical
	// KeyedLocker is reference-counted and self-cleaning.
	vidrushL1Caches = []*sync.Map{
		&vidrushArtlistCache,
		&vidrushImageCache,
		&vidrushBindingCache,
		&vidrushMaterializedCache,
		&entityImageCache,
	}
)

// startVidrushCacheJanitor launches the periodic L1 clear exactly once per
// process. It is started lazily on the first L1 access so unit tests that
// never touch the caches stay free of background goroutines.
func startVidrushCacheJanitor() {
	vidrushCacheJanitorOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(vidrushL1CacheJanitorInterval)
			defer ticker.Stop()
			for range ticker.C {
				for _, cache := range vidrushL1Caches {
					cache.Range(func(key, _ any) bool {
						cache.Delete(key)
						return true
					})
				}
			}
		}()
	})
}

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
			ExecutionMode: sceneExecutionModeFor(scenes, id, 0),
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
			sceneID := explicitSegmentSceneID(scenes, id, i)
			out = append(out, scriptpkg.CanonicalSegment{
				ID: id, SceneID: sceneID, Position: i, Text: segText, SourceText: segText,
				TextHash: segmentTextHash(segText), SourceTextHash: segmentTextHash(segText),
				ExecutionMode: sceneExecutionModeFor(scenes, id, i),
			})
		}
		return out
	}
	if len(scenes) > 0 {
		return buildCanonicalSegmentsFromScenes(scenes, text)
	}
	return buildCanonicalSegmentsFromScenes(nil, text)
}

func explicitSegmentSceneID(scenes []scriptpkg.SpecScene, segmentID string, position int) string {
	for _, scene := range scenes {
		if strings.TrimSpace(scene.SegmentID) == strings.TrimSpace(segmentID) {
			return strings.TrimSpace(scene.ID)
		}
	}
	for _, scene := range scenes {
		if strings.TrimSpace(scene.ID) == strings.TrimSpace(segmentID) {
			return strings.TrimSpace(scene.ID)
		}
	}
	if position >= 0 && position < len(scenes) {
		return strings.TrimSpace(scenes[position].ID)
	}
	return ""
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
				ExecutionMode: scene.ExecutionMode.Normalize(),
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
	startVidrushCacheJanitor()
	if cache == nil || key == "" {
		return nil, false
	}
	return cache.Load(key)
}

func cacheStore(cache *sync.Map, key string, value any) {
	startVidrushCacheJanitor()
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
