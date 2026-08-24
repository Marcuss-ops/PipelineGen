package catalogsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// upsertPreservingExisting upserts a MediaAsset while preserving fields that
// the catalog sync should not overwrite (hash, local_path, metadata, tags).
// Routes through the canonical outbox dispatcher: the media_assets UPDATE
// and the outbox_events INSERT commit atomically.
func (s *Service) upsertPreservingExisting(ctx context.Context, repo CatalogRepository, indexer AssetIndexer, clip *asset.Asset) error {
	if repo == nil {
		return fmt.Errorf("upsertPreservingExisting: repository is nil")
	}
	if indexer == nil {
		return fmt.Errorf("upsertPreservingExisting: asset indexer is nil")
	}
	if clip == nil {
		return fmt.Errorf("upsertPreservingExisting: asset is nil")
	}

	// Catalog topology is source-owned metadata. Preserve the freshly
	// scanned containing folder while merging enrichment/local fields from
	// the existing asset; otherwise a stale metadata blob can put a file's
	// own ID back into folder_id and make Drive sidecar publishing fail with
	// parentNotAFolder.
	incomingFolderID := clip.FolderID()
	incomingParentFolderID := clip.ParentFolderID()
	incomingFolderPath := clip.FolderPath()
	if existing, err := repo.GetClip(ctx, clip.ID); err == nil && existing != nil {
		if existing.LegacyFileMD5() != "" {
			clip.SetLegacyFileMD5(existing.LegacyFileMD5())
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
		clip.SetFolderID(incomingFolderID)
		clip.SetParentFolderID(incomingParentFolderID)
		clip.SetFolderPath(incomingFolderPath)

	}
	// Always re-emit the canonical index intent. The outbox event key is
	// deterministic and absorbs exact retries, while this deliberately
	// avoids trusting SQLite INDEXED as proof that the Qdrant projection
	// exists (the projection may be missing or stale).

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
	if err := s.dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
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
