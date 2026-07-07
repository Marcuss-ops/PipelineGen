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
// PR-P12-VOICEOVER-SEMANTIC-FIELDS (July 2026): the canonical surface is
// now the SEMANTIC publish (req.ProjectID + req.Language). The legacy
// RootFolderOverride path is preserved ONLY as a backward-compat
// fallback when cmd.Project is empty AND cmd.FolderID is non-empty
// (i.e. pre-PR-12 callers that resolved the folder manually).
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - Language is REQUIRED. Empty Language → typed sentinel
//     ErrVoiceoverPublishLanguageRequired (callers can errors.Is-probe).
//   - Project is PREFERRED. Empty Project → fallback chain:
//     (a) cmd.FolderID non-empty → req.RootFolderOverride = cmd.FolderID
//     (backward-compat with pre-PR-12 callers)
//     (b) cmd.FolderID empty → req.ProjectID = cmd.ID (graceful
//     degradation so the Publisher's VoiceoverPath validation
//     never blocks on an empty ProjectID; operator-warning log
//     captures the degradation signal).
//
// Field precedence summary (semantic-first per godlike/06 SSOT):
//  1. cmd.Project  → req.ProjectID
//  2. cmd.FolderID → req.RootFolderOverride (only when Project empty)
//  3. cmd.ID       → req.ProjectID (last-resort fallback)
func (a *useCasePublisherAdapter) Publish(ctx context.Context, cmd voiceover.VoiceoverPublishCommand) (string, error) {
	if cmd.LocalPath == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty LocalPath (use case supplied no local payload)")
	}
	if cmd.Filename == "" {
		return "", fmt.Errorf("useCasePublisherAdapter.Publish: empty Filename (use case supplied no display name)")
	}
	// godlike/07 fail-closed: Language is required for the canonical
	// semantic voiceover publish (VoiceoverPath builds {project}/{language}/).
	// Empty Language MUST surface the typed sentinel so callers can probe
	// via errors.Is — no silent fallback to a default language.
	if cmd.Language == "" {
		return "", fmt.Errorf(
			"useCasePublisherAdapter.Publish: empty Language (voiceover publish requires Language for canonical semantic routing per PR-P12-VOICEOVER-SEMANTIC-FIELDS): %w",
			voiceover.ErrVoiceoverPublishLanguageRequired,
		)
	}
	// FolderID is OPTIONAL post-PR-12 — it was the legacy root-folder
	// override. Removed the pre-PR-12 hard fail-closed at empty
	// FolderID; the adapter now derives ProjectID via the fallback
	// chain documented above.

	req := delivery.PublishRequest{
		Destination: delivery.DestinationVoiceover,
		LocalPath:   cmd.LocalPath,
		Filename:    cmd.Filename,
		AssetID:     cmd.ID,
		Language:    cmd.Language,
	}
	switch {
	case cmd.Project != "":
		// Canonical semantic path: caller populated the Project field
		// (typically from GenerateVoiceoverItemCommand.Project, threaded
		// from the API request). Publisher builds the {project}/{language}/
		// subpath via VoiceoverPath.
		req.ProjectID = cmd.Project
	case cmd.FolderID != "":
		// Backward-compat fallback: pre-PR-12 callers that resolved the
		// folder manually and pass a FolderID. The Publisher bypasses
		// DestinationRegistry root-folder resolution and writes to the
		// pre-resolved root.
		req.RootFolderOverride = cmd.FolderID
	default:
		// Graceful degradation: both Project and FolderID are empty.
		// Use the canonical voiceover ID (cmd.ID) as the ProjectID so
		// VoiceoverPath's {project}/{language}/ subpath still resolves.
		// The warning log captures the godlike/07 no-fake-availability
		// signal so operators can detect callers that haven't migrated
		// to the semantic surface.
		req.ProjectID = cmd.ID
		if a.log != nil {
			a.log.Warn("voiceover publisher: neither Project nor FolderID set, using voiceover ID as ProjectID fallback (this is a godlike/07 degradation signal — set Project for canonical semantic routing)")
		}
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
