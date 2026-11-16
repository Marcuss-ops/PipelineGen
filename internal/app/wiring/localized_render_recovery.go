package wiring

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// UploadRendered publishes a durable local RENDERED checkpoint directly.
// It deliberately does not call build/render code beyond resolving the
// destination folder, so a retry after a crash cannot rerun Chronon.
func (a *localizedRenderEnqueuerAdapter) UploadRendered(ctx context.Context, in scriptgeneration.LocalizedRenderInput, staged scriptgeneration.LocalizedRenderResult) error {
	if a == nil || a.svc == nil {
		return fmt.Errorf("localized render recovery: service is not wired")
	}
	// A checkpoint written after publication names an already uploaded
	// artifact. Re-publishing it would waste Drive work and can race a
	// duplicate upload; project the recorded certified identity directly.
	if strings.EqualFold(strings.TrimSpace(staged.Status), string(localization.LocalizedClipUploaded)) {
		if strings.TrimSpace(staged.DriveFileID) == "" && strings.TrimSpace(staged.DriveLink) == "" {
			return fmt.Errorf("localized render recovery: uploaded artifact has no Drive identity")
		}
		if in.OnRendered != nil {
			return in.OnRendered(staged)
		}
		return nil
	}
	if strings.TrimSpace(staged.LocalPath) == "" || strings.TrimSpace(staged.SHA256) == "" {
		return fmt.Errorf("localized render recovery: staged artifact is incomplete")
	}
	info, err := os.Stat(staged.LocalPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("localized render recovery: staged local artifact is unavailable: %s", staged.LocalPath)
	}
	computed, _, err := digest.SHA256File(staged.LocalPath)
	if err != nil || !strings.EqualFold(computed, staged.SHA256) {
		return fmt.Errorf("localized render recovery: staged sha256 mismatch for %s", staged.LocalPath)
	}
	clipID := strings.TrimSpace(staged.ClipID)
	if clipID == "" {
		clipID = strings.TrimSpace(in.ClipID)
	}
	// The recovery path must resolve the SAME destination the render used,
	// per-language level included, or a crash-retry would re-upload the
	// staged artifact into a different folder than its certified sibling.
	destination, _, err := a.resolveRenderFolders(ctx, in, clipID, string(staged.Language))
	if err != nil {
		return fmt.Errorf("localized render recovery: resolve destination: %w", err)
	}
	artifact := localization.LocalizedClipArtifact{
		Version: localization.LocalizedClipArtifactVersion, JobID: in.RunID,
		SceneID: staged.SceneID, ClipID: clipID, Language: string(staged.Language),
		// Carry the original render identity across the crash recovery: the
		// recovered artifact was produced by that same render job, so the run
		// result keeps naming it after the upload-only retry.
		PlanRevision: staged.RenderJobID,
		AssetID:      staged.AssetID, LocalPath: staged.LocalPath, SHA256: staged.SHA256,
		SizeBytes: staged.SizeBytes, StorageKey: staged.StorageKey, ArtifactURL: staged.ArtifactURL,
		DurationMS: staged.DurationMS, Status: localization.LocalizedClipRendered,
	}
	uploader, ok := a.svc.(localizedArtifactUploader)
	if !ok {
		return fmt.Errorf("localized render recovery: upload-only service is not wired")
	}
	published, err := uploader.UploadRendered(ctx, artifact, destination)
	if err != nil {
		return fmt.Errorf("localized render recovery: upload: %w", err)
	}
	if in.OnRendered != nil {
		return in.OnRendered(scriptgeneration.LocalizedRenderResult{
			SceneID: published.SceneID, SceneIndex: in.SceneIndex, Language: scriptgeneration.Language(published.Language),
			RenderJobID: published.PlanRevision,
			Reused:      published.Reused, RenderSource: published.RenderSource,
			ClipID: published.ClipID, AssetID: published.AssetID, SHA256: published.SHA256,
			DriveFileID: published.DriveFileID, DriveLink: published.DriveLink, DriveFolderID: published.DriveFolderID,
			DurationMS: published.DurationMS, SizeBytes: published.SizeBytes,
			StorageKey: published.StorageKey, ArtifactURL: published.ArtifactURL,
			LocalPath: published.LocalPath, Status: string(published.Status), Backend: published.Backend, Metrics: metricsMapFromJSON(published.MetricsJSON), StartedAt: staged.StartedAt,
			FinishedAt: time.Now().UTC(),
		})
	}
	return nil
}
