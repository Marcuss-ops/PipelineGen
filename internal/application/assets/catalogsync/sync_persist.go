package catalogsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// upsertPreservingExisting upserts a MediaAsset while preserving fields that
// the catalog sync should not overwrite (hash, local_path, metadata, tags).
// Routes through the canonical outbox dispatcher: the media_assets UPDATE
// and the outbox_events INSERT commit atomically.
func (s *Service) upsertPreservingExisting(ctx context.Context, repo *assets.ClipsRepository, clip *asset.Asset) error {
	if repo == nil || clip == nil {
		return nil
	}

	var alreadyIndexedUnchanged bool
	if existing, err := repo.GetClip(ctx, clip.ID); err == nil && existing != nil {
		// Capture the freshly computed remote fingerprint (set by
		// sync_recursive.go via remoteFileFingerprint) BEFORE the
		// preserve-existing overwrite below. The re-index guard must
		// compare the CURRENT Drive fingerprint against the stored one
		// — comparing after the overwrite would compare the existing
		// row against itself and skip re-indexing even when the remote
		// content actually changed.
		freshFingerprint := clip.FileHash()
		if existing.FileHash() != "" {
			clip.SetFileHash(existing.FileHash())
		}
		if existing.LocalPath() != "" {
			clip.SetLocalPath(existing.LocalPath())
		}
		if len(existing.Metadata) > 0 {
			clip.Metadata = existing.Metadata
		}
		if !existing.CreatedAt.IsZero() {
			clip.CreatedAt = existing.CreatedAt
		}
		clip.Tags = mergeTags(clip.Tags, existing.Tags)

		// Producer-side re-index guard (July 2026): a bulk folder
		// re-sync must NOT re-enqueue asset.index.requested for rows
		// that are already INDEXED with unchanged content. The outbox
		// event_key dedup would suppress the duplicate event anyway, but
		// skipping it producer-side avoids the wasted upsert+enqueue
		// round-trip on every catalog re-sync of a large tree. When the
		// remote fingerprint differs from the stored one, the content
		// changed and the row must be re-indexed normally.
		if existing.FileHash() != "" && existing.FileHash() == freshFingerprint {
			if state, stErr := repo.GetIndexState(ctx, clip.ID); stErr == nil && state == asset.StateIndexed {
				alreadyIndexedUnchanged = true
			}
		}
	}

	if s.dispatcher == nil {
		return fmt.Errorf("upsertPreservingExisting: dispatcher is nil — production wiring required")
	}
	// Drive catalog rows use the Drive file ID as their stable asset ID.
	// Apply this after merging existing metadata so a stale metadata blob
	// cannot erase the canonical drive_file_id on re-sync.
	if strings.TrimSpace(clip.ID) != "" {
		clip.SetDriveFileID(clip.ID)
	}

	// Canonical PR1 path: atomic upsert + outbox enqueue via dispatcher.
	// Atomicity is guaranteed by the dispatcher: either both the
	// media_assets UPDATE and the outbox_events INSERT commit, or neither does.
	// Folders go through the dispatcher; Dispatcher handles IsFolder by
	// skipping the outbox enqueue, so embedding is never requested for
	// folder metadata rows.
	//
	// Wave G (June 2026) DECOUPLING — the post-commit writeAssetIndex
	// call is removed. The asset_index row was a best-effort mirror
	// of media_assets; media_assets + outbox_events are now the single
	// source of truth. Callers that need the asset_index view should
	// derive it from media_assets (the canonical projection), not
	// duplicate the write here.
	if alreadyIndexedUnchanged {
		// Refresh the row (drive links, names, timestamps) but skip the
		// redundant index request — see the guard above.
		if err := s.dispatcher.UpsertClipNoIndex(ctx, clip); err != nil {
			return fmt.Errorf("dispatcher.UpsertClipNoIndex %s: %w", clip.ID, err)
		}
		return nil
	}
	if err := s.dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("dispatcher.EnqueueAndIndex %s: %w", clip.ID, err)
	}

	return nil
}

func mergeTags(base, extra []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(base)+len(extra))
	add := func(items []string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	add(base)
	add(extra)
	return out
}
