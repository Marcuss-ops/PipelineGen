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
	// A worker that reports success with a zero-frame or zero-duration video
	// has not produced a publishable artifact. Reject it at the completion
	// gate instead of recording a SUCCEEDED job with unusable output.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.ContentType)), "video/") {
		if artifact.FrameCount <= 0 {
			return fmt.Errorf("artifact frame_count must be positive for video output")
		}
		if artifact.DurationUS <= 0 {
			return fmt.Errorf("artifact duration_us must be positive for video output")
		}
	}
	return nil
}
