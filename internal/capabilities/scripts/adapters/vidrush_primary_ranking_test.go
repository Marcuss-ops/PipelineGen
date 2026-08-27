package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func durableVideoCandidate(provider, assetID string, relevance float64) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{
		AssetID: assetID, Provider: provider,
		SourceURL: "https://example.test/" + assetID,
		DriveLink: "https://drive.google.com/file/d/" + assetID,
		Score:     relevance, RelevanceScore: relevance,
		TechnicalQualityScore: 1, RightsScore: 1,
		ProviderReliability: 1, RightsStatus: "verified",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        scriptpkg.VidRushStatusIndexed,
		LegacyFileMD5:      "md5-" + assetID,
	}
}

func TestChooseVidRushPrimary_YouTubeWinsWhenScoreIsHigher(t *testing.T) {
	candidates := []scriptpkg.SegmentAssetCandidate{
		durableVideoCandidate(scriptpkg.VidRushProviderArtlist, "artlist-1", .71),
		durableVideoCandidate(scriptpkg.VidRushProviderYouTube, "youtube-1", .94),
	}
	got := chooseVidRushPrimary(candidates, map[string]string{})
	if got == nil || got.AssetID != "youtube-1" {
		t.Fatalf("primary = %+v, want higher-scoring YouTube candidate", got)
	}
}

func TestChooseVidRushPrimary_ArtlistWinsWhenScoreIsHigher(t *testing.T) {
	candidates := []scriptpkg.SegmentAssetCandidate{
		durableVideoCandidate(scriptpkg.VidRushProviderYouTube, "youtube-1", .51),
		durableVideoCandidate(scriptpkg.VidRushProviderArtlist, "artlist-1", .87),
	}
	got := chooseVidRushPrimary(candidates, map[string]string{})
	if got == nil || got.AssetID != "artlist-1" {
		t.Fatalf("primary = %+v, want higher-scoring Artlist candidate", got)
	}
}

func TestCompareVidRushPrimaryCandidates_TieBreakIsDeterministic(t *testing.T) {
	candidates := []scriptpkg.SegmentAssetCandidate{
		durableVideoCandidate(scriptpkg.VidRushProviderYouTube, "youtube-1", .8),
		durableVideoCandidate(scriptpkg.VidRushProviderArtlist, "artlist-1", .8),
	}
	for i := 0; i < 10; i++ {
		if got := compareVidRushPrimaryCandidates(candidates[1], candidates[0]); got <= 0 {
			t.Fatalf("iteration %d comparison = %d, want Artlist to win deterministically", i, got)
		}
	}
}
