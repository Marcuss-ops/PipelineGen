// Package lifecycle — service_voiceover.go: voiceover-specific lifecycle
// methods extracted from service.go (PR-LIFECYCLE-SPLIT, July 2026).
//
// UploadOnly (Phase 1 of the 2-phase split) and
// UpsertVoiceoverProjectionTx (Phase 2) are the canonical Drive-upload
// + SQLite-projection surfaces for the voiceover pipeline.
package lifecycle

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// UploadOnly uploads a local file to Drive WITHOUT any database
// write. This is Phase 1 of the new canonical VOICEOVER 2-PHASE
// SPLIT (P0.7 Wave 21, June 2026 — Step 9/12 finalizer unification).
//
// Difference vs. ProcessAsset: ProcessAsset uploads AND persists
// to media_assets (via finalizer.Finalize + registry.UpsertMedia);
// UploadOnly stops at the Drive surface and returns the canonical
// upload URLs. The caller (voiceover.Service.destinationStage) is
// responsible for marking BatchItem Status=StatusUploaded on
// success and for routing Drive upload failures through
// FailureUpload at the Stage-2 fail() contract.
//
// Atomicity note: this method writes NOTHING to SQLite. Persistence
// happens separately via finalizeStage's tx-scoped write chain
// (voiceover.persistence.Repository.* + outbox + the new
// UpsertVoiceoverProjectionTx). The pre-fix bug was: ProcessAsset
// wrote media_assets at this point; finalizeStage then ALSO wrote
// voiceovers in a SECOND tx. A Drive upload success followed by an
// InsertTx failure would leave media_assets orphan (audio uploaded
// but listed-as-failed). Removing the ProcessAsset call from
// destinationStage eliminates this partial-save bug because
// NOTHING is persisted until finalizeStage's tx commits — a tx
// failure aborts the entire atomic-write, and the upload becomes
// a fire-and-detect orphan (handled by the replace-mode cleanup
// goroutine downstream).
//
// Layering note: this method does NOT touch the dedupe gate —
// the gate (PR-VO-B3 CountByDriveFileIDTx) runs in finalizeStage
// INSIDE the actual finalize tx, not here. A Drive upload of a
// duplicate is permitted at this step (upload == idempotent —
// the actual finalization gate is the visibility boundary).
//
// Returns nil error + empty DriveLink when upload is disabled
// (s.uploadPolicy.Enabled == false) so callers can still
// proceed through finalizeStage with the local-only path.
//
// F2.7 (June 2026): replaced drive.Admin.UploadFile with
// delivery.Publisher.Publish — every Drive write goes through
// the canonical DestinationRegistry/RequireSubpath/ConflictPolicy
// belt instead of bypassing it via the raw drive.Admin port.
// Caller-failure semantics preserved (UploadOnly returns err on
// any publisher failure; the caller surfaces FailureUpload on the
// voiceover BatchItem path).
func (s *Service) UploadOnly(ctx context.Context, input *FinalizeInput) (*UploadOnlyResult, error) {
	if input == nil {
		return nil, fmt.Errorf("lifecycle.UploadOnly: FinalizeInput is required (nil input)")
	}
	if s.publisher == nil {
		return nil, fmt.Errorf("lifecycle.UploadOnly: publisher not wired (composition root)")
	}

	driveLink := input.DriveLink
	driveFileID := input.DriveFileID
	downloadLink := input.DownloadLink

	if s.uploadPolicy.Enabled && driveLink == "" && input.LocalPath != "" {
		// F2.7: build the canonical delivery.PublishRequest. Routed
		// through DestinationRegistry + RequireSubpath + ConflictPolicy.
		filename := input.Filename
		if filename == "" {
			filename = filepath.Base(input.LocalPath)
		}
		// PR-P12-LIFECYCLE-SEMANTIC (July 2026): ParentFolderID
		// REMOVED. Same rationale as service.go — semantic routing via
		// Destination + Group + Subject + ProjectID + Language.
		pubReq := delivery.PublishRequest{
			Destination: input.Destination,
			LocalPath:   input.LocalPath,
			Filename:    filename,
			AssetID:     input.ID,
			Group:       input.Group,
			Subject:     input.Subject,
			ProjectID:   input.ProjectID,
			Language:    input.Language,
			Style:       input.Style,
		}
		pubRes, err := s.publisher.Publish(ctx, pubReq)
		if err != nil {
			if s.log != nil {
				s.log.Warn("lifecycle.UploadOnly: Drive upload failed (caller surfaces FailureUpload)",
					zap.String("id", input.ID),
					zap.String("destination", string(input.Destination)),
					zap.Error(err))
			}
			return nil, fmt.Errorf("lifecycle.UploadOnly: publisher.Publish: %w", err)
		}
		driveLink = pubRes.WebViewLink
		downloadLink = pubRes.DownloadLink
		if downloadLink == "" && pubRes.FileID != "" {
			downloadLink = "https://drive.google.com/uc?id=" + pubRes.FileID
		}
		driveFileID = pubRes.FileID

		if s.log != nil {
			s.log.Info("lifecycle.UploadOnly: Drive upload OK (Phase 1 of new 2-phase split)",
				zap.String("id", input.ID),
				zap.String("file_id", pubRes.FileID),
				zap.String("destination", string(input.Destination)),
				zap.String("action", string(pubRes.Action)))
		}
	}

	return &UploadOnlyResult{
		DriveLink:    driveLink,
		DriveFileID:  driveFileID,
		DownloadLink: downloadLink,
	}, nil
}

// UpsertVoiceoverProjectionTx writes the canonical media_assets
// projection row for a voiceover asset INSIDE the caller-owned tx.
// This is Phase 2 of the new canonical VOICEOVER 2-PHASE SPLIT
// (P0.7 Wave 21, June 2026 — Step 9/12 finalizer unification).
//
// Atomicity guarantee: the caller (voiceover.finalizeStage) holds
// the *sql.Tx from BeginTx → Commit. Inside that tx, three writes
// happen atomically:
//
//  1. voiceovers table UPSERT     → voiceover.persistence.Repository.InsertTx (existing)
//  2. media_assets projection UPSERT (this method)
//  3. asset.index.requested outbox → outboxEnqueuer.EnqueueIndexEvent (existing)
//
// All three commit together via tx.Commit() — partial-save is
// impossible. Pre-fix the same canonical content was written TWICE
// (ProcessAsset + finalizeStage) across TWO transactions; a
// failure between the two left an orphan row in media_assets.
//
// UPSERT semantics: ON CONFLICT (id) DO UPDATE SET. Idempotent
// on retry (a re-run of finalizeStage updates the projection
// columns in place, doesn't double-insert).
// `source` is forced to `voiceover` so the row is discoverable
// by the voiceover→media_assets SQL verification query:
// `SELECT 1 FROM media_assets WHERE id = ? AND source = 'voiceover'`.
//
// Layering note: this method EXPECTS the caller to have already
// verified that the BatchItem has a Drive link from Phase 1
// (else we persist a projection without a Drive URL — fail-closed
// at the voiceover.persistence.Repository layer per its own
// fail-fast policies; this method does not re-validate).
func (s *Service) UpsertVoiceoverProjectionTx(ctx context.Context, tx *sql.Tx, in *VoiceoverProjectionInput) error {
	_ = ctx
	_ = tx
	_ = in
	return fmt.Errorf("lifecycle.UpsertVoiceoverProjectionTx: legacy media_assets writer retired; use the canonical MediaCommitter")
}
