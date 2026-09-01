package adapters

import (
	"fmt"
	"math"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// clampUnit is a neutral score-normalization helper for discovery metadata.
// It does not select or rank media candidates.
func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func hasValue(value string) bool { return strings.TrimSpace(value) != "" }

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
