package generation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// BuildImageManifest materializes the canonical Sender-side image artifact
// manifest used by the async finalizer.
func BuildImageManifest(jobID string, position int, outputPath, format string) (*job.ArtifactManifest, error) {
	if strings.TrimSpace(outputPath) == "" {
		return nil, fmt.Errorf("buildImageManifest: outputPath is empty")
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("buildImageManifest: jobID is empty")
	}
	fi, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("buildImageManifest: stat %q: %w", outputPath, err)
	}
	sha, err := job.ComputeSHA256(filesystem.NewOS(), outputPath)
	if err != nil {
		return nil, fmt.Errorf("buildImageManifest: sha256 %q: %w", outputPath, err)
	}
	mimeType := "image/" + strings.ToLower(strings.TrimSpace(format))
	if strings.TrimSpace(format) == "" {
		mimeType = "image/png"
	}
	filename := filepath.Base(outputPath)
	if filename == "" || filename == "." || filename == "/" {
		filename = fmt.Sprintf("image-%d.png", position)
	}
	return &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		JobID:         jobID,
		Artifacts: []job.Artifact{{
			ID: fmt.Sprintf("%s:image:%d", jobID, position), Kind: job.ArtifactKindImage,
			Path: outputPath, Filename: filename, MIMEType: mimeType,
			SizeBytes: fi.Size(), SHA256: sha, Required: true,
		}},
	}, nil
}
