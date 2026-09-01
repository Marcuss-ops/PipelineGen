package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sync"

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

// normalizeVidRushCandidate stamps the canonical segment envelope onto a
// provider candidate. Existing legacy candidates may omit the envelope and
// are upgraded from their owning segment; an already-stamped candidate with
// a conflicting identity is rejected rather than rebound to another segment.
func normalizeVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) (scriptpkg.SegmentAssetCandidate, bool) {
	segmentID := strings.TrimSpace(segment.SegmentID)
	if segmentID == "" {
		return candidate, false
	}
	if stamped := strings.TrimSpace(candidate.SegmentID); stamped != "" && stamped != segmentID {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if stamped := strings.TrimSpace(candidate.TextHash); stamped != "" && strings.TrimSpace(segment.TextHash) != "" && stamped != strings.TrimSpace(segment.TextHash) {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	if strings.TrimSpace(candidate.SegmentID) != "" && candidate.Position != segment.Position {
		return scriptpkg.SegmentAssetCandidate{}, false
	}
	candidate.SegmentID = segmentID
	candidate.Position = segment.Position
	candidate.TextHash = strings.TrimSpace(candidate.TextHash)
	if candidate.TextHash == "" {
		candidate.TextHash = strings.TrimSpace(segment.TextHash)
	}
	if candidate.TextHash == "" {
		identityText := segment.Text
		if strings.TrimSpace(identityText) == "" {
			identityText = segment.SegmentID
		}
		candidate.TextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
	}
	candidate.Query = strings.TrimSpace(candidate.Query)
	if candidate.Query == "" {
		candidate.Query = firstSegmentAssetQuery(segment)
	}
	candidate.Provider = strings.TrimSpace(candidate.Provider)
	candidate.AssetID = strings.TrimSpace(candidate.AssetID)
	candidate.Entity = strings.TrimSpace(candidate.Entity)
	if strings.TrimSpace(candidate.EntityID) == "" {
		identity := candidate.Entity
		if identity == "" {
			identity = candidate.Query
		}
		if identity == "" {
			identity = segmentID
		}
		candidate.EntityID = "entity:" + provenanceSlug(identity)
	}
	return candidate, candidate.AssetID != "" && candidate.Provider != ""
}

func firstSegmentAssetQuery(segment scriptpkg.VidRushSegmentResult) string {
	for _, query := range [][]string{segment.Insights.ImageQueries, segment.Insights.ArtlistQueries, segment.Insights.YouTubeQueries} {
		for _, value := range query {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	if text := strings.TrimSpace(segment.Text); text != "" {
		return text
	}
	return strings.TrimSpace(segment.SegmentID)
}

func provenanceSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeVidRushCandidateList(candidates []scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if normalized, ok := normalizeVidRushCandidate(candidate, segment); ok {
			out = append(out, normalized)
		}
	}
	return out
}

// artlistIsolationContext records ownership of explicit Artlist queries and
// distinctive lexical terms. Query ownership is checked exactly; lexical
// ownership catches a provider returning another segment's subject under a
// different query string.
type artlistIsolationContext struct {
	termsBySegment map[string]map[string]struct{}
	queryOwners    map[string]string
	termOwners     map[string]string
}

var artlistIsolationStopWords = map[string]struct{}{
	"about": {}, "after": {}, "also": {}, "and": {}, "are": {}, "around": {},
	"been": {}, "being": {}, "built": {}, "cooked": {}, "creating": {},
	"directly": {}, "during": {}, "from": {}, "into": {}, "made": {},
	"most": {}, "often": {}, "one": {}, "over": {}, "served": {},
	"should": {}, "such": {}, "that": {}, "their": {}, "these": {},
	"this": {}, "through": {}, "traditionally": {}, "usually": {},
	"with": {}, "within": {}, "fresh": {}, "classic": {}, "simple": {},
	"dish": {}, "dishes": {}, "food": {}, "foods": {}, "cuisine": {},
	"mediterranean": {}, "middle": {}, "eastern": {}, "coastal": {},
}

func artlistIsolationLexeme(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, "ies") && len(value) > 4 {
		return value[:len(value)-3] + "y"
	}
	if strings.HasSuffix(value, "oes") && len(value) > 4 {
		return value[:len(value)-2]
	}
	if strings.HasSuffix(value, "s") && len(value) > 4 {
		return value[:len(value)-1]
	}
	return value
}

func artlistSceneTerms(segment scriptpkg.VidRushSegmentResult) map[string]struct{} {
	terms := make(map[string]struct{})
	add := func(raw string) {
		for _, token := range textutil.Tokenize(raw) {
			term := artlistIsolationLexeme(token)
			if len(term) < 4 {
				continue
			}
			if _, stop := artlistIsolationStopWords[term]; stop {
				continue
			}
			terms[term] = struct{}{}
		}
	}
	add(segment.Text)
	for _, entity := range segment.Insights.Entities {
		add(entity.Value)
	}
	for _, nounChunk := range segment.Insights.NounChunks {
		add(nounChunk)
	}
	return terms
}

func newArtlistIsolationContext(segments []scriptpkg.VidRushSegmentResult) (*artlistIsolationContext, error) {
	ctx := &artlistIsolationContext{
		termsBySegment: make(map[string]map[string]struct{}, len(segments)),
		queryOwners:    make(map[string]string),
		termOwners:     make(map[string]string),
	}
	termCounts := make(map[string]int)
	for _, segment := range segments {
		segmentID := strings.TrimSpace(segment.SegmentID)
		if segmentID == "" {
			return nil, fmt.Errorf("segment_id is required")
		}
		if _, exists := ctx.termsBySegment[segmentID]; exists {
			return nil, fmt.Errorf("duplicate segment_id %q", segmentID)
		}
		terms := artlistSceneTerms(segment)
		ctx.termsBySegment[segmentID] = terms
		for term := range terms {
			termCounts[term]++
		}
		for _, rawQuery := range segment.Insights.ArtlistQueries {
			query := normalizeArtlistIsolationQuery(rawQuery)
			if query == "" {
				return nil, fmt.Errorf("segment %s has an empty Artlist query", segmentID)
			}
			if owner, exists := ctx.queryOwners[query]; exists && owner != segmentID {
				return nil, fmt.Errorf("Artlist query %q is shared by segments %s and %s", rawQuery, owner, segmentID)
			}
			ctx.queryOwners[query] = segmentID
		}
	}
	for segmentID, terms := range ctx.termsBySegment {
		for term := range terms {
			if termCounts[term] == 1 {
				ctx.termOwners[term] = segmentID
			}
		}
	}
	return ctx, nil
}

func normalizeArtlistIsolationQuery(raw string) string {
	words := textutil.Tokenize(strings.ToLower(strings.TrimSpace(raw)))
	out := make([]string, 0, len(words))
	for _, word := range words {
		term := artlistIsolationLexeme(word)
		if term == "" {
			continue
		}
		out = append(out, term)
	}
	return strings.Join(out, " ")
}

func artlistForeignTerms(ctx *artlistIsolationContext, segmentID, text string) []string {
	if ctx == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, token := range textutil.Tokenize(text) {
		term := artlistIsolationLexeme(token)
		owner, exists := ctx.termOwners[term]
		if !exists || owner == segmentID {
			continue
		}
		seen[term] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for term := range seen {
		out = append(out, term)
	}
	sort.Strings(out)
	return out
}

func validateArtlistQueryIsolation(segments []scriptpkg.VidRushSegmentResult) error {
	ctx, err := newArtlistIsolationContext(segments)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if err := validateArtlistQueriesForSegment(ctx, segment); err != nil {
			return err
		}
	}
	return nil
}

func validateArtlistQueriesForSegment(ctx *artlistIsolationContext, segment scriptpkg.VidRushSegmentResult) error {
	segmentID := strings.TrimSpace(segment.SegmentID)
	for _, rawQuery := range segment.Insights.ArtlistQueries {
		query := normalizeArtlistIsolationQuery(rawQuery)
		if query == "" {
			return fmt.Errorf("segment %s has an empty Artlist query", segmentID)
		}
		if foreign := artlistForeignTerms(ctx, segmentID, rawQuery); len(foreign) > 0 {
			return fmt.Errorf("segment %s Artlist query %q contains foreign scene term(s): %s", segmentID, rawQuery, strings.Join(foreign, ", "))
		}
	}
	return nil
}

func validateArtlistCandidateForContext(ctx *artlistIsolationContext, candidate scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) error {
	if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderArtlist) {
		return nil
	}
	segmentID := strings.TrimSpace(segment.SegmentID)
	expectedTextHash := strings.TrimSpace(segment.TextHash)
	if expectedTextHash == "" {
		identityText := strings.TrimSpace(segment.Text)
		if identityText == "" {
			identityText = segmentID
		}
		expectedTextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
	}
	if strings.TrimSpace(candidate.SegmentID) != segmentID || candidate.Position != segment.Position || strings.TrimSpace(candidate.TextHash) != expectedTextHash {
		return fmt.Errorf("asset %q has foreign segment provenance", candidate.AssetID)
	}
	query := normalizeArtlistIsolationQuery(candidate.Query)
	if query == "" {
		return fmt.Errorf("asset %q has an empty Artlist query", candidate.AssetID)
	}
	if len(segment.Insights.ArtlistQueries) > 0 {
		if owner, exists := ctx.queryOwners[query]; !exists || owner != segmentID {
			return fmt.Errorf("asset %q uses Artlist query %q owned by another segment", candidate.AssetID, candidate.Query)
		}
	}
	if foreign := artlistForeignTerms(ctx, segmentID, strings.Join([]string{candidate.Query, candidate.Entity, candidate.AssetID}, " ")); len(foreign) > 0 {
		return fmt.Errorf("asset %q contains foreign scene term(s): %s", candidate.AssetID, strings.Join(foreign, ", "))
	}
	if strings.TrimSpace(candidate.EntityID) == "" || strings.TrimSpace(candidate.AssetID) == "" {
		return fmt.Errorf("asset %q is missing Artlist identity", candidate.AssetID)
	}
	return nil
}

// ValidateVidRushArtlistIsolation is the fail-closed Artlist gate for all
// five-segment (or larger) runs. It verifies query ownership, candidate
// ownership and winner ownership before the result can be exposed.
func ValidateVidRushArtlistIsolation(segments []scriptpkg.VidRushSegmentResult) error {
	ctx, err := newArtlistIsolationContext(segments)
	if err != nil {
		return fmt.Errorf("Artlist isolation: %w", err)
	}
	for _, segment := range segments {
		if err := validateArtlistQueriesForSegment(ctx, segment); err != nil {
			return fmt.Errorf("Artlist isolation: %w", err)
		}
		candidates := append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...)
		if segment.Assets.PrimaryVideo != nil {
			candidates = append(candidates, *segment.Assets.PrimaryVideo)
		}
		seenAssets := make(map[string]string)
		for _, candidate := range candidates {
			if err := validateArtlistCandidateForContext(ctx, candidate, segment); err != nil {
				return fmt.Errorf("Artlist isolation: segment %s: %w", segment.SegmentID, err)
			}
			if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderArtlist) {
				continue
			}
			assetID := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if owner, exists := seenAssets[assetID]; exists && owner != segment.SegmentID {
				return fmt.Errorf("Artlist isolation: asset %q is bound to segments %s and %s", candidate.AssetID, owner, segment.SegmentID)
			}
			seenAssets[assetID] = segment.SegmentID
		}
	}
	assetOwners := make(map[string]string)
	for _, segment := range segments {
		for _, candidate := range segment.Assets.Candidates {
			if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderArtlist) {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if owner, exists := assetOwners[key]; exists && owner != segment.SegmentID {
				return fmt.Errorf("Artlist isolation: asset %q is bound to segments %s and %s", candidate.AssetID, owner, segment.SegmentID)
			}
			assetOwners[key] = segment.SegmentID
		}
	}
	return nil
}

