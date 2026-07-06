// Package app — voiceover VoiceoverPublisher adapter
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Capability cluster: DRIVE external I/O bridges.
//
// VoiceoverPublisher ← delivery.Publisher.Publish (P1-5 cutover, July 2026)
//
// Azione #9 follow-up (July 2026): newVoiceoverDriveAdapter + DriveUploaderPort
// interface removed from ports.go. voiceoverDriveAdapter struct also removed —
// drive.Admin now satisfies jobsoutbox.VoiceoverCleanupDriver structurally,
// passed directly from composition.go.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// ─────────────────────────────────────────────────────────────────────
// VoiceoverPublisher adapter (P1-5 cutover, July 2026).
//
// Bridges delivery.Publisher → voiceover.VoiceoverPublisher.Publish.
// The legacy drive.Admin.UploadFile call-through (E1 cutover, June 2026)
// is retired; the adapter now routes all voiceover Drive writes through
// the canonical Publisher seam shared by YouTube clips, stock, images,
// sound effects, and books.
//
// VoiceoverPublishCommand.FolderID is passed as PublishRequest.RootFolderOverride
// so the Publisher bypasses DestinationRegistry root-folder resolution
// (the voiceover use case already resolved the canonical folder via
// DestinationResolver).
//
// Publish returns the canonical Drive file ID; downstream callers
// (process_voiceover_item.go::Execute + usecase.go::processOneLanguage)
// reconstruct DriveLink + DownloadLink via the canonical
// CanonicalDriveWebURL / CanonicalDriveDownloadURL helpers in
// voiceover/ports.go.
//
// Fail-closed: nil publisher panics at construction (fail-fast per
// AGENTS.md WireUp pattern).
// ─────────────────────────────────────────────────────────────────────

type useCasePublisherAdapter struct {
	publisher delivery.Publisher
}

func newUseCasePublisherAdapter(publisher delivery.Publisher) *useCasePublisherAdapter {
	if publisher == nil {
		panic("app.adapters_voiceover_use_case: newUseCasePublisherAdapter: publisher is required (delivery.Publisher)")
	}
	return &useCasePublisherAdapter{publisher: publisher}
}

// Publish routes the voiceover upload through the canonical delivery.Publisher
// (P1-5 cutover, July 2026). The legacy drive.Admin.UploadFile call-through is
// retired; all voiceover Drive writes now flow through the same Publisher seam
// as YouTube clips, stock, images, sound effects, and books.
//
// VoiceoverPublishCommand carries a pre-resolved FolderID — the
// publisher's RootFolderOverride field receives it so the Publisher bypasses
// DestinationRegistry root-folder resolution (the voiceover use case already
// resolved the canonical folder via DestinationResolver).
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
	res, err := a.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:        delivery.DestinationVoiceover,
		LocalPath:          cmd.LocalPath,
		Filename:           cmd.Filename,
		AssetID:            cmd.ID,
		RootFolderOverride: cmd.FolderID,
	})
	if err != nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: %w", err)
	}
	if res.FileID == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: publisher returned empty FileID")
	}
	return res.FileID, nil
}

var _ voiceover.VoiceoverPublisher = (*useCasePublisherAdapter)(nil)
