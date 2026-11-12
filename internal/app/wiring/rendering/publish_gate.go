package rendering

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

// validateRenderedArtifact is the final success gate before publication. A
// renderer exit code is not proof of output: a zero-byte, zero-duration or
// unhashed file must become a quarantine event, never a READY artifact.
func validateRenderedArtifact(probed capmodel.OverlayProbeResult) error {
	if probed.SizeBytes <= 0 {
		return fmt.Errorf("empty_artifact: size_bytes=%d", probed.SizeBytes)
	}
	if probed.DurationUS <= 0 {
		return fmt.Errorf("empty_artifact: duration_us=%d", probed.DurationUS)
	}
	sha := strings.TrimSpace(probed.SHA256)
	if sha == "" {
		return fmt.Errorf("empty_artifact: sha256 is empty")
	}
	if len(sha) != 64 {
		return fmt.Errorf("invalid_artifact: sha256 length=%d, want 64", len(sha))
	}
	return nil
}

// quarantineRendered keeps an unprobeable or contract-invalid artifact out of
// the consumer-visible path while preserving the bytes and the reason for
// diagnosis. Failure to quarantine is logged but never promotes the artifact.
func quarantineRendered(jobDir, path, reason string) {
	dir := filepath.Join(jobDir, "quarantine")
	if _, err := atomicwrite.Quarantine(path, dir, reason); err != nil {
		log.Printf("overlay.render quarantine failed path=%s reason=%s err=%v", path, reason, err)
	}
}