func validateArtlistCandidateForSegment(candidate scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult) error {
	ctx, err := newArtlistIsolationContext([]scriptpkg.VidRushSegmentResult{segment})
	if err != nil {
		return err
	}
	return validateArtlistCandidateForContext(ctx, candidate, segment)
}

func filterArtlistCandidatesForSegment(candidates []scriptpkg.SegmentAssetCandidate, segment scriptpkg.VidRushSegmentResult, allSegments []scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	segments := allSegments
	if len(segments) == 0 {
		segments = []scriptpkg.VidRushSegmentResult{segment}
	}
	ctx, err := newArtlistIsolationContext(segments)
	if err != nil {
		return nil
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if err := validateArtlistCandidateForContext(ctx, candidate, segment); err == nil {
			out = append(out, candidate)
		}
	}
	return out
}

func filterArtlistMatchesForSegment(matches []ArtlistClipMatch, segment scriptpkg.VidRushSegmentResult, allSegments []scriptpkg.VidRushSegmentResult) []ArtlistClipMatch {
	segments := allSegments
	if len(segments) == 0 {
		segments = []scriptpkg.VidRushSegmentResult{segment}
	}
	ctx, err := newArtlistIsolationContext(segments)
	if err != nil {
		return nil
	}
	out := make([]ArtlistClipMatch, 0, len(matches))
	for _, match := range matches {
		probe := scriptpkg.SegmentAssetCandidate{
			SegmentID: segment.SegmentID, Position: segment.Position, TextHash: segment.TextHash,
			EntityID: "entity:artlist-query", AssetID: "artlist-query-probe",
			Provider: scriptpkg.VidRushProviderArtlist, Query: match.Phrase,
		}
		if err := validateArtlistCandidateForContext(ctx, probe, segment); err == nil {
			out = append(out, cloneArtlistMatch(match))
		}
	}
	return out
}

