// Package stockpipeline — destination_reconcile_port.go (Sept 2026).
//
// The Drive-backed DestinationArtifactStore built on the stock DriveReaderPort.
// Keeping the store on the application port (rather than reaching into
// internal/platform/drive from this package) preserves the import-boundary
// discipline of service_types.go: the composition root adapts *drive.Uploader
// once, and this file never learns a Drive concept beyond "list children of a
// folder" and "remove one file".
package stockpipeline

import "context"

// portDestinationStore adapts DriveReaderPort to DestinationArtifactStore.
type portDestinationStore struct {
	reader DriveReaderPort
}

func (s portDestinationStore) ListArtifacts(ctx context.Context, folderID string) ([]DestinationArtifact, error) {
	infos, err := s.reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]DestinationArtifact, 0, len(infos))
	for _, info := range infos {
		out = append(out, DestinationArtifact{
			FileID:          info.ID,
			Name:            info.Name,
			MimeType:        info.MimeType,
			OwnedByPipeline: info.PipelineOwned,
		})
	}
	return out, nil
}

func (s portDestinationStore) RemoveArtifact(ctx context.Context, fileID string) error {
	return s.reader.TrashFile(ctx, fileID)
}

// portDestinationReconciler is the DestinationReconciler the production Service
// attaches when a Drive read port is wired. Nil reader ⇒ nil reconciler, which
// the publish step treats as "hygiene disabled".
func portDestinationReconciler(reader DriveReaderPort) DestinationReconciler {
	if reader == nil {
		return nil
	}
	return destinationReconciler{store: portDestinationStore{reader: reader}}
}

// destinationReconciler is the canonical implementation of the
// DestinationReconciler contract: list → pure selection → remove.
type destinationReconciler struct {
	store DestinationArtifactStore
}

func (r destinationReconciler) ReconcileDestination(
	ctx context.Context,
	folderID string,
	keepFileIDs []string,
) (StaleArtifactReport, error) {
	return ReconcileDestination(ctx, r.store, folderID, keepFileIDs)
}
