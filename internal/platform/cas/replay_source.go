package cas

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

// ReplayAssetSource adapts the CAS store to the replay materialization port:
// it pulls each replay asset out of durable storage by its SHA-256 address,
// streams it into a fresh local staging file and re-hashes the bytes so
// replay never trusts the recorded row alone. The recorded CAS URI is
// advisory — the address IS the digest.
type ReplayAssetSource struct {
	store      *Store
	stagingDir string
}

// NewReplayAssetSource constructs the adapter. Fail-closed: a nil store or an
// empty staging directory is a construction error, never a silent fallback.
func NewReplayAssetSource(store *Store, stagingDir string) (*ReplayAssetSource, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrNotWired)
	}
	if strings.TrimSpace(stagingDir) == "" {
		return nil, fmt.Errorf("%w: staging directory is required", ErrInvalidInput)
	}
	if err := os.MkdirAll(stagingDir, RootDirMode); err != nil {
		return nil, fmt.Errorf("cas: mkdir replay staging %s: %w", stagingDir, err)
	}
	return &ReplayAssetSource{store: store, stagingDir: filepath.Clean(stagingDir)}, nil
}

var _ capreplay.AssetSource = (*ReplayAssetSource)(nil)

// Materialize restores one asset into a fresh staging file and verifies its
// bytes against the recorded SHA-256. Any mismatch (missing object, corrupt
// bytes, wrong size when the recorded size is known) removes the staging file
// and returns an error — replay never proceeds on unverified bytes.
func (s *ReplayAssetSource) Materialize(ctx context.Context, asset capreplay.ReplayAsset) (capreplay.MaterializedAsset, error) {
	if s == nil || s.store == nil {
		return capreplay.MaterializedAsset{}, ErrNotWired
	}
	rc, err := s.store.Open(ctx, asset.SHA256)
	if err != nil {
		return capreplay.MaterializedAsset{}, err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(s.stagingDir, "replay-*.asset")
	if err != nil {
		return capreplay.MaterializedAsset{}, fmt.Errorf("cas: create replay staging file: %w", err)
	}
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), rc)
	if err != nil {
		cleanup()
		return capreplay.MaterializedAsset{}, fmt.Errorf("cas: materialize %s: %w", asset.AssetID, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return capreplay.MaterializedAsset{}, fmt.Errorf("cas: fsync replay staging file %s: %w", asset.AssetID, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return capreplay.MaterializedAsset{}, fmt.Errorf("cas: close replay staging file %s: %w", asset.AssetID, err)
	}

	digest := hex.EncodeToString(h.Sum(nil))
	if digest != asset.SHA256 {
		_ = os.Remove(tmp.Name())
		return capreplay.MaterializedAsset{}, fmt.Errorf("%w: asset %s hashed to %s, want %s", ErrCorruption, asset.AssetID, digest, asset.SHA256)
	}
	if asset.SizeBytes > 0 && n != asset.SizeBytes {
		_ = os.Remove(tmp.Name())
		return capreplay.MaterializedAsset{}, fmt.Errorf("%w: asset %s size %d, want %d", ErrCorruption, asset.AssetID, n, asset.SizeBytes)
	}
	return capreplay.MaterializedAsset{
		AssetID:   asset.AssetID,
		SHA256:    asset.SHA256,
		LocalPath: tmp.Name(),
		SizeBytes: n,
	}, nil
}
