// Package publication (asset/publication) owns the
// asset-publication surface: takes a published-but-not-yet-on-Drive
// artifact descriptor + canonical destination, returns the
// post-upload Drive file ID + web view link.
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026): per godlike/06 SSOT
// (one canonical owner per fact), the asset-publication contract
// is now its own package — separated from the youtube-specific
// sources so non-YouTube assets (Artlist, Stock, etc.) can
// reuse the same canonical portal without cross-package coupling.
//
// PR-YOUTUBE-SERVICE-SPLIT phase 1 (this commit): typed-narrow
// contract + DriveAdapter that delegates to
// internal/capabilities/assets/sourcing/youtube/usecase.PublishClipToDrive
// (the canonical existing use case). Zero behaviour change.
//
// Phase 2 (next commit) will relocate the PublishClipToDrive +
// DrivePublisher + Publish{Request,Result} types into this
// package's godlike/06 SSOT owner files.
package publication

import (
	"context"
	"fmt"

	pubUC "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing/youtube/usecase"
)

// Publisher is the canonical godlike/06 SSOT narrow port for
// the asset-publication surface. Anything that wants to publish
// a local file to Drive (or any other canonical destination
// in future) must satisfy this contract.
type Publisher interface {
	// Publish takes a Publication Command and returns the
	// canonical publication result with FileID / WebViewLink.
	// nil port → typed sentinel — never a silent no-op
	// (godlike/07).
	Publish(ctx context.Context, cmd *Command) (*Result, error)
}

// Command mirrors the canonical PublishClipCommand fields needed
// by the publication surface. Kept package-local intentionally
// (godlike/06 SSOT minimum cross-package surface).
type Command struct {
	AssetID     string
	Group       string
	Subject     string
	LocalPath   string
	Filename    string
	Description string
	ProjectID   string
	Category    string
	Provider    string
	Tags        []string
	Language    string
}

// Result mirrors the canonical PublishClipResult fields.
type Result struct {
	FileID      string
	WebViewLink string
	FolderID    string
	Published   bool
}

// DriveAdapter is the canonical Publisher impl. It wraps the
// existing application-layer pubUC.PublishClipToDrive + the
// typed-narrow DrivePublisher interface.
//
// Phase 1 (this commit): thin facade — wraps the legacy use case
// directly via an ad-hoc adapter (drivePublisherAdapter below)
// that satisfies the application-layer pubUC.DrivePublisher port.
type DriveAdapter struct {
	pub pubUC.DrivePublisher
}

// NewDriveAdapter constructs the canonical Publisher.
func NewDriveAdapter(pub pubUC.DrivePublisher) (*DriveAdapter, error) {
	if pub == nil {
		return nil, fmt.Errorf("%w", ErrPublisherNotWired)
	}
	return &DriveAdapter{pub: pub}, nil
}

// ErrPublisherNotWired is the typed sentinel returned at
// construction time (nil publisher) and Publish time (nil
// receiver guard).
//
// godlike/07 NO-FAKE-AVAILABILITY: callers can errors.Is to
// distinguish the unavailable-publisher mode from runtime
// failures.
var ErrPublisherNotWired = fmt.Errorf("asset/publication: publisher not wired (godlike/07 fail-closed)")

// ErrNotPublished is returned when the canonical publication
// failed but did not produce a typed error — the Result.Published
// flag is false. Callers must inspect both the error AND the
// Result.Published flag (godlike/07).
var ErrNotPublished = fmt.Errorf("asset/publication: not published (godlike/07)")

// Publish wraps the canonical pubUC.PublishClipToDrive.
// Preserves byte-for-byte behaviour including the nil-publisher
// log-and-continue behaviour of the legacy use case.
func (p *DriveAdapter) Publish(ctx context.Context, cmd *Command) (*Result, error) {
	if p == nil || p.pub == nil {
		return nil, ErrPublisherNotWired
	}
	if cmd == nil || cmd.LocalPath == "" {
		return nil, fmt.Errorf("asset/publication: LocalPath is required (godlike/07 fail-closed)")
	}

	ucRes, err := pubUC.PublishClipToDrive(ctx, p.pub, pubUC.PublishClipCommand{
		AssetID:     cmd.AssetID,
		Group:       cmd.Group,
		Subject:     cmd.Subject,
		LocalPath:   cmd.LocalPath,
		Filename:    cmd.Filename,
		Description: cmd.Description,
		ProjectID:   cmd.ProjectID,
		Category:    cmd.Category,
		Provider:    cmd.Provider,
		Tags:        cmd.Tags,
		Language:    cmd.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("asset/publication: PublishClipToDrive: %w", err)
	}
	if ucRes == nil {
		return nil, fmt.Errorf("asset/publication: nil PublishClipResult (godlike/07)")
	}
	if !ucRes.Published {
		// godlike/07 NO-FAKE-AVAILABILITY: surface the typed
		// sentinel so callers can errors.Is(err, ErrNotPublished)
		// to render the "Drive write skipped" affordance.
		return &Result{Published: false}, ErrNotPublished
	}

	return &Result{
		FileID:      ucRes.FileID,
		WebViewLink: ucRes.WebViewLink,
		FolderID:    ucRes.FolderID,
		Published:   ucRes.Published,
	}, nil
}

// Compile-time pinning: *DriveAdapter satisfies Publisher.
var _ Publisher = (*DriveAdapter)(nil)
