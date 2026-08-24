// Package usecase — PublishClipToDrive is a narrow use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-3, July 2026).
//
// It owns the Drive-publish step (step 6+9) of the legacy 14-step Register
// pipeline: resolve the destination folder and upload the media file to
// Google Drive via the canonical delivery.Publisher port. The mandatory-Drive
// check (step 9a) is a caller-side concern and stays in Register(); this
// use case only wraps the Publish call with typed diagnostics.
//
// Per AGENTS.md Pattern 0 + Pattern 5: the use case depends on a narrow
// DrivePublisher port (single method: Publish) rather than importing the
// full delivery.Publisher surface. The adapter lives in the composition
// root (internal/app/).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the canonical
// owner of YouTube-clip Drive-publish orchestration for the sourcing/youtube
// registration pipeline. The canonical Drive write canal is
// delivery.Publisher.Publish (FASE 5 since June 2026).
package assets

import (
	"context"
	"fmt"
)

// PublishClipCommand carries every input needed to publish a clip to Drive.
// It mirrors the fields that Register() passes to delivery.Publisher.Publish.
//
// PR-YT-CLIP-SEMANTIC-LOCATION-FIX (July 2026): Category, Provider, Tags,
// and Language added so the semantic-location metadata from the API payload
// reaches the Drive Publisher. Previously only Group/Subject/RootFolder were
// threaded, causing location.category="Boxe" to be silently dropped before
// the Publisher could build the correct Drive folder hierarchy.
type PublishClipCommand struct {
	AssetID     string   // clipID derived from videoID + file hash
	Group       string   // logical group (e.g. actor / project)
	Subject     string   // folder segment (e.g. videoID-titleSlug)
	RootFolder  string   // backward-compat override for cmd.FolderID
	LocalPath   string   // path to the downloaded .mp4 on disk
	Filename    string   // Drive filename (e.g. "dQw4w9WgXcQ - title.mp4")
	Description string   // human-readable Drive file description
	ProjectID   string   // canonical project umbrella (e.g. "boxing-doc-2026")
	Category    string   // semantic category (e.g. "Boxe", "Personaggi")
	Provider    string   // upstream source (e.g. "youtube", "pexels")
	Tags        []string // semantic keywords for Qdrant payload
	Language    string   // BCP-47 language tag (optional)
}

// PublishClipResult is the canonical output of the Drive-publish step.
// When Published is false the caller should treat the clip as local-only
// or retry (the mandatory-Drive check in Register() gates this).
type PublishClipResult struct {
	FileID      string
	WebViewLink string
	FolderID    string
	Published   bool
}

// DrivePublisher is the narrow port for publishing a file to Drive.
// There is exactly ONE method: Publish. The use case does not need
// ResolveFolder or any other Drive surface — it only uploads files.
//
// The concrete adapter (composition root, internal/app/) wraps
// delivery.Publisher.Publish and translates the canonical
// delivery.PublishRequest / delivery.PublishResult to the local
// PublishRequest / PublishResult shapes owned by this package.
type DrivePublisher interface {
	Publish(ctx context.Context, req PublishRequest) (*PublishResult, error)
}

// PublishRequest is the use-case-owned wire shape for a Drive publish call.
// It mirrors delivery.PublishRequest but is owned by this package so the
// use case does not import delivery.
//
// PR-YT-CLIP-SEMANTIC-LOCATION-FIX: Category, Provider, Tags, Language added.
type PublishRequest struct {
	Destination string // canonical destination key (e.g. "youtube-clip")
	LocalPath   string
	Filename    string
	Description string
	AssetID     string
	ProjectID   string // canonical project umbrella (e.g. "boxing-doc-2026")
	Group       string
	Subject     string
	RootFolder  string   // caller-specified root folder override (e.g. from Location resolution)
	Category    string   // semantic category (e.g. "Boxe")
	Provider    string   // upstream source (e.g. "youtube")
	Tags        []string // semantic keywords for Qdrant
	Language    string   // BCP-47 language tag
}

// PublishResult is the use-case-owned wire shape for a Drive publish outcome.
type PublishResult struct {
	FileID      string
	WebViewLink string
	FolderID    string
}

// PublishClipToDrive publishes a clip to Google Drive via the canonical
// Publisher port. It is a thin orchestration function:
//
//  1. nil-publisher guard → returns Published=false (log-and-continue)
//  2. Build PublishRequest from the command fields
//  3. Delegate to pub.Publish
//  4. On success → Published=true with FileID / WebViewLink / FolderID
//  5. On failure → Published=false with wrapped error
//
// The mandatory-Drive check (RequireDrive) is a caller-side concern and
// is NOT enforced here — the caller inspects result.Published and decides
// whether to fail the entire registration or proceed local-only.
func PublishClipToDrive(ctx context.Context, pub DrivePublisher, cmd PublishClipCommand) (*PublishClipResult, error) {
	if pub == nil {
		return &PublishClipResult{Published: false}, nil
	}

	// PR-YT-CLIP-SEMANTIC-LOCATION-FIX: thread Category/Provider/Tags/Language
	// so the Drive Publisher's YouTubeClipPath can build the correct folder
	// hierarchy from semantic metadata rather than relying solely on Group.
	result, err := pub.Publish(ctx, PublishRequest{
		Destination: "youtube-clip",
		LocalPath:   cmd.LocalPath,
		Filename:    cmd.Filename,
		Description: cmd.Description,
		AssetID:     cmd.AssetID,
		ProjectID:   cmd.ProjectID,
		Group:       cmd.Group,
		Subject:     cmd.Subject,
		RootFolder:  cmd.RootFolder,
		Category:    cmd.Category,
		Provider:    cmd.Provider,
		Tags:        cmd.Tags,
		Language:    cmd.Language,
	})
	if err != nil {
		return &PublishClipResult{Published: false}, fmt.Errorf("usecase.PublishClipToDrive: %w", err)
	}

	return &PublishClipResult{
		FileID:      result.FileID,
		WebViewLink: result.WebViewLink,
		FolderID:    result.FolderID,
		Published:   true,
	}, nil
}
