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

	"go.uber.org/zap"

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
// VoiceoverPublishCommand.FolderID is passed as PublishRequest.DestinationFolderID
// because the voiceover use case already resolved the canonical leaf folder via
// DestinationResolver. The Publisher must consume that resolved destination
// directly and must not re-resolve it through registry, config, or root overrides.
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
	// log is the canonical codebase-wide logger surface (godlike/06 SSOT).
	// Nil-safe at the constructor: callers may pass nil in test
	// contexts where the warning log is irrelevant. The adapter
	// internally nil-guards every log call (per the PR-P12
	// godlike/07 no-fake-availability degradation-signal path).
	log *zap.Logger
}

func newUseCasePublisherAdapter(publisher delivery.Publisher, log *zap.Logger) *useCasePublisherAdapter {
	if publisher == nil {
		panic("app.adapters_voiceover_use_case: newUseCasePublisherAdapter: publisher is required (delivery.Publisher)")
	}
	return &useCasePublisherAdapter{publisher: publisher, log: log}
}

// Publish routes the voiceover upload through the canonical delivery.Publisher
// (P1-5 cutover, July 2026). The legacy drive.Admin.UploadFile call-through is
// retired; all voiceover Drive writes now flow through the same Publisher seam
// as YouTube clips, stock, images, sound effects, and books.
//
// PR-VOICEOVER-DRIVE-DRIFT (2026-08-08): the canonical surface is now the
// SEMANTIC publish (req.ProjectID + req.Language). The legacy fallback
// chain (Project→FolderID→voiceover-ID) is RETIRED per godlike/07
// NO-FAKE-AVAILABILITY: an empty Project silently routed to
// req.RootFolderOverride, which surfaced upstream as
// "PathBuilder incomplete but RootFolderOverride is set (direct-to-
// root fallback)" — operators saw the uploaded audio land in the
// wrong Drive folder with no typed diagnostic.
//
// godlike/07 NO-FAKE-AVAILABILITY (post-drift-closure):
//   - Language is REQUIRED. Empty Language → typed sentinel
//     ErrVoiceoverPublishLanguageRequired (callers can errors.Is-probe).
//   - Project is REQUIRED. Empty Project → typed sentinel
//     ErrVoiceoverPublishProjectRequired (callers can errors.Is-probe).
//   - FolderID is OPTIONAL. Empty FolderID is OK (the canonical
//     semantic surface does NOT consume FolderID for voiceover
//     publishes — VoiceoverPath derives the path from
//     Project + Language). The pre-PR-12 silent-fallback
//     to RootFolderOverride is REMOVED.
//
// Field precedence summary (semantic-first per godlike/06 SSOT):
//  1. cmd.Project  → req.ProjectID (REQUIRED, fail-closed via Validate)
//  2. cmd.Language → req.Language   (REQUIRED, fail-closed via Validate)
//
// Validate() runs as the FIRST step (before the Publish call). The
// typed error envelope carries a wrapped sentinel (errors.Is-probable)
// so callers can probe without parsing string fragments. The
// fallback chain is RETIRED — the adapter no longer reads
// cmd.FolderID, no longer logs a Warn, no longer falls back to
// cmd.ID.
func (a *useCasePublisherAdapter) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	// godlike/07 fail-closed at the typed-sentinel boundary.
	// cmd.Validate() returns a wrapped sentinel (errors.Is-probable)
	// for the FIRST missing field per the precedence order documented
	// on the Validate method. Pre-Stage-3 gates (LocalPath, Filename,
	// ID) run BEFORE the semantic gates (Language, Project) so a
	// caller with multiple missing fields gets a deterministic
	// single error.
	if err := cmd.Validate(); err != nil {
		return "", fmt.Errorf(
			"useCasePublisherAdapter.Publish: validate: %w",
			err,
		)
	}

	req := delivery.PublishRequest{
		Destination: delivery.DestinationVoiceover,
		LocalPath:   cmd.LocalPath,
		Filename:    cmd.Filename,
		AssetID:     cmd.ID,
		ProjectID:   cmd.Project,
		Language:    cmd.Language,
		// FolderID is already the resolved destination selected by the
		// generation plan and DestinationResolver. DestinationFolderID
		// makes the canonical Publisher bypass registry/config/root
		// resolution and upload directly into this folder.
		DestinationFolderID: cmd.FolderID,
		// Voiceover is an immutable/versioned artifact. Resolve the
		// collision policy here so an already-resolved destination does
		// not require the Publisher to consult its registry again.
		ConflictPolicy: delivery.ConflictSkip,
	}
	res, err := a.publisher.Publish(ctx, req)
	if err != nil {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: %w", err)
	}
	if res.FileID == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: publisher returned empty FileID")
	}
	return res.FileID, nil
}

var _ voiceover.VoiceoverPublisher = (*useCasePublisherAdapter)(nil)
