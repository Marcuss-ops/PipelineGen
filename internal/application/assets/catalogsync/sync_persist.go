package catalogsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// upsertPreservingExisting upserts a MediaAsset while preserving fields that
// the catalog sync should not overwrite (hash, local_path, metadata, tags).
// Routes through the canonical outbox dispatcher: the media_assets UPDATE
// and the outbox_events INSERT commit atomically.
func (s *Service) upsertPreservingExisting(ctx context.Context, repo *assets.ClipsRepository, clip *asset.Asset) error {
	if repo == nil || clip == nil {
		return nil
	}

	if existing, err := repo.GetClip(ctx, clip.ID); err == nil && existing != nil {
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
