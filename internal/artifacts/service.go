package artifacts

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// Service manages the artifact lifecycle: Stage → Verify → Promote.
// It coordinates between the BlobStore (content-addressed storage) and
// the Repository (metadata persistence).
type Service struct {
	blobs  BlobStore
	repo   Repository
	log    *zap.Logger
}

// NewService creates a new artifact service.
func NewService(blobs BlobStore, repo Repository, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{blobs: blobs, repo: repo, log: log}
}

// CreateAndVerify handles the full staging→verification→promotion flow
// for a new artifact. The caller provides the content via r, and the
// service streams it to staging, verifies the SHA-256, and promotes it
// to canonical storage.
//
// Returns the promoted artifact with status READY, or an error if
// verification fails or storage is unavailable.
func (s *Service) CreateAndVerify(ctx context.Context, input CreateInput) (*Artifact, error) {
	if input.ID == "" {
		input.ID = "art_" + hashutil.RandomString(16)
	}
	if input.Kind == "" {
		input.Kind = "unknown"
	}

	// Phase 1: Stage the blob
	s.log.Info("staging artifact", zap.String("id", input.ID), zap.String("kind", input.Kind))
	artifact := &Artifact{
		ID:             input.ID,
		JobID:          input.JobID,
		Kind:           input.Kind,
		Status:         StatusStaging,
		StorageBackend: "local",
		MimeType:       input.MimeType,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, artifact); err != nil {
		return nil, fmt.Errorf("artifacts: create record: %w", err)
	}

	writer, err := s.blobs.Stage(ctx, input.ID)
	if err != nil {
		_ = s.repo.UpdateStatus(ctx, input.ID, StatusFailed, "", 0)
		return nil, fmt.Errorf("artifacts: begin stage: %w", err)
	}

	stagingKey := writer.Key()

	// Stream content to staging
	written, err := io.Copy(writer, input.Reader)
	writer.Close()
	if err != nil {
		_ = s.repo.UpdateStatus(ctx, input.ID, StatusFailed, "", 0)
		return nil, fmt.Errorf("artifacts: write stage: %w", err)
	}
	s.log.Info("staged artifact content",
		zap.String("id", input.ID),
		zap.Int64("bytes", written),
	)

	// Phase 2: Verify (SHA-256) and promote to canonical storage
	_ = s.repo.UpdateStatus(ctx, input.ID, StatusVerifying, "", 0)

	result, err := s.blobs.VerifyAndPromote(ctx, stagingKey, input.ExpectedSHA256)
	if err != nil {
		_ = s.repo.UpdateStatus(ctx, input.ID, StatusFailed, result.SHA256, result.SizeBytes)
		return nil, fmt.Errorf("artifacts: verify/promote: %w", err)
	}

	// Phase 3: Mark READY
	if err := s.repo.UpdateStatus(ctx, input.ID, StatusReady, result.SHA256, result.SizeBytes); err != nil {
		return nil, fmt.Errorf("artifacts: update ready status: %w", err)
	}

	s.log.Info("artifact ready",
		zap.String("id", input.ID),
		zap.String("sha256", result.SHA256),
		zap.String("storage_key", result.StorageKey),
		zap.Int64("size_bytes", result.SizeBytes),
	)

	now := time.Now().UTC()
	return &Artifact{
		ID:             input.ID,
		JobID:          input.JobID,
		Kind:           input.Kind,
		Status:         StatusReady,
		StorageBackend: "local",
		StorageKey:     result.StorageKey,
		SHA256:         result.SHA256,
		SizeBytes:      result.SizeBytes,
		MimeType:       input.MimeType,
		CreatedAt:      artifact.CreatedAt,
		UpdatedAt:      now,
		VerifiedAt:     &now,
	}, nil
}

// Get retrieves an artifact by ID.
func (s *Service) Get(ctx context.Context, id string) (*Artifact, error) {
	return s.repo.Get(ctx, id)
}

// GetBySHA256 finds an artifact by content hash (for deduplication).
func (s *Service) GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error) {
	return s.repo.GetBySHA256(ctx, sha256)
}

// Open returns a reader for an artifact's blob content.
func (s *Service) Open(ctx context.Context, artifactID string) (io.ReadCloser, *Artifact, error) {
	a, err := s.repo.Get(ctx, artifactID)
	if err != nil {
		return nil, nil, fmt.Errorf("artifacts: get %s: %w", artifactID, err)
	}
	if a == nil {
		return nil, nil, fmt.Errorf("artifacts: not found: %s", artifactID)
	}
	if a.Status != StatusReady {
		return nil, a, fmt.Errorf("artifacts: not ready: %s (status=%s)", artifactID, a.Status)
	}

	r, err := s.blobs.Open(ctx, a.StorageKey)
	if err != nil {
		return nil, a, fmt.Errorf("artifacts: open blob: %w", err)
	}
	return r, a, nil
}

// ListByJob returns all artifacts for a job.
func (s *Service) ListByJob(ctx context.Context, jobID string) ([]Artifact, error) {
	return s.repo.ListByJob(ctx, jobID)
}

// Delete marks an artifact as DELETED and removes the blob.
func (s *Service) Delete(ctx context.Context, id string) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil || a == nil {
		return fmt.Errorf("artifacts: not found: %s", id)
	}

	if a.StorageKey != "" {
		if err := s.blobs.Delete(ctx, a.StorageKey); err != nil {
			s.log.Warn("failed to delete blob", zap.String("id", id), zap.Error(err))
		}
	}
	return s.repo.UpdateStatus(ctx, id, StatusDeleted, a.SHA256, a.SizeBytes)
}

// LocalPath resolves an artifact's storage key to a local filesystem path.
// Returns the absolute path, or an error if the blob store is not local.
func (s *Service) LocalPath(ctx context.Context, artifactID string) (string, error) {
	a, err := s.repo.Get(ctx, artifactID)
	if err != nil {
		return "", fmt.Errorf("artifacts: get %s: %w", artifactID, err)
	}
	if a == nil {
		return "", fmt.Errorf("artifacts: not found: %s", artifactID)
	}
	if a.StorageKey == "" {
		return "", fmt.Errorf("artifacts: no storage key for %s (status=%s)", artifactID, a.Status)
	}

	// Type-assert to LocalBlobStore for path resolution
	local, ok := s.blobs.(*LocalBlobStore)
	if !ok {
		return "", fmt.Errorf("artifacts: blob store is not local (backend=%s)", a.StorageBackend)
	}
	return local.LocalPath(a.StorageKey)
}


