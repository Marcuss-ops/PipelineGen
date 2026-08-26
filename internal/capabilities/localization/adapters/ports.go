// Package adapters provides the concrete adapters that satisfy the
// localization capability's typed ports using the canonical platform
// concretes (delivery.Publisher, drive.DocClient, detail.TextTrackRepository,
// rustexec render boundary). These are the production implementations wired
// once at the composition root; the capability package itself stays free of
// platform imports.
package adapters

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ── DriveUploader (→ delivery.Publisher) ────────────────────────────

// DriveUploader implements localization.DriveUploader via the canonical
// delivery.Publisher. It uploads a rendered localized clip into the
// caller-resolved folder with content-hash idempotency + ConflictSkip (an
// identical clip is never re-uploaded).
type DriveUploader struct {
	publisher delivery.Publisher
}

// NewDriveUploader builds the Drive uploader adapter. Fail-closed at call
// time: the nil checks live in the Upload methods.
func NewDriveUploader(publisher delivery.Publisher) *DriveUploader {
	return &DriveUploader{publisher: publisher}
}

func (u *DriveUploader) Upload(ctx context.Context, in localization.DriveUploadInput) (*localization.DriveUploadResult, error) {
	if u == nil || u.publisher == nil {
		return nil, fmt.Errorf("localization: drive publisher not wired")
	}
	if strings.TrimSpace(in.LocalPath) == "" || strings.TrimSpace(in.Filename) == "" || strings.TrimSpace(in.FolderID) == "" {
		return nil, fmt.Errorf("localization: drive upload input is incomplete")
	}
	// The filename "<clip>.<lang>.mp4" is the deterministic artifact identity;
	// the idempotency key folds it + the content hash so a re-run reuses the
	// same Drive file.
	artifactID := strings.TrimSuffix(in.Filename, filepath.Ext(in.Filename))
	result, err := u.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: in.FolderID,
		LocalPath:           in.LocalPath,
		Filename:            in.Filename,
		Language:            in.Language,
		ContentHash:         in.ContentHash,
		SizeBytes:           in.SizeBytes,
		// The script output folder is part of artifact identity. Without it,
		// rerunning a script can ConflictSkip onto the original clip's Drive
		// file because the same clip/hash exists elsewhere.
		IdempotencyKey: delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, artifactID+"|folder:"+in.FolderID, in.ContentHash, 1),
		ConflictPolicy: delivery.ConflictSkip,
	})
	if err != nil {
		return nil, fmt.Errorf("localization: publish rendered clip: %w", err)
	}
	if result == nil || result.FileID == "" {
		return nil, fmt.Errorf("localization: publish rendered clip: empty Drive result")
	}
	return &localization.DriveUploadResult{FileID: result.FileID, Link: result.WebViewLink}, nil
}

func (u *DriveUploader) UploadSubtitle(ctx context.Context, in localization.SubtitleUploadInput) (*localization.DriveUploadResult, error) {
	if u == nil || u.publisher == nil {
		return nil, fmt.Errorf("localization: subtitle Drive publisher not wired")
	}
	if strings.TrimSpace(in.LocalPath) == "" || strings.TrimSpace(in.Filename) == "" || strings.TrimSpace(in.FolderID) == "" {
		return nil, fmt.Errorf("localization: subtitle upload input is incomplete")
	}
	artifactID := strings.TrimSuffix(in.Filename, filepath.Ext(in.Filename))
	result, err := u.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:         delivery.DestinationClipMetadata,
		DestinationFolderID: in.FolderID,
		LocalPath:           in.LocalPath,
		Filename:            in.Filename,
		Language:            in.Language,
		ContentHash:         in.ContentHash,
		SizeBytes:           in.SizeBytes,
		IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, "subtitle-"+artifactID+"|folder:"+in.FolderID, in.ContentHash, 1),
		ConflictPolicy:      delivery.ConflictSkip,
	})
	if err != nil {
		return nil, fmt.Errorf("localization: publish subtitle: %w", err)
	}
	if result == nil || result.FileID == "" {
		return nil, fmt.Errorf("localization: publish subtitle: empty Drive result")
	}
	return &localization.DriveUploadResult{FileID: result.FileID, Link: result.WebViewLink}, nil
}

var _ localization.DriveUploader = (*DriveUploader)(nil)

// ── DocPublisher (→ drive.DocClient) ────────────────────────────────

// DocPublisher implements localization.DocPublisher via the canonical
// drive.DocClient. The rendered HTML manifest is published idempotently
// (force refreshes an existing doc's content).
type DocPublisher struct {
	doc drive.DocClient
}

// NewDocPublisher builds the Doc publisher adapter.
func NewDocPublisher(doc drive.DocClient) *DocPublisher {
	return &DocPublisher{doc: doc}
}

func (p *DocPublisher) Publish(ctx context.Context, in localization.DocPublishInput) (*localization.DocPublishResult, error) {
	if p == nil || p.doc == nil {
		return nil, fmt.Errorf("localization: doc client not wired")
	}
	doc, err := p.doc.CreateDocIdempotent(ctx, in.Title, in.Content, in.FolderID, in.IdempotencyKey, in.Force)
	if err != nil {
		return nil, fmt.Errorf("localization: publish doc: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("localization: publish doc: empty result")
	}
	return &localization.DocPublishResult{ID: doc.ID, Link: doc.URL}, nil
}

var _ localization.DocPublisher = (*DocPublisher)(nil)

// ── TrackResolver (→ detail.TextTrackRepository) ─────────────────────

// TrackResolver implements localization.TrackResolver via the canonical
// detail.TextTrackRepository READY lookup. The returned reference is
// (TrackID, TextHash) — the plan stores the reference, never the text.
type TrackResolver struct {
	repo detail.TextTrackRepository
}

// NewTrackResolver builds the track resolver adapter.
func NewTrackResolver(repo detail.TextTrackRepository) *TrackResolver {
	return &TrackResolver{repo: repo}
}

func (r *TrackResolver) ResolveTrack(ctx context.Context, assetID, language string, kind detail.TextTrackKind) (*localization.TrackRef, error) {
	if r == nil || r.repo == nil {
		return nil, fmt.Errorf("localization: text track repository not wired")
	}
	track, _, err := r.repo.FindReady(ctx, assetID, language, kind)
	if err != nil {
		return nil, fmt.Errorf("localization: find ready track %s/%s: %w", assetID, language, err)
	}
	if track == nil {
		return nil, fmt.Errorf("localization: no READY track for asset %q language %q", assetID, language)
	}
	hash := track.TextHash
	if hash == "" {
		hash = detail.TextHash(track.TextContent, language, kind)
	}
	return &localization.TrackRef{TrackID: track.ID, SHA256: hash}, nil
}

var _ localization.TrackResolver = (*TrackResolver)(nil)
