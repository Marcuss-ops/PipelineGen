// Package images — generated_image_ingest.go (commit 8, 2026-07):
// SYNC-ONLY consumer of ImageStorageService::IngestImage.
//
// PR-GODOBJ-3 KILL LIST b (commit 8 split): ingestGeneratedImage is
// the canonical sync-path ingestion entry point. The async job
// NEVER calls this — it emits the *job.ArtifactManifest sidecar
// via job.ManifestKey and the runner's finalizer handles
// media_assets persistence.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of
// "the synchronous ingest-from-GeneratedImageResult path" in
// the images package. The pure naming helpers it depends on
// live in generated_image_naming.go (same package, free
// functions, no pkg/imgsyncutil per the wave rule).
//
// File size invariant: stays under the 66-LoC transitional cap
// because the 4 inline naming expressions are now helper calls.
package generation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ingestGeneratedImage routes the on-disk artifact (or in-memory
// bytes if no OutputPath was set by the provider) into
// storage.IngestImage. Sync paths are the SOLE consumer of
// IngestImage per PR-GODOBJ-3 KILL LIST b.
//
// The 4 typing-shaped arguments to IngestImage (slug, filename,
// description, source) are produced by the canonical pure helpers
// in generated_image_naming.go. Behavior preserved byte-byte
// from the pre-split inline block.
func (g *GenerationService) ingestGeneratedImage(
	ctx context.Context,
	result *GeneratedImage,
	style string,
	tags []string,
	skipDrive bool,
) (*asset.ImageAsset, error) {
	if result == nil {
		return nil, fmt.Errorf("generated image result is nil")
	}

	slug := buildGeneratedImageSlug(result.PromptUsed)
	filename := buildGeneratedImageFilename(result.PromptUsed, result.Format)
	description := buildGeneratedImageDescription(result.PromptUsed)
	source := resolveGeneratedImageSource(result.Provider)

	var dataReader io.Reader = bytes.NewReader(result.Data)
	if result.OutputPath != "" {
		f, err := os.Open(result.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("ingestGeneratedImage: failed to open output path %s: %w", result.OutputPath, err)
		}
		defer f.Close()
		dataReader = f
	}

	if result.OutputPath == "" && len(result.Data) == 0 {
		return nil, fmt.Errorf("generated image has no data and no output path")
	}

	return g.storage.IngestImage(
		ctx,
		slug,
		style,
		result.SourceHash,
		dataReader,
		filename,
		source,
		description,
		tags,
		skipDrive,
		false,
	)
}
