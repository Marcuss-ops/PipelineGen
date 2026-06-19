package catalogsync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	concurrent "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// upsertPreservingExisting upserts a MediaAsset while preserving fields that
// the catalog sync should not overwrite (hash, local_path, metadata, tags).
// Routes through the canonical outbox dispatcher when available, so the
// media_assets UPDATE and the outbox_events INSERT commit atomically.
func (s *Service) upsertPreservingExisting(ctx context.Context, repo *clips.Repository, clip *assets.Asset) error {
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

	// PR1: when the canonical outbox dispatcher is wired, route the
	// upsert + outbox enqueue through it. Atomicity is guaranteed by the
	// dispatcher: either both the media_assets UPDATE and the
	// outbox_events INSERT commit, or neither does. The previous
	// pattern of `repo.UpsertClip; concurrent.SafeGoFunc(IndexClip)`
	// violated atomicity — the goroutine could crash before IndexClip
	// ran, or start before the upsert committed (half-state visible to
	// readers). Folders still go through the dispatcher; Dispatcher handles
	// IsFolder by skipping the outbox enqueue, so embedding is never
	// requested for folder metadata rows.
	//
	// When s.dispatcher is nil (partial bring-up / tests) we fall back to
	// the legacy SafeGoFunc path so existing code keeps working.
	//
	// Concurrency: we read s.dispatcher directly without re-acquiring s.mu.
	// s.mu serialises the entry points (SyncAll, SyncSource, SyncFolderID)
	// that call into upsertPreservingExisting, so the field is a stable
	// snapshot for the duration of any sync. SetDispatcher is documented
	// "production wiring calls once at startup" — so even outside the Lock
	// window, the field has a happens-before relationship with any later
	// service start (handler registration happens after WireServices).
	dispatcher := s.dispatcher

	var upsertErr error
	if dispatcher != nil {
		upsertErr = dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash())
	} else {
		upsertErr = repo.UpsertClip(ctx, clip)
	}
	if upsertErr != nil {
		return fmt.Errorf("unsert %s: %w", clip.ID, upsertErr)
	}

	s.writeAssetIndex(ctx, clip)

	// Indexing trigger — only on the legacy path. On the dispatcher path
	// the outbox.Worker (configured in app/compose_integration.go) picks
	// up the new outbox_events row and the outboxevents Pool runs IndexClip,
	// preserving the same indexing behaviour, just delivered through the
	// outbox queue instead of a fire-and-forget goroutine. This eliminates
	// the race window where the goroutine could start before the upsert
	// committed, AND the half-state window where process exit would leak
	// a "metadata-written-but-no-embedding" row.
	if dispatcher == nil && s.clipIndexer != nil && s.clipIndexer.IsEnabled() && !clip.IsFolder() {
		concurrent.SafeGoFunc("catalog-indexing", clip.ID, func(id string) {
			indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			s.log.Debug("triggering automatic vector indexing for synced catalog asset", zap.String("id", id))
			if err := s.clipIndexer.IndexClip(indexCtx, id); err != nil {
				s.log.Error("failed to automatically index catalog asset", zap.String("id", id), zap.Error(err))
			}
		})
	}

	return nil
}

// writeAssetIndex writes a unified asset_index record (best-effort, runs
// after the canonical media_assets / outbox_events commit). Failure
// is logged but never propagated: a stale asset_index row is preferable
// to rolling back the canonical outbox state. Identical payload across
// dispatcher + legacy paths — extracted here so the two branches in
// upsertPreservingExisting stay in lockstep.
func (s *Service) writeAssetIndex(ctx context.Context, clip *assets.Asset) {
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
