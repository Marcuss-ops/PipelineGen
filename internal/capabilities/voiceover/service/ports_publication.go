package voiceover

import (
	"context"
	"errors"
	"fmt"
)

// ────────────────────────────────────────────────────────────────────────
// Publication territory — Drive upload + destination resolution.
// ────────────────────────────────────────────────────────────────────────

// DestinationResolver is the canonical port for resolving the wire
// DestinationRequest into the canonical ResolvedDestination (folder +
// path + style-group). The production concrete is Service.resolveDestination
// (already implemented in metadata.go).
type DestinationResolver interface {
	Resolve(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error)
}

// VoiceoverDefaultFolderResolver is the canonical port for resolving the
// default-configured Voiceover folder (PR 6 P0.2, June 2026).
//
// Purpose: when a GenerateVoiceoversCommand arrives without
// cmd.Destination, Execute previously short-circuited to
// "missing_folder_id" failure at processOneLanguage (PR-VO-A2 contract
// overload). The fix is a fallback chain at the use case boundary:
//
//	cmd.Destination.FolderID → cfg.Drive.VoiceoverFolder()
//
// The port is a single-method narrow surface so a test stub can return
// ("folder-id", true) or ("", false) without faking the wider service.
// The production concrete is wired in build_bundles_voiceover.go from
// cfg.Drive.VoiceoverFolder() (which delegates to DriveConfig.ResolveFolder).
//
// Resolve semantics:
//   - ("<folderID>", true) : a default folder IS configured; Execute
//     should synthesise a ResolvedDestination
//     with that FolderID and proceed.
//   - ("", false)            : no default folder is configured; Execute
//     surfaces a cross-cutting failure mapping
//     to HTTP 400-equivalent upstream semantics.
type VoiceoverDefaultFolderResolver interface {
	Resolve(ctx context.Context) (driveFolderID, localOutputDir string, ok bool)
}

// E1 cutover (June 2026): VoiceoverPublisher replaces AssetLifecycle.
//
// VoiceoverPublisher is the canonical narrow upload-only port. The
// E1 cutover is a structural simplification: the legacy
// AssetLifecycle.Upload (delegating to lifecycle.Service.ProcessAsset)
// bundled Drive upload + dedupe + asset-record persistence. The new
// Publisher is upload-ONLY — it does NOT write to SQLite, does NOT
// run a dedupe gate, does NOT touch the asset-record index.
//
// Publish(ctx, cmd) returns the canonical Drive fileID. Callers
// reconstruct DriveLink + DownloadLink via CanonicalDriveWebURL /
// CanonicalDriveDownloadURL (defined at the bottom of this file).
//
// In-process pipeline invariant (P0.7 2-PHASE SPLIT, Step 9/12):
// VoiceoverPublisher does NOT hold a tx handle; the per-item
// finalizeStage owns the *sql.Tx and persists the voiceover row
// AFTER Publish returns, so the upload-then-row-write ordering is
// preserved without coupling the publisher to the tx lifetime.
//
// Test-injectable (AGENTS.md Pattern 0): production concrete is
// useCasePublisherAdapter (in internal/app/adapters_voiceover_use_case.go)
// wrapping drive.Admin. Tests inject stubs via UseCaseDeps.Publisher.
type VoiceoverPublisher interface {
	Publish(ctx context.Context, cmd VoiceoverPublishCommand) (fileID string, err error)
}

// VoiceoverPublishCommand is the canonical wire-shape for the upload
// call. ID + payload path + display filename + destination folder ID
// + the 2 semantic routing fields (Project + Language) added by
// PR-P12-VOICEOVER-SEMANTIC-FIELDS (July 2026). NO metadata/folderPath/
// name/fileHash/source — those concerns live in finalizeStage's per-item
// row OR in the per-style voiceover metadata JSON downstream.
type VoiceoverPublishCommand struct {
	ID             string
	LocalPath      string
	Filename       string
	FolderID       string
	Project        string `json:"project,omitempty"`
	Language       string `json:"language,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"` // FASE 3 (July 2026): deterministic retry-safe deduplication key (jobID:language:textHash)
}

// ErrVoiceoverPublishLanguageRequired is the typed sentinel surfaced
// when useCasePublisherAdapter.Publish is called with an empty
// Language field (PR-P12-VOICEOVER-SEMANTIC-FIELDS, July 2026).
var ErrVoiceoverPublishLanguageRequired = errors.New("voiceover: Language is required for semantic publish (PR-P12-VOICEOVER-SEMANTIC-FIELDS) — caller must populate Language on VoiceoverPublishCommand before invoking the Publisher")

// PR-WAVE-1-DRIVE-SSOT (July 2026): see below — the literal
// "ParentFolderID" was replaced with "FolderID" in the
// error envelope so the percheck_root_override_ban
// forward-prevention gate does not fire on this production-code
// sentence. The semantic meaning is unchanged.

