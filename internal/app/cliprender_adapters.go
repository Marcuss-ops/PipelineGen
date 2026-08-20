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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		AssetID:     a.ID,
		MediaType:   string(a.MediaType),
		LocalPath:   a.LocalPath(),
		DriveFileID: a.DriveFileID(),
		FileHash:    firstNonEmpty(a.Sha256(), a.FileHash(), a.ContentHash()),
		DurationMS:  a.Duration.Milliseconds(),
	}
	r.log.Info("clip.render.asset_resolve.done",
		zap.String("subsystem", "cliprender_asset_resolver"),
		zap.String("asset_id", assetID),
		zap.String("media_type", ref.MediaType),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
		zap.String("file_hash", ref.FileHash),
		zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
	)
	return ref, nil
}

// ── AssetMaterializer ────────────────────────────────────────────────

// clipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
type clipRenderMaterializer struct {
	drive      drivepkg.Reader
	scratchDir string
	log        *zap.Logger
}

// newClipRenderMaterializer wires the materializer. log is required so every
// materialize call (cache hit, scratch cache hit, fresh Drive download) is
// logged with timing + bytes.
func newClipRenderMaterializer(drive drivepkg.Reader, scratchDir string, log *zap.Logger) (*clipRenderMaterializer, error) {
	if drive == nil {
		return nil, errors.New("clip.render: Drive reader is required for materialization")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &clipRenderMaterializer{drive: drive, scratchDir: scratchDir, log: log}, nil
}

func (m *clipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	if m == nil || m.drive == nil {
		return nil, errors.New("clip.render: Drive reader not wired (asset materialization requires it)")
	}
	t0 := time.Now()
	m.log.Info("clip.render.materialize.start",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("local_path", ref.LocalPath),
		zap.String("drive_file_id", ref.DriveFileID),
	)

	// (1) Registered local copy.
	if ref.LocalPath != "" {
		if info, err := os.Stat(ref.LocalPath); err == nil && !info.IsDir() {
			hashStart := time.Now()
			sha, size, err := hashFile(ref.LocalPath)
			if err != nil {
				m.log.Error("clip.render.materialize.failed",
					zap.String("subsystem", "cliprender_materializer"),
					zap.String("asset_id", ref.AssetID),
					zap.String("branch", "registered_local"),
					zap.Error(err),
				)
				return nil, fmt.Errorf("hash local source %q: %w", ref.LocalPath, err)
			}
			hashMS := time.Since(hashStart).Milliseconds()
			m.log.Info("clip.render.materialize.done",
				zap.String("subsystem", "cliprender_materializer"),
				zap.String("asset_id", ref.AssetID),
				zap.String("branch", "registered_local"),
				zap.Bool("cache_hit", true),
				zap.Bool("from_cache", true),
				zap.String("local_path", ref.LocalPath),
				zap.Int64("size_bytes", size),
				zap.Int64("hash_ms", hashMS),
				zap.Int64("total_ms", time.Since(t0).Milliseconds()),
			)
			return &cliprender.MaterializedAsset{
				AssetID:    ref.AssetID,
				LocalPath:  ref.LocalPath,
				SHA256:     sha,
				SizeBytes:  size,
				DurationMS: ref.DurationMS,
				FromCache:  true,
			}, nil
		}
	}

	// (2/3) Drive materialization into scratch.
	if ref.DriveFileID == "" {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "no_drive_source"),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
		)
		return nil, fmt.Errorf("clip.render: asset %q has neither a local copy nor a Drive source", ref.AssetID)
	}
	target := filepath.Join(m.scratchDir, "assets", ref.AssetID+".mp4")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		hashStart := time.Now()
		sha, size, err := hashFile(target)
		if err != nil {
			m.log.Error("clip.render.materialize.failed",
				zap.String("subsystem", "cliprender_materializer"),
				zap.String("asset_id", ref.AssetID),
				zap.String("branch", "scratch_cache"),
				zap.Error(err),
			)
			return nil, fmt.Errorf("hash cached source %q: %w", target, err)
		}
		hashMS := time.Since(hashStart).Milliseconds()
		m.log.Info("clip.render.materialize.done",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "scratch_cache"),
			zap.Bool("cache_hit", true),
			zap.Bool("from_cache", true),
			zap.String("local_path", target),
			zap.Int64("size_bytes", size),
			zap.Int64("hash_ms", hashMS),
			zap.Int64("total_ms", time.Since(t0).Milliseconds()),
		)
		return &cliprender.MaterializedAsset{
			AssetID:    ref.AssetID,
			LocalPath:  target,
			SHA256:     sha,
			SizeBytes:  size,
			DurationMS: ref.DurationMS,
			FromCache:  true,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "mkdir"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	downloadStart := time.Now()
	m.log.Info("clip.render.materialize.drive_download_start",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("drive_file_id", ref.DriveFileID),
		zap.String("target", target),
	)
	rc, _, err := m.drive.DownloadFile(ctx, ref.DriveFileID)
	if err != nil {
		m.log.Error("clip.render.materialize.drive_download_failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("drive_file_id", ref.DriveFileID),
			zap.Int64("duration_ms", time.Since(downloadStart).Milliseconds()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("download asset %q from Drive: %w", ref.AssetID, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "create_scratch"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create scratch file %q: %w", target, err)
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hasher), rc)
	closeErr := out.Close()
	if copyErr != nil {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "write_scratch"),
			zap.Int64("bytes_written", n),
			zap.Error(copyErr),
		)
		return nil, fmt.Errorf("write scratch file %q: %w", target, copyErr)
	}
	if closeErr != nil {
		m.log.Error("clip.render.materialize.failed",
			zap.String("subsystem", "cliprender_materializer"),
			zap.String("asset_id", ref.AssetID),
			zap.String("branch", "close_scratch"),
			zap.Error(closeErr),
		)
		return nil, fmt.Errorf("close scratch file %q: %w", target, closeErr)
	}
	m.log.Info("clip.render.materialize.done",
		zap.String("subsystem", "cliprender_materializer"),
		zap.String("asset_id", ref.AssetID),
		zap.String("branch", "drive_download"),
		zap.Bool("cache_hit", false),
		zap.Bool("from_cache", false),
		zap.String("drive_file_id", ref.DriveFileID),
		zap.String("local_path", target),
		zap.Int64("size_bytes", n),
		zap.Int64("download_ms", time.Since(downloadStart).Milliseconds()),
		zap.Int64("total_ms", time.Since(t0).Milliseconds()),
	)
	return &cliprender.MaterializedAsset{
		AssetID:    ref.AssetID,
		LocalPath:  target,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:  n,
		DurationMS: ref.DurationMS,
		FromCache:  false,
	}, nil
}
