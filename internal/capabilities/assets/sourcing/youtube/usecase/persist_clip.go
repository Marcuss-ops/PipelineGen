// Package usecase — PersistClipAndEmitEvent is a narrow use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-3, July 2026).
//
// It complements PersistClipAndIndex (persist_index.go) by splitting the
// atomic upsert+outbox contract into two independent narrow ports:
//
//	ClipPersister  — upsert the clip record into media_assets
//	OutboxEmitter  — emit the asset.index.requested outbox event
//
// This decomposition lets callers persist without emitting, emit without
// re-persisting, or do both — giving the orchestrator maximum flexibility
// while keeping each port testable in isolation.
//
// Per AGENTS.md Pattern 0: the use case defines its own ClipPersister and
// OutboxEmitter interfaces (one method each). The concrete adapters live
// in the composition root (internal/app/).
//
// The existing ClipRecord type (persist_index.go) is reused as the canonical
// wire shape — godlike/06 SSOT one-canonical-owner-per-fact.
package assets

import (
	"context"
	"fmt"
	"time"
)

// ClipPersister upserts a clip into the media_assets table. The returned
// string is the canonical clip ID (may differ from input on dedup).
type ClipPersister interface {
	Upsert(ctx context.Context, clip ClipRecord) (string, error)
}

// OutboxEmitter emits the asset.index.requested outbox event for a clip.
type OutboxEmitter interface {
	EmitIndexEvent(ctx context.Context, clipID, contentHash string) error
}

// PersistAndEmitResult reports the outcome of PersistClipAndEmitEvent.
//
// ClipID is the canonical clip identifier (from the persister, or the
// command's ClipID when persister is nil). Persisted is true only when
// Upsert succeeded. EventEmitted is true only when EmitIndexEvent
// succeeded.
type PersistAndEmitResult struct {
	ClipID       string
	Persisted    bool
	EventEmitted bool
}

// PersistAndEmitCommand carries every input needed to persist a clip and
// emit its index event. Same fields as PersistClipCommand.
type PersistAndEmitCommand struct {
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

// PersistClipAndEmitEvent upserts a clip into media_assets and emits the
// asset.index.requested outbox event. Each port is independently optional
// (nil → step skipped, flag set to false). A non-nil port that returns an
// error aborts the sequence (fail-closed).
//
//  1. nil-persister → skip upsert; use cmd.ClipID as the canonical ID
//  2. persister.Upsert → build ClipRecord, delegate; on error, abort
//  3. nil-emitter → skip outbox event; EventEmitted = false
//  4. emitter.EmitIndexEvent → delegate; on error, abort
//
// The caller receives an explicit PersistAndEmitResult so partial success
// (Persisted=true, EventEmitted=false e.g. because emitter is nil) is
// transparent and auditable.
func PersistClipAndEmitEvent(ctx context.Context, persister ClipPersister, emitter OutboxEmitter, cmd PersistAndEmitCommand) (*PersistAndEmitResult, error) {
	result := &PersistAndEmitResult{ClipID: cmd.ClipID}

	// ── Step 1: Upsert ──────────────────────────────────────────
	if persister != nil {
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
		clipID, err := persister.Upsert(ctx, record)
		if err != nil {
			return result, fmt.Errorf("usecase.PersistClipAndEmitEvent: upsert: %w", err)
		}
		result.ClipID = clipID
		result.Persisted = true
	}

	// ── Step 2: Emit outbox event ───────────────────────────────
	if emitter != nil {
		if err := emitter.EmitIndexEvent(ctx, result.ClipID, cmd.LegacyFileMD5); err != nil {
			return result, fmt.Errorf("usecase.PersistClipAndEmitEvent: emit event: %w", err)
		}
		result.EventEmitted = true
	}

	return result, nil
}
