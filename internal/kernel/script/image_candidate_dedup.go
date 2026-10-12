package script

// removeCrossSceneImageCandidateReuse makes the per-scene image surface match
// its contract before semantic certification. Provider searches and the shared
// catalog can legitimately return the same asset for several scenes; keeping
// those copies in each segment makes the certifier treat every retrieval hit
// as a bound asset, even when entity binding selected a different variant.
// Keep the first scene's copy and remove later copies from every image list.
func RemoveCrossSceneImageCandidateReuse(segments []VidRushSegmentResult) []VidRushSegmentResult {
	seenInEarlierScene := make(map[string]struct{})
	out := make([]VidRushSegmentResult, len(segments))
	for i, segment := range segments {
		out[i] = segment
		out[i].Assets = segment.Assets
		out[i].Assets.Candidates = filterImageCandidatesFromEarlierScenes(segment.Assets.Candidates, seenInEarlierScene)
		out[i].Assets.SecondaryImages = filterImageCandidatesFromEarlierScenes(segment.Assets.SecondaryImages, seenInEarlierScene)
		out[i].Assets.GeneratedImages = filterImageCandidatesFromEarlierScenes(segment.Assets.GeneratedImages, seenInEarlierScene)
		for _, candidates := range [][]SegmentAssetCandidate{
			out[i].Assets.SecondaryImages,
			out[i].Assets.GeneratedImages,
		} {
			for _, candidate := range candidates {
				if candidate.AssetID != "" {
					seenInEarlierScene[candidate.AssetID] = struct{}{}
				}
			}
		}
	}
	return out
}

func filterImageCandidatesFromEarlierScenes(candidates []SegmentAssetCandidate, seen map[string]struct{}) []SegmentAssetCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	filtered := make([]SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.AssetID != "" {
			if _, exists := seen[candidate.AssetID]; exists {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}
