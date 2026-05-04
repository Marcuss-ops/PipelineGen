package cas

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"velox/go-master/pkg/hashutil"
)

type Store struct {
	Root string
}

func NewStore(root string) *Store {
	return &Store{Root: root}
}

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
