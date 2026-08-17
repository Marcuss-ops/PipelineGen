package deletion

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// Sentinel errors for the asset-centric deletion path.
//
// DeleteAsset is deliberately source-agnostic: `source` describes where
// an asset came from (filter / provenance / display / legacy API
// compatibility), not whether it has the right to be deleted. The
// canonical media_assets lookup and the outbox dispatcher are the only
// authorities in this path — no source/type/provider whitelist
// participates in the decision.
var (
	// ErrAssetIDRequired is returned when DeleteAsset receives an empty
	// asset ID. The dispatcher cannot stamp a lifecycle hop without an
	// aggregate id.
	ErrAssetIDRequired = errors.New("delete asset: asset_id is required")

	// ErrAssetRepositoryUnavailable is returned when the canonical
	// media_assets repository (clipsRepo) is not wired.
	ErrAssetRepositoryUnavailable = errors.New("delete asset: clipsRepo not wired (production wiring must supply *assets.ClipsRepository)")

	// ErrAssetNotFound is returned when the canonical media_assets
	// lookup cannot resolve the asset ID.
	ErrAssetNotFound = errors.New("delete asset: asset not found in canonical media_assets")

	// ErrDeletionDispatcherUnavailable is returned when the deletion
	// dispatcher is not wired. It is surfaced BEFORE any mutation:
	// without a dispatcher there is no outbox state machine to drive
	// the Drive/Qdrant/SQLite chain.
	ErrDeletionDispatcherUnavailable = errors.New("delete asset: dispatcher is nil — production wiring must configure the canonical outbox.Dispatcher")
)

// DeleteAsset deletes a canonical media_assets asset by its ID.
//
// This is the asset-centric deletion entry point. It accepts ANY
// canonical asset (clip, stock, artlist, script, document, voiceover,
// image, final_audio, rendered_video, ...) — the only questions that
// matter are:
//
//  1. does the asset exist in the canonical media_assets catalog?
//  2. is the deletion dispatcher wired?
//
// No source/type/provider whitelist participates in the decision. The
// dispatcher's EnqueueDriveDelete atomically stamps
// lifecycle_state=DELETE_REQUESTED and emits
// asset.drive.delete_requested.v1 in a single tx; the Drive/Qdrant/SQLite
// chain then runs asynchronously (see the dispatcher's EnqueueDriveDelete
// docstring for the full state-machine sequence).
//
// Fail-fast ordering (no partial side-effects on any error path):
//
//   - empty asset_id              → ErrAssetIDRequired
//   - clipsRepo not wired         → ErrAssetRepositoryUnavailable
//   - media_assets lookup error   → wrapped lookup error
//   - asset absent                → ErrAssetNotFound
//   - dispatcher not wired        → ErrDeletionDispatcherUnavailable
func (s *DeletionService) DeleteAsset(ctx context.Context, assetID string, permanently bool) error {
	logger := s.log
	if logger == nil {
		logger = zap.NewNop()
	}

	if assetID == "" {
		return ErrAssetIDRequired
	}

	if s.clipsRepo == nil {
		return ErrAssetRepositoryUnavailable
	}

	a, err := s.clipsRepo.Get(ctx, assetID)
	if err != nil {
		return fmt.Errorf("delete asset lookup %q: %w", assetID, err)
	}
	if a == nil {
		return ErrAssetNotFound
	}

	if s.dispatcher == nil {
		return ErrDeletionDispatcherUnavailable
	}

	logger.Info("deleting asset",
		zap.String("asset_id", assetID),
		zap.Bool("permanently", permanently),
	)
	return s.dispatcher.EnqueueDriveDelete(ctx, assetID, permanently)
}

// DeleteClip is the legacy source-scoped deletion entry point.
//
// It is now a compatibility wrapper that delegates to the canonical
// asset-centric DeleteAsset path. The `source` argument is retained
// for legacy API compatibility only — it no longer participates in
// authorization. `source` describes where an asset came from
// (provenance / display / legacy API shape), not whether it has the
// right to be deleted: the canonical media_assets lookup and the
// outbox dispatcher are the sole authorities.
//
// Callers should migrate to DeleteAsset; this wrapper is removed once
// the last caller has moved.
func (s *DeletionService) DeleteClip(ctx context.Context, source string, clipID string, permanently bool) error {
	_ = source // legacy API compatibility only; authorization is source-agnostic
	return s.DeleteAsset(ctx, clipID, permanently)
}

// DeleteByDriveFile handles deletion by Drive file ID or link.
func (s *DeletionService) DeleteByDriveFile(ctx context.Context, fileID string, source string, permanently bool) error {
	if fileID == "" {
		return fmt.Errorf("file_id is required")
	}

	clip, foundSource, err := s.FindClipByDriveFileID(ctx, fileID, source)
	if err != nil {
		return err
	}

	if clip == nil {
		return fmt.Errorf("clip not found in database for file %s", fileID)
	}

	return s.DeleteClip(ctx, foundSource, clip.ID, permanently)
}

// FindClipByDriveFileID searches for a clip across repositories
// using the canonical SourceCatalog typed-port dispatch. Collapse
// (June 2026): local repos map + switch source eliminated —
// SourceCatalog.Resolve→SourceRepo.GetByDriveFileID handles every
// source uniformly with adapter-side shape conversion.
func (s *DeletionService) FindClipByDriveFileID(ctx context.Context, fileID string, sourceLimit string) (*asset.Asset, string, error) {
	if s.catalog == nil {
		return nil, "", fmt.Errorf("deletion: source catalog is not configured")
	}
	sources := s.catalog.Names()

	if sourceLimit != "" && sourceLimit != "all" {
		canonical := artifacts.CanonicalSource(sourceLimit)
		if canonical == "" {
			return nil, "", fmt.Errorf("invalid source limit: %s", sourceLimit)
		}
		sources = []string{canonical}
	}

	for _, source := range sources {
		repo, ok := s.catalog.Resolve(source)
		if !ok || repo == nil {
			return nil, "", fmt.Errorf("deletion: source catalog repository unavailable for %q", source)
		}
		resolved, err := repo.GetByDriveFileID(ctx, fileID)
		if err != nil {
			return nil, "", fmt.Errorf("deletion: source catalog lookup %q: %w", source, err)
		}
		if resolved != nil {
			return resolved, source, nil
		}
	}

	return nil, "", nil
}
