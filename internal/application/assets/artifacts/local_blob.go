package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalBlobStore implements BlobStore using the local filesystem with
// content-addressed layout: <dataDir>/blobs/sha256/XX/XXXX... where XX
// is the first two hex characters of the SHA-256 hash.
type LocalBlobStore struct {
	dataDir    string
	stagingDir string
}

// NewLocalBlobStore creates a filesystem-backed BlobStore. Both the blobs
// and staging directories are created under dataDir if they don't exist.
func NewLocalBlobStore(dataDir string) (*LocalBlobStore, error) {
	blobsDir := filepath.Join(dataDir, "blobs", "sha256")
	stagingDir := filepath.Join(dataDir, "blobs", "staging")

	for _, dir := range []string{blobsDir, stagingDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("artifacts: create dir %s: %w", dir, err)
		}
	}

	return &LocalBlobStore{
		dataDir:    dataDir,
		stagingDir: stagingDir,
	}, nil
}

// stagingFile implements StagingWriter.
type stagingFile struct {
	f   *os.File
	key string
}

func (s *stagingFile) Write(p []byte) (int, error) { return s.f.Write(p) }
func (s *stagingFile) Close() error                { return s.f.Close() }
func (s *stagingFile) Key() string                 { return s.key }

// Stage creates a temp file in the staging directory and returns a writer.
// The caller writes the blob content and calls Close() to finalize.
func (s *LocalBlobStore) Stage(ctx context.Context, hint string) (StagingWriter, error) {
	// Generate a unique staging key
	f, err := os.CreateTemp(s.stagingDir, "stage-*")
	if err != nil {
		return nil, fmt.Errorf("artifacts: create staging file: %w", err)
	}

	return &stagingFile{
		f:   f,
		key: filepath.Base(f.Name()),
	}, nil
}

// VerifyAndPromote computes SHA-256 of the staged file, validates against
// expectedSHA256 (if non-empty), and moves it to its canonical location:
// <dataDir>/blobs/sha256/XX/XXXXX...
func (s *LocalBlobStore) VerifyAndPromote(ctx context.Context, stagingKey string, expectedSHA256 string) (PromoteResult, error) {
	stagingPath := filepath.Join(s.stagingDir, stagingKey)

	// Compute SHA-256
	f, err := os.Open(stagingPath)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("artifacts: open staging %s: %w", stagingKey, err)
	}
	defer f.Close()

	h := sha256.New()
	sizeBytes, err := io.Copy(h, f)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("artifacts: hash staging %s: %w", stagingKey, err)
	}
	f.Close()

	hash := hex.EncodeToString(h.Sum(nil))

	// Validate expected hash (if provided)
	if expectedSHA256 != "" && hash != expectedSHA256 {
		// Clean up the staging file since it doesn't match
		os.Remove(stagingPath)
		return PromoteResult{}, fmt.Errorf(
			"artifacts: hash mismatch for %s: expected=%s actual=%s",
			stagingKey, expectedSHA256, hash,
		)
	}

	// Build canonical path: blobs/sha256/ab/abcdef...
	prefix := hash[:2]
	canonicalDir := filepath.Join(s.dataDir, "blobs", "sha256", prefix)
	if err := os.MkdirAll(canonicalDir, 0755); err != nil {
		return PromoteResult{}, fmt.Errorf("artifacts: create canonical dir: %w", err)
	}

	canonicalPath := filepath.Join(canonicalDir, hash)
	storageKey := "sha256/" + prefix + "/" + hash

	// Atomic rename from staging to canonical
	if err := os.Rename(stagingPath, canonicalPath); err != nil {
		os.Remove(stagingPath)
		return PromoteResult{}, fmt.Errorf("artifacts: rename staging→canonical: %w", err)
	}

	return PromoteResult{
		StorageKey: storageKey,
		SHA256:     hash,
		SizeBytes:  sizeBytes,
	}, nil
}

// Open returns a reader for a canonical blob.
func (s *LocalBlobStore) Open(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	// storageKey format: "sha256/XX/HASH..."
	if !strings.HasPrefix(storageKey, "sha256/") {
		return nil, fmt.Errorf("artifacts: invalid storage key: %s", storageKey)
	}

	path := filepath.Join(s.dataDir, "blobs", storageKey)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("artifacts: open %s: %w", storageKey, err)
	}
	return f, nil
}

// Delete removes a blob from the canonical store.
func (s *LocalBlobStore) Delete(ctx context.Context, storageKey string) error {
	if !strings.HasPrefix(storageKey, "sha256/") {
		return fmt.Errorf("artifacts: invalid storage key: %s", storageKey)
	}
	path := filepath.Join(s.dataDir, "blobs", storageKey)
	return os.Remove(path)
}

// Stat returns metadata about a stored blob.
func (s *LocalBlobStore) Stat(ctx context.Context, storageKey string) (BlobInfo, error) {
	if !strings.HasPrefix(storageKey, "sha256/") {
		return BlobInfo{}, fmt.Errorf("artifacts: invalid storage key: %s", storageKey)
	}
	path := filepath.Join(s.dataDir, "blobs", storageKey)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BlobInfo{Exists: false}, nil
		}
		return BlobInfo{}, err
	}

	// Extract SHA-256 from the storage key (after the prefix/)
	parts := strings.SplitN(storageKey, "/", 3)
	sha := ""
	if len(parts) >= 3 {
		sha = parts[2]
	}

	return BlobInfo{
		SHA256:    sha,
		SizeBytes: info.Size(),
		Exists:    true,
	}, nil
}

// CleanupStaging removes all files from the staging directory older than
// maxAge. Called periodically by the artifact reconciler.
func (s *LocalBlobStore) CleanupStaging(ctx context.Context, maxAge int64) (int, error) {
	entries, err := os.ReadDir(s.stagingDir)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, e := range entries {
		path := filepath.Join(s.stagingDir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().Unix() < maxAge {
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// LocalPath resolves a storage key to a filesystem path.
// Returns the absolute path to the blob on disk.
func (s *LocalBlobStore) LocalPath(storageKey string) (string, error) {
	if !strings.HasPrefix(storageKey, "sha256/") {
		return "", fmt.Errorf("artifacts: invalid storage key: %s", storageKey)
	}
	return filepath.Join(s.dataDir, "blobs", storageKey), nil
}

// DataDir returns the root data directory for this blob store.
func (s *LocalBlobStore) DataDir() string { return s.dataDir }
