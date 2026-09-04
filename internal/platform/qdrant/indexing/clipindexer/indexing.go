package clipindexer

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

const (
	// embeddingModel and embeddingModelVersion are appended to metadata_json
	// as $.embedding_model and $.embedding_model_version when a clip reaches
	// the "indexed" state. They are separate from vectorstore's
	// CurrentEmbeddingVersion / CurrentSearchTextVersion because those
	// track the *content schema* version, whereas these track the
	// *model identity* (which model produced the vector).
	//
	// SSOT (godlike/06): the canonical embedding identity lives in
	// internal/kernel/embedding (CanonicalText). These now anchor to the
	// canonical constants so the airlock stamps the real model identity.
	embeddingModel        = coreembedding.ModelIDMultilingualE5
	embeddingModelVersion = coreembedding.ModelRevisionMultilingualE5

	// collectionVersion is retained for legacy envelope parity only. The
	// canonical PostgreSQL media projection no longer writes Qdrant.
	collectionVersion = "v3"
)

// EmbeddingModel returns the current embedding model name.
func EmbeddingModel() string { return embeddingModel }

// EmbeddingModelVersion returns the current embedding model version.
func EmbeddingModelVersion() string { return embeddingModelVersion }

// CollectionVersion returns the compatibility index-schema version.
func CollectionVersion() string { return collectionVersion }

// IndexAsset is the compatibility entry point of MediaIndexer. In canonical
// PostgreSQL media mode, composition wires canonicalIndexRequester and this
// method immediately enqueues asset.index.requested in the PostgreSQL outbox.
// The legacy SQLite/Qdrant implementation below is then unreachable. It is
// retained temporarily only for isolated legacy tests while callers converge
// away from the concrete clipindexer.Service type.
func (s *Service) IndexAsset(ctx context.Context, assetID string) error {
	if s != nil && s.canonicalIndexRequester != nil {
		return s.canonicalIndexRequester.RequestIndex(ctx, assetID)
	}

	eligibility, err := s.Eligibility(ctx, assetID)
	if err != nil {
		if errors.Is(err, capregistry.ErrTaxonomySchemaUnavailable) {
			// Compatibility window for databases predating the taxonomy
			// migration. Once the columns exist, every lookup error is
			// fail-closed below.
			s.log.Debug("taxonomy columns unavailable; using legacy indexing path",
				zap.String("asset_id", assetID))
			return s.indexAsset(ctx, assetID)
		}
		// Taxonomy is the canonical searchability gate. If it cannot be
		// read, fail closed: do not guess that a registered row is
		// searchable and do not start embedding work.
		return fmt.Errorf("resolve index eligibility for %q: %w", assetID, err)
	}
	if eligibility == capregistry.IndexEligibilityRegistered {
		s.log.Debug("asset registered but not semantic-searchable, skipping indexing",
			zap.String("asset_id", assetID),
			zap.String("eligibility", string(eligibility)))
		return nil
	}
	return s.indexAsset(ctx, assetID)
}

// IndexClip is the legacy compatible wrapper. It delegates to the canonical
// IndexAsset so existing clip-vocabulary callers converge without a rename.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	return s.IndexAsset(ctx, clipID)
}

