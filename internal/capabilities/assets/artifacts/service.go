package assets

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
)

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		fallback := fmt.Sprintf("%x", time.Now().UnixNano())
		for len(fallback) < n {
			fallback += fallback
		}
		return fallback[:n]
	}
	return hex.EncodeToString(buf)[:n]
}

// Service manages the artifact lifecycle: Stage → Verify → Promote.
// It coordinates between the BlobStore (content-addressed storage) and
// the Repository (metadata persistence).
type Service struct {
	blobs BlobStore
	repo  Repository
	log   *zap.Logger
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
// to canonical drive.
//
// Returns the promoted artifact with status READY, or an error if
// verification fails or storage is unavailable.
func (s *Service) CreateAndVerify(ctx context.Context, input CreateInput) (*Artifact, error) {
	if input.ID == "" {
		input.ID = "art_" + randomHex(16)
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
		return nil, fmt.Errorf("artifacts: begin stage: %w", s.markFailed(ctx, input.ID, "", 0, err))
	}

	stagingKey := writer.Key()

	// Stream content to staging
	written, copyErr := io.Copy(writer, input.Reader)
	closeErr := writer.Close()
	if copyErr != nil || closeErr != nil {
		cause := errors.Join(copyErr, closeErr)
		return nil, fmt.Errorf("artifacts: write stage: %w", s.markFailed(ctx, input.ID, "", 0, cause))
	}
	s.log.Info("staged artifact content",
		zap.String("id", input.ID),
		zap.Int64("bytes", written),
	)

	// Phase 2: Verify (SHA-256) and promote to canonical storage
	if err := s.repo.UpdateStatus(ctx, input.ID, StatusVerifying, "", 0); err != nil {
		return nil, fmt.Errorf("artifacts: update verifying status: %w", s.markFailed(ctx, input.ID, "", 0, err))
	}

	result, err := s.blobs.VerifyAndPromote(ctx, stagingKey, input.ExpectedSHA256)
	if err != nil {
		return nil, fmt.Errorf("artifacts: verify/promote: %w", s.markFailed(ctx, input.ID, result.SHA256, result.SizeBytes, err))
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

// markFailed records the terminal failure state and preserves both the
// operation error and any persistence error. A failure to record FAILED is
// itself part of the returned error; callers must not see a persistence
// failure as if the lifecycle state were durable.
func (s *Service) markFailed(ctx context.Context, id string, sha256 string, sizeBytes int64, cause error) error {
	if statusErr := s.repo.UpdateStatus(ctx, id, StatusFailed, sha256, sizeBytes); statusErr != nil {
		return errors.Join(cause, fmt.Errorf("artifacts: persist failed status: %w", statusErr))
	}
	return cause
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

// ResolveAndRegister buffers content, computes SHA-256, deduplicates against
// existing artifacts, stores the blob if new, and records provenance.
// Ported from assetregistry.Service.ResolveAndRegister (PR3 merge).
//
// Content is buffered for SHA-256 computation before the dedup check.
// The blob is only written to storage if the content is truly new
// (no matching SHA-256 in the database).
func (s *Service) ResolveAndRegister(ctx context.Context, input ResolveAndRegisterInput) (*ResolveAndRegisterResult, error) {
	// Step 1: Buffer content and compute SHA-256 BEFORE touching blob store.
	const maxBufferSize = 500 * 1024 * 1024 // 500 MB limit
	limitedReader := io.LimitReader(input.Content, maxBufferSize+1)

	var buf bytes.Buffer
	hasher := sha256.New()
	teeReader := io.TeeReader(limitedReader, hasher)

	written, err := io.Copy(&buf, teeReader)
	if err != nil {
		return nil, fmt.Errorf("artifacts: read content: %w", err)
	}
	if written > maxBufferSize {
		return nil, fmt.Errorf("artifacts: content exceeds max size (%d bytes)", maxBufferSize)
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// Step 2: Check for duplicate by SHA-256 BEFORE writing to blob store
	existing, err := s.repo.GetBySHA256(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("artifacts: dedup check: %w", err)
	}
	if existing != nil {
		s.log.Info("artifact deduplicated",
			zap.String("existing_id", existing.ID),
			zap.String("sha256", hash),
		)

		// Record provenance even for deduplicated artifacts
		sourceID := "src_" + randomHex(16)
		source := &ArtifactSource{
			SourceID:        sourceID,
			ArtifactID:      existing.ID,
			SourceType:      input.SourceType,
			SourceReference: input.SourceRef,
			SourceAccountID: input.AccountID,
			ImportedAt:      time.Now().UTC(),
		}
		if err := s.repo.CreateSource(ctx, source); err != nil {
			s.log.Warn("failed to record provenance for deduplicated artifact", zap.Error(err))
		}

		return &ResolveAndRegisterResult{
			Artifact:     existing,
			SHA256:       hash,
			NewlyCreated: false,
		}, nil
	}

	// Step 3: Create artifact via CreateAndVerify (handles staging→verify→promote)
	artifact, err := s.CreateAndVerify(ctx, CreateInput{
		Kind:           input.Kind,
		MimeType:       input.MimeType,
		Reader:         bytes.NewReader(buf.Bytes()),
		ExpectedSHA256: hash,
	})
	if err != nil {
		return nil, fmt.Errorf("artifacts: create and verify: %w", err)
	}

	// Step 4: Record provenance
	sourceID := "src_" + randomHex(16)
	source := &ArtifactSource{
		SourceID:        sourceID,
		ArtifactID:      artifact.ID,
		SourceType:      input.SourceType,
		SourceReference: input.SourceRef,
		SourceAccountID: input.AccountID,
		ImportedAt:      time.Now().UTC(),
	}
	if err := s.repo.CreateSource(ctx, source); err != nil {
		s.log.Warn("failed to record provenance", zap.Error(err))
	}

	s.log.Info("artifact registered",
		zap.String("artifact_id", artifact.ID),
		zap.String("kind", input.Kind),
		zap.String("sha256", hash),
		zap.Int64("size_bytes", artifact.SizeBytes),
	)

	return &ResolveAndRegisterResult{
		Artifact:     artifact,
		SHA256:       hash,
		NewlyCreated: true,
	}, nil
}

// TouchAccess updates the last-accessed timestamp for an artifact (PR3: from assetregistry).
func (s *Service) TouchAccess(ctx context.Context, artifactID string) error {
	return s.repo.TouchAccess(ctx, artifactID)
}

// ── Job Artifact Linking (PR3: from assetregistry) ─────────────────────

// LinkJobArtifact creates a job_artifact association.
func (s *Service) LinkJobArtifact(ctx context.Context, ja *JobArtifact) error {
	return s.repo.UpsertJobArtifact(ctx, ja)
}

// GetJobArtifact retrieves a specific job-artifact link.
func (s *Service) GetJobArtifact(ctx context.Context, jobID, artifactID string) (*JobArtifact, error) {
	return s.repo.GetJobArtifact(ctx, jobID, artifactID)
}

// ListJobArtifacts lists all artifacts for a job.
func (s *Service) ListJobArtifacts(ctx context.Context, jobID string) ([]JobArtifact, error) {
	return s.repo.ListJobArtifacts(ctx, jobID)
}
