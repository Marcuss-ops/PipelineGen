package app

// cliprender_adapters.go wires the concrete adapters for the clip.render
// parallel preparation phase. The capability (internal/capabilities/cliprender)
// owns the ports; THIS file (composition root) owns the mechanics:
//
//   - AssetResolver     → canonical asset registry (asset.Service)
//   - AssetMaterializer → local copy reuse + Drive download to scratch
//
// Every adapter is fail-closed: a missing dependency surfaces a typed error
// at call time, never a silent no-op path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── AssetResolver ────────────────────────────────────────────────────

// clipRenderAssetResolver maps a canonical asset_id to the capability's
// AssetRef via the canonical asset registry.
type clipRenderAssetResolver struct {
	assets *asset.Service
}

func (r *clipRenderAssetResolver) ResolveAsset(ctx context.Context, assetID string) (*cliprender.AssetRef, error) {
	if r.assets == nil {
		return nil, errors.New("clip.render: asset registry not wired")
	}
	details, err := r.assets.Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("load asset %q: %w", assetID, err)
	}
	if details == nil || details.Asset == nil {
		return nil, fmt.Errorf("asset %q not found", assetID)
	}
	a := details.Asset
	return &cliprender.AssetRef{
		AssetID:     a.ID,
		MediaType:   string(a.MediaType),
		LocalPath:   a.LocalPath(),
		DriveFileID: a.DriveFileID(),
		FileHash:    firstNonEmpty(a.Sha256(), a.FileHash(), a.ContentHash()),
		DurationMS:  a.Duration.Milliseconds(),
	}, nil
}

// ── AssetMaterializer ────────────────────────────────────────────────

// clipRenderMaterializer ensures the asset bytes are local. Precedence:
// (1) the registry's local_path when the file exists, (2) a content-addressed
// scratch copy already downloaded in a prior run, (3) a fresh Drive download
// into scratch. A missing local copy AND missing Drive source fails closed.
type clipRenderMaterializer struct {
	drive      drivepkg.Reader
	scratchDir string
}

func (m *clipRenderMaterializer) Materialize(ctx context.Context, ref cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	// (1) Registered local copy.
	if ref.LocalPath != "" {
		if info, err := os.Stat(ref.LocalPath); err == nil && !info.IsDir() {
			sha, size, err := hashFile(ref.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("hash local source %q: %w", ref.LocalPath, err)
			}
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
		return nil, fmt.Errorf("clip.render: asset %q has neither a local copy nor a Drive source", ref.AssetID)
	}
	if m.drive == nil {
		return nil, fmt.Errorf("clip.render: Drive reader not wired (asset %q requires Drive materialization)", ref.AssetID)
	}
	target := filepath.Join(m.scratchDir, "assets", ref.AssetID+".mp4")
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		sha, size, err := hashFile(target)
		if err != nil {
			return nil, fmt.Errorf("hash cached source %q: %w", target, err)
		}
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
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	rc, _, err := m.drive.DownloadFile(ctx, ref.DriveFileID)
	if err != nil {
		return nil, fmt.Errorf("download asset %q from Drive: %w", ref.AssetID, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return nil, fmt.Errorf("create scratch file: %w", err)
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hasher), rc)
	closeErr := out.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("write scratch file %q: %w", target, copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close scratch file %q: %w", target, closeErr)
	}
	return &cliprender.MaterializedAsset{
		AssetID:    ref.AssetID,
		LocalPath:  target,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:  n,
		DurationMS: ref.DurationMS,
		FromCache:  false,
	}, nil
}
