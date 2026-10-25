package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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

func CloneVidRushSegmentResult(in scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := in
	out.Warnings = append([]string(nil), in.Warnings...)
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
	out.Insights.SpecialNames = append([]string(nil), in.Insights.SpecialNames...)
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
