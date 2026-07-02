package clipindexer

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

const (
	// embeddingModel and embeddingModelVersion are appended to metadata_json
	// as $.embedding_model and $.embedding_model_version when a clip reaches
	// the "indexed" state. They are separate from vectorstore's
	// CurrentEmbeddingVersion / CurrentSearchTextVersion because those
	// track the *content schema* version, whereas these track the
	// *model identity* (which model produced the vector).
	// Bump these when switching models to force re-indexing of all assets.
	embeddingModel        = "multilingual-e5-base"
	embeddingModelVersion = "2026-06-16-v1"

	// collectionVersion tracks the Qdrant collection schema/alias binding.
	// When the collection schema changes (e.g. new named vector, payload
	// field, BM25 tokenization rules), bump this and all clips will be
	// identified as needing re-indexing via content hash mismatch.
	// QDRANT-003: bumped to v3 to match the new versioned collection schema
	// with real SigLIP visual vectors and CLAP audio (no synthetic placeholders).
	collectionVersion = "v3"
)

// EmbeddingModel returns the current embedding model name.
func EmbeddingModel() string { return embeddingModel }

// EmbeddingModelVersion returns the current embedding model version.
func EmbeddingModelVersion() string { return embeddingModelVersion }

// CollectionVersion returns the current collection version.
func CollectionVersion() string { return collectionVersion }

// IndexClip generates embeddings for a clip and upserts it into Qdrant.
// Uses the canonical state machine in media_assets.index_state (column,
// QDRANT-002 PR6 / migration 094) — see internal/domain/asset/index_state.go
// for the IndexState enum:
//
//	DISCOVERED → INDEX_PENDING → INDEXING → INDEXED → INDEX_FAILED
//	                                                → DELETE_PENDING → DELETED
//
// The fast path skips regeneration when BOTH embeddings exist AND the
// content hash matches AND index_state == asset.StateIndexed.
// Clips without a transcript only require the semantic embedding to be valid.
//
// Writers to media_assets.index_state:
//   - setIndexState (transient + failure states INDEXING, INDEX_PENDING,
//     INDEX_FAILED; refuses to write INDEXED — see panic guard).
//   - setIndexedAt (terminal INDEXED + sidecar metadata in single atomic
//     UPDATE — folded with $.indexed_at, $.indexed_content_hash,
//     $.embedding_model, $.embedding_model_version).
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		s.log.Debug("clipindexer disabled, skipping", zap.String("clip_id", clipID))
		return nil
	}

	// Fast early-out: skip metadata-only asset names BEFORE any embedding work.
	// These rows exist in media_assets (sidecars ingested by Drive upload) but
	// are not real searchable media and would just pollute the vector store.
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

	if err := s.setIndexState(ctx, clipID, asset.StateIndexing, ""); err != nil {
		return fmt.Errorf("setIndexState INDEXING for %s: %w", clipID, err)
	}

	// Read source_version from the DB for the CAS fence in setIndexedAt.
	// A failed read is fatal: continuing with sourceVersion="" would
	// silently degrade the CAS fence (BLOCKER #2) to a no-op.
	var sourceVersion string
	if svErr := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(source_version, '') FROM media_assets WHERE id = ?`,
		clipID,
	).Scan(&sourceVersion); svErr != nil {
		return fmt.Errorf("failed to read source_version for CAS fence on %s: %w", clipID, retry.WrapTransient(svErr))
	}

	if s.cfg.ServerURL != "" {
		err := s.indexViaAPI(ctx, clipID)
		if err == nil {
			return s.finalizeIndex(ctx, clipID, contentHash, sourceVersion)
		}
		s.log.Warn("embedding server failed, falling back to script",
			zap.String("clip_id", clipID),
			zap.String("server_url", s.cfg.ServerURL),
			zap.Error(err))
	}

	err = s.indexViaScript(ctx, clipID)
	if err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist index failed state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return fmt.Errorf("indexViaScript failed for %s: %w", clipID, err)
	}
	return s.finalizeIndex(ctx, clipID, contentHash, sourceVersion)
}

// tryFastPath returns true if the clip is already fully indexed with valid
// embeddings and a matching content hash. The caller short-circuits to nil;
// otherwise fast-path upsert is attempted and the result returned.
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
	if !embeddingsOK || indexState != string(asset.StateIndexed) || storedHash != contentHash {
		return false
	}

	s.log.Info("clip already indexed with valid content hash, fast-path upsert",
		zap.String("clip_id", clipID))

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexPending, err.Error()); setErr != nil {
			s.log.Error("fast-path: failed to persist index state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		s.log.Error("fast-path upsert failed", zap.String("clip_id", clipID), zap.Error(err))
		return false
	}

	if setErr := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); setErr != nil {
		s.log.Error("fast-path: failed to persist indexed state, falling through to full re-index",
			zap.String("clip_id", clipID), zap.Error(setErr))
		return false
	}
	return true
}

// finalizeIndex upserts to vector store and persists the indexed state.
// sourceVersion is the canonical source_version column value read from
// the media_assets row BEFORE the upsert; it is passed to setIndexedAt
// for the CAS fence (prevents an obsolete indexing goroutine from
// overwriting a newer version's state).
func (s *Service) finalizeIndex(ctx context.Context, clipID, contentHash, sourceVersion string) error {
	if err := s.setIndexState(ctx, clipID, asset.StateIndexing, ""); err != nil {
		return fmt.Errorf("setIndexState INDEXING for %s: %w", clipID, err)
	}

	if err := s.UpsertVectorStore(ctx, clipID); err != nil {
		if setErr := s.setIndexState(ctx, clipID, asset.StateIndexFailed, err.Error()); setErr != nil {
			s.log.Error("failed to persist failed index state", zap.String("clip_id", clipID), zap.Error(setErr))
		}
		return fmt.Errorf("Qdrant upsert failed for %s: %w", clipID, err)
	}

	if err := s.setIndexedAt(ctx, clipID, contentHash, sourceVersion); err != nil {
		return fmt.Errorf("failed to persist indexed state for %s: %w", clipID, err)
	}
	s.log.Info("clip fully indexed and upserted to Qdrant", zap.String("clip_id", clipID))
	return nil
}


