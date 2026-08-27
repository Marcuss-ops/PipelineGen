package script

import "strings"

// IsVidRushProvider reports whether a provider is part of the VidRush
// registry contract. Unknown providers must not be silently accepted by a
// production composition root.
func IsVidRushProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case VidRushProviderArtlist, VidRushProviderInternetImages, VidRushProviderImageGeneration, VidRushProviderYouTube:
		return true
	default:
		return false
	}
}

// LifecycleComplete reports whether the candidate has passed every durable
// stage required before scene binding. A legacy candidate with no lifecycle
// fields returns false; callers may use IsLegacyCandidate when reading old
// result rows during the compatibility window.
func (c SegmentAssetCandidate) LifecycleComplete() bool {
	return c.AcquisitionStatus == VidRushStatusAcquired &&
		c.VerificationStatus == VidRushStatusVerified &&
		c.PersistenceStatus == VidRushStatusPersisted &&
		c.IndexStatus == VidRushStatusIndexed
}

// IsLegacyCandidate identifies the pre-lifecycle wire shape. It exists only
// for migration/read compatibility and must not be used by new acquisition
// code as proof that an artifact is ready to bind.
func (c SegmentAssetCandidate) IsLegacyCandidate() bool {
	return strings.TrimSpace(c.AcquisitionStatus) == "" &&
		strings.TrimSpace(c.VerificationStatus) == "" &&
		strings.TrimSpace(c.PersistenceStatus) == "" &&
		strings.TrimSpace(c.IndexStatus) == ""
}

// ReadyForBinding is the fail-closed predicate for new lifecycle-aware
// artifacts. Legacy payloads are accepted only when every field needed by the
// old binding contract is present; a candidate that starts a lifecycle but
// does not finish it can never win.
func (c SegmentAssetCandidate) ReadyForBinding() bool {
	if c.LifecycleComplete() {
		return strings.EqualFold(strings.TrimSpace(c.RightsStatus), "verified")
	}
	// Scene binding is durable after Drive persistence; Qdrant indexing is a
	// rebuildable projection and may complete asynchronously. Keep failed
	// indexing fail-closed, but do not delay the canonical scene binding for
	// candidates that are persisted and still awaiting projection.
	if c.AcquisitionStatus == VidRushStatusAcquired &&
		c.VerificationStatus == VidRushStatusVerified &&
		c.PersistenceStatus == VidRushStatusPersisted &&
		(strings.EqualFold(c.IndexStatus, "pending") ||
			strings.EqualFold(c.IndexStatus, "discovered") ||
			strings.EqualFold(c.IndexStatus, "indexing_skipped_no_indexer")) {
		return strings.EqualFold(strings.TrimSpace(c.RightsStatus), "verified")
	}
	if !c.IsLegacyCandidate() {
		return false
	}
	return strings.TrimSpace(c.AssetID) != "" && strings.TrimSpace(c.DriveLink) != ""
}
