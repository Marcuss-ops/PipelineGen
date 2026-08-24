package drive

// materializer.go provides the CanonicalAssetMaterializer — the single,
// reusable mechanic that ensures an asset's bytes are available locally
// and content-verified.
//
// Every caller (clip.render, BGM/SFX resolver, localization, VidRush,
// images) should use this ONE implementation. The key invariants:
//
//   1. Cache is content-addressed: scratch/assets/<sha256>/source.<ext>
//      (when expectedSHA256 is known), not scratch/assets/<asset_id>.mp4.
//   2. The cache hit check verifies the computed SHA256 matches the
//      expected value — a stale cache silently serving wrong bytes is a
//      bug, not a feature.
//   3. Downloads write to a .part file, verify the hash, fsync, then
//      atomically rename to the canonical path. An interrupted download
//      never leaves a partial file at the canonical location.
//   4. The extension is caller-specified — never hardcoded (.m4a, .mp4
//      forced on unrelated media types).
//   5. Fail-closed: a hash mismatch against expectedSHA256 is a typed
//      error; too-small/empty output is rejected.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// MaterializeRequest is the input for a single asset materialization.
// All fields except AssetID and DriveFileID are optional; the
// materializer degrades gracefully when expectedSHA256 or extension is
// unknown.
type MaterializeRequest struct {
	// AssetID is the canonical media_assets.id (for logging).
	AssetID string
	// DriveFileID is the canonical Drive file identifier for download.
	DriveFileID string
	// ExpectedSHA256 is the canonical content SHA-256 for this asset
	// version. When non-empty the materializer content-addresses the
	// cache under scratch/<sha256>/source.<ext> AND verifies the
	// downloaded bytes match — a mismatch is a typed error.
	ExpectedSHA256 string
	// Extension is the file extension WITH the leading dot (".mp4",
	// ".m4a", ".png", ".wav"). Empty means no extension is appended.
	Extension string
	// RegisteredPath is the asset's registered local_path from SQLite.
	// When non-empty and the file exists, it is returned directly
	// (after hashing for verification).
	RegisteredPath string
}

// MaterializeResult is the output of a successful materialization.
type MaterializeResult struct {
	LocalPath string // absolute path to the verified local file
	SHA256    string // hex-encoded SHA-256 of the bytes at LocalPath
	SizeBytes int64  // file size in bytes
	FromCache bool   // true if no download was performed
	Branch    string // internal branch name (for logging)
}

// OriginTag returns the branch label for observability.
func (r *MaterializeResult) OriginTag() string {
	if r == nil {
		return "nil"
	}
	if r.Branch != "" {
		return r.Branch
	}
	if r.FromCache {
		return "cache"
	}
	return "download"
}

// CanonicalAssetMaterializer is the single, reusable asset materializer
// for every asset type (video, audio, image, watermark, background,
// overlay). It is safe for concurrent use.
type CanonicalAssetMaterializer struct {
	reader     Reader
	scratchDir string
	log        *zap.Logger
}