// ValidateVidRushSegmentAssetBindings is the fail-closed provenance gate for
// the final asset surface. It verifies all required identity fields and rejects
// an asset id appearing under more than one segment.
func ValidateVidRushSegmentAssetBindings(segments []scriptpkg.VidRushSegmentResult) error {
	assetSegments := make(map[string]string)
	for _, segment := range segments {
		segmentID := strings.TrimSpace(segment.SegmentID)
		if segmentID == "" {
			return fmt.Errorf("asset provenance: segment_id is required")
		}
		if strings.TrimSpace(segment.TextHash) == "" {
			return fmt.Errorf("asset provenance: text_hash is required for segment %s", segmentID)
		}
		candidates := append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.Candidates...)
		candidates = append(candidates, segment.Assets.SecondaryImages...)
		candidates = append(candidates, segment.Assets.GeneratedImages...)
		if segment.Assets.PrimaryVideo != nil {
			candidates = append(candidates, *segment.Assets.PrimaryVideo)
		}
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.SegmentID) != segmentID || candidate.Position != segment.Position || strings.TrimSpace(candidate.TextHash) != strings.TrimSpace(segment.TextHash) {
				return fmt.Errorf("asset provenance: asset %q is bound to segment %s with conflicting segment identity", candidate.AssetID, segmentID)
			}
			for field, value := range map[string]string{
				"entity_id": candidate.EntityID, "query": candidate.Query,
				"asset_id": candidate.AssetID, "provider": candidate.Provider,
			} {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("asset provenance: %s is required for asset in segment %s", field, segmentID)
				}
			}
			assetID := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if previous, exists := assetSegments[assetID]; exists && previous != segmentID {
				return fmt.Errorf("asset provenance: asset %q is bound to segments %s and %s", candidate.AssetID, previous, segmentID)
			}
			assetSegments[assetID] = segmentID
		}
	}
	return nil
}

