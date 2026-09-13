package adapters

// cliprender_adapters.go wires the concrete adapters for the clip.render
// parallel preparation phase. The capability (internal/capabilities/cliprender)
// owns the ports; THIS file (composition root) owns the mechanics:
//
//   - AssetMaterializer → local copy reuse + Drive download to scratch
//
// AssetResolver is NOT defined here: PostgreSQL is the media SSOT, so the only
// resolver is ClipRenderPGAssetResolver (cliprender_pg_resolver.go). The legacy
// SQLite `ClipRenderAssetResolver` was removed with the graceful-degrade
// fallback — there is no second media catalog to read from.

// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path. Each call emits structured zap
// logs so the upstream preparation timeline is reconstructible from the
// server log alone.

import (
	"context"
	"errors"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"go.uber.org/zap"
)

// ── AssetMaterializer ────────────────────────────────────────────────

// ClipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
// ClipRenderMaterializer is the clip.render-facing adapter that delegates
// every asset type (video, image, watermark, background) to the single
// CanonicalAssetMaterializer.
type ClipRenderMaterializer struct {
	canonical *drivepkg.CanonicalAssetMaterializer
	log       *zap.Logger
}

// NewClipRenderMaterializer wires the materializer over the canonical
// implementation. log is required so every materialize call is observable.
func NewClipRenderMaterializer(drive drivepkg.Reader, scratchDir string, log *zap.Logger) (*ClipRenderMaterializer, error) {
	if log == nil {
		log = zap.NewNop()
	}
	canonical, err := drivepkg.NewCanonicalAssetMaterializer(drive, scratchDir, log)
	if err != nil {
		return nil, err
	}
	return &ClipRenderMaterializer{canonical: canonical, log: log}, nil
}

func (m *ClipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	if m == nil || m.canonical == nil {
		return nil, errors.New("clip.render: Drive reader not wired (asset materialization requires it)")
	}
	t0 := time.Now()
	m.log.Info("clip.render.materialize.start",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("title", ref.Title),
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
		ExpectedSHA256: ref.LegacyFileMD5,
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
		zap.String("title", ref.Title),
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
		Title:      ref.Title,
		LocalPath:  result.LocalPath,
		SHA256:     result.SHA256,
		SizeBytes:  result.SizeBytes,
		DurationMS: ref.DurationMS,
		FromCache:  result.FromCache,
	}, nil
}
