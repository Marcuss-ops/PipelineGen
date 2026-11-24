package vidrush

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/imagegate"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MinInternetImageSemanticScore is the acceptance floor applied to a candidate
// that carries an explicit semantic score. It is deliberately low: the gate
// exists to drop clearly off-topic results, not to second-guess a working
// scorer with a second ranking authority.
const MinInternetImageSemanticScore = 0.35

// SemanticCandidateScorer annotates discovered image candidates with a
// semantic verdict by setting SemanticStatus / SemanticScore (and optionally
// RelevanceScore / QualityReason). Implementations belong to infrastructure;
// returning an error is treated as "scoring unavailable" and the unscored
// candidates flow through unchanged.
type SemanticCandidateScorer interface {
	ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error)
}

// resolveEntityVisualCandidates is the shared semantic-verification boundary
// used by the batch planner, VidRush fanout and MediaResolver. It keeps source
// context separate from retrieval text and does not choose a winner; callers
// apply their existing lifecycle and catalog policies to the returned verdicts.
func resolveEntityVisualCandidates(ctx context.Context, scorer SemanticCandidateScorer, class, sourceContext string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	if contextText := strings.TrimSpace(sourceContext); contextText != "" {
		for i := range out {
			out[i].EntityContext = contextText
		}
	}
	if scorer == nil {
		return out, nil
	}
	class = strings.TrimSpace(class)
	if class == "" {
		class = "generic"
	}
	return scoreInternetImageCandidates(ctx, scorer, "entity image class="+class, out)
}

// applyInternetImageSemanticGate drops only candidates a scorer explicitly
// rejected or scored below MinInternetImageSemanticScore. Discovery is a
// recall stage: an unwired or failing scorer must not empty a run. Entity
// images are fail-closed later, at selection time, where an accepted verdict
// is required before a candidate may consume an entity slot.
func applyInternetImageSemanticGate(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if candidate.SemanticScore > 0 && candidate.SemanticScore < MinInternetImageSemanticScore {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// scoreInternetImageCandidates runs the optional scorer over a query's
// candidates. A nil scorer, an empty batch, or a scorer error leaves the
// candidates untouched; the returned error is surfaced so the caller can warn
// without failing discovery.
func scoreInternetImageCandidates(ctx context.Context, scorer SemanticCandidateScorer, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if scorer == nil || len(candidates) == 0 {
		return candidates, nil
	}
	if imagegate.AllCandidatesHaveReusableVerdict(candidates) {
		// The catalog/cache already holds a definitive pixel verdict for every
		// candidate. Re-running the visual scorer here is exactly the double
		// download + VLM call this fast path removes.
		return candidates, nil
	}
	scored, err := scorer.ScoreImageCandidates(ctx, query, candidates)
	if err != nil {
		return candidates, err
	}
	if scored == nil {
		return candidates, nil
	}
	return scored, nil
}

// isInternetImageSourceProvider is the explicit allowlist of concrete search
// backends that may enter the generic internet_images media lifecycle.
func isInternetImageSourceProvider(provider string) bool {
	return scriptpkg.IsInternetImageSourceProvider(provider)
}

// normalizeInternetImageCandidates performs discovery normalization only.
// It intentionally does not assign scores or choose a candidate.
func normalizeInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate, query string) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.Provider) == "" {
			candidate.Provider = scriptpkg.VidRushProviderInternetImages
		}
		// Preserve the concrete backend before any layer rewrites Provider to
		// the internet_images lifecycle bucket.
		if strings.TrimSpace(candidate.RetrievalProvider) == "" &&
			!strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			candidate.RetrievalProvider = strings.ToLower(strings.TrimSpace(candidate.Provider))
		}
		if strings.TrimSpace(candidate.Query) == "" {
			candidate.Query = query
		}
		if candidate.RightsStatus == "" {
			candidate.RightsStatus = "unknown"
		}
		if candidate.SelectionReason == "" {
			candidate.SelectionReason = "retrieved image candidate matching a segment entity/query"
		}
		out = append(out, candidate)
	}
	return out
}

// isPlaceholderImageReference rejects the vector-icon and spacer assets that
// image search backends sometimes return for generic queries. They are not
// photographs, cannot satisfy a documentary image slot, and must never become
// a semantic-plan ResolvedImage. The check is deliberately narrow: it targets
// SVG/icon hosts and explicit placeholder names, not ordinary small photos.
func isPlaceholderImageReference(rawURL, mimeType string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(mimeType)), "svg") {
		return true
	}
	u := strings.ToLower(strings.TrimSpace(rawURL))
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "data:") {
		return true
	}
	path := u
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if strings.HasSuffix(path, ".svg") {
		return true
	}
	for _, marker := range []string{
		"lucide-static", "/devicons/", "/icons/", "icon-", "placeholder",
		"spacer.", "/1x1.", "pixel.gif", "transparent.png",
	} {
		if strings.Contains(u, marker) {
			return true
		}
	}
	return false
}

func deduplicateInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, candidate := range in {
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func filterInternetImageCandidates(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
		if !isInternetImageSourceProvider(provider) || !validVidRushCandidate(candidate) {
			continue
		}
		if isPlaceholderImageReference(candidate.SourceURL, candidate.MIMEType) ||
			isPlaceholderImageReference(candidate.PreviewURL, candidate.MIMEType) {
			continue
		}
		if strings.TrimSpace(candidate.RetrievalProvider) == "" && provider != scriptpkg.VidRushProviderInternetImages {
			candidate.RetrievalProvider = provider
		}
		candidate.Provider = scriptpkg.VidRushProviderInternetImages
		out = append(out, candidate)
	}
	// The semantic gate runs last: a candidate must first be a well-formed
	// internet image before its (optional) relevance verdict is consulted.
	return applyInternetImageSemanticGate(out)
}
