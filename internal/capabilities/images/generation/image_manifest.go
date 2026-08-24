// Package images — image_manifest.go: typed ArtifactManifest builder
// for the canonical Sender-side upload cycle (PR-GODOBJ-3-IMAGES-GENERATION, July 2026).
//
// godlike/06 SSOT: one canonical owner per fact — the manifest-building
// concern for image generation lives ONLY here. Caller paths (sync
// adapter, async job adapter) receive the typed *job.ArtifactManifest
// via UsecaseOutput.Manifest and route it through their own persistence
// (sync → direct IngestImage, async → manifest sidecar to runner finalizer).
//
// PR-GODOBJ-3 godlike/07 typed-error contract: every error path is
// returned as `error` (godlike/07 typed-error envelope).
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~99 LoC) because the
// buildImageManifest path incorporates typed validation (empty jobID +
// empty outputPath guard), on-disk stat + ComputeSHA256, MIMEType
// derivation (format → "image/...", not from filename suffix), and
// the canonical *job.ArtifactManifest assembly with the position-aware
// artifact ID convention (`<jobID>:image:<position>`). Forward-pointer
// linked_issue: PR-GODOBJ-3e-IMAGE-MANIFEST-SLIM extracts stat+sha
// into pkg/artifactstat helper + per-format MIME mapping (≤30 LoC each).
package generation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// buildImageManifest materialises the canonical Sender-side ArtifactManifest
// sidecar for a generated image. Per C11 (P0 Commit 11): one REQUIRED
// kind=image artifact per job; the runner's uploadManifest reads via job.Decode.
//
// ID convention (mirrors script.generate):
//
//	fmt.Sprintf("%s:image:%d", jobID, position)
//
// so the runner's `uploaded[ID]` map cannot collide when the same
// jobID is processed in a batch context (multiple Position values).
//
// MIMEType derives from the provider's result.Format (e.g. png/jpg/webp)
// — NOT from the local filename. The provider's format wins; the .png
// suffix on outputPath is a hint, not a contract.
//
// PR-GODOBJ-3: moved from generation_service.go after the god-object
// decomposition. The async job handler returns this manifest via
// job.ManifestKey in handlerResult (typed *job.ArtifactManifest); the
// finalizer handles persistence (NO inline IngestImage in the handler
// per KILL LIST b).
func buildImageManifest(jobID string, position int, outputPath, format string) (*job.ArtifactManifest, error) {
	if strings.TrimSpace(outputPath) == "" {
		return nil, fmt.Errorf("buildImageManifest: outputPath is empty")
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("buildImageManifest: jobID is empty")
	}

	fi, statErr := os.Stat(outputPath)
	if statErr != nil {
		return nil, fmt.Errorf("buildImageManifest: stat %q: %w", outputPath, statErr)
	}
	size := fi.Size()

	sha, shaErr := job.ComputeSHA256(filesystem.NewOS(), outputPath)
	if shaErr != nil {
		return nil, fmt.Errorf("buildImageManifest: sha256 %q: %w", outputPath, shaErr)
	}

	mimeType := "image/" + strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		mimeType = "image/png"
	}

	filename := filepath.Base(outputPath)
	if filename == "" || filename == "." || filename == "/" {
		filename = fmt.Sprintf("image-%d.png", position)
	}

	art := job.Artifact{
		ID:        fmt.Sprintf("%s:image:%d", jobID, position),
		Kind:      job.ArtifactKindImage,
		Path:      outputPath,
		Filename:  filename,
		MIMEType:  mimeType,
		SizeBytes: size,
		SHA256:    sha,
		Required:  true,
	}

	return &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "",
		JobID:         jobID,
		Artifacts:     []job.Artifact{art},
	}, nil
}

// NOTE: the prior version of this file declared a fourth type,
// `RunManifest`, that was intended as the typed envelope for
// handlerResult[ManifestKey]. Production moved on to typed
// *job.ArtifactManifest (interfaces.go + job.Decode handle the
// decode gate); RunManifest was unused (zero callers in this PR).
// Per godlike/06 SSOT ("facts with no owner should not be declared"),
// the type has been removed. The handlerResult[ManifestKey] slot
// carries *job.ArtifactManifest directly via generation_usecase.go's
// UsecaseOutput.Manifest field.