// NewCanonicalAssetMaterializer builds the materializer. reader is the
// Drive download source; scratchDir is the root for cache assets (a
// subdirectory "assets" is created under it). log is required for
// structured observability of every materialize call.
func NewCanonicalAssetMaterializer(reader Reader, scratchDir string, log *zap.Logger) (*CanonicalAssetMaterializer, error) {
	if reader == nil {
		return nil, errors.New("canonical asset materializer: Drive reader is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &CanonicalAssetMaterializer{reader: reader, scratchDir: scratchDir, log: log}, nil
}

// Materialize ensures the asset bytes are local and content-verified.
//
// Precedence:
//  1. RegisteredPath: if the file exists at the registered SQLite path,
//     hash it, return it.
//  2. Content-addressed cache: if ExpectedSHA256 is non-empty, check
//     scratch/assets/<sha256>/source.<ext> — verify the hash, return on
//     match.
//  3. Legacy asset_id cache: check scratch/assets/<assetID><ext> — hash
//     it, return it.
//  4. Download: fetch from Drive into a .part file, hash-verify against
//     ExpectedSHA256 (when known), fsync, rename into the cache.
//
// Fail-closed: an asset with neither a local copy nor a Drive source, a
// hash mismatch against ExpectedSHA256, or a zero-length output is a
// typed error.
func (m *CanonicalAssetMaterializer) Materialize(ctx context.Context, req MaterializeRequest) (*MaterializeResult, error) {
	if m == nil || m.reader == nil {
		return nil, errors.New("canonical asset materializer: Drive reader not wired")
	}
	t0 := time.Now()
	expected := cleanExpectedSHA256(req.ExpectedSHA256)

	m.log.Info("canonical_materializer.materialize.start",
		zap.String("asset_id", req.AssetID),
		zap.String("registered_path", req.RegisteredPath),
		zap.String("drive_file_id", req.DriveFileID),
		zap.String("expected_sha256", expected),
		zap.String("extension", req.Extension),
	)

	// (1) Registered local copy.
	if req.RegisteredPath != "" {
		if info, err := os.Stat(req.RegisteredPath); err == nil && !info.IsDir() {
			return m.verifyAndReturn(req.RegisteredPath, "registered_local", req, t0)
		}
	}

	// (2) Content-addressed cache: scratch/assets/<sha256>/source.<ext>
	if expected != "" {
		casPath := m.contentAddressedPath(expected, req.Extension)
		if info, err := os.Stat(casPath); err == nil && !info.IsDir() {
			computed, size, hashErr := hashFilePath(casPath)
			if hashErr == nil && computed == expected {
				m.log.Info("canonical_materializer.materialize.done",
					zap.String("asset_id", req.AssetID),
					zap.String("branch", "cas_cache"),
					zap.Bool("cache_hit", true),
					zap.Bool("from_cache", true),
					zap.String("local_path", casPath),
					zap.Int64("size_bytes", size),
					zap.Int64("total_ms", time.Since(t0).Milliseconds()),
				)
				return &MaterializeResult{
					LocalPath: casPath,
					SHA256:    computed,
					SizeBytes: size,
					FromCache: true,
					Branch:    "cas_cache",
				}, nil
			}
			// Hash mismatch: the cached bytes are stale. Remove and
			// re-download. log as a warning — this is a cache integrity
			// anomaly the operator should investigate.
			m.log.Warn("canonical_materializer.cas_cache_mismatch",
				zap.String("asset_id", req.AssetID),
				zap.String("cached_path", casPath),
				zap.String("expected_sha256", expected),
				zap.String("computed_sha256", computed),
			)
			_ = os.Remove(casPath)
		}
	}

	// (3) Legacy asset_id cache (fallback when expectedSHA256 is unknown).
	if expected == "" {
		legacyPath := m.legacyPath(req.AssetID, req.Extension)
		if info, err := os.Stat(legacyPath); err == nil && !info.IsDir() {
			return m.verifyAndReturn(legacyPath, "legacy_cache", req, t0)
		}
	}

	// (4) Drive download.
	if req.DriveFileID == "" {
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "no_drive_source"),
			zap.Int64("duration_ms", time.Since(t0).Milliseconds()),
		)
		return nil, fmt.Errorf("asset %q has neither a local copy nor a Drive source", req.AssetID)
	}
	return m.downloadAndVerify(ctx, req, expected, t0)
}

