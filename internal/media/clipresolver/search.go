package clipresolver

import (
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/clipcatalog"
)

// qdrantResultToCandidate converts a vector store SearchResult to ClipCandidate.
func qdrantResultToCandidate(r SearchResult) clipcatalog.ClipCandidate {
	return clipcatalog.ClipCandidate{
		ID:        r.AssetID,
		Name:      r.Name,
		DriveLink: r.DriveLink,
		LocalPath: r.LocalPath,
		Category:  r.Category,
	}
}
