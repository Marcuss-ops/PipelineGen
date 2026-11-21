package adapters

import (
	"net/url"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// vidRushForbiddenProviders lists provider values that MUST be rejected
// regardless of provenance. This gate enforces the VidRush provider separation
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
	// Selection is an HTTP(S) boundary. A data:, file:, javascript: or
	// scheme-relative locator is never a provider image, even when the
	// provider field is correct.
	if source := strings.TrimSpace(candidate.SourceURL); source != "" && !isAbsoluteHTTPURL(source) {
		return false
	}
	if preview := strings.TrimSpace(candidate.PreviewURL); preview != "" && !isAbsoluteHTTPURL(preview) {
		return false
	}
	return isAbsoluteHTTPURL(candidate.SourceURL) || isAbsoluteHTTPURL(candidate.PreviewURL)
}

func isAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func readyVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	return readyVidRushCandidateWithMode(candidate, false)
}

// readyVidRushCandidateWithMode is the single readiness predicate for every
// provider. allowURLFallback selects the explicit durable-vs-local mode used
// by verifiedImageCandidateWithoutDrive: a verified content address may bind
// its original URL when the durable Drive copy is unavailable. The normal
// binding path never accepts a URL-only candidate.
func readyVidRushCandidateWithMode(candidate scriptpkg.SegmentAssetCandidate, allowURLFallback bool) bool {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if provider == scriptpkg.VidRushProviderYouTube {
		return youtubeCandidateReady(candidate)
	}
	// Lifecycle-aware candidates are fail-closed. Legacy candidates remain
	// readable during the migration window, but only when a durable Drive
	// location proves they were actually persisted.
	if candidate.IsLegacyCandidate() {
		return strings.TrimSpace(candidate.DriveLink) != ""
	}
	if provider == scriptpkg.VidRushProviderInternetImages {
		return internetImageCandidateReady(candidate, allowURLFallback)
	}
	if allowURLFallback {
		return false
	}
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.ContentSHA256) != "" && strings.TrimSpace(candidate.DriveLink) != ""
}

// youtubeCandidateReady is the one YouTube readiness rule. The historical
// implementation carried four near-identical branches with subtly different
// case handling; one predicate keeps the contract testable.
func youtubeCandidateReady(candidate scriptpkg.SegmentAssetCandidate) bool {
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.DriveLink) == "" {
		return false
	}
	rights := strings.TrimSpace(candidate.RightsStatus)
	if rights != "" && !strings.EqualFold(rights, "verified") {
		return false
	}
	if strings.EqualFold(candidate.AcquisitionStatus, scriptpkg.VidRushStatusAcquired) &&
		strings.EqualFold(candidate.VerificationStatus, scriptpkg.VidRushStatusVerified) &&
		strings.EqualFold(candidate.PersistenceStatus, scriptpkg.VidRushStatusPersisted) &&
		vidRushIndexReady(candidate.IndexStatus) {
		return true
	}
	// Legacy YouTube candidates can carry a ready local/Drive binding without
	// the newer lifecycle stamps. ReadyForBinding remains the authority for
	// that migration window, but a Drive copy is still mandatory.
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.DriveLink) != ""
}

// internetImageCandidateReady is the one internet-image readiness rule.
// unknown_allowed is sufficient for the technical retrieval contract; only an
// explicit rejection blocks binding. The durable path requires persistence,
// index projection and a Drive copy; the URL fallback requires the verified
// content address and the original locator instead.
func internetImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, allowURLFallback bool) bool {
	rights := strings.TrimSpace(candidate.RightsStatus)
	if strings.EqualFold(rights, "rejected") {
		return false
	}
	if rights != "" && !strings.EqualFold(rights, "verified") && !strings.EqualFold(rights, "unknown_allowed") {
		return false
	}
	if !strings.EqualFold(candidate.AcquisitionStatus, scriptpkg.VidRushStatusAcquired) ||
		!strings.EqualFold(candidate.VerificationStatus, scriptpkg.VidRushStatusVerified) ||
		strings.TrimSpace(candidate.ContentSHA256) == "" {
		return false
	}
	if allowURLFallback {
		return (strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != "") &&
			strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), "accepted")
	}
	durable := strings.EqualFold(candidate.PersistenceStatus, scriptpkg.VidRushStatusPersisted) &&
		vidRushIndexReady(candidate.IndexStatus) &&
		strings.TrimSpace(candidate.DriveLink) != ""
	if durable {
		return true
	}
	// Older lanes can record a usable Drive copy with a non-canonical index
	// status (for example "queued"). The lifecycle predicate remains the
	// authority; the fallback only accepts a candidate that already proves it
	// is ready for binding and has both a content address and a Drive copy.
	return candidate.ReadyForBinding() &&
		strings.TrimSpace(candidate.ContentSHA256) != "" &&
		strings.TrimSpace(candidate.DriveLink) != ""
}

func vidRushIndexReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "indexed", "pending", "discovered", "indexing_skipped_no_indexer":
		return true
	default:
		return false
	}
}

func preserveSelectedVidRushImages(selected, valid []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	if len(selected) == 0 {
		return filterVidRushImages(valid)
	}
	validByIdentity := make(map[string]scriptpkg.SegmentAssetCandidate, len(valid))
	for _, candidate := range valid {
		if candidate.Provider == scriptpkg.VidRushProviderImageGeneration {
			continue
		}
		validByIdentity[selectedCandidateIdentity(candidate)] = candidate
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		key := selectedCandidateIdentity(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		if _, ok := validByIdentity[key]; ok {
			// The selected record is the stable asset identity. The validation
			// pass may return a refreshed projection of the same candidate, but
			// silently swapping the selected struct (asset id/Drive link) made
			// finalization observe a different image than selection chose.
			out = append(out, candidate)
			seen[key] = struct{}{}
		}
	}
	if len(out) > 0 {
		return out
	}
	if len(selected) > 0 {
		// A non-empty selection that did not survive validation is a real
		// failure, not permission to swap in a different asset. Replacing it
		// with the validator's projection made finalization silently bind an
		// image selection never chose.
		return nil
	}
	return filterVidRushImages(valid)
}

// selectedCandidateIdentity is the discovery identity used to preserve the
// selected asset across finalization. DriveLink is deliberately excluded: it
// is assigned by the persistence step and may change between selection and
// finalization, while AssetID and the source locators identify the image the
// selector actually chose.
func selectedCandidateIdentity(candidate scriptpkg.SegmentAssetCandidate) string {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	for _, value := range []string{candidate.AssetID, candidate.SourceURL, candidate.PreviewURL, candidate.DriveLink} {
		if value = strings.TrimSpace(value); value != "" {
			return provider + "\x00" + strings.ToLower(value)
		}
	}
	return ""
}

func filterVidRushImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
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
	// The hash is the discovery identity of the set. Acquisition, verification,
	// persistence and index statuses mutate after discovery, so including them
	// made the same candidate set hash differently across lifecycle stages.
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, strings.Join([]string{
			candidate.AssetID, candidate.Provider, candidate.Query, candidate.Entity,
			candidate.SourceURL, candidate.PreviewURL,
		}, "\x00"))
	}
	return segmentCacheKey(parts...)
}