// verifyAndReturn hashes the file at path, optionally validates against
// ExpectedSHA256, and returns the result. It is used for the registered
// local path and legacy cache paths.
func (m *CanonicalAssetMaterializer) verifyAndReturn(path, branch string, req MaterializeRequest, t0 time.Time) (*MaterializeResult, error) {
	computed, size, err := hashFilePath(path)
	if err != nil {
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", branch),
			zap.Error(err),
		)
		return nil, fmt.Errorf("hash %s %q: %w", branch, path, err)
	}
	expected := cleanExpectedSHA256(req.ExpectedSHA256)
	if expected != "" && computed != expected {
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", branch),
			zap.String("expected_sha256", expected),
			zap.String("computed_sha256", computed),
		)
		return nil, fmt.Errorf("asset %q: %s hash mismatch: expected %s, got %s", req.AssetID, branch, expected, computed)
	}
	m.log.Info("canonical_materializer.materialize.done",
		zap.String("asset_id", req.AssetID),
		zap.String("branch", branch),
		zap.Bool("cache_hit", true),
		zap.Bool("from_cache", true),
		zap.String("local_path", path),
		zap.Int64("size_bytes", size),
		zap.Int64("total_ms", time.Since(t0).Milliseconds()),
	)
	return &MaterializeResult{
		LocalPath: path,
		SHA256:    computed,
		SizeBytes: size,
		FromCache: true,
		Branch:    branch,
	}, nil
}

// downloadAndVerify downloads the asset from Drive into a .part file,
// verifies the hash, and atomically renames into the appropriate cache
// location.
func (m *CanonicalAssetMaterializer) downloadAndVerify(ctx context.Context, req MaterializeRequest, expected string, t0 time.Time) (*MaterializeResult, error) {
	cacheDir := filepath.Join(m.scratchDir, "assets")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "mkdir"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}

	// Determine the final cache path.
	var finalPath string
	if expected != "" {
		finalPath = m.contentAddressedPath(expected, req.Extension)
	} else {
		finalPath = m.legacyPath(req.AssetID, req.Extension)
	}

	// If the final path already exists with the right hash, it was just
	// downloaded by a concurrent call or the CAS cache was populated.
	if info, err := os.Stat(finalPath); err == nil && !info.IsDir() {
		computed, size, hashErr := hashFilePath(finalPath)
		if hashErr == nil {
			if expected == "" || computed == expected {
				m.log.Info("canonical_materializer.materialize.done",
					zap.String("asset_id", req.AssetID),
					zap.String("branch", "download_concurrent_cache"),
					zap.Bool("cache_hit", true),
					zap.Bool("from_cache", true),
					zap.String("local_path", finalPath),
					zap.Int64("size_bytes", size),
					zap.Int64("total_ms", time.Since(t0).Milliseconds()),
				)
				return &MaterializeResult{
					LocalPath: finalPath,
					SHA256:    computed,
					SizeBytes: size,
					FromCache: true,
					Branch:    "download_concurrent_cache",
				}, nil
			}
		}
	}

	// Download to .part file.
	partPath := finalPath + ".part"
	downloadStart := time.Now()
	m.log.Info("canonical_materializer.drive_download.start",
		zap.String("asset_id", req.AssetID),
		zap.String("drive_file_id", req.DriveFileID),
		zap.String("part_path", partPath),
		zap.String("final_path", finalPath),
	)

	rc, _, err := m.reader.DownloadFile(ctx, req.DriveFileID)
	if err != nil {
		m.log.Error("canonical_materializer.drive_download.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("drive_file_id", req.DriveFileID),
			zap.Int64("duration_ms", time.Since(downloadStart).Milliseconds()),
			zap.Error(err),
		)
		return nil, fmt.Errorf("download asset %q from Drive: %w", req.AssetID, err)
	}
	defer rc.Close()

	out, err := os.Create(partPath)
	if err != nil {
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "create_part"),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create part file %q: %w", partPath, err)
	}

	n, copyErr := io.Copy(out, rc)

	// fsync before close to flush OS buffers.
	syncErr := out.Sync()
	closeErr := out.Close()

	computed, _, hashErr := hashFilePath(partPath)
	if copyErr == nil && hashErr != nil {
		copyErr = hashErr
	}

	// On any write/close error, clean up the part file.
	if copyErr != nil || closeErr != nil || syncErr != nil {
		_ = os.Remove(partPath)
		if copyErr != nil {
			m.log.Error("canonical_materializer.materialize.failed",
				zap.String("asset_id", req.AssetID),
				zap.String("branch", "write_part"),
				zap.Int64("bytes_written", n),
				zap.Error(copyErr),
			)
			return nil, fmt.Errorf("write part file %q: %w", partPath, copyErr)
		}
		if syncErr != nil {
			m.log.Error("canonical_materializer.materialize.failed",
				zap.String("asset_id", req.AssetID),
				zap.String("branch", "sync_part"),
				zap.Error(syncErr),
			)
			return nil, fmt.Errorf("sync part file %q: %w", partPath, syncErr)
		}
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "close_part"),
			zap.Error(closeErr),
		)
		return nil, fmt.Errorf("close part file %q: %w", partPath, closeErr)
	}

	if n <= 0 {
		_ = os.Remove(partPath)
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "empty_download"),
		)
		return nil, fmt.Errorf("asset %q downloaded empty from Drive", req.AssetID)
	}

	// Hash verification against expected SHA256.
	if expected != "" && computed != expected {
		_ = os.Remove(partPath)
		m.log.Error("canonical_materializer.materialize.failed",
			zap.String("asset_id", req.AssetID),
			zap.String("branch", "hash_mismatch"),
			zap.String("expected_sha256", expected),
			zap.String("computed_sha256", computed),
		)
		return nil, fmt.Errorf("asset %q: downloaded hash %s does not match expected %s", req.AssetID, computed, expected)
	}

	// Atomic rename: part → final.
	if err := os.Rename(partPath, finalPath); err != nil {
		// If rename fails (e.g., cross-device), fall back to copy+remove.
		if copyErr := copyFile(partPath, finalPath); copyErr != nil {
			_ = os.Remove(partPath)
			return nil, fmt.Errorf("rename/copy part to final %q: %w (rename: %v)", finalPath, copyErr, err)
		}
		_ = os.Remove(partPath)
	}

	m.log.Info("canonical_materializer.materialize.done",
		zap.String("asset_id", req.AssetID),
		zap.String("branch", "drive_download"),
		zap.Bool("cache_hit", false),
		zap.Bool("from_cache", false),
		zap.String("drive_file_id", req.DriveFileID),
		zap.String("local_path", finalPath),
		zap.String("computed_sha256", computed),
		zap.Int64("size_bytes", n),
		zap.Int64("download_ms", time.Since(downloadStart).Milliseconds()),
		zap.Int64("total_ms", time.Since(t0).Milliseconds()),
	)
	return &MaterializeResult{
		LocalPath: finalPath,
		SHA256:    computed,
		SizeBytes: n,
		FromCache: false,
	}, nil
}

