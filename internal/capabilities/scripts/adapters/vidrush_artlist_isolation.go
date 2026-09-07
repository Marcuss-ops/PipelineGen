package adapters

import (
	"fmt"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

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