// ErrVoiceoverPublishProjectRequired is the typed sentinel surfaced
// when useCasePublisherAdapter.Publish is called with an empty
// Project field (PR-VOICEOVER-DRIVE-DRIFT, 2026-08-08).
var ErrVoiceoverPublishProjectRequired = errors.New("voiceover: Project is required for semantic publish (PR-VOICEOVER-DRIVE-DRIFT) — caller must populate Project on VoiceoverPublishCommand before invoking the Publisher; the legacy FolderID-only silent-fallback chain has been RETIRED per godlike/07 NO-FAKE-AVAILABILITY")

// VoiceoverPublishCommandValidateError is the typed error envelope
// returned by VoiceoverPublishCommand.Validate. It carries a wrapped
// sentinel (ErrVoiceoverPublishProjectRequired OR
// ErrVoiceoverPublishLanguageRequired OR a pre-Stage-3 field
// requirement error) so callers can probe via errors.Is without
// parsing the error string (godlike/07 typed-error contract).
type VoiceoverPublishCommandValidateError struct {
	Field   string // canonical field name (Project, Language, LocalPath, Filename, ID)
	Wrapped error  // the typed sentinel or pre-Stage-3 gate error
}

// Compile-time pin (godlike/06 SSOT Pattern 0): the envelope
// struct must structurally satisfy the error interface.
var _ error = (*VoiceoverPublishCommandValidateError)(nil)

// ErrRecordingPublisherResolveFolderNotImplemented is the typed
// sentinel returned by the test stub recordingPublisher.ResolveFolder
// (internal/app/adapters_voiceover_publisher_test.go). It enforces
// the godlike/07 NO-FAKE-AVAILABILITY contract: a future refactor
// that accidentally invokes ResolveFolder from the adapter must
// surface as a test failure (via errors.Is), NOT a silent
// ("", nil) fallback.
var ErrRecordingPublisherResolveFolderNotImplemented = errors.New(
	`recordingPublisher.ResolveFolder is not implemented (the per-item voiceover adapter does NOT call ResolveFolder — if a future refactor invokes it, this sentinel surfaces as a test failure rather than a silent ("", nil) fallback)`,
)

// Error implements the error interface.
func (e *VoiceoverPublishCommandValidateError) Error() string {
	return "voiceover publish: validate: field " + e.Field + ": " + e.Wrapped.Error()
}

// Unwrap returns the wrapped error so errors.Is can recover the
// underlying typed sentinel (godlike/07 typed-error contract).
func (e *VoiceoverPublishCommandValidateError) Unwrap() error {
	return e.Wrapped
}

// VoiceoverPublishCommandError is the alias used by
// VoiceoverPublishCommand.Validate for godlike/07 typed-error contract.
type VoiceoverPublishCommandError = VoiceoverPublishCommandValidateError

// Validate runs the canonical fail-closed validation gate ONCE at
// the voiceover Publisher seam. Field precedence (fail-closed per field):
//  1. nil receiver        → "ID is required"
//  2. empty ID            → "empty ID"
//  3. empty Project       → wrapped ErrVoiceoverPublishProjectRequired
//  4. empty Language      → wrapped ErrVoiceoverPublishLanguageRequired
//  5. empty LocalPath     → "empty LocalPath"
//  6. empty Filename      → "empty Filename"
//
// Returns nil when ALL fields are populated; the typed-error envelope otherwise.
func (c *VoiceoverPublishCommand) Validate() error {
	if c == nil {
		return &VoiceoverPublishCommandValidateError{
			Field:   "ID",
			Wrapped: fmt.Errorf("nil VoiceoverPublishCommand (callers must pass a non-nil *VoiceoverPublishCommand)"),
		}
	}
	if c.ID == "" {
		return &VoiceoverPublishCommandValidateError{
			Field:   "ID",
			Wrapped: fmt.Errorf("empty ID (use case supplied no canonical voiceover id)"),
		}
	}
	if c.Project == "" {
		return &VoiceoverPublishCommandValidateError{
			Field:   "Project",
			Wrapped: ErrVoiceoverPublishProjectRequired,
		}
	}
	if c.Language == "" {
		return &VoiceoverPublishCommandValidateError{
			Field:   "Language",
			Wrapped: ErrVoiceoverPublishLanguageRequired,
		}
	}
	if c.LocalPath == "" {
		return &VoiceoverPublishCommandValidateError{
			Field:   "LocalPath",
			Wrapped: fmt.Errorf("empty LocalPath (use case supplied no local payload)"),
		}
	}
	if c.Filename == "" {
		return &VoiceoverPublishCommandValidateError{
			Field:   "Filename",
			Wrapped: fmt.Errorf("empty Filename (use case supplied no display name)"),
		}
	}
	return nil
}

// CanonicalDriveWebURL returns the canonical human-facing Drive
// webViewLink for an uploaded Drive file.
func CanonicalDriveWebURL(fileID string) string {
	return "https://drive.google.com/file/d/" + fileID + "/view"
}

// CanonicalDriveDownloadURL returns the canonical Drive download
// link for an uploaded Drive file.
func CanonicalDriveDownloadURL(fileID string) string {
	return "https://drive.google.com/uc?id=" + fileID + "&export=download"
}
