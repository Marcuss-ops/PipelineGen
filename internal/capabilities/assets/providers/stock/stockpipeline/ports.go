// Package stockpipeline — ports.go slim orchestrator (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Per PR6 spec (Pattern 0 + Pattern 8): the application layer decides WHICH
// clips receive transitions/effects and WHAT the encoding policy is.
// It does NOT know how FFmpeg builds the filter_complex, runs the binary, or
// assembles the codec args — all that lives in the infrastructure layer
// behind the canonical typed ports declared in this directory.
//
// Import-boundary invariant (verified by `go vet`):
//
//	go vet ./internal/capabilities/assets/providers/stock/...
//
// must NOT import `internal/platform/media/ffmpeg` OR
// `internal/platform/process`. Both are infra concerns; the app layer
// only depends on the typed ports declared in the companion files below.
//
// Port surface split (per godlike/06 SSOT one-canonical-owner-per-fact):
//
//   - render_ports.go — application-layer render + cutter + transition
//   - clip surface (StockRenderer + VideoCutter + their
//     DTOs + the TransitionRegistry catalog + the Clip
//     DTO + the noOp test fixtures + the 2 var _ pins
//     for the no-op concretes).
//   - source_ports.go — source-discovery narrow port (ChannelLister for
//     YouTube channel listing + the var _ pin locking
//     *downloader.YTDLPDownloader to the port).
//   - job_ports.go    — job-side narrow infra ports (3 narrow Pattern 0
//     interfaces scoped to the methods the stock
//     pipeline actually invokes: stockAssetIndexUpserter
//   - stockClipsSearchTermUpdater + stockChunkDispatcher).
//   - step_publish.go — the destination-reconcile adapter (DriveReaderPort →
//     DestinationArtifactStore → DestinationReconciler); folded in from
//     destination_reconcile_port.go (2026-09-15) so the registered hotspot
//     gains no production file.
package stockpipeline

import (
	"context"
)

// ── Destination-reconcile port adapter (PR-STOCK-DESTINATION-RECONCILE) ──
//
// The Drive-backed DestinationArtifactStore built on the stock DriveReaderPort.
// Keeping the store on the application port (rather than reaching into
// internal/platform/drive from this package) preserves the import-boundary
// discipline of service_types.go: the composition root adapts *drive.Uploader
// once, and this surface never learns a Drive concept beyond "list children of
// a folder" and "remove one file".

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