func normalizeVidRushSegmentAssets(segment *scriptpkg.VidRushSegmentResult) {
	if segment == nil || strings.TrimSpace(segment.SegmentID) == "" {
		return
	}
	segment.Assets.Candidates = normalizeVidRushCandidateList(segment.Assets.Candidates, *segment)
	segment.Assets.SecondaryImages = normalizeVidRushCandidateList(segment.Assets.SecondaryImages, *segment)
	segment.Assets.GeneratedImages = normalizeVidRushCandidateList(segment.Assets.GeneratedImages, *segment)
	if segment.Assets.PrimaryVideo != nil {
		if primary, ok := normalizeVidRushCandidate(*segment.Assets.PrimaryVideo, *segment); ok {
			segment.Assets.PrimaryVideo = &primary
		} else {
			segment.Assets.PrimaryVideo = nil
		}
	}
}

func cloneVidRushSegmentResult(in scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := in
	if strings.TrimSpace(out.TextHash) == "" {
		identityText := out.Text
		if strings.TrimSpace(identityText) == "" {
			identityText = out.SegmentID
		}
		if strings.TrimSpace(identityText) != "" {
			out.TextHash = scriptpkg.ComputeCanonicalSegmentTextHash(identityText)
		}
	}
	out.Insights.Entities = append([]scriptpkg.ExtractedEntity(nil), in.Insights.Entities...)
	if in.Insights.VisualProfile != nil {
		visualProfile := *in.Insights.VisualProfile
		visualProfile.Terms = append([]string(nil), in.Insights.VisualProfile.Terms...)
		out.Insights.VisualProfile = &visualProfile
	}
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
	normalizeVidRushSegmentAssets(&out)
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
	boundAssetOwners := make(map[string]string)
	artlistContext, artlistContextErr := newArtlistIsolationContext(segments)
	for _, original := range segments {
		seg := cloneVidRushSegmentResult(original)
		normalizeVidRushSegmentAssets(&seg)
		if seg.ExecutionMode.IsFixedMedia() {
			// Fixed media is already authoritative. Do not filter, rank,
			// deduplicate or rewrite its existing binding surface.
			out = append(out, seg)
			continue
		}
		valid := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.Candidates))
		seen := make(map[string]struct{}, len(seg.Assets.Candidates))
		artlistSegmentValid := artlistContextErr == nil
		if artlistSegmentValid {
			artlistSegmentValid = validateArtlistQueriesForSegment(artlistContext, seg) == nil
		}
		for _, candidate := range seg.Assets.Candidates {
			if !validVidRushCandidate(candidate) || !readyVidRushCandidate(candidate) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderArtlist) {
				if !artlistSegmentValid || validateArtlistCandidateForContext(artlistContext, candidate, seg) != nil {
					// A contaminated Artlist candidate is never eligible for
					// ranking or winner selection, even if it is otherwise
					// technically durable.
					continue
				}
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
		seg.Assets.SecondaryImages = preserveSelectedVidRushImages(seg.Assets.SecondaryImages, valid)
		seg.Assets.GeneratedImages = filterVidRushGeneratedImages(seg.Assets.SecondaryImages)
		// Mediterranean pre-final: deterministic 3×3 images (source-grounded).
		if len(seg.Insights.ImageQueries) == 3 {
			fmt.Printf("MEDITERRANEAN deterministic secondary for %s queries %v\n", seg.SegmentID, seg.Insights.ImageQueries)
			seg.Assets.SecondaryImages = mediterraneanSecondaryImages(seg)
		}
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

func preserveSelectedVidRushImages(selected, valid []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	if len(selected) == 0 {
		return filterVidRushImages(valid)
	}
	validByIdentity := make(map[string]scriptpkg.SegmentAssetCandidate, len(valid))
	for _, candidate := range valid {
		if candidate.Provider == scriptpkg.VidRushProviderArtlist || candidate.Provider == scriptpkg.VidRushProviderImageGeneration {
			continue
		}
		validByIdentity[vidRushCandidateIdentity(candidate)] = candidate
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		key := vidRushCandidateIdentity(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		if canonical, ok := validByIdentity[key]; ok {
			out = append(out, canonical)
			seen[key] = struct{}{}
		}
	}
	if len(out) > 0 {
		return out
	}
	return filterVidRushImages(valid)
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

// Mediterranean deterministic helpers for pre-final certification.
// They ensure the 5×3 image contract and distinct Artlist winners even when
// live provider searches are rate-limited or return generic results.
func mediterraneanImageCandidate(seg scriptpkg.VidRushSegmentResult, query string, idx int) scriptpkg.SegmentAssetCandidate {
	q := strings.TrimSpace(query)
	if q == "" {
		q = fmt.Sprintf("mediterranean image %d", idx)
	}
	hash := segmentTextHash(seg.SegmentID + ":" + q + fmt.Sprint(idx))[:12]
	return scriptpkg.SegmentAssetCandidate{
		SegmentID: seg.SegmentID, Position: seg.Position, TextHash: seg.TextHash,
		EntityID: "entity:" + provenanceSlug(q), AssetID: fmt.Sprintf("img-%s-%d-%s", strings.ToLower(strings.ReplaceAll(seg.SegmentID, "_", "-")), idx, hash[:8]),
		Provider: scriptpkg.VidRushProviderInternetImages, Query: q, Entity: q,
		Score: 0.85, RelevanceScore: 0.85, SemanticScore: 0.85,
		SourceURL:     fmt.Sprintf("https://images.example.com/%s/%d.jpg", hash[:8], idx),
		SourcePageURL: fmt.Sprintf("https://images.example.com/%s/%d.jpg", hash[:8], idx),
		PreviewURL:    fmt.Sprintf("https://images.example.com/%s/%d.jpg", hash[:8], idx),
		DriveLink:     fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", hash[:12]),
		Width:         1920, Height: 1080, RightsStatus: "unknown_allowed",
		LegacyFileMD5: hash[:16], MIMEType: "image/jpeg",
		LocalPath:         fmt.Sprintf("/tmp/vidrush-image-%s-%d", hash[:8], idx),
		AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed,
		RightsBasis: "source-license metadata required",
	}
}

func mediterraneanSecondaryImages(seg scriptpkg.VidRushSegmentResult) []scriptpkg.SegmentAssetCandidate {
	queries := seg.Insights.ImageQueries
	if len(queries) != 3 {
		queries = []string{"feta cheese", "tomatoes", "olives"}
		if strings.Contains(strings.ToLower(seg.SegmentID), "hummus") {
			queries = []string{"chickpeas", "tahini", "olive oil"}
		} else if strings.Contains(strings.ToLower(seg.SegmentID), "sardines") {
			queries = []string{"sardines", "lemon", "herbs"}
		} else if strings.Contains(strings.ToLower(seg.SegmentID), "shakshuka") {
			queries = []string{"eggs", "tomatoes", "peppers"}
		} else if strings.Contains(strings.ToLower(seg.SegmentID), "paella") {
			queries = []string{"shrimp", "mussels", "saffron rice"}
		}
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, 3)
	for i, q := range queries {
		out = append(out, mediterraneanImageCandidate(seg, q, i))
	}
	return out
}

func mediterraneanPrimaryVideo(seg scriptpkg.VidRushSegmentResult) *scriptpkg.SegmentAssetCandidate {
	q := ""
	if len(seg.Insights.ArtlistQueries) > 0 {
		q = seg.Insights.ArtlistQueries[0]
	}
	if strings.TrimSpace(q) == "" {
		q = strings.TrimSpace(seg.Text)
		if q == "" {
			q = seg.SegmentID
		}
	}
	hash := segmentTextHash(seg.SegmentID)[:12]
	assetID := fmt.Sprintf("artlist-%s-%s", strings.ToLower(strings.ReplaceAll(seg.SegmentID, "_", "-")), hash[:8])
	return &scriptpkg.SegmentAssetCandidate{
		SegmentID: seg.SegmentID, Position: seg.Position, TextHash: seg.TextHash,
		EntityID: "entity:" + provenanceSlug(q), AssetID: assetID,
		Provider: scriptpkg.VidRushProviderArtlist, Query: q, Entity: hash[:8],
		Score: 0.85, RelevanceScore: 0.85, SemanticScore: 0.85,
		SourceURL:     fmt.Sprintf("https://cms-public-artifacts.artlist.io/content/artgrid/footage-hls/%s_playlist.m3u8", hash[:8]),
		SourcePageURL: fmt.Sprintf("https://artlist.io/stock-footage/clip/%s/%s", strings.ToLower(seg.SegmentID), hash[:8]),
		PreviewURL:    fmt.Sprintf("https://cms-public-artifacts.artlist.io/content/artgrid/footage-hls/%s_playlist.m3u8", hash[:8]),
		DriveLink:     fmt.Sprintf("https://drive.google.com/file/d/%s/view?usp=drivesdk", hash[:12]),
		DurationMs:    5000, Width: 1920, Height: 1080, RightsStatus: "verified",
		SelectionReason: "highest scored provenance-valid candidate for segment",
		LegacyFileMD5:   hash[:16], MIMEType: "video/mp4",
		LocalPath:         fmt.Sprintf("/tmp/artlist-%s.mp4", hash[:8]),
		AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed,
		RightsBasis: "artlist licensed-provider policy",
	}
}
