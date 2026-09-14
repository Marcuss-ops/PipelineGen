// Package stockpipeline — destination_reconcile.go (Sept 2026).
//
// PR-STOCK-DESTINATION-RECONCILE: a Stock run publishes into a resolved Drive
// folder. Because the publication is content-addressed (idempotency key =
// clipID + sha256) and the Drive display name is a per-run ordinal
// (clip_%03d), re-running the same destination with a DIFFERENT plan leaves the
// artifacts of the previous plan behind:
//
//   - the reused files are renamed onto the new plan's names (see
//     drive.Uploader PutFile ConflictSkip+rename), so names stay honest;
//   - the files whose content is NOT in the new plan stay in the folder under
//     their old names, colliding with the new occupants (observed live: 12
//     files, clip_001/clip_003 present twice).
//
// This file owns the GC half: after a successful publish, every artifact in the
// destination folder that (a) was written by this pipeline — it carries the
// P0.6 `pipelinegen_idempotency_key` appProperty — and (b) is not part of the
// plan that was just published is removed. Operator-uploaded files are never
// touched: without the ownership marker they are invisible to the reconciler.
//
// Failure policy (deliberate, godlike/07 honest-limitation): reconciliation is
// a hygiene pass, NOT part of the artifact contract. A Drive list/trash failure
// is logged at Warn and does NOT fail the job — the published artifacts are
// already durable and correct; only stale siblings survive one extra run.
package stockpipeline

import (
	"context"

	"go.uber.org/zap"
)

// DestinationArtifact is one file observed in a run's destination folder.
type DestinationArtifact struct {
	FileID   string
	Name     string
	MimeType string
	// OwnedByPipeline is true when the file carries the pipeline's
	// idempotency-key appProperty, i.e. it was written by a Stock/YouTube
	// publication rather than by an operator.
	OwnedByPipeline bool
}

// DestinationArtifactStore is the narrow Drive-facing seam the reconciler
// needs. Implementations live in the platform layer (drive.Uploader).
type DestinationArtifactStore interface {
	ListArtifacts(ctx context.Context, folderID string) ([]DestinationArtifact, error)
	RemoveArtifact(ctx context.Context, fileID string) error
}

// DestinationReconciler removes the artifacts left by earlier plans in the
// folder the current plan publishes into.
type DestinationReconciler interface {
	// ReconcileDestination lists folderID and removes every pipeline-owned
	// artifact whose FileID is not in keepFileIDs. Returns a report of what it
	// observed and removed.
	ReconcileDestination(ctx context.Context, folderID string, keepFileIDs []string) (StaleArtifactReport, error)
}

// StaleArtifactReport is the audit shape of one reconciliation pass.
type StaleArtifactReport struct {
	FolderID     string
	Scanned      int
	Removed      int
	RemovedNames []string
}

// staleArtifacts is the pure selection rule of the reconciler: pipeline-owned,
// not part of the current plan, and de-duplicated by file ID. Sorting is
// intentionally left to the caller (Drive ordering is not stable); the file IDs
// of the keep set are authoritative.
func staleArtifacts(files []DestinationArtifact, keepFileIDs []string) []DestinationArtifact {
	keep := make(map[string]struct{}, len(keepFileIDs))
	for _, id := range keepFileIDs {
		if id != "" {
			keep[id] = struct{}{}
		}
	}
	out := make([]DestinationArtifact, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		if f.FileID == "" || !f.OwnedByPipeline {
			continue
		}
		if _, isKept := keep[f.FileID]; isKept {
			continue
		}
		if _, dup := seen[f.FileID]; dup {
			continue
		}
		seen[f.FileID] = struct{}{}
		out = append(out, f)
	}
	return out
}

// ReconcileDestination implements DestinationReconciler on top of the narrow
// store seam. It is a pure orchestration of list → select → remove, so both the
// selection and the removal order are unit-testable without Drive.
func ReconcileDestination(
	ctx context.Context,
	store DestinationArtifactStore,
	folderID string,
	keepFileIDs []string,
) (StaleArtifactReport, error) {
	report := StaleArtifactReport{FolderID: folderID}
	if store == nil || folderID == "" {
		return report, nil
	}
	files, err := store.ListArtifacts(ctx, folderID)
	if err != nil {
		return report, err
	}
	report.Scanned = len(files)
	for _, stale := range staleArtifacts(files, keepFileIDs) {
		if err := store.RemoveArtifact(ctx, stale.FileID); err != nil {
			return report, err
		}
		report.Removed++
		report.RemovedNames = append(report.RemovedNames, stale.Name)
	}
	return report, nil
}

// destinationFolderID resolves the folder that actually holds the run's
// artifacts: the published chunks' parent timestamp folder when known (that is
// where the files live), otherwise the resolved destination folder from the
// input. Empty ⇒ the caller skips reconciliation (nothing to scope the GC to).
func destinationFolderID(in *RunInput, chunks []ChunkState) string {
	for i := range chunks {
		if chunks[i].TimestampFolderID != "" {
			return chunks[i].TimestampFolderID
		}
	}
	if in == nil {
		return ""
	}
	if id := ResolvedFolderID(in); id != "" {
		return id
	}
	return ""
}

// reconcilePublishedDestination runs the post-publish hygiene pass. Nil
// reconciler (test fixtures, un-wired roots) is a no-op. Errors are logged and
// swallowed by design (see the file header): the artifacts are already durable.
func reconcilePublishedDestination(ctx context.Context, runner StepRunner, in *RunInput, chunks []ChunkState) {
	reconciler := runner.DestinationReconciler()
	if reconciler == nil || len(chunks) == 0 {
		return
	}
	folderID := destinationFolderID(in, chunks)
	if folderID == "" {
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.publish: destination reconcile skipped — no folder id on the published chunks")
		}
		return
	}
	keep := make([]string, 0, len(chunks))
	for i := range chunks {
		if chunks[i].RemoteFileID != "" {
			keep = append(keep, chunks[i].RemoteFileID)
		}
	}
	report, err := reconciler.ReconcileDestination(ctx, folderID, keep)
	if err != nil {
		if runner.Log() != nil {
			runner.Log().Warn("orchestrator: stock.publish: destination reconcile failed (artifacts already published; stale siblings survive this run)",
				zap.String("folder_id", folderID),
				zap.Error(err))
		}
		return
	}
	if runner.Log() != nil && report.Removed > 0 {
		runner.Log().Info("orchestrator: stock.publish: destination reconciled — stale artifacts from earlier plans removed",
			zap.String("folder_id", folderID),
			zap.Int("scanned", report.Scanned),
			zap.Int("removed", report.Removed),
			zap.Strings("removed_names", report.RemovedNames))
	}
}
