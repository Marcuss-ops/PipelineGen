// Package voiceover — stage_destination.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// Stage 3 of the 5-stage voiceover pipeline: Drive upload via
// lifecycle.Service.UploadOnly. Populates BatchItem.DriveLink,
// DriveFileID, DownloadLink on success. Returns
// item.fail(FailureLifecycleUnavailable, …) when the composition
// root didn't wire lifecycleService, item.fail(FailureMissingFolder, …)
// when the resolved destination has no FolderID, and
// item.fail(FailureNoLocalPayload, …) when synthesizeStage produced
// no local path.
//
// Mechanical extraction from the pre-split stages.go (which
// carried 3 stages in 628 LoC). No behavior change in EXPAND.
// Compile-time lock: process_voiceover_item.go reads the same
// VoiceoverPublisher / VoiceoverPublishCommand / Language types
// via the ports package — preserved verbatim.
package voiceover

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"go.uber.org/zap"
)

// voiceoverProjectID extracts the project ID from a voiceover
// filename. The convention is:
//   - Strip the .mp3 extension.
//   - If a "_scene-N" segment marker is present, return everything
//     before it (the per-script project ID).
//   - Otherwise return the filename (minus .mp3).
//
// nil-safe (returns "" when filename is empty or whitespace-only).
func voiceoverProjectID(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		return ""
	}
	if idx := strings.Index(name, "_scene-"); idx > 0 {
		return strings.TrimSpace(name[:idx])
	}
	return strings.TrimSuffix(name, ".mp3")
}

// truncatePreview caps the text_preview metadata field at 100 chars
// to limit row size. Inline here (no textutil import) so
// stage_destination.go keeps a tight import surface.
func truncatePreview(s string) string {
	if len(s) <= 100 {
		return s
	}
	return s[:100]
}

// destinationStage is Stage 2 (Drive upload via Lifecycle). Wired
// between the stageLog("destination") wrappers in process.go.
//
// P0.7 Wave 21 (June 2026) — Step 9/12 finalizer unification: this
// stage now calls lifecycle.Service.UploadOnly (Drive only, NO DB
// writes). The previous ProcessAsset call wrote media_assets at
// Stage 2 and finalizeStage then ALSO wrote voiceovers in a SECOND
// tx — the partial-save bug pattern. Removing ProcessAsset from
// the Stage-2 surface eliminates the partial-save because NOTHING
// is persisted until finalizeStage's tx commits; a tx failure aborts
// the entire atomic-write, and the replace-mode cleanup goroutine
// handles the orphan Drive file downstream.
//
// On success populates DriveLink/DriveFileID/DownloadLink and
// advances Status to StatusUploaded. On error the item.fail
// plumbing surfaces a BatchItem with typed FailureUpload (matches
// the audit-P01 fail() contract — typed status, NOT legacy literal).
//
// PR-VO-TYPED-PRIMITIVES (July 2026): item.Language is the typed
// BCP-47 envelope; the downstream lifecycle.FinalizeInput.Language
// field is raw string, so convert at the typed→string wire-shape
// boundary.
func (s *Service) destinationStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	dest *ResolvedDestination,
	metaJSON []byte,
) BatchItem {
	if s.lifecycleService == nil {
		return item.fail(FailureLifecycleUnavailable,
			fmt.Errorf("%s: lifecycleService not wired (composition root)", restoreIdent))
	}
	if dest == nil || dest.FolderID == "" {
		return item.fail(FailureMissingFolder,
			fmt.Errorf("%s: destination has no FolderID (Stage 2 cannot upload)", restoreIdent))
	}
	if item.CleanedPath == "" && item.LocalPath == "" {
		return item.fail(FailureNoLocalPayload,
			fmt.Errorf("%s: synthesizeStage produced no local path (Stage 2 cannot upload)", restoreIdent))
	}

	localPath := item.CleanedPath
	if localPath == "" {
		localPath = item.LocalPath
	}

	finalInput := &lifecycle.FinalizeInput{
		ID:          item.ID,
		Name:        truncatePreview(req.Text),
		Filename:    item.Filename,
		LocalPath:   localPath,
		Destination: delivery.DestinationVoiceover,
		FolderID:    dest.FolderID,
		FolderPath:  dest.FolderPath,
		Source:      "voiceover",
		ProjectID:   voiceoverProjectID(item.Filename),
		// PR-VO-TYPED-PRIMITIVES (July 2026): the destination-stage
		// struct expects raw string Language; convert at the
		// typed→string wire-shape boundary.
		Language:     string(item.Language),
		Metadata:     string(metaJSON),
		RequireDrive: true,
	}

	// P0.7 2-PHASE SPLIT (Step 9/12): UploadOnly uploads to Drive
	// without writing to the DB. The phase-2 writes (voiceovers +
	// media_assets projection + outbox event) happen inside
	// finalizeStage's caller-owned tx. See lifecycle.Service.UploadOnly
	// for the atomicity rationale.
	result, err := s.lifecycleService.UploadOnly(ctx, finalInput)
	if err != nil {
		if s.log != nil {
			s.log.Warn("destinationStage: lifecycle.UploadOnly failed (Phase 1)",
				zap.String("restored", restoreIdent),
				zap.String("language", string(item.Language)),
				zap.Error(err))
		}
		return item.fail(FailureUpload, err)
	}

	item.DriveLink = result.DriveLink
	item.DriveFileID = result.DriveFileID
	item.DownloadLink = result.DownloadLink
	item.Status = StatusUploaded
	return item
}
