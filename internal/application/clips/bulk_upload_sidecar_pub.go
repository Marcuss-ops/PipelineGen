// Package clips (bulk_upload_sidecar_pub) — Step "sidecar-pub" of
// the per-clip pipeline. Best-effort upload of the per-clip
// co-located sidecars (clip_manifest.json + transcript.txt) to the
// SAME Drive folder that clip_pub resolved for the .mp4.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// Pre-extraction behaviour (preserved verbatim):
//   - Group is intentionally empty: setting Group on sidecar
//     publishes would create double-nesting under the canonical
//     folder hierarchy established by clip_pub.publishClip.
//   - ParentFolderID is the resolved folder id from the
//     .mp4 publish (pubRes.FolderID).
//   - Errors from Publisher.Publish are deliberately swallowed:
//     sidecars are observation metadata, not the primary payload.
//     A sidecar publish failure must NOT bump the failed counter
//     nor abort the .mp4 commit.
//
// No new abstractions — top-level helper function with no return
// value (errors intentionally swallowed to preserve pre-split
// semantics; a future wave that wants strict-error-propagation
// can introduce an Out[*SidecarError] channel without changing
// this signature).
package clips

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// publishSidecars uploads the per-clip manifest + transcript
// sidecars (when present on disk) into the SAME Drive folder
// resolved by the .mp4 publish.
//
// Caller-side responsibilities:
//   - call only after a successful clip-pub.publishClip (so
//     targetFolderID is the resolved pubRes.FolderID).
//   - not bump any counter on silent failure (best-effort).
//
// The function is intentionally nothing-returning — pre-split
// behaviour silently dropped errors. Future hardening is a
// separate wave.
func publishSidecars(
	ctx context.Context,
	publisher ClipPublisherPort,
	cand clipCandidate,
	targetFolderID string,
	log *zap.Logger,
) {
	if publisher == nil {
		return
	}
	dir := filepath.Dir(cand.LocalPath)
	baseNoExt := strings.TrimSuffix(filepath.Base(cand.LocalPath), filepath.Ext(cand.LocalPath))

	manifestPath := filepath.Join(dir, "clip_manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		if _, err := publisher.Publish(ctx, delivery.PublishRequest{
			Destination: delivery.DestinationYouTubeClip,
			LocalPath:   manifestPath,
			Filename:    baseNoExt + ".clip_manifest.json",
			Description: "Clip manifest for " + baseNoExt,
			ProjectID:   targetFolderID,
			// PR-P12-CLIPS-AND-BOOKS (July 2026): ParentFolderID RETIRED.
			// The resolved .mp4 folder ID is now routed via ProjectID so
			// the sidecar co-locates with the clip under the canonical
			// DestinationYouTubeClip root + PathBuilder hierarchy.
		}); err != nil && log != nil {
			log.Warn("sidecar publish failed (non-fatal)",
				zap.String("path", manifestPath),
				zap.Error(err))
		}
	}

	for _, tp := range []string{
		filepath.Join(dir, baseNoExt+".txt"),
		filepath.Join(dir, "transcript.txt"),
	} {
		if _, err := os.Stat(tp); err == nil {
			if _, err := publisher.Publish(ctx, delivery.PublishRequest{
				Destination: delivery.DestinationYouTubeClip,
				LocalPath:   tp,
				Filename:    baseNoExt + ".transcript.txt",
				Description: "Whisper transcript for " + baseNoExt,
				ProjectID:   targetFolderID,
				// PR-P12-CLIPS-AND-BOOKS (July 2026): ParentFolderID RETIRED.
				// See manifest publish above for the canonical routing rationale.
			}); err != nil && log != nil {
				log.Warn("sidecar publish failed (non-fatal)",
					zap.String("path", tp),
					zap.Error(err))
			}
			// Match pre-split semantics: publish the FIRST transcript
			// sibling found, then break. Avoids double-uploading both
			// per-name.txt AND generic transcript.txt if both exist.
			break
		}
	}
}
