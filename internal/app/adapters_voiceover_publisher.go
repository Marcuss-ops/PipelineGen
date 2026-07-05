// Package app — voiceover VoiceoverPublisher + DriveUploaderPort
// adapters (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Capability cluster: DRIVE external I/O bridges. Both adapters wrap
// drive.Admin (the canonical 4-port Drive surface from DRIVE-005) to
// satisfy the narrow ports declared in
// internal/application/voiceover/ports.go:
//
// VoiceoverPublisher   ← drive.Admin.UploadFile (E1 cutover, June 2026)
// DriveUploaderPort    ← drive.Admin.DeleteFile (post-commit cleanup)
//
// Fail-closed: nil admin panics at constructor time (fail-fast per
// AGENTS.md WireUp pattern), returning nil from newVoiceoverDriveAdapter
// when admin is unconfigured is acceptable because the consumer guards nil
// at the call site (process_voiceover_item.go::Execute).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverPublisher adapter (E1 cutover, June 2026).
//
// Bridges *drive.Uploader (which satisfies drive.Admin via the
// compile-time assertion in internal/infrastructure/drive/ports.go)
// → voiceover.VoiceoverPublisher.Publish. The upload-only port
// replaces the pre-E1 voiceover.AssetLifecycle.Upload (which
// delegated to lifecycle.Service.ProcessAsset and bundled Drive
// upload + dedupe + asset-record persistence). The new Publisher
// is upload-only:
//   - no SQLite write (process_voiceover_item.go::Execute owns
//     the canonical row INSERT inside its atomic-swap tx),
//   - no dedupe gate (Executor paths already pre-resolve the
//     canonical voiceover ID via buildVoiceoverID),
//   - no asset-record projection (media_assets projection is
//     written by lifecycle.Service.UpsertVoiceoverProjectionTx
//     inside the same tx).
//
// Publish returns the canonical Drive file ID; downstream callers
// (process_voiceover_item.go::Execute + usecase.go::processOneLanguage)
// reconstruct DriveLink + DownloadLink via the canonical
// CanonicalDriveWebURL / CanonicalDriveDownloadURL helpers in
// voiceover/ports.go.
//
// Fail-closed: nil admin panics at construction (fail-fast per
// AGENTS.md WireUp pattern). The UploadFile retry policy itself
// is owned by the production drive.Uploader (3-attempt exponential
// backoff via pkg/retry; see internal/infrastructure/drive/uploader.go).
// ─────────────────────────────────────────────────────────────────────

type useCasePublisherAdapter struct {
	admin drive.Admin
}

func newUseCasePublisherAdapter(admin drive.Admin) *useCasePublisherAdapter {
	if admin == nil {
		panic("app.adapters_voiceover_use_case: newUseCasePublisherAdapter: admin is required (*drive.Uploader implementing drive.Admin)")
	}
	return &useCasePublisherAdapter{admin: admin}
}

// TODO(Fase 3.5): migrate from drive.Admin.UploadFile to delivery.Publisher.Publish.
// The useCasePublisherAdapter currently calls a.admin.UploadFile() through the
// drive.Admin Pattern 0 port. The canonical upload path is
// delivery.Publisher.Publish(ctx, PublishRequest{Destination: DestinationVoiceover, ...})
// which adds conflict policy, filename normalisation, and structured PublishResult.
func (a *useCasePublisherAdapter) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	if cmd.LocalPath == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty LocalPath (use case supplied no local payload)")
	}
	if cmd.FolderID == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty FolderID (use case supplied no destination folder)")
	}
	if cmd.Filename == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty Filename (use case supplied no display name)")
	}
	res, err := a.admin.UploadFile(ctx, cmd.LocalPath, cmd.FolderID, cmd.Filename)
	if err != nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: drive.UploadFile: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: drive.UploadFile returned nil UploadResult")
	}
	return res.FileID, nil
}

var _ voiceover.VoiceoverPublisher = (*useCasePublisherAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// voiceoverDriveAdapter - Drive port adapter for voiceover (moved from
// voiceover_adapters_drive.go, Phase 5 consolidation, June 2026).
// Wraps drive.Admin to satisfy voiceover.DriveUploaderPort.
// ─────────────────────────────────────────────────────────────────────

type voiceoverDriveAdapter struct {
	drive drive.Admin
}

var _ voiceover.DriveUploaderPort = (*voiceoverDriveAdapter)(nil)

func newVoiceoverDriveAdapter(admin drive.Admin) voiceover.DriveUploaderPort {
	if admin == nil {
		return nil
	}
	return &voiceoverDriveAdapter{drive: admin}
}

func (a *voiceoverDriveAdapter) DeleteFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return fmt.Errorf("voiceoverDriveAdapter.DeleteFile: fileID is required")
	}
	if a == nil || a.drive == nil {
		return fmt.Errorf("voiceoverDriveAdapter: drive not wired")
	}
	return a.drive.DeleteFile(ctx, fileID)
}
