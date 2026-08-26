// Package usecase — PersistClipAndIndex is a narrow use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-4, July 2026).
//
// It owns the DB-persist step (step 11 in the legacy Register pipeline,
// step 8 in the post-CLIP-DECOM-5 orchestrator): build the canonical
// ClipRecord from the resolved metadata and delegate to the
// IndexDispatcherPort for an atomic upsert + outbox-event emission
// in a single SQLite transaction.
//
// Per AGENTS.md Pattern 0 + Pattern 5: the use case depends on a narrow
// ClipIndexer port (single method: EnqueueAndIndex) rather than importing
// sourcing.IndexDispatcherPort directly. The adapter lives in the
// composition root (internal/app/).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the canonical
// owner of YouTube-clip DB persistence for the sourcing/youtube registration
// pipeline. The atomic upsert + outbox contract is enforced here.
package usecase

import (
	"context"
	"fmt"
	"time"
)

// PersistClipCommand carries every input needed to persist a clip to the
// canonical media_assets table and emit the asset.index.requested outbox event.
type PersistClipCommand struct {
	ClipID         string // yt_<videoID>_<hash8>
	Name           string // resolved display name
	Filename       string // Drive filename (e.g. "dQw4w9WgXcQ - title.mp4")
	Source         string // e.g. "youtube-manual"
	SourceURL      string
	SourceProvider string
	SourceVideoID  string
	StartSec       float64
	EndSec         float64
	Category       string   // user-supplied category
	Tags           []string // user-supplied tags
	DurationSec    int      // clip duration in whole seconds
	LocalPath      string   // path to the downloaded .mp4 on disk
	LegacyFileMD5  string   // MD5 hex digest (content hash for supersede gate)
	DriveLink      string   // Google Drive web view link (empty when not published)
	DriveFileID    string   // Google Drive file ID (empty when not published)

	// Rich metadata fields (RICH-METADATA-QDRANT-VERIFY, July 2026).
	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
}

// ClipIndexer is the narrow port for persisting a clip and emitting the
// outbox event atomically. There is exactly ONE method: EnqueueAndIndex.
//
// The concrete adapter (composition root) wraps sourcing.IndexDispatcherPort
// and translates the local ClipRecord to sourcing.ExistingClip.
type ClipIndexer interface {
	EnqueueAndIndex(ctx context.Context, clip ClipRecord, contentHash string) error
}

// ClipRecord is the use-case-owned wire shape for a clip row in media_assets.
// It mirrors sourcing.ExistingClip but is owned by this package so the use
// case does not import sourcing.
type ClipRecord struct {
	ID             string
	Name           string
	Filename       string
	Source         string
	SourceURL      string
	SourceProvider string
	SourceVideoID  string
	StartSec       float64
	EndSec         float64
	Category       string
	Tags           []string
	Duration       time.Duration
	LocalPath      string
	LegacyFileMD5  string
	DriveLink      string
	DriveFileID    string

	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
}

// PersistClipAndIndex persists a clip to the media_assets table and emits
// the asset.index.requested outbox event in a single atomic transaction.
//
//  1. nil-indexer guard → returns typed error (fail-closed per QDRANT-002
//     asset-mutation isolation — no silent-skip in production)
//  2. Build ClipRecord from the command fields
//  3. Delegate to indexer.EnqueueAndIndex
//  4. Wrap errors with usecase prefix for caller diagnostics
func PersistClipAndIndex(ctx context.Context, indexer ClipIndexer, cmd PersistClipCommand) error {
	if indexer == nil {
		return fmt.Errorf("usecase.PersistClipAndIndex: indexer is nil (QDRANT-asset-mutation isolation forbids the legacy UpsertClip fallback; wire IndexDispatcherPort at composition time)")
	}

	record := ClipRecord{
		ID:              cmd.ClipID,
		Name:            cmd.Name,
		Filename:        cmd.Filename,
		Source:          cmd.Source,
		SourceURL:       cmd.SourceURL,
		SourceProvider:  cmd.SourceProvider,
		SourceVideoID:   cmd.SourceVideoID,
		StartSec:        cmd.StartSec,
		EndSec:          cmd.EndSec,
		Category:        cmd.Category,
		Tags:            append([]string(nil), cmd.Tags...),
		Duration:        time.Duration(cmd.DurationSec) * time.Second,
		LocalPath:       cmd.LocalPath,
		LegacyFileMD5:   cmd.LegacyFileMD5,
		DriveLink:       cmd.DriveLink,
		DriveFileID:     cmd.DriveFileID,
		Summary:         cmd.Summary,
		Topics:          append([]string(nil), cmd.Topics...),
		Speakers:        append([]string(nil), cmd.Speakers...),
		MentionedPeople: append([]string(nil), cmd.MentionedPeople...),
		Hook:            cmd.Hook,
	}

	if err := indexer.EnqueueAndIndex(ctx, record, cmd.LegacyFileMD5); err != nil {
		return fmt.Errorf("usecase.PersistClipAndIndex: %w", err)
	}
	return nil
}
