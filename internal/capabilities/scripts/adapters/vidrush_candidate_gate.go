package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
