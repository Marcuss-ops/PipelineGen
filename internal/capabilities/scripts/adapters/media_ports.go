package adapters

import (
	"context"

	scriptmetrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ArtlistClipMatch is the discovery projection returned by an Artlist
// provider. It contains remote provenance only; materialization owns durable
// binding.
type ArtlistClipMatch struct {
	Phrase           string   `json:"phrase"`
	ClipNames        []string `json:"clip_names,omitempty"`
	ClipDriveLinks   []string `json:"clip_drive_links,omitempty"`
	FolderLink       string   `json:"folder_link,omitempty"`
	FolderName       string   `json:"folder_name,omitempty"`
	FolderID         string   `json:"folder_id,omitempty"`
	TranslationError string   `json:"translation_error,omitempty"`
	Remote           bool     `json:"remote,omitempty"`
}

type ArtlistClipSearcher interface {
	SearchClips(ctx context.Context, title string, phrases []string) ([]ArtlistClipMatch, error)
}

type InternetImageSearchRequest struct {
	SegmentID string
	Position  int
	TextHash  string
	Query     string
	Entity    string
	Language  string
	Limit     int
	Provider  string
	// ForceRefresh bypasses durable retrieved-image reuse: the bytes behind
	// a stored URL can change upstream (source re-encode), so a recorded
	// hash is only a claim until bytes are re-downloaded and re-hashed
	// in-run. Callers set it from the plan's refresh flags.
	ForceRefresh bool
}

type InternetImageSearcher interface {
	SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error)
}

type VidRushMetrics = scriptmetrics.VidRushMetrics

type MetadataGenerator interface {
	GenerateMetadata(ctx context.Context, req scriptpkg.MetadataGenerationRequest) ([]scriptpkg.VideoMetadata, error)
}