// contentAddressedPath returns scratch/assets/<sha256>/source.<ext>.
func (m *CanonicalAssetMaterializer) contentAddressedPath(sha256, ext string) string {
	return filepath.Join(m.scratchDir, "assets", sha256, "source"+ext)
}

// legacyPath returns scratch/assets/<assetID><ext> (used when
// expectedSHA256 is not known — a transitional path).
func (m *CanonicalAssetMaterializer) legacyPath(assetID, ext string) string {
	return filepath.Join(m.scratchDir, "assets", assetID+ext)
}

// ── helpers ─────────────────────────────────────────────────────────

// hashFilePath returns the SHA-256 hex digest and byte size of the file
// at path. The caller must ensure the file exists and is readable.
func hashFilePath(path string) (sha256hex string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	computed, err := digest.SHA256Reader(f)
	if err != nil {
		return "", 0, err
	}
	return computed, info.Size(), nil
}

// copyFile copies src to dst (used as a fallback when os.Rename fails
// across device boundaries).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// cleanExpectedSHA256 returns the value when it is a canonical 64-char
// lowercase hex SHA-256, and "" otherwise. A prefixed, uppercase, or
// legacy hash must never be used for content-addressed caching.
func cleanExpectedSHA256(s string) string {
	if len(s) != 64 {
		return ""
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return s
}
