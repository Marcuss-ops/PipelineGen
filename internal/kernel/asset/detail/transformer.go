// Package asset — MediaTransformer contract (Wave 3, July 2026).
//
// MediaTransformer replaces the legacy Processor contract for the
// media-transformation concern. It is intentionally narrow: it takes a
// local source file, runs the FFmpeg normalization/rendition pipeline,
// computes hashes, and returns the local result. It does NOT download,
// upload to Drive, or touch the database/outbox.
//
// Download, Drive upload, and DB/outbox persistence are orchestrated
// by the caller in the asset application flow.
package detail

import "context"

// MediaTransformer is the canonical interface for transforming a media
// asset that is already present on local disk.
type MediaTransformer interface {
	// Transform runs the media transformation pipeline on the file at
	// input.LocalPath and returns the canonical local result.
	Transform(ctx context.Context, input *TransformInput) (*TransformResult, error)
}

// TransformInput contains the input for transforming a media asset.
// LocalPath is required and must point to an existing file.
type TransformInput struct {
	ID               string
	Name             string
	LocalPath        string
	OutputDir        string
	Filename         string
	Duration         int
	ForceKeyframes   bool
	StreamCopy       bool
	DownloadSections []string
	Normalize        *bool
	KeepAudio        bool
	DisableDuration  bool
	Width            int
	Height           int
	RenditionLayout  bool
}

// TransformResult contains the result of transforming a media asset.
// It carries only local media metadata; Drive/DB concerns are removed
// from this DTO.
type TransformResult struct {
	ID            string
	Filename      string
	LocalPath     string
	LegacyFileMD5 string
	ContentHash   string
	Status        string
	Error         string
	// Renditions lists the generated technical variants for this asset.
	// Empty for processors that have not been updated to the rendition
	// contract; callers must treat nil/empty as "only the canonical
	// LocalPath/Filename/LegacyFileMD5 are available".
	Renditions []RenditionOutput
}
