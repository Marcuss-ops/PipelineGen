package app

// localization_ports.go wires the localization capability's remaining
// infrastructure ports to concrete adapters:
//
//   - DriveUploader   → delivery.Publisher (canonical Drive upload canal)
//   - DocPublisher    → drive.DocClient (canonical Google Docs canal)
//   - TrackResolver   → asset.TextTrackRepository (canonical READY-track lookup)
//
// godlike/06 SSOT (one canonical owner per fact): each adapter is a thin
// bridge to the ONE canonical concrete implementation already wired in the
// composition root — no second Drive/docs/track path is introduced.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── DriveUploader (→ delivery.Publisher) ────────────────────────────

// localizationDriveUploader implements localization.DriveUploader via the
// canonical delivery.Publisher. It uploads a rendered localized clip into the
// caller-resolved folder with content-hash idempotency + ConflictSkip (an
// identical clip is never re-uploaded).
type localizationDriveUploader struct {
	publisher delivery.Publisher
}

func (u *localizationDriveUploader) Upload(ctx context.Context, in localization.DriveUploadInput) (*localization.DriveUploadResult, error) {
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

func (u *localizationDriveUploader) UploadSubtitle(ctx context.Context, in localization.SubtitleUploadInput) (*localization.DriveUploadResult, error) {
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

var _ localization.DriveUploader = (*localizationDriveUploader)(nil)

// ── DocPublisher (→ drive.DocClient) ────────────────────────────────

// localizationDocPublisher implements localization.DocPublisher via the
// canonical drive.DocClient. The rendered HTML manifest is published
// idempotently (force refreshes an existing doc's content).
type localizationDocPublisher struct {
	doc drive.DocClient
}

func (p *localizationDocPublisher) Publish(ctx context.Context, in localization.DocPublishInput) (*localization.DocPublishResult, error) {
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

var _ localization.DocPublisher = (*localizationDocPublisher)(nil)

// ── TrackResolver (→ asset.TextTrackRepository) ─────────────────────

// localizationTrackResolver implements localization.TrackResolver via the
// canonical asset.TextTrackRepository READY lookup. The returned reference is
// (TrackID, TextHash) — the plan stores the reference, never the text.
type localizationTrackResolver struct {
	repo asset.TextTrackRepository
}

func (r *localizationTrackResolver) ResolveTrack(ctx context.Context, assetID, language string, kind asset.TextTrackKind) (*localization.TrackRef, error) {
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
		hash = asset.TextHash(track.TextContent, language, kind)
	}
	return &localization.TrackRef{TrackID: track.ID, SHA256: hash}, nil
}

var _ localization.TrackResolver = (*localizationTrackResolver)(nil)
