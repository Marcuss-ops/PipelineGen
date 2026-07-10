// Package voiceover — Stage 3 publish extraction (PR-VO-USECASE-PROCESS-DRY
// decomposition, per YouTube DoD wave process_segment.go split precedent).
//
// publishStage owns the metadata-building + idempotency-key derivation +
// VoiceoverPublisher.Publish call. The orchestrator's Execute method
// delegates Stage 3 here and uses the returned publishStageResult to
// populate Stage 4's FinalizeCommand.
//
// godlike/06 SSOT: publishStageResult is the SINGLE canonical shape
// carrying Stage 3 output to Stage 4 input. Unexported (package-internal).
package voiceover

import (
	"context"
	"encoding/json"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// publishStageResult carries Stage 3 output to Stage 4 input.
type publishStageResult struct {
	MetaJSON []byte
	IdemKey  string
}

// publishStage executes Stage 3: metadata building + idempotency key
// derivation + VoiceoverPublisher.Publish (Drive upload).
//
// Returns publishStageResult carrying MetaJSON + IdemKey for Stage 4's
// FinalizeCommand. On failure, returns a typed error that the orchestrator
// maps to "upload_failed:" prefix for per-item stage classification.
func (u *ProcessSegmentUseCase) publishStage(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	log *zap.Logger,
) (*publishStageResult, error) {
	// Build metadata JSON envelope.
	metaBuf := map[string]any{
		"text_hash":    cmd.TextHash,
		"text_preview": textutil.Truncate(cmd.Text, 100),
		"language":     cmd.Language,
		"voice":        out.Voice,
		"strategy":     cmd.Strategy,
		"cleaned_path": out.CleanedPath,
	}
	if cmd.Dest != nil && !cmd.Dest.StyleGroup.IsEmpty() {
		metaBuf["style_group"] = cmd.Dest.StyleGroup
	}
	mergeUserMetadata(metaBuf, cmd.Dest, cmd.Metadata, u.deps.Logger)
	metaJSON, _ := json.Marshal(metaBuf)

	// Derive deterministic idempotency key.
	var idemKey string
	if cmd.JobID != "" {
		idemKey = BuildVoiceoverIdempotencyKey(cmd.JobID, cmd.Language, cmd.TextHash)
	}

	// Publish to Drive.
	uploadPath := out.CleanedPath
	if uploadPath == "" {
		uploadPath = out.LocalPath
	}

	emitPublish := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "publish", string(cmd.Language))
	fileID, err := u.deps.Publisher.Publish(ctx, VoiceoverPublishCommand{
		ID:             cmd.ID,
		LocalPath:      uploadPath,
		Filename:       cmd.Filename,
		FolderID:       cmd.Dest.FolderID,
		Project:        cmd.Project,
		Language:       string(cmd.Language),
		IdempotencyKey: idemKey,
	})
	if err != nil {
		emitPublish("failed")
		return nil, err
	}
	emitPublish("completed")

	out.DriveFileID = fileID
	out.DriveLink = CanonicalDriveWebURL(fileID)
	out.DownloadLink = CanonicalDriveDownloadURL(fileID)

	return &publishStageResult{MetaJSON: metaJSON, IdemKey: idemKey}, nil
}
