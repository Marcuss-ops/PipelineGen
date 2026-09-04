package images

import (
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Compatibility helper for package-local characterization tests. Canonical
// manifest ownership lives in images/generation.
func buildImageManifest(jobID string, position int, outputPath, format string) (*job.ArtifactManifest, error) {
	return imggeneration.BuildImageManifest(jobID, position, outputPath, format)
}
