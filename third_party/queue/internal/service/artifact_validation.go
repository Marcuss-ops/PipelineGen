package service

import (
	"fmt"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"strings"
)

// validateArtifact is the queue-side completion gate. A worker may only move
// a job to completed after it has published a verifiable artifact reference;
// an empty payload would create a completed job with no durable output.
func validateArtifact(artifact model.Artifact) error {
	if strings.TrimSpace(artifact.StorageKey) == "" {
		return fmt.Errorf("artifact storage_key is required")
	}
	if strings.TrimSpace(artifact.ArtifactHash) == "" {
		return fmt.Errorf("artifact sha256 is required")
	}
	if artifact.SizeBytes <= 0 {
		return fmt.Errorf("artifact size_bytes must be positive")
	}
	if strings.TrimSpace(artifact.ContentType) == "" {
		return fmt.Errorf("artifact content_type is required")
	}
	return nil
}
