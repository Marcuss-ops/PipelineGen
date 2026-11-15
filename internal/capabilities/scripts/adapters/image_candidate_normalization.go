package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// isInternetImageSourceProvider is the explicit allowlist of concrete search
// backends that may enter the generic internet_images media lifecycle.
func isInternetImageSourceProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "internet_images", "duckduckgo", "wikimedia_commons", "searxng":
		return true
	default:
		return false
	}
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