// indexAsset is the retired SQLite -> Qdrant implementation. Canonical
// PostgreSQL media composition never reaches this function; see IndexAsset.
// It remains temporarily to keep isolated compatibility tests compiling while
// concrete clipindexer dependencies are removed from capability constructors.
func (s *Service) indexAsset(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		// Legacy fail-closed behavior for isolated compatibility mode.
		s.log.Warn("clipindexer disabled, returning sentinel for outbox retry",
			zap.String("clip_id", clipID))
		return ErrIndexClipDisabledButEventRequested
	}

	// Fast early-out: skip metadata-only asset names BEFORE any embedding work.
	if skippable, name := s.shouldSkipByName(ctx, clipID); skippable {
		s.log.Debug("skipping indexing for non-media asset name",
			zap.String("clip_id", clipID),
			zap.String("name", name))
		return nil
	}

	contentHash, hasTranscript, err := s.computeContentHash(ctx, clipID)
	if err != nil {
		s.log.Warn("failed to compute content hash, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		contentHash = ""
		hasTranscript = false
	}

	if contentHash != "" {
		if s.tryFastPath(ctx, clipID, contentHash, hasTranscript) {
			return nil
		}
	}

	if err := s.setIndexState(ctx, clipID, asset.StateEmbedding, ""); err != nil {
		return fmt.Errorf("setIndexState EMBEDDING for %s: %w", clipID, err)
	}

	var sourceVersion string
	if svErr := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(source_version, '') FROM media_assets WHERE id = ?`,
		clipID,
	).Scan(&sourceVersion); svErr != nil {
		return fmt.Errorf("failed to read source_version for CAS fence on %s: %w", clipID, retry.WrapTransient(svErr))
	}

	if s.cfg.ServerURL == "" {
		err := retry.WrapTransient(fmt.Errorf("embedding server is not configured"))
		_ = s.setIndexState(ctx, clipID, asset.StateEmbeddingFailed, err.Error())
		return err
	}

	err = s.indexViaAPI(ctx, clipID)
	if err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateEmbeddingFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist embedding failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return retry.WrapTransient(fmt.Errorf("embedding server failed for %s: %w", clipID, err))
	}
	if setErr := s.setIndexState(ctx, clipID, asset.StateEmbedded, ""); setErr != nil {
		s.log.Error("failed to persist EMBEDDED state after script", zap.String("clip_id", clipID), zap.Error(setErr))
	}
	return s.finalizeIndex(ctx, clipID, contentHash, sourceVersion)
}

// tryFastPath is retained only for the retired compatibility implementation.
func (s *Service) tryFastPath(ctx context.Context, clipID, contentHash string, hasTranscript bool) bool {
	var hasSemantic bool
	var hasTranscriptEmb bool
	var storedHash string
	var indexState string
	var sourceVersion string
	err := s.db.QueryRowContext(ctx, `SELECT
		(embedding_json IS NOT NULL AND embedding_json != '' AND embedding_json != '[]' AND embedding_json != '{}'),
		(transcript_embedding IS NOT NULL AND transcript_embedding != '' AND transcript_embedding != '[]' AND transcript_embedding != '{}'),
		COALESCE(json_extract(metadata_json, '$.indexed_content_hash'), ''),
		COALESCE(index_state, ''),
		COALESCE(source_version, '')
		FROM media_assets WHERE id = ?`, clipID).Scan(&hasSemantic, &hasTranscriptEmb, &storedHash, &indexState, &sourceVersion)
	if err != nil {
		s.log.Warn("fast path check failed, will re-index",
			zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	embeddingsOK := hasSemantic && (hasTranscriptEmb || !hasTranscript)
	indexedOrEmbedded := indexState == string(asset.StateIndexed) || indexState == string(asset.StateEmbedded)
	if !embeddingsOK || !indexedOrEmbedded || storedHash != contentHash {
		return false
	}

	s.log.Info("legacy clip has valid embeddings, compatibility upsert",
		zap.String("clip_id", clipID))

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexingFailed, err.Error()); setErr != nil {
			s.log.Error("fast-path: failed to persist index failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		s.log.Error("fast-path upsert failed", zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	if setErr := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); setErr != nil {
		s.log.Error("fast-path: failed to persist indexed state, falling through to full re-index",
			zap.String("clip_id", clipID), zap.Error(setErr))
		return false
	}
	s.advanceProjectionSequence(ctx, clipID)
	return true
}

// finalizeIndex is retained only for the retired compatibility implementation.
func (s *Service) finalizeIndex(ctx context.Context, clipID, contentHash, sourceVersion string) error {
	if err := s.setIndexState(ctx, clipID, asset.StateIndexing, ""); err != nil {
		return fmt.Errorf("setIndexState INDEXING for %s: %w", clipID, err)
	}

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexingFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist indexing-failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return fmt.Errorf("legacy vector upsert failed for %s: %w", clipID, err)
	}

	if err := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); err != nil {
		return fmt.Errorf("failed to persist indexed state for %s: %w", clipID, err)
	}
	s.advanceProjectionSequence(ctx, clipID)
	s.log.Info("legacy compatibility clip indexed", zap.String("clip_id", clipID))
	return nil
}
