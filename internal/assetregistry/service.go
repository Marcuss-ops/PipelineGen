package assetregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// Service manages asset lifecycle: resolve → store → deduplicate → register.
// It is the canonical entry point for creating and retrieving assets.
type Service struct {
	repo   AssetRepository
	blobs  ArtifactWriter
	log    *zap.Logger
}

// NewService creates a new asset registry service.
func NewService(repo AssetRepository, blobs ArtifactWriter, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{repo: repo, blobs: blobs, log: log}
}

// ResolveAndRegister resolves asset content from a source, computes
// its SHA-256, deduplicates against existing assets, stores the blob
// if new, and records provenance via asset_sources.
//
// Content is buffered for SHA-256 computation before the dedup check.
// The blob is only written to storage if the content is truly new
// (no matching SHA-256 in the database).
//
// Returns the asset (new or existing) and whether it was newly created.
func (s *Service) ResolveAndRegister(ctx context.Context, input CreateInput) (*CreateResult, error) {
	// Step 1: Buffer content and compute SHA-256 BEFORE touching blob store.
	// This avoids wasting storage on duplicate content and prevents
	// os.Rename failures from duplicate canonical paths.
	const maxBufferSize = 500 * 1024 * 1024 // 500 MB limit
	limitedReader := io.LimitReader(input.Content, maxBufferSize+1)

	var buf bytes.Buffer
	hasher := sha256.New()
	teeReader := io.TeeReader(limitedReader, hasher)

	written, err := io.Copy(&buf, teeReader)
	if err != nil {
		return nil, fmt.Errorf("assetregistry: read content: %w", err)
	}
	if written > maxBufferSize {
		return nil, fmt.Errorf("assetregistry: content exceeds max size (%d bytes)", maxBufferSize)
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// Step 2: Check for duplicate by SHA-256 BEFORE writing to blob store
	existing, err := s.repo.GetAssetBySHA256(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("assetregistry: dedup check: %w", err)
	}
	if existing != nil {
		s.log.Info("asset deduplicated",
			zap.String("existing_id", existing.AssetID),
			zap.String("sha256", hash),
		)

		// Record provenance even for deduplicated assets
		sourceID := "src_" + hashutil.RandomString(16)
		source := &AssetSource{
			SourceID:        sourceID,
			AssetID:         existing.AssetID,
			SourceType:      input.SourceType,
			SourceReference: input.SourceRef,
			SourceAccountID: input.AccountID,
			ImportedAt:      time.Now().UTC(),
		}
		if err := s.repo.CreateSource(ctx, source); err != nil {
			s.log.Warn("failed to record provenance for deduplicated asset",
				zap.Error(err))
		}

		return &CreateResult{
			Asset:        existing,
			SHA256:       hash,
			NewlyCreated: false,
		}, nil
	}

	// Step 3: Write to blob store (content is confirmed new)
	key, sizeBytes, err := s.blobs.Put(ctx, "", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("assetregistry: store blob: %w", err)
	}

	// Step 4: Generate opaque asset ID
	assetID := "ast_" + hashutil.RandomString(20)

	// Step 5: Create asset record
	asset := &Asset{
		AssetID:        assetID,
		Kind:           input.Kind,
		Status:         StatusReady,
		SHA256:         hash,
		StorageBackend: "local",
		StorageKey:     key,
		MimeType:       input.MimeType,
		SizeBytes:      sizeBytes,
		DurationMs:     input.DurationMs,
		Width:          input.Width,
		Height:         input.Height,
		CreatedAt:      time.Now().UTC(),
	}
	now := time.Now().UTC()
	asset.VerifiedAt = &now

	if err := s.repo.CreateAsset(ctx, asset); err != nil {
		return nil, fmt.Errorf("assetregistry: create asset record: %w", err)
	}

	// Step 6: Record provenance
	sourceID := "src_" + hashutil.RandomString(16)
	source := &AssetSource{
		SourceID:        sourceID,
		AssetID:         assetID,
		SourceType:      input.SourceType,
		SourceReference: input.SourceRef,
		SourceAccountID: input.AccountID,
		ImportedAt:      time.Now().UTC(),
	}
	if err := s.repo.CreateSource(ctx, source); err != nil {
		s.log.Warn("failed to record provenance", zap.Error(err))
	}

	s.log.Info("asset registered",
		zap.String("asset_id", assetID),
		zap.String("kind", string(input.Kind)),
		zap.String("sha256", hash),
		zap.Int64("size_bytes", sizeBytes),
	)

	return &CreateResult{
		Asset:        asset,
		SHA256:       hash,
		NewlyCreated: true,
	}, nil
}

// Get retrieves an asset by ID.
func (s *Service) Get(ctx context.Context, assetID string) (*Asset, error) {
	return s.repo.GetAsset(ctx, assetID)
}

// GetBySHA256 retrieves an asset by content hash.
func (s *Service) GetBySHA256(ctx context.Context, sha256 string) (*Asset, error) {
	return s.repo.GetAssetBySHA256(ctx, sha256)
}

// TouchAccess updates the last-accessed timestamp for an asset.
func (s *Service) TouchAccess(ctx context.Context, assetID string) error {
	return s.repo.TouchAccess(ctx, assetID)
}

// ── Job Asset Linking ──────────────────────────────────────────────────

// LinkJobAsset creates a job_asset association.
func (s *Service) LinkJobAsset(ctx context.Context, ja *JobAsset) error {
	return s.repo.UpsertJobAsset(ctx, ja)
}

// GetJobAsset retrieves a specific job-asset link.
func (s *Service) GetJobAsset(ctx context.Context, jobID, assetID string) (*JobAsset, error) {
	return s.repo.GetJobAsset(ctx, jobID, assetID)
}

// ListJobAssets lists all assets for a job.
func (s *Service) ListJobAssets(ctx context.Context, jobID string) ([]JobAsset, error) {
	return s.repo.ListJobAssets(ctx, jobID)
}
