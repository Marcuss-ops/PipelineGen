package assetregistry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
)

// ArtifactBlobWriter adapts artifacts.BlobStore to the ArtifactWriter interface.
// It handles staging + verification + promotion as one Put call.
type ArtifactBlobWriter struct {
	blobs artifacts.BlobStore
}

// NewArtifactBlobWriter wraps a BlobStore as an ArtifactWriter.
func NewArtifactBlobWriter(blobs artifacts.BlobStore) *ArtifactBlobWriter {
	return &ArtifactBlobWriter{blobs: blobs}
}

// Put streams content to staging, computes SHA-256, and promotes to canonical storage.
// The key parameter is a hint (ignored for content-addressed stores).
// Returns the canonical storage key, size in bytes, and any error.
func (w *ArtifactBlobWriter) Put(ctx context.Context, key string, r io.Reader) (string, int64, error) {
	// Stage the blob
	writer, err := w.blobs.Stage(ctx, key)
	if err != nil {
		return "", 0, fmt.Errorf("artifact writer: stage: %w", err)
	}
	stagingKey := writer.Key()

	// Compute SHA-256 while writing
	hasher := sha256.New()
	teeReader := io.TeeReader(r, hasher)
	written, err := io.Copy(writer, teeReader)
	writer.Close()
	if err != nil {
		return "", 0, fmt.Errorf("artifact writer: write stage: %w", err)
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// Verify and promote to canonical
	result, err := w.blobs.VerifyAndPromote(ctx, stagingKey, hash)
	if err != nil {
		return "", 0, fmt.Errorf("artifact writer: promote: %w", err)
	}

	return result.StorageKey, written, nil
}
