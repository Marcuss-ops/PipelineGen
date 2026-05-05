package cas

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/pkg/hashutil"
)

// Store provides content-addressable storage for media files.
// Files are stored by hash in a directory structure (first 2 chars / next 2 chars / hash.ext).
type Store struct {
	Root string
}

// NewStore creates a new CAS store with the given root directory.
func NewStore(root string) *Store {
	return &Store{Root: root}
}

// PutFile stores a file in CAS, returning the content hash and storage path.
// If the file already exists (same hash), it returns AlreadyExists=true.
// After copying, it verifies file integrity by comparing hashes.
func (s *Store) PutFile(srcPath string) (*PutResult, error) {
	hash, err := hashutil.SHA256File(srcPath)
	if err != nil {
		return nil, err
	}

	ext := filepath.Ext(srcPath)
	dir := filepath.Join(s.Root, hash[0:2], hash[2:4])
	finalPath := filepath.Join(dir, hash+ext)

	if _, err := os.Stat(finalPath); err == nil {
		// File already exists
		return &PutResult{
			ContentHash: hash,
			Path:        finalPath,
			AlreadyExists: true,
			Timestamp:   time.Now().UTC(),
		}, nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}

	dst, err := os.Create(finalPath)
	if err != nil {
		src.Close()
		return nil, err
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		src.Close()
		return nil, err
	}

	// Close files to flush data
	if err := dst.Close(); err != nil {
		src.Close()
		return nil, fmt.Errorf("failed to close destination file: %w", err)
	}
	if err := src.Close(); err != nil {
		return nil, fmt.Errorf("failed to close source file: %w", err)
	}

	// Verify file integrity
	dstHash, err := hashutil.SHA256File(finalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash destination file: %w", err)
	}
	if dstHash != hash {
		os.Remove(finalPath)
		return nil, fmt.Errorf("integrity check failed: source hash %s, destination hash %s", hash, dstHash)
	}

	return &PutResult{
		ContentHash: hash,
		Path:        finalPath,
		AlreadyExists: false,
		Timestamp:   time.Now().UTC(),
	}, nil
}

// Verify checks if a file with the given hash exists in CAS and validates its integrity.
// Returns true if the file exists and its hash matches.
func (s *Store) Verify(contentHash string) (bool, error) {
	dir := filepath.Join(s.Root, contentHash[0:2], contentHash[2:4])
	
	// Try to find the file with any extension
	pattern := filepath.Join(dir, contentHash + ".*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return false, err
	}
	
	if len(matches) == 0 {
		return false, nil
	}
	
	// Verify the hash of the first match
	actualHash, err := hashutil.SHA256File(matches[0])
	if err != nil {
		return false, err
	}
	
	return actualHash == contentHash, nil
}

// PathForHash returns the expected canonical path for a given content hash.
// This is useful for looking up where a file should be stored without actually storing it.
func (s *Store) PathForHash(contentHash string, ext string) string {
	dir := filepath.Join(s.Root, contentHash[0:2], contentHash[2:4])
	return filepath.Join(dir, contentHash+ext)
}
