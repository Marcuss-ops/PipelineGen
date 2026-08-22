package app

// cliprender_adapters.go wires the concrete adapters for the clip.render
// parallel preparation phase. The capability (internal/capabilities/cliprender)
// owns the ports; THIS file (composition root) owns the mechanics:
//
//   - AssetResolver     → canonical asset registry (asset.Service)
//   - AssetMaterializer → local copy reuse + Drive download to scratch
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path. Each call emits structured zap
// logs so the upstream preparation timeline is reconstructible from the
// server log alone.

import (
	"context"
	"errors"
	"fmt"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// ── AssetResolver ────────────────────────────────────────────────────

// clipRenderAssetResolver maps a canonical asset_id to the capability's
// AssetRef via the canonical asset registry.
type clipRenderAssetResolver struct {
	assets *asset.Service
	log    *zap.Logger
}

// newClipRenderAssetResolver wires the resolver with the canonical asset
// service. log is required so each resolve call is observable.
func newClipRenderAssetResolver(assets *asset.Service, log *zap.Logger) (*clipRenderAssetResolver, error) {
	if assets == nil {
		return nil, errors.New("clip.render: asset registry not wired")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &clipRenderAssetResolver{assets: assets, log: log}, nil
}

func (r *clipRenderAssetResolver) ResolveAsset(ctx context.Context, assetID string) (*cliprender.AssetRef, error) {
	if r == nil || r.assets == nil {
		return nil, errors.New("clip.render: asset registry not wired")
	}
	t0 := time.Now()
	r.log.Info("clip.render.asset_resolve.start",
		zap.String("subsystem", "cliprender_asset_resolver"),
		zap.String("asset_id", assetID),
	)
	details, err := r.assets.Get(ctx, assetID)
	if err != nil {
		r.log.Error("clip.render.asset_resolve.failed",
			zap.String("subsystem", "cliprender_asset_resolver"),
			zap.String("asset_id", assetID),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("load asset %q: %w", assetID, err)
	}
	if details == nil || details.Asset == nil {
		r.log.Error("clip.render.asset_resolve.not_found",
			zap.String("subsystem", "cliprender_asset_resolver"),
			zap.String("asset_id", assetID),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
		)
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	a := details.Asset
	ref := &cliprender.AssetRef{
		AssetID:       a.ID,
		MediaType:     string(a.MediaType),
		LocalPath:     a.LocalPath(),
		DriveFileID:   a.DriveFileID(),
		LegacyFileMD5: firstNonEmpty(a.Sha256(), a.LegacyFileMD5(), a.ContentHash()),
		DurationMS:    a.Duration.Milliseconds(),
	}
	r.log.Info("clip.render.asset_resolve.done",
		zap.String("subsystem", "cliprender_asset_resolver"),
		zap.String("asset_id", assetID),
		zap.String("media_type", ref.MediaType),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
		zap.String("file_hash", ref.LegacyFileMD5),
		zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
	)
	return ref, nil
}

// ── AssetMaterializer ────────────────────────────────────────────────

// clipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
// clipRenderMaterializer is the clip.render-facing adapter that delegates
// every asset type (video, image, watermark, background) to the single
// CanonicalAssetMaterializer.
type clipRenderMaterializer struct {
	canonical *drivepkg.CanonicalAssetMaterializer
	log       *zap.Logger
}

// newClipRenderMaterializer wires the materializer over the canonical
// implementation. log is required so every materialize call is observable.
func newClipRenderMaterializer(drive drivepkg.Reader, scratchDir string, log *zap.Logger) (*clipRenderMaterializer, error) {
	if log == nil {
		log = zap.NewNop()
	}
	canonical, err := drivepkg.NewCanonicalAssetMaterializer(drive, scratchDir, log)
	if err != nil {
		return nil, err
	}
	return &clipRenderMaterializer{canonical: canonical, log: log}, nil
}

func (m *clipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	if m == nil || m.canonical == nil {
		return nil, errors.New("clip.render: Drive reader not wired (asset materialization requires it)")
	}
	t0 := time.Now()
	m.log.Info("clip.render.materialize.start",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
	)

	// Derive the extension from the media type when possible.
	ext := ".mp4"
	switch ref.MediaType {
	case "audio", "sound_effect":
		ext = ".m4a"
	case "image":
		ext = ".jpg"
	case "watermark":
		ext = ".png"
	}

	result, err := m.canonical.Materialize(ctx, drivepkg.MaterializeRequest{
		AssetID:        ref.AssetID,
		DriveFileID:    ref.DriveFileID,
		ExpectedSHA256: ref.FileHash,
		Extension:      ext,
		RegisteredPath: ref.LocalPath,
	})
	if err != nil {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
			zap.Error(err),
		)
		return nil, err
	}


	m.log.Info("clip.render.materialize.done",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("branch", result.OriginTag()),
		zap.Bool("cache_hit", result.FromCache),
		zap.Bool("from_cache", result.FromCache),
		zap.String("local_path", result.LocalPath),
		zap.String("sha256", result.SHA256),
		zap.Int64("size_bytes", result.SizeBytes),
		zap.Int64("total_ms", time.Since(t0).Milliseconds()),
	)

	return &cliprender.MaterializedAsset{
		AssetID:    ref.AssetID,
		LocalPath:  result.LocalPath,
		SHA256:     result.SHA256,
		SizeBytes:  result.SizeBytes,
		DurationMS: ref.DurationMS,
		FromCache:  result.FromCache,
	}, nil
}
