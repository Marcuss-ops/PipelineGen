package clipresolver

import (
	"velox/go-master/internal/media/clipcatalog"
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
