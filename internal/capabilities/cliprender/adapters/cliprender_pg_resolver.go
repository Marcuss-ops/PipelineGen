package adapters

// cliprender_pg_resolver.go wires the clip.render / localization AssetResolver
// against the PostgreSQL media SSOT instead of the operational SQLite
// detail.Service. This closes the READ split-brain from the media cutover:
// producers commit media_assets to PostgreSQL, so consumers MUST resolve
// asset_id there. The legacy SQLite resolver stays only as the explicit
// graceful-degrade path for deployments with the media plane disabled.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// PostgresMediaAssetReader is the narrow read port the PG resolver consumes.
// *pgmedia.MediaSearcher (the canonical PostgreSQL media read authority)
// implements it.
type PostgresMediaAssetReader interface {
	GetAsset(ctx context.Context, assetID string) (*pgmedia.MediaAssetRecord, error)
}

// ClipRenderPGAssetResolver resolves a canonical asset_id from the PostgreSQL
// media SSOT.
type ClipRenderPGAssetResolver struct {
	media PostgresMediaAssetReader
	log   *zap.Logger
}

// NewClipRenderPGAssetResolver wires the resolver over the PostgreSQL media
// reader. media is required (fail-closed) so a wiring gap surfaces at
// composition time rather than as a silent not-found at render time.
func NewClipRenderPGAssetResolver(media PostgresMediaAssetReader, log *zap.Logger) (*ClipRenderPGAssetResolver, error) {
	if media == nil {
		return nil, errors.New("clip.render: postgres media reader not wired")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipRenderPGAssetResolver{media: media, log: log}, nil
}

// ResolveAsset maps a canonical asset_id to the capability's AssetRef via the
// PostgreSQL media SSOT.
func (r *ClipRenderPGAssetResolver) ResolveAsset(ctx context.Context, assetID string) (*cliprender.AssetRef, error) {
	if r == nil || r.media == nil {
		return nil, errors.New("clip.render: postgres media reader not wired")
	}
	t0 := time.Now()
	r.log.Info("clip.render.asset_resolve.start",
		zap.String("subsystem", "cliprender_pg_asset_resolver"),
		zap.String("asset_id", assetID),
	)
	rec, err := r.media.GetAsset(ctx, assetID)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			r.log.Error("clip.render.asset_resolve.not_found",
				zap.String("subsystem", "cliprender_pg_asset_resolver"),
				zap.String("asset_id", assetID),
				zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
			)
			return nil, fmt.Errorf("asset %q not found in postgres media SSOT", assetID)
		}
		r.log.Error("clip.render.asset_resolve.failed",
			zap.String("subsystem", "cliprender_pg_asset_resolver"),
			zap.String("asset_id", assetID),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("load asset %q: %w", assetID, err)
	}
	// A curated background plate must be registered with the certified
	// NORMALIZED bytes. The original supplied Drive file carries an audio
	// stream, so resolving it under a plate id would add a second audio source
	// beneath the master voiceover/BGM. Fail closed at the read boundary rather
	// than rendering the wrong artifact. Non-plate assets are unaffected.
	if err := mediaregistry.ValidateEditorialBackgroundIdentity(rec.ID, rec.SHA256); err != nil {
		r.log.Error("clip.render.asset_resolve.background_identity_mismatch",
			zap.String("subsystem", "cliprender_pg_asset_resolver"),
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		return nil, err
	}
	ref := &cliprender.AssetRef{
		AssetID:       rec.ID,
		Title:         rec.TitleOrName(),
		MediaType:     rec.MediaType,
		LocalPath:     rec.LocalPath,
		DriveFileID:   rec.DriveFileID,
		LegacyFileMD5: rec.SHA256,
		DurationMS:    rec.DurationMS,
	}
	r.log.Info("clip.render.asset_resolve.done",
		zap.String("subsystem", "cliprender_pg_asset_resolver"),
		zap.String("asset_id", assetID),
		zap.String("title", ref.Title),
		zap.String("media_type", ref.MediaType),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
		zap.String("file_hash", ref.LegacyFileMD5),
		zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
	)
	return ref, nil
}

var _ cliprender.AssetResolver = (*ClipRenderPGAssetResolver)(nil)
