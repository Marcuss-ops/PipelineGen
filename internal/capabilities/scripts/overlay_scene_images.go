package scriptgeneration

import (
	"encoding/hex"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// sceneImageCandidate projects one already-materialized still image onto its
// owning scene. Scene images are independent of entity cards: recurring people
// can keep their canonical portrait while each scene gets its own contextual
// visual. Timing is anchored at the certified scene start, never estimated
// from the narration text.
func sceneImageCandidate(result *GenerateResult, sceneID string, startUS int64, excludedHashes ...map[string]struct{}) (capabilityoverlay.ImageCandidate, bool) {
	if result == nil || strings.TrimSpace(sceneID) == "" || startUS < 0 {
		return capabilityoverlay.ImageCandidate{}, false
	}
	for _, segment := range result.Segments {
		if segment.SceneID != sceneID && segment.SegmentID != sceneID {
			continue
		}
		candidates := append(append([]scriptpkg.SegmentAssetCandidate(nil), segment.Assets.SecondaryImages...), segment.Assets.Candidates...)
		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			assetID := strings.TrimSpace(candidate.AssetID)
			digest := strings.TrimSpace(candidate.LegacyFileMD5)
			if _, duplicate := seen[assetID]; duplicate {
				continue
			}
			seen[assetID] = struct{}{}
			if !sceneImageCandidateReady(candidate, digest) {
				continue
			}
			if len(excludedHashes) > 0 {
				if _, duplicate := excludedHashes[0][strings.ToLower(digest)]; duplicate {
					continue
				}
			}
			url := strings.TrimSpace(candidate.SourceURL)
			if url == "" {
				url = strings.TrimSpace(candidate.PreviewURL)
			}
			// Entity cards keep the opening beat for canonical subject context.
			// Scene-context images enter immediately after that five-second card
			// window so the remote replace-only timeline can carry both without an
			// invalid overlapping range.
			startUS = ((startUS / 1000) * 1000) + capabilityoverlay.MaxImageOverlayDurationMS*1000
			return capabilityoverlay.ImageCandidate{
				AssetID: digest, URL: url, LocalPath: strings.TrimSpace(candidate.LocalPath),
				SHA256: digest, MediaType: strings.TrimSpace(candidate.MIMEType),
				StartMs: startUS / 1000, EndMs: startUS/1000 + capabilityoverlay.MaxImageOverlayDurationMS,
				StartUS: startUS, DurationUS: capabilityoverlay.MaxImageOverlayDurationMS * 1000,
				Score: 1,
			}, true
		}
	}
	return capabilityoverlay.ImageCandidate{}, false
}

func sceneImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, digest string) bool {
	if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) ||
		strings.TrimSpace(candidate.AssetID) == "" || candidate.AcquisitionStatus != scriptpkg.VidRushStatusAcquired ||
		candidate.VerificationStatus != scriptpkg.VidRushStatusVerified ||
		candidate.PersistenceStatus != scriptpkg.VidRushStatusPersisted || candidate.IndexStatus != scriptpkg.VidRushStatusIndexed || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	return strings.TrimSpace(candidate.LocalPath) != "" || strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != ""
}
