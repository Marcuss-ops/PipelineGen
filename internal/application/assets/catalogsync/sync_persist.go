package catalogsync

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
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

	// Canonical PR1 path: atomic upsert + outbox enqueue via dispatcher.
	// Atomicity is guaranteed by the dispatcher: either both the
	// media_assets UPDATE and the outbox_events INSERT commit, or neither does.
	// Folders go through the dispatcher; Dispatcher handles IsFolder by
	// skipping the outbox enqueue, so embedding is never requested for
	// folder metadata rows.
	if err := s.dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("dispatcher.EnqueueAndIndex %s: %w", clip.ID, err)
	}

	s.writeAssetIndex(ctx, clip)

	return nil
}

// writeAssetIndex writes a unified asset_index record (best-effort, runs
// after the canonical media_assets / outbox_events commit). Failure
// is logged but never propagated: a stale asset_index row is preferable
// to rolling back the canonical outbox state. Identical payload across
// dispatcher + legacy paths — extracted here so the two branches in
// upsertPreservingExisting stay in lockstep.
func (s *Service) writeAssetIndex(ctx context.Context, clip *asset.Asset) {
	if s.assetIndex == nil {
		return
	}
	rec := &assetindex.AssetRecord{
		AssetID:   string(clip.Source) + "_" + clip.ID,
		AssetType: string(clip.MediaType),
		Source:    string(clip.Source),
		SourceID:  clip.ID,
		GroupName: clip.Group,
		LocalPath: clip.LocalPath(),
		DriveLink: clip.DriveLink(),
		FileHash:  clip.FileHash(),
		Status:    "ready",
		Metadata:  clip.MetadataJSON(),
		CreatedAt: clip.CreatedAt,
		UpdatedAt: clip.UpdatedAt,
	}
	if err := s.assetIndex.Upsert(ctx, rec); err != nil {
		s.log.Warn("failed to write clip to asset_index", zap.Error(err))
	}
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
