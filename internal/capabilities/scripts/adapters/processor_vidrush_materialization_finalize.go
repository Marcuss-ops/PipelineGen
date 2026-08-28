package adapters

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func selectVidRushPrimaryVideo(candidates []scriptpkg.SegmentAssetCandidate) *scriptpkg.SegmentAssetCandidate {
	var selected *scriptpkg.SegmentAssetCandidate
	for i := range candidates {
		candidate := candidates[i]
		if (candidate.Provider != scriptpkg.VidRushProviderArtlist && candidate.Provider != scriptpkg.VidRushProviderYouTube) || !readyVidRushCandidate(candidate) {
			continue
		}
		if selected == nil || compareVidRushPrimaryCandidates(candidate, *selected) > 0 {
			copy := candidate
			copy.Score = ScoreVidRushCandidate(candidate, false)
			copy.SelectionReason = "highest ranked verified and persisted video"
			selected = &copy
		}
	}
	return selected
}

func vidRushArtlistDiagnostics(candidates []scriptpkg.SegmentAssetCandidate) []string {
	diagnostics := make([]string, 0, 3)
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderArtlist {
			continue
		}
		diagnostics = append(diagnostics, fmt.Sprintf("asset=%s acquire=%s verify=%s persist=%s source=%t page=%t drive=%t", candidate.AssetID, candidate.AcquisitionStatus, candidate.VerificationStatus, candidate.PersistenceStatus, hasValue(candidate.SourceURL), hasValue(candidate.SourcePageURL), hasValue(candidate.DriveLink)))
		if len(diagnostics) == 3 {
			break
		}
	}
	return diagnostics
}

func hasValue(value string) bool {
	return strings.TrimSpace(value) != ""
}
