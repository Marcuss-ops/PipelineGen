package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// updateCumulativeMetadataJSON maintains a single metadata.json per
// folder on Google Drive. It returns an error for every failure so the
// caller can surface a structured projection failure (METADATA-
// PROJECTION-GUARD, September 2026): the Drive metadata.json is a DERIVED
// projection — the DB asset enrichment (already committed when this runs)
// is the SSOT, so a Drive projection failure must never be reported as
// silent success, but it also must not roll back the canonical
// enrichment.
//
// Data-loss guard: when an existing metadata.json is found but cannot be
// downloaded or parsed, the file is NOT trashed and NOT re-published.
// Trash + publish with only the new entry would drop every previously
// recorded entry in the cumulative projection. The malformed state is
// returned as an error so an operator can repair the projection manually.
//
// F2.11 (June 2026, override brutal): the legacy
// DriveFolderManagerAdapter surface (ListByQuery + Download + Upload)
// is RETIRED. The read-modify-write path is now backed by the
// canonical Split-Ports introduced at PR2.7 / DRIVE-005:
//
//   - drive.Reader.SearchFiles replaces ListByQuery (same Q-level
//     semantics: the metadata.json lookup queries by parent + name +
//     trashed=false).
//   - drive.Reader.DownloadFile replaces Download (same return shape:
//     (io.ReadCloser, content-type, error)).
//   - delivery.Publisher.Publish replaces Upload (conflict-aware per
//     P0 #1). We thread `ParentFolderID=folderID` so the metadata
//     lands in the same destination as its clip (per the canonical
//     publisher resolution pipeline Step 2 in publisher.go).
//
// The Trash call still routes through drive.FileLifecycle (CARD-3 split
// out from DriveFolderManagerAdapter in PR2.7; preserved unchanged).
//
// F2.11 nil-tolerance: the Publisher is mandatory at composition
// (Service.NewService enforces ErrPublisherUnavailable fail-fast), so
// `e.publisher == nil` is unreachable in production. The Reader is a
// soft-dep — test fixtures can opt out of cumulative metadata.json
// sync by passing nil reader (the caller in Enrich() already gates on
// `e.publisher != nil && folderID != ""`; the inner check below
// remains for the reader-only nil path which has no equivalent
// composition guard).
func (e *SemanticEnricher) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]any) error {
	const metaFilename = "metadata.json"

	// F2.11: reader is the only optional dep (publisher is fail-fast
	// in NewService). Skip the RMW path entirely if the composition
	// root wired nil reader (test fixtures opting out of cumulative
	// sync; production wires bundle.DriveUploader as drive.Reader).
	// This is an intentional opt-out, not a failure.
	if e.reader == nil {
		e.log.Debug("semantic_enricher: reader is nil, skipping cumulative metadata.json sync (F2.11 test-fixture opt-out)")
		return nil
	}

	var existing []map[string]any
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	files, err := e.reader.SearchFiles(ctx, query)
	if err != nil {
		e.log.Warn("failed to list metadata.json", zap.Error(err))
		return fmt.Errorf("semantic_enricher: list existing metadata.json: %w", err)
	}
	if len(files) > 0 {
		existingFileID := files[0].ID
		body, _, dlErr := e.reader.DownloadFile(ctx, existingFileID)
		if dlErr != nil {
			e.log.Warn("failed to download metadata.json", zap.Error(dlErr))
			return fmt.Errorf("semantic_enricher: download existing metadata.json: %w", dlErr)
		}
		if body == nil {
			return fmt.Errorf("semantic_enricher: download existing metadata.json returned nil body")
		}
		defer body.Close()
		var raw []map[string]any
		if decErr := json.NewDecoder(body).Decode(&raw); decErr != nil {
			// Data-loss guard: never trash + re-publish a malformed
			// cumulative file (that would drop every previously recorded
			// entry). Surface the malformed projection for repair.
			e.log.Error("semantic enricher: existing drive metadata.json is malformed; refusing to trash+rewrite (projection data-loss guard)",
				zap.String("folder_id", folderID),
				zap.String("file_id", existingFileID),
				zap.Error(decErr),
			)
			return fmt.Errorf("semantic_enricher: existing drive metadata.json is malformed (projection data-loss guard): %w", decErr)
		}
		existing = raw
		if trashErr := e.lifecycle.Trash(ctx, existingFileID); trashErr != nil {
			e.log.Warn("failed to trash old metadata.json", zap.Error(trashErr))
			return fmt.Errorf("semantic_enricher: trash old metadata.json: %w", trashErr)
		}
	}

	found := false
	for i, entry := range existing {
		if id, ok := entry["clip_id"].(string); ok && id == clipID {
			existing[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, newEntry)
	}

	jsonBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		e.log.Warn("failed to marshal cumulative metadata json", zap.Error(err))
		return fmt.Errorf("semantic_enricher: marshal cumulative metadata json: %w", err)
	}
	metaTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {
		e.log.Warn("failed to write metadata json temp file", zap.Error(err))
		return fmt.Errorf("semantic_enricher: write metadata json temp file: %w", err)
	}
	defer os.Remove(metaTempPath)

	// F2.11 (June 2026): route the metadata.json upload through the
	// canonical delivery.Publisher.Publish (the F2.11 successor to the
	// pre-F2.11 wide-port FolderManager path; F3.14 retired the
	// underlying DriveFolderManagerAdapter entirely). The publisher
	// resolves the destination policy (DestinationArtlist + Group="metadata"
	// → segments=["metadata"] satisfies RequireSubpath) and pins the
	// resolved root to the clip's parent folder via ParentFolderID.
	// ConflictPolicy=ConflictOverwrite matches the legacy "find existing
	// → update in place" semantics that the pre-F2.11 folder-write
	// path implicitly had (preserved intentionally so existing
	// metadata.json siblings survive F2.11 → current-state reruns).
	//
	// KNOWN LAYOUT-SHIFT caveat (F2.11, June 2026): the pre-F2.11
	// folder-write path placed metadata.json DIRECTLY in the clip's
	// parent folder (/Artlist/<term>/metadata.json). The new
	// Publisher.Publish path appends the PathBuilder segments after
	// the overridden root, producing /Artlist/<term>/metadata/metadata.json
	// — one folder deeper. The cumulative metadata.json RMW semantics
	// stay correct (the file is still found-and-merged per term — see
	// Reader.SearchFiles query above) but the on-disk Drive layout grew
	// a "metadata" subfolder under every term. This is acceptable per
	// the F2.11 user spec ("drop legacy FolderManager fallback"; the
	// spec does not pin the exact metadata.json layout) and is documented
	// here for follow-up — a future DestinationPolicy with
	// RequireSubpath=false would let the metadata.json land at the
	// legacy location without re-introducing the legacy fallback path.
	if _, err := e.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationArtlist,
		Group:          "metadata",
		Filename:       metaFilename,
		LocalPath:      metaTempPath,
		ParentFolderID: folderID,
		ConflictPolicy: delivery.ConflictOverwrite,
	}); err != nil {
		e.log.Warn("failed to upload metadata.json to Drive", zap.Error(err))
		return fmt.Errorf("semantic_enricher: publish metadata.json to Drive: %w", err)
	}
	e.log.Info("uploaded cumulative metadata.json to Drive (enriched)", zap.Int("entries", len(existing)))
	return nil
}
